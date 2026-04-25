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

	"github.com/crhntr/jsonschema"
)

func main() {
	ctx := context.Background()
	wd, err := os.Getwd()
	if err != nil {
		log.Fatalln(err)
	}
	if code := run(ctx, wd, os.Args, os.Stdout, os.Stderr, http.DefaultClient); code != 0 {
		os.Exit(code)
	}
}

func run(ctx context.Context, wd string, args []string, stdout, stderr io.Writer, client *http.Client) int {
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
		if err := validate(ctx, wd, flagSet.Args()[1:], stdout, stderr, client); err != nil {
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

func schemaFileFlag(flagSet *flag.FlagSet, schemaPath *string) {
	flagSet.StringVar(schemaPath, "schema-file", "", "path to a local JSON Schema file")
}

func schemaURLFlag(flagSet *flag.FlagSet, schemaURL *string) {
	flagSet.StringVar(schemaURL, "schema-url", "", "absolute URL of a JSON Schema document (mutually exclusive with --schema-file)")
}

func validate(ctx context.Context, wd string, args []string, stdout, stderr io.Writer, client *http.Client) error {
	var (
		schemaFile string
		schemaURL  string
	)
	flagSet := flag.NewFlagSet("validate", flag.ContinueOnError)
	schemaFileFlag(flagSet, &schemaFile)
	schemaURLFlag(flagSet, &schemaURL)
	if err := flagSet.Parse(args); err != nil {
		return err
	}
	switch {
	case schemaFile != "" && schemaURL != "":
		return fmt.Errorf("--schema-file and --schema-url are mutually exclusive")
	case schemaFile == "" && schemaURL == "":
		return fmt.Errorf("--schema-file or --schema-url is required")
	}

	r := jsonschema.NewResolver(client)
	var m *jsonschema.Schema
	switch {
	case schemaFile != "":
		schemaJSON, err := os.ReadFile(filepath.Clean(filepath.Join(wd, schemaFile)))
		if err != nil {
			return err
		}
		absPath, err := filepath.Abs(filepath.Join(wd, schemaFile))
		if err != nil {
			return err
		}
		fileURI := "file://" + absPath
		if _, err := r.Load(fileURI, schemaJSON); err != nil {
			return fmt.Errorf("load schema: %w", err)
		}
		m, err = r.Resolve(ctx, fileURI)
		if err != nil {
			return fmt.Errorf("resolve schema: %w", err)
		}
	case schemaURL != "":
		var err error
		m, err = r.Resolve(ctx, schemaURL)
		if err != nil {
			return fmt.Errorf("resolve schema: %w", err)
		}
	}

	for _, arg := range flagSet.Args() {
		buf, err := os.ReadFile(filepath.Clean(filepath.Join(wd, arg)))
		if err != nil {
			return err
		}
		if err := m.Evaluate(arg, buf); err != nil {
			return err
		}
	}
	return nil
}

func generate(wd string, args []string, stdout, stderr io.Writer) error {
	var (
		schemaPath string
	)
	flagSet := flag.NewFlagSet("generate", flag.ContinueOnError)
	schemaFileFlag(flagSet, &schemaPath)
	if err := flagSet.Parse(args); err != nil {
		return err
	}
	return nil
}
