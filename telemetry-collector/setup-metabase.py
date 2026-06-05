#!/usr/bin/env python3
"""Provision Metabase for the canton-devkit telemetry collector.

Idempotent: connects the `telemetry` Postgres database, creates a
collection, builds five saved questions (charts) over `counter_period`,
and assembles them into a dashboard. Re-running updates in place rather
than duplicating.

Your Metabase password stays on this machine — it's read from the
environment and POSTed only to your local Metabase. Nothing is sent
anywhere else.

Usage:
    export MB_USER='you@example.com'
    export MB_PASS='your-metabase-password'
    export DB_PASS='localtest_pw_8842'          # the POSTGRES_PASSWORD from .env
    python3 setup-metabase.py

Env (with defaults):
    MB_URL   http://localhost:3000     Metabase base URL
    DB_HOST  postgres                  how Metabase reaches Postgres (compose service name)
    DB_NAME  telemetry
    DB_USER  postgres
"""
import json
import os
import sys
import time
import urllib.error
import urllib.request

MB_URL = os.environ.get("MB_URL", "http://localhost:3000").rstrip("/")
MB_USER = os.environ.get("MB_USER")
MB_PASS = os.environ.get("MB_PASS")
MB_API_KEY = os.environ.get("MB_API_KEY")  # alternative to MB_USER/MB_PASS
DB_PASS = os.environ.get("DB_PASS")
DB_HOST = os.environ.get("DB_HOST", "postgres")
DB_NAME = os.environ.get("DB_NAME", "telemetry")
DB_USER = os.environ.get("DB_USER", "postgres")
DB_DISPLAY = "telemetry counters"
COLLECTION_NAME = "canton-devkit telemetry"
DASHBOARD_NAME = "canton-devkit usage"

if not DB_PASS:
    sys.exit("set DB_PASS (the POSTGRES_PASSWORD from .env)")
if not (MB_API_KEY or (MB_USER and MB_PASS)):
    sys.exit("auth: set MB_API_KEY, or both MB_USER and MB_PASS (see header)")

_session = None


def api(method, path, body=None, auth=True):
    url = MB_URL + path
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    if auth:
        if MB_API_KEY:
            req.add_header("x-api-key", MB_API_KEY)
        elif _session:
            req.add_header("X-Metabase-Session", _session)
    try:
        with urllib.request.urlopen(req) as resp:
            raw = resp.read()
            return json.loads(raw) if raw else {}
    except urllib.error.HTTPError as e:
        detail = e.read().decode(errors="replace")
        raise SystemExit(f"{method} {path} -> HTTP {e.code}: {detail}")


def login():
    global _session
    if MB_API_KEY:
        print("✓ using Metabase API key")
        return
    r = api("POST", "/api/session", {"username": MB_USER, "password": MB_PASS}, auth=False)
    _session = r["id"]
    print("✓ logged in to Metabase")


def ensure_database():
    dbs = api("GET", "/api/database")
    items = dbs.get("data", dbs) if isinstance(dbs, dict) else dbs
    for db in items:
        if db.get("name") == DB_DISPLAY:
            print(f"✓ database '{DB_DISPLAY}' already connected (id={db['id']})")
            return db["id"]
    body = {
        "name": DB_DISPLAY,
        "engine": "postgres",
        "details": {
            "host": DB_HOST, "port": 5432, "dbname": DB_NAME,
            "user": DB_USER, "password": DB_PASS, "ssl": False,
        },
    }
    db = api("POST", "/api/database", body)
    print(f"✓ connected database '{DB_DISPLAY}' (id={db['id']})")
    return db["id"]


def wait_for_table(db_id, table="counter_period", tries=20):
    for _ in range(tries):
        meta = api("GET", f"/api/database/{db_id}/metadata")
        for t in meta.get("tables", []):
            if t.get("name") == table:
                print(f"✓ Metabase synced table '{table}'")
                return
        api("POST", f"/api/database/{db_id}/sync_schema")
        time.sleep(2)
    print(f"! table '{table}' not visible yet — Metabase will finish syncing shortly")


def ensure_collection():
    cols = api("GET", "/api/collection")
    for c in cols:
        if c.get("name") == COLLECTION_NAME:
            print(f"✓ collection '{COLLECTION_NAME}' exists (id={c['id']})")
            return c["id"]
    c = api("POST", "/api/collection", {"name": COLLECTION_NAME})
    print(f"✓ created collection '{COLLECTION_NAME}' (id={c['id']})")
    return c["id"]


def native_card(name, sql, display, db_id, coll_id, viz=None):
    """Create or update a saved question by name within the collection."""
    payload = {
        "name": name,
        "display": display,
        "visualization_settings": viz or {},
        "dataset_query": {"type": "native", "native": {"query": sql}, "database": db_id},
        "collection_id": coll_id,
    }
    existing = api("GET", f"/api/collection/{coll_id}/items?models=card")
    for it in existing.get("data", []):
        if it.get("name") == name:
            card = api("PUT", f"/api/card/{it['id']}", payload)
            print(f"  ↻ updated card: {name} (id={card['id']})")
            return card["id"]
    card = api("POST", "/api/card", payload)
    print(f"  + created card: {name} (id={card['id']})")
    return card["id"]


