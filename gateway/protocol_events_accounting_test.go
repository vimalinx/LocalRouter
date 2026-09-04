package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestProtocolEventPersistsOneCallUsageAndPriceSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := newProtocolEventStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/p/demo/chat/completions", nil)
	context.Status(http.StatusOK)
	context.Set(tokenPolicyContextID, 7)
	context.Set("localrouter_usage_model", "demo-model")
	context.Set("localrouter_usage_metrics", usageMetrics{InputTokens: 100, OutputTokens: 20, CacheReadInputTokens: 40, TotalTokens: 120})

	amount := 1.0
	definition := protocolDefinition{ID: "demo", Pricing: &protocolPricing{Entries: []protocolPricingEntry{{
		ID: "chat.completions", Scope: "operation", Amount: &amount, Currency: "USD", Unit: "per-100-requests", Status: "confirmed",
	}}}}
	store.recordRequestWithDefinition(context, "p", definition, "chat.completions", time.Now().Add(-time.Millisecond))

	events, err := store.read(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one call event, got %d", len(events))
	}
	event := events[0]
	if event.AccountingVersion != 1 || event.TokenID != 7 || event.Model != "demo-model" || event.Usage == nil || event.Usage.TotalTokens != 120 {
		t.Fatalf("unexpected persisted usage event: %+v", event)
	}
	if event.Cost == nil || event.Cost.AmountUSD != 0.01 || event.Cost.Source != "published-pricing" {
		t.Fatalf("unexpected persisted price snapshot: %+v", event.Cost)
	}
}
