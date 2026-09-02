package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/expr-lang/expr"
	"github.com/tidwall/sjson"
)

type protocolRetryConfig struct {
	Mode                 string `json:"mode,omitempty"`
	MaxAttempts          int    `json:"max_attempts,omitempty"`
	TransportErrors      *bool  `json:"transport_errors,omitempty"`
	Statuses             []int  `json:"statuses,omitempty"`
	IdempotencyHeader    string `json:"idempotency_header,omitempty"`
	UnknownOutcomeStatus int    `json:"unknown_outcome_status,omitempty"`
}

type protocolRouteAvailability struct {
	Status          string   `json:"status,omitempty"`
	Reason          string   `json:"reason,omitempty"`
	Level           string   `json:"level,omitempty"`
	Covers          []string `json:"covers,omitempty"`
	VerifiedAt      string   `json:"verified_at,omitempty"`
	ValidForSeconds int      `json:"valid_for_seconds,omitempty"`
	EvidenceRun     string   `json:"evidence_run,omitempty"`
}

// protocolTargetSelector maps a protected credential's operator-defined
// metadata value to one of the Pack's fixed named targets. The credential can
// select only a name present in Mappings; it can never supply a URL.
type protocolTargetSelector struct {
	MetadataKey   string            `json:"metadata_key"`
	Mappings      map[string]string `json:"mappings,omitempty"`
	DefaultTarget string            `json:"default_target,omitempty"`
}

type protocolReadinessConfig struct {
	Mode            string `json:"mode,omitempty"`
	Method          string `json:"method,omitempty"`
	Path            string `json:"path,omitempty"`
	Target          string `json:"target,omitempty"`
	IntervalSeconds int    `json:"interval_seconds,omitempty"`
	TimeoutSeconds  int    `json:"timeout_seconds,omitempty"`
	SuccessStatuses []int  `json:"success_statuses,omitempty"`
	Expression      string `json:"expression,omitempty"`
	RequireVerified bool   `json:"require_verified_operations,omitempty"`
}

type protocolReadinessRuntime struct {
	Ready     bool
	Status    string
	CheckedAt time.Time
	ExpiresAt time.Time
}

type protocolHMACConfig struct {
	Algorithm       string `json:"algorithm,omitempty"`
	Header          string `json:"header"`
	Encoding        string `json:"encoding,omitempty"`
	MessageTemplate string `json:"message_template"`
	TimestampHeader string `json:"timestamp_header,omitempty"`
	KeyIDHeader     string `json:"key_id_header,omitempty"`
}

type protocolAWSSigV4Config struct {
	Region  string `json:"region"`
	Service string `json:"service"`
}

type protocolOAuth2Config struct {
	TokenURL       string            `json:"token_url"`
	GrantType      string            `json:"grant_type,omitempty"`
	Scopes         []string          `json:"scopes,omitempty"`
	Audience       string            `json:"audience,omitempty"`
	ClientAuth     string            `json:"client_auth,omitempty"`
	Extra          map[string]string `json:"extra,omitempty"`
	ExpirySkewSecs int               `json:"expiry_skew_seconds,omitempty"`
}

type protocolOAuthToken struct {
	AccessToken string
	TokenType   string
	ExpiresAt   time.Time
}

type protocolAdapterConfig struct {
	Type           string `json:"type"`
	Path           string `json:"path,omitempty"`
	Target         string `json:"target,omitempty"`
	ModuleFile     string `json:"module_file,omitempty"`
	Function       string `json:"function,omitempty"`
	AllocFunction  string `json:"alloc_function,omitempty"`
	FreeFunction   string `json:"free_function,omitempty"`
	TimeoutMS      int    `json:"timeout_ms,omitempty"`
	MaxMemoryPages uint32 `json:"max_memory_pages,omitempty"`
}

func protocolTargetForRoute(definition protocolDefinition, route protocolRoute) *url.URL {
	if route.Target != "" && definition.ParsedTargets != nil {
		if target := definition.ParsedTargets[route.Target]; target != nil {
			return target
		}
	}
	return definition.ParsedBaseURL
}

