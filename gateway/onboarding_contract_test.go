package main

import (
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestOnboardingTemplatesThroughPublicHTTP(t *testing.T) {
	for _, auth := range []string{"none", "bearer", "header"} {
		for _, recipe := range builtinServiceTemplates() {
			t.Run(recipe.ID+"-"+auth, func(t *testing.T) {
				rt, owner, _, _, server := serviceWorkspaceFixture(t)
				var example map[string]any
				raw, err := json.Marshal(recipe.Example)
				require.NoError(t, err)
				require.NoError(t, json.Unmarshal(raw, &example))
				example["id"] = "new-fixture"
				example["auth"] = map[string]any{"type": auth}
				if auth == "header" {
					example["auth"] = map[string]any{"type": auth, "header": "X-API-Key"}
				}
				body := map[string]any{"kind": "connection", "reason": "Inspect unchanged published template wire shape", "connection": map[string]any{"template_id": recipe.ID, "template_version": recipe.Version, "definition": example}}
				status, response := setupHTTP(t, server, owner, "POST", "/agent/onboarding", body)
				require.Equal(t, 201, status, string(response))
				var view struct {
					Proposal serviceProposal `json:"proposal"`
				}
				require.NoError(t, json.Unmarshal(response, &view))
				require.Equal(t, "awaiting_approval", view.Proposal.State)
				require.True(t, view.Proposal.Connection.Definition.Enabled)
				_, installed := rt.protocols.get("new-fixture")
				require.False(t, installed)
				t.Logf("published template with top-level enabled and auth=%s is accepted without installing or calling provider", auth)
			})
		}
	}
}

func TestOnboardingContractRecoveryAndFirstServiceCLI(t *testing.T) {
	rt, _, _, _, server := serviceWorkspaceFixture(t)
	dir := t.TempDir()
	for _, path := range []string{"/docs/identity-request.schema.json", "/docs/setup-proposal.schema.json", "/docs/protocol-pack-v3.schema.json"} {
		status, raw := setupHTTP(t, server, localToken{}, "GET", path, nil)
		require.Equal(t, 200, status, string(raw))
		require.True(t, json.Valid(raw))
		rr := httptest.NewRecorder()
		buildLANServer(rt).ServeHTTP(rr, httptest.NewRequest("GET", path, nil))
		require.Equal(t, 200, rr.Code)
	}
	raw, err := treeCLI(t, server, localToken{}, dir, "identity", "request", "first-agent", `{"packs":[{"id":"future-pack"}]}`)
	require.Error(t, err)
	var failure map[string]any
	require.NoError(t, json.Unmarshal(raw, &failure))
	require.Equal(t, "identity_request_invalid", failure["code"])
	require.Contains(t, string(raw), "policy.packs")
	_, err = treeCLI(t, server, localToken{}, dir, "identity", "cancel")
	require.NoError(t, err)
	raw, err = treeCLI(t, server, localToken{}, dir, "identity", "request", "first-agent", `{"packs":["future-pack"],"operations":["future-pack.search"]}`)
	require.Error(t, err)
	require.NoError(t, json.Unmarshal(raw, &failure))
	require.Equal(t, "identity_scope_unpublished", failure["code"])
	require.Contains(t, failure["message"], "lr identity prepare")
	require.Contains(t, failure["next_action"], "lr identity prepare")
	for _, key := range []string{"code", "reason", "retryable", "retry_after", "next_action", "alternatives"} {
		require.Contains(t, failure, key)
	}
	_, err = treeCLI(t, server, localToken{}, dir, "identity", "cancel")
	require.NoError(t, err)
	raw, err = treeCLI(t, server, localToken{}, dir, "identity", "prepare", "first-agent")
	require.NoError(t, err, string(raw))
	var identity identityRequest
	require.NoError(t, json.Unmarshal(raw, &identity))
	require.True(t, identity.Policy.SetupOnly)
	status, raw := setupHTTP(t, server, localToken{}, "POST", "/local/api/identity-requests/"+identity.ID+"/decision", map[string]any{"digest": identity.Digest, "approve": true})
	require.Equal(t, 200, status, string(raw))
	raw, err = treeCLI(t, server, localToken{}, dir, "identity", "claim")
	require.NoError(t, err, string(raw))
	require.Contains(t, string(raw), `"setup_only": true`)
	raw, err = treeCLI(t, server, localToken{}, dir, "setup", "schema")
	require.NoError(t, err, string(raw))
	require.Contains(t, string(raw), `"$defs"`)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, `{"ok":true}`) }))
	defer upstream.Close()
	raw, err = treeCLI(t, server, localToken{}, dir, "setup", "template", "read-api", "1")
	require.NoError(t, err, string(raw))
	var recipe struct {
		Template serviceTemplate `json:"template"`
	}
	require.NoError(t, json.Unmarshal(raw, &recipe))
	recipe.Template.Example.ID = "future-pack"
	recipe.Template.Example.BaseURL = upstream.URL
	input := serviceProposalInput{Kind: "connection", Reason: "First service without carrier scope", Connection: &serviceConnection{TemplateID: "read-api", TemplateVersion: "1", Definition: recipe.Template.Example}, Bundle: &serviceBundle{ID: "first-bundle", Name: "First", Members: []serviceBundleMember{{Pack: "future-pack", Operations: []string{"search"}}}}}
	body, _ := json.Marshal(input)
	raw, err = treeCLI(t, server, localToken{}, dir, "setup", "prepare", string(body))
	require.NoError(t, err, string(raw))
	var proposal struct {
		Proposal serviceProposal `json:"proposal"`
	}
	require.NoError(t, json.Unmarshal(raw, &proposal))
	require.Equal(t, "awaiting_approval", proposal.Proposal.State)
	require.True(t, proposal.Proposal.GrantPolicy.Before.SetupOnly)
	_, err = treeCLI(t, server, localToken{}, dir, "call", "future-pack", "search", "{}", "{}", `{"q":"fixture"}`)
	require.Error(t, err)
	require.Zero(t, calls.Load())
	status, raw = setupHTTP(t, server, localToken{}, "POST", "/local/api/service-proposals/"+proposal.Proposal.ID+"/decision", map[string]any{"digest": proposal.Proposal.Digest, "decision": "approve"})
	require.Equal(t, 200, status, string(raw))
	raw, err = treeCLI(t, server, localToken{}, dir, "preflight", "future-pack", "search", "{}", "{}", `{"q":"fixture"}`)
	require.NoError(t, err, string(raw))
	require.Zero(t, calls.Load())
	raw, err = treeCLI(t, server, localToken{}, dir, "call", "future-pack", "search", "{}", "{}", `{"q":"fixture"}`)
	require.NoError(t, err, string(raw))
	require.EqualValues(t, 1, calls.Load())
}

