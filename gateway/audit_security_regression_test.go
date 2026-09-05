package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type auditTransport func(*http.Request) (*http.Response, error)

func (f auditTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func auditRuntime(t *testing.T) localRuntime {
	store, err := openLocalStore(filepath.Join(t.TempDir(), "audit.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	root, err := store.ensureRootUser()
	require.NoError(t, err)
	return localRuntime{store: store, rootUser: root, channelProfiles: testChannelProfiles(t), adminAuth: &adminAuthStore{enabled: false}}
}

func TestAuditCrossOriginSimplePostCreatesChannel(t *testing.T) {
	rt := auditRuntime(t)
	engine := gin.New()
	engine.Use(localSecurityHeaders(), localAdminAuth(rt.adminAuth, nil, rt.rootUser))
	engine.POST("/local/api/channels", handleAddChannel(rt))
	req := httptest.NewRequest("POST", "/local/api/channels", strings.NewReader(`{"channel":{"type":1,"name":"audit-cross-origin","key":"fixture-only","base_url":"https://untrusted.example.invalid","models":"*","priority":999}}`))
	req.Header.Set("Origin", "https://untrusted.example.invalid")
	req.Header.Set("Content-Type", "text/plain")
	out := httptest.NewRecorder()
	engine.ServeHTTP(out, req)
	n, err := rt.store.count("channels", "")
	require.NoError(t, err)
	t.Logf("external Origin + text/plain + no credentials: HTTP %d, persisted channels=%d", out.Code, n)
	require.Equal(t, http.StatusForbidden, out.Code, "cross-origin management mutation must be rejected")
	require.Zero(t, n)
}

func TestAuditUnsafePostReplayedAfterUpstreamAccepted(t *testing.T) {
	rt := auditRuntime(t)
	calls := 0
	rt.relayClient = &http.Client{Transport: auditTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		code := 200
		if calls == 1 {
			code = 504
		}
		return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":[]}`)), Request: req}, nil
	})}
	out := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(out)
	body := []byte(`{"model":"image-model","prompt":"fixture"}`)
	c.Request = httptest.NewRequest("POST", "/v1/images/generations", strings.NewReader(string(body)))
	channels := []localChannel{{ID: 1, Type: 1, Key: "fixture-only", BaseURL: "http://first.example.invalid"}, {ID: 2, Type: 1, Key: "fixture-only", BaseURL: "http://second.example.invalid"}}
	relayAcrossChannels(c, rt, channels, "image-model", body)
	t.Logf("one POST without idempotency key, first provider accepted then returned 504: upstream calls=%d, downstream HTTP=%d", calls, out.Code)
	require.Equal(t, 1, calls, "unknown paid operation outcome must not be replayed")
}

func TestAuditRedirectDoesNotLeakCustomAPIKey(t *testing.T) {
	profile := testChannelProfile(t, "anthropic-messages")
	incoming := httptest.NewRequest("POST", "/v1/messages", nil)
	req, err := makeUpstreamRequest(incoming, localChannel{Type: profile.ID, Key: "fixture-only", BaseURL: "https://trusted.example.invalid"}, profile, []byte(`{"model":"fixture"}`))
	require.NoError(t, err)
	leaked := false
	calls := 0
	client := &http.Client{CheckRedirect: rejectUpstreamRedirect, Transport: auditTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host == "trusted.example.invalid" {
			return &http.Response{StatusCode: 307, Header: http.Header{"Location": []string{"https://other.example.invalid/receive"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		}
		leaked = r.Header.Get("X-Api-Key") != ""
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}")), Request: r}, nil
	})}
	resp, err := client.Do(req)
	require.Error(t, err)
	if resp != nil {
		resp.Body.Close()
	}
	t.Log(fmt.Sprintf("cross-host 307: requests=%d, upstream key forwarded=%t", calls, leaked))
	require.False(t, leaked, "upstream credential must not follow redirects to another host")
}

func TestOperatorHostGuardRejectsRebinding(t *testing.T) {
	engine := gin.New()
	engine.Use(localHostGuard())
	engine.GET("/local/api/summary", func(c *gin.Context) { c.Status(http.StatusOK) })
	for _, tc := range []struct {
		host   string
		status int
	}{{"127.0.0.1:8317", 200}, {"localhost:8317", 200}, {"[::1]:8317", 200}, {"rebinding.example.invalid:8317", 403}} {
		req := httptest.NewRequest("GET", "/local/api/summary", nil)
		req.Host = tc.host
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		require.Equal(t, tc.status, out.Code, tc.host)
	}
}
