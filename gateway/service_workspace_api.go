package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func serviceWorkspaceError(c *gin.Context, err error) {
	code, status := "service_proposal_invalid", http.StatusBadRequest
	message := err.Error()
	if strings.Contains(message, "needs_approval") {
		code, status = "needs_approval", http.StatusForbidden
	}
	if strings.Contains(message, "needs_credential") {
		code, status = "needs_credential", http.StatusConflict
	}
	if strings.Contains(message, "stale") || strings.Contains(message, "changed") {
		code, status = "proposal_changed", http.StatusConflict
	}
	writeAgentError(c, status, code, message, message, false, "localrouter", "inspect the proposal and its next_action; preparation never authorizes a provider call", nil, nil, nil)
}

func serviceOwner(c *gin.Context, runtime localRuntime) (localToken, error) {
	id := c.GetInt(tokenPolicyContextID)
	if id <= 0 || runtime.store == nil {
		return localToken{}, errors.New("a registered Token identity is required")
	}
	token, err := runtime.store.tokenByID(runtime.rootUser.ID, id, false)
	if err != nil {
		return token, errors.New("unknown Agent identity")
	}
	if token.Status != localStatusEnabled || (token.ExpiredTime >= 0 && token.ExpiredTime <= time.Now().Unix()) {
		return token, errors.New("Agent Token is disabled or expired")
	}
	if policy, exists := runtime.policies.policyFor(id); exists && policy.ExpiresAt > 0 && policy.ExpiresAt <= time.Now().Unix() {
		return token, errors.New("Agent policy has expired")
	}
	return token, nil
}

func proposalNextAction(proposal serviceProposal) string {
	switch proposal.State {
	case "awaiting_approval":
		return "The exact proposal is ready for the human console. A delegated maintainer may apply a compatible repair through /manage/mcp; otherwise wait for approval."
	case "applied":
		if proposal.Kind == "connection" {
			return "Inspect readiness; request a capability bundle if needed, then preflight and execute one authorized call. Verify from its recorded trace."
		}
		return "Refresh effective permissions or the template catalogue. Existing bundle grants remain pinned to their approved revisions."
	case "applying":
		return "Reconcile the installed digest using setup reconcile; do not repeat apply."
	case "preparation_failed", "apply_failed":
		return "Inspect the retained error and draft; prepare a fresh proposal after reconciling live state."
	default:
		return "Inspect this proposal; no automatic provider call will be made."
	}
}

func serviceProposalView(proposal serviceProposal) gin.H {
	return gin.H{"proposal": proposal, "next_action": proposalNextAction(proposal), "upstream_called_by_setup": false}
}

