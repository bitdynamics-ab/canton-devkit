package localnet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteContainerRenameOverlay(t *testing.T) {
	tmp := t.TempDir()
	path, err := WriteContainerRenameOverlay(tmp, "alice-")
	if err != nil {
		t.Fatalf("WriteContainerRenameOverlay: %v", err)
	}
	if filepath.Base(path) != "containers.yaml" {
		t.Errorf("unexpected filename: %s", path)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)

	// Every known container must appear with the prefix applied.
	for _, svc := range splice610Containers {
		want := "container_name: alice-" + svc
		if !strings.Contains(s, want) {
			t.Errorf("overlay missing %q\n---\n%s\n---", want, s)
		}
	}

	// Service block must precede each container_name (basic YAML shape).
	if !strings.Contains(s, "services:\n") {
		t.Errorf("overlay missing `services:` header")
	}
}

func TestWriteContainerRenameOverlay_RejectsEmptyPrefix(t *testing.T) {
	tmp := t.TempDir()
	_, err := WriteContainerRenameOverlay(tmp, "")
	if err == nil {
		t.Error("expected error for empty prefix")
	}
}

func TestWriteContainerRenameOverlay_DeterministicOrder(t *testing.T) {
	a, _ := WriteContainerRenameOverlay(t.TempDir(), "alice-")
	b, _ := WriteContainerRenameOverlay(t.TempDir(), "alice-")

	ba, _ := os.ReadFile(a)
	bb, _ := os.ReadFile(b)
	if string(ba) != string(bb) {
		t.Errorf("overlay output is non-deterministic — needed for diff-stable inspection")
	}
}

// TestWriteContainerRenameOverlay_RelaxesNginxSpliceDep locks the fix
// for the dep-health-timeout bug: nginx's depends_on:splice condition
// must be downgraded to service_started. Without this, `docker compose
// up -d` blocks on splice's healthcheck and times out before Splice's
// ~10-min onboarding completes.
// --------------- loopback-ports overlay tests ---------------

func TestWriteLoopbackPortsOverlay(t *testing.T) {
	tmp := t.TempDir()
	path, err := WriteLoopbackPortsOverlay(tmp)
	if err != nil {
		t.Fatalf("WriteLoopbackPortsOverlay: %v", err)
	}
	if filepath.Base(path) != "loopback-ports.yaml" {
		t.Errorf("unexpected filename: %s", path)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)

	// Every port binding must include 127.0.0.1.
	if !strings.Contains(s, "services:\n") {
		t.Errorf("overlay missing `services:` header")
	}

	// Every service in the table must appear.
	for _, svc := range loopbackPortTable() {
		svcHeader := "  " + svc.Name + ":\n"
		if !strings.Contains(s, svcHeader) {
			t.Errorf("overlay missing service %q\n---\n%s\n---", svc.Name, s)
		}
		for _, p := range svc.Ports {
			want := fmt.Sprintf("127.0.0.1:%s:%s", p.HostExpr, p.ContainerExpr)
			if !strings.Contains(s, want) {
				t.Errorf("overlay missing port binding %q\n---\n%s\n---", want, s)
			}
		}
	}

	// Must use !override to replace (not append) the ports list.
	if !strings.Contains(s, "ports: !override") {
		t.Errorf("overlay must use `ports: !override` to replace upstream port lists\n---\n%s\n---", s)
	}

	// Must NOT contain 0.0.0.0 anywhere.
	if strings.Contains(s, "0.0.0.0") {
		t.Errorf("overlay must not contain 0.0.0.0\n---\n%s\n---", s)
	}
}

