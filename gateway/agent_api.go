package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const agentContractSchemaVersion = "9"

type agentOperationRef struct {
	OperationKey string   `json:"operation_key"`
	Pack         string   `json:"pack"`
	Operation    string   `json:"operation"`
	OperationID  string   `json:"operation_id"`
	Capabilities []string `json:"capabilities,omitempty"`
	Summary      string   `json:"summary,omitempty"`
	Ready        bool     `json:"ready"`
	Status       string   `json:"status"`
	CallURL      string   `json:"call_url"`
	Methods      []string `json:"methods,omitempty"`
}

type agentOperationDescriptor struct {
	ContractDigest        string                          `json:"contract_digest"`
	ContractSchemaVersion string                          `json:"contract_schema_version"`
	OperationKey          string                          `json:"operation_key"`
	Pack                  string                          `json:"pack"`
	PackName              string                          `json:"pack_name"`
	Operation             string                          `json:"operation"`
	OperationID           string                          `json:"operation_id"`
	OperationIDRole       string                          `json:"operation_id_role"`
	OperationIDIsURL      bool                            `json:"operation_id_is_url"`
	Capabilities          []string                        `json:"capabilities"`
	Aliases               []string                        `json:"aliases"`
	Summary               string                          `json:"summary"`
	Ready                 bool                            `json:"ready"`
	Status                string                          `json:"status"`
	Reason                string                          `json:"reason,omitempty"`
	NextAction            string                          `json:"next_action,omitempty"`
	Methods               []string                        `json:"methods"`
	Path                  string                          `json:"path"`
	CallURL               string                          `json:"call_url"`
	PathParameters        []string                        `json:"path_parameters"`
	QueryParameters       []string                        `json:"query_parameters"`
	Streaming             bool                            `json:"streaming"`
	Transport             string                          `json:"transport"`
	SideEffecting         bool                            `json:"side_effecting"`
	RequestSchema         any                             `json:"request_schema"`
	SchemaSource          string                          `json:"schema_source"`
	RequestExample        any                             `json:"request_example,omitempty"`
	RequestExampleRole    string                          `json:"request_example_role,omitempty"`
	DynamicInputs         map[string]protocolDynamicInput `json:"dynamic_inputs,omitempty"`
	ResponseSchema        any                             `json:"response_schema"`
	Retry                 protocolRetryConfig             `json:"retry"`
	Verification          protocolRouteAvailability       `json:"verification"`
	Pricing               []protocolPricingEntry          `json:"pricing"`
	Guides                []protocolGuideSummary          `json:"guides"`
	Workflows             []string                        `json:"workflows"`
	PoolID                string                          `json:"pool_id"`
	Pool                  protocolPoolView                `json:"pool"`
	Call                  map[string]any                  `json:"call"`
}

type agentPreflightCheck struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Message  string `json:"message"`
	Blocking bool   `json:"blocking"`
}

type agentResolveRequest struct {
	Query        string `json:"query"`
	Limit        int    `json:"limit,omitempty"`
	RequireReady *bool  `json:"require_ready,omitempty"`
}

type agentCompareRequest struct {
	OperationKeys []string `json:"operation_keys"`
}

type agentPreflightRequest struct {
	Pack            string            `json:"pack"`
	Operation       string            `json:"operation"`
	Method          string            `json:"method,omitempty"`
	Input           json.RawMessage   `json:"input,omitempty"`
	PathParameters  map[string]string `json:"path_parameters,omitempty"`
	QueryParameters map[string]string `json:"query_parameters,omitempty"`
}

type agentResolveCandidate struct {
	Descriptor agentOperationDescriptor
	Score      int
	MatchKind  string
	GuideRank  int
}

type agentResolvedMatch struct {
	agentOperationDescriptor
	MatchKind string `json:"match_kind"`
	Score     int    `json:"match_score"`
}

