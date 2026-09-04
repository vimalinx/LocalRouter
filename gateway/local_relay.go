package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const maxRelayBodyBytes = 64 << 20

var localRelayV1PostPaths = []string{
	"/messages", "/completions", "/chat/completions", "/responses", "/responses/compact",
	"/alpha/search", "/edits", "/images/generations", "/images/edits", "/embeddings",
	"/audio/transcriptions", "/audio/translations", "/audio/speech", "/rerank", "/moderations",
}

type localRelayBalancer struct {
	mu      sync.Mutex
	cursors map[string]uint64
}

func newLocalRelayBalancer() *localRelayBalancer {
	return &localRelayBalancer{cursors: make(map[string]uint64)}
}

func (balancer *localRelayBalancer) ordered(model string, channels []localChannel) []localChannel {
	if len(channels) < 2 {
		return channels
	}
	ordered := make([]localChannel, 0, len(channels))
	for start := 0; start < len(channels); {
		end := start + 1
		for end < len(channels) && channels[end].Priority == channels[start].Priority {
			end++
		}
		ordered = append(ordered, balancer.rotateWeighted(model+":"+strconv.Itoa(channels[start].Priority), channels[start:end])...)
		start = end
	}
	return ordered
}

func (balancer *localRelayBalancer) rotateWeighted(key string, channels []localChannel) []localChannel {
	if len(channels) < 2 {
		return append([]localChannel(nil), channels...)
	}
	balancer.mu.Lock()
	cursor := balancer.cursors[key]
	balancer.cursors[key] = cursor + 1
	balancer.mu.Unlock()

	totalWeight := uint64(0)
	for _, channel := range channels {
		weight := channel.Weight
		if weight < 1 {
			weight = 1
		}
		totalWeight += uint64(weight)
	}
	point := cursor % totalWeight
	start := 0
	for index, channel := range channels {
		weight := channel.Weight
		if weight < 1 {
			weight = 1
		}
		if point < uint64(weight) {
			start = index
			break
		}
		point -= uint64(weight)
	}
	ordered := make([]localChannel, 0, len(channels))
	ordered = append(ordered, channels[start:]...)
	ordered = append(ordered, channels[:start]...)
	return ordered
}

func localRelayTokenAuth(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		provided := bearerToken(c.GetHeader("Authorization"))
		if provided == "" {
			provided = strings.TrimSpace(c.GetHeader("x-api-key"))
		}
		if provided == "" {
			provided = strings.TrimSpace(c.GetHeader("x-goog-api-key"))
		}
		if provided == "" {
			provided = strings.TrimSpace(c.Query("key"))
		}
		token, err := runtime.store.validateToken(provided)
		if err != nil || token.UserID != runtime.rootUser.ID {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "message": "local API token required"})
			return
		}
		c.Set(tokenPolicyContextID, token.ID)
		c.Set("token_id", token.ID)
		c.Set("token_name", token.Name)
		c.Set("local_token", token)
		c.Next()
	}
}

func bearerToken(header string) string {
	parts := strings.SplitN(strings.TrimSpace(header), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func registerRelayRoutesWithRuntime(engine *gin.Engine, runtime localRuntime) {
	auth := localRelayTokenAuth(runtime)

	models := engine.Group("/v1/models")
	models.Use(auth)
	if runtime.policies != nil {
		models.Use(runtime.policies.middleware("v1"))
	}
	models.GET("", handleRelayModels(runtime))
	models.GET("/:model", handleRelayModel(runtime))

	geminiModels := engine.Group("/v1beta/models")
	geminiModels.Use(auth)
	if runtime.policies != nil {
		geminiModels.Use(runtime.policies.middleware("v1beta"))
	}
	geminiModels.GET("", handleRelayModels(runtime))
	geminiModels.GET("/:model", handleRelayModel(runtime))

	relayV1 := engine.Group("/v1")
	relayV1.Use(auth)
	if runtime.policies != nil {
		relayV1.Use(runtime.policies.middleware("v1"))
	}
	relayV1.GET("/realtime", unsupportedRealtime)
	for _, path := range localRelayV1PostPaths {
		relayV1.POST(path, handleRelayRequest(runtime))
	}
	relayV1.POST("/engines/:model/embeddings", handleRelayRequest(runtime))
	geminiRelay := engine.Group("/v1beta")
	geminiRelay.Use(auth)
	if runtime.policies != nil {
		geminiRelay.Use(runtime.policies.middleware("v1beta"))
	}
	geminiRelay.POST("/models/*path", handleRelayRequest(runtime))
}

func unsupportedRealtime(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": gin.H{"message": "realtime WebSocket relay is not enabled in the native engine", "type": "unsupported_transport"}})
}

func handleRelayModels(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		channels, err := runtime.store.enabledChannels()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "cannot load model catalogue"}})
			return
		}
		seen := make(map[string]bool)
		data := make([]gin.H, 0)
		for _, channel := range channels {
			for _, model := range strings.Split(channel.Models, ",") {
				model = strings.TrimSpace(model)
				if model == "" || model == "*" || seen[model] {
					continue
				}
				seen[model] = true
				data = append(data, gin.H{"id": model, "object": "model", "created": channel.CreatedTime, "owned_by": channel.Name})
			}
		}
		if strings.HasPrefix(c.Request.URL.Path, "/v1beta/") {
			models := make([]gin.H, 0, len(data))
			for _, entry := range data {
				models = append(models, gin.H{"name": "models/" + entry["id"].(string), "displayName": entry["id"]})
			}
			c.JSON(http.StatusOK, gin.H{"models": models})
			return
		}
		c.JSON(http.StatusOK, gin.H{"object": "list", "data": data})
	}
}

