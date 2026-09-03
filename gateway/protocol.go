package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	protocolSchemaVersion   = "1"
	protocolSchemaVersionV2 = "2"
	protocolSchemaVersionV3 = "3"
	maxProtocolBodyBytes    = 32 << 20
)

var protocolIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)
var protocolOperationIDPattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{1,63}$`)
var protocolQueryParameterPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)
var protocolHeaderPattern = regexp.MustCompile("^[!#$%&'*+\\-.^_`|~0-9A-Za-z]+$")

type protocolRegistry struct {
	mu             sync.RWMutex
	dir            string
	dataDir        string
	stateDir       string
	templates      map[string]protocolDefinition
	guides         map[string][]protocolGuide
	poolMu         sync.Mutex
	workflowMu     sync.Mutex
	lifecycleMu    sync.Mutex
	liveDigest     string
	pendingPlan    *protocolPackPlan
	client         *http.Client
	readinessMu    sync.Mutex
	readinessCache map[string]protocolReadinessRuntime
	oauthMu        sync.Mutex
	oauthTokens    map[string]protocolOAuthToken
	schedulerMu    sync.Mutex
	schedulerStop  chan struct{}
	schedulerDone  chan struct{}
	policies       *tokenPolicyStore
	events         *protocolEventStore
	verifyInstall  func(root, digest string) error
}

type protocolDefinition struct {
	SchemaVersion    string                             `json:"schema_version"`
	ID               string                             `json:"id"`
	Name             string                             `json:"name"`
	Description      string                             `json:"description"`
	Enabled          bool                               `json:"enabled"`
	BaseURL          string                             `json:"base_url"`
	Timeout          int                                `json:"timeout_seconds"`
	Auth             protocolAuth                       `json:"auth"`
	ForwardHeaders   []string                           `json:"forward_headers,omitempty"`
	ResponseHeaders  []string                           `json:"response_headers,omitempty"`
	Routes           []protocolRoute                    `json:"routes"`
	Pool             *protocolPoolConfig                `json:"pool,omitempty"`
	Workflows        []protocolWorkflow                 `json:"workflows,omitempty"`
	Targets          map[string]string                  `json:"targets,omitempty"`
	UpstreamProfiles map[string]protocolUpstreamProfile `json:"upstream_profiles,omitempty"`
	Readiness        *protocolReadinessConfig           `json:"readiness,omitempty"`
	Pricing          *protocolPricing                   `json:"pricing,omitempty"`
	SourceFile       string                             `json:"-"`
	ParsedBaseURL    *url.URL                           `json:"-"`
	ParsedTargets    map[string]*url.URL                `json:"-"`
}

type protocolAuth struct {
	Type          string                  `json:"type"`
	Header        string                  `json:"header,omitempty"`
	SecretFile    string                  `json:"secret_file,omitempty"`
	ValueTemplate string                  `json:"value_template,omitempty"`
	HMAC          *protocolHMACConfig     `json:"hmac,omitempty"`
	AWSSigV4      *protocolAWSSigV4Config `json:"aws_sigv4,omitempty"`
	OAuth2        *protocolOAuth2Config   `json:"oauth2,omitempty"`
}

type protocolRoute struct {
	OperationID              string                    `json:"operation_id,omitempty"`
	Capabilities             []string                  `json:"capabilities,omitempty"`
	Methods                  []string                  `json:"methods"`
	Path                     string                    `json:"path"`
	QueryParameters          []string                  `json:"query_parameters,omitempty"`
	Summary                  string                    `json:"summary"`
	Streaming                bool                      `json:"streaming,omitempty"`
	RequestBody              json.RawMessage           `json:"request_example,omitempty"`
	RequestSchema            json.RawMessage           `json:"request_schema,omitempty"`
	ResponseSchema           json.RawMessage           `json:"response_schema,omitempty"`
	UpstreamPath             string                    `json:"upstream_path,omitempty"`
	UpstreamMethod           string                    `json:"upstream_method,omitempty"`
	RequestTransform         protocolRequestTransform  `json:"request_transform,omitempty"`
	ResponseTransform        protocolJSONTransform     `json:"response_transform,omitempty"`
	Affinity                 protocolAffinityConfig    `json:"affinity,omitempty"`
	Target                   string                    `json:"target,omitempty"`
	TargetSelector           *protocolTargetSelector   `json:"target_selector,omitempty"`
	UpstreamProfile          string                    `json:"upstream_profile,omitempty"`
	UpstreamProfilesByTarget map[string]string         `json:"upstream_profiles_by_target,omitempty"`
	Transport                string                    `json:"transport,omitempty"`
	Retry                    protocolRetryConfig       `json:"retry,omitempty"`
	Availability             protocolRouteAvailability `json:"availability,omitempty"`
	PoolSelector             string                    `json:"pool_selector,omitempty"`
	Adapter                  *protocolAdapterConfig    `json:"adapter,omitempty"`
}

// protocolCallContract is the executable, LocalRouter-side binding published
// for a semantic operation. Operation IDs select an operation in Agent APIs,
// the CLI and MCP; they are never path fragments. Consumers that issue HTTP
// directly must use URL (also published as call_url on the route).
type protocolCallContract struct {
	Methods       []string `json:"methods"`
	DefaultMethod string   `json:"default_method"`
	URL           string   `json:"url"`
	Authenticated bool     `json:"authenticated"`
	Authorization string   `json:"authorization"`
	CLI           string   `json:"cli,omitempty"`
}

type protocolDynamicInput struct {
	SourceOperationKey string `json:"source_operation_key,omitempty"`
	SourceCallURL      string `json:"source_call_url,omitempty"`
	Extract            string `json:"extract,omitempty"`
	Rule               string `json:"rule"`
}

// protocolPublicRoute publishes the validated route plus its fully resolved
// invocation binding. It is derived by the runtime and cannot be supplied by a
// Pack, so a Pack can never publish a misleading call URL.
type protocolPublicRoute struct {
	protocolRoute
	OperationKey       string                          `json:"operation_key"`
	OperationIDRole    string                          `json:"operation_id_role"`
	OperationIDIsURL   bool                            `json:"operation_id_is_url"`
	CallURL            string                          `json:"call_url"`
	Call               protocolCallContract            `json:"call"`
	RequestExampleRole string                          `json:"request_example_role,omitempty"`
	DynamicInputs      map[string]protocolDynamicInput `json:"dynamic_inputs,omitempty"`
}

