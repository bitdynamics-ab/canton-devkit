package localnet

import (
	"bufio"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/skills"
	"github.com/spf13/cobra"
)

// TestSkillsLint_AgainstLiveCobraSurface is the structural pin:
// skill docs reference `dpm localnet <verb>` commands and `--flag`
// arguments, and manual
// review can't keep them in sync with the live cobra surface. This
// test reads every bundled skill via skills.List() (the same docs the
// CLI/UI ship), parses every fenced `sh` block, extracts the
// (verb, flags) pairs, and asserts:
//
// 1. each verb exists as a subcommand under `dpm localnet`
// (or under a sub-subcommand for multi-level verbs like
// `token create`)
// 2. each `--flag` exists on that subcommand's flag set
//
// Two escape hatches keep the lint usable:
//
// - futureVerbs allowlist: verbs whose implementation hasn't
// landed yet. Skills documenting future commands carry a "TODO:"
// note so `grep -rn 'TODO' internal/cli/localnet/skills_lint_test.go`
// enumerates outstanding implementation work.
// - skill-lint-ignore marker: a line in the skill can carry
// "<!-- skill-lint: skip-next -->" to opt out of the lint
// for the next fenced block (e.g. shell utility examples that
// don't reference dpm flags meaningfully).
//
// Without this test, a future cobra refactor that renames --port to
// --tcp-port (say) silently breaks every skill that teaches an agent
// the old flag — and the agent then teaches the user the broken flag.
// The test fails CI on the next push.
func TestSkillsLint_AgainstLiveCobraSurface(t *testing.T) {
	root := Build() // `dpm localnet` root

	docs, err := skills.List()
	if err != nil {
		t.Fatalf("list skills: %v", err)
	}
	cmds := skillCmds(docs)
	if len(cmds) == 0 {
		t.Fatal("no `dpm localnet ...` invocations found in skills — parser broken?")
	}

	for _, sc := range cmds {
		t.Run(sc.location, func(t *testing.T) {
			if reason, ok := futureVerbs[sc.verbPath[0]]; ok {
				t.Logf("verb %q deferred (future command, tracked by %s) — flag checks skipped",
					sc.verbPath[0], reason)
				return
			}
			cmd, found := resolveCobra(root, sc.verbPath)
			if !found {
				t.Errorf("verb %q not found under `dpm localnet`; skill at %s references a command that doesn't exist (typo, or add it to futureVerbs with a TODO note)",
					strings.Join(sc.verbPath, " "), sc.location)
				return
			}
			// Stub commands (DisableFlagParsing=true) get the
			// flag-check skip too. Validating flags against a
			// transitional stub would force the skill to track the
			// stub shape instead of the final one. Same logging
			// contract as futureVerbs so CI output makes the
			// deferred verification visible.
			if cmd.DisableFlagParsing {
				t.Logf("verb %q is a stub (DisableFlagParsing=true) — flag checks skipped until real impl lands",
					strings.Join(sc.verbPath, " "))
				return
			}
			for _, flag := range sc.flags {
				if !cmdHasFlag(cmd, flag) {
					t.Errorf("flag %q not on `dpm localnet %s` (skill: %s); cobra renames silently break the skill",
						flag, strings.Join(sc.verbPath, " "), sc.location)
				}
			}
		})
	}
}

// futureVerbs is the explicit allowlist of `dpm localnet <verb>`
// invocations whose implementation hasn't landed yet. Skills can
// document them in advance, but the lint skips the (verb, flags)
// check because there's no cobra command to validate against.
//
// Each entry has a short TODO note so unfinished verbs stay
// grep-able:
//
//	grep -rn 'TODO' internal/cli/localnet/skills_lint_test.go
//
// When the verb's implementation lands, the entry MUST be removed
// from this map. A regression then fails the lint with the real
// flag-mismatch error rather than the allowlist skip.
var futureVerbs = map[string]string{
	"token": "TODO: token create/mint/transfer/burn/balance",
}

