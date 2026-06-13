// DAR deep-inspection, structural diff, and per-participant vetting
// handlers — Web UI parity for the DAR screen.
//
// Endpoints registered here (mounted from dar.go's MountDAR):
//
//	GET  /api/instances/{name}/dar/{id}/inspect
//	  → 200 {schema_version, instance, role, main, name, version,
//	         packages: [{package_id, name, version, lf_version,
//	                     contents?: {modules:[…]}}]}
//	  → 404 NOT_FOUND if no DAR with that main_package_id is on the
//	         participant
//	  → 413 REQUEST_TOO_LARGE if the assembled response would exceed
//	         maxInspectBytes (4 MiB)
//
//	GET  /api/instances/{name}/dar/diff?a=<id>&b=<id>
//	  → 200 {schema_version, instance, role, a:{main,name,version},
//	         b:{main,name,version},
//	         modules_added:[…], modules_removed:[…],
//	         templates_added:[{module,name,choices?}],
//	         templates_removed:[{module,name,choices?}],
//	         templates_changed:[{module,name,
//	           choices_added:[…], choices_removed:[…]}],
//	         interfaces_added:[…], interfaces_removed:[…],
//	         interfaces_changed:[{module,name,
//	           choices_added, choices_removed, methods_added, methods_removed}]}
//
//	GET  /api/instances/{name}/dar/{id}/vetting
//	  → 200 {schema_version, instance, main, participants:
//	          [{role, vetted, error?}]}
//
//	POST /api/instances/{name}/dar/{id}/vetting/{role}
//	  body {"vetted": bool}
//	  → 200 {schema_version, instance, main, role, vetted}
//	  → 412 VETTING_UNSUPPORTED if the Canton admin RPC isn't
//	         available on the target participant
//
// All read endpoints use the same per-role admin-port lookup
// pattern as dar.go's handleDARList. Write endpoints follow the
// 64 KiB body cap + DisallowUnknownFields convention shared by
// the rest of internal/ui/handlers.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/canton/admin"
	adminproto "github.com/bitdynamics-ab/canton-devkit/internal/canton/admin/proto"
	cdkdar "github.com/bitdynamics-ab/canton-devkit/internal/dar"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

// maxInspectBytes caps the inspect-response body. A maliciously-large
// DAR (one with thousands of modules) could otherwise yield a payload
// that OOMs the browser tab. 4 MiB is well above any real-world DAR
// inspect output observed in Splice (~50 KiB typical).
const maxInspectBytes = 4 << 20

// inspectRequestTimeout caps a single inspect / diff / vetting call.
// Two gRPC round-trips at most (GetDar + maybe ListDars), each
// usually sub-second.
const inspectRequestTimeout = 15 * time.Second

// vettingPostMax is the request-body cap on the vetting toggle
// endpoint. The body is a one-field JSON document; 64 KiB is the
// shared convention with the rest of handlers/.
const vettingPostMax = 64 << 10

