package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
)

// Internal projections reuse the real proxy handler with server-owned identity
// context. No root bearer credential, unsigned identity header or second public
// listener is used to impersonate a workflow/MCP caller.
func callOwnedProtocol(runtime localRuntime, pack, method, path, query, contentType string, body []byte) (int, []byte, string, error) {
	token, err := runtime.store.tokenByID(runtime.rootUser.ID, runtime.callOwner, false)
	if err != nil || token.Status != localStatusEnabled || (token.ExpiredTime >= 0 && token.ExpiredTime <= time.Now().Unix()) {
		return http.StatusForbidden, nil, "", errors.New("workflow owner Token is unavailable")
	}
	if runtime.policies.hasCapability(token.ID, localRouterMaintainCapability) {
		return http.StatusForbidden, nil, "", errors.New("maintenance Token cannot call services")
	}
	ctx := runtime.workflowContext
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	target := url.URL{Scheme: "http", Host: "127.0.0.1", Path: "/p/" + pack + path, RawQuery: query}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(body))
	if err != nil {
		return 0, nil, "", err
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if runtime.callTraceID != "" && runtime.callParentID != "" {
		request.Header.Set("traceparent", "00-"+runtime.callTraceID+"-"+runtime.callParentID+"-00")
	}
	if runtime.callTaskID != "" {
		request.Header.Set("X-LocalRouter-Task-Id", runtime.callTaskID)
	}
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set(tokenPolicyContextID, token.ID)
		c.Set("token_id", token.ID)
		c.Set("local_token", &token)
		c.Set("localrouter_projection_surface", runtime.callSurface)
		c.Next()
	}, serviceTraceMiddleware(runtime, "p"))
	engine.Any("/p/:protocol/*path", runtime.protocols.handleProxy)
	writer := &serviceResponseBuffer{header: make(http.Header), status: http.StatusOK, context: ctx}
	engine.ServeHTTP(writer, request)
	if writer.err != nil {
		return writer.status, nil, writer.header.Get("Content-Type"), writer.err
	}
	return writer.status, writer.body.Bytes(), writer.header.Get("Content-Type"), nil
}

type serviceResponseBuffer struct {
	header  http.Header
	status  int
	body    bytes.Buffer
	err     error
	context context.Context
}

func (writer *serviceResponseBuffer) Header() http.Header    { return writer.header }
func (writer *serviceResponseBuffer) WriteHeader(status int) { writer.status = status }
func (writer *serviceResponseBuffer) Write(data []byte) (int, error) {
	if writer.body.Len()+len(data) > maxProtocolBodyBytes {
		writer.err = errors.New("local protocol response is too large")
		return 0, writer.err
	}
	return writer.body.Write(data)
}
func (writer *serviceResponseBuffer) Flush() {}
func (writer *serviceResponseBuffer) CloseNotify() <-chan bool {
	closed := make(chan bool, 1)
	go func() { <-writer.context.Done(); closed <- true }()
	return closed
}
func (writer *serviceResponseBuffer) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("bidirectional transports require their direct Pack endpoint")
}

// A projection's outer policy check does not reserve a second concurrency slot.
// Each actual internal operation acquires and releases its own caller lease.
func (store *tokenPolicyStore) authorizeProjection(c *gin.Context, surface, pack, operation, model string) (func(), bool) {
	if allowed, status, message := store.preview(c.GetInt(tokenPolicyContextID), surface, pack, operation, model); !allowed {
		abortTokenPolicy(c, status, message)
		return nil, false
	}
	return func() {}, true
}

func servicePolicySurface(c *gin.Context) string {
	if surface := c.GetString("localrouter_projection_surface"); surface == "mcp" || surface == "w" {
		return surface
	}
	return "p"
}

func (registry *protocolRegistry) authorizeProtocolModel(c *gin.Context, body []byte) bool {
	if registry.policies == nil {
		return true
	}
	policy, exists := registry.policies.policyFor(c.GetInt(tokenPolicyContextID))
	if !exists {
		return true
	}
	if model := requestModel(body); model != "" && !containsPolicyValue(policy.Models, model) {
		abortTokenPolicy(c, http.StatusForbidden, "token policy does not allow this model")
		return false
	}
	return true
}

var _ http.ResponseWriter = (*serviceResponseBuffer)(nil)
var _ io.Writer = (*serviceResponseBuffer)(nil)
