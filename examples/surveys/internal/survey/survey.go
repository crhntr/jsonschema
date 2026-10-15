// Package survey turns a survey definition (what an author writes) into the
// JSON Schema of its answers (what a respondent fills in), and summarizes
// the answers people gave.
package survey

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// Definition is a survey as an author writes it.
type Definition struct {
	Title       string     `json:"title"`
	Description string     `json:"description,omitzero"`
	Questions   []Question `json:"questions"`
}

// Question kinds.
const (
	Text   = "text"
	Choice = "choice"
	Scale  = "scale"
	YesNo  = "yesno"
)

// Question is one question; which fields apply depends on Kind.
type Question struct {
	Kind     string   `json:"kind"`
	Prompt   string   `json:"prompt"`
	Help     string   `json:"help,omitzero"`
	Required bool     `json:"required,omitzero"`
	Long     bool     `json:"long,omitzero"`
	Options  []string `json:"options,omitzero"`
	Multiple bool     `json:"multiple,omitzero"`
	Min      *int     `json:"min,omitzero"`
	Max      *int     `json:"max,omitzero"`
	Low      string   `json:"low,omitzero"`
	High     string   `json:"high,omitzero"`
}

// Parse reads a stored definition.
func Parse(raw []byte) (Definition, error) {
	var d Definition
	err := json.Unmarshal(raw, &d)
	return d, err
}

// Range returns the lowest and highest value of a scale question.
func (q Question) Range() (lo, hi int) {
	lo, hi = 1, 5
	if q.Min != nil {
		lo = *q.Min
	}
	if q.Max != nil {
		hi = *q.Max
	}
	return lo, hi
}

// IDs returns the property name each question's answer has: a short form
// of its prompt, made unique within the survey. Answers are stored under
// these names, so rewording a question starts its answers afresh.
func (d Definition) IDs() []string {
	seen := map[string]int{}
	ids := make([]string, len(d.Questions))
	for i, q := range d.Questions {
		id := slugify(q.Prompt, 40, isWordRune)
		if id == "" {
			id = "q" + strconv.Itoa(i+1)
		}
		seen[id]++
		if n := seen[id]; n > 1 {
			id += "-" + strconv.Itoa(n)
		}
		ids[i] = id
	}
	return ids
}

// slugify lowercases s and joins its words (runs of kept characters) with
// hyphens, stopping before the word that would take it past max bytes.
func slugify(s string, max int, keep func(rune) bool) string {
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !keep(r) })
	var b strings.Builder
	for _, w := range words {
		if b.Len() > 0 {
			if b.Len()+1+len(w) > max {
				break
			}
			b.WriteByte('-')
		} else if len(w) > max {
			w = w[:max]
		}
		b.WriteString(w)
	}
	return strings.ToValidUTF8(b.String(), "")
}

func isWordRune(r rune) bool  { return unicode.IsLetter(r) || unicode.IsDigit(r) }
func isASCIIWord(r rune) bool { return r < unicode.MaxASCII && isWordRune(r) }

// Slug returns a URL-safe name for a survey title, or "" if the title has
// no ASCII letters or digits.
func Slug(title string) string { return slugify(title, 60, isASCIIWord) }

// Schema returns the JSON Schema of the answers to the survey: an object
// with one property per question, the form a respondent fills in.
func (d Definition) Schema() ([]byte, error) {
	ids := d.IDs()
	props := map[string]any{}
	var required []string
	for i, q := range d.Questions {
		p, err := q.schema()
		if err != nil {
			return nil, fmt.Errorf("question %d: %w", i+1, err)
		}
		props[ids[i]] = p
		if q.Required {
			required = append(required, ids[i])
		}
	}
	doc := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"title":                d.Title,
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
		"x-form":               map[string]any{"order": ids},
	}
	if d.Description != "" {
		doc["description"] = d.Description
	}
	if len(required) > 0 {
		doc["required"] = required
	}
	return json.Marshal(doc, json.Deterministic(true))
}