type protocolView struct {
	ID            string                 `json:"id"`
	PackKey       string                 `json:"pack_key"`
	Kind          string                 `json:"kind"`
	Name          string                 `json:"name"`
	Description   string                 `json:"description"`
	Mount         string                 `json:"mount"`
	OpenAIBaseURL string                 `json:"openai_v1_base_url,omitempty"`
	WorkflowMount string                 `json:"workflow_mount"`
	Enabled       bool                   `json:"enabled"`
	Ready         bool                   `json:"ready"`
	Status        string                 `json:"status"`
	StatusLabel   string                 `json:"status_label"`
	Routes        []protocolRoute        `json:"-"`
	PublicRoutes  []protocolPublicRoute  `json:"routes"`
	Guides        []protocolGuideSummary `json:"guides"`
	Docs          protocolDocLinks       `json:"docs"`
	Pool          protocolPoolView       `json:"pool"`
	Workflows     []protocolWorkflow     `json:"workflows"`
	Pricing       *protocolPricing       `json:"pricing,omitempty"`
}

func protocolOpenAIBaseURL(packID string, routes []protocolRoute) string {
	models := false
	chat := false
	for _, route := range routes {
		switch route.Path {
		case "/models":
			models = routeSupportsMethod(route, http.MethodGet)
		case "/chat/completions":
			chat = routeSupportsMethod(route, http.MethodPost)
		}
	}
	if models && chat {
		return "/p/" + packID + "/v1"
	}
	return ""
}

func publishProtocolRoutes(packID, mount string, routes []protocolRoute) []protocolPublicRoute {
	published := make([]protocolPublicRoute, 0, len(routes))
	for _, route := range routes {
		published = append(published, publishProtocolRoute(packID, mount, routes, route))
	}
	return published
}

func publishProtocolRoute(packID, mount string, routes []protocolRoute, route protocolRoute) protocolPublicRoute {
	callURL := mount + route.Path
	call := protocolCallContract{
		Methods:       append([]string(nil), route.Methods...),
		URL:           callURL,
		Authenticated: true,
		Authorization: "Bearer service token",
	}
	if len(route.Methods) > 0 {
		call.DefaultMethod = route.Methods[0]
	}
	if len(protocolPathParameterNames(route.Path)) == 0 {
		call.CLI = "lr call " + packID + " " + route.OperationID + " '<json>'"
	}
	published := protocolPublicRoute{
		protocolRoute:    route,
		OperationKey:     packID + "." + route.OperationID,
		OperationIDRole:  "semantic-selector",
		OperationIDIsURL: false,
		CallURL:          callURL,
		Call:             call,
	}
	if len(route.RequestBody) == 0 {
		return published
	}
	published.RequestExampleRole = "illustrative-shape"
	var example map[string]any
	if json.Unmarshal(route.RequestBody, &example) != nil {
		return published
	}
	if _, hasModel := example["model"]; !hasModel {
		return published
	}
	input := protocolDynamicInput{Rule: "choose a currently advertised model ID; request_example.model is not availability evidence"}
	for _, candidate := range routes {
		if candidate.OperationID == route.OperationID || (!containsString(candidate.Capabilities, "ai.models") && !containsString(candidate.Capabilities, "openai.models")) {
			continue
		}
		input.SourceOperationKey = packID + "." + candidate.OperationID
		input.SourceCallURL = mount + candidate.Path
		input.Extract = "data[].id"
		break
	}
	published.DynamicInputs = map[string]protocolDynamicInput{"model": input}
	return published
}

// protocolAdminView extends the public Pack view with a management-only,
// secret-safe pool snapshot. It must never be used by /docs or discovery.
type protocolAdminView struct {
	protocolView
	PoolRuntime protocolPoolRuntimeView `json:"pool_runtime"`
}

type protocolPricing struct {
	Entries []protocolPricingEntry `json:"entries"`
}

type protocolPricingEntry struct {
	ID         string   `json:"id"`
	Scope      string   `json:"scope"`
	Label      string   `json:"label,omitempty"`
	Amount     *float64 `json:"amount,omitempty"`
	Currency   string   `json:"currency,omitempty"`
	Unit       string   `json:"unit,omitempty"`
	FreeTier   string   `json:"free_tier,omitempty"`
	Status     string   `json:"status"`
	SourceURL  string   `json:"source_url"`
	SourceType string   `json:"source_type"`
	CheckedAt  string   `json:"checked_at"`
	Note       string   `json:"note,omitempty"`
}

func newProtocolRegistry(dir, dataDir string) (*protocolRegistry, error) {
	return newProtocolRegistryWithState(dir, dataDir, dataDir)
}

func newProtocolRegistryWithState(dir, dataDir, stateDir string) (*protocolRegistry, error) {
	registry := &protocolRegistry{
		dir:       dir,
		dataDir:   dataDir,
		stateDir:  stateDir,
		templates: make(map[string]protocolDefinition),
		guides:    make(map[string][]protocolGuide),
		client: &http.Client{
			Transport: http.DefaultTransport.(*http.Transport).Clone(),
		},
		readinessCache: make(map[string]protocolReadinessRuntime),
		oauthTokens:    make(map[string]protocolOAuthToken),
	}
	if err := registry.reload(); err != nil {
		return nil, err
	}
	return registry, nil
}

func (registry *protocolRegistry) reload() error {
	candidate, err := registry.readProtocolCandidate(registry.dir)
	if err != nil {
		return err
	}
	if err := registry.captureProtocolRevision(registry.dir, candidate.Digest); err != nil {
		return err
	}
	registry.mu.Lock()
	registry.templates = candidate.Definitions
	registry.guides = candidate.Guides
	registry.liveDigest = candidate.Digest
	registry.mu.Unlock()
	registry.readinessMu.Lock()
	registry.readinessCache = make(map[string]protocolReadinessRuntime)
	registry.readinessMu.Unlock()
	return nil
}

