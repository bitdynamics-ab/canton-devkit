package packaging

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v3"
)

func bashTool(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("release scripts run on Unix build hosts")
	}
	p, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReleaseMetadata(t *testing.T) {
	for _, tc := range []struct {
		ref, version, prerelease, campaign, channel, push string
	}{
		{"refs/tags/v0.18.2", "v0.18.2", "false", "off", "stable", "true"},
		{"refs/tags/v0.18.2-cctools.1", "v0.18.2-cctools.1", "true", "on", "dev", "true"},
		{"refs/tags/v0.18.2-rc.1", "v0.18.2-rc.1", "true", "off", "dev", "true"},
		{"refs/heads/campaign", "dev-abcdef1", "true", "off", "dev", "false"},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			out, err := exec.Command(bashTool(t), "../scripts/release-metadata.sh", tc.ref, "abcdef1234567890").CombinedOutput()
			if err != nil {
				t.Fatalf("metadata: %v: %s", err, out)
			}
			for key, want := range map[string]string{
				"devkit_version": tc.version, "prerelease": tc.prerelease,
				"campaign": tc.campaign, "channel": tc.channel, "push": tc.push,
			} {
				if !strings.Contains(string(out), key+"="+want+"\n") {
					t.Errorf("missing %s=%s in %s", key, want, out)
				}
			}
		})
	}
	for _, tag := range []string{"v0.18", "v01.18.2", "v0.18.2-cctools", "v0.18.2-cctools.0", "v0.18.2-cctools.01", "v0.18.2-cctools.1-extra", "v0.18.2;echo bad"} {
		if out, err := exec.Command(bashTool(t), "../scripts/release-metadata.sh", "refs/tags/"+tag, "abcdef1").CombinedOutput(); err == nil {
			t.Errorf("accepted invalid tag %q: %s", tag, out)
		}
	}
	if err := exec.Command(bashTool(t), "../scripts/release-metadata.sh", "refs/tags/v0.18.2", "invalid-sha").Run(); err == nil {
		t.Error("accepted invalid commit identity")
	}
}

func writeReleaseFixture(t *testing.T, root, name, content string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func releaseFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, script := range []string{"build-release.sh", "release-metadata.sh"} {
		b, err := os.ReadFile("../scripts/" + script)
		if err != nil {
			t.Fatal(err)
		}
		writeReleaseFixture(t, root, "scripts/"+script, string(b), 0o644)
	}
	writeReleaseFixture(t, root, "internal/ui/dist/index.html", `<script src="/assets/app.js"></script>`, 0o644)
	writeReleaseFixture(t, root, "internal/ui/dist/assets/app.js", "console.log('bundle')", 0o644)
	writeReleaseFixture(t, root, "README.md", "regular install instructions", 0o644)
	writeReleaseFixture(t, root, "docs/cctools-campaign.md", "campaign install instructions", 0o644)
	writeReleaseFixture(t, root, "LICENSE", "license", 0o644)
	// Capture compiler flags in place of the binary, without cross-compiling
	// the full application in a packaging unit test.
	writeReleaseFixture(t, root, "tools/go", `#!/bin/sh
args="$*"
while [ "$#" -gt 0 ]; do
  if [ "$1" = -o ]; then
    printf '%s\n' "$args" > "$2"
    exit 0
  fi
  shift
done
exit 1
`, 0o755)
	return root
}

func runReleaseFixture(t *testing.T, root, version string) ([]byte, error) {
	t.Helper()
	cmd := exec.Command(bashTool(t), "scripts/build-release.sh")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "VERSION="+version, "COMMIT=abcdef1234567890",
		"OUTPUT_DIR=dist", "COMPONENT_BIN_DIR=component-bin", "TELEMETRY_ENDPOINT=",
		"RELEASE_TARGETS=linux/amd64 darwin/arm64 windows/amd64", "PATH="+filepath.Join(root, "tools")+string(os.PathListSeparator)+os.Getenv("PATH"))
	return cmd.CombinedOutput()
}

