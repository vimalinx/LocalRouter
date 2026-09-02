package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type protocolTransformMove struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type protocolJSONTransform struct {
	Set         map[string]json.RawMessage `json:"set,omitempty"`
	SetExpr     map[string]string          `json:"set_expr,omitempty"`
	Rename      []protocolTransformMove    `json:"rename,omitempty"`
	Remove      []string                   `json:"remove,omitempty"`
	Extract     string                     `json:"extract,omitempty"`
	Envelope    string                     `json:"envelope,omitempty"`
	ReplaceExpr string                     `json:"replace_expr,omitempty"`
}

type protocolRequestTransform struct {
	JSON       protocolJSONTransform `json:"json,omitempty"`
	Headers    map[string]string     `json:"headers,omitempty"`
	Query      map[string]string     `json:"query,omitempty"`
	HeaderExpr map[string]string     `json:"header_expr,omitempty"`
	QueryExpr  map[string]string     `json:"query_expr,omitempty"`
}

type protocolAffinityConfig struct {
	ResponseJSONPath string `json:"response_json_path,omitempty"`
	RequestPathParam string `json:"request_path_param,omitempty"`
	TTLSeconds       int    `json:"ttl_seconds,omitempty"`
}

type protocolWorkflow struct {
	ID              string                 `json:"id"`
	Name            string                 `json:"name"`
	CreateOperation string                 `json:"create_operation,omitempty"`
	PollOperation   string                 `json:"poll_operation,omitempty"`
	CancelOperation string                 `json:"cancel_operation,omitempty"`
	ResourceIDPath  string                 `json:"resource_id_path,omitempty"`
	StatusPath      string                 `json:"status_path,omitempty"`
	ResultPath      string                 `json:"result_path,omitempty"`
	PendingValues   []string               `json:"pending_values,omitempty"`
	RunningValues   []string               `json:"running_values,omitempty"`
	SuccessValues   []string               `json:"success_values,omitempty"`
	FailureValues   []string               `json:"failure_values,omitempty"`
	PollIntervalMS  int                    `json:"poll_interval_ms,omitempty"`
	MaxPollAttempts int                    `json:"max_poll_attempts,omitempty"`
	InitialStep     string                 `json:"initial_step,omitempty"`
	CancelStep      string                 `json:"cancel_step,omitempty"`
	Steps           []protocolWorkflowStep `json:"steps,omitempty"`
}

type protocolWorkflowStep struct {
	ID          string                       `json:"id"`
	Kind        string                       `json:"kind,omitempty"`
	Operation   string                       `json:"operation,omitempty"`
	Method      string                       `json:"method,omitempty"`
	PathParams  map[string]string            `json:"path_params,omitempty"`
	Query       map[string]string            `json:"query,omitempty"`
	BodyExpr    string                       `json:"body_expr,omitempty"`
	WaitMS      int                          `json:"wait_ms,omitempty"`
	Parallel    []protocolWorkflowCall       `json:"parallel,omitempty"`
	Transitions []protocolWorkflowTransition `json:"transitions"`
}

type protocolWorkflowCall struct {
	ID         string            `json:"id"`
	Operation  string            `json:"operation"`
	Method     string            `json:"method,omitempty"`
	PathParams map[string]string `json:"path_params,omitempty"`
	Query      map[string]string `json:"query,omitempty"`
	BodyExpr   string            `json:"body_expr,omitempty"`
}

type protocolWorkflowTransition struct {
	When       string `json:"when,omitempty"`
	To         string `json:"to,omitempty"`
	Terminal   string `json:"terminal,omitempty"`
	ResultExpr string `json:"result_expr,omitempty"`
}

type protocolRouteMatch struct {
	Route  protocolRoute
	Params map[string]string
}

var protocolTemplateVariable = regexp.MustCompile(`\{\{(path|query)\.([a-zA-Z][a-zA-Z0-9_-]*)\}\}`)

