package main

import (
	"math"
	"strings"
)

type usageCost struct {
	AmountUSD  float64              `json:"amount_usd"`
	Status     string               `json:"status"`
	Source     string               `json:"source"`
	Components []usageCostComponent `json:"components,omitempty"`
}

type usageCostComponent struct {
	PricingID string  `json:"pricing_id"`
	Scope     string  `json:"scope"`
	Unit      string  `json:"unit"`
	Units     int64   `json:"units"`
	AmountUSD float64 `json:"amount_usd"`
	Status    string  `json:"status"`
}

func protocolUsageCost(definition protocolDefinition, operationID, model string, usage usageMetrics, successful bool) *usageCost {
	usage = usage.normalized()
	if !successful {
		return nil
	}
	if usage.ReportedCostUSD != nil {
		return &usageCost{AmountUSD: *usage.ReportedCostUSD, Status: "reported", Source: "provider"}
	}
	if definition.Pricing == nil {
		return nil
	}

	components := make([]usageCostComponent, 0, 4)
	estimated := false
	partial := false
	seenMetric := make(map[string]float64)
	for _, entry := range definition.Pricing.Entries {
		relevant := (entry.Scope == "operation" && entry.ID == operationID) || (entry.Scope == "model" && model != "" && entry.ID == model)
		if !relevant {
			continue
		}
		if entry.Amount == nil || entry.Currency != "USD" || (entry.Status != "confirmed" && entry.Status != "estimated") {
			partial = true
			continue
		}
		quantity, rawUnit, ok := parseProtocolPricingUnit(entry.Unit)
		if !ok {
			partial = true
			continue
		}
		unit := canonicalProtocolQuotaUnit(rawUnit)
		units, supported := usageUnitsForPricing(entry.Scope, unit, usage)
		if !supported {
			partial = true
			continue
		}
		rate := *entry.Amount / quantity
		if rate < 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
			partial = true
			continue
		}
		metric := entry.Scope + ":" + unit
		if existing, exists := seenMetric[metric]; exists {
			if !sameProtocolPricingRate(existing, rate) {
				partial = true
			} else if entry.Status == "estimated" {
				estimated = true
			}
			continue
		}
		seenMetric[metric] = rate
		component := usageCostComponent{
			PricingID: entry.ID, Scope: entry.Scope, Unit: entry.Unit, Units: units,
			AmountUSD: float64(units) * rate, Status: entry.Status,
		}
		components = append(components, component)
		estimated = estimated || entry.Status == "estimated"
	}
	if len(components) == 0 {
		return nil
	}
	cost := &usageCost{Status: "confirmed", Source: "published-pricing", Components: components}
	for _, component := range components {
		cost.AmountUSD += component.AmountUSD
	}
	if partial {
		cost.Status = "partial"
	} else if estimated {
		cost.Status = "estimated"
	}
	return cost
}

func usageUnitsForPricing(scope, unit string, usage usageMetrics) (int64, bool) {
	if scope == "model" && !usage.hasTokens() {
		return 0, false
	}
	switch unit {
	case "request":
		if scope != "operation" {
			return 0, false
		}
		return 1, true
	case "input-token", "prompt-token":
		return usage.InputTokens, scope == "model"
	case "output-token", "completion-token":
		return usage.OutputTokens, scope == "model"
	case "cached-input-token", "cache-read-input-token":
		return usage.CacheReadInputTokens, scope == "model"
	case "cache-write-input-token", "cache-creation-input-token":
		return usage.CacheWriteInputTokens, scope == "model"
	case "reasoning-token":
		return usage.ReasoningTokens, scope == "model"
	case "token", "total-token":
		return usage.TotalTokens, scope == "model"
	default:
		return 0, false
	}
}

func mergeCostStatus(current, candidate string) string {
	current = strings.TrimSpace(current)
	candidate = strings.TrimSpace(candidate)
	if current == "" || current == "unavailable" {
		return candidate
	}
	if candidate == "" || candidate == "unavailable" || current == candidate {
		return current
	}
	if current == "partial" || candidate == "partial" {
		return "partial"
	}
	// Provider-reported totals and published-price calculations have different
	// provenance. Once both appear in one model aggregate, preserve that fact
	// instead of presenting the whole aggregate as either source alone.
	if current == "reported" || candidate == "reported" {
		return "partial"
	}
	if current == "estimated" || candidate == "estimated" {
		return "estimated"
	}
	return "confirmed"
}
