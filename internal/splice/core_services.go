package splice

// coreServices is the canonical list of compose service names whose
// absence means an instance can't serve a Ledger API call (BIT-222).
// Hoisted to a single source of truth because every supported Splice
// major (0.5.x, 0.6.x) returns the same set — duplicating it per
// adapter invites the lists to silently drift on the next major bump.
//
// Unexported + returned by value via CoreServices() so callers can't
// mutate the shared backing array. Adapters call CoreServices() from
// their own CoreServices() method to satisfy the Adapter interface.
var coreServices = []string{"canton", "splice", "postgres", "nginx"}

// CoreServices returns a fresh copy of the canonical core-services
// list. Adapters whose core stack matches the canonical set should
// return this directly from their CoreServices() method; adapters
// for a future major with a different core stack should define their
// own slice rather than mutating this one.
func CoreServices() []string {
	out := make([]string, len(coreServices))
	copy(out, coreServices)
	return out
}
