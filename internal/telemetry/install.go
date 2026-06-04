package telemetry

import "runtime"

// RecordInstallOnce increments dpm/install exactly once per machine — the
// first time the CLI records telemetry on a real (non-CI) host. It is the
// privacy-preserving device-count proxy: a count of DISTINCT installs,
// bucketed by platform, with NO identifier and NO cross-invocation
// linkage. Each install contributes exactly one increment, ever — so the
// collector can answer "how many devices/installs" without ever being
// able to single out or follow a device.
//
// Gated to NON-CI on purpose. CI runners are ephemeral: each job starts
// from a fresh container/image, so the once-flag in the config never
// persists between runs — counting first-runs there would make every CI
// job look like a brand-new install and inflate the device count for what
// is really one pipeline. CI usage is already captured (truthfully) by
// dpm/ci, so gating loses nothing.
//
// Caveats this CANNOT fix (documented for honesty):
//   - It counts TOTAL installs, not ACTIVE devices — a host that installed
//     once and never returned still counts once.
//   - It cannot dedupe re-installs / config wipes / new OS images: with no
//     persistent identifier (the very thing that keeps this zero-PII),
//     wiping ~/.config/canton-devkit and re-running counts as a new
//     install. So the number is a slight over-estimate / directional
//     proxy, not an exact unique-device count.
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