func decodeAgentRequest(c *gin.Context, destination any) error {
	decoder := json.NewDecoder(io.LimitReader(c.Request.Body, maxTokenPolicyBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("request must contain one JSON object")
	}
	return nil
}

func (registry *protocolRegistry) handleAgentDocs(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	console := "/#tokens"
	if requestServerScope(c) == lanServiceScope {
		console = "loopback-only"
	}
	c.JSON(http.StatusOK, gin.H{
		"object": "localrouter.agent.contract", "schema_version": agentContractSchemaVersion,
		"contract_digest": registry.currentDigest(),
		"authentication":  gin.H{"type": "bearer", "header": "Authorization", "token_purpose": "service-call-only"},
		"identity": gin.H{
			"binding": "service-token", "registration": "operator-issued", "console": console,
			"required_fields": []string{"agent_code", "agent_name", "workspace"},
			"whoami":          "/agent/whoami", "secret_values_returned": false,
		},
		"flow": []string{"catalog-or-resolve", "compare-optional", "agent-chooses-operation-key", "describe", "preflight", "run", "watch-or-read-result"},
		"selection": gin.H{
			"mode": "agent", "merged": false,
			"rule": "every Pack operation remains independently addressable; LocalRouter never collapses providers or silently chooses one",
		},
		"service_packs": gin.H{
			"discovery":     "lr tree [pack]",
			"compatibility": "lightweight standard API Pack on /v1 or /v1beta, backed by Channel Profiles and Channels; invoke its published path with lr request",
			"protocol":      "full Pack on /p/<pack> for isolated paths, transforms, special auth, dedicated pools, adapters or workflows; use describe and preflight",
			"projections":   gin.H{"workflow": "/w/{pack}", "mcp": "/mcp"},
		},
		"discovery_domains": gin.H{
			"operation": gin.H{
				"contains": "callable capabilities, operation keys, methods, schemas and call URLs",
				"cli":      "lr find operation <intent>",
				"next":     "choose an operation_key, then use lr describe and lr preflight",
			},
			"model": gin.H{
				"contains": "current model IDs returned by ready Packs' advertised model catalog operations",
				"cli":      "lr find model --exact <pack>:<model-id>",
				"next":     "require one exact live provider-qualified model_key and choose that Pack's compatible operation",
			},
			"pool": gin.H{
				"contains": "Pack readiness, credential eligibility, quota freshness and pricing metadata",
				"cli":      "lr find pool <provider-or-pack>",
				"next":     "require ready=true and inspect quota and pricing independently; unknown never means free or zero",
			},
			"agent_runtime": gin.H{
				"contains":          "OMP or another Agent runtime's own model assignment and configuration",
				"localrouter_owned": false,
				"next":              "after choosing an exact operation_key and model_key, configure the external runtime with the published endpoint and model ID",
			},
		},
		"identifiers": gin.H{
			"operation_key":       "stable choice identity: <pack>.<operation_id>",
			"operation_id":        "semantic selector accepted by describe, preflight, lr and MCP; never append it to /p/<pack>",
			"operation_id_is_url": false,
			"call_url":            "fully resolved LocalRouter HTTP path; use this exact value for direct HTTP",
		},
		"endpoints": gin.H{
			"catalog": gin.H{"method": "GET", "path": "/agent/operations", "result": "all independently addressable operations allowed by the service Token"},
			"resolve": gin.H{
				"method": "POST", "path": "/agent/resolve",
				"request_schema": gin.H{"type": "object", "required": []string{"query"}, "properties": gin.H{"query": gin.H{"type": "string"}, "limit": gin.H{"type": "integer", "minimum": 1, "maximum": 200, "default": 100}, "require_ready": gin.H{"type": "boolean", "default": false}}},
				"result":         "independent matches; no selected provider is returned",
			},
			"describe": gin.H{"method": "GET", "path": "/agent/operations/{pack}/{operation}"},
			"compare": gin.H{
				"method": "POST", "path": "/agent/compare",
				"request_schema": gin.H{"type": "object", "required": []string{"operation_keys"}, "properties": gin.H{"operation_keys": gin.H{"type": "array", "minItems": 2, "maxItems": 50, "uniqueItems": true, "items": gin.H{"type": "string"}}}},
				"result":         "full independent operation contracts in caller order; no provider is selected or merged",
			},
			"preflight": gin.H{
				"method": "POST", "path": "/agent/preflight", "upstream_called": false,
				"request_schema": gin.H{"type": "object", "required": []string{"pack", "operation"}, "properties": gin.H{"pack": gin.H{"type": "string"}, "operation": gin.H{"type": "string"}, "method": gin.H{"type": "string"}, "input": gin.H{}, "path_parameters": gin.H{"type": "object", "additionalProperties": gin.H{"type": "string"}}, "query_parameters": gin.H{"type": "object", "additionalProperties": gin.H{"type": "string"}}}},
			},
			"whoami":          gin.H{"method": "GET", "path": "/agent/whoami", "secret_values_returned": false},
			"workflow_create": gin.H{"method": "POST", "path": "/w/{pack}/{workflow}"},
			"workflow_read":   gin.H{"method": "GET", "path": "/w/{pack}/{workflow}/{job}"},
			"workflow_cancel": gin.H{"method": "DELETE", "path": "/w/{pack}/{workflow}/{job}", "only_when_advertised": true},
		},
		"cli": gin.H{
			"find": "lr find <intent>", "find_operation": "lr find operation <intent>", "find_model": "lr find model [--exact] [--limit N|--all] <name>", "find_pool": "lr find pool <provider-or-pack>",
			"catalog": "lr catalog [pack] [--limit N|--all]", "resolve": "lr resolve <capability-or-intent> [limit]", "describe": "lr describe <pack> <operation>", "compare": "lr compare <pack.operation> <pack.operation> [...]",
			"preflight": "lr preflight <pack> <operation> '<json>'", "run": "lr run <pack> <operation|workflow> '<json>'",
			"watch": "lr watch <pack> <workflow> <job> [timeout-seconds]", "cancel": "lr cancel <pack> <workflow> <job>",
		},
		"direct_http": gin.H{
			"rule":          "use call_url and an advertised method; never derive a URL from operation_id",
			"authorization": "Authorization: Bearer <service-token>",
		},
		"request_examples": gin.H{
			"role": "illustrative-shape",
			"rule": "example values are not availability evidence; resolve fields listed in dynamic_inputs before calling",
		},
		"errors": gin.H{
			"fields":          []string{"code", "reason", "retryable", "retry_after", "next_action", "alternatives"},
			"unknown_outcome": "never replay a side-effecting request until provider state is reconciled",
		},
		"cache": "reuse operation contracts until contract_digest or schema_version changes",
	})
}

func handleAgentWhoAmI(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenID := c.GetInt(tokenPolicyContextID)
		response := gin.H{
			"object": "localrouter.identity", "token_id": tokenID,
			"token_name": c.GetString("token_name"), "service_access": true,
			"maintenance_access": false, "contract_digest": runtime.protocols.currentDigest(),
			"contract_schema_version": agentContractSchemaVersion,
		}
		if runtime.store != nil && tokenID > 0 {
			if token, err := runtime.store.tokenByID(runtime.rootUser.ID, tokenID, false); err == nil {
				response["token_name"] = token.Name
				response["agent_code"] = token.AgentCode
				response["agent_name"] = token.AgentName
				response["workspace"] = token.Workspace
				response["runtime"] = token.Runtime
				response["created_at"] = token.CreatedTime
				response["expires_at"] = token.ExpiredTime
			}
		}
		if policy, exists := runtime.policies.policyFor(tokenID); exists {
			response["unrestricted"] = false
			response["policy"] = policy
		} else {
			response["unrestricted"] = true
			response["policy"] = gin.H{"surfaces": []string{"*"}, "packs": []string{"*"}, "operations": []string{"*"}}
		}
		c.JSON(http.StatusOK, response)
	}
}

