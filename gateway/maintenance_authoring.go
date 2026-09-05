package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

type maintenanceValidationIssue struct {
	Path            string   `json:"path"`
	Code            string   `json:"code"`
	Message         string   `json:"message"`
	Allowed         []string `json:"allowed,omitempty"`
	SuggestedAction string   `json:"suggested_action"`
}

type maintenancePackInput struct {
	DraftID     string                   `json:"draft_id"`
	PackID      string                   `json:"pack_id"`
	Name        string                   `json:"name"`
	Description string                   `json:"description"`
	BaseURL     string                   `json:"base_url"`
	Enabled     *bool                    `json:"enabled,omitempty"`
	Timeout     *int                     `json:"timeout_seconds,omitempty"`
	Auth        *protocolAuth            `json:"auth,omitempty"`
	Pool        *protocolPoolConfig      `json:"pool,omitempty"`
	Targets     *map[string]string       `json:"targets,omitempty"`
	Readiness   *protocolReadinessConfig `json:"readiness,omitempty"`
}

type maintenanceOperationInput struct {
	DraftID   string        `json:"draft_id"`
	PackID    string        `json:"pack_id"`
	Operation protocolRoute `json:"operation"`
}

type maintenanceUpstreamProfileInput struct {
	DraftID   string                  `json:"draft_id"`
	PackID    string                  `json:"pack_id"`
	ProfileID string                  `json:"profile_id"`
	Profile   protocolUpstreamProfile `json:"profile"`
}

func maintenanceEnum(description string, values ...string) gin.H {
	return gin.H{"type": "string", "description": description, "enum": values}
}

func maintenanceStringArray(description string) gin.H {
	return gin.H{"type": "array", "description": description, "items": gin.H{"type": "string"}, "uniqueItems": true}
}

func maintenanceAuthSchema() gin.H {
	return maintenanceObjectSchema(gin.H{
		"type":           maintenanceEnum("Authentication type; defaults to none when auth is omitted", "none", "bearer", "header", "cookie", "dual", "hmac-sha256", "aws-sigv4", "oauth2"),
		"header":         maintenanceString("Provider authentication header name"),
		"secret_file":    maintenanceString("Protected path below gateway/data; never a literal secret"),
		"value_template": maintenanceString("Provider value template containing exactly one {{secret}}"),
		"hmac":           gin.H{"type": "object", "additionalProperties": true},
		"aws_sigv4":      gin.H{"type": "object", "additionalProperties": true},
		"oauth2":         gin.H{"type": "object", "additionalProperties": true},
	})
}

func maintenancePoolSchema() gin.H {
	return maintenanceObjectSchema(gin.H{
		"mode":                          maintenanceEnum("Credential ownership", "local", "external"),
		"credentials_file":              maintenanceString("Protected pool file below gateway/data"),
		"strategy":                      maintenanceEnum("Local selection strategy", "round-robin", "lru", "least-inflight", "balance-aware", "smooth-weighted"),
		"max_attempts":                  gin.H{"type": "integer", "minimum": 1, "maximum": 20},
		"max_inflight_per_credential":   gin.H{"type": "integer", "minimum": 0, "maximum": 10000},
		"min_balance":                   gin.H{"type": "number"},
		"unauthorized_action":           maintenanceEnum("401/403 handling", "disable", "cooldown"),
		"unauthorized_cooldown_seconds": gin.H{"type": "integer", "minimum": 1},
		"rate_limit_cooldown_seconds":   gin.H{"type": "integer", "minimum": 1},
		"transient_cooldown_seconds":    gin.H{"type": "integer", "minimum": 1},
		"inflight_lease_seconds":        gin.H{"type": "integer", "minimum": 30},
		"quota_stale_after_seconds":     gin.H{"type": "integer", "minimum": 60},
		"source":                        gin.H{"type": "object", "description": "External read-only pool source mapping", "additionalProperties": true},
	}, "mode")
}