func registerServiceAgentRoutes(agent *gin.RouterGroup, runtime localRuntime) {
	if runtime.workspace == nil {
		return
	}
	hub := runtime.workspace
	agent.GET("/service-templates", func(c *gin.Context) {
		if _, err := serviceOwner(c, runtime); err != nil {
			serviceWorkspaceError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": hub.templates(), "example_role": "shape-only; adapt to observed provider documentation"})
	})
	agent.GET("/bundles", func(c *gin.Context) {
		owner, err := serviceOwner(c, runtime)
		if err != nil {
			serviceWorkspaceError(c, err)
			return
		}
		c.JSON(http.StatusOK, hub.bundleView(owner.ID))
	})
	agent.GET("/onboarding", func(c *gin.Context) {
		owner, err := serviceOwner(c, runtime)
		if err != nil {
			serviceWorkspaceError(c, err)
			return
		}
		items := hub.proposals(owner.ID)
		page, size := parsePage(c.Request.URL.Query(), 20)
		c.JSON(http.StatusOK, serviceProposalPage(items, page, size))
	})
	agent.POST("/onboarding", func(c *gin.Context) {
		owner, err := serviceOwner(c, runtime)
		if err != nil {
			serviceWorkspaceError(c, err)
			return
		}
		var input serviceProposalInput
		if err := decodeAgentRequest(c, &input); err != nil {
			serviceWorkspaceError(c, err)
			return
		}
		proposal, err := hub.prepare(owner, input)
		if err != nil {
			serviceWorkspaceError(c, err)
			return
		}
		c.JSON(http.StatusCreated, serviceProposalView(proposal))
	})
	agent.GET("/onboarding/:id", func(c *gin.Context) {
		owner, err := serviceOwner(c, runtime)
		if err != nil {
			serviceWorkspaceError(c, err)
			return
		}
		proposal, exists := hub.proposal(c.Param("id"), owner.ID)
		if !exists {
			c.Status(http.StatusNotFound)
			return
		}
		c.JSON(http.StatusOK, serviceProposalView(proposal))
	})
	agent.DELETE("/onboarding/:id", func(c *gin.Context) {
		owner, err := serviceOwner(c, runtime)
		if err != nil {
			serviceWorkspaceError(c, err)
			return
		}
		var input struct {
			Digest string `json:"digest"`
		}
		if err := decodeAgentRequest(c, &input); err != nil {
			serviceWorkspaceError(c, err)
			return
		}
		proposal, err := hub.decide(c.Param("id"), input.Digest, "withdraw", "", owner.ID)
		if err != nil {
			serviceWorkspaceError(c, err)
			return
		}
		c.JSON(http.StatusOK, serviceProposalView(proposal))
	})
	agent.GET("/onboarding/:id/verification", func(c *gin.Context) {
		owner, err := serviceOwner(c, runtime)
		if err != nil {
			serviceWorkspaceError(c, err)
			return
		}
		result, err := hub.verifyProposal(c.Param("id"), owner.ID)
		if err != nil {
			serviceWorkspaceError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	})
	agent.POST("/onboarding/:id/reconcile", func(c *gin.Context) {
		owner, err := serviceOwner(c, runtime)
		if err != nil {
			serviceWorkspaceError(c, err)
			return
		}
		proposal, err := hub.reconcile(c.Param("id"), owner.ID)
		if err != nil {
			serviceWorkspaceError(c, err)
			return
		}
		c.JSON(http.StatusOK, serviceProposalView(proposal))
	})
	agent.GET("/traces", func(c *gin.Context) {
		owner, err := serviceOwner(c, runtime)
		if err != nil {
			serviceWorkspaceError(c, err)
			return
		}
		handleServiceTraceList(c, runtime, owner.ID)
	})
}

func serviceProposalPage(items []serviceProposal, page, size int) gin.H {
	start := (page - 1) * size
	if start > len(items) {
		start = len(items)
	}
	end := start + size
	if end > len(items) {
		end = len(items)
	}
	return gin.H{"items": items[start:end], "total": len(items), "page": page, "page_size": size}
}

func handleServiceTraceList(c *gin.Context, runtime localRuntime, owner int) {
	page, size := parsePage(c.Request.URL.Query(), 50)
	items, total, err := runtime.store.serviceTraces(owner, c.Query("trace_id"), c.Query("task_id"), page, size)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot read service traces"})
		return
	}
	summary, err := runtime.store.serviceTraceSummary(owner, c.Query("trace_id"), c.Query("task_id"))
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"items": items, "total": total, "page": page, "page_size": size, "summary": summary, "accounting_rule": "projection and attempt spans are not additional business tasks; deduplicate snapshot units by owner, Pack, resource reference and unit"}})
}

