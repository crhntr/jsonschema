package jsonschema

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
)


// Resolver fetches and links JSON Schema 2020-12 documents.
//
// The zero value is usable; Client defaults to http.DefaultClient. The
// resolver caches each resource (top-level document or embedded $id schema)
// by its absolute URI so that repeated resolutions reuse already-fetched
// data. The cache is internal — callers cannot inspect or seed it.
type Resolver struct {
	Client *http.Client

	mu    sync.Mutex
	cache map[string]*Schema // key = absolute resource URI (no fragment)
}

// Resolve fetches the schema at rawURL, transitively fetches every referenced
// schema, and links every $ref / $dynamicRef to its lexical-scope target. The
// returned Schema is the resource root for rawURL (or the document root when
// rawURL has no $id). Use (*Schema).Resolved to follow links.
func (r *Resolver) Resolve(ctx context.Context, rawURL string) (*Schema, error) {
	if r.Client == nil {
		r.Client = http.DefaultClient
	}

	rootURI, err := absoluteResourceURI(rawURL)
	if err != nil {
		return nil, err
	}

	if err := r.loadResource(ctx, rootURI); err != nil {
		return nil, err
	}
	if err := r.linkAll(); err != nil {
		return nil, err
	}

	r.mu.Lock()
	root := r.cache[rootURI]
	r.mu.Unlock()
	if root == nil {
		return nil, fmt.Errorf("resolve: %q not found after load", rootURI)
	}
	return root, nil
}

// Resolve is a convenience wrapper that constructs a one-shot Resolver.
func Resolve(ctx context.Context, client *http.Client, rawURL string) (*Schema, error) {
	r := &Resolver{Client: client}
	return r.Resolve(ctx, rawURL)
}

// Load ingests a pre-fetched JSON Schema document at absURI into the
// resolver's cache. The document is parsed and indexed (embedded $id
// resources, $anchor / $dynamicAnchor) but no HTTP requests are made;
// external $refs discovered during indexing are left for a subsequent
// call to Resolve to fetch (or are matched against schemas already
// cached via prior Load / LoadFS calls).
//
// absURI must be absolute. If the document declares its own $id the
// resolver also caches it under that $id.
func (r *Resolver) Load(absURI string, body []byte) (*Schema, error) {
	abs, err := absoluteResourceURI(absURI)
	if err != nil {
		return nil, err
	}
	doc, _, err := r.ingest(abs, body)
	return doc, err
}

// LoadFS reads each fs.Glob match for the given patterns from fsys, parses
// it as a JSON Schema, and registers it in the cache under the schema's
// own $id. Files lacking $id are an error. Modeled after
// (*template.Template).ParseFS.
func (r *Resolver) LoadFS(fsys fs.FS, patterns ...string) error {
	if len(patterns) == 0 {
		return fmt.Errorf("jsonschema: LoadFS requires at least one pattern")
	}
	seen := map[string]bool{}
	for _, pat := range patterns {
		matches, err := fs.Glob(fsys, pat)
		if err != nil {
			return fmt.Errorf("jsonschema: glob %q: %w", pat, err)
		}
		if len(matches) == 0 {
			return fmt.Errorf("jsonschema: pattern %q matched no files", pat)
		}
		for _, name := range matches {
			if seen[name] {
				continue
			}
			seen[name] = true
			body, err := fs.ReadFile(fsys, name)
			if err != nil {
				return fmt.Errorf("jsonschema: read %s: %w", name, err)
			}
			doc, err := Parse(body)
			if err != nil {
				return fmt.Errorf("jsonschema: parse %s: %w", name, err)
			}
			obj, ok := doc.TypeObject()
			if !ok {
				return fmt.Errorf("jsonschema: %s: top-level boolean schema has no $id", name)
			}
			if obj.ID == "" {
				return fmt.Errorf("jsonschema: %s: missing $id", name)
			}
			if _, err := r.Load(obj.ID, body); err != nil {
				return fmt.Errorf("jsonschema: load %s: %w", name, err)
			}
		}
	}
	return nil
}

