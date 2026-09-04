package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func TestProtocolV3ExpressionsTargetsHMACAndReadiness(t *testing.T) {
	t.Parallel()
	const secret = "fixture-hmac-secret"
	var probeCount atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/health" {
			probeCount.Add(1)
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"nexus":2}`))
			return
		}
		body, _ := io.ReadAll(request.Body)
		digest := sha256.Sum256(body)
		message := request.Method + "\n" + request.URL.EscapedPath() + "\n" + request.URL.RawQuery + "\n" + hex.EncodeToString(digest[:]) + "\n" + request.Header.Get("X-Timestamp")
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(message))
		assert.Equal(t, hex.EncodeToString(mac.Sum(nil)), request.Header.Get("X-Signature"))
		assert.Equal(t, "5", request.Header.Get("X-Computed"))
		assert.Equal(t, "4", request.URL.Query().Get("double"))
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	protocolDir := t.TempDir()
	dataDir := t.TempDir()
	writeProtocolSecret(t, dataDir, "hmac", secret)
	writeProtocolDefinition(t, protocolDir, protocolDefinition{
		SchemaVersion: protocolSchemaVersionV3, ID: "computed", Name: "Computed", Description: "Computed protocol", Enabled: true,
		BaseURL: "http://127.0.0.1:1", Targets: map[string]string{"provider": upstream.URL},
		Auth: protocolAuth{Type: "hmac-sha256", SecretFile: "protocol-secrets/hmac", HMAC: &protocolHMACConfig{
			Header: "X-Signature", TimestampHeader: "X-Timestamp", Encoding: "hex",
			MessageTemplate: "{{method}}\n{{path}}\n{{query}}\n{{body_sha256}}\n{{timestamp}}",
		}},
		Readiness: &protocolReadinessConfig{Mode: "probe", Target: "provider", Path: "/health", Expression: "body.nexus > 0", IntervalSeconds: 60},
		Routes: []protocolRoute{{
			OperationID: "compute", Methods: []string{http.MethodPost}, Path: "/compute", Target: "provider", Summary: "Compute",
			RequestTransform: protocolRequestTransform{
				JSON:       protocolJSONTransform{SetExpr: map[string]string{"sum": "body.a + body.b"}},
				HeaderExpr: map[string]string{"X-Computed": "string(body.sum)"},
				QueryExpr:  map[string]string{"double": "string(body.a * 2)"},
			},
			ResponseTransform: protocolJSONTransform{SetExpr: map[string]string{"verified": "status == 200 && body.ok"}},
			Availability:      protocolRouteAvailability{Status: "verified", VerifiedAt: time.Now().UTC().Format(time.RFC3339), ValidForSeconds: 3600, EvidenceRun: "RUN-fixture"},
		}},
	})
	registry, err := newProtocolRegistry(protocolDir, dataDir)
	require.NoError(t, err)
	engine := gin.New()
	registerProtocolRoutes(engine, localRuntime{apiToken: "sk-local", protocols: registry})

	for range 2 {
		request := httptest.NewRequest(http.MethodPost, "/p/computed/compute", strings.NewReader(`{"a":2,"b":3}`))
		request.Header.Set("Authorization", "Bearer sk-local")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		require.Equal(t, http.StatusOK, response.Code)
		assert.JSONEq(t, `{"ok":true,"verified":true}`, response.Body.String())
	}
	assert.Equal(t, int32(1), probeCount.Load(), "readiness probe should be cached")
}

func TestProtocolV3ReadinessProbeTriesAnotherPooledCredential(t *testing.T) {
	t.Parallel()
	var probeCount atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		probeCount.Add(1)
		if request.Header.Get("Authorization") == "Bearer good-secret" {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"id":"current-user"}`))
			return
		}
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write([]byte(`{"error":"invalid token"}`))
	}))
	defer upstream.Close()

	protocolDir, dataDir := t.TempDir(), t.TempDir()
	writeProtocolPool(t, dataDir, "probe", protocolCredentialFile{SchemaVersion: "1", Credentials: []protocolCredential{
		{ID: "bad", Secret: "bad-secret"},
		{ID: "good", Secret: "good-secret"},
	}})
	writeProtocolDefinition(t, protocolDir, protocolDefinition{
		SchemaVersion: protocolSchemaVersionV3, ID: "probe", Name: "Probe", Description: "Pooled readiness probe", Enabled: true,
		BaseURL: upstream.URL,
		Auth:    protocolAuth{Type: "bearer"},
		Pool: &protocolPoolConfig{
			Mode: "local", CredentialsFile: "protocol-pools/probe.json", Strategy: "least-inflight", MaxAttempts: 2,
			MaxInFlightPerCredential: 1, InFlightLeaseSeconds: 30, UnauthorizedAction: "cooldown", UnauthorizedCooldownSeconds: 60,
		},
		Readiness: &protocolReadinessConfig{Mode: "probe", Path: "/health", Expression: `body.id == "current-user"`},
		Routes:    []protocolRoute{{OperationID: "read", Methods: []string{http.MethodGet}, Path: "/read", Summary: "Read"}},
	})
	registry, err := newProtocolRegistry(protocolDir, dataDir)
	require.NoError(t, err)

	ready, status := registry.readiness(registry.templates["probe"])
	require.True(t, ready)
	assert.Equal(t, "ready", status)
	assert.Equal(t, int32(2), probeCount.Load())
}

