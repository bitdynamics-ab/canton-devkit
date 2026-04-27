# 002 - Consider Cobra CLI Framework

**Type:** Task
**Status:** ⏳ Not started
**Created:** 2026-04-27
**Updated:** 2026-04-27

## Goal

Evaluate whether Canton DevKit should migrate from its initial standard-library
CLI parser to Cobra once the command surface and UX requirements are clearer.

## Progress

- [ ] Review command complexity after the initial CLI boilerplate is in use
- [ ] Compare standard-library parsing against Cobra for help text, validation, and subcommands
- [ ] Decide whether the dependency and migration cost are justified
- [ ] Document the decision and implementation plan if migration is approved

## Notes

- The initial CLI intentionally avoids external dependencies.
- Revisit this if command nesting, flags, shell completion, or generated docs become important.

## Next Steps

Wait until the first real localnet workflows are implemented, then reassess CLI
framework needs.