def build_cards(db_id, coll_id):
    """Build every saved question, tagged by source in its title:
    [telemetry] = privacy-preserving counters, [GitHub] = release/repo
    signals, [qualitative] = maintainer-entered evidence."""
    ids = {}
    # ---- M1: LocalNet lifecycle (cross-platform incl. Windows) ----
    ids["daily"] = native_card(
        "Commands per day", "SELECT period_date, sum(count) AS commands "
        "FROM counter_period WHERE chart='dpm/command' GROUP BY period_date ORDER BY period_date",
        "line", db_id, coll_id,
        {"graph.dimensions": ["period_date"], "graph.metrics": ["commands"]})
    ids["up_outcome"] = native_card(
        "LocalNet starts — ok vs fail",
        "SELECT split_part(bucket,'/',2) AS outcome, sum(count) AS n "
        "FROM counter_period WHERE chart='dpm/command_exit' AND bucket LIKE 'up/%' GROUP BY 1 ORDER BY 1",
        "bar", db_id, coll_id,
        {"graph.dimensions": ["outcome"], "graph.metrics": ["n"]})
    ids["os"] = native_card(
        "Platform split (incl. Windows)", "SELECT bucket AS os, sum(count) AS total "
        "FROM counter_period WHERE chart='dpm/os' GROUP BY bucket ORDER BY total DESC",
        "pie", db_id, coll_id)
    ids["top"] = native_card(
        "Top commands", "SELECT bucket AS command, sum(count) AS total "
        "FROM counter_period WHERE chart='dpm/command' GROUP BY bucket ORDER BY total DESC",
        "row", db_id, coll_id,
        {"graph.dimensions": ["command"], "graph.metrics": ["total"]})
    # ---- M2: Web UI, observability, DAR, explorer ----
    ids["ui"] = native_card(
        "Web UI features used (per session)",
        "SELECT bucket AS feature, sum(count) AS sessions "
        "FROM counter_period WHERE chart='dpm/ui_feature' GROUP BY 1 ORDER BY 2 DESC",
        "row", db_id, coll_id,
        {"graph.dimensions": ["feature"], "graph.metrics": ["sessions"]})
    ids["ci"] = native_card(
        "CI vs interactive",
        "SELECT CASE bucket WHEN 'true' THEN 'CI' ELSE 'interactive' END AS env, sum(count) AS n "
        "FROM counter_period WHERE chart='dpm/ci' GROUP BY 1",
        "pie", db_id, coll_id)
    ids["agent"] = native_card(
        "AI agent usage",
        "SELECT bucket AS agent, sum(count) AS n "
        "FROM counter_period WHERE chart='dpm/llm_agent' GROUP BY 1 ORDER BY 2 DESC",
        "pie", db_id, coll_id)
    # ---- M3: Token Standard CIP-0112 flow ----
    ids["token"] = native_card(
        "Token actions (CIP-0112 flow)",
        "SELECT bucket AS action, sum(count) AS n "
        "FROM counter_period WHERE chart='dpm/token_action' GROUP BY 1 ORDER BY 2 DESC",
        "bar", db_id, coll_id,
        {"graph.dimensions": ["action"], "graph.metrics": ["n"]})
    # ---- M4: composite adoption — installs, visibility, qualitative ----
    ids["installs"] = native_card(
        "Distinct installs by platform (telemetry device-count proxy)",
        "SELECT bucket AS platform, sum(count) AS installs "
        "FROM counter_period WHERE chart='dpm/install' GROUP BY 1 ORDER BY 2 DESC",
        "bar", db_id, coll_id,
        {"graph.dimensions": ["platform"], "graph.metrics": ["installs"]})
    ids["downloads"] = native_card(
        "Cumulative downloads (toward 250 floor)",
        "SELECT captured_on, total_downloads FROM v_downloads_total ORDER BY captured_on",
        "line", db_id, coll_id,
        {"graph.dimensions": ["captured_on"], "graph.metrics": ["total_downloads"]})
    ids["stars"] = native_card(
        "Stars & forks over time (visibility)",
        "SELECT captured_on, stars, forks FROM github_repo_stats ORDER BY captured_on",
        "line", db_id, coll_id,
        {"graph.dimensions": ["captured_on"], "graph.metrics": ["stars", "forks"]})
    ids["evidence"] = native_card(
        "External adoption evidence",
        "SELECT milestone, team, evidence_type, url, noted_on FROM adoption_evidence "
        "ORDER BY milestone, noted_on DESC",
        "table", db_id, coll_id)
    ids["raw"] = native_card(
        "All counters (raw, exportable)",
        "SELECT period_date, granularity, chart, bucket, count FROM counter_period ORDER BY period_date DESC, chart, bucket",
        "table", db_id, coll_id)
    return ids


