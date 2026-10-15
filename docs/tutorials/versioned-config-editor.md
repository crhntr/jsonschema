# Build an editor for JSON configuration kept in git

In this tutorial you build a small web app over a git repository of JSON
configuration files and the JSON Schema that describes them. The app lists
branches, checks every file against the schema committed alongside it, shows
one setting across every branch at once, and edits a file through a form that
commits the change back to its branch. Git stays the source of truth the whole
time, so `git log`, `git diff` and code review keep working on everything the
app writes.

The cross-revision reading and the commits written without a checkout follow
the approach of [qyt](https://github.com/crhntr/qyt), which runs `yq` queries
across branches with [go-git](https://github.com/go-git/go-git) and can commit
the results without touching a working tree. This app does the same with JSON,
a schema from this repository, and forms from
[`jsonhandler`](../../jsonhandler/README.md).

You need Go 1.27 and git. The finished app is about 500 lines across three
files.

## 1. Make a configuration repository

The repository holds one schema at the root and one JSON file per service in
`configs/`:

```sh
mkdir -p config/configs && cd config
```

Save this as `config.schema.json`:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "Service",
  "type": "object",
  "required": ["image", "replicas"],
  "properties": {
    "image": {"type": "string", "minLength": 1},
    "replicas": {"type": "integer", "minimum": 1, "maximum": 20},
    "env": {
      "type": "object",
      "propertyNames": {"pattern": "^[A-Z_][A-Z0-9_]*$"},
      "additionalProperties": {"type": "string"}
    }
  },
  "additionalProperties": false
}
```

Save this as `configs/api.json`:

```json
{
  "image": "example/api:1.4.0",
  "replicas": 2,
  "env": {"LOG_LEVEL": "info"}
}
```

And this as `configs/worker.json`:

```json
{
  "image": "example/worker:1.4.0",
  "replicas": 1
}
```

Commit them, then make two more branches so there is something to compare.
`staging` scales the API up, and `broken` turns the worker off in a way the
schema does not allow:

```sh
git init -b main
git add .
git commit -m "Add the service schema and two services"

git switch -c staging
sed -i.bak 's/"replicas": 2/"replicas": 4/' configs/api.json && rm configs/api.json.bak
git commit -am "Scale the API to 4 replicas"

git switch -c broken
sed -i.bak 's/"replicas": 1/"replicas": 0/' configs/worker.json && rm configs/worker.json.bak
git commit -am "Turn the worker off"

git switch main
cd ..
git clone --bare config config.git
```

The app works on the bare clone, `config.git`. It moves branches by writing
commits straight into the object store, and a bare repository has no checked
out files to fall out of date when that happens.

## 2. Start the module

```sh
mkdir configui && cd configui
go mod init example.com/configui
go get github.com/crhntr/jsonschema@main github.com/go-git/go-git/v5@v5.19.3
```

## 3. Read configuration at any revision

go-git reads a repository the way git stores it. A branch is a reference to a
commit, a commit points at a tree, and a tree lists files and subtrees. Reading
a file at a revision means following those links, and no checkout is involved,
so the app can read every branch at once.

Create `repo.go` with the reading half of a small `Store`. The import block
already lists what the writing half in step 6 needs.

```go
package main

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

const (
	schemaPath = "config.schema.json"
	configDir  = "configs"
)

// Store reads and writes configuration in a git repository.
type Store struct {
	repo *git.Repository
}

func Open(dir string) (*Store, error) {
	repo, err := git.PlainOpen(dir)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", dir, err)
	}
	return &Store{repo: repo}, nil
}

// Branches returns the branch names, sorted.
func (s *Store) Branches() ([]string, error) {
	refs, err := s.repo.Branches()
	if err != nil {
		return nil, err
	}
	var names []string
	err = refs.ForEach(func(ref *plumbing.Reference) error {
		names = append(names, ref.Name().Short())
		return nil
	})
	sort.Strings(names)
	return names, err
}

// Head returns the commit a branch points to.
func (s *Store) Head(branch string) (*object.Commit, error) {
	ref, err := s.repo.Reference(plumbing.NewBranchReferenceName(branch), true)
	if err != nil {
		return nil, fmt.Errorf("branch %q: %w", branch, err)
	}
	return s.At(ref.Hash())
}

// At returns the commit with the hash.
func (s *Store) At(hash plumbing.Hash) (*object.Commit, error) {
	return s.repo.CommitObject(hash)
}

