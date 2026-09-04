package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
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

func TestProtocolTemplateProxyAndSanitizedDocs(t *testing.T) {
	t.Parallel()

	var sawAuthorization string
	var sawClientAuthorization string
	var sawPath string
	var sawMode string
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		sawAuthorization = request.Header.Get("Authorization")
		sawClientAuthorization = request.Header.Get("X-Client-Authorization")
		sawPath = request.URL.Path
		sawMode = request.URL.Query().Get("mode")
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		if request.URL.Path == "/chat/completions" {
			_, _ = writer.Write([]byte(`{"ok":true,"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":4}}}`))
			return
		}
		_, _ = writer.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	protocolDir := t.TempDir()
	dataDir := t.TempDir()
	writeProtocolSecret(t, dataDir, "search-secret", "upstream-secret")
	writeProtocolDefinition(t, protocolDir, protocolDefinition{
		SchemaVersion: protocolSchemaVersion,
		ID:            "searchx",
		Name:          "Search X",
		Description:   "A search protocol",
		Enabled:       true,
		BaseURL:       upstream.URL,
		Auth:          protocolAuth{Type: "bearer", SecretFile: "protocol-secrets/search-secret"},
		Routes: []protocolRoute{
			{OperationID: "search", Methods: []string{http.MethodPost}, Path: "/search", Summary: "Search"},
			{OperationID: "models", Capabilities: []string{"ai.models"}, Methods: []string{http.MethodGet}, Path: "/models", Summary: "List models"},
			{OperationID: "chat.completions", Methods: []string{http.MethodPost}, Path: "/chat/completions", Summary: "Chat completions", RequestBody: json.RawMessage(`{"model":"stale-example","messages":[]}`)},
		},
		Pricing: &protocolPricing{Entries: []protocolPricingEntry{
			{ID: "chat.completions", Scope: "operation", Amount: analyticsPrice(1), Currency: "USD", Unit: "per-100-requests", Status: "confirmed", SourceURL: "https://example.com/pricing", SourceType: "official-docs", CheckedAt: "2026-09-04"},
			{ID: "fixture-model-alpha", Scope: "model", Amount: analyticsPrice(2), Currency: "USD", Unit: "per-1m-input-tokens", Status: "confirmed", SourceURL: "https://example.com/pricing", SourceType: "official-docs", CheckedAt: "2026-09-04"},
		}},
	})
	writeProtocolGuide(t, protocolDir, "searchx", "quickstart", `---
id: quickstart
title: Search X quickstart
summary: Agent-authored search instructions.
status: observed
operations:
  - search
visibility: local
---
# Search safely

Use the generated operation contract before calling search.
`)
	registry, err := newProtocolRegistry(protocolDir, dataDir)
	require.NoError(t, err)
	events, err := newProtocolEventStore(t.TempDir())
	require.NoError(t, err)
	registry.events = events

	engine := gin.New()
	registerProtocolRoutes(engine, localRuntime{apiToken: "sk-local", protocols: registry})

	unauthorized := httptest.NewRecorder()
	engine.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/p/searchx/search", strings.NewReader(`{}`)))
	assert.Equal(t, http.StatusUnauthorized, unauthorized.Code)

	request := httptest.NewRequest(http.MethodPost, "/p/searchx/search?mode=fresh", strings.NewReader(`{"query":"hello"}`))
	request.Header.Set("Authorization", "Bearer sk-local")
	request.Header.Set("X-Client-Authorization", "must-not-forward")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	assert.Equal(t, http.StatusCreated, response.Code)
	assert.JSONEq(t, `{"ok":true}`, response.Body.String())
	assert.Equal(t, "Bearer upstream-secret", sawAuthorization)
	assert.Empty(t, sawClientAuthorization)
	assert.Equal(t, "/search", sawPath)
	assert.Equal(t, "fresh", sawMode)

	// A dotted semantic operation ID must never become the HTTP path. The
	// runtime publishes and accepts the slash path from call_url.
	dottedRequest := httptest.NewRequest(http.MethodPost, "/p/searchx/chat/completions", strings.NewReader(`{"model":"fixture-model-alpha","messages":[]}`))
	dottedRequest.Header.Set("Authorization", "Bearer sk-local")
	dottedResponse := httptest.NewRecorder()
	engine.ServeHTTP(dottedResponse, dottedRequest)
	assert.Equal(t, http.StatusCreated, dottedResponse.Code)
	assert.Equal(t, "/chat/completions", sawPath)
	recorded, err := events.read(10)
	require.NoError(t, err)
	require.NotEmpty(t, recorded)
	chatEvent := recorded[0]
	assert.Equal(t, "fixture-model-alpha", chatEvent.Model)
	require.NotNil(t, chatEvent.Usage)
	assert.Equal(t, int64(12), chatEvent.Usage.InputTokens)
	assert.Equal(t, int64(3), chatEvent.Usage.OutputTokens)
	assert.Equal(t, int64(4), chatEvent.Usage.CacheReadInputTokens)
	require.NotNil(t, chatEvent.Cost)
	assert.InDelta(t, 0.010024, chatEvent.Cost.AmountUSD, 1e-12)

	wrongDottedPath := httptest.NewRequest(http.MethodPost, "/p/searchx/chat.completions", strings.NewReader(`{}`))
	wrongDottedPath.Header.Set("Authorization", "Bearer sk-local")
	wrongDottedResponse := httptest.NewRecorder()
	engine.ServeHTTP(wrongDottedResponse, wrongDottedPath)
	assert.Equal(t, http.StatusMethodNotAllowed, wrongDottedResponse.Code)
	assert.Contains(t, wrongDottedResponse.Body.String(), `"code":"route_not_allowed"`)
	assert.Contains(t, wrongDottedResponse.Body.String(), `operation_id is a semantic selector, not a URL`)
	assert.Contains(t, wrongDottedResponse.Body.String(), `"next_action"`)

	forbidden := httptest.NewRecorder()
	forbiddenRequest := httptest.NewRequest(http.MethodGet, "/p/searchx/private", nil)
	forbiddenRequest.Header.Set("Authorization", "Bearer sk-local")
	engine.ServeHTTP(forbidden, forbiddenRequest)
	assert.Equal(t, http.StatusMethodNotAllowed, forbidden.Code)
	assert.Contains(t, forbidden.Body.String(), `"code":"route_not_allowed"`)

	docs := httptest.NewRecorder()
	engine.ServeHTTP(docs, httptest.NewRequest(http.MethodGet, "/docs/protocols/searchx", nil))
	assert.Equal(t, http.StatusOK, docs.Code)
	assert.Contains(t, docs.Body.String(), `"mount":"/p/searchx"`)
	assert.NotContains(t, docs.Body.String(), upstream.URL)
	assert.NotContains(t, docs.Body.String(), "secret_file")
	assert.NotContains(t, docs.Body.String(), "upstream-secret")

	discovery := httptest.NewRecorder()
	engine.ServeHTTP(discovery, httptest.NewRequest(http.MethodGet, "/.well-known/localrouter.json", nil))
	assert.Equal(t, http.StatusOK, discovery.Code)
	assert.Contains(t, discovery.Body.String(), `"openapi":"/docs/openapi.json"`)
	assert.Contains(t, discovery.Body.String(), `"console":"/#control"`)
	assert.Contains(t, discovery.Body.String(), `"drafts":"/local/api/protocol-drafts"`)
	assert.Contains(t, discovery.Body.String(), `"review-impact"`)
	assert.Contains(t, discovery.Body.String(), `"markdown":"/docs/packs/searchx/guide.md"`)
	assert.Contains(t, discovery.Body.String(), `"operation_key":"searchx.chat.completions"`)
	assert.Contains(t, discovery.Body.String(), `"operation_id":"chat.completions"`)
	assert.Contains(t, discovery.Body.String(), `"operation_id_role":"semantic-selector"`)
	assert.Contains(t, discovery.Body.String(), `"operation_id_is_url":false`)
	assert.Contains(t, discovery.Body.String(), `"call_url":"/p/searchx/chat/completions"`)
	assert.Contains(t, discovery.Body.String(), `"request_example_role":"illustrative-shape"`)
	assert.Contains(t, discovery.Body.String(), `"source_operation_key":"searchx.models"`)
	assert.Contains(t, discovery.Body.String(), `"source_call_url":"/p/searchx/models"`)
	assert.NotContains(t, discovery.Body.String(), upstream.URL)
	assert.NotContains(t, discovery.Body.String(), "upstream-secret")

	openapi := httptest.NewRecorder()
	engine.ServeHTTP(openapi, httptest.NewRequest(http.MethodGet, "/docs/openapi.json", nil))
	assert.Equal(t, http.StatusOK, openapi.Code)
	assert.Contains(t, openapi.Body.String(), `"operationId":"searchx.search"`)
	assert.Contains(t, openapi.Body.String(), `"/p/searchx/search"`)
	assert.Contains(t, openapi.Body.String(), `"x-localrouter-call-url":"/p/searchx/chat/completions"`)
	assert.Contains(t, openapi.Body.String(), `"x-localrouter-operation-id-is-url":false`)
	assert.Contains(t, openapi.Body.String(), `"x-localrouter-request-example-role":"illustrative-shape"`)
	assert.Contains(t, openapi.Body.String(), `"source_operation_key":"searchx.models"`)

	markdown := httptest.NewRecorder()
	engine.ServeHTTP(markdown, httptest.NewRequest(http.MethodGet, "/docs/packs/searchx/guide.md", nil))
	assert.Equal(t, http.StatusOK, markdown.Code)
	assert.Equal(t, "text/markdown; charset=utf-8", markdown.Header().Get("Content-Type"))
	assert.Contains(t, markdown.Body.String(), "# Search X")
	assert.Contains(t, markdown.Body.String(), "# Search safely")
	assert.Contains(t, markdown.Body.String(), "operation_id` 是给 Agent API")
	assert.Contains(t, markdown.Body.String(), "POST /p/searchx/chat/completions")
	assert.Contains(t, markdown.Body.String(), "request_example` 只说明请求形状")
	assert.NotContains(t, markdown.Body.String(), "/p/searchx/chat.completions")
	assert.NotContains(t, markdown.Body.String(), "secret_file")

	examples := httptest.NewRecorder()
	engine.ServeHTTP(examples, httptest.NewRequest(http.MethodGet, "/docs/packs/searchx/examples.json", nil))
	assert.Equal(t, http.StatusOK, examples.Code)
	assert.Contains(t, examples.Body.String(), `"operation_key":"searchx.chat.completions"`)
	assert.Contains(t, examples.Body.String(), `"call_url":"/p/searchx/chat/completions"`)
	assert.Contains(t, examples.Body.String(), `"operation_id_is_url":false`)
	assert.Contains(t, examples.Body.String(), `"request_example_role":"illustrative-shape"`)
	assert.Contains(t, examples.Body.String(), `"source_operation_key":"searchx.models"`)

	guide := httptest.NewRecorder()
	engine.ServeHTTP(guide, httptest.NewRequest(http.MethodGet, "/docs/packs/searchx/guides/quickstart.md", nil))
	assert.Equal(t, http.StatusOK, guide.Code)
	assert.Contains(t, guide.Body.String(), "# Search safely")
	assert.NotContains(t, guide.Body.String(), "id: quickstart")

	packPage := httptest.NewRecorder()
	engine.ServeHTTP(packPage, httptest.NewRequest(http.MethodGet, "/docs/packs/searchx", nil))
	assert.Equal(t, http.StatusOK, packPage.Code)
	assert.Contains(t, packPage.Body.String(), "Search X quickstart")
	assert.Contains(t, packPage.Body.String(), "<h1>Search safely</h1>")

	docAlias := httptest.NewRecorder()
	engine.ServeHTTP(docAlias, httptest.NewRequest(http.MethodGet, "/doc", nil))
	assert.Equal(t, http.StatusPermanentRedirect, docAlias.Code)
	assert.Equal(t, "/docs", docAlias.Header().Get("Location"))
}