// handleDARInspect serves a deep package tree for one DAR. The DAR
// bytes are fetched via the Canton admin API's GetDar RPC, then
// parsed with internal/dar.Open which surfaces module / template /
// choice / interface structure.
func handleDARInspect(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := registry.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid instance name", err)
		return
	}
	mainID := r.PathValue("id")
	if !looksLikePackageID(mainID) {
		writeErrorWithCode(w, http.StatusBadRequest,
			ErrCodeInvalidRequest,
			"invalid package id",
			"package id must be 64 hex chars")
		return
	}
	role, ok := resolveRoleParam(w, r)
	if !ok {
		return
	}

	state, err := registry.Read(name)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			writeErrorWithCode(w, http.StatusNotFound,
				ErrCodeNotFound,
				"instance "+name+" not registered")
			return
		}
		writeError(w, http.StatusInternalServerError, "read state", err)
		return
	}
	adminPort, cred, ok := requireParticipantAccess(w, state, name, role)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), inspectRequestTimeout)
	defer cancel()

	client, err := admin.Connect(ctx, admin.Config{
		Host:     "localhost:" + strconv.Itoa(adminPort),
		Token:    cred.JWT,
		Insecure: true,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "dial canton admin", err)
		return
	}
	defer func() { _ = client.Close() }()

	resp, err := client.Package.GetDar(ctx, &adminproto.GetDarRequest{
		MainPackageId: mainID,
	})
	if err != nil {
		// The admin API returns NOT_FOUND for an unknown main id; the
		// gRPC error string carries it but we don't import gRPC's
		// codes package here — match on the canonical substring.
		if strings.Contains(strings.ToLower(err.Error()), "not_found") ||
			strings.Contains(strings.ToLower(err.Error()), "notfound") {
			writeErrorWithCode(w, http.StatusNotFound,
				ErrCodeNotFound,
				"no DAR with main package id "+mainID+" on participant "+role)
			return
		}
		writeError(w, http.StatusBadGateway, "fetch dar", err)
		return
	}
	payload := resp.GetPayload()
	if len(payload) == 0 {
		writeError(w, http.StatusBadGateway, "empty DAR payload from participant", nil)
		return
	}

	tree, err := buildInspectTree(payload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "parse DAR", err)
		return
	}
	tree["schema_version"] = 1
	tree["instance"] = name
	tree["role"] = role

	// Enforce the response size cap by marshalling once, checking
	// length, then writing. Cheaper than a streaming counter and
	// correctness-clear.
	body, err := json.MarshalIndent(tree, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "marshal inspect", err)
		return
	}
	if len(body) > maxInspectBytes {
		writeErrorWithCode(w, http.StatusRequestEntityTooLarge,
			ErrCodeRequestTooLarge,
			fmt.Sprintf("inspect payload %d bytes exceeds %d cap",
				len(body), maxInspectBytes),
			"DAR has too many modules/templates to inspect via the Web UI; use `dpm localnet dar info` from the CLI")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// buildInspectTree decodes the DAR bytes into a JSON-ready tree. The
// shape matches the contract documented at the top of this file.
func buildInspectTree(payload []byte) (map[string]any, error) {
	// internal/dar.Open expects a path; the bytes-variant lives a
	// layer down. We use OpenBytes when available — fall back to a
	// tempfile is overkill for the loopback UI. Parse via Open
	// equivalent: re-use the same Reader internals through a
	// public bytes entrypoint.
	info, err := cdkdar.OpenBytes(payload)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"main":     info.MainPackageID,
		"sha256":   info.SHA256,
		"packages": []map[string]any{},
	}
	if main := info.MainPackage(); main != nil {
		out["name"] = main.Name
		out["version"] = main.Version
	}
	pkgs := make([]map[string]any, 0, len(info.Packages))
	for _, p := range info.Packages {
		pkg := map[string]any{
			"package_id": p.PackageID,
			"name":       p.Name,
			"version":    p.Version,
			"lf_version": p.LFVersion(),
			"size_bytes": p.SizeBytes,
			"is_main":    p.IsMain,
		}
		if p.Contents != nil {
			pkg["contents"] = p.Contents
		}
		pkgs = append(pkgs, pkg)
	}
	out["packages"] = pkgs
	return out, nil
}

// looksLikePackageID returns true when s is plausibly a Canton
// package id (64 lowercase hex). Cheap, non-cryptographic — guards
// against path injection (no slashes) before the value lands in a
// gRPC field.
func looksLikePackageID(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// resolveRoleParam reads ?role= with the same defaulting + validation
// as handleDARList. Returns ("", false) after writing the error
// response if the role is invalid.
func resolveRoleParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	role := r.URL.Query().Get("role")
	if role == "" {
		role = "app-user"
	}
	if !validRole[role] {
		writeErrorWithCode(w, http.StatusBadRequest,
			ErrCodeInvalidRequest,
			"invalid role: "+role,
			"role must be one of app-user, app-provider, sv")
		return "", false
	}
	return role, true
}

