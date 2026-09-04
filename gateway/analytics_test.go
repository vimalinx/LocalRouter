package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func analyticsPrice(value float64) *float64 { return &value }

func TestAddModelAnalyticsAcceptsEmptyLocalDatabase(t *testing.T) {
	store, err := openLocalStore(filepath.Join(t.TempDir(), "localrouter.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	root, err := store.ensureRootUser()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	analytics := localAnalytics{
		Trend:    newAnalyticsTrend(now),
		Services: []localAnalyticsService{},
		Models:   []localAnalyticsModel{},
	}
	if err := addModelAnalytics(&analytics, localRuntime{store: store, rootUser: root}, now); err != nil {
		t.Fatalf("empty local analytics query failed: %v", err)
	}
	if len(analytics.Models) != 0 {
		t.Fatalf("expected no model aggregates, got %+v", analytics.Models)
	}
}

func TestProtocolOperationRequestRateUsesOnlyExactRequestPricing(t *testing.T) {
	definition := protocolDefinition{Pricing: &protocolPricing{Entries: []protocolPricingEntry{
		{ID: "search", Scope: "operation", Amount: analyticsPrice(7), Currency: "USD", Unit: "per-1k-requests", Status: "confirmed"},
		{ID: "search", Scope: "operation", Amount: analyticsPrice(2), Currency: "USD", Unit: "per-100-pages", Status: "confirmed"},
		{ID: "other", Scope: "operation", Amount: analyticsPrice(100), Currency: "USD", Unit: "per-request", Status: "confirmed"},
	}}}

	rate, status, ok := protocolOperationRequestRate(definition, "search")
	if !ok || status != "confirmed" || math.Abs(rate-0.007) > 1e-12 {
		t.Fatalf("unexpected request price: rate=%v status=%q ok=%v", rate, status, ok)
	}
	if _, _, ok := protocolOperationRequestRate(definition, "missing"); ok {
		t.Fatal("pricing for a different operation must not be applied")
	}
}

func TestProtocolOperationRequestRateRejectsAmbiguousRates(t *testing.T) {
	definition := protocolDefinition{Pricing: &protocolPricing{Entries: []protocolPricingEntry{
		{ID: "search", Scope: "operation", Amount: analyticsPrice(1), Currency: "USD", Unit: "per-request", Status: "confirmed"},
		{ID: "search", Scope: "operation", Amount: analyticsPrice(2), Currency: "USD", Unit: "per-request", Status: "confirmed"},
	}}}
	if _, _, ok := protocolOperationRequestRate(definition, "search"); ok {
		t.Fatal("conflicting prices must remain unavailable")
	}
}

func TestProtocolPricingCoverageStatusDescribesConfiguredOperationsBeforeTraffic(t *testing.T) {
	amount := 1.0
	definition := protocolDefinition{
		Routes: []protocolRoute{{OperationID: "priced"}, {OperationID: "usage-based"}},
		Pricing: &protocolPricing{Entries: []protocolPricingEntry{{
			ID: "priced", Scope: "operation", Amount: &amount, Currency: "USD", Unit: "per-request", Status: "confirmed",
		}}},
	}
	if status := protocolPricingCoverageStatus(definition); status != "partial" {
		t.Fatalf("expected partial configured coverage, got %q", status)
	}
	definition.Routes = definition.Routes[:1]
	if status := protocolPricingCoverageStatus(definition); status != "confirmed" {
		t.Fatalf("expected confirmed configured coverage, got %q", status)
	}
}

func TestAddProtocolAnalyticsCombinesServiceCallsWithoutInventingUnits(t *testing.T) {
	amount := 10.0
	definitions := map[string]protocolDefinition{
		"search": {
			ID: "search", Name: "Search", Enabled: true, Auth: protocolAuth{Type: "none"},
			Routes: []protocolRoute{{OperationID: "query"}},
			Pricing: &protocolPricing{Entries: []protocolPricingEntry{{
				ID: "query", Scope: "operation", Amount: &amount, Currency: "USD", Unit: "per-1k-requests", Status: "confirmed",
			}}},
		},
		"video": {
			ID: "video", Name: "Video", Enabled: true, Auth: protocolAuth{Type: "none"},
			Routes: []protocolRoute{{OperationID: "generate"}},
			Pricing: &protocolPricing{Entries: []protocolPricingEntry{{
				ID: "generate", Scope: "operation", Amount: &amount, Currency: "USD", Unit: "per-second", Status: "confirmed",
			}}},
		},
	}
	registry := &protocolRegistry{templates: definitions, readinessCache: map[string]protocolReadinessRuntime{}}
	now := time.Now().UTC()
	analytics := localAnalytics{Trend: newAnalyticsTrend(now)}
	addProtocolAnalytics(&analytics, []protocolEvent{
		{CreatedAt: now, ProtocolID: "search", OperationID: "query", Status: 200, LatencyMS: 20},
		{CreatedAt: now, ProtocolID: "search", OperationID: "query", Status: 500, LatencyMS: 60},
		{CreatedAt: now, ProtocolID: "video", OperationID: "generate", Status: 200, LatencyMS: 100},
	}, definitions, registry)
	finalizeLocalAnalytics(&analytics)

	if analytics.Totals.ProtocolRequests != 3 || analytics.Totals.ProtocolPricedCalls != 1 {
		t.Fatalf("unexpected totals: %+v", analytics.Totals)
	}
	if math.Abs(analytics.Totals.ProtocolCostUSD-0.01) > 1e-12 {
		t.Fatalf("unexpected safely attributable cost: %v", analytics.Totals.ProtocolCostUSD)
	}
	if math.Abs(analytics.Totals.SuccessRate-200.0/3.0) > 1e-12 {
		t.Fatalf("unexpected success rate: %v", analytics.Totals.SuccessRate)
	}
	foundSearchTrend := false
	for _, service := range analytics.Services {
		if service.ID != "protocol:search" {
			continue
		}
		foundSearchTrend = true
		var requests int64
		var cost float64
		for _, point := range service.Trend {
			requests += point.Requests
			cost += point.CostUSD
		}
		if requests != 2 || math.Abs(cost-0.01) > 1e-12 {
			t.Fatalf("unexpected search supplier trend: requests=%d cost=%v", requests, cost)
		}
	}
	if !foundSearchTrend {
		t.Fatal("search supplier trend was not returned")
	}
}

func TestProtocolEventStoreReadRetainedIncludesRotatedFile(t *testing.T) {
	directory := t.TempDir()
	store := &protocolEventStore{path: filepath.Join(directory, "protocol-events.jsonl")}
	write := func(path string, event protocolEvent) {
		data, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(data, '\n'), credentialMode); err != nil {
			t.Fatal(err)
		}
	}
	write(store.path+".1", protocolEvent{ID: "old", CreatedAt: time.Now().UTC()})
	write(store.path, protocolEvent{ID: "new", CreatedAt: time.Now().UTC()})

	events, err := store.readRetained()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].ID != "old" || events[1].ID != "new" {
		t.Fatalf("unexpected retained events: %+v", events)
	}
}

