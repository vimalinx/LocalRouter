package main

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"net/http"
	"strings"
	"testing"
)

func TestAgentIdentityEntryAndCompactTemplateDiscovery(t *testing.T) {
	rt, owner, _, _, server := serviceWorkspaceFixture(t)
	status, raw := setupHTTP(t, server, owner, http.MethodGet, "/agent/whoami", nil)
	require.Equal(t, 200, status)
	require.Contains(t, string(raw), `"identity_ready":true`)
	owner.AgentCode = "localrouter-system"
	_, err := rt.workspace.prepare(owner, serviceProposalInput{Kind: "bundle", Reason: "should require independent identity", Bundle: &serviceBundle{ID: "empty", Name: "Empty"}})
	require.ErrorContains(t, err, "agent_identity_required")
	compact, err := rt.workspace.callSetupTool(owner, "localrouter_setup_templates", json.RawMessage(`{}`), false)
	require.NoError(t, err)
	encoded, _ := json.Marshal(compact)
	require.NotContains(t, string(encoded), `"example"`)
	exact, err := rt.workspace.callSetupTool(owner, "localrouter_setup_templates", json.RawMessage(`{"id":"read-api","version":"1"}`), false)
	require.NoError(t, err)
	encoded, _ = json.Marshal(exact)
	require.Contains(t, string(encoded), `"example"`)
	status, raw = setupHTTP(t, server, owner, http.MethodGet, "/docs/agent-start.md", nil)
	require.Equal(t, 200, status)
	require.Contains(t, string(raw), "lr init")
}

func TestPublishedLegacyGuidanceUsesFileLocatorWithoutChangingOtherExamples(t *testing.T) {
	raw := "intro\n```sh\ncurl -H \"Authorization: Bearer $LOCALROUTER_API_TOKEN\" example.invalid\n```\nkeep this paragraph\n```sh\nLOCALROUTER_API_TOKEN_FILE=/private/service lr init\n```\n"
	got := currentPublishedGuide(raw)
	require.NotContains(t, got, "curl -H")
	require.Contains(t, got, "keep this paragraph")
	require.Contains(t, got, "LOCALROUTER_API_TOKEN_FILE=/private/service lr init")
	require.NotRegexp(t, legacyTokenValueExample, got)
	route := protocolRoute{OperationID: "crawl.status", Path: "/jobs/{jobId}", Methods: []string{http.MethodGet}, QueryParameters: []string{"format"}}
	call := publishProtocolRoute("fixture", "/p/fixture", []protocolRoute{route}, route).Call.CLI
	require.True(t, strings.HasPrefix(call, "lr call fixture crawl.status '{}'"))
	require.Contains(t, call, `'{"jobId":"actual-jobId"}'`)
	require.Contains(t, call, `'{"format":"actual-format"}'`)
}
