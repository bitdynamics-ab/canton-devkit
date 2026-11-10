// Package v10 implements the Adapter contract for Splice LocalNet 0.10.x.
//
// The cluster/compose/localnet/ tree keeps the same compose surface as
// 0.6.x upstream — same compose files, env-file set, profile names,
// service/port map, and the env-var contract the adapter wires
// (IMAGE_TAG, IMAGE_REPO, PARTY_HINT, ALPHA_PROTOCOL_VERSION_ENV, …).
// Content changes in 0.10.x (Postgres 18 default, loopback host binds)
// live inside those files; DevKit's overlay path still applies. So
// 0.10.x embeds the 0.6.x adapter and only relabels its major slot.
package v10

import (
	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
	v06 "github.com/bitdynamics-ab/canton-devkit/internal/splice/v06"
)

// Adapter is the 0.6.x adapter under the 0.10 major label.
type Adapter struct{ *v06.Adapter }

// New returns a fresh 0.10.x adapter. Stateless — safe to reuse.
func New() *Adapter { return &Adapter{v06.New()} }

func (*Adapter) MajorVersion() string { return "0.10" }

var _ splice.Adapter = (*Adapter)(nil)