func TestOnboardingRepreparePreservesHistoryAndRequiresNewApproval(t *testing.T) {
	rt, owner, other, _, server := serviceWorkspaceFixture(t)
	conn := templateConnection(t, rt.workspace, "read-api", "legacy-fixture", "https://service.example.invalid")
	p, err := rt.workspace.prepare(owner, serviceProposalInput{Kind: "connection", Reason: "Legacy fixture", Connection: &conn, Bundle: &serviceBundle{ID: "legacy-kit", Name: "Legacy", Members: []serviceBundleMember{{Pack: "legacy-fixture", Operations: []string{"search"}}}}})
	require.NoError(t, err)
	p.GrantPolicy = nil
	p.Connection.Definition.Enabled = false
	require.NoError(t, rt.workspace.update(func(doc *serviceWorkspaceDocument) error { doc.Proposals[p.ID] = p; return nil }))
	_, err = rt.workspace.reprepare(p.ID, p.Digest, other.ID, true)
	require.Error(t, err)
	_, err = rt.workspace.reprepare(p.ID, "stale", owner.ID, true)
	require.Error(t, err)
	_, err = rt.workspace.reprepare(p.ID, p.Digest, owner.ID, false)
	require.ErrorContains(t, err, "enabled must be true")
	_, err = rt.workspace.decide(p.ID, p.Digest, "approve", "", 0)
	require.ErrorContains(t, err, "legacy proposal")
	// Refresh against the current policy rather than restoring the old limit.
	rt.policies.policies[owner.ID] = localTokenPolicy{TokenID: owner.ID, SetupOnly: true, DailyRequestLimit: 2}
	raw, err := treeCLI(t, server, owner, t.TempDir(), "setup", "reprepare", p.ID, p.Digest, "--enable")
	require.NoError(t, err, string(raw))
	var response struct {
		Proposal serviceProposal `json:"proposal"`
	}
	require.NoError(t, json.Unmarshal(raw, &response))
	fresh := response.Proposal
	require.NotEqual(t, p.ID, fresh.ID)
	require.NotEqual(t, p.Digest, fresh.Digest)
	require.Equal(t, p.ID, fresh.Supersedes)
	require.True(t, fresh.Connection.Definition.Enabled)
	require.Equal(t, 2, fresh.GrantPolicy.After.DailyRequestLimit)
	old, _ := rt.workspace.proposal(p.ID, owner.ID)
	require.Equal(t, "superseded", old.State)
	require.Equal(t, fresh.ID, old.SupersededBy)
	require.False(t, old.Connection.Definition.Enabled)
	require.Nil(t, old.GrantPolicy)
	_, installed := rt.protocols.get("legacy-fixture")
	require.False(t, installed)
	require.True(t, rt.policies.policies[owner.ID].SetupOnly)
	// Simulate a crash after replacement preparation but before linking history.
	require.NoError(t, rt.workspace.update(func(doc *serviceWorkspaceDocument) error { doc.Proposals[p.ID] = p; return nil }))
	again, err := rt.workspace.reprepare(p.ID, p.Digest, owner.ID, true)
	require.NoError(t, err)
	require.Equal(t, fresh.ID, again.ID)
	again, err = rt.workspace.reprepare(p.ID, p.Digest, owner.ID, true)
	require.NoError(t, err)
	require.Equal(t, fresh.ID, again.ID)
	require.Len(t, rt.workspace.proposals(owner.ID), 2)
	_, err = rt.workspace.decide(p.ID, p.Digest, "approve", "", 0)
	require.ErrorContains(t, err, "superseded")
	_, err = rt.workspace.decide(fresh.ID, p.Digest, "approve", "", 0)
	require.ErrorContains(t, err, "digest changed")
	_, err = rt.workspace.decide(fresh.ID, fresh.Digest, "approve", "", 0)
	require.NoError(t, err)
	installedDef, installed := rt.protocols.get("legacy-fixture")
	require.True(t, installed)
	require.True(t, installedDef.Enabled)
	require.False(t, rt.policies.policies[owner.ID].SetupOnly)
}

