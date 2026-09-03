package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeControlPlanePack(t *testing.T, root, upstream string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "demo", "guides"), 0o755))
	definition := `{
  "schema_version":"3","id":"demo","name":"Demo","description":"Control plane fixture",
  "base_url":` + mustJSON(upstream) + `,"enabled":true,"auth":{"type":"none"},
  "routes":[{"operation_id":"search","methods":["POST"],"path":"/search","query_parameters":["locale"],"summary":"Search fixture","request_example":{"query":"hello","limit":2},"response_schema":{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"]}}]
}`
	require.NoError(t, os.WriteFile(filepath.Join(root, "demo.json"), []byte(definition), 0o600))
	guide := "---\nid: usage\ntitle: Usage\nsummary: Fixture guide\nstatus: verified\nlast_verified: 2026-08-30\noperations:\n  - search\nvisibility: local\n---\n# Usage\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "demo", "guides", "usage.md"), []byte(guide), 0o600))
}

func mustJSON(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func performJSON(engine http.Handler, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}

func TestProtocolDraftValidatePlanApplyAndDelete(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"ok":true}`)) }))
	defer upstream.Close()
	protocolDir, dataDir := t.TempDir(), t.TempDir()
	writeControlPlanePack(t, protocolDir, upstream.URL)
	registry, err := newProtocolRegistry(protocolDir, dataDir)
	require.NoError(t, err)
	engine := gin.New()
	engine.POST("/drafts", registry.handleDraftCreate)
	engine.PUT("/drafts/:id/files/*path", registry.handleDraftFilePut)
	engine.POST("/drafts/:id/validate", registry.handleDraftValidate)
	engine.POST("/drafts/:id/plan", registry.handleDraftPlan)
	engine.POST("/apply", registry.handleAdminApply)
	engine.DELETE("/drafts/:id", registry.handleDraftDelete)

	created := performJSON(engine, http.MethodPost, "/drafts", `{"id":"change"}`)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	updatedGuide := "---\nid: usage\ntitle: Usage\nsummary: Updated fixture guide\nstatus: verified\nlast_verified: 2026-08-30\noperations:\n  - search\nvisibility: local\n---\n# Updated Usage\n"
	updated := performJSON(engine, http.MethodPut, "/drafts/change/files/demo/guides/usage.md", updatedGuide)
	require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())
	var updatedEnvelope struct {
		Data protocolDraftView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(updated.Body.Bytes(), &updatedEnvelope))
	require.Equal(t, 1, updatedEnvelope.Data.Impact.ChangedFiles)
	require.Len(t, updatedEnvelope.Data.Impact.Protocols, 1)
	assert.Equal(t, "demo", updatedEnvelope.Data.Impact.Protocols[0].ID)
	assert.Equal(t, []string{"guides"}, updatedEnvelope.Data.Impact.Protocols[0].Sections)
	assert.Empty(t, updatedEnvelope.Data.Impact.PoolIDs)
	updatedDefinition := `{
  "schema_version":"3","id":"demo","name":"Demo","description":"Control plane fixture",
  "base_url":` + mustJSON(upstream.URL) + `,"enabled":true,"auth":{"type":"none"},
  "pool":{"mode":"external"},
  "routes":[
    {"operation_id":"search","methods":["POST"],"path":"/search","summary":"Search fixture","request_example":{"query":"hello","limit":2},"response_schema":{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"]}},
    {"operation_id":"status","methods":["GET"],"path":"/status","summary":"Status fixture"}
  ]
}`
	updatedRoot := performJSON(engine, http.MethodPut, "/drafts/change/files/demo.json", updatedDefinition)
	require.Equal(t, http.StatusOK, updatedRoot.Code, updatedRoot.Body.String())
	require.NoError(t, json.Unmarshal(updatedRoot.Body.Bytes(), &updatedEnvelope))
	require.Equal(t, 2, updatedEnvelope.Data.Impact.ChangedFiles)
	require.Equal(t, []string{"demo"}, updatedEnvelope.Data.Impact.PoolIDs)
	require.Len(t, updatedEnvelope.Data.Impact.Protocols, 1)
	assert.Contains(t, updatedEnvelope.Data.Impact.Protocols[0].Sections, "pool")
	assert.Contains(t, updatedEnvelope.Data.Impact.Protocols[0].Sections, "routes")
	assert.Equal(t, []string{"status"}, updatedEnvelope.Data.Impact.Protocols[0].OperationsAdded)
	assert.Equal(t, "static", updatedEnvelope.Data.Impact.Protocols[0].PoolModeBefore)
	assert.Equal(t, "external", updatedEnvelope.Data.Impact.Protocols[0].PoolModeAfter)
	validated := performJSON(engine, http.MethodPost, "/drafts/change/validate", "")
	require.Equal(t, http.StatusOK, validated.Code, validated.Body.String())
	planned := performJSON(engine, http.MethodPost, "/drafts/change/plan", "")
	require.Equal(t, http.StatusOK, planned.Code, planned.Body.String())
	var envelope struct {
		Data protocolPackPlan `json:"data"`
	}
	require.NoError(t, json.Unmarshal(planned.Body.Bytes(), &envelope))
	require.Equal(t, "change", envelope.Data.DraftID)
	applied := performJSON(engine, http.MethodPost, "/apply", `{"digest":"`+envelope.Data.Digest+`"}`)
	require.Equal(t, http.StatusOK, applied.Code, applied.Body.String())
	guideBytes, err := os.ReadFile(filepath.Join(protocolDir, "demo", "guides", "usage.md"))
	require.NoError(t, err)
	assert.Contains(t, string(guideBytes), "Updated Usage")
	deleted := performJSON(engine, http.MethodDelete, "/drafts/change", "")
	assert.Equal(t, http.StatusOK, deleted.Code)
}

func TestProtocolDraftImpactLinksRuntimePoolWithoutClaimingPoolConfigChange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"ok":true}`)) }))
	defer upstream.Close()
	protocolDir, dataDir := t.TempDir(), t.TempDir()
	writeControlPlanePack(t, protocolDir, upstream.URL)
	livePath := filepath.Join(protocolDir, "demo.json")
	liveData, err := os.ReadFile(livePath)
	require.NoError(t, err)
	liveData = []byte(strings.Replace(string(liveData), `"routes":`, `"pool":{"mode":"external"},"routes":`, 1))
	require.NoError(t, os.WriteFile(livePath, liveData, 0o600))
	registry, err := newProtocolRegistry(protocolDir, dataDir)
	require.NoError(t, err)
	engine := gin.New()
	engine.POST("/drafts", registry.handleDraftCreate)
	engine.PUT("/drafts/:id/files/*path", registry.handleDraftFilePut)

	created := performJSON(engine, http.MethodPost, "/drafts", `{"id":"runtime-change"}`)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	changedData := []byte(strings.Replace(string(liveData), "Search fixture", "Search fixture revised", 1))
	updated := performJSON(engine, http.MethodPut, "/drafts/runtime-change/files/demo.json", string(changedData))
	require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())
	var envelope struct {
		Data protocolDraftView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(updated.Body.Bytes(), &envelope))
	require.Equal(t, []string{"demo"}, envelope.Data.Impact.PoolIDs)
	require.Len(t, envelope.Data.Impact.Protocols, 1)
	assert.Contains(t, envelope.Data.Impact.Protocols[0].Sections, "routes")
	assert.NotContains(t, envelope.Data.Impact.Protocols[0].Sections, "pool")
	assert.Equal(t, []string{"search"}, envelope.Data.Impact.Protocols[0].OperationsModified)
}

