package jsonschema

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"math/big"
	"net/mail"
	"net/netip"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/idna"

	"github.com/crhntr/jsonschema/jsonptr"
)

// evalScope carries dynamic-scope state through validation: the chain
// of resource roots being evaluated (for $dynamicRef per §8.2.3.2),
// the set of vocabulary flags derived from the current resource's
// $vocabulary declaration, and a few transient toggles.
type evalScope struct {
	resources       []*Schema
	skipPrefixItems bool

	// forceAssertFormat is set by ValidateWithFormatAssertion to make
	// /format an assertion regardless of $vocabulary.
	forceAssertFormat bool

	// Per-vocabulary gates. These are recomputed from the current
	// resource's vocabularies whenever evaluation crosses into a new
	// resource. Default values (zero / false) match the implicit
	// 2020-12 defaults: every gate active except format-assertion.
	skipValidation       bool
	skipApplicator       bool
	skipFormatAnnotation bool
	assertFormat         bool
	skipMetaData         bool
	skipContent          bool
	skipUnevaluated      bool
}

// applyResourceVocabularies updates the per-vocabulary gates on s
// from m's resource-level $vocabulary declaration. forceAssertFormat
// (set by ValidateWithFormatAssertion) wins regardless.
func (s *evalScope) applyResourceVocabularies(m *Schema) {
	var vocabs map[string]bool
	if m != nil && m.resource != nil {
		vocabs = m.resource.vocabularies
	}
	s.skipValidation = !vocabEnabled(vocabs, VocabValidation)
	s.skipApplicator = !vocabEnabled(vocabs, VocabApplicator)
	s.skipFormatAnnotation = !vocabEnabled(vocabs, VocabFormatAnnotation)
	s.assertFormat = vocabEnabled(vocabs, VocabFormatAssertion) || s.forceAssertFormat
	s.skipMetaData = !vocabEnabled(vocabs, VocabMetaData)
	s.skipContent = !vocabEnabled(vocabs, VocabContent)
	s.skipUnevaluated = !vocabEnabled(vocabs, VocabUnevaluated)
}

func (s evalScope) push(resource *Schema) evalScope {
	if resource == nil || (len(s.resources) > 0 && s.resources[len(s.resources)-1] == resource) {
		return s
	}
	out := s
	out.resources = append(out.resources, resource)
	return out
}

func (s evalScope) findDynamicAnchor(name string) *Schema {
	if name == "" {
		return nil
	}
	for _, res := range s.resources {
		if a := res.dynamicAnchors[name]; a != nil {
			return a
		}
	}
	return nil
}

// isPre2020Schema reports whether the schema's declared $schema URL
// references a JSON Schema draft older than 2020-12 (where keywords
// like prefixItems didn't exist).
func isPre2020Schema(schemaURL string) bool {
	if schemaURL == "" {
		return false
	}
	for _, draft := range []string{"draft-04", "draft-06", "draft-07", "2019-09"} {
		if strings.Contains(schemaURL, draft) {
			return true
		}
	}
	return false
}

// dynamicRefAnchor extracts the plain-name fragment from a $dynamicRef
// URI reference (e.g. "extended#meta" -> "meta").
func dynamicRefAnchor(ref string) string {
	if i := strings.LastIndexByte(ref, '#'); i >= 0 {
		return ref[i+1:]
	}
	return ""
}

// patternRegex pairs a compiled regex with the schema it applies and
// the original ECMA-262 source pattern (used to construct keyword
// locations like /patternProperties/<pattern>).
type patternRegex struct {
	pattern string
	re      *regexp.Regexp
	schema  *Schema
}

func compilePatternProperties(pp map[string]*Schema) ([]patternRegex, error) {
	if len(pp) == 0 {
		return nil, nil
	}
	out := make([]patternRegex, 0, len(pp))
	for pat, sub := range pp {
		re, err := compileECMA262(pat)
		if err != nil {
			return nil, fmt.Errorf("patternProperties %q: %w", pat, err)
		}
		out = append(out, patternRegex{pattern: pat, re: re, schema: sub})
	}
	return out, nil
}

