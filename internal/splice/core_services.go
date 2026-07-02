package splice

// coreServices is the canonical list of compose service names whose
// absence means an instance can't serve a Ledger API call. Hoisted to
// a single source of truth because every supported Splice major
// (0.5.x, 0.6.x) uses the same set — duplicating it per adapter
// invites silent drift on the next major bump. Unexported and copied
// on return so callers can't mutate the shared backing array.
var coreServices = []string{"canton", "splice", "postgres", "nginx"}

// CoreServices returns a fresh copy of the canonical core-services
// list. Adapters whose core stack matches should return this from
// their CoreServices() method; a future major with a different core
// stack should define its own slice rather than mutating this one.
func CoreServices() []string {
	out := make([]string, len(coreServices))
	copy(out, coreServices)
	return out
}
