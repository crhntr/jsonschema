package hypertext_test

import (
	"context"
	"encoding/json/jsontext"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/typelate/dom/domtest"
	"github.com/typelate/dom/spec"

	"github.com/crhntr/jsonschema/examples/surveys/internal/hypertext"
	"github.com/crhntr/jsonschema/examples/surveys/internal/store"
	"github.com/crhntr/jsonschema/examples/surveys/internal/survey"
)

// app is the surveys server over a fresh store holding the sample survey.
type app struct {
	t     *testing.T
	mux   *http.ServeMux
	store *store.Store
	ids   []string // the sample's question ids
}

func newApp(t *testing.T) *app {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile("../../testdata/docs-feedback.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Create("docs-feedback", src); err != nil {
		t.Fatal(err)
	}
	def, err := survey.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := hypertext.New(context.Background(), s, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	hypertext.Routes(mux, srv)
	return &app{t: t, mux: mux, store: s, ids: def.IDs()}
}

func (a *app) do(req *http.Request) *http.Response {
	a.t.Helper()
	rec := httptest.NewRecorder()
	a.mux.ServeHTTP(rec, req)
	return rec.Result()
}

func (a *app) get(path string) *http.Response {
	a.t.Helper()
	return a.do(httptest.NewRequest(http.MethodGet, path, nil))
}

func (a *app) page(path string, want int) spec.Document {
	a.t.Helper()
	res := a.get(path)
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

// controls returns what a browser would submit for a form.
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

func texts(doc interface {
	QuerySelectorSequence(string) iter.Seq[spec.Element]
}, selector string) []string {
	var out []string
	for el := range doc.QuerySelectorSequence(selector) {
		out = append(out, strings.Join(strings.Fields(el.TextContent()), " "))
	}
	return out
}

func TestHome(t *testing.T) {
	a := newApp(t)
	doc := a.page("/", http.StatusOK)
	if got := texts(doc, ".surveys .title"); !slices.Equal(got, []string{"Docs feedback"}) {
		t.Errorf("surveys = %q", got)
	}
	var links []string
	for el := range doc.QuerySelectorSequence(".surveys a") {
		links = append(links, el.GetAttribute("href")+" "+el.TextContent())
	}
	want := []string{"/s/docs-feedback Open the survey", "/surveys/docs-feedback/results 0 responses", "/surveys/docs-feedback/edit Edit"}
	if !slices.Equal(links, want) {
		t.Errorf("links = %q, want %q", links, want)
	}
}

func TestTake(t *testing.T) {
	a := newApp(t)
	doc := a.page("/s/docs-feedback", http.StatusOK)
	form := doc.QuerySelector("form.jsonform")
	if form == nil || form.GetAttribute("action") != "/s/docs-feedback" {
		t.Fatalf("form = %v", form)
	}
	if got := texts(doc, ".jsonform-toolbar button"); !slices.Equal(got, []string{"Edit with wizard"}) {
		t.Errorf("toolbar = %q: a respondent has the wizard but no JSON", got)
	}
	if nav := doc.QuerySelector("header nav"); nav != nil {
		t.Errorf("the public page links to the authors' pages: %s", nav.OuterHTML())
	}
	// Optional radio questions start on a labelled "No answer", not on an
	// option that looks chosen.
	where := form.QuerySelector(`[data-pointer="/` + a.ids[2] + `"]`)
	if got := texts(where, ".jsonform-option"); !slices.Equal(got, []string{"No answer", "At my desk", "On my phone", "In the terminal"}) {
		t.Errorf("where options = %q", got)
	}
	if got := texts(form, `[data-pointer="/`+a.ids[0]+`"] .jsonform-option`); slices.Contains(got, "No answer") {
		t.Errorf("a required question offers No answer: %q", got)
	}
	for _, path := range []string{"/s/nope", "/s/..%2F..%2Fetc", "/s/UPPER", "/surveys/nope/results", "/surveys/nope/edit"} {
		if res := a.get(path); res.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", path, res.StatusCode)
		}
	}
}

func TestAnswers(t *testing.T) {
	a := newApp(t)
	form := a.page("/s/docs-feedback", http.StatusOK).QuerySelector("form.jsonform")

	t.Run("required questions cannot be left blank", func(t *testing.T) {
		res := a.post("/s/docs-feedback", controls(form))
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("status %d", res.StatusCode)
		}
		doc := domtest.ParseResponseDocument(t, res)
		for _, id := range a.ids[:2] {
			if got := texts(doc, `[data-pointer="/`+id+`"] .jsonform-errors li`); len(got) == 0 {
				t.Errorf("no error on required question %s", id)
			}
		}
		if got, _ := a.store.Responses("docs-feedback"); len(got) != 0 {
			t.Errorf("an incomplete response was stored: %s", got)
		}
	})

	t.Run("a complete response is stored once", func(t *testing.T) {
		v := controls(form)
		v.Set("$/"+a.ids[0], "4")
		v.Add("$/"+a.ids[1], "Tutorial")
		v.Add("$/"+a.ids[1], "Agent skills")
		v.Set("$/"+a.ids[3], "Yes")
		res := a.post("/s/docs-feedback", v)
		if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/s/docs-feedback/thanks" {
			t.Fatalf("status %d, location %q", res.StatusCode, res.Header.Get("Location"))
		}
		got, err := a.store.Responses("docs-feedback")
		if err != nil || len(got) != 1 {
			t.Fatalf("stored %d responses (%v)", len(got), err)
		}
		want := `{"` + a.ids[0] + `":4,"` + a.ids[1] + `":["Tutorial","Agent skills"],"` + a.ids[3] + `":"Yes"}`
		if string(got[0]) != want {
			t.Errorf("stored %s, want %s", got[0], want)
		}
		a.page("/s/docs-feedback/thanks", http.StatusOK)
	})

	t.Run("JSON mode is refused", func(t *testing.T) {
		for _, v := range []url.Values{
			{"@action": {"json"}},
			{"@mode": {"json"}, "$": {`{"x":1}`}, "@action": {"refresh"}},
		} {
			res := a.post("/s/docs-feedback", v)
			body, _ := io.ReadAll(res.Body)
			if res.StatusCode != http.StatusOK || strings.Contains(string(body), `name="$"`) || strings.Contains(string(body), `value="json"`) {
				t.Errorf("POST %v: status %d, and the page offers JSON", v, res.StatusCode)
			}
		}
	})

	t.Run("a stale option is refused", func(t *testing.T) {
		v := controls(form)
		v.Set("$/"+a.ids[0], "4")
		v.Add("$/"+a.ids[1], "Podcast")
		if res := a.post("/s/docs-feedback", v); res.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("an option the survey does not have: status %d", res.StatusCode)
		}
	})
}