func (registry *protocolRegistry) validate(definition *protocolDefinition) error {
	if definition.SchemaVersion != protocolSchemaVersion && definition.SchemaVersion != protocolSchemaVersionV2 && definition.SchemaVersion != protocolSchemaVersionV3 {
		return fmt.Errorf("schema_version must be %q, %q, or %q", protocolSchemaVersion, protocolSchemaVersionV2, protocolSchemaVersionV3)
	}
	if !protocolIDPattern.MatchString(definition.ID) {
		return errors.New("id must match ^[a-z][a-z0-9-]{1,31}$")
	}
	if strings.TrimSpace(definition.Name) == "" || strings.TrimSpace(definition.Description) == "" {
		return errors.New("name and description are required")
	}
	parsed, err := url.Parse(definition.BaseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("base_url must be an absolute http or https URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("base_url cannot contain credentials, query, or fragment")
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	definition.ParsedBaseURL = parsed
	if err := validateProtocolV3Definition(definition); err != nil {
		return err
	}
	if definition.Timeout == 0 {
		definition.Timeout = 300
	}
	if definition.Timeout < 1 || definition.Timeout > 3600 {
		return errors.New("timeout_seconds must be between 1 and 3600")
	}
	if definition.SchemaVersion == protocolSchemaVersion && (definition.Pool != nil || len(definition.Workflows) > 0) {
		return errors.New("pool and workflows require schema_version 2")
	}
	if err := registry.validateAuth(definition.Auth, definition.Pool != nil); err != nil {
		return err
	}
	if err := registry.validatePool(definition); err != nil {
		return err
	}
	if len(definition.Routes) == 0 {
		return errors.New("at least one route is required")
	}
	operationIDs := make(map[string]bool)
	operationRoutes := make(map[string]protocolRoute)
	for routeIndex := range definition.Routes {
		route := &definition.Routes[routeIndex]
		if err := validateProtocolRoute(route); err != nil {
			return fmt.Errorf("route %d: %w", routeIndex+1, err)
		}
		if route.OperationID == "" {
			route.OperationID = deriveProtocolOperationID(route.Path)
		}
		if !protocolOperationIDPattern.MatchString(route.OperationID) {
			return fmt.Errorf("route %d: operation_id must match ^[a-z][a-z0-9._-]{1,63}$", routeIndex+1)
		}
		if operationIDs[route.OperationID] {
			return fmt.Errorf("route %d: duplicate operation_id %q", routeIndex+1, route.OperationID)
		}
		operationIDs[route.OperationID] = true
		operationRoutes[route.OperationID] = *route
		if err := validateProtocolRouteV2(route); err != nil {
			return fmt.Errorf("route %d: %w", routeIndex+1, err)
		}
		if err := validateProtocolV3Route(route, definition); err != nil {
			return fmt.Errorf("route %d: %w", routeIndex+1, err)
		}
	}
	if err := validateProtocolWorkflows(definition.Workflows, operationRoutes, definition.SchemaVersion); err != nil {
		return err
	}
	definition.ForwardHeaders = normalizeHeaderList(definition.ForwardHeaders)
	definition.ResponseHeaders = normalizeHeaderList(definition.ResponseHeaders)
	return nil
}

func deriveProtocolOperationID(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	derived := make([]string, 0, len(parts))
	for _, part := range parts {
		switch {
		case part == "":
			continue
		case part == "*":
			derived = append(derived, "wildcard")
		case strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}"):
			name := strings.Trim(part, "{}")
			name = strings.ToLower(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(name, "-"))
			if name == "" {
				name = "param"
			}
			derived = append(derived, "by-"+name)
		default:
			part = strings.ToLower(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(part, "-"))
			part = strings.Trim(part, "-")
			if part != "" {
				derived = append(derived, part)
			}
		}
	}
	if len(derived) == 0 {
		return "root"
	}
	return strings.Join(derived, ".")
}

func (registry *protocolRegistry) validateAuth(auth protocolAuth, pooled bool) error {
	if auth.ValueTemplate != "" && (strings.Count(auth.ValueTemplate, "{{secret}}") != 1 || strings.Contains(strings.ReplaceAll(auth.ValueTemplate, "{{secret}}", ""), "{{")) {
		return errors.New("auth value_template must contain exactly one {{secret}} placeholder")
	}
	switch auth.Type {
	case "none":
		if auth.SecretFile != "" || auth.Header != "" || auth.ValueTemplate != "" || auth.HMAC != nil || auth.AWSSigV4 != nil || auth.OAuth2 != nil {
			return errors.New("auth type none cannot define header, secret_file, or value_template")
		}
	case "bearer":
		if auth.SecretFile == "" && !pooled {
			return errors.New("bearer auth requires secret_file")
		}
	case "header", "dual", "cookie":
		if (auth.SecretFile == "" && !pooled) || strings.TrimSpace(auth.Header) == "" {
			return fmt.Errorf("%s auth requires header and secret_file", auth.Type)
		}
		if !protocolHeaderPattern.MatchString(auth.Header) || isHopByHopHeader(auth.Header) || strings.EqualFold(auth.Header, "Host") {
			return errors.New("auth header is unsafe")
		}
	case "hmac-sha256":
		if (auth.SecretFile == "" && !pooled) || auth.HMAC == nil {
			return errors.New("hmac-sha256 auth requires secret_file and hmac config")
		}
		if err := validateProtocolHMAC(*auth.HMAC); err != nil {
			return err
		}
	case "aws-sigv4":
		if (auth.SecretFile == "" && !pooled) || auth.AWSSigV4 == nil {
			return errors.New("aws-sigv4 auth requires secret_file and aws_sigv4 config")
		}
		if strings.TrimSpace(auth.AWSSigV4.Region) == "" || strings.TrimSpace(auth.AWSSigV4.Service) == "" {
			return errors.New("aws-sigv4 requires region and service")
		}
	case "oauth2":
		if (auth.SecretFile == "" && !pooled) || auth.OAuth2 == nil {
			return errors.New("oauth2 auth requires secret_file and oauth2 config")
		}
		if err := validateProtocolOAuth2(*auth.OAuth2); err != nil {
			return err
		}
	default:
		return errors.New("auth type must be none, bearer, header, cookie, dual, hmac-sha256, aws-sigv4, or oauth2")
	}
	if auth.SecretFile != "" {
		_, err := registry.secretPath(auth.SecretFile)
		if err != nil {
			return err
		}
	}
	return nil
}

func validateProtocolRoute(route *protocolRoute) error {
	if !strings.HasPrefix(route.Path, "/") || strings.Contains(route.Path, "?") || strings.Contains(route.Path, "#") {
		return errors.New("path must be an absolute path pattern without query or fragment")
	}
	if unsafeProtocolPath(route.Path) {
		return errors.New("path contains an unsafe segment")
	}
	if strings.TrimSpace(route.Summary) == "" {
		return errors.New("summary is required")
	}
	if len(route.Methods) == 0 {
		return errors.New("methods cannot be empty")
	}
	seenQueryParameters := make(map[string]bool, len(route.QueryParameters))
	for _, parameter := range route.QueryParameters {
		if !protocolQueryParameterPattern.MatchString(parameter) {
			return fmt.Errorf("query parameter %q is invalid", parameter)
		}
		if seenQueryParameters[parameter] {
			return fmt.Errorf("query parameter %q is duplicated", parameter)
		}
		seenQueryParameters[parameter] = true
	}
	sort.Strings(route.QueryParameters)
	seenCapabilities := make(map[string]bool)
	capabilities := make([]string, 0, len(route.Capabilities))
	for _, capability := range route.Capabilities {
		capability = strings.ToLower(strings.TrimSpace(capability))
		if !protocolOperationIDPattern.MatchString(capability) {
			return fmt.Errorf("capability %q must match ^[a-z][a-z0-9._-]{1,63}$", capability)
		}
		if !seenCapabilities[capability] {
			seenCapabilities[capability] = true
			capabilities = append(capabilities, capability)
		}
	}
	sort.Strings(capabilities)
	route.Capabilities = capabilities
	if err := validateProtocolJSONSchema("request_schema", route.RequestSchema); err != nil {
		return err
	}
	if err := validateProtocolJSONSchema("response_schema", route.ResponseSchema); err != nil {
		return err
	}
	seen := make(map[string]bool)
	for index, method := range route.Methods {
		method = strings.ToUpper(strings.TrimSpace(method))
		switch method {
		case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			return fmt.Errorf("unsupported method %q", method)
		}
		if seen[method] {
			return fmt.Errorf("duplicate method %q", method)
		}
		seen[method] = true
		route.Methods[index] = method
	}
	return nil
}

func validateProtocolJSONSchema(name string, raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	if len(raw) > 256<<10 {
		return fmt.Errorf("%s exceeds 256 KiB", name)
	}
	var schema any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&schema); err != nil {
		return fmt.Errorf("%s must be valid JSON", name)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("%s must contain exactly one JSON value", name)
	}
	switch schema.(type) {
	case map[string]any, bool:
		return nil
	default:
		return fmt.Errorf("%s must be a JSON Schema object or boolean", name)
	}
}