func protocolTargetForCredential(definition protocolDefinition, route protocolRoute, credential protocolCredential) (*url.URL, string, bool) {
	if route.TargetSelector == nil {
		target := protocolTargetForRoute(definition, route)
		return target, route.Target, target != nil
	}
	targetName := route.TargetSelector.DefaultTarget
	if value := credential.Metadata[route.TargetSelector.MetadataKey]; value != "" {
		if mapped := route.TargetSelector.Mappings[value]; mapped != "" {
			targetName = mapped
		} else if route.TargetSelector.DefaultTarget == "" {
			return nil, "", false
		}
	}
	if targetName == "" {
		return nil, "", false
	}
	target := definition.ParsedTargets[targetName]
	return target, targetName, target != nil
}

func protocolRouteTargets(definition protocolDefinition, route protocolRoute) []*url.URL {
	if route.TargetSelector == nil {
		return []*url.URL{protocolTargetForRoute(definition, route)}
	}
	seen := make(map[string]bool)
	names := make([]string, 0, len(route.TargetSelector.Mappings)+1)
	for _, name := range route.TargetSelector.Mappings {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	if name := route.TargetSelector.DefaultTarget; name != "" && !seen[name] {
		names = append(names, name)
	}
	targets := make([]*url.URL, 0, len(names))
	for _, name := range names {
		targets = append(targets, definition.ParsedTargets[name])
	}
	return targets
}

func routeMethodNaturallyIdempotent(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodOptions:
		return true
	default:
		return false
	}
}

func routeUnknownOutcomeStatus(route protocolRoute) int {
	if route.Retry.UnknownOutcomeStatus >= 500 && route.Retry.UnknownOutcomeStatus <= 599 {
		return route.Retry.UnknownOutcomeStatus
	}
	return 520
}

func routeRetryAllowed(route protocolRoute, method string, wrote bool, idempotencyKey string, transportError bool, status int) bool {
	if transportError && route.Retry.TransportErrors != nil && !*route.Retry.TransportErrors {
		return false
	}
	if !wrote {
		return route.Retry.Mode != "never"
	}
	allowedByMode := false
	switch route.Retry.Mode {
	case "always":
		allowedByMode = true
	case "idempotent":
		allowedByMode = idempotencyKey != ""
	case "never":
		allowedByMode = false
	default:
		allowedByMode = routeMethodNaturallyIdempotent(method)
	}
	if !allowedByMode {
		return false
	}
	if transportError {
		return true
	}
	if len(route.Retry.Statuses) == 0 {
		return protocolShouldRetry(status)
	}
	for _, candidate := range route.Retry.Statuses {
		if status == candidate {
			return true
		}
	}
	return false
}

