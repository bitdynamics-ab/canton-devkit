package localnet

import "testing"

// TestDiagnoseFromObs pins the BIT-174 false-positive fix: the JVM
// `-Xmx exceeds half of the container's total memory` warning is
// logged on EVERY startup of a memory-tight canton — it must NOT
// alone trigger ErrCodeCantonOOM (that would false-positive every
// 4 GiB Docker Desktop boot), AND a real OOM-kill must still be
// detected even when the JVM didn't get a chance to log.
func TestDiagnoseFromObs(t *testing.T) {
	jvmWarn := "WARN: -Xmx (2.50 GB) exceeds half of the container's total memory 4.00 GB."
	healthyUp := "Up 5 minutes (healthy)"
	restartingOOM := "Restarting (137) 3 seconds ago"
	restartingTerm := "Restarting (143) 1 second ago"
	restartingClean := "Restarting (0) 1 second ago"

	cases := []struct {
		name string
		obs  containerObs
		want string
	}{
		{
			name: "healthy canton with JVM warning — must NOT fire (false-positive guard)",
			obs: containerObs{
				Service: "canton",
				State:   "running",
				Health:  "healthy",
				Status:  healthyUp,
				LogTail: jvmWarn,
			},
			want: "",
		},
		{
			name: "starting canton with JVM warning — also must NOT fire",
			obs: containerObs{
				Service: "canton",
				State:   "running",
				Health:  "starting",
				Status:  "Up 30 seconds (health: starting)",
				LogTail: jvmWarn,
			},
			want: "",
		},
		{
			name: "canton restart-loop with SIGKILL + JVM warning — fires CANTON_OOM",
			obs: containerObs{
				Service: "canton",
				State:   "restarting",
				Status:  restartingOOM,
				LogTail: "some early init line\n" + jvmWarn + "\nmore lines",
			},
			want: ErrCodeCantonOOM,
		},
		{
			name: "canton restart-loop with SIGTERM (143) — also fires CANTON_OOM",
			obs: containerObs{
				Service: "canton",
				State:   "restarting",
				Status:  restartingTerm,
				LogTail: jvmWarn,
			},
			want: ErrCodeCantonOOM,
		},
		{
			name: "canton restart-loop with SIGKILL but no log evidence — still CANTON_OOM (kernel killed before JVM logged)",
			obs: containerObs{
				Service: "canton",
				State:   "restarting",
				Status:  restartingOOM,
				LogTail: "",
			},
			want: ErrCodeCantonOOM,
		},
		{
			name: "non-canton service restart-loop with SIGKILL — NOT CANTON_OOM (caller falls back to generic unhealthy)",
			obs: containerObs{
				Service: "postgres",
				State:   "restarting",
				Status:  restartingOOM,
				LogTail: "",
			},
			want: "",
		},
		{
			name: "canton restart-loop with clean exit (0) — NOT OOM",
			obs: containerObs{
				Service: "canton",
				State:   "restarting",
				Status:  restartingClean,
				LogTail: "",
			},
			want: "",
		},
		{
			name: "unhealthy canton without restart-loop signal — no diagnosis (could be any cause)",
			obs: containerObs{
				Service: "canton",
				State:   "running",
				Health:  "unhealthy",
				Status:  "Up 2 minutes (unhealthy)",
				LogTail: jvmWarn,
			},
			want: "",
		},
		{
			name: "exited canton with no SIGKILL signal — no diagnosis",
			obs: containerObs{
				Service: "canton",
				State:   "exited",
				Status:  "Exited (0) 30 seconds ago",
			},
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := diagnoseFromObs(c.obs)
			if got != c.want {
				t.Errorf("diagnoseFromObs() = %q, want %q\n  obs: %+v",
					got, c.want, c.obs)
			}
		})
	}
}

// TestIsRestartingOOM pins the docker-status parsing. The status
// string format is brittle (docker's wire format isn't a typed
// API) — locking the strings we match means a docker version
// bump that changes the wording is caught by this test rather
// than a silent false-negative in production.
func TestIsRestartingOOM(t *testing.T) {
	yes := []string{
		"Restarting (137) 3 seconds ago",
		"Restarting (143) 1 minute ago",
		"Restarting (137) Less than a second ago",
	}
	no := []string{
		"Up 5 minutes (healthy)",
		"Restarting (0) 1 second ago",
		"Restarting (1) 3 seconds ago", // generic app error, not OOM
		"Exited (137) 10 minutes ago",  // exited, not restart-looping
		"",
	}
	for _, s := range yes {
		if !isRestartingOOM(s) {
			t.Errorf("isRestartingOOM(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if isRestartingOOM(s) {
			t.Errorf("isRestartingOOM(%q) = true, want false", s)
		}
	}
}