func normalizeHeaderList(headers []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(headers))
	for _, header := range headers {
		header = http.CanonicalHeaderKey(strings.TrimSpace(header))
		if !protocolHeaderPattern.MatchString(header) || seen[header] || isHopByHopHeader(header) || strings.EqualFold(header, "Host") || strings.EqualFold(header, "Authorization") || strings.EqualFold(header, "X-Api-Key") {
			continue
		}
		seen[header] = true
		result = append(result, header)
	}
	sort.Strings(result)
	return result
}

func isHopByHopHeader(header string) bool {
	switch strings.ToLower(strings.TrimSpace(header)) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}

func (registry *protocolRegistry) secretPath(relative string) (string, error) {
	if filepath.IsAbs(relative) {
		return "", errors.New("secret_file must be relative to the local data directory")
	}
	clean := filepath.Clean(relative)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("secret_file escapes the local data directory")
	}
	path := filepath.Join(registry.dataDir, clean)
	relativePath, err := filepath.Rel(registry.dataDir, path)
	if err != nil || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
		return "", errors.New("secret_file escapes the local data directory")
	}
	return path, nil
}

func (registry *protocolRegistry) readSecret(auth protocolAuth) (string, error) {
	if auth.Type == "none" {
		return "", nil
	}
	path, err := registry.secretPath(auth.SecretFile)
	if err != nil {
		return "", err
	}
	if err := requirePrivateRegularFile(path, "secret file"); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	secret := strings.TrimSpace(string(data))
	if secret == "" {
		return "", errors.New("secret file is empty")
	}
	return secret, nil
}

func (registry *protocolRegistry) get(id string) (protocolDefinition, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	definition, ok := registry.templates[id]
	return definition, ok
}

func (registry *protocolRegistry) views() []protocolView {
	registry.mu.RLock()
	definitions := make([]protocolDefinition, 0, len(registry.templates))
	guideSummaries := make(map[string][]protocolGuideSummary, len(registry.guides))
	for _, definition := range registry.templates {
		definitions = append(definitions, definition)
	}
	for protocolID, guides := range registry.guides {
		guideSummaries[protocolID] = summarizeProtocolGuides(protocolID, guides)
	}
	registry.mu.RUnlock()
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].ID < definitions[j].ID })
	views := make([]protocolView, 0, len(definitions))
	for _, definition := range definitions {
		ready, status := registry.readiness(definition)
		mount := "/p/" + definition.ID
		views = append(views, protocolView{
			ID: definition.ID, PackKey: "protocol:" + definition.ID, Kind: "protocol",
			Name: definition.Name, Description: definition.Description,
			Mount: mount, OpenAIBaseURL: protocolOpenAIBaseURL(definition.ID, definition.Routes), Enabled: definition.Enabled, Ready: ready,
			WorkflowMount: "/w/" + definition.ID,
			Status:        status, StatusLabel: protocolStatusLabel(status), Routes: definition.Routes,
			PublicRoutes: publishProtocolRoutes(definition.ID, mount, definition.Routes),
			Guides:       guideSummaries[definition.ID], Docs: protocolDocLinksFor(definition.ID),
			Pool: registry.poolView(definition), Workflows: definition.Workflows,
			Pricing: definition.Pricing,
		})
	}
	return views
}

func (registry *protocolRegistry) adminViews() []protocolAdminView {
	views := registry.views()
	adminViews := make([]protocolAdminView, 0, len(views))
	for _, view := range views {
		definition, ok := registry.get(view.ID)
		if !ok {
			continue
		}
		adminViews = append(adminViews, protocolAdminView{
			protocolView: view,
			PoolRuntime:  registry.poolRuntimeView(definition),
		})
	}
	return adminViews
}