// ReadFile returns a file's contents as of a commit.
func ReadFile(c *object.Commit, name string) ([]byte, error) {
	f, err := c.File(name)
	if err != nil {
		return nil, fmt.Errorf("%s at %s: %w", name, c.Hash.String()[:7], err)
	}
	text, err := f.Contents()
	return []byte(text), err
}

// ConfigNames lists the JSON files in configs/ as of a commit.
func ConfigNames(c *object.Commit) ([]string, error) {
	tree, err := c.Tree()
	if err != nil {
		return nil, err
	}
	dir, err := tree.Tree(configDir)
	if err != nil {
		return nil, fmt.Errorf("%s at %s: %w", configDir, c.Hash.String()[:7], err)
	}
	var names []string
	for _, entry := range dir.Entries {
		if entry.Mode.IsFile() && strings.HasSuffix(entry.Name, ".json") {
			names = append(names, path.Join(configDir, entry.Name))
		}
	}
	return names, nil
}
```

qyt does the same walk at a larger scale. It picks branches with a regular
expression, then visits every file in each branch's tree whose name matches a
second expression. This app keeps the layout fixed instead: one schema and one
directory.

## 4. Check each file against the schema from the same commit

The schema lives in the repository next to the files it describes, which
means a branch that changes the schema and the configuration together stays
consistent, and an old commit is checked against the rules it was written
for. The checks below always read both from the same commit.

Create `main.go`:

```go
// Command configui serves pages for browsing, querying and editing JSON
// configuration files kept in a git repository with their schema.
package main

import (
	"context"
	"embed"
	"encoding/json/jsontext"
	"flag"
	"html/template"
	"log"
	"net/http"
	"net/url"

	"github.com/crhntr/jsonschema/jsonhandler"
	"github.com/crhntr/jsonschema/jsonptr"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

//go:embed pages.gohtml
var pages embed.FS

func main() {
	repoDir := flag.String("repo", "config.git", "path to the configuration repository")
	addr := flag.String("addr", "localhost:8080", "address to listen on")
	flag.Parse()

	store, err := Open(*repoDir)
	if err != nil {
		log.Fatal(err)
	}
	t, err := jsonhandler.Templates()
	if err != nil {
		log.Fatal(err)
	}
	t, err = t.ParseFS(pages, "pages.gohtml")
	if err != nil {
		log.Fatal(err)
	}
	s := &server{store: store, templates: t}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.branches)
	mux.HandleFunc("GET /branches/{branch}/{$}", s.configs)
	mux.HandleFunc("GET /query", s.query)
	mux.HandleFunc("GET /branches/{branch}/edit/{file...}", s.edit)
	mux.HandleFunc("POST /branches/{branch}/edit/{file...}", s.edit)
	mux.HandleFunc("GET /jsonform.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		_, _ = w.Write(jsonhandler.Stylesheet())
	})

	log.Printf("serving %s on http://%s/", *repoDir, *addr)
	log.Fatal(http.ListenAndServe(*addr, http.NewCrossOriginProtection().Handler(mux)))
}

type server struct {
	store     *Store
	templates *template.Template
}

// branches lists the branches.
func (s *server) branches(w http.ResponseWriter, r *http.Request) {
	names, err := s.store.Branches()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, http.StatusOK, "branches", names)
}

// configFile is one configuration file as of a commit, checked against the
// schema from the same commit.
type configFile struct {
	Name     string
	Problems string
}

// configs lists a branch's configuration files and whether each one
// matches the schema committed alongside it.
func (s *server) configs(w http.ResponseWriter, r *http.Request) {
	branch := r.PathValue("branch")
	head, err := s.store.Head(branch)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	files, err := checkConfigs(r.Context(), head)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, http.StatusOK, "configs", struct {
		Branch string
		Commit *object.Commit
		Files  []configFile
	}{branch, head, files})
}

func checkConfigs(ctx context.Context, c *object.Commit) ([]configFile, error) {
	schema, err := ReadFile(c, schemaPath)
	if err != nil {
		return nil, err
	}
	h, err := jsonhandler.New(ctx, schema)
	if err != nil {
		return nil, err
	}
	names, err := ConfigNames(c)
	if err != nil {
		return nil, err
	}
	var files []configFile
	for _, name := range names {
		data, err := ReadFile(c, name)
		if err != nil {
			return nil, err
		}
		f := configFile{Name: name}
		if err := h.Schema.Validate(name, data).AsError(); err != nil {
			f.Problems = err.Error()
		}
		files = append(files, f)
	}
	return files, nil
}

