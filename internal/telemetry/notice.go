package telemetry

import (
	"fmt"
	"io"
)

// noticeOperationalVerbs are the localnet subcommands "real" enough to
// trigger the one-time disclosure. Informational commands (version,
// help, `telemetry …`) deliberately don't, so a user can read and decide
// before any operational run.
var noticeOperationalVerbs = set("up", "down", "restart", "status", "doctor", "remove", "clean", "snapshot", "restore")

// MaybeNotice prints the one-time opt-out disclosure to w and records it,
// but only when ALL of these hold: telemetry is effectively enabled, the
// notice hasn't been shown, the invocation is interactive (a TTY — never
// in CI/pipes), and the verb is operational. Returns true if it printed.
func MaybeNotice(w io.Writer, verb string, interactive bool) bool {
	if !interactive {
		return false
	}
	if _, ok := noticeOperationalVerbs[verb]; !ok {
		return false
	}
	on, _ := Enabled()
	if !on {
		return false // disabled → nothing collected → no notice owed
	}
	c := loadConsent()
	if c.NoticeShown {
		return false
	}
	_, _ = fmt.Fprint(w, noticeText)
	c.NoticeShown = true
	_ = saveConsent(c)
	return true
}

const noticeText = `
canton-devkit sends anonymous usage counters (command name, OS, Docker
engine, exit status) to help us prioritize fixes. No file paths, no
instance/party ids, no JWTs, no error messages — just counters,
aggregated daily, plus one random token (a UUID, NOT derived from your
machine) used only to count distinct installs. Rotate it anytime with
'telemetry reset-id'.

Telemetry is ON by default. Turn it off anytime:
  canton-devkit telemetry off        (or set DPM_TELEMETRY=off / DO_NOT_TRACK=1)
See exactly what's queued:
  canton-devkit telemetry preview

`
