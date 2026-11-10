package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bitdynamics-ab/canton-devkit/internal/api/types"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet"
	"github.com/bitdynamics-ab/canton-devkit/internal/localnet/containers"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/httpsec"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/progress"
	"github.com/bitdynamics-ab/canton-devkit/internal/ui/stream"
)

const upBodyMax = 4 << 10

const upJobTimeout = 30 * time.Minute

const progressBufferCap = 128

func MountInstances(mux *http.ServeMux, hub *stream.Hub) {
	mux.HandleFunc("GET /api/instances", handleList)
	mux.HandleFunc("GET /api/instances/{name}", handleDetail)
	if hub != nil {
		mux.HandleFunc("POST /api/instances", handleCreate(hub))
		mux.HandleFunc("GET /api/instances/{name}/events", handleInstanceEvents(hub))
		mux.HandleFunc("DELETE /api/instances/{name}/up", handleCancelUp(hub))
		mux.HandleFunc("DELETE /api/instances/{name}", handleRemoveInstance(hub))
		mux.HandleFunc("POST /api/instances/{name}/down", handleDownInstance())
		mux.HandleFunc("POST /api/instances/{name}/up", handleResumeInstance(hub))
		mux.HandleFunc("POST /api/instances/{name}/recreate", handleRecreateInstance(hub))
		mux.HandleFunc("POST /api/instances/{name}/observability", handleObservabilityToggle())
	} else {
		stub := func(w http.ResponseWriter, _ *http.Request) {
			writeErrorWithCode(w, http.StatusServiceUnavailable,
				"SSE_DISABLED",
				"create-instance flow disabled (no event hub configured)",
				"start the server with the default config; --no-hub is a test seam")
		}
		mux.HandleFunc("POST /api/instances", stub)
		mux.HandleFunc("GET /api/instances/{name}/events", stub)
		mux.HandleFunc("DELETE /api/instances/{name}/up", stub)
		mux.HandleFunc("DELETE /api/instances/{name}", stub)
		mux.HandleFunc("POST /api/instances/{name}/down", stub)
		mux.HandleFunc("POST /api/instances/{name}/up", stub)
		mux.HandleFunc("POST /api/instances/{name}/recreate", stub)
	}
	mux.HandleFunc("GET /api/instances/{name}/containers", handleInstanceContainers())
	mux.HandleFunc("GET /api/instances/{name}/containers/{container}/logs", handleContainerLogs())
	mux.HandleFunc("POST /api/instances/{name}/containers/{container}/restart", handleContainerRestart())
	mux.HandleFunc("POST /api/instances/{name}/pause", handlePauseInstance(true))
	mux.HandleFunc("POST /api/instances/{name}/resume", handlePauseInstance(false))
	mux.HandleFunc("POST /api/instances/{name}/stop", handleStopInstance())
	if hub != nil {
		mux.HandleFunc("POST /api/instances/{name}/start", handleStartInstance(hub))
	} else {
		mux.HandleFunc("POST /api/instances/{name}/start", func(w http.ResponseWriter, _ *http.Request) {
			writeErrorWithCode(w, http.StatusServiceUnavailable,
				"SSE_DISABLED",
				"start flow disabled (no event hub configured)",
				"start the server with the default config; --no-hub is a test seam")
		})
	}
}

func handleStopInstance() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := registry.ValidateName(name); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest,
				"invalid instance name: "+err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), downTimeout)
		defer cancel()

		var outBuf, errBuf bytes.Buffer
		exit := localnet.RunStop(ctx, &outBuf, &errBuf, &localnet.StopOptions{Name: name})
		if exit == localnet.ExitSuccess {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		status := http.StatusInternalServerError
		switch exit {
		case localnet.ExitUserError:
			status = http.StatusBadRequest
		case localnet.ExitTimeout:
			status = http.StatusRequestTimeout
		}
		cause := firstNonWarningLine(errBuf.String())
		if cause == "" {
			cause = "operation failed with exit code " + strconv.FormatUint(uint64(exit), 10)
		}
		writeErrorWithCode(w, status, "STOP_FAILED", cause)
	}
}

var (
	listContainers = containers.List
	runStart       = localnet.RunStart
	runUp          = localnet.RunUp
)

func handleStartInstance(hub *stream.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := localnet.ValidateName(name); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest,
				"invalid instance name: "+err.Error())
			return
		}

		state, err := registry.Read(name)
		if err != nil {
			if errors.Is(err, registry.ErrNotFound) {
				writeErrorWithCode(w, http.StatusNotFound, ErrCodeNotFound,
					"instance "+name+" not registered",
					"create it first via POST /api/instances")
				return
			}
			writeError(w, http.StatusInternalServerError, "read state", err)
			return
		}
		switch state.Status {
		case registry.StatusRunning:
			w.WriteHeader(http.StatusNoContent)
			return
		case registry.StatusPaused:
			writeErrorWithCode(w, http.StatusConflict, "INSTANCE_PAUSED",
				"instance "+name+" is paused",
				"resume it via POST /api/instances/"+name+"/resume")
			return
		}

		existing, listErr := listContainers(r.Context(), state.ComposeProject)
		if listErr == nil && len(existing) > 0 {
			ctx, cancel := context.WithTimeout(r.Context(), downTimeout)
			defer cancel()
			var outBuf, errBuf bytes.Buffer
			exit := runStart(ctx,
				localnet.NewTextProgress(io.Discard, io.Discard),
				&outBuf, &errBuf,
				&localnet.StartOptions{Name: name, SkipWait: true})
			if exit == localnet.ExitSuccess {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			status := http.StatusInternalServerError
			switch exit {
			case localnet.ExitUserError:
				status = http.StatusBadRequest
			case localnet.ExitTimeout:
				status = http.StatusRequestTimeout
			}
			cause := firstNonWarningLine(errBuf.String())
			if cause == "" {
				cause = "operation failed with exit code " + strconv.FormatUint(uint64(exit), 10)
			}
			writeErrorWithCode(w, status, "START_FAILED", cause)
			return
		}

		startBringUp(w, name, state.SpliceVersion, state.Profiles, hub)
	}
}

