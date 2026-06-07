// Command collector runs the canton-devkit telemetry ingestion endpoint.
//
// Env:
//
//	DATABASE_URL      Postgres DSN (required), e.g.
//	                  postgres://user:pass@host:5432/telemetry?sslmode=disable
//	LISTEN_ADDR       listen address (default ":8080")
//	INGEST_TOKEN      optional shared secret; when set, requests must send
//	                  it in the X-Telemetry-Token header
//
// Point the CLI at it with:
//
//	CANTON_DEVKIT_TELEMETRY_ENDPOINT=http://<host>:8080/v1/counters
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	collector "github.com/bitdynamics-ab/canton-devkit/telemetry-collector"
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is required")
	}
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := collector.NewPgStore(ctx, dsn)
	if err != nil {
		log.Fatalf("connect postgres: %v", err)
	}
	defer store.Close()

	h := collector.New(store, os.Getenv("INGEST_TOKEN"))
	mux := http.NewServeMux()
	mux.Handle("/", h) // accepts POST on any path; /healthz is special-cased

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}
	log.Printf("telemetry collector listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