// loadResource fetches absURI (if not cached), parses it, and indexes every
// resource and anchor inside it. Recursively fetches any $ref / $dynamicRef
// targets it discovers. Resources are deduplicated by absolute URI.
func (r *Resolver) loadResource(ctx context.Context, absURI string) error {
	r.mu.Lock()
	if existing, ok := r.cache[absURI]; ok && existing != nil {
		r.mu.Unlock()
		return nil
	}
	if r.cache == nil {
		r.cache = map[string]*Schema{}
	}
	// reserve the slot so recursive calls don't refetch.
	r.cache[absURI] = nil
	r.mu.Unlock()

	buf, err := r.fetch(ctx, absURI)
	if err != nil {
		return err
	}
	_, external, err := r.ingest(absURI, buf)
	if err != nil {
		return err
	}
	for _, ext := range external {
		if err := r.loadResource(ctx, ext); err != nil {
			return err
		}
	}
	return nil
}

// ingest parses body, walks the tree to register the resource(s) and
// anchors it contains, and returns the document plus the deduplicated set
// of external URIs referenced by $ref / $dynamicRef.
func (r *Resolver) ingest(absURI string, body []byte) (*Schema, []string, error) {
	doc, err := Parse(body)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve: parse %q: %w", absURI, err)
	}

	external, err := r.indexDocument(doc, absURI)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve: index %q: %w", absURI, err)
	}

	// indexDocument always caches under absURI; if the doc had its own
	// $id, indexSubtree also caches under that. Nothing more to do.
	return doc, external, nil
}

func (r *Resolver) fetch(ctx context.Context, absURI string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, absURI, nil)
	if err != nil {
		return nil, fmt.Errorf("resolve: new request for %q: %w", absURI, err)
	}
	req.Header.Set("Accept", "application/schema+json, application/json")
	resp, err := r.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("resolve: GET %q: %w", absURI, err)
	}
	defer closeAndIgnoreError(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("resolve: GET %q: %s", absURI, resp.Status)
	}
	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("resolve: read %q: %w", absURI, err)
	}
	return buf, nil
}

func closeAndIgnoreError(c io.Closer) { _ = c.Close() }

// indexDocument walks doc, sets baseURI / resource on every Meta, registers
// resource roots ($id) and anchors ($anchor / $dynamicAnchor), and returns
// the set of *external* absolute URIs that need to be fetched (i.e. $refs
// pointing outside this document, deduplicated).
func (r *Resolver) indexDocument(doc *Meta, fetchURI string) ([]string, error) {
	seenExternal := map[string]struct{}{}
	var external []string

	// We pre-register the doc as a resource keyed by fetchURI; if it has
	// its own $id, indexSubtree will register the $id-keyed resource too.
	doc.baseURI = fetchURI
	doc.resource = doc
	doc.anchors = map[string]*Meta{}
	doc.dynamicAnchors = map[string]*Meta{}

	r.mu.Lock()
	if r.cache == nil {
		r.cache = map[string]*Schema{}
	}
	r.cache[fetchURI] = doc
	r.mu.Unlock()

	if err := r.indexSubtree(doc, fetchURI, doc, seenExternal, &external); err != nil {
		return nil, err
	}
	return external, nil
}

