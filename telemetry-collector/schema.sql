-- canton-devkit telemetry collector schema.
--
-- One row per (period, chart, bucket). The fleet is many machines
-- reporting the same period (zero-PII, so no machine identifier exists),
-- so ingestion SUMS count on conflict — machine A's up=5 and machine B's
-- up=3 for the same day aggregate to 8.
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

-- Unique-install dedup. One row per (anonymous token, active date). The
-- token is a random UUIDv4 minted client-side (installid.go) — NOT derived
-- from any hardware attribute, and stored here ALONE, never joined to a
-- counter. Its only purpose is to let us count DISTINCT installs active in
-- a period, the one adoption number the additive counter_period table
-- cannot give. A fresh container/VM/reinstall mints a new token by design,
-- so this counts environments, not people.
CREATE TABLE IF NOT EXISTS seen_install (
    install_id  text        NOT NULL,            -- opaque random UUIDv4, hardware-independent
    period_date date        NOT NULL,            -- start-of-period the token was active in
    first_seen  timestamptz NOT NULL DEFAULT now(),
    last_seen   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (install_id, period_date)
);

CREATE INDEX IF NOT EXISTS seen_install_date_idx ON seen_install (period_date);

-- Unique active installs per day — the headline "how many distinct
-- devices" number (counts each environment once per period).
CREATE OR REPLACE VIEW v_unique_installs_daily AS
SELECT period_date, count(DISTINCT install_id) AS unique_installs
FROM seen_install
GROUP BY period_date
ORDER BY period_date;

-- All-time distinct installs ever seen — cumulative unique-environment
-- count toward the adoption floor.
CREATE OR REPLACE VIEW v_unique_installs_total AS
SELECT count(DISTINCT install_id) AS unique_installs_all_time
FROM seen_install;

-- GitHub adoption signals (populated by cmd/github-stats on a daily
-- schedule). These cover the install/visibility legs of the proposal's
-- composite adoption measure that zero-PII telemetry can't provide.

CREATE TABLE IF NOT EXISTS github_release_downloads (
    captured_on    date    NOT NULL,            -- snapshot date (UTC)
    tag            text    NOT NULL,            -- release tag, e.g. v0.3.1
    asset          text    NOT NULL,            -- asset file name
    download_count integer NOT NULL CHECK (download_count >= 0), -- cumulative since publish
    PRIMARY KEY (captured_on, tag, asset)
);

CREATE TABLE IF NOT EXISTS github_repo_stats (
    captured_on date    NOT NULL PRIMARY KEY,
    stars       integer NOT NULL,
    forks       integer NOT NULL,
    watchers    integer NOT NULL,
    open_issues integer NOT NULL
);

-- Cumulative downloads across all assets at the latest snapshot — the
-- headline number toward the Milestone-4 install floor.
CREATE OR REPLACE VIEW v_downloads_total AS
SELECT captured_on, sum(download_count) AS total_downloads
FROM github_release_downloads
GROUP BY captured_on
ORDER BY captured_on;

-- Qualitative adoption evidence — the leg neither telemetry (zero-PII)
-- nor GitHub can provide: the named external teams/projects and their
-- proof artifacts that the milestone acceptance bars require (M1: 3
-- teams, M2: 5, M3: 7, M4: 5 apps + case study). Filled by the
-- maintainer (SQL insert or Metabase data entry), surfaced on the
-- dashboard so the adoption story is one place.
CREATE TABLE IF NOT EXISTS adoption_evidence (
    id            serial PRIMARY KEY,
    milestone     text NOT NULL,                 -- 'M1' | 'M2' | 'M3' | 'M4'
    team          text NOT NULL,                 -- external team / project name
    evidence_type text NOT NULL,                 -- issue | demo | case-study | feedback | attestation
    url           text,                          -- link to the artifact
    noted_on      date NOT NULL DEFAULT current_date,
    notes         text
);
