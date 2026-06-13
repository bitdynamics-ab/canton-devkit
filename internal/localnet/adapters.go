package localnet

import (
	"fmt"

	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
	v05 "github.com/bitdynamics-ab/canton-devkit/internal/splice/v05"
	v06 "github.com/bitdynamics-ab/canton-devkit/internal/splice/v06"
)

// adapterFor returns the splice.Adapter implementation for a Version.
// Lives outside the splice package to avoid an import cycle: splice/v0X
// already import splice for InstanceParams/Adapter, so a registration
// in splice/ would close the cycle.
//
// When a new Splice major is supported, add a case here AND a new entry
// to internal/splice/versions.go with the matching `Major` value.
func adapterFor(v splice.Version) (splice.Adapter, error) {
	switch v.Major {
	case "0.5":
		return v05.New(), nil
	case "0.6":
		return v06.New(), nil
	default:
		return nil, fmt.Errorf("no adapter registered for Splice major %q (tag %s) — this is a canton-devkit bug",
			v.Major, v.Tag)
	}
}

// CoreServicesFor resolves the core-services list (compose
// service names whose absence collapses the reconciler's evalStatus
// to `failed`) for a given persisted splice_version. Returns nil
// when the version isn't resolvable — callers treat nil as "skip the
// core-services check" to preserve back-compat with registry entries
// whose splice_version has been renamed or removed.
//
// Uses ResolveForOperation (not Resolve) so an uncurated instance —
// whose tag isn't in the catalogue — still gets its core-services
// evaluated by the reconciler instead of silently skipping the check.
// The list depends only on the adapter Major, which ResolveForOperation
// recovers from the resolved cache or the tag.
//
// Exported here so handlers (which can't see adapterFor) have a
// single-call entry point and keep the adapter-resolver invariants
// in one place rather than scattered across packages.
func CoreServicesFor(spliceVersion string) []string {
	v, err := splice.ResolveForOperation(spliceVersion)
	if err != nil {
		return nil
	}
	a, err := adapterFor(v)
	if err != nil {
		return nil
	}
	return a.CoreServices()
}
