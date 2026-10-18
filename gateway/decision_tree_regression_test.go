package main

// Decision-flow regression tests. All upstreams and private state are isolated fixtures.
import (
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func treePublished(t *testing.T, rt localRuntime, target string) {
	_, registry := agentAPIFixture(t)
	def, _ := registry.get("readysearch")
	def.BaseURL = target
	writeProtocolDefinition(t, rt.config.ProtocolDir, def)
	require.NoError(t, rt.protocols.reload())
}
func treeEnroll(t *testing.T, rt localRuntime, s *httptest.Server, p localTokenPolicy) localToken {
	wd, err := os.Getwd()
	require.NoError(t, err)
	code, raw := setupHTTP(t, s, localToken{ID: 999, Key: strings.Repeat("ab", 32)}, "POST", "/agent/identity-requests", map[string]any{"agent_code": "tree-agent", "agent_name": "Tree fixture", "workspace": wd, "runtime": "fixture", "policy": p})
	require.Equal(t, 200, code, string(raw))
	var e struct {
		Data identityRequest `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &e))
	code, raw = setupHTTP(t, s, localToken{}, "POST", "/local/api/identity-requests/"+e.Data.ID+"/decision", map[string]any{"digest": e.Data.Digest, "approve": true})
	require.Equal(t, 200, code, string(raw))
	req, _, err := loadIdentityRequest(rt.store, e.Data.ID)
	require.NoError(t, err)
	token, err := rt.store.tokenByID(rt.rootUser.ID, req.TokenID, true)
	require.NoError(t, err)
	return token
}
func treeCLI(t *testing.T, s *httptest.Server, token localToken, dir string, args ...string) ([]byte, error) {
	if dir == "" {
		dir = t.TempDir()
	}
	cmd := exec.Command("bash", append([]string{"../tools/lr"}, args...)...)
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "LOCALROUTER_") && !strings.HasPrefix(e, "XDG_") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	cmd.Env = append(cmd.Env, "LOCALROUTER_BASE_URL="+s.URL, "LOCALROUTER_AGENT_SESSION=tree-audit", "XDG_DATA_HOME="+dir, "XDG_CONFIG_HOME="+dir, "XDG_STATE_HOME="+dir)
	if token.ID > 0 {
		path := filepath.Join(dir, "fixture-token")
		require.NoError(t, os.WriteFile(path, []byte(token.Key+"\n"), 0600))
		cmd.Env = append(cmd.Env, "LOCALROUTER_API_TOKEN_FILE="+path)
	}
	return cmd.CombinedOutput()
}
func treeJobs(t *testing.T, rt localRuntime, target, pack string) {
	c := templateConnection(t, rt.workspace, "async-job", pack, target)
	writeProtocolDefinition(t, rt.config.ProtocolDir, c.Definition)
	require.NoError(t, rt.protocols.reload())
}
func treeDecodeJob(t *testing.T, raw []byte) protocolWorkflowJob {
	var e struct {
		Data protocolWorkflowJob `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &e))
	return e.Data
}

func TestDecisionTreeA01SetupOnlyBreaksIdentityServiceCycle(t *testing.T) {
	rt, _, _, _, server := serviceWorkspaceFixture(t)
	r := identityRequest{AgentCode: "setup-agent", AgentName: "Fixture", Workspace: "/fixture", Policy: localTokenPolicy{SetupOnly: true}}
	require.NoError(t, validateIdentityRequest(rt, &r))
	token := treeEnroll(t, rt, server, r.Policy)
	allowed, _, _ := rt.policies.preview(token.ID, "p", "future-service", "search", "")
	require.False(t, allowed)
	_, err := rt.workspace.prepare(localToken{ID: 1, AgentCode: "localrouter-system"}, serviceProposalInput{Kind: "connection", Reason: "first service"})
	require.ErrorContains(t, err, "agent_identity_required")
	c := templateConnection(t, rt.workspace, "read-api", "first-service", "https://example.invalid")
	p, err := rt.workspace.prepare(token, serviceProposalInput{Kind: "connection", Reason: "first service", Connection: &c, Bundle: &serviceBundle{ID: "first-kit", Name: "First", Members: []serviceBundleMember{{Pack: "first-service", Operations: []string{"search"}}}}})
	require.NoError(t, err)
	require.True(t, p.GrantPolicy.Before.SetupOnly)
	require.False(t, p.GrantPolicy.After.SetupOnly)
	allowed, _, _ = rt.policies.preview(token.ID, "p", "first-service", "search", "")
	require.False(t, allowed, "preparation must not grant service access")
	p, err = rt.workspace.decide(p.ID, p.Digest, "approve", "", 0)
	require.NoError(t, err)
	allowed, _, _ = rt.policies.preview(token.ID, "p", "first-service", "search", "")
	require.True(t, allowed)
	allowed, _, _ = rt.policies.preview(token.ID, "p", "unapproved-service", "search", "")
	require.False(t, allowed)

}