func TestWizard(t *testing.T) {
	a := newApp(t)
	res := a.post("/s/docs-feedback", url.Values{"@action": {"step"}})
	doc := domtest.ParseResponseDocument(t, res)
	if got := texts(doc, ".jsonform-progress"); len(got) != 1 || !strings.HasPrefix(got[0], "Step 1 of 5: How useful are the docs?") {
		t.Fatalf("progress = %q", got)
	}
	if got := texts(doc, ".jsonform-toolbar button"); !slices.Equal(got, []string{"Edit as form"}) {
		t.Errorf("wizard toolbar = %q", got)
	}
	form := doc.QuerySelector("form.jsonform")
	next := form.QuerySelector("button.jsonform-next")
	if next == nil || next.HasAttribute("formnovalidate") {
		t.Fatalf("no Next that checks required answers: %v", next)
	}
	// Next past a required question left blank is refused by the server too.
	v := controls(form)
	v.Set("@action", next.GetAttribute("value"))
	res = a.post("/s/docs-feedback", v)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("Next past an unanswered required question: status %d", res.StatusCode)
	}
	v.Set("$/"+a.ids[0], "5")
	res = a.post("/s/docs-feedback", v)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Next after answering: status %d", res.StatusCode)
	}
	doc = domtest.ParseResponseDocument(t, res)
	if got := texts(doc, ".jsonform-progress"); len(got) != 1 || !strings.HasPrefix(got[0], "Step 2 of 5") {
		t.Fatalf("progress after Next = %q", got)
	}
	if got := texts(doc, ".jsonform-elsewhere"); len(got) != 0 {
		t.Errorf("arriving at step 2 flags steps nobody reached: %q", got)
	}

	// A browser cannot ask for at least one checkbox, so the server does:
	// Next past an unchecked required group is refused, with the reason.
	form = doc.QuerySelector("form.jsonform")
	v = controls(form)
	v.Set("@action", form.QuerySelector("button.jsonform-next").GetAttribute("value"))
	res = a.post("/s/docs-feedback", v)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("Next past an empty required group: status %d", res.StatusCode)
	}
	got := texts(domtest.ParseResponseDocument(t, res), `[data-pointer="/`+a.ids[1]+`"] .jsonform-errors li`)
	if !slices.Equal(got, []string{"array has 0 items, minItems 1"}) {
		t.Errorf("errors on the group = %q", got)
	}
	v.Add("$/"+a.ids[1], "Examples")
	if res = a.post("/s/docs-feedback", v); res.StatusCode != http.StatusOK {
		t.Errorf("Next with a box checked: status %d", res.StatusCode)
	}
}

