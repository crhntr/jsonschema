package hypertext_test

import (
	"context"
	"database/sql"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/typelate/dom/domtest"
	"github.com/typelate/dom/spec"
	_ "modernc.org/sqlite"

	"github.com/crhntr/jsonschema/examples/issues/internal/database"
	"github.com/crhntr/jsonschema/examples/issues/internal/hypertext"
)

// app is the tracker with a fresh in-memory database.
type app struct {
	t   *testing.T
	mux *http.ServeMux
}

func newApp(t *testing.T) *app {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1) // every connection to :memory: is its own database
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := database.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	srv, err := hypertext.New(context.Background(), db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	hypertext.Routes(mux, srv)
	return &app{t: t, mux: mux}
}

func (a *app) do(req *http.Request) *http.Response {
	a.t.Helper()
	rec := httptest.NewRecorder()
	a.mux.ServeHTTP(rec, req)
	return rec.Result()
}

// page gets a page and parses it, failing unless the status is want.
func (a *app) page(path string, want int) spec.Document {
	a.t.Helper()
	res := a.do(httptest.NewRequest(http.MethodGet, path, nil))
	if res.StatusCode != want {
		a.t.Fatalf("GET %s: status %d, want %d", path, res.StatusCode, want)
	}
	return domtest.ParseResponseDocument(a.t, res)
}

func (a *app) post(path string, form url.Values, htmx bool) *http.Response {
	a.t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	return a.do(req)
}

// save posts a form and expects to be sent on to want.
func (a *app) save(path string, form url.Values, want string) {
	a.t.Helper()
	res := a.post(path, form, false)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != want {
		b, _ := io.ReadAll(res.Body)
		a.t.Fatalf("POST %s: status %d to %q, want 303 to %q\n%s", path, res.StatusCode, res.Header.Get("Location"), want, b)
	}
}

func texts(doc interface {
	QuerySelectorSequence(string) iter.Seq[spec.Element]
}, selector string,
) []string {
	var out []string
	for el := range doc.QuerySelectorSequence(selector) {
		out = append(out, strings.TrimSpace(el.TextContent()))
	}
	return out
}

