package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"
	"golang.org/x/net/http2"
)

type protocolAdapterEnvelope struct {
	SchemaVersion string                 `json:"schema_version"`
	OperationID   string                 `json:"operation_id"`
	Request       protocolAdapterRequest `json:"request"`
}

type protocolAdapterRequest struct {
	Method     string              `json:"method"`
	URL        string              `json:"url"`
	Headers    map[string][]string `json:"headers"`
	BodyBase64 string              `json:"body_base64"`
}

type protocolAdapterResponse struct {
	Status     int                 `json:"status"`
	Headers    map[string][]string `json:"headers,omitempty"`
	BodyBase64 string              `json:"body_base64,omitempty"`
	Outcome    string              `json:"outcome,omitempty"`
	Error      string              `json:"error,omitempty"`
}

func (registry *protocolRegistry) forwardGRPC(c *gin.Context, definition protocolDefinition, matched protocolRouteMatch, path string) {
	route := matched.Route
	upstreamPath := path
	if route.UpstreamPath != "" {
		var err error
		upstreamPath, err = renderProtocolPath(route.UpstreamPath, matched.Params)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "cannot render upstream path"})
			return
		}
	}
	query := c.Request.URL.Query()
	for key, value := range route.RequestTransform.Query {
		query.Set(key, renderProtocolTemplate(value, matched.Params, c.Request.URL.Query()))
	}
	expressionEnv := protocolExpressionEnv(nil, matched.Params, query, c.Request.Header, c.Request.Method, 0)
	for key, expression := range route.RequestTransform.QueryExpr {
		value, evalErr := evalProtocolExpression(expression, expressionEnv)
		if evalErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "request query expression failed"})
			return
		}
		scalar, scalarErr := expressionScalar(value)
		if scalarErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "request query expression must return a scalar"})
			return
		}
		query.Set(key, scalar)
	}
	acquired, err := registry.acquireCredential(definition, route, "", map[string]bool{})
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "protocol credential is unavailable"})
		return
	}
	c.Set("localrouter_protocol_attempts", 1)
	c.Set("localrouter_protocol_credential", acquired.Credential.ID)
	status := 0
	defer func() { _ = registry.releaseCredential(definition, acquired, status, "") }()
	stopLeaseHeartbeat := registry.startCredentialLeaseHeartbeat(definition, acquired)
	defer stopLeaseHeartbeat()
	targetBase, targetName, targetOK := protocolTargetForCredential(definition, route, acquired.Credential)
	if !targetOK {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "protocol target is unavailable"})
		return
	}
	target := *targetBase
	if target.Scheme == "grpc" {
		target.Scheme = "http"
	}
	target.Path = strings.TrimSuffix(target.Path, "/") + upstreamPath
	_, upstreamProfile := protocolUpstreamProfileForRoute(definition, route, targetName)
	target.RawQuery = protocolUpstreamRawQuery(upstreamProfile, c.Request.URL.RawQuery, query)
	if targetName != "" {
		c.Set("localrouter_protocol_target", targetName)
	}
	request, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, target.String(), c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "cannot construct grpc request"})
		return
	}
	request.Header = protocolUpstreamHeaders(definition, route, targetName, c.Request.Header, nil, true, matched.Params, c.Request.URL.Query(), "preserve")
	for header, value := range route.RequestTransform.Headers {
		request.Header.Set(header, renderProtocolTemplate(value, matched.Params, c.Request.URL.Query()))
	}
	for header, expression := range route.RequestTransform.HeaderExpr {
		value, evalErr := evalProtocolExpression(expression, expressionEnv)
		if evalErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "request header expression failed"})
			return
		}
		scalar, scalarErr := expressionScalar(value)
		if scalarErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "request header expression must return a scalar"})
			return
		}
		request.Header.Set(header, scalar)
	}
	request.Header.Set("TE", "trailers")
	if err := registry.injectProtocolCredential(request, definition, acquired, nil); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": "cannot prepare upstream authentication"})
		return
	}
	var client *http.Client
	if target.Scheme == "http" {
		transport := &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, address string, _ *tls.Config) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, address)
			},
		}
		client = &http.Client{Transport: transport, CheckRedirect: rejectUpstreamRedirect}
	} else {
		client = registry.client
	}
	finishAttempt, traceErr := registry.startServiceAttempt(c, 1)
	if traceErr != nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	response, err := client.Do(request)
	attemptStatus := 0
	if response != nil {
		attemptStatus = response.StatusCode
	}
	finishAttempt(attemptStatus, true, err)
	if err != nil {
		c.Set("localrouter_protocol_outcome", "unknown")
		writeAgentError(c, routeUnknownOutcomeStatus(route), "upstream_outcome_unknown", "grpc upstream outcome is unknown", "the gRPC request may have reached the provider but no authoritative response was received", false, "provider", "reconcile provider state using the returned resource or idempotency key; do not replay blindly", nil, registry.alternativeOperationRefs(c.GetInt(tokenPolicyContextID), definition.ID, route.OperationID), gin.H{"outcome": "unknown", "operation_id": route.OperationID})
		return
	}
	defer response.Body.Close()
	status = response.StatusCode
	for header, values := range response.Header {
		if strings.EqualFold(header, "Transfer-Encoding") || strings.EqualFold(header, "Connection") {
			continue
		}
		for _, value := range values {
			c.Writer.Header().Add(header, value)
		}
	}
	for header := range response.Trailer {
		c.Writer.Header().Add("Trailer", header)
	}
	c.Status(response.StatusCode)
	buffer := make([]byte, 32<<10)
	if _, err := io.CopyBuffer(c.Writer, response.Body, buffer); err != nil {
		if trace := serviceTraceContext(c); trace != nil {
			trace.Outcome = "response_incomplete"
		}
	}
	for header, values := range response.Trailer {
		for _, value := range values {
			c.Writer.Header().Add(header, value)
		}
	}
}