func TestProtocolV3SupplierProfilePreservesRawQueryAndCustomizesHeaders(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "b=2&a=%2F&a=1", request.URL.RawQuery)
		assert.Equal(t, "beta-/", request.Header.Get("X-Supplier"))
		assert.Equal(t, []string{"one", "two"}, request.Header.Values("X-Multi"))
		assert.Empty(t, request.Header.Values("X-Remove"))
		assert.Empty(t, request.Header.Get("User-Agent"))
		assert.Empty(t, request.Header.Get("Authorization"), "local client authentication must never reach the supplier")
		_, _ = writer.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	protocolDir, dataDir := t.TempDir(), t.TempDir()
	writeProtocolDefinition(t, protocolDir, protocolDefinition{
		SchemaVersion: protocolSchemaVersionV3, ID: "profileapi", Name: "Profile API", Description: "Supplier profile fixture", Enabled: true,
		BaseURL: "http://127.0.0.1:1", Targets: map[string]string{"beta": upstream.URL}, Auth: protocolAuth{Type: "none"},
		ForwardHeaders: []string{"X-Remove"},
		UpstreamProfiles: map[string]protocolUpstreamProfile{
			"default":       {SetHeaders: map[string]string{"X-Supplier": "default"}},
			"provider-beta": {ForwardHeaders: []string{"X-Multi"}, SetHeaders: map[string]string{"X-Supplier": "{{path.kind}}-{{query.a}}"}, RemoveHeaders: []string{"X-Remove"}, UserAgent: "omit", Query: "preserve-raw"},
		},
		Routes: []protocolRoute{{OperationID: "echo", Methods: []string{http.MethodGet}, Path: "/echo/{kind}", Target: "beta", Summary: "Echo", UpstreamProfile: "default", UpstreamProfilesByTarget: map[string]string{"beta": "provider-beta"}}},
	})
	registry, err := newProtocolRegistry(protocolDir, dataDir)
	require.NoError(t, err)
	engine := gin.New()
	registerProtocolRoutes(engine, localRuntime{apiToken: "sk-local", protocols: registry})

	request := httptest.NewRequest(http.MethodGet, "/p/profileapi/echo/beta?b=2&a=%2F&a=1", nil)
	request.Header.Set("Authorization", "Bearer sk-local")
	request.Header.Add("X-Multi", "one")
	request.Header.Add("X-Multi", "two")
	request.Header.Set("X-Remove", "remove-me")
	request.Header.Set("User-Agent", "caller-agent")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
}

func TestProtocolV3SupplierProfileRejectsAmbiguousOrReservedRules(t *testing.T) {
	registry := &protocolRegistry{dataDir: t.TempDir()}
	definition := protocolDefinition{
		SchemaVersion: protocolSchemaVersionV3, ID: "profileapi", Name: "Profile API", Description: "Supplier profile fixture", Enabled: true,
		BaseURL: "https://example.invalid", Auth: protocolAuth{Type: "none"},
		UpstreamProfiles: map[string]protocolUpstreamProfile{"provider": {SetHeaders: map[string]string{"Authorization": "unsafe"}}},
		Routes:           []protocolRoute{{OperationID: "echo", Methods: []string{http.MethodGet}, Path: "/echo", Summary: "Echo", UpstreamProfile: "provider"}},
	}
	err := registry.validate(&definition)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reserved header")

	definition.UpstreamProfiles["provider"] = protocolUpstreamProfile{Query: "preserve-raw"}
	definition.Routes[0].RequestTransform.Query = map[string]string{"page": "1"}
	err = registry.validate(&definition)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be combined with query transforms")

	definition.Routes[0].RequestTransform.Query = nil
	definition.UpstreamProfiles["provider"] = protocolUpstreamProfile{ForwardHeaders: []string{"User-Agent"}}
	err = registry.validate(&definition)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be controlled with user_agent")
}

