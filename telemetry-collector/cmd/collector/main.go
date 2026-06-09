// Command collector runs the canton-devkit telemetry ingestion endpoint.
//
// Env:
//
//	DATABASE_URL      Postgres DSN (required), e.g.
//	                  postgres://user:pass@host:5432/telemetry?sslmode=disable
//	LISTEN_ADDR       listen address (default ":8080")
//	INGEST_TOKEN      optional shared secret; when set, requests must send
//	                  it in the X-Telemetry-Token header
//	RATE_PER_IP_PER_MIN, RATE_BURST, RATE_GLOBAL_PER_SEC
//	                  in-process rate-limit knobs (defaults 30 / 15 / 50).
//	                  Defense-in-depth; run behind a Cloudflare Tunnel for
//	                  edge DDoS absorption — see DEPLOY.md.
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
	// Wrap with the in-process rate limiter (defense-in-depth; the edge
	// does the heavy DDoS absorption — see DEPLOY.md).
	handler := collector.RateLimit(h, collector.RateLimitConfigFromEnv())
	mux := http.NewServeMux()
	mux.Handle("/", handler) // accepts POST on any path; /healthz is special-cased

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