func TestDecisionTreeA02ApprovalPreservesAndExtendsPolicy(t *testing.T) {
	rt, _, _, _, s := serviceWorkspaceFixture(t)
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, `{}`) }))
	defer up.Close()
	treePublished(t, rt, up.URL)
	token := treeEnroll(t, rt, s, localTokenPolicy{Packs: []string{"readysearch"}, Operations: []string{"readysearch.search"}})
	code, _ := setupHTTP(t, s, token, "POST", "/p/readysearch/search", map[string]any{"query": "before"})
	require.Equal(t, 200, code)
	c := templateConnection(t, rt.workspace, "read-api", "new-service", up.URL)
	p := approveServiceProposal(t, rt, token, serviceProposalInput{Kind: "connection", Reason: "new service", Connection: &c, Bundle: &serviceBundle{ID: "new-kit", Name: "New", Members: []serviceBundleMember{{Pack: "new-service", Operations: []string{"search"}}}}})
	require.Equal(t, "applied", p.State)
	require.True(t, rt.workspace.bundleAllows(token.ID, "p", "new-service", "search"))
	code, raw := setupHTTP(t, s, token, "GET", "/p/new-service/search?q=test", nil)
	require.Equal(t, 200, code, string(raw))
	require.NotNil(t, p.GrantPolicy)
	require.NotNil(t, p.RetainedBundle)
	code, raw = setupHTTP(t, s, token, "POST", "/p/readysearch/search", map[string]any{"query": "after"})
	require.Equal(t, 200, code, string(raw))

	require.EqualValues(t, 3, calls.Load())
}
func TestDecisionTreeA03ScopeIncludesRequiredModelLookup(t *testing.T) {
	rt, _, _, _, s := serviceWorkspaceFixture(t)
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"data":[{"id":"fixture-model"}]}`)
	}))
	defer up.Close()
	treePublished(t, rt, up.URL)
	token := treeEnroll(t, rt, s, localTokenPolicy{Packs: []string{"readysearch"}, Operations: []string{"readysearch.chat.completions"}})
	out, err := treeCLI(t, s, token, "", "exec", "readysearch", "chat.completions", `{"model":"fixture-model","messages":[]}`)
	require.NoError(t, err, string(out))
	require.EqualValues(t, 2, calls.Load())
	policy, _ := rt.policies.policyFor(token.ID)
	require.Contains(t, policy.Operations, "readysearch.models")
}

func TestDecisionTreeA04PreparationPreservesBusinessQuota(t *testing.T) {
	rt, _, _, _, s := serviceWorkspaceFixture(t)
	var catalogs, chats atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/models" {
			catalogs.Add(1)
			fmt.Fprint(w, `{"data":[{"id":"fixture-model"}]}`)
		} else {
			chats.Add(1)
			fmt.Fprint(w, `{}`)
		}
	}))
	defer up.Close()
	treePublished(t, rt, up.URL)
	token := treeEnroll(t, rt, s, localTokenPolicy{Packs: []string{"readysearch"}, Operations: []string{"readysearch.models", "readysearch.chat.completions"}, DailyRequestLimit: 1})
	out, err := treeCLI(t, s, token, "", "exec", "readysearch", "chat.completions", `{"model":"fixture-model","messages":[]}`)
	require.NoError(t, err, string(out))

	require.EqualValues(t, 1, catalogs.Load())
	require.EqualValues(t, 1, chats.Load())
	code, raw := setupHTTP(t, s, token, "GET", "/agent/operations/readysearch/chat.completions", nil)
	require.Equal(t, 200, code, string(raw))
	code, _ = setupHTTP(t, s, token, "POST", "/p/readysearch/chat/completions", map[string]any{"model": "fixture-model"})
	require.Equal(t, 429, code)
}
func TestDecisionTreeA05WorkflowScopeCanBeRequested(t *testing.T) {
	rt, _, _, _, server := serviceWorkspaceFixture(t)
	treeJobs(t, rt, "https://example.invalid", "jobs-fixture")
	token := treeEnroll(t, rt, server, localTokenPolicy{Packs: []string{"jobs-fixture"}, Operations: []string{"jobs-fixture.workflow.generate.create"}})
	policy, _ := rt.policies.policyFor(token.ID)
	for _, op := range []string{"jobs-fixture.create", "jobs-fixture.status", "jobs-fixture.cancel", "jobs-fixture.workflow.generate.advance"} {
		require.Contains(t, policy.Operations, op)
	}
	require.Contains(t, policy.Surfaces, "w")
	allowed, _, _ := rt.policies.preview(token.ID, "w", "jobs-fixture", "workflow.generate.create", "")
	require.True(t, allowed)

}

func TestDecisionTreeA06CompatibilityScopeEnforced(t *testing.T) {
	rt, _, _, _, server := serviceWorkspaceFixture(t)
	pack, op, ok := rt.channelProfiles.requestCapability("POST", "/v1/chat/completions")
	require.True(t, ok)
	token := treeEnroll(t, rt, server, localTokenPolicy{Packs: []string{pack}, Operations: []string{pack + "." + op}, Surfaces: []string{"v1"}})
	policy, _ := rt.policies.policyFor(token.ID)
	require.Contains(t, policy.Operations, pack+".get.v1.models")
	allowed, _, _ := rt.policies.preview(token.ID, "v1", pack, op, "")
	require.True(t, allowed)
	code, raw := setupHTTP(t, server, token, "POST", "/v1/embeddings", map[string]any{"model": "fixture-model", "input": "test"})
	require.Equal(t, 403, code, string(raw))
	require.Contains(t, string(raw), "operation")

}

func TestDecisionTreeA07RejectedEnrollmentCanRecover(t *testing.T) {
	rt, _, _, _, s := serviceWorkspaceFixture(t)
	treePublished(t, rt, "https://example.invalid")
	dir := t.TempDir()
	bad := `{"packs":["not-published"],"operations":["not-published.search"]}`
	good := `{"packs":["readysearch"],"operations":["readysearch.search"]}`
	out, err := treeCLI(t, s, localToken{}, dir, "identity", "request", "tree-agent", bad)
	require.Error(t, err)
	require.Contains(t, string(out), "published exact operation_key")
	out, err = treeCLI(t, s, localToken{}, dir, "identity", "request", "tree-agent", good)
	require.Error(t, err)
	require.Contains(t, string(out), "already has a different identity request")
	out, err = treeCLI(t, s, localToken{}, dir, "identity", "cancel")
	require.NoError(t, err, string(out))
	require.Contains(t, string(out), `"withdrawn": true`)
	out, err = treeCLI(t, s, localToken{}, dir, "identity", "request", "tree-agent", good)
	require.NoError(t, err, string(out))
	var n int
	require.NoError(t, rt.store.db.QueryRow(`SELECT count(*) FROM identity_requests WHERE state='pending'`).Scan(&n))
	require.Equal(t, 1, n)
}

func TestDecisionTreeA08QuotaAllowsJobRecovery(t *testing.T) {
	rt, owner, _, _, s := serviceWorkspaceFixture(t)
	var creates, statuses, cancels atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case "POST":
			creates.Add(1)
			fmt.Fprint(w, `{"id":"resource-1","status":"pending"}`)
		case "GET":
			statuses.Add(1)
			fmt.Fprint(w, `{"status":"succeeded"}`)
		case "DELETE":
			cancels.Add(1)
			fmt.Fprint(w, `{}`)
		}
	}))
	defer up.Close()
	treeJobs(t, rt, up.URL, "quota-jobs")
	rt.policies.policies[owner.ID] = localTokenPolicy{TokenID: owner.ID, Surfaces: []string{"p", "w"}, Packs: []string{"quota-jobs"}, Operations: []string{"*"}, DailyRequestLimit: 1}
	code, raw := setupHTTP(t, s, owner, "POST", "/w/quota-jobs/generate", map[string]any{})
	require.Equal(t, 202, code, string(raw))
	job := treeDecodeJob(t, raw)
	require.NotEmpty(t, job.ID)
	code, raw = setupHTTP(t, s, owner, "GET", "/w/quota-jobs/generate/"+job.ID, nil)
	require.Equal(t, 200, code, string(raw))
	require.Zero(t, statuses.Load(), "GET must not dispatch upstream")
	code, _ = setupHTTP(t, s, owner, "GET", "/w/quota-jobs/generate", nil)
	require.Equal(t, 200, code)
	code, raw = setupHTTP(t, s, owner, "DELETE", "/w/quota-jobs/generate/"+job.ID, nil)
	require.Equal(t, 200, code, string(raw))
	require.Equal(t, "cancelled", treeDecodeJob(t, raw).State)
	require.EqualValues(t, 1, cancels.Load())
	code, _ = setupHTTP(t, s, owner, "POST", "/w/quota-jobs/generate", map[string]any{})
	require.Equal(t, 429, code)
	require.EqualValues(t, 1, creates.Load())
}

func TestDecisionTreeA09UnknownWatchExits(t *testing.T) {
	rt, owner, _, _, s := serviceWorkspaceFixture(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected dispatch") }))
	defer up.Close()
	treeJobs(t, rt, up.URL, "unknown-jobs")
	job := protocolWorkflowJob{ID: "job-auditunknown1234", ProtocolID: "unknown-jobs", WorkflowID: "generate", State: "outcome_unknown", OwnerTokenID: owner.ID, MaxAttempts: 5}
	require.NoError(t, rt.protocols.saveWorkflowState(job.ProtocolID, protocolWorkflowState{Jobs: map[string]protocolWorkflowJob{job.ID: job}}))
	start := time.Now()
	out, err := treeCLI(t, s, owner, "", "watch", job.ProtocolID, job.WorkflowID, job.ID, "1")
	require.Error(t, err)
	require.NotContains(t, string(out), "watch_timeout")
	require.Contains(t, string(out), "outcome_unknown")
	require.Contains(t, string(out), "reconcile this existing job")
	require.Less(t, time.Since(start), 2*time.Second)
}
func TestDecisionTreeA10BudgetFailureRequiresReview(t *testing.T) {
	rt, owner, _, _, s := serviceWorkspaceFixture(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected dispatch") }))
	defer up.Close()
	treePublished(t, rt, up.URL)
	var err error
	rt.policies.budgets, err = initializeAgentBudgets(rt)
	require.NoError(t, err)
	rt.policies.policies[owner.ID] = localTokenPolicy{TokenID: owner.ID, Packs: []string{"readysearch"}, Operations: []string{"readysearch.search"}, DailyBudgetUSDMicros: 1000}
	code, raw := setupHTTP(t, s, owner, "POST", "/agent/preflight", map[string]any{"pack": "readysearch", "operation": "search", "input": map[string]any{"query": "test"}})
	require.Equal(t, 200, code)
	var v struct {
		Success    bool   `json:"success"`
		BlockedAt  string `json:"blocked_at"`
		Reason     string `json:"reason"`
		Next       string `json:"next_action"`
		Resolution struct {
			Actor    string `json:"next_actor"`
			Approval bool   `json:"approval_required"`
		} `json:"resolution"`
	}
	require.NoError(t, json.Unmarshal(raw, &v))
	require.False(t, v.Success)
	require.Equal(t, "agent_budget", v.BlockedAt)
	require.Equal(t, "agent_budget_quote_required", v.Reason)
	require.NotContains(t, v.Next, "call the operation")
	require.Equal(t, "human", v.Resolution.Actor)
	require.True(t, v.Resolution.Approval)
}
func TestDecisionTreeA11GraphStopsUnknownWrite(t *testing.T) {
	rt, owner, _, _, s := serviceWorkspaceFixture(t)
	var accepted atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		accepted.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close()
	}))
	defer up.Close()
	d := protocolDefinition{SchemaVersion: protocolSchemaVersionV3, ID: "replay-jobs", Name: "Replay mock", Description: "Audit", Enabled: true, BaseURL: up.URL, Auth: protocolAuth{Type: "none"}, Routes: []protocolRoute{{OperationID: "create", Methods: []string{"POST"}, Path: "/create", Summary: "Mock create", Retry: protocolRetryConfig{Mode: "never", MaxAttempts: 1}}}, Workflows: []protocolWorkflow{{ID: "generate", Name: "Fixture", InitialStep: "send", MaxPollAttempts: 3, Steps: []protocolWorkflowStep{{ID: "send", Operation: "create", Transitions: []protocolWorkflowTransition{{When: "status >= 500", To: "send"}, {Terminal: "succeeded"}}}}}}}
	writeProtocolDefinition(t, rt.config.ProtocolDir, d)
	require.NoError(t, rt.protocols.reload())
	code, raw := setupHTTP(t, s, owner, "POST", "/w/replay-jobs/generate", map[string]any{})
	require.Equal(t, 202, code, string(raw))
	job := treeDecodeJob(t, raw)
	require.EqualValues(t, 1, accepted.Load())
	require.Equal(t, "outcome_unknown", job.State)
	require.Contains(t, string(raw), "upstream_outcome_unknown")
	code, raw = setupHTTP(t, s, owner, "GET", "/w/replay-jobs/generate/"+job.ID, nil)
	require.Equal(t, 200, code, string(raw))
	require.EqualValues(t, 1, accepted.Load())
	code, raw = setupHTTP(t, s, owner, "POST", "/w/replay-jobs/generate/"+job.ID+"/advance", nil)
	require.Equal(t, 200, code, string(raw))
	require.EqualValues(t, 1, accepted.Load())
}
func TestDecisionTreeA12PersistBeforeCreate(t *testing.T) {
	rt, owner, _, _, s := serviceWorkspaceFixture(t)
	var accepted atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accepted.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"resource-already-created","status":"pending"}`)
	}))
	defer up.Close()
	treeJobs(t, rt, up.URL, "persist-jobs")
	require.NoError(t, os.WriteFile(filepath.Join(rt.config.StateDir, "workflow-state"), []byte("fixture directory fault"), 0600))
	code, raw := setupHTTP(t, s, owner, "POST", "/w/persist-jobs/generate", map[string]any{})
	require.Equal(t, 503, code)
	require.Zero(t, accepted.Load())
	require.Contains(t, string(raw), "workflow_intent_unavailable")
	require.NotContains(t, string(raw), "resource-already-created")
	require.NotContains(t, string(raw), "outcome_unknown")
}
func TestDecisionTreeControlCredentialAndVerification(t *testing.T) {
	rt, owner, _, _, _ := serviceWorkspaceFixture(t)
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, `{}`) }))
	defer up.Close()
	c := templateConnection(t, rt.workspace, "read-api", "credential-fixture", up.URL)
	c.Definition.Auth = protocolAuth{Type: "bearer"}
	p, err := rt.workspace.prepare(owner, serviceProposalInput{Kind: "connection", Reason: "credential test", Connection: &c})
	require.NoError(t, err)
	require.Equal(t, "awaiting_approval", p.State)
	_, err = rt.workspace.decide(p.ID, p.Digest, "approve", "", 0)
	require.ErrorContains(t, err, "needs_credential")
	p, err = rt.workspace.decide(p.ID, p.Digest, "approve", "fixture-only-secret", 0)
	require.NoError(t, err)
	require.Equal(t, "applied", p.State)
	v, err := rt.workspace.verifyProposal(p.ID, owner.ID)
	require.NoError(t, err)
	require.Equal(t, "not-covered", v["upstream_operation"])
	require.Zero(t, calls.Load())
	t.Log("CONTROL: preparation needs no secret, human supplies at approval, verification reads only")
}