func TestTracker(t *testing.T) {
	a := newApp(t)

	home := a.page("/", http.StatusOK)
	if got := texts(home, ".project"); !slices.Equal(got, []string{"My project"}) {
		t.Errorf("project = %q", got)
	}
	if got := texts(home, ".counts li"); !slices.Equal(got, []string{"No issues yet."}) {
		t.Errorf("counts = %q", got)
	}

	// Add a contact through the generated form.
	form := a.page("/contacts/new", http.StatusOK)
	if form.QuerySelector(`form#contact[action="/contacts/new/form"] input[name="$/email"][type="email"]`) == nil {
		t.Fatalf("new contact form is missing its email input")
	}
	a.save("/contacts/new/form", url.Values{"@present": {"1"}, "$/name": {"Ada Lovelace"}, "$/email": {"ada@example.com"}, "$/role": {"developer"}}, "/contacts/1")

	// The same email again is refused with a message, not a database error.
	res := a.post("/contacts/new/form", url.Values{"$/name": {"Imposter"}, "$/email": {"ada@example.com"}}, false)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate contact: status %d", res.StatusCode)
	}
	doc := domtest.ParseResponseDocument(t, res)
	if got := texts(doc, "#contact_errors li"); !slices.Equal(got, []string{"That email address already belongs to another contact."}) {
		t.Errorf("duplicate contact errors = %q", got)
	}
	if doc.QuerySelector("header nav") == nil {
		t.Errorf("a browser post got a partial instead of a page")
	}

	// New issues start from the default severity in the settings.
	a.save("/settings/form", url.Values{"@present": {"1"}, "$/project": {"Engine"}, "$/defaultSeverity": {"high"}, "@len/labels": {"1"}, "@key/labels/~k0": {"bug"}, "$/labels/~k0": {"#ff0000"}}, "/")
	form = a.page("/issues/new", http.StatusOK)
	if el := form.QuerySelector(`input[name="$/severity"][value="high"]`); el == nil || !el.HasAttribute("checked") {
		t.Errorf("new issue does not start at the default severity")
	}
	if got := texts(form, ".project"); !slices.Equal(got, []string{"Engine"}) {
		t.Errorf("project after settings = %q", got)
	}

	// An invalid issue comes back with errors; a valid one is saved.
	res = a.post("/issues/new/form", url.Values{"@present": {"1"}, "$/title": {"no"}, "$/severity": {"high"}, "$/status": {"open"}}, true)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("short title: status %d", res.StatusCode)
	}
	doc = domtest.ParseResponseDocument(t, res)
	if doc.QuerySelector("header") != nil {
		t.Errorf("htmx got a whole page instead of the form")
	}
	if got := texts(doc, `[data-pointer="/title"] .jsonform-errors li`); len(got) != 1 {
		t.Errorf("title errors = %q", got)
	}
	issue := url.Values{
		"@present": {"1"}, "$/title": {"Engine stalls at idle"}, "$/severity": {"critical"}, "$/status": {"open"},
		"$/assignee": {"ada@example.com"}, "$/tags": {"", "bug"}, "@len/steps": {"1"}, "$/steps/0/action": {"Start the engine"},
		"@variant/environment": {"1"}, "@present/environment": {"1"}, "$/environment/os": {"linux"}, "$/environment/arch": {"arm64"},
	}
	res = a.post("/issues/new/form", issue, true)
	if res.StatusCode != http.StatusOK || res.Header.Get("HX-Redirect") != "/issues/1" {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("htmx save: status %d, HX-Redirect %q\n%s", res.StatusCode, res.Header.Get("HX-Redirect"), b)
	}

	// The issue page shows the document and links the assignee.
	page := a.page("/issues/1", http.StatusOK)
	if got := texts(page, "h1"); !slices.Equal(got, []string{"Engine stalls at idle"}) {
		t.Errorf("issue title = %q", got)
	}
	if el := page.QuerySelector(`dd a[href="/contacts/1"]`); el == nil || el.TextContent() != "Ada Lovelace" {
		t.Errorf("assignee link = %v", el)
	}
	for _, want := range []string{"linux arm64", "bug", "Start the engine"} {
		if !strings.Contains(page.QuerySelector("main").TextContent(), want) {
			t.Errorf("issue page does not show %q", want)
		}
	}
	if el := page.QuerySelector(".tag"); el == nil || !strings.Contains(el.GetAttribute("style"), "#ff0000") {
		t.Errorf("tag color = %v", el)
	}
	if el := page.QuerySelector(`form#issue[action="/issues/1/form"] input[name="$/title"]`); el == nil || el.GetAttribute("value") != "Engine stalls at idle" {
		t.Errorf("edit form = %v", el)
	}

	// Lists join issues and contacts.
	if got := texts(a.page("/issues", http.StatusOK), "tbody td:nth-child(4)"); !slices.Equal(got, []string{"Ada Lovelace"}) {
		t.Errorf("assignee column = %q", got)
	}
	if got := texts(a.page("/contacts", http.StatusOK), "tbody td:nth-child(4)"); !slices.Equal(got, []string{"1"}) {
		t.Errorf("open issues column = %q", got)
	}

	// Close it from the edit form: the counts follow.
	issue.Set("$/status", "closed")
	a.save("/issues/1/form", issue, "/issues/1")
	if got := texts(a.page("/", http.StatusOK), ".counts li"); !slices.Equal(got, []string{"1 closed"}) {
		t.Errorf("counts after closing = %q", got)
	}
	if got := texts(a.page("/contacts", http.StatusOK), "tbody td:nth-child(4)"); !slices.Equal(got, []string{"0"}) {
		t.Errorf("open issues after closing = %q", got)
	}

	// Validation as you type answers with error lists only.
	res = a.post("/issues/1/form/validate", url.Values{"$/title": {"x"}, "$/severity": {"low"}, "$/status": {"open"}}, true)
	doc = domtest.ParseResponseDocument(t, res)
	if el := doc.QuerySelector("#issue-title_errors"); el == nil || el.GetAttribute("hx-swap-oob") != "true" || el.HasAttribute("hidden") {
		t.Errorf("live title errors = %v", el)
	}

	// Edits re-render the form, as a whole page for a browser.
	res = a.post("/issues/1/form", url.Values{"@len/steps": {"1"}, "$/title": {"Engine"}, "@action": {"add/steps"}}, false)
	doc = domtest.ParseResponseDocument(t, res)
	if res.StatusCode != http.StatusOK || doc.QuerySelector("header nav") == nil || len(texts(doc, `[data-pointer="/steps"] .jsonform-item`)) != 2 {
		t.Errorf("add step: status %d", res.StatusCode)
	}

	// Delete, and missing records are 404 pages.
	a.save("/issues/1/delete", url.Values{}, "/issues")
	if got := texts(a.page("/issues/1", http.StatusNotFound), "h1"); !slices.Equal(got, []string{"Not Found"}) {
		t.Errorf("missing issue = %q", got)
	}
	if res := a.post("/issues/1/delete", url.Values{}, false); res.StatusCode != http.StatusNotFound {
		t.Errorf("deleting a missing issue: status %d", res.StatusCode)
	}
	a.page("/contacts/9", http.StatusNotFound)
	a.save("/contacts/1/delete", url.Values{}, "/contacts")
}

func TestStylesheet(t *testing.T) {
	res := newApp(t).do(httptest.NewRequest(http.MethodGet, "/jsonform.css", nil))
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("Content-Type = %q", ct)
	}
}