func TestProtocolV3OAuthTokenCache(t *testing.T) {
	t.Parallel()
	var tokenCalls atomic.Int32
	tokenServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		tokenCalls.Add(1)
		username, password, ok := request.BasicAuth()
		assert.True(t, ok)
		assert.Equal(t, "client-id", username)
		assert.Equal(t, "client-secret", password)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"access_token":"runtime-token","token_type":"Bearer","expires_in":3600}`))
	}))
	defer tokenServer.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "Bearer runtime-token", request.Header.Get("Authorization"))
		_, _ = writer.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	protocolDir := t.TempDir()
	dataDir := t.TempDir()
	secret, _ := json.Marshal(protocolOAuthSecret{ClientID: "client-id", ClientSecret: "client-secret"})
	writeProtocolSecret(t, dataDir, "oauth", string(secret))
	writeProtocolDefinition(t, protocolDir, protocolDefinition{
		SchemaVersion: protocolSchemaVersionV3, ID: "oauthapi", Name: "OAuth API", Description: "OAuth test", Enabled: true,
		BaseURL: upstream.URL,
		Auth:    protocolAuth{Type: "oauth2", SecretFile: "protocol-secrets/oauth", OAuth2: &protocolOAuth2Config{TokenURL: tokenServer.URL, GrantType: "client_credentials", ClientAuth: "basic"}},
		Routes:  []protocolRoute{{OperationID: "read", Methods: []string{http.MethodGet}, Path: "/read", Summary: "Read"}},
	})
	registry, err := newProtocolRegistry(protocolDir, dataDir)
	require.NoError(t, err)
	engine := gin.New()
	registerProtocolRoutes(engine, localRuntime{apiToken: "sk-local", protocols: registry})
	for range 2 {
		request := httptest.NewRequest(http.MethodGet, "/p/oauthapi/read", nil)
		request.Header.Set("Authorization", "Bearer sk-local")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		require.Equal(t, http.StatusOK, response.Code)
	}
	assert.Equal(t, int32(1), tokenCalls.Load())
}

func TestProtocolV3AWSSigV4Authentication(t *testing.T) {
	t.Parallel()
	request, err := http.NewRequest(http.MethodPost, "https://service.us-east-1.amazonaws.com/resource?mode=test", strings.NewReader(`{"x":1}`))
	require.NoError(t, err)
	secret, err := json.Marshal(protocolAWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "fixture-secret", SessionToken: "fixture-session"})
	require.NoError(t, err)
	registry := &protocolRegistry{oauthTokens: make(map[string]protocolOAuthToken)}
	definition := protocolDefinition{ID: "awsapi", Auth: protocolAuth{Type: "aws-sigv4", AWSSigV4: &protocolAWSSigV4Config{Region: "us-east-1", Service: "execute-api"}}}
	err = registry.injectProtocolCredential(request, definition, acquiredProtocolCredential{Credential: protocolCredential{ID: "aws", Secret: string(secret)}}, []byte(`{"x":1}`))
	require.NoError(t, err)
	assert.Contains(t, request.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/")
	assert.Contains(t, request.Header.Get("Authorization"), "/us-east-1/execute-api/aws4_request")
	assert.NotEmpty(t, request.Header.Get("X-Amz-Date"))
	assert.Equal(t, "fixture-session", request.Header.Get("X-Amz-Security-Token"))
}

func TestProtocolV3RetryUnknownOutcomeAndIdempotency(t *testing.T) {
	t.Parallel()
	var unsafeCount atomic.Int32
	var idempotentCount atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var count int32
		if request.URL.Path == "/unsafe" {
			count = unsafeCount.Add(1)
		} else {
			count = idempotentCount.Add(1)
		}
		if count == 1 {
			hijacker := writer.(http.Hijacker)
			connection, _, _ := hijacker.Hijack()
			_ = connection.Close()
			return
		}
		if request.URL.Path == "/idempotent" {
			assert.NotEmpty(t, request.Header.Get("Idempotency-Key"))
		}
		_, _ = writer.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	protocolDir := t.TempDir()
	dataDir := t.TempDir()
	writeProtocolDefinition(t, protocolDir, protocolDefinition{
		SchemaVersion: protocolSchemaVersionV3, ID: "retryapi", Name: "Retry API", Description: "Retry semantics", Enabled: true,
		BaseURL: upstream.URL, Auth: protocolAuth{Type: "none"},
		Routes: []protocolRoute{
			{OperationID: "unsafe", Methods: []string{http.MethodPost}, Path: "/unsafe", Summary: "Unsafe", Retry: protocolRetryConfig{Mode: "safe", MaxAttempts: 2}},
			{OperationID: "idempotent", Methods: []string{http.MethodPost}, Path: "/idempotent", Summary: "Idempotent", Retry: protocolRetryConfig{Mode: "idempotent", MaxAttempts: 2, IdempotencyHeader: "Idempotency-Key"}},
		},
	})
	registry, err := newProtocolRegistry(protocolDir, dataDir)
	require.NoError(t, err)
	engine := gin.New()
	registerProtocolRoutes(engine, localRuntime{apiToken: "sk-local", protocols: registry})

	unsafe := httptest.NewRequest(http.MethodPost, "/p/retryapi/unsafe", strings.NewReader(`{}`))
	unsafe.Header.Set("Authorization", "Bearer sk-local")
	unsafeResponse := httptest.NewRecorder()
	engine.ServeHTTP(unsafeResponse, unsafe)
	assert.Equal(t, 520, unsafeResponse.Code)
	assert.Contains(t, unsafeResponse.Body.String(), `"outcome":"unknown"`)
	assert.Equal(t, int32(1), unsafeCount.Load())

	idempotent := httptest.NewRequest(http.MethodPost, "/p/retryapi/idempotent", strings.NewReader(`{}`))
	idempotent.Header.Set("Authorization", "Bearer sk-local")
	idempotentResponse := httptest.NewRecorder()
	engine.ServeHTTP(idempotentResponse, idempotent)
	assert.Equal(t, http.StatusOK, idempotentResponse.Code)
	assert.Equal(t, int32(2), idempotentCount.Load())
}

func TestProtocolV3PoolCapabilitySelectorAndLeaseExpiry(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = fmt.Fprintf(writer, `{"credential":%q}`, strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer "))
	}))
	defer upstream.Close()
	protocolDir := t.TempDir()
	dataDir := t.TempDir()
	writeProtocolPool(t, dataDir, "capabilities", protocolCredentialFile{SchemaVersion: "1", Credentials: []protocolCredential{
		{ID: "text-account", Secret: "text-secret", Metadata: map[string]string{"capability": "text"}},
		{ID: "video-account", Secret: "video-secret", Metadata: map[string]string{"capability": "video"}},
	}})
	writeProtocolDefinition(t, protocolDir, protocolDefinition{
		SchemaVersion: protocolSchemaVersionV3, ID: "capapi", Name: "Capability API", Description: "Capability pool", Enabled: true,
		BaseURL: upstream.URL, Auth: protocolAuth{Type: "bearer"},
		Pool:   &protocolPoolConfig{Mode: "local", CredentialsFile: "protocol-pools/capabilities.json", Strategy: "least-inflight", MaxAttempts: 1, InFlightLeaseSeconds: 30},
		Routes: []protocolRoute{{OperationID: "video", Methods: []string{http.MethodGet}, Path: "/video", Summary: "Video", PoolSelector: `metadata.capability == "video"`}},
	})
	registry, err := newProtocolRegistry(protocolDir, dataDir)
	require.NoError(t, err)
	engine := gin.New()
	registerProtocolRoutes(engine, localRuntime{apiToken: "sk-local", protocols: registry})
	request := httptest.NewRequest(http.MethodGet, "/p/capapi/video", nil)
	request.Header.Set("Authorization", "Bearer sk-local")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	assert.JSONEq(t, `{"credential":"video-secret"}`, response.Body.String())

	runtime := &protocolCredentialRuntime{InFlight: 2, Leases: map[string]int64{"expired": time.Now().Add(-time.Second).Unix(), "active": time.Now().Add(time.Minute).Unix()}}
	pruneProtocolCredentialLeases(runtime, time.Now())
	assert.Equal(t, 1, runtime.InFlight)
	assert.NotContains(t, runtime.Leases, "expired")
}

func TestProtocolV3TargetSelectorFailsOverAcrossCompatibleProviders(t *testing.T) {
	t.Parallel()
	var alphaCalls atomic.Int32
	var betaCalls atomic.Int32
	alpha := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		alphaCalls.Add(1)
		assert.Equal(t, "Bearer alpha-secret", request.Header.Get("Authorization"))
		http.Error(writer, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer alpha.Close()
	beta := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		betaCalls.Add(1)
		assert.Equal(t, "Bearer beta-secret", request.Header.Get("Authorization"))
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"provider":"beta","ok":true}`))
	}))
	defer beta.Close()

	protocolDir := t.TempDir()
	dataDir := t.TempDir()
	writeProtocolPool(t, dataDir, "federated", protocolCredentialFile{SchemaVersion: "1", Credentials: []protocolCredential{
		{ID: "alpha-key", Secret: "alpha-secret", Metadata: map[string]string{"provider": "alpha"}},
		{ID: "beta-key", Secret: "beta-secret", Metadata: map[string]string{"provider": "beta"}},
		{ID: "unmapped-key", Secret: "must-not-be-used", Metadata: map[string]string{"provider": "unknown"}},
	}})
	writeProtocolDefinition(t, protocolDir, protocolDefinition{
		SchemaVersion: protocolSchemaVersionV3, ID: "federated", Name: "Federated API", Description: "Compatible providers", Enabled: true,
		BaseURL: alpha.URL,
		Targets: map[string]string{"alpha": alpha.URL, "beta": beta.URL},
		Auth:    protocolAuth{Type: "bearer"},
		Pool:    &protocolPoolConfig{Mode: "local", CredentialsFile: "protocol-pools/federated.json", Strategy: "round-robin", MaxAttempts: 2, InFlightLeaseSeconds: 30},
		Routes: []protocolRoute{{
			OperationID: "generate", Methods: []string{http.MethodGet}, Path: "/generate", Summary: "Generate",
			TargetSelector: &protocolTargetSelector{MetadataKey: "provider", Mappings: map[string]string{"alpha": "alpha", "beta": "beta"}},
			Retry:          protocolRetryConfig{Mode: "safe", MaxAttempts: 2, Statuses: []int{http.StatusServiceUnavailable}},
		}},
	})
	registry, err := newProtocolRegistry(protocolDir, dataDir)
	require.NoError(t, err)
	pool := registry.poolView(registry.templates["federated"])
	assert.Equal(t, 3, pool.Total)
	assert.Equal(t, 2, pool.Eligible, "credentials without an allowlisted provider mapping must not be advertised as eligible")
	engine := gin.New()
	registerProtocolRoutes(engine, localRuntime{apiToken: "sk-local", protocols: registry})

	request := httptest.NewRequest(http.MethodGet, "/p/federated/generate", nil)
	request.Header.Set("Authorization", "Bearer sk-local")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	assert.JSONEq(t, `{"provider":"beta","ok":true}`, response.Body.String())
	assert.Equal(t, int32(1), alphaCalls.Load())
	assert.Equal(t, int32(1), betaCalls.Load())
}