func TestPublishProtocolRouteLinksMediaModelClassToCatalog(t *testing.T) {
	routes := []protocolRoute{
		{
			OperationID:  "models.list",
			Capabilities: []string{"media.models"},
			Methods:      []string{http.MethodGet},
			Path:         "/models",
			Summary:      "List media models",
		},
		{
			OperationID:  "tasks.create",
			Capabilities: []string{"media.generate"},
			Methods:      []string{http.MethodPost},
			Path:         "/tasks",
			Summary:      "Create media task",
			RequestBody:  json.RawMessage(`{"task":"t2v","model_cls":"fixture-video"}`),
		},
	}

	published := publishProtocolRoute("media", "/p/media", routes, routes[1])
	require.Contains(t, published.DynamicInputs, "model_cls")
	assert.Equal(t, "media.models.list", published.DynamicInputs["model_cls"].SourceOperationKey)
	assert.Equal(t, "/p/media/models", published.DynamicInputs["model_cls"].SourceCallURL)
	assert.Equal(t, "models[].model_cls", published.DynamicInputs["model_cls"].Extract)
}

func TestPublishProtocolRouteUsesExplicitModelEnumWithoutCatalog(t *testing.T) {
	route := protocolRoute{
		OperationID:  "video.create",
		Capabilities: []string{"video.generate"},
		Methods:      []string{http.MethodPost},
		Path:         "/video",
		Summary:      "Create video",
		RequestBody:  json.RawMessage(`{"model":"v6","prompt":"fixture"}`),
		RequestSchema: json.RawMessage(`{
			"type":"object",
			"required":["model","prompt"],
			"properties":{"model":{"type":"string","enum":["v6"]},"prompt":{"type":"string"}}
		}`),
	}

	published := publishProtocolRoute("video", "/p/video", []protocolRoute{route}, route)
	require.Contains(t, published.DynamicInputs, "model")
	assert.Equal(t, "request-schema-enum", published.DynamicInputs["model"].SourceKind)
	assert.Equal(t, []string{"v6"}, published.DynamicInputs["model"].AllowedValues)
	assert.Equal(t, "request_schema.properties.model.enum[]", published.DynamicInputs["model"].Extract)
	assert.Empty(t, published.DynamicInputs["model"].SourceOperationKey)
}

