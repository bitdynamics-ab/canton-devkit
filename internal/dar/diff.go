package dar

import (
	"sort"
	"strings"
)

// Diff is the structured comparison of two DARs returned by Compare.
// JSON tags are stable; `dar diff --format json` serialises this.
//
// V1 compares at the package level: main-package name/version/LF
// version, plus added/removed/changed dependency packages. SCU-relevant
// signals are flagged but explicitly labelled best-effort — the
// authoritative upgrade check stays with the Ledger API / `daml`
// tooling.
type Diff struct {
	// Left is the "before" / "a" side of the comparison.
	Left *DiffSide `json:"left"`
	// Right is the "after" / "b" side.
	Right *DiffSide `json:"right"`

	// SDKVersionChanged is true when the two DARs were built with
	// different SDK versions.
	SDKVersionChanged bool `json:"sdk_version_changed"`

	// MainNameMatches is true when both DARs' main packages share
	// the same package name. Mismatched names indicate the two
	// DARs are not related (definitely not an SCU upgrade).
	MainNameMatches bool `json:"main_name_matches"`

	// MainVersionDelta is a human label: "unchanged", "bumped",
	// "downgraded", "incomparable" (when either side has no
	// version). Used as a fast SCU triage signal.
	MainVersionDelta string `json:"main_version_delta"`

	// MainLFMajorChanged is true when the two main packages target
	// different Daml-LF majors (e.g. 1 vs 2). This is always an
	// incompatibility for SCU.
	MainLFMajorChanged bool `json:"main_lf_major_changed"`

	// AddedPackages, RemovedPackages, ChangedPackages are the dep-
	// graph deltas keyed by package name.
	AddedPackages   []*PackageMeta  `json:"added_packages"`
	RemovedPackages []*PackageMeta  `json:"removed_packages"`
	ChangedPackages []PackageChange `json:"changed_packages"`

	// SCUSignals is a list of high-level upgrade-relevant
	// observations, surfaced in the CLI text output so users see
	// them at a glance.
	SCUSignals []SCUSignal `json:"scu_signals"`
}

// DiffSide is the per-DAR summary embedded in a Diff result.
type DiffSide struct {
	Path          string `json:"path"`
	MainName      string `json:"main_name"`
	MainVersion   string `json:"main_version"`
	MainPackageID string `json:"main_package_id"`
	MainLFVersion string `json:"main_lf_version"`
	SdkVersion    string `json:"sdk_version"`
	TotalPackages int    `json:"total_packages"`
}

// PackageChange is one "same name, different version" entry.
type PackageChange struct {
	Name             string `json:"name"`
	LeftVersion      string `json:"left_version"`
	RightVersion     string `json:"right_version"`
	LeftPackageID    string `json:"left_package_id"`
	RightPackageID   string `json:"right_package_id"`
	LFVersionChanged bool   `json:"lf_version_changed"`
}

// SCUSignal describes one upgrade-relevant observation. Severity is a
// best-effort hint:
//
//	"info"  — informational
//	"warn"  — likely an issue for SCU but might be intentional
//	"block" — definitely incompatible for an SCU upgrade
type SCUSignal struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// Compare produces a Diff between `left` (the older / lhs) and `right`
// (the newer / rhs). Both args must be non-nil.
func Compare(left, right *Info) *Diff {
	leftMain := left.MainPackage()
	rightMain := right.MainPackage()

	d := &Diff{
		Left:  sideOf(left, leftMain),
		Right: sideOf(right, rightMain),
	}

	if left.Manifest != nil && right.Manifest != nil {
		d.SDKVersionChanged = left.Manifest.SdkVersion != right.Manifest.SdkVersion
	}

	if leftMain != nil && rightMain != nil {
		d.MainNameMatches = leftMain.Name == rightMain.Name
		d.MainVersionDelta = versionDelta(leftMain.Version, rightMain.Version)
		d.MainLFMajorChanged = leftMain.LFMajor != rightMain.LFMajor && leftMain.LFMajor != "" && rightMain.LFMajor != ""
	}

	d.AddedPackages, d.RemovedPackages, d.ChangedPackages = packageDelta(left.Packages, right.Packages)

	d.SCUSignals = computeSCUSignals(d, leftMain, rightMain)
	return d
}

func sideOf(info *Info, main *PackageMeta) *DiffSide {
	side := &DiffSide{
		Path:          info.Path,
		MainPackageID: info.MainPackageID,
		TotalPackages: len(info.Packages),
	}
	if info.Manifest != nil {
		side.SdkVersion = info.Manifest.SdkVersion
	}
	if main != nil {
		side.MainName = main.Name
		side.MainVersion = main.Version
		side.MainLFVersion = main.LFVersion()
	}
	return side
}