func TestProtocolDraftFilePolicySeparatesReadableAndWritableFiles(t *testing.T) {
	_, readable := protocolDraftRelativePath("README.md", false)
	assert.False(t, readable)
	_, schemaReadable := protocolDraftRelativePath("schema/protocol-pack-v3.schema.json", false)
	assert.True(t, schemaReadable)
	_, schemaWritable := protocolDraftRelativePath("schema/protocol-pack-v3.schema.json", true)
	assert.False(t, schemaWritable)
	_, guideWritable := protocolDraftRelativePath("demo/guides/usage.md", true)
	assert.True(t, guideWritable)
	_, operatorPackWritable := protocolDraftRelativePath("operator-pack.json", true)
	assert.True(t, operatorPackWritable)
	_, tokenFileWritable := protocolDraftRelativePath("api-token.json", true)
	assert.False(t, tokenFileWritable)
}

func TestTokenPolicyScopesAndConcurrency(t *testing.T) {
	store, err := newTokenPolicyStore(t.TempDir())
	require.NoError(t, err)
	store.policies[42] = localTokenPolicy{TokenID: 42, Surfaces: []string{"p"}, Packs: []string{"demo"}, Operations: []string{"demo.search"}, MaxInFlight: 1}
	release, status, message := store.begin(42, "p", "demo", "search", "")
	require.Zero(t, status, message)
	_, status, _ = store.begin(42, "p", "demo", "search", "")
	assert.Equal(t, http.StatusTooManyRequests, status)
	release()
	release, status, _ = store.begin(42, "p", "demo", "search", "")
	require.Zero(t, status)
	release()
	_, status, _ = store.begin(42, "mcp", "demo", "search", "")
	assert.Equal(t, http.StatusForbidden, status)
	_, status, _ = store.begin(42, "p", "demo", "delete", "")
	assert.Equal(t, http.StatusForbidden, status)
	assert.False(t, store.hasCapability(42, localRouterMaintainCapability))
	assert.False(t, store.hasCapability(99, localRouterMaintainCapability), "an absent policy is unlimited for calls but must not grant maintenance")
	store.policies[42] = localTokenPolicy{TokenID: 42, Capabilities: []string{localRouterMaintainCapability}}
	assert.True(t, store.hasCapability(42, localRouterMaintainCapability))
	_, status, message = store.begin(42, "p", "demo", "search", "")
	assert.Equal(t, http.StatusForbidden, status)
	assert.Contains(t, message, "maintenance token")
}