func (s *server) render(w http.ResponseWriter, status int, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := s.templates.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("rendering %s: %v", name, err)
	}
}
```

`jsonhandler.New` parses the schema and resolves its references, and the
`Schema` field on the handler it returns is ready to validate with. Step 7
uses the same handler for the form.

## 5. Ask one question of every branch

This is the cross-revision part. Given a file and a
[JSON Pointer](https://www.rfc-editor.org/rfc/rfc6901) such as `/replicas`,
the query page reads that location from the file at the head of every branch
and shows the answers side by side. qyt answers the same kind of question with
a `yq` expression in place of the pointer.

Add to `main.go`:

```go
// row is one branch's answer to a query.
type row struct {
	Branch, Commit, Value, Problem string
}

// query reads one location in one file on every branch.
func (s *server) query(w http.ResponseWriter, r *http.Request) {
	file := r.FormValue("file")
	pointer := jsonptr.Pointer(r.FormValue("pointer"))
	branches, err := s.store.Branches()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var rows []row
	for _, branch := range branches {
		head, err := s.store.Head(branch)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		rw := row{Branch: branch, Commit: head.Hash.String()[:7]}
		data, err := ReadFile(head, file)
		if err != nil {
			rw.Problem = "no such file"
			rows = append(rows, rw)
			continue
		}
		value, err := jsonptr.Find(data, pointer)
		if err != nil {
			rw.Problem = err.Error()
		} else {
			rw.Value = string(value)
		}
		rows = append(rows, rw)
	}
	s.render(w, http.StatusOK, "query", struct {
		File, Pointer string
		Rows          []row
	}{file, string(pointer), rows})
}
```

An empty pointer selects the whole document, so the same page doubles as a
way to read a file on every branch.

## 6. Commit without a checkout

Writing goes the other way down the same chain. The app stores the new file
contents as a blob, writes new copies of every tree on the path to it, writes
a commit whose parent is the commit the form was opened on, and finally moves
the branch. qyt's `apply` command builds its commits the same way.

The last step uses `CheckAndSetReference`, which moves the branch only if it
still points at that parent. If someone committed to the branch while the
form was open, the save fails instead of quietly replacing their change.

Add to `repo.go`:

```go
// errBranchMoved reports that someone else committed to the branch after
// the form was opened.
var errBranchMoved = errors.New("the branch changed since you opened this file; reload and try again")

// Save replaces one existing file on a branch with a new commit. It
// writes the objects straight into the repository, with no checkout, and
// moves the branch only if it still points at parent.
func (s *Store) Save(branch string, parent plumbing.Hash, name string, content []byte, message string) (plumbing.Hash, error) {
	parentCommit, err := s.repo.CommitObject(parent)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	parentTree, err := parentCommit.Tree()
	if err != nil {
		return plumbing.ZeroHash, err
	}

	blob := s.repo.Storer.NewEncodedObject()
	blob.SetType(plumbing.BlobObject)
	w, err := blob.Writer()
	if err != nil {
		return plumbing.ZeroHash, err
	}
	if _, err := w.Write(content); err != nil {
		return plumbing.ZeroHash, err
	}
	if err := w.Close(); err != nil {
		return plumbing.ZeroHash, err
	}
	blobHash, err := s.repo.Storer.SetEncodedObject(blob)
	if err != nil {
		return plumbing.ZeroHash, err
	}

	treeHash, err := s.replace(parentTree, strings.Split(name, "/"), blobHash)
	if err != nil {
		return plumbing.ZeroHash, err
	}

	author, err := s.signature()
	if err != nil {
		return plumbing.ZeroHash, err
	}
	commit := &object.Commit{
		Author:       author,
		Committer:    author,
		Message:      message,
		TreeHash:     treeHash,
		ParentHashes: []plumbing.Hash{parent},
	}
	obj := s.repo.Storer.NewEncodedObject()
	if err := commit.Encode(obj); err != nil {
		return plumbing.ZeroHash, err
	}
	commitHash, err := s.repo.Storer.SetEncodedObject(obj)
	if err != nil {
		return plumbing.ZeroHash, err
	}

	ref := plumbing.NewBranchReferenceName(branch)
	err = s.repo.Storer.CheckAndSetReference(
		plumbing.NewHashReference(ref, commitHash),
		plumbing.NewHashReference(ref, parent),
	)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("%w (%v)", errBranchMoved, err)
	}
	return commitHash, nil
}