func handleRelayModel(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		model := strings.TrimSpace(c.Param("model"))
		profile, ok := runtime.channelProfiles.matchRequestPath(c.Request.URL.Path)
		if !ok {
			c.JSON(http.StatusNotImplemented, gin.H{"error": gin.H{"message": "no channel protocol profile matches this request path", "type": "unsupported_protocol"}})
			return
		}
		channels, err := matchingChannels(runtime.store, model, profile.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "cannot load model catalogue"}})
			return
		}
		if len(channels) == 0 {
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "model not found", "type": "invalid_request_error"}})
			return
		}
		c.JSON(http.StatusOK, gin.H{"id": model, "object": "model", "created": channels[0].CreatedTime, "owned_by": channels[0].Name})
	}
}

func handleRelayRequest(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxRelayBodyBytes+1))
		if err != nil || len(body) > maxRelayBodyBytes {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": gin.H{"message": "request body exceeds 64 MiB"}})
			return
		}
		model := relayModel(c, body)
		if model == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "model is required", "type": "invalid_request_error"}})
			return
		}
		profile, ok := runtime.channelProfiles.matchRequestPath(c.Request.URL.Path)
		if !ok {
			c.JSON(http.StatusNotImplemented, gin.H{"error": gin.H{"message": "no channel protocol profile matches this request path", "type": "unsupported_protocol"}})
			return
		}
		channels, err := matchingChannels(runtime.store, model, profile.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "cannot select an upstream channel"}})
			return
		}
		if len(channels) == 0 {
			writeCompatibilityRelayError(c, runtime, http.StatusServiceUnavailable, "channel_not_ready", "no enabled channel supports this model and protocol", false)
			return
		}
		channels = runtime.balancer.ordered(model, channels)
		relayAcrossChannels(c, runtime, channels, model, body)
	}
}

func relayAcrossChannels(c *gin.Context, runtime localRuntime, channels []localChannel, model string, body []byte) {
	started := time.Now()
	requestID := newRelayRequestID()
	var lastStatus int
	var lastBody []byte
	var lastErr error
	for index, channel := range channels {
		profile, ok := runtime.channelProfiles.profile(channel.Type)
		if !ok {
			lastErr = errors.New("channel references an unknown protocol profile")
			continue
		}
		upstreamRequest, err := makeUpstreamRequest(c.Request, channel, profile, body)
		if err != nil {
			lastErr = err
			continue
		}
		response, err := runtime.relayClient.Do(upstreamRequest)
		if err != nil {
			lastErr = err
			continue
		}
		if retryableRelayStatus(response.StatusCode) && index+1 < len(channels) {
			lastStatus = response.StatusCode
			lastBody, _ = io.ReadAll(io.LimitReader(response.Body, 1<<20))
			_ = response.Body.Close()
			continue
		}
		copyRelayHeaders(c.Writer.Header(), response.Header)
		c.Header("X-LocalRouter-Channel", strconv.Itoa(channel.ID))
		c.Header("X-Request-Id", requestID)
		c.Status(response.StatusCode)
		usage, streamed, copyErr := copyRelayResponse(c, response)
		entryType := localLogTypeConsume
		content := "request completed"
		if response.StatusCode < 200 || response.StatusCode >= 300 || copyErr != nil {
			entryType = localLogTypeError
			content = "upstream request failed"
		}
		logRelay(runtime, c, channel, model, requestID, started, usage, streamed, entryType, content)
		return
	}
	status := http.StatusBadGateway
	if lastStatus != 0 {
		status = lastStatus
	}
	message := "all matching upstream channels failed"
	if lastErr != nil {
		message += ": " + lastErr.Error()
	}
	if len(lastBody) > 0 {
		copyRelayError(c, status, lastBody)
	} else {
		writeCompatibilityRelayError(c, runtime, status, "upstream_unavailable", message, true)
	}
	logRelay(runtime, c, channels[len(channels)-1], model, requestID, started, usageMetrics{}, false, localLogTypeError, message)
}

