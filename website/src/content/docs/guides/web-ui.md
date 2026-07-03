---
title: "Web UI"
description: "Launch the embedded dashboard, what it offers, and the loopback-first security model."
---

`canton-devkit localnet ui` launches the embedded Vite/React dashboard
(baked into the binary at build time). It mirrors the CLI — every
user-facing operation is available on both surfaces.

## Launch

```bash
canton-devkit localnet ui --name demo          # default: 127.0.0.1:7777
canton-devkit localnet ui --name demo --port 8080
```

Flags: `--port` (default `7777`, `0` = OS-assigned), `--host` (default
`127.0.0.1`), `--allow-non-loopback` (see [Security](#security) below).

## What you get

- **Live overview** — instance status, container health (SSE)
- **Developer setup** — copy JWTs, export `.env` / `.json` / `.yaml`
- **Backup & restore** — download a snapshot, drag-drop to restore
- **Per-container logs** — `docker logs --tail` in the browser
- **Command palette** — fuzzy-jump between instances and routes
  (<kbd>⌘ K</kbd> on macOS, <kbd>Ctrl K</kbd> elsewhere)
- **Live preflight** — Docker, memory, disk checks before every `up`

See also the [Explorer](explorer/) and [Tokens (CIP-0112 / V2)](tokens/)
guides for ledger and token workspace features.

## Security

The UI handles JWTs and party identifiers. It is designed for **local
development on loopback**, not for unauthenticated LAN-wide exposure.

- **Loopback binding** — binds to `127.0.0.1` by default. Non-loopback
  `--host` values are refused unless `--allow-non-loopback` is also set
  (DNS-rebinding defence).
- **CSRF** — same-Origin gate on all state-changing API requests.
- **JWT redaction** — tokens are redacted by default in API responses;
  opt in via an explicit query flag when you need the raw value.
- **Embedded SPA** — no external CDN, no analytics, no phone-home.

### Remote access

Prefer an SSH tunnel over widening the bind address:

```sh
ssh -L 7777:127.0.0.1:7777 dev-host
```

Then open `http://127.0.0.1:7777` locally. Only use
`--allow-non-loopback` when you understand the exposure and have a
firewall or reverse proxy in front.