func TestDecisionTreeGrantPolicyCASAndInterruptedActivation(t *testing.T) {
	rt, owner, _, _, _ := serviceWorkspaceFixture(t)
	treePublished(t, rt, "https://example.invalid")
	rt.policies.policies[owner.ID] = localTokenPolicy{TokenID: owner.ID, Packs: []string{"readysearch"}, Operations: []string{"readysearch.search"}, DailyRequestLimit: 3}
	input := serviceProposalInput{Kind: "bundle", Reason: "reviewed model calls", Bundle: &serviceBundle{ID: "chat-kit", Name: "Chat", Members: []serviceBundleMember{{Pack: "readysearch", Operations: []string{"chat.completions"}}}}}
	proposal, err := rt.workspace.prepare(owner, input)
	require.NoError(t, err)
	require.Contains(t, proposal.Bundle.Members[0].Operations, "models")
	require.Contains(t, proposal.GrantPolicy.After.Operations, "readysearch.models")
	require.Equal(t, 3, proposal.GrantPolicy.After.DailyRequestLimit)
	old := rt.policies.policies[owner.ID]
	changed := old
	changed.DailyRequestLimit = 2
	rt.policies.policies[owner.ID] = changed
	_, err = rt.workspace.decide(proposal.ID, proposal.Digest, "approve", "", 0)
	require.ErrorContains(t, err, "policy changed")
	require.Equal(t, changed, rt.policies.policies[owner.ID])
	rt.policies.policies[owner.ID] = old
	require.NoError(t, rt.workspace.update(func(doc *serviceWorkspaceDocument) error {
		p := doc.Proposals[proposal.ID]
		p.State = "applying"
		p.AuthorityBase = serviceAuthorityDigest(doc)
		doc.Proposals[p.ID] = p
		return nil
	}))
	// Simulate a crash after policy persistence but before grant activation.
	rt.policies.mu.Lock()
	rt.policies.policies[owner.ID] = proposal.GrantPolicy.After
	require.NoError(t, rt.policies.saveLocked())
	rt.policies.mu.Unlock()
	allowed, _, _ := rt.policies.preview(owner.ID, "p", "readysearch", "chat.completions", "")
	require.False(t, allowed, "partial activation must fail closed")
	require.NoError(t, rt.workspace.finishApprovedProposal(proposal.ID))
	allowed, _, _ = rt.policies.preview(owner.ID, "p", "readysearch", "chat.completions", "")
	require.True(t, allowed)
	require.Error(t, rt.workspace.finishApprovedProposal(proposal.ID), "finished journal cannot be replayed")
}

