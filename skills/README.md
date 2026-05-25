# Skill docs source

The canonical .md files for `dpm localnet skills install` live at:

    internal/cli/localnet/skills_embed/

This README sits at the repo root because that's where contributors and
the Web UI Agent screen expect to find them. The actual files moved
into `internal/cli/localnet/skills_embed/` so Go's `//go:embed`
directive can reach them — `embed.FS` does not support `..` traversal,
and the import direction (CLI → skills) is wrong for the reverse.

When editing or adding a skill, edit the file in `internal/cli/localnet/skills_embed/`.

See [the embed-location README](../internal/cli/localnet/skills_embed/README.md)
for the authoring rules and the list of bundled skills.