// indexSubtree recurses through every subschema. base is the lexical base URI
// in effect; resource is the current resource root.
func (r *Resolver) indexSubtree(m *Meta, base string, resource *Meta, seenExternal map[string]struct{}, external *[]string) error {
	if m == nil {
		return nil
	}
	obj, ok := m.TypeObject()
	if !ok {
		// boolean schema: still tag it.
		m.baseURI = base
		m.resource = resource
		return nil
	}

	// $id introduces a new resource. Resolve relative to current base.
	if obj.ID != "" {
		newBase, err := resolveRelative(base, obj.ID)
		if err != nil {
			return fmt.Errorf("$id %q: %w", obj.ID, err)
		}
		newBase = stripFragment(newBase)
		// embedded resource: this Meta becomes the new resource root.
		if m != resource || base != newBase {
			m.anchors = map[string]*Meta{}
			m.dynamicAnchors = map[string]*Meta{}
			resource = m
			r.mu.Lock()
			r.cache[newBase] = m
			// share underlying source with enclosing document if known.
			if m.source == nil && resource.source != nil {
				m.source = resource.source
			}
			r.mu.Unlock()
		}
		base = newBase
	}

	m.baseURI = base
	m.resource = resource

	if obj.Anchor != "" {
		resource.anchors[obj.Anchor] = m
	}
	if obj.DynamicAnchor != "" {
		resource.dynamicAnchors[obj.DynamicAnchor] = m
	}

	if obj.Ref != "" {
		if err := r.recordExternal(base, obj.Ref, seenExternal, external); err != nil {
			return err
		}
	}
	if obj.DynamicRef != "" {
		if err := r.recordExternal(base, obj.DynamicRef, seenExternal, external); err != nil {
			return err
		}
	}

	for _, c := range obj.Defs {
		if err := r.indexSubtree(c, base, resource, seenExternal, external); err != nil {
			return err
		}
	}
	for _, c := range obj.Properties {
		if err := r.indexSubtree(c, base, resource, seenExternal, external); err != nil {
			return err
		}
	}
	for i := range obj.AllOf {
		if err := r.indexSubtree(&obj.AllOf[i], base, resource, seenExternal, external); err != nil {
			return err
		}
	}
	for i := range obj.AnyOf {
		if err := r.indexSubtree(&obj.AnyOf[i], base, resource, seenExternal, external); err != nil {
			return err
		}
	}
	for i := range obj.OneOf {
		if err := r.indexSubtree(&obj.OneOf[i], base, resource, seenExternal, external); err != nil {
			return err
		}
	}
	for _, child := range []*Meta{obj.If, obj.Then, obj.Else, obj.Not, obj.Items, obj.AdditionalProperties, obj.PropertyNames} {
		if err := r.indexSubtree(child, base, resource, seenExternal, external); err != nil {
			return err
		}
	}
	return nil
}

func (r *Resolver) recordExternal(base, ref string, seen map[string]struct{}, out *[]string) error {
	target, err := resolveRelative(base, ref)
	if err != nil {
		return fmt.Errorf("ref %q: %w", ref, err)
	}
	target = stripFragment(target)
	if target == "" {
		return nil
	}
	r.mu.Lock()
	_, cached := r.cache[target]
	r.mu.Unlock()
	if cached {
		return nil
	}
	if _, dup := seen[target]; dup {
		return nil
	}
	seen[target] = struct{}{}
	*out = append(*out, target)
	return nil
}

// linkAll runs Phase B over every cached resource: every $ref / $dynamicRef
// gets its resolved pointer set; bookended $dynamicRefs get marked dynamic.
func (r *Resolver) linkAll() error {
	r.mu.Lock()
	resources := make([]*Meta, 0, len(r.cache))
	seen := map[*Meta]struct{}{}
	for _, m := range r.cache {
		if m == nil {
			continue
		}
		if _, ok := seen[m]; ok {
			continue
		}
		seen[m] = struct{}{}
		resources = append(resources, m)
	}
	r.mu.Unlock()

	for _, root := range resources {
		if err := r.linkSubtree(root); err != nil {
			return err
		}
	}
	return nil
}

func (r *Resolver) linkSubtree(m *Meta) error {
	if m == nil {
		return nil
	}
	obj, ok := m.TypeObject()
	if !ok {
		return nil
	}

	if obj.Ref != "" {
		target, _, err := r.resolveReference(m.baseURI, obj.Ref, false)
		if err != nil {
			return fmt.Errorf("$ref %q at %q: %w", obj.Ref, m.baseURI, err)
		}
		m.resolved = target
	}
	if obj.DynamicRef != "" {
		target, bookended, err := r.resolveReference(m.baseURI, obj.DynamicRef, true)
		if err != nil {
			return fmt.Errorf("$dynamicRef %q at %q: %w", obj.DynamicRef, m.baseURI, err)
		}
		m.resolved = target
		m.dynamic = bookended
	}

	for _, c := range obj.Defs {
		if err := r.linkSubtree(c); err != nil {
			return err
		}
	}
	for _, c := range obj.Properties {
		if err := r.linkSubtree(c); err != nil {
			return err
		}
	}
	for i := range obj.AllOf {
		if err := r.linkSubtree(&obj.AllOf[i]); err != nil {
			return err
		}
	}
	for i := range obj.AnyOf {
		if err := r.linkSubtree(&obj.AnyOf[i]); err != nil {
			return err
		}
	}
	for i := range obj.OneOf {
		if err := r.linkSubtree(&obj.OneOf[i]); err != nil {
			return err
		}
	}
	for _, child := range []*Meta{obj.If, obj.Then, obj.Else, obj.Not, obj.Items, obj.AdditionalProperties, obj.PropertyNames} {
		if err := r.linkSubtree(child); err != nil {
			return err
		}
	}
	return nil
}