// versionDelta returns a human-readable comparison of two semver-ish
// strings: "unchanged", "bumped", "downgraded", or "incomparable".
//
// Semver-ish because Splice and Daml-stdlib versions use dotted
// numeric tuples sometimes followed by pre-release tags. We do a
// numeric-component compare for as long as both sides are digits;
// fall back to string compare for tails (rough but stable).
func versionDelta(a, b string) string {
	if a == "" || b == "" {
		return "incomparable"
	}
	if a == b {
		return "unchanged"
	}
	cmp := compareSemverish(a, b)
	switch {
	case cmp < 0:
		return "bumped"
	case cmp > 0:
		return "downgraded"
	}
	return "unchanged"
}

// compareSemverish returns -1 / 0 / +1 comparing two semver-ish version
// strings on a best-effort basis. Used only by versionDelta; not
// exported because we don't claim full semver compliance.
//
// Pre-release ordering follows semver §11: the release part
// (major.minor.patch…) is compared first, numerically per dotted
// component; if those are equal, a version WITH a pre-release tail
// (after the first '-') has LOWER precedence than one without — so
// 1.0.0-snapshot < 1.0.0, and promoting a snapshot to its release is
// a "bumped", not a false "downgraded". Build metadata (after '+') is
// ignored for precedence, as the spec requires.
func compareSemverish(a, b string) int {
	aRel, aPre := splitPreRelease(a)
	bRel, bPre := splitPreRelease(b)

	if c := compareDotted(aRel, bRel); c != 0 {
		return c
	}

	// Release parts equal. Now the pre-release rule applies: no
	// pre-release tail outranks any pre-release tail.
	switch {
	case aPre == "" && bPre == "":
		return 0
	case aPre == "":
		return 1 // a is the release, b is a pre-release → a is higher
	case bPre == "":
		return -1
	}
	return compareDotted(aPre, bPre)
}

// splitPreRelease separates a semver-ish string into its release part
// (before the first '-') and its pre-release part (between the first
// '-' and the first '+'). Build metadata after '+' is dropped because
// semver excludes it from precedence.
func splitPreRelease(v string) (release, pre string) {
	// Strip build metadata first so a '+' inside it can't be mistaken
	// for a separator.
	if plus := strings.IndexByte(v, '+'); plus >= 0 {
		v = v[:plus]
	}
	if dash := strings.IndexByte(v, '-'); dash >= 0 {
		return v[:dash], v[dash+1:]
	}
	return v, ""
}

// compareDotted compares two dot-separated identifier lists per the
// semver field-by-field rule: numeric identifiers compare numerically
// and rank LOWER than non-numeric ones; non-numeric identifiers compare
// lexically (ASCII). When one side runs out of fields and all prior
// fields were equal, the longer side has higher precedence.
func compareDotted(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	n := len(aParts)
	if len(bParts) > n {
		n = len(bParts)
	}
	for i := 0; i < n; i++ {
		// A missing field means that side is shorter; the side with
		// more fields wins (all preceding fields were equal).
		if i >= len(aParts) {
			return -1
		}
		if i >= len(bParts) {
			return 1
		}
		ap, bp := aParts[i], bParts[i]
		an, aNum := atoi(ap)
		bn, bNum := atoi(bp)
		switch {
		case aNum && bNum:
			if an != bn {
				if an < bn {
					return -1
				}
				return 1
			}
		case aNum:
			// Numeric identifiers have lower precedence than
			// non-numeric ones (semver §11).
			return -1
		case bNum:
			return 1
		default:
			if ap != bp {
				if ap < bp {
					return -1
				}
				return 1
			}
		}
	}
	return 0
}