func protocolStatusLabel(status string) string {
	switch status {
	case "ready":
		return "已就绪"
	case "disabled":
		return "已停用"
	case "secret-not-ready":
		return "等待私有凭据"
	case "pool-not-ready":
		return "等待本地号池"
	default:
		return "不可用"
	}
}

func (registry *protocolRegistry) readiness(definition protocolDefinition) (bool, string) {
	if !definition.Enabled {
		return false, "disabled"
	}
	credentialReady := definition.Auth.Type == "none"
	if !credentialReady && definition.Pool != nil && definition.Pool.Mode == "local" {
		credentials, err := registry.loadPoolCredentials(definition)
		if err != nil {
			return false, "pool-not-ready"
		}
		state, _ := registry.loadPoolState(definition.ID)
		now := time.Now()
		for _, credential := range credentials {
			runtime := state.Credentials[credential.ID]
			if runtime == nil {
				runtime = &protocolCredentialRuntime{}
			}
			if protocolCredentialEligible(credential, runtime, definition.Pool, now) {
				credentialReady = true
				break
			}
		}
		if !credentialReady {
			return false, "pool-not-ready"
		}
	}
	if !credentialReady {
		if _, err := registry.readSecret(definition.Auth); err != nil {
			return false, "secret-not-ready"
		}
		credentialReady = true
	}
	if definition.Readiness != nil && definition.Readiness.RequireVerified {
		now := time.Now().UTC()
		for _, route := range definition.Routes {
			if route.Availability.Status != "verified" {
				return false, "verification-required"
			}
			if ready, _ := routeAvailabilityReady(route.Availability, now); !ready {
				return false, "verification-stale"
			}
		}
	}
	if definition.Readiness != nil && definition.Readiness.Mode == "probe" {
		return registry.probeReadiness(definition)
	}
	return true, "ready"
}

func (registry *protocolRegistry) counts() (int, int) {
	views := registry.views()
	ready := 0
	for _, view := range views {
		if view.Ready {
			ready++
		}
	}
	return len(views), ready
}

func registerProtocolRoutes(engine *gin.Engine, runtime localRuntime) {
	engine.GET("/.well-known/localrouter.json", runtime.protocols.handleDiscovery(runtime))
	engine.GET("/doc", func(c *gin.Context) { c.Redirect(http.StatusPermanentRedirect, "/docs") })
	engine.GET("/docs", runtime.protocols.handleDocsHTML)
	engine.GET("/docs/index.json", runtime.protocols.handleDocsIndex)
	engine.GET("/docs/openapi.json", runtime.protocols.handleOpenAPI)
	engine.GET("/docs/agent.json", runtime.protocols.handleAgentDocs)
	engine.GET("/docs/pools/index.json", runtime.protocols.handlePoolCatalogJSON)
	engine.GET("/docs/pools/catalog.md", runtime.protocols.handlePoolCatalogMarkdown)
	engine.GET("/docs/protocols", runtime.protocols.handleDocsIndex)
	engine.GET("/docs/protocols/:id", runtime.protocols.handleDocsOne)
	engine.GET("/docs/packs/:id", runtime.protocols.handlePackHTML)
	engine.GET("/docs/packs/:id/manifest.json", runtime.protocols.handleDocsOne)
	engine.GET("/docs/packs/:id/guide.md", runtime.protocols.handlePackMarkdown)
	engine.GET("/docs/packs/:id/guides/:guide", runtime.protocols.handleGuideMarkdown)
	engine.GET("/docs/packs/:id/examples.json", runtime.protocols.handleExamples)
	agent := engine.Group("/agent")
	agent.Use(localAPITokenAuth(runtime))
	agent.GET("/whoami", handleAgentWhoAmI(runtime))
	agent.POST("/resolve", runtime.protocols.handleAgentResolve(runtime))
	agent.POST("/compare", runtime.protocols.handleAgentCompare(runtime))
	agent.GET("/operations", runtime.protocols.handleAgentCatalog(runtime))
	agent.GET("/operations/:protocol/:operation", runtime.protocols.handleAgentDescribe(runtime))
	agent.POST("/preflight", runtime.protocols.handleAgentPreflight(runtime))
	mcp := engine.Group("/mcp")
	mcp.Use(localAPITokenAuth(runtime))
	mcp.POST("", runtime.protocols.handleMCP(runtime))
	maintenance := engine.Group("/manage/mcp")
	maintenance.Use(localMaintenanceAuth(runtime))
	maintenance.POST("", runtime.protocols.handleMaintenanceMCP())
	workflow := engine.Group("/w")
	workflow.Use(localAPITokenAuth(runtime))
	workflow.POST("/:protocol/:workflow", runtime.protocols.handleWorkflowCreate(runtime))
	workflow.GET("/:protocol/:workflow", runtime.protocols.handleWorkflowList)
	workflow.GET("/:protocol/:workflow/:job", runtime.protocols.handleWorkflowGet(runtime))
	workflow.DELETE("/:protocol/:workflow/:job", runtime.protocols.handleWorkflowCancel(runtime))
	engine.POST("/w/callback/:protocol/:workflow/:job/:token", runtime.protocols.handleGraphWorkflowCallback(runtime))
	proxy := engine.Group("/p")
	proxy.Use(localAPITokenAuth(runtime))
	proxy.Any("/:protocol", runtime.protocols.handleProxy)
	proxy.Any("/:protocol/*path", runtime.protocols.handleProxy)
}

func localMaintenanceAuth(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		providedAdminToken := c.GetHeader(adminTokenHeader)
		if runtime.adminToken != nil && runtime.adminToken.matches(providedAdminToken) {
			c.Set("id", runtime.rootUser.ID)
			c.Set("username", runtime.rootUser.Username)
			c.Set("role", runtime.rootUser.Role)
			c.Set("group", "default")
			c.Set("user_group", "default")
			c.Next()
			return
		}
		if providedAdminToken != "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"code":    "local_administrator_required",
				"message": "a valid local administrator credential is required",
			})
			return
		}
		if runtime.policies == nil || !runtime.policies.isAgentMaintenanceEnabled() {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"success": false,
				"code":    "agent_maintenance_disabled",
				"message": "Agent maintenance tokens are disabled; use the local administrator credential",
			})
			return
		}
		token, authorized := authenticateLocalAPIToken(runtime, c.GetHeader("Authorization"))
		if !authorized || token == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"code":    "maintenance_token_required",
				"message": "a valid Agent maintenance token is required",
			})
			return
		}
		if !runtime.policies.hasCapability(token.ID, localRouterMaintainCapability) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"success": false,
				"code":    "maintenance_capability_required",
				"message": "this token is not authorized to modify LocalRouter",
			})
			return
		}
		c.Set(tokenPolicyContextID, token.ID)
		c.Set("token_id", token.ID)
		c.Set("token_name", token.Name)
		c.Next()
	}
}

