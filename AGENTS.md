# Agent instructions

## Comments

Do not add comments by default. No narration, section banners, or why-comments.

Allowed only:

- Legal / license headers
- Public API doc comments that define a contract
- `TODO(#issue):` / `FIXME(#issue):` parity markers (CLI ↔ Web UI), per CONTRIBUTING
- Issue/RFC links that state a constraint code cannot express
- Behavior forced by an external dependency, platform, vendor, or protocol we cannot reshape

Encode other constraints in types, names, tests, or CI. Before review, run `/no-comments` on the diff.