func maintenanceReadinessSchema() gin.H {
	return maintenanceObjectSchema(gin.H{
		"mode":                        maintenanceEnum("Readiness source", "credential", "probe"),
		"method":                      maintenanceEnum("Probe method", "GET", "HEAD"),
		"path":                        maintenanceString("Safe absolute probe path"),
		"target":                      maintenanceString("Named target used by the probe"),
		"interval_seconds":            gin.H{"type": "integer", "minimum": 5, "maximum": 86400},
		"timeout_seconds":             gin.H{"type": "integer", "minimum": 1, "maximum": 30},
		"success_statuses":            gin.H{"type": "array", "items": gin.H{"type": "integer", "minimum": 100, "maximum": 599}},
		"expression":                  maintenanceString("Optional response expression"),
		"require_verified_operations": gin.H{"type": "boolean"},
	})
}

func maintenancePutPackSchema() gin.H {
	return maintenanceObjectSchema(gin.H{
		"draft_id": maintenanceString("Draft id"), "pack_id": maintenanceString("Pack id"),
		"name": maintenanceString("Human-readable supplier or service name"), "description": maintenanceString("One sentence purpose"),
		"base_url": maintenanceString("Protected absolute provider base URL; required when creating and omitted to preserve an existing Pack target"), "enabled": gin.H{"type": "boolean", "default": true},
		"timeout_seconds": gin.H{"type": "integer", "minimum": 1, "maximum": 3600, "default": 300},
		"auth":            maintenanceAuthSchema(), "pool": maintenancePoolSchema(),
		"targets":   gin.H{"type": "object", "description": "Named provider targets", "additionalProperties": gin.H{"type": "string"}},
		"readiness": maintenanceReadinessSchema(),
	}, "draft_id", "pack_id", "name", "description")
}

func maintenanceRetrySchema() gin.H {
	return maintenanceObjectSchema(gin.H{
		"mode":                   maintenanceEnum("Replay policy", "never", "safe", "idempotent", "always"),
		"max_attempts":           gin.H{"type": "integer", "minimum": 1, "maximum": 20},
		"transport_errors":       gin.H{"type": "boolean"},
		"statuses":               gin.H{"type": "array", "items": gin.H{"type": "integer", "minimum": 400, "maximum": 599}},
		"idempotency_header":     maintenanceString("Provider idempotency header"),
		"unknown_outcome_status": gin.H{"type": "integer", "minimum": 500, "maximum": 599},
	})
}

func maintenanceAvailabilitySchema() gin.H {
	return maintenanceObjectSchema(gin.H{
		"status":            maintenanceEnum("Operation verification status", "draft", "observed", "verified", "blocked", "deprecated"),
		"reason":            maintenanceString("Human-readable evidence limit"),
		"level":             maintenanceEnum("Highest proven execution level", "contract", "mock", "real-readonly", "real-operation", "real-stream", "real-workflow"),
		"covers":            gin.H{"type": "array", "uniqueItems": true, "items": gin.H{"type": "string", "enum": []string{"schema", "auth", "pool", "upstream", "response", "stream", "workflow", "side-effects"}}},
		"verified_at":       maintenanceString("RFC3339 verification time"),
		"valid_for_seconds": gin.H{"type": "integer", "minimum": 0, "maximum": 31536000},
		"evidence_run":      maintenanceString("Optional RUN id; do not populate while AI evidence automation is paused"),
	})
}

func maintenancePutOperationSchema() gin.H {
	operation := maintenanceObjectSchema(gin.H{
		"metering":     serviceMeteringSchema(),
		"operation_id": maintenanceString("Stable operation id"), "methods": gin.H{"type": "array", "minItems": 1, "uniqueItems": true, "items": gin.H{"type": "string", "enum": []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}}},
		"path": maintenanceString("Public absolute path pattern"), "summary": maintenanceString("One sentence operation purpose"),
		"capabilities": maintenanceStringArray("Searchable capability tags"), "query_parameters": maintenanceStringArray("Allowed public query names"),
		"upstream_path": maintenanceString("Provider path template"), "upstream_method": maintenanceEnum("Provider method override", "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"),
		"streaming": gin.H{"type": "boolean"}, "request_example": gin.H{}, "request_schema": gin.H{}, "response_schema": gin.H{},
		"target": maintenanceString("Default named target"), "target_selector": gin.H{"type": "object", "additionalProperties": true},
		"upstream_profile": maintenanceString("Default supplier request profile"), "upstream_profiles_by_target": gin.H{"type": "object", "additionalProperties": gin.H{"type": "string"}},
		"transport":         maintenanceEnum("Provider transport", "http", "websocket", "grpc", "adapter"),
		"request_transform": gin.H{"type": "object", "additionalProperties": true}, "response_transform": gin.H{"type": "object", "additionalProperties": true},
		"retry": maintenanceRetrySchema(), "availability": maintenanceAvailabilitySchema(), "pool_selector": maintenanceString("Credential eligibility expression"),
		"adapter": gin.H{"type": "object", "additionalProperties": true}, "affinity": gin.H{"type": "object", "additionalProperties": true},
	}, "operation_id", "methods", "path", "summary")
	return maintenanceObjectSchema(gin.H{"draft_id": maintenanceString("Draft id"), "pack_id": maintenanceString("Pack id"), "operation": operation}, "draft_id", "pack_id", "operation")
}

