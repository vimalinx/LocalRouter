package main

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func allowanceFixture(t *testing.T, limit int64) *serviceAllowanceStore {
	t.Helper()
	s := newServiceAllowanceStore(t.TempDir())
	require.NoError(t, s.transaction(true, func(doc *serviceAllowanceDocument) error {
		r := defaultAllowanceRule()
		r.Enabled = true
		r.Limit = limit
		doc.Rules["test"] = r
		return nil
	}))
	return s
}
func TestServiceAllowanceDefaultAndConcurrentRestart(t *testing.T) {
	s := newServiceAllowanceStore(t.TempDir())
	d, id, err := s.evaluate("test", "run", 0, -1, false)
	require.NoError(t, err)
	require.True(t, d.Allowed)
	require.False(t, d.Enabled)
	require.Empty(t, id)
	s = allowanceFixture(t, 7)
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(token int) {
			defer wg.Done()
			other := newServiceAllowanceStore(filepath.Dir(s.path))
			d, _, err := other.evaluate("test", "run", token, -1, true)
			if err != nil {
				t.Error(err)
				return
			}
			if d.Allowed {
				admitted.Add(1)
			}
		}(i + 1)
	}
	wg.Wait()
	require.EqualValues(t, 7, admitted.Load())
	restarted := newServiceAllowanceStore(filepath.Dir(s.path))
	d, _, err = restarted.evaluate("test", "run", 99, -1, true)
	require.NoError(t, err)
	require.False(t, d.Allowed)
	require.Equal(t, "service_approval_required", d.Code)
	info, err := os.Stat(s.path)
	require.NoError(t, err)
	require.EqualValues(t, 0600, info.Mode().Perm())
}
func TestServiceAllowancePreviewUnknownCostAndRollover(t *testing.T) {
	s := allowanceFixture(t, 100)
	require.NoError(t, s.transaction(true, func(doc *serviceAllowanceDocument) error {
		r := doc.Rules["test"]
		r.Unit = "usd_micros"
		r.Period = "day"
		doc.Rules["test"] = r
		return nil
	}))
	for i := 0; i < 3; i++ {
		d, id, err := s.evaluate("test", "run", 2, 60, false)
		require.NoError(t, err)
		require.True(t, d.Allowed)
		require.Empty(t, id)
		require.EqualValues(t, 100, d.Remaining)
	}
	d, _, err := s.evaluate("test", "run", 2, -1, true)
	require.NoError(t, err)
	require.False(t, d.Allowed)
	_, id, err := s.evaluate("test", "run", 2, 60, true)
	require.NoError(t, err)
	require.NotEmpty(t, id)
	require.NoError(t, s.transaction(true, func(doc *serviceAllowanceDocument) error {
		r := doc.Receipts[id]
		r.Period = "2000-01-01"
		doc.Receipts[id] = r
		return nil
	}))
	d, _, err = s.evaluate("test", "run", 3, 60, true)
	require.NoError(t, err)
	require.False(t, d.Allowed)
	require.EqualValues(t, 40, d.Remaining)
	require.NoError(t, s.settle(id, 60))
	d, _, err = s.evaluate("test", "run", 3, 60, false)
	require.NoError(t, err)
	require.True(t, d.Allowed)
	require.EqualValues(t, 100, d.Remaining)
	require.NoError(t, s.settle(id, 0))
	require.NoError(t, s.transaction(false, func(doc *serviceAllowanceDocument) error {
		require.EqualValues(t, 60, doc.Receipts[id].Amount)
		return nil
	}))
}
func TestServiceAllowanceGrantScopedSingleUseAndDeny(t *testing.T) {
	s := allowanceFixture(t, 0)
	require.NoError(t, s.transaction(true, func(doc *serviceAllowanceDocument) error {
		doc.Grants["g"] = serviceAllowanceGrant{ID: "g", Service: "test", Operation: "run", TokenID: 2, ExpiresAt: time.Now().Add(time.Hour).Unix()}
		return nil
	}))
	for _, args := range []struct {
		op    string
		token int
	}{{"run", 3}, {"other", 2}, {"run", 0}} {
		d, _, err := s.evaluate("test", args.op, args.token, -1, true)
		require.NoError(t, err)
		require.False(t, d.Allowed)
	}
	d, _, err := s.evaluate("test", "run", 2, -1, false)
	require.NoError(t, err)
	require.True(t, d.Allowed)
	d, _, err = s.evaluate("test", "run", 2, -1, true)
	require.NoError(t, err)
	require.True(t, d.Allowed)
	d, _, err = s.evaluate("test", "run", 2, -1, true)
	require.NoError(t, err)
	require.False(t, d.Allowed)
	require.NoError(t, s.transaction(true, func(doc *serviceAllowanceDocument) error {
		r := doc.Rules["test"]
		r.Mode = "deny"
		doc.Rules["test"] = r
		g := doc.Grants["g"]
		g.Used = false
		doc.Grants["g"] = g
		return nil
	}))
	d, _, err = s.evaluate("test", "run", 2, -1, true)
	require.NoError(t, err)
	require.False(t, d.Allowed)
	require.Equal(t, "service_use_denied", d.Code)
}
func TestServiceAllowanceStorageFailureBlocksDispatch(t *testing.T) {
	s := allowanceFixture(t, 5)
	require.NoError(t, os.WriteFile(s.path, []byte("broken"), 0600))
	p := &tokenPolicyStore{allowances: s}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set(tokenPolicyContextID, 2)
	_, ok := p.reserveAllowance(c, "test", "run", -1)
	require.False(t, ok)
	require.Equal(t, 503, w.Code)
}
func TestServiceAllowanceFixedQuote(t *testing.T) {
	var def protocolDefinition
	require.NoError(t, json.Unmarshal([]byte(`{"pricing":{"entries":[{"scope":"operation","id":"run","unit":"per-request","amount":0.001,"currency":"USD","status":"confirmed"}]}}`), &def))
	require.EqualValues(t, 1000, fixedAllowanceQuote(def, "run"))
	require.EqualValues(t, -1, fixedAllowanceQuote(def, "other"))
	def.Pricing.Entries[0].Status = "estimated"
	require.EqualValues(t, -1, fixedAllowanceQuote(def, "run"))
	def.Pricing.Entries[0].Status = "confirmed"
	def.Pricing.Entries[0].Unit = "token"
	require.EqualValues(t, -1, fixedAllowanceQuote(def, "run"))
}