func TestProtocolAnalyticsUsesPersistedUsageAndCostSnapshot(t *testing.T) {
	now := time.Now().UTC()
	currentPrice := 99.0
	definitions := map[string]protocolDefinition{"llm": {
		ID: "llm", Name: "LLM", Enabled: true, Auth: protocolAuth{Type: "none"},
		Routes:  []protocolRoute{{OperationID: "chat"}},
		Pricing: &protocolPricing{Entries: []protocolPricingEntry{{ID: "chat", Scope: "operation", Amount: &currentPrice, Currency: "USD", Unit: "per-request", Status: "confirmed"}}},
	}}
	registry := &protocolRegistry{templates: definitions, readinessCache: map[string]protocolReadinessRuntime{}}
	usage := usageMetrics{InputTokens: 80, OutputTokens: 20, CacheReadInputTokens: 30, ReasoningTokens: 5, TotalTokens: 100}
	analytics := localAnalytics{Trend: newAnalyticsTrend(now), Models: []localAnalyticsModel{}, Services: []localAnalyticsService{}}
	addProtocolAnalytics(&analytics, []protocolEvent{{
		AccountingVersion: 1, CreatedAt: now, ProtocolID: "llm", OperationID: "chat", Model: "m1", Status: 200,
		Usage: &usage, Cost: &usageCost{AmountUSD: 0.25, Status: "reported", Source: "provider"},
	}}, definitions, registry)
	finalizeLocalAnalytics(&analytics)

	if analytics.Totals.TotalTokens != 100 || analytics.Totals.CachedInputTokens != 30 || analytics.Totals.ReasoningTokens != 5 || analytics.Totals.ModelRequests != 1 {
		t.Fatalf("unexpected token totals: %+v", analytics.Totals)
	}
	if math.Abs(analytics.Totals.ProtocolCostUSD-0.25) > 1e-12 {
		t.Fatalf("historical event was repriced: %+v", analytics.Totals)
	}
	if len(analytics.Models) != 1 || analytics.Models[0].Name != "llm:m1" || analytics.Models[0].CostStatus != "reported" {
		t.Fatalf("unexpected protocol model aggregate: %+v", analytics.Models)
	}
}