func TestProtocolActivationPersistsWithoutChangingPackDefinition(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	protocolDir := t.TempDir()
	dataDir := t.TempDir()
	writeProtocolDefinition(t, protocolDir, protocolDefinition{
		SchemaVersion: protocolSchemaVersion,
		ID:            "switchable",
		Name:          "Switchable",
		Description:   "Runtime activation test",
		Enabled:       true,
		BaseURL:       upstream.URL,
		Auth:          protocolAuth{Type: "none"},
		Routes:        []protocolRoute{{OperationID: "run", Methods: []string{http.MethodPost}, Path: "/run", Summary: "Run"}},
	})

	registry, err := newProtocolRegistry(protocolDir, dataDir)
	require.NoError(t, err)
	require.NoError(t, registry.setActivation("switchable", "run", false))
	view, ok := registry.viewFor("switchable")
	require.True(t, ok)
	assert.True(t, view.Enabled)
	require.Len(t, view.PublicRoutes, 1)
	assert.False(t, view.PublicRoutes[0].Enabled)
	descriptor, found := registry.describeOperation("switchable", "run")
	require.True(t, found)
	assert.False(t, descriptor.Ready)
	assert.Equal(t, "disabled", descriptor.Status)

	reloaded, err := newProtocolRegistry(protocolDir, dataDir)
	require.NoError(t, err)
	view, ok = reloaded.viewFor("switchable")
	require.True(t, ok)
	assert.False(t, view.PublicRoutes[0].Enabled)
	require.NoError(t, reloaded.setActivation("switchable", "", false))
	view, _ = reloaded.viewFor("switchable")
	assert.False(t, view.Enabled)
	assert.False(t, view.Ready)
	assert.Equal(t, "disabled", view.Status)

	data, err := os.ReadFile(filepath.Join(dataDir, "service-controls.json"))
	require.NoError(t, err)
	assert.Contains(t, string(data), `"switchable": false`)
}