func TestProtocolV3TargetSelectorValidation(t *testing.T) {
	t.Parallel()
	base := protocolDefinition{
		SchemaVersion: protocolSchemaVersionV3, ID: "targets", Name: "Targets", Description: "Target validation", Enabled: true,
		BaseURL: "https://example.com", Targets: map[string]string{"alpha": "https://alpha.example.com"}, Auth: protocolAuth{Type: "bearer"},
		Pool: &protocolPoolConfig{Mode: "local", CredentialsFile: "protocol-pools/targets.json", Strategy: "round-robin", MaxAttempts: 1, InFlightLeaseSeconds: 30},
	}
	t.Run("unknown named target", func(t *testing.T) {
		definition := base
		definition.Routes = []protocolRoute{{OperationID: "call", Methods: []string{http.MethodGet}, Path: "/call", Summary: "Call", TargetSelector: &protocolTargetSelector{MetadataKey: "provider", Mappings: map[string]string{"beta": "missing"}}}}
		require.NoError(t, validateProtocolV3Definition(&definition))
		assert.ErrorContains(t, validateProtocolV3Route(&definition.Routes[0], &definition), "unknown target")
	})
	t.Run("requires local pool", func(t *testing.T) {
		definition := base
		definition.Pool = &protocolPoolConfig{Mode: "external"}
		definition.Routes = []protocolRoute{{OperationID: "call", Methods: []string{http.MethodGet}, Path: "/call", Summary: "Call", TargetSelector: &protocolTargetSelector{MetadataKey: "provider", Mappings: map[string]string{"alpha": "alpha"}}}}
		require.NoError(t, validateProtocolV3Definition(&definition))
		assert.ErrorContains(t, validateProtocolV3Route(&definition.Routes[0], &definition), "local credential pool")
	})
}

func TestProtocolV3CanRewriteClientPostToUpstreamGet(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, http.MethodGet, request.Method)
		assert.Equal(t, "web search", request.URL.Query().Get("q"))
		_, _ = writer.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	protocolDir := t.TempDir()
	dataDir := t.TempDir()
	writeProtocolDefinition(t, protocolDir, protocolDefinition{
		SchemaVersion: protocolSchemaVersionV3, ID: "methodapi", Name: "Method API", Description: "Method rewrite", Enabled: true,
		BaseURL: upstream.URL, Auth: protocolAuth{Type: "none"},
		Routes: []protocolRoute{{
			OperationID: "tools.find", Methods: []string{http.MethodPost}, Path: "/find", UpstreamPath: "/native/find", UpstreamMethod: http.MethodGet, Summary: "Find",
			RequestTransform: protocolRequestTransform{QueryExpr: map[string]string{"q": `body.query`}},
		}},
	})
	registry, err := newProtocolRegistry(protocolDir, dataDir)
	require.NoError(t, err)
	engine := gin.New()
	registerProtocolRoutes(engine, localRuntime{apiToken: "sk-local", protocols: registry})

	request := httptest.NewRequest(http.MethodPost, "/p/methodapi/find", strings.NewReader(`{"query":"web search"}`))
	request.Header.Set("Authorization", "Bearer sk-local")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	assert.Equal(t, http.StatusOK, response.Code)
}

func TestProtocolV3PoolStateCoordinatesAcrossRegistries(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	writeProtocolPool(t, dataDir, "shared", protocolCredentialFile{SchemaVersion: "1", Credentials: []protocolCredential{{ID: "only-account", Secret: "secret"}}})
	definition := protocolDefinition{
		SchemaVersion: protocolSchemaVersionV3, ID: "sharedpool", Auth: protocolAuth{Type: "bearer"}, Timeout: 30,
		Pool: &protocolPoolConfig{Mode: "local", CredentialsFile: "protocol-pools/shared.json", Strategy: "least-inflight", MaxAttempts: 1, MaxInFlightPerCredential: 1, InFlightLeaseSeconds: 30, UnauthorizedAction: "disable", UnauthorizedCooldownSeconds: 60, RateLimitCooldownSeconds: 60, TransientCooldownSeconds: 60},
	}
	firstRegistry := &protocolRegistry{dataDir: dataDir, stateDir: dataDir}
	secondRegistry := &protocolRegistry{dataDir: dataDir, stateDir: dataDir}
	route := protocolRoute{OperationID: "shared.call", Methods: []string{http.MethodGet}, Path: "/call"}
	first, err := firstRegistry.acquireCredential(definition, route, "", map[string]bool{})
	require.NoError(t, err)
	stateBefore, err := firstRegistry.loadPoolState(definition.ID)
	require.NoError(t, err)
	expiresBefore := stateBefore.Credentials[first.Credential.ID].Leases[first.LeaseID]
	require.NoError(t, firstRegistry.renewCredentialLease(definition, first))
	stateAfter, err := firstRegistry.loadPoolState(definition.ID)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, stateAfter.Credentials[first.Credential.ID].Leases[first.LeaseID], expiresBefore)
	_, err = secondRegistry.acquireCredential(definition, route, "", map[string]bool{})
	assert.Error(t, err, "the persisted lease must be visible to another registry instance")
	require.NoError(t, firstRegistry.releaseCredential(definition, first, http.StatusOK, ""))
	second, err := secondRegistry.acquireCredential(definition, route, "", map[string]bool{})
	require.NoError(t, err)
	require.NoError(t, secondRegistry.releaseCredential(definition, second, http.StatusOK, ""))
}