func TestResults(t *testing.T) {
	a := newApp(t)
	for _, r := range []string{
		`{"` + a.ids[0] + `":5,"` + a.ids[1] + `":["Tutorial","Reference"],"` + a.ids[3] + `":"Yes","` + a.ids[4] + `":"More recipes, please."}`,
		`{"` + a.ids[0] + `":3,"` + a.ids[1] + `":["Tutorial"],"` + a.ids[2] + `":"Home"}`,
	} {
		if err := a.store.AddResponse("docs-feedback", jsontext.Value(r)); err != nil {
			t.Fatal(err)
		}
	}
	doc := a.page("/surveys/docs-feedback/results", http.StatusOK)
	if got := texts(doc, ".lead"); len(got) != 1 || !strings.HasPrefix(got[0], "2 responses.") {
		t.Errorf("lead = %q", got)
	}
	if got := texts(doc, ".result .answered"); !slices.Equal(got, []string{"2 answered, average 4.0", "2 answered", "1 answered", "1 answered", "1 answered"}) {
		t.Errorf("answered = %q", got)
	}
	var parts []string
	for el := range doc.QuerySelectorSequence(`.result:nth-of-type(2) .counts dt, .result:nth-of-type(2) .counts dd`) {
		if text := strings.TrimSpace(el.TextContent()); text != "" { // the meters have none
			parts = append(parts, text)
		}
	}
	if want := []string{"Tutorial", "2 (100%)", "Reference", "1 (50%)", "Examples", "0 (0%)", "Agent skills", "0 (0%)"}; !slices.Equal(parts, want) {
		t.Errorf("counts = %q, want %q", parts, want)
	}
	if got := texts(doc, ".answers li"); !slices.Equal(got, []string{"More recipes, please."}) {
		t.Errorf("text answers = %q", got)
	}
	if m := doc.QuerySelector(`meter[aria-label="Tutorial: 2"]`); m == nil || m.GetAttribute("max") != "2" || m.GetAttribute("value") != "2" {
		t.Errorf("tutorial meter = %v", m)
	}
}