func writeCompatibilityRelayError(c *gin.Context, runtime localRuntime, status int, code, reason string, retryable bool) {
	query := compatibilityAlternativeQuery(c.Request.URL.Path)
	alternatives := []agentOperationRef{}
	if runtime.protocols != nil {
		alternatives = runtime.protocols.readyOperationRefs(c.GetInt(tokenPolicyContextID), query, 3)
	}
	nextAction := "run lr tree and choose a ready Pack, or ask the operator to configure an eligible Channel for this compatibility Pack"
	if len(alternatives) > 0 {
		nextAction = "choose one published ready alternative explicitly, then use its call_url or lr describe/preflight/call flow"
	}
	message := "the standard compatibility Pack is not ready for this request"
	c.JSON(status, gin.H{
		"success": false, "code": code, "message": message, "reason": reason,
		"retryable": retryable, "owner": "localrouter", "retry_after": nil,
		"next_action": nextAction, "alternatives": alternatives,
		"error": gin.H{"message": reason, "type": "upstream_unavailable", "code": code},
	})
}

func compatibilityAlternativeQuery(requestPath string) string {
	path := strings.TrimPrefix(requestPath, "/v1beta")
	path = strings.TrimPrefix(path, "/v1")
	switch {
	case strings.Contains(path, "/chat/completions"):
		return "chat.completions"
	case strings.Contains(path, "/responses"):
		return "responses"
	case strings.Contains(path, "/models"):
		return "ai.models"
	case strings.Contains(path, "/embeddings"):
		return "embeddings"
	case strings.Contains(path, "/images"):
		return "images"
	case strings.Contains(path, "/audio"):
		return "audio"
	default:
		return strings.Trim(strings.ReplaceAll(path, "/", "."), ".")
	}
}

func matchingChannels(store *localStore, model string, profileID int) ([]localChannel, error) {
	channels, err := store.enabledChannels()
	if err != nil {
		return nil, err
	}
	result := make([]localChannel, 0)
	for _, channel := range channels {
		if channel.Type != profileID || !channelSupportsModel(channel.Models, model) {
			continue
		}
		result = append(result, channel)
	}
	return result, nil
}

func channelSupportsModel(models, requested string) bool {
	for _, candidate := range strings.Split(models, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || candidate == requested {
			return true
		}
		if strings.HasSuffix(candidate, "*") && strings.HasPrefix(requested, strings.TrimSuffix(candidate, "*")) {
			return true
		}
	}
	return false
}

func relayModel(c *gin.Context, body []byte) string {
	if model := strings.TrimSpace(c.Param("model")); model != "" {
		return model
	}
	if path := strings.Trim(c.Param("path"), "/"); path != "" {
		return strings.SplitN(path, ":", 2)[0]
	}
	var document struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &document)
	return strings.TrimSpace(document.Model)
}

