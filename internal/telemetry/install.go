package telemetry

import "runtime"

// RecordInstallOnce increments dpm/install exactly once per machine — the
// first time the CLI records telemetry on a real (non-CI) host. It is a
// privacy-preserving device-count proxy: distinct installs bucketed by
// platform, with no identifier and no cross-invocation linkage.
//
// Gated to non-CI because CI runners are ephemeral: the once-flag never
// persists between jobs, so every job would look like a fresh install.
// CI usage is already captured by dpm/ci.
//
// It counts total installs, not active devices, and cannot dedupe
// re-installs or config wipes (there is deliberately no persistent
// identifier) — a directional over-estimate, not an exact device count.
//
// Call once per process, inside the telemetry-enabled path, alongside
// RecordContext.
func RecordInstallOnce() {
	c := loadConsent()
	if c.InstallCounted {
		return // already counted this machine
	}
	if detectCI() {
		return // ephemeral CI host — don't count, don't burn the flag
	}
	os := normOS(runtime.GOOS)
	if os == "" {
		return // unsupported GOOS — allow-list would drop it anyway
	}
	Inc("dpm/install", os)
	c.InstallCounted = true
	_ = saveConsent(c) // best-effort; a write failure just risks a re-count
}