func startBringUp(w http.ResponseWriter, name, version string, profiles []string, hub *stream.Hub) {
	jobCtx, cancelJob := context.WithTimeout(context.Background(), upJobTimeout)
	if !jobs.Register(name, cancelJob) {
		cancelJob()
		writeErrorWithCode(w, http.StatusConflict,
			"INSTANCE_CREATING",
			"instance "+name+" is already being brought up",
			"open /api/instances/"+name+"/events to watch the existing run")
		return
	}

	topic := progress.TopicFor(name)
	hub.EnableBuffering(topic, progressBufferCap)

	opts := &localnet.UpOptions{Name: name, Version: version, Profiles: profiles}
	go func() {
		defer cancelJob()
		defer hub.ClearBuffer(topic)
		defer jobs.Unregister(name)
		prog := progress.New(hub, name)
		exitCode := runUp(jobCtx, prog, opts)
		log.Printf("start (bring-up) instance %q: exit_code=%d", name, exitCode)
	}()

	writeJSON(w, http.StatusAccepted, upAcceptedResponse{
		SchemaVersion: types.SchemaVersion,
		Instance:      name,
		EventsURL:     "/api/instances/" + name + "/events",
	})
}

func handlePauseInstance(pause bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := registry.ValidateName(name); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest, ErrCodeInvalidRequest,
				"invalid instance name: "+err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), downTimeout)
		defer cancel()

		var outBuf, errBuf bytes.Buffer
		opts := &localnet.PauseOptions{Name: name}
		var exit int
		if pause {
			exit = localnet.RunPause(ctx, &outBuf, &errBuf, opts)
		} else {
			exit = localnet.RunResume(ctx, &outBuf, &errBuf, opts)
		}
		if exit == localnet.ExitSuccess {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		status := http.StatusInternalServerError
		switch exit {
		case localnet.ExitUserError:
			status = http.StatusBadRequest
		case localnet.ExitTimeout:
			status = http.StatusRequestTimeout
		}
		cause := firstNonWarningLine(errBuf.String())
		if cause == "" {
			cause = "operation failed with exit code " + strconv.FormatUint(uint64(exit), 10)
		}
		writeErrorWithCode(w, status, "PAUSE_FAILED", cause)
	}
}

func handleList(w http.ResponseWriter, _ *http.Request) {
	idx, err := registry.ReadIndex()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read registry index", err)
		return
	}
	resp := types.ListResponse{
		SchemaVersion: types.SchemaVersion,
		Instances:     make([]types.InstanceSummary, 0, len(idx.Entries)),
	}
	var unreadable []string
	for _, e := range idx.Entries {
		row := types.InstanceSummary{
			Name:          e.Name,
			Status:        string(e.Status),
			SpliceVersion: e.SpliceVersion,
			StartedAgo:    localnet.InstanceStartedAgo(e.CreatedAt, e.Status),
		}
		if s, err := registry.Read(e.Name); err == nil {
			row.Ports = formatPortRange(s.Ports)
		} else {
			row.Ports = formatPortRange(nil)
			unreadable = append(unreadable, e.Name)
		}
		resp.Instances = append(resp.Instances, row)
	}
	sort.Slice(resp.Instances, func(i, j int) bool {
		return resp.Instances[i].Name < resp.Instances[j].Name
	})
	if len(unreadable) > 0 {
		resp.Warning = "unreadable per-instance state files: " +
			strings.Join(unreadable, ", ")
	}
	writeJSON(w, http.StatusOK, resp)
}

func handleDetail(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := registry.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid instance name", err)
		return
	}
	inst, err := localnet.CollectStatus(r.Context(), name, true)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			writeError(w, http.StatusNotFound, "instance not registered", err)
			return
		}
		writeError(w, http.StatusInternalServerError, "read state", err)
		return
	}
	writeJSON(w, http.StatusOK, inst)
}

func formatPortRange(ports map[string]int) string {
	if len(ports) == 0 {
		return "—"
	}
	allowlist := []string{
		"app_user_ui", "app_provider_ui", "sv_ui",
		"swagger_ui", "postgres",
	}
	var lo, hi int
	first := true
	for _, k := range allowlist {
		p, ok := ports[k]
		if !ok || p <= 0 {
			continue
		}
		if first || p < lo {
			lo = p
		}
		if first || p > hi {
			hi = p
		}
		first = false
	}
	if first {
		return "—"
	}
	if lo == hi {
		return strconv.Itoa(lo)
	}
	return strconv.Itoa(lo) + "–" + strconv.Itoa(hi)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(body)
}

