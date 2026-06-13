// Command github-stats snapshots GitHub release-download counts and repo
// visibility (stars/forks/watchers) into Postgres, for the adoption
// dashboard. Run it on a daily schedule (cron / GitHub Action).
//
// Env:
//
//	DATABASE_URL   Postgres DSN (required)
//	GITHUB_REPO    "owner/name" (required), e.g. bitdynamics-ab/canton-devkit
//	GITHUB_TOKEN   optional; required for private repos / higher rate limits
//	CAPTURED_ON    optional YYYY-MM-DD override (default: today UTC) — for
//	               deterministic backfill/testing
package main

import (
	"context"
	"log"
	"os"
	"time"

	collector "github.com/bitdynamics-ab/canton-devkit/telemetry-collector"
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	repo := os.Getenv("GITHUB_REPO")
	if dsn == "" || repo == "" {
		log.Fatal("DATABASE_URL and GITHUB_REPO are required")
	}
	capturedOn := time.Now().UTC().Truncate(24 * time.Hour)
	if v := os.Getenv("CAPTURED_ON"); v != "" {
		t, err := time.Parse("2006-01-02", v)
		if err != nil {
			log.Fatalf("CAPTURED_ON must be YYYY-MM-DD: %v", err)
		}
		capturedOn = t
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	gh := collector.NewGitHubClient(repo, os.Getenv("GITHUB_TOKEN"))
	store, err := collector.NewGitHubStore(ctx, dsn)
	if err != nil {
		log.Fatalf("connect postgres: %v", err)
	}
	defer store.Close()

	dls, err := gh.Downloads(ctx)
	if err != nil {
		log.Fatalf("fetch downloads: %v", err)
	}
	if err := store.SaveDownloads(ctx, capturedOn, dls); err != nil {
		log.Fatalf("save downloads: %v", err)
	}
	total := 0
	for _, d := range dls {
		total += d.DownloadCount
	}

	st, err := gh.Stats(ctx)
	if err != nil {
		log.Fatalf("fetch repo stats: %v", err)
	}
	if err := store.SaveStats(ctx, capturedOn, st); err != nil {
		log.Fatalf("save stats: %v", err)
	}

	log.Printf("github-stats %s: %d assets, %d cumulative downloads; stars=%d forks=%d watchers=%d",
		capturedOn.Format("2006-01-02"), len(dls), total, st.Stars, st.Forks, st.Watchers)
}
