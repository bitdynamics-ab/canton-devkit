-- canton-devkit telemetry collector schema.
--
-- One row per (period, chart, bucket). The CLI sends cumulative totals
-- for a period, so ingestion REPLACES count on conflict (last write
-- wins) rather than summing — re-sending a period is idempotent.
--
-- Zero-PII: every column is either a coarse time bucket, an allow-listed
-- counter name, or an integer. No identifiers, no timestamps finer than
-- the period, no free-form text from clients beyond the constrained
-- period/chart/bucket strings.

CREATE TABLE IF NOT EXISTS counter_period (
    period       text        NOT NULL,            -- raw key: '2026-06-04' (daily) or '2026-W22' (weekly)
    period_date  date,                            -- start-of-period for time-series; NULL if unparseable
    granularity  text        NOT NULL,            -- 'daily' | 'weekly'
    chart        text        NOT NULL,            -- e.g. 'dpm/command'
    bucket       text        NOT NULL,            -- e.g. 'up'
    count        integer     NOT NULL CHECK (count >= 0),
    received_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (period, chart, bucket)
);

CREATE INDEX IF NOT EXISTS counter_period_date_idx  ON counter_period (period_date);
CREATE INDEX IF NOT EXISTS counter_period_chart_idx ON counter_period (chart, bucket);

-- Convenience view: daily command usage, newest first. Metabase can chart
-- this directly or you can SELECT/COPY it to CSV for export.
CREATE OR REPLACE VIEW v_command_usage AS
SELECT period_date, bucket AS command, count
FROM counter_period
WHERE chart = 'dpm/command'
ORDER BY period_date DESC, count DESC;
