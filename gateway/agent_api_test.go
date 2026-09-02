package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func agentAPIFixture(t *testing.T) (*gin.Engine, *protocolRegistry) {
	t.Helper()
	protocolDir, dataDir := t.TempDir(), t.TempDir()
	writeProtocolDefinition(t, protocolDir, protocolDefinition{
		SchemaVersion: protocolSchemaVersionV3,
		ID:            "readysearch", Name: "Ready Search", Description: "Ready web search", Enabled: true,
		BaseURL: "https://example.com", Auth: protocolAuth{Type: "none"},
		Routes: []protocolRoute{
			{
				OperationID: "models", Capabilities: []string{"ai.models"}, Methods: []string{http.MethodGet},
				Path: "/models", Summary: "List models",
			},
			{
				OperationID: "search", Capabilities: []string{"web.search"}, Methods: []string{http.MethodPost},
				Path: "/search", Summary: "Search the web", RequestBody: json.RawMessage(`{"query":"hello"}`),
				QueryParameters: []string{"freshness"},
				RequestSchema:   json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`),
				ResponseSchema:  json.RawMessage(`{"type":"object"}`),
				Availability:    protocolRouteAvailability{Status: "verified", Level: "real-readonly", Covers: []string{"schema", "auth", "upstream", "response"}, VerifiedAt: "2026-09-01T00:00:00Z"},
			},
			{
				OperationID: "scrape", Capabilities: []string{"web.scrape"}, Methods: []string{http.MethodPost},
				Path: "/scrape", Summary: "Search and scrape web content",
			},
			{
				OperationID: "chat.completions", Capabilities: []string{"ai.chat"}, Methods: []string{http.MethodPost},
				Path: "/chat/completions", Summary: "Create chat completion", RequestBody: json.RawMessage(`{"model":"stale-example","messages":[]}`),
			},
		},
	})
	writeProtocolGuide(t, protocolDir, "readysearch", "quickstart", `---
id: quickstart
title: Search quickstart
summary: Search through the ready Pack.
status: verified
last_verified: 2026-09-01
operations:
  - search
visibility: local
---
# Search
`)
	writeProtocolDefinition(t, protocolDir, protocolDefinition{
		SchemaVersion: protocolSchemaVersionV3,
		ID:            "offsearch", Name: "Disabled Search", Description: "Unavailable web search", Enabled: false,
		BaseURL: "https://example.com", Auth: protocolAuth{Type: "none"},
		Routes: []protocolRoute{{OperationID: "search", Capabilities: []string{"web.search"}, Methods: []string{http.MethodPost}, Path: "/search", Summary: "Search fallback"}},
	})
	registry, err := newProtocolRegistry(protocolDir, dataDir)
	require.NoError(t, err)
	runtime := localRuntime{apiToken: "sk-local", protocols: registry}
	engine := gin.New()
	registerProtocolRoutes(engine, runtime)
	return engine, registry
}

func agentAPIRequest(engine http.Handler, method, path, body string, authenticated bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if authenticated {
		request.Header.Set("Authorization", "Bearer sk-local")
	}
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}

func TestAgentResolveDescribePreflightAndWhoAmI(t *testing.T) {
	engine, registry := agentAPIFixture(t)

	unauthorized := agentAPIRequest(engine, http.MethodPost, "/agent/resolve", `{"query":"web.search"}`, false)
	require.Equal(t, http.StatusUnauthorized, unauthorized.Code)
	assert.JSONEq(t, `{"success":false,"code":"service_token_required","message":"local API token required","reason":"Authorization is missing or invalid","retryable":false,"owner":"localrouter","retry_after":null,"next_action":"read the configured mode-600 service Token file and retry with Authorization: Bearer","alternatives":null}`, unauthorized.Body.String())

	resolved := agentAPIRequest(engine, http.MethodPost, "/agent/resolve", `{"query":"web.search"}`, true)
	require.Equal(t, http.StatusOK, resolved.Code, resolved.Body.String())
	var resolution struct {
		ContractDigest    string               `json:"contract_digest"`
		SelectionMode     string               `json:"selection_mode"`
		Merged            bool                 `json:"merged"`
		SelectionRequired bool                 `json:"selection_required"`
		Matches           []agentResolvedMatch `json:"matches"`
	}
	require.NoError(t, json.Unmarshal(resolved.Body.Bytes(), &resolution))
	assert.Equal(t, registry.currentDigest(), resolution.ContractDigest)
	assert.Equal(t, "agent", resolution.SelectionMode)
	assert.False(t, resolution.Merged)
	assert.True(t, resolution.SelectionRequired)
	require.Len(t, resolution.Matches, 2)
	assert.Equal(t, "readysearch.search", resolution.Matches[0].OperationKey)
	assert.Equal(t, "capability", resolution.Matches[0].MatchKind)
	assert.Equal(t, "offsearch.search", resolution.Matches[1].OperationKey)
	for _, match := range resolution.Matches {
		assert.Equal(t, "search", match.Operation, "exact capability resolution must not include text-related scrape operations")
	}

	catalog := agentAPIRequest(engine, http.MethodGet, "/agent/operations", "", true)
	require.Equal(t, http.StatusOK, catalog.Code, catalog.Body.String())
	assert.Contains(t, catalog.Body.String(), `"object":"localrouter.operation.list"`)
	assert.Contains(t, catalog.Body.String(), `"operation_key":"readysearch.scrape"`)
	assert.Contains(t, catalog.Body.String(), `"merged":false`)

	described := agentAPIRequest(engine, http.MethodGet, "/agent/operations/readysearch/search", "", true)
	require.Equal(t, http.StatusOK, described.Code, described.Body.String())
	assert.Contains(t, described.Body.String(), `"schema_source":"explicit"`)
	assert.Contains(t, described.Body.String(), `"side_effecting":true`)
	assert.Contains(t, described.Body.String(), `"web.search"`)
	assert.Contains(t, described.Body.String(), `"level":"real-readonly"`)
	assert.Contains(t, described.Body.String(), `"query_parameters":["freshness"]`)
	assert.Contains(t, described.Body.String(), `"operation_id_role":"semantic-selector"`)
	assert.Contains(t, described.Body.String(), `"operation_id_is_url":false`)
	assert.Contains(t, described.Body.String(), `"call_url":"/p/readysearch/search"`)
	assert.Contains(t, described.Body.String(), `"call":{"authenticated":true`)
	assert.Contains(t, described.Body.String(), `"url":"/p/readysearch/search"`)

	dotted := agentAPIRequest(engine, http.MethodGet, "/agent/operations/readysearch/chat.completions", "", true)
	require.Equal(t, http.StatusOK, dotted.Code, dotted.Body.String())
	assert.Contains(t, dotted.Body.String(), `"operation_id":"chat.completions"`)
	assert.Contains(t, dotted.Body.String(), `"call_url":"/p/readysearch/chat/completions"`)
	assert.NotContains(t, dotted.Body.String(), `"call_url":"/p/readysearch/chat.completions"`)
	assert.Contains(t, dotted.Body.String(), `"request_example_role":"illustrative-shape"`)
	assert.Contains(t, dotted.Body.String(), `"source_operation_key":"readysearch.models"`)
	assert.Contains(t, dotted.Body.String(), `"source_call_url":"/p/readysearch/models"`)

	compared := agentAPIRequest(engine, http.MethodPost, "/agent/compare", `{"operation_keys":["readysearch.search","offsearch.search"]}`, true)
	require.Equal(t, http.StatusOK, compared.Code, compared.Body.String())
	assert.Contains(t, compared.Body.String(), `"object":"localrouter.operation.comparison"`)
	assert.Contains(t, compared.Body.String(), `"recommendation":null`)
	assert.Contains(t, compared.Body.String(), `"merged":false`)
	assert.Contains(t, compared.Body.String(), `"operation_key":"readysearch.search"`)
	assert.Contains(t, compared.Body.String(), `"operation_key":"offsearch.search"`)

	invalid := agentAPIRequest(engine, http.MethodPost, "/agent/preflight", `{"pack":"readysearch","operation":"search","input":{}}`, true)
	require.Equal(t, http.StatusOK, invalid.Code, invalid.Body.String())
	assert.Contains(t, invalid.Body.String(), `"ok":false`)
	assert.Contains(t, invalid.Body.String(), `$.query is required`)
	assert.Contains(t, invalid.Body.String(), `"upstream_called":false`)

	valid := agentAPIRequest(engine, http.MethodPost, "/agent/preflight", `{"pack":"readysearch","operation":"search","input":{"query":"hello"},"query_parameters":{"freshness":"day"}}`, true)
	require.Equal(t, http.StatusOK, valid.Code, valid.Body.String())
	assert.Contains(t, valid.Body.String(), `"ok":true`)

	whoami := agentAPIRequest(engine, http.MethodGet, "/agent/whoami", "", true)
	require.Equal(t, http.StatusOK, whoami.Code, whoami.Body.String())
	assert.Contains(t, whoami.Body.String(), `"service_access":true`)
	assert.Contains(t, whoami.Body.String(), `"maintenance_access":false`)
	assert.NotContains(t, whoami.Body.String(), "sk-local")

	discovery := agentAPIRequest(engine, http.MethodGet, "/.well-known/localrouter.json", "", false)
	require.Equal(t, http.StatusOK, discovery.Code)
	assert.Contains(t, discovery.Body.String(), `"resolve":"/agent/resolve"`)
	assert.Contains(t, discovery.Body.String(), `"compare":"/agent/compare"`)
	assert.Contains(t, discovery.Body.String(), registry.currentDigest())

	agentDocs := agentAPIRequest(engine, http.MethodGet, "/docs/agent.json", "", false)
	require.Equal(t, http.StatusOK, agentDocs.Code)
	assert.Contains(t, agentDocs.Body.String(), `"upstream_called":false`)
	assert.Contains(t, agentDocs.Body.String(), `"merged":false`)
	assert.Contains(t, agentDocs.Body.String(), `"catalog":"lr catalog`)
	assert.Contains(t, agentDocs.Body.String(), `"find_model":"lr find model`)
	assert.Contains(t, agentDocs.Body.String(), `"find_pool":"lr find pool`)
	assert.Contains(t, agentDocs.Body.String(), `"agent_runtime"`)
	assert.Contains(t, agentDocs.Body.String(), `"localrouter_owned":false`)
	assert.Contains(t, agentDocs.Body.String(), `"watch":"lr watch`)
	assert.Contains(t, agentDocs.Body.String(), `"operation_id_is_url":false`)
	assert.Contains(t, agentDocs.Body.String(), `"call_url":"fully resolved LocalRouter HTTP path`)
	assert.Contains(t, agentDocs.Body.String(), `never derive a URL from operation_id`)
	assert.Contains(t, agentDocs.Body.String(), `"role":"illustrative-shape"`)
}

func TestAgentResolveCanRequireReadyMatches(t *testing.T) {
	engine, _ := agentAPIFixture(t)
	response := agentAPIRequest(engine, http.MethodPost, "/agent/resolve", `{"query":"web.search","require_ready":true}`, true)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"operation_key":"readysearch.search"`)
	assert.NotContains(t, response.Body.String(), `"operation_key":"offsearch.search"`)
}