func makeUpstreamRequest(incoming *http.Request, channel localChannel, profile localChannelProfile, body []byte) (*http.Request, error) {
	query := cleanedRelayQuery(incoming.URL.Query())
	rawQuery := query.Encode()
	if channelHasUpstreamProfile(channel.UpstreamProfile) && channel.UpstreamProfile.Query == "preserve-raw" {
		rawQuery = removeRawQueryParameter(incoming.URL.RawQuery, "key")
	}
	target, err := relayTargetURL(channel.BaseURL, incoming.URL.Path, rawQuery)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(incoming.Context(), incoming.Method, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	copyRelayRequestHeaders(request.Header, incoming.Header)
	applyChannelUpstreamProfile(request.Header, incoming.Header, channel.UpstreamProfile)
	if err := applyLocalProfileAuthorization(request, channel.Key, profile); err != nil {
		return nil, err
	}
	request.ContentLength = int64(len(body))
	return request, nil
}

func removeRawQueryParameter(rawQuery, blocked string) string {
	parts := strings.Split(rawQuery, "&")
	kept := parts[:0]
	for _, part := range parts {
		name := strings.SplitN(part, "=", 2)[0]
		decoded, err := url.QueryUnescape(name)
		if err == nil && decoded == blocked {
			continue
		}
		kept = append(kept, part)
	}
	return strings.Join(kept, "&")
}

func applyChannelUpstreamProfile(destination, source http.Header, profile protocolUpstreamProfile) {
	if !channelHasUpstreamProfile(profile) {
		return
	}
	for _, header := range profile.ForwardHeaders {
		if values := source.Values(header); len(values) > 0 {
			destination[header] = append([]string(nil), values...)
		}
	}
	for header, value := range profile.SetHeaders {
		destination.Set(header, value)
	}
	for _, header := range profile.RemoveHeaders {
		destination.Del(header)
	}
	switch profile.UserAgent {
	case "omit":
		destination["User-Agent"] = []string{}
	case "localrouter":
		destination.Set("User-Agent", "LocalRouter/2 model-channel")
	case "configured":
		// Already set above.
	case "inherit", "preserve":
		if values := source.Values("User-Agent"); len(values) > 0 {
			destination["User-Agent"] = append([]string(nil), values...)
		} else if profile.UserAgent == "preserve" {
			destination["User-Agent"] = []string{}
		}
	}
}

func cleanedRelayQuery(query url.Values) url.Values {
	cleaned := make(url.Values, len(query))
	for key, values := range query {
		cleaned[key] = append([]string(nil), values...)
	}
	cleaned.Del("key")
	return cleaned
}

func relayTargetURL(baseURL, requestPath, rawQuery string) (string, error) {
	base, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", errors.New("channel has an invalid base URL")
	}
	basePath := strings.TrimRight(base.Path, "/")
	path := requestPath
	for _, version := range []string{"/v1beta", "/v1"} {
		if strings.HasSuffix(basePath, version) && (path == version || strings.HasPrefix(path, version+"/")) {
			path = strings.TrimPrefix(path, version)
			break
		}
	}
	base.Path = basePath + "/" + strings.TrimLeft(path, "/")
	base.RawQuery = rawQuery
	return base.String(), nil
}

func copyRelayRequestHeaders(destination, source http.Header) {
	for name, values := range source {
		if relayRequestHeaderBlocked(name) {
			continue
		}
		for _, value := range values {
			destination.Add(name, value)
		}
	}
}

func relayRequestHeaderBlocked(name string) bool {
	switch strings.ToLower(name) {
	case "authorization", "x-api-key", "x-goog-api-key", "host", "connection", "proxy-connection", "keep-alive", "transfer-encoding", "te", "trailer", "upgrade":
		return true
	default:
		return false
	}
}

func relayRequestHeaderHopByHop(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "host", "connection", "proxy-connection", "keep-alive", "transfer-encoding", "te", "trailer", "upgrade", "content-length":
		return true
	default:
		return false
	}
}

func copyRelayHeaders(destination, source http.Header) {
	for name, values := range source {
		switch strings.ToLower(name) {
		case "connection", "proxy-connection", "keep-alive", "transfer-encoding", "te", "trailer", "upgrade", "content-length":
			continue
		}
		for _, value := range values {
			destination.Add(name, value)
		}
	}
}