func TestProtocolV3WebSocketAndAdapterTransports(t *testing.T) {
	t.Parallel()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	websocketUpstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(writer, request, nil)
		require.NoError(t, err)
		defer connection.Close()
		_, payload, err := connection.ReadMessage()
		require.NoError(t, err)
		_ = connection.WriteMessage(websocket.TextMessage, []byte(request.Header.Get("Authorization")+":"+string(payload)))
	}))
	defer websocketUpstream.Close()

	var adapterSawURL string
	adapter := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var envelope protocolAdapterEnvelope
		require.NoError(t, json.NewDecoder(request.Body).Decode(&envelope))
		adapterSawURL = envelope.Request.URL
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(protocolAdapterResponse{Status: http.StatusCreated, Headers: map[string][]string{"Content-Type": {"application/octet-stream"}}, BodyBase64: base64.StdEncoding.EncodeToString([]byte("adapter-ok"))})
	}))
	defer adapter.Close()

	protocolDir := t.TempDir()
	dataDir := t.TempDir()
	writeProtocolSecret(t, dataDir, "transport", "transport-secret")
	writeProtocolDefinition(t, protocolDir, protocolDefinition{
		SchemaVersion: protocolSchemaVersionV3, ID: "transport", Name: "Transport", Description: "Transport adapters", Enabled: true,
		BaseURL: websocketUpstream.URL,
		Targets: map[string]string{"adapter": adapter.URL, "tcp-service": "tcp://127.0.0.1:9123"},
		Auth:    protocolAuth{Type: "bearer", SecretFile: "protocol-secrets/transport"},
		Routes: []protocolRoute{
			{OperationID: "socket", Methods: []string{http.MethodGet}, Path: "/socket", Summary: "Socket", Transport: "websocket"},
			{OperationID: "tcp.invoke", Methods: []string{http.MethodPost}, Path: "/tcp", Summary: "TCP adapter", Transport: "adapter", Target: "tcp-service", Adapter: &protocolAdapterConfig{Type: "http-envelope", Target: "adapter", Path: "/invoke"}},
		},
	})
	registry, err := newProtocolRegistry(protocolDir, dataDir)
	require.NoError(t, err)
	engine := gin.New()
	registerProtocolRoutes(engine, localRuntime{apiToken: "sk-local", protocols: registry})
	local := httptest.NewServer(engine)
	defer local.Close()

	wsURL := "ws" + strings.TrimPrefix(local.URL, "http") + "/p/transport/socket"
	headers := http.Header{"Authorization": []string{"Bearer sk-local"}}
	connection, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	require.NoError(t, err)
	require.NoError(t, connection.WriteMessage(websocket.TextMessage, []byte("hello")))
	_, payload, err := connection.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, "Bearer transport-secret:hello", string(payload))
	_ = connection.Close()

	adapterRequest := httptest.NewRequest(http.MethodPost, "/p/transport/tcp", strings.NewReader("binary"))
	adapterRequest.Header.Set("Authorization", "Bearer sk-local")
	adapterResponse := httptest.NewRecorder()
	engine.ServeHTTP(adapterResponse, adapterRequest)
	assert.Equal(t, http.StatusCreated, adapterResponse.Code)
	assert.Equal(t, "adapter-ok", adapterResponse.Body.String())
	assert.Equal(t, "tcp://127.0.0.1:9123/tcp", adapterSawURL)
}

func TestProtocolV3AdapterRetryOutcomeAndTransforms(t *testing.T) {
	t.Parallel()
	var notSentCalls atomic.Int32
	var idempotentCalls atomic.Int32
	var unknownCalls atomic.Int32
	var firstIdempotencyKey atomic.Value
	adapter := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var envelope protocolAdapterEnvelope
		require.NoError(t, json.NewDecoder(request.Body).Decode(&envelope))
		writer.Header().Set("Content-Type", "application/json")
		switch envelope.OperationID {
		case "adapter.notsent":
			count := notSentCalls.Add(1)
			assert.Contains(t, envelope.Request.URL, "computed=4")
			assert.Equal(t, "3", http.Header(envelope.Request.Headers).Get("X-Computed"))
			decoded, _ := base64.StdEncoding.DecodeString(envelope.Request.BodyBase64)
			assert.JSONEq(t, `{"value":3}`, string(decoded))
			if count == 1 {
				_ = json.NewEncoder(writer).Encode(protocolAdapterResponse{Status: http.StatusServiceUnavailable, Outcome: "not_sent"})
				return
			}
			_ = json.NewEncoder(writer).Encode(protocolAdapterResponse{Status: http.StatusOK, Headers: map[string][]string{"Content-Type": {"application/json"}}, BodyBase64: base64.StdEncoding.EncodeToString([]byte(`{"ok":true}`)), Outcome: "complete"})
		case "adapter.idempotent":
			count := idempotentCalls.Add(1)
			key := http.Header(envelope.Request.Headers).Get("Idempotency-Key")
			assert.NotEmpty(t, key)
			if count == 1 {
				firstIdempotencyKey.Store(key)
				_ = json.NewEncoder(writer).Encode(protocolAdapterResponse{Status: http.StatusServiceUnavailable, Outcome: "sent"})
				return
			}
			assert.Equal(t, firstIdempotencyKey.Load(), key)
			_ = json.NewEncoder(writer).Encode(protocolAdapterResponse{Status: http.StatusOK, BodyBase64: base64.StdEncoding.EncodeToString([]byte(`{"ok":true}`)), Outcome: "complete"})
		case "adapter.unknown":
			unknownCalls.Add(1)
			_ = json.NewEncoder(writer).Encode(protocolAdapterResponse{Status: http.StatusBadGateway, Outcome: "unknown"})
		}
	}))
	defer adapter.Close()

	protocolDir, dataDir := t.TempDir(), t.TempDir()
	requestTransform := protocolRequestTransform{
		JSON:       protocolJSONTransform{ReplaceExpr: `{"value": body.value + 1}`},
		HeaderExpr: map[string]string{"X-Computed": "string(body.value)"},
		QueryExpr:  map[string]string{"computed": "string(body.value + 1)"},
	}
	writeProtocolDefinition(t, protocolDir, protocolDefinition{
		SchemaVersion: protocolSchemaVersionV3, ID: "adapterapi", Name: "Adapter API", Description: "Adapter retry semantics", Enabled: true,
		BaseURL: "http://127.0.0.1:1", Targets: map[string]string{"adapter": adapter.URL, "provider": "tcp://127.0.0.1:9000"}, Auth: protocolAuth{Type: "none"},
		Routes: []protocolRoute{
			{OperationID: "adapter.notsent", Methods: []string{http.MethodPost}, Path: "/notsent", Target: "provider", Transport: "adapter", Summary: "Not sent", Adapter: &protocolAdapterConfig{Type: "http-envelope", Target: "adapter", Path: "/invoke"}, RequestTransform: requestTransform, ResponseTransform: protocolJSONTransform{SetExpr: map[string]string{"transformed": "body.ok && status == 200"}}, Retry: protocolRetryConfig{Mode: "safe", MaxAttempts: 2}},
			{OperationID: "adapter.idempotent", Methods: []string{http.MethodPost}, Path: "/idempotent", Target: "provider", Transport: "adapter", Summary: "Idempotent", Adapter: &protocolAdapterConfig{Type: "http-envelope", Target: "adapter", Path: "/invoke"}, Retry: protocolRetryConfig{Mode: "idempotent", MaxAttempts: 2, IdempotencyHeader: "Idempotency-Key", Statuses: []int{http.StatusServiceUnavailable}}},
			{OperationID: "adapter.unknown", Methods: []string{http.MethodPost}, Path: "/unknown", Target: "provider", Transport: "adapter", Summary: "Unknown", Adapter: &protocolAdapterConfig{Type: "http-envelope", Target: "adapter", Path: "/invoke"}, Retry: protocolRetryConfig{Mode: "always", MaxAttempts: 2}},
		},
	})
	registry, err := newProtocolRegistry(protocolDir, dataDir)
	require.NoError(t, err)
	engine := gin.New()
	registerProtocolRoutes(engine, localRuntime{apiToken: "sk-local", protocols: registry})
	invoke := func(path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/p/adapterapi"+path, strings.NewReader(`{"value":2}`))
		request.Header.Set("Authorization", "Bearer sk-local")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		return response
	}
	notSent := invoke("/notsent")
	require.Equal(t, http.StatusOK, notSent.Code, notSent.Body.String())
	assert.JSONEq(t, `{"ok":true,"transformed":true}`, notSent.Body.String())
	assert.Equal(t, int32(2), notSentCalls.Load())
	idempotent := invoke("/idempotent")
	require.Equal(t, http.StatusOK, idempotent.Code, idempotent.Body.String())
	assert.Equal(t, int32(2), idempotentCalls.Load())
	unknown := invoke("/unknown")
	require.Equal(t, 520, unknown.Code, unknown.Body.String())
	assert.Contains(t, unknown.Body.String(), `"outcome":"unknown"`)
	assert.Contains(t, unknown.Body.String(), `"code":"upstream_outcome_unknown"`)
	assert.Contains(t, unknown.Body.String(), `"owner":"provider"`)
	assert.Contains(t, unknown.Body.String(), `"retryable":false`)
	assert.Contains(t, unknown.Body.String(), `"next_action":"reconcile provider state`)
	assert.Equal(t, int32(1), unknownCalls.Load())
}

