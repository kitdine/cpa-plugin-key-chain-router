package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
    void* ptr;
    size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);

typedef struct {
    uint32_t abi_version;
    void* host_ctx;
    cliproxy_host_call_fn call;
    cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct {
    uint32_t abi_version;
    cliproxy_plugin_call_fn call;
    cliproxy_plugin_free_fn free_buffer;
    cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

extern int keyChainRouterPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void keyChainRouterPluginFree(void*, size_t);
extern void keyChainRouterPluginShutdown(void);

static const cliproxy_host_api* stored_host;
static void store_host_api(const cliproxy_host_api* host) { stored_host = host; }
static int call_host_api(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
    if (stored_host == NULL || stored_host->call == NULL) return 1;
    return stored_host->call(stored_host->host_ctx, method, request, request_len, response);
}
static void free_host_buffer(void* ptr, size_t len) {
    if (stored_host != NULL && stored_host->free_buffer != NULL && ptr != NULL) stored_host->free_buffer(ptr, len);
}
*/
import "C"

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"
)

var pluginVersion = "0.4.0"

const (
	pluginID   = "key-chain-router"
	pluginName = "Key Chain Router"
	abiVersion = 1
	maxSchema  = 5 // v7.2.154 uses schema 5; newer hosts may negotiate down safely.

	methodPluginRegister        = "plugin.register"
	methodPluginReconfigure     = "plugin.reconfigure"
	methodModelRoute            = "model.route"
	methodSchedulerPick         = "scheduler.pick"
	methodExecutorIdentifier    = "executor.identifier"
	methodExecutorExecute       = "executor.execute"
	methodExecutorExecuteStream = "executor.execute_stream"
	methodExecutorCountTokens   = "executor.count_tokens"
	methodManagementRegister    = "management.register"
	methodManagementHandle      = "management.handle"

	methodHostAuthList           = "host.auth.list"
	methodHostModelExecute       = "host.model.execute"
	methodHostModelExecuteStream = "host.model.execute_stream"
	methodHostModelStreamRead    = "host.model.stream_read"
	methodHostModelStreamClose   = "host.model.stream_close"
	methodHostStreamEmit         = "host.stream.emit"
	methodHostStreamClose        = "host.stream.close"

	ticketHeader = "X-CPA-Key-Chain-Ticket"
)

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}
type envelopeError struct{ Code, Message string }

type lifecycleRequest struct {
	ConfigYAML    []byte `json:"config_yaml"`
	SchemaVersion uint32 `json:"schema_version"`
}

type registration struct {
	SchemaVersion uint32                 `json:"schema_version"`
	Metadata      map[string]any         `json:"metadata"`
	Capabilities  registrationCapability `json:"capabilities"`
}
type registrationCapability struct {
	Scheduler             bool     `json:"scheduler"`
	ModelRouter           bool     `json:"model_router"`
	Executor              bool     `json:"executor"`
	ExecutorModelScope    string   `json:"executor_model_scope"`
	ExecutorInputFormats  []string `json:"executor_input_formats"`
	ExecutorOutputFormats []string `json:"executor_output_formats"`
	ManagementAPI         bool     `json:"management_api"`
}

type managementRegistration struct {
	Resources []managementResource `json:"resources,omitempty"`
}
type managementResource struct {
	Path        string `json:"Path"`
	Menu        string `json:"Menu"`
	Description string `json:"Description"`
}
type managementRequest struct {
	Method         string
	Path           string
	Headers        http.Header
	Query          url.Values
	Body           []byte
	HostCallbackID string `json:"host_callback_id,omitempty"`
}
type managementResponse struct {
	StatusCode int         `json:"StatusCode"`
	Headers    http.Header `json:"Headers"`
	Body       []byte      `json:"Body"`
}

type pluginConfig struct {
	Enabled            bool
	StateFile          string
	CPAConfigFile      string
	TicketTTL          time.Duration
	FallbackEnabled    bool
	FallbackOn         map[int]bool
	NoFallbackOn       map[int]bool
	LegacyClientModels []string
}

type State struct {
	Version   int               `json:"version"`
	UpdatedAt string            `json:"updated_at"`
	Routes    map[string]*Route `json:"routes"`
}
type Route struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	KeyFingerprint string       `json:"key_fingerprint"`
	KeyHint        string       `json:"key_hint"`
	Enabled        bool         `json:"enabled"`
	MatchModels    []string     `json:"match_models,omitempty"`
	Candidates     []*Candidate `json:"candidates"`
}
type Candidate struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ResourceID    string `json:"resource_id,omitempty"`
	ResourceKind  string `json:"resource_kind,omitempty"`
	Provider      string `json:"provider"`
	AuthID        string `json:"auth_id,omitempty"` // legacy/migration aid
	AuthIndex     string `json:"auth_index,omitempty"`
	OverrideModel string `json:"override_model,omitempty"`
	Model         string `json:"model,omitempty"` // legacy v0.1/v0.2 field; migrated to OverrideModel
	Enabled       bool   `json:"enabled"`
}

type ticketRecord struct {
	AuthIndex string
	Provider  string
	ExpiresAt time.Time
}

type apiResource struct {
	ID             string   `json:"id"`
	Kind           string   `json:"kind"`
	Provider       string   `json:"provider"`
	DisplayName    string   `json:"display_name"`
	AuthID         string   `json:"auth_id,omitempty"`
	AuthIndex      string   `json:"auth_index,omitempty"`
	Prefix         string   `json:"prefix,omitempty"`
	BaseURL        string   `json:"base_url,omitempty"`
	KeyHint        string   `json:"key_hint,omitempty"`
	Status         string   `json:"status,omitempty"`
	Unavailable    bool     `json:"unavailable,omitempty"`
	Models         []string `json:"models,omitempty"`
	SuggestedModel string   `json:"suggested_model,omitempty"`
}

type downstreamKey struct {
	Fingerprint string `json:"fingerprint"`
	Hint        string `json:"hint"`
}

