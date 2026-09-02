package main

import (
	"bufio"
	"encoding/json"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const analyticsTrendHours = 24

type localAnalytics struct {
	GeneratedAt string                  `json:"generated_at"`
	Window      string                  `json:"window"`
	Totals      localAnalyticsTotals    `json:"totals"`
	Trend       []localAnalyticsBucket  `json:"trend"`
	Services    []localAnalyticsService `json:"services"`
	Models      []localAnalyticsModel   `json:"models"`
}

type localAnalyticsTotals struct {
	Requests             int64   `json:"requests"`
	ModelRequests        int64   `json:"model_requests"`
	ProtocolRequests     int64   `json:"protocol_requests"`
	Successful           int64   `json:"successful"`
	Failed               int64   `json:"failed"`
	SuccessRate          float64 `json:"success_rate"`
	PromptTokens         int64   `json:"prompt_tokens"`
	CompletionTokens     int64   `json:"completion_tokens"`
	TotalTokens          int64   `json:"total_tokens"`
	ModelCostUSD         float64 `json:"model_cost_usd"`
	ProtocolCostUSD      float64 `json:"protocol_cost_usd"`
	ProtocolPricedCalls  int64   `json:"protocol_priced_calls"`
	AverageLatencyMS     float64 `json:"average_latency_ms"`
	ProtocolP95LatencyMS float64 `json:"protocol_p95_latency_ms"`
	ActiveServices       int     `json:"active_services"`
	ActiveOperations     int     `json:"active_operations"`
}

type localAnalyticsBucket struct {
	StartedAt        string  `json:"started_at"`
	ModelRequests    int64   `json:"model_requests"`
	ProtocolRequests int64   `json:"protocol_requests"`
	Failed           int64   `json:"failed"`
	Tokens           int64   `json:"tokens"`
	CostUSD          float64 `json:"cost_usd"`
}

type localAnalyticsService struct {
	ID               string                        `json:"id"`
	Name             string                        `json:"name"`
	Kind             string                        `json:"kind"`
	Status           string                        `json:"status"`
	Requests         int64                         `json:"requests"`
	Successful       int64                         `json:"successful"`
	Failed           int64                         `json:"failed"`
	SuccessRate      float64                       `json:"success_rate"`
	AverageLatencyMS float64                       `json:"average_latency_ms"`
	Operations       int                           `json:"operations"`
	PromptTokens     int64                         `json:"prompt_tokens,omitempty"`
	CompletionTokens int64                         `json:"completion_tokens,omitempty"`
	CostUSD          float64                       `json:"cost_usd,omitempty"`
	PricedRequests   int64                         `json:"priced_requests,omitempty"`
	CostStatus       string                        `json:"cost_status"`
	Trend            []localAnalyticsServiceBucket `json:"trend"`
}

type localAnalyticsServiceBucket struct {
	Requests int64   `json:"requests"`
	Tokens   int64   `json:"tokens"`
	CostUSD  float64 `json:"cost_usd"`
}

type localAnalyticsModel struct {
	Name             string  `json:"name"`
	Requests         int64   `json:"requests"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	CostUSD          float64 `json:"cost_usd"`
}

type modelAnalyticsAggregate struct {
	ChannelID        int
	Requests         int64
	Successful       int64
	Failed           int64
	PromptTokens     int64
	CompletionTokens int64
	Quota            int64
	AverageUseTime   float64
	Operations       int
}

type modelAnalyticsModelAggregate struct {
	Name             string
	Requests         int64
	PromptTokens     int64
	CompletionTokens int64
	Quota            int64
}

type modelAnalyticsTrendAggregate struct {
	ChannelID        int
	Bucket           int64
	Requests         int64
	Failed           int64
	PromptTokens     int64
	CompletionTokens int64
	Quota            int64
}

func localAnalyticsHandler(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		analytics, err := buildLocalAnalytics(runtime, time.Now().UTC())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot calculate local analytics"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": analytics})
	}
}

func buildLocalAnalytics(runtime localRuntime, now time.Time) (localAnalytics, error) {
	analytics := localAnalytics{
		GeneratedAt: now.Format(time.RFC3339),
		Window:      "retained-all + 24h-trend",
		Trend:       newAnalyticsTrend(now),
		Services:    []localAnalyticsService{},
		Models:      []localAnalyticsModel{},
	}
	if err := addModelAnalytics(&analytics, runtime, now); err != nil {
		return localAnalytics{}, err
	}
	events, err := runtime.events.readRetained()
	if err != nil {
		return localAnalytics{}, err
	}
	definitions := runtime.protocols.analyticsDefinitions()
	addProtocolAnalytics(&analytics, events, definitions, runtime.protocols)
	finalizeLocalAnalytics(&analytics)
	return analytics, nil
}

func addModelAnalytics(analytics *localAnalytics, runtime localRuntime, now time.Time) error {
	if runtime.store == nil || runtime.store.db == nil {
		return nil
	}
	var aggregates []modelAnalyticsAggregate
	rows, err := runtime.store.db.Query(`SELECT
		channel_id,
		COUNT(*) AS requests,
		SUM(CASE WHEN type = ? THEN 1 ELSE 0 END),
		SUM(CASE WHEN type = ? THEN 1 ELSE 0 END),
		COALESCE(SUM(CASE WHEN type = ? THEN prompt_tokens ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN type = ? THEN completion_tokens ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN type = ? THEN quota ELSE 0 END), 0),
		COALESCE(AVG(CASE WHEN type = ? THEN use_time ELSE NULL END), 0),
		COUNT(DISTINCT CASE WHEN model_name <> '' THEN model_name ELSE NULL END)
		FROM logs WHERE user_id = ? AND type IN (?, ?) AND model_name <> '' GROUP BY channel_id`,
		localLogTypeConsume, localLogTypeError, localLogTypeConsume, localLogTypeConsume,
		localLogTypeConsume, localLogTypeConsume, runtime.rootUser.ID, localLogTypeConsume, localLogTypeError)
	if err != nil {
		return err
	}
	for rows.Next() {
		var aggregate modelAnalyticsAggregate
		if err := rows.Scan(&aggregate.ChannelID, &aggregate.Requests, &aggregate.Successful, &aggregate.Failed, &aggregate.PromptTokens, &aggregate.CompletionTokens, &aggregate.Quota, &aggregate.AverageUseTime, &aggregate.Operations); err != nil {
			_ = rows.Close()
			return err
		}
		aggregates = append(aggregates, aggregate)
	}
	if err := rows.Close(); err != nil {
		return err
	}

	channels, _, err := runtime.store.listChannels(1, 200)
	if err != nil {
		return err
	}
	aggregateByChannel := make(map[int]modelAnalyticsAggregate, len(aggregates))
	for _, aggregate := range aggregates {
		aggregateByChannel[aggregate.ChannelID] = aggregate
	}
	knownChannels := make(map[int]bool, len(channels))
	for _, channel := range channels {
		knownChannels[channel.ID] = true
		analytics.Services = append(analytics.Services, modelAnalyticsService(channel, aggregateByChannel[channel.ID]))
	}
	for channelID, aggregate := range aggregateByChannel {
		if knownChannels[channelID] {
			continue
		}
		analytics.Services = append(analytics.Services, modelAnalyticsService(localChannel{ID: channelID, Name: "已移除模型渠道"}, aggregate))
	}

	var modelAggregates []modelAnalyticsModelAggregate
	rows, err = runtime.store.db.Query(`SELECT
		model_name AS name,
		SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS requests,
		COALESCE(SUM(CASE WHEN type = ? THEN prompt_tokens ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN type = ? THEN completion_tokens ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN type = ? THEN quota ELSE 0 END), 0)
		FROM logs WHERE user_id = ? AND type IN (?, ?) AND model_name <> ''
		GROUP BY model_name ORDER BY requests DESC LIMIT 24`,
		localLogTypeConsume, localLogTypeConsume, localLogTypeConsume, localLogTypeConsume,
		runtime.rootUser.ID, localLogTypeConsume, localLogTypeError)
	if err != nil {
		return err
	}
	for rows.Next() {
		var aggregate modelAnalyticsModelAggregate
		if err := rows.Scan(&aggregate.Name, &aggregate.Requests, &aggregate.PromptTokens, &aggregate.CompletionTokens, &aggregate.Quota); err != nil {
			_ = rows.Close()
			return err
		}
		modelAggregates = append(modelAggregates, aggregate)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, aggregate := range modelAggregates {
		analytics.Models = append(analytics.Models, localAnalyticsModel{
			Name: aggregate.Name, Requests: aggregate.Requests,
			PromptTokens: aggregate.PromptTokens, CompletionTokens: aggregate.CompletionTokens,
			CostUSD: quotaToUSD(aggregate.Quota),
		})
	}

	trendStart := now.Truncate(time.Hour).Add(-(analyticsTrendHours - 1) * time.Hour).Unix()
	var trendAggregates []modelAnalyticsTrendAggregate
	rows, err = runtime.store.db.Query(`SELECT
		channel_id,
		(created_at / 3600),
		SUM(CASE WHEN type = ? THEN 1 ELSE 0 END),
		SUM(CASE WHEN type = ? THEN 1 ELSE 0 END),
		COALESCE(SUM(CASE WHEN type = ? THEN prompt_tokens ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN type = ? THEN completion_tokens ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN type = ? THEN quota ELSE 0 END), 0)
		FROM logs WHERE user_id = ? AND type IN (?, ?) AND model_name <> '' AND created_at >= ?
		GROUP BY channel_id, created_at / 3600`,
		localLogTypeConsume, localLogTypeError, localLogTypeConsume, localLogTypeConsume, localLogTypeConsume,
		runtime.rootUser.ID, localLogTypeConsume, localLogTypeError, trendStart)
	if err != nil {
		return err
	}
	for rows.Next() {
		var aggregate modelAnalyticsTrendAggregate
		if err := rows.Scan(&aggregate.ChannelID, &aggregate.Bucket, &aggregate.Requests, &aggregate.Failed, &aggregate.PromptTokens, &aggregate.CompletionTokens, &aggregate.Quota); err != nil {
			_ = rows.Close()
			return err
		}
		trendAggregates = append(trendAggregates, aggregate)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	trendIndex := analyticsTrendIndex(analytics.Trend)
	serviceIndex := make(map[string]int, len(analytics.Services))
	for index := range analytics.Services {
		serviceIndex[analytics.Services[index].ID] = index
	}
	for _, aggregate := range trendAggregates {
		index, ok := trendIndex[aggregate.Bucket]
		if !ok {
			continue
		}
		analytics.Trend[index].ModelRequests += aggregate.Requests
		analytics.Trend[index].Failed += aggregate.Failed
		analytics.Trend[index].Tokens += aggregate.PromptTokens + aggregate.CompletionTokens
		analytics.Trend[index].CostUSD += quotaToUSD(aggregate.Quota)
		if service, exists := serviceIndex["channel:"+strconv.Itoa(aggregate.ChannelID)]; exists {
			analytics.Services[service].Trend[index].Requests += aggregate.Requests
			analytics.Services[service].Trend[index].Tokens += aggregate.PromptTokens + aggregate.CompletionTokens
			analytics.Services[service].Trend[index].CostUSD += quotaToUSD(aggregate.Quota)
		}
	}
	return nil
}

func modelAnalyticsService(channel localChannel, aggregate modelAnalyticsAggregate) localAnalyticsService {
	name := strings.TrimSpace(channel.Name)
	if name == "" {
		name = "未命名模型渠道"
	}
	status := "disabled"
	if channel.Status == localStatusEnabled {
		status = "ready"
	}
	return localAnalyticsService{
		ID: "channel:" + strconv.Itoa(channel.ID), Name: name, Kind: "model-provider", Status: status,
		Requests: aggregate.Requests, Successful: aggregate.Successful, Failed: aggregate.Failed,
		SuccessRate:      analyticsPercent(aggregate.Successful, aggregate.Requests),
		AverageLatencyMS: aggregate.AverageUseTime * 1000, Operations: aggregate.Operations,
		PromptTokens: aggregate.PromptTokens, CompletionTokens: aggregate.CompletionTokens,
		CostUSD: quotaToUSD(aggregate.Quota), CostStatus: "measured", Trend: newAnalyticsServiceTrend(),
	}
}

func addProtocolAnalytics(analytics *localAnalytics, events []protocolEvent, definitions map[string]protocolDefinition, registry *protocolRegistry) {
	services := make(map[string]*localAnalyticsService, len(definitions))
	latencies := make(map[string][]int64, len(definitions))
	operations := make(map[string]map[string]bool, len(definitions))
	estimatedCost := make(map[string]bool, len(definitions))
	for id, definition := range definitions {
		ready, status := registry.readiness(definition)
		if ready {
			status = "ready"
		}
		services[id] = &localAnalyticsService{
			ID: "protocol:" + id, Name: definition.Name, Kind: "protocol", Status: status,
			Operations: len(definition.Routes), CostStatus: protocolPricingCoverageStatus(definition), Trend: newAnalyticsServiceTrend(),
		}
		operations[id] = make(map[string]bool)
	}
	trendIndex := analyticsTrendIndex(analytics.Trend)
	allLatencies := make([]int64, 0, len(events))
	for _, event := range events {
		service := services[event.ProtocolID]
		if service == nil {
			service = &localAnalyticsService{ID: "protocol:" + event.ProtocolID, Name: event.ProtocolID, Kind: "protocol", Status: "removed", CostStatus: "unavailable", Trend: newAnalyticsServiceTrend()}
			services[event.ProtocolID] = service
			operations[event.ProtocolID] = make(map[string]bool)
		}
		service.Requests++
		if event.Status > 0 && event.Status < http.StatusBadRequest {
			service.Successful++
		} else {
			service.Failed++
		}
		if event.LatencyMS >= 0 {
			latencies[event.ProtocolID] = append(latencies[event.ProtocolID], event.LatencyMS)
			allLatencies = append(allLatencies, event.LatencyMS)
		}
		if event.OperationID != "" {
			operations[event.ProtocolID][event.OperationID] = true
		}
		if event.Status > 0 && event.Status < http.StatusBadRequest {
			if rate, status, ok := protocolOperationRequestRate(definitions[event.ProtocolID], event.OperationID); ok {
				service.CostUSD += rate
				service.PricedRequests++
				if status == "estimated" {
					estimatedCost[event.ProtocolID] = true
				}
			}
		}
		bucket := event.CreatedAt.UTC().Truncate(time.Hour).Unix() / 3600
		if index, ok := trendIndex[bucket]; ok {
			service.Trend[index].Requests++
			analytics.Trend[index].ProtocolRequests++
			if event.Status <= 0 || event.Status >= http.StatusBadRequest {
				analytics.Trend[index].Failed++
			}
			if rate, _, ok := protocolOperationRequestRate(definitions[event.ProtocolID], event.OperationID); ok && event.Status > 0 && event.Status < http.StatusBadRequest {
				analytics.Trend[index].CostUSD += rate
				service.Trend[index].CostUSD += rate
			}
		}
	}
	for id, service := range services {
		service.SuccessRate = analyticsPercent(service.Successful, service.Requests)
		service.AverageLatencyMS = averageInt64(latencies[id])
		if service.Requests > 0 {
			if service.PricedRequests == service.Successful && service.Successful > 0 {
				service.CostStatus = "confirmed"
				if estimatedCost[id] {
					service.CostStatus = "estimated"
				}
			} else if service.PricedRequests > 0 {
				service.CostStatus = "partial"
			}
		}
		if service.Operations == 0 {
			service.Operations = len(operations[id])
		}
		analytics.Services = append(analytics.Services, *service)
	}
	analytics.Totals.ProtocolP95LatencyMS = percentile95(allLatencies)
}

func protocolPricingCoverageStatus(definition protocolDefinition) string {
	priced := 0
	estimated := false
	for _, route := range definition.Routes {
		_, status, ok := protocolOperationRequestRate(definition, route.OperationID)
		if !ok {
			continue
		}
		priced++
		if status == "estimated" {
			estimated = true
		}
	}
	if priced == 0 {
		return "unavailable"
	}
	if priced < len(definition.Routes) {
		return "partial"
	}
	if estimated {
		return "estimated"
	}
	return "confirmed"
}

func protocolOperationRequestRate(definition protocolDefinition, operationID string) (float64, string, bool) {
	if operationID == "" || definition.Pricing == nil {
		return 0, "", false
	}
	var rate *float64
	status := "confirmed"
	for _, entry := range definition.Pricing.Entries {
		if entry.Scope != "operation" || entry.ID != operationID || entry.Amount == nil || entry.Currency != "USD" || (entry.Status != "confirmed" && entry.Status != "estimated") {
			continue
		}
		quantity, unit, ok := parseProtocolPricingUnit(entry.Unit)
		if !ok || canonicalProtocolQuotaUnit(unit) != "request" {
			continue
		}
		candidate := *entry.Amount / quantity
		if candidate < 0 || math.IsNaN(candidate) || math.IsInf(candidate, 0) {
			continue
		}
		if rate != nil && !sameProtocolPricingRate(*rate, candidate) {
			return 0, "", false
		}
		copy := candidate
		rate = &copy
		if entry.Status == "estimated" {
			status = "estimated"
		}
	}
	if rate == nil {
		return 0, "", false
	}
	return *rate, status, true
}

func finalizeLocalAnalytics(analytics *localAnalytics) {
	var weightedLatency float64
	var latencyRequests int64
	operations := 0
	for index := range analytics.Services {
		service := &analytics.Services[index]
		analytics.Totals.Requests += service.Requests
		analytics.Totals.Successful += service.Successful
		analytics.Totals.Failed += service.Failed
		if service.Kind == "model-provider" {
			analytics.Totals.ModelRequests += service.Requests
			analytics.Totals.PromptTokens += service.PromptTokens
			analytics.Totals.CompletionTokens += service.CompletionTokens
			analytics.Totals.ModelCostUSD += service.CostUSD
		} else {
			analytics.Totals.ProtocolRequests += service.Requests
			analytics.Totals.ProtocolCostUSD += service.CostUSD
			analytics.Totals.ProtocolPricedCalls += service.PricedRequests
		}
		if service.Requests > 0 {
			analytics.Totals.ActiveServices++
			weightedLatency += service.AverageLatencyMS * float64(service.Successful)
			latencyRequests += service.Successful
		}
		operations += service.Operations
	}
	analytics.Totals.TotalTokens = analytics.Totals.PromptTokens + analytics.Totals.CompletionTokens
	analytics.Totals.SuccessRate = analyticsPercent(analytics.Totals.Successful, analytics.Totals.Requests)
	if latencyRequests > 0 {
		analytics.Totals.AverageLatencyMS = weightedLatency / float64(latencyRequests)
	}
	analytics.Totals.ActiveOperations = operations
	sort.Slice(analytics.Services, func(i, j int) bool {
		if analytics.Services[i].Requests == analytics.Services[j].Requests {
			return analytics.Services[i].Name < analytics.Services[j].Name
		}
		return analytics.Services[i].Requests > analytics.Services[j].Requests
	})
}

func newAnalyticsTrend(now time.Time) []localAnalyticsBucket {
	current := now.UTC().Truncate(time.Hour)
	buckets := make([]localAnalyticsBucket, 0, analyticsTrendHours)
	for offset := analyticsTrendHours - 1; offset >= 0; offset-- {
		buckets = append(buckets, localAnalyticsBucket{StartedAt: current.Add(-time.Duration(offset) * time.Hour).Format(time.RFC3339)})
	}
	return buckets
}

func newAnalyticsServiceTrend() []localAnalyticsServiceBucket {
	return make([]localAnalyticsServiceBucket, analyticsTrendHours)
}

func analyticsTrendIndex(trend []localAnalyticsBucket) map[int64]int {
	index := make(map[int64]int, len(trend))
	for position, bucket := range trend {
		startedAt, err := time.Parse(time.RFC3339, bucket.StartedAt)
		if err == nil {
			index[startedAt.Unix()/3600] = position
		}
	}
	return index
}

func (registry *protocolRegistry) analyticsDefinitions() map[string]protocolDefinition {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	definitions := make(map[string]protocolDefinition, len(registry.templates))
	for id, definition := range registry.templates {
		definitions[id] = definition
	}
	return definitions
}

func (store *protocolEventStore) readRetained() ([]protocolEvent, error) {
	if store == nil {
		return []protocolEvent{}, nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	events := make([]protocolEvent, 0)
	for _, path := range []string{store.path + ".1", store.path} {
		exists, inspectErr := inspectPrivateRegularFile(path, "protocol event file")
		if inspectErr != nil {
			return nil, inspectErr
		}
		if !exists {
			continue
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64<<10), 1<<20)
		for scanner.Scan() {
			var event protocolEvent
			if json.Unmarshal(scanner.Bytes(), &event) == nil {
				events = append(events, event)
			}
		}
		scanErr := scanner.Err()
		_ = file.Close()
		if scanErr != nil {
			return nil, scanErr
		}
	}
	return events, nil
}

func quotaToUSD(quota int64) float64 {
	if localQuotaPerUnit <= 0 {
		return 0
	}
	return float64(quota) / localQuotaPerUnit
}

func analyticsPercent(numerator, denominator int64) float64 {
	if denominator <= 0 {
		return 0
	}
	return math.Max(0, math.Min(100, float64(numerator)/float64(denominator)*100))
}

func averageInt64(values []int64) float64 {
	if len(values) == 0 {
		return 0
	}
	var total float64
	for _, value := range values {
		total += float64(value)
	}
	return total / float64(len(values))
}

func percentile95(values []int64) float64 {
	if len(values) == 0 {
		return 0
	}
	copy := append([]int64(nil), values...)
	sort.Slice(copy, func(i, j int) bool { return copy[i] < copy[j] })
	index := int(math.Ceil(float64(len(copy))*0.95)) - 1
	if index < 0 {
		index = 0
	}
	return float64(copy[index])
}