func validateProtocolRouteV2(route *protocolRoute) error {
	if route.UpstreamPath != "" {
		if !strings.HasPrefix(route.UpstreamPath, "/") || unsafeProtocolPath(route.UpstreamPath) || strings.ContainsAny(route.UpstreamPath, "?#") {
			return errors.New("upstream_path must be a safe absolute path template")
		}
		publicParams := protocolPathParameterNames(route.Path)
		for name := range protocolPathParameterNames(route.UpstreamPath) {
			if !publicParams[name] {
				return fmt.Errorf("upstream_path references unknown path parameter %q", name)
			}
		}
	}
	if err := validateProtocolJSONTransform(route.RequestTransform.JSON); err != nil {
		return fmt.Errorf("request_transform.json: %w", err)
	}
	if err := validateProtocolJSONTransform(route.ResponseTransform); err != nil {
		return fmt.Errorf("response_transform: %w", err)
	}
	for header, value := range route.RequestTransform.Headers {
		canonical := http.CanonicalHeaderKey(strings.TrimSpace(header))
		if !protocolHeaderPattern.MatchString(canonical) || isHopByHopHeader(canonical) || strings.EqualFold(canonical, "Host") || strings.EqualFold(canonical, "Authorization") || strings.EqualFold(canonical, "Cookie") || strings.EqualFold(canonical, "X-Api-Key") {
			return fmt.Errorf("request_transform header %q is unsafe", header)
		}
		if err := validateProtocolTemplate(value, route.Path); err != nil {
			return fmt.Errorf("request_transform header %q: %w", header, err)
		}
	}
	for key, value := range route.RequestTransform.Query {
		if strings.TrimSpace(key) == "" || strings.ContainsAny(key, "&#=?") {
			return fmt.Errorf("request_transform query key %q is unsafe", key)
		}
		if err := validateProtocolTemplate(value, route.Path); err != nil {
			return fmt.Errorf("request_transform query %q: %w", key, err)
		}
	}
	if route.Affinity.TTLSeconds == 0 && (route.Affinity.ResponseJSONPath != "" || route.Affinity.RequestPathParam != "") {
		route.Affinity.TTLSeconds = 86400
	}
	if route.Affinity.TTLSeconds < 0 || route.Affinity.TTLSeconds > 30*24*60*60 {
		return errors.New("affinity ttl_seconds must be between 1 and 2592000")
	}
	if route.Affinity.RequestPathParam != "" && !protocolPathParameterNames(route.Path)[route.Affinity.RequestPathParam] {
		return fmt.Errorf("affinity request_path_param %q is not present in path", route.Affinity.RequestPathParam)
	}
	return nil
}

func validateProtocolJSONTransform(transform protocolJSONTransform) error {
	if transform.ReplaceExpr != "" && (len(transform.Set) > 0 || len(transform.SetExpr) > 0 || len(transform.Rename) > 0 || len(transform.Remove) > 0 || transform.Extract != "" || transform.Envelope != "") {
		return errors.New("replace_expr cannot be combined with other JSON transforms")
	}
	for path, value := range transform.Set {
		if err := validateJSONTransformPath(path); err != nil {
			return fmt.Errorf("set %q: %w", path, err)
		}
		if !json.Valid(value) {
			return fmt.Errorf("set %q is not valid JSON", path)
		}
	}
	for path := range transform.SetExpr {
		if err := validateJSONTransformPath(path); err != nil {
			return fmt.Errorf("set_expr %q: %w", path, err)
		}
	}
	for _, move := range transform.Rename {
		if err := validateJSONTransformPath(move.From); err != nil {
			return fmt.Errorf("rename from %q: %w", move.From, err)
		}
		if err := validateJSONTransformPath(move.To); err != nil {
			return fmt.Errorf("rename to %q: %w", move.To, err)
		}
		if move.From == move.To {
			return fmt.Errorf("rename %q has identical source and target", move.From)
		}
	}
	for _, path := range transform.Remove {
		if err := validateJSONTransformPath(path); err != nil {
			return fmt.Errorf("remove %q: %w", path, err)
		}
	}
	if transform.Extract != "" {
		if err := validateJSONTransformPath(transform.Extract); err != nil {
			return fmt.Errorf("extract: %w", err)
		}
	}
	if transform.Envelope != "" {
		if err := validateJSONTransformPath(transform.Envelope); err != nil {
			return fmt.Errorf("envelope: %w", err)
		}
	}
	return nil
}

func validateJSONTransformPath(path string) error {
	if strings.TrimSpace(path) == "" || strings.HasPrefix(path, ".") || strings.HasSuffix(path, ".") || strings.ContainsAny(path, "\x00\r\n") {
		return errors.New("path must be a non-empty gjson/sjson dot path")
	}
	return nil
}