func (registry *protocolRegistry) handleAgentResolve(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request agentResolveRequest
		if err := decodeAgentRequest(c, &request); err != nil {
			writeAgentError(c, http.StatusBadRequest, "invalid_arguments", "resolve requires a valid JSON request", err.Error(), false, "agent", "send {query, limit?, require_ready?}", nil, nil, nil)
			return
		}
		request.Query = strings.TrimSpace(request.Query)
		if request.Query == "" {
			writeAgentError(c, http.StatusBadRequest, "query_required", "resolve query is required", "empty capability or intent", false, "agent", "provide a capability such as web.search", nil, nil, nil)
			return
		}
		if request.Limit == 0 {
			request.Limit = 100
		}
		if request.Limit < 1 || request.Limit > 200 {
			writeAgentError(c, http.StatusBadRequest, "invalid_limit", "resolve limit must be between 1 and 200", "limit out of range", false, "agent", "use a limit from 1 to 200", nil, nil, nil)
			return
		}
		requireReady := false
		if request.RequireReady != nil {
			requireReady = *request.RequireReady
		}
		candidates := registry.resolveCandidates(c.GetInt(tokenPolicyContextID), request.Query)
		if len(candidates) == 0 {
			writeAgentError(c, http.StatusNotFound, "capability_not_found", "no operation matched the requested capability", request.Query, false, "agent", "use lr find operation <intent>; use lr find model <name> for model IDs or lr find pool <provider> for quota and readiness", nil, nil, nil)
			return
		}
		eligible := candidates
		if requireReady {
			ready := make([]agentResolveCandidate, 0, len(candidates))
			for _, candidate := range candidates {
				if candidate.Descriptor.Ready {
					ready = append(ready, candidate)
				}
			}
			if len(ready) == 0 {
				alternatives := make([]agentOperationRef, 0, min(request.Limit, len(candidates)))
				for _, candidate := range candidates {
					item := candidate.Descriptor
					alternatives = append(alternatives, agentOperationRef{OperationKey: item.OperationKey, Pack: item.Pack, Operation: item.Operation, OperationID: item.OperationID, Capabilities: item.Capabilities, Summary: item.Summary, Ready: item.Ready, Status: item.Status, CallURL: item.CallURL, Methods: item.Methods})
					if len(alternatives) == request.Limit {
						break
					}
				}
				writeAgentError(c, http.StatusServiceUnavailable, "no_ready_operation", "matching operations exist but none is ready", request.Query, false, "localrouter", "inspect the listed Pack status or resolve another capability", nil, alternatives, nil)
				return
			}
			eligible = ready
		}
		total := len(eligible)
		if total > request.Limit {
			eligible = eligible[:request.Limit]
		}
		matches := make([]agentResolvedMatch, 0, len(eligible))
		for _, candidate := range eligible {
			matches = append(matches, agentResolvedMatch{agentOperationDescriptor: candidate.Descriptor, MatchKind: candidate.MatchKind, Score: candidate.Score})
		}
		c.JSON(http.StatusOK, gin.H{
			"object": "localrouter.operation.matches", "query": request.Query,
			"contract_digest": registry.currentDigest(), "schema_version": agentContractSchemaVersion,
			"selection_mode": "agent", "merged": false, "selection_required": total > 1,
			"count": total, "returned": len(matches), "truncated": total > len(matches), "matches": matches,
		})
	}
}

