// Command issues is a small issue tracker built on jsonhandler: its forms
// come from the JSON Schemas in schemas, its Go types from jsch generate,
// and its queries from sqlc.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	_ "modernc.org/sqlite"

	"github.com/crhntr/jsonschema/examples/issues/internal/database"
	"github.com/crhntr/jsonschema/examples/issues/internal/hypertext"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	err := run(ctx, os.Args[1:], log)
	stop()
	if err != nil {
		log.Error("issues", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, log *slog.Logger) error {
	flags := flag.NewFlagSet("issues", flag.ContinueOnError)
	addr := flags.String("addr", "localhost:8080", "address to listen on")
	file := flags.String("db", "issues.db", "SQLite database file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", *file)
	if err != nil {
		return err
	}
	defer closeAndLog(db, log)
	if err := database.Migrate(ctx, db); err != nil {
		return err
	}
	srv, err := hypertext.New(ctx, db, log)
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
	log.Info("serving", "url", fmt.Sprintf("http://%s/", ln.Addr()))
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

func closeAndLog(db *sql.DB, log *slog.Logger) {
	if err := db.Close(); err != nil {
		log.Error("close database", "error", err)
	}
}
