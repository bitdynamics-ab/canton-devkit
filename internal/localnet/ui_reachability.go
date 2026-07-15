package localnet

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/docker"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// UI reachability probe.
//
// A UI host port can accept TCP without serving anything: docker-proxy
// owns the host port even when the container-side mapping points at a
// port nothing listens on (e.g. an instance whose generated
// loopback-ports.yaml predates the env-templated nginx ports). That
// state is invisible to `docker compose ps` health, so status probes
// the recorded UI endpoints over HTTP directly.

// uiProbePortKeys are the state.json port keys that carry a browser UI
// behind Splice's nginx. Other endpoints are not probed: grpc and
// postgres don't speak plain HTTP, and swagger-ui is served by its own
// container with a fixed port mapping.
var uiProbePortKeys = []string{"app_user_ui", "app_provider_ui", "sv_ui"}

// uiProbeTimeout bounds each endpoint GET. Loopback either answers in
// milliseconds or never; anything slower than this is unreachable for
// status purposes.
const uiProbeTimeout = 2 * time.Second

// uiProbeFn is the test seam for the per-URL HTTP probe. nil selects
// defaultUIProbe (same pattern as statusProberFn). hostHeader, when
// non-empty, overrides the request Host — needed for the wallet UI,
// whose route only matches the instance-scoped wallet vhost (the flat
// loopback Host now falls through to a different server block).
var uiProbeFn func(ctx context.Context, rawURL, hostHeader string) error

// uiProbeClient never follows redirects: a 30x from nginx already
// proves the UI answers HTTP, and login redirects would otherwise
// multiply the probe cost.
var uiProbeClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// defaultUIProbe answers "does this URL speak HTTP at all". Any status
// code counts as reachable — a 404/502 still proves nginx is serving
// on the port. Only transport-level failures (empty reply, refused,
// timeout) count as unreachable.
func defaultUIProbe(ctx context.Context, rawURL, hostHeader string) error {
	ctx, cancel := context.WithTimeout(ctx, uiProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	if hostHeader != "" {
		req.Host = hostHeader
	}
	resp, err := uiProbeClient.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
	return nil
}

// probeUIEndpoints marks the browser-UI rows of endpoints with a
// reachability verdict. Probes run concurrently; total added latency
// is one probe timeout in the worst case. Mutates endpoints in place.
func probeUIEndpoints(ctx context.Context, s *registry.State, endpoints []types.Endpoint) {
	probe := uiProbeFn
	if probe == nil {
		probe = defaultUIProbe
	}

	uiPorts := make(map[int]bool, len(uiProbePortKeys))
	for _, k := range uiProbePortKeys {
		if p := s.Ports[k]; p > 0 {
			uiPorts[p] = true
		}
	}
	if len(uiPorts) == 0 {
		return
	}

	staleOverlay := hasStaleLoopbackOverlay(s.DataDir)

	var wg sync.WaitGroup
	for i := range endpoints {
		e := &endpoints[i]
		if e.Scheme != "http" || !uiPorts[e.Port] {
			continue
		}
		// Always dial loopback (multi-label *.localhost does NOT resolve
		// via the OS/Go resolver on macOS, so we can't dial the vhost
		// directly). Wallet UIs only answer on their instance-scoped
		// vhost, so carry it as the Host header to validate the real
		// route rather than whatever server block owns the bare Host.
		var host string
		if isWalletUIKey(e.Key) {
			host = instanceVHost(VHostServiceWallet, s.Name)
		}
		dialURL := fmt.Sprintf("http://localhost:%d", e.Port)
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := probe(ctx, dialURL, host)
			if err == nil {
				e.Reachability = types.ReachabilityOK
				return
			}
			e.Reachability = types.ReachabilityUnreachable
			e.ReachabilityDetail = uiProbeErrorDetail(err)
			if staleOverlay {
				e.ReachabilityDetail += "; stale loopback-ports.yaml from an older DevKit detected"
			}
		}()
	}
	wg.Wait()
}

// uiProbeErrorDetail humanizes transport errors. EOF — TCP accepted,
// zero HTTP bytes back — gets its own wording because "EOF" alone
// tells the user nothing.
func uiProbeErrorDetail(err error) string {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		err = uerr.Err
	}
	switch {
	case errors.Is(err, io.EOF):
		return "connection accepted but no HTTP response (empty reply)"
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Sprintf("no HTTP response within %s", uiProbeTimeout)
	default:
		return err.Error()
	}
}

// uiReachabilityCheckName intentionally contains "ports" so
// PreflightReportFromDocker files the check under the Network section.
const uiReachabilityCheckName = "UI ports reachability (running instances)"