func (registry *protocolRegistry) handleAgentCompare(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request agentCompareRequest
		if err := decodeAgentRequest(c, &request); err != nil {
			writeAgentError(c, http.StatusBadRequest, "invalid_arguments", "compare requires a valid JSON request", err.Error(), false, "agent", "send {operation_keys:[\"pack.operation\", ...]}", nil, nil, nil)
			return
		}
		if len(request.OperationKeys) < 2 || len(request.OperationKeys) > 50 {
			writeAgentError(c, http.StatusBadRequest, "invalid_operation_count", "compare requires between 2 and 50 operations", strconv.Itoa(len(request.OperationKeys)), false, "agent", "provide 2 to 50 unique operation keys", nil, nil, nil)
			return
		}
		seen := make(map[string]bool, len(request.OperationKeys))
		operations := make([]agentOperationDescriptor, 0, len(request.OperationKeys))
		for _, rawKey := range request.OperationKeys {
			operationKey := strings.TrimSpace(rawKey)
			if operationKey == "" || seen[operationKey] {
				writeAgentError(c, http.StatusBadRequest, "invalid_operation_key", "compare operation keys must be non-empty and unique", operationKey, false, "agent", "use exact keys returned by resolve or catalog", nil, nil, nil)
				return
			}
			seen[operationKey] = true
			packID, operationID, ok := strings.Cut(operationKey, ".")
			if !ok || !protocolIDPattern.MatchString(packID) || !protocolOperationIDPattern.MatchString(operationID) {
				writeAgentError(c, http.StatusBadRequest, "invalid_operation_key", "compare operation key is invalid", operationKey, false, "agent", "use pack.operation from resolve or catalog", nil, nil, nil)
				return
			}
			descriptor, found := registry.describeOperation(packID, operationID)
			if !found {
				writeAgentError(c, http.StatusNotFound, "operation_not_found", "compare operation was not found", operationKey, false, "agent", "refresh the catalog and use a published operation key", nil, nil, nil)
				return
			}
			if allowed, _, reason := runtime.policies.preview(c.GetInt(tokenPolicyContextID), "p", descriptor.Pack, descriptor.Operation, ""); !allowed {
				writeAgentError(c, http.StatusForbidden, "token_policy_denied", "token policy does not allow a compared operation", reason, false, "localrouter", "remove the operation or ask the operator to adjust this service Token policy", nil, nil, nil)
				return
			}
			operations = append(operations, descriptor)
		}
		c.JSON(http.StatusOK, gin.H{
			"object": "localrouter.operation.comparison", "schema_version": agentContractSchemaVersion,
			"contract_digest": registry.currentDigest(), "selection_mode": "agent", "merged": false,
			"recommendation": nil, "count": len(operations),
			"dimensions": []string{"readiness", "verification", "pool", "pricing", "methods", "transport", "streaming", "side_effects", "request_schema", "retry", "workflows"},
			"operations": operations,
		})
	}
}

func (registry *protocolRegistry) handleAgentCatalog(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		packFilter := strings.TrimSpace(c.Query("pack"))
		if packFilter != "" && !protocolIDPattern.MatchString(packFilter) {
			writeAgentError(c, http.StatusBadRequest, "invalid_pack", "catalog Pack filter is invalid", packFilter, false, "agent", "use a published Pack ID", nil, nil, nil)
			return
		}
		if packFilter != "" {
			if _, exists := registry.viewFor(packFilter); !exists {
				writeAgentError(c, http.StatusNotFound, "pack_not_found", "catalog Pack was not found", packFilter, false, "agent", "use lr catalog without a Pack filter", nil, nil, nil)
				return
			}
		}
		operations := make([]agentOperationDescriptor, 0)
		for _, view := range registry.views() {
			if packFilter != "" && view.ID != packFilter {
				continue
			}
			for _, route := range view.Routes {
				if allowed, _, _ := runtime.policies.preview(c.GetInt(tokenPolicyContextID), "p", view.ID, route.OperationID, ""); !allowed {
					continue
				}
				operations = append(operations, registry.describeOperationFromView(view, route))
			}
		}
		c.JSON(http.StatusOK, gin.H{
			"object": "localrouter.operation.list", "schema_version": agentContractSchemaVersion,
			"contract_digest": registry.currentDigest(), "selection_mode": "agent", "merged": false,
			"count": len(operations), "operations": operations,
		})
	}
}

func (registry *protocolRegistry) handleAgentDescribe(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		descriptor, ok := registry.describeOperation(c.Param("protocol"), c.Param("operation"))
		if !ok {
			writeAgentError(c, http.StatusNotFound, "operation_not_found", "operation was not found", c.Param("protocol")+"."+c.Param("operation"), false, "agent", "refresh with lr catalog <pack> or search with lr find operation <intent>", nil, nil, nil)
			return
		}
		if allowed, _, reason := runtime.policies.preview(c.GetInt(tokenPolicyContextID), "p", descriptor.Pack, descriptor.Operation, ""); !allowed {
			writeAgentError(c, http.StatusForbidden, "token_policy_denied", "token policy does not allow this operation", reason, false, "localrouter", "ask the operator to adjust this service Token policy", nil, nil, nil)
			return
		}
		c.JSON(http.StatusOK, gin.H{"object": "localrouter.operation", "schema_version": agentContractSchemaVersion, "data": descriptor})
	}
}