func maintenanceUpstreamProfileSchema() gin.H {
	return maintenanceObjectSchema(gin.H{
		"forward_headers": maintenanceStringArray("Caller headers allowed upstream"),
		"set_headers":     gin.H{"type": "object", "description": "Non-secret provider headers; path/query templates are supported", "additionalProperties": gin.H{"type": "string"}},
		"remove_headers":  maintenanceStringArray("Headers removed after forwarding and static rules"),
		"user_agent":      maintenanceEnum("Explicit upstream User-Agent behavior", "inherit", "preserve", "omit", "localrouter", "configured"),
		"query":           maintenanceEnum("Query serialization; preserve-raw cannot be combined with query transforms", "normalized", "preserve-raw"),
	})
}

func maintenancePutUpstreamProfileSchema() gin.H {
	return maintenanceObjectSchema(gin.H{"draft_id": maintenanceString("Draft id"), "pack_id": maintenanceString("Pack id"), "profile_id": maintenanceString("Reusable profile id"), "profile": maintenanceUpstreamProfileSchema()}, "draft_id", "pack_id", "profile_id", "profile")
}

func (registry *protocolRegistry) readMaintenanceDefinition(root, packID string) (protocolDefinition, error) {
	data, err := os.ReadFile(filepath.Join(root, packID+".json"))
	if err != nil {
		return protocolDefinition{}, err
	}
	var definition protocolDefinition
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&definition); err != nil {
		return protocolDefinition{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return protocolDefinition{}, errors.New("trailing JSON data is not allowed")
	}
	return definition, nil
}

func writeMaintenanceDefinition(path string, definition protocolDefinition) error {
	definition.SourceFile = ""
	definition.ParsedBaseURL = nil
	definition.ParsedTargets = nil
	encoded, err := json.MarshalIndent(definition, "", "  ")
	if err != nil || len(encoded) > maxProtocolDraftFileBytes {
		return errors.New("Pack cannot be encoded within the size limit")
	}
	return writeMaintenanceFileAtomic(path, append(encoded, '\n'))
}

func validateMaintenanceDefinition(registry *protocolRegistry, definition *protocolDefinition, allowMissingRoutes bool) error {
	if definition.SchemaVersion == "" {
		definition.SchemaVersion = protocolSchemaVersionV3
	}
	if definition.Timeout == 0 {
		definition.Timeout = 300
	}
	if definition.Auth.Type == "" {
		definition.Auth.Type = "none"
	}
	if definition.Readiness == nil {
		definition.Readiness = &protocolReadinessConfig{Mode: "credential"}
	} else if definition.Readiness.Mode == "" {
		definition.Readiness.Mode = "credential"
	}
	candidate := *definition
	if allowMissingRoutes && len(candidate.Routes) == 0 {
		candidate.Routes = []protocolRoute{{OperationID: "draft-operation", Methods: []string{http.MethodGet}, Path: "/draft-operation", Summary: "Authoring placeholder"}}
	}
	if err := registry.validate(&candidate); err != nil {
		return err
	}
	definition.Timeout = candidate.Timeout
	definition.Auth = candidate.Auth
	definition.Readiness = candidate.Readiness
	definition.UpstreamProfiles = candidate.UpstreamProfiles
	if len(definition.Routes) > 0 {
		definition.Routes = candidate.Routes
	}
	return nil
}

func maintenanceValidationFailure(registry *protocolRegistry, stage string, err error) *maintenanceToolFailure {
	failure := maintenanceFailure(registry, "pack_invalid", stage, err.Error(), false, true)
	failure.Issues = []maintenanceValidationIssue{maintenanceValidationIssueFromError(err)}
	return &failure
}

