package telemetry

// allowedCounters is the CLOSED allow-list of telemetry counters (design
// v1, decision #3). Format is "<chart>": {set of allowed "<bucket>"}.
//
// Nothing is recorded that isn't here. A compile-time test
// (allowlist_test.go) walks every telemetry.Inc("chart","bucket") call
// site in the repo and asserts it against this map — so a new counter or
// bucket cannot be slipped past review. The dpm/command and
// dpm/command_exit buckets are validated at runtime against
// commandBuckets (their bucket space is the verb set, too large to inline
// twice).
var allowedCounters = map[string]map[string]struct{}{
	"dpm/command":                set(commandVerbs...),
	"dpm/command_exit":           nil, // "<verb>/<ok|fail>" — validated against commandVerbs at runtime
	"dpm/channel":                set("stable", "nightly", "dev"),
	"dpm/os":                     set("linux", "darwin", "windows"),
	"dpm/arch":                   set("amd64", "arm64"),
	"dpm/ci":                     set("true", "false"),
	"dpm/llm_agent":              set("claude", "copilot", "cursor", "gemini", "none"),
	"dpm/docker_engine":          set("docker", "colima", "orbstack", "podman", "other"),
	"dpm/compose_version_bucket": set("v2.20-", "v2.20-v2.27", "v2.28+"),
	"dpm/doctor_fail":            nil, // check IDs from internal/docker/checks.go, validated at runtime
}

// commandVerbs is the bucket space for dpm/command — the localnet
// subcommand names. Mirrors the subcommands registered in
// internal/cli/localnet. Unknown verbs are dropped (never recorded).
var commandVerbs = []string{
	"up", "down", "restart", "status", "list", "doctor", "logs", "env",
	"creds", "clean", "snapshot", "restore", "versions", "ui", "container",
	"refresh", "metrics", "contracts", "tx", "dar", "skills", "telemetry",
	"pause", "resume",
}

func set(items ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(items))
	for _, it := range items {
		m[it] = struct{}{}
	}
	return m
}

// allowed reports whether (chart, bucket) is recordable. dpm/command_exit
// accepts "<verb>/<ok|fail>"; dpm/doctor_fail accepts any non-empty check
// id (the doctor allow-list lives in internal/docker, validated there).
func allowed(chart, bucket string) bool {
	buckets, ok := allowedCounters[chart]
	if !ok {
		return false
	}
	if buckets == nil {
		switch chart {
		case "dpm/command_exit":
			return validCommandExit(bucket)
		case "dpm/doctor_fail":
			return bucket != ""
		}
		return false
	}
	_, ok = buckets[bucket]
	return ok
}

func validCommandExit(bucket string) bool {
	// "<verb>/<ok|fail>"
	for i := len(bucket) - 1; i >= 0; i-- {
		if bucket[i] == '/' {
			verb, outcome := bucket[:i], bucket[i+1:]
			if outcome != "ok" && outcome != "fail" {
				return false
			}
			_, ok := allowedCounters["dpm/command"][verb]
			return ok
		}
	}
	return false
}