func TestDecisionTreeGrantWriteFailurePreservesPolicy(t *testing.T) {
	rt, owner, _, _, _ := serviceWorkspaceFixture(t)
	treePublished(t, rt, "https://example.invalid")
	rt.policies.policies[owner.ID] = localTokenPolicy{TokenID: owner.ID, SetupOnly: true}
	p, err := rt.workspace.prepare(owner, serviceProposalInput{Kind: "bundle", Reason: "grant", Bundle: &serviceBundle{ID: "kit", Name: "Kit", Members: []serviceBundleMember{{Pack: "readysearch", Operations: []string{"search"}}}}})
	require.NoError(t, err)
	require.NoError(t, rt.workspace.update(func(doc *serviceWorkspaceDocument) error {
		item := doc.Proposals[p.ID]
		item.State = "applying"
		item.AuthorityBase = serviceAuthorityDigest(doc)
		doc.Proposals[item.ID] = item
		return nil
	}))
	actual := rt.workspace.path
	rt.workspace.path = t.TempDir() // rename over a directory fails
	require.Error(t, rt.workspace.finishApprovedProposal(p.ID))
	policy, _ := rt.policies.policyFor(owner.ID)
	require.True(t, policy.SetupOnly)
	rt.workspace.path = actual
	require.NoError(t, rt.workspace.finishApprovedProposal(p.ID))
	policy, _ = rt.policies.policyFor(owner.ID)
	require.False(t, policy.SetupOnly)
}

