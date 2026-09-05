package main

import (
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
)

type localAgentUsage struct {
	TokenID               int     `json:"token_id"`
	TokenName             string  `json:"token_name"`
	AgentCode             string  `json:"agent_code"`
	AgentName             string  `json:"agent_name"`
	Workspace             string  `json:"workspace"`
	Runtime               string  `json:"runtime"`
	Status                int     `json:"status"`
	Registered            bool    `json:"registered"`
	System                bool    `json:"system"`
	Requests              int64   `json:"requests"`
	Successful            int64   `json:"successful"`
	Failed                int64   `json:"failed"`
	TodayRequests         int64   `json:"today_requests"`
	PromptTokens          int64   `json:"prompt_tokens"`
	CompletionTokens      int64   `json:"completion_tokens"`
	CachedInputTokens     int64   `json:"cached_input_tokens"`
	CacheWriteInputTokens int64   `json:"cache_write_input_tokens"`
	ReasoningTokens       int64   `json:"reasoning_tokens"`
	CostUSD               float64 `json:"cost_usd"`
	CostStatus            string  `json:"cost_status"`
	LastUsedAt            int64   `json:"last_used_at"`
	RequestsPerMinute     int     `json:"requests_per_minute"`
	DailyRequestLimit     int     `json:"daily_request_limit"`
	MaxInFlight           int     `json:"max_in_flight"`
	pricedSuccessful      int64
	estimatedCost         bool
	reportedCost          bool
	publishedCost         bool
}

type localAgentModelAggregate struct {
	TokenID               int
	Requests              int64
	Successful            int64
	Failed                int64
	TodayRequests         int64
	PromptTokens          int64
	CompletionTokens      int64
	CachedInputTokens     int64
	CacheWriteInputTokens int64
	ReasoningTokens       int64
	Quota                 int64
	ReportedCostUSD       float64
	PricedRequests        int64
	EstimatedRequests     int64
	ReportedRequests      int64
	LastUsedAt            int64
}

func localAgentUsageHandler(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		usage, err := buildLocalAgentUsage(runtime, time.Now().UTC())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot calculate Agent usage"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": usage})
	}
}