func localAPITokenAuth(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, authorized := authenticateLocalAPIToken(runtime, c.GetHeader("Authorization"))
		if !authorized {
			c.Abort()
			writeAgentError(c, http.StatusUnauthorized, "service_token_required", "local API token required", "Authorization is missing or invalid", false, "localrouter", "read the configured mode-600 service Token file and retry with Authorization: Bearer", nil, nil, nil)
			return
		}
		if token != nil {
			if runtime.policies != nil && runtime.policies.hasCapability(token.ID, localRouterMaintainCapability) {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
					"success": false,
					"code":    "maintenance_token_service_denied",
					"message": "maintenance tokens cannot call service surfaces",
				})
				return
			}
			c.Set(tokenPolicyContextID, token.ID)
			c.Set("token_id", token.ID)
			c.Set("token_name", token.Name)
		}
		c.Next()
	}
}

func authenticateLocalAPIToken(runtime localRuntime, authorization string) (*localToken, bool) {
	if !strings.HasPrefix(authorization, "Bearer ") {
		return nil, false
	}
	provided := strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
	if runtime.store != nil {
		return resolveLocalAPIToken(runtime.rootUser.ID, provided, runtime.store.validateToken)
	}
	if runtime.apiToken != "" && len(provided) == len(runtime.apiToken) {
		// Isolated handler tests do not initialize SQLite. Production runtimes
		// always take the validated store branch above.
		return nil, subtle.ConstantTimeCompare([]byte(provided), []byte(runtime.apiToken)) == 1
	}
	return nil, false
}

func resolveLocalAPIToken(rootUserID int, provided string, validate func(string) (*localToken, error)) (*localToken, bool) {
	if provided == "" {
		return nil, false
	}
	key := strings.TrimPrefix(provided, "sk-")
	if rootUserID > 0 && key != "" {
		if token, err := validate(key); err == nil && token != nil && token.UserID == rootUserID {
			return token, true
		}
	}
	return nil, false
}

func (registry *protocolRegistry) handleProxy(c *gin.Context) {
	definition, ok := registry.get(c.Param("protocol"))
	if !ok {
		writeAgentError(c, http.StatusNotFound, "pack_not_found", "Protocol Pack was not found", c.Param("protocol"), false, "agent", "run lr tree or lr find operation <intent>, then use a published Pack ID", nil, nil, nil)
		return
	}
	path := c.Param("path")
	if path == "" {
		path = "/"
	}
	decodedPath, err := url.PathUnescape(path)
	if err != nil || unsafeProtocolPath(decodedPath) {
		writeAgentError(c, http.StatusBadRequest, "unsafe_protocol_path", "Protocol Pack path is unsafe", decodedPath, false, "agent", "use the exact call_url published by lr show or lr describe", nil, nil, nil)
		return
	}
	forwardPath := path
	matched, ok := matchProtocolRoute(definition.Routes, c.Request.Method, decodedPath)
	if !ok && protocolOpenAIBaseURL(definition.ID, definition.Routes) != "" && strings.HasPrefix(decodedPath, "/v1/") {
		forwardPath = strings.TrimPrefix(decodedPath, "/v1")
		matched, ok = matchProtocolRoute(definition.Routes, c.Request.Method, forwardPath)
	}
	if !ok {
		writeAgentError(c, http.StatusMethodNotAllowed, "route_not_allowed", "path or method is not allowed by the Protocol Pack", decodedPath, false, "agent", "operation_id is a semantic selector, not a URL; run lr show "+definition.ID+" and use the exact published call_url and method", nil, nil, gin.H{"pack": definition.ID, "requested_method": c.Request.Method, "requested_path": decodedPath})
		return
	}
	ready, status := registry.readiness(definition)
	if !ready {
		view, _ := registry.viewFor(definition.ID)
		registry.writeReadinessError(c, view, matched.Route.OperationID, status)
		return
	}
	if available, availabilityStatus := routeAvailabilityReady(matched.Route.Availability, time.Now().UTC()); !available {
		view, _ := registry.viewFor(definition.ID)
		registry.writeReadinessError(c, view, matched.Route.OperationID, availabilityStatus)
		return
	}
	if registry.policies != nil {
		release, allowed := registry.policies.authorizeRequest(c, "p", definition.ID, matched.Route.OperationID, "")
		if !allowed {
			return
		}
		defer release()
	}
	if registry.events != nil {
		started := time.Now()
		defer registry.events.recordRequest(c, "p", definition.ID, matched.Route.OperationID, started)
	}
	if matched.Route.Transport == "websocket" {
		registry.forwardWebSocket(c, definition, matched, forwardPath)
		return
	}
	if matched.Route.Transport == "grpc" {
		registry.forwardGRPC(c, definition, matched, forwardPath)
		return
	}
	if matched.Route.Transport == "adapter" {
		registry.forwardAdapter(c, definition, matched, forwardPath)
		return
	}
	registry.forward(c, definition, matched, forwardPath)
}

func (registry *protocolRegistry) handleAdminReadinessRefresh(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		definition, ok := registry.get(c.Param("id"))
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "unknown protocol"})
			return
		}
		for _, route := range definition.Routes {
			if route.OperationID != "health" || !routeSupportsMethod(route, http.MethodGet) {
				continue
			}
			statusCode, responseBody, _, err := callLocalProtocol(runtime, definition.ID, http.MethodGet, route.Path, "", "application/json", nil)
			ready := err == nil && statusCode >= 200 && statusCode < 300
			status := "upstream-unavailable"
			if ready {
				status = "ready"
			}
			data := gin.H{"id": definition.ID, "ready": ready, "status": status, "check": "upstream-health"}
			if snapshot := sanitizeUpstreamPoolHealth(responseBody); snapshot != nil {
				data["pool"] = snapshot
			}
			c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
			return
		}
		registry.readinessMu.Lock()
		delete(registry.readinessCache, definition.ID)
		registry.readinessMu.Unlock()
		ready, status := registry.readiness(definition)
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"id": definition.ID, "ready": ready, "status": status, "check": "local-readiness"}})
	}
}