func performMCP(engine http.Handler, tool string, arguments any) *httptest.ResponseRecorder {
	params := map[string]any{"name": tool, "arguments": arguments}
	paramsJSON, _ := json.Marshal(params)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":` + string(paramsJSON) + `}`
	return performJSON(engine, http.MethodPost, "/manage/mcp", body)
}

func maintenanceStructuredData(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var envelope struct {
		Result struct {
			IsError           bool           `json:"isError"`
			StructuredContent map[string]any `json:"structuredContent"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.False(t, envelope.Result.IsError, response.Body.String())
	data, ok := envelope.Result.StructuredContent["data"].(map[string]any)
	require.True(t, ok, response.Body.String())
	return data
}

func TestMaintenanceMCPUsesSemanticDraftLifecycleAndSanitizesPack(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"ok":true}`)) }))
	defer upstream.Close()
	protocolDir, dataDir := t.TempDir(), t.TempDir()
	writeControlPlanePack(t, protocolDir, upstream.URL)
	registry, err := newProtocolRegistry(protocolDir, dataDir)
	require.NoError(t, err)
	engine := gin.New()
	engine.POST("/manage/mcp", registry.handleMaintenanceMCP())

	opened := performMCP(engine, "localrouter_draft_open", map[string]any{"draft_id": "agent-change"})
	maintenanceStructuredData(t, opened)
	read := performMCP(engine, "localrouter_draft_get_pack", map[string]any{"draft_id": "agent-change", "pack_id": "demo"})
	assert.NotContains(t, read.Body.String(), upstream.URL)

	patched := performMCP(engine, "localrouter_draft_patch_pack", map[string]any{"draft_id": "agent-change", "pack_id": "demo", "patch": map[string]any{"description": "Updated by semantic tool"}})
	maintenanceStructuredData(t, patched)
	review := performMCP(engine, "localrouter_draft_review", map[string]any{"draft_id": "agent-change"})
	reviewData := maintenanceStructuredData(t, review)
	digest, _ := reviewData["digest"].(string)
	require.True(t, validProtocolDigest(digest))
	impact := reviewData["impact"].(map[string]any)
	assert.NotEmpty(t, impact["files"])
	assert.NotEmpty(t, impact["protocols"])
	protocolImpact := impact["protocols"].([]any)[0].(map[string]any)
	assert.Equal(t, []any{"metadata"}, protocolImpact["sections"])
	assert.Empty(t, impact["pool_ids"])

	planned := performMCP(engine, "localrouter_draft_plan", map[string]any{"draft_id": "agent-change", "reviewed_digest": digest})
	maintenanceStructuredData(t, planned)
	applied := performMCP(engine, "localrouter_draft_apply", map[string]any{"digest": digest})
	applyData := maintenanceStructuredData(t, applied)
	assert.Equal(t, true, applyData["verified"])
	liveBytes, err := os.ReadFile(filepath.Join(protocolDir, "demo.json"))
	require.NoError(t, err)
	assert.Contains(t, string(liveBytes), "Updated by semantic tool")
	_, draftStillExists := registry.protocolDraftRoot("agent-change")
	assert.True(t, draftStillExists)
}

