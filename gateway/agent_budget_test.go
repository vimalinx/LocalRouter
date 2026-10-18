package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func billingFixture(t *testing.T, handler http.HandlerFunc) (localRuntime, localToken, localToken, *httptest.Server) {
	t.Helper()
	rt, a, b, _, old := serviceWorkspaceFixture(t)
	old.Close()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	rt.relayClient, rt.balancer = upstream.Client(), newLocalRelayBalancer()
	var err error
	rt.policies.budgets, err = initializeAgentBudgets(rt)
	require.NoError(t, err)
	_, err = rt.store.insertChannel(localChannel{Type: 1, Key: "fixture-only", Status: 1, Name: "Fixture channel", BaseURL: upstream.URL, Models: "demo", Weight: 100})
	require.NoError(t, err)
	server := httptest.NewServer(buildServer(rt))
	t.Cleanup(server.Close)
	return rt, a, b, server
}

func TestAgentBudgetHTTPIsolationAndRevokedHistory(t *testing.T) {
	var upstreamCalls atomic.Int64
	rt, a, b, server := billingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"usage":{"prompt_tokens":5,"completion_tokens":2,"cost_usd":0.01},"choices":[]}`)
	})
	rt.policies.policies[a.ID] = localTokenPolicy{TokenID: a.ID, DailyBudgetUSDMicros: 20_000, MonthlyBudgetUSDMicros: 30_000, RequestReserveUSDMicros: 10_000}
	body := map[string]any{"model": "demo", "messages": []any{}}
	for range 2 {
		status, raw := setupHTTP(t, server, a, "POST", "/v1/chat/completions", body)
		require.Equal(t, 200, status, string(raw))
	}
	status, raw := setupHTTP(t, server, a, "POST", "/v1/chat/completions", body)
	require.Equal(t, 429, status, string(raw))
	assert.Contains(t, string(raw), "agent_daily_budget_exceeded")
	status, raw = setupHTTP(t, server, b, "POST", "/v1/chat/completions", body)
	require.Equal(t, 200, status, string(raw))
	assert.Equal(t, int64(3), upstreamCalls.Load())
	view, err := rt.policies.budgets.view(a.ID, rt.policies.policies[a.ID], time.Now())
	require.NoError(t, err)
	assert.Equal(t, int64(20_000), view.DailySpent)
	assert.Zero(t, view.Held)
	before, err := buildLocalAgentUsage(rt, time.Now())
	require.NoError(t, err)
	require.NoError(t, rt.store.deleteToken(rt.rootUser.ID, a.ID))
	after, err := buildLocalAgentUsage(rt, time.Now())
	require.NoError(t, err)
	require.Len(t, after, len(before))
	var found bool
	for _, item := range after {
		if item.TokenID == a.ID {
			found = true
			assert.True(t, item.Revoked)
			assert.Equal(t, int64(2), item.Requests)
			assert.InDelta(t, 0.02, item.CostUSD, 1e-9)
		}
	}
	require.True(t, found)
	status, _ = setupHTTP(t, server, a, "POST", "/v1/chat/completions", body)
	assert.Equal(t, 401, status)
	status, raw = setupHTTP(t, server, localToken{}, "GET", fmt.Sprintf("/local/api/agent-billing/%d", a.ID), nil)
	require.Equal(t, 200, status, string(raw))
	assert.Contains(t, string(raw), "Fixture channel")
	status, _ = setupHTTP(t, server, b, "POST", fmt.Sprintf("/local/api/agent-billing/%d/absent/reconcile", a.ID), map[string]any{"amount_usd_micros": 0, "reason": "fixture confirmation"})
	assert.Equal(t, 403, status)
}

func TestAgentBudgetConcurrentReservationRestartAndUTCPeriod(t *testing.T) {
	rt := auditRuntime(t)
	s, err := initializeAgentBudgets(rt)
	require.NoError(t, err)
	p := localTokenPolicy{TokenID: 7, DailyBudgetUSDMicros: 10_000, MonthlyBudgetUSDMicros: 15_000, RequestReserveUSDMicros: 10_000}
	now := time.Date(2026, 9, 30, 23, 59, 59, 0, time.UTC)
	var admitted atomic.Int64
	var wait sync.WaitGroup
	for i := range 20 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			code, err := s.reserve(agentBudgetEntry{ID: fmt.Sprint(i), TokenID: 7}, p, -1, now)
			assert.NoError(t, err)
			if code == "" {
				admitted.Add(1)
			}
		}()
	}
	wait.Wait()
	assert.Equal(t, int64(1), admitted.Load())
	reloaded, err := initializeAgentBudgets(rt)
	require.NoError(t, err)
	next := now.Add(2 * time.Second)
	v, err := reloaded.view(7, p, next)
	require.NoError(t, err)
	assert.Zero(t, v.DailySpent)
	assert.Equal(t, int64(10_000), v.Held)
	code, err := reloaded.reserve(agentBudgetEntry{ID: "new-month", TokenID: 7}, p, -1, next)
	require.NoError(t, err)
	assert.Equal(t, "agent_daily_budget_exceeded", code)
	entries, _, err := s.entries(7, 1, 50)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "unknown", entries[0].State)
	require.NoError(t, s.reconcile(7, entries[0].ID, 4_000, "provider invoice confirmed"))
	code, err = s.reserve(agentBudgetEntry{ID: "after-confirmation", TokenID: 7}, p, -1, next)
	require.NoError(t, err)
	assert.Empty(t, code)
	assert.Error(t, s.reconcile(7, "after-confirmation", 0, "cannot reconcile an active call"))
}

func TestAgentBudgetUnknownPriceOverrunAndPersistenceFailure(t *testing.T) {
	rt := auditRuntime(t)
	s, err := initializeAgentBudgets(rt)
	require.NoError(t, err)
	now := time.Now().UTC()
	p := localTokenPolicy{TokenID: 2, MonthlyBudgetUSDMicros: 30_000}
	code, err := s.reserve(agentBudgetEntry{ID: "missing-quote", TokenID: 2}, p, -1, now)
	require.NoError(t, err)
	assert.Equal(t, "agent_budget_quote_required", code)
	p.RequestReserveUSDMicros = 10_000
	code, err = s.reserve(agentBudgetEntry{ID: "unknown", TokenID: 2}, p, -1, now)
	require.NoError(t, err)
	require.Empty(t, code)
	require.NoError(t, s.finish("unknown", nil, usageMetrics{}, "demo", "fixture", 200, false))
	v, err := s.view(2, p, now)
	require.NoError(t, err)
	assert.Equal(t, int64(10_000), v.Held)
	code, err = s.reserve(agentBudgetEntry{ID: "overrun", TokenID: 2}, p, -1, now)
	require.NoError(t, err)
	require.Empty(t, code)
	require.NoError(t, s.finish("overrun", &usageCost{AmountUSD: 0.02, Status: "reported", Source: "provider"}, usageMetrics{}, "demo", "fixture", 200, false))
	v, err = s.view(2, p, now)
	require.NoError(t, err)
	assert.Equal(t, int64(20_000), v.MonthlySpent)
	assert.Equal(t, int64(1), v.Overruns)
	code, err = s.reserve(agentBudgetEntry{ID: "denied", TokenID: 2}, p, -1, now)
	require.NoError(t, err)
	assert.Equal(t, "agent_budget_reconciliation_required", code)
	require.NoError(t, s.reconcile(2, "overrun", 20_000, "confirmed on provider statement"))
	code, err = s.reserve(agentBudgetEntry{ID: "exhausted", TokenID: 2}, p, -1, now)
	require.NoError(t, err)
	assert.Equal(t, "agent_monthly_budget_exceeded", code)
	require.NoError(t, s.reconcile(2, "unknown", 0, "provider confirms no charge"))
	code, err = s.reserve(agentBudgetEntry{ID: "last-call", TokenID: 2}, p, -1, now)
	require.NoError(t, err)
	assert.Empty(t, code)
	// Inject a storage failure: the pre-dispatch reservation is retained.
	_, err = rt.store.db.Exec(`CREATE TRIGGER billing_fail BEFORE UPDATE ON agent_billing BEGIN SELECT RAISE(FAIL,'fixture failure'); END`)
	require.NoError(t, err)
	assert.Error(t, s.finish("last-call", &usageCost{AmountUSD: 0.001, Status: "reported"}, usageMetrics{}, "", "", 200, false))
	v, err = s.view(2, p, now)
	require.NoError(t, err)
	assert.Equal(t, int64(10_000), v.Held)
}

func TestAgentBudgetHTTPStreamingAndUnknownOutcome(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		t.Run(fmt.Sprint(terminal), func(t *testing.T) {
			rt, a, _, server := billingFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"usage\":{\"cost_usd\":0.01}}\n\n")
				if terminal {
					fmt.Fprint(w, "data: [DONE]\n\n")
				}
			})
			rt.policies.policies[a.ID] = localTokenPolicy{TokenID: a.ID, MonthlyBudgetUSDMicros: 20_000, RequestReserveUSDMicros: 20_000}
			status, raw := setupHTTP(t, server, a, "POST", "/v1/chat/completions", map[string]any{"model": "demo", "stream": true})
			require.Equal(t, 200, status, string(raw))
			v, err := rt.policies.budgets.view(a.ID, rt.policies.policies[a.ID], time.Now())
			require.NoError(t, err)
			if terminal {
				assert.Equal(t, int64(10_000), v.MonthlySpent)
				assert.Zero(t, v.Held)
			} else {
				assert.Zero(t, v.MonthlySpent)
				assert.Equal(t, int64(20_000), v.Held)
			}
		})
	}
}

func TestAgentBudgetHistoryMigrationExactlyOnce(t *testing.T) {
	rt := auditRuntime(t)
	require.NoError(t, rt.store.logRequest(context.Background(), localRequestLog{UserID: rt.rootUser.ID, TokenID: 42, CreatedAt: time.Now().Unix(), CostUSD: 0.03, CostStatus: "reported", Type: localLogTypeConsume, ChannelName: "historical channel"}))
	s, err := initializeAgentBudgets(rt)
	require.NoError(t, err)
	s, err = initializeAgentBudgets(rt)
	require.NoError(t, err)
	entries, total, err := s.entries(42, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, entries, 1)
	assert.Equal(t, int64(30_000), entries[0].Charged)
	code, err := s.reserve(agentBudgetEntry{ID: "current", TokenID: 42}, localTokenPolicy{MonthlyBudgetUSDMicros: 30_000, RequestReserveUSDMicros: 1}, -1, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "agent_monthly_budget_exceeded", code)
}

func TestAgentBudgetPackSharesBudgetWithCompatibilityAndKeepsDurableHistory(t *testing.T) {
	rt, a, _, server := billingFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"usage":{"cost_usd":0.01,"prompt_tokens":3,"completion_tokens":2}}`)
	})
	channels, err := rt.store.enabledChannels()
	require.NoError(t, err)
	writeProtocolDefinition(t, rt.config.ProtocolDir, protocolDefinition{
		SchemaVersion: protocolSchemaVersionV3, ID: "billing-fixture", Name: "Billing fixture", Description: "Test billing", Enabled: true, BaseURL: channels[0].BaseURL, Auth: protocolAuth{Type: "none"},
		Routes:  []protocolRoute{{OperationID: "run", Methods: []string{"POST"}, Path: "/run", Summary: "Fixture request"}},
		Pricing: &protocolPricing{Entries: []protocolPricingEntry{{ID: "run", Scope: "operation", Amount: analyticsPrice(0.01), Currency: "USD", Unit: "per-request", Status: "confirmed", SourceURL: "https://example.com/pricing", SourceType: "official-docs", CheckedAt: "2026-09-23"}}},
	})
	require.NoError(t, rt.protocols.reload())
	rt.policies.policies[a.ID] = localTokenPolicy{TokenID: a.ID, MonthlyBudgetUSDMicros: 20_000, RequestReserveUSDMicros: 10_000}
	_, before, err := rt.policies.budgets.entries(a.ID, 1, 20)
	require.NoError(t, err)
	pfStatus, pfRaw := setupHTTP(t, server, a, "POST", "/agent/preflight", map[string]any{"pack": "billing-fixture", "operation": "run", "input": map[string]any{}})
	require.Equal(t, 200, pfStatus, string(pfRaw))
	assert.Contains(t, string(pfRaw), "agent_budget")
	_, afterPreflight, err := rt.policies.budgets.entries(a.ID, 1, 20)
	require.NoError(t, err)
	assert.Equal(t, before, afterPreflight)
	status, raw := setupHTTP(t, server, a, "POST", "/p/billing-fixture/run", map[string]any{})
	require.Equal(t, 200, status, string(raw))
	status, raw = setupHTTP(t, server, a, "POST", "/v1/chat/completions", map[string]any{"model": "demo"})
	require.Equal(t, 200, status, string(raw))
	status, raw = setupHTTP(t, server, a, "POST", "/p/billing-fixture/run", map[string]any{})
	require.Equal(t, 429, status, string(raw))
	assert.Contains(t, string(raw), "agent_monthly_budget_exceeded")
	// Rotating protocol transport diagnostics cannot remove already billed usage.
	require.NoError(t, os.WriteFile(rt.events.path, nil, 0600))
	items, err := buildLocalAgentUsage(rt, time.Now())
	require.NoError(t, err)
	for _, item := range items {
		if item.TokenID == a.ID {
			assert.Equal(t, int64(2), item.Requests)
			assert.InDelta(t, 0.02, item.CostUSD, 1e-9)
		}
	}
	entries, total, err := rt.policies.budgets.entries(a.ID, 1, 20)
	require.NoError(t, err)
	require.Equal(t, int64(2), total)
	assert.Equal(t, "billing-fixture", entries[1].Service)
}