func protocolPathParameterNames(path string) map[string]bool {
	result := make(map[string]bool)
	for _, part := range strings.Split(strings.Trim(path, "/"), "/") {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			name := strings.Trim(part, "{}")
			if name != "" {
				result[name] = true
			}
		}
	}
	return result
}

func validateProtocolTemplate(value, path string) error {
	pathParams := protocolPathParameterNames(path)
	for _, match := range protocolTemplateVariable.FindAllStringSubmatch(value, -1) {
		if match[1] == "path" && !pathParams[match[2]] {
			return fmt.Errorf("references unknown path parameter %q", match[2])
		}
	}
	withoutKnown := protocolTemplateVariable.ReplaceAllString(value, "")
	if strings.Contains(withoutKnown, "{{") || strings.Contains(withoutKnown, "}}") {
		return errors.New("contains an unsupported template variable")
	}
	return nil
}

func validateProtocolWorkflows(workflows []protocolWorkflow, operations map[string]protocolRoute, schemaVersion string) error {
	seen := make(map[string]bool)
	for index := range workflows {
		workflow := &workflows[index]
		if !protocolOperationIDPattern.MatchString(workflow.ID) || strings.TrimSpace(workflow.Name) == "" {
			return fmt.Errorf("workflow %d: id and name are required", index+1)
		}
		if seen[workflow.ID] {
			return fmt.Errorf("workflow %d: duplicate id %q", index+1, workflow.ID)
		}
		seen[workflow.ID] = true
		if len(workflow.Steps) > 0 {
			if schemaVersion != protocolSchemaVersionV3 {
				return fmt.Errorf("workflow %s: steps require schema_version 3", workflow.ID)
			}
			if err := validateProtocolGraphWorkflow(workflow, operations); err != nil {
				return fmt.Errorf("workflow %s: %w", workflow.ID, err)
			}
			continue
		}
		for label, operation := range map[string]string{
			"create_operation": workflow.CreateOperation,
			"poll_operation":   workflow.PollOperation,
			"cancel_operation": workflow.CancelOperation,
		} {
			if operation == "" && label == "cancel_operation" {
				continue
			}
			if _, exists := operations[operation]; !exists {
				return fmt.Errorf("workflow %s: %s references unknown operation %q", workflow.ID, label, operation)
			}
		}
		createRoute := operations[workflow.CreateOperation]
		pollRoute := operations[workflow.PollOperation]
		if !routeSupportsMethod(createRoute, http.MethodPost) || len(protocolPathParameterNames(createRoute.Path)) != 0 || strings.Contains(createRoute.Path, "*") {
			return fmt.Errorf("workflow %s: create_operation must be a POST route without path parameters", workflow.ID)
		}
		if !routeSupportsMethod(pollRoute, http.MethodGet) || len(protocolPathParameterNames(pollRoute.Path)) != 1 || strings.Contains(pollRoute.Path, "*") {
			return fmt.Errorf("workflow %s: poll_operation must be a GET route with exactly one path parameter", workflow.ID)
		}
		if workflow.CancelOperation != "" {
			cancelRoute := operations[workflow.CancelOperation]
			if len(protocolPathParameterNames(cancelRoute.Path)) != 1 || (!routeSupportsMethod(cancelRoute, http.MethodDelete) && !routeSupportsMethod(cancelRoute, http.MethodPost)) {
				return fmt.Errorf("workflow %s: cancel_operation must accept DELETE or POST and exactly one path parameter", workflow.ID)
			}
		}
		if workflow.ResourceIDPath == "" || workflow.StatusPath == "" || len(workflow.SuccessValues) == 0 || len(workflow.FailureValues) == 0 {
			return fmt.Errorf("workflow %s: resource_id_path, status_path, success_values, and failure_values are required", workflow.ID)
		}
		if workflow.PollIntervalMS == 0 {
			workflow.PollIntervalMS = 3000
		}
		if workflow.MaxPollAttempts == 0 {
			workflow.MaxPollAttempts = 200
		}
		if workflow.PollIntervalMS < 250 || workflow.PollIntervalMS > 300000 || workflow.MaxPollAttempts < 1 || workflow.MaxPollAttempts > 10000 {
			return fmt.Errorf("workflow %s: polling bounds are unsafe", workflow.ID)
		}
	}
	return nil
}

func routeSupportsMethod(route protocolRoute, method string) bool {
	for _, candidate := range route.Methods {
		if candidate == method {
			return true
		}
	}
	return false
}