func TestMaintenanceMCPAuthorsCanonicalPackOperationAndSupplierProfile(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"ok":true}`)) }))
	defer upstream.Close()
	protocolDir, dataDir := t.TempDir(), t.TempDir()
	writeControlPlanePack(t, protocolDir, upstream.URL)
	registry, err := newProtocolRegistry(protocolDir, dataDir)
	require.NoError(t, err)
	engine := gin.New()
	engine.POST("/manage/mcp", registry.handleMaintenanceMCP())

	maintenanceStructuredData(t, performMCP(engine, "localrouter_draft_open", map[string]any{"draft_id": "authoring"}))
	created := performMCP(engine, "localrouter_draft_put_pack", map[string]any{
		"draft_id": "authoring", "pack_id": "publicapi", "name": "Public API", "description": "No-key provider fixture", "base_url": upstream.URL,
	})
	createdData := maintenanceStructuredData(t, created)
	assert.NotContains(t, created.Body.String(), upstream.URL, "protected upstream URL must not be echoed")
	assert.Equal(t, []any{"operations"}, createdData["pending"])

	profile := performMCP(engine, "localrouter_draft_put_upstream_profile", map[string]any{
		"draft_id": "authoring", "pack_id": "publicapi", "profile_id": "supplier",
		"profile": map[string]any{"set_headers": map[string]any{"X-Provider-Version": "2026-09"}, "user_agent": "omit", "query": "preserve-raw"},
	})
	maintenanceStructuredData(t, profile)

	operation := performMCP(engine, "localrouter_draft_put_operation", map[string]any{
		"draft_id": "authoring", "pack_id": "publicapi",
		"operation": map[string]any{"operation_id": "chat", "methods": []string{"POST"}, "path": "/chat", "summary": "Send chat", "upstream_profile": "supplier"},
	})
	maintenanceStructuredData(t, operation)
	lint := performMCP(engine, "localrouter_draft_lint_pack", map[string]any{"draft_id": "authoring", "pack_id": "publicapi"})
	lintData := maintenanceStructuredData(t, lint)
	assert.Equal(t, true, lintData["valid"])
	assert.Equal(t, float64(1), lintData["operations"])
	assert.Equal(t, float64(1), lintData["upstream_profiles"])

	root, ok := registry.protocolDraftRoot("authoring")
	require.True(t, ok)
	definition, err := registry.readMaintenanceDefinition(root, "publicapi")
	require.NoError(t, err)
	assert.Equal(t, protocolSchemaVersionV3, definition.SchemaVersion)
	assert.Equal(t, "none", definition.Auth.Type)
	assert.Equal(t, "credential", definition.Readiness.Mode)
	assert.Equal(t, 300, definition.Timeout)
	require.Len(t, definition.Routes, 1)
	assert.Equal(t, "http", definition.Routes[0].Transport)
	assert.Equal(t, "safe", definition.Routes[0].Retry.Mode)
	assert.Equal(t, "draft", definition.Routes[0].Availability.Status)
	assert.Equal(t, "omit", definition.UpstreamProfiles["supplier"].UserAgent)

	updated := performMCP(engine, "localrouter_draft_put_pack", map[string]any{
		"draft_id": "authoring", "pack_id": "publicapi", "name": "Public API", "description": "Updated without re-reading a protected target",
	})
	maintenanceStructuredData(t, updated)
	definition, err = registry.readMaintenanceDefinition(root, "publicapi")
	require.NoError(t, err)
	assert.Equal(t, upstream.URL, definition.BaseURL)
	assert.Equal(t, "Updated without re-reading a protected target", definition.Description)

	review := performMCP(engine, "localrouter_draft_review", map[string]any{"draft_id": "authoring"})
	reviewData := maintenanceStructuredData(t, review)
	assert.True(t, validProtocolDigest(reviewData["digest"].(string)))
}

func TestMaintenanceLintReturnsStructuredRepairForMissingAuthType(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"ok":true}`)) }))
	defer upstream.Close()
	protocolDir, dataDir := t.TempDir(), t.TempDir()
	writeControlPlanePack(t, protocolDir, upstream.URL)
	registry, err := newProtocolRegistry(protocolDir, dataDir)
	require.NoError(t, err)
	engine := gin.New()
	engine.POST("/manage/mcp", registry.handleMaintenanceMCP())

	maintenanceStructuredData(t, performMCP(engine, "localrouter_draft_open", map[string]any{"draft_id": "repair"}))
	maintenanceStructuredData(t, performMCP(engine, "localrouter_draft_patch_pack", map[string]any{
		"draft_id": "repair", "pack_id": "missingauth",
		"patch": map[string]any{"schema_version": "3", "name": "Missing Auth", "description": "Broken fixture", "enabled": true, "base_url": upstream.URL, "routes": []any{map[string]any{"operation_id": "models", "methods": []string{"GET"}, "path": "/models", "summary": "List models"}}},
	}))
	lintData := maintenanceStructuredData(t, performMCP(engine, "localrouter_draft_lint_pack", map[string]any{"draft_id": "repair", "pack_id": "missingauth"}))
	assert.Equal(t, false, lintData["valid"])
	issues := lintData["issues"].([]any)
	require.Len(t, issues, 1)
	issue := issues[0].(map[string]any)
	assert.Equal(t, "auth.type", issue["path"])
	assert.Equal(t, "invalid_enum", issue["code"])
	assert.Contains(t, issue["allowed"], "none")
	assert.Contains(t, issue["suggested_action"], "auth.type=none")
}