func validateProtocolV3Definition(definition *protocolDefinition) error {
	if definition.SchemaVersion != protocolSchemaVersionV3 {
		if len(definition.Targets) > 0 || definition.Readiness != nil || len(definition.UpstreamProfiles) > 0 {
			return errors.New("targets, readiness, and upstream_profiles require schema_version 3")
		}
		return nil
	}
	if err := validateProtocolUpstreamProfiles(definition); err != nil {
		return err
	}
	definition.ParsedTargets = make(map[string]*url.URL, len(definition.Targets))
	for name, raw := range definition.Targets {
		if !protocolOperationIDPattern.MatchString(name) {
			return fmt.Errorf("target %q must use a safe identifier", name)
		}
		parsed, err := url.Parse(raw)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "ws" && parsed.Scheme != "wss" && parsed.Scheme != "grpc" && parsed.Scheme != "tcp" && parsed.Scheme != "udp" && parsed.Scheme != "unix") {
			return fmt.Errorf("target %q uses an unsupported transport scheme", name)
		}
		if parsed.Scheme == "unix" {
			if !strings.HasPrefix(parsed.Path, "/") {
				return fmt.Errorf("unix target %q must use an absolute socket path", name)
			}
		} else if parsed.Host == "" {
			return fmt.Errorf("target %q must include a host", name)
		}
		if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("target %q cannot contain credentials, query, or fragment", name)
		}
		parsed.Path = strings.TrimSuffix(parsed.Path, "/")
		definition.ParsedTargets[name] = parsed
	}
	if definition.Readiness != nil {
		if err := validateProtocolReadiness(*definition.Readiness, definition); err != nil {
			return fmt.Errorf("readiness: %w", err)
		}
	}
	if definition.Pricing != nil {
		if err := validateProtocolPricing(definition.Pricing); err != nil {
			return fmt.Errorf("pricing: %w", err)
		}
	}
	return nil
}
func validateProtocolPricing(pricing *protocolPricing) error {
	if len(pricing.Entries) == 0 {
		return errors.New("requires at least one entry")
	}
	seen := make(map[string]bool, len(pricing.Entries))
	for _, entry := range pricing.Entries {
		if entry.ID == "" {
			return errors.New("entries require id")
		}
		if seen[entry.ID] {
			return fmt.Errorf("entry %q is duplicated", entry.ID)
		}
		seen[entry.ID] = true
		switch entry.Scope {
		case "operation", "model", "platform":
		default:
			return fmt.Errorf("entry %q has invalid scope %q", entry.ID, entry.Scope)
		}
		switch entry.Status {
		case "confirmed", "estimated":
			if entry.Amount == nil {
				return fmt.Errorf("entry %q requires amount", entry.ID)
			}
			if entry.Currency == "" {
				return fmt.Errorf("entry %q requires currency", entry.ID)
			}
			if entry.Unit == "" {
				return fmt.Errorf("entry %q requires unit", entry.ID)
			}
		case "unknown", "unpublished":
			if entry.Amount != nil {
				return fmt.Errorf("entry %q must omit amount", entry.ID)
			}
			if strings.TrimSpace(entry.Note) == "" {
				return fmt.Errorf("entry %q requires a note explaining the evidence limit", entry.ID)
			}
		default:
			return fmt.Errorf("entry %q has invalid status %q", entry.ID, entry.Status)
		}
		if entry.Status == "confirmed" || entry.Status == "estimated" {
			if strings.TrimSpace(entry.Unit) == "" {
				return fmt.Errorf("entry %q requires unit", entry.ID)
			}
		}
		if !strings.HasPrefix(entry.SourceURL, "http://") && !strings.HasPrefix(entry.SourceURL, "https://") {
			return fmt.Errorf("entry %q requires an http(s) source_url", entry.ID)
		}
		switch entry.SourceType {
		case "official-pricing-page", "official-docs", "official-homepage", "official-console", "official-api-observed", "maintainer-verified":
		default:
			return fmt.Errorf("entry %q has invalid source_type %q", entry.ID, entry.SourceType)
		}
		if len(entry.CheckedAt) != 10 || entry.CheckedAt[4] != '-' || entry.CheckedAt[7] != '-' {
			return fmt.Errorf("entry %q requires checked_at as YYYY-MM-DD", entry.ID)
		}
	}
	return nil
}

func validateProtocolReadiness(config protocolReadinessConfig, definition *protocolDefinition) error {
	if config.Mode == "" {
		config.Mode = "credential"
	}
	if config.Mode != "credential" && config.Mode != "probe" {
		return errors.New("mode must be credential or probe")
	}
	if config.Mode == "credential" {
		return nil
	}
	if config.Path == "" || !strings.HasPrefix(config.Path, "/") || unsafeProtocolPath(config.Path) {
		return errors.New("probe path must be a safe absolute path")
	}
	method := strings.ToUpper(config.Method)
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet && method != http.MethodHead {
		return errors.New("probe method must be GET or HEAD")
	}
	if config.Target != "" {
		if _, ok := definition.ParsedTargets[config.Target]; !ok {
			return fmt.Errorf("probe references unknown target %q", config.Target)
		}
	}
	if config.IntervalSeconds != 0 && (config.IntervalSeconds < 5 || config.IntervalSeconds > 86400) {
		return errors.New("interval_seconds must be between 5 and 86400")
	}
	if config.TimeoutSeconds != 0 && (config.TimeoutSeconds < 1 || config.TimeoutSeconds > 30) {
		return errors.New("timeout_seconds must be between 1 and 30")
	}
	if config.Expression != "" {
		if err := validateProtocolExpression(config.Expression); err != nil {
			return fmt.Errorf("expression: %w", err)
		}
	}
	return nil
}

