package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (registry *protocolRegistry) probeReadiness(definition protocolDefinition) (bool, string) {
	config := *definition.Readiness
	interval := config.IntervalSeconds
	if interval == 0 {
		interval = 60
	}
	now := time.Now().UTC()
	registry.readinessMu.Lock()
	if cached, ok := registry.readinessCache[definition.ID]; ok && now.Before(cached.ExpiresAt) {
		registry.readinessMu.Unlock()
		return cached.Ready, cached.Status
	}
	registry.readinessMu.Unlock()

	ready, status := registry.executeReadinessProbe(definition, config)
	if !ready && interval > 5 {
		// A failed pooled probe may have quarantined one or more bad
		// credentials. Recheck promptly so a healthy credential behind that
		// tranche can restore the Pack without waiting for the normal healthy
		// cache interval.
		interval = 5
	}
	registry.readinessMu.Lock()
	registry.readinessCache[definition.ID] = protocolReadinessRuntime{
		Ready: ready, Status: status, CheckedAt: now, ExpiresAt: now.Add(time.Duration(interval) * time.Second),
	}
	registry.readinessMu.Unlock()
	return ready, status
}

func (registry *protocolRegistry) executeReadinessProbe(definition protocolDefinition, config protocolReadinessConfig) (bool, string) {
	target := definition.ParsedBaseURL
	if config.Target != "" {
		target = definition.ParsedTargets[config.Target]
	}
	if target == nil || (target.Scheme != "http" && target.Scheme != "https") {
		return false, "probe-invalid-target"
	}
	probeURL := *target
	probeURL.Path = strings.TrimSuffix(probeURL.Path, "/") + config.Path
	method := strings.ToUpper(config.Method)
	if method == "" {
		method = http.MethodGet
	}
	timeout := config.TimeoutSeconds
	if timeout == 0 {
		timeout = 5
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()
	route := protocolRoute{OperationID: "readiness.probe", Methods: []string{method}, Path: config.Path, Transport: "http"}
	maxAttempts := 1
	if definition.Pool != nil && definition.Pool.Mode == "local" && definition.Pool.MaxAttempts > maxAttempts {
		maxAttempts = definition.Pool.MaxAttempts
	}
	excluded := make(map[string]bool, maxAttempts)
	lastStatus := "probe-credential-unavailable"
	for range maxAttempts {
		acquired, err := registry.acquireCredential(definition, route, "", excluded)
		if err != nil {
			break
		}
		if acquired.Pooled {
			excluded[acquired.Credential.ID] = true
		}
		request, err := http.NewRequestWithContext(ctx, method, probeURL.String(), bytes.NewReader(nil))
		if err != nil {
			_ = registry.releaseCredential(definition, acquired, 0, "")
			return false, "probe-request-invalid"
		}
		request.Header.Set("Accept", "application/json")
		if err := registry.injectProtocolCredential(request, definition, acquired, nil); err != nil {
			_ = registry.releaseCredential(definition, acquired, 0, "")
			lastStatus = "probe-auth-failed"
			continue
		}
		response, err := registry.client.Do(request)
		if err != nil {
			_ = registry.releaseCredential(definition, acquired, http.StatusBadGateway, "")
			lastStatus = "probe-transport-failed"
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		_ = response.Body.Close()
		_ = registry.releaseCredential(definition, acquired, response.StatusCode, response.Header.Get("Retry-After"))
		if readErr != nil {
			lastStatus = "probe-read-failed"
			continue
		}
		statusAllowed := response.StatusCode >= 200 && response.StatusCode < 300
		if len(config.SuccessStatuses) > 0 {
			statusAllowed = false
			for _, candidate := range config.SuccessStatuses {
				if response.StatusCode == candidate {
					statusAllowed = true
					break
				}
			}
		}
		if !statusAllowed {
			lastStatus = "probe-unhealthy"
			continue
		}
		if config.Expression != "" {
			env := protocolExpressionEnv(body, nil, url.Values{}, response.Header, method, response.StatusCode)
			value, evalErr := evalProtocolExpression(config.Expression, env)
			if evalErr != nil {
				return false, "probe-expression-failed"
			}
			accepted, ok := value.(bool)
			if !ok || !accepted {
				lastStatus = "probe-unhealthy"
				continue
			}
		}
		return true, "ready"
	}
	return false, lastStatus
}