func (registry *protocolRegistry) handleAgentPreflight(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request agentPreflightRequest
		if err := decodeAgentRequest(c, &request); err != nil {
			writeAgentError(c, http.StatusBadRequest, "invalid_arguments", "preflight requires a valid JSON request", err.Error(), false, "agent", "send {pack, operation, input?, path_parameters?, query_parameters?, method?}", nil, nil, nil)
			return
		}
		descriptor, ok := registry.describeOperation(strings.TrimSpace(request.Pack), strings.TrimSpace(request.Operation))
		if !ok {
			writeAgentError(c, http.StatusNotFound, "operation_not_found", "operation was not found", request.Pack+"."+request.Operation, false, "agent", "refresh with lr catalog <pack> or search with lr find operation <intent>", nil, nil, nil)
			return
		}
		input := any(map[string]any{})
		var inputError error
		if len(request.Input) > 0 {
			decoder := json.NewDecoder(bytes.NewReader(request.Input))
			decoder.UseNumber()
			inputError = decoder.Decode(&input)
		}
		modelName := agentInputModel(input)
		if modelName == "" {
			modelName = strings.TrimSpace(request.PathParameters["model"])
		}
		checks := make([]agentPreflightCheck, 0, 8)
		addCheck := func(name, status, message string, blocking bool) {
			checks = append(checks, agentPreflightCheck{Name: name, Status: status, Message: message, Blocking: blocking})
		}
		allowed, policyStatus, policyReason := runtime.policies.preview(c.GetInt(tokenPolicyContextID), "p", descriptor.Pack, descriptor.Operation, modelName)
		if allowed {
			message := "service Token allows this operation"
			if modelName != "" {
				message += " and does not deny model selector " + modelName
			}
			addCheck("authorization", "pass", message, false)
		} else {
			addCheck("authorization", "fail", policyReason, true)
		}
		if descriptor.Ready {
			addCheck("readiness", "pass", "Pack and operation are ready", false)
		} else {
			addCheck("readiness", "fail", descriptor.Reason, true)
		}
		method := strings.ToUpper(strings.TrimSpace(request.Method))
		if method == "" && len(descriptor.Methods) > 0 {
			method = descriptor.Methods[0]
		}
		if containsString(descriptor.Methods, method) {
			addCheck("method", "pass", method+" is allowed", false)
		} else {
			addCheck("method", "fail", method+" is not allowed", true)
		}
		missingParameters := make([]string, 0)
		for _, parameter := range descriptor.PathParameters {
			if strings.TrimSpace(request.PathParameters[parameter]) == "" {
				missingParameters = append(missingParameters, parameter)
			}
		}
		if len(missingParameters) == 0 {
			addCheck("path_parameters", "pass", "all required path parameters are present", false)
		} else {
			addCheck("path_parameters", "fail", "missing: "+strings.Join(missingParameters, ", "), true)
		}
		missingQueryParameters := make([]string, 0)
		for _, parameter := range descriptor.QueryParameters {
			if strings.TrimSpace(request.QueryParameters[parameter]) == "" {
				missingQueryParameters = append(missingQueryParameters, parameter)
			}
		}
		if len(missingQueryParameters) == 0 {
			addCheck("query_parameters", "pass", "all required query parameters are present", false)
		} else {
			addCheck("query_parameters", "fail", "missing: "+strings.Join(missingQueryParameters, ", "), true)
		}
		if inputError != nil {
			addCheck("input", "fail", "input is not valid JSON", true)
		}
		if violations := validateAgentSchemaValue(descriptor.RequestSchema, input, "$"); len(violations) > 0 {
			blocking := descriptor.SchemaSource == "explicit"
			status := "warn"
			if blocking {
				status = "fail"
			}
			addCheck("request_schema", status, descriptor.SchemaSource+" schema: "+strings.Join(violations, "; "), blocking)
		} else {
			addCheck("request_schema", "pass", descriptor.SchemaSource+" request schema accepts the input", false)
		}
		dynamicFields := make([]string, 0, len(descriptor.DynamicInputs))
		mappedInput, _ := input.(map[string]any)
		for field := range descriptor.DynamicInputs {
			dynamicFields = append(dynamicFields, field)
		}
		sort.Strings(dynamicFields)
		for _, field := range dynamicFields {
			value, _ := mappedInput[field].(string)
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			dynamic := descriptor.DynamicInputs[field]
			source := dynamic.SourceOperationKey
			if dynamic.SourceKind == "request-schema-enum" {
				source = "the current versioned request schema enum"
			}
			addCheck("dynamic_inputs."+field, "warn", "preflight does not refresh model discovery; first require an exact match for "+descriptor.Pack+":"+value+" from "+source, false)
		}
		if len(descriptor.Pricing) == 0 {
			addCheck("pricing", "warn", "operation price is unknown", false)
		} else {
			addCheck("pricing", "pass", "pricing status: "+descriptor.Pricing[0].Status, false)
		}
		if descriptor.SideEffecting {
			addCheck("side_effects", "warn", "the selected method may create or modify provider state; preserve any returned resource or Job ID", false)
		} else {
			addCheck("side_effects", "pass", "the selected method is read-only", false)
		}
		ok = true
		for _, check := range checks {
			if check.Blocking && check.Status == "fail" {
				ok = false
				break
			}
		}
		nextAction := "call the operation using the returned LocalRouter path"
		if !ok {
			nextAction = descriptor.NextAction
			if !allowed && policyStatus != 0 {
				nextAction = "ask the operator to adjust this service Token policy"
			}
		}
		alternatives := []agentOperationRef{}
		if !ok {
			alternatives = registry.alternativeOperationRefs(c.GetInt(tokenPolicyContextID), descriptor.Pack, descriptor.Operation)
		}
		var code any
		var reason any
		if !ok {
			code = "preflight_blocked"
			reason = "one or more blocking preflight checks failed"
		}
		c.JSON(http.StatusOK, gin.H{
			"object": "localrouter.preflight", "success": ok, "ok": ok, "code": code,
			"reason": reason, "retryable": false, "owner": "agent", "upstream_called": false,
			"contract_digest": registry.currentDigest(), "schema_version": agentContractSchemaVersion, "operation": descriptor,
			"checks": checks, "next_action": nextAction, "alternatives": alternatives,
		})
	}
}

func (registry *protocolRegistry) describeOperation(packID, operationID string) (agentOperationDescriptor, bool) {
	view, ok := registry.viewFor(packID)
	if !ok {
		return agentOperationDescriptor{}, false
	}
	var route protocolRoute
	found := false
	for _, candidate := range view.Routes {
		if candidate.OperationID == operationID {
			route, found = candidate, true
			break
		}
	}
	if !found {
		return agentOperationDescriptor{}, false
	}
	return registry.describeOperationFromView(view, route), true
}