// requireParticipantAccess pulls the per-role admin port + JWT from
// state. Mirrors handleDARList's preconditions so the error envelope
// stays consistent across DAR endpoints.
func requireParticipantAccess(w http.ResponseWriter, state *registry.State, name, role string) (int, registry.Credential, bool) {
	portKey := "participant_admin_" + role
	adminPort, hasPort := state.Ports[portKey]
	if !hasPort || adminPort == 0 {
		writeErrorWithCode(w, http.StatusServiceUnavailable,
			"PARTICIPANT_PORT_NOT_RECORDED",
			"instance "+name+" was started before participant ports were recorded",
			"restart the instance with `dpm localnet down --name "+name+
				"` followed by `dpm localnet up --name "+name+
				"` — the new up flow captures all Canton API ports")
		return 0, registry.Credential{}, false
	}
	cred, hasCred := state.Credentials[role]
	if !hasCred {
		writeError(w, http.StatusInternalServerError,
			"no JWT recorded for role "+role,
			fmt.Errorf("missing credential for role %q", role))
		return 0, registry.Credential{}, false
	}
	return adminPort, cred, true
}

// handleDARDiff returns the structural delta between two DARs by
// main package id. Both must be already uploaded to the named
// participant — diff against a not-yet-uploaded local file is the
// CLI's `dar diff` job, not this endpoint.
func handleDARDiff(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := registry.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid instance name", err)
		return
	}
	a := r.URL.Query().Get("a")
	b := r.URL.Query().Get("b")
	if !looksLikePackageID(a) || !looksLikePackageID(b) {
		writeErrorWithCode(w, http.StatusBadRequest,
			ErrCodeInvalidRequest,
			"a and b must each be a 64-hex package id")
		return
	}
	if a == b {
		writeErrorWithCode(w, http.StatusBadRequest,
			ErrCodeInvalidRequest,
			"a and b refer to the same package id; diff is trivially empty")
		return
	}
	role, ok := resolveRoleParam(w, r)
	if !ok {
		return
	}
	state, err := registry.Read(name)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			writeErrorWithCode(w, http.StatusNotFound, ErrCodeNotFound,
				"instance "+name+" not registered")
			return
		}
		writeError(w, http.StatusInternalServerError, "read state", err)
		return
	}
	adminPort, cred, ok := requireParticipantAccess(w, state, name, role)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), inspectRequestTimeout)
	defer cancel()

	client, err := admin.Connect(ctx, admin.Config{
		Host:     "localhost:" + strconv.Itoa(adminPort),
		Token:    cred.JWT,
		Insecure: true,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "dial canton admin", err)
		return
	}
	defer func() { _ = client.Close() }()

	fetch := func(id string) (*cdkdar.Info, error) {
		resp, err := client.Package.GetDar(ctx, &adminproto.GetDarRequest{MainPackageId: id})
		if err != nil {
			return nil, err
		}
		return cdkdar.OpenBytes(resp.GetPayload())
	}
	infoA, err := fetch(a)
	if err != nil {
		writeError(w, http.StatusBadGateway, "fetch dar a", err)
		return
	}
	infoB, err := fetch(b)
	if err != nil {
		writeError(w, http.StatusBadGateway, "fetch dar b", err)
		return
	}

	diff := structuralDiff(infoA, infoB)
	diff["schema_version"] = 1
	diff["instance"] = name
	diff["role"] = role
	writeJSON(w, http.StatusOK, diff)
}