type errorBody struct {
	Code        string   `json:"code"`
	Error       string   `json:"error"`
	Detail      string   `json:"detail,omitempty"`
	Remediation []string `json:"remediation,omitempty"`
}

// Stable error-code tokens. Mirror the ErrorCode constants in
// internal/localnet/friendly_errors.go where applicable; new codes
// here belong in the docs at devkit.dev/e/<CODE>.
const (
	ErrCodeInvalidRequest  = "INVALID_REQUEST"
	ErrCodeForbidden       = "FORBIDDEN"
	ErrCodeNotFound        = "NOT_FOUND"
	ErrCodeInternal        = "INTERNAL"
	ErrCodeRegistry        = "REGISTRY_READ_FAILED"
	ErrCodeUnknownRole     = "UNKNOWN_ROLE"
	ErrCodeUnknownFormat   = "UNKNOWN_FORMAT"
	ErrCodeRequestTooLarge = "REQUEST_TOO_LARGE"
)

func writeError(w http.ResponseWriter, status int, summary string, cause error) {
	if cause != nil && status >= 500 {
		log.Printf("handler error: status=%d code=%s summary=%q cause=%v",
			status, codeForStatus(status), summary, cause)
	}
	writeJSON(w, status, errorBody{
		Code:  codeForStatus(status),
		Error: summary,
	})
}

func writeErrorWithCode(w http.ResponseWriter, status int, code, summary string, remediation ...string) {
	writeJSON(w, status, errorBody{
		Code:        code,
		Error:       summary,
		Remediation: remediation,
	})
}

func codeForStatus(status int) string {
	switch {
	case status == http.StatusNotFound:
		return ErrCodeNotFound
	case status == http.StatusBadRequest:
		return ErrCodeInvalidRequest
	case status == http.StatusRequestEntityTooLarge:
		return ErrCodeRequestTooLarge
	case status == http.StatusConflict:
		return "INSTANCE_EXISTS"
	case status >= 500:
		return ErrCodeInternal
	default:
		return ErrCodeInvalidRequest
	}
}

type upRequest struct {
	Name              string   `json:"name"`
	Version           string   `json:"version,omitempty"`
	AllowUncurated    bool     `json:"allow_uncurated,omitempty"`
	Profiles          []string `json:"profiles,omitempty"`
	PortBase          int      `json:"port_base,omitempty"`
	ObservabilityMode string   `json:"observability_mode,omitempty"`
}

var allowedProfiles = func() map[string]bool {
	m := make(map[string]bool, len(localnet.KnownProfiles()))
	for _, p := range localnet.KnownProfiles() {
		m[p] = true
	}
	return m
}()

type upAcceptedResponse struct {
	SchemaVersion int    `json:"schema_version"`
	Instance      string `json:"instance"`
	EventsURL     string `json:"events_url"`
}