func validateProtocolV3Route(route *protocolRoute, definition *protocolDefinition) error {
	if definition.SchemaVersion != protocolSchemaVersionV3 {
		if route.Target != "" || route.TargetSelector != nil || route.UpstreamProfile != "" || len(route.UpstreamProfilesByTarget) > 0 || route.Transport != "" || route.UpstreamMethod != "" || !protocolRetryEmpty(route.Retry) || route.Availability.Status != "" || route.PoolSelector != "" || route.Adapter != nil || len(route.RequestTransform.HeaderExpr) > 0 || len(route.RequestTransform.QueryExpr) > 0 || len(route.RequestTransform.JSON.SetExpr) > 0 || route.RequestTransform.JSON.ReplaceExpr != "" || len(route.ResponseTransform.SetExpr) > 0 || route.ResponseTransform.ReplaceExpr != "" {
			return errors.New("v3 route capabilities require schema_version 3")
		}
		return nil
	}
	if err := validateProtocolRouteUpstreamProfiles(route, definition); err != nil {
		return err
	}
	if route.Target != "" {
		if _, ok := definition.ParsedTargets[route.Target]; !ok {
			return fmt.Errorf("target references unknown target %q", route.Target)
		}
	}
	if route.TargetSelector != nil {
		selector := route.TargetSelector
		if definition.Pool == nil || definition.Pool.Mode != "local" {
			return errors.New("target_selector requires a local credential pool")
		}
		if route.Target != "" {
			return errors.New("target and target_selector cannot be combined")
		}
		if !protocolOperationIDPattern.MatchString(selector.MetadataKey) {
			return errors.New("target_selector metadata_key must use a safe identifier")
		}
		if len(selector.Mappings) == 0 && selector.DefaultTarget == "" {
			return errors.New("target_selector requires mappings or default_target")
		}
		for value, targetName := range selector.Mappings {
			if strings.TrimSpace(value) == "" || len(value) > 128 {
				return errors.New("target_selector mapping keys must be between 1 and 128 characters")
			}
			if _, ok := definition.ParsedTargets[targetName]; !ok {
				return fmt.Errorf("target_selector references unknown target %q", targetName)
			}
		}
		if selector.DefaultTarget != "" {
			if _, ok := definition.ParsedTargets[selector.DefaultTarget]; !ok {
				return fmt.Errorf("target_selector references unknown default target %q", selector.DefaultTarget)
			}
		}
	}
	if route.UpstreamMethod != "" {
		route.UpstreamMethod = strings.ToUpper(route.UpstreamMethod)
		switch route.UpstreamMethod {
		case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions:
		default:
			return errors.New("upstream_method is unsupported")
		}
	}
	if route.Transport == "" {
		route.Transport = "http"
	}
	switch route.Transport {
	case "http", "websocket", "grpc", "adapter":
	default:
		return errors.New("transport must be http, websocket, grpc, or adapter")
	}
	for _, selectedTarget := range protocolRouteTargets(*definition, *route) {
		if selectedTarget == nil {
			return errors.New("route target is unavailable")
		}
		if route.Transport == "http" && (selectedTarget.Scheme != "http" && selectedTarget.Scheme != "https") {
			return errors.New("http routes require http or https targets")
		}
		if route.Transport == "websocket" && (selectedTarget.Scheme != "http" && selectedTarget.Scheme != "https" && selectedTarget.Scheme != "ws" && selectedTarget.Scheme != "wss") {
			return errors.New("websocket routes require http, https, ws, or wss targets")
		}
		if route.Transport == "grpc" && (selectedTarget.Scheme != "http" && selectedTarget.Scheme != "https" && selectedTarget.Scheme != "grpc") {
			return errors.New("grpc routes require http, https, or grpc targets")
		}
	}
	if route.Transport == "websocket" && !routeSupportsMethod(*route, http.MethodGet) {
		return errors.New("websocket routes must accept GET")
	}
	if route.Transport == "grpc" && !routeSupportsMethod(*route, http.MethodPost) {
		return errors.New("grpc routes must accept POST")
	}
	if route.Transport == "grpc" {
		if !protocolJSONTransformEmpty(route.RequestTransform.JSON) || !protocolJSONTransformEmpty(route.ResponseTransform) {
			return errors.New("grpc routes use protobuf passthrough and cannot define JSON transforms")
		}
		if definition.Auth.Type == "hmac-sha256" || definition.Auth.Type == "aws-sigv4" {
			return errors.New("streaming grpc routes cannot use body-hash authentication")
		}
	}
	if route.Transport == "adapter" && (route.Adapter == nil || (route.Adapter.Type != "http-envelope" && route.Adapter.Type != "wasm-envelope")) {
		return errors.New("adapter routes require adapter.type http-envelope or wasm-envelope")
	}
	if route.Transport == "adapter" && route.Adapter.Type == "http-envelope" {
		if route.Adapter.Target == "" {
			return errors.New("adapter.target is required")
		}
		adapterTarget, ok := definition.ParsedTargets[route.Adapter.Target]
		if !ok {
			return fmt.Errorf("adapter references unknown target %q", route.Adapter.Target)
		}
		if adapterTarget.Scheme != "http" || !isLoopbackHostname(adapterTarget.Hostname()) {
			return errors.New("http-envelope adapter target must use loopback http")
		}
		if route.Adapter.Path == "" || !strings.HasPrefix(route.Adapter.Path, "/") || unsafeProtocolPath(route.Adapter.Path) {
			return errors.New("adapter.path must be a safe absolute path")
		}
	}
	if route.Transport == "adapter" && route.Adapter.Type == "wasm-envelope" {
		if err := validateProtocolWASMAdapter(definition, route.Adapter); err != nil {
			return fmt.Errorf("adapter: %w", err)
		}
	}
	if err := validateProtocolRetry(&route.Retry); err != nil {
		return fmt.Errorf("retry: %w", err)
	}
	if err := validateProtocolAvailability(route.Availability); err != nil {
		return fmt.Errorf("availability: %w", err)
	}
	if route.PoolSelector != "" {
		if err := validateProtocolExpression(route.PoolSelector); err != nil {
			return fmt.Errorf("pool_selector: %w", err)
		}
	}
	for label, expressions := range map[string]map[string]string{
		"request header_expr": route.RequestTransform.HeaderExpr,
		"request query_expr":  route.RequestTransform.QueryExpr,
		"request set_expr":    route.RequestTransform.JSON.SetExpr,
		"response set_expr":   route.ResponseTransform.SetExpr,
	} {
		for key, expression := range expressions {
			if err := validateProtocolExpression(expression); err != nil {
				return fmt.Errorf("%s %q: %w", label, key, err)
			}
		}
	}
	for label, expression := range map[string]string{
		"request replace_expr":  route.RequestTransform.JSON.ReplaceExpr,
		"response replace_expr": route.ResponseTransform.ReplaceExpr,
	} {
		if expression != "" {
			if err := validateProtocolExpression(expression); err != nil {
				return fmt.Errorf("%s: %w", label, err)
			}
		}
	}
	return nil
}

