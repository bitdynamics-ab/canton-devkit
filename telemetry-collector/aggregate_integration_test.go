package collector

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestUpsertCounters_SumsAcrossMachines exercises the real Postgres store
// to prove fleet aggregation: two submissions for the same
// (period, chart, bucket) ADD rather than replace. Skipped unless
// TEST_DATABASE_URL points at a Postgres with schema.sql applied, so the
// default `go test ./...` stays DB-free.
//
//	TEST_DATABASE_URL=postgres://postgres:pw@localhost:5432/telemetry?sslmode=disable \
//	  go test ./... -run TestUpsertCounters_SumsAcrossMachines
func TestUpsertCounters_SumsAcrossMachines(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres integration test")
	}
	ctx := context.Background()
	store, err := NewPgStore(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer store.Close()

	// Throwaway period so we never touch real data; clean up after.
	const period = "1999-12-31"
	day := time.Date(1999, 12, 31, 0, 0, 0, 0, time.UTC)
	cleanup := func() {
		_, _ = store.pool.Exec(ctx, "DELETE FROM counter_period WHERE period=$1", period)
	}
	cleanup()
	defer cleanup()

	// Machine A: up=5, down=2.
	if err := store.UpsertCounters(ctx, period, "daily", &day,
		map[string]map[string]int{"dpm/command": {"up": 5, "down": 2}}); err != nil {
		t.Fatalf("machine A: %v", err)
	}
	// Machine B (same period): up=3.
	if err := store.UpsertCounters(ctx, period, "daily", &day,
		map[string]map[string]int{"dpm/command": {"up": 3}}); err != nil {
		t.Fatalf("machine B: %v", err)
	}

	got := map[string]int{}
	rows, err := store.pool.Query(ctx,
		"SELECT bucket, count FROM counter_period WHERE period=$1 AND chart='dpm/command'", period)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var b string
		var c int
		if err := rows.Scan(&b, &c); err != nil {
			t.Fatal(err)
		}
		got[b] = c
	}
	if got["up"] != 8 { // 5 + 3, summed across the two machines
		t.Errorf("up = %d, want 8 (must SUM across machines, not replace)", got["up"])
	}
	if got["down"] != 2 {
		t.Errorf("down = %d, want 2", got["down"])
	}
}
