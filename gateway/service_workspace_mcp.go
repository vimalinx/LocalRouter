package main

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
)

func serviceProposalSchema() gin.H {
	return maintenanceObjectSchema(gin.H{
		"id":     maintenanceString("Optional unique id for this proposal; inspect an existing id instead of replaying preparation"),
		"kind":   maintenanceEnum("Preparation kind", "connection", "bundle", "template"),
		"reason": maintenanceString("What the Agent needs and why this authority is appropriate"),
		"connection": maintenanceObjectSchema(gin.H{
			"template_id": maintenanceString("Exact template id"), "template_version": maintenanceString("Exact template version"),
			"definition": gin.H{"type": "object", "description": "Complete Protocol Pack v3 adapted from the template and observed provider documentation; no credential values or locators", "additionalProperties": true},
			"guide":      maintenanceString("Observed provider usage and maintenance instructions"), "source_url": maintenanceString("Documentation source, without credentials or query"),
		}, "template_id", "template_version", "definition"),
		"bundle": maintenanceObjectSchema(gin.H{
			"id": maintenanceString("Stable bundle id"), "name": maintenanceString("Human-readable purpose"), "description": maintenanceString("Capability scope"), "guide": maintenanceString("How to combine these operations"),
			"includes": maintenanceStringArray("Exact immutable bundle revision digests to compose"),
			"members":  gin.H{"type": "array", "items": maintenanceObjectSchema(gin.H{"pack": maintenanceString("Installed Pack id"), "operations": maintenanceStringArray("Exact operation ids; * expands to today's explicit list"), "workflows": maintenanceStringArray("Workflow ids; all constituent operations are disclosed")}, "pack", "operations")},
		}, "id", "name", "members"),
		"template":            gin.H{"type": "object", "description": "Shareable template with id/version/name/summary/maintenance_guide/checks/example; example base URL must be https://service.example.invalid and auth none", "additionalProperties": true},
		"grant_token_id":      gin.H{"type": "integer", "minimum": 1, "description": "Service identity to assign the bundle; defaults to the requesting service Token"},
		"grant_expires_at":    gin.H{"type": "integer", "minimum": 0, "description": "Optional Unix expiry; zero means no expiry"},
		"maintainer_token_id": gin.H{"type": "integer", "minimum": 1, "description": "Optional separate maintenance-only Token to receive a compatible repair delegation"},
		"maintenance_mode":    maintenanceEnum("Requested maintenance authority, requires human approval", "compatible"),
	}, "kind", "reason")
}