// resolveReference resolves ref against base, returning the target *Meta.
// When dynamic is true, also reports whether the reference is bookended by a
// matching $dynamicAnchor in the initial target's resource (per §8.2.3.2).
func (r *Resolver) resolveReference(base, ref string, dynamic bool) (*Meta, bool, error) {
	abs, err := resolveRelative(base, ref)
	if err != nil {
		return nil, false, err
	}
	u, err := url.Parse(abs)
	if err != nil {
		return nil, false, err
	}
	frag := u.Fragment
	u.Fragment = ""
	resourceURI := u.String()

	r.mu.Lock()
	resource := r.cache[resourceURI]
	r.mu.Unlock()
	if resource == nil {
		return nil, false, fmt.Errorf("resource %q not loaded", resourceURI)
	}

	target, isPlainName, err := resolveFragment(resource, frag)
	if err != nil {
		return nil, false, err
	}

	if dynamic && isPlainName {
		// Bookending: if the *initial target's resource* declares the
		// same $dynamicAnchor name, the reference is dynamic. The
		// validator will at runtime walk the dynamic scope (outermost
		// to innermost) for the first resource with the same anchor.
		if _, ok := target.resource.dynamicAnchors[frag]; ok {
			return target, true, nil
		}
	}
	return target, false, nil
}

// resolveFragment resolves frag within resource. Returns target, whether the
// fragment was a plain name (vs JSON Pointer or empty), and any error.
func resolveFragment(resource *Meta, frag string) (*Meta, bool, error) {
	if frag == "" {
		return resource, false, nil
	}
	// JSON Pointer fragments start with "/" (or are empty after the "#").
	if strings.HasPrefix(frag, "/") {
		target, err := walkJSONPointer(resource, frag)
		if err != nil {
			return nil, false, err
		}
		return target, false, nil
	}
	// Plain name: $anchor or $dynamicAnchor lookup.
	decoded, err := url.PathUnescape(frag)
	if err != nil {
		return nil, false, fmt.Errorf("fragment %q: %w", frag, err)
	}
	if a := resource.anchors[decoded]; a != nil {
		return a, true, nil
	}
	if a := resource.dynamicAnchors[decoded]; a != nil {
		return a, true, nil
	}
	return nil, true, fmt.Errorf("anchor %q not found in resource %q", decoded, resource.baseURI)
}