// compileECMA262 translates an ECMA-262 regular expression into a Go
// regexp.Regexp, expanding escapes that differ between ECMA and Go's
// RE2: \cX control codes and \s / \S extended whitespace classes.
func compileECMA262(pattern string) (*regexp.Regexp, error) {
	return regexp.Compile(translateECMA262(pattern))
}

const ecma262WhitespaceClass = `[\t\n\v\f\r \x{00A0}\x{1680}\x{2000}-\x{200A}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}\x{FEFF}]`

var ecma262WhitespaceNegClass = "[^" + ecma262WhitespaceClass[1:len(ecma262WhitespaceClass)-1] + "]"

func translateECMA262(pattern string) string {
	var b strings.Builder
	b.Grow(len(pattern))
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if c != '\\' || i+1 >= len(pattern) {
			b.WriteByte(c)
			continue
		}
		next := pattern[i+1]
		switch {
		case next == 'c' && i+2 < len(pattern) && isASCIILetter(pattern[i+2]):
			letter := pattern[i+2] & 0x1F
			fmt.Fprintf(&b, `\x{%02X}`, letter)
			i += 2
		case next == 's':
			b.WriteString(ecma262WhitespaceClass)
			i++
		case next == 'S':
			b.WriteString(ecma262WhitespaceNegClass)
			i++
		default:
			b.WriteByte(c)
			b.WriteByte(next)
			i++
		}
	}
	return b.String()
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// isValidECMA262Regex performs a syntactic check that the pattern uses
// only ECMA 262-recognized escape sequences. Catches escapes like \a
// (bell) that Go's regex accepts but ECMA does not. Doesn't aim for a
// full parse — just rejects unknown letter escapes.
func isValidECMA262Regex(pattern string) bool {
	for i := 0; i < len(pattern); i++ {
		if pattern[i] != '\\' {
			continue
		}
		if i+1 >= len(pattern) {
			return false
		}
		c := pattern[i+1]
		if isASCIILetter(c) {
			switch c {
			case 'd', 'D', 's', 'S', 'w', 'W', 'b', 'B',
				'f', 'n', 'r', 't', 'v',
				'c', 'x', 'u', 'p', 'P', 'k':
				// recognized ECMA 262 letter escapes
			default:
				return false
			}
		}
		i++
	}
	return true
}

// compareRat parses keyword as a big.Rat and reports keyword.Cmp(n).
// ok is false when keyword is empty (i.e. not present in the schema).
func compareRat(keyword jsontext.Value, n *big.Rat) (int, bool) {
	if len(keyword) == 0 {
		return 0, false
	}
	r, ok := new(big.Rat).SetString(string(keyword))
	if !ok {
		return 0, false
	}
	return r.Cmp(n), true
}

// validateFormat enforces format assertions for known JSON Schema 2020-12
// formats. Unknown formats pass silently (per spec, format is an annotation
// by default; we treat known ones as assertions which matches what the
// suite expects).
func validateFormat(format, s string) error {
	switch format {
	case "ipv4":
		addr, err := netip.ParseAddr(s)
		if err != nil || !addr.Is4() || strings.Contains(s, ":") {
			return fmt.Errorf("not a valid ipv4: %q", s)
		}
	case "ipv6":
		addr, err := netip.ParseAddr(s)
		if err != nil || !addr.Is6() || !strings.Contains(s, ":") || addr.Zone() != "" {
			return fmt.Errorf("not a valid ipv6: %q", s)
		}
	case "date-time":
		if !isDateTime(s) {
			return fmt.Errorf("not a valid date-time: %q", s)
		}
	case "date":
		if !isDate(s) {
			return fmt.Errorf("not a valid date: %q", s)
		}
	case "time":
		if !isTime(s) {
			return fmt.Errorf("not a valid time: %q", s)
		}
	case "duration":
		if !isISO8601Duration(s) {
			return fmt.Errorf("not a valid duration: %q", s)
		}
	case "email", "idn-email":
		if err := validateEmailFormat(s, format == "idn-email"); err != nil {
			return err
		}
	case "hostname":
		if !isHostname(s) {
			return fmt.Errorf("not a valid hostname: %q", s)
		}
	case "idn-hostname":
		if !isIDNHostname(s) {
			return fmt.Errorf("not a valid idn-hostname: %q", s)
		}
	case "uri":
		if !isURI(s, true) {
			return fmt.Errorf("not a valid uri: %q", s)
		}
	case "uri-reference":
		if !isURI(s, false) {
			return fmt.Errorf("not a valid uri-reference: %q", s)
		}
	case "iri":
		if !isIRI(s, true) {
			return fmt.Errorf("not a valid iri: %q", s)
		}
	case "iri-reference":
		if !isIRI(s, false) {
			return fmt.Errorf("not a valid iri-reference: %q", s)
		}
	case "uuid":
		if !isUUID(s) {
			return fmt.Errorf("not a valid uuid: %q", s)
		}
	case "regex":
		if !isValidECMA262Regex(s) {
			return fmt.Errorf("not a valid ECMA 262 regex: %q", s)
		}
		if _, err := compileECMA262(s); err != nil {
			return fmt.Errorf("not a valid regex: %w", err)
		}
	case "json-pointer":
		if err := jsonptr.Pointer(s).Validate(); err != nil {
			return fmt.Errorf("not a valid json-pointer: %w", err)
		}
	case "relative-json-pointer":
		if !isRelativeJSONPointer(s) {
			return fmt.Errorf("not a valid relative-json-pointer: %q", s)
		}
	case "uri-template":
		if !isURITemplate(s) {
			return fmt.Errorf("not a valid uri-template: %q", s)
		}
	}
	return nil
}

