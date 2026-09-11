// Package v07 implements the Adapter contract for Splice LocalNet 0.7.x.
//
// The cluster/compose/localnet/ tree is unchanged from 0.6.x upstream —
// same compose files, env-file set, profile names, service/port map, and
// the env-var contract the adapter wires (IMAGE_TAG, IMAGE_REPO,
// PARTY_HINT, ALPHA_PROTOCOL_VERSION_ENV, …). So 0.7.x embeds the 0.6.x
// adapter and only relabels its major slot.
package v07

import (
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
	v06 "github.com/bitdynamics-ab/canton-devkit/internal/splice/v06"
)

// Adapter is the 0.6.x adapter under the 0.7 major label.
type Adapter struct{ *v06.Adapter }

// New returns a fresh 0.7.x adapter. Stateless — safe to reuse.
func New() *Adapter { return &Adapter{v06.New()} }

func (*Adapter) MajorVersion() string { return "0.7" }

var _ splice.Adapter = (*Adapter)(nil)