func (q Question) schema() (map[string]any, error) {
	p := map[string]any{"title": q.Prompt}
	if q.Help != "" {
		p["description"] = q.Help
	}
	switch q.Kind {
	case Text:
		p["type"] = "string"
		p["maxLength"] = 500
		if q.Long {
			p["maxLength"] = 4000
			p["x-form"] = map[string]any{"widget": "textarea", "rows": 5}
		}
	case Choice:
		if len(q.Options) < 2 {
			return nil, fmt.Errorf("a choice needs at least two options")
		}
		if q.Multiple {
			p["type"] = "array"
			p["items"] = map[string]any{"type": "string", "enum": q.Options}
			p["uniqueItems"] = true
			if q.Required {
				p["minItems"] = 1
			}
		} else {
			p["type"] = "string"
			p["enum"] = q.Options
			p["x-form"] = map[string]any{"widget": "radio"}
		}
	case Scale:
		lo, hi := q.Range()
		if hi <= lo {
			return nil, fmt.Errorf("a scale must end above where it starts")
		}
		values := make([]int, 0, hi-lo+1)
		for v := lo; v <= hi; v++ {
			values = append(values, v)
		}
		p["type"] = "integer"
		p["enum"] = values
		p["x-form"] = map[string]any{"widget": "radio"}
		if ends := q.ends(lo, hi); ends != "" {
			p["description"] = strings.TrimSpace(q.Help + " " + ends)
		}
	case YesNo:
		p["type"] = "string"
		p["enum"] = []string{"Yes", "No"}
		p["x-form"] = map[string]any{"widget": "radio"}
	default:
		return nil, fmt.Errorf("unknown kind %q", q.Kind)
	}
	return p, nil
}

// ends describes what the ends of a scale mean.
func (q Question) ends(lo, hi int) string {
	var parts []string
	if q.Low != "" {
		parts = append(parts, fmt.Sprintf("%d is %s", lo, q.Low))
	}
	if q.High != "" {
		parts = append(parts, fmt.Sprintf("%d is %s", hi, q.High))
	}
	return strings.Join(parts, ", ") + "."
}

// Result is how people answered one question.
type Result struct {
	Question Question
	ID       string
	// Answered is how many responses answered it.
	Answered int
	// Counts are the answers to a choice, scale or yes/no question, in the
	// order of the options.
	Counts []Count
	// Mean is the average of a scale question's answers.
	Mean float64
	// Texts are the answers to a text question, newest last.
	Texts []string
}

// Count is how many responses gave one answer.
type Count struct {
	Label   string
	N       int
	Percent int // of the responses that answered the question
}

// Summarize tallies responses (objects keyed by question id) per question.
// Answers that do not fit the question, such as an option that has since
// been removed, are counted only in Answered.
func (d Definition) Summarize(responses []jsontext.Value) []Result {
	ids := d.IDs()
	out := make([]Result, len(d.Questions))
	for i, q := range d.Questions {
		out[i] = Result{Question: q, ID: ids[i], Counts: q.labels()}
	}
	for _, raw := range responses {
		var answers map[string]jsontext.Value
		if json.Unmarshal(raw, &answers) != nil {
			continue
		}
		for i := range out {
			if v, ok := answers[out[i].ID]; ok {
				out[i].add(v)
			}
		}
	}
	for i := range out {
		out[i].finish()
	}
	return out
}

// labels lists the answers a question can have, as empty counts.
func (q Question) labels() []Count {
	var out []Count
	switch q.Kind {
	case Choice:
		for _, o := range q.Options {
			out = append(out, Count{Label: o})
		}
	case Scale:
		lo, hi := q.Range()
		for v := lo; v <= hi; v++ {
			out = append(out, Count{Label: strconv.Itoa(v)})
		}
	case YesNo:
		out = []Count{{Label: "Yes"}, {Label: "No"}}
	}
	return out
}

func (r *Result) add(v jsontext.Value) {
	switch r.Question.Kind {
	case Text:
		var s string
		if json.Unmarshal(v, &s) == nil && strings.TrimSpace(s) != "" {
			r.Answered++
			r.Texts = append(r.Texts, s)
		}
	case Choice:
		var picks []string
		if r.Question.Multiple {
			if json.Unmarshal(v, &picks) != nil {
				return
			}
		} else {
			var s string
			if json.Unmarshal(v, &s) != nil {
				return
			}
			picks = []string{s}
		}
		if len(picks) == 0 {
			return
		}
		r.Answered++
		for _, p := range picks {
			r.tally(p)
		}
	case Scale:
		var n int
		if json.Unmarshal(v, &n) != nil {
			return
		}
		r.Answered++
		r.Mean += float64(n)
		r.tally(strconv.Itoa(n))
	case YesNo:
		var s string
		if json.Unmarshal(v, &s) != nil {
			return
		}
		r.Answered++
		r.tally(s)
	}
}

func (r *Result) tally(label string) {
	for i := range r.Counts {
		if r.Counts[i].Label == label {
			r.Counts[i].N++
			return
		}
	}
}

func (r *Result) finish() {
	if r.Answered == 0 {
		return
	}
	if r.Question.Kind == Scale {
		r.Mean /= float64(r.Answered)
	}
	for i := range r.Counts {
		r.Counts[i].Percent = (100*r.Counts[i].N + r.Answered/2) / r.Answered
	}
}