// walkJSONPointer follows an RFC 6901 JSON Pointer through a Meta tree. The
// pointer is interpreted as a path through the JSON-shaped representation of
// the schema, so e.g. "/$defs/foo/properties/bar" descends through Meta
// fields that correspond to those JSON keys.
func walkJSONPointer(m *Meta, ptr string) (*Meta, error) {
	if ptr == "" {
		return m, nil
	}
	if !strings.HasPrefix(ptr, "/") {
		return nil, fmt.Errorf("invalid JSON Pointer %q", ptr)
	}
	raw := strings.Split(ptr[1:], "/")
	tokens := make([]string, len(raw))
	for i, t := range raw {
		tokens[i] = unescapeJSONPointerToken(t)
	}
	cur := m
	i := 0
	for i < len(tokens) {
		obj, ok := cur.TypeObject()
		if !ok {
			return nil, fmt.Errorf("JSON Pointer %q: cannot descend into boolean schema", ptr)
		}
		tok := tokens[i]
		switch tok {
		case "$defs", "$vocabulary":
			if i+1 >= len(tokens) {
				return nil, fmt.Errorf("JSON Pointer %q: trailing key required after %s", ptr, tok)
			}
			next, ok := obj.Defs[tokens[i+1]]
			if !ok || next == nil {
				return nil, fmt.Errorf("JSON Pointer %q: %s/%s missing", ptr, tok, tokens[i+1])
			}
			cur = next
			i += 2
		case "properties", "patternProperties":
			if i+1 >= len(tokens) {
				return nil, fmt.Errorf("JSON Pointer %q: trailing key required after %s", ptr, tok)
			}
			next, ok := obj.Properties[tokens[i+1]]
			if !ok || next == nil {
				return nil, fmt.Errorf("JSON Pointer %q: %s/%s missing", ptr, tok, tokens[i+1])
			}
			cur = next
			i += 2
		case "allOf", "anyOf", "oneOf":
			if i+1 >= len(tokens) {
				return nil, fmt.Errorf("JSON Pointer %q: trailing index required after %s", ptr, tok)
			}
			idx, err := strconv.Atoi(tokens[i+1])
			if err != nil {
				return nil, fmt.Errorf("JSON Pointer %q: %s index %q: %w", ptr, tok, tokens[i+1], err)
			}
			var arr []Meta
			switch tok {
			case "allOf":
				arr = obj.AllOf
			case "anyOf":
				arr = obj.AnyOf
			case "oneOf":
				arr = obj.OneOf
			}
			if idx < 0 || idx >= len(arr) {
				return nil, fmt.Errorf("JSON Pointer %q: %s index %d out of range", ptr, tok, idx)
			}
			cur = &arr[idx]
			i += 2
		case "if":
			if obj.If == nil {
				return nil, fmt.Errorf("JSON Pointer %q: if not present", ptr)
			}
			cur = obj.If
			i++
		case "then":
			if obj.Then == nil {
				return nil, fmt.Errorf("JSON Pointer %q: then not present", ptr)
			}
			cur = obj.Then
			i++
		case "else":
			if obj.Else == nil {
				return nil, fmt.Errorf("JSON Pointer %q: else not present", ptr)
			}
			cur = obj.Else
			i++
		case "not":
			if obj.Not == nil {
				return nil, fmt.Errorf("JSON Pointer %q: not not present", ptr)
			}
			cur = obj.Not
			i++
		case "items":
			if obj.Items == nil {
				return nil, fmt.Errorf("JSON Pointer %q: items not present", ptr)
			}
			cur = obj.Items
			i++
		case "additionalProperties":
			if obj.AdditionalProperties == nil {
				return nil, fmt.Errorf("JSON Pointer %q: additionalProperties not present", ptr)
			}
			cur = obj.AdditionalProperties
			i++
		case "propertyNames":
			if obj.PropertyNames == nil {
				return nil, fmt.Errorf("JSON Pointer %q: propertyNames not present", ptr)
			}
			cur = obj.PropertyNames
			i++
		default:
			return nil, fmt.Errorf("JSON Pointer %q: token %q not traversable", ptr, tok)
		}
	}
	return cur, nil
}

func unescapeJSONPointerToken(s string) string {
	// RFC 6901: ~1 -> /, ~0 -> ~ (in that order).
	s = strings.ReplaceAll(s, "~1", "/")
	s = strings.ReplaceAll(s, "~0", "~")
	if dec, err := url.PathUnescape(s); err == nil {
		s = dec
	}
	return s
}

// resolveRelative resolves ref against base. base is expected to be an
// absolute URI; ref may be relative or absolute.
func resolveRelative(base, ref string) (string, error) {
	bu, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse base %q: %w", base, err)
	}
	ru, err := url.Parse(ref)
	if err != nil {
		return "", fmt.Errorf("parse ref %q: %w", ref, err)
	}
	return bu.ResolveReference(ru).String(), nil
}

func stripFragment(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.Fragment = ""
	return u.String()
}

func absoluteResourceURI(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("resolve: parse %q: %w", rawURL, err)
	}
	if !u.IsAbs() {
		return "", fmt.Errorf("resolve: %q is not absolute", rawURL)
	}
	u.Fragment = ""
	return u.String(), nil
}

// keep the unused import 'strconv' eligible; jsonPointerStep doesn't yet
// index into arrays, but the fixture tests in this file may.
var _ = strconv.Atoi