func sanitizeUpstreamPoolHealth(body []byte) gin.H {
	var source map[string]any
	if len(body) == 0 || json.Unmarshal(body, &source) != nil {
		return nil
	}
	result := gin.H{}
	for _, key := range []string{"service", "mode"} {
		if value, ok := source[key].(string); ok && len(value) <= 80 {
			result[key] = value
		}
	}
	for _, pair := range [][2]string{{"pool_total", "total"}, {"pool_alive", "ready"}, {"pool_ready", "ready"}, {"pool_eligible", "ready"}, {"in_flight", "in_flight"}, {"sticky_resources", "sticky_resources"}} {
		if value, ok := source[pair[0]].(float64); ok && value >= 0 {
			result[pair[1]] = int(value)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func unsafeProtocolPath(path string) bool {
	if strings.ContainsRune(path, '\x00') || strings.Contains(path, "\\") || !strings.HasPrefix(path, "/") {
		return true
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == ".." {
			return true
		}
	}
	return false
}

func (registry *protocolRegistry) forward(c *gin.Context, definition protocolDefinition, matched protocolRouteMatch, path string) {
	route := matched.Route
	upstreamPath := path
	if route.UpstreamPath != "" {
		var err error
		upstreamPath, err = renderProtocolPath(route.UpstreamPath, matched.Params)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "cannot render upstream path"})
			return
		}
	}
	query := c.Request.URL.Query()
	for key, value := range route.RequestTransform.Query {
		query.Set(key, renderProtocolTemplate(value, matched.Params, c.Request.URL.Query()))
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Duration(definition.Timeout)*time.Second)
	defer cancel()
	if c.Request.ContentLength > maxProtocolBodyBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"success": false, "message": "protocol request body is too large"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxProtocolBodyBytes))
	if err != nil {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"success": false, "message": "protocol request body is too large"})
		return
	}
	body, err = applyProtocolJSONTransform(body, route.RequestTransform.JSON)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	expressionEnv := protocolExpressionEnv(body, matched.Params, query, c.Request.Header, c.Request.Method, 0)
	body, err = applyProtocolExpressions(body, route.RequestTransform.JSON, expressionEnv)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "request expression failed"})
		return
	}
	expressionEnv = protocolExpressionEnv(body, matched.Params, query, c.Request.Header, c.Request.Method, 0)
	for key, expression := range route.RequestTransform.QueryExpr {
		value, evalErr := evalProtocolExpression(expression, expressionEnv)
		if evalErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "request query expression failed"})
			return
		}
		scalar, scalarErr := expressionScalar(value)
		if scalarErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "request query expression must return a scalar"})
			return
		}
		query.Set(key, scalar)
	}
	affinityKey := ""
	if route.Affinity.RequestPathParam != "" {
		affinityKey = matched.Params[route.Affinity.RequestPathParam]
	}
	maxAttempts := 1
	if definition.Pool != nil && definition.Pool.Mode == "local" {
		maxAttempts = definition.Pool.MaxAttempts
	}
	if route.Retry.MaxAttempts > 0 {
		maxAttempts = route.Retry.MaxAttempts
	}
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	idempotencyKey := ""
	if route.Retry.IdempotencyHeader != "" {
		idempotencyKey = strings.TrimSpace(c.GetHeader(route.Retry.IdempotencyHeader))
		if idempotencyKey == "" {
			idempotencyKey, err = randomSecret(16, "lr-idem-")
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot allocate idempotency key"})
				return
			}
		}
	}
	excluded := make(map[string]bool)
	for attempt := 0; attempt < maxAttempts; attempt++ {
		c.Set("localrouter_protocol_attempts", attempt+1)
		acquired, acquireErr := registry.acquireCredential(definition, route, affinityKey, excluded)
		if acquireErr != nil {
			view, _ := registry.viewFor(definition.ID)
			registry.writeReadinessError(c, view, route.OperationID, "pool-not-ready")
			return
		}
		excluded[acquired.Credential.ID] = true
		c.Set("localrouter_protocol_credential", acquired.Credential.ID)
		targetBase, targetName, targetOK := protocolTargetForCredential(definition, route, acquired.Credential)
		if !targetOK {
			_ = registry.releaseCredential(definition, acquired, 0, "")
			c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "protocol target is unavailable"})
			return
		}
		target := *targetBase
		target.Path = strings.TrimSuffix(target.Path, "/") + upstreamPath
		_, upstreamProfile := protocolUpstreamProfileForRoute(definition, route, targetName)
		target.RawQuery = protocolUpstreamRawQuery(upstreamProfile, c.Request.URL.RawQuery, query)
		if targetName != "" {
			c.Set("localrouter_protocol_target", targetName)
		}
		wroteRequest := false
		trace := &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) { wroteRequest = true }}
		requestContext := httptrace.WithClientTrace(ctx, trace)
		providerMethod := c.Request.Method
		if route.UpstreamMethod != "" {
			providerMethod = route.UpstreamMethod
		}
		request, requestErr := http.NewRequestWithContext(requestContext, providerMethod, target.String(), bytes.NewReader(body))
		if requestErr != nil {
			_ = registry.releaseCredential(definition, acquired, http.StatusBadGateway, "")
			c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": "cannot construct upstream request"})
			return
		}
		request.ContentLength = int64(len(body))
		request.Header = protocolUpstreamHeaders(definition, route, targetName, c.Request.Header, []string{"Accept", "Content-Type"}, false, matched.Params, c.Request.URL.Query(), "localrouter")
		for header, value := range route.RequestTransform.Headers {
			request.Header.Set(header, renderProtocolTemplate(value, matched.Params, c.Request.URL.Query()))
		}
		for header, expression := range route.RequestTransform.HeaderExpr {
			value, evalErr := evalProtocolExpression(expression, expressionEnv)
			if evalErr != nil {
				_ = registry.releaseCredential(definition, acquired, 0, "")
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "request header expression failed"})
				return
			}
			scalar, scalarErr := expressionScalar(value)
			if scalarErr != nil {
				_ = registry.releaseCredential(definition, acquired, 0, "")
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "request header expression must return a scalar"})
				return
			}
			request.Header.Set(header, scalar)
		}
		if route.Retry.IdempotencyHeader != "" {
			request.Header.Set(route.Retry.IdempotencyHeader, idempotencyKey)
		}
		if authErr := registry.injectProtocolCredential(request, definition, acquired, body); authErr != nil {
			_ = registry.releaseCredential(definition, acquired, 0, "")
			c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": "cannot prepare upstream authentication"})
			return
		}
		response, requestErr := registry.client.Do(request)
		if requestErr != nil {
			_ = registry.releaseCredential(definition, acquired, http.StatusBadGateway, "")
			if attempt+1 < maxAttempts && routeRetryAllowed(route, providerMethod, wroteRequest, idempotencyKey, true, 0) {
				continue
			}
			if wroteRequest && !routeMethodNaturallyIdempotent(providerMethod) && idempotencyKey == "" {
				c.Set("localrouter_protocol_outcome", "unknown")
				writeAgentError(c, routeUnknownOutcomeStatus(route), "upstream_outcome_unknown", "upstream request outcome is unknown", "the request was written but no authoritative response was received", false, "provider", "reconcile provider state using the returned resource or idempotency key; do not replay blindly", nil, registry.alternativeOperationRefs(c.GetInt(tokenPolicyContextID), definition.ID, route.OperationID), gin.H{"outcome": "unknown", "operation_id": route.OperationID})
				return
			}
			if errors.Is(requestErr, context.DeadlineExceeded) {
				c.JSON(http.StatusGatewayTimeout, gin.H{"success": false, "message": "protocol upstream timed out"})
				return
			}
			c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": "protocol upstream request failed"})
			return
		}
		if response.StatusCode == http.StatusUnauthorized && definition.Auth.Type == "oauth2" {
			registry.invalidateOAuthToken(definition, acquired)
		}
		shouldRetry := attempt+1 < maxAttempts && routeRetryAllowed(route, providerMethod, true, idempotencyKey, false, response.StatusCode)
		if releaseErr := registry.releaseCredential(definition, acquired, response.StatusCode, response.Header.Get("Retry-After")); releaseErr != nil {
			response.Body.Close()
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot persist protocol pool state"})
			return
		}
		if shouldRetry {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
			response.Body.Close()
			continue
		}
		registry.writeProtocolResponse(c, definition, route, affinityKey, acquired, response)
		return
	}
	view, _ := registry.viewFor(definition.ID)
	registry.writeReadinessError(c, view, route.OperationID, "pool-not-ready")
}