func TestMaintenanceApplyAutomaticallyRestoresPreviousRevision(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"ok":true}`)) }))
	defer upstream.Close()
	protocolDir, dataDir := t.TempDir(), t.TempDir()
	writeControlPlanePack(t, protocolDir, upstream.URL)
	registry, err := newProtocolRegistry(protocolDir, dataDir)
	require.NoError(t, err)
	previousDigest := registry.currentDigest()
	view, failure := registry.maintenanceOpenDraft("rollback-check")
	require.Nil(t, failure)
	_, failure = registry.maintenancePatchPack(view.ID, "demo", map[string]any{"description": "Must roll back"})
	require.Nil(t, failure)
	reviewed, failure := registry.maintenanceReviewDraft(view.ID)
	require.Nil(t, failure)
	digest := reviewed.(gin.H)["digest"].(string)
	_, failure = registry.maintenancePlanDraft(view.ID, digest)
	require.Nil(t, failure)
	registry.verifyInstall = func(_, _ string) error { return errors.New("injected local verification failure") }
	_, failure = registry.applyPlannedProtocolDigest(digest)
	require.NotNil(t, failure)
	assert.Equal(t, "verification_failed", failure.Code)
	require.NotNil(t, failure.Rollback)
	assert.True(t, failure.Rollback.Attempted)
	assert.True(t, failure.Rollback.Succeeded)
	assert.Equal(t, previousDigest, registry.currentDigest())
	liveBytes, err := os.ReadFile(filepath.Join(protocolDir, "demo.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(liveBytes), "Must roll back")
	_, draftStillExists := registry.protocolDraftRoot(view.ID)
	assert.True(t, draftStillExists)
}

func TestMaintenanceUsesAdminByDefaultAndOptionalDedicatedAgentToken(t *testing.T) {
	rootDir := t.TempDir()
	policyStore, err := newTokenPolicyStore(rootDir)
	require.NoError(t, err)
	dataStore, err := openLocalStore(filepath.Join(rootDir, "localrouter.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, dataStore.Close()) })
	root, err := dataStore.ensureRootUser()
	require.NoError(t, err)
	_, err = dataStore.db.Exec(`INSERT INTO tokens (id, user_id, key, status, name, agent_code, agent_name, workspace, runtime, created_time, expired_time, unlimited_quota, "group") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, 7, root.ID, "maintenance-key", localStatusEnabled, "agent-maintainer", "maintainer-007", "Maintenance Agent", "/workspace/maintenance", "codex", time.Now().Unix(), -1, 1, "default")
	require.NoError(t, err)
	runtime := localRuntime{adminToken: newAdminTokenStore("admin-secret"), rootUser: root, store: dataStore, policies: policyStore}
	engine := gin.New()
	engine.POST("/manage", localMaintenanceAuth(runtime), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	adminRequest := httptest.NewRequest(http.MethodPost, "/manage", strings.NewReader(`{}`))
	adminRequest.Header.Set(adminTokenHeader, "admin-secret")
	adminResponse := httptest.NewRecorder()
	engine.ServeHTTP(adminResponse, adminRequest)
	assert.Equal(t, http.StatusNoContent, adminResponse.Code)

	disabledRequest := httptest.NewRequest(http.MethodPost, "/manage", strings.NewReader(`{}`))
	disabledRequest.Header.Set("Authorization", "Bearer sk-maintenance-key")
	disabledResponse := httptest.NewRecorder()
	engine.ServeHTTP(disabledResponse, disabledRequest)
	assert.Equal(t, http.StatusForbidden, disabledResponse.Code)
	assert.Contains(t, disabledResponse.Body.String(), "agent_maintenance_disabled")

	policyStore.agentMaintenanceEnabled = true
	missingCapabilityRequest := httptest.NewRequest(http.MethodPost, "/manage", strings.NewReader(`{}`))
	missingCapabilityRequest.Header.Set("Authorization", "Bearer sk-maintenance-key")
	missingCapabilityResponse := httptest.NewRecorder()
	engine.ServeHTTP(missingCapabilityResponse, missingCapabilityRequest)
	assert.Equal(t, http.StatusForbidden, missingCapabilityResponse.Code)
	assert.Contains(t, missingCapabilityResponse.Body.String(), "maintenance_capability_required")

	policyStore.policies[7] = localTokenPolicy{TokenID: 7, Capabilities: []string{localRouterMaintainCapability}}
	allowedRequest := httptest.NewRequest(http.MethodPost, "/manage", strings.NewReader(`{}`))
	allowedRequest.Header.Set("Authorization", "Bearer sk-maintenance-key")
	allowedResponse := httptest.NewRecorder()
	engine.ServeHTTP(allowedResponse, allowedRequest)
	assert.Equal(t, http.StatusNoContent, allowedResponse.Code)
}