func protocolRetryEmpty(config protocolRetryConfig) bool {
	return config.Mode == "" && config.MaxAttempts == 0 && config.TransportErrors == nil && len(config.Statuses) == 0 && config.IdempotencyHeader == "" && config.UnknownOutcomeStatus == 0
}

func validateProtocolRetry(config *protocolRetryConfig) error {
	if config.Mode == "" {
		config.Mode = "safe"
	}
	if config.Mode != "never" && config.Mode != "safe" && config.Mode != "idempotent" && config.Mode != "always" {
		return errors.New("mode must be never, safe, idempotent, or always")
	}
	if config.MaxAttempts != 0 && (config.MaxAttempts < 1 || config.MaxAttempts > 20) {
		return errors.New("max_attempts must be between 1 and 20")
	}
	if config.IdempotencyHeader != "" && !protocolHeaderPattern.MatchString(http.CanonicalHeaderKey(config.IdempotencyHeader)) {
		return errors.New("idempotency_header is unsafe")
	}
	if config.Mode == "idempotent" && config.IdempotencyHeader == "" {
		return errors.New("idempotent mode requires idempotency_header")
	}
	if config.UnknownOutcomeStatus == 0 {
		config.UnknownOutcomeStatus = 520
	}
	if config.UnknownOutcomeStatus < 500 || config.UnknownOutcomeStatus > 599 {
		return errors.New("unknown_outcome_status must be between 500 and 599")
	}
	for _, status := range config.Statuses {
		if status < 400 || status > 599 {
			return errors.New("retry statuses must be between 400 and 599")
		}
	}
	return nil
}