// structuralDiff is the hand-rolled, package-public-shape comparator
// for two DARs' module/template/interface trees. We deliberately
// don't reuse internal/dar.Compare because that comparator works at
// the package-level (added / removed / changed deps) — useful for
// SCU triage but not for the "what choice changed on this template?"
// question this endpoint answers.
func structuralDiff(a, b *cdkdar.Info) map[string]any {
	type tplKey struct{ Module, Name string }
	type ifKey struct{ Module, Name string }

	gather := func(info *cdkdar.Info) (
		modules map[string]struct{},
		templates map[tplKey][]string, // choices, sorted
		interfaces map[ifKey]struct {
			choices, methods []string
		},
	) {
		modules = map[string]struct{}{}
		templates = map[tplKey][]string{}
		interfaces = map[ifKey]struct{ choices, methods []string }{}
		for _, pkg := range info.Packages {
			if pkg.Contents == nil {
				continue
			}
			for _, mod := range pkg.Contents.Modules {
				modules[mod.Name] = struct{}{}
				for _, t := range mod.Templates {
					key := tplKey{Module: mod.Name, Name: t.Name}
					// Multiple .dalfs can declare the same module
					// name across versions; we union choices.
					existing := templates[key]
					templates[key] = mergeSorted(existing, t.Choices)
				}
				for _, i := range mod.Interfaces {
					key := ifKey{Module: mod.Name, Name: i.Name}
					cur := interfaces[key]
					cur.choices = mergeSorted(cur.choices, i.Choices)
					cur.methods = mergeSorted(cur.methods, i.Methods)
					interfaces[key] = cur
				}
			}
		}
		return
	}

	modsA, tplA, ifA := gather(a)
	modsB, tplB, ifB := gather(b)

	out := map[string]any{}

	if mainA := a.MainPackage(); mainA != nil {
		out["a"] = map[string]any{
			"main":    a.MainPackageID,
			"name":    mainA.Name,
			"version": mainA.Version,
		}
	}
	if mainB := b.MainPackage(); mainB != nil {
		out["b"] = map[string]any{
			"main":    b.MainPackageID,
			"name":    mainB.Name,
			"version": mainB.Version,
		}
	}

	out["modules_added"] = sortedSetDiff(modsB, modsA)
	out["modules_removed"] = sortedSetDiff(modsA, modsB)

	// Templates: added / removed / changed (same key, different
	// choice list).
	var tplAdded, tplRemoved []map[string]any
	var tplChanged []map[string]any
	allTplKeys := map[tplKey]struct{}{}
	for k := range tplA {
		allTplKeys[k] = struct{}{}
	}
	for k := range tplB {
		allTplKeys[k] = struct{}{}
	}
	tplKeys := make([]tplKey, 0, len(allTplKeys))
	for k := range allTplKeys {
		tplKeys = append(tplKeys, k)
	}
	sort.Slice(tplKeys, func(i, j int) bool {
		if tplKeys[i].Module != tplKeys[j].Module {
			return tplKeys[i].Module < tplKeys[j].Module
		}
		return tplKeys[i].Name < tplKeys[j].Name
	})
	for _, k := range tplKeys {
		cA, inA := tplA[k]
		cB, inB := tplB[k]
		switch {
		case !inA && inB:
			tplAdded = append(tplAdded, map[string]any{
				"module": k.Module, "name": k.Name, "choices": cB,
			})
		case inA && !inB:
			tplRemoved = append(tplRemoved, map[string]any{
				"module": k.Module, "name": k.Name, "choices": cA,
			})
		case inA && inB:
			added := sortedSliceDiff(cB, cA)
			removed := sortedSliceDiff(cA, cB)
			if len(added) > 0 || len(removed) > 0 {
				tplChanged = append(tplChanged, map[string]any{
					"module":          k.Module,
					"name":            k.Name,
					"choices_added":   added,
					"choices_removed": removed,
				})
			}
		}
	}
	out["templates_added"] = nonNil(tplAdded)
	out["templates_removed"] = nonNil(tplRemoved)
	out["templates_changed"] = nonNil(tplChanged)

	// Interfaces: same shape, plus methods.
	var ifAdded, ifRemoved, ifChanged []map[string]any
	allIfKeys := map[ifKey]struct{}{}
	for k := range ifA {
		allIfKeys[k] = struct{}{}
	}
	for k := range ifB {
		allIfKeys[k] = struct{}{}
	}
	ifKeys := make([]ifKey, 0, len(allIfKeys))
	for k := range allIfKeys {
		ifKeys = append(ifKeys, k)
	}
	sort.Slice(ifKeys, func(i, j int) bool {
		if ifKeys[i].Module != ifKeys[j].Module {
			return ifKeys[i].Module < ifKeys[j].Module
		}
		return ifKeys[i].Name < ifKeys[j].Name
	})
	for _, k := range ifKeys {
		cA, inA := ifA[k]
		cB, inB := ifB[k]
		switch {
		case !inA && inB:
			ifAdded = append(ifAdded, map[string]any{
				"module": k.Module, "name": k.Name,
				"choices": cB.choices, "methods": cB.methods,
			})
		case inA && !inB:
			ifRemoved = append(ifRemoved, map[string]any{
				"module": k.Module, "name": k.Name,
				"choices": cA.choices, "methods": cA.methods,
			})
		case inA && inB:
			cAdded := sortedSliceDiff(cB.choices, cA.choices)
			cRemoved := sortedSliceDiff(cA.choices, cB.choices)
			mAdded := sortedSliceDiff(cB.methods, cA.methods)
			mRemoved := sortedSliceDiff(cA.methods, cB.methods)
			if len(cAdded)+len(cRemoved)+len(mAdded)+len(mRemoved) > 0 {
				ifChanged = append(ifChanged, map[string]any{
					"module":          k.Module,
					"name":            k.Name,
					"choices_added":   cAdded,
					"choices_removed": cRemoved,
					"methods_added":   mAdded,
					"methods_removed": mRemoved,
				})
			}
		}
	}
	out["interfaces_added"] = nonNil(ifAdded)
	out["interfaces_removed"] = nonNil(ifRemoved)
	out["interfaces_changed"] = nonNil(ifChanged)

	return out
}