// scannedCommand is one extracted `dpm localnet <verb> ... --flag ...`
// invocation, paired with its source location for error attribution.
type scannedCommand struct {
	location string   // e.g. "localnet-lifecycle.md:42"
	verbPath []string // e.g. ["status"] or ["token", "create"]
	flags    []string // e.g. ["--name", "--format"]
}

// dpmInvocationRE matches the start of a `dpm localnet ...` line in a
// fenced sh block. The capture is the rest of the line after
// "dpm localnet ". We accept both `dpm localnet` and the standalone
// `canton-devkit localnet` form since the docs use both.
var dpmInvocationRE = regexp.MustCompile(`(?m)^\s*(?:dpm|canton-devkit)\s+localnet\s+(.*)$`)

// shFenceRE matches the opening fence of a fenced code block we should
// scan: ```sh / ```bash / ```shell, OR a bare ``` with no language tag
// (the convention the bundled skill docs use for command blocks).
// Accepting bare fences is safe because only lines matching
// dpmInvocationRE are extracted — a non-shell block (e.g. JSON output)
// simply yields no commands. A tagged non-shell fence like ```python
// still won't match (the language must be one of sh/bash/shell or
// absent), so those stay skipped.
var shFenceRE = regexp.MustCompile("^```(sh|bash|shell)?\\s*$")

// flagRE matches `--foo` (with or without `=value` or a following
// argument). We deliberately ignore single-letter `-f` shorthand
// because the cobra commands don't define shorthands; if that changes
// the lint needs updating.
var flagRE = regexp.MustCompile(`--[a-zA-Z][a-zA-Z0-9-]*`)

// skillCmds extracts every `dpm localnet ...` invocation from the
// fenced sh blocks of the bundled skills, tagged with source location
// (filename:line). Sorted for deterministic subtest ordering.
func skillCmds(docs []skills.Skill) []scannedCommand {
	var out []scannedCommand
	for _, d := range docs {
		cmds := extractDPMCommands(d.Body)
		for i := range cmds {
			cmds[i].location = d.Filename + cmds[i].location
		}
		out = append(out, cmds...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].location < out[j].location
	})
	return out
}

// extractDPMCommands parses a single markdown file's text and pulls
// out every `dpm localnet <verb> ... --flag ...` invocation it finds
// inside fenced sh / bash / shell blocks.
//
// Multi-line commands joined by trailing `\` are collapsed onto a
// single line before flag extraction.
//
// "<!-- skill-lint: skip-next -->" preceding a fenced block opts the
// whole block out of the lint.
func extractDPMCommands(text string) []scannedCommand {
	var out []scannedCommand
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	inFence := false
	skipNext := false
	skipThis := false
	var lineNo int
	var blockLines []string
	var blockStartLine int

	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if !inFence {
			if strings.Contains(line, "<!-- skill-lint: skip-next -->") {
				skipNext = true
				continue
			}
			if shFenceRE.MatchString(strings.TrimSpace(line)) {
				inFence = true
				skipThis = skipNext
				skipNext = false
				blockLines = blockLines[:0]
				blockStartLine = lineNo + 1
			}
			continue
		}
		if strings.TrimSpace(line) == "```" {
			if !skipThis {
				out = append(out, parseBlock(blockLines, blockStartLine)...)
			}
			inFence = false
			skipThis = false
			continue
		}
		blockLines = append(blockLines, line)
	}
	return out
}