func (registry *protocolRegistry) describeOperationFromView(view protocolView, route protocolRoute) agentOperationDescriptor {
	available, availabilityStatus := routeAvailabilityReady(route.Availability, time.Now().UTC())
	operationEnabled := registry.operationEnabled(view.ID, route.OperationID)
	ready := view.Ready && operationEnabled && available
	status := view.Status
	if view.Ready && !operationEnabled {
		status = "disabled"
	} else if view.Ready && !available {
		status = availabilityStatus
	}
	reason, nextAction, _, _, _ := agentReadinessAdvice(view, status)
	requestSchema := plainAgentJSONValue(protocolOpenAPISchema(route.RequestSchema, route.RequestBody))
	schemaSource := "open"
	if len(route.RequestSchema) > 0 {
		schemaSource = "explicit"
	} else if len(route.RequestBody) > 0 {
		schemaSource = "inferred-from-example"
	}
	responseSchema := plainAgentJSONValue(protocolOpenAPISchema(route.ResponseSchema, nil))
	var requestExample any
	if len(route.RequestBody) > 0 {
		_ = json.Unmarshal(route.RequestBody, &requestExample)
	}
	parameters := make([]string, 0)
	for parameter := range protocolPathParameterNames(route.Path) {
		parameters = append(parameters, parameter)
	}
	sort.Strings(parameters)
	guides := make([]protocolGuideSummary, 0)
	for _, guide := range view.Guides {
		if containsString(guide.Operations, route.OperationID) {
			guides = append(guides, guide)
		}
	}
	workflows := operationWorkflows(view.Workflows, route.OperationID)
	capabilities := append([]string(nil), route.Capabilities...)
	sort.Strings(capabilities)
	pricing := operationPricing(view.Pricing, route.OperationID)
	aliases := operationAliases(pricing, capabilities)
	transport := route.Transport
	if transport == "" {
		transport = "http"
	}
	retry := route.Retry
	if retry.Mode == "" {
		retry.Mode = "safe"
	}
	callURL := view.Mount + route.Path
	published := publishProtocolRoute(view.ID, view.Mount, view.Routes, route)
	call := map[string]any{
		"authenticated": true, "authorization": "Bearer service token",
		"methods": append([]string(nil), route.Methods...), "default_method": route.Methods[0],
		"url":                 callURL,
		"operation_id_is_url": false,
	}
	if len(route.QueryParameters) > 0 {
		call["required_query"] = append([]string(nil), route.QueryParameters...)
	}
	if len(parameters) == 0 {
		call["cli"] = "lr call " + view.ID + " " + route.OperationID + " '<json>'"
	}
	return agentOperationDescriptor{
		ContractDigest: registry.currentDigest(), ContractSchemaVersion: agentContractSchemaVersion,
		OperationKey: view.ID + "." + route.OperationID,
		Pack:         view.ID, PackName: view.Name, Operation: route.OperationID, OperationID: route.OperationID,
		OperationIDRole: "semantic-selector", OperationIDIsURL: false,
		Capabilities: capabilities, Aliases: aliases, Summary: route.Summary,
		Ready: ready, Status: status, Reason: reason, NextAction: nextAction,
		Methods: append([]string(nil), route.Methods...), Path: route.Path, CallURL: callURL,
		PathParameters: parameters, QueryParameters: append([]string(nil), route.QueryParameters...), Streaming: route.Streaming, Transport: transport,
		SideEffecting: routeSideEffecting(route), RequestSchema: requestSchema, SchemaSource: schemaSource,
		RequestExample: requestExample, RequestExampleRole: published.RequestExampleRole, DynamicInputs: published.DynamicInputs,
		ResponseSchema: responseSchema, Retry: retry, Verification: route.Availability,
		Pricing: pricing, Guides: guides, Workflows: workflows, PoolID: view.ID, Pool: view.Pool, Call: call,
	}
}

func (registry *protocolRegistry) resolveCandidates(tokenID int, query string) []agentResolveCandidate {
	normalizedQuery := normalizeAgentSearch(query)
	queryTokens := strings.Fields(normalizedQuery)
	result := make([]agentResolveCandidate, 0)
	hasExactMatch := false
	for _, view := range registry.views() {
		for _, route := range view.Routes {
			if allowed, _, _ := registry.policies.preview(tokenID, "p", view.ID, route.OperationID, ""); !allowed {
				continue
			}
			descriptor := registry.describeOperationFromView(view, route)
			score, matchKind := agentOperationScore(normalizedQuery, queryTokens, descriptor)
			if score == 0 {
				continue
			}
			if score >= 1000 {
				hasExactMatch = true
			}
			guideRank := 0
			for _, guide := range descriptor.Guides {
				if rank := agentGuideRank(guide.Status); rank > guideRank {
					guideRank = rank
				}
			}
			result = append(result, agentResolveCandidate{Descriptor: descriptor, Score: score, MatchKind: matchKind, GuideRank: guideRank})
		}
	}
	if hasExactMatch {
		exact := result[:0]
		for _, candidate := range result {
			if candidate.Score >= 1000 {
				exact = append(exact, candidate)
			}
		}
		result = exact
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Descriptor.Ready != result[j].Descriptor.Ready {
			return result[i].Descriptor.Ready
		}
		if result[i].Score != result[j].Score {
			return result[i].Score > result[j].Score
		}
		if result[i].GuideRank != result[j].GuideRank {
			return result[i].GuideRank > result[j].GuideRank
		}
		left := result[i].Descriptor.Pack + "." + result[i].Descriptor.Operation
		right := result[j].Descriptor.Pack + "." + result[j].Descriptor.Operation
		return left < right
	})
	return result
}