func (registry *protocolRegistry) forwardAdapter(c *gin.Context, definition protocolDefinition, matched protocolRouteMatch, path string) {
	route := matched.Route
	upstreamPath := path
	if route.UpstreamPath != "" {
		var err error
		upstreamPath, err = renderProtocolPath(route.UpstreamPath, matched.Params)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "cannot render upstream path"})
			return
		}
	}
	query := c.Request.URL.Query()
	for key, value := range route.RequestTransform.Query {
		query.Set(key, renderProtocolTemplate(value, matched.Params, c.Request.URL.Query()))
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxProtocolBodyBytes))
	if err != nil {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"success": false, "message": "protocol request body is too large"})
		return
	}
	if !registry.authorizeProtocolModel(c, body) {
		return
	}
	observeServiceUnits(c, route, body, "request")
	body, err = applyProtocolJSONTransform(body, route.RequestTransform.JSON)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	expressionEnv := protocolExpressionEnv(body, matched.Params, query, c.Request.Header, c.Request.Method, 0)
	body, err = applyProtocolExpressions(body, route.RequestTransform.JSON, expressionEnv)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "request expression failed"})
		return
	}
	if !registry.authorizeProtocolModel(c, body) {
		return
	}
	expressionEnv = protocolExpressionEnv(body, matched.Params, query, c.Request.Header, c.Request.Method, 0)
	for key, expression := range route.RequestTransform.QueryExpr {
		value, evalErr := evalProtocolExpression(expression, expressionEnv)
		if evalErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "request query expression failed"})
			return
		}
		scalar, scalarErr := expressionScalar(value)
		if scalarErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "request query expression must return a scalar"})
			return
		}
		query.Set(key, scalar)
	}
	affinityKey := ""
	if route.Affinity.RequestPathParam != "" {
		affinityKey = matched.Params[route.Affinity.RequestPathParam]
	}
	maxAttempts := 1
	if definition.Pool != nil && definition.Pool.Mode == "local" {
		maxAttempts = definition.Pool.MaxAttempts
	}
	if route.Retry.MaxAttempts > 0 {
		maxAttempts = route.Retry.MaxAttempts
	}
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	idempotencyKey := ""
	if route.Retry.IdempotencyHeader != "" {
		idempotencyKey = strings.TrimSpace(c.GetHeader(route.Retry.IdempotencyHeader))
		if idempotencyKey == "" {
			idempotencyKey, err = randomSecret(16, "lr-idem-")
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot allocate idempotency key"})
				return
			}
		}
	}
	excluded := make(map[string]bool)
	for attempt := 0; attempt < maxAttempts; attempt++ {
		c.Set("localrouter_protocol_attempts", attempt+1)
		acquired, acquireErr := registry.acquireCredential(definition, route, affinityKey, excluded)
		if acquireErr != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "protocol credential is unavailable"})
			return
		}
		excluded[acquired.Credential.ID] = true
		c.Set("localrouter_protocol_credential", acquired.Credential.ID)
		providerBase, targetName, targetOK := protocolTargetForCredential(definition, route, acquired.Credential)
		if !targetOK {
			_ = registry.releaseCredential(definition, acquired, 0, "")
			c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "protocol target is unavailable"})
			return
		}
		provider := *providerBase
		provider.Path = strings.TrimSuffix(provider.Path, "/") + upstreamPath
		_, upstreamProfile := protocolUpstreamProfileForRoute(definition, route, targetName)
		provider.RawQuery = protocolUpstreamRawQuery(upstreamProfile, c.Request.URL.RawQuery, query)
		if targetName != "" {
			c.Set("localrouter_protocol_target", targetName)
		}
		providerMethod := c.Request.Method
		if route.UpstreamMethod != "" {
			providerMethod = route.UpstreamMethod
		}
		prepared, prepareErr := http.NewRequestWithContext(c.Request.Context(), providerMethod, provider.String(), bytes.NewReader(body))
		if prepareErr != nil {
			_ = registry.releaseCredential(definition, acquired, 0, "")
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "cannot construct adapter request"})
			return
		}
		prepared.Header = protocolUpstreamHeaders(definition, route, targetName, c.Request.Header, []string{"Accept", "Content-Type"}, false, matched.Params, c.Request.URL.Query(), "localrouter")
		for header, value := range route.RequestTransform.Headers {
			prepared.Header.Set(header, renderProtocolTemplate(value, matched.Params, c.Request.URL.Query()))
		}
		for header, expression := range route.RequestTransform.HeaderExpr {
			value, evalErr := evalProtocolExpression(expression, expressionEnv)
			if evalErr != nil {
				_ = registry.releaseCredential(definition, acquired, 0, "")
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "request header expression failed"})
				return
			}
			scalar, scalarErr := expressionScalar(value)
			if scalarErr != nil {
				_ = registry.releaseCredential(definition, acquired, 0, "")
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "request header expression must return a scalar"})
				return
			}
			prepared.Header.Set(header, scalar)
		}
		if route.Retry.IdempotencyHeader != "" {
			prepared.Header.Set(route.Retry.IdempotencyHeader, idempotencyKey)
		}
		if authErr := registry.injectProtocolCredential(prepared, definition, acquired, body); authErr != nil {
			_ = registry.releaseCredential(definition, acquired, 0, "")
			c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": "cannot prepare adapter authentication"})
			return
		}
		envelopeBody, _ := json.Marshal(protocolAdapterEnvelope{
			SchemaVersion: "1", OperationID: route.OperationID,
			Request: protocolAdapterRequest{Method: prepared.Method, URL: prepared.URL.String(), Headers: prepared.Header.Clone(), BodyBase64: base64.StdEncoding.EncodeToString(body)},
		})
		finishAttempt, traceErr := registry.startServiceAttempt(c, attempt+1)
		if traceErr != nil {
			c.Status(http.StatusServiceUnavailable)
			return
		}
		raw, definitelyNotSent, invokeErr := registry.invokeProtocolAdapter(c.Request.Context(), definition, route, envelopeBody)
		finishAttempt(0, !definitelyNotSent, invokeErr)
		if invokeErr != nil {
			_ = registry.releaseCredential(definition, acquired, http.StatusBadGateway, "")
			if attempt+1 < maxAttempts && routeRetryAllowed(route, providerMethod, !definitelyNotSent, idempotencyKey, true, 0) {
				continue
			}
			if !definitelyNotSent && !routeMethodNaturallyIdempotent(providerMethod) && idempotencyKey == "" {
				c.Set("localrouter_protocol_outcome", "unknown")
				writeAgentError(c, routeUnknownOutcomeStatus(route), "upstream_outcome_unknown", "adapter provider outcome is unknown", "the adapter invocation may have reached the provider but no authoritative response was received", false, "provider", "reconcile provider state using the returned resource or idempotency key; do not replay blindly", nil, registry.alternativeOperationRefs(c.GetInt(tokenPolicyContextID), definition.ID, route.OperationID), gin.H{"outcome": "unknown", "operation_id": route.OperationID})
				return
			}
			c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": "adapter invocation failed"})
			return
		}
		var adapted protocolAdapterResponse
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decodeErr := decoder.Decode(&adapted); decodeErr != nil || adapted.Status < 100 || adapted.Status > 599 || !validProtocolAdapterOutcome(adapted.Outcome) {
			_ = registry.releaseCredential(definition, acquired, http.StatusBadGateway, "")
			c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": "adapter returned an invalid envelope"})
			return
		}
		if adapted.Outcome == "unknown" {
			_ = registry.releaseCredential(definition, acquired, http.StatusBadGateway, "")
			c.Set("localrouter_protocol_outcome", "unknown")
			writeAgentError(c, routeUnknownOutcomeStatus(route), "upstream_outcome_unknown", "adapter provider outcome is unknown", "the adapter explicitly reported that the provider outcome is unknown", false, "provider", "reconcile provider state using the returned resource or idempotency key; do not replay blindly", nil, registry.alternativeOperationRefs(c.GetInt(tokenPolicyContextID), definition.ID, route.OperationID), gin.H{"outcome": "unknown", "operation_id": route.OperationID})
			return
		}
		decodedBody, decodeErr := base64.StdEncoding.DecodeString(adapted.BodyBase64)
		if decodeErr != nil || len(decodedBody) > maxProtocolBodyBytes {
			_ = registry.releaseCredential(definition, acquired, http.StatusBadGateway, "")
			c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": "adapter returned an invalid body"})
			return
		}
		if releaseErr := registry.releaseCredential(definition, acquired, adapted.Status, ""); releaseErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot persist protocol pool state"})
			return
		}
		wroteProvider := adapted.Outcome != "not_sent"
		if attempt+1 < maxAttempts && routeRetryAllowed(route, providerMethod, wroteProvider, idempotencyKey, false, adapted.Status) {
			continue
		}
		if route.Affinity.ResponseJSONPath != "" && adapted.Status >= 200 && adapted.Status < 400 {
			resourceID := gjson.GetBytes(decodedBody, route.Affinity.ResponseJSONPath).String()
			_ = registry.bindAffinity(definition, resourceID, acquired.Credential.ID, route.Affinity.TTLSeconds)
		}
		if affinityKey != "" && adapted.Status >= 200 && adapted.Status < 400 {
			_ = registry.bindAffinity(definition, affinityKey, acquired.Credential.ID, route.Affinity.TTLSeconds)
		}
		observeServiceUnits(c, route, decodedBody, "response")
		decodedBody, err = applyProtocolJSONTransform(decodedBody, route.ResponseTransform)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": "protocol response transform failed"})
			return
		}
		responseHeaders := make(http.Header, len(adapted.Headers))
		for header, values := range adapted.Headers {
			responseHeaders[http.CanonicalHeaderKey(header)] = append([]string(nil), values...)
		}
		responseEnv := protocolExpressionEnv(decodedBody, nil, nil, responseHeaders, "", adapted.Status)
		decodedBody, err = applyProtocolExpressions(decodedBody, route.ResponseTransform, responseEnv)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": "protocol response expression failed"})
			return
		}
		registry.copyProtocolResponseHeaders(c, definition, &http.Response{Header: responseHeaders})
		if !protocolJSONTransformEmpty(route.ResponseTransform) {
			c.Header("Content-Type", "application/json; charset=utf-8")
		}
		c.Status(adapted.Status)
		_, _ = c.Writer.Write(decodedBody)
		return
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "no available protocol credential"})
}