func TestServiceAllowanceHTTPPreflightAndHumanBoundary(t *testing.T) {
	rt, owner, other, maintainer, server := serviceWorkspaceFixture(t)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	writeProtocolDefinition(t, rt.config.ProtocolDir, protocolDefinition{SchemaVersion: protocolSchemaVersion, ID: "allowtest", Name: "Allowance test", Description: "Isolated allowance fixture", Enabled: true, BaseURL: upstream.URL, Auth: protocolAuth{Type: "none"}, Routes: []protocolRoute{{OperationID: "run", Methods: []string{"POST"}, Path: "/run", Summary: "Run"}}})
	require.NoError(t, rt.protocols.reload())
	rule := defaultAllowanceRule()
	rule.Enabled = true
	rule.Limit = 1
	for _, token := range []localToken{owner, maintainer} {
		status, _ := setupHTTP(t, server, token, "PUT", "/local/api/service-allowances/allowtest", rule)
		require.Equal(t, 403, status)
	}
	status, body := setupHTTP(t, server, localToken{}, "PUT", "/local/api/service-allowances/allowtest", rule)
	require.Equal(t, 200, status, string(body))
	// Optimistic revisions prevent two human editors overwriting each other.
	status, _ = setupHTTP(t, server, localToken{}, "PUT", "/local/api/service-allowances/allowtest", rule)
	require.Equal(t, 400, status)
	rt.policies.mu.Lock()
	rt.policies.policies[owner.ID] = localTokenPolicy{TokenID: owner.ID, Models: []string{"allowed-model"}}
	rt.policies.mu.Unlock()
	status, _ = setupHTTP(t, server, owner, "POST", "/p/allowtest/run", map[string]any{"model": "denied-model"})
	require.Equal(t, 403, status)
	rt.policies.mu.Lock()
	delete(rt.policies.policies, owner.ID)
	rt.policies.mu.Unlock()
	for i := 0; i < 2; i++ {
		status, body = setupHTTP(t, server, owner, "POST", "/agent/preflight", map[string]any{"pack": "allowtest", "operation": "run"})
		require.Equal(t, 200, status, string(body))
		require.Contains(t, string(body), `"name":"service_allowance","status":"pass"`)
	}
	require.EqualValues(t, 0, calls.Load())
	status, body = setupHTTP(t, server, owner, "POST", "/p/allowtest/run", map[string]any{})
	require.Equal(t, 200, status, string(body))
	status, body = setupHTTP(t, server, other, "POST", "/p/allowtest/run", map[string]any{})
	require.Equal(t, 403, status, string(body))
	require.Contains(t, string(body), "service_approval_required")
	require.EqualValues(t, 1, calls.Load())
	status, body = setupHTTP(t, server, other, "POST", "/agent/preflight", map[string]any{"pack": "allowtest", "operation": "run"})
	require.Equal(t, 200, status)
	require.Contains(t, string(body), `"name":"service_allowance","status":"fail"`)
	status, body = setupHTTP(t, server, localToken{}, "POST", "/local/api/service-allowances/allowtest/grants", map[string]any{"token_id": other.ID, "operation": "run"})
	require.Equal(t, 200, status, string(body))
	// A one-shot approval cannot override existing token policy.
	rt.policies.mu.Lock()
	rt.policies.policies[other.ID] = localTokenPolicy{TokenID: other.ID, Packs: []string{"elsewhere"}}
	rt.policies.mu.Unlock()
	status, _ = setupHTTP(t, server, other, "POST", "/p/allowtest/run", map[string]any{})
	require.Equal(t, 403, status)
	rt.policies.mu.Lock()
	delete(rt.policies.policies, other.ID)
	rt.policies.mu.Unlock()
	status, body = setupHTTP(t, server, other, "POST", "/p/allowtest/run", map[string]any{})
	require.Equal(t, 200, status, string(body))
	status, _ = setupHTTP(t, server, other, "POST", "/p/allowtest/run", map[string]any{})
	require.Equal(t, 403, status)
	require.EqualValues(t, 2, calls.Load())
}