func registerServiceAdminRoutes(admin *gin.RouterGroup, runtime localRuntime) {
	if runtime.workspace == nil {
		return
	}
	hub := runtime.workspace
	admin.GET("/service-workspace", func(c *gin.Context) {
		view := hub.bundleView(0)
		view["templates"] = hub.templates()
		page, size := parsePage(c.Request.URL.Query(), 50)
		view["proposals"] = serviceProposalPage(hub.proposals(0), page, size)
		hub.mu.RLock()
		view["delegations"] = hub.doc.Delegations
		hub.mu.RUnlock()
		c.JSON(http.StatusOK, gin.H{"success": true, "data": view})
	})
	admin.GET("/service-proposals/:id", func(c *gin.Context) {
		proposal, ok := hub.proposal(c.Param("id"), 0)
		if !ok {
			c.Status(http.StatusNotFound)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": serviceProposalView(proposal)})
	})
	admin.POST("/service-proposals/:id/decision", func(c *gin.Context) {
		var input struct {
			Digest     string `json:"digest"`
			Decision   string `json:"decision"`
			Credential string `json:"credential,omitempty"`
		}
		if err := decodeAgentRequest(c, &input); err != nil {
			serviceWorkspaceError(c, err)
			return
		}
		proposal, err := hub.decide(c.Param("id"), input.Digest, input.Decision, input.Credential, 0)
		if err != nil {
			serviceWorkspaceError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": serviceProposalView(proposal)})
	})
	admin.GET("/service-proposals/:id/verification", func(c *gin.Context) {
		result, err := hub.verifyProposal(c.Param("id"), 0)
		if err != nil {
			serviceWorkspaceError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
	})
	admin.POST("/service-proposals/:id/reconcile", func(c *gin.Context) {
		proposal, err := hub.reconcile(c.Param("id"), 0)
		if err != nil {
			serviceWorkspaceError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": serviceProposalView(proposal)})
	})
	admin.DELETE("/service-grants/:id", func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil || id <= 0 {
			c.Status(http.StatusBadRequest)
			return
		}
		if err := hub.update(func(doc *serviceWorkspaceDocument) error { doc.Grants[id] = []serviceBundleGrant{}; return nil }); err != nil {
			serviceWorkspaceError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"token_id": id, "service_access": "denied", "model_relay": "existing policy retained"}})
	})
	admin.DELETE("/service-delegations/:id", func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil || id <= 0 {
			c.Status(http.StatusBadRequest)
			return
		}
		if err := hub.update(func(doc *serviceWorkspaceDocument) error { doc.Delegations[id] = []serviceDelegation{}; return nil }); err != nil {
			serviceWorkspaceError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"token_id": id, "maintenance": "revoked"}})
	})
	admin.GET("/service-traces", func(c *gin.Context) {
		owner, _ := strconv.Atoi(c.Query("token_id"))
		handleServiceTraceList(c, runtime, owner)
	})
}

func (hub *serviceWorkspace) verifyProposal(id string, owner int) (gin.H, error) {
	proposal, exists := hub.proposal(id, owner)
	if !exists {
		return nil, errors.New("unknown proposal")
	}
	if proposal.Connection == nil {
		return gin.H{"proposal_id": id, "state": proposal.State, "verification": "not-applicable"}, nil
	}
	result := gin.H{"proposal_id": id, "state": proposal.State, "syntax": "passed", "contract": "passed", "upstream_operation": "not-covered", "upstream_called_by_verification": false}
	if proposal.State != "applied" {
		result["installation"] = "not-applied"
		return result, nil
	}
	definition, installed := hub.registry.get(proposal.Connection.Definition.ID)
	result["installation"] = "changed"
	if !installed {
		result["installation"] = "missing"
		return result, nil
	}
	if serviceDigest(definition) == serviceDigest(proposal.Connection.Definition) {
		result["installation"] = "matches"
	}
	var raw string
	err := hub.store.db.QueryRow(`SELECT payload FROM service_traces WHERE finished=1 AND json_extract(payload,'$.kind')='request' AND json_extract(payload,'$.pack')=? AND json_extract(payload,'$.pack_digest')=? AND (?<=0 OR token_id=?) AND json_extract(payload,'$.upstream_called')=1 AND json_extract(payload,'$.http_status') BETWEEN 200 AND 299 AND json_extract(payload,'$.outcome')='response_received' ORDER BY started_at DESC LIMIT 1`, definition.ID, serviceDigest(proposal.Connection.Definition), owner, owner).Scan(&raw)
	if err == nil {
		var trace serviceTrace
		if json.Unmarshal([]byte(raw), &trace) == nil {
			result["upstream_operation"] = "response-received"
			result["evidence"] = gin.H{"trace_id": trace.TraceID, "operation": trace.Operation, "http_status": trace.HTTPStatus, "finished_at": trace.FinishedAt, "resource_state": trace.ResourceState}
			result["business_result"] = "inspect resource state; HTTP success alone is not task completion"
		}
	}
	return result, nil
}

func (hub *serviceWorkspace) reconcile(id string, owner int) (serviceProposal, error) {
	hub.applyMu.Lock()
	defer hub.applyMu.Unlock()
	proposal, exists := hub.proposal(id, owner)
	if !exists {
		return proposal, errors.New("unknown proposal")
	}
	if proposal.State != "applying" {
		return proposal, nil
	}
	if proposal.Connection != nil && hub.registry.currentDigest() != proposal.PackDigest {
		return proposal, errors.New("installed digest does not match; inspect release history and prepare a new proposal without replaying")
	}
	err := hub.finishApprovedProposal(id)
	item, _ := hub.proposal(id, owner)
	return item, err
}
