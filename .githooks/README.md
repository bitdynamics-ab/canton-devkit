# Git hooks

Repo hooks for Cloud Agents and local commits.

| Hook | Purpose |
| --- | --- |
| `commit-msg` | Strip `Co-authored-by: Zhe Li <linuxcity.jn@gmail.com>` (Cursor Cloud initiator trailer) |
| `strip-coauthored-by.sh` | Shared implementation |

## Why the ensure script exists

Cursor Cloud sets `core.hooksPath` to a managed dispatcher under
`~/.cursor/agent-hooks/…`. That dispatcher:

1. Runs hooks from `.cursor-original-hooks-path` (we point this at `.githooks/`)
2. Then runs Cursor’s own `commit-msg.cursor.co-author`, which **re-adds** the trailer

So we also install `commit-msg.cursor.zz-strip-coauthor` into the managed
hooks dir so it runs **after** Cursor’s co-author hook.

```bash
bash scripts/ci/ensure-cursor-commit-hooks.sh
```

The Cursor Agent hook `.cursor/hooks/check-git-commit.sh` runs that ensure
step before every `git commit`.