func agentOperationScore(query string, tokens []string, descriptor agentOperationDescriptor) (int, string) {
	if query == normalizeAgentSearch(descriptor.OperationKey) {
		return 1400, "operation-key"
	}
	for _, capability := range descriptor.Capabilities {
		if query == normalizeAgentSearch(capability) {
			return 1300, "capability"
		}
	}
	for _, alias := range descriptor.Aliases {
		if query == normalizeAgentSearch(alias) {
			return 1200, "alias"
		}
	}
	if query == normalizeAgentSearch(descriptor.Operation) {
		return 1100, "operation-id"
	}
	searchValues := append([]string{descriptor.Pack, descriptor.PackName, descriptor.Operation, descriptor.Summary, descriptor.CallURL}, descriptor.Capabilities...)
	searchValues = append(searchValues, descriptor.Aliases...)
	searchable := normalizeAgentSearch(strings.Join(searchValues, " "))
	score := 0
	if strings.Contains(searchable, query) {
		score += 400
	}
	for _, token := range tokens {
		if len(token) > 1 && strings.Contains(searchable, token) {
			score += 50
		}
	}
	return score, "text"
}

func normalizeAgentSearch(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer(".", " ", "_", " ", "-", " ", "/", " ", ":", " ")
	return strings.Join(strings.Fields(replacer.Replace(value)), " ")
}

func agentInputModel(input any) string {
	mapped, ok := input.(map[string]any)
	if !ok {
		return ""
	}
	for _, field := range []string{"model", "model_cls"} {
		model, _ := mapped[field].(string)
		if model = strings.TrimSpace(model); model != "" {
			return model
		}
	}
	return ""
}

func operationAliases(pricing []protocolPricingEntry, capabilities []string) []string {
	values := make([]string, 0)
	for _, price := range pricing {
		if protocolOperationIDPattern.MatchString(strings.ToLower(price.Label)) {
			values = append(values, strings.ToLower(price.Label))
		}
	}
	seen := make(map[string]bool)
	for _, capability := range capabilities {
		seen[capability] = true
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func operationPricing(pricing *protocolPricing, operationID string) []protocolPricingEntry {
	result := make([]protocolPricingEntry, 0)
	if pricing == nil {
		return result
	}
	for _, entry := range pricing.Entries {
		if entry.Scope == "operation" && entry.ID == operationID {
			result = append(result, entry)
		}
	}
	return result
}

func operationWorkflows(workflows []protocolWorkflow, operationID string) []string {
	result := make([]string, 0)
	for _, workflow := range workflows {
		used := workflow.CreateOperation == operationID || workflow.PollOperation == operationID || workflow.CancelOperation == operationID
		for _, step := range workflow.Steps {
			used = used || step.Operation == operationID
		}
		if used {
			result = append(result, workflow.ID)
		}
	}
	sort.Strings(result)
	return result
}

func routeSideEffecting(route protocolRoute) bool {
	for _, method := range route.Methods {
		if method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
			return true
		}
	}
	return false
}

func agentGuideRank(status string) int {
	switch status {
	case "verified":
		return 4
	case "observed":
		return 3
	case "draft":
		return 2
	case "deprecated":
		return 1
	default:
		return 0
	}
}

func agentReadinessAdvice(view protocolView, status string) (reason, nextAction string, retryable bool, retryAfter any, owner string) {
	owner = "localrouter"
	if view.Pool.Source == "external-readonly" {
		owner = "external-maintainer"
	}
	switch status {
	case "ready", "verified", "observed", "draft":
		return "operation is ready", "call the operation", false, nil, owner
	case "disabled":
		return "Pack is disabled by the operator", "choose an alternative or ask the operator to enable the Pack", false, nil, owner
	case "secret-not-ready":
		return "the protected credential is missing or unavailable", "choose an alternative or ask the operator to repair the protected credential", false, nil, owner
	case "pool-not-ready", "probe-credential-unavailable":
		if view.Pool.Total > 0 {
			return "no eligible credential; accounts may be disabled, expired, cooling down, busy, below quota, or unroutable", "choose a ready alternative or ask the operator to inspect the Pack pool", false, nil, owner
		}
		return "the credential source is unavailable or contains no eligible account", "choose a ready alternative or ask the operator to repair the Pack pool source", false, nil, owner
	case "verification-required", "verification-stale":
		return "operation verification is missing or stale", "choose a verified alternative or ask the operator to refresh readiness", false, nil, owner
	case "blocked", "deprecated":
		return "operation availability is " + status, "choose a supported alternative", false, nil, owner
	default:
		if strings.HasPrefix(status, "probe-") {
			return "the configured readiness probe did not pass", "retry later or choose a ready alternative", true, nil, owner
		}
		return "operation is unavailable: " + status, "inspect discovery and choose a ready alternative", false, nil, owner
	}
}

func (registry *protocolRegistry) alternativeOperationRefs(tokenID int, packID, operationID string) []agentOperationRef {
	descriptor, ok := registry.describeOperation(packID, operationID)
	if !ok {
		return []agentOperationRef{}
	}
	queries := descriptor.Capabilities
	if len(queries) == 0 {
		queries = []string{operationID}
	}
	seen := make(map[string]bool)
	result := make([]agentOperationRef, 0, 3)
	for _, query := range queries {
		for _, candidate := range registry.resolveCandidates(tokenID, query) {
			item := candidate.Descriptor
			key := item.Pack + "." + item.Operation
			if key == packID+"."+operationID || seen[key] || !item.Ready {
				continue
			}
			seen[key] = true
			result = append(result, agentOperationRef{
				OperationKey: item.OperationKey, Pack: item.Pack, Operation: item.Operation, OperationID: item.OperationID,
				Capabilities: item.Capabilities, Summary: item.Summary,
				Ready: item.Ready, Status: item.Status, CallURL: item.CallURL, Methods: item.Methods,
			})
			if len(result) == 3 {
				return result
			}
		}
	}
	return result
}

func (registry *protocolRegistry) readyOperationRefs(tokenID int, query string, limit int) []agentOperationRef {
	if registry == nil || strings.TrimSpace(query) == "" || limit < 1 {
		return []agentOperationRef{}
	}
	seen := make(map[string]bool)
	result := make([]agentOperationRef, 0, limit)
	for _, candidate := range registry.resolveCandidates(tokenID, query) {
		item := candidate.Descriptor
		if !item.Ready || seen[item.OperationKey] {
			continue
		}
		seen[item.OperationKey] = true
		result = append(result, agentOperationRef{
			OperationKey: item.OperationKey, Pack: item.Pack, Operation: item.Operation, OperationID: item.OperationID,
			Capabilities: item.Capabilities, Summary: item.Summary, Ready: item.Ready, Status: item.Status,
			CallURL: item.CallURL, Methods: item.Methods,
		})
		if len(result) == limit {
			break
		}
	}
	return result
}

func (registry *protocolRegistry) writeReadinessError(c *gin.Context, view protocolView, operationID, status string) {
	reason, nextAction, retryable, retryAfter, owner := agentReadinessAdvice(view, status)
	code := "operation_not_available"
	message := "protocol operation is not ready"
	switch status {
	case "pool-not-ready", "probe-credential-unavailable":
		code = "pool_not_ready"
	case "secret-not-ready":
		code = "secret_not_ready"
	case "disabled":
		code = "pack_disabled"
	default:
		if strings.HasPrefix(status, "probe-") {
			code = "readiness_probe_failed"
		}
	}
	writeAgentError(c, http.StatusServiceUnavailable, code, message, reason, retryable, owner, nextAction, retryAfter, registry.alternativeOperationRefs(c.GetInt(tokenPolicyContextID), view.ID, operationID), gin.H{
		"status": status, "pack": view.ID, "operation": operationID,
	})
}

func writeAgentError(c *gin.Context, status int, code, message, reason string, retryable bool, owner, nextAction string, retryAfter any, alternatives []agentOperationRef, extra gin.H) {
	if alternatives == nil {
		alternatives = []agentOperationRef{}
	}
	payload := gin.H{
		"success": false, "code": code, "message": message, "reason": reason,
		"retryable": retryable, "owner": owner, "retry_after": retryAfter,
		"next_action": nextAction, "alternatives": alternatives,
	}
	for key, value := range extra {
		payload[key] = value
	}
	c.JSON(status, payload)
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func plainAgentJSONValue(value any) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return map[string]any{}
	}
	var plain any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&plain); err != nil {
		return map[string]any{}
	}
	return plain
}

