package jsonhandler

import "testing"

func TestHTMLPattern(t *testing.T) {
	for _, tc := range []struct {
		name, pattern, want string
	}{
		{"plain", "^a", ".*(?:^a).*"},
		{"a class without syntax characters", "^[a-z]+$", ".*(?:^[a-z]+$).*"},
		{"a literal hyphen at the end of a class", "^[a-z0-9_.-]*$", `.*(?:^[a-z0-9_.\-]*$).*`},
		{"a literal hyphen at the start of a class", "[-a]", `.*(?:[\-a]).*`},
		{"a literal hyphen after a negation", "[^-a]", `.*(?:[^\-a]).*`},
		{"parentheses in a class", `^\(\([^()]+\)\)$`, `.*(?:^\(\([^\(\)]+\)\)$).*`},
		{"braces, slash and bar in a class", "[{}/|]", `.*(?:[\{\}\/\|]).*`},
		{"a bracket in a class", `[[]`, `.*(?:[\[]).*`},
		{"a doubled punctuator in a class", "[a&&b]", `.*(?:[a\&\&b]).*`},
		{"escapes are kept", `[\]\-a]`, `.*(?:[\]\-a]).*`},
		{"an escaped bracket outside a class", `\[x\]`, `.*(?:\[x\]).*`},
		{"inline flags have no JavaScript form", "(?i)abc", ""},
		{"Go's named groups have no JavaScript form", "(?P<n>a)", ""},
		{"POSIX classes have no JavaScript form", "[[:alpha:]]", ""},
		{"Go's text anchors have no JavaScript form", `\Aabc\z`, ""},
		// Patterns that end mid-construct are invalid in any dialect; the
		// conversion must still give a result, not panic or loop.
		{"a trailing backslash", `a\`, `.*(?:a\).*`},
		{"an unterminated class", `[`, `.*(?:[).*`},
		{"an unterminated negated class", `[^`, `.*(?:[^).*`},
		{"a class holding only a bracket", `[]`, `.*(?:[\]).*`},
		{"a hyphen ending the pattern inside a class", `[a-`, `.*(?:[a\-).*`},
		{"punctuation ending the pattern inside a class", `[a&`, `.*(?:[a&).*`},
		{"a group opener ending the pattern", `a(`, `.*(?:a().*`},
		{"a lone group modifier", `(?`, ""},
		{"escaped text spans", `\Qa.b\E`, ""},
		{"a Go end-of-text anchor", `abc\Z`, ""},
		{"an escape that is not an anchor", `\d\w\.`, `.*(?:\d\w\.).*`},
		{"JavaScript groups are fine", "(?:a)(?=b)(?!c)(?<=d)(?<!e)(?<n>f)", ".*(?:(?:a)(?=b)(?!c)(?<=d)(?<!e)(?<n>f)).*"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := htmlPattern(tc.pattern); got != tc.want {
				t.Errorf("htmlPattern(%q) = %q, want %q", tc.pattern, got, tc.want)
			}
		})
	}
}
