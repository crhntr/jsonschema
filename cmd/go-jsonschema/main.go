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

	"github.com/crhntr/jsonschema"
)

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
	flagSet := flag.NewFlagSet("go-jsonschema", flag.ContinueOnError)
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
		schema       string
		formatAssert bool
		strict       bool
		quiet        bool
	)
	flagSet := flag.NewFlagSet("validate", flag.ContinueOnError)
	flagSet.StringVar(&schema, "schema", "", "path or URL of a JSON Schema document (required)")
	flagSet.BoolVar(&formatAssert, "format-assert", false, "treat the format keyword as an assertion (per the format-assertion vocabulary)")
	flagSet.BoolVar(&strict, "strict", false, "fail on unknown schema keywords or unresolvable external $refs")
	flagSet.BoolVar(&quiet, "quiet", false, "do not print success messages; failures still go to stderr")
	if err := flagSet.Parse(args); err != nil {
		return err
	}
	if schema == "" {
		return fmt.Errorf("--schema is required")
	}

	m, err := loadSchema(ctx, wd, schema, strict, client)
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

// loadSchema parses arg as either an absolute URL or a local file path
// and runs it through a Resolver. Strict mode rejects schemas that
// declared unknown top-level keywords.
func loadSchema(ctx context.Context, wd, arg string, strict bool, client *http.Client) (*jsonschema.Schema, error) {
	r := jsonschema.NewResolver(client)
	uri, body, err := schemaSource(wd, arg)
	if err != nil {
		return nil, err
	}
	if body != nil {
		if _, err := r.Load(uri, body); err != nil {
			return nil, fmt.Errorf("load schema: %w", err)
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

// schemaSource returns the absolute URI for r.Resolve plus the schema
// body if it's a local file (so the resolver doesn't try to fetch it).
// Inputs starting with http://, https://, or file:// are URLs; anything
// else is a local path resolved against wd.
func schemaSource(wd, arg string) (uri string, body []byte, err error) {
	switch {
	case strings.HasPrefix(arg, "http://"), strings.HasPrefix(arg, "https://"):
		return arg, nil, nil
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