def heading(text):
    """A full-width markdown section header (a Metabase 'virtual' text card)."""
    return {
        "text": text,
        "virtual_card": {"name": None, "display": "text",
                         "visualization_settings": {}, "dataset_query": {}, "archived": False},
    }


def ensure_dashboard(coll_id, card_ids):
    dashes = api("GET", f"/api/collection/{coll_id}/items?models=dashboard")
    dash_id = None
    for it in dashes.get("data", []):
        if it.get("name") == DASHBOARD_NAME:
            dash_id = it["id"]
            break
    if dash_id is None:
        d = api("POST", "/api/dashboard", {"name": DASHBOARD_NAME, "collection_id": coll_id})
        dash_id = d["id"]
        print(f"✓ created dashboard '{DASHBOARD_NAME}' (id={dash_id})")
    else:
        print(f"✓ dashboard '{DASHBOARD_NAME}' exists (id={dash_id})")

    # Milestone-grouped 18-col layout. Charts are a uniform 2-per-row
    # (each 9 wide, 7 tall) so titles and legends never truncate;
    # row-charts/tables span the full 18. Each section opens with a
    # full-width heading that names the data source, so the source is
    # clear without crowding every chart title. Entries:
    # (kind, ref/text, col, row, w, h). kind 'h'=heading, 'c'=card.
    GAP = 0
    plan = []
    r = 0

    def head(text, h=1):
        nonlocal r
        plan.append(("h", text, 0, r, 18, h))
        r += h

    def row1(key, h=7):  # one full-width card
        nonlocal r
        plan.append(("c", key, 0, r, 18, h))
        r += h

    def row2(left, right, h=7):  # two side-by-side cards
        nonlocal r
        plan.append(("c", left, 0, r, 9, h))
        plan.append(("c", right, 9, r, 9, h))
        r += h

    head("# Canton DevKit — Adoption metrics\n\n"
         "Grouped by proposal milestone. **Sources:** _telemetry_ = privacy-preserving "
         "CLI/Web-UI counters (opt-out, zero-PII) · _GitHub_ = release & repo signals · "
         "_evidence_ = maintainer-logged external teams.", h=3)

    head("## M1 · LocalNet lifecycle  —  source: telemetry")
    row2("daily", "up_outcome")
    row2("os", "top")

    head("## M2 · Web UI, observability, DAR & Explorer  —  source: telemetry")
    row1("ui", h=6)
    row2("ci", "agent")

    head("## M3 · Token Standard (CIP-0112) flow  —  source: telemetry")
    row1("token")

    head("## M4 · Composite adoption  —  sources: telemetry installs + GitHub + maintainer evidence")
    row2("installs", "downloads")
    row2("stars", "evidence", h=6)

    head("### Appendix · raw counters (one-click CSV / Excel export)")
    row1("raw", h=8)

    dashcards = []
    n_cards = n_head = 0
    for i, (kind, ref, col, row, w, h) in enumerate(plan):
        dc = {"id": -(i + 1), "row": row, "col": col, "size_x": w, "size_y": h,
              "parameter_mappings": []}
        if kind == "h":
            dc["card_id"] = None
            dc["visualization_settings"] = heading(ref)
            n_head += 1
        else:
            dc["card_id"] = card_ids[ref]
            dc["visualization_settings"] = {}
            n_cards += 1
        dashcards.append(dc)
    api("PUT", f"/api/dashboard/{dash_id}", {"dashcards": dashcards})
    print(f"✓ laid out {n_cards} charts under {n_head} milestone sections")
    return dash_id


def archive_stale_cards(coll_id, keep_ids):
    """Archive any card in the collection that isn't part of the current
    dashboard set — clears duplicates left by earlier title changes so the
    collection stays clean. Scoped to this dedicated collection only."""
    keep = set(keep_ids)
    items = api("GET", f"/api/collection/{coll_id}/items?models=card")
    n = 0
    for it in items.get("data", []):
        if it["id"] not in keep and not it.get("archived"):
            api("PUT", f"/api/card/{it['id']}", {"archived": True})
            n += 1
    if n:
        print(f"✓ archived {n} stale card(s) from earlier runs")


def main():
    login()
    db_id = ensure_database()
    wait_for_table(db_id)
    coll_id = ensure_collection()
    print("building charts:")
    card_ids = build_cards(db_id, coll_id)
    dash_id = ensure_dashboard(coll_id, card_ids)
    archive_stale_cards(coll_id, card_ids.values())
    print()
    print("Done. Open your dashboard:")
    print(f"  {MB_URL}/dashboard/{dash_id}")
    print(f"  collection: {MB_URL}/collection/{coll_id}")


if __name__ == "__main__":
    main()