func atoi(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// packageDelta computes added / removed / changed package sets keyed by
// package name. A package is "changed" when both sides have it but at
// different versions or package IDs.
func packageDelta(left, right []*PackageMeta) (added, removed []*PackageMeta, changed []PackageChange) {
	// Index by name on both sides. There may be multiple versions of
	// the same name in a single DAR (rare but possible for stdlib
	// upgrades); for simplicity we take the highest-version one as
	// the comparison anchor.
	leftByName := indexByName(left)
	rightByName := indexByName(right)

	for name, l := range leftByName {
		r, ok := rightByName[name]
		if !ok {
			removed = append(removed, l)
			continue
		}
		if l.PackageID != r.PackageID || l.Version != r.Version {
			changed = append(changed, PackageChange{
				Name:             name,
				LeftVersion:      l.Version,
				RightVersion:     r.Version,
				LeftPackageID:    l.PackageID,
				RightPackageID:   r.PackageID,
				LFVersionChanged: l.LFMajor != r.LFMajor && l.LFMajor != "" && r.LFMajor != "",
			})
		}
	}
	for name, r := range rightByName {
		if _, ok := leftByName[name]; !ok {
			added = append(added, r)
		}
	}

	sort.SliceStable(added, func(i, j int) bool { return added[i].Name < added[j].Name })
	sort.SliceStable(removed, func(i, j int) bool { return removed[i].Name < removed[j].Name })
	sort.SliceStable(changed, func(i, j int) bool { return changed[i].Name < changed[j].Name })
	return
}

// indexByName builds a name→package map across the dependency set.
// The main package is excluded because Compare treats it separately
// (main-name-matches / main-version-delta / main-LF-major-changed) and
// counting it as a "changed dep" would double-report SCU signals.
//
// When two deps share a name (rare — usually only when stdlib upgrades
// pull in two copies of the same internal module), the higher-version
// one wins so the diff shows what a downstream consumer would see.
func indexByName(pkgs []*PackageMeta) map[string]*PackageMeta {
	m := make(map[string]*PackageMeta, len(pkgs))
	for _, p := range pkgs {
		if p.IsMain {
			continue
		}
		if prev, ok := m[p.Name]; ok && compareSemverish(prev.Version, p.Version) >= 0 {
			continue
		}
		m[p.Name] = p
	}
	return m
}

// computeSCUSignals turns the structured diff into a list of
// human-readable upgrade-relevant observations. These are
// **best-effort** heuristics; the canonical SCU check still happens at
// `daml damlc` / Ledger API time.
func computeSCUSignals(d *Diff, leftMain, rightMain *PackageMeta) []SCUSignal {
	var out []SCUSignal

	if leftMain == nil || rightMain == nil {
		out = append(out, SCUSignal{Severity: "warn", Message: "could not identify main package on one side; SCU comparison skipped"})
		return out
	}

	if !d.MainNameMatches {
		out = append(out, SCUSignal{
			Severity: "block",
			Message:  "main package names differ (" + leftMain.Name + " vs " + rightMain.Name + ") — these DARs are not an SCU upgrade pair",
		})
		return out // no point in further checks
	}

	switch d.MainVersionDelta {
	case "downgraded":
		out = append(out, SCUSignal{Severity: "block", Message: "main package version was downgraded (" + leftMain.Version + " → " + rightMain.Version + "); SCU rejects downgrades"})
	case "unchanged":
		if leftMain.PackageID != rightMain.PackageID {
			out = append(out, SCUSignal{Severity: "warn", Message: "main package version unchanged but package ID differs — same source rebuilt, or republished with different contents"})
		}
	case "bumped":
		out = append(out, SCUSignal{Severity: "info", Message: "main package version bumped (" + leftMain.Version + " → " + rightMain.Version + ")"})
	}

	if d.MainLFMajorChanged {
		out = append(out, SCUSignal{
			Severity: "block",
			Message:  "Daml-LF major version changed (" + leftMain.LFVersion() + " → " + rightMain.LFVersion() + ") — cross-LF-major SCU is not supported",
		})
	} else if leftMain.LFVersion() != rightMain.LFVersion() {
		out = append(out, SCUSignal{
			Severity: "info",
			Message:  "Daml-LF minor version changed (" + leftMain.LFVersion() + " → " + rightMain.LFVersion() + ")",
		})
	}

	if len(d.ChangedPackages) > 0 {
		out = append(out, SCUSignal{
			Severity: "info",
			Message:  pluralize(len(d.ChangedPackages), "dependency", "dependencies") + " changed version/id — review the per-package changes for SCU impact",
		})
	}
	if len(d.AddedPackages) > 0 {
		out = append(out, SCUSignal{
			Severity: "info",
			Message:  pluralize(len(d.AddedPackages), "new dependency", "new dependencies") + " added — make sure the ledger has all of them vetted",
		})
	}
	if len(d.RemovedPackages) > 0 {
		out = append(out, SCUSignal{
			Severity: "warn",
			Message:  pluralize(len(d.RemovedPackages), "dependency", "dependencies") + " removed — references to them from older versions will dangle",
		})
	}

	out = append(out, SCUSignal{
		Severity: "info",
		Message:  "best-effort signals — authoritative SCU validation lives with daml damlc / the Ledger API",
	})
	return out
}

func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return itoa(n) + " " + plural
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
