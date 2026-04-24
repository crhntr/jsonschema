package jsonptr_test

import (
	"strings"
	"testing"

	"github.com/go-json-experiment/json/jsontext"

	"github.com/crhntr/jsonschema/jsonptr"
)

type address struct {
	Street string `json:"street"`
	City   string `json:"city,omitempty"`
}

type person struct {
	Name    string    `json:"name"`
	Age     int       `json:"age"`
	Pets    []string  `json:"pets"`
	Home    address   `json:"home"`
	Friends []*person `json:"friends,omitempty"`
	Secret  string    `json:"-"`
	skip    string    //nolint:unused // intentionally unexported
}

func TestFindValueStruct(t *testing.T) {
	p := &person{
		Name: "Ada",
		Age:  37,
		Pets: []string{"cat", "owl"},
		Home: address{Street: "1 Analytical Way", City: "London"},
	}

	cases := []struct {
		ptr     jsonptr.Pointer
		wantRaw string
		wantGo  any
	}{
		{"/name", `"Ada"`, "Ada"},
		{"/age", `37`, 37},
		{"/pets/1", `"owl"`, "owl"},
		{"/home/city", `"London"`, "London"},
	}
	for _, tc := range cases {
		t.Run(string(tc.ptr), func(t *testing.T) {
			raw, live, err := jsonptr.FindValue(tc.ptr, p)
			if err != nil {
				t.Fatal(err)
			}
			if normalize(string(raw)) != normalize(tc.wantRaw) {
				t.Errorf("raw = %s, want %s", raw, tc.wantRaw)
			}
			if !equalAny(live, tc.wantGo) {
				t.Errorf("live = %#v (%T), want %#v (%T)", live, live, tc.wantGo, tc.wantGo)
			}
		})
	}
}