// nonNil ensures we serialise [] (not null) when a delta bucket is
// empty. Frontend code-paths read `.length` directly; null would
// throw.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// mergeSorted unions two sorted-input string slices and returns a
// new sorted slice. O(n log n) — fine for small choice/method lists.
func mergeSorted(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	for _, s := range a {
		seen[s] = struct{}{}
	}
	for _, s := range b {
		seen[s] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// sortedSliceDiff returns elements of a not in b. Inputs assumed
// sorted; output is sorted.
func sortedSliceDiff(a, b []string) []string {
	bSet := make(map[string]struct{}, len(b))
	for _, s := range b {
		bSet[s] = struct{}{}
	}
	out := make([]string, 0, len(a))
	for _, s := range a {
		if _, ok := bSet[s]; !ok {
			out = append(out, s)
		}
	}
	return out
}

// sortedSetDiff returns elements of a not in b, as a sorted slice.
func sortedSetDiff(a, b map[string]struct{}) []string {
	out := make([]string, 0, len(a))
	for k := range a {
		if _, ok := b[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// ────────────────────────────────────────────────────────────────────
// Per-participant vetting
// ────────────────────────────────────────────────────────────────────

// handleDARVettingList probes every recorded participant for whether
// the given DAR is vetted. We don't have a native "is this DAR
// vetted?" RPC — instead we read ListDars and check whether the
// main id is present. A DAR uploaded with vet_all_packages=true is
// vetted iff it's in the list (the participant prunes unvetted
// DARs lazily, but for the dev-flow this is a faithful signal).
func handleDARVettingList(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := registry.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid instance name", err)
		return
	}
	mainID := r.PathValue("id")
	if !looksLikePackageID(mainID) {
		writeErrorWithCode(w, http.StatusBadRequest,
			ErrCodeInvalidRequest, "invalid package id")
		return
	}
	state, err := registry.Read(name)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			writeErrorWithCode(w, http.StatusNotFound, ErrCodeNotFound,
				"instance "+name+" not registered")
			return
		}
		writeError(w, http.StatusInternalServerError, "read state", err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), inspectRequestTimeout)
	defer cancel()

	type partRow struct {
		Role   string `json:"role"`
		Vetted bool   `json:"vetted"`
		Error  string `json:"error,omitempty"`
	}
	rows := make([]partRow, 0, 3)

	// Iterate roles in a stable order so the UI rendering is
	// deterministic regardless of map iteration order.
	for _, role := range []string{"app-user", "app-provider", "sv"} {
		row := partRow{Role: role}
		portKey := "participant_admin_" + role
		adminPort, hasPort := state.Ports[portKey]
		if !hasPort || adminPort == 0 {
			row.Error = "port not recorded"
			rows = append(rows, row)
			continue
		}
		cred, hasCred := state.Credentials[role]
		if !hasCred {
			row.Error = "no JWT"
			rows = append(rows, row)
			continue
		}
		client, err := admin.Connect(ctx, admin.Config{
			Host:     "localhost:" + strconv.Itoa(adminPort),
			Token:    cred.JWT,
			Insecure: true,
		})
		if err != nil {
			row.Error = "dial: " + err.Error()
			rows = append(rows, row)
			continue
		}
		resp, lerr := client.Package.ListDars(ctx, &adminproto.ListDarsRequest{})
		_ = client.Close()
		if lerr != nil {
			row.Error = "list: " + lerr.Error()
			rows = append(rows, row)
			continue
		}
		for _, d := range resp.GetDars() {
			if d.GetMain() == mainID {
				row.Vetted = true
				break
			}
		}
		rows = append(rows, row)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version": 1,
		"instance":       name,
		"main":           mainID,
		"participants":   rows,
	})
}

// vettingToggleBody is the POST body shape for the vetting toggle
// endpoint. DisallowUnknownFields catches typos like {"Vet": true}
// before they silently no-op against the default-false target.
type vettingToggleBody struct {
	Vetted bool `json:"vetted"`
}

// handleDARVettingToggle flips a single participant's vetting state
// for the given DAR. Maps to VetDar / UnvetDar.
func handleDARVettingToggle(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := registry.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid instance name", err)
		return
	}
	mainID := r.PathValue("id")
	if !looksLikePackageID(mainID) {
		writeErrorWithCode(w, http.StatusBadRequest,
			ErrCodeInvalidRequest, "invalid package id")
		return
	}
	role := r.PathValue("role")
	if !validRole[role] {
		writeErrorWithCode(w, http.StatusBadRequest,
			ErrCodeInvalidRequest,
			"invalid role: "+role,
			"role must be one of app-user, app-provider, sv")
		return
	}

	// Body parse — capped + strict.
	r.Body = http.MaxBytesReader(w, r.Body, vettingPostMax)
	var body vettingToggleBody
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "decode body", err)
		return
	}

	// Lock+re-read on the registry. Writes to state
	// are limited to recording the toggled state, NOT to fields the
	// reconciler is responsible for; the lock is still the canonical
	// guard against torn reads with a concurrent up/down.
	releaseLock, err := registry.Lock(name)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			writeErrorWithCode(w, http.StatusNotFound, ErrCodeNotFound,
				"instance "+name+" not registered")
			return
		}
		writeError(w, http.StatusInternalServerError, "lock state", err)
		return
	}
	defer releaseLock()

	state, err := registry.Read(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read state", err)
		return
	}
	adminPort, cred, ok := requireParticipantAccess(w, state, name, role)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), inspectRequestTimeout)
	defer cancel()

	client, err := admin.Connect(ctx, admin.Config{
		Host:     "localhost:" + strconv.Itoa(adminPort),
		Token:    cred.JWT,
		Insecure: true,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "dial canton admin", err)
		return
	}
	defer func() { _ = client.Close() }()

	if body.Vetted {
		_, err = client.Package.VetDar(ctx, &adminproto.VetDarRequest{
			MainPackageId: mainID,
			Synchronize:   true,
		})
	} else {
		_, err = client.Package.UnvetDar(ctx, &adminproto.UnvetDarRequest{
			MainPackageId: mainID,
		})
	}
	if err != nil {
		lower := strings.ToLower(err.Error())
		// Some Splice/Canton versions don't implement Unvet (or the
		// synchronizer requirement differs). Surface as 412 so the
		// frontend can disable the toggle gracefully rather than
		// rendering it as a generic upstream error.
		if strings.Contains(lower, "unimplemented") ||
			strings.Contains(lower, "not supported") {
			writeErrorWithCode(w, http.StatusPreconditionFailed,
				"VETTING_UNSUPPORTED",
				"vetting toggle not supported on this Splice/Canton version",
				"upgrade the participant or vet via `daml ledger upload-dar` from the CLI")
			return
		}
		writeError(w, http.StatusBadGateway, "vet/unvet dar", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version": 1,
		"instance":       name,
		"main":           mainID,
		"role":           role,
		"vetted":         body.Vetted,
	})
}
