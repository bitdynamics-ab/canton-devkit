package localnet

import (
	"context"
	"fmt"
	"io"
	"os/signal"
	"sort"
	"syscall"

	"github.com/bitdynamics-ab/canton-devkit/internal/docker"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// CleanOptions captures `localnet clean` flags. Cobra binds directly.
type CleanOptions struct {
	// Name targets a single instance. The CLI layer rejects --name
	// together with --all as mutually exclusive; if both are set on
	// the struct directly (e.g. a programmatic caller), cleanTargets
	// honours All and walks every instance.
	Name string
	// All cleans every registered instance (walks the index).
	All bool
	// Force allows cleaning a RUNNING instance — clean tears it
	// down (compose down --volumes) first. Without Force, a running
	// instance is refused with a "run down first" hint.
	Force bool
	// DryRun prints what WOULD be removed without touching anything.
	DryRun bool

	// NewRunner is a test-only seam mirroring DownOptions.NewRunner.
	// When nil, RunClean constructs the real *docker.ComposeRunner.
	NewRunner func(projectName string, composeFiles, envFiles, env []string, workDir string, logw io.Writer) composeDowner
}

// RunClean removes DevKit-managed state for one or all instances.
//
// Unlike `down` (which stops a running instance and is the normal
// lifecycle exit), `clean` is the recovery / housekeeping verb: it
// removes the registry entry, the per-instance data dir, and — for
// instances that still have docker resources — runs
// `docker compose down --volumes --remove-orphans` to reclaim
// lingering containers/volumes. It is the documented remedy for
// orphaned or corrupted instance state.
//
// Safety rules:
//   - A RUNNING instance is refused unless --force (which tears it
//     down first). This prevents a `clean` from silently nuking a
//     live demo.
//   - --dry-run prints the plan and changes nothing.
//   - Orphaned entries (index row present, state.json gone) are
//     scrubbed idempotently.
//
// Exit code: ExitSuccess when every targeted instance was cleaned
// (or nothing matched). ExitUserError when a running instance was
// refused without --force. ExitRuntimeFailure on a teardown/delete
// error.
func RunClean(ctx context.Context, out io.Writer, errw io.Writer, opts *CleanOptions) int {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	targets, err := cleanTargets(opts)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "%s\n", err)
		return ExitUserError
	}
	if len(targets) == 0 {
		if opts.All {
			_, _ = fmt.Fprintln(out, "No registered instances. Nothing to clean.")
		} else {
			_, _ = fmt.Fprintf(out, "No instance named %q is registered. Nothing to clean.\n", opts.Name)
		}
		return ExitSuccess
	}

	exit := ExitSuccess
	cleaned := 0
	for _, name := range targets {
		code := cleanOne(ctx, out, errw, opts, name)
		switch code {
		case ExitSuccess:
			cleaned++
		case ExitUserError:
			// Refused (running, no --force). Keep going through the
			// rest of an --all sweep but remember the soft failure.
			if exit == ExitSuccess {
				exit = ExitUserError
			}
		default:
			// Hard failure trumps a soft refusal.
			exit = code
		}
	}

	if opts.DryRun {
		_, _ = fmt.Fprintf(out, "\nDry run — nothing was removed. %d instance(s) matched.\n", len(targets))
		return exit
	}
	_, _ = fmt.Fprintf(out, "\nCleaned %d of %d instance(s).\n", cleaned, len(targets))
	return exit
}

// cleanTargets resolves the set of instance names to operate on.
func cleanTargets(opts *CleanOptions) ([]string, error) {
	if opts.All {
		idx, err := registry.ReadIndex()
		if err != nil {
			return nil, fmt.Errorf("read registry index: %w", err)
		}
		names := make([]string, 0, len(idx.Entries))
		for _, e := range idx.Entries {
			names = append(names, e.Name)
		}
		sort.Strings(names)
		return names, nil
	}
	if opts.Name == "" {
		return nil, fmt.Errorf("provide --name <instance> or --all")
	}
	if err := ValidateName(opts.Name); err != nil {
		return nil, err
	}
	return []string{opts.Name}, nil
}

