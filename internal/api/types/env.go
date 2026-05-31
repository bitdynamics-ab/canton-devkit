package types

// EnvExport is the public shape returned by `localnet env --format=json`
// and the future Web UI env export handler.
type EnvExport struct {
	SchemaVersion int               `json:"schema_version"`
	Instance      string            `json:"instance"`
	Vars          map[string]string `json:"vars"`
}
