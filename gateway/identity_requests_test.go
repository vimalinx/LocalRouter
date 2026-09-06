package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestIdentityEnrollmentApprovalScopeRecoveryAndRevocation(t *testing.T) {
	rt, owner, _, _, server := serviceWorkspaceFixture(t)
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
	require.Equal(t, "pending", pending.State)
	count, err := rt.store.count("tokens", "agent_code = ?", "enrolled-agent")
	require.NoError(t, err)
	require.Zero(t, count)
	// Idempotent submission and possession checks, without authority or secrets.
	again := post("/agent/identity-requests", body, claim)
	require.Equal(t, prepared.Body.String(), again.Body.String())
	wrong := post("/agent/identity-requests/"+pending.ID+"/claim", map[string]any{}, strings.Repeat("cd", 32))
	require.Equal(t, 404, wrong.Code)
	before := post("/agent/identity-requests/"+pending.ID+"/claim", map[string]any{}, claim)
	require.NotContains(t, before.Body.String(), `"token":`)
	status, _ := setupHTTP(t, server, owner, "POST", "/local/api/identity-requests/"+pending.ID+"/decision", map[string]any{"digest": pending.Digest, "approve": true})
	require.Equal(t, 403, status)
	status, _ = setupHTTP(t, server, localToken{}, "POST", "/local/api/identity-requests/"+pending.ID+"/decision", map[string]any{"digest": "stale", "approve": true})
	require.Equal(t, 409, status)
	status, raw := setupHTTP(t, server, localToken{}, "POST", "/local/api/identity-requests/"+pending.ID+"/decision", map[string]any{"digest": pending.Digest, "approve": true})
	require.Equal(t, 200, status, string(raw))
	require.NotContains(t, string(raw), `"key":`)
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
	status, raw = setupHTTP(t, server, localToken{}, "GET", "/local/api/identity-requests", nil)
	require.Equal(t, 200, status)
	require.NotContains(t, string(raw), delivery.Data.Token)
	require.NotContains(t, string(raw), claim)
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

func TestIdentityEnrollmentExpiryAndFailedPersistenceStayUnapproved(t *testing.T) {
	rt, _, _, _, _ := serviceWorkspaceFixture(t)
	_, registry := agentAPIFixture(t)
	rt.protocols = registry
	request := identityRequest{ID: "identity-fixture", AgentCode: "fixture-pending", AgentName: "Fixture", Workspace: "/fixture", Runtime: "fixture", Policy: localTokenPolicy{Packs: []string{"readysearch"}, Operations: []string{"readysearch.search"}}, State: "pending", CreatedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Hour).Unix(), Digest: "fixture-digest"}
	data, err := json.Marshal(request)
	require.NoError(t, err)
	_, err = rt.store.db.Exec(`INSERT INTO identity_requests (id,claim_hash,document,state,created_at,expires_at) VALUES (?,?,?,'pending',?,?)`, request.ID, "fixture", string(data), request.CreatedAt, request.ExpiresAt)
	require.NoError(t, err)
	// Failed policy persistence must not leave an active/unrestricted identity.
	rt.policies.path = t.TempDir() + "/missing/policies.json"
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"digest":"fixture-digest","approve":true}`))
	out := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(out)
	c.Request = req
	c.Params = gin.Params{{Key: "id", Value: request.ID}}
	handleIdentityDecision(rt)(c)
	require.Equal(t, 500, out.Code)
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

func TestIdentityAccessChangesNeedFreshHumanApprovalAndPreserveUsage(t *testing.T) {
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
	var pending struct {
		Data identityRequest `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &pending))
	allowed, _, _ := rt.policies.preview(owner.ID, "p", "readysearch", "search", "")
	require.False(t, allowed)
	status, _ = setupHTTP(t, server, other, "GET", "/agent/access-requests/"+pending.Data.ID, nil)
	require.Equal(t, 404, status)
	status, _ = setupHTTP(t, server, owner, "POST", "/local/api/identity-requests/"+pending.Data.ID+"/decision", map[string]any{"digest": pending.Data.Digest, "approve": true})
	require.Equal(t, 403, status)
	status, raw = setupHTTP(t, server, localToken{}, "POST", "/local/api/identity-requests/"+pending.Data.ID+"/decision", map[string]any{"digest": pending.Data.Digest, "approve": true})
	require.Equal(t, 200, status, string(raw))
	allowed, _, _ = rt.policies.preview(owner.ID, "p", "readysearch", "search", "")
	require.True(t, allowed)
	require.Equal(t, 1, rt.policies.usage[owner.ID].DayCount)
	// A separately edited limit invalidates pending authorization instead of
	// letting an old approval silently restore a wider scope or higher limit.
	requested.DailyRequestLimit = 10
	status, raw = setupHTTP(t, server, owner, "POST", "/agent/access-requests", map[string]any{"policy": requested})
	require.Equal(t, 200, status)
	require.NoError(t, json.Unmarshal(raw, &pending))
	policy := rt.policies.policies[owner.ID]
	policy.DailyRequestLimit = 2
	rt.policies.policies[owner.ID] = policy
	status, _ = setupHTTP(t, server, localToken{}, "POST", "/local/api/identity-requests/"+pending.Data.ID+"/decision", map[string]any{"digest": pending.Data.Digest, "approve": true})
	require.Equal(t, 409, status)
	require.Equal(t, 2, rt.policies.policies[owner.ID].DailyRequestLimit)
	// All human console APIs reject service credentials, even in loopback
	// password-free mode. The dedicated request endpoint grants no authority.
	status, _ = setupHTTP(t, server, owner, "POST", "/local/api/tokens", map[string]any{})
	require.Equal(t, 403, status)
}
