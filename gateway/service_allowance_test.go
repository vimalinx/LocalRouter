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
		r.Unit = "requests"
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
		r.OperationLimits = map[string]int64{"run": 80}
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
	require.EqualValues(t, 20, *d.OperationRemaining)
	require.NoError(t, s.settle(id, 60))
	d, _, err = s.evaluate("test", "run", 3, 60, false)
	require.NoError(t, err)
	require.True(t, d.Allowed)
	require.EqualValues(t, 100, d.Remaining)
	require.EqualValues(t, 80, *d.OperationRemaining)
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
	rule.Unit = "requests"
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
		r.Unit = "requests"
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

func TestServiceAllowanceSubLimitsShareParentAtomically(t *testing.T) {
	s := allowanceFixture(t, 5)
	require.NoError(t, s.transaction(true, func(doc *serviceAllowanceDocument) error {
		r := doc.Rules["test"]
		r.OperationLimits = map[string]int64{"search": 3, "generate": 4}
		doc.Rules["test"] = r
		return nil
	}))
	var search, generate atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			op := "search"
			if i%2 == 0 {
				op = "generate"
			}
			d, _, err := s.evaluate("test", op, i+1, -1, true)
			if err != nil {
				t.Error(err)
				return
			}
			if d.Allowed {
				if op == "search" {
					search.Add(1)
				} else {
					generate.Add(1)
				}
			}
		}(i)
	}
	wg.Wait()
	require.EqualValues(t, 5, search.Load()+generate.Load())
	require.LessOrEqual(t, search.Load(), int32(3))
	require.LessOrEqual(t, generate.Load(), int32(4))
	d, _, err := newServiceAllowanceStore(filepath.Dir(s.path)).evaluate("test", "search", 2, -1, false)
	require.NoError(t, err)
	require.False(t, d.Allowed)
	require.NotNil(t, d.OperationRemaining)
}
func TestServiceAllowanceSubLimitExhaustionAndCanonicalPaths(t *testing.T) {
	s := allowanceFixture(t, 10)
	require.NoError(t, s.transaction(true, func(doc *serviceAllowanceDocument) error {
		r := doc.Rules["test"]
		r.OperationLimits = map[string]int64{"search": 1, "zero": 0}
		doc.Rules["test"] = r
		return nil
	}))
	d, _, err := s.evaluate("test", "search", 2, -1, true)
	require.NoError(t, err)
	require.True(t, d.Allowed)
	d, _, err = s.evaluate("test", "search", 3, -1, true)
	require.NoError(t, err)
	require.False(t, d.Allowed)
	require.EqualValues(t, 9, d.Remaining)
	require.Contains(t, d.Message, "sub-allowance exhausted")
	d, _, err = s.evaluate("test", "zero", 2, -1, true)
	require.NoError(t, err)
	require.False(t, d.Allowed)
	d, _, err = s.evaluate("test", "unconfigured", 2, -1, true)
	require.NoError(t, err)
	require.True(t, d.Allowed)
	service := "compatibility:gemini-native"
	op := "POST /v1beta/models/{path}"
	require.NoError(t, s.transaction(true, func(doc *serviceAllowanceDocument) error {
		r := defaultAllowanceRule()
		r.Enabled = true
		r.Unit = "requests"
		r.Limit = 10
		r.OperationLimits = map[string]int64{op: 1}
		doc.Rules[service] = r
		return nil
	}))
	d, _, err = s.evaluate(service, "POST /v1beta/models/model-a:generateContent", 2, -1, true)
	require.NoError(t, err)
	require.True(t, d.Allowed)
	d, _, err = s.evaluate(service, "POST /v1beta/models/model-b:generateContent", 2, -1, true)
	require.NoError(t, err)
	require.False(t, d.Allowed)
	require.EqualValues(t, 0, *d.OperationRemaining)
	d, _, err = s.evaluate(service, "POST /v1beta/models/publisher/model-c:generateContent", 2, -1, true)
	require.NoError(t, err)
	require.False(t, d.Allowed)
	require.NotNil(t, d.OperationRemaining)
	require.EqualValues(t, 0, *d.OperationRemaining)
}
func TestServiceAllowanceBatchDefaultsAndRollback(t *testing.T) {
	rt, owner, _, _, server := serviceWorkspaceFixture(t)
	status, body := setupHTTP(t, server, localToken{}, "GET", "/local/api/service-allowances", nil)
	require.Equal(t, 200, status)
	var list struct {
		Data []struct {
			Service    string               `json:"service"`
			Rule       serviceAllowanceRule `json:"rule"`
			Operations []struct {
				ID string `json:"id"`
			} `json:"operations"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &list))
	require.GreaterOrEqual(t, len(list.Data), 2)
	a, b := list.Data[0], list.Data[1]
	require.False(t, a.Rule.Enabled)
	require.Equal(t, "usd_micros", a.Rule.Unit)
	require.EqualValues(t, 3000000, a.Rule.Limit)
	require.NotEmpty(t, a.Operations)
	a.Rule.OperationLimits = map[string]int64{a.Operations[0].ID: 3000000}
	batch := func() map[string]any {
		return map[string]any{"services": []any{map[string]any{"service": a.Service, "rule": a.Rule}, map[string]any{"service": b.Service, "rule": b.Rule}}}
	}
	status, _ = setupHTTP(t, server, owner, "POST", "/local/api/service-allowances/batch", batch())
	require.Equal(t, 403, status)
	status, body = setupHTTP(t, server, localToken{}, "POST", "/local/api/service-allowances/batch", batch())
	require.Equal(t, 200, status, string(body))
	a.Rule.Revision = 1
	a.Rule.Limit = 9000000 // second service intentionally retains stale revision 0
	status, _ = setupHTTP(t, server, localToken{}, "POST", "/local/api/service-allowances/batch", batch())
	require.Equal(t, 400, status)
	require.NoError(t, rt.policies.allowances.transaction(false, func(doc *serviceAllowanceDocument) error {
		require.EqualValues(t, 3000000, doc.Rules[a.Service].Limit)
		require.EqualValues(t, 1, doc.Rules[a.Service].Revision)
		require.False(t, doc.Rules[a.Service].Enabled)
		require.EqualValues(t, 3000000, doc.Rules[a.Service].OperationLimits[a.Operations[0].ID])
		return nil
	}))
	a.Rule.OperationLimits = map[string]int64{"does.not.exist": 1}
	status, _ = setupHTTP(t, server, localToken{}, "PUT", "/local/api/service-allowances/"+a.Service, a.Rule)
	require.Equal(t, 400, status)
}