func (registry *protocolRegistry) writeProtocolResponse(c *gin.Context, definition protocolDefinition, route protocolRoute, affinityKey string, acquired acquiredProtocolCredential, response *http.Response) {
	defer response.Body.Close()
	needsBody := !protocolJSONTransformEmpty(route.ResponseTransform) || route.Affinity.ResponseJSONPath != "" || affinityKey != ""
	if needsBody {
		body, err := io.ReadAll(io.LimitReader(response.Body, maxProtocolBodyBytes+1))
		if err != nil || len(body) > maxProtocolBodyBytes {
			c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": "protocol response body is too large"})
			return
		}
		if route.Affinity.ResponseJSONPath != "" && response.StatusCode >= 200 && response.StatusCode < 400 {
			resourceID := gjson.GetBytes(body, route.Affinity.ResponseJSONPath).String()
			_ = registry.bindAffinity(definition, resourceID, acquired.Credential.ID, route.Affinity.TTLSeconds)
		}
		if affinityKey != "" && response.StatusCode >= 200 && response.StatusCode < 400 {
			_ = registry.bindAffinity(definition, affinityKey, acquired.Credential.ID, route.Affinity.TTLSeconds)
		}
		body, err = applyProtocolJSONTransform(body, route.ResponseTransform)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": "protocol response transform failed"})
			return
		}
		responseEnv := protocolExpressionEnv(body, nil, nil, response.Header, "", response.StatusCode)
		body, err = applyProtocolExpressions(body, route.ResponseTransform, responseEnv)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": "protocol response expression failed"})
			return
		}
		registry.copyProtocolResponseHeaders(c, definition, response)
		if !protocolJSONTransformEmpty(route.ResponseTransform) {
			c.Header("Content-Type", "application/json; charset=utf-8")
		}
		c.Status(response.StatusCode)
		_, _ = c.Writer.Write(body)
		return
	}
	registry.copyProtocolResponseHeaders(c, definition, response)
	c.Status(response.StatusCode)
	buffer := make([]byte, 32<<10)
	for {
		count, readErr := response.Body.Read(buffer)
		if count > 0 {
			if _, writeErr := c.Writer.Write(buffer[:count]); writeErr != nil {
				return
			}
			if flusher, ok := c.Writer.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		if readErr != nil {
			return
		}
	}
}

func (registry *protocolRegistry) copyProtocolResponseHeaders(c *gin.Context, definition protocolDefinition, response *http.Response) {
	for _, header := range append([]string{"Content-Type", "Content-Disposition", "Retry-After"}, definition.ResponseHeaders...) {
		for _, value := range response.Header.Values(header) {
			c.Writer.Header().Add(header, value)
		}
	}
	c.Writer.Header().Set("Cache-Control", "no-store")
}

func (registry *protocolRegistry) handleAdminList(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"success": true, "data": registry.adminViews()})
}

func (registry *protocolRegistry) handleAdminReload(c *gin.Context) {
	if err := registry.reload(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	registry.lifecycleMu.Lock()
	registry.pendingPlan = nil
	registry.lifecycleMu.Unlock()
	c.JSON(http.StatusOK, gin.H{"success": true, "data": registry.adminViews()})
}

func (registry *protocolRegistry) handleDocsIndex(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"object": "protocol.list", "contract_digest": registry.currentDigest(), "contract_schema_version": agentContractSchemaVersion, "agent_docs": "/docs/agent.json", "data": registry.views()})
}

func (registry *protocolRegistry) handleDocsOne(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	for _, view := range registry.views() {
		if view.ID == c.Param("id") {
			c.JSON(http.StatusOK, view)
			return
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"message": "unknown protocol"})
}