func handleCreate(hub *stream.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, upBodyMax)

		var req upRequest
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				writeErrorWithCode(w, http.StatusRequestEntityTooLarge,
					ErrCodeRequestTooLarge,
					"request body too large",
					"the create-instance body should be tiny — check you didn't paste binary data")
				return
			}
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid request body",
				"the body must be JSON: {\"name\":\"<dns-label>\", \"version\":\"<tag>\"?}")
			return
		}

		if err := localnet.ValidateName(req.Name); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid instance name: "+err.Error(),
				"names must be lowercase DNS labels (a-z, 0-9, hyphen); 1–63 chars; can't start or end with hyphen")
			return
		}

		if err := localnet.ValidateObservabilityMode(localnet.ObservabilityMode(req.ObservabilityMode)); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				err.Error(),
				"observability_mode must be one of: auto, shared, per-instance")
			return
		}

		if _, err := registry.Read(req.Name); err == nil {
			writeErrorWithCode(w, http.StatusConflict,
				"INSTANCE_EXISTS",
				"instance "+req.Name+" already exists",
				"pick a different name, or stop the existing one first via `dpm localnet down "+req.Name+"`")
			return
		}
		if jobs.Active(req.Name) {
			writeErrorWithCode(w, http.StatusConflict,
				"INSTANCE_CREATING",
				"instance "+req.Name+" is already being created",
				"open the progress stream at /api/instances/"+req.Name+"/events to watch the existing run")
			return
		}

		if gateVersion, ok := resolveForGate(r.Context(), req.Version, req.AllowUncurated); ok {
			report := runPreflightForVersion(r.Context(), gateVersion)
			if !report.OK {
				w.Header().Set("X-Preflight-Failed", "1")
				writeJSON(w, http.StatusUnprocessableEntity, report)
				return
			}
		} else if !req.AllowUncurated {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"unknown splice version: "+req.Version,
				"call GET /api/splice/versions for the curated list, or set allow_uncurated to bypass")
			return
		}

		topic := progress.TopicFor(req.Name)
		hub.EnableBuffering(topic, progressBufferCap)

		jobCtx, cancelJob := context.WithTimeout(context.Background(), upJobTimeout)
		if !jobs.Register(req.Name, cancelJob) {
			cancelJob()
			hub.ClearBuffer(topic)
			writeErrorWithCode(w, http.StatusConflict,
				"INSTANCE_CREATING",
				"instance "+req.Name+" is already being created",
				"open /api/instances/"+req.Name+"/events to watch the existing run")
			return
		}

		if err := localnet.ValidateProfiles(req.Profiles); err != nil {
			cancelJob()
			hub.ClearBuffer(topic)
			jobs.Unregister(req.Name)
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				err.Error(),
				"supported profiles: "+strings.Join(localnet.KnownProfiles(), ", "))
			return
		}

		if req.PortBase != 0 {
			nPorts := len(localnet.UIPortEnvVars())
			for _, p := range req.Profiles {
				if p == localnet.ObservabilityProfileName {
					nPorts += len(localnet.ObservabilityPortEnvVars())
				}
			}
			maxBase := 65535 - nPorts
			if req.PortBase < localnet.MinPortBase || req.PortBase > maxBase {
				cancelJob()
				hub.ClearBuffer(topic)
				jobs.Unregister(req.Name)
				writeErrorWithCode(w, http.StatusBadRequest,
					ErrCodeInvalidRequest,
					fmt.Sprintf("port_base %d is out of range", req.PortBase),
					fmt.Sprintf("use 0 (auto) or a base in %d..%d", localnet.MinPortBase, maxBase))
				return
			}
		}

		opts := &localnet.UpOptions{
			Name:              req.Name,
			Version:           req.Version,
			AllowUncurated:    req.AllowUncurated,
			Profiles:          req.Profiles,
			PortBase:          req.PortBase,
			ObservabilityMode: localnet.ObservabilityMode(req.ObservabilityMode),
		}

		go func() {
			defer cancelJob()
			defer hub.ClearBuffer(topic)
			defer jobs.Unregister(req.Name)

			prog := progress.New(hub, req.Name)
			exitCode := runUp(jobCtx, prog, opts)
			log.Printf("create instance %q: exit_code=%d", req.Name, exitCode)
		}()

		writeJSON(w, http.StatusAccepted, upAcceptedResponse{
			SchemaVersion: types.SchemaVersion,
			Instance:      req.Name,
			EventsURL:     "/api/instances/" + req.Name + "/events",
		})
	}
}

const downTimeout = 3 * time.Minute

type downRequest struct{}

func handleResumeInstance(hub *stream.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := localnet.ValidateName(name); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid instance name: "+err.Error())
			return
		}
		jobCtx, cancelJob := context.WithTimeout(context.Background(), upJobTimeout)
		if !jobs.Register(name, cancelJob) {
			cancelJob()
			writeErrorWithCode(w, http.StatusConflict,
				"INSTANCE_CREATING",
				"instance "+name+" is already being brought up",
				"open /api/instances/"+name+"/events to watch the existing run")
			return
		}

		prior, err := registry.Read(name)
		if err != nil {
			jobs.Unregister(name)
			cancelJob()
			if errors.Is(err, registry.ErrNotFound) {
				writeErrorWithCode(w, http.StatusNotFound,
					ErrCodeNotFound,
					"instance "+name+" not registered",
					"create it first via POST /api/instances")
				return
			}
			writeError(w, http.StatusInternalServerError, "read state", err)
			return
		}
		if prior.Status == registry.StatusRunning {
			jobs.Unregister(name)
			cancelJob()
			writeErrorWithCode(w, http.StatusConflict,
				"INSTANCE_RUNNING",
				"instance "+name+" is already running",
				"to restart, stop it first via POST /api/instances/"+name+"/down")
			return
		}

		topic := progress.TopicFor(name)
		hub.EnableBuffering(topic, progressBufferCap)

		opts := &localnet.UpOptions{
			Name:    name,
			Version: prior.SpliceVersion,
		}

		go func() {
			defer cancelJob()
			defer hub.ClearBuffer(topic)
			defer jobs.Unregister(name)
			prog := progress.New(hub, name)
			exitCode := runUp(jobCtx, prog, opts)
			log.Printf("resume instance %q: exit_code=%d", name, exitCode)
		}()

		writeJSON(w, http.StatusAccepted, upAcceptedResponse{
			SchemaVersion: types.SchemaVersion,
			Instance:      name,
			EventsURL:     "/api/instances/" + name + "/events",
		})
	}
}

func profilesFromComposeFiles(files []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range files {
		base := f
		if i := strings.LastIndexByte(f, '/'); i >= 0 {
			base = f[i+1:]
		}
		switch base {
		case "observability.yaml":
			if !seen[localnet.ObservabilityProfileName] {
				out = append(out, localnet.ObservabilityProfileName)
				seen[localnet.ObservabilityProfileName] = true
			}
		case "tokens-v2.yaml":
			if !seen[localnet.TokensV2ProfileName] {
				out = append(out, localnet.TokensV2ProfileName)
				seen[localnet.TokensV2ProfileName] = true
			}
		}
	}
	return out
}

