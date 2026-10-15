package hypertext_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/typelate/dom/domtest"
	"github.com/typelate/dom/spec"
	"golang.org/x/net/html/atom"

	"github.com/crhntr/jsonschema/examples/concourse/internal/hypertext"
)

// app is the editor writing to a copy of the sample pipeline.
type app struct {
	t    *testing.T
	mux  *http.ServeMux
	file string
	orig string
}

func newApp(t *testing.T) *app {
	t.Helper()
	src, err := os.ReadFile("../../testdata/pipeline.yml")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "pipeline.yml")
	if err := os.WriteFile(file, src, 0o644); err != nil {
		t.Fatal(err)
	}
	srv, err := hypertext.New(context.Background(), src, file, true, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	hypertext.Routes(mux, srv)
	return &app{t: t, mux: mux, file: file, orig: string(src)}
}

func (a *app) do(req *http.Request) *http.Response {
	a.t.Helper()
	rec := httptest.NewRecorder()
	a.mux.ServeHTTP(rec, req)
	return rec.Result()
}

func (a *app) page(path string, want int) spec.Document {
	a.t.Helper()
	res := a.do(httptest.NewRequest(http.MethodGet, path, nil))
	if res.StatusCode != want {
		a.t.Fatalf("GET %s: status %d, want %d", path, res.StatusCode, want)
	}
	return domtest.ParseResponseDocument(a.t, res)
}

func (a *app) post(path string, form url.Values) *http.Response {
	a.t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return a.do(req)
}

// saved returns the pipeline file as it is on disk.
func (a *app) saved() string {
	a.t.Helper()
	b, err := os.ReadFile(a.file)
	if err != nil {
		a.t.Fatal(err)
	}
	return string(b)
}

// controls returns what a browser would submit for a form: every named
// control's value, checked boxes only, and no button.
func controls(form spec.Element) url.Values {
	values := url.Values{}
	for el := range form.QuerySelectorSequence("input, select, textarea") {
		name := el.GetAttribute("name")
		if name == "" || el.HasAttribute("disabled") {
			continue
		}
		switch strings.ToLower(el.TagName()) {
		case "input":
			if t := el.GetAttribute("type"); (t == "checkbox" || t == "radio") && !el.HasAttribute("checked") {
				continue
			}
			values.Add(name, el.GetAttribute("value"))
		case "select":
			opt := el.QuerySelector("option[selected]")
			if opt == nil {
				opt = el.QuerySelector("option")
			}
			if opt != nil {
				values.Add(name, opt.GetAttribute("value"))
			}
		case "textarea":
			values.Add(name, el.TextContent())
		}
	}
	return values
}

func texts(doc spec.Document, selector string) []string {
	var out []string
	for el := range doc.QuerySelectorSequence(selector) {
		out = append(out, strings.Join(strings.Fields(el.TextContent()), " "))
	}
	return out
}

func TestOverview(t *testing.T) {
	a := newApp(t)
	doc := a.page("/", http.StatusOK)
	if got := texts(doc, ".status"); !slices.Equal(got, []string{"valid"}) {
		t.Errorf("status = %q", got)
	}
	if got := texts(doc, ".tabs a"); !slices.Equal(got, []string{"all jobs", "ci", "deploy"}) {
		t.Errorf("group tabs = %q", got)
	}
	var nodes []string
	for el := range doc.QuerySelectorSequence(".graph svg a") {
		nodes = append(nodes, el.GetAttribute("href"))
	}
	if len(nodes) != 8 || !slices.Contains(nodes, "/jobs/deploy-production") || !slices.Contains(nodes, "/resources/nightly") {
		t.Errorf("graph links = %q", nodes)
	}
	if n := len(texts(doc, ".graph path.edge")); n == 0 {
		t.Errorf("the graph has no edges")
	}
	group := a.page("/?group=deploy", http.StatusOK)
	if n := len(texts(group, ".graph svg a")); n != 5 {
		t.Errorf("deploy group draws %d nodes, want 5", n)
	}
	if got := texts(doc, ".lists section:nth-of-type(1) li a"); !slices.Equal(got, []string{"unit", "build", "deploy-staging", "deploy-production"}) {
		t.Errorf("jobs = %q", got)
	}
}