func TestLogSearchIncludesRevokedAgentsAcrossPages(t *testing.T) {
	rt := auditRuntime(t)
	id, err := rt.store.createToken(rt.rootUser.ID, localToken{Name: "old-token", AgentCode: "agent-a", AgentName: "Writer", Workspace: "/fixture"})
	require.NoError(t, err)
	for i := range 60 {
		require.NoError(t, rt.store.logRequest(context.Background(), localRequestLog{UserID: rt.rootUser.ID, TokenID: int(id), TokenName: "old-token", ModelName: fmt.Sprintf("model-%d", i), Type: localLogTypeConsume}))
	}
	require.NoError(t, rt.store.deleteToken(rt.rootUser.ID, int(id)))
	items, total, err := rt.store.filteredLogs(rt.rootUser.ID, 2, 50, int(id), "agent-a")
	require.NoError(t, err)
	assert.Equal(t, int64(60), total)
	require.Len(t, items, 10)
	assert.True(t, items[0].Revoked)
	items, total, err = rt.store.filteredLogs(rt.rootUser.ID, 1, 50, 0, "model-0")
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, items, 1)
	_, total, err = rt.store.filteredLogs(rt.rootUser.ID, 1, 50, 0, "' OR 1=1 --")
	require.NoError(t, err)
	assert.Zero(t, total)
	raw, err := json.Marshal(items)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), `"key"`)
}

