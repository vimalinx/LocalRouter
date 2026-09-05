package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func serviceWorkspaceFixture(t *testing.T) (localRuntime, localToken, localToken, localToken, *httptest.Server) {
	t.Helper()
	runtime := auditRuntime(t)
	runtime.config.DataDir, runtime.config.StateDir, runtime.config.ProtocolDir = t.TempDir(), t.TempDir(), t.TempDir()
	var err error
	runtime.protocols, err = newProtocolRegistryWithState(runtime.config.ProtocolDir, runtime.config.DataDir, runtime.config.StateDir)
	require.NoError(t, err)
	runtime.policies, err = newTokenPolicyStore(runtime.config.DataDir)
	require.NoError(t, err)
	runtime.events, err = newProtocolEventStore(runtime.config.StateDir)
	require.NoError(t, err)
	runtime.protocols.events, runtime.protocols.policies = runtime.events, runtime.policies
	runtime.workspace, err = newServiceWorkspace(runtime)
	require.NoError(t, err)
	runtime.protocols.workspace, runtime.policies.workspace = runtime.workspace, runtime.workspace
	require.NoError(t, runtime.store.initializeServiceTraces())
	create := func(code string) localToken {
		id, err := runtime.store.createToken(runtime.rootUser.ID, localToken{Name: code, AgentCode: code, AgentName: code, Workspace: "/fixture", Status: 1, ExpiredTime: -1})
		require.NoError(t, err)
		token, err := runtime.store.tokenByID(runtime.rootUser.ID, int(id), true)
		require.NoError(t, err)
		return token
	}
	owner, other, maintainer := create("research-agent"), create("other-agent"), create("service-maintainer")
	runtime.apiToken = owner.Key
	runtime.policies.agentMaintenanceEnabled = true
	runtime.policies.policies[maintainer.ID] = localTokenPolicy{TokenID: maintainer.ID, Capabilities: []string{localRouterMaintainCapability}}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	runtime.config.Host, runtime.config.Port = "127.0.0.1", listener.Addr().(*net.TCPAddr).Port
	server := httptest.NewUnstartedServer(buildServer(runtime))
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	return runtime, owner, other, maintainer, server
}

func setupHTTP(t *testing.T, server *httptest.Server, token localToken, method, path string, body any) (int, []byte) {
	t.Helper()
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		require.NoError(t, err)
	}
	request, err := http.NewRequest(method, server.URL+path, bytes.NewReader(data))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	if token.ID > 0 {
		request.Header.Set("Authorization", "Bearer "+token.Key)
	}
	request.Header.Set("X-LocalRouter-Task-Id", "fixture-task")
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	result, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	return response.StatusCode, result
}

func templateConnection(t *testing.T, hub *serviceWorkspace, id, pack, target string) serviceConnection {
	t.Helper()
	template, ok := hub.template(id, "1")
	require.True(t, ok)
	data, err := json.Marshal(template.Example)
	require.NoError(t, err)
	var definition protocolDefinition
	require.NoError(t, json.Unmarshal(data, &definition))
	definition.ID, definition.BaseURL = pack, target
	return serviceConnection{TemplateID: id, TemplateVersion: "1", Definition: definition, SourceURL: "https://docs.example.invalid/service", Guide: "Observed fixture contract; no provider verification is claimed."}
}

func approveServiceProposal(t *testing.T, rt localRuntime, owner localToken, input serviceProposalInput) serviceProposal {
	t.Helper()
	proposal, err := rt.workspace.prepare(owner, input)
	require.NoError(t, err)
	proposal, err = rt.workspace.decide(proposal.ID, proposal.Digest, "approve", "", 0)
	require.NoError(t, err)
	require.Equal(t, "applied", proposal.State)
	return proposal
}

func TestServiceTemplatesAreStrictValidCredentialFreeRecipes(t *testing.T) {
	rt, _, _, _, _ := serviceWorkspaceFixture(t)
	templates := builtinServiceTemplates()
	require.Len(t, templates, 4)
	for _, template := range templates {
		t.Run(template.ID, func(t *testing.T) {
			raw, err := serviceTemplateFiles.ReadFile("service-templates/" + template.ID + ".json")
			require.NoError(t, err)
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.DisallowUnknownFields()
			var exact serviceTemplate
			require.NoError(t, decoder.Decode(&exact))
			require.NoError(t, rt.protocols.validate(&exact.Example))
			require.Equal(t, "https://service.example.invalid", exact.Example.BaseURL)
			require.Empty(t, exact.Example.Auth.SecretFile)
		})
	}
}