type hostAuthListResponse struct {
	Files []hostAuthEntry `json:"files"`
}
type hostAuthEntry struct {
	ID          string `json:"id,omitempty"`
	AuthIndex   string `json:"auth_index,omitempty"`
	Name        string `json:"name,omitempty"`
	Type        string `json:"type,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Email       string `json:"email,omitempty"`
	Label       string `json:"label,omitempty"`
	Status      string `json:"status,omitempty"`
	Priority    int    `json:"priority,omitempty"`
	Unavailable bool   `json:"unavailable,omitempty"`
	RuntimeOnly bool   `json:"runtime_only,omitempty"`
	Source      string `json:"source,omitempty"`
}

type hostModelExecutionResponse struct {
	StatusCode int         `json:"status_code"`
	Headers    http.Header `json:"headers"`
	Body       []byte      `json:"body"`
}
type hostModelStreamResponse struct {
	StatusCode int         `json:"status_code"`
	Headers    http.Header `json:"headers"`
	StreamID   string      `json:"stream_id"`
}
type hostModelStreamReadRequest struct {
	StreamID string `json:"stream_id"`
}
type hostModelStreamReadResponse struct {
	Payload []byte `json:"payload,omitempty"`
	Error   string `json:"error,omitempty"`
	Done    bool   `json:"done,omitempty"`
}
type hostModelStreamCloseRequest struct {
	StreamID string `json:"stream_id"`
}
type hostStreamEmitRequest struct {
	StreamID string `json:"stream_id"`
	Payload  []byte `json:"payload,omitempty"`
	Error    string `json:"error,omitempty"`
}
type hostStreamCloseRequest struct {
	StreamID string `json:"stream_id,omitempty"`
	Error    string `json:"error,omitempty"`
}

var runtimeState = struct {
	sync.RWMutex
	cfg        pluginConfig
	state      State
	schema     uint32
	pluginDir  string
	statePath  string
	configPath string
	tickets    map[string]ticketRecord
	recent     map[string]executionTrace
}{tickets: map[string]ticketRecord{}, recent: map[string]executionTrace{}}

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return 1
	}
	C.store_host_api(host)
	plugin.abi_version = C.uint32_t(abiVersion)
	plugin.call = C.cliproxy_plugin_call_fn(C.keyChainRouterPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.keyChainRouterPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.keyChainRouterPluginShutdown)
	return 0
}

//export keyChainRouterPluginCall
func keyChainRouterPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeResponse(response, errorEnvelope("invalid_method", "method is required"))
		return 1
	}
	var req []byte
	if request != nil && requestLen > 0 {
		req = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	raw, err := handleMethod(C.GoString(method), req)
	if err != nil {
		writeResponse(response, errorEnvelope("plugin_error", err.Error()))
		return 1
	}
	writeResponse(response, raw)
	return 0
}

//export keyChainRouterPluginFree
func keyChainRouterPluginFree(ptr unsafe.Pointer, _ C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
}

//export keyChainRouterPluginShutdown
func keyChainRouterPluginShutdown() { shutdownObservabilityV4() }

func handleMethod(method string, raw []byte) ([]byte, error) {
	switch method {
	case methodPluginRegister, methodPluginReconfigure:
		if err := configure(raw); err != nil {
			return nil, err
		}
		return okEnvelope(pluginRegistration())
	case methodModelRoute:
		return handleModelRouteV4(raw)
	case methodSchedulerPick:
		return handleSchedulerPick(raw)
	case methodExecutorIdentifier:
		return okEnvelope(map[string]any{"identifier": pluginID})
	case methodExecutorExecute:
		return handleExecuteV4(raw, false)
	case methodExecutorExecuteStream:
		return handleExecuteV4(raw, true)
	case methodExecutorCountTokens:
		return okEnvelope(map[string]any{"Payload": []byte(`{"input_tokens":0}`)})
	case methodManagementRegister:
		return okEnvelope(managementRegistration{Resources: []managementResource{
			{Path: "/status", Menu: "Key Chain Router", Description: "按 CPA 原生 API Key 和模型规则执行可观测策略路由"},
			{Path: "/api", Description: "Key Chain Router 管理数据接口"},
		}})
	case methodManagementHandle:
		return handleManagement(raw)
	default:
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}

func configure(raw []byte) error {
	var req lifecycleRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return fmt.Errorf("decode lifecycle: %w", err)
		}
	}
	schema := req.SchemaVersion
	if schema == 0 {
		schema = 1
	}
	if schema > maxSchema {
		schema = maxSchema
	}
	cfg := parsePluginConfig(string(req.ConfigYAML))
	pluginDir := detectPluginDir()
	configPath := resolveCPAConfigPath(cfg.CPAConfigFile)
	statePath := cfg.StateFile
	if !filepath.IsAbs(statePath) {
		statePath = filepath.Join(pluginDir, statePath)
	}
	st, err := loadState(statePath, cfg)
	if err != nil {
		return err
	}

	runtimeState.Lock()
	runtimeState.cfg = cfg
	runtimeState.schema = schema
	runtimeState.pluginDir = pluginDir
	runtimeState.configPath = configPath
	runtimeState.statePath = statePath
	runtimeState.state = st
	runtimeState.Unlock()
	if err := configureV4(statePath, st); err != nil {
		return err
	}
	return nil
}

func pluginRegistration() registration {
	runtimeState.RLock()
	schema := runtimeState.schema
	runtimeState.RUnlock()
	if schema == 0 {
		schema = 1
	}
	return registration{
		SchemaVersion: schema,
		Metadata: map[string]any{
			"Name":             pluginName,
			"Version":          pluginVersion,
			"Author":           "kitdine",
			"GitHubRepository": "https://github.com/kitdine/cpa-plugin-key-chain-router",
			"Logo":             "",
			"ConfigFields":     []any{},
		},
		Capabilities: registrationCapability{
			Scheduler: true, ModelRouter: true, Executor: true, ManagementAPI: true,
			ExecutorModelScope:    "static",
			ExecutorInputFormats:  []string{"openai", "openai-response", "claude", "gemini", "interactions"},
			ExecutorOutputFormats: []string{"openai", "openai-response", "claude", "gemini", "interactions"},
		},
	}
}

func parsePluginConfig(raw string) pluginConfig {
	cfg := pluginConfig{
		Enabled:         true,
		StateFile:       "key-chain-router-state.json",
		TicketTTL:       30 * time.Second,
		FallbackEnabled: true,
		FallbackOn:      map[int]bool{401: true, 403: true, 408: true, 409: true, 429: true, 500: true, 502: true, 503: true, 504: true},
		NoFallbackOn:    map[int]bool{400: true, 404: true, 422: true},
	}
	lines := strings.Split(raw, "\n")
	for _, ln := range lines {
		s := strings.TrimSpace(stripYAMLComment(ln))
		if s == "" {
			continue
		}
		key, val, ok := splitYAMLKeyValue(s)
		if !ok {
			continue
		}
		switch key {
		case "enabled":
			cfg.Enabled = parseBool(val, cfg.Enabled)
		case "state_file":
			if v := unquoteYAML(val); v != "" {
				cfg.StateFile = v
			}
		case "cpa_config_file":
			cfg.CPAConfigFile = unquoteYAML(val)
		case "ticket_ttl_seconds":
			if n, e := strconv.Atoi(strings.TrimSpace(val)); e == nil && n > 0 {
				cfg.TicketTTL = time.Duration(n) * time.Second
			}
		case "fallback_on_status":
			if xs := parseIntList(val); len(xs) > 0 {
				cfg.FallbackOn = toIntSet(xs)
			}
		case "no_fallback_on_status":
			if xs := parseIntList(val); len(xs) > 0 {
				cfg.NoFallbackOn = toIntSet(xs)
			}
		case "client_models":
			cfg.LegacyClientModels = parseStringList(val)
		}
	}
	return cfg
}

func detectPluginDir() string {
	wd, _ := os.Getwd()
	candidates := []string{
		filepath.Join(wd, "plugins", "linux", "amd64"),
		filepath.Join(wd, "plugins"),
		"/CLIProxyAPI/plugins/linux/amd64",
		"/CLIProxyAPI/plugins",
	}
	for _, dir := range candidates {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			ents, _ := os.ReadDir(dir)
			for _, e := range ents {
				name := strings.ToLower(e.Name())
				if !e.IsDir() && strings.HasPrefix(name, "key-chain-router") && strings.HasSuffix(name, ".so") {
					return dir
				}
			}
		}
	}
	return wd
}

func resolveCPAConfigPath(explicit string) string {
	candidates := []string{}
	if strings.TrimSpace(explicit) != "" {
		candidates = append(candidates, strings.TrimSpace(explicit))
	}
	args := os.Args
	for i := 0; i < len(args); i++ {
		if args[i] == "-config" || args[i] == "--config" {
			if i+1 < len(args) {
				candidates = append(candidates, args[i+1])
			}
		}
		if strings.HasPrefix(args[i], "-config=") {
			candidates = append(candidates, strings.TrimPrefix(args[i], "-config="))
		}
		if strings.HasPrefix(args[i], "--config=") {
			candidates = append(candidates, strings.TrimPrefix(args[i], "--config="))
		}
	}
	candidates = append(candidates, "config.yaml", "config.yml", "/CLIProxyAPI/config.yaml", "/CLIProxyAPI/config.yml")
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			if a, e := filepath.Abs(c); e == nil {
				return a
			}
			return c
		}
	}
	return ""
}

func loadState(path string, cfg pluginConfig) (State, error) {
	st := State{Version: 3, UpdatedAt: time.Now().UTC().Format(time.RFC3339), Routes: map[string]*Route{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
		return st, fmt.Errorf("read state: %w", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return st, nil
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return st, fmt.Errorf("decode state: %w", err)
	}
	if st.Routes == nil {
		st.Routes = map[string]*Route{}
	}
	// v0.1/v0.2 migration: fixed model/code becomes wildcard match and candidate model becomes optional override.
	for _, r := range st.Routes {
		if r == nil {
			continue
		}
		if len(r.MatchModels) == 0 {
			r.MatchModels = []string{"*"}
		}
		for _, c := range r.Candidates {
			if c == nil {
				continue
			}
			if c.OverrideModel == "" && c.Model != "" {
				c.OverrideModel = c.Model
			}
			c.Model = ""
		}
	}
	st.Version = 3
	return st, nil
}

func saveState() error {
	runtimeState.Lock()
	defer runtimeState.Unlock()
	runtimeState.state.Version = 3
	runtimeState.state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	raw, err := json.MarshalIndent(runtimeState.state, "", "  ")
	if err != nil {
		return err
	}
	path := runtimeState.statePath
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func handleModelRoute(raw []byte) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	runtimeState.RLock()
	cfg := runtimeState.cfg
	st := cloneState(runtimeState.state)
	runtimeState.RUnlock()
	if !cfg.Enabled {
		return okEnvelope(map[string]any{"Handled": false})
	}
	headers := mapToHeader(anyMap(req["Headers"]))
	key := extractDownstreamKey(headers)
	if key == "" {
		return okEnvelope(map[string]any{"Handled": false})
	}
	fp := fingerprint(key)
	requested := stringAny(req, "RequestedModel")
	if requested == "" {
		requested = stringAny(req, "requested_model")
	}
	var route *Route
	for _, r := range st.Routes {
		if r != nil && r.Enabled && secureEqual(r.KeyFingerprint, fp) && modelMatches(r.MatchModels, requested) {
			route = r
			break
		}
	}
	if route == nil {
		return okEnvelope(map[string]any{"Handled": false})
	}
	if firstEnabledCandidate(route) == nil {
		return okEnvelope(map[string]any{"Handled": false, "Reason": "key_chain_route_has_no_enabled_candidates"})
	}
	return okEnvelope(map[string]any{"Handled": true, "TargetKind": "self", "Target": pluginID, "Reason": "key_chain_route:" + route.ID})
}

func handleSchedulerPick(raw []byte) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	opts := anyMap(req["Options"])
	headers := mapToHeader(anyMap(opts["Headers"]))
	if len(headers) == 0 {
		headers = mapToHeader(anyMap(opts["headers"]))
	}
	tok := headers.Get(ticketHeader)
	if tok == "" {
		return okEnvelope(map[string]any{"Handled": false})
	}
	rec, ok := consumeTicket(tok)
	if !ok {
		return okEnvelope(map[string]any{"Handled": false})
	}
	provider := strings.TrimSpace(rec.Provider)
	if provider == "" {
		provider = stringAny(req, "Provider")
		if provider == "" {
			provider = stringAny(req, "provider")
		}
	}
	authID, err := resolveAuthIDByIndex(rec.AuthIndex, provider)
	if err != nil {
		return okEnvelope(map[string]any{"Handled": true, "AuthID": "", "Reason": "kcr_auth_resolution_failed"})
	}
	return okEnvelope(map[string]any{"Handled": true, "AuthID": authID})
}

func handleExecute(raw []byte, streaming bool) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	callbackID := stringAny(req, "host_callback_id")
	streamID := stringAny(req, "stream_id")
	model := stringAny(req, "Model")
	if model == "" {
		model = stringAny(req, "model")
	}
	sourceFormat := stringAny(req, "SourceFormat")
	if sourceFormat == "" {
		sourceFormat = stringAny(req, "source_format")
	}
	original := bytesAny(req, "OriginalRequest")
	if len(original) == 0 {
		original = bytesAny(req, "original_request")
	}
	payload := bytesAny(req, "Payload")
	if len(payload) == 0 {
		payload = bytesAny(req, "payload")
	}
	headers := mapToHeader(anyMap(req["Headers"]))
	if len(headers) == 0 {
		headers = mapToHeader(anyMap(req["headers"]))
	}
	query := mapToValues(anyMap(req["Query"]))
	if len(query) == 0 {
		query = mapToValues(anyMap(req["query"]))
	}
	metadata := anyMap(req["Metadata"])
	if metadata == nil {
		metadata = anyMap(req["metadata"])
	}
	alt := stringAny(req, "Alt")
	if alt == "" {
		alt = stringAny(req, "alt")
	}
	key := extractDownstreamKey(headers)
	if key == "" {
		key = stringAny(metadata, "api_key")
	}
	if key == "" {
		return errorEnvelope("route_key_missing", "无法从执行上下文识别 CPA 原生 API Key"), nil
	}
	route := findRouteForRequest(fingerprint(key), model, original)
	if route == nil {
		return errorEnvelope("route_not_found", "没有匹配当前 API Key/模型的路由"), nil
	}
	if sourceFormat == "" {
		sourceFormat = "openai"
	}
	body := original
	if len(body) == 0 {
		body = payload
	}
	if streaming {
		go runStreamChain(route, sourceFormat, model, body, headers, query, alt, callbackID, streamID)
		return okEnvelope(map[string]any{"headers": http.Header{"Content-Type": []string{"text/event-stream"}}})
	}
	resp, attempts, err := runNonStreamChain(route, sourceFormat, model, body, headers, query, alt, callbackID)
	_ = attempts
	if err != nil {
		return errorEnvelope("executor_error", err.Error()), nil
	}
	return okEnvelope(map[string]any{"Payload": resp.Body, "Headers": resp.Headers})
}

func findRouteForRequest(fp, model string, body []byte) *Route {
	if len(body) > 0 {
		var m map[string]any
		if json.Unmarshal(body, &m) == nil {
			if originalModel := stringAny(m, "model"); originalModel != "" {
				model = originalModel
			}
		}
	}
	runtimeState.RLock()
	defer runtimeState.RUnlock()
	for _, r := range runtimeState.state.Routes {
		if r != nil && r.Enabled && secureEqual(r.KeyFingerprint, fp) && modelMatches(r.MatchModels, model) {
			return cloneRoute(r)
		}
	}
	return nil
}

type attemptResult struct {
	Candidate  string        `json:"candidate"`
	Provider   string        `json:"provider"`
	AuthIndex  string        `json:"auth_index"`
	Model      string        `json:"model"`
	Status     int           `json:"status"`
	Error      string        `json:"error,omitempty"`
	Duration   time.Duration `json:"-"`
	DurationMs int64         `json:"duration_ms"`
}

type executionTrace struct {
	At       string          `json:"at"`
	RouteID  string          `json:"route_id"`
	Model    string          `json:"model"`
	Stream   bool            `json:"stream"`
	Attempts []attemptResult `json:"attempts"`
	Final    string          `json:"final,omitempty"`
	Success  bool            `json:"success"`
	Error    string          `json:"error,omitempty"`
}

func runNonStreamChain(route *Route, sourceFormat, clientModel string, body []byte, headers http.Header, query url.Values, alt, callbackID string) (hostModelExecutionResponse, []attemptResult, error) {
	var attempts []attemptResult
	var lastErr error
	for _, c := range enabledCandidates(route) {
		started := time.Now()
		model := strings.TrimSpace(c.OverrideModel)
		if model == "" {
			model = clientModel
		}
		h := cloneHeader(headers)
		if c.AuthIndex != "" {
			h.Set(ticketHeader, issueTicket(c.AuthIndex, c.Provider))
		}
		result, err := callHost(methodHostModelExecute, map[string]any{"entry_protocol": sourceFormat, "exit_protocol": sourceFormat, "model": model, "stream": false, "body": rewriteBodyModel(body, model), "headers": h, "query": query, "alt": alt, "host_callback_id": callbackID})
		ar := attemptResult{Candidate: c.Name, Provider: c.Provider, AuthIndex: c.AuthIndex, Model: model, Duration: time.Since(started)}
		ar.DurationMs = ar.Duration.Milliseconds()
		if err == nil {
			var resp hostModelExecutionResponse
			if e := json.Unmarshal(result, &resp); e == nil {
				ar.Status = resp.StatusCode
				attempts = append(attempts, ar)
				recordTrace(executionTrace{At: time.Now().UTC().Format(time.RFC3339), RouteID: route.ID, Model: clientModel, Stream: false, Attempts: attempts, Final: c.Name, Success: true})
				return resp, attempts, nil
			} else {
				err = e
			}
		}
		ar.Error = err.Error()
		ar.Status = statusFromError(err)
		attempts = append(attempts, ar)
		lastErr = err
		if !shouldFallback(ar.Status, err) {
			break
		}
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用候选")
	}
	recordTrace(executionTrace{At: time.Now().UTC().Format(time.RFC3339), RouteID: route.ID, Model: clientModel, Stream: false, Attempts: attempts, Success: false, Error: lastErr.Error()})
	return hostModelExecutionResponse{}, attempts, lastErr
}

func recordTrace(tr executionTrace) {
	runtimeState.Lock()
	defer runtimeState.Unlock()
	if runtimeState.recent == nil {
		runtimeState.recent = map[string]executionTrace{}
	}
	runtimeState.recent[tr.RouteID] = tr
}

func runStreamChain(route *Route, sourceFormat, clientModel string, body []byte, headers http.Header, query url.Values, alt, callbackID, outStreamID string) {
	defer func() {
		if r := recover(); r != nil {
			_ = closeOutputStream(outStreamID, fmt.Sprintf("panic: %v", r))
		}
	}()
	var lastErr error
	for _, c := range enabledCandidates(route) {
		model := strings.TrimSpace(c.OverrideModel)
		if model == "" {
			model = clientModel
		}
		h := cloneHeader(headers)
		if c.AuthIndex != "" {
			h.Set(ticketHeader, issueTicket(c.AuthIndex, c.Provider))
		}
		result, err := callHost(methodHostModelExecuteStream, map[string]any{"entry_protocol": sourceFormat, "exit_protocol": sourceFormat, "model": model, "stream": true, "body": rewriteBodyModel(body, model), "headers": h, "query": query, "alt": alt, "host_callback_id": callbackID})
		if err != nil {
			lastErr = err
			if shouldFallback(statusFromError(err), err) {
				continue
			}
			break
		}
		var sr hostModelStreamResponse
		if err = json.Unmarshal(result, &sr); err != nil {
			lastErr = err
			continue
		}
		if sr.StreamID == "" {
			lastErr = errors.New("host stream id 为空")
			continue
		}
		first := true
		for {
			rr, e := readHostStream(sr.StreamID)
			if e != nil {
				_ = closeHostStream(sr.StreamID)
				lastErr = e
				if first && shouldFallback(statusFromError(e), e) {
					break
				}
				_ = closeOutputStream(outStreamID, e.Error())
				return
			}
			if rr.Error != "" {
				_ = closeHostStream(sr.StreamID)
				lastErr = errors.New(rr.Error)
				if first && shouldFallback(statusFromError(lastErr), lastErr) {
					break
				}
				_ = closeOutputStream(outStreamID, rr.Error)
				return
			}
			if len(rr.Payload) > 0 {
				first = false
				if e := emitOutput(outStreamID, rr.Payload); e != nil {
					_ = closeHostStream(sr.StreamID)
					return
				}
			}
			if rr.Done {
				_ = closeHostStream(sr.StreamID)
				_ = closeOutputStream(outStreamID, "")
				return
			}
		}
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用候选")
	}
	_ = closeOutputStream(outStreamID, lastErr.Error())
}

func readHostStream(id string) (hostModelStreamReadResponse, error) {
	raw, err := callHost(methodHostModelStreamRead, hostModelStreamReadRequest{StreamID: id})
	if err != nil {
		return hostModelStreamReadResponse{}, err
	}
	var r hostModelStreamReadResponse
	if err = json.Unmarshal(raw, &r); err != nil {
		return r, err
	}
	return r, nil
}
func closeHostStream(id string) error {
	_, err := callHost(methodHostModelStreamClose, hostModelStreamCloseRequest{StreamID: id})
	return err
}
func emitOutput(id string, p []byte) error {
	_, err := callHost(methodHostStreamEmit, hostStreamEmitRequest{StreamID: id, Payload: p})
	return err
}
func closeOutputStream(id, msg string) error {
	_, err := callHost(methodHostStreamClose, map[string]any{"stream_id": id, "error": msg})
	return err
}

func issueTicket(authIndex, provider string) string {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	tok := hex.EncodeToString(b)
	runtimeState.Lock()
	defer runtimeState.Unlock()
	runtimeState.tickets[tok] = ticketRecord{AuthIndex: authIndex, Provider: provider, ExpiresAt: time.Now().Add(runtimeState.cfg.TicketTTL)}
	cleanupTicketsLocked()
	return tok
}
func consumeTicket(tok string) (ticketRecord, bool) {
	runtimeState.Lock()
	defer runtimeState.Unlock()
	cleanupTicketsLocked()
	r, ok := runtimeState.tickets[tok]
	if ok {
		delete(runtimeState.tickets, tok)
	}
	return r, ok
}
func cleanupTicketsLocked() {
	now := time.Now()
	for k, v := range runtimeState.tickets {
		if now.After(v.ExpiresAt) {
			delete(runtimeState.tickets, k)
		}
	}
}

func shouldFallback(status int, err error) bool {
	runtimeState.RLock()
	cfg := runtimeState.cfg
	runtimeState.RUnlock()
	if !cfg.FallbackEnabled {
		return false
	}
	if status > 0 {
		if cfg.NoFallbackOn[status] {
			return false
		}
		return cfg.FallbackOn[status]
	}
	return err != nil
}

var statusRE = regexp.MustCompile(`\b(400|401|403|404|408|409|422|429|500|502|503|504)\b`)

func statusFromError(err error) int {
	if err == nil {
		return 0
	}
	m := statusRE.FindStringSubmatch(err.Error())
	if len(m) > 1 {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

func rewriteBodyModel(body []byte, model string) []byte {
	if len(body) == 0 || model == "" {
		return append([]byte(nil), body...)
	}
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return append([]byte(nil), body...)
	}
	m["model"] = model
	b, e := json.Marshal(m)
	if e != nil {
		return append([]byte(nil), body...)
	}
	return b
}

func modelMatches(patterns []string, model string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if p == "*" {
			return true
		}
		if wildcardMatch(p, model) {
			return true
		}
	}
	return false
}
func wildcardMatch(pattern, s string) bool {
	if !strings.ContainsAny(pattern, "*?") {
		return strings.EqualFold(pattern, s)
	}
	var b strings.Builder
	b.WriteString("(?i)^")
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteByte('.')
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	ok, _ := regexp.MatchString(b.String(), s)
	return ok
}
func normalizeMatchModels(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' || r == ';' })
	out := []string{}
	seen := map[string]bool{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		out = []string{"*"}
	}
	return out
}

func handleManagement(raw []byte) ([]byte, error) {
	var req managementRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	if strings.HasSuffix(req.Path, "/status") {
		return okEnvelope(htmlResponse(200, []byte(renderHTML())))
	}
	if strings.HasSuffix(req.Path, "/api") {
		resp, err := handleAPIV4(req)
		if err != nil {
			return okEnvelope(jsonResponse(400, map[string]any{"ok": false, "error": err.Error()}))
		}
		return okEnvelope(jsonResponse(200, resp))
	}
	return okEnvelope(jsonResponse(404, map[string]any{"ok": false, "error": "not found"}))
}

func handleAPI(req managementRequest) (map[string]any, error) {
	action := strings.TrimSpace(req.Query.Get("action"))
	if action == "" {
		action = "snapshot"
	}
	switch action {
	case "snapshot":
		return buildSnapshot(), nil
	case "save_route":
		payload := req.Query.Get("payload")
		if payload == "" {
			return nil, errors.New("payload is required")
		}
		var in struct {
			ID, Name, KeyFingerprint, KeyHint, MatchModels string
			Enabled                                        bool
			Candidates                                     []*Candidate
		}
		if err := json.Unmarshal([]byte(payload), &in); err != nil {
			return nil, fmt.Errorf("invalid route payload: %w", err)
		}
		if strings.TrimSpace(in.KeyFingerprint) == "" {
			return nil, errors.New("请选择下游 CPA API Key")
		}
		id := strings.TrimSpace(in.ID)
		if id == "" {
			id = "route-" + randomShort()
		}
		r := &Route{ID: id, Name: strings.TrimSpace(in.Name), KeyFingerprint: in.KeyFingerprint, KeyHint: in.KeyHint, Enabled: in.Enabled, MatchModels: normalizeMatchModels(in.MatchModels), Candidates: in.Candidates}
		if r.Name == "" {
			r.Name = "路由 " + r.KeyHint
		}
		for _, c := range r.Candidates {
			if c != nil {
				c.Model = ""
				c.OverrideModel = strings.TrimSpace(c.OverrideModel)
				if c.ID == "" {
					c.ID = "cand-" + randomShort()
				}
			}
		}
		runtimeState.Lock()
		if runtimeState.state.Routes == nil {
			runtimeState.state.Routes = map[string]*Route{}
		}
		runtimeState.state.Routes[id] = r
		runtimeState.Unlock()
		if err := saveState(); err != nil {
			return nil, err
		}
		return buildSnapshot(), nil
	case "delete_route":
		id := strings.TrimSpace(req.Query.Get("id"))
		runtimeState.Lock()
		delete(runtimeState.state.Routes, id)
		runtimeState.Unlock()
		if err := saveState(); err != nil {
			return nil, err
		}
		return buildSnapshot(), nil
	case "diagnose":
		id := strings.TrimSpace(req.Query.Get("id"))
		model := strings.TrimSpace(req.Query.Get("model"))
		return diagnoseRoute(id, model), nil
	default:
		return nil, fmt.Errorf("unknown action %q", action)
	}
}

func buildSnapshot() map[string]any {
	keys, resources, configErr := currentEnvironment()
	runtimeState.RLock()
	st := cloneState(runtimeState.state)
	cfgPath := runtimeState.configPath
	schema := runtimeState.schema
	runtimeState.RUnlock()
	rebindRoutesToResources(&st, resources)
	routes := make([]*Route, 0, len(st.Routes))
	for _, r := range st.Routes {
		routes = append(routes, r)
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].Name < routes[j].Name })
	return map[string]any{"ok": true, "version": pluginVersion, "schema": schema, "config_path": cfgPath, "config_error": configErr, "downstream_keys": keys, "resources": resources, "routes": routes}
}

func rebindRoutesToResources(st *State, resources []apiResource) {
	if st == nil {
		return
	}
	byIndex := map[string]apiResource{}
	byID := map[string]apiResource{}
	for _, r := range resources {
		byID[r.ID] = r
		if r.AuthIndex != "" {
			byIndex[r.AuthIndex] = r
		}
	}
	for _, route := range st.Routes {
		if route == nil {
			continue
		}
		for _, c := range route.Candidates {
			if c == nil {
				continue
			}
			if _, ok := byID[c.ResourceID]; ok {
				continue
			}
			if c.AuthIndex != "" {
				if r, ok := byIndex[c.AuthIndex]; ok {
					c.ResourceID = r.ID
					c.ResourceKind = r.Kind
					if c.Provider == "" {
						c.Provider = r.Provider
					}
					if c.Name == "" {
						c.Name = r.DisplayName
					}
				}
			}
		}
	}
}

func diagnoseRoute(id, model string) map[string]any {
	runtimeState.RLock()
	r := cloneRoute(runtimeState.state.Routes[id])
	runtimeState.RUnlock()
	if r == nil {
		return map[string]any{"ok": false, "error": "route not found"}
	}
	_, resources, _ := currentEnvironment()
	byID := map[string]apiResource{}
	byIndex := map[string]apiResource{}
	for _, x := range resources {
		byID[x.ID] = x
		if x.AuthIndex != "" {
			byIndex[x.AuthIndex] = x
		}
	}
	match := modelMatches(r.MatchModels, model)
	items := []map[string]any{}
	first := ""
	for i, c := range r.Candidates {
		if c == nil {
			continue
		}
		res, ok := byID[c.ResourceID]
		if !ok && c.AuthIndex != "" {
			res, ok = byIndex[c.AuthIndex]
		}
		available := c.Enabled && ok && !res.Unavailable && strings.ToLower(res.Status) != "disabled"
		if available && first == "" {
			first = c.Name
		}
		effectiveModel := c.OverrideModel
		if effectiveModel == "" {
			effectiveModel = model
		}
		items = append(items, map[string]any{"order": i + 1, "name": c.Name, "enabled": c.Enabled, "resource_found": ok, "available": available, "provider": c.Provider, "auth_index": c.AuthIndex, "status": res.Status, "override_model": c.OverrideModel, "effective_model": effectiveModel})
	}
	curl := buildDiagnosticCurl(r, model)
	runtimeState.RLock()
	last, hasLast := runtimeState.recent[id]
	runtimeState.RUnlock()
	resp := map[string]any{"ok": true, "route": r, "model": model, "model_matches": match, "candidates": items, "expected_first": first, "curl": curl, "note": "这是静态诊断，不会实际调用上游；API Provider 的 configured 状态不代表当前无 cooldown。真实 failover 请复制 curl 执行后查看 CPA usage/request log。"}
	if hasLast {
		resp["last_trace"] = last
	}
	return resp
}

func buildDiagnosticCurl(r *Route, model string) string {
	if model == "" {
		model = firstConcretePattern(r.MatchModels)
		if model == "" {
			model = "<MODEL>"
		}
	}
	return fmt.Sprintf("curl http://<CPA_HOST>:8317/v1/responses \\\n  -H 'Authorization: Bearer <该路由对应的完整 CPA API Key>' \\\n  -H 'Content-Type: application/json' \\\n  -d '{\"model\":\"%s\",\"input\":\"只回复 ROUTE_OK\"}'", strings.ReplaceAll(model, "'", ""))
}
func firstConcretePattern(xs []string) string {
	for _, x := range xs {
		if x != "*" && !strings.ContainsAny(x, "*?") {
			return x
		}
	}
	return ""
}

func currentEnvironment() ([]downstreamKey, []apiResource, string) {
	runtimeState.RLock()
	path := runtimeState.configPath
	runtimeState.RUnlock()
	if path == "" {
		return nil, oauthResourcesOnly(), "无法定位当前 CPA config.yaml"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, oauthResourcesOnly(), err.Error()
	}
	keys, apis := parseCPAConfig(string(data))
	oauth := oauthResourcesOnly()
	resources := mergeResources(oauth, apis)
	return keys, resources, ""
}

func oauthResourcesOnly() []apiResource {
	raw, err := callHost(methodHostAuthList, map[string]any{})
	if err != nil {
		return nil
	}
	var resp hostAuthListResponse
	if json.Unmarshal(raw, &resp) != nil {
		return nil
	}
	out := []apiResource{}
	for _, a := range resp.Files {
		p := strings.TrimSpace(a.Provider)
		if p == "" {
			p = a.Type
		}
		name := strings.TrimSpace(a.Email)
		if name == "" {
			name = a.Label
		}
		if name == "" {
			name = a.Name
		}
		if name == "" {
			name = a.ID
		}
		id := "oauth:" + a.AuthIndex
		if a.AuthIndex == "" {
			id = "oauth-id:" + a.ID
		}
		out = append(out, apiResource{ID: id, Kind: "OAuth", Provider: p, DisplayName: name, AuthID: a.ID, AuthIndex: a.AuthIndex, Status: a.Status, Unavailable: a.Unavailable})
	}
	return out
}

func mergeResources(a, b []apiResource) []apiResource {
	seen := map[string]bool{}
	out := []apiResource{}
	for _, xs := range [][]apiResource{a, b} {
		for _, r := range xs {
			key := r.AuthIndex
			if key == "" {
				key = r.ID
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind == out[j].Kind {
			return out[i].DisplayName < out[j].DisplayName
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

func parseCPAConfig(raw string) ([]downstreamKey, []apiResource) {
	lines := preprocessYAMLLines(raw)
	keys := parseTopStringList(lines, "api-keys")
	dks := make([]downstreamKey, 0, len(keys))
	for _, k := range keys {
		dks = append(dks, downstreamKey{Fingerprint: fingerprint(k), Hint: maskKey(k)})
	}
	resources := []apiResource{}
	specs := []struct{ Section, Kind, Provider, SeedPrefix string }{
		{"codex-api-key", "API", "codex", "codex-api-key"}, {"xai-api-key", "API", "xai", "xai-api-key"}, {"claude-api-key", "API", "claude", "claude-api-key"}, {"gemini-api-key", "API", "gemini", "gemini-api-key"}, {"interactions-api-key", "API", "gemini-interactions", "interactions-api-key"}, {"vertex-api-key", "API", "vertex", "vertex"},
	}
	for _, sp := range specs {
		for i, e := range parseTopMapList(lines, sp.Section) {
			key := scalar(e.Fields["api-key"])
			base := scalar(e.Fields["base-url"])
			if key == "" && base == "" {
				continue
			}
			prefix := scalar(e.Fields["prefix"])
			models := modelsFromNested(e.Nested["models"])
			idx := ""
			if sp.Section == "vertex-api-key" {
				id := stableID("vertex:apikey", key, base, scalar(e.Fields["proxy-url"]))
				idx = stableAuthIndex("id:" + id)
			} else {
				idx = stableAuthIndex(sp.SeedPrefix + ":" + base + "+" + key)
			}
			name := strings.ToUpper(strings.TrimSuffix(sp.Section, "-api-key")) + " API"
			if len(models) > 0 {
				name += " · " + models[0]
			}
			rid := fmt.Sprintf("api:%s:%s:%d", sp.Provider, idx, i)
			resources = append(resources, apiResource{ID: rid, Kind: "API", Provider: sp.Provider, DisplayName: name, AuthIndex: idx, Prefix: prefix, BaseURL: base, KeyHint: maskKey(key), Status: "active", Models: models, SuggestedModel: suggestedModel(prefix, models)})
		}
	}
	for i, e := range parseTopMapList(lines, "openai-compatibility") {
		if parseBool(scalar(e.Fields["disabled"]), false) {
			continue
		}
		name := scalar(e.Fields["name"])
		provider := openAICompatibleProviderKey(name)
		base := scalar(e.Fields["base-url"])
		prefix := scalar(e.Fields["prefix"])
		models := modelsFromNested(e.Nested["models"])
		entries := e.Nested["api-key-entries"]
		if len(entries) == 0 {
			entries = []yamlMapEntry{{Fields: map[string]string{}}}
		}
		for j, kent := range entries {
			key := scalar(kent.Fields["api-key"])
			idx := ""
			if key != "" {
				idx = stableAuthIndex("openai-compatibility:" + base + "+" + key)
			} else {
				id := stableID("openai-compatibility:"+strings.ToLower(strings.TrimSpace(name)), base)
				idx = stableAuthIndex("id:" + id)
			}
			display := name
			if display == "" {
				display = "OpenAI Compatibility"
			}
			rid := fmt.Sprintf("api:%s:%s:%d:%d", provider, idx, i, j)
			resources = append(resources, apiResource{ID: rid, Kind: "API", Provider: provider, DisplayName: display, AuthIndex: idx, Prefix: prefix, BaseURL: base, KeyHint: maskKey(key), Status: "active", Models: models, SuggestedModel: suggestedModel(prefix, models)})
		}
	}
	return dks, resources
}

func suggestedModel(prefix string, models []string) string {
	if len(models) == 0 {
		return ""
	}
	m := models[0]
	if prefix != "" && !strings.HasPrefix(m, prefix+"/") {
		return prefix + "/" + m
	}
	return m
}
func openAICompatibleProviderKey(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "openai-compatibility"
	}
	if name == "openai-compatibility" || strings.HasPrefix(name, "openai-compatible-") {
		return name
	}
	return "openai-compatible-" + name
}
func stableID(kind string, parts ...string) string {
	h := sha256.New()
	h.Write([]byte(kind))
	for _, p := range parts {
		h.Write([]byte{0})
		h.Write([]byte(strings.TrimSpace(p)))
	}
	sum := hex.EncodeToString(h.Sum(nil))
	return kind + ":" + sum[:12]
}
func stableAuthIndex(seed string) string {
	s := sha256.Sum256([]byte(strings.TrimSpace(seed)))
	return hex.EncodeToString(s[:8])
}

type yamlLine struct {
	Indent int
	Text   string
}
type yamlMapEntry struct {
	Fields map[string]string
	Nested map[string][]yamlMapEntry
}

func preprocessYAMLLines(raw string) []yamlLine {
	out := []yamlLine{}
	for _, ln := range strings.Split(raw, "\n") {
		clean := stripYAMLComment(ln)
		if strings.TrimSpace(clean) == "" {
			continue
		}
		indent := len(clean) - len(strings.TrimLeft(clean, " "))
		out = append(out, yamlLine{Indent: indent, Text: strings.TrimSpace(clean)})
	}
	return out
}
func parseTopStringList(lines []yamlLine, section string) []string {
	start, end := sectionBounds(lines, section)
	if start < 0 {
		return nil
	}
	out := []string{}
	for _, l := range lines[start:end] {
		if l.Indent == 2 && strings.HasPrefix(l.Text, "- ") {
			v := unquoteYAML(strings.TrimSpace(strings.TrimPrefix(l.Text, "- ")))
			if v != "" {
				out = append(out, v)
			}
		}
	}
	return out
}
func sectionBounds(lines []yamlLine, section string) (int, int) {
	needle := section + ":"
	for i, l := range lines {
		if l.Indent == 0 && strings.HasPrefix(l.Text, needle) {
			end := len(lines)
			for j := i + 1; j < len(lines); j++ {
				if lines[j].Indent == 0 {
					end = j
					break
				}
			}
			return i + 1, end
		}
	}
	return -1, -1
}
func parseTopMapList(lines []yamlLine, section string) []yamlMapEntry {
	start, end := sectionBounds(lines, section)
	if start < 0 {
		return nil
	}
	block := lines[start:end]
	return parseMapListAt(block, 2)
}
func parseMapListAt(lines []yamlLine, indent int) []yamlMapEntry {
	out := []yamlMapEntry{}
	for i := 0; i < len(lines); {
		l := lines[i]
		if l.Indent != indent || !strings.HasPrefix(l.Text, "-") {
			i++
			continue
		}
		e := yamlMapEntry{Fields: map[string]string{}, Nested: map[string][]yamlMapEntry{}}
		rest := strings.TrimSpace(strings.TrimPrefix(l.Text, "-"))
		if k, v, ok := splitYAMLKeyValue(rest); ok {
			e.Fields[k] = v
		}
		i++
		for i < len(lines) && lines[i].Indent > indent {
			cur := lines[i]
			if cur.Indent == indent+2 {
				if k, v, ok := splitYAMLKeyValue(cur.Text); ok {
					if strings.TrimSpace(v) == "" {
						j := i + 1
						for j < len(lines) && lines[j].Indent > cur.Indent {
							j++
						}
						e.Nested[k] = parseMapListAt(lines[i+1:j], cur.Indent+2)
						i = j
						continue
					} else {
						e.Fields[k] = v
					}
				}
			}
			i++
		}
		out = append(out, e)
	}
	return out
}
func modelsFromNested(entries []yamlMapEntry) []string {
	out := []string{}
	for _, e := range entries {
		a := scalar(e.Fields["alias"])
		n := scalar(e.Fields["name"])
		if a != "" {
			out = append(out, a)
		} else if n != "" {
			out = append(out, n)
		}
	}
	return out
}
func stripYAMLComment(s string) string {
	sq, dq := false, false
	for i, r := range s {
		switch r {
		case '\'':
			if !dq {
				sq = !sq
			}
		case '"':
			if !sq {
				dq = !dq
			}
		case '#':
			if !sq && !dq {
				return strings.TrimRight(s[:i], " ")
			}
		}
	}
	return s
}
func splitYAMLKeyValue(s string) (string, string, bool) {
	if s == "" {
		return "", "", false
	}
	sq, dq := false, false
	for i, r := range s {
		switch r {
		case '\'':
			if !dq {
				sq = !sq
			}
		case '"':
			if !sq {
				dq = !dq
			}
		case ':':
			if !sq && !dq {
				return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:]), true
			}
		}
	}
	return "", "", false
}
func unquoteYAML(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
		if s[0] == '"' {
			if q, e := strconv.Unquote(s); e == nil {
				return q
			}
		}
		return s[1 : len(s)-1]
	}
	return s
}
func scalar(s string) string { return unquoteYAML(s) }
func parseBool(s string, def bool) bool {
	switch strings.ToLower(unquoteYAML(strings.TrimSpace(s))) {
	case "true", "yes", "on", "1":
		return true
	case "false", "no", "off", "0":
		return false
	}
	return def
}
func parseStringList(s string) []string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		s = s[1 : len(s)-1]
	}
	parts := strings.Split(s, ",")
	out := []string{}
	for _, p := range parts {
		if v := unquoteYAML(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}
func parseIntList(s string) []int {
	out := []int{}
	for _, p := range parseStringList(s) {
		if n, e := strconv.Atoi(strings.TrimSpace(p)); e == nil {
			out = append(out, n)
		}
	}
	return out
}
func toIntSet(xs []int) map[int]bool {
	m := map[int]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return m
}

func extractDownstreamKey(h http.Header) string {
	auth := strings.TrimSpace(h.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	for _, k := range []string{"X-API-Key", "Api-Key", "API-Key"} {
		if v := strings.TrimSpace(h.Get(k)); v != "" {
			return v
		}
	}
	return ""
}
func fingerprint(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func maskKey(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return s[:1] + "…" + s[len(s)-1:]
	}
	return s[:4] + "…" + s[len(s)-4:]
}
func secureEqual(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}
func randomShort() string { b := make([]byte, 5); _, _ = rand.Read(b); return hex.EncodeToString(b) }
func firstEnabledCandidate(r *Route) *Candidate {
	for _, c := range r.Candidates {
		if c != nil && c.Enabled {
			return c
		}
	}
	return nil
}
func enabledCandidates(r *Route) []*Candidate {
	out := []*Candidate{}
	for _, c := range r.Candidates {
		if c != nil && c.Enabled {
			out = append(out, c)
		}
	}
	return out
}

func callHost(method string, payload any) (json.RawMessage, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	cm := C.CString(method)
	defer C.free(unsafe.Pointer(cm))
	var resp C.cliproxy_buffer
	var ptr *C.uint8_t
	if len(raw) > 0 {
		p := C.CBytes(raw)
		if p == nil {
			return nil, errors.New("alloc host payload")
		}
		defer C.free(p)
		ptr = (*C.uint8_t)(p)
	}
	code := C.call_host_api(cm, ptr, C.size_t(len(raw)), &resp)
	var out []byte
	if resp.ptr != nil && resp.len > 0 {
		out = C.GoBytes(resp.ptr, C.int(resp.len))
	}
	if resp.ptr != nil {
		C.free_host_buffer(resp.ptr, resp.len)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("host callback %s empty, code=%d", method, int(code))
	}
	var env envelope
	if err := json.Unmarshal(out, &env); err != nil {
		return nil, fmt.Errorf("decode host envelope: %w", err)
	}
	if !env.OK {
		if env.Error != nil {
			return nil, fmt.Errorf("%s: %s", env.Error.Code, env.Error.Message)
		}
		return nil, fmt.Errorf("host callback %s failed", method)
	}
	if code != 0 {
		return nil, fmt.Errorf("host callback %s code=%d", method, int(code))
	}
	return env.Result, nil
}

func okEnvelope(v any) ([]byte, error) {
	r, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	return json.Marshal(envelope{OK: true, Result: r})
}
func errorEnvelope(code, msg string) []byte {
	b, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: msg}})
	return b
}
func writeResponse(resp *C.cliproxy_buffer, raw []byte) {
	if resp == nil || len(raw) == 0 {
		return
	}
	p := C.CBytes(raw)
	if p == nil {
		return
	}
	resp.ptr = p
	resp.len = C.size_t(len(raw))
}
func htmlResponse(code int, body []byte) managementResponse {
	return managementResponse{StatusCode: code, Headers: http.Header{"Content-Type": []string{"text/html; charset=utf-8"}, "Cache-Control": []string{"no-store"}}, Body: body}
}
func jsonResponse(code int, v any) managementResponse {
	b, _ := json.Marshal(v)
	return managementResponse{StatusCode: code, Headers: http.Header{"Content-Type": []string{"application/json; charset=utf-8"}, "Cache-Control": []string{"no-store"}}, Body: b}
}
func cloneHeader(h http.Header) http.Header {
	if h == nil {
		return http.Header{}
	}
	return h.Clone()
}
func cloneState(st State) State {
	b, _ := json.Marshal(st)
	var x State
	_ = json.Unmarshal(b, &x)
	if x.Routes == nil {
		x.Routes = map[string]*Route{}
	}
	return x
}
func cloneRoute(r *Route) *Route {
	if r == nil {
		return nil
	}
	b, _ := json.Marshal(r)
	var x Route
	_ = json.Unmarshal(b, &x)
	return &x
}
func anyMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}
func anySlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}
func stringAny(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[k]; ok {
		switch x := v.(type) {
		case string:
			return x
		case json.Number:
			return x.String()
		case float64:
			return strconv.FormatFloat(x, 'f', -1, 64)
		}
	}
	return ""
}
func bytesAny(m map[string]any, k string) []byte {
	if m == nil {
		return nil
	}
	v := m[k]
	switch x := v.(type) {
	case string:
		if b, e := decodeMaybeBase64(x); e == nil {
			return b
		}
		return []byte(x)
	case []byte:
		return x
	}
	return nil
}
func decodeMaybeBase64(s string) ([]byte, error) {
	var b []byte
	err := json.Unmarshal([]byte(strconv.Quote(s)), &b)
	return b, err
}
func mapToHeader(m map[string]any) http.Header {
	h := http.Header{}
	for k, v := range m {
		switch x := v.(type) {
		case string:
			h.Set(k, x)
		case []any:
			for _, vv := range x {
				if s, ok := vv.(string); ok {
					h.Add(k, s)
				}
			}
		case []string:
			for _, s := range x {
				h.Add(k, s)
			}
		}
	}
	return h
}
func mapToValues(m map[string]any) url.Values {
	q := url.Values{}
	for k, v := range m {
		switch x := v.(type) {
		case string:
			q.Set(k, x)
		case []any:
			for _, vv := range x {
				if s, ok := vv.(string); ok {
					q.Add(k, s)
				}
			}
		}
	}
	return q
}

func renderHTML() string { return strings.Replace(pageHTML, "{{KEY_CHAIN_ROUTER_UI}}", uiJS, 1) }

//go:embed page.html
var pageHTML string

//go:embed ui.js
var uiJS string

var _ = html.EscapeString
