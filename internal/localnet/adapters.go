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