func TestReleasePackagesCampaignAndStable(t *testing.T) {
	for _, tc := range []struct{ version, campaign, readme string }{
		{"v0.18.2-cctools.1", "on", "campaign install instructions"},
		{"v0.18.2", "off", "regular install instructions"},
		{"dev-abcdef1", "off", "regular install instructions"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			root := releaseFixture(t)
			if out, err := runReleaseFixture(t, root, tc.version); err != nil {
				t.Fatalf("build: %v: %s", err, out)
			}
			for _, platform := range []string{"linux_amd64", "darwin_arm64", "windows_amd64"} {
				binary, suffix := "canton-devkit", ".tar.gz"
				if platform == "windows_amd64" {
					binary, suffix = "canton-devkit.exe", ".zip"
				}
				path := filepath.Join(root, "dist", "canton-devkit_"+tc.version+"_"+platform+suffix)
				files := readReleaseArchive(t, path)
				built, err := os.ReadFile(filepath.Join(root, "component-bin", platform, binary))
				if err != nil {
					t.Fatal(err)
				}
				if files[binary] != string(built) || len(built) == 0 {
					t.Error("standalone and component binaries differ")
				}
				for _, flag := range []string{"main.version=" + tc.version, "main.commit=abcdef1", "internal/campaign.enabled=" + tc.campaign} {
					if !strings.Contains(string(built), flag) {
						t.Errorf("missing build flag %s", flag)
					}
				}
				if files["README.md"] != tc.readme || files["LICENSE"] != "license" {
					t.Error("wrong archive documentation")
				}
			}
			sums, err := os.ReadFile(filepath.Join(root, "dist", "SHA256SUMS"))
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(sums)), "\n")
			if len(lines) != 4 {
				t.Fatalf("want checksums for 3 archives and provenance, got %d", len(lines))
			}
			for _, line := range lines {
				fields := strings.Fields(line)
				if len(fields) != 2 {
					t.Fatalf("invalid checksum line: %s", line)
				}
				b, err := os.ReadFile(filepath.Join(root, "dist", fields[1]))
				if err != nil || fmt.Sprintf("%x", sha256.Sum256(b)) != fields[0] {
					t.Errorf("checksum mismatch: %s (%v)", fields[1], err)
				}
			}
		})
	}
}

func readReleaseArchive(t *testing.T, path string) map[string]string {
	t.Helper()
	files := map[string]string{}
	if strings.HasSuffix(path, ".zip") {
		z, err := zip.OpenReader(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = z.Close() }()
		for _, f := range z.File {
			r, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			b, err := io.ReadAll(r)
			_ = r.Close()
			if err != nil {
				t.Fatal(err)
			}
			files[f.Name] = string(b)
		}
		return files
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gz.Close() }()
	r := tar.NewReader(gz)
	for {
		h, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		files[h.Name] = string(b)
	}
	return files
}

func TestReleaseRefusesPlaceholderFrontend(t *testing.T) {
	root := releaseFixture(t)
	writeReleaseFixture(t, root, "internal/ui/dist/index.html", "DEVKIT_FRONTEND_PLACEHOLDER", 0o644)
	if out, err := runReleaseFixture(t, root, "v0.18.2-cctools.1"); err == nil || !strings.Contains(string(out), "Build the frontend first") {
		t.Fatalf("placeholder was not rejected: %v: %s", err, out)
	}
}

func TestReleaseWorkflowProtectsStableChannels(t *testing.T) {
	b, err := os.ReadFile("../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name, If, Run string
				With          map[string]string
			}
		}
	}
	if err := yaml.Unmarshal(b, &workflow); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, step := range workflow.Jobs["release"].Steps {
		seen[step.Name] = true
		switch step.Name {
		case "Create GitHub release":
			if step.With["prerelease"] != "${{ steps.meta.outputs.prerelease == 'true' }}" || step.With["make_latest"] != "${{ steps.meta.outputs.prerelease == 'true' && 'false' || 'legacy' }}" {
				t.Error("prereleases must not become the regular latest release")
			}
		case "Update Homebrew formula in public builds repo":
			if !strings.Contains(step.If, "steps.meta.outputs.prerelease == 'false'") {
				t.Error("Homebrew update must exclude prereleases")
			}
		case "Publish DPM component (tag pushes only)":
			if !strings.Contains(step.Run, "*-*)") || !strings.Contains(step.Run, "extra_tags+=(--extra-tags latest)") {
				t.Error("DPM latest must remain restricted to final releases")
			}
		}
	}
	for _, name := range []string{"Test Go packages", "Lint Go packages", "Verify packaged campaign identity", "Create GitHub release", "Update Homebrew formula in public builds repo", "Publish DPM component (tag pushes only)"} {
		if !seen[name] {
			t.Errorf("missing release gate: %s", name)
		}
	}
}
