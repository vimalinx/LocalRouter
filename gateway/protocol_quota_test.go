package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func quotaNumber(value float64) *float64 { return &value }

func TestNormalizeProtocolQuotaDerivesOnlyKnowableValues(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	fromRemaining := normalizeProtocolQuota(&protocolCredentialQuota{
		Total: quotaNumber(100), Remaining: quotaNumber(35), Unit: "credits", Status: "confirmed",
	}, now, 3600)
	require.NotNil(t, fromRemaining.Used)
	assert.Equal(t, 65.0, *fromRemaining.Used)

	fromUsed := normalizeProtocolQuota(&protocolCredentialQuota{
		Total: quotaNumber(100), Used: quotaNumber(40), Unit: "credits",
	}, now, 3600)
	require.NotNil(t, fromUsed.Remaining)
	assert.Equal(t, 60.0, *fromUsed.Remaining)

	remainingOnly := protocolQuotaAccount(&protocolCredentialQuota{Remaining: quotaNumber(12)}, now, 3600)
	assert.True(t, remainingOnly.Tracked)
	assert.Nil(t, remainingOnly.Total)
	assert.Nil(t, remainingOnly.UsedPercent)

	unknown := protocolQuotaAccount(&protocolCredentialQuota{Status: "confirmed"}, now, 3600)
	assert.False(t, unknown.Tracked)
	assert.Equal(t, "unknown", unknown.Status)
	assert.Nil(t, unknown.Remaining)

	inconsistent := protocolQuotaAccount(&protocolCredentialQuota{
		Total: quotaNumber(100), Remaining: quotaNumber(80), Used: quotaNumber(50), Status: "confirmed",
	}, now, 3600)
	assert.Equal(t, "estimated", inconsistent.Status)
	assert.NotNil(t, inconsistent.UsedPercent)
}

func TestProtocolQuotaStaleAndMixedUnitAggregation(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	stale := protocolQuotaAccount(&protocolCredentialQuota{
		Total: quotaNumber(10), Remaining: quotaNumber(4), Unit: "credits", CheckedAt: now.Add(-2 * time.Hour).Format(time.RFC3339),
	}, now, 3600)
	assert.True(t, stale.Stale)
	assert.Equal(t, "stale", stale.Status)

	credits := protocolQuotaAccount(&protocolCredentialQuota{Total: quotaNumber(10), Remaining: quotaNumber(4), Unit: "credits"}, now, 0)
	dollars := protocolQuotaAccount(&protocolCredentialQuota{Total: quotaNumber(20), Remaining: quotaNumber(10), Unit: "USD"}, now, 0)
	mixed := aggregateProtocolQuota([]protocolQuotaAccountView{credits, dollars}, true)
	assert.Equal(t, "mixed-unit", mixed.Status)
	assert.Nil(t, mixed.Total)
	assert.Nil(t, mixed.Remaining)
	assert.Nil(t, mixed.UsedPercent)

	complete := aggregateProtocolQuota([]protocolQuotaAccountView{credits}, true)
	assert.Equal(t, "confirmed", complete.Status)
	require.NotNil(t, complete.UsedPercent)
	assert.InDelta(t, 60, *complete.UsedPercent, 0.001)

	estimated := credits
	estimated.Status = "estimated"
	estimatedAggregate := aggregateProtocolQuota([]protocolQuotaAccountView{estimated}, true)
	assert.Equal(t, "estimated", estimatedAggregate.Status)
	require.NotNil(t, estimatedAggregate.UsedPercent)
	assert.InDelta(t, 60, *estimatedAggregate.UsedPercent, 0.001)
}

func TestMappedQuotaMissingIsUnknownNotZero(t *testing.T) {
	record := []byte(`{"quota":{"remaining":"7.5"}}`)
	assert.Nil(t, mappedPoolNumber(record, "quota.total"))
	remaining := mappedPoolNumber(record, "quota.remaining")
	require.NotNil(t, remaining)
	assert.Equal(t, 7.5, *remaining)
	assert.Nil(t, mappedPoolNumber([]byte(`{"quota":{"remaining":-1}}`), "quota.remaining"))
}

func TestQuotaAggregationUsesCompensatedSummation(t *testing.T) {
	accounts := make([]protocolQuotaAccountView, 0, 100)
	for range 100 {
		accounts = append(accounts, protocolQuotaAccountView{Tracked: true, Status: "confirmed", Total: quotaNumber(1), Remaining: quotaNumber(0.1), Used: quotaNumber(0.9), Unit: "credits"})
	}
	quota := aggregateProtocolQuota(accounts, true)
	require.NotNil(t, quota.Remaining)
	assert.Equal(t, 10.0, *quota.Remaining)
}

func TestProtocolQuotaReferenceValueUsesMatchingUniqueRate(t *testing.T) {
	pricing := &protocolPricing{Entries: []protocolPricingEntry{
		{ID: "credit-pack", Status: "confirmed", Amount: quotaNumber(10), Currency: "USD", Unit: "per-1000-credits"},
		{ID: "per-model", Status: "estimated", Amount: quotaNumber(0.16), Currency: "USD", Unit: "per-model"},
	}}
	value := protocolQuotaReferenceValue(
		quotaNumber(20), quotaNumber(12), quotaNumber(8), "credit", "confirmed", pricing,
	)
	require.NotNil(t, value)
	assert.Equal(t, "confirmed", value.Status)
	assert.Equal(t, "USD", value.Currency)
	assert.Equal(t, "credit-pack", value.PricingID)
	require.NotNil(t, value.Total)
	require.NotNil(t, value.Remaining)
	require.NotNil(t, value.Used)
	assert.InDelta(t, 0.20, *value.Total, 1e-12)
	assert.InDelta(t, 0.12, *value.Remaining, 1e-12)
	assert.InDelta(t, 0.08, *value.Used, 1e-12)
}

func TestProtocolQuotaReferenceValueRejectsIncompatibleOrAmbiguousRates(t *testing.T) {
	incompatible := &protocolPricing{Entries: []protocolPricingEntry{
		{ID: "search", Status: "confirmed", Amount: quotaNumber(7), Currency: "USD", Unit: "per-1k-requests"},
	}}
	assert.Nil(t, protocolQuotaReferenceValue(quotaNumber(20), nil, nil, "credits", "confirmed", incompatible))

	ambiguous := &protocolPricing{Entries: []protocolPricingEntry{
		{ID: "search", Status: "confirmed", Amount: quotaNumber(7), Currency: "USD", Unit: "per-1k-requests"},
		{ID: "answer", Status: "confirmed", Amount: quotaNumber(5), Currency: "USD", Unit: "per-1k-requests"},
	}}
	value := protocolQuotaReferenceValue(quotaNumber(1000), nil, nil, "requests", "confirmed", ambiguous)
	require.NotNil(t, value)
	assert.Equal(t, "ambiguous", value.Status)
	assert.Nil(t, value.Total)
}

func TestParseProtocolPricingUnitSupportsScaledDenominators(t *testing.T) {
	quantity, unit, ok := parseProtocolPricingUnit("per-1M-input-tokens")
	assert.True(t, ok)
	assert.Equal(t, 1_000_000.0, quantity)
	assert.Equal(t, "input-tokens", unit)

	quantity, unit, ok = parseProtocolPricingUnit("per-request")
	assert.True(t, ok)
	assert.Equal(t, 1.0, quantity)
	assert.Equal(t, "request", unit)

	_, _, ok = parseProtocolPricingUnit("monthly")
	assert.False(t, ok)
}
