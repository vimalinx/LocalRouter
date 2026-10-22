package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const setupIdentityAction = "For a new unpublished service, run lr identity prepare <agent-code>; the setup-only identity is issued at once. Existing independent identities can prepare directly. Never borrow unrelated service scope."

type unpublishedIdentityScope struct{ Pack, Operation string }

func (e *unpublishedIdentityScope) Error() string { return "select a published exact operation_key" }

func identityValidationError(c *gin.Context, err error) {
	code, path, reason := "identity_request_invalid", "policy", err.Error()
	next := "Read lr identity schema and correct the indicated field. For a retained rejected enrollment, use lr identity cancel before submitting the corrected request."
	var scope *unpublishedIdentityScope
	var field *json.UnmarshalTypeError
	switch {
	case errors.As(err, &scope):
		code, path, next = "identity_scope_unpublished", "policy.operations", setupIdentityAction
		reason += "; for a new service, use lr identity cancel (if needed), then lr identity prepare <agent-code> (setup-only, no call rights)"
	case errors.As(err, &field):
		path, reason = field.Field, "expected "+field.Type.String()+"; received "+field.Value
	case strings.HasPrefix(reason, "json: unknown field"):
		path = strings.Trim(strings.TrimPrefix(reason, "json: unknown field "), "\"")
	case strings.Contains(reason, "setup_only"):
		next = setupIdentityAction
	}
	writeAgentError(c, http.StatusBadRequest, code, reason, reason, false, "agent", next, nil, nil,
		gin.H{"issues": []gin.H{{"path": path, "reason": reason, "suggested_action": next}}, "schema_url": "/docs/identity-request.schema.json"})
}

func identityFailure(c *gin.Context, status int, message string) {
	next := "Inspect lr init and the existing request. Resume lr identity claim after human approval; use lr identity cancel to withdraw a pending or rejected enrollment. Do not create another identity for an unknown outcome."
	writeAgentError(c, status, "identity_request_failed", message, message, false, "localrouter", next, nil, nil, nil)
}

func identityRequestSchema() gin.H {
	policy := gin.H{
		"setup_only": gin.H{"type": "boolean", "description": "Preparation identity; no Pack, operation, model or surface selectors and no service calls"},
		"packs":      maintenanceStringArray("Published Pack IDs; omit for setup_only"),
		"operations": maintenanceStringArray("Published exact Pack-qualified operation keys; omit for setup_only"),
		"models":     maintenanceStringArray("Optional exact model restrictions"),
		"surfaces":   maintenanceStringArray("Optional service surfaces"),
	}
	for _, key := range []string{"requests_per_minute", "daily_request_limit", "max_in_flight", "expires_at", "daily_budget_usd_micros", "monthly_budget_usd_micros", "request_reserve_usd_micros"} {
		policy[key] = gin.H{"type": "integer", "minimum": 0}
	}
	policySchema := maintenanceObjectSchema(policy)
	policySchema["oneOf"] = []gin.H{
		{"required": []string{"setup_only"}, "properties": gin.H{"setup_only": gin.H{"const": true}}, "not": gin.H{"anyOf": []gin.H{{"required": []string{"packs"}}, {"required": []string{"operations"}}, {"required": []string{"models"}}, {"required": []string{"surfaces"}}}}},
		{"required": []string{"packs", "operations"}, "properties": gin.H{"setup_only": gin.H{"const": false}, "packs": gin.H{"minItems": 1}, "operations": gin.H{"minItems": 1}}},
	}
	return maintenanceObjectSchema(gin.H{
		"agent_code": maintenanceString("Stable Agent code"), "agent_name": maintenanceString("Display name"),
		"workspace": maintenanceString("Absolute workspace path"), "runtime": maintenanceString("Agent runtime"), "policy": policySchema,
	}, "agent_code", "agent_name", "workspace", "policy")
}

func protocolAuthoringSchema() gin.H {
	data, err := webFiles.ReadFile("protocols/schema/protocol-pack-v3.schema.json")
	if err != nil {
		panic(err)
	} // Required embedded build input.
	var schema gin.H
	if err := json.Unmarshal(data, &schema); err != nil {
		panic(err)
	}
	return schema
}

func registerAuthoringSchemaRoutes(engine *gin.Engine) {
	for path, schema := range map[string]func() gin.H{
		"/docs/protocol-pack-v3.schema.json": protocolAuthoringSchema,
		"/docs/setup-proposal.schema.json":   serviceProposalSchema,
		"/docs/identity-request.schema.json": identityRequestSchema,
	} {
		engine.GET(path, func(c *gin.Context) { c.Header("Cache-Control", "no-store"); c.JSON(http.StatusOK, schema()) })
	}
}

func setupDefinitionSchema() gin.H {
	schema := protocolAuthoringSchema()
	delete(schema, "$id")
	delete(schema, "$schema")
	delete(schema, "$defs")
	props := schema["properties"].(map[string]any)
	props["enabled"] = gin.H{"const": true, "description": "The connection must be explicitly enabled; preparation itself grants no call rights"}
	props["auth"] = maintenanceObjectSchema(gin.H{"type": maintenanceEnum("Credential comes from the top-level credential field (lr setup prepare --credential-file) or from the human at approval", "none", "bearer", "header"), "header": maintenanceString("Required for header authentication"), "value_template": maintenanceString("Optional header value template")}, "type")
	return schema
}