var (
	uuidRE     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	hostnameRE = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$`)
	dateRE     = regexp.MustCompile(`^([0-9]{4})-([0-9]{2})-([0-9]{2})$`)
	timeRE     = regexp.MustCompile(`^([0-9]{2}):([0-9]{2}):([0-9]{2})(\.[0-9]+)?([Zz]|[+-][0-9]{2}:[0-9]{2})$`)
	dateTimeRE = regexp.MustCompile(`^([0-9]{4})-([0-9]{2})-([0-9]{2})[Tt]([0-9]{2}):([0-9]{2}):([0-9]{2})(\.[0-9]+)?([Zz]|[+-][0-9]{2}:[0-9]{2})$`)
)

func isDate(s string) bool {
	m := dateRE.FindStringSubmatch(s)
	if m == nil {
		return false
	}
	y := atoi(m[1])
	mo := atoi(m[2])
	d := atoi(m[3])
	return validDate(y, mo, d)
}

func isTime(s string) bool {
	m := timeRE.FindStringSubmatch(s)
	if m == nil {
		return false
	}
	h := atoi(m[1])
	mi := atoi(m[2])
	se := atoi(m[3])
	off := m[5]
	return validTime(h, mi, se, off)
}

func isDateTime(s string) bool {
	m := dateTimeRE.FindStringSubmatch(s)
	if m == nil {
		return false
	}
	y := atoi(m[1])
	mo := atoi(m[2])
	d := atoi(m[3])
	h := atoi(m[4])
	mi := atoi(m[5])
	se := atoi(m[6])
	off := m[8]
	return validDate(y, mo, d) && validTime(h, mi, se, off)
}

func atoi(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		n = n*10 + int(s[i]-'0')
	}
	return n
}

func validDate(y, mo, d int) bool {
	if mo < 1 || mo > 12 {
		return false
	}
	if d < 1 || d > daysInMonth(y, mo) {
		return false
	}
	return true
}

func daysInMonth(y, mo int) int {
	switch mo {
	case 1, 3, 5, 7, 8, 10, 12:
		return 31
	case 4, 6, 9, 11:
		return 30
	case 2:
		if (y%4 == 0 && y%100 != 0) || y%400 == 0 {
			return 29
		}
		return 28
	}
	return 0
}

func validTime(h, mi, se int, offset string) bool {
	if h > 23 || mi > 59 {
		return false
	}
	if se > 60 {
		return false
	}
	if se == 60 {
		offH, offM, ok := parseOffset(offset)
		if !ok {
			return false
		}
		hUTC := h - offH
		miUTC := mi - offM
		if miUTC < 0 {
			miUTC += 60
			hUTC--
		}
		if miUTC >= 60 {
			miUTC -= 60
			hUTC++
		}
		if hUTC < 0 {
			hUTC += 24
		}
		if hUTC >= 24 {
			hUTC -= 24
		}
		if hUTC != 23 || miUTC != 59 {
			return false
		}
	}
	if _, _, ok := parseOffset(offset); !ok {
		return false
	}
	return true
}

func parseOffset(s string) (h, m int, ok bool) {
	if s == "Z" || s == "z" {
		return 0, 0, true
	}
	if len(s) != 6 {
		return 0, 0, false
	}
	if s[0] != '+' && s[0] != '-' {
		return 0, 0, false
	}
	h = atoi(s[1:3])
	m = atoi(s[4:6])
	if h > 23 || m > 59 {
		return 0, 0, false
	}
	if s[0] == '-' {
		h, m = -h, -m
	}
	return h, m, true
}

func isUUID(s string) bool { return uuidRE.MatchString(s) }

// validateEmailFormat validates an addr-spec per RFC 5321 (email) or
// RFC 6531 (idn-email).
func validateEmailFormat(s string, idn bool) error {
	if strings.ContainsAny(s, "<>") {
		return fmt.Errorf("not a valid email: %q", s)
	}
	at := strings.LastIndexByte(s, '@')
	if at < 0 || at == 0 || at == len(s)-1 {
		return fmt.Errorf("not a valid email: %q", s)
	}
	local := s[:at]
	domain := s[at+1:]

	if !idn {
		if _, err := mail.ParseAddress(local + "@x"); err != nil {
			return fmt.Errorf("not a valid email local part: %q", s)
		}
	}

	if strings.HasPrefix(domain, "[") && strings.HasSuffix(domain, "]") {
		ip := domain[1 : len(domain)-1]
		ip = strings.TrimPrefix(ip, "IPv6:")
		if _, err := netip.ParseAddr(ip); err != nil {
			return fmt.Errorf("not a valid email IP literal: %q", s)
		}
		return nil
	}
	if idn {
		if !isIDNHostname(domain) {
			return fmt.Errorf("not a valid idn-email domain: %q", s)
		}
		return nil
	}
	if !isHostname(domain) {
		return fmt.Errorf("not a valid email domain: %q", s)
	}
	return nil
}

func isHostname(s string) bool {
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	if !hostnameRE.MatchString(s) {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) >= 4 && label[2] == '-' && label[3] == '-' {
			lower := strings.ToLower(label)
			if !strings.HasPrefix(lower, "xn--") {
				return false
			}
			decoded, err := idnPunycode.ToUnicode(label)
			if err != nil {
				return false
			}
			if !isValidIDNALabel(decoded) {
				return false
			}
		}
	}
	return true
}

// idnStrict is a strict IDNA 2008 profile used for format-assertion of
// host names — rejects invalid Unicode sequences, disallowed
// characters, and bad punycode. Uses MapForLookup which combined with
// ValidateLabels enforces the IDNA 2008 disallowed character set.
var idnStrict = idna.New(
	idna.MapForLookup(),
	idna.Transitional(false),
	idna.StrictDomainName(true),
	idna.ValidateLabels(true),
	idna.VerifyDNSLength(true),
	idna.BidiRule(),
	idna.CheckHyphens(true),
	idna.CheckJoiners(true),
)

// idnPunycode is a profile that decodes A-labels and validates the
// resulting Unicode against the IDNA 2008 disallowed table — used to
// catch disallowed code points hiding inside xn-- labels that the
// surface-level lookup would otherwise accept.
var idnPunycode = idna.New(
	idna.ValidateForRegistration(),
	idna.VerifyDNSLength(true),
)

// isIDNHostname round-trips via the strict profile to catch invalid
// Unicode sequences and disallowed characters in IDN labels.
func isIDNHostname(s string) bool {
	if s == "" || len(s) > 253*4 {
		return false
	}
	for _, sep := range []rune{'.', 0x3002, 0xFF0E, 0xFF61} {
		if strings.HasSuffix(s, string(sep)) {
			return false
		}
	}
	if _, err := idnStrict.ToASCII(s); err != nil {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if !isValidIDNALabel(label) {
			return false
		}
	}
	return true
}

// idna2008DisallowedExceptions is the RFC 5892 §2.6 Exceptions table,
// limited to entries marked DISALLOWED. The Go idna package's
// processing profile decodes these to Unicode but doesn't reject
// them; we enforce the registration-time rule here.
var idna2008DisallowedExceptions = map[rune]bool{
	0x0640: true, // ARABIC TATWEEL
	0x07FA: true, // NKO LAJANYALAN
	0x302E: true, // HANGUL SINGLE DOT TONE MARK
	0x302F: true, // HANGUL DOUBLE DOT TONE MARK
	0x3031: true, // VERTICAL KANA REPEAT MARK
	0x3032: true, // VERTICAL KANA REPEAT WITH VOICED SOUND MARK
	0x3033: true, // VERTICAL KANA REPEAT MARK UPPER HALF
	0x3034: true, // VERTICAL KANA REPEAT WITH VOICED SOUND MARK UPPER HALF
	0x3035: true, // VERTICAL KANA REPEAT MARK LOWER HALF
	0x303B: true, // VERTICAL IDEOGRAPHIC ITERATION MARK
}

// isValidIDNALabel checks the decoded U-label against the IDNA 2008
// disallowed character set (subset: §2.6 Exceptions DISALLOWED entries
// and the most common CONTEXTO rules — middle dot, joiners). Catches
// labels the Go idna processing profile decodes but should reject.
func isValidIDNALabel(label string) bool {
	if label == "" {
		return false
	}
	runes := []rune(label)
	for i, r := range runes {
		if idna2008DisallowedExceptions[r] {
			return false
		}
		switch r {
		case 0x00B7:
			if i == 0 || i == len(runes)-1 {
				return false
			}
			if runes[i-1] != 'l' || runes[i+1] != 'l' {
				return false
			}
		case 0x0375:
			if i == len(runes)-1 || !isGreekLetter(runes[i+1]) {
				return false
			}
		case 0x05F3, 0x05F4:
			if i == 0 || !isHebrewLetter(runes[i-1]) {
				return false
			}
		case 0x30FB:
			if !labelHasCJK(runes) {
				return false
			}
		case 0x200C, 0x200D:
			if i == 0 || i == len(runes)-1 {
				return false
			}
		}
	}
	return true
}

func isGreekLetter(r rune) bool {
	return (r >= 0x0370 && r <= 0x03FF) || (r >= 0x1F00 && r <= 0x1FFF)
}

func isHebrewLetter(r rune) bool {
	return r >= 0x05D0 && r <= 0x05EA
}

func labelHasCJK(runes []rune) bool {
	for _, r := range runes {
		if r == 0x30FB {
			continue
		}
		if (r >= 0x3040 && r <= 0x309F) ||
			(r >= 0x30A0 && r <= 0x30FF) ||
			(r >= 0x4E00 && r <= 0x9FFF) {
			return true
		}
	}
	return false
}

func isISO8601Duration(s string) bool {
	if !strings.HasPrefix(s, "P") || len(s) < 2 {
		return false
	}
	rest := s[1:]
	if strings.HasSuffix(rest, "W") {
		digits := rest[:len(rest)-1]
		return digits != "" && allASCIIDigits(digits)
	}
	var datePart, timePart string
	if i := strings.Index(rest, "T"); i >= 0 {
		datePart = rest[:i]
		timePart = rest[i+1:]
		if timePart == "" {
			return false
		}
	} else {
		datePart = rest
	}
	if datePart == "" && timePart == "" {
		return false
	}
	if datePart != "" && !isDurationDate(datePart) {
		return false
	}
	if timePart != "" && !isDurationTime(timePart) {
		return false
	}
	return true
}

func isDurationDate(s string) bool {
	return parseDurationFields(s, "YMD", true)
}

func isDurationTime(s string) bool {
	return parseDurationFields(s, "HMS", true)
}

func parseDurationFields(s, markers string, gapForbidden bool) bool {
	if s == "" {
		return false
	}
	pos := 0
	seen := [3]bool{}
	for pos < len(s) {
		start := pos
		for pos < len(s) && s[pos] >= '0' && s[pos] <= '9' {
			pos++
		}
		if pos == start || pos == len(s) {
			return false
		}
		marker := s[pos]
		idx := strings.IndexByte(markers, marker)
		if idx < 0 {
			return false
		}
		for j := 0; j <= idx; j++ {
			if j == idx {
				if seen[j] {
					return false
				}
				seen[j] = true
			} else if seen[j] {
				continue
			}
		}
		for j := idx + 1; j < 3; j++ {
			if seen[j] {
				return false
			}
		}
		pos++
	}
	if gapForbidden && seen[0] && seen[2] && !seen[1] {
		return false
	}
	if !seen[0] && !seen[1] && !seen[2] {
		return false
	}
	return true
}

func allASCIIDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isRelativeJSONPointer(s string) bool {
	if s == "" {
		return false
	}
	i := 0
	if s[0] == '0' {
		i = 1
	} else {
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == 0 {
			return false
		}
	}
	rest := s[i:]
	if rest == "#" {
		return true
	}
	return jsonptr.Pointer(rest).Validate() == nil
}

func isURI(s string, requireAbs bool) bool {
	if !isASCII(s) {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isURIChar(s[i]) {
			return false
		}
	}
	if requireAbs {
		u, err := url.Parse(s)
		if err != nil || !u.IsAbs() {
			return false
		}
		return true
	}
	_, err := url.Parse(s)
	return err == nil
}

func isIRI(s string, requireAbs bool) bool {
	for _, c := range s {
		if c < 0x21 || c == ' ' || c == '<' || c == '>' || c == '"' ||
			c == '`' || c == '{' || c == '}' || c == '|' || c == '\\' || c == '^' {
			return false
		}
	}
	if requireAbs {
		u, err := url.Parse(s)
		if err != nil || !u.IsAbs() {
			return false
		}
		return true
	}
	_, err := url.Parse(s)
	return err == nil
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 0x7E || s[i] < 0x20 {
			return false
		}
	}
	return true
}

