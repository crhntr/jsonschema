// Command surveys writes and runs surveys. An author writes a survey in a
// form built from a JSON Schema for survey definitions; the survey becomes
// a JSON Schema of its answers, and respondents fill in a form built from
// that, one question at a time if they like. Results are tallied from the
// answers.
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

	"github.com/crhntr/jsonschema/examples/surveys/internal/hypertext"
	"github.com/crhntr/jsonschema/examples/surveys/internal/store"
)

// maxBody is the most a request may carry; surveys take input from anyone.
const maxBody = 1 << 20

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	err := run(ctx, os.Args[1:], log)
	stop()
	if err != nil {
		log.Error("surveys", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, log *slog.Logger) error {
	flags := flag.NewFlagSet("surveys", flag.ContinueOnError)
	addr := flags.String("addr", "localhost:8080", "address to listen on")
	dir := flags.String("data", "data", "directory for surveys and responses")
	if err := flags.Parse(args); err != nil {
		return err
	}
	s, err := store.Open(*dir)
	if err != nil {
		return err
	}
	srv, err := hypertext.New(ctx, s, log)
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
		Handler:           http.MaxBytesHandler(http.NewCrossOriginProtection().Handler(mux), maxBody),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Info("serving", "url", fmt.Sprintf("http://%s/", ln.Addr()), "data", *dir)
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