func handleRecreateInstance(hub *stream.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := localnet.ValidateName(name); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid instance name: "+err.Error())
			return
		}

		jobCtx, cancelJob := context.WithTimeout(context.Background(),
			upJobTimeout+downTimeout)
		if !jobs.Register(name, cancelJob) {
			cancelJob()
			writeErrorWithCode(w, http.StatusConflict,
				"INSTANCE_CREATING",
				"instance "+name+" is already being created or restarted",
				"open /api/instances/"+name+"/events to watch the existing run")
			return
		}

		prior, err := registry.Read(name)
		if err != nil {
			jobs.Unregister(name)
			cancelJob()
			if errors.Is(err, registry.ErrNotFound) {
				writeErrorWithCode(w, http.StatusNotFound,
					ErrCodeNotFound,
					"instance "+name+" not registered",
					"create it first via POST /api/instances")
				return
			}
			writeError(w, http.StatusInternalServerError, "read state", err)
			return
		}

		topic := progress.TopicFor(name)
		hub.EnableBuffering(topic, progressBufferCap)

		recordedVersion := prior.SpliceVersion
		recordedProfiles := prior.Profiles
		if len(recordedProfiles) == 0 {
			recordedProfiles = profilesFromComposeFiles(prior.ComposeFiles)
		}

		go func() {
			defer cancelJob()
			defer hub.ClearBuffer(topic)
			defer jobs.Unregister(name)
			recreateWork(jobCtx, hub, name, recordedVersion, recordedProfiles)
		}()

		writeJSON(w, http.StatusAccepted, upAcceptedResponse{
			SchemaVersion: types.SchemaVersion,
			Instance:      name,
			EventsURL:     "/api/instances/" + name + "/events",
		})
	}
}

var recreateWork = realRecreateWork

func realRecreateWork(ctx context.Context, hub *stream.Hub, name, version string, profiles []string) {
	prog := progress.New(hub, name)

	var downOut, downErr bytes.Buffer
	downExit := localnet.RunDown(ctx, &downOut, &downErr,
		&localnet.DownOptions{Name: name})
	if downExit != localnet.ExitSuccess {
		log.Printf("restart instance %q: down phase failed exit=%d err=%s",
			name, downExit, downErr.String())
		prog.Warn("down phase failed during restart: " +
			firstNonWarningLine(downErr.String()))
	}

	upOpts := &localnet.UpOptions{
		Name:     name,
		Version:  version,
		Profiles: profiles,
	}
	exitCode := runUp(ctx, prog, upOpts)
	log.Printf("restart instance %q: down_exit=%d up_exit=%d",
		name, downExit, exitCode)
}

func handleDownInstance() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := registry.ValidateName(name); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid instance name: "+err.Error())
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, upBodyMax)
		var req downRequest
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil && err != io.EOF {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid request body",
				"the body should be empty or {}")
			return
		}

		if jobs.Active(name) {
			writeErrorWithCode(w, http.StatusConflict,
				"INSTANCE_CREATING",
				"instance "+name+" is being created — cancel the bring-up first",
				"call DELETE /api/instances/"+name+"/up to cancel, then retry")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), downTimeout)
		defer cancel()

		var outBuf, errBuf bytes.Buffer
		exit := localnet.RunDown(ctx, &outBuf, &errBuf, &localnet.DownOptions{
			Name: name,
		})

		if exit == localnet.ExitSuccess {
			log.Printf("down instance %q: ok", name)
			w.WriteHeader(http.StatusNoContent)
			return
		}

		status := http.StatusInternalServerError
		switch exit {
		case localnet.ExitUserError:
			status = http.StatusBadRequest
		case localnet.ExitTimeout:
			status = http.StatusRequestTimeout
		}
		cause := errBuf.String()
		if cause == "" {
			cause = "down failed with exit code " + strconv.FormatUint(uint64(exit), 10)
		}
		log.Printf("down instance %q: exit=%d err=%s", name, exit, cause)
		writeErrorWithCode(w, status,
			"DOWN_FAILED",
			"failed to stop "+name+": "+firstNonWarningLine(cause),
			"the docker compose down output is in the server log; "+
				"try `dpm localnet down "+name+"` from a terminal for full output")
	}
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}

func firstNonWarningLine(s string) string {
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '\n' {
			line := s[start:i]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			if !looksLikeWarningLine(line) && line != "" {
				return line
			}
			start = i + 1
		}
	}
	return firstLine(s)
}

func looksLikeWarningLine(line string) bool {
	for len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
		line = line[1:]
	}
	switch {
	case len(line) >= 8 && (line[:8] == "Warning:" || line[:8] == "warning:" || line[:8] == "WARNING:"):
		return true
	case len(line) >= 5 && (line[:5] == "WARN:" || line[:5] == "warn:" || line[:5] == "Warn:"):
		return true
	}
	return false
}

type ContainerHealth struct {
	Name    string `json:"name"`
	Service string `json:"service"`
	State   string `json:"state"`
	Health  string `json:"health,omitempty"`
	Status  string `json:"status"`
	Image   string `json:"image,omitempty"`
}

