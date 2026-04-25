package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
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
	if code := run(ctx, wd, os.Args, os.Stdout, os.Stderr); code != 0 {
		os.Exit(code)
	}
}

func run(ctx context.Context, wd string, args []string, stdout, stderr io.Writer) int {
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
		if err := validate(wd, flagSet.Args()[1:], stdout, stderr); err != nil {
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
	flagSet.StringVar(schemaPath, "schema-file", "", "path to JSON Schema file")
}

func validate(wd string, args []string, stdout, stderr io.Writer) error {
	var (
		schemaPath string
	)
	flagSet := flag.NewFlagSet("validate", flag.ContinueOnError)
	schemaFileFlag(flagSet, &schemaPath)
	if err := flagSet.Parse(args); err != nil {
		return err
	}
	if schemaPath == "" {
		return fmt.Errorf("schema-file flag is required")
	}
	schemaJSON, err := os.ReadFile(filepath.Clean(filepath.Join(wd, schemaPath)))
	if err != nil {
		return err
	}
	// Run the schema through a Resolver so internal $refs are linked.
	// The schema may not declare $id, so seed under a synthetic URI.
	r := &jsonschema.Resolver{}
	if _, err := r.Load("file:///cli/schema", schemaJSON); err != nil {
		return fmt.Errorf("load schema: %w", err)
	}
	m, err := r.Resolve(context.Background(), "file:///cli/schema")
	if err != nil {
		return fmt.Errorf("resolve schema: %w", err)
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