func serviceSetupTools(maintenance bool) []maintenanceToolDefinition {
	tools := []maintenanceToolDefinition{
		{Name: "localrouter_setup_templates", Description: "Discover versioned service recipes and maintenance guides. Examples are shape-only; no upstream call.", InputSchema: maintenanceObjectSchema(gin.H{"id": maintenanceString("Optional exact template id; requires version"), "version": maintenanceString("Exact template version; requires id"), "full": gin.H{"type": "boolean", "description": "Return all full recipes instead of compact metadata"}}), ReadOnly: true},
		{Name: "localrouter_setup_prepare", Description: "Prepare an owned service connection, capability bundle or template proposal. Returns exact impact and approval digest; never grants authority or calls a provider.", InputSchema: serviceProposalSchema()},
		{Name: "localrouter_setup_list", Description: "List this Agent's durable onboarding proposals and their progress.", InputSchema: maintenanceObjectSchema(gin.H{"page": gin.H{"type": "integer", "minimum": 1}, "page_size": gin.H{"type": "integer", "minimum": 1, "maximum": 100}}), ReadOnly: true},
		{Name: "localrouter_setup_get", Description: "Inspect one owned proposal and its next action.", InputSchema: maintenanceObjectSchema(gin.H{"id": maintenanceString("Proposal id")}, "id"), ReadOnly: true},
		{Name: "localrouter_setup_withdraw", Description: "Withdraw an owned proposal before approval without modifying installed services.", InputSchema: maintenanceObjectSchema(gin.H{"id": maintenanceString("Proposal id"), "digest": maintenanceString("Exact proposal digest")}, "id", "digest")},
		{Name: "localrouter_setup_verify", Description: "Inspect installation and recorded real-response evidence. Does not call the provider or claim business completion from HTTP status.", InputSchema: maintenanceObjectSchema(gin.H{"id": maintenanceString("Proposal id")}, "id"), ReadOnly: true},
		{Name: "localrouter_setup_reconcile", Description: "Reconcile an interrupted apply with the installed exact digest, without replaying the release or upstream requests.", InputSchema: maintenanceObjectSchema(gin.H{"id": maintenanceString("Proposal id")}, "id")},
		{Name: "localrouter_setup_bundles", Description: "List available capability bundle revisions and this identity's effective assignments.", InputSchema: maintenanceObjectSchema(gin.H{}), ReadOnly: true},
		{Name: "localrouter_setup_traces", Description: "Inspect this Agent's service calls, attempts, resource units and cost provenance. Correlation never grants access to another Agent's records.", InputSchema: maintenanceObjectSchema(gin.H{"trace_id": maintenanceString("Optional trace id"), "task_id": maintenanceString("Optional Agent task correlation label"), "page": gin.H{"type": "integer", "minimum": 1}, "page_size": gin.H{"type": "integer", "minimum": 1, "maximum": 200}}), ReadOnly: true},
	}
	if maintenance {
		tools = append(tools, maintenanceToolDefinition{Name: "localrouter_setup_apply", Description: "Apply a prepared compatible repair within this maintenance Token's human-approved Pack delegation. Out-of-scope changes return needs_approval.", InputSchema: maintenanceObjectSchema(gin.H{"id": maintenanceString("Owned proposal id"), "digest": maintenanceString("Exact reviewed proposal digest")}, "id", "digest")})
	}
	return tools
}

func serviceMCPToolViews(maintenance bool) []gin.H {
	views := []gin.H{}
	for _, tool := range serviceSetupTools(maintenance) {
		views = append(views, gin.H{"name": tool.Name, "description": tool.Description, "inputSchema": tool.InputSchema, "annotations": gin.H{"readOnlyHint": tool.ReadOnly, "destructiveHint": false}})
	}
	return views
}

func (registry *protocolRegistry) handleServiceSetupTool(c *gin.Context, runtime localRuntime, request mcpRequest, name string, raw json.RawMessage, maintenance bool) bool {
	if registry.workspace == nil || !strings.HasPrefix(name, "localrouter_setup_") {
		return false
	}
	known := false
	for _, tool := range serviceSetupTools(maintenance) {
		if tool.Name == name {
			known = true
		}
	}
	if !known {
		mcpError(c, request.ID, -32602, "unknown setup tool for this authentication lane")
		return true
	}
	if trace := serviceTraceContext(c); trace != nil {
		trace.Kind, trace.Operation = "management", name
		if err := runtime.store.saveServiceTrace(*trace); err != nil {
			mcpError(c, request.ID, -32603, "cannot record setup operation")
			return true
		}
	}
	owner, err := serviceOwner(c, runtime)
	var result any
	if err == nil {
		result, err = registry.workspace.callSetupTool(owner, name, raw, maintenance)
	}
	if err != nil {
		code := "service_proposal_invalid"
		if strings.Contains(err.Error(), "needs_approval") {
			code = "needs_approval"
		}
		payload := gin.H{"success": false, "code": code, "message": err.Error(), "retryable": false, "next_action": "Inspect the owned proposal. Human approval is required for new authority; do not replay an uncertain apply."}
		text, _ := json.Marshal(payload)
		mcpResult(c, request.ID, gin.H{"isError": true, "structuredContent": payload, "content": []gin.H{{"type": "text", "text": string(text)}}})
		return true
	}
	encoded, _ := json.Marshal(result)
	mcpResult(c, request.ID, gin.H{"isError": false, "structuredContent": result, "content": []gin.H{{"type": "text", "text": string(encoded)}}})
	return true
}

