-- Give Metabase its OWN database for its ~40 internal application tables,
-- separate from the `telemetry` database that holds your counter data.
-- This keeps `telemetry` clean: pg_dump / CSV exports contain only your
-- counters, never Metabase's bookkeeping. Runs before 01-schema.sql
-- (alphabetical order) on first cluster init.
CREATE DATABASE metabase;