// parseBlock handles one fenced block. Joins backslash-continued
// lines, then runs dpmInvocationRE.
func parseBlock(lines []string, startLine int) []scannedCommand {
	// Collapse trailing-backslash continuations so a multi-line
	// command like
	// dpm localnet token create \
	// --symbol RTK \
	// --decimals 6
	// becomes one line for parsing.
	joined := make([]string, 0, len(lines))
	joinedLineNos := make([]int, 0, len(lines))
	var pending strings.Builder
	var pendingStart int
	for i, l := range lines {
		trimmed := strings.TrimRight(l, " \t")
		if pending.Len() == 0 {
			pendingStart = startLine + i
		}
		if strings.HasSuffix(trimmed, `\`) {
			pending.WriteString(strings.TrimSuffix(trimmed, `\`))
			pending.WriteString(" ")
			continue
		}
		pending.WriteString(trimmed)
		joined = append(joined, pending.String())
		joinedLineNos = append(joinedLineNos, pendingStart)
		pending.Reset()
	}
	if pending.Len() > 0 {
		joined = append(joined, pending.String())
		joinedLineNos = append(joinedLineNos, pendingStart)
	}

	var out []scannedCommand
	for i, line := range joined {
		// Strip inline `#` comments so a comment like `--port 7777
		// # dev default` doesn't get parsed as two flags.
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = line[:idx]
		}
		m := dpmInvocationRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		rest := strings.TrimSpace(m[1])
		if rest == "" {
			continue
		}
		// Verb path is the leading positional args (everything up to
		// the first `--flag`, `<placeholder>`, `$var`, `"quoted"` or
		// shell metacharacter). We stop at the first token that starts
		// with `-`, `<`, `$`, `"`, `'`, `|`, `&`, or `;`.
		fields := strings.Fields(rest)
		verbPath := make([]string, 0, 2)
		for _, f := range fields {
			if len(f) == 0 {
				continue
			}
			c := f[0]
			if c == '-' || c == '<' || c == '$' || c == '"' || c == '\'' || c == '|' || c == '&' || c == ';' {
				break
			}
			verbPath = append(verbPath, f)
		}
		if len(verbPath) == 0 {
			continue
		}
		flags := flagRE.FindAllString(line, -1)
		out = append(out, scannedCommand{
			location: fmt.Sprintf(":%d", joinedLineNos[i]),
			verbPath: verbPath,
			flags:    dedupStrings(flags),
		})
	}
	return out
}

// resolveCobra walks the cobra tree following verbPath as a
// LONGEST-PREFIX match: it consumes tokens as command segments only
// while they match a child, then stops — the remaining tokens are
// positional arguments (a DAR path, a service name, a package id),
// not subcommands. This is what lets `dar upload ./app.dar` resolve to
// the `upload` command (validating its flags) instead of treating
// `./app.dar` as a missing sub-subcommand.
//
// Returns the deepest matched command and true, or false when not even
// the first token is a real verb (a genuine top-level typo).
func resolveCobra(root *cobra.Command, verbPath []string) (*cobra.Command, bool) {
	current := root
	matchedAny := false
	for _, verb := range verbPath {
		var next *cobra.Command
		for _, child := range current.Commands() {
			if child.Name() == verb {
				next = child
				break
			}
		}
		if next == nil {
			break // positional arg (or end) — stop at the deepest match
		}
		current = next
		matchedAny = true
	}
	return current, matchedAny
}

// cmdHasFlag asks whether `cmd` (or any of its parents — cobra's
// persistent-flag inheritance) defines the given flag name. The
// leading `--` is stripped before lookup.
func cmdHasFlag(cmd *cobra.Command, flag string) bool {
	name := strings.TrimPrefix(flag, "--")
	if cmd.Flags().Lookup(name) != nil {
		return true
	}
	if cmd.PersistentFlags().Lookup(name) != nil {
		return true
	}
	for p := cmd.Parent(); p != nil; p = p.Parent() {
		if p.PersistentFlags().Lookup(name) != nil {
			return true
		}
	}
	return false
}