type ContainersResponse struct {
	SchemaVersion   int               `json:"schema_version"`
	Instance        string            `json:"instance"`
	Containers      []ContainerHealth `json:"containers"`
	HealthyCount    int               `json:"healthy_count"`
	StartingCount   int               `json:"starting_count"`
	UnhealthyCount  int               `json:"unhealthy_count"`
	RestartingCount int               `json:"restarting_count"`
	ExitedCount     int               `json:"exited_count"`

	CPUPercent    *float64 `json:"cpu_percent,omitempty"`
	MemUsedBytes  *int64   `json:"mem_used_bytes,omitempty"`
	MemLimitBytes *int64   `json:"mem_limit_bytes,omitempty"`
}

func handleInstanceContainers() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := registry.ValidateName(name); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid instance name: "+err.Error())
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

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		health, runErr := containersList(ctx, state.ComposeProject)
		if runErr != nil {
			writeErrorWithCode(w, http.StatusServiceUnavailable,
				"DOCKER_PROBE_FAILED",
				"could not query docker for compose project "+state.ComposeProject,
				runErr.Error())
			return
		}

		resp := ContainersResponse{
			SchemaVersion: types.SchemaVersion,
			Instance:      name,
			Containers:    health,
		}
		for _, c := range health {
			switch c.Health {
			case "healthy":
				resp.HealthyCount++
			case "starting":
				resp.StartingCount++
			case "unhealthy":
				resp.UnhealthyCount++
			}
			switch c.State {
			case "restarting":
				resp.RestartingCount++
			case "exited", "dead":
				resp.ExitedCount++
			}
		}

		if len(health) > 0 {
			names := make([]string, len(health))
			for i, c := range health {
				names[i] = c.Name
			}
			statCtx, statCancel := context.WithTimeout(r.Context(), 4*time.Second)
			if stats, err := containers.Stats(statCtx, names); err == nil {
				resp.CPUPercent, resp.MemUsedBytes, resp.MemLimitBytes = aggregateContainerStats(health, stats)
			}
			statCancel()
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func aggregateContainerStats(health []ContainerHealth, stats map[string]containers.Stat) (cpu *float64, used, limit *int64) {
	var cpuSum float64
	var usedSum, limitSum int64
	var any bool
	for _, c := range health {
		if s, ok := stats[c.Name]; ok {
			cpuSum += s.CPUPct
			usedSum += s.MemBytes
			limitSum += s.MemLimit
			any = true
		}
	}
	if !any {
		return nil, nil, nil
	}
	used = &usedSum
	cpu = &cpuSum
	if limitSum > 0 {
		limit = &limitSum
	}
	return cpu, used, limit
}

func containersList(ctx context.Context, project string) ([]ContainerHealth, error) {
	infos, err := containers.List(ctx, project)
	if err != nil {
		return nil, err
	}
	out := make([]ContainerHealth, 0, len(infos))
	for _, c := range infos {
		out = append(out, ContainerHealth{
			Name:    c.Name,
			Service: c.Service,
			State:   c.State,
			Health:  c.Health,
			Status:  c.Status,
			Image:   c.Image,
		})
	}
	return out, nil
}

func handleContainerLogs() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		container := r.PathValue("container")
		if err := registry.ValidateName(name); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid instance name: "+err.Error())
			return
		}
		if !validContainerName(container) {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid container name",
				"container names are alphanumeric + hyphens; arbitrary shell metachars rejected")
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

		probeCtx, cancelProbe := context.WithTimeout(r.Context(), 5*time.Second)
		belongsErr := containers.BelongsToProject(probeCtx, state.ComposeProject, container)
		cancelProbe()
		if belongsErr != nil {
			if errors.Is(belongsErr, containers.ErrContainerNotInProject) {
				writeErrorWithCode(w, http.StatusNotFound,
					ErrCodeNotFound,
					"container "+container+" not in compose project "+state.ComposeProject)
				return
			}
			writeErrorWithCode(w, http.StatusServiceUnavailable,
				"DOCKER_PROBE_FAILED",
				"could not query docker", belongsErr.Error())
			return
		}

		tail := 200
		if t := r.URL.Query().Get("tail"); t != "" {
			if n, err := parseClampedInt(t, 10, 2000); err == nil {
				tail = n
			}
		}
		since := r.URL.Query().Get("since")
		if since != "" && !validDuration(since) {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid since duration",
				"format: <number><h|m|s>, e.g. 5m, 30s, 2h")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		body, err := containers.Logs(ctx, container, containers.LogsOptions{
			Tail:  tail,
			Since: since,
		})
		if err != nil && body == "" {
			writeErrorWithCode(w, http.StatusServiceUnavailable,
				"DOCKER_LOGS_FAILED",
				"could not tail container logs", err.Error())
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}
}

