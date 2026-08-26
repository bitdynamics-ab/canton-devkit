package telemetry

// RecordInstallSurfaceOnce increments dpm/install_surface exactly once per
// machine per distribution surface. Package-manager hooks use it to record
// which install path was used, distinct from the first real runtime install
// proxy (dpm/install).
//
// Like RecordInstallOnce, this is gated to non-CI hosts so release-smoke
// pipelines and package tests do not inflate adoption counts.
func RecordInstallSurfaceOnce(surface string) bool {
	if !allowed("dpm/install_surface", surface) {
		return false
	}
	if detectCI() {
		return false
	}
	c := loadConsent()
	if c.InstallSurfaceCounted == nil {
		c.InstallSurfaceCounted = map[string]bool{}
	}
	if c.InstallSurfaceCounted[surface] {
		return false
	}
	Inc("dpm/install_surface", surface)
	c.InstallSurfaceCounted[surface] = true
	_ = saveConsent(c) // best-effort; a write miss just risks a re-count
	return true
}