func (registry *protocolRegistry) invokeProtocolAdapter(ctx context.Context, definition protocolDefinition, route protocolRoute, envelopeBody []byte) ([]byte, bool, error) {
	if route.Adapter.Type == "wasm-envelope" {
		raw, err := invokeProtocolWASMAdapter(ctx, definition, *route.Adapter, envelopeBody)
		return raw, true, err
	}
	adapterBase := definition.ParsedTargets[route.Adapter.Target]
	adapterURL := *adapterBase
	adapterURL.Path = strings.TrimSuffix(adapterURL.Path, "/") + route.Adapter.Path
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, adapterURL.String(), bytes.NewReader(envelopeBody))
	if err != nil {
		return nil, true, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := registry.client.Do(request)
	if err != nil {
		return nil, false, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, false, errors.New("adapter invocation returned non-success status")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxProtocolBodyBytes+1))
	if err != nil || len(raw) > maxProtocolBodyBytes {
		return nil, false, errors.New("adapter response is too large")
	}
	return raw, false, nil
}

func validProtocolAdapterOutcome(outcome string) bool {
	return outcome == "" || outcome == "complete" || outcome == "not_sent" || outcome == "sent" || outcome == "unknown"
}

func (registry *protocolRegistry) forwardWebSocket(c *gin.Context, definition protocolDefinition, matched protocolRouteMatch, path string) {
	route := matched.Route
	upstreamPath := path
	if route.UpstreamPath != "" {
		var err error
		upstreamPath, err = renderProtocolPath(route.UpstreamPath, matched.Params)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "cannot render upstream path"})
			return
		}
	}
	query := c.Request.URL.Query()
	for key, value := range route.RequestTransform.Query {
		query.Set(key, renderProtocolTemplate(value, matched.Params, c.Request.URL.Query()))
	}
	expressionEnv := protocolExpressionEnv(nil, matched.Params, query, c.Request.Header, c.Request.Method, 0)
	for key, expression := range route.RequestTransform.QueryExpr {
		value, evalErr := evalProtocolExpression(expression, expressionEnv)
		if evalErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "request query expression failed"})
			return
		}
		scalar, scalarErr := expressionScalar(value)
		if scalarErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "request query expression must return a scalar"})
			return
		}
		query.Set(key, scalar)
	}
	maxAttempts := 1
	if definition.Pool != nil && definition.Pool.Mode == "local" {
		maxAttempts = definition.Pool.MaxAttempts
	}
	if route.Retry.MaxAttempts > 0 {
		maxAttempts = route.Retry.MaxAttempts
	}
	excluded := make(map[string]bool)
	var upstream *websocket.Conn
	var acquired acquiredProtocolCredential
	for attempt := 0; attempt < maxAttempts; attempt++ {
		c.Set("localrouter_protocol_attempts", attempt+1)
		candidate, err := registry.acquireCredential(definition, route, "", excluded)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "protocol credential is unavailable"})
			return
		}
		acquired = candidate
		excluded[candidate.Credential.ID] = true
		c.Set("localrouter_protocol_credential", candidate.Credential.ID)
		targetBase, targetName, targetOK := protocolTargetForCredential(definition, route, candidate.Credential)
		if !targetOK {
			_ = registry.releaseCredential(definition, candidate, 0, "")
			c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "protocol target is unavailable"})
			return
		}
		target := *targetBase
		if target.Scheme == "http" {
			target.Scheme = "ws"
		} else if target.Scheme == "https" {
			target.Scheme = "wss"
		}
		target.Path = strings.TrimSuffix(target.Path, "/") + upstreamPath
		_, upstreamProfile := protocolUpstreamProfileForRoute(definition, route, targetName)
		target.RawQuery = protocolUpstreamRawQuery(upstreamProfile, c.Request.URL.RawQuery, query)
		if targetName != "" {
			c.Set("localrouter_protocol_target", targetName)
		}
		headers := protocolUpstreamHeaders(definition, route, targetName, c.Request.Header, []string{"Origin", "User-Agent"}, false, matched.Params, c.Request.URL.Query(), "preserve")
		for header, value := range route.RequestTransform.Headers {
			headers.Set(header, renderProtocolTemplate(value, matched.Params, c.Request.URL.Query()))
		}
		for header, expression := range route.RequestTransform.HeaderExpr {
			value, evalErr := evalProtocolExpression(expression, expressionEnv)
			if evalErr != nil {
				_ = registry.releaseCredential(definition, candidate, 0, "")
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "request header expression failed"})
				return
			}
			scalar, scalarErr := expressionScalar(value)
			if scalarErr != nil {
				_ = registry.releaseCredential(definition, candidate, 0, "")
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "request header expression must return a scalar"})
				return
			}
			headers.Set(header, scalar)
		}
		authRequest := &http.Request{Method: http.MethodGet, URL: &target, Header: headers}
		if err := registry.injectProtocolCredential(authRequest, definition, candidate, nil); err != nil {
			_ = registry.releaseCredential(definition, candidate, 0, "")
			c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": "cannot prepare upstream authentication"})
			return
		}
		subprotocols := websocket.Subprotocols(c.Request)
		dialer := websocket.Dialer{HandshakeTimeout: time.Duration(definition.Timeout) * time.Second, Subprotocols: subprotocols, EnableCompression: true}
		ctx, cancel := context.WithTimeout(c.Request.Context(), time.Duration(definition.Timeout)*time.Second)
		finishAttempt, traceErr := registry.startServiceAttempt(c, attempt+1)
		if traceErr != nil {
			cancel()
			_ = registry.releaseCredential(definition, candidate, 0, "")
			c.Status(http.StatusServiceUnavailable)
			return
		}
		connection, response, dialErr := dialer.DialContext(ctx, target.String(), headers)
		cancel()
		status := http.StatusBadGateway
		if response != nil {
			status = response.StatusCode
			_ = response.Body.Close()
		}
		finishAttempt(status, true, dialErr)
		if dialErr == nil {
			upstream = connection
			break
		}
		_ = registry.releaseCredential(definition, candidate, status, "")
		if attempt+1 >= maxAttempts || !routeRetryAllowed(route, http.MethodGet, true, "", false, status) {
			c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": "websocket upstream handshake failed"})
			return
		}
	}
	if upstream == nil {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": "websocket upstream is unavailable"})
		return
	}
	stopLeaseHeartbeat := registry.startCredentialLeaseHeartbeat(definition, acquired)
	defer func() {
		stopLeaseHeartbeat()
		_ = upstream.Close()
		_ = registry.releaseCredential(definition, acquired, http.StatusOK, "")
	}()

	upgrader := websocket.Upgrader{
		CheckOrigin:       func(*http.Request) bool { return true },
		Subprotocols:      websocket.Subprotocols(c.Request),
		EnableCompression: true,
	}
	downstream, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer downstream.Close()
	upstream.SetReadLimit(maxProtocolBodyBytes)
	downstream.SetReadLimit(maxProtocolBodyBytes)

	errCh := make(chan error, 2)
	var once sync.Once
	bridge := func(destination, source *websocket.Conn) {
		for {
			messageType, payload, readErr := source.ReadMessage()
			if readErr != nil {
				errCh <- readErr
				return
			}
			if len(payload) > maxProtocolBodyBytes {
				errCh <- errors.New("websocket message exceeds protocol limit")
				return
			}
			if writeErr := destination.WriteMessage(messageType, payload); writeErr != nil {
				errCh <- writeErr
				return
			}
		}
	}
	go bridge(upstream, downstream)
	go bridge(downstream, upstream)
	<-errCh
	once.Do(func() {
		_ = downstream.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		_ = upstream.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	})
}