func TestMaintenanceAccessSettingDefaultsOffAndPersists(t *testing.T) {
	rootDir := t.TempDir()
	store, err := newTokenPolicyStore(rootDir)
	require.NoError(t, err)
	assert.False(t, store.isAgentMaintenanceEnabled())

	engine := gin.New()
	engine.PUT("/setting", store.handleMaintenanceAccessPut)
	response := performJSON(engine, http.MethodPut, "/setting", `{"agent_tokens_enabled":true}`)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.True(t, store.isAgentMaintenanceEnabled())

	reloaded, err := newTokenPolicyStore(rootDir)
	require.NoError(t, err)
	assert.True(t, reloaded.isAgentMaintenanceEnabled())
}

func TestMCPToolsAndTypedOpenAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"ok":true}`)) }))
	defer upstream.Close()
	protocolDir, dataDir := t.TempDir(), t.TempDir()
	writeControlPlanePack(t, protocolDir, upstream.URL)
	registry, err := newProtocolRegistry(protocolDir, dataDir)
	require.NoError(t, err)
	engine := gin.New()
	registerProtocolRoutes(engine, localRuntime{apiToken: "sk-test", protocols: registry})
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	request.Header.Set("Authorization", "Bearer sk-test")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"name":"demo__search"`)
	assert.Contains(t, response.Body.String(), `"query":{"type":"string"}`)
	assert.Contains(t, response.Body.String(), `"locale":{"type":"string"}`)

	document := registry.openAPIDocument()
	data, err := json.Marshal(document)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"required":["limit","query"]`)
	assert.Contains(t, string(data), `"in":"query","name":"locale","required":true`)
	assert.Contains(t, string(data), `"ok":{"type":"boolean"}`)
}

func TestProtocolEventStorePersistsSanitizedFields(t *testing.T) {
	store, err := newProtocolEventStore(t.TempDir())
	require.NoError(t, err)
	engine := gin.New()
	engine.POST("/mcp", func(c *gin.Context) {
		c.Set("localrouter_protocol_credential", "private-account-id")
		c.Set("localrouter_protocol_target", "provider-a")
		store.recordRequest(c, "mcp", "demo", "search", time.Now())
		c.Status(http.StatusOK)
	})
	request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	events, err := store.read(10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "demo", events[0].ProtocolID)
	assert.Equal(t, "provider-a", events[0].Target)
	assert.Equal(t, protocolCredentialRef("private-account-id"), events[0].CredentialRef)
	data, err := json.Marshal(events)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "private-account-id")
}

func TestSanitizeUpstreamPoolHealthKeepsOnlyAggregateFields(t *testing.T) {
	snapshot := sanitizeUpstreamPoolHealth([]byte(`{"service":"private-gateway","pool_total":8,"pool_alive":5,"sticky_resources":3,"upstream":"private.example","per_key_stats":{"secret-key":{"failures":1}}}`))
	require.NotNil(t, snapshot)
	assert.Equal(t, 8, snapshot["total"])
	assert.Equal(t, 5, snapshot["ready"])
	data, err := json.Marshal(snapshot)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "private.example")
	assert.NotContains(t, string(data), "secret-key")
}