var maintenanceRouteIssuePattern = regexp.MustCompile(`(?:^|: )route ([0-9]+): (.*)$`)

func maintenanceValidationIssueFromError(err error) maintenanceValidationIssue {
	message := err.Error()
	issue := maintenanceValidationIssue{Path: "$", Code: "invalid_value", Message: message, SuggestedAction: "Correct this field, then call localrouter_draft_lint_pack again."}
	if match := maintenanceRouteIssuePattern.FindStringSubmatch(message); len(match) == 3 {
		routeIndex, _ := strconv.Atoi(match[1])
		if routeIndex > 0 {
			routeIndex--
		}
		issue.Path = "routes[" + strconv.Itoa(routeIndex) + "]"
		message = match[2]
		issue.Message = message
	}
	classifications := []struct {
		contains string
		path     string
		allowed  []string
		action   string
	}{
		{"auth type must be", "auth.type", []string{"none", "bearer", "header", "cookie", "dual", "hmac-sha256", "aws-sigv4", "oauth2"}, "Use auth.type=none for a public or no-key provider; otherwise select the provider's real authentication type."},
		{"mode must be credential or probe", "readiness.mode", []string{"credential", "probe"}, "Use credential when readiness follows credential availability, or probe with a safe GET/HEAD path."},
		{"status must be draft", "availability.status", []string{"draft", "observed", "verified", "blocked", "deprecated"}, "Use draft until the operation has matching verification."},
		{"level must be contract", "availability.level", []string{"contract", "mock", "real-readonly", "real-operation", "real-stream", "real-workflow"}, "Choose only the highest execution level actually verified."},
		{"transport must be", "transport", []string{"http", "websocket", "grpc", "adapter"}, "Select the provider transport used by this operation."},
		{"user_agent must be", "upstream_profiles.*.user_agent", []string{"inherit", "preserve", "omit", "localrouter", "configured"}, "Choose inherit to retain transport compatibility, or select the provider-specific User-Agent behavior explicitly."},
		{"query must be normalized", "upstream_profiles.*.query", []string{"normalized", "preserve-raw"}, "Use preserve-raw only when the route has no query transforms."},
	}
	for _, classification := range classifications {
		if strings.Contains(message, classification.contains) {
			if strings.HasPrefix(issue.Path, "routes[") && (strings.HasPrefix(classification.path, "availability") || classification.path == "transport") {
				issue.Path += "." + classification.path
			} else if !strings.HasPrefix(issue.Path, "routes[") {
				issue.Path = classification.path
			}
			issue.Code = "invalid_enum"
			issue.Allowed = classification.allowed
			issue.SuggestedAction = classification.action
			break
		}
	}
	return issue
}

func (registry *protocolRegistry) maintenancePutPack(input maintenancePackInput) (any, *maintenanceToolFailure) {
	if !protocolIDPattern.MatchString(input.PackID) || strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Description) == "" {
		failure := maintenanceFailure(registry, "invalid_pack_core", "input", "pack_id, name, and description are required", false, true)
		return nil, &failure
	}
	registry.lifecycleMu.Lock()
	defer registry.lifecycleMu.Unlock()
	root, ok := registry.protocolDraftRoot(input.DraftID)
	if !ok {
		failure := maintenanceFailure(registry, "unknown_draft", "draft", "unknown draft", false, false)
		return nil, &failure
	}
	path := filepath.Join(root, input.PackID+".json")
	definition, err := registry.readMaintenanceDefinition(root, input.PackID)
	newPack := errors.Is(err, os.ErrNotExist)
	if err != nil && !newPack {
		return nil, maintenanceValidationFailure(registry, "edit", err)
	}
	if newPack && strings.TrimSpace(input.BaseURL) == "" {
		failure := maintenanceFailure(registry, "base_url_required", "input", "base_url is required when creating a Pack; omit it only when updating an existing Pack", false, true)
		return nil, &failure
	}
	definition.SchemaVersion = protocolSchemaVersionV3
	definition.ID = input.PackID
	definition.Name = strings.TrimSpace(input.Name)
	definition.Description = strings.TrimSpace(input.Description)
	if strings.TrimSpace(input.BaseURL) != "" {
		definition.BaseURL = strings.TrimSpace(input.BaseURL)
	}
	if input.Enabled != nil {
		definition.Enabled = *input.Enabled
	} else if newPack {
		definition.Enabled = true
	}
	if input.Timeout != nil {
		definition.Timeout = *input.Timeout
	}
	if input.Auth != nil {
		definition.Auth = *input.Auth
	} else if newPack {
		definition.Auth = protocolAuth{Type: "none"}
	}
	if input.Pool != nil {
		definition.Pool = input.Pool
	}
	if input.Targets != nil {
		definition.Targets = *input.Targets
	}
	if input.Readiness != nil {
		definition.Readiness = input.Readiness
	}
	if err := validateMaintenanceDefinition(registry, &definition, true); err != nil {
		return nil, maintenanceValidationFailure(registry, "validation", err)
	}
	if err := writeMaintenanceDefinition(path, definition); err != nil {
		failure := maintenanceFailure(registry, "pack_write_failed", "edit", err.Error(), true, true)
		return nil, &failure
	}
	registry.invalidateDraftPlanLocked(input.DraftID)
	pending := []string{}
	if len(definition.Routes) == 0 {
		pending = append(pending, "operations")
	}
	return gin.H{"draft": registry.draftView(input.DraftID, root), "pack_id": input.PackID, "created": newPack, "canonical_defaults": gin.H{"schema_version": "3", "auth.type": definition.Auth.Type, "timeout_seconds": definition.Timeout, "readiness.mode": definition.Readiness.Mode}, "pending": pending, "next_action": "Add operations with localrouter_draft_put_operation, then lint and review the complete draft."}, nil
}