// cleanOne cleans a single instance under its per-instance lock.
func cleanOne(ctx context.Context, out io.Writer, errw io.Writer, opts *CleanOptions, name string) int {
	indexed, err := cleanIndexHasEntry(name)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "%s: read registry index: %s\n", name, err)
		return ExitRuntimeFailure
	}

	if opts.DryRun {
		return dryRunCleanOne(out, errw, opts, name, indexed)
	}

	if !indexed {
		_, _ = fmt.Fprintf(out, "No instance named %q is registered. Nothing to clean.\n", name)
		return ExitSuccess
	}

	release, err := registry.Lock(name)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "%s: %s\n", name, err)
		return ExitUserError
	}
	defer release()

	state, err := registry.Read(name)
	if err == registry.ErrNotFound {
		// Orphan: index row present but state.json gone (crashed up,
		// manual rm, etc.). Scrub the index idempotently.
		if delErr := registry.Delete(name); delErr != nil {
			_, _ = fmt.Fprintf(errw, "%s: scrub orphan entry: %s\n", name, delErr)
			return ExitRuntimeFailure
		}
		_, _ = fmt.Fprintf(out, "Scrubbed orphan registry entry %q.\n", name)
		return ExitSuccess
	}
	if err != nil {
		_, _ = fmt.Fprintf(errw, "%s: read state: %s\n", name, err)
		return ExitRuntimeFailure
	}

	running := state.Status == registry.StatusRunning
	if running && !opts.Force {
		_, _ = fmt.Fprintf(errw,
			"%s is running — refusing to clean. Run `localnet down --name %s` first, "+
				"or pass --force to tear it down and clean in one step.\n",
			name, name)
		return ExitUserError
	}

	// Best-effort teardown of any lingering docker resources. Even a
	// `stopped` instance can leave volumes behind if it was stopped
	// without --volumes; clean's contract is "reclaim everything",
	// so we always attempt compose down --volumes here. Failures are
	// non-fatal for a non-running instance (the containers may
	// already be gone) but fatal-ish for a running --force teardown.
	env, envFiles, ctxErr := composeContext(state)
	if ctxErr != nil {
		_, _ = fmt.Fprintf(errw, "%s: Warning: could not reconstruct compose context: %s\n", name, ctxErr)
	}
	var runner composeDowner
	if opts.NewRunner != nil {
		runner = opts.NewRunner(state.ComposeProject, state.ComposeFiles, envFiles, env, state.ProjectDir, out)
	} else {
		runner = &docker.ComposeRunner{
			ProjectName:  state.ComposeProject,
			ComposeFiles: state.ComposeFiles,
			EnvFiles:     envFiles,
			Env:          env,
			WorkDir:      state.ProjectDir,
			LogWriter:    out,
			// Replay the up-time --profile set so the teardown reaches
			// every profile-gated Splice service (see down.go).
			Profiles: composeProfiles(state),
		}
	}
	// removeVolumes=true: `clean` is the destructive verb — it reclaims
	// the named ledger volumes (unlike `down`, which preserves them).
	if derr := runner.Stop(ctx, true); derr != nil {
		if running {
			// We were asked to --force a running instance and the
			// teardown failed — do NOT scrub state, or we'd orphan
			// live containers. Surface and bail.
			_, _ = fmt.Fprintf(errw,
				"%s: docker compose down failed during --force clean: %s\n"+
					"Registry state preserved; retry once the host is healthy.\n",
				name, derr)
			return ExitRuntimeFailure
		}
		// Non-running instance: the down failure is usually "no such
		// project" because containers are already gone. Warn and
		// proceed to scrub state anyway — that's the whole point of
		// clean for an orphaned/stopped instance.
		_, _ = fmt.Fprintf(errw,
			"%s: docker compose down reported an error (continuing — "+
				"containers may already be gone): %s\n", name, derr)
	}

	if err := registry.Delete(name); err != nil {
		_, _ = fmt.Fprintf(errw, "%s: remove instance state: %s\n", name, err)
		return ExitRuntimeFailure
	}

	// Deregister from the shared observability stack (#39) and tear it
	// down if this was the last instance referencing it — atomically under
	// the shared-stack lock so a concurrent `up` can't race the teardown.
	_ = DeregisterInstanceAndTeardownIfIdle(ctx, name, io.Discard)

	_, _ = fmt.Fprintf(out, "Cleaned %q.\n", name)
	return ExitSuccess
}

func cleanIndexHasEntry(name string) (bool, error) {
	idx, err := registry.ReadIndex()
	if err != nil {
		return false, err
	}
	for _, e := range idx.Entries {
		if e.Name == name {
			return true, nil
		}
	}
	return false, nil
}

func dryRunCleanOne(out io.Writer, errw io.Writer, opts *CleanOptions, name string, indexed bool) int {
	if !indexed {
		_, _ = fmt.Fprintf(out, "No instance named %q is registered. Nothing to clean.\n", name)
		return ExitSuccess
	}

	state, err := registry.Read(name)
	if err == registry.ErrNotFound {
		_, _ = fmt.Fprintf(out, "would scrub orphan registry entry %q (state.json missing)\n", name)
		return ExitSuccess
	}
	if err != nil {
		_, _ = fmt.Fprintf(errw, "%s: read state: %s\n", name, err)
		return ExitRuntimeFailure
	}

	running := state.Status == registry.StatusRunning
	if running && !opts.Force {
		_, _ = fmt.Fprintf(errw,
			"%s is running — refusing to clean. Run `localnet down --name %s` first, "+
				"or pass --force to tear it down and clean in one step.\n",
			name, name)
		return ExitUserError
	}

	extra := ""
	if running {
		extra = " (running — would be torn down via --force)"
	}
	_, _ = fmt.Fprintf(out,
		"would remove %q%s: state.json + data dir %s + docker resources for project %s\n",
		name, extra, state.DataDir, state.ComposeProject)
	return ExitSuccess
}