func TestServiceSetupRequiresExactApprovalAndDoesNotCallProvider(t *testing.T) {
	rt, owner, other, _, server := serviceWorkspaceFixture(t)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true,"private":"do-not-record-body"}`)
	}))
	defer upstream.Close()
	connection := templateConnection(t, rt.workspace, "read-api", "fixture-search", upstream.URL)
	input := serviceProposalInput{Kind: "connection", Reason: "Search a fixture without sharing the provider credential", Connection: &connection, Bundle: &serviceBundle{ID: "research-kit", Name: "Research", Members: []serviceBundleMember{{Pack: "fixture-search", Operations: []string{"search"}}}}}
	status, raw := setupHTTP(t, server, owner, "POST", "/agent/onboarding", input)
	require.Equal(t, 201, status, string(raw))
	var prepared struct {
		Proposal serviceProposal `json:"proposal"`
	}
	require.NoError(t, json.Unmarshal(raw, &prepared))
	proposal := prepared.Proposal
	require.Equal(t, "awaiting_approval", proposal.State)
	require.Equal(t, owner.ID, proposal.GrantTokenID)
	require.Zero(t, calls.Load())
	_, exists := rt.protocols.get("fixture-search")
	require.False(t, exists)
	status, _ = setupHTTP(t, server, other, "GET", "/agent/onboarding/"+proposal.ID, nil)
	assert.Equal(t, 404, status)
	status, raw = setupHTTP(t, server, localToken{}, "POST", "/local/api/service-proposals/"+proposal.ID+"/decision", map[string]any{"digest": strings.Repeat("a", 64), "decision": "approve"})
	assert.Equal(t, 409, status, string(raw))
	assert.Zero(t, calls.Load())
	status, raw = setupHTTP(t, server, localToken{}, "POST", "/local/api/service-proposals/"+proposal.ID+"/decision", map[string]any{"digest": proposal.Digest, "decision": "approve"})
	require.Equal(t, 200, status, string(raw))
	require.Zero(t, calls.Load())
	verification, err := rt.workspace.verifyProposal(proposal.ID, owner.ID)
	require.NoError(t, err)
	assert.Equal(t, "not-covered", verification["upstream_operation"])
	status, raw = setupHTTP(t, server, owner, "GET", "/p/fixture-search/search?q=test", nil)
	require.Equal(t, 200, status, string(raw))
	require.Equal(t, int32(1), calls.Load())
	traces, total, err := rt.store.serviceTraces(owner.ID, "", "fixture-task", 1, 100)
	require.NoError(t, err)
	require.GreaterOrEqual(t, total, int64(2))
	for _, trace := range traces {
		require.Equal(t, owner.ID, trace.TokenID)
		encoded, _ := json.Marshal(trace)
		assert.NotContains(t, string(encoded), "do-not-record-body")
	}
	verification, err = rt.workspace.verifyProposal(proposal.ID, owner.ID)
	require.NoError(t, err)
	assert.Equal(t, "response-received", verification["upstream_operation"])
	status, raw = setupHTTP(t, server, other, "GET", "/agent/traces?task_id=fixture-task", nil)
	require.Equal(t, 200, status)
	assert.Contains(t, string(raw), `"total":0`)
	// Re-reading or replaying an already applied decision never repeats work.
	_, err = rt.workspace.decide(proposal.ID, proposal.Digest, "approve", "", 0)
	require.NoError(t, err)
	assert.Equal(t, int32(1), calls.Load())
}

func TestServiceBundlesEnforceEmptyPinnedAndComposedGrants(t *testing.T) {
	rt, owner, _, _, server := serviceWorkspaceFixture(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{}`) }))
	defer upstream.Close()
	connection := templateConnection(t, rt.workspace, "read-api", "bundle-fixture", upstream.URL)
	connection.Definition.Routes = append(connection.Definition.Routes, protocolRoute{OperationID: "other", Methods: []string{"GET"}, Path: "/other", Summary: "Other operation"})
	approveServiceProposal(t, rt, owner, serviceProposalInput{Kind: "connection", Reason: "Prepare fixture", Connection: &connection})
	bundle := serviceBundle{ID: "search-only", Name: "Search only", Members: []serviceBundleMember{{Pack: "bundle-fixture", Operations: []string{"search"}}}}
	approved := approveServiceProposal(t, rt, owner, serviceProposalInput{Kind: "bundle", Reason: "Restrict this Agent", Bundle: &bundle})
	allowed, _, _ := rt.policies.preview(owner.ID, "p", "bundle-fixture", "search", "")
	assert.True(t, allowed)
	status, _ := setupHTTP(t, server, owner, "GET", "/p/bundle-fixture/other", nil)
	assert.Equal(t, 403, status)
	status, raw := setupHTTP(t, server, owner, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	require.Equal(t, 200, status)
	assert.Contains(t, string(raw), "bundle-fixture__search")
	assert.NotContains(t, string(raw), "bundle-fixture__other")
	allowed, _, _ = rt.policies.preview(owner.ID, "v1", "", "", "any-exploration-model")
	assert.True(t, allowed)
	composed, err := rt.workspace.resolveBundle(serviceBundle{ID: "composed-kit", Name: "Composed", Includes: []string{approved.Bundle.Revision}, Members: []serviceBundleMember{}})
	require.NoError(t, err)
	require.Len(t, composed.Members, 1)
	assert.Equal(t, []string{"search"}, composed.Members[0].Operations)
	// A Pack acquiring a new operation does not change the approved member list.
	definition, _ := rt.protocols.get("bundle-fixture")
	definition.Routes = append(definition.Routes, protocolRoute{OperationID: "new-paid", Methods: []string{"POST"}, Path: "/paid", Summary: "New paid action"})
	writeProtocolDefinition(t, rt.protocols.dir, definition)
	require.NoError(t, rt.protocols.reload())
	allowed, _, _ = rt.policies.preview(owner.ID, "p", "bundle-fixture", "new-paid", "")
	assert.False(t, allowed)
	loaded, err := newServiceWorkspace(rt)
	require.NoError(t, err)
	assert.False(t, loaded.bundleAllows(owner.ID, "p", "bundle-fixture", "new-paid"))
	status, raw = setupHTTP(t, server, localToken{}, "DELETE", fmt.Sprintf("/local/api/service-grants/%d", owner.ID), nil)
	require.Equal(t, 200, status, string(raw))
	assert.False(t, rt.workspace.bundleAllows(owner.ID, "p", "bundle-fixture", "search"))
	assert.True(t, rt.workspace.bundleAllows(owner.ID, "v1", "", "anything"))
}

