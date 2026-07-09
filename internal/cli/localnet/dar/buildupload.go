package dar

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	adminproto "github.com/bitdynamics-ab/canton-devkit/internal/canton/admin/proto"
	cdkdar "github.com/bitdynamics-ab/canton-devkit/internal/dar"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/spf13/cobra"
)

// dar build-upload — shell out to `dpm build` (or `daml build`), then
// upload the resulting DAR. Compilation is deliberately delegated to
// the user's toolchain; DevKit never reimplements damlc / dpm.
func buildBuildUpload(viaDPM bool) *cobra.Command {
	var (
		conn     connectFlags
		project  string
		builder  string
		vet      bool
		dry      bool
		printDAR bool
	)
	cmd := &cobra.Command{
		Use:   "build-upload",
		Short: "Build a Daml project (via dpm or daml) and upload the DAR",
		Long: `Convenience shortcut: invoke the user's installed build tool
(dpm build or daml build) on a Daml project, then upload the resulting
DAR to the targeted participant.

DevKit never reimplements compilation. If neither dpm nor daml is on
PATH, the command exits 1 with a clear message.

Auto-discovery:
  --builder auto  (default) — prefers dpm, falls back to daml
  --builder dpm   — force dpm
  --builder daml  — force daml (legacy)

Exit codes:
  0  Built and uploaded
  1  Build tool missing, invalid args, or build itself failed
  4  Upload RPC failed`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			proj := project
			if proj == "" {
				wd, err := os.Getwd()
				if err != nil {
					return localnet.AsExitError(localnet.ExitUserError)
				}
				proj = wd
			}
			if _, err := os.Stat(filepath.Join(proj, "daml.yaml")); err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"dar build-upload: no daml.yaml found in %s — pass --project to point at a Daml project root\n", proj)
				return localnet.AsExitError(localnet.ExitUserError)
			}

			tool, err := pickBuilder(builder)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Building %s with %s...\n", proj, tool)
			darPath, err := runBuild(tool, proj, cmd.ErrOrStderr())
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "dar build-upload: build failed: %s\n", err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Built: %s\n", darPath)

			if printDAR {
				return nil
			}

			data, err := cdkdar.ReadDARFile(darPath)
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "dar build-upload: read %s: %s\n", darPath, err)
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}

			client, err := conn.connect(cmd.Context())
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "dar build-upload: %s\n", err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			defer func() { _ = client.Close() }()

			if dry {
				resp, err := client.Package.ValidateDar(cmd.Context(), &adminproto.ValidateDarRequest{
					Data: data, Filename: filepath.Base(darPath),
				})
				if err != nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "dar build-upload (dry run): %s\n", err)
					return localnet.AsExitError(localnet.ExitRuntimeFailure)
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Validated (dry run): %s\n", resp.GetMainPackageId())
				return nil
			}

			resp, err := client.Package.UploadDar(cmd.Context(), &adminproto.UploadDarRequest{
				Dars:           []*adminproto.UploadDarRequest_UploadDarData{{Bytes: data}},
				VetAllPackages: vet,
			})
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "dar build-upload: upload failed: %s\n", err)
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}
			for _, id := range resp.GetDarIds() {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "uploaded: %s\n", id)
			}
			return nil
		},
	}
	conn.register(cmd)
	cmd.Flags().StringVar(&project, "project", "", "Path to the Daml project (the dir containing daml.yaml). Defaults to cwd.")
	// Under `dpm localnet …` the working directory is always the Daml
	// project root, so pointing elsewhere doesn't apply — hide the flag
	// (still accepted, still defaults to cwd) to keep DPM help focused.
	if viaDPM {
		_ = cmd.Flags().MarkHidden("project")
	}
	cmd.Flags().StringVar(&builder, "builder", "auto", "Build tool: auto (prefers dpm), dpm, or daml.")
	cmd.Flags().BoolVar(&vet, "vet", true, "Vet packages after upload.")
	cmd.Flags().BoolVar(&dry, "dry-run", false, "Build but validate instead of upload.")
	cmd.Flags().BoolVar(&printDAR, "build-only", false, "Just build and print the DAR path; skip upload.")
	return cmd
}

// pickBuilder returns the path of the build tool to use, honouring the
// --builder flag. "auto" prefers dpm, falling back to daml.
func pickBuilder(pref string) (string, error) {
	switch pref {
	case "auto":
		if p, err := exec.LookPath("dpm"); err == nil {
			return p, nil
		}
		if p, err := exec.LookPath("daml"); err == nil {
			return p, nil
		}
		return "", fmt.Errorf("neither `dpm` nor `daml` was found on PATH — install the Daml/DPM toolchain or pass --build-only false with a pre-built DAR")
	case "dpm":
		p, err := exec.LookPath("dpm")
		if err != nil {
			return "", fmt.Errorf("`dpm` not on PATH (was forced via --builder dpm)")
		}
		return p, nil
	case "daml":
		p, err := exec.LookPath("daml")
		if err != nil {
			return "", fmt.Errorf("`daml` not on PATH (was forced via --builder daml)")
		}
		return p, nil
	}
	return "", fmt.Errorf("--builder must be one of: auto, dpm, daml (got %q)", pref)
}

// runBuild invokes the build tool and returns the path of the
// resulting .dar. Both dpm and daml put the DAR in .daml/dist/ by
// convention; we glob for the freshest .dar there after the build
// finishes.
func runBuild(tool, project string, stderr io.Writer) (string, error) {
	c := exec.Command(tool, "build")
	c.Dir = project
	var stderrBuf bytes.Buffer
	c.Stderr = &stderrBuf
	if err := c.Run(); err != nil {
		// Surface the build tool's stderr so the user sees what failed.
		if stderrBuf.Len() > 0 {
			_, _ = stderr.Write(stderrBuf.Bytes())
		}
		return "", err
	}

	// Find the DAR in .daml/dist/. Either tool produces exactly one.
	matches, _ := filepath.Glob(filepath.Join(project, ".daml", "dist", "*.dar"))
	if len(matches) == 0 {
		return "", fmt.Errorf("no DAR produced in %s/.daml/dist/", project)
	}
	if len(matches) > 1 {
		// Pick the newest by mtime.
		newest := matches[0]
		newestStat, _ := os.Stat(newest)
		for _, m := range matches[1:] {
			st, _ := os.Stat(m)
			if newestStat == nil || (st != nil && st.ModTime().After(newestStat.ModTime())) {
				newest = m
				newestStat = st
			}
		}
		return newest, nil
	}
	return strings.TrimSpace(matches[0]), nil
}
