package localnet

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/spf13/cobra"
)

func assertNoBarePrefix(t *testing.T, got string) {
	t.Helper()
	if strings.Contains(got, "`localnet ") || strings.Contains(got, "'localnet ") {
		t.Errorf("output has a prefix-less localnet hint:\n%s", got)
	}
}

func TestCLIHints_RunDownFollowsPrefix(t *testing.T) {
	forEachPrefix(t, func(t *testing.T, prefix, other string) {
		t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
		seedDownInstance(t, "demo", registry.StatusRunning)
		installFakeStopper(t, func(context.Context, *registry.State) error { return nil })

		var out, errBuf bytes.Buffer
		if code := RunDown(context.Background(), &out, &errBuf, DownOptions{Name: "demo"}); code != localnet.ExitSuccess {
			t.Fatalf("down = %d, stderr=%q", code, errBuf.String())
		}
		for _, want := range []string{prefix + " down demo", prefix + " up demo", prefix + " remove demo"} {
			assertNamesPrefix(t, out.String(), want, other)
		}
		assertNoBarePrefix(t, out.String())

		seedDownInstance(t, "flaky", registry.StatusRunning)
		installFakeStopper(t, func(context.Context, *registry.State) error { return errors.New("daemon gone") })
		out.Reset()
		errBuf.Reset()
		if code := RunDown(context.Background(), &out, &errBuf, DownOptions{Name: "flaky"}); code != localnet.ExitRuntimeFailure {
			t.Fatalf("failing down = %d, want ExitRuntimeFailure", code)
		}
		assertNamesPrefix(t, errBuf.String(), "Re-run `"+prefix+" down flaky` once the host is healthy.", other)
		assertNoBarePrefix(t, errBuf.String())
	})
}

func TestCLIHints_NameRequiredFollowsPrefix(t *testing.T) {
	run := func(t *testing.T, cmd *cobra.Command, args ...string) string {
		t.Helper()
		var out, errBuf bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&errBuf)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("%s %v without a name must fail", cmd.Name(), args)
		}
		return errBuf.String()
	}

	forEachPrefix(t, func(t *testing.T, prefix, other string) {
		t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())

		up := &cobra.Command{Use: "up [name]"}
		up.Flags().String("name", "", "")
		_, err := resolveName(up, nil)
		if err == nil {
			t.Fatal("resolveName without a name must fail")
		}
		want := "instance name required: `" + prefix + " up <name>` or `" + prefix + " up --name <name>`"
		if err.Error() != want {
			t.Errorf("resolveName error = %q, want %q", err.Error(), want)
		}

		got := run(t, buildSnapshot(), "--to", "snap.tgz")
		assertNamesPrefix(t, got,
			"`"+prefix+" snapshot <name> --to <to>` or `"+prefix+" snapshot --name <name> --to <to>`", other)

		got = run(t, buildRestore(), "--from", "snap.tgz")
		assertNamesPrefix(t, got,
			"`"+prefix+" restore <name> --from <from>` or `"+prefix+" restore --name <name> --from <from>`", other)

		got = run(t, buildDown())
		assertNamesPrefix(t, got, "`"+prefix+" down <name>` or `"+prefix+" down --name <name>`", other)

		got = run(t, buildRemove())
		assertNamesPrefix(t, got, "instance name required: `"+prefix+" remove <name>` (or --name)", other)
		assertNoBarePrefix(t, got)
	})
}

func TestCLIHints_RemoveRunningNonInteractiveFollowsPrefix(t *testing.T) {
	forEachPrefix(t, func(t *testing.T, prefix, other string) {
		t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
		seedDownInstance(t, "live", registry.StatusRunning)

		pipeR, pipeW, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = pipeR.Close(); _ = pipeW.Close() }()

		cmd := buildRemove()
		var out, errBuf bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&errBuf)
		cmd.SetIn(pipeR)
		cmd.SetArgs([]string{"live"})
		if err := cmd.ExecuteContext(context.Background()); err == nil {
			t.Fatal("remove of a running instance without a terminal must fail")
		}
		assertNamesPrefix(t, errBuf.String(), "Run `"+prefix+" down live` first, or pass --force", other)
		assertNoBarePrefix(t, errBuf.String())
		if _, err := registry.Read("live"); err != nil {
			t.Errorf("refused remove must keep the instance, got %v", err)
		}
	})
}

func TestCLIHints_HelpTextFollowsPrefix(t *testing.T) {
	forEachPrefix(t, func(t *testing.T, prefix, other string) {
		for _, tc := range []struct {
			cmd  *cobra.Command
			want []string
		}{
			{buildStart(), []string{"use '" + prefix + " resume' instead", "Use '" + prefix + " up' directly"}},
			{buildStop(), []string{"'" + prefix + " start'", "'" + prefix + " down'", "'" + prefix + " pause'"}},
			{buildDoctor(), []string{"Runs the same preflight checks `" + prefix + " up` runs before"}},
			{buildSkills(), []string{"describing safe `" + prefix + "` workflows"}},
			{buildRestore(), []string{"run '" + prefix + " down X' first", "run '" + prefix + " up X' to start"}},
		} {
			for _, w := range tc.want {
				assertNamesPrefix(t, tc.cmd.Long, w, other)
			}
			assertNoBarePrefix(t, tc.cmd.Long)
		}
	})
}
