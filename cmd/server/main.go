// Command server runs the Football Team Balancer: the REST API and the static
// frontend from one process.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"football-balancer/internal/api"
	"football-balancer/internal/storage"
	"football-balancer/web"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address (host:port)")
	data := flag.String("data", "data/players.json", "path to the JSON roster file")
	history := flag.String("matches", "data/matches.json", "path to the JSON match history file")
	flag.Parse()

	if err := run(*addr, *data, *history); err != nil {
		log.Fatalf("football-balancer: %v", err)
	}
}

func run(addr, dataPath, historyPath string) error {
	store, err := storage.NewJSONStore(dataPath)
	if err != nil {
		return err
	}
	players, err := store.List()
	if err != nil {
		return err
	}
	log.Printf("roster: %d players from %s", len(players), dataPath)

	matchStore, err := storage.NewMatchJSONStore(historyPath)
	if err != nil {
		return err
	}
	saved, err := matchStore.List()
	if err != nil {
		return err
	}
	log.Printf("history: %d matches from %s", len(saved), historyPath)

	assets, err := fs.Sub(web.Assets, ".")
	if err != nil {
		return fmt.Errorf("frontend assets: %w", err)
	}

	server := &http.Server{
		Addr:              addr,
		Handler:           api.New(store, matchStore, http.FS(assets)),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() {
		errCh <- listenAndServe(server, addr)
	}()

	host := displayHost(addr)
	fmt.Printf("Football Team Balancer ready → http://%s/\n", host)

	select {
	case err := <-errCh:
		return err
	case sig := <-shutdown:
		log.Printf("received %s, shutting down", sig)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return <-errCh
}

// listenAndServe keeps http.ErrServerClosed out of the fatal path.
func listenAndServe(s *http.Server, addr string) error {
	if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	return nil
}

// displayHost turns ":8080" into "localhost:8080" for the startup banner.
func displayHost(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return "localhost:8080"
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "*" {
		host = "localhost"
	}
	return net.JoinHostPort(host, port)
}
