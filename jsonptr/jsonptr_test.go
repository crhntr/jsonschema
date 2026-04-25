package jsonptr_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/crhntr/jsonschema/jsonptr"
)

// rfc6901Doc is the example document from RFC 6901 §5.
const rfc6901Doc = `{
   "foo": ["bar", "baz"],
   "": 0,
   "a/b": 1,
   "c%d": 2,
   "e^f": 3,
   "g|h": 4,
   "i\\j": 5,
   "k\"l": 6,
   " ": 7,
   "m~n": 8
}`

// TestFindRFC6901 covers every example pointer listed in RFC 6901 §5.
func TestFindRFC6901(t *testing.T) {
	cases := []struct {
		ptr  jsonptr.Pointer
		want string
	}{
		{"", strings.TrimSpace(rfc6901Doc)},
		{"/foo", `["bar", "baz"]`},
		{"/foo/0", `"bar"`},
		{"/", `0`},
		{"/a~1b", `1`},
		{"/c%d", `2`},
		{"/e^f", `3`},
		{"/g|h", `4`},
		{`/i\j`, `5`},
		{`/k"l`, `6`},
		{"/ ", `7`},
		{"/m~0n", `8`},
	}
	for _, tc := range cases {
		t.Run(string(tc.ptr), func(t *testing.T) {
			got, err := jsonptr.Find([]byte(rfc6901Doc), tc.ptr)
			if err != nil {
				t.Fatalf("Find(%q): %v", tc.ptr, err)
			}
			if normalize(string(got)) != normalize(tc.want) {
				t.Errorf("Find(%q) = %s, want %s", tc.ptr, got, tc.want)
			}
		})
	}
}

func TestFindMissingMember(t *testing.T) {
	_, err := jsonptr.Find([]byte(`{"a":1}`), "/b")
	if err == nil || !strings.Contains(err.Error(), "missing object member") {
		t.Errorf("expected missing-member error, got %v", err)
	}
}

func TestFindArrayOutOfRange(t *testing.T) {
	_, err := jsonptr.Find([]byte(`[1,2]`), "/5")
	if err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Errorf("expected out-of-range error, got %v", err)
	}
}

func TestFindArrayNegativeIndex(t *testing.T) {
	_, err := jsonptr.Find([]byte(`[1,2]`), "/-1")
	if err == nil || !strings.Contains(err.Error(), "negative") {
		t.Errorf("expected negative-index error, got %v", err)
	}
}

func TestFindArrayNonNumericToken(t *testing.T) {
	_, err := jsonptr.Find([]byte(`[1,2]`), "/foo")
	if err == nil || !strings.Contains(err.Error(), "not a valid array index") {
		t.Errorf("expected token-not-index error, got %v", err)
	}
}

func TestFindDescendIntoScalar(t *testing.T) {
	_, err := jsonptr.Find([]byte(`{"a":42}`), "/a/0")
	if err == nil || !strings.Contains(err.Error(), "cannot descend") {
		t.Errorf("expected cannot-descend error, got %v", err)
	}
}

func TestFindInvalidPointer(t *testing.T) {
	_, err := jsonptr.Find([]byte(`{}`), "noslash")
	if err == nil || !strings.Contains(err.Error(), "does not begin with") {
		t.Errorf("expected invalid-pointer error, got %v", err)
	}
}

func TestFindInvalidJSON(t *testing.T) {
	_, err := jsonptr.Find([]byte(`{not json`), "/x")
	if err == nil {
		t.Error("expected error parsing invalid JSON")
	}
}

func TestFindPreservesNumberPrecision(t *testing.T) {
	// A float64 round-trip would lose this many digits. Verify the
	// returned bytes match the source verbatim.
	doc := []byte(`{"big":12345678901234567890.0987654321}`)
	got, err := jsonptr.Find(doc, "/big")
	if err != nil {
		t.Fatal(err)
	}
	want := "12345678901234567890.0987654321"
	if string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestPointerTokens(t *testing.T) {
	cases := []struct {
		ptr  jsonptr.Pointer
		want []string
	}{
		{"", nil},
		{"/", []string{""}},
		{"/foo", []string{"foo"}},
		{"/foo/bar", []string{"foo", "bar"}},
		{"/a~1b", []string{"a/b"}},
		{"/m~0n", []string{"m~n"}},
		{"/~01", []string{"~1"}}, // ~0 decoded first then "1" stays.
	}
	for _, tc := range cases {
		t.Run(string(tc.ptr), func(t *testing.T) {
			got := slices.Collect(tc.ptr.Tokens())
			if !equal(got, tc.want) {
				t.Errorf("%q.Tokens() = %v, want %v", tc.ptr, got, tc.want)
			}
		})
	}
}

func TestPointerAppend(t *testing.T) {
	cases := []struct {
		base  jsonptr.Pointer
		token string
		want  jsonptr.Pointer
	}{
		{"", "foo", "/foo"},
		{"/foo", "bar", "/foo/bar"},
		{"", "a/b", "/a~1b"},
		{"", "m~n", "/m~0n"},
		{"/x", "~/", "/x/~0~1"},
	}
	for _, tc := range cases {
		t.Run(string(tc.base)+"+"+tc.token, func(t *testing.T) {
			got := tc.base.Append(tc.token)
			if got != tc.want {
				t.Errorf("Append(%q) = %q, want %q", tc.token, got, tc.want)
			}
		})
	}
}

func TestPointerAppendRoundTrip(t *testing.T) {
	tokens := []string{"foo", "a/b", "m~n", "", "1"}
	var p jsonptr.Pointer
	for _, t := range tokens {
		p = p.Append(t)
	}
	got := slices.Collect(p.Tokens())
	if !equal(got, tokens) {
		t.Errorf("round-trip = %v, want %v", got, tokens)
	}
}

func TestPointerIsRoot(t *testing.T) {
	if !jsonptr.Pointer("").IsRoot() {
		t.Error(`Pointer("").IsRoot() = false`)
	}
	if jsonptr.Pointer("/foo").IsRoot() {
		t.Error(`Pointer("/foo").IsRoot() = true`)
	}
}

func TestPointerValidate(t *testing.T) {
	if err := jsonptr.Pointer("").Validate(); err != nil {
		t.Errorf("root: %v", err)
	}
	if err := jsonptr.Pointer("/").Validate(); err != nil {
		t.Errorf("slash: %v", err)
	}
	if err := jsonptr.Pointer("/foo/bar").Validate(); err != nil {
		t.Errorf("/foo/bar: %v", err)
	}
	if err := jsonptr.Pointer("noslash").Validate(); err == nil {
		t.Error(`"noslash".Validate() returned nil`)
	}
}

// normalize collapses whitespace differences so we can compare jsontext
// output against hand-typed literals.
func normalize(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func equal[T comparable](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