func TestFindValueRoot(t *testing.T) {
	p := &person{Name: "Bo", Age: 1}
	raw, live, err := jsonptr.FindValue("", p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"name":"Bo"`) {
		t.Errorf("raw missing name: %s", raw)
	}
	// FindValue dereferences pointers and interfaces uniformly — at the
	// root that means *person becomes person.
	if _, ok := live.(person); !ok {
		t.Errorf("live = %T, want person", live)
	}
}

func TestFindValuePointerChain(t *testing.T) {
	pointee := &person{Name: "Ada", Friends: []*person{{Name: "Bo"}}}
	pp := &pointee
	raw, _, err := jsonptr.FindValue("/friends/0/name", pp)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `"Bo"` {
		t.Errorf("raw = %s, want \"Bo\"", raw)
	}
}

func TestFindValueMapStringAny(t *testing.T) {
	in := map[string]any{
		"top": map[string]any{
			"list": []any{"a", "b", "c"},
		},
	}
	raw, live, err := jsonptr.FindValue("/top/list/2", in)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `"c"` {
		t.Errorf("raw = %s, want \"c\"", raw)
	}
	if live != "c" {
		t.Errorf("live = %v, want c", live)
	}
}

// stamp implements json.Marshaler so we can verify FindValue switches to
// byte-mode when it hits a custom marshaler.
type stamp struct{ secret string }

func (s stamp) MarshalJSON() ([]byte, error) {
	return []byte(`{"label":"S:` + s.secret + `","kind":"stamp"}`), nil
}

func TestFindValueCrossesMarshaler(t *testing.T) {
	// stamp is wrapped in a struct so the descent has to enter it before
	// hitting the marshaler boundary.
	type letter struct {
		Mark stamp `json:"mark"`
	}
	in := letter{Mark: stamp{secret: "hi"}}

	raw, live, err := jsonptr.FindValue("/mark/label", in)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `"S:hi"` {
		t.Errorf("raw = %s, want \"S:hi\"", raw)
	}
	if live != "S:hi" {
		t.Errorf("live = %v, want S:hi", live)
	}
}

func TestFindValueMarshalerAtRoot(t *testing.T) {
	in := stamp{secret: "x"}
	raw, _, err := jsonptr.FindValue("/kind", in)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `"stamp"` {
		t.Errorf("raw = %s, want \"stamp\"", raw)
	}
}

// pointerStamp implements json.Marshaler with a *pointer* receiver so we
// can verify hasCustomJSON's addressable-pointer branch.
type pointerStamp struct{ secret string }

func (p *pointerStamp) MarshalJSON() ([]byte, error) {
	return []byte(`{"label":"P:` + p.secret + `","kind":"pstamp"}`), nil
}

func TestFindValuePointerReceiverMarshaler(t *testing.T) {
	type letter struct {
		Mark pointerStamp `json:"mark"`
	}
	// Pass a pointer to the outer so the embedded field is addressable —
	// pointer-receiver marshalers are only detected on addressable values.
	in := &letter{Mark: pointerStamp{secret: "yo"}}

	raw, live, err := jsonptr.FindValue("/mark/label", in)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `"P:yo"` {
		t.Errorf("raw = %s, want \"P:yo\"", raw)
	}
	if live != "P:yo" {
		t.Errorf("live = %v, want P:yo", live)
	}
}

// streamStamp implements jsontext.MarshalerTo (v2 streaming marshal)
// instead of json.Marshaler.
type streamStamp struct{ secret string }

func (s streamStamp) MarshalJSONTo(enc *jsontext.Encoder) error {
	return enc.WriteValue([]byte(`{"label":"V2:` + s.secret + `","kind":"v2"}`))
}

func TestFindValueV2Marshaler(t *testing.T) {
	type letter struct {
		Mark streamStamp `json:"mark"`
	}
	in := letter{Mark: streamStamp{secret: "ok"}}

	raw, live, err := jsonptr.FindValue("/mark/label", in)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `"V2:ok"` {
		t.Errorf("raw = %s, want \"V2:ok\"", raw)
	}
	if live != "V2:ok" {
		t.Errorf("live = %v, want V2:ok", live)
	}
}

// streamStampPointer implements jsontext.MarshalerTo with a pointer
// receiver — exercises the addressable-pointer + v2 branch.
type streamStampPointer struct{ secret string }

func (s *streamStampPointer) MarshalJSONTo(enc *jsontext.Encoder) error {
	return enc.WriteValue([]byte(`{"label":"PV2:` + s.secret + `"}`))
}

func TestFindValueV2PointerReceiver(t *testing.T) {
	type letter struct {
		Mark streamStampPointer `json:"mark"`
	}
	in := &letter{Mark: streamStampPointer{secret: "z"}}

	raw, _, err := jsonptr.FindValue("/mark/label", in)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `"PV2:z"` {
		t.Errorf("raw = %s, want \"PV2:z\"", raw)
	}
}

func TestFindValueMissingField(t *testing.T) {
	p := &person{Name: "x"}
	_, _, err := jsonptr.FindValue("/nope", p)
	if err == nil || !strings.Contains(err.Error(), "no struct field") {
		t.Errorf("expected no-field error, got %v", err)
	}
}

func TestFindValueSkipsTaggedDash(t *testing.T) {
	p := &person{Secret: "sssh"}
	_, _, err := jsonptr.FindValue("/Secret", p)
	if err == nil {
		t.Error("expected error: json:\"-\" field should be unreachable")
	}
}

func TestFindValueSkipsUnexported(t *testing.T) {
	p := &person{skip: "x"}
	_, _, err := jsonptr.FindValue("/skip", p)
	if err == nil {
		t.Error("expected error: unexported field should be unreachable")
	}
}

func TestFindValueArrayBounds(t *testing.T) {
	in := []int{1, 2, 3}
	if _, _, err := jsonptr.FindValue("/5", in); err == nil {
		t.Error("expected out-of-range error")
	}
	if _, _, err := jsonptr.FindValue("/-1", in); err == nil {
		t.Error("expected negative-index error")
	}
	if _, _, err := jsonptr.FindValue("/foo", in); err == nil {
		t.Error("expected non-numeric error")
	}
}

func TestFindValueDescendIntoScalar(t *testing.T) {
	p := &person{Name: "z"}
	_, _, err := jsonptr.FindValue("/name/0", p)
	if err == nil || !strings.Contains(err.Error(), "cannot descend") {
		t.Errorf("expected cannot-descend error, got %v", err)
	}
}

func TestFindValueNilPointer(t *testing.T) {
	var p *person
	_, _, err := jsonptr.FindValue("/name", p)
	if err == nil || !strings.Contains(err.Error(), "nil") {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestFindValueInvalidPointer(t *testing.T) {
	_, _, err := jsonptr.FindValue("noslash", map[string]any{})
	if err == nil {
		t.Error("expected invalid-pointer error")
	}
}

func TestFindValueReturnsJSONTextValue(t *testing.T) {
	// Ensure the first return is a jsontext.Value (not a []byte alias),
	// since callers will type-assert it.
	raw, _, err := jsonptr.FindValue("/age", &person{Age: 7})
	if err != nil {
		t.Fatal(err)
	}
	var _ jsontext.Value = raw // compile-time check
}

// equalAny compares Go values for test purposes, treating numeric kinds
// loosely so that map[string]any decoding (float64) matches typed ints.
func equalAny(a, b any) bool {
	switch av := a.(type) {
	case int:
		switch bv := b.(type) {
		case int:
			return av == bv
		case float64:
			return float64(av) == bv
		}
	case float64:
		switch bv := b.(type) {
		case int:
			return av == float64(bv)
		case float64:
			return av == bv
		}
	}
	return a == b
}