// replace writes a copy of tree in which the file at path has the blob
// hash, along with a new copy of every directory on the way, and returns
// the new tree's hash. The file must already exist, so the entries keep
// their order.
func (s *Store) replace(tree *object.Tree, path []string, blob plumbing.Hash) (plumbing.Hash, error) {
	entries := append([]object.TreeEntry(nil), tree.Entries...)
	i := -1
	for j, e := range entries {
		if e.Name == path[0] {
			i = j
		}
	}
	if i < 0 {
		return plumbing.ZeroHash, fmt.Errorf("%s: %w", path[0], object.ErrFileNotFound)
	}
	if len(path) == 1 {
		entries[i].Hash = blob
	} else {
		sub, err := tree.Tree(path[0])
		if err != nil {
			return plumbing.ZeroHash, err
		}
		if entries[i].Hash, err = s.replace(sub, path[1:], blob); err != nil {
			return plumbing.ZeroHash, err
		}
	}
	obj := s.repo.Storer.NewEncodedObject()
	if err := (&object.Tree{Entries: entries}).Encode(obj); err != nil {
		return plumbing.ZeroHash, err
	}
	return s.repo.Storer.SetEncodedObject(obj)
}

// signature uses user.name and user.email from git config, as git does.
func (s *Store) signature() (object.Signature, error) {
	cfg, err := s.repo.ConfigScoped(config.GlobalScope)
	if err != nil {
		return object.Signature{}, err
	}
	return object.Signature{Name: cfg.User.Name, Email: cfg.User.Email, When: time.Now()}, nil
}
```

`replace` only changes files that already exist. Git requires the entries of
a tree in a particular order, and swapping the hash of an existing entry
keeps that order without sorting anything.

## 7. Edit a file with a form

The edit page builds a `jsonhandler.Handler` for each request from the schema
and the file at one commit. On the first visit that commit is the branch head.
The page then puts the commit's hash in the URL the form posts to, so every
later edit and the final submission work on that same commit, and that hash
becomes the parent the save in step 6 checks against.

Add to `main.go`:

```go
// edit shows a form for one configuration file and, when a valid form is
// submitted, commits it to the branch.
func (s *server) edit(w http.ResponseWriter, r *http.Request) {
	branch, name := r.PathValue("branch"), r.PathValue("file")

	// The form edits the file as of one commit: the branch head when it
	// opens, and the same commit (from the form's URL) when it is posted.
	var c *object.Commit
	var err error
	if parent := r.URL.Query().Get("parent"); parent != "" {
		c, err = s.store.At(plumbing.NewHash(parent))
	} else {
		c, err = s.store.Head(branch)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	schema, err := ReadFile(c, schemaPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	value, err := ReadFile(c, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	h, err := jsonhandler.New(r.Context(), schema)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.Value = jsontext.Value(value)
	h.SubmitLabel = "Commit to " + branch
	h.OnSubmit = func(ctx context.Context, v jsontext.Value) (string, error) {
		if err := v.Indent(jsontext.WithIndent("  ")); err != nil {
			return "", err
		}
		_, err := s.store.Save(branch, c.Hash, name, append(v, '\n'), "Update "+name)
		return "Committed.", err
	}

	var form *jsonhandler.Form
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		form, err = h.SubmitForm(r.Context(), r.PostForm)
	} else {
		form, err = h.Form(r.Context())
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if form.Valid {
		http.Redirect(w, r, "/branches/"+url.PathEscape(branch)+"/", http.StatusSeeOther)
		return
	}
	action := r.URL.Path + "?parent=" + c.Hash.String()
	form.WithActions(action, "")
	s.render(w, form.StatusCode(), "edit", struct {
		Branch, File string
		Commit       *object.Commit
		Form         *jsonhandler.Form
	}{branch, name, c, form})
}
```

`SubmitForm` handles both kinds of post. A button such as "Add property"
re-renders the form with the change, and the submit button validates the
value against the schema and calls `OnSubmit`, which commits it. An error from
`OnSubmit`, such as the branch having moved, is shown on the form with status
422. Passing an empty second argument to `WithActions` leaves out validation
as you type, which would need a route of its own; the browser and the server
still check the value on submit.

## 8. Add the pages

Create `pages.gohtml`. The page templates are parsed into the set
`jsonhandler.Templates` returns, which is how the edit page can call
`jsonhandler-form`.

```gotemplate
{{define "head"}}<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.}} · configui</title>
<link rel="stylesheet" href="https://unpkg.com/open-props@1.7.16/open-props.min.css">
<link rel="stylesheet" href="/jsonform.css">
</head>
<body>
<main>
<p><a href="/">Branches</a></p>
{{end}}

{{define "foot"}}
</main>
</body>
</html>
{{end}}

{{define "branches"}}{{template "head" "Branches"}}
<h1>Branches</h1>
<ul>
{{- range .}}
<li><a href="/branches/{{.}}/">{{.}}</a></li>
{{- end}}
</ul>
{{template "foot"}}{{end}}

{{define "configs"}}{{template "head" .Branch}}
<h1>{{.Branch}}</h1>
<p>At {{.Commit.Hash.String | printf "%.7s"}}: {{.Commit.Message}}</p>
<table>
<thead><tr><th>File</th><th>Schema</th><th></th></tr></thead>
<tbody>
{{- range .Files}}
<tr>
<td>{{.Name}}</td>
<td>{{if .Problems}}{{.Problems}}{{else}}valid{{end}}</td>
<td><a href="/branches/{{$.Branch}}/edit/{{.Name}}">Edit</a> · <a href="/query?file={{.Name}}&amp;pointer=">On every branch</a></td>
</tr>
{{- end}}
</tbody>
</table>
{{template "foot"}}{{end}}

{{define "query"}}{{template "head" "Query"}}
<h1>{{.File}} on every branch</h1>
<form method="get" action="/query">
<input type="hidden" name="file" value="{{.File}}">
<label>JSON Pointer <input name="pointer" value="{{.Pointer}}" placeholder="/replicas"></label>
<button>Query</button>
</form>
<table>
<thead><tr><th>Branch</th><th>Commit</th><th>Value</th></tr></thead>
<tbody>
{{- range .Rows}}
<tr><td>{{.Branch}}</td><td>{{.Commit}}</td><td>{{if .Problem}}{{.Problem}}{{else}}<code>{{.Value}}</code>{{end}}</td></tr>
{{- end}}
</tbody>
</table>
{{template "foot"}}{{end}}

{{define "edit"}}{{template "head" .File}}
<h1>{{.File}} on {{.Branch}}</h1>
<p>Editing the version from {{.Commit.Hash.String | printf "%.7s"}}.</p>
{{template "jsonhandler-form" .Form}}
{{template "foot"}}{{end}}
```

## 9. Try it

Start the app from the `configui` directory, pointing it at the bare
repository:

```sh
go mod tidy
go run . -repo ../config.git
```

Open http://localhost:8080/ and work through the pages:

1. **Branches.** The first page lists `broken`, `main` and `staging`.
2. **A branch.** Open `broken`. `configs/api.json` is valid, and
   `configs/worker.json` shows the schema's complaint,
   `configs/worker.json:1:0: /replicas: less than minimum 1`.
3. **A query.** Follow "On every branch" next to `configs/api.json` and query
   `/replicas`. The table shows `4` on `broken` and `staging` and `2` on
   `main`, each with its commit.
4. **An edit.** Back on `broken`, edit `configs/worker.json`. Setting replicas
   to `0` does not get past the browser, because the form carries the schema's
   minimum as `min="1"`. Set it to `3` and press "Commit to broken". The
   branch page now shows both files as valid, at a new commit named
   "Update configs/worker.json".
5. **The history.** Git sees an ordinary commit:

   ```sh
   git -C ../config.git log --oneline broken
   git -C ../config.git show broken:configs/worker.json
   ```

6. **A conflict.** Open the edit page for the same file in two tabs and commit
   from both. The second commit fails with "the branch changed since you
   opened this file; reload and try again", and the branch keeps the first
   one.

## Where to go next

- The app trusts whoever can reach it. Put it behind your usual
  authentication before it listens anywhere but `localhost`, and take the
  commit author from the signed-in user rather than from git config.
- Branch names with a `/` in them, such as `feature/api`, do not fit the
  `{branch}` path segment. Escape them in links or move the branch into a
  query parameter.
- `git push` from the bare repository, or a go-git `Push`, sends the commits
  to a shared remote, where a pull request can review them like any other
  change.
- qyt's [`HandleMatchingFiles` and `Apply`](https://github.com/crhntr/qyt/blob/main/qyt.go)
  show how to generalize the fixed layout here to any branches and files that
  match a pattern.
- The [jsonhandler README](../../jsonhandler/README.md) covers htmx, live
  validation, the wizard and styling. The
  [error message reference](../reference/schema-error-messages.md) has advice
  on writing a schema whose errors people can act on.
