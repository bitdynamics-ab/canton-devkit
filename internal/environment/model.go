// Package environment is a Phase-0 spike (see docs/design/env-topology-spike.md)
// for compiling a developer-described topology into an upstream Splice LocalNet
// via generated config + a compose override — no fork of the Splice stack.
//
// This package is not yet wired to any CLI command; it exists to prove the
// generation mechanism and seed the module structure. The runtime baseline
// remains the pinned upstream LocalNet resolved by internal/splice.
package environment

// Participant is a generated Canton participant (one validator's node).
type Participant struct {
	// Name is the Canton node name and topology key, e.g. "project-a".
	Name string
	// PortBase is the participant's deterministic 100-port block base, e.g.
	// 21000. API ports are fixed offsets within the block so the layout is
	// stable across restarts and collision-checkable at plan time.
	PortBase int
}

// Port offsets within a participant's block.
const (
	offsetLedgerAPI  = 1
	offsetAdminAPI   = 2
	offsetJSONAPI    = 3
	offsetHTTPHealth = 5
	offsetGRPCHealth = 6
)

// Database is the generated Postgres database name for the participant.
func (p Participant) Database() string { return "participant-" + p.Name }

func (p Participant) LedgerAPIPort() int  { return p.PortBase + offsetLedgerAPI }
func (p Participant) AdminAPIPort() int   { return p.PortBase + offsetAdminAPI }
func (p Participant) JSONAPIPort() int    { return p.PortBase + offsetJSONAPI }
func (p Participant) HTTPHealthPort() int { return p.PortBase + offsetHTTPHealth }
func (p Participant) GRPCHealthPort() int { return p.PortBase + offsetGRPCHealth }

// Topology is the environment input. Phase 0 constructs it by hand; a
// devkit.yaml parser (plan §15) produces it later.
type Topology struct {
	Name         string
	Participants []Participant
}