func handleContainerRestart() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		container := r.PathValue("container")
		if err := registry.ValidateName(name); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid instance name: "+err.Error())
			return
		}
		if !validContainerName(container) {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid container name")
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

		probeCtx, cancelProbe := context.WithTimeout(r.Context(), 5*time.Second)
		belongsErr := containers.BelongsToProject(probeCtx, state.ComposeProject, container)
		cancelProbe()
		if belongsErr != nil {
			if errors.Is(belongsErr, containers.ErrContainerNotInProject) {
				writeErrorWithCode(w, http.StatusNotFound,
					ErrCodeNotFound,
					"container "+container+" not in compose project "+state.ComposeProject)
				return
			}
			writeErrorWithCode(w, http.StatusServiceUnavailable,
				"DOCKER_PROBE_FAILED",
				"could not query docker", belongsErr.Error())
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := containers.Restart(ctx, container); err != nil {
			log.Printf("restart container %q: %v", container, err)
			writeErrorWithCode(w, http.StatusInternalServerError,
				"CONTAINER_RESTART_FAILED",
				"failed to restart "+container+": "+firstLine(err.Error()))
			return
		}
		log.Printf("restart container %q: ok", container)
		w.WriteHeader(http.StatusNoContent)
	}
}

func validContainerName(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.'
		if !ok {
			return false
		}
	}
	return true
}

func validDuration(s string) bool {
	if len(s) < 2 || len(s) > 6 {
		return false
	}
	last := s[len(s)-1]
	if last != 'h' && last != 'm' && last != 's' {
		return false
	}
	for i := 0; i < len(s)-1; i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func parseClampedInt(s string, lo, hi int) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	if n < lo {
		return lo, nil
	}
	if n > hi {
		return hi, nil
	}
	return n, nil
}

type runRemoveFunc func(context.Context, io.Writer, io.Writer, *localnet.RemoveOptions) int

func handleRemoveInstance(hub *stream.Hub) http.HandlerFunc {
	return handleRemoveInstanceWithRunner(hub, localnet.RunRemove)
}

func handleRemoveInstanceWithRunner(hub *stream.Hub, runRemove runRemoveFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := registry.ValidateName(name); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid instance name: "+err.Error())
			return
		}

		if jobs.Active(name) {
			writeErrorWithCode(w, http.StatusConflict,
				"INSTANCE_CREATING",
				"instance "+name+" is being created — cancel the bring-up first",
				"call DELETE /api/instances/"+name+"/up to cancel, then retry this DELETE")
			return
		}

		state, err := registry.Read(name)
		if err != nil {
			if errors.Is(err, registry.ErrNotFound) {
				idx, ierr := registry.ReadIndex()
				if ierr != nil {
					writeError(w, http.StatusInternalServerError, "read registry index", ierr)
					return
				}
				indexed := false
				for _, e := range idx.Entries {
					if e.Name == name {
						indexed = true
						break
					}
				}
				if !indexed {
					writeErrorWithCode(w, http.StatusNotFound,
						ErrCodeNotFound,
						"instance "+name+" not registered")
					return
				}
			} else {
				writeError(w, http.StatusInternalServerError, "read state", err)
				return
			}
		}

		force := r.URL.Query().Get("force") == "true"

		if state != nil && state.Status == registry.StatusRunning && !force {
			writeErrorWithCode(w, http.StatusConflict,
				"INSTANCE_RUNNING",
				"instance "+name+" is running — stop it first",
				"retry with ?force=true to stop and remove it, "+
					"or run `dpm localnet down "+name+"` from a terminal")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), downTimeout)
		defer cancel()

		var outBuf, errBuf bytes.Buffer
		exit := runRemove(ctx, &outBuf, &errBuf, &localnet.RemoveOptions{Name: name, Force: force})
		if exit == localnet.ExitSuccess {
			hub.ClearBuffer(progress.TopicFor(name))
			log.Printf("remove instance %q via DELETE: ok", name)
			w.WriteHeader(http.StatusNoContent)
			return
		}

		status := http.StatusInternalServerError
		switch exit {
		case localnet.ExitUserError:
			status = http.StatusConflict
		case localnet.ExitTimeout:
			status = http.StatusRequestTimeout
		}
		cause := errBuf.String()
		if cause == "" {
			cause = "remove failed with exit code " + strconv.FormatUint(uint64(exit), 10)
		}
		log.Printf("remove instance %q: exit=%d out=%s err=%s", name, exit, outBuf.String(), cause)
		writeErrorWithCode(w, status,
			"REMOVE_FAILED",
			"failed to remove "+name+": "+firstNonWarningLine(cause),
			"retry with `dpm localnet remove "+name+"` from a terminal for full output")
	}
}

func handleInstanceEvents(hub *stream.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := registry.ValidateName(name); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid instance name: "+err.Error())
			return
		}

		if origin := r.Header.Get("Origin"); origin != "" {
			if err := httpsec.CheckOriginAgainstHost(
				origin, r.Header.Get("Referer"), r.Host); err != nil {
				writeErrorWithCode(w, http.StatusForbidden,
					ErrCodeForbidden, "forbidden: "+err.Error())
				return
			}
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			writeError(w, http.StatusInternalServerError,
				"streaming unsupported", nil)
			return
		}

		h := w.Header()
		h.Set("Content-Type", "text/event-stream; charset=utf-8")
		h.Set("Cache-Control", "no-cache, no-transform")
		h.Set("Connection", "keep-alive")
		h.Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		topic := progress.TopicFor(name)
		ch, cancel := hub.SubscribeWithReplay(topic)
		defer cancel()

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				writeInstanceEventFrame(w, ev)
				flusher.Flush()
			case <-ticker.C:
				_, _ = w.Write([]byte(": keepalive\n\n"))
				flusher.Flush()
			}
		}
	}
}