func TestDecisionTreeCreateCommitFailureRetainsResource(t *testing.T) {
	rt, owner, _, _, s := serviceWorkspaceFixture(t)
	var accepted atomic.Int32
	path := rt.protocols.workflowStatePath("commit-jobs")
	var intentSaved atomic.Bool
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accepted.Add(1)
		state, err := rt.protocols.loadWorkflowState("commit-jobs")
		if err == nil {
			for _, job := range state.Jobs {
				if job.State == "creating" && job.ExecutionID != "" {
					intentSaved.Store(true)
				}
			}
		}
		// Fault only the result commit, after the provider has accepted the create.
		if err := os.Remove(path); err != nil {
			t.Error(err)
		}
		if err := os.Mkdir(path, 0700); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"resource-accepted","status":"pending"}`)
	}))
	defer up.Close()
	treeJobs(t, rt, up.URL, "commit-jobs")
	code, raw := setupHTTP(t, s, owner, "POST", "/w/commit-jobs/generate", map[string]any{})
	require.Equal(t, 502, code, string(raw))
	require.True(t, intentSaved.Load())
	require.EqualValues(t, 1, accepted.Load())
	job := treeDecodeJob(t, raw)
	require.Equal(t, "outcome_unknown", job.State)
	require.Equal(t, "resource-accepted", job.ResourceID)
	require.NotEmpty(t, job.ID)
	require.Contains(t, string(raw), `"retryable":false`)
}

func TestDecisionTreeControlLaneBoundedAndRevocable(t *testing.T) {
	rt, owner, _, _, _ := serviceWorkspaceFixture(t)
	rt.policies.policies[owner.ID] = localTokenPolicy{TokenID: owner.ID, Packs: []string{"fixture"}, Operations: []string{"fixture.status"}, DailyRequestLimit: 1}
	release, code, _ := rt.policies.begin(owner.ID, "p", "fixture", "status", "")
	require.Zero(t, code)
	release()
	for i := 0; i < 120; i++ {
		release, code, reason := rt.policies.beginClass(owner.ID, "p", "fixture", "status", "", true)
		require.Zero(t, code, reason)
		release()
	}
	_, code, _ = rt.policies.beginClass(owner.ID, "p", "fixture", "status", "", true)
	require.Equal(t, 429, code)
	_, code, _ = rt.policies.beginClass(owner.ID, "p", "fixture", "create", "", true)
	require.Equal(t, 403, code)
	policy := rt.policies.policies[owner.ID]
	policy.ExpiresAt = time.Now().Add(-time.Second).Unix()
	rt.policies.policies[owner.ID] = policy
	allowed, code, _ := rt.policies.previewScope(owner.ID, "p", "fixture", "status", "")
	require.False(t, allowed)
	require.Equal(t, 403, code)
}

func TestDecisionTreeParallelUnknownCannotTransition(t *testing.T) {
	rt, owner, _, _, s := serviceWorkspaceFixture(t)
	var accepted atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accepted.Add(1)
		io.Copy(io.Discard, r.Body)
		if r.URL.Path == "/unknown" {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			conn.Close()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"accepted":true}`)
	}))
	defer up.Close()
	def := protocolDefinition{SchemaVersion: protocolSchemaVersionV3, ID: "parallel-jobs", Name: "Parallel", Description: "Parallel outcome fixture", Enabled: true, BaseURL: up.URL, Auth: protocolAuth{Type: "none"}, Routes: []protocolRoute{
		{OperationID: "known", Methods: []string{"POST"}, Path: "/known", Summary: "Known", Retry: protocolRetryConfig{Mode: "never", MaxAttempts: 1}},
		{OperationID: "unknown", Methods: []string{"POST"}, Path: "/unknown", Summary: "Unknown", Retry: protocolRetryConfig{Mode: "never", MaxAttempts: 1}},
	}, Workflows: []protocolWorkflow{{ID: "flow", Name: "Flow", InitialStep: "fanout", Steps: []protocolWorkflowStep{{ID: "fanout", Kind: "parallel", Parallel: []protocolWorkflowCall{{ID: "known-branch", Operation: "known"}, {ID: "unknown-branch", Operation: "unknown"}}, Transitions: []protocolWorkflowTransition{{When: "status >= 500", To: "fanout"}, {Terminal: "succeeded"}}}}}}}
	writeProtocolDefinition(t, rt.config.ProtocolDir, def)
	require.NoError(t, rt.protocols.reload())
	code, raw := setupHTTP(t, s, owner, "POST", "/w/parallel-jobs/flow", map[string]any{})
	require.Equal(t, 202, code, string(raw))
	job := treeDecodeJob(t, raw)
	require.Equal(t, "outcome_unknown", job.State)
	require.EqualValues(t, 2, accepted.Load())
	require.Contains(t, string(job.Context["fanout"]), `"accepted":true`)
	rt.protocols.advanceDueGraphWorkflows(rt)
	code, raw = setupHTTP(t, s, owner, "POST", "/w/parallel-jobs/flow/"+job.ID+"/advance", nil)
	require.Equal(t, 200, code, string(raw))
	require.EqualValues(t, 2, accepted.Load())
}

