// Command concourse edits a Concourse pipeline file in the browser. The
// YAML is converted to JSON and checked against a JSON Schema for
// pipelines; jobs and resources are edited through forms rendered from that
// schema, and written back into the YAML in place.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/crhntr/jsonschema/examples/concourse/internal/hypertext"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	err := run(ctx, os.Args[1:], log)
	stop()
	if err != nil {
		log.Error("concourse", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, log *slog.Logger) error {
	flags := flag.NewFlagSet("concourse", flag.ContinueOnError)
	addr := flags.String("addr", "localhost:8080", "address to listen on")
	write := flags.Bool("write", false, "save changes to the pipeline file")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "usage: concourse [-addr host:port] [-write] pipeline.yml")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		flags.Usage()
		return errors.New("name one pipeline file")
	}
	file := flags.Arg(0)
	src, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	srv, err := hypertext.New(ctx, src, file, *write, log)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	hypertext.Routes(mux, srv)

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	server := &http.Server{
		Handler:           http.NewCrossOriginProtection().Handler(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Info("serving", "url", fmt.Sprintf("http://%s/", ln.Addr()), "pipeline", file, "write", *write)
	served := make(chan error, 1)
	go func() { served <- server.Serve(ln) }()
	select {
	case err := <-served:
		return err
	case <-ctx.Done():
		if err := server.Shutdown(context.Background()); err != nil {
			return err
		}
		if err := <-served; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}
