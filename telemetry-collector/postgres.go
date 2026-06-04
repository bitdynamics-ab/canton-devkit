package collector

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgStore is a Postgres-backed Store using a pgx connection pool.
type PgStore struct{ pool *pgxpool.Pool }

// NewPgStore opens a pooled connection to the given DSN and verifies it.
func NewPgStore(ctx context.Context, dsn string) (*PgStore, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &PgStore{pool: pool}, nil
}

// Close releases the pool.
func (s *PgStore) Close() { s.pool.Close() }

// UpsertCounters writes every (chart, bucket) of one period in a single
// transaction. The fleet is MANY machines reporting the same period (no
// machine identifier exists — telemetry is zero-PII), so ON CONFLICT
// ADDS to the running total rather than replacing it: machine A's up=5
// and machine B's up=3 for the same day sum to 8. period_date /
// granularity / received_at are refreshed on each submission.
//
// Tradeoff: there is no per-upload dedup key (a persistent one would be a
// pseudo-identifier and break the zero-PII boundary), so a machine whose
// upload committed but whose response was lost will over-count that one
// period by one cycle on its deferred retry. This is rare, bounded, and
// negligible for the adoption *trends* this feeds. (A future random,
// per-period-file nonce — non-persistent, so not a machine id — could
// dedup exact retries while still summing across machines.)
func (s *PgStore) UpsertCounters(ctx context.Context, period, granularity string, periodStart *time.Time, counters map[string]map[string]int) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after commit

	batch := &pgx.Batch{}
	const q = `
INSERT INTO counter_period (period, period_date, granularity, chart, bucket, count, received_at)
VALUES ($1, $2, $3, $4, $5, $6, now())
ON CONFLICT (period, chart, bucket)
DO UPDATE SET count = counter_period.count + EXCLUDED.count,
              period_date = EXCLUDED.period_date,
              granularity = EXCLUDED.granularity,
              received_at = now()`
	for chart, buckets := range counters {
		for bucket, n := range buckets {
			batch.Queue(q, period, periodStart, granularity, chart, bucket, n)
		}
	}
	br := tx.SendBatch(ctx, batch)
	// Drain every queued result; the first error aborts.
	for range countItems(counters) {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			return fmt.Errorf("exec upsert: %w", err)
		}
	}
	if err := br.Close(); err != nil {
		return fmt.Errorf("close batch: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func countItems(counters map[string]map[string]int) []struct{} {
	n := 0
	for _, b := range counters {
		n += len(b)
	}
	return make([]struct{}, n)
}