func copyRelayResponse(c *gin.Context, response *http.Response) (usageMetrics, bool, error) {
	defer response.Body.Close()
	streamed := strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream")
	if !streamed {
		body, err := io.ReadAll(io.LimitReader(response.Body, maxRelayBodyBytes+1))
		if err != nil {
			return usageMetrics{}, false, err
		}
		if len(body) > maxRelayBodyBytes {
			return usageMetrics{}, false, errors.New("upstream response exceeds 64 MiB")
		}
		_, err = c.Writer.Write(body)
		return parseUsageMetrics(body), false, err
	}

	capture := &limitedCaptureWriter{limit: 2 << 20}
	writer := io.MultiWriter(c.Writer, capture)
	buffer := make([]byte, 32<<10)
	for {
		count, readErr := response.Body.Read(buffer)
		if count > 0 {
			if _, err := writer.Write(buffer[:count]); err != nil {
				return parseStreamUsageMetrics(capture.Bytes()), true, err
			}
			c.Writer.Flush()
		}
		if errors.Is(readErr, io.EOF) {
			return parseStreamUsageMetrics(capture.Bytes()), true, nil
		}
		if readErr != nil {
			return parseStreamUsageMetrics(capture.Bytes()), true, readErr
		}
	}
}

type limitedCaptureWriter struct {
	data  []byte
	limit int
}

func (writer *limitedCaptureWriter) Write(data []byte) (int, error) {
	if writer.limit <= 0 {
		return len(data), nil
	}
	if len(data) >= writer.limit {
		writer.data = append(writer.data[:0], data[len(data)-writer.limit:]...)
		return len(data), nil
	}
	writer.data = append(writer.data, data...)
	if overflow := len(writer.data) - writer.limit; overflow > 0 {
		copy(writer.data, writer.data[overflow:])
		writer.data = writer.data[:writer.limit]
	}
	return len(data), nil
}

func (writer *limitedCaptureWriter) Bytes() []byte { return writer.data }

func retryableRelayStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
}

func copyRelayError(c *gin.Context, status int, body []byte) {
	if json.Valid(body) {
		c.Data(status, "application/json", body)
		return
	}
	c.JSON(status, gin.H{"error": gin.H{"message": strings.TrimSpace(string(body)), "type": "upstream_error"}})
}

func logRelay(runtime localRuntime, c *gin.Context, channel localChannel, model, requestID string, started time.Time, usage usageMetrics, streamed bool, entryType int, content string) {
	token, _ := c.Get("local_token")
	local, _ := token.(*localToken)
	entry := localRequestLog{
		UserID: runtime.rootUser.ID, CreatedAt: time.Now().Unix(), Type: entryType, Content: content,
		Username: runtime.rootUser.Username, ModelName: model, PromptTokens: usage.InputTokens,
		CompletionTokens: usage.OutputTokens, CachedInputTokens: usage.CacheReadInputTokens,
		CacheWriteInputTokens: usage.CacheWriteInputTokens, ReasoningTokens: usage.ReasoningTokens,
		TotalTokens: usage.TotalTokens, UseTime: int64(time.Since(started).Seconds()), IsStream: streamed,
		ChannelID: channel.ID, ChannelName: channel.Name, RequestID: requestID, Group: "default",
	}
	if usage.ReportedCostUSD != nil {
		entry.CostUSD = *usage.ReportedCostUSD
		entry.CostStatus = "reported"
	}
	if local != nil {
		entry.TokenID = local.ID
		entry.TokenName = local.Name
	}
	_ = runtime.store.logRequest(context.Background(), entry)
}

func newRelayRequestID() string {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return "lr-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return "lr-" + hex.EncodeToString(buffer)
}

func fetchChannelModels(ctx context.Context, client *http.Client, store *localStore, profiles *localChannelProfileRegistry, id int) ([]string, int, error) {
	channel, err := store.channel(id)
	if err != nil {
		return nil, 0, err
	}
	profile, ok := profiles.profile(channel.Type)
	if !ok {
		return nil, 0, errors.New("channel references an unknown protocol profile")
	}
	target, err := relayTargetURL(channel.BaseURL, profile.ModelCatalogue.Path, "")
	if err != nil {
		return nil, 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Accept", "application/json")
	applyChannelUpstreamProfile(request.Header, nil, channel.UpstreamProfile)
	if err := applyLocalProfileAuthorization(request, channel.Key, profile); err != nil {
		return nil, 0, err
	}
	started := time.Now()
	response, err := client.Do(request)
	latency := int(time.Since(started).Milliseconds())
	if err != nil {
		return nil, latency, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return nil, latency, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, latency, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	models := parseProfileModels(body, profile)
	if len(models) == 0 && channel.Models != "" {
		models = strings.Split(channel.Models, ",")
	}
	if len(models) == 0 {
		return nil, latency, errors.New("upstream returned no models")
	}
	return models, latency, nil
}