func (registry *protocolRegistry) maintenancePutOperation(input maintenanceOperationInput) (any, *maintenanceToolFailure) {
	if !protocolIDPattern.MatchString(input.PackID) || !protocolOperationIDPattern.MatchString(input.Operation.OperationID) {
		failure := maintenanceFailure(registry, "invalid_operation_id", "input", "pack_id or operation.operation_id is invalid", false, true)
		return nil, &failure
	}
	registry.lifecycleMu.Lock()
	defer registry.lifecycleMu.Unlock()
	root, ok := registry.protocolDraftRoot(input.DraftID)
	if !ok {
		failure := maintenanceFailure(registry, "unknown_draft", "draft", "unknown draft", false, false)
		return nil, &failure
	}
	definition, err := registry.readMaintenanceDefinition(root, input.PackID)
	if err != nil {
		failure := maintenanceFailure(registry, "pack_not_found", "edit", "create the Pack core with localrouter_draft_put_pack first", false, true)
		return nil, &failure
	}
	operation := input.Operation
	if operation.Transport == "" {
		operation.Transport = "http"
	}
	if operation.Retry.Mode == "" {
		operation.Retry.Mode = "safe"
	}
	if operation.Availability.Status == "" {
		operation.Availability.Status = "draft"
		operation.Availability.Reason = "not yet verified"
	}
	replaced := false
	for index := range definition.Routes {
		if definition.Routes[index].OperationID == operation.OperationID {
			definition.Routes[index] = operation
			replaced = true
			break
		}
	}
	if !replaced {
		definition.Routes = append(definition.Routes, operation)
	}
	if err := validateMaintenanceDefinition(registry, &definition, false); err != nil {
		return nil, maintenanceValidationFailure(registry, "validation", err)
	}
	if err := writeMaintenanceDefinition(filepath.Join(root, input.PackID+".json"), definition); err != nil {
		failure := maintenanceFailure(registry, "pack_write_failed", "edit", err.Error(), true, true)
		return nil, &failure
	}
	registry.invalidateDraftPlanLocked(input.DraftID)
	return gin.H{"draft": registry.draftView(input.DraftID, root), "pack_id": input.PackID, "operation_id": operation.OperationID, "replaced": replaced, "canonical_defaults": gin.H{"transport": operation.Transport, "retry.mode": operation.Retry.Mode, "availability.status": operation.Availability.Status}, "next_action": "Attach a supplier profile if needed, then call localrouter_draft_lint_pack."}, nil
}

