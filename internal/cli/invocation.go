package cli

// InvocationMode records how the binary was launched, so the CLI can show
// mode-appropriate help/examples and hide flags that don't apply under DPM.
//
// The two modes are indistinguishable from the command arguments alone —
// both `canton-devkit localnet up` and `dpm localnet up` reach the binary
// as argv `["localnet","up"]`. DPM therefore prepends a marker via the
// component manifest's exec-args (see component.yaml); detectMode strips
// that marker and reports the mode.
type InvocationMode int

const (
	// Direct means the user ran the `canton-devkit` binary themselves.
	Direct InvocationMode = iota
	// ViaDPM means the binary was launched by DPM (`dpm localnet …`).
	ViaDPM
)

// viaDPMMarker is the leading argv token DPM prepends (via exec-args in the
// component manifest) to signal a DPM-launched invocation. It is stripped
// before Cobra parses, so it never appears as a real flag.
const viaDPMMarker = "--via-dpm"

// displayName is the command name shown in help text and examples: the DPM
// front-end command under DPM, the binary name otherwise.
func (m InvocationMode) displayName() string {
	if m == ViaDPM {
		return "dpm"
	}
	return appName
}

// detectMode inspects args for the leading viaDPMMarker, returning the
// resolved mode and the args with the marker removed. Only a leading marker
// counts: DPM always prepends it (exec-args come before user args), and
// restricting to the front keeps a user-supplied "--via-dpm" further down
// the line from being silently swallowed.
func detectMode(args []string) (InvocationMode, []string) {
	if len(args) > 0 && args[0] == viaDPMMarker {
		return ViaDPM, args[1:]
	}
	return Direct, args
}