// uiReachabilityCheck is the doctor surface of the UI probe: one GET
// per recorded browser-UI port of every running instance. Warn, not
// fail — a broken instance doesn't make the host unready for
// `localnet up`, so doctor still exits 0.
func uiReachabilityCheck(ctx context.Context) docker.CheckResult {
	idx, err := registry.ReadIndex()
	if err != nil {
		return docker.CheckResult{
			Name:   uiReachabilityCheckName,
			Status: docker.StatusSkipped,
			Detail: "instance registry unreadable: " + err.Error(),
		}
	}
	probe := uiProbeFn
	if probe == nil {
		probe = defaultUIProbe
	}

	type uiTarget struct {
		instance string
		key      string
		url      string
		host     string // Host-header override for vhost-scoped routes
		stale    bool
		detail   string // filled by the probe on failure
	}
	var targets []uiTarget
	running := 0
	for _, e := range idx.Entries {
		if e.Status != registry.StatusRunning {
			continue
		}
		s, rerr := registry.Read(e.Name)
		if rerr != nil {
			// Unreadable state files are list/status's problem to
			// surface; doctor just probes what it can read.
			continue
		}
		running++
		stale := hasStaleLoopbackOverlay(s.DataDir)
		for _, key := range uiProbePortKeys {
			if port := s.Ports[key]; port > 0 {
				// All probed UI keys are wallet UIs, which only answer
				// on their instance-scoped wallet vhost.
				host := ""
				if isWalletUIKey(key) {
					host = instanceVHost(VHostServiceWallet, e.Name)
				}
				targets = append(targets, uiTarget{
					instance: e.Name,
					key:      key,
					url:      fmt.Sprintf("http://localhost:%d", port),
					host:     host,
					stale:    stale,
				})
			}
		}
	}

	switch {
	case running == 0:
		return docker.CheckResult{
			Name:   uiReachabilityCheckName,
			Status: docker.StatusSkipped,
			Detail: "no running LocalNet instances",
		}
	case len(targets) == 0:
		return docker.CheckResult{
			Name:   uiReachabilityCheckName,
			Status: docker.StatusSkipped,
			Detail: "running instances record no UI ports",
		}
	}

	var wg sync.WaitGroup
	for i := range targets {
		t := &targets[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			if perr := probe(ctx, t.url, t.host); perr != nil {
				t.detail = uiProbeErrorDetail(perr)
			}
		}()
	}
	wg.Wait()

	var brokenParts []string
	brokenInstances := make([]string, 0, running)
	seenInstance := map[string]bool{}
	staleSeen := false
	for _, t := range targets {
		if t.detail == "" {
			continue
		}
		brokenParts = append(brokenParts, fmt.Sprintf("%s/%s (%s): %s", t.instance, t.key, t.url, t.detail))
		staleSeen = staleSeen || t.stale
		if !seenInstance[t.instance] {
			seenInstance[t.instance] = true
			brokenInstances = append(brokenInstances, t.instance)
		}
	}
	if len(brokenParts) == 0 {
		return docker.CheckResult{
			Name:   uiReachabilityCheckName,
			Status: docker.StatusOK,
			Detail: fmt.Sprintf("%d UI endpoint%s answering HTTP across %d running instance%s",
				len(targets), preflightPluralS(len(targets)), running, preflightPluralS(running)),
		}
	}

	detail := fmt.Sprintf("%d of %d UI endpoint%s not serving HTTP — %s",
		len(brokenParts), len(targets), preflightPluralS(len(targets)),
		strings.Join(brokenParts, "; "))
	if staleSeen {
		detail += " — stale loopback-ports.yaml from an older DevKit detected"
	}
	var remediation strings.Builder
	remediation.WriteString("The port accepts TCP but nothing answers HTTP — usually a stale port overlay generated by an older DevKit.\n")
	for _, name := range brokenInstances {
		fmt.Fprintf(&remediation, "Re-run `dpm localnet up %s` (or Recreate in the Web UI) to regenerate its overlays.\n", name)
	}
	return docker.CheckResult{
		Name:        uiReachabilityCheckName,
		Status:      docker.StatusWarn,
		Detail:      detail,
		Remediation: remediation.String(),
	}
}

// hasStaleLoopbackOverlay reports whether the instance's generated
// loopback-ports.yaml still maps UI host ports onto fixed container
// ports 2000/3000/4000, as older DevKits emitted. Current DevKits
// template both sides of the nginx mapping
// ("${SV_UI_PORT}:${SV_UI_PORT}"), so a literal container side is a
// definitive stale-overlay signal. Best effort: a missing or
// unreadable overlay is not evidence of staleness.
func hasStaleLoopbackOverlay(dataDir string) bool {
	if dataDir == "" {
		return false
	}
	buf, err := os.ReadFile(filepath.Join(dataDir, "loopback-ports.yaml"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(buf), "\n") {
		for _, p := range []string{":2000\"", ":3000\"", ":4000\""} {
			if strings.Contains(line, p) {
				return true
			}
		}
	}
	return false
}