func (registry *protocolRegistry) maintenancePutUpstreamProfile(input maintenanceUpstreamProfileInput) (any, *maintenanceToolFailure) {
	if !protocolIDPattern.MatchString(input.PackID) || !protocolOperationIDPattern.MatchString(input.ProfileID) {
		failure := maintenanceFailure(registry, "invalid_profile_id", "input", "pack_id or profile_id is invalid", false, true)
		return nil, &failure
	}
	registry.lifecycleMu.Lock()
	defer registry.lifecycleMu.Unlock()
	root, ok := registry.protocolDraftRoot(input.DraftID)
	if !ok {
		failure := maintenanceFailure(registry, "unknown_draft", "draft", "unknown draft", false, false)
		return nil, &failure
	}
	definition, err := registry.readMaintenanceDefinition(root, input.PackID)
	if err != nil {
		failure := maintenanceFailure(registry, "pack_not_found", "edit", "create the Pack core first", false, true)
		return nil, &failure
	}
	if definition.UpstreamProfiles == nil {
		definition.UpstreamProfiles = make(map[string]protocolUpstreamProfile)
	}
	_, replaced := definition.UpstreamProfiles[input.ProfileID]
	definition.UpstreamProfiles[input.ProfileID] = input.Profile
	if err := validateMaintenanceDefinition(registry, &definition, len(definition.Routes) == 0); err != nil {
		return nil, maintenanceValidationFailure(registry, "validation", err)
	}
	if err := writeMaintenanceDefinition(filepath.Join(root, input.PackID+".json"), definition); err != nil {
		failure := maintenanceFailure(registry, "pack_write_failed", "edit", err.Error(), true, true)
		return nil, &failure
	}
	registry.invalidateDraftPlanLocked(input.DraftID)
	affected := make([]string, 0)
	for _, route := range definition.Routes {
		if route.UpstreamProfile == input.ProfileID {
			affected = append(affected, route.OperationID)
			continue
		}
		for _, profileID := range route.UpstreamProfilesByTarget {
			if profileID == input.ProfileID {
				affected = append(affected, route.OperationID)
				break
			}
		}
	}
	sort.Strings(affected)
	return gin.H{"draft": registry.draftView(input.DraftID, root), "pack_id": input.PackID, "profile_id": input.ProfileID, "replaced": replaced, "affected_operations": affected, "canonical": gin.H{"user_agent": definition.UpstreamProfiles[input.ProfileID].UserAgent, "query": definition.UpstreamProfiles[input.ProfileID].Query}, "next_action": "Reference this profile from an operation, then call localrouter_draft_lint_pack."}, nil
}

func (registry *protocolRegistry) maintenanceLintPack(draftID, packID string) (any, *maintenanceToolFailure) {
	if !protocolIDPattern.MatchString(packID) {
		failure := maintenanceFailure(registry, "invalid_pack_id", "input", "pack_id is invalid", false, true)
		return nil, &failure
	}
	registry.lifecycleMu.Lock()
	defer registry.lifecycleMu.Unlock()
	root, ok := registry.protocolDraftRoot(draftID)
	if !ok {
		failure := maintenanceFailure(registry, "unknown_draft", "draft", "unknown draft", false, false)
		return nil, &failure
	}
	definition, err := registry.readMaintenanceDefinition(root, packID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			failure := maintenanceFailure(registry, "pack_not_found", "validation", "Pack does not exist in this draft", false, true)
			return nil, &failure
		}
		issue := maintenanceValidationIssueFromError(err)
		issue.Path = "$"
		issue.Code = "invalid_json"
		return gin.H{"draft_id": draftID, "pack_id": packID, "valid": false, "issues": []maintenanceValidationIssue{issue}, "next_action": "Repair the reported field and lint again; the draft was not changed."}, nil
	}
	if err := registry.validate(&definition); err != nil {
		return gin.H{"draft_id": draftID, "pack_id": packID, "valid": false, "issues": []maintenanceValidationIssue{maintenanceValidationIssueFromError(err)}, "next_action": "Repair the reported field and lint again; the draft was not changed."}, nil
	}
	return gin.H{"draft_id": draftID, "pack_id": packID, "valid": true, "issues": []maintenanceValidationIssue{}, "operations": len(definition.Routes), "upstream_profiles": len(definition.UpstreamProfiles), "next_action": "Call localrouter_draft_review and inspect the full draft impact."}, nil
}

func attachMaintenanceValidationIssue(failure *maintenanceToolFailure, err error) {
	if failure != nil && err != nil && len(failure.Issues) == 0 {
		failure.Issues = []maintenanceValidationIssue{maintenanceValidationIssueFromError(err)}
	}
}