func TestDecisionTreeGraphReadRecoveryKeepsWriteQuota(t *testing.T) {
	rt, owner, _, _, s := serviceWorkspaceFixture(t)
	var writes, reads atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			reads.Add(1)
		} else {
			writes.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer up.Close()
	def := protocolDefinition{SchemaVersion: protocolSchemaVersionV3, ID: "graph-quota", Name: "Graph", Description: "Quota recovery", Enabled: true, BaseURL: up.URL, Auth: protocolAuth{Type: "none"}, Routes: []protocolRoute{
		{OperationID: "create", Methods: []string{"POST"}, Path: "/create", Summary: "Create"}, {OperationID: "status", Methods: []string{"GET"}, Path: "/status", Summary: "Status"},
	}, Workflows: []protocolWorkflow{{ID: "flow", Name: "Flow", InitialStep: "start", Steps: []protocolWorkflowStep{
		{ID: "start", Operation: "create", Transitions: []protocolWorkflowTransition{{To: "poll"}}},
		{ID: "poll", Operation: "status", Transitions: []protocolWorkflowTransition{{Terminal: "succeeded"}}},
	}}}}
	writeProtocolDefinition(t, rt.config.ProtocolDir, def)
	require.NoError(t, rt.protocols.reload())
	rt.policies.policies[owner.ID] = localTokenPolicy{TokenID: owner.ID, DailyRequestLimit: 1}
	code, raw := setupHTTP(t, s, owner, "POST", "/w/graph-quota/flow", map[string]any{})
	require.Equal(t, 202, code, string(raw))
	job := treeDecodeJob(t, raw)
	code, raw = setupHTTP(t, s, owner, "GET", "/w/graph-quota/flow/"+job.ID, nil)
	require.Equal(t, 200, code)
	require.Zero(t, reads.Load())
	code, raw = setupHTTP(t, s, owner, "POST", "/w/graph-quota/flow/"+job.ID+"/advance", nil)
	require.Equal(t, 200, code, string(raw))
	require.Equal(t, "succeeded", treeDecodeJob(t, raw).State)
	require.EqualValues(t, 1, reads.Load())
	require.EqualValues(t, 1, writes.Load())
	code, _ = setupHTTP(t, s, owner, "POST", "/p/graph-quota/create", map[string]any{})
	require.Equal(t, 429, code)
	require.EqualValues(t, 1, writes.Load())
}

