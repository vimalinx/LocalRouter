package main

import (
	"math"
	"testing"
)

func TestParseUsageMetricsNormalizesProviderShapes(t *testing.T) {
	tests := []struct {
		name string
		body string
		want usageMetrics
	}{
		{
			name: "openai chat",
			body: `{"usage":{"prompt_tokens":120,"completion_tokens":30,"total_tokens":150,"prompt_tokens_details":{"cached_tokens":40},"completion_tokens_details":{"reasoning_tokens":12}}}`,
			want: usageMetrics{InputTokens: 120, OutputTokens: 30, TotalTokens: 150, CacheReadInputTokens: 40, ReasoningTokens: 12},
		},
		{
			name: "anthropic messages",
			body: `{"usage":{"input_tokens":80,"output_tokens":20,"cache_read_input_tokens":50,"cache_creation_input_tokens":10}}`,
			want: usageMetrics{InputTokens: 80, OutputTokens: 20, TotalTokens: 100, CacheReadInputTokens: 50, CacheWriteInputTokens: 10},
		},
		{
			name: "gemini",
			body: `{"usageMetadata":{"promptTokenCount":70,"candidatesTokenCount":15,"totalTokenCount":85,"cachedContentTokenCount":25,"thoughtsTokenCount":5}}`,
			want: usageMetrics{InputTokens: 70, OutputTokens: 15, TotalTokens: 85, CacheReadInputTokens: 25, ReasoningTokens: 5},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := parseUsageMetrics([]byte(test.body))
			if got.InputTokens != test.want.InputTokens || got.OutputTokens != test.want.OutputTokens || got.TotalTokens != test.want.TotalTokens || got.CacheReadInputTokens != test.want.CacheReadInputTokens || got.CacheWriteInputTokens != test.want.CacheWriteInputTokens || got.ReasoningTokens != test.want.ReasoningTokens {
				t.Fatalf("unexpected usage: got=%+v want=%+v", got, test.want)
			}
		})
	}
}

func TestParseStreamUsageMetricsKeepsTerminalCumulativeUsage(t *testing.T) {
	body := []byte("data: {\"usage\":{\"input_tokens\":10,\"output_tokens\":1}}\n\n" +
		"data: {\"usage\":{\"input_tokens\":10,\"output_tokens\":9,\"output_tokens_details\":{\"reasoning_tokens\":3}}}\n\n" +
		"data: [DONE]\n")
	usage := parseStreamUsageMetrics(body)
	if usage.InputTokens != 10 || usage.OutputTokens != 9 || usage.TotalTokens != 19 || usage.ReasoningTokens != 3 {
		t.Fatalf("unexpected stream usage: %+v", usage)
	}
}

func TestProtocolUsageCostCombinesCallAndTokenPricing(t *testing.T) {
	requestAmount := 2.0
	inputAmount := 3.0
	outputAmount := 9.0
	definition := protocolDefinition{Pricing: &protocolPricing{Entries: []protocolPricingEntry{
		{ID: "chat.completions", Scope: "operation", Amount: &requestAmount, Currency: "USD", Unit: "per-1k-requests", Status: "confirmed"},
		{ID: "test-model", Scope: "model", Amount: &inputAmount, Currency: "USD", Unit: "per-1m-input-tokens", Status: "confirmed"},
		{ID: "test-model", Scope: "model", Amount: &outputAmount, Currency: "USD", Unit: "per-1m-output-tokens", Status: "estimated"},
	}}}
	cost := protocolUsageCost(definition, "chat.completions", "test-model", usageMetrics{InputTokens: 1_000, OutputTokens: 500}, true)
	if cost == nil {
		t.Fatal("expected a cost snapshot")
	}
	want := 0.002 + 0.003 + 0.0045
	if math.Abs(cost.AmountUSD-want) > 1e-12 || cost.Status != "estimated" || cost.Source != "published-pricing" || len(cost.Components) != 3 {
		t.Fatalf("unexpected cost snapshot: %+v", cost)
	}
}

func TestProtocolUsageCostPrefersExplicitProviderUSD(t *testing.T) {
	reported := 0.0123
	cost := protocolUsageCost(protocolDefinition{}, "chat", "model", usageMetrics{ReportedCostUSD: &reported}, true)
	if cost == nil || cost.AmountUSD != reported || cost.Status != "reported" || cost.Source != "provider" {
		t.Fatalf("unexpected reported cost: %+v", cost)
	}
	if failed := protocolUsageCost(protocolDefinition{}, "chat", "model", usageMetrics{ReportedCostUSD: &reported}, false); failed != nil {
		t.Fatalf("failed calls must not invent an attributable charge: %+v", failed)
	}
}

func TestMergeCostStatusPreservesMixedProvenance(t *testing.T) {
	if got := mergeCostStatus("reported", "confirmed"); got != "partial" {
		t.Fatalf("reported plus published pricing must be partial, got %q", got)
	}
	if got := mergeCostStatus("confirmed", "estimated"); got != "estimated" {
		t.Fatalf("confirmed plus estimated pricing must be estimated, got %q", got)
	}
}