// isURIChar reports whether c is in the RFC 3986 unreserved /
// reserved / pct-encoded character set.
func isURIChar(c byte) bool {
	switch {
	case c >= 'A' && c <= 'Z',
		c >= 'a' && c <= 'z',
		c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '-', '.', '_', '~',
		':', '/', '?', '#', '[', ']', '@',
		'!', '$', '&', '\'', '(', ')', '*', '+', ',', ';', '=',
		'%':
		return true
	}
	return false
}

func isURITemplate(s string) bool {
	depth := 0
	for _, r := range s {
		switch r {
		case '{':
			if depth > 0 {
				return false
			}
			depth++
		case '}':
			if depth == 0 {
				return false
			}
			depth--
		}
	}
	return depth == 0
}

// decodeJSONString returns the Go string represented by a JSON-encoded
// string value (i.e. the bytes inside the quotes, with escapes
// interpreted).
func decodeJSONString(val jsontext.Value) (string, error) {
	var s string
	if err := json.Unmarshal(val, &s); err != nil {
		return "", err
	}
	return s, nil
}

func typeNames(t *Type) []string {
	if t == nil {
		return nil
	}
	if s, ok := t.TypeString(); ok {
		return []string{string(s)}
	}
	if a, ok := t.TypeArray(); ok {
		out := make([]string, len(a))
		for i, v := range a {
			out[i] = string(v)
		}
		return out
	}
	return nil
}

func typeListString(t *Type) string {
	return strings.Join(typeNames(t), "|")
}

func matchesType(t string, kind jsontext.Kind, val jsontext.Value) bool {
	switch t {
	case "string":
		return kind == jsontext.KindString
	case "number":
		return kind == jsontext.KindNumber
	case "integer":
		return kind == jsontext.KindNumber && numberIsInteger(val)
	case "boolean":
		return kind == jsontext.KindTrue || kind == jsontext.KindFalse
	case "null":
		return kind == jsontext.KindNull
	case "object":
		return kind == jsontext.KindBeginObject
	case "array":
		return kind == jsontext.KindBeginArray
	}
	return false
}

// numberIsInteger reports whether the JSON number bytes represent an
// integer-valued number (no fractional part, or a fractional part that
// is all zeros).
func numberIsInteger(val jsontext.Value) bool {
	val = bytes.TrimSpace(val)
	dot := bytes.IndexByte(val, '.')
	if dot < 0 {
		return true
	}
	end := len(val)
	if e := bytes.IndexAny(val, "eE"); e >= 0 {
		end = e
	}
	for _, c := range val[dot+1 : end] {
		if c != '0' {
			return false
		}
	}
	return true
}
