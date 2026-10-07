package main

import (
	"context"
	_ "embed"
	"encoding/json/jsontext"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/crhntr/jsonschema/jsonhandler"
)

//go:embed form.gohtml
var formPageTemplate string

// formCommand serves a page with a form for editing a JSON value. Without
// --schema the value is itself a JSON Schema, edited through the 2020-12
// meta-schema extended with jsonhandler's x-form annotations.
func formCommand(ctx context.Context, wd string, args []string, stdout, stderr io.Writer, client *http.Client) error {
	var (
		schemaArg string
		addr      string
		write     bool
	)
	flagSet := flag.NewFlagSet("form", flag.ContinueOnError)
	flagSet.SetOutput(stderr)
	flagSet.StringVar(&schemaArg, "schema", "", "path or URL of the JSON Schema the value must match (default: JSON Schema 2020-12 with x-form annotations, for editing a schema)")
	flagSet.StringVar(&addr, "addr", "localhost:8080", "address to listen on")
	flagSet.BoolVar(&write, "write", false, "write each valid submission to the instance file instead of standard output")
	if err := flagSet.Parse(args); err != nil {
		return err
	}
	if flagSet.NArg() > 1 {
		return fmt.Errorf("form takes at most one instance file, got %d", flagSet.NArg())
	}
	instance := flagSet.Arg(0)
	if write && instance == "" {
		return errors.New("--write needs an instance file")
	}
	handler, err := newFormServer(ctx, wd, schemaArg, instance, write, stdout, client)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stderr, "serving form at http://%s/\n", ln.Addr())
	server := &http.Server{Handler: handler}
	served := make(chan error, 1)
	go func() { served <- server.Serve(ln) }()
	select {
	case err := <-served:
		return err
	case <-ctx.Done():
		if err := server.Shutdown(context.Background()); err != nil {
			return err
		}
		if err := <-served; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

// formServer is the routes receiver: the jsonhandler.Handler whose Value
// moves forward each time a submission is saved.
type formServer struct {
	mu      sync.Mutex
	handler jsonhandler.Handler
}

func (s *formServer) current() *jsonhandler.Handler {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.handler
	return &h
}

func (s *formServer) saved(v jsontext.Value) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handler.Value = v
}

func (s *formServer) Form(ctx context.Context) (*jsonhandler.Form, error) {
	return s.current().Form(ctx)
}

func (s *formServer) SubmitForm(ctx context.Context, form url.Values) (*jsonhandler.Form, error) {
	return s.current().SubmitForm(ctx, form)
}

func (s *formServer) Validate(ctx context.Context, form url.Values) (*jsonhandler.Form, error) {
	return s.current().Validate(ctx, form)
}

type formPage struct {
	Title, Subtitle string
	Form            *jsonhandler.Form
}

func newFormServer(ctx context.Context, wd, schemaArg, instance string, write bool, stdout io.Writer, client *http.Client) (http.Handler, error) {
	srv := &formServer{}
	page := formPage{Title: "Edit a JSON Schema", Subtitle: "A new schema"}
	if schemaArg == "" {
		h, err := jsonhandler.New(ctx, jsonhandler.MetaSchema())
		if err != nil {
			return nil, err
		}
		srv.handler = *h
	} else {
		s, err := loadSchema(ctx, wd, schemaArg, false, false, client)
		if err != nil {
			return nil, err
		}
		srv.handler.Schema = s
		page.Title, page.Subtitle = "Edit a value", "A new value for "+schemaArg
	}
	path := inDir(wd, instance)
	if instance != "" {
		page.Subtitle = instance
		if schemaArg != "" {
			page.Subtitle = instance + " (" + schemaArg + ")"
		}
		body, err := os.ReadFile(path)
		switch {
		case err == nil:
			if !jsontext.Value(body).IsValid() {
				return nil, fmt.Errorf("%s is not valid JSON", instance)
			}
			srv.handler.Value = body
		case errors.Is(err, fs.ErrNotExist) && write:
			// --write creates the file on the first submission.
		default:
			return nil, fmt.Errorf("read instance %s: %w", instance, err)
		}
	}
	srv.handler.SubmitLabel = "Save"
	srv.handler.Outline = true
	srv.handler.OnSubmit = func(_ context.Context, v jsontext.Value) (string, error) {
		out := v.Clone()
		if err := out.Indent(jsontext.WithIndent("  ")); err != nil {
			return "", err
		}
		out = append(out, '\n')
		if !write {
			if _, err := stdout.Write(out); err != nil {
				return "", err
			}
			return "Valid. Printed to standard output.", nil
		}
		if err := os.WriteFile(path, out, 0o644); err != nil {
			return "", err
		}
		srv.saved(v)
		return "Saved to " + instance + ".", nil
	}

	templates, err := jsonhandler.Templates()
	if err != nil {
		return nil, err
	}
	pageTemplate, err := templates.New("page").Parse(formPageTemplate)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	paths := jsonhandler.TemplateRoutes(mux, srv, "")
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		f, err := srv.Form(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		data := page
		data.Form = f.WithActions(paths.SubmitForm(), paths.Validate())
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(f.StatusCode())
		renderPage(w, pageTemplate, data)
	})
	mux.HandleFunc("GET /jsonform.css", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = w.Write(jsonhandler.Stylesheet())
	})
	// The server can write files, so other sites must not be able to
	// drive it: cross-origin posts are refused, and so are requests for
	// any host name but localhost, which stops DNS rebinding.
	return http.NewCrossOriginProtection().Handler(localHostOnly(mux)), nil
}

// localHostOnly refuses requests whose Host is a name other than
// localhost. IP addresses are allowed so the server can listen on other
// interfaces; a rebinding attack needs a name it controls.
func localHostOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		host = strings.Trim(host, "[]")
		if host != "localhost" && net.ParseIP(host) == nil {
			http.Error(w, "this server only answers to localhost or an IP address", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// renderPage writes the page. The status is already sent, so a template
// error can only be reported in the body.
func renderPage(w io.Writer, t *template.Template, data formPage) {
	if err := t.Execute(w, data); err != nil {
		_, _ = io.WriteString(w, template.HTMLEscapeString(err.Error()))
	}
}
