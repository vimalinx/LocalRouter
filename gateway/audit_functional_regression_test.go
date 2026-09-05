package main

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestFunctionalMultipartModelRouting(t *testing.T) {
	rt := auditRuntime(t)
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	require.NoError(t, form.WriteField("model", "fixture-transcriber"))
	file, err := form.CreateFormFile("file", "fixture.wav")
	require.NoError(t, err)
	_, err = file.Write([]byte("RIFF-fixture"))
	require.NoError(t, err)
	require.NoError(t, form.Close())
	calls := 0
	rt.balancer = newLocalRelayBalancer()
	rt.relayClient = &http.Client{Transport: auditTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"text":"fixture"}`)), Request: r}, nil
	})}
	_, err = rt.store.insertChannel(localChannel{Type: 1, Status: 1, Name: "fixture", Key: "fixture-only", Models: "fixture-transcriber", BaseURL: "https://fixture.example.invalid", Weight: 100})
	require.NoError(t, err)
	out := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(out)
	c.Request = httptest.NewRequest("POST", "/v1/audio/transcriptions", &body)
	c.Request.Header.Set("Content-Type", form.FormDataContentType())
	handleRelayRequest(rt)(c)
	t.Logf("multipart includes valid model + file: HTTP=%d, upstream calls=%d, body=%s", out.Code, calls, out.Body.String())
	require.Equal(t, 200, out.Code)
	require.Equal(t, 1, calls)
}

func TestFunctionalModelListMatchesCallableProtocol(t *testing.T) {
	rt := auditRuntime(t)
	for _, ch := range []localChannel{{Type: 1, Models: "openai-fixture"}, {Type: 24, Models: "gemini-fixture"}} {
		ch.Name = ch.Models
		ch.Status = 1
		ch.BaseURL = "https://fixture.example.invalid"
		ch.Key = "fixture-only"
		_, err := rt.store.insertChannel(ch)
		require.NoError(t, err)
	}
	out := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(out)
	c.Request = httptest.NewRequest("GET", "/v1beta/models", nil)
	handleRelayModels(rt)(c)
	t.Logf("Gemini catalogue: %s", out.Body.String())
	detail := httptest.NewRecorder()
	detailContext, _ := gin.CreateTestContext(detail)
	detailContext.Request = httptest.NewRequest("GET", "/v1beta/models/openai-fixture", nil)
	detailContext.Params = gin.Params{{Key: "model", Value: "openai-fixture"}}
	handleRelayModel(rt)(detailContext)
	t.Logf("retrieve advertised model on the same API: HTTP=%d", detail.Code)
	require.NotContains(t, out.Body.String(), "openai-fixture")
}

func TestFunctionalModelLimitForLargeAndChunkedBodies(t *testing.T) {
	for _, variant := range []string{"small", "large", "chunked"} {
		t.Run(variant, func(t *testing.T) {
			store, err := newTokenPolicyStore(t.TempDir())
			require.NoError(t, err)
			store.policies[7] = localTokenPolicy{TokenID: 7, Models: []string{"allowed"}}
			rt := auditRuntime(t)
			rt.balancer = newLocalRelayBalancer()
			_, err = rt.store.insertChannel(localChannel{Type: 1, Status: 1, Name: "fixture", Key: "fixture-only", Models: "forbidden", BaseURL: "https://fixture.example.invalid", Weight: 100})
			require.NoError(t, err)
			calls := 0
			rt.relayClient = &http.Client{Transport: auditTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"choices":[]}`)), Request: r}, nil
			})}
			engine := gin.New()
			engine.Use(func(c *gin.Context) { c.Set("token_id", 7); c.Next() }, store.middleware("v1"))
			engine.POST("/v1/chat/completions", handleRelayRequest(rt))
			content := "fixture"
			if variant == "large" {
				content = strings.Repeat("x", (1<<20)+1)
			}
			req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"forbidden","messages":[{"role":"user","content":"`+content+`"}]}`))
			req.Header.Set("Content-Type", "application/json")
			if variant == "chunked" {
				req.ContentLength = -1
				req.TransferEncoding = []string{"chunked"}
			}
			out := httptest.NewRecorder()
			engine.ServeHTTP(out, req)
			t.Logf("forbidden model, %s request: HTTP=%d upstream calls=%d", variant, out.Code, calls)
			require.Equal(t, 403, out.Code)
		})
	}
}

func TestFunctionalDailyLimitSurvivesReload(t *testing.T) {
	dir := t.TempDir()
	store, err := newTokenPolicyStore(dir)
	require.NoError(t, err)
	store.policies[7] = localTokenPolicy{TokenID: 7, DailyRequestLimit: 1}
	require.NoError(t, store.saveLocked())
	done, status, _ := store.begin(7, "v1", "", "", "fixture")
	require.Zero(t, status)
	done()
	_, status, _ = store.begin(7, "v1", "", "", "fixture")
	require.Equal(t, 429, status)
	restored, err := newTokenPolicyStore(dir)
	require.NoError(t, err)
	done, status, _ = restored.begin(7, "v1", "", "", "fixture")
	if done != nil {
		done()
	}
	t.Logf("daily limit=1, already used=1; same-day reload next-request status=%d (0 means admitted)", status)
	require.Equal(t, 429, status)
}

func TestFunctionalAnthropicStreamRetainsInputUsage(t *testing.T) {
	body := []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":25,\"output_tokens\":1}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":15}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	usage := parseStreamUsageMetrics(body)
	t.Logf("input=25 output=15 fixture: accounted input=%d output=%d total=%d", usage.InputTokens, usage.OutputTokens, usage.TotalTokens)
	require.Equal(t, int64(25), usage.InputTokens)
	require.Equal(t, int64(40), usage.TotalTokens)
}

func TestLongStreamRetainsInitialUsage(t *testing.T) {
	body := "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":123,\"cache_read_input_tokens\":45}}}\n\n" + strings.Repeat("data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"hello\"}}\n\n", 50000) + "data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":67}}\n\n"
	require.Greater(t, len(body), 2<<20)
	out := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(out)
	usage, streamed, err := copyRelayResponse(c, &http.Response{Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))})
	require.NoError(t, err)
	require.True(t, streamed)
	require.Equal(t, body, out.Body.String())
	require.EqualValues(t, 123, usage.InputTokens)
	require.EqualValues(t, 67, usage.OutputTokens)
	require.EqualValues(t, 190, usage.TotalTokens)
	require.EqualValues(t, 45, usage.CacheReadInputTokens)
	collector := &streamUsageCollector{}
	for _, part := range []string{"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_", "tokens\":9,\"output_tokens\":3}}}\n"} {
		_, _ = collector.Write([]byte(part))
	}
	require.EqualValues(t, 12, collector.result().TotalTokens)
}