func validateProtocolAvailability(config protocolRouteAvailability) error {
	if config.Status == "" {
		return nil
	}
	switch config.Status {
	case "draft", "observed", "verified", "blocked", "deprecated":
	default:
		return errors.New("status must be draft, observed, verified, blocked, or deprecated")
	}
	if config.Level != "" {
		switch config.Level {
		case "contract", "mock", "real-readonly", "real-operation", "real-stream", "real-workflow":
		default:
			return errors.New("level must be contract, mock, real-readonly, real-operation, real-stream, or real-workflow")
		}
	}
	seenCovers := make(map[string]bool, len(config.Covers))
	for _, cover := range config.Covers {
		switch cover {
		case "schema", "auth", "pool", "upstream", "response", "stream", "workflow", "side-effects":
		default:
			return fmt.Errorf("unsupported verification cover %q", cover)
		}
		if seenCovers[cover] {
			return fmt.Errorf("duplicate verification cover %q", cover)
		}
		seenCovers[cover] = true
	}
	if config.VerifiedAt != "" {
		if _, err := time.Parse(time.RFC3339, config.VerifiedAt); err != nil {
			return errors.New("verified_at must be RFC3339")
		}
	}
	if config.ValidForSeconds < 0 || config.ValidForSeconds > 365*24*60*60 {
		return errors.New("valid_for_seconds is out of range")
	}
	if config.EvidenceRun != "" && !strings.HasPrefix(config.EvidenceRun, "RUN-") {
		return errors.New("evidence_run must be a RUN id")
	}
	return nil
}

func validateProtocolExpression(source string) error {
	if strings.TrimSpace(source) == "" || len(source) > 8192 {
		return errors.New("expression must be between 1 and 8192 bytes")
	}
	_, err := expr.Compile(source, expr.AllowUndefinedVariables(), expr.AsAny(), expr.MaxNodes(512))
	return err
}

func evalProtocolExpression(source string, env map[string]any) (any, error) {
	program, err := expr.Compile(source, expr.AllowUndefinedVariables(), expr.AsAny(), expr.MaxNodes(512))
	if err != nil {
		return nil, err
	}
	return expr.Run(program, env)
}

func protocolExpressionEnv(body []byte, params map[string]string, query url.Values, headers http.Header, method string, status int) map[string]any {
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&decoded); err != nil {
		decoded = nil
	}
	queryMap := make(map[string]any, len(query))
	for key, values := range query {
		if len(values) == 1 {
			queryMap[key] = values[0]
		} else {
			queryMap[key] = append([]string(nil), values...)
		}
	}
	headerMap := make(map[string]any, len(headers))
	for key, values := range headers {
		if len(values) == 1 {
			headerMap[key] = values[0]
		} else {
			headerMap[key] = append([]string(nil), values...)
		}
	}
	now := time.Now().UTC()
	return map[string]any{
		"body": decoded, "path": params, "query": queryMap, "headers": headerMap,
		"method": method, "status": status, "now_unix": now.Unix(), "now_rfc3339": now.Format(time.RFC3339),
	}
}

func applyProtocolExpressions(body []byte, transform protocolJSONTransform, env map[string]any) ([]byte, error) {
	if transform.ReplaceExpr != "" {
		value, err := evalProtocolExpression(transform.ReplaceExpr, env)
		if err != nil {
			return nil, err
		}
		body, err = json.Marshal(value)
		if err != nil {
			return nil, err
		}
		env["body"] = value
	}
	if len(transform.SetExpr) == 0 {
		return body, nil
	}
	if !json.Valid(body) {
		return nil, errors.New("set_expr requires a valid JSON body")
	}
	keys := make([]string, 0, len(transform.SetExpr))
	for key := range transform.SetExpr {
		keys = append(keys, key)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	for _, key := range keys {
		value, err := evalProtocolExpression(transform.SetExpr[key], env)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		body, err = sjson.SetRawBytes(body, key, raw)
		if err != nil {
			return nil, err
		}
	}
	return body, nil
}

func expressionScalar(value any) (string, error) {
	switch value := value.(type) {
	case nil:
		return "", nil
	case string:
		return value, nil
	case bool:
		return strconv.FormatBool(value), nil
	case json.Number:
		return value.String(), nil
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return fmt.Sprint(value), nil
	default:
		return "", errors.New("expression result must be a scalar")
	}
}

func routeAvailabilityReady(config protocolRouteAvailability, now time.Time) (bool, string) {
	if config.Status == "" || config.Status == "observed" || config.Status == "verified" {
		if config.Status == "verified" && config.ValidForSeconds > 0 && config.VerifiedAt != "" {
			verified, _ := time.Parse(time.RFC3339, config.VerifiedAt)
			if now.After(verified.Add(time.Duration(config.ValidForSeconds) * time.Second)) {
				return false, "verification-stale"
			}
		}
		return true, "ready"
	}
	return false, "operation-" + config.Status
}