func TestWriteLoopbackPortsOverlay_NginxUsesEnvBackedContainerPorts(t *testing.T) {
	path, err := WriteLoopbackPortsOverlay(t.TempDir())
	if err != nil {
		t.Fatalf("WriteLoopbackPortsOverlay: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)

	for _, env := range []string{"APP_USER_UI_PORT", "APP_PROVIDER_UI_PORT", "SV_UI_PORT"} {
		want := fmt.Sprintf("127.0.0.1:${%s}:${%s}", env, env)
		if !strings.Contains(s, want) {
			t.Errorf("nginx UI binding must keep host and container ports aligned; missing %q\n---\n%s\n---", want, s)
		}
	}
	for _, stale := range []string{
		"127.0.0.1:${APP_USER_UI_PORT}:2000",
		"127.0.0.1:${APP_PROVIDER_UI_PORT}:3000",
		"127.0.0.1:${SV_UI_PORT}:4000",
	} {
		if strings.Contains(s, stale) {
			t.Errorf("nginx UI binding still contains stale fixed container port %q\n---\n%s\n---", stale, s)
		}
	}
}

func TestWriteLoopbackPortsOverlay_DeterministicOrder(t *testing.T) {
	a, _ := WriteLoopbackPortsOverlay(t.TempDir())
	b, _ := WriteLoopbackPortsOverlay(t.TempDir())

	ba, _ := os.ReadFile(a)
	bb, _ := os.ReadFile(b)
	if string(ba) != string(bb) {
		t.Errorf("overlay output is non-deterministic — needed for diff-stable inspection")
	}
}

// TestWriteLoopbackPortsOverlay_CoversAllCantonPorts ensures the
// overlay covers every port in CantonPortInternal. If Splice adds a
// new participant port, this test catches the omission.
func TestWriteLoopbackPortsOverlay_CoversAllCantonPorts(t *testing.T) {
	path, err := WriteLoopbackPortsOverlay(t.TempDir())
	if err != nil {
		t.Fatalf("WriteLoopbackPortsOverlay: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for key, port := range CantonPortInternal {
		want := fmt.Sprintf("127.0.0.1:0:%d", port)
		if !strings.Contains(s, want) {
			t.Errorf("overlay missing canton port %d (key %q)", port, key)
		}
	}
}

// TestWriteLoopbackPortsOverlay_CoversAllSplicePorts ensures the
// overlay covers all splice internal ports.
func TestWriteLoopbackPortsOverlay_CoversAllSplicePorts(t *testing.T) {
	path, err := WriteLoopbackPortsOverlay(t.TempDir())
	if err != nil {
		t.Fatalf("WriteLoopbackPortsOverlay: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, port := range SplicePortInternal {
		want := fmt.Sprintf("127.0.0.1:0:%d", port)
		if !strings.Contains(s, want) {
			t.Errorf("overlay missing splice port %d", port)
		}
	}
}

// --------------- container-rename overlay tests (continued) ---------------

func TestWriteContainerRenameOverlay_RelaxesNginxSpliceDep(t *testing.T) {
	path, err := WriteContainerRenameOverlay(t.TempDir(), "alice-")
	if err != nil {
		t.Fatalf("WriteContainerRenameOverlay: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)

	// Must contain the relaxation block under nginx.
	for _, want := range []string{
		"  nginx:",
		"    depends_on:",
		"      splice:",
		"        condition: service_started",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("overlay missing %q\n---\n%s", want, src)
		}
	}

	// Must NOT carry a service_healthy condition under nginx — that's
	// what we're explicitly overriding.
	if strings.Contains(src, "        condition: service_healthy") {
		t.Errorf("overlay still has service_healthy condition; the relaxation didn't take\n%s", src)
	}
}

func TestWriteNginxVhostOverlay(t *testing.T) {
	tmp := t.TempDir()
	const instance = "localnet-2"
	path, err := WriteNginxVhostOverlay(tmp, instance, nil)
	if err != nil {
		t.Fatalf("WriteNginxVhostOverlay: %v", err)
	}
	if filepath.Base(path) != "nginx-vhosts.yaml" {
		t.Errorf("unexpected filename: %s", path)
	}

	// The three role .conf files must be materialized. Their server_name
	// directives are ${VHOST_*} env vars (expanded by envsubst at nginx
	// boot), so the materialized copies carry the placeholders, not the
	// resolved hostnames. The wallet block is role-scoped, so each conf
	// carries its own ${VHOST_WALLET_<ROLE>} var. The flat Splice names
	// must be gone.
	walletVarByConf := map[string]string{
		"app-provider.conf": "${VHOST_WALLET_APP_PROVIDER}",
		"app-user.conf":     "${VHOST_WALLET_APP_USER}",
		"sv.conf":           "${VHOST_WALLET_SV}",
	}
	for _, name := range nginxVhostConfNames {
		confPath := filepath.Join(tmp, "nginx", name)
		body, rerr := os.ReadFile(confPath)
		if rerr != nil {
			t.Fatalf("missing materialized %s: %v", name, rerr)
		}
		s := string(body)
		walletVar := walletVarByConf[name]
		if !strings.Contains(s, "server_name "+walletVar+";") {
			t.Errorf("%s: wallet block missing %s server_name\n---\n%s", name, walletVar, s)
		}
		// Drop the flat wallet.localhost name entirely (comments aside).
		for _, line := range strings.Split(s, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				continue
			}
			if strings.Contains(trimmed, "wallet.localhost") {
				t.Errorf("%s: flat wallet.localhost still present in a directive: %q", name, trimmed)
			}
		}
	}

	// The sv catch-all stays the bare `_`.
	svBody, err := os.ReadFile(filepath.Join(tmp, "nginx", "sv.conf"))
	if err != nil {
		t.Fatal(err)
	}
	sawCatchAll := false
	for _, line := range strings.Split(string(svBody), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if trimmed == "server_name _;" {
			sawCatchAll = true
		}
	}
	if !sawCatchAll {
		t.Errorf("sv.conf missing the `server_name _;` catch-all directive\n%s", svBody)
	}

	// The http-context tuning snippet must be materialized with the
	// bigger server-name hash bucket — nginx can't boot with the longer
	// role-scoped vhosts under the default 64-byte bucket.
	tuning, err := os.ReadFile(filepath.Join(tmp, "nginx", "00-devkit-tuning.conf"))
	if err != nil {
		t.Fatalf("missing materialized tuning conf: %v", err)
	}
	if !strings.Contains(string(tuning), "server_names_hash_bucket_size 128;") {
		t.Errorf("tuning conf missing bucket-size directive\n%s", tuning)
	}

	// The compose overlay must inject the per-instance ${VHOST_*} values
	// as nginx container env and remap the three role templates onto the
	// materialized copies under !override while leaving nginx.conf and
	// includes/ pointed at the untouched Splice cache.
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"  nginx:",
		"    environment:",
		`      VHOST_WALLET_APP_USER: "wallet.app-user.localnet-2.localhost"`,
		`      VHOST_WALLET_APP_PROVIDER: "wallet.app-provider.localnet-2.localhost"`,
		`      VHOST_WALLET_SV: "wallet.sv.localnet-2.localhost"`,
		`      VHOST_ANS_APP_USER: "ans.app-user.localnet-2.localhost"`,
		`      VHOST_ANS_APP_PROVIDER: "ans.app-provider.localnet-2.localhost"`,
		`      VHOST_JSON_LEDGER_APP_USER: "json-ledger-api.app-user.localnet-2.localhost"`,
		`      VHOST_JSON_LEDGER_APP_PROVIDER: "json-ledger-api.app-provider.localnet-2.localhost"`,
		`      VHOST_GRPC_LEDGER_APP_USER: "grpc-ledger-api.app-user.localnet-2.localhost"`,
		`      VHOST_GRPC_LEDGER_APP_PROVIDER: "grpc-ledger-api.app-provider.localnet-2.localhost"`,
		`      VHOST_SCAN: "scan.localnet-2.localhost"`,
		`      VHOST_SV: "sv.localnet-2.localhost"`,
		"    volumes: !override",
		filepath.ToSlash(filepath.Join(tmp, "nginx")) + "/app-provider.conf:/etc/nginx/templates/app-provider.c${APP_PROVIDER_PROFILE}f.template",
		filepath.ToSlash(filepath.Join(tmp, "nginx")) + "/app-user.conf:/etc/nginx/templates/app-user.c${APP_USER_PROFILE}f.template",
		filepath.ToSlash(filepath.Join(tmp, "nginx")) + "/sv.conf:/etc/nginx/templates/sv.c${SV_PROFILE}f.template",
		filepath.ToSlash(filepath.Join(tmp, "nginx")) + "/00-devkit-tuning.conf:/etc/nginx/conf.d/00-devkit-tuning.conf",
		"${LOCALNET_DIR}/conf/nginx/nginx.conf:/etc/nginx/nginx.conf",
		"${LOCALNET_DIR}/conf/nginx/swagger-ui:/etc/nginx/includes",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("overlay missing %q\n---\n%s", want, s)
		}
	}
}

func TestWriteNginxVhostOverlay_RejectsEmptyDataDir(t *testing.T) {
	if _, err := WriteNginxVhostOverlay("", "localnet-2", nil); err == nil {
		t.Error("expected error for empty dataDir")
	}
}

func TestWriteNginxVhostOverlay_RejectsEmptyInstanceName(t *testing.T) {
	if _, err := WriteNginxVhostOverlay(t.TempDir(), "", nil); err == nil {
		t.Error("expected error for empty instanceName")
	}
}

func TestWriteNginxVhostOverlay_PreservesOperatorEdits(t *testing.T) {
	tmp := t.TempDir()
	if _, err := WriteNginxVhostOverlay(tmp, "localnet-2", nil); err != nil {
		t.Fatalf("first write: %v", err)
	}
	edited := filepath.Join(tmp, "nginx", "sv.conf")
	custom := []byte("# operator edit\n")
	if err := os.WriteFile(edited, custom, 0o644); err != nil {
		t.Fatal(err)
	}
	// A second run must not clobber the operator's edit.
	if _, err := WriteNginxVhostOverlay(tmp, "localnet-2", nil); err != nil {
		t.Fatalf("second write: %v", err)
	}
	got, err := os.ReadFile(edited)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(custom) {
		t.Errorf("operator edit clobbered: got %q", got)
	}
}
