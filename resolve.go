package jsonschema

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"

	"github.com/crhntr/jsonschema/jsonptr"
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

func NewResolver(client *http.Client) *Resolver {
	return &Resolver{Client: client}
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
	if err := r.fetchMissingExternals(ctx); err != nil {
		return nil, err
	}
	if err := r.fetchMetaschemas(ctx); err != nil {
		return nil, err
	}
	if err := r.linkAll(); err != nil {
		return nil, err
	}
	r.applyVocabularies()

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
			if err := r.loadFSFile(fsys, name); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Resolver) loadFSFile(fsys fs.FS, name string) error {
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
	return nil
}

// loadResource fetches absURI (if not cached), parses it, and indexes every
// resource and anchor inside it. Recursively fetches any $ref / $dynamicRef
// targets it discovers. Resources are deduplicated by absolute URI.
func (r *Resolver) loadResource(ctx context.Context, absURI string) error {
	if r.reserveCacheSlot(absURI) {
		return nil
	}

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

// reserveCacheSlot returns true if absURI is already loaded; otherwise it
// reserves a nil placeholder so recursive callers don't refetch.
func (r *Resolver) reserveCacheSlot(absURI string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cache == nil {
		r.cache = map[string]*Schema{}
	}
	if existing, ok := r.cache[absURI]; ok && existing != nil {
		return true
	}
	r.cache[absURI] = nil
	return false
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

// indexDocument walks doc, sets baseURI / resource on every Schema, registers
// resource roots ($id) and anchors ($anchor / $dynamicAnchor), and returns
// the deduplicated set of external URIs the document references.
func (r *Resolver) indexDocument(doc *Schema, fetchURI string) ([]string, error) {
	r.initResource(doc, fetchURI)
	r.cacheResource(fetchURI, doc)

	idx := indexState{
		seen: map[string]struct{}{},
	}
	if err := r.indexSubtree(doc, fetchURI, doc, "", &idx); err != nil {
		return nil, err
	}
	return idx.external, nil
}

// initResource initializes the resource-root metadata on m. Pre-condition
// for indexing: every resource root has empty anchor maps and a known
// baseURI before its subtree is walked.
func (r *Resolver) initResource(m *Schema, base string) {
	m.baseURI = base
	m.resource = m
	m.anchors = map[string]*Schema{}
	m.dynamicAnchors = map[string]*Schema{}
}

func (r *Resolver) cacheResource(absURI string, m *Schema) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cache == nil {
		r.cache = map[string]*Schema{}
	}
	r.cache[absURI] = m
}

// indexState carries the per-document accumulators threaded through the
// indexing recursion.
type indexState struct {
	seen     map[string]struct{}
	external []string
}

func (r *Resolver) indexSubtree(m *Schema, base string, resource *Schema, path string, idx *indexState) error {
	if m == nil {
		return nil
	}
	obj, ok := m.TypeObject()
	if !ok {
		m.baseURI = base
		m.resource = resource
		m.pathInResource = path
		return nil
	}

	if obj.ID != "" {
		newBase, newResource, err := r.openEmbeddedResource(m, base, resource, obj.ID)
		if err != nil {
			return err
		}
		if newResource != resource {
			// Embedded $id opens a new resource; its internal path
			// resets so descendants are indexed relative to the
			// embedded resource root rather than the outer one.
			path = ""
		}
		base, resource = newBase, newResource
	}

	m.baseURI = base
	m.resource = resource
	m.pathInResource = path

	if obj.Anchor != "" {
		resource.anchors[obj.Anchor] = m
	}
	if obj.DynamicAnchor != "" {
		resource.dynamicAnchors[obj.DynamicAnchor] = m
	}

	for _, ref := range []string{obj.Ref, obj.DynamicRef} {
		if ref == "" {
			continue
		}
		if err := r.recordExternal(base, ref, idx); err != nil {
			return err
		}
	}

	return r.indexKeywords(obj, base, resource, path, idx)
}

// indexKeywords descends through every subschema-bearing keyword of obj,
// extending path with the keyword name (and key/index) so that each
// child Schema receives the JSON Pointer of its location within the
// current resource.
func (r *Resolver) indexKeywords(obj SchemaObject, base string, resource *Schema, path string, idx *indexState) error {
	tokenChild := func(token string) string {
		return jsonptr.NewBuilder(path).Token(token).String()
	}
	keyedChild := func(keyword, key string) string {
		return jsonptr.NewBuilder(path).Token(keyword).Token(key).String()
	}
	indexedChild := func(keyword string, i int) string {
		return jsonptr.NewBuilder(path).Token(keyword).Index(i).String()
	}

	for k, sub := range obj.Defs {
		if err := r.indexSubtree(sub, base, resource, keyedChild("$defs", k), idx); err != nil {
			return err
		}
	}
	for k, sub := range obj.Properties {
		if err := r.indexSubtree(sub, base, resource, keyedChild("properties", k), idx); err != nil {
			return err
		}
	}
	for k, sub := range obj.PatternProperties {
		if err := r.indexSubtree(sub, base, resource, keyedChild("patternProperties", k), idx); err != nil {
			return err
		}
	}
	for k, sub := range obj.DependentSchemas {
		if err := r.indexSubtree(sub, base, resource, keyedChild("dependentSchemas", k), idx); err != nil {
			return err
		}
	}
	for k, dep := range obj.Dependencies {
		if dep == nil {
			continue
		}
		if sub := dep.Schema(); sub != nil {
			if err := r.indexSubtree(sub, base, resource, keyedChild("dependencies", k), idx); err != nil {
				return err
			}
		}
	}
	for i, sub := range obj.AllOf {
		if err := r.indexSubtree(sub, base, resource, indexedChild("allOf", i), idx); err != nil {
			return err
		}
	}
	for i, sub := range obj.AnyOf {
		if err := r.indexSubtree(sub, base, resource, indexedChild("anyOf", i), idx); err != nil {
			return err
		}
	}
	for i, sub := range obj.OneOf {
		if err := r.indexSubtree(sub, base, resource, indexedChild("oneOf", i), idx); err != nil {
			return err
		}
	}
	for i, sub := range obj.PrefixItems {
		if err := r.indexSubtree(sub, base, resource, indexedChild("prefixItems", i), idx); err != nil {
			return err
		}
	}
	singletonKeywords := []struct {
		keyword string
		sub     *Schema
	}{
		{"if", obj.If},
		{"then", obj.Then},
		{"else", obj.Else},
		{"not", obj.Not},
		{"items", obj.Items},
		{"contains", obj.Contains},
		{"additionalProperties", obj.AdditionalProperties},
		{"unevaluatedProperties", obj.UnevaluatedProperties},
		{"unevaluatedItems", obj.UnevaluatedItems},
		{"propertyNames", obj.PropertyNames},
		{"contentSchema", obj.ContentSchema},
	}
	for _, sk := range singletonKeywords {
		if sk.sub == nil {
			continue
		}
		if err := r.indexSubtree(sk.sub, base, resource, tokenChild(sk.keyword), idx); err != nil {
			return err
		}
	}
	return nil
}

// openEmbeddedResource handles a subschema with $id. If the resolved $id
// differs from the enclosing base, the subschema becomes a new resource
// root cached under that URI.
func (r *Resolver) openEmbeddedResource(m *Schema, base string, resource *Schema, id string) (string, *Schema, error) {
	newBase, err := resolveRelative(base, id)
	if err != nil {
		return "", nil, fmt.Errorf("$id %q: %w", id, err)
	}
	newBase = stripFragment(newBase)
	if m == resource && base == newBase {
		return newBase, resource, nil
	}
	m.anchors = map[string]*Schema{}
	m.dynamicAnchors = map[string]*Schema{}
	if m.source == nil && resource.source != nil {
		m.source = resource.source
	}
	r.cacheResource(newBase, m)
	return newBase, m, nil
}

func (r *Resolver) recordExternal(base, ref string, idx *indexState) error {
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
	if _, dup := idx.seen[target]; dup {
		return nil
	}
	idx.seen[target] = struct{}{}
	idx.external = append(idx.external, target)
	return nil
}

// fetchMissingExternals walks every cached resource looking for $ref /
// $dynamicRef targets whose resource isn't loaded yet, fetching each.
// Repeats until the cache stabilizes — newly fetched resources may
// introduce further refs.
func (r *Resolver) fetchMissingExternals(ctx context.Context) error {
	for {
		missing := r.findMissingExternals()
		if len(missing) == 0 {
			return nil
		}
		for _, uri := range missing {
			if err := r.loadResource(ctx, uri); err != nil {
				return err
			}
		}
	}
}

func (r *Resolver) findMissingExternals() []string {
	r.mu.Lock()
	resources := make([]*Schema, 0, len(r.cache))
	cached := make(map[string]bool, len(r.cache))
	for k, m := range r.cache {
		cached[k] = m != nil
		if m != nil {
			resources = append(resources, m)
		}
	}
	r.mu.Unlock()

	seen := map[string]struct{}{}
	var missing []string
	var walk func(*Schema)
	walk = func(c *Schema) {
		if c == nil {
			return
		}
		obj, ok := c.TypeObject()
		if !ok {
			return
		}
		for _, ref := range []string{obj.Ref, obj.DynamicRef} {
			if ref == "" {
				continue
			}
			target, err := resolveRelative(c.baseURI, ref)
			if err != nil {
				continue
			}
			target = stripFragment(target)
			if target == "" || cached[target] {
				continue
			}
			if _, dup := seen[target]; dup {
				continue
			}
			seen[target] = struct{}{}
			missing = append(missing, target)
		}
		for child := range c.Subschemas() {
			walk(child)
		}
	}
	for _, m := range resources {
		walk(m)
	}
	return missing
}

// fetchMetaschemas loads the metaschema declared by each cached
// resource's $schema field so that vocabulary information is available
// during applyVocabularies.
func (r *Resolver) fetchMetaschemas(ctx context.Context) error {
	r.mu.Lock()
	urls := map[string]struct{}{}
	for _, m := range r.cache {
		if m == nil {
			continue
		}
		obj, ok := m.TypeObject()
		if !ok || obj.Schema == "" {
			continue
		}
		uri := stripFragment(obj.Schema)
		if uri == "" {
			continue
		}
		if _, cached := r.cache[uri]; cached {
			continue
		}
		urls[uri] = struct{}{}
	}
	r.mu.Unlock()
	for uri := range urls {
		if err := r.loadResource(ctx, uri); err != nil {
			// Metaschema fetch is best-effort: skip silently.
			continue
		}
	}
	return nil
}

// applyVocabularies walks every cached resource, resolves the
// metaschema declared by $schema, and copies the metaschema's
// $vocabulary declaration onto the resource. The evaluator reads
// resource.vocabularies to gate per-vocabulary keyword evaluation
// (validation, applicator, format-assertion, content, etc.).
func (r *Resolver) applyVocabularies() {
	r.mu.Lock()
	resources := make([]*Schema, 0, len(r.cache))
	seen := map[*Schema]struct{}{}
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

	for _, m := range resources {
		obj, ok := m.TypeObject()
		if !ok || obj.Schema == "" {
			continue
		}
		uri := stripFragment(obj.Schema)
		r.mu.Lock()
		meta := r.cache[uri]
		r.mu.Unlock()
		if meta == nil {
			continue
		}
		mObj, ok := meta.TypeObject()
		if !ok {
			continue
		}
		if mObj.Vocabulary == nil {
			continue
		}
		// Copy the metaschema's $vocabulary declaration onto every
		// schema using it (the resource root and its non-resource
		// descendants — nested resources inherit their own metaschema
		// independently).
		r.markVocabularies(m, mObj.Vocabulary)
	}
}

// markVocabularies sets m.vocabularies to vocabs and recurses into
// non-resource descendants. A descendant that declares its own
// $schema becomes its own resource and is not modified here; it gets
// its own vocabularies from its own iteration of applyVocabularies.
func (r *Resolver) markVocabularies(m *Schema, vocabs map[string]bool) {
	if m == nil {
		return
	}
	m.vocabularies = vocabs
	obj, ok := m.TypeObject()
	if !ok {
		return
	}
	if obj.Schema != "" && m != m.resource {
		return
	}
	for c := range m.Subschemas() {
		r.markVocabularies(c, vocabs)
	}
}

// linkAll runs Phase B over every cached resource: every $ref / $dynamicRef
// gets its resolved pointer set; bookended $dynamicRefs get marked dynamic.
func (r *Resolver) linkAll() error {
	r.mu.Lock()
	resources := make([]*Schema, 0, len(r.cache))
	seen := map[*Schema]struct{}{}
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

func (r *Resolver) linkSubtree(m *Schema) error {
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

	for c := range m.Subschemas() {
		if err := r.linkSubtree(c); err != nil {
			return err
		}
	}
	return nil
}

// resolveReference resolves ref against base, returning the target *Schema.
// When dynamic is true, also reports whether the reference is bookended by a
// matching $dynamicAnchor in the initial target's resource (per §8.2.3.2).
// Bookending tells a future validator to walk the runtime dynamic scope
// (outermost to innermost) for the first matching $dynamicAnchor instead
// of using the lexical fallback returned here.
func (r *Resolver) resolveReference(base, ref string, dynamic bool) (*Schema, bool, error) {
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
		if _, ok := target.resource.dynamicAnchors[frag]; ok {
			return target, true, nil
		}
	}
	return target, false, nil
}

// resolveFragment resolves frag within resource. Returns target, whether
// the fragment was a plain name (vs JSON Pointer or empty), and any error.
func resolveFragment(resource *Schema, frag string) (*Schema, bool, error) {
	if frag == "" {
		return resource, false, nil
	}
	if strings.HasPrefix(frag, "/") {
		target, err := walkJSONPointer(resource, frag)
		if err != nil {
			return nil, false, err
		}
		return target, false, nil
	}
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

// FindJSONPtrValue implements jsonptr.Walker. It delegates to
// jsonptr.FindValue on the underlying SchemaObject — the reflection walker
// already knows how to descend through SchemaObject's json-tagged fields
// (and into nested *Schema children, which themselves implement Walker).
// Identity is preserved end-to-end without a custom traversal table.
//
// Boolean schemas have nothing to descend into; the root pointer
// resolves to the bool value, anything deeper is an error.
func (m *Schema) FindJSONPtrValue(ptr jsonptr.Pointer, opts ...json.Options) (jsonptr.Pointer, any, error) {
	if obj, ok := m.TypeObject(); ok {
		_, live, err := jsonptr.FindValue(ptr, obj, opts...)
		if err != nil {
			return ptr, nil, err
		}
		return "", live, nil
	}
	if ptr == "" {
		b, _ := m.TypeBool()
		return "", b, nil
	}
	return ptr, nil, fmt.Errorf("cannot descend into boolean schema at %q", ptr)
}

// walkJSONPointer follows an RFC 6901 JSON Pointer through a Schema tree.
// Used by the resolver during link phase. Most fragments terminate at
// a *Schema, but JSON Schema allows refs into unknown keywords (whose
// values are captured in SchemaObject.Extra as raw bytes); those are
// lazily parsed into a fresh Schema.
func walkJSONPointer(m *Schema, ptr string) (*Schema, error) {
	p := jsonptr.Pointer(ptr)
	if err := p.Validate(); err != nil {
		return nil, err
	}
	_, live, err := jsonptr.FindValue(p, m)
	if err != nil {
		return nil, fmt.Errorf("JSON Pointer %q: %w", ptr, err)
	}
	if target, ok := live.(*Schema); ok {
		return target, nil
	}
	if raw, ok := live.(jsontext.Value); ok {
		return Parse(raw)
	}
	return nil, fmt.Errorf("JSON Pointer %q: target is %T, not a schema", ptr, live)
}

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