func TestProtocolTemplateStreamsSSE(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		flusher := writer.(http.Flusher)
		_, _ = fmt.Fprint(writer, "data: first\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(writer, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	protocolDir := t.TempDir()
	dataDir := t.TempDir()
	writeProtocolDefinition(t, protocolDir, protocolDefinition{
		SchemaVersion: protocolSchemaVersion,
		ID:            "events",
		Name:          "Events",
		Description:   "Streaming events",
		Enabled:       true,
		BaseURL:       upstream.URL,
		Auth:          protocolAuth{Type: "none"},
		Routes: []protocolRoute{{
			Methods: []string{http.MethodPost}, Path: "/answer", Summary: "Answer", Streaming: true,
		}},
	})
	registry, err := newProtocolRegistry(protocolDir, dataDir)
	require.NoError(t, err)
	engine := gin.New()
	registerProtocolRoutes(engine, localRuntime{apiToken: "sk-local", protocols: registry})

	request := httptest.NewRequest(http.MethodPost, "/p/events/answer", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer sk-local")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "text/event-stream", response.Header().Get("Content-Type"))
	scanner := bufio.NewScanner(strings.NewReader(response.Body.String()))
	var lines []string
	for scanner.Scan() {
		if scanner.Text() != "" {
			lines = append(lines, scanner.Text())
		}
	}
	assert.Equal(t, []string{"data: first", "data: [DONE]"}, lines)
}

func TestProtocolReloadIsAtomicAndSecretsStayInsideDataDir(t *testing.T) {
	t.Parallel()
	protocolDir := t.TempDir()
	dataDir := t.TempDir()
	definition := protocolDefinition{
		SchemaVersion: protocolSchemaVersion,
		ID:            "stable",
		Name:          "Stable",
		Description:   "Stable contract",
		Enabled:       true,
		BaseURL:       "https://example.com",
		Auth:          protocolAuth{Type: "none"},
		Routes: []protocolRoute{{
			Methods: []string{http.MethodGet}, Path: "/health", Summary: "Health",
		}},
	}
	writeProtocolDefinition(t, protocolDir, definition)
	writeProtocolGuide(t, protocolDir, "stable", "operations", `---
id: operations
title: Stable operations
summary: A valid guide bound to a real operation.
status: draft
operations:
  - health
---
# Stable

This guide is valid.
`)
	registry, err := newProtocolRegistry(protocolDir, dataDir)
	require.NoError(t, err)

	writeProtocolGuide(t, protocolDir, "stable", "operations", `---
id: operations
title: Broken operations
summary: This update references an unknown operation.
status: draft
operations:
  - missing
---
# Broken

This guide must never replace the live version.
`)
	require.Error(t, registry.reload())
	_, stillPresent := registry.get("stable")
	assert.True(t, stillPresent)
	guides, ok := registry.guidesFor("stable")
	require.True(t, ok)
	require.Len(t, guides, 1)
	assert.Equal(t, "Stable operations", guides[0].Title)
}

func TestProtocolPathPatterns(t *testing.T) {
	t.Parallel()
	_, matched := matchProtocolPath("/agent/runs/{id}", "/agent/runs/run-1")
	assert.True(t, matched)
	_, matched = matchProtocolPath("/agent/runs/{id}", "/agent/runs/run-1/events")
	assert.False(t, matched)
	_, matched = matchProtocolPath("/files/*", "/files/a/b/c")
	assert.True(t, matched)
	_, matched = matchProtocolPath("/files/*", "/other/a")
	assert.False(t, matched)
	assert.True(t, unsafeProtocolPath("/safe/../secret"))
	assert.True(t, unsafeProtocolPath("/safe\\secret"))
}

func TestProtocolV2TransformsPoolRetryAffinityAndWorkflow(t *testing.T) {
	t.Parallel()

	var seenCreateBodies []string
	var seenCreateKeys []string
	var seenPollKey string
	var seenQuery string
	var seenHeader string
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		key := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
		switch request.URL.Path {
		case "/native/jobs":
			body, _ := io.ReadAll(request.Body)
			seenCreateBodies = append(seenCreateBodies, string(body))
			seenCreateKeys = append(seenCreateKeys, key)
			seenQuery = request.URL.Query().Get("mode")
			seenHeader = request.Header.Get("X-Workflow")
			writer.Header().Set("Content-Type", "application/json")
			if key == "credential-a" {
				writer.Header().Set("Retry-After", "1")
				writer.WriteHeader(http.StatusTooManyRequests)
				_, _ = writer.Write([]byte(`{"error":"rate limited"}`))
				return
			}
			_, _ = writer.Write([]byte(`{"data":{"task_id":"job-1"},"internal":"remove-me"}`))
		case "/native/jobs/job-1":
			seenPollKey = key
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"status":"done","result":{"url":"https://example.invalid/result"}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer upstream.Close()

	protocolDir := t.TempDir()
	dataDir := t.TempDir()
	writeProtocolPool(t, dataDir, "video-pool", protocolCredentialFile{
		SchemaVersion: "1",
		Credentials: []protocolCredential{
			{ID: "account-a", Secret: "credential-a", Priority: 1, Weight: 1, Balance: 10},
			{ID: "account-b", Secret: "credential-b", Priority: 1, Weight: 1, Balance: 10},
		},
	})
	writeProtocolDefinition(t, protocolDir, protocolDefinition{
		SchemaVersion: protocolSchemaVersionV3,
		ID:            "video",
		Name:          "Video Jobs",
		Description:   "Config-defined async video jobs",
		Enabled:       true,
		BaseURL:       upstream.URL,
		Auth:          protocolAuth{Type: "bearer"},
		Pool: &protocolPoolConfig{
			Mode: "local", CredentialsFile: "protocol-pools/video-pool.json", Strategy: "round-robin",
			MaxAttempts: 2, MaxInFlightPerCredential: 1, RateLimitCooldownSeconds: 1,
		},
		Routes: []protocolRoute{
			{
				OperationID: "jobs.create", Methods: []string{http.MethodPost}, Path: "/jobs", UpstreamPath: "/native/jobs", Summary: "Create job",
				Retry: protocolRetryConfig{Mode: "idempotent", MaxAttempts: 2, IdempotencyHeader: "Idempotency-Key"},
				RequestTransform: protocolRequestTransform{
					JSON: protocolJSONTransform{
						Rename: []protocolTransformMove{{From: "prompt", To: "input.prompt"}},
						Set:    map[string]json.RawMessage{"task_type": json.RawMessage(`"video"`)},
						Remove: []string{"private"},
					},
					Headers: map[string]string{"X-Workflow": "create-{{query.mode}}"},
					Query:   map[string]string{"mode": "{{query.mode}}"},
				},
				ResponseTransform: protocolJSONTransform{
					Rename: []protocolTransformMove{{From: "data.task_id", To: "job_id"}},
					Remove: []string{"data", "internal"},
				},
				Affinity: protocolAffinityConfig{ResponseJSONPath: "data.task_id", TTLSeconds: 3600},
			},
			{
				OperationID: "jobs.get", Methods: []string{http.MethodGet}, Path: "/jobs/{id}", UpstreamPath: "/native/jobs/{id}", Summary: "Get job",
				Affinity: protocolAffinityConfig{RequestPathParam: "id", TTLSeconds: 3600},
			},
		},
		Workflows: []protocolWorkflow{{
			ID: "video.generate", Name: "Generate video", CreateOperation: "jobs.create", PollOperation: "jobs.get",
			ResourceIDPath: "job_id", StatusPath: "status", PendingValues: []string{"pending"}, RunningValues: []string{"running"},
			SuccessValues: []string{"done"}, FailureValues: []string{"failed"}, ResultPath: "result.url", PollIntervalMS: 500, MaxPollAttempts: 20,
		}},
	})
	registry, err := newProtocolRegistry(protocolDir, dataDir)
	require.NoError(t, err)
	engine := gin.New()
	registerProtocolRoutes(engine, localRuntime{apiToken: "sk-local", protocols: registry})

	create := httptest.NewRequest(http.MethodPost, "/p/video/jobs?mode=fast", strings.NewReader(`{"prompt":"orbiting camera","private":"drop"}`))
	create.Header.Set("Authorization", "Bearer sk-local")
	create.Header.Set("Content-Type", "application/json")
	created := httptest.NewRecorder()
	engine.ServeHTTP(created, create)
	require.Equal(t, http.StatusOK, created.Code)
	assert.JSONEq(t, `{"job_id":"job-1"}`, created.Body.String())
	require.Equal(t, []string{"credential-a", "credential-b"}, seenCreateKeys)
	require.Len(t, seenCreateBodies, 2)
	assert.JSONEq(t, `{"input":{"prompt":"orbiting camera"},"task_type":"video"}`, seenCreateBodies[1])
	assert.Equal(t, "fast", seenQuery)
	assert.Equal(t, "create-fast", seenHeader)

	poll := httptest.NewRequest(http.MethodGet, "/p/video/jobs/job-1", nil)
	poll.Header.Set("Authorization", "Bearer sk-local")
	polled := httptest.NewRecorder()
	engine.ServeHTTP(polled, poll)
	require.Equal(t, http.StatusOK, polled.Code)
	assert.Equal(t, "credential-b", seenPollKey)
	assert.Contains(t, polled.Body.String(), `"status":"done"`)

	manifest := httptest.NewRecorder()
	engine.ServeHTTP(manifest, httptest.NewRequest(http.MethodGet, "/docs/packs/video/manifest.json", nil))
	require.Equal(t, http.StatusOK, manifest.Code)
	assert.Contains(t, manifest.Body.String(), `"mode":"local"`)
	assert.Contains(t, manifest.Body.String(), `"video.generate"`)
	assert.NotContains(t, manifest.Body.String(), "credential-a")
	assert.NotContains(t, manifest.Body.String(), "credentials_file")

	stateData, err := os.ReadFile(filepath.Join(dataDir, "protocol-state", "video.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(stateData), "credential-a")
	assert.NotContains(t, string(stateData), "credential-b")
	assert.Contains(t, string(stateData), "job-1")
}

func TestExternalReadonlyPoolSourceMappingAndProtection(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	sourceDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "source.json")
	future := float64(time.Now().Add(2 * time.Hour).Unix())
	cookiesA, err := json.Marshal([]map[string]any{{"name": "session_cookie", "value": "credential-a", "expires": future}})
	require.NoError(t, err)
	cookiesB, err := json.Marshal([]map[string]any{{"name": "session_cookie", "value": "credential-b", "expires": future}})
	require.NoError(t, err)
	sourceRecords := []map[string]any{
		{"email": "first@example.invalid", "cookies": string(cookiesA), "ok": true, "disabled": false, "capability": "video"},
		{"email": "second@example.invalid", "cookies": string(cookiesB), "ok": true, "disabled": true, "capability": "text"},
	}
	sourceData, err := json.Marshal(sourceRecords)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(sourcePath, sourceData, 0o600))

	locatorDir := filepath.Join(dataDir, "protocol-pool-sources")
	require.NoError(t, os.MkdirAll(locatorDir, 0o700))
	locatorPath := filepath.Join(locatorDir, "mapped.json")
	locatorData, err := json.Marshal(protocolPoolSourceLocator{SchemaVersion: "1", Path: sourcePath})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(locatorPath, locatorData, 0o600))

	registry := &protocolRegistry{dataDir: dataDir, stateDir: t.TempDir()}
	definition := protocolDefinition{
		SchemaVersion: protocolSchemaVersionV3,
		ID:            "mapped",
		Name:          "Mapped pool",
		Description:   "External read-only source",
		Enabled:       true,
		Auth:          protocolAuth{Type: "cookie", Header: "Cookie", ValueTemplate: "session_cookie={{secret}}"},
		Pool: &protocolPoolConfig{
			Mode: "local", Strategy: "least-inflight", MaxAttempts: 2,
			Source: &protocolPoolSourceConfig{
				Format: "json", LocatorFile: "protocol-pool-sources/mapped.json", IDPath: "email", SecretPath: "cookies",
				SecretCodec: "cookie-list-json", SecretSelector: "session_cookie", DisabledPath: "disabled", EligiblePath: "ok",
				MetadataPaths: map[string]string{"capability": "capability"},
			},
		},
	}
	require.NoError(t, registry.validatePool(&definition))
	credentials, err := registry.loadPoolCredentials(definition)
	require.NoError(t, err)
	require.Len(t, credentials, 2)
	assert.Equal(t, "credential-a", credentials[0].Secret)
	assert.NotContains(t, credentials[0].ID, "first")
	assert.True(t, strings.HasPrefix(credentials[0].ID, "src-"))
	assert.False(t, credentials[0].Disabled)
	assert.Equal(t, "video", credentials[0].Metadata["capability"])
	assert.True(t, credentials[1].Disabled)

	definition.Pool.Source.SecretPath = "password"
	definition.Pool.Source.SecretCodec = "json-fields"
	definition.Pool.Source.SecretSelector = ""
	definition.Pool.Source.SecretFields = map[string]string{"email": "email", "password": "cookies"}
	credentials, err = registry.loadPoolCredentials(definition)
	require.NoError(t, err)
	var composite map[string]string
	require.NoError(t, json.Unmarshal([]byte(credentials[0].Secret), &composite))
	assert.Equal(t, "first@example.invalid", composite["email"])
	assert.Equal(t, string(cookiesA), composite["password"])
	assert.Len(t, composite, 2)
	ready, status := registry.readiness(definition)
	assert.True(t, ready)
	assert.Equal(t, "ready", status)
	view := registry.poolView(definition)
	assert.Equal(t, "external-readonly", view.Source)
	assert.Equal(t, 2, view.Total)
	assert.Equal(t, 1, view.Eligible)
	viewJSON, err := json.Marshal(view)
	require.NoError(t, err)
	assert.NotContains(t, string(viewJSON), "example.invalid")
	assert.NotContains(t, string(viewJSON), "credential-a")
	assert.NotContains(t, string(viewJSON), sourcePath)

	require.NoError(t, os.Chmod(sourcePath, 0o644))
	_, err = registry.loadPoolCredentials(definition)
	assert.ErrorContains(t, err, "private regular file")
}

func TestAdminPoolRuntimeViewIsUsefulAndSecretSafe(t *testing.T) {
	t.Parallel()
	protocolDir := t.TempDir()
	dataDir := t.TempDir()
	writeProtocolPool(t, dataDir, "safe-pool", protocolCredentialFile{
		SchemaVersion: "1",
		Credentials: []protocolCredential{
			{ID: "operator-account-01", Secret: "never-show-cookie", Balance: 10, Metadata: map[string]string{"email": "private@example.invalid"}},
			{ID: "disabled-account", Secret: "never-show-token", Disabled: true, Balance: 10},
		},
	})
	writeProtocolDefinition(t, protocolDir, protocolDefinition{
		SchemaVersion: protocolSchemaVersionV3, ID: "safe", Name: "Safe", Description: "Safe pool", Enabled: true,
		BaseURL: "https://example.invalid", Auth: protocolAuth{Type: "bearer"},
		Pool:   &protocolPoolConfig{Mode: "local", CredentialsFile: "protocol-pools/safe-pool.json", Strategy: "least-inflight", MinBalance: 1, MaxInFlightPerCredential: 2, InFlightLeaseSeconds: 60},
		Routes: []protocolRoute{{OperationID: "safe.call", Methods: []string{http.MethodPost}, Path: "/call", Summary: "Call"}},
	})
	registry, err := newProtocolRegistry(protocolDir, dataDir)
	require.NoError(t, err)

	adminJSON, err := json.Marshal(registry.adminViews())
	require.NoError(t, err)
	adminBody := string(adminJSON)
	assert.Contains(t, adminBody, `"pool_runtime"`)
	assert.Contains(t, adminBody, `"label":"账号 01 ·`)
	assert.Contains(t, adminBody, `"ready":1`)
	assert.Contains(t, adminBody, `"disabled":1`)
	assert.Contains(t, adminBody, `"balance_tracked":true`)
	assert.Contains(t, adminBody, `"balance_remaining":20`)
	assert.Contains(t, adminBody, `"balance_empty":0`)
	assert.Contains(t, adminBody, `"balance":10`)
	assert.NotContains(t, adminBody, "never-show-cookie")
	assert.NotContains(t, adminBody, "never-show-token")
	assert.NotContains(t, adminBody, "operator-account-01")
	assert.NotContains(t, adminBody, "private@example.invalid")
	assert.NotContains(t, adminBody, "safe-pool.json")

	publicJSON, err := json.Marshal(registry.views())
	require.NoError(t, err)
	assert.NotContains(t, string(publicJSON), `"pool_runtime"`)
}

func TestProtocolPackLifecyclePlanDriftApplyHistoryAndRollback(t *testing.T) {
	t.Parallel()

	protocolDir := t.TempDir()
	dataDir := t.TempDir()
	initial := protocolDefinition{
		SchemaVersion: protocolSchemaVersion,
		ID:            "stable",
		Name:          "Stable Pack",
		Description:   "Initial contract",
		Enabled:       true,
		BaseURL:       "https://example.com",
		Auth:          protocolAuth{Type: "none"},
		Routes:        []protocolRoute{{OperationID: "health", Methods: []string{http.MethodGet}, Path: "/health", Summary: "Initial health"}},
	}
	writeProtocolDefinition(t, protocolDir, initial)
	registry, err := newProtocolRegistry(protocolDir, dataDir)
	require.NoError(t, err)
	initialDigest := registry.liveDigest
	require.True(t, validProtocolDigest(initialDigest))

	updated := initial
	updated.Description = "Updated contract"
	updated.Routes[0].Summary = "Updated health"
	writeProtocolDefinition(t, protocolDir, updated)

	planRecorder := httptest.NewRecorder()
	planContext, _ := gin.CreateTestContext(planRecorder)
	planContext.Request = httptest.NewRequest(http.MethodPost, "/local/api/protocols/plan", nil)
	registry.handleAdminPlan(planContext)
	require.Equal(t, http.StatusOK, planRecorder.Code)
	var planResponse struct {
		Data protocolPackPlan `json:"data"`
	}
	require.NoError(t, json.Unmarshal(planRecorder.Body.Bytes(), &planResponse))
	require.NotEqual(t, initialDigest, planResponse.Data.Digest)

	drifted := updated
	drifted.Description = "Drifted after review"
	writeProtocolDefinition(t, protocolDir, drifted)
	driftApply := httptest.NewRecorder()
	driftContext, _ := gin.CreateTestContext(driftApply)
	driftContext.Request = httptest.NewRequest(http.MethodPost, "/local/api/protocols/apply", strings.NewReader(`{"digest":"`+planResponse.Data.Digest+`"}`))
	registry.handleAdminApply(driftContext)
	assert.Equal(t, http.StatusConflict, driftApply.Code)
	view, ok := registry.viewFor("stable")
	require.True(t, ok)
	assert.Equal(t, "Initial contract", view.Description)

	writeProtocolDefinition(t, protocolDir, updated)
	planRecorder = httptest.NewRecorder()
	planContext, _ = gin.CreateTestContext(planRecorder)
	planContext.Request = httptest.NewRequest(http.MethodPost, "/local/api/protocols/plan", nil)
	registry.handleAdminPlan(planContext)
	require.NoError(t, json.Unmarshal(planRecorder.Body.Bytes(), &planResponse))
	updatedDigest := planResponse.Data.Digest

	applyRecorder := httptest.NewRecorder()
	applyContext, _ := gin.CreateTestContext(applyRecorder)
	applyContext.Request = httptest.NewRequest(http.MethodPost, "/local/api/protocols/apply", strings.NewReader(`{"digest":"`+updatedDigest+`"}`))
	registry.handleAdminApply(applyContext)
	require.Equal(t, http.StatusOK, applyRecorder.Code)
	view, ok = registry.viewFor("stable")
	require.True(t, ok)
	assert.Equal(t, "Updated contract", view.Description)
	assert.Equal(t, updatedDigest, registry.liveDigest)

	historyRecorder := httptest.NewRecorder()
	historyContext, _ := gin.CreateTestContext(historyRecorder)
	historyContext.Request = httptest.NewRequest(http.MethodGet, "/local/api/protocols/history", nil)
	registry.handleAdminHistory(historyContext)
	require.Equal(t, http.StatusOK, historyRecorder.Code)
	assert.Contains(t, historyRecorder.Body.String(), initialDigest)
	assert.Contains(t, historyRecorder.Body.String(), updatedDigest)

	rollbackRecorder := httptest.NewRecorder()
	rollbackContext, _ := gin.CreateTestContext(rollbackRecorder)
	rollbackContext.Request = httptest.NewRequest(http.MethodPost, "/local/api/protocols/rollback", strings.NewReader(`{"digest":"`+initialDigest+`"}`))
	registry.handleAdminRollback(rollbackContext)
	require.Equal(t, http.StatusOK, rollbackRecorder.Code, rollbackRecorder.Body.String())
	view, ok = registry.viewFor("stable")
	require.True(t, ok)
	assert.Equal(t, "Initial contract", view.Description)
	assert.Equal(t, initialDigest, registry.liveDigest)
	restoredData, err := os.ReadFile(filepath.Join(protocolDir, "stable.json"))
	require.NoError(t, err)
	assert.Contains(t, string(restoredData), "Initial contract")
}

func writeProtocolDefinition(t *testing.T, dir string, definition protocolDefinition) {
	t.Helper()
	data, err := json.MarshalIndent(definition, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, definition.ID+".json"), data, 0o644))
}

func writeProtocolSecret(t *testing.T, dataDir, name, value string) {
	t.Helper()
	dir := filepath.Join(dataDir, "protocol-secrets")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(value+"\n"), 0o600))
}

func writeProtocolGuide(t *testing.T, protocolDir, protocolID, guideID, content string) {
	t.Helper()
	dir := filepath.Join(protocolDir, protocolID, "guides")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, guideID+".md"), []byte(content), 0o644))
}

func writeProtocolPool(t *testing.T, dataDir, name string, pool protocolCredentialFile) {
	t.Helper()
	dir := filepath.Join(dataDir, "protocol-pools")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	data, err := json.MarshalIndent(pool, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, name+".json"), data, 0o600))
}
