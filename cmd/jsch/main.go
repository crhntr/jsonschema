package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-json-experiment/json"

	"github.com/crhntr/jsonschema"
	"github.com/crhntr/jsonschema/internal/metaschema"
)

// jsonMarshal is a tiny indirection so the test for emitOutput can
// share marshal style with the rest of the CLI.
func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

func main() {
	ctx := context.Background()
	wd, err := os.Getwd()
	if err != nil {
		log.Fatalln(err)
	}
	if code := run(ctx, wd, os.Args[1:], os.Stdout, os.Stderr, os.Stdin, http.DefaultClient); code != 0 {
		os.Exit(code)
	}
}

func run(ctx context.Context, wd string, args []string, stdout, stderr io.Writer, stdin io.Reader, client *http.Client) int {
	const (
		exitOK = iota
		exitError
	)
	flagSet := flag.NewFlagSet("jsch", flag.ContinueOnError)
	if err := flagSet.Parse(args); err != nil {
		_, _ = io.WriteString(stderr, err.Error())
		return exitError
	}
	switch flagSet.Arg(0) {
	case "validate":
		if err := validate(ctx, wd, flagSet.Args()[1:], stdout, stderr, stdin, client); err != nil {
			_, _ = io.WriteString(stderr, err.Error()+"\n")
			return exitError
		}
		return exitOK
	case "generate":
		if err := generate(wd, flagSet.Args()[1:], stdout, stderr); err != nil {
			_, _ = io.WriteString(stderr, err.Error()+"\n")
			return exitError
		}
		return exitOK
	default:
		return exitOK
	}
}