func TestServiceScopedMaintainerRepairsOnlyApprovedAuthority(t *testing.T) {
	rt, owner, _, maintainer, server := serviceWorkspaceFixture(t)
	connection := templateConnection(t, rt.workspace, "read-api", "maint-fixture", "https://service.example.invalid")
	approveServiceProposal(t, rt, owner, serviceProposalInput{Kind: "connection", Reason: "Delegate compatible service repairs", Connection: &connection, MaintainerID: maintainer.ID, MaintenanceMode: "compatible"})
	connection.Definition.Description = "Corrected maintenance explanation"
	proposal, err := rt.workspace.prepare(maintainer, serviceProposalInput{Kind: "connection", Reason: "Correct description", Connection: &connection})
	require.NoError(t, err)
	require.True(t, rt.workspace.mayRepair(maintainer.ID, proposal))
	status, raw := setupHTTP(t, server, maintainer, "POST", "/manage/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "localrouter_setup_apply", "arguments": map[string]any{"id": proposal.ID, "digest": proposal.Digest}}})
	require.Equal(t, 200, status, string(raw))
	require.Contains(t, string(raw), `"isError":false`)
	definition, _ := rt.protocols.get("maint-fixture")
	assert.Equal(t, "Corrected maintenance explanation", definition.Description)
	status, raw = setupHTTP(t, server, maintainer, "POST", "/manage/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "localrouter_draft_open", "arguments": map[string]any{"draft_id": "escape-scope"}}})
	require.Equal(t, 200, status)
	assert.Contains(t, string(raw), "maintenance_scope_denied")
	connection.Definition.BaseURL = "https://another.example.invalid"
	proposal, err = rt.workspace.prepare(maintainer, serviceProposalInput{Kind: "connection", Reason: "Change target needs human approval", Connection: &connection})
	require.NoError(t, err)
	_, err = rt.workspace.decide(proposal.ID, proposal.Digest, "approve", "", maintainer.ID)
	require.ErrorContains(t, err, "needs_approval")
	definition, _ = rt.protocols.get("maint-fixture")
	assert.Equal(t, "https://service.example.invalid", definition.BaseURL)
	status, _ = setupHTTP(t, server, maintainer, "GET", "/agent/onboarding", nil)
	assert.Equal(t, 403, status)
}

func TestServiceWorkflowAndMCPPreserveOwnerTraceAndRevocation(t *testing.T) {
	rt, owner, other, _, server := serviceWorkspaceFixture(t)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" {
			fmt.Fprint(w, `{"id":"fixture-resource"}`)
		} else {
			fmt.Fprint(w, `{"id":"fixture-resource","status":"succeeded","result":{"url":"https://result.example.invalid"}}`)
		}
	}))
	defer upstream.Close()
	connection := templateConnection(t, rt.workspace, "async-job", "job-fixture", upstream.URL)
	approveServiceProposal(t, rt, owner, serviceProposalInput{Kind: "connection", Reason: "Asynchronous fixture", Connection: &connection, Bundle: &serviceBundle{ID: "job-kit", Name: "Job kit", Members: []serviceBundleMember{{Pack: "job-fixture", Workflows: []string{"generate"}}}}})
	status, raw := setupHTTP(t, server, owner, "POST", "/w/job-fixture/generate", map[string]any{"input": "fixture"})
	require.Equal(t, 202, status, string(raw))
	var created struct {
		Data protocolWorkflowJob `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &created))
	require.NotEmpty(t, created.Data.ID)
	stored, err := rt.protocols.loadWorkflowState("job-fixture")
	require.NoError(t, err)
	job := stored.Jobs[created.Data.ID]
	require.Equal(t, owner.ID, job.OwnerTokenID)
	require.NotEmpty(t, job.TraceID)
	items, _, err := rt.store.serviceTraces(owner.ID, job.TraceID, "", 1, 200)
	require.NoError(t, err)
	requests := 0
	for _, item := range items {
		require.Equal(t, owner.ID, item.TokenID)
		if item.Kind == "request" {
			requests++
		}
	}
	require.Equal(t, 1, requests)
	status, _ = setupHTTP(t, server, other, "GET", "/w/job-fixture/generate/"+job.ID, nil)
	assert.Equal(t, 404, status)
	require.NoError(t, rt.workspace.update(func(doc *serviceWorkspaceDocument) error { doc.Grants[owner.ID] = []serviceBundleGrant{}; return nil }))
	require.NoError(t, rt.protocols.editWorkflowState("job-fixture", func(state *protocolWorkflowState) (bool, error) {
		item := state.Jobs[job.ID]
		item.NextPollAt = time.Now().Add(-time.Second)
		state.Jobs[job.ID] = item
		return true, nil
	}))
	definition, workflow, ok := rt.protocols.workflowDefinition("job-fixture", "generate")
	require.True(t, ok)
	_, err = rt.protocols.runStoredWorkflow(t.Context(), rt, definition, workflow, job.ID, owner.ID, "advance", nil, 0)
	require.NoError(t, err)
	assert.Equal(t, int32(1), calls.Load(), "revocation must stop workflow internal calls before reaching the provider")
	// An unrelated identity with an allowed MCP call retains its own attribution.
	status, raw = setupHTTP(t, server, other, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "job-fixture__status", "arguments": map[string]any{"path_params": map[string]string{"task_id": "fixture-resource"}}}})
	require.Equal(t, 200, status, string(raw))
	assert.Contains(t, string(raw), `"isError":false`)
	items, _, err = rt.store.serviceTraces(other.ID, "", "", 1, 100)
	require.NoError(t, err)
	for _, item := range items {
		assert.Equal(t, other.ID, item.TokenID)
	}
}

func TestServiceTraceRecoveryAndNumericResourcePrivacy(t *testing.T) {
	rt, owner, _, _, server := serviceWorkspaceFixture(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"private-provider-id","state":"succeeded","usage":{"seconds":12.5},"body":"private-content"}`)
	}))
	defer upstream.Close()
	connection := templateConnection(t, rt.workspace, "read-api", "units-fixture", upstream.URL)
	connection.Definition.Routes[0].Metering = &serviceMetering{ResourceIDPath: "id", StatePath: "state", Units: []serviceUnitMapping{{Unit: "video-second", Path: "usage.seconds", Source: "response", Mode: "snapshot"}}}
	approveServiceProposal(t, rt, owner, serviceProposalInput{Kind: "connection", Reason: "Trace resources without guessing money", Connection: &connection})
	status, raw := setupHTTP(t, server, owner, "GET", "/p/units-fixture/search?q=fixture", nil)
	require.Equal(t, 200, status, string(raw))
	items, _, err := rt.store.serviceTraces(owner.ID, "", "", 1, 100)
	require.NoError(t, err)
	found := false
	for _, item := range items {
		if item.Kind != "request" {
			continue
		}
		found = true
		require.Len(t, item.Units, 1)
		assert.Equal(t, 12.5, item.Units[0].Quantity)
		assert.NotEmpty(t, item.ResourceRef)
		assert.Nil(t, item.Cost)
		data, _ := json.Marshal(item)
		assert.NotContains(t, string(data), "private-provider-id")
		assert.NotContains(t, string(data), "private-content")
	}
	require.True(t, found)
	require.NoError(t, rt.store.saveServiceTrace(serviceTrace{SpanID: "fixture-interrupted", TraceID: strings.Repeat("b", 32), TokenID: owner.ID, Kind: "request", StartedAt: time.Now(), Outcome: "running"}))
	require.NoError(t, rt.store.initializeServiceTraces())
	items, _, err = rt.store.serviceTraces(owner.ID, strings.Repeat("b", 32), "", 1, 10)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "outcome_unknown", items[0].Outcome)
	assert.Equal(t, filepath.Join(rt.config.DataDir, "service-workspace.json"), rt.workspace.path)
}