// dedupStrings removes duplicates while preserving order. Used so
// `--name <X> --name <Y>` reports as one flag, not two.
func dedupStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// TestSkillsLint_ParserHandlesEdgeCases is the unit-level pin for
// extractDPMCommands. The end-to-end lint would catch parser
// regressions too, but only by failing on shipped skill text — a
// parser bug that skips a fenced block would mean the end-to-end test
// never runs the affected assertion. This feeds known-tricky inputs
// and asserts the parsed output.
func TestSkillsLint_ParserHandlesEdgeCases(t *testing.T) {
	cases := []struct {
		name string
		md   string
		want []scannedCommand
	}{
		{
			name: "single fenced block, simple invocation",
			md:   "header\n```sh\ndpm localnet status --name demo\n```\n",
			want: []scannedCommand{
				{location: ":3", verbPath: []string{"status"}, flags: []string{"--name"}},
			},
		},
		{
			name: "multi-line backslash continuation",
			md:   "```sh\ndpm localnet up \\\n  --name demo \\\n  --version 0.6.4\n```\n",
			want: []scannedCommand{
				{location: ":2", verbPath: []string{"up"}, flags: []string{"--name", "--version"}},
			},
		},
		{
			name: "two-level verb (token create)",
			md:   "```sh\ndpm localnet token create --symbol RTK\n```\n",
			want: []scannedCommand{
				{location: ":2", verbPath: []string{"token", "create"}, flags: []string{"--symbol"}},
			},
		},
		{
			name: "canton-devkit form is also recognised",
			md:   "```sh\ncanton-devkit localnet up --name demo\n```\n",
			want: []scannedCommand{
				{location: ":2", verbPath: []string{"up"}, flags: []string{"--name"}},
			},
		},
		{
			name: "inline comment ignored",
			md:   "```sh\ndpm localnet up --name demo # leading dev instance\n```\n",
			want: []scannedCommand{
				{location: ":2", verbPath: []string{"up"}, flags: []string{"--name"}},
			},
		},
		{
			name: "bash fence is also recognised",
			md:   "```bash\ndpm localnet down --name demo\n```\n",
			want: []scannedCommand{
				{location: ":2", verbPath: []string{"down"}, flags: []string{"--name"}},
			},
		},
		{
			name: "bare fence (no language) is recognised — the skill-doc convention",
			md:   "```\ndpm localnet status --name demo\n```\n",
			want: []scannedCommand{
				{location: ":2", verbPath: []string{"status"}, flags: []string{"--name"}},
			},
		},
		{
			name: "non-sh block is skipped",
			md:   "```python\ndpm localnet up --name demo\n```\n",
			want: nil,
		},
		{
			name: "skip-next marker opts out a fenced block",
			md:   "<!-- skill-lint: skip-next -->\n```sh\ndpm localnet bogus --made-up\n```\n",
			want: nil,
		},
		{
			name: "prose mention not in fenced block is ignored",
			md:   "Try `dpm localnet up --port 7777` if you want.\n",
			want: nil,
		},
		{
			name: "dedups repeated flags",
			md:   "```sh\ndpm localnet dar upload --file a --file b\n```\n",
			want: []scannedCommand{
				{location: ":2", verbPath: []string{"dar", "upload"}, flags: []string{"--file"}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractDPMCommands(tc.md)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d commands, want %d: %+v", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i].location != tc.want[i].location {
					t.Errorf("[%d] location = %q, want %q", i, got[i].location, tc.want[i].location)
				}
				if strings.Join(got[i].verbPath, " ") != strings.Join(tc.want[i].verbPath, " ") {
					t.Errorf("[%d] verbPath = %v, want %v", i, got[i].verbPath, tc.want[i].verbPath)
				}
				if strings.Join(got[i].flags, ",") != strings.Join(tc.want[i].flags, ",") {
					t.Errorf("[%d] flags = %v, want %v", i, got[i].flags, tc.want[i].flags)
				}
			}
		})
	}
}

// TestSkillsLint_FutureVerbsHaveTODOs is the contract pin for the
// futureVerbs map: every entry MUST carry a "TODO:" note so unfinished
// skill drift stays grep-able. Empty values get rejected.
func TestSkillsLint_FutureVerbsHaveTODOs(t *testing.T) {
	for verb, note := range futureVerbs {
		if note == "" {
			t.Errorf("futureVerbs[%q] has empty note — add a TODO so the entry is grep-able", verb)
		}
		if !strings.HasPrefix(note, "TODO") {
			t.Errorf("futureVerbs[%q] = %q — expected TODO prefix for grep-ability", verb, note)
		}
	}
}