func (hub *serviceWorkspace) callSetupTool(owner localToken, name string, raw json.RawMessage, maintenance bool) (any, error) {
	if name == "localrouter_setup_prepare" {
		var input serviceProposalInput
		if err := decodeMaintenanceArguments(raw, &input); err != nil {
			return nil, err
		}
		proposal, err := hub.prepare(owner, input)
		if err != nil {
			return nil, err
		}
		return serviceProposalView(proposal), nil
	}
	if name == "localrouter_setup_templates" {
		var input struct {
			ID      string `json:"id"`
			Version string `json:"version"`
			Full    bool   `json:"full"`
		}
		if err := decodeMaintenanceArguments(raw, &input); err != nil {
			return nil, err
		}
		if input.ID != "" || input.Version != "" {
			if input.ID == "" || input.Version == "" || input.Full {
				return nil, errors.New("select one exact template id and version")
			}
			item, ok := hub.template(input.ID, input.Version)
			if !ok {
				return nil, errors.New("template id/version not found")
			}
			return gin.H{"template": item, "example_role": "shape-only"}, nil
		}
		if input.Full {
			return gin.H{"templates": hub.templates(), "example_role": "shape-only"}, nil
		}
		items := []gin.H{}
		for _, item := range hub.templates() {
			items = append(items, gin.H{"id": item.ID, "version": item.Version, "name": item.Name, "summary": item.Summary, "digest": item.Digest})
		}
		return gin.H{"templates": items, "next_action": "Select one recipe with lr setup template <id> <version>, then lr setup schema; examples are shape-only."}, nil
	}
	if name == "localrouter_setup_bundles" {
		var input struct{}
		if err := decodeMaintenanceArguments(raw, &input); err != nil {
			return nil, err
		}
		return hub.bundleView(owner.ID), nil
	}
	if name == "localrouter_setup_list" {
		var input struct {
			Page     int `json:"page"`
			PageSize int `json:"page_size"`
		}
		if err := decodeMaintenanceArguments(raw, &input); err != nil {
			return nil, err
		}
		if input.Page < 1 {
			input.Page = 1
		}
		if input.PageSize < 1 || input.PageSize > 100 {
			input.PageSize = 20
		}
		return serviceProposalPage(hub.proposals(owner.ID), input.Page, input.PageSize), nil
	}
	if name == "localrouter_setup_traces" {
		var input struct {
			TraceID  string `json:"trace_id"`
			TaskID   string `json:"task_id"`
			Page     int    `json:"page"`
			PageSize int    `json:"page_size"`
		}
		if err := decodeMaintenanceArguments(raw, &input); err != nil {
			return nil, err
		}
		items, total, err := hub.store.serviceTraces(owner.ID, input.TraceID, input.TaskID, input.Page, input.PageSize)
		if err != nil {
			return nil, err
		}
		summary, err := hub.store.serviceTraceSummary(owner.ID, input.TraceID, input.TaskID)
		return gin.H{"items": items, "total": total, "summary": summary}, err
	}
	var input struct {
		ID     string `json:"id"`
		Digest string `json:"digest,omitempty"`
	}
	if err := decodeMaintenanceArguments(raw, &input); err != nil {
		return nil, err
	}
	proposal, exists := hub.proposal(input.ID, owner.ID)
	if !exists {
		return nil, errors.New("unknown proposal")
	}
	switch name {
	case "localrouter_setup_get":
		return serviceProposalView(proposal), nil
	case "localrouter_setup_verify":
		return hub.verifyProposal(input.ID, owner.ID)
	case "localrouter_setup_reconcile":
		proposal, err := hub.reconcile(input.ID, owner.ID)
		return serviceProposalView(proposal), err
	case "localrouter_setup_withdraw":
		proposal, err := hub.decide(input.ID, input.Digest, "withdraw", "", owner.ID)
		return serviceProposalView(proposal), err
	case "localrouter_setup_apply":
		if !maintenance {
			return nil, errors.New("needs_approval: apply requires the maintenance-only lane")
		}
		proposal, err := hub.decide(input.ID, input.Digest, "approve", "", owner.ID)
		return serviceProposalView(proposal), err
	}
	return nil, errors.New("unknown setup operation")
}