func TestProtocolV3GraphWorkflowParallelCallbackCancelAndScheduler(t *testing.T) {
	t.Parallel()
	var cancelCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/first":
			_, _ = writer.Write([]byte(`{"value":2}`))
		case "/second":
			_, _ = writer.Write([]byte(`{"value":3}`))
		case "/finish":
			_, _ = writer.Write([]byte(`{"done":true}`))
		case "/cancel":
			cancelCalls.Add(1)
			_, _ = writer.Write([]byte(`{"cancelled":true}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer upstream.Close()

	protocolDir := t.TempDir()
	dataDir := t.TempDir()
	writeProtocolDefinition(t, protocolDir, protocolDefinition{
		SchemaVersion: protocolSchemaVersionV3, ID: "workflowapi", Name: "Workflow API", Description: "Graph workflows", Enabled: true,
		BaseURL: upstream.URL, Auth: protocolAuth{Type: "none"},
		Routes: []protocolRoute{
			{OperationID: "first", Methods: []string{http.MethodPost}, Path: "/first", Summary: "First"},
			{OperationID: "second", Methods: []string{http.MethodPost}, Path: "/second", Summary: "Second"},
			{OperationID: "finish", Methods: []string{http.MethodPost}, Path: "/finish", Summary: "Finish"},
			{OperationID: "cancel", Methods: []string{http.MethodPost}, Path: "/cancel", Summary: "Cancel"},
		},
		Workflows: []protocolWorkflow{
			{ID: "callback-flow", Name: "Callback flow", InitialStep: "fanout", CancelStep: "cancel-step", Steps: []protocolWorkflowStep{
				{ID: "fanout", Kind: "parallel", Parallel: []protocolWorkflowCall{{ID: "one", Operation: "first"}, {ID: "two", Operation: "second"}}, Transitions: []protocolWorkflowTransition{{When: "status == 200", To: "wait"}, {Terminal: "failed"}}},
				{ID: "wait", Kind: "callback", Transitions: []protocolWorkflowTransition{{When: "response.approved == true", Terminal: "succeeded", ResultExpr: `{"sum": context.fanout.one.body.value + context.fanout.two.body.value}`}, {Terminal: "failed"}}},
				{ID: "cancel-step", Operation: "cancel", Transitions: []protocolWorkflowTransition{{Terminal: "cancelled"}}},
			}},
			{ID: "scheduled-flow", Name: "Scheduled flow", InitialStep: "begin", Steps: []protocolWorkflowStep{
				{ID: "begin", Operation: "first", WaitMS: 10, Transitions: []protocolWorkflowTransition{{To: "finish"}}},
				{ID: "finish", Operation: "finish", Transitions: []protocolWorkflowTransition{{When: "response.done", Terminal: "succeeded", ResultExpr: "response"}, {Terminal: "failed"}}},
			}},
		},
	})
	registry, err := newProtocolRegistry(protocolDir, dataDir)
	require.NoError(t, err)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	runtime := localRuntime{config: runtimeConfig{Host: "127.0.0.1", Port: port}, apiToken: "sk-local", protocols: registry}
	engine := gin.New()
	registerProtocolRoutes(engine, runtime)
	server := &http.Server{Handler: engine}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	base := "http://127.0.0.1:" + strconv.Itoa(port)

	create := func(workflow string) protocolWorkflowJob {
		request, _ := http.NewRequest(http.MethodPost, base+"/w/workflowapi/"+workflow, strings.NewReader(`{"prompt":"test"}`))
		request.Header.Set("Authorization", "Bearer sk-local")
		request.Header.Set("Content-Type", "application/json")
		response, requestErr := http.DefaultClient.Do(request)
		require.NoError(t, requestErr)
		defer response.Body.Close()
		require.Equal(t, http.StatusAccepted, response.StatusCode)
		var payload struct {
			Data protocolWorkflowJob `json:"data"`
		}
		require.NoError(t, json.NewDecoder(response.Body).Decode(&payload))
		return payload.Data
	}

	callbackJob := create("callback-flow")
	require.Equal(t, "wait", callbackJob.CurrentStep)
	getRequest, _ := http.NewRequest(http.MethodGet, base+"/w/workflowapi/callback-flow/"+callbackJob.ID, nil)
	getRequest.Header.Set("Authorization", "Bearer sk-local")
	getResponse, err := http.DefaultClient.Do(getRequest)
	require.NoError(t, err)
	_ = getResponse.Body.Close()
	require.Equal(t, http.StatusOK, getResponse.StatusCode)
	callbackRequest, _ := http.NewRequest(http.MethodPost, callbackJob.CallbackURL, strings.NewReader(`{"approved":true}`))
	callbackRequest.Header.Set("Content-Type", "application/json")
	callbackResponse, err := http.DefaultClient.Do(callbackRequest)
	require.NoError(t, err)
	defer callbackResponse.Body.Close()
	require.Equal(t, http.StatusAccepted, callbackResponse.StatusCode)
	callbackReceipt, err := io.ReadAll(callbackResponse.Body)
	require.NoError(t, err)
	assert.NotContains(t, string(callbackReceipt), callbackJob.CallbackURL)
	resultRequest, _ := http.NewRequest(http.MethodGet, base+"/w/workflowapi/callback-flow/"+callbackJob.ID, nil)
	resultRequest.Header.Set("Authorization", "Bearer sk-local")
	resultResponse, err := http.DefaultClient.Do(resultRequest)
	require.NoError(t, err)
	defer resultResponse.Body.Close()
	var callbackPayload struct {
		Data protocolWorkflowJob `json:"data"`
	}
	require.NoError(t, json.NewDecoder(resultResponse.Body).Decode(&callbackPayload))
	assert.Equal(t, "succeeded", callbackPayload.Data.State)
	assert.JSONEq(t, `{"sum":5}`, string(callbackPayload.Data.Result))

	cancelJob := create("callback-flow")
	cancelRequest, _ := http.NewRequest(http.MethodDelete, base+"/w/workflowapi/callback-flow/"+cancelJob.ID, nil)
	cancelRequest.Header.Set("Authorization", "Bearer sk-local")
	cancelResponse, err := http.DefaultClient.Do(cancelRequest)
	require.NoError(t, err)
	defer cancelResponse.Body.Close()
	require.Equal(t, http.StatusOK, cancelResponse.StatusCode)
	assert.Equal(t, int32(1), cancelCalls.Load())

	registry.startWorkflowScheduler(runtime)
	defer registry.stopWorkflowScheduler()
	scheduledJob := create("scheduled-flow")
	deadline := time.Now().Add(3 * time.Second)
	for scheduledJob.State != "succeeded" && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		request, _ := http.NewRequest(http.MethodGet, base+"/w/workflowapi/scheduled-flow/"+scheduledJob.ID, nil)
		request.Header.Set("Authorization", "Bearer sk-local")
		response, requestErr := http.DefaultClient.Do(request)
		require.NoError(t, requestErr)
		var payload struct {
			Data protocolWorkflowJob `json:"data"`
		}
		require.NoError(t, json.NewDecoder(response.Body).Decode(&payload))
		_ = response.Body.Close()
		scheduledJob = payload.Data
	}
	assert.Equal(t, "succeeded", scheduledJob.State)
}

func TestProtocolV3RawGRPCH2CPassthrough(t *testing.T) {
	t.Parallel()
	upstreamHandler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		assert.Equal(t, "application/grpc", request.Header.Get("Content-Type"))
		assert.Equal(t, []byte{0, 0, 0, 0, 1, 7}, body)
		writer.Header().Set("Content-Type", "application/grpc")
		writer.Header().Set("Trailer", "Grpc-Status")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte{0, 0, 0, 0, 1, 9})
		writer.Header().Set("Grpc-Status", "0")
	})
	upstream := httptest.NewUnstartedServer(h2c.NewHandler(upstreamHandler, &http2.Server{}))
	upstream.EnableHTTP2 = true
	upstream.Start()
	defer upstream.Close()

	protocolDir, dataDir := t.TempDir(), t.TempDir()
	parsed, err := url.Parse(upstream.URL)
	require.NoError(t, err)
	writeProtocolDefinition(t, protocolDir, protocolDefinition{
		SchemaVersion: protocolSchemaVersionV3, ID: "grpcapi", Name: "gRPC API", Description: "Raw gRPC", Enabled: true,
		BaseURL: "http://127.0.0.1:1", Targets: map[string]string{"grpc": "grpc://" + parsed.Host}, Auth: protocolAuth{Type: "none"},
		Routes: []protocolRoute{{OperationID: "grpc.call", Methods: []string{http.MethodPost}, Path: "/pkg.Service/Call", Summary: "Call", Transport: "grpc", Target: "grpc", Streaming: true}},
	})
	registry, err := newProtocolRegistry(protocolDir, dataDir)
	require.NoError(t, err)
	engine := gin.New()
	registerProtocolRoutes(engine, localRuntime{apiToken: "sk-local", protocols: registry})
	local := httptest.NewUnstartedServer(h2c.NewHandler(engine, &http2.Server{}))
	local.EnableHTTP2 = true
	local.Start()
	defer local.Close()
	transport := &http2.Transport{AllowHTTP: true, DialTLSContext: func(ctx context.Context, network, address string, _ *tls.Config) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}}
	request, err := http.NewRequest(http.MethodPost, local.URL+"/p/grpcapi/pkg.Service/Call", bytes.NewReader([]byte{0, 0, 0, 0, 1, 7}))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer sk-local")
	request.Header.Set("Content-Type", "application/grpc")
	request.Header.Set("TE", "trailers")
	response, err := transport.RoundTrip(request)
	require.NoError(t, err)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	assert.Equal(t, []byte{0, 0, 0, 0, 1, 9}, body)
	assert.Equal(t, "0", response.Trailer.Get("Grpc-Status"))
}

func TestProtocolV3GRPCTransportFailureReturnsAgentError(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	unreachableAddress := listener.Addr().String()
	require.NoError(t, listener.Close())

	protocolDir, dataDir := t.TempDir(), t.TempDir()
	writeProtocolDefinition(t, protocolDir, protocolDefinition{
		SchemaVersion: protocolSchemaVersionV3, ID: "grpcfail", Name: "gRPC failure", Description: "Unknown outcome", Enabled: true,
		BaseURL: "http://127.0.0.1:1", Targets: map[string]string{"grpc": "grpc://" + unreachableAddress}, Auth: protocolAuth{Type: "none"},
		Routes: []protocolRoute{{OperationID: "grpc.mutate", Methods: []string{http.MethodPost}, Path: "/pkg.Service/Mutate", Summary: "Mutate", Transport: "grpc", Target: "grpc", Streaming: true, Retry: protocolRetryConfig{Mode: "never", UnknownOutcomeStatus: 521}}},
	})
	registry, err := newProtocolRegistry(protocolDir, dataDir)
	require.NoError(t, err)
	engine := gin.New()
	registerProtocolRoutes(engine, localRuntime{apiToken: "sk-local", protocols: registry})

	request := httptest.NewRequest(http.MethodPost, "/p/grpcfail/pkg.Service/Mutate", bytes.NewReader([]byte{0, 0, 0, 0, 0}))
	request.Header.Set("Authorization", "Bearer sk-local")
	request.Header.Set("Content-Type", "application/grpc")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	require.Equal(t, 521, response.Code, response.Body.String())
	assert.JSONEq(t, `{
		"success":false,
		"code":"upstream_outcome_unknown",
		"message":"grpc upstream outcome is unknown",
		"reason":"the gRPC request may have reached the provider but no authoritative response was received",
		"retryable":false,
		"owner":"provider",
		"retry_after":null,
		"next_action":"reconcile provider state using the returned resource or idempotency key; do not replay blindly",
		"alternatives":[],
		"outcome":"unknown",
		"operation_id":"grpc.mutate"
	}`, response.Body.String())
}

func TestProtocolV3WASMEnvelopeAdapter(t *testing.T) {
	t.Parallel()
	protocolDir, dataDir := t.TempDir(), t.TempDir()
	moduleDir := filepath.Join(protocolDir, "wasmapi", "modules")
	require.NoError(t, os.MkdirAll(moduleDir, 0o755))
	wasmResponse := []byte(`{"status":200,"headers":{"Content-Type":["application/json"]},"body_base64":"eyJydW50aW1lIjoid2FzbSJ9"}`)
	require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "fixture.wasm"), staticEnvelopeWASM(wasmResponse), 0o644))
	writeProtocolDefinition(t, protocolDir, protocolDefinition{
		SchemaVersion: protocolSchemaVersionV3, ID: "wasmapi", Name: "WASM API", Description: "Sandbox adapter", Enabled: true,
		BaseURL: "http://127.0.0.1:1", Targets: map[string]string{"service": "tcp://127.0.0.1:9"}, Auth: protocolAuth{Type: "none"},
		Routes: []protocolRoute{{OperationID: "wasm.run", Methods: []string{http.MethodPost}, Path: "/run", Summary: "Run", Transport: "adapter", Target: "service", Adapter: &protocolAdapterConfig{Type: "wasm-envelope", ModuleFile: "wasmapi/modules/fixture.wasm"}}},
	})
	registry, err := newProtocolRegistry(protocolDir, dataDir)
	require.NoError(t, err)
	engine := gin.New()
	registerProtocolRoutes(engine, localRuntime{apiToken: "sk-local", protocols: registry})
	request := httptest.NewRequest(http.MethodPost, "/p/wasmapi/run", strings.NewReader(`{"x":1}`))
	request.Header.Set("Authorization", "Bearer sk-local")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.JSONEq(t, `{"runtime":"wasm"}`, response.Body.String())
}

func staticEnvelopeWASM(output []byte) []byte {
	section := func(id byte, body []byte) []byte {
		return append(append([]byte{id}, wasmULEB(uint64(len(body)))...), body...)
	}
	name := func(value string) []byte { return append(wasmULEB(uint64(len(value))), []byte(value)...) }
	types := []byte{3, 0x60, 1, 0x7f, 1, 0x7f, 0x60, 2, 0x7f, 0x7f, 1, 0x7e, 0x60, 2, 0x7f, 0x7f, 0}
	functions := []byte{3, 0, 1, 2}
	memory := []byte{1, 1, 2, 2}
	exports := []byte{4}
	exports = append(exports, name("memory")...)
	exports = append(exports, 2, 0)
	exports = append(exports, name("alloc")...)
	exports = append(exports, 0, 0)
	exports = append(exports, name("transform")...)
	exports = append(exports, 0, 1)
	exports = append(exports, name("dealloc")...)
	exports = append(exports, 0, 2)
	allocBody := []byte{0, 0x41, 0x80, 0x08, 0x0b}
	transformInstructions := append([]byte{0, 0x42}, wasmSLEB(int64(100))...)
	transformInstructions = append(transformInstructions, 0x42, 32, 0x86, 0x42)
	transformInstructions = append(transformInstructions, wasmSLEB(int64(len(output)))...)
	transformInstructions = append(transformInstructions, 0x84, 0x0b)
	deallocBody := []byte{0, 0x0b}
	code := []byte{3}
	for _, body := range [][]byte{allocBody, transformInstructions, deallocBody} {
		code = append(code, wasmULEB(uint64(len(body)))...)
		code = append(code, body...)
	}
	data := []byte{1, 0, 0x41}
	data = append(data, wasmSLEB(100)...)
	data = append(data, 0x0b)
	data = append(data, wasmULEB(uint64(len(output)))...)
	data = append(data, output...)
	module := []byte{0, 0x61, 0x73, 0x6d, 1, 0, 0, 0}
	module = append(module, section(1, types)...)
	module = append(module, section(3, functions)...)
	module = append(module, section(5, memory)...)
	module = append(module, section(7, exports)...)
	module = append(module, section(10, code)...)
	module = append(module, section(11, data)...)
	return module
}

func wasmULEB(value uint64) []byte {
	var encoded []byte
	for {
		part := byte(value & 0x7f)
		value >>= 7
		if value != 0 {
			part |= 0x80
		}
		encoded = append(encoded, part)
		if value == 0 {
			return encoded
		}
	}
}

func wasmSLEB(value int64) []byte {
	var encoded []byte
	for {
		part := byte(value & 0x7f)
		value >>= 7
		done := (value == 0 && part&0x40 == 0) || (value == -1 && part&0x40 != 0)
		if !done {
			part |= 0x80
		}
		encoded = append(encoded, part)
		if done {
			return encoded
		}
	}
}