func TestServiceAllowanceCompatibilitySharedAcrossAgents(t *testing.T) {
	rt := auditRuntime(t)
	var err error
	rt.policies, err = newTokenPolicyStore(t.TempDir())
	require.NoError(t, err)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	rt.relayClient = upstream.Client()
	profile, ok := rt.channelProfiles.matchRequestPath("/v1/chat/completions")
	require.True(t, ok)
	service := "compatibility:" + profile.Key
	require.NoError(t, rt.policies.allowances.transaction(true, func(doc *serviceAllowanceDocument) error {
		r := defaultAllowanceRule()
		r.Enabled = true
		r.Limit = 1
		doc.Rules[service] = r
		return nil
	}))
	engine := gin.New()
	token := 2
	engine.POST("/v1/chat/completions", func(c *gin.Context) {
		c.Set(tokenPolicyContextID, token)
		relayAcrossChannels(c, rt, []localChannel{{ID: 1, Type: profile.ID, BaseURL: upstream.URL}}, "test", []byte(`{"model":"test"}`))
	})
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`)))
	require.Equal(t, 200, w.Code)
	token = 3
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`)))
	require.Equal(t, 403, w.Code)
	require.EqualValues(t, 1, calls.Load())
}
func TestServiceAllowanceMoneySettlementAndUnknown(t *testing.T) {
	s := allowanceFixture(t, 1000)
	require.NoError(t, s.transaction(true, func(doc *serviceAllowanceDocument) error {
		r := doc.Rules["test"]
		r.Unit = "usd_micros"
		doc.Rules["test"] = r
		return nil
	}))
	p := &tokenPolicyStore{allowances: s}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(tokenPolicyContextID, 2)
	id, ok := p.reserveAllowance(c, "test", "run", 1000)
	require.True(t, ok)
	actual := 0.0004
	c.Set("localrouter_usage_metrics", usageMetrics{ReportedCostUSD: &actual})
	c.Set("localrouter_protocol_outcome", "unknown")
	p.finishAllowance(c, id, protocolDefinition{ID: "test"}, "run")
	d, _, err := s.evaluate("test", "run", 2, 1000, false)
	require.NoError(t, err)
	require.False(t, d.Allowed)
	require.Zero(t, d.Remaining)
	c.Set("localrouter_protocol_outcome", "")
	p.finishAllowance(c, id, protocolDefinition{ID: "test"}, "run")
	d, _, err = s.evaluate("test", "run", 2, 500, false)
	require.NoError(t, err)
	require.True(t, d.Allowed)
	require.EqualValues(t, 600, d.Remaining)
}