func buildLocalAgentUsage(runtime localRuntime, now time.Time) ([]localAgentUsage, error) {
	if runtime.store == nil || runtime.store.db == nil {
		return []localAgentUsage{}, nil
	}
	tokens, err := runtime.store.allTokens(runtime.rootUser.ID)
	if err != nil {
		return nil, err
	}
	usage := make([]localAgentUsage, 0, len(tokens))
	index := make(map[int]int, len(tokens))
	for _, token := range tokens {
		item := localAgentUsage{
			TokenID: token.ID, TokenName: token.Name, AgentCode: token.AgentCode, AgentName: token.AgentName,
			Workspace: token.Workspace, Runtime: token.Runtime, Status: token.Status,
			Registered: token.AgentCode != "" && token.Workspace != "", System: token.Name == localTokenName,
			CostStatus: "unavailable", LastUsedAt: token.AccessedTime,
		}
		if runtime.policies != nil {
			if policy, exists := runtime.policies.policyFor(token.ID); exists {
				item.RequestsPerMinute = policy.RequestsPerMinute
				item.DailyRequestLimit = policy.DailyRequestLimit
				item.MaxInFlight = policy.MaxInFlight
			}
		}
		index[token.ID] = len(usage)
		usage = append(usage, item)
	}

	dayStart := now.UTC().Truncate(24 * time.Hour).Unix()
	rows, err := runtime.store.db.Query(`SELECT
		token_id,
		COUNT(*),
		SUM(CASE WHEN type = ? THEN 1 ELSE 0 END),
		SUM(CASE WHEN type = ? THEN 1 ELSE 0 END),
		SUM(CASE WHEN created_at >= ? THEN 1 ELSE 0 END),
		COALESCE(SUM(CASE WHEN type = ? THEN prompt_tokens ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN type = ? THEN completion_tokens ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN type = ? THEN cached_input_tokens ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN type = ? THEN cache_write_input_tokens ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN type = ? THEN reasoning_tokens ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN type = ? THEN quota ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN type = ? THEN cost_usd ELSE 0 END), 0),
		SUM(CASE WHEN type = ? AND (quota > 0 OR cost_status IN ('confirmed', 'estimated', 'reported')) THEN 1 ELSE 0 END),
		SUM(CASE WHEN type = ? AND cost_status = 'estimated' THEN 1 ELSE 0 END),
		SUM(CASE WHEN type = ? AND cost_status = 'reported' THEN 1 ELSE 0 END),
		COALESCE(MAX(created_at), 0)
		FROM logs WHERE user_id = ? AND type IN (?, ?) AND token_id > 0 GROUP BY token_id`,
		localLogTypeConsume, localLogTypeError, dayStart, localLogTypeConsume, localLogTypeConsume,
		localLogTypeConsume, localLogTypeConsume, localLogTypeConsume, localLogTypeConsume,
		localLogTypeConsume, localLogTypeConsume, localLogTypeConsume, localLogTypeConsume,
		runtime.rootUser.ID, localLogTypeConsume, localLogTypeError)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var aggregate localAgentModelAggregate
		if err := rows.Scan(&aggregate.TokenID, &aggregate.Requests, &aggregate.Successful, &aggregate.Failed, &aggregate.TodayRequests, &aggregate.PromptTokens, &aggregate.CompletionTokens, &aggregate.CachedInputTokens, &aggregate.CacheWriteInputTokens, &aggregate.ReasoningTokens, &aggregate.Quota, &aggregate.ReportedCostUSD, &aggregate.PricedRequests, &aggregate.EstimatedRequests, &aggregate.ReportedRequests, &aggregate.LastUsedAt); err != nil {
			_ = rows.Close()
			return nil, err
		}
		position, exists := index[aggregate.TokenID]
		if !exists {
			continue
		}
		item := &usage[position]
		item.Requests += aggregate.Requests
		item.Successful += aggregate.Successful
		item.Failed += aggregate.Failed
		item.TodayRequests += aggregate.TodayRequests
		item.PromptTokens += aggregate.PromptTokens
		item.CompletionTokens += aggregate.CompletionTokens
		item.CachedInputTokens += aggregate.CachedInputTokens
		item.CacheWriteInputTokens += aggregate.CacheWriteInputTokens
		item.ReasoningTokens += aggregate.ReasoningTokens
		item.CostUSD += quotaToUSD(aggregate.Quota) + aggregate.ReportedCostUSD
		item.pricedSuccessful += aggregate.PricedRequests
		item.estimatedCost = item.estimatedCost || aggregate.EstimatedRequests > 0
		item.reportedCost = item.reportedCost || aggregate.ReportedRequests > 0
		if aggregate.LastUsedAt > item.LastUsedAt {
			item.LastUsedAt = aggregate.LastUsedAt
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	events, err := runtime.events.readRetained()
	if err != nil {
		return nil, err
	}
	definitions := map[string]protocolDefinition{}
	if runtime.protocols != nil {
		definitions = runtime.protocols.analyticsDefinitions()
	}
	for _, event := range events {
		position, exists := index[event.TokenID]
		if !exists {
			continue
		}
		item := &usage[position]
		item.Requests++
		if event.CreatedAt.UTC().Unix() >= dayStart {
			item.TodayRequests++
		}
		if event.Status > 0 && event.Status < http.StatusBadRequest {
			item.Successful++
			if cost := protocolEventCost(event, definitions[event.ProtocolID]); cost != nil {
				item.CostUSD += cost.AmountUSD
				item.pricedSuccessful++
				item.estimatedCost = item.estimatedCost || cost.Status == "estimated"
				item.reportedCost = item.reportedCost || cost.Status == "reported"
				item.publishedCost = item.publishedCost || cost.Source == "published-pricing"
			}
		} else {
			item.Failed++
		}
		if event.Usage != nil {
			metrics := event.Usage.normalized()
			item.PromptTokens += metrics.InputTokens
			item.CompletionTokens += metrics.OutputTokens
			item.CachedInputTokens += metrics.CacheReadInputTokens
			item.CacheWriteInputTokens += metrics.CacheWriteInputTokens
			item.ReasoningTokens += metrics.ReasoningTokens
		}
		if usedAt := event.CreatedAt.UTC().Unix(); usedAt > item.LastUsedAt {
			item.LastUsedAt = usedAt
		}
	}

	for position := range usage {
		item := &usage[position]
		switch {
		case item.Successful == 0:
			item.CostStatus = "unavailable"
		case item.pricedSuccessful == item.Successful && item.reportedCost && !item.publishedCost:
			item.CostStatus = "reported"
		case item.pricedSuccessful == item.Successful && item.estimatedCost:
			item.CostStatus = "estimated"
		case item.pricedSuccessful == item.Successful:
			item.CostStatus = "measured"
		case item.pricedSuccessful > 0:
			item.CostStatus = "partial"
		default:
			item.CostStatus = "unavailable"
		}
	}
	sort.Slice(usage, func(left, right int) bool {
		if usage[left].System != usage[right].System {
			return !usage[left].System
		}
		if usage[left].Requests != usage[right].Requests {
			return usage[left].Requests > usage[right].Requests
		}
		return usage[left].AgentCode < usage[right].AgentCode
	})
	return usage, nil
}