func validateAgentSchemaValue(schema any, value any, path string) []string {
	object, ok := schema.(map[string]any)
	if !ok || len(object) == 0 {
		return nil
	}
	violations := make([]string, 0)
	if enum, ok := object["enum"].([]any); ok {
		matched := false
		for _, candidate := range enum {
			if fmt.Sprint(candidate) == fmt.Sprint(value) {
				matched = true
				break
			}
		}
		if !matched {
			violations = append(violations, path+" is not one of the allowed values")
		}
	}
	if typeName, ok := object["type"].(string); ok && !agentJSONTypeMatches(typeName, value) {
		return append(violations, path+" must be "+typeName)
	}
	properties, hasProperties := object["properties"].(map[string]any)
	if hasProperties {
		mapped, mappedOK := value.(map[string]any)
		if !mappedOK {
			return append(violations, path+" must be object")
		}
		if required, ok := object["required"].([]any); ok {
			for _, raw := range required {
				name, _ := raw.(string)
				if _, exists := mapped[name]; name != "" && !exists {
					violations = append(violations, path+"."+name+" is required")
				}
			}
		}
		for name, childSchema := range properties {
			if child, exists := mapped[name]; exists {
				violations = append(violations, validateAgentSchemaValue(childSchema, child, path+"."+name)...)
			}
		}
		if additional, ok := object["additionalProperties"].(bool); ok && !additional {
			for name := range mapped {
				if _, exists := properties[name]; !exists {
					violations = append(violations, path+"."+name+" is not allowed")
				}
			}
		}
	}
	if items, ok := object["items"]; ok {
		if values, ok := value.([]any); ok {
			for index, child := range values {
				violations = append(violations, validateAgentSchemaValue(items, child, path+"["+strconv.Itoa(index)+"]")...)
			}
		}
	}
	return violations
}

func agentJSONTypeMatches(typeName string, value any) bool {
	switch typeName {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		switch value.(type) {
		case float64, json.Number:
			return true
		}
	case "integer":
		switch typed := value.(type) {
		case json.Number:
			_, err := typed.Int64()
			return err == nil
		case float64:
			return typed == float64(int64(typed))
		}
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	}
	return false
}