func TestSection(t *testing.T) {
	a := newApp(t)
	doc := a.page("/jobs/deploy-staging", http.StatusOK)
	form := doc.QuerySelector("form.jsonform")
	if form == nil || form.GetAttribute("action") != "/jobs/deploy-staging/form" {
		t.Fatalf("form = %v", form)
	}
	if yaml := texts(doc, "pre.yaml"); len(yaml) != 1 || !strings.HasPrefix(yaml[0], "name: deploy-staging") {
		t.Errorf("YAML panel = %q", yaml)
	}
	a.page("/jobs/nope", http.StatusNotFound)
	a.page("/widgets/x", http.StatusNotFound)

	t.Run("save changes only the edited line", func(t *testing.T) {
		values := controls(form)
		values.Set("$/plan/2/timeout", "15m")
		res := a.post("/jobs/deploy-staging/form", values)
		if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/jobs/deploy-staging" {
			t.Fatalf("save: status %d, location %q", res.StatusCode, res.Header.Get("Location"))
		}
		if want := strings.Replace(a.orig, "timeout: 10m", "timeout: 15m", 1); a.saved() != want {
			t.Errorf("the file changed more than the timeout line:\n%s", a.saved())
		}
	})

	t.Run("an invalid value is shown on the form and not saved", func(t *testing.T) {
		before := a.saved()
		values := controls(a.page("/jobs/deploy-staging", http.StatusOK).QuerySelector("form.jsonform"))
		values.Set("$/plan/2/timeout", "soon")
		res := a.post("/jobs/deploy-staging/form", values)
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("status %d", res.StatusCode)
		}
		errs := texts(domtest.ParseResponseDocument(t, res), ".jsonform-errors li")
		if !slices.Equal(errs, []string{"Use a duration such as 30s, 5m or 1h30m, or a ((var))."}) {
			t.Errorf("errors = %q", errs)
		}
		if a.saved() != before {
			t.Errorf("an invalid form changed the file")
		}
	})

	t.Run("a rename onto another job is refused", func(t *testing.T) {
		values := controls(a.page("/jobs/build", http.StatusOK).QuerySelector("form.jsonform"))
		values.Set("$/name", "unit")
		res := a.post("/jobs/build/form", values)
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("status %d", res.StatusCode)
		}
		body := strings.Join(texts(domtest.ParseResponseDocument(t, res), ".jsonform"), " ")
		if !strings.Contains(body, `There is already a job named "unit".`) {
			t.Errorf("no message about the name in %q", body)
		}
	})
}

func TestNewAndDelete(t *testing.T) {
	a := newApp(t)
	form := a.page("/new/resources", http.StatusOK).QuerySelector("form.jsonform")
	values := controls(form)
	values.Set("$/name", "docs")
	values.Set("$/type", "git")
	values.Set("$/source", `{"uri": "https://example.com/docs.git"}`)
	res := a.post("/new/resources/form", values)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/resources/docs" {
		t.Fatalf("add: status %d, location %q", res.StatusCode, res.Header.Get("Location"))
	}
	if !strings.Contains(a.saved(), "- name: docs\n  type: git\n  source:\n    uri: https://example.com/docs.git\n") {
		t.Errorf("the new resource is not in the file:\n%s", a.saved())
	}
	a.page("/resources/docs", http.StatusOK)

	res = a.post("/resources/docs/delete", nil)
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete: status %d", res.StatusCode)
	}
	if a.saved() != a.orig {
		t.Errorf("adding then deleting did not restore the file:\n%s", a.saved())
	}
	if res := a.post("/resources/docs/delete", nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("deleting again: status %d", res.StatusCode)
	}
}

func TestSource(t *testing.T) {
	a := newApp(t)
	doc := a.page("/yaml", http.StatusOK)
	if got := doc.QuerySelector("#L47"); got == nil || !strings.Contains(got.TextContent(), "trigger: true") {
		t.Errorf("line 47 = %v", got)
	}

	broken := strings.Replace(a.orig, "    trigger: true\n  - task: test", "    triger: true\n  - task: test", 1)
	check := domtest.ParseResponseDocumentFragment(t, a.post("/yaml/check", url.Values{"yaml": {broken}}), atom.Body)
	if got := texts2(check, "#source-check li"); !slices.Equal(got, []string{`job unit, line 47 unknown field "triger"`}) {
		t.Errorf("live check = %q", got)
	}
	if a.saved() != a.orig {
		t.Errorf("checking saved the file")
	}

	res := a.post("/yaml", url.Values{"yaml": {"jobs: [\n"}})
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("unreadable YAML: status %d", res.StatusCode)
	}
	if a.saved() != a.orig {
		t.Errorf("unreadable YAML was saved")
	}

	res = a.post("/yaml", url.Values{"yaml": {broken}})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("save: status %d", res.StatusCode)
	}
	page := domtest.ParseResponseDocument(t, res)
	if got := texts(page, ".status"); !slices.Equal(got, []string{"1 problem"}) {
		t.Errorf("status after saving = %q", got)
	}
	if got := texts(page, "#L47 .flag"); !slices.Equal(got, []string{`unknown field "triger"`}) {
		t.Errorf("line 47 flags = %q", got)
	}
	if a.saved() != broken {
		t.Errorf("the file is not what was saved")
	}
}

func texts2(doc spec.DocumentFragment, selector string) []string {
	var out []string
	for el := range doc.QuerySelectorSequence(selector) {
		out = append(out, strings.Join(strings.Fields(el.TextContent()), " "))
	}
	return out
}
