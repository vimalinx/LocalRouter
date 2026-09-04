package main

import (
	"encoding/json"
	"math"
	"strings"

	"github.com/tidwall/gjson"
)

// usageMetrics is the provider-neutral usage shape persisted by LocalRouter.
// Cache and reasoning counts are dimensions of input/output usage and must not
// be added to TotalTokens a second time.
type usageMetrics struct {
	InputTokens           int64    `json:"input_tokens,omitempty"`
	OutputTokens          int64    `json:"output_tokens,omitempty"`
	CacheReadInputTokens  int64    `json:"cache_read_input_tokens,omitempty"`
	CacheWriteInputTokens int64    `json:"cache_write_input_tokens,omitempty"`
	ReasoningTokens       int64    `json:"reasoning_tokens,omitempty"`
	TotalTokens           int64    `json:"total_tokens,omitempty"`
	ReportedCostUSD       *float64 `json:"reported_cost_usd,omitempty"`
}

func (usage usageMetrics) normalized() usageMetrics {
	usage.InputTokens = nonNegativeInt64(usage.InputTokens)
	usage.OutputTokens = nonNegativeInt64(usage.OutputTokens)
	usage.CacheReadInputTokens = nonNegativeInt64(usage.CacheReadInputTokens)
	usage.CacheWriteInputTokens = nonNegativeInt64(usage.CacheWriteInputTokens)
	usage.ReasoningTokens = nonNegativeInt64(usage.ReasoningTokens)
	usage.TotalTokens = nonNegativeInt64(usage.TotalTokens)
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	if usage.ReportedCostUSD != nil && (*usage.ReportedCostUSD < 0 || math.IsNaN(*usage.ReportedCostUSD) || math.IsInf(*usage.ReportedCostUSD, 0)) {
		usage.ReportedCostUSD = nil
	}
	return usage
}

func nonNegativeInt64(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

func (usage usageMetrics) hasTokens() bool {
	return usage.InputTokens > 0 || usage.OutputTokens > 0 || usage.CacheReadInputTokens > 0 || usage.CacheWriteInputTokens > 0 || usage.ReasoningTokens > 0 || usage.TotalTokens > 0
}

func parseUsageMetrics(body []byte) usageMetrics {
	if len(body) == 0 || !json.Valid(body) {
		return usageMetrics{}
	}
	usage := usageMetrics{
		InputTokens: firstUsageInt(body,
			"usage.prompt_tokens", "usage.input_tokens", "usage.inputTokens", "usageMetadata.promptTokenCount"),
		OutputTokens: firstUsageInt(body,
			"usage.completion_tokens", "usage.output_tokens", "usage.outputTokens", "usageMetadata.candidatesTokenCount"),
		CacheReadInputTokens: firstUsageInt(body,
			"usage.prompt_tokens_details.cached_tokens", "usage.input_tokens_details.cached_tokens", "usage.cache_read_input_tokens", "usage.cacheReadInputTokens", "usageMetadata.cachedContentTokenCount"),
		CacheWriteInputTokens: firstUsageInt(body,
			"usage.cache_creation_input_tokens", "usage.cacheCreationInputTokens"),
		ReasoningTokens: firstUsageInt(body,
			"usage.completion_tokens_details.reasoning_tokens", "usage.output_tokens_details.reasoning_tokens", "usage.reasoning_tokens", "usage.reasoningTokens", "usageMetadata.thoughtsTokenCount"),
		TotalTokens: firstUsageInt(body,
			"usage.total_tokens", "usage.totalTokens", "usageMetadata.totalTokenCount"),
	}
	if value, ok := firstUsageFloat(body, "usage.cost_usd", "usage.total_cost_usd"); ok {
		usage.ReportedCostUSD = &value
	}
	return usage.normalized()
}

func firstUsageInt(body []byte, paths ...string) int64 {
	for _, path := range paths {
		value := gjson.GetBytes(body, path)
		if !value.Exists() || value.Type != gjson.Number || value.Num < 0 || math.IsNaN(value.Num) || math.IsInf(value.Num, 0) {
			continue
		}
		return int64(value.Num)
	}
	return 0
}

func firstUsageFloat(body []byte, paths ...string) (float64, bool) {
	for _, path := range paths {
		value := gjson.GetBytes(body, path)
		if !value.Exists() || value.Type != gjson.Number || value.Num < 0 || math.IsNaN(value.Num) || math.IsInf(value.Num, 0) {
			continue
		}
		return value.Num, true
	}
	return 0, false
}

func parseStreamUsageMetrics(body []byte) usageMetrics {
	best := usageMetrics{}
	for _, line := range strings.Split(string(body), "\n") {
		candidate := strings.TrimSpace(line)
		if strings.HasPrefix(candidate, "data:") {
			candidate = strings.TrimSpace(strings.TrimPrefix(candidate, "data:"))
		}
		if candidate == "" || candidate == "[DONE]" {
			continue
		}
		parsed := parseUsageMetrics([]byte(candidate))
		if usageInformationScore(parsed) >= usageInformationScore(best) {
			best = parsed
		}
	}
	return best.normalized()
}

func usageInformationScore(usage usageMetrics) int64 {
	usage = usage.normalized()
	score := usage.TotalTokens * 10
	if usage.CacheReadInputTokens > 0 {
		score++
	}
	if usage.CacheWriteInputTokens > 0 {
		score++
	}
	if usage.ReasoningTokens > 0 {
		score++
	}
	if usage.ReportedCostUSD != nil {
		score++
	}
	return score
}

func requestModel(body []byte) string {
	if len(body) == 0 || !json.Valid(body) {
		return ""
	}
	model := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if len(model) > 256 {
		return ""
	}
	return model
}
