package main

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentRegistrationIsRequiredAndCodeIsUnique(t *testing.T) {
	store, err := openLocalStore(filepath.Join(t.TempDir(), "localrouter.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	root, err := store.ensureRootUser()
	require.NoError(t, err)
	runtime := localRuntime{store: store, rootUser: root}
	engine := gin.New()
	engine.POST("/tokens", handleAddToken(runtime))

	missing := performJSON(engine, http.MethodPost, "/tokens", `{"name":"missing-agent"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, missing.Code)
	assert.Contains(t, missing.Body.String(), "agent_code")
	reserved := performJSON(engine, http.MethodPost, "/tokens", `{"name":"LocalRouter default","agent_code":"build-system","agent_name":"Build Agent","workspace":"/workspace/project"}`)
	assert.Equal(t, http.StatusConflict, reserved.Code)
	unicodeCode := performJSON(engine, http.MethodPost, "/tokens", `{"name":"builder","agent_code":"构建-agent","agent_name":"Build Agent","workspace":"/workspace/project"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, unicodeCode.Code)

	document := `{"name":"builder","agent_code":"build-007","agent_name":"Build Agent","workspace":"/workspace/project","runtime":"codex","expired_time":-1}`
	created := performJSON(engine, http.MethodPost, "/tokens", document)
	require.Equal(t, http.StatusOK, created.Code, created.Body.String())
	duplicate := performJSON(engine, http.MethodPost, "/tokens", document)
	assert.Equal(t, http.StatusConflict, duplicate.Code)

	tokens, _, err := store.listTokens(root.ID, 1, 20)
	require.NoError(t, err)
	require.Len(t, tokens, 1)
	assert.Equal(t, "build-007", tokens[0].AgentCode)
	assert.Equal(t, "/workspace/project", tokens[0].Workspace)

	_, err = store.db.Exec(`INSERT INTO tokens (user_id, key, status, name, created_time, expired_time) VALUES (?, 'legacy-key', 1, 'legacy', ?, -1)`, root.ID, time.Now().Unix())
	require.NoError(t, err)
	_, err = store.validateToken("legacy-key")
	assert.ErrorContains(t, err, "Agent registration is required")
}

func TestAgentUsageAttributesRequestsCostAndDailyLimitByToken(t *testing.T) {
	rootDir := t.TempDir()
	store, err := openLocalStore(filepath.Join(rootDir, "localrouter.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	root, err := store.ensureRootUser()
	require.NoError(t, err)
	token := localToken{Name: "builder", AgentCode: "build-007", AgentName: "Build Agent", Workspace: "/workspace/project", Runtime: "codex", ExpiredTime: -1}
	id, err := store.createToken(root.ID, token)
	require.NoError(t, err)
	policyStore, err := newTokenPolicyStore(rootDir)
	require.NoError(t, err)
	policyStore.policies[int(id)] = localTokenPolicy{TokenID: int(id), RequestsPerMinute: 12, DailyRequestLimit: 100, MaxInFlight: 3}

	now := time.Now().UTC()
	require.NoError(t, store.logRequest(context.Background(), localRequestLog{
		UserID: root.ID, TokenID: int(id), TokenName: token.Name, CreatedAt: now.Unix(), Type: localLogTypeConsume,
		PromptTokens: 120, CompletionTokens: 30, Quota: 250_000,
	}))
	require.NoError(t, store.logRequest(context.Background(), localRequestLog{
		UserID: root.ID, TokenID: int(id), TokenName: token.Name, CreatedAt: now.Unix(), Type: localLogTypeError,
	}))

	usage, err := buildLocalAgentUsage(localRuntime{store: store, rootUser: root, policies: policyStore}, now)
	require.NoError(t, err)
	require.Len(t, usage, 1)
	assert.Equal(t, "build-007", usage[0].AgentCode)
	assert.Equal(t, int64(2), usage[0].Requests)
	assert.Equal(t, int64(1), usage[0].Successful)
	assert.Equal(t, int64(1), usage[0].Failed)
	assert.Equal(t, int64(2), usage[0].TodayRequests)
	assert.Equal(t, int64(150), usage[0].PromptTokens+usage[0].CompletionTokens)
	assert.InDelta(t, 0.5, usage[0].CostUSD, 1e-12)
	assert.Equal(t, "measured", usage[0].CostStatus)
	assert.Equal(t, 100, usage[0].DailyRequestLimit)
	assert.Equal(t, 12, usage[0].RequestsPerMinute)
	assert.Equal(t, 3, usage[0].MaxInFlight)
}