func TestAgentBudgetIncompleteCostRetainsObservedLowerBound(t *testing.T) {
	rt := auditRuntime(t)
	store, err := initializeAgentBudgets(rt)
	require.NoError(t, err)
	policy := localTokenPolicy{MonthlyBudgetUSDMicros: 50_000, RequestReserveUSDMicros: 10_000}
	code, err := store.reserve(agentBudgetEntry{ID: "partial", TokenID: 2}, policy, -1, time.Now())
	require.NoError(t, err)
	require.Empty(t, code)
	partial := 0.02
	require.NoError(t, store.finish("partial", nil, usageMetrics{ReportedCostUSD: &partial}, "model", "fixture", 200, false))
	view, err := store.view(2, policy, time.Now())
	require.NoError(t, err)
	assert.Equal(t, int64(20_000), view.Held)
	assert.Equal(t, int64(1), view.Overruns)
	assert.Zero(t, view.MonthlySpent)
	code, err = store.reserve(agentBudgetEntry{ID: "blocked", TokenID: 2}, policy, -1, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "agent_budget_reconciliation_required", code)
	require.NoError(t, store.reconcile(2, "partial", 30_000, "final provider invoice"))
	view, err = store.view(2, policy, time.Now())
	require.NoError(t, err)
	assert.Zero(t, view.Held)
	assert.Zero(t, view.Overruns)
	assert.Equal(t, int64(30_000), view.MonthlySpent)
	micro, valid := usdMicros(0.00000000000001)
	assert.True(t, valid)
	assert.Equal(t, int64(1), micro)
}