func TestDecisionTreeInterruptedActivationCanBeAbandonedWithoutOverwritingEdits(t *testing.T) {
	for _, humanEdited := range []bool{false, true} {
		t.Run(fmt.Sprint(humanEdited), func(t *testing.T) {
			rt, owner, _, _, _ := serviceWorkspaceFixture(t)
			treePublished(t, rt, "https://example.invalid")
			rt.policies.policies[owner.ID] = localTokenPolicy{TokenID: owner.ID, SetupOnly: true}
			p, err := rt.workspace.prepare(owner, serviceProposalInput{Kind: "bundle", Reason: "scope", Bundle: &serviceBundle{ID: "test-kit", Name: "Test", Members: []serviceBundleMember{{Pack: "readysearch", Operations: []string{"search"}}}}})
			require.NoError(t, err)
			require.NoError(t, rt.workspace.update(func(doc *serviceWorkspaceDocument) error {
				item := doc.Proposals[p.ID]
				item.State = "applying"
				item.AuthorityBase = serviceAuthorityDigest(doc)
				doc.Proposals[item.ID] = item
				return nil
			}))
			rt.policies.policies[owner.ID] = p.GrantPolicy.After
			if humanEdited {
				policy := p.GrantPolicy.Before
				policy.DailyRequestLimit = 7
				rt.policies.policies[owner.ID] = policy
			}
			_, err = rt.workspace.decide(p.ID, p.Digest, "abandon", "", owner.ID)
			require.ErrorContains(t, err, "human operator")
			p, err = rt.workspace.decide(p.ID, p.Digest, "abandon", "", 0)
			require.NoError(t, err)
			require.Equal(t, "abandoned", p.State)
			policy, _ := rt.policies.policyFor(owner.ID)
			require.True(t, policy.SetupOnly)
			if humanEdited {
				require.Equal(t, 7, policy.DailyRequestLimit)
			}
			require.True(t, rt.workspace.bundleAllows(owner.ID, "p", "readysearch", "search"), "unfinished journal no longer blocks")
			allowed, _, _ := rt.policies.previewScope(owner.ID, "p", "readysearch", "search", "")
			require.False(t, allowed, "restored setup-only policy still denies calls")
		})
	}
}

func TestDecisionTreeOldProposalCannotRestoreRevokedGrants(t *testing.T) {
	rt, owner, _, _, _ := serviceWorkspaceFixture(t)
	treePublished(t, rt, "https://example.invalid")
	c := templateConnection(t, rt.workspace, "read-api", "added-service", "https://example.invalid")
	p, err := rt.workspace.prepare(owner, serviceProposalInput{Kind: "connection", Reason: "extend", Connection: &c, Bundle: &serviceBundle{ID: "added-kit", Name: "Added", Members: []serviceBundleMember{{Pack: "added-service", Operations: []string{"search"}}}}})
	require.NoError(t, err)
	require.NotNil(t, p.RetainedBundle)
	require.NoError(t, rt.workspace.update(func(doc *serviceWorkspaceDocument) error { doc.Grants[owner.ID] = []serviceBundleGrant{}; return nil }))
	_, err = rt.workspace.decide(p.ID, p.Digest, "approve", "", 0)
	require.ErrorContains(t, err, "grants changed")
	require.False(t, rt.workspace.bundleAllows(owner.ID, "p", "readysearch", "search"))
	_, installed := rt.protocols.get("added-service")
	require.False(t, installed)
}
