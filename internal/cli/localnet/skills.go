package localnet

import (
	"encoding/json"
	"fmt"

	api "github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/skills"
	"github.com/spf13/cobra"
)

// buildSkills wires `dpm localnet skills <list|install>` — .
//
// Ships editor-agnostic AI-agent skill docs (safe `dpm localnet`
// workflows) and installs them into an agent's skills directory. The
// same embedded docs back the Web UI Agent Skills screen ,
// so the two surfaces never drift.
func buildSkills() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "skills",
		Short:         "List and install bundled agent skills",
		Long:          "Skill documents for common `dpm localnet` workflows. Install into ~/.claude/skills or ~/.codex/skills.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(buildSkillsList())
	cmd.AddCommand(buildSkillsInstall())
	return cmd
}

func buildSkillsList() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:           "list",
		Short:         "List the bundled skill documents",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := localnet.ValidateFormat(format, "text", "json"); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			list, err := skills.List()
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}
			out := cmd.OutOrStdout()
			if format == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				_ = enc.Encode(api.SkillsListResponse{
					SchemaVersion: api.SchemaVersion,
					Skills:        list,
				})
				return nil
			}
			_, _ = fmt.Fprintf(out, "%d skill document(s):\n\n", len(list))
			for _, s := range list {
				_, _ = fmt.Fprintf(out, "  %-22s %s\n", s.Name, s.Filename)
				if s.Description != "" {
					_, _ = fmt.Fprintf(out, "    %s\n", s.Description)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "text", "output format: text or json")
	return cmd
}

func buildSkillsInstall() *cobra.Command {
	var (
		target string
		dir    string
		force  bool
	)
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install bundled skills into an agent skills directory",
		Long: `Copy bundled skill documents into a skills directory.
Defaults to ~/.claude/skills; use --target codex for ~/.codex/skills,
or --dir for an explicit path. Each skill lands in <name>/SKILL.md.

Existing SKILL.md files with different content are left alone unless
--force is passed.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dest, err := skills.TargetDir(skills.Target(target), dir)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitUserError)
			}
			res, err := skills.Install(dest, force)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				return localnet.AsExitError(localnet.ExitRuntimeFailure)
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Installed %d skill(s) into %s:\n", len(res.Written), dest)
			for _, p := range res.Written {
				_, _ = fmt.Fprintf(out, "  %s\n", p)
			}
			if len(res.Skipped) > 0 {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"\nSkipped %d locally-modified skill(s) (re-run with --force to overwrite):\n",
					len(res.Skipped))
				for _, p := range res.Skipped {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  %s\n", p)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", "claude", "agent target: claude or codex")
	cmd.Flags().StringVar(&dir, "dir", "", "explicit install directory (overrides --target)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite skill files that have local modifications")
	return cmd
}