func TestOnboardingReprepareCannotReplaceUnknownOrRevokedWork(t *testing.T) {
	rt, owner, _, _, server := serviceWorkspaceFixture(t)
	conn := templateConnection(t, rt.workspace, "read-api", "protected-fixture", "https://service.example.invalid")
	p, err := rt.workspace.prepare(owner, serviceProposalInput{Kind: "connection", Reason: "Protected fixture", Connection: &conn})
	require.NoError(t, err)
	require.NoError(t, rt.workspace.update(func(doc *serviceWorkspaceDocument) error {
		v := doc.Proposals[p.ID]
		v.State = "applying"
		doc.Proposals[p.ID] = v
		return nil
	}))
	_, err = rt.workspace.reprepare(p.ID, p.Digest, owner.ID, true)
	require.ErrorContains(t, err, "reconcile")
	require.NoError(t, rt.workspace.update(func(doc *serviceWorkspaceDocument) error { doc.Proposals[p.ID] = p; return nil }))
	rt.policies.policies[owner.ID] = localTokenPolicy{TokenID: owner.ID, ExpiresAt: time.Now().Unix() - 1}
	_, err = rt.workspace.reprepare(p.ID, p.Digest, 0, true)
	require.ErrorContains(t, err, "expired")
	status, _ := setupHTTP(t, server, owner, "POST", "/local/api/service-proposals/"+p.ID+"/reprepare", map[string]any{"digest": p.Digest})
	require.Equal(t, 403, status)
	require.Len(t, rt.workspace.proposals(owner.ID), 1)
}