// We DO NOT emit an `event:` line. The hub's Topic field is
// internal routing data (e.g. "instance:test"); when emitted on
// the wire it becomes a NAMED SSE event, which EventSource's
// onmessage handler ignores by spec (only the default-type events
// fire onmessage; named events require addEventListener(name, …)).
//
// Per spec, multi-line data needs one "data:" prefix per line; we
// strip a trailing newline so the payload doesn't gain a trailing
// empty data: line.
func writeInstanceEventFrame(w http.ResponseWriter, e stream.Event) {
	if e.ID != "" {
		_, _ = fmt.Fprintf(w, "id: %s\n", e.ID)
	}
	if len(e.Data) == 0 {
		_, _ = w.Write([]byte("data:\n\n"))
		return
	}
	body := strings.TrimRight(string(e.Data), "\n")
	for _, line := range strings.Split(body, "\n") {
		_, _ = fmt.Fprintf(w, "data: %s\n", line)
	}
	_, _ = w.Write([]byte("\n"))
}

func handleCancelUp(hub *stream.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := registry.ValidateName(name); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid instance name: "+err.Error())
			return
		}

		if !jobs.Active(name) {
			writeErrorWithCode(w, http.StatusNotFound,
				ErrCodeNotFound,
				"no in-flight create job for "+name,
				"the up may have already finished — refresh the instance list to check")
			return
		}

		progress.PublishCancelled(hub, name, "user requested via DELETE")

		jobs.Cancel(name)

		log.Printf("cancel instance %q via DELETE", name)
		w.WriteHeader(http.StatusNoContent)
	}
}

const observabilityToggleTimeout = 90 * time.Second

type observabilityToggleRequest struct {
	Prometheus *bool `json:"prometheus,omitempty"`
	Grafana    *bool `json:"grafana,omitempty"`
	Enabled    *bool `json:"enabled,omitempty"`
}

func (r observabilityToggleRequest) resolveTargets() (prom, graf bool, ok bool) {
	if r.Enabled != nil {
		prom = *r.Enabled
		graf = *r.Enabled
		ok = true
	}
	if r.Prometheus != nil {
		prom = *r.Prometheus
		ok = true
	}
	if r.Grafana != nil {
		graf = *r.Grafana
		ok = true
	}
	return
}

func handleObservabilityToggle() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := localnet.ValidateName(name); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid instance name: "+err.Error())
			return
		}
		var req observabilityToggleRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"invalid request body",
				`expected {"prometheus": true|false, "grafana": true|false} `+
					`(legacy {"enabled": true|false} also accepted)`)
			return
		}
		wantProm, wantGraf, ok := req.resolveTargets()
		if !ok {
			writeErrorWithCode(w, http.StatusBadRequest,
				ErrCodeInvalidRequest,
				"request body must set at least one of "+
					`"prometheus", "grafana", or "enabled"`)
			return
		}

		release, lerr := registry.Lock(name)
		if lerr != nil {
			writeErrorWithCode(w, http.StatusConflict,
				"INSTANCE_BUSY",
				"instance "+name+" is busy — another op holds the lock",
				"retry once the in-flight operation finishes")
			return
		}
		defer release()

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
		if state.Status != registry.StatusRunning {
			writeErrorWithCode(w, http.StatusConflict,
				"INSTANCE_NOT_RUNNING",
				"instance "+name+" is not running (status="+string(state.Status)+")",
				"bring it up first via POST /api/instances/"+name+"/up")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), observabilityToggleTimeout)
		defer cancel()

		var logBuf bytes.Buffer
		res, err := localnet.SetObservability(ctx, state, wantProm, wantGraf, &logBuf)
		if logBuf.Len() > 0 {
			log.Printf("observability toggle %q: %s", name, strings.TrimSpace(logBuf.String()))
		}
		if err != nil {
			log.Printf("observability toggle %q failed: %v", name, err)
			svc := failedObservabilityService(err)
			writeErrorWithCode(w, http.StatusBadGateway,
				"OBSERVABILITY_TOGGLE_FAIL",
				"failed to apply observability toggle: "+truncateForUser(err.Error()),
				"check `docker compose -p "+state.ComposeProject+" ps` and `docker logs "+state.ComposeProject+"-"+svc+"`")
			return
		}

		resp := map[string]any{
			"schema_version": types.SchemaVersion,
			"instance":       name,
			"prometheus":     res.Prometheus,
			"grafana":        res.Grafana,
			"enabled":        res.Prometheus && res.Grafana,
		}
		if res.PrometheusPort != 0 {
			resp["prometheus_ui"] = res.PrometheusPort
		}
		if res.GrafanaPort != 0 {
			resp["grafana_ui"] = res.GrafanaPort
		}
		if res.Warning != "" {
			resp["warning"] = res.Warning
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func failedObservabilityService(err error) string {
	if err != nil {
		msg := err.Error()
		if strings.HasPrefix(msg, "enable grafana") || strings.HasPrefix(msg, "disable grafana") {
			return "grafana"
		}
	}
	return "prometheus"
}

func truncateForUser(s string) string {
	const max = 400
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
