package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIdentityEnrollmentIssuesAtOnceWithScopeRecoveryAndRevocation(t *testing.T) {
	rt, _, _, _, server := serviceWorkspaceFixture(t)
	_, registry := agentAPIFixture(t)
	// The real fixture server holds this registry pointer; install fixture-only
	// operation definitions without touching any real provider.
	definition, _ := registry.get("readysearch")
	writeProtocolDefinition(t, rt.config.ProtocolDir, definition)
	require.NoError(t, rt.protocols.reload())
	claim := strings.Repeat("ab", 32)
	post := func(path string, body any, proof string) *httptest.ResponseRecorder {
		encoded, err := json.Marshal(body)
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(encoded))
		req.Header.Set("Authorization", "Bearer "+proof)
		req.Header.Set("Content-Type", "application/json")
		req.Host = strings.TrimPrefix(server.URL, "http://")
		out := httptest.NewRecorder()
		buildServer(rt).ServeHTTP(out, req)
		return out
	}
	body := map[string]any{"agent_code": "enrolled-agent", "agent_name": "Enrolled Agent", "workspace": "/fixture", "runtime": "fixture", "policy": localTokenPolicy{Packs: []string{"readysearch"}, Operations: []string{"readysearch.models", "readysearch.search"}, DailyRequestLimit: 3}}
	prepared := post("/agent/identity-requests", body, claim)
	require.Equal(t, 200, prepared.Code, prepared.Body.String())
	var envelope struct {
		Data identityRequest `json:"data"`
	}
	require.NoError(t, json.Unmarshal(prepared.Body.Bytes(), &envelope))
	pending := envelope.Data
	// No human review: the identity exists as soon as the request returns.
	require.Equal(t, "approved", pending.State)
	require.NotContains(t, prepared.Body.String(), `"key":`)
	count, err := rt.store.count("tokens", "agent_code = ?", "enrolled-agent")
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	// Idempotent submission and possession checks, without secrets.
	again := post("/agent/identity-requests", body, claim)
	require.Equal(t, prepared.Body.String(), again.Body.String())
	wrong := post("/agent/identity-requests/"+pending.ID+"/claim", map[string]any{}, strings.Repeat("cd", 32))
	require.Equal(t, 404, wrong.Code)
	// The human review endpoints no longer exist.
	status, _ := setupHTTP(t, server, localToken{}, "POST", "/local/api/identity-requests/"+pending.ID+"/decision", map[string]any{"digest": pending.Digest, "approve": true})
	require.Equal(t, 404, status)
	issued := post("/agent/identity-requests/"+pending.ID+"/claim", map[string]any{}, claim)
	require.Equal(t, 200, issued.Code)
	var delivery struct {
		Data struct {
			Ready bool   `json:"ready"`
			Token string `json:"token"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(issued.Body.Bytes(), &delivery))
	require.True(t, delivery.Data.Ready)
	token, err := rt.store.validateToken(delivery.Data.Token)
	require.NoError(t, err)
	allowed, _, _ := rt.policies.preview(token.ID, "p", "readysearch", "search", "")
	require.True(t, allowed)
	allowed, _, _ = rt.policies.preview(token.ID, "p", "readysearch", "scrape", "")
	require.False(t, allowed)
	allowed, _, _ = rt.policies.preview(token.ID, "v1", "", "", "")
	require.False(t, allowed)
	// Lost claim responses and gateway restart preserve the same Token and policy.
	repeated := post("/agent/identity-requests/"+pending.ID+"/claim", map[string]any{}, claim)
	require.Equal(t, identityHash(issued.Body.String()), identityHash(repeated.Body.String()))
	reloaded, err := newTokenPolicyStore(rt.config.DataDir)
	require.NoError(t, err)
	policy, exists := reloaded.policyFor(token.ID)
	require.True(t, exists)
	require.Equal(t, 3, policy.DailyRequestLimit)
	// Neither the LAN listener nor a hostile browser Origin may enroll.
	request := httptest.NewRequest("POST", "/agent/identity-requests", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer "+claim)
	response := httptest.NewRecorder()
	buildLANServer(rt).ServeHTTP(response, request)
	require.Equal(t, 404, response.Code)
	request = httptest.NewRequest("POST", "/agent/identity-requests", strings.NewReader(`{}`))
	request.Host = strings.TrimPrefix(server.URL, "http://")
	request.Header.Set("Origin", "https://untrusted.example.invalid")
	response = httptest.NewRecorder()
	buildServer(rt).ServeHTTP(response, request)
	require.Equal(t, 403, response.Code)
	replacement, err := randomSecret(32, "")
	require.NoError(t, err)
	require.NoError(t, rt.store.updateTokenKey(rt.rootUser.ID, token.ID, replacement))
	rotated := post("/agent/identity-requests/"+pending.ID+"/claim", map[string]any{}, claim)
	require.Equal(t, 410, rotated.Code)
	require.NoError(t, rt.store.deleteToken(rt.rootUser.ID, token.ID))
	revoked := post("/agent/identity-requests/"+pending.ID+"/claim", map[string]any{}, claim)
	require.Equal(t, 410, revoked.Code)
	_, err = rt.store.validateToken(delivery.Data.Token)
	require.Error(t, err)
}

func TestIdentityEnrollmentFailedPersistenceStaysPendingAndExpires(t *testing.T) {
	rt, _, _, _, _ := serviceWorkspaceFixture(t)
	_, registry := agentAPIFixture(t)
	rt.protocols = registry
	request := identityRequest{ID: "identity-fixture", AgentCode: "fixture-pending", AgentName: "Fixture", Workspace: "/fixture", Runtime: "fixture", Policy: localTokenPolicy{Packs: []string{"readysearch"}, Operations: []string{"readysearch.search"}}, State: "pending", CreatedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Hour).Unix(), Digest: "fixture-digest"}
	require.NoError(t, validateIdentityRequest(rt, &request))
	data, err := json.Marshal(request)
	require.NoError(t, err)
	_, err = rt.store.db.Exec(`INSERT INTO identity_requests (id,claim_hash,document,state,created_at,expires_at) VALUES (?,?,?,'pending',?,?)`, request.ID, "fixture", string(data), request.CreatedAt, request.ExpiresAt)
	require.NoError(t, err)
	// Failed policy persistence must not leave an active/unrestricted identity.
	rt.policies.path = t.TempDir() + "/missing/policies.json"
	rt.store.identityMu.Lock()
	_, status, _ := issueIdentityLocked(rt, request)
	rt.store.identityMu.Unlock()
	require.Equal(t, 500, status)
	count, err := rt.store.count("tokens", "agent_code = ?", request.AgentCode)
	require.NoError(t, err)
	require.Zero(t, count)
	stored, _, err := loadIdentityRequest(rt.store, request.ID)
	require.NoError(t, err)
	require.Equal(t, "pending", stored.State)
	request.ExpiresAt = time.Now().Add(-time.Second).Unix()
	data, _ = json.Marshal(request)
	_, err = rt.store.db.Exec(`UPDATE identity_requests SET document=? WHERE id=?`, string(data), request.ID)
	require.NoError(t, err)
	stored, _, err = loadIdentityRequest(rt.store, request.ID)
	require.NoError(t, err)
	require.Equal(t, "expired", stored.State)
}

func TestIdentityAccessChangesApplyAtOnceAndPreserveUsage(t *testing.T) {
	rt, owner, other, _, server := serviceWorkspaceFixture(t)
	_, registry := agentAPIFixture(t)
	def, _ := registry.get("readysearch")
	writeProtocolDefinition(t, rt.config.ProtocolDir, def)
	require.NoError(t, rt.protocols.reload())
	rt.policies.policies[owner.ID] = localTokenPolicy{TokenID: owner.ID, Surfaces: []string{"p"}, Packs: []string{"readysearch"}, Operations: []string{"readysearch.models"}, DailyRequestLimit: 4}
	release, status, _ := rt.policies.begin(owner.ID, "p", "readysearch", "models", "")
	require.Zero(t, status)
	release()
	requested := localTokenPolicy{Surfaces: []string{"p"}, Packs: []string{"readysearch"}, Operations: []string{"readysearch.models", "readysearch.search"}, DailyRequestLimit: 8}
	status, raw := setupHTTP(t, server, owner, "POST", "/agent/access-requests", map[string]any{"policy": requested})
	require.Equal(t, 200, status, string(raw))
	var applied struct {
		Data identityRequest `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &applied))
	require.Equal(t, "approved", applied.Data.State)
	allowed, _, _ := rt.policies.preview(owner.ID, "p", "readysearch", "search", "")
	require.True(t, allowed)
	require.Equal(t, 1, rt.policies.usage[owner.ID].DayCount)
	require.Equal(t, 8, rt.policies.policies[owner.ID].DailyRequestLimit)
	status, _ = setupHTTP(t, server, other, "GET", "/agent/access-requests/"+applied.Data.ID, nil)
	require.Equal(t, 404, status)
	// All human console APIs reject service credentials, even in loopback
	// password-free mode. The dedicated request endpoint grants no maintenance.
	status, _ = setupHTTP(t, server, owner, "POST", "/local/api/tokens", map[string]any{})
	require.Equal(t, 403, status)
}