func matchProtocolRoute(routes []protocolRoute, method, path string) (protocolRouteMatch, bool) {
	method = strings.ToUpper(method)
	for _, route := range routes {
		allowed := false
		for _, candidate := range route.Methods {
			if candidate == method {
				allowed = true
				break
			}
		}
		if !allowed {
			continue
		}
		params, matched := matchProtocolPath(route.Path, path)
		if matched {
			return protocolRouteMatch{Route: route, Params: params}, true
		}
	}
	return protocolRouteMatch{}, false
}

func matchProtocolPath(pattern, actual string) (map[string]string, bool) {
	patternParts := strings.Split(strings.Trim(pattern, "/"), "/")
	actualParts := strings.Split(strings.Trim(actual, "/"), "/")
	if pattern == "/" && actual == "/" {
		return map[string]string{}, true
	}
	params := make(map[string]string)
	for index, patternPart := range patternParts {
		if patternPart == "*" && index == len(patternParts)-1 {
			params["wildcard"] = strings.Join(actualParts[index:], "/")
			return params, len(actualParts) >= index
		}
		if index >= len(actualParts) {
			return nil, false
		}
		if strings.HasPrefix(patternPart, "{") && strings.HasSuffix(patternPart, "}") {
			if actualParts[index] == "" {
				return nil, false
			}
			params[strings.Trim(patternPart, "{}")] = actualParts[index]
			continue
		}
		if patternPart != actualParts[index] {
			return nil, false
		}
	}
	return params, len(patternParts) == len(actualParts)
}

func renderProtocolPath(template string, params map[string]string) (string, error) {
	if template == "" {
		return "", nil
	}
	parts := strings.Split(strings.Trim(template, "/"), "/")
	for index, part := range parts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			name := strings.Trim(part, "{}")
			value, ok := params[name]
			if !ok {
				return "", fmt.Errorf("missing path parameter %q", name)
			}
			parts[index] = url.PathEscape(value)
		}
	}
	return "/" + strings.Join(parts, "/"), nil
}

func renderProtocolTemplate(value string, params map[string]string, query url.Values) string {
	return protocolTemplateVariable.ReplaceAllStringFunc(value, func(raw string) string {
		match := protocolTemplateVariable.FindStringSubmatch(raw)
		if len(match) != 3 {
			return ""
		}
		if match[1] == "path" {
			return params[match[2]]
		}
		return query.Get(match[2])
	})
}

func applyProtocolJSONTransform(body []byte, transform protocolJSONTransform) ([]byte, error) {
	if protocolJSONTransformEmpty(transform) {
		return body, nil
	}
	if len(bytes.TrimSpace(body)) == 0 {
		body = []byte(`{}`)
	}
	if !json.Valid(body) {
		return nil, errors.New("JSON transform requires a valid JSON body")
	}
	var err error
	for _, move := range transform.Rename {
		value := gjson.GetBytes(body, move.From)
		if !value.Exists() {
			continue
		}
		body, err = sjson.SetRawBytes(body, move.To, []byte(value.Raw))
		if err != nil {
			return nil, err
		}
		body, err = sjson.DeleteBytes(body, move.From)
		if err != nil {
			return nil, err
		}
	}
	keys := make([]string, 0, len(transform.Set))
	for path := range transform.Set {
		keys = append(keys, path)
	}
	sort.Strings(keys)
	for _, path := range keys {
		body, err = sjson.SetRawBytes(body, path, transform.Set[path])
		if err != nil {
			return nil, err
		}
	}
	for _, path := range transform.Remove {
		body, err = sjson.DeleteBytes(body, path)
		if err != nil {
			return nil, err
		}
	}
	if transform.Extract != "" {
		value := gjson.GetBytes(body, transform.Extract)
		if !value.Exists() {
			return nil, fmt.Errorf("extract path %q does not exist", transform.Extract)
		}
		body = []byte(value.Raw)
	}
	if transform.Envelope != "" {
		body, err = sjson.SetRawBytes([]byte(`{}`), transform.Envelope, body)
		if err != nil {
			return nil, err
		}
	}
	return body, nil
}

func protocolJSONTransformEmpty(transform protocolJSONTransform) bool {
	return len(transform.Set) == 0 && len(transform.SetExpr) == 0 && len(transform.Rename) == 0 && len(transform.Remove) == 0 && transform.Extract == "" && transform.Envelope == "" && transform.ReplaceExpr == ""
}