func TestServiceReconcileCompletesApprovedReceiptAndPreservesRevocation(t *testing.T) {
	for _, revoked := range []bool{false, true} {
		t.Run(fmt.Sprint(revoked), func(t *testing.T) {
			rt, owner, _, _, _ := serviceWorkspaceFixture(t)
			proposal, err := rt.workspace.prepare(owner, serviceProposalInput{Kind: "bundle", Reason: "empty scope", Bundle: &serviceBundle{ID: "empty", Name: "Empty", Members: []serviceBundleMember{}}})
			require.NoError(t, err)
			require.NoError(t, rt.workspace.update(func(doc *serviceWorkspaceDocument) error {
				item := doc.Proposals[proposal.ID]
				item.State = "applying"
				item.DecidedBy = "operator"
				item.AuthorityBase = serviceAuthorityDigest(doc)
				doc.Proposals[item.ID] = item
				if revoked {
					doc.Grants[owner.ID] = []serviceBundleGrant{}
				}
				return nil
			}))
			result, err := rt.workspace.reconcile(proposal.ID, owner.ID)
			if revoked {
				require.ErrorContains(t, err, "authority changed")
				require.Equal(t, "applying", result.State)
				require.Empty(t, rt.workspace.doc.Grants[owner.ID])
			} else {
				require.NoError(t, err)
				require.Equal(t, "applied", result.State)
				require.Len(t, rt.workspace.doc.Grants[owner.ID], 1)
			}
		})
	}
}