func TestAuthoring(t *testing.T) {
	a := newApp(t)

	t.Run("the editor shows each question as its kind", func(t *testing.T) {
		doc := a.page("/surveys/docs-feedback/edit", http.StatusOK)
		var kinds []string
		for el := range doc.QuerySelectorSequence(`select[name^="@variant/questions/"] option[selected]`) {
			kinds = append(kinds, el.TextContent())
		}
		want := []string{"Scale", "Multiple choice", "Multiple choice", "Yes or no", "Text answer"}
		if !slices.Equal(kinds, want) {
			t.Errorf("question kinds = %q, want %q", kinds, want)
		}
	})

	t.Run("an unchanged survey saves as it was", func(t *testing.T) {
		before, err := a.store.Get("docs-feedback")
		if err != nil {
			t.Fatal(err)
		}
		v := controls(a.page("/surveys/docs-feedback/edit", http.StatusOK).QuerySelector("form.jsonform"))
		res := a.post("/surveys/docs-feedback/edit", v)
		if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/surveys/docs-feedback/results" {
			t.Fatalf("status %d, location %q", res.StatusCode, res.Header.Get("Location"))
		}
		after, _ := a.store.Get("docs-feedback")
		var b, c jsontext.Value = slices.Clone(before), slices.Clone(after)
		if b.Canonicalize() != nil || c.Canonicalize() != nil || string(b) != string(c) {
			t.Errorf("saving an unchanged form changed the survey:\n%s\n%s", before, after)
		}
	})

	t.Run("a new survey", func(t *testing.T) {
		v := url.Values{
			"$/title":                  {"Lunch"},
			"@len/questions":           {"1"},
			"@variant/questions/0":     {"1"},
			"$/questions/0/prompt":     {"Where shall we eat?"},
			"@len/questions/0/options": {"2"},
			"$/questions/0/options/0":  {"Thai"},
			"$/questions/0/options/1":  {"Pizza"},
			"$/questions/0/required":   {"true"},
		}
		res := a.post("/surveys/new", v)
		if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/surveys/lunch/results" {
			body, _ := io.ReadAll(res.Body)
			t.Fatalf("status %d, location %q\n%s", res.StatusCode, res.Header.Get("Location"), body)
		}
		raw, err := a.store.Get("lunch")
		if err != nil {
			t.Fatal(err)
		}
		def, err := survey.Parse(raw)
		if err != nil || def.Title != "Lunch" || len(def.Questions) != 1 || def.Questions[0].Kind != survey.Choice || !slices.Equal(def.Questions[0].Options, []string{"Thai", "Pizza"}) || !def.Questions[0].Required {
			t.Errorf("stored %s (%v)", raw, err)
		}
		// And it can be taken straight away.
		doc := a.page("/s/lunch", http.StatusOK)
		if got := texts(doc, ".jsonform-option"); !slices.Equal(got, []string{"Thai", "Pizza"}) {
			t.Errorf("the new survey's options = %q", got)
		}
	})

	t.Run("the same title twice is refused with a reason", func(t *testing.T) {
		v := url.Values{
			"$/title":              {"Lunch"},
			"@len/questions":       {"1"},
			"@variant/questions/0": {"3"},
			"$/questions/0/prompt": {"Hungry?"},
		}
		res := a.post("/surveys/new", v)
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("status %d", res.StatusCode)
		}
		body := strings.Join(texts(domtest.ParseResponseDocument(t, res), ".jsonform-errors li, .jsonform-failure"), " ")
		if !strings.Contains(body, `There is already a survey called "lunch".`) {
			t.Errorf("no explanation in %q", body)
		}
	})

	t.Run("an invalid survey is shown on the form and not saved", func(t *testing.T) {
		v := url.Values{
			"$/title":                  {"Pick one"},
			"@len/questions":           {"1"},
			"@variant/questions/0":     {"1"},
			"$/questions/0/prompt":     {"Which?"},
			"@len/questions/0/options": {"1"},
			"$/questions/0/options/0":  {"Only"},
		}
		res := a.post("/surveys/new", v)
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("status %d", res.StatusCode)
		}
		if _, err := a.store.Get("pick-one"); err == nil {
			t.Errorf("an invalid survey was saved")
		}
	})

	t.Run("delete", func(t *testing.T) {
		if res := a.post("/surveys/lunch/delete", nil); res.StatusCode != http.StatusSeeOther {
			t.Fatalf("status %d", res.StatusCode)
		}
		a.page("/s/lunch", http.StatusNotFound)
		if res := a.post("/surveys/lunch/delete", nil); res.StatusCode != http.StatusNotFound {
			t.Errorf("deleting again: status %d", res.StatusCode)
		}
	})
}