func validate(ctx context.Context, wd string, args []string, stdout, stderr io.Writer, stdin io.Reader, client *http.Client) error {
	var (
		schema               string
		schema202012         bool
		skipSchemaValidation bool
		formatAssert         bool
		strict               bool
		quiet                bool
		outputFmt            string
	)
	flagSet := flag.NewFlagSet("validate", flag.ContinueOnError)
	flagSet.StringVar(&schema, "schema", "", "path or URL of a JSON Schema document (required)")
	flagSet.BoolVar(&schema202012, "schema-2020-12", false, "shorthand for --schema=https://json-schema.org/draft/2020-12/schema (served from the embedded copy)")
	flagSet.BoolVar(&skipSchemaValidation, "skip-schema-validation", false, "skip the pre-flight check that validates --schema against the JSON Schema 2020-12 meta-schema")
	flagSet.BoolVar(&formatAssert, "format-assert", false, "treat the format keyword as an assertion (per the format-assertion vocabulary)")
	flagSet.BoolVar(&strict, "strict", false, "fail on unknown schema keywords or unresolvable external $refs")
	flagSet.BoolVar(&quiet, "quiet", false, "do not print success messages; failures still go to stderr")
	flagSet.StringVar(&outputFmt, "output", "", "emit a JSON Schema 2020-12 output document per instance: flag, basic, detailed, or verbose")
	if err := flagSet.Parse(args); err != nil {
		return err
	}
	if schema202012 {
		if schema != "" {
			return fmt.Errorf("--schema and --schema-2020-12 are mutually exclusive")
		}
		schema = metaschema.SchemaURI
	}
	if schema == "" {
		return fmt.Errorf("--schema is required")
	}
	if err := validateOutputFormat(outputFmt); err != nil {
		return err
	}

	m, err := loadSchema(ctx, wd, schema, strict, skipSchemaValidation, client)
	if err != nil {
		return err
	}

	instances := flagSet.Args()
	if len(instances) == 0 {
		return fmt.Errorf("at least one instance file (or '-' for stdin) is required")
	}
	failed := 0
	for _, arg := range instances {
		buf, name, err := readInstance(wd, arg, stdin)
		if err != nil {
			return err
		}
		var out jsonschema.Output
		if formatAssert {
			out = m.ValidateWithFormatAssertion(name, buf)
		} else {
			out = m.Validate(name, buf)
		}
		if outputFmt != "" {
			if err := emitOutput(stdout, outputFmt, out); err != nil {
				return err
			}
			if !out.Valid {
				failed++
			}
			continue
		}
		if !out.Valid {
			failed++
			if quiet {
				fmt.Fprintln(stderr, name+": invalid")
			} else {
				fmt.Fprintln(stderr, out.AsError())
			}
			continue
		}
		if !quiet {
			fmt.Fprintln(stdout, name+": ok")
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d instance(s) failed validation", failed, len(instances))
	}
	return nil
}

// validateOutputFormat returns an error when name is non-empty but
// not one of the four spec output formats.
func validateOutputFormat(name string) error {
	switch name {
	case "", "flag", "basic", "detailed", "verbose":
		return nil
	}
	return fmt.Errorf("--output: %q not in {flag, basic, detailed, verbose}", name)
}

// emitOutput writes a single Output to w as JSON in the requested
// spec format, terminated with a newline. Unknown formats are
// rejected by validateOutputFormat earlier.
func emitOutput(w io.Writer, format string, out jsonschema.Output) error {
	var picked jsonschema.Output
	switch format {
	case "flag":
		picked = out.Flag()
	case "basic":
		picked = out.Basic()
	case "detailed":
		picked = out.Detailed()
	case "verbose":
		picked = out.Verbose()
	}
	body, err := jsonMarshal(picked)
	if err != nil {
		return fmt.Errorf("marshal output: %w", err)
	}
	_, err = fmt.Fprintln(w, string(body))
	return err
}

// loadSchema parses arg as either an absolute URL or a local file path
// and runs it through a Resolver. Strict mode rejects schemas that
// declared unknown top-level keywords.
func loadSchema(ctx context.Context, wd, arg string, strict, skipMeta bool, client *http.Client) (*jsonschema.Schema, error) {
	r := jsonschema.NewResolver(client)
	if err := metaschema.Seed(r); err != nil {
		return nil, fmt.Errorf("seed metaschema: %w", err)
	}
	uri, body, err := schemaSource(ctx, wd, arg, client)
	if err != nil {
		return nil, err
	}
	if body != nil {
		if _, err := r.Load(uri, body); err != nil {
			return nil, fmt.Errorf("load schema: %w", err)
		}
	}
	if !skipMeta && uri != metaschema.SchemaURI {
		if err := validateAgainstMetaSchema(ctx, r, uri, body); err != nil {
			return nil, err
		}
	}
	m, err := r.Resolve(ctx, uri)
	if err != nil {
		return nil, fmt.Errorf("resolve schema: %w", err)
	}
	if strict {
		if obj, ok := m.TypeObject(); ok && len(obj.Extra) > 0 {
			keys := make([]string, 0, len(obj.Extra))
			for k := range obj.Extra {
				keys = append(keys, k)
			}
			return nil, fmt.Errorf("strict: schema has unknown keywords: %s", strings.Join(keys, ", "))
		}
	}
	return m, nil
}

// validateAgainstMetaSchema runs body through the embedded JSON
// Schema 2020-12 meta-schema and returns a descriptive error when
// the input doesn't pass. body is taken pre-resolution so the
// validation reflects the document as written, not the
// resolver-augmented value.
func validateAgainstMetaSchema(ctx context.Context, r *jsonschema.Resolver, uri string, body []byte) error {
	if body == nil {
		// Body is nil for URL-loaded schemas that the resolver
		// will fetch itself; we'd need to hold onto those bytes
		// up front. For now skip the meta-validation in that
		// path rather than re-fetching.
		return nil
	}
	meta, err := r.Resolve(ctx, metaschema.SchemaURI)
	if err != nil {
		return fmt.Errorf("resolve meta-schema: %w", err)
	}
	out := meta.Validate(uri, body)
	if !out.Valid {
		return fmt.Errorf("schema fails JSON Schema 2020-12 meta-schema validation:\n%s", out.AsError())
	}
	return nil
}

// schemaSource returns the absolute URI for r.Resolve plus the
// schema's raw bytes. URLs are fetched once via client so callers
// (e.g. the meta-schema pre-flight check) can inspect the document
// before handing it to the Resolver. URLs that match an
// embedded-meta-schema $id resolve from the embedded copy: callers
// that have already pre-seeded the resolver with metaschema.Seed
// can simply skip the fetch when body is non-nil.
func schemaSource(ctx context.Context, wd, arg string, client *http.Client) (uri string, body []byte, err error) {
	switch {
	case strings.HasPrefix(arg, "http://"), strings.HasPrefix(arg, "https://"):
		// Try the embedded copy first so air-gapped invocations
		// of jsch on the canonical meta-schema URLs always
		// succeed.
		if buf, ok := metaschema.BytesForURL(arg); ok {
			return arg, buf, nil
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, arg, nil)
		if err != nil {
			return "", nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return "", nil, fmt.Errorf("fetch %s: %w", arg, err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return "", nil, fmt.Errorf("fetch %s: %s", arg, resp.Status)
		}
		buf, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", nil, fmt.Errorf("read %s: %w", arg, err)
		}
		return arg, buf, nil
	case strings.HasPrefix(arg, "file://"):
		path := strings.TrimPrefix(arg, "file://")
		buf, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			return "", nil, err
		}
		return arg, buf, nil
	default:
		path := filepath.Clean(filepath.Join(wd, arg))
		buf, err := os.ReadFile(path)
		if err != nil {
			return "", nil, err
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return "", nil, err
		}
		return "file://" + abs, buf, nil
	}
}

// embeddedBytes returns the bytes of an embedded meta-schema
// document keyed by its canonical URL, or false if the URL is not
// one we ship.

// readInstance returns the bytes and a display name for an instance
// argument. "-" means stdin.
func readInstance(wd, arg string, stdin io.Reader) ([]byte, string, error) {
	if arg == "-" {
		buf, err := io.ReadAll(stdin)
		if err != nil {
			return nil, "", fmt.Errorf("read stdin: %w", err)
		}
		return buf, "<stdin>", nil
	}
	buf, err := os.ReadFile(filepath.Clean(filepath.Join(wd, arg)))
	if err != nil {
		return nil, "", err
	}
	return buf, arg, nil
}

func generate(wd string, args []string, stdout, stderr io.Writer) error {
	var schemaPath string
	flagSet := flag.NewFlagSet("generate", flag.ContinueOnError)
	flagSet.StringVar(&schemaPath, "schema", "", "path or URL of a JSON Schema document")
	if err := flagSet.Parse(args); err != nil {
		return err
	}
	return nil
}