func TestServiceSummaryDeduplicatesSnapshotsWithoutMixingIdentityOrCost(t *testing.T) {
	rt, owner, other, _, _ := serviceWorkspaceFixture(t)
	now := time.Now().UTC()
	for index, token := range []int{owner.ID, owner.ID, other.ID} {
		started := now.Add(time.Duration(index) * time.Second)
		item := serviceTrace{SpanID: serviceID("")[:16], TraceID: serviceID(""), TokenID: token, Kind: "request", Pack: "video", ResourceRef: "resource-one", TaskID: "video-task", StartedAt: started, FinishedAt: &started, UpstreamCalled: true, Outcome: "response_received", Units: []serviceUnitObservation{{Unit: "second", Quantity: float64(index+1) * 5, Source: "response", Mode: "snapshot"}}}
		require.NoError(t, rt.store.saveServiceTrace(item))
	}
	summary, err := rt.store.serviceTraceSummary(0, "", "video-task")
	require.NoError(t, err)
	require.Equal(t, 3, summary.Requests)
	require.Equal(t, 3, summary.UnknownCosts)
	require.Empty(t, summary.CostByStatus)
	require.Len(t, summary.Units, 2)
	own, err := rt.store.serviceTraceSummary(owner.ID, "", "video-task")
	require.NoError(t, err)
	require.Len(t, own.Units, 1)
	require.Equal(t, float64(10), own.Units[0].Quantity)
}

func TestServiceMCPDoesNotReserveDuplicateConcurrencyOrDailyUsage(t *testing.T) {
	rt, owner, _, _, server := serviceWorkspaceFixture(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"ok":true}`) }))
	defer upstream.Close()
	connection := templateConnection(t, rt.workspace, "read-api", "limited", upstream.URL)
	approveServiceProposal(t, rt, owner, serviceProposalInput{Kind: "connection", Reason: "limited fixture", Connection: &connection})
	rt.policies.mu.Lock()
	rt.policies.policies[owner.ID] = localTokenPolicy{TokenID: owner.ID, Surfaces: []string{"mcp"}, MaxInFlight: 1, DailyRequestLimit: 1}
	rt.policies.mu.Unlock()
	status, raw := setupHTTP(t, server, owner, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "limited__search", "arguments": map[string]any{"query": map[string]string{"q": "fixture"}}}})
	require.Equal(t, 200, status, string(raw))
	require.NotContains(t, string(raw), `"isError":true`)
	require.Contains(t, string(raw), `"isError":false`)
}
