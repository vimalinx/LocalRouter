package main

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

func TestProtectDatabaseFiles(t *testing.T) {
	t.Parallel()

	databasePath := filepath.Join(t.TempDir(), "localrouter.db")
	require.NoError(t, os.WriteFile(databasePath, []byte("db"), 0o644))
	require.NoError(t, os.WriteFile(databasePath+"-wal", []byte("wal"), 0o644))
	require.NoError(t, protectDatabaseFiles(databasePath))

	for _, path := range []string{databasePath, databasePath + "-wal"} {
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
}

func TestLocalStoreMigratesLegacyLogsForUsageAccounting(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "localrouter.db")
	require.NoError(t, os.WriteFile(databasePath, nil, credentialMode))
	database, err := sql.Open("sqlite", databasePath)
	require.NoError(t, err)
	_, err = database.Exec(`CREATE TABLE logs (
		id INTEGER PRIMARY KEY, user_id INTEGER, created_at INTEGER, type INTEGER, content TEXT,
		username TEXT, token_name TEXT, model_name TEXT, quota INTEGER DEFAULT 0,
		prompt_tokens INTEGER DEFAULT 0, completion_tokens INTEGER DEFAULT 0, use_time INTEGER DEFAULT 0,
		is_stream NUMERIC, channel_id INTEGER, channel_name TEXT, token_id INTEGER DEFAULT 0,
		"group" TEXT, request_id TEXT DEFAULT ''
	)`)
	require.NoError(t, err)
	require.NoError(t, database.Close())

	store, err := openLocalStore(databasePath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	require.NoError(t, store.logRequest(t.Context(), localRequestLog{
		CreatedAt: time.Now().Unix(), Type: localLogTypeConsume, ModelName: "migrated-model",
		PromptTokens: 10, CompletionTokens: 2, CachedInputTokens: 4, ReasoningTokens: 1, TotalTokens: 12,
		CostUSD: 0.01, CostStatus: "reported",
	}))
	logs, _, err := store.listLogs(0, 1, 10)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	assert.Equal(t, int64(4), logs[0].CachedInputTokens)
	assert.Equal(t, int64(1), logs[0].ReasoningTokens)
	assert.Equal(t, int64(12), logs[0].TotalTokens)
	assert.Equal(t, "reported", logs[0].CostStatus)
}

func TestProtectedRuntimeFilesRejectSymlinksAndLooseModes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "target")
	require.NoError(t, os.WriteFile(target, []byte("protected-value\n"), 0o600))

	symlinkSecret := filepath.Join(root, "api-token")
	require.NoError(t, os.Symlink(target, symlinkSecret))
	_, err := readOrCreateSecret(symlinkSecret, "sk-")
	assert.ErrorContains(t, err, "not a symlink")

	looseSecret := filepath.Join(root, "admin-token")
	require.NoError(t, os.WriteFile(looseSecret, []byte("protected-value\n"), 0o644))
	_, err = readOrCreateSecret(looseSecret, "lr-admin-")
	assert.ErrorContains(t, err, "must not be group or world accessible")

	symlinkDatabase := filepath.Join(root, "localrouter.db")
	require.NoError(t, os.Symlink(target, symlinkDatabase))
	_, err = openLocalStore(symlinkDatabase)
	assert.ErrorContains(t, err, "not a symlink")
}

func TestReadOrCreateSecretCreatesPrivateRegularFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "api-token")
	secret, err := readOrCreateSecret(path, "sk-")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(secret, "sk-"))
	require.NoError(t, requirePrivateRegularFile(path, "credential file"))
	info, err := os.Lstat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestUpdateTokenKeyInvalidatesPreviousSecret(t *testing.T) {
	t.Parallel()
	store, err := openLocalStore(filepath.Join(t.TempDir(), "localrouter.db"))
	require.NoError(t, err)
	defer store.Close()
	root, err := store.ensureRootUser()
	require.NoError(t, err)
	id, err := store.createToken(root.ID, localToken{Name: "maintenance", AgentCode: "maintainer", AgentName: "Maintainer", Workspace: "operator", Runtime: "maintenance", ExpiredTime: -1})
	require.NoError(t, err)
	before, err := store.tokenByID(root.ID, int(id), true)
	require.NoError(t, err)

	require.NoError(t, store.updateTokenKey(root.ID, int(id), "sk-0123456789abcdef0123456789abcdef"))
	_, err = store.validateToken(before.Key)
	require.Error(t, err)
	updated, err := store.validateToken("sk-0123456789abcdef0123456789abcdef")
	require.NoError(t, err)
	assert.Equal(t, int(id), updated.ID)
}

func TestNormalizeLoopbackHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "default", input: "", want: "127.0.0.1"},
		{name: "localhost", input: "localhost", want: "127.0.0.1"},
		{name: "ipv4", input: "127.0.0.2", want: "127.0.0.2"},
		{name: "ipv6", input: "::1", want: "::1"},
		{name: "all interfaces ipv4", input: "0.0.0.0", wantErr: true},
		{name: "private lan", input: "192.168.1.8", wantErr: true},
		{name: "hostname", input: "gateway.local", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeLoopbackHost(test.input)
			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestNormalizeLANHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "default", input: "", want: "0.0.0.0"},
		{name: "all interfaces ipv4", input: "0.0.0.0", want: "0.0.0.0"},
		{name: "all interfaces ipv6", input: "::", want: "::"},
		{name: "private ipv4", input: "192.168.1.8", want: "192.168.1.8"},
		{name: "private ipv6", input: "fd00::8", want: "fd00::8"},
		{name: "loopback ipv4", input: "127.0.0.1", wantErr: true},
		{name: "loopback ipv6", input: "::1", wantErr: true},
		{name: "public address", input: "203.0.113.8", wantErr: true},
		{name: "hostname", input: "gateway.local", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeLANHost(test.input)
			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestParseAllowedOrigins(t *testing.T) {
	t.Parallel()

	origins, err := parseAllowedOrigins("https://router.home, http://192.168.1.20:3000, https://router.home/")
	require.NoError(t, err)
	assert.Equal(t, []string{"https://router.home", "http://192.168.1.20:3000"}, origins)

	for _, invalid := range []string{"router.home", "https://user@router.home", "https://router.home/path", "https://router.home?query=1", "file:///tmp/router"} {
		_, err := parseAllowedOrigins(invalid)
		assert.Error(t, err, invalid)
	}
}

func TestNormalizePublicBaseURL(t *testing.T) {
	t.Parallel()

	for input, expected := range map[string]string{
		"":                         "",
		"http://192.168.1.10:8318": "http://192.168.1.10:8318",
		"https://router.home/":     "https://router.home",
	} {
		actual, err := normalizePublicBaseURL(input)
		require.NoError(t, err)
		assert.Equal(t, expected, actual)
	}
	for _, invalid := range []string{"router.home", "https://router.home/path", "https://user@router.home", "file:///tmp/router"} {
		_, err := normalizePublicBaseURL(invalid)
		assert.Error(t, err, invalid)
	}
}

func TestWorkflowCallbackBaseURLNeverUsesRequestHost(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "http://attacker.invalid/w/demo", nil)

	localURL, err := workflowCallbackBaseURL(context, localRuntime{config: runtimeConfig{Host: "127.0.0.1", Port: 8317}})
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:8317", localURL)

	context.Set(serverScopeKey, lanServiceScope)
	lanURL, err := workflowCallbackBaseURL(context, localRuntime{config: runtimeConfig{LANHost: "192.168.1.10", LANPort: 8318}})
	require.NoError(t, err)
	assert.Equal(t, "http://192.168.1.10:8318", lanURL)

	configuredURL, err := workflowCallbackBaseURL(context, localRuntime{config: runtimeConfig{LANHost: "0.0.0.0", LANPort: 8318, LANPublicBaseURL: "https://router.home"}})
	require.NoError(t, err)
	assert.Equal(t, "https://router.home", configuredURL)

	_, err = workflowCallbackBaseURL(context, localRuntime{config: runtimeConfig{LANHost: "0.0.0.0", LANPort: 8318}})
	assert.ErrorContains(t, err, "LOCAL_GATEWAY_LAN_PUBLIC_BASE_URL")
}

func TestLANServerExposesOnlyServiceRoutes(t *testing.T) {
	engine := buildLANServer(localRuntime{config: runtimeConfig{LANAllowedOrigins: []string{"https://client.home"}}})
	routes := make(map[string]bool)
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = true
	}

	assert.True(t, routes["GET /healthz"])
	assert.True(t, routes["GET /.well-known/localrouter.json"])
	assert.True(t, routes["GET /agent/operations"])
	assert.True(t, routes["POST /mcp"])
	assert.True(t, routes["POST /v1/chat/completions"])
	assert.True(t, routes["POST /p/:protocol"])
	assert.False(t, routes["GET /local/status"])
	assert.False(t, routes["GET /local/api/summary"])
	assert.False(t, routes["POST /manage/mcp"])

	allowed := httptest.NewRecorder()
	allowedRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	allowedRequest.Header.Set("Origin", "https://client.home")
	engine.ServeHTTP(allowed, allowedRequest)
	assert.Equal(t, http.StatusOK, allowed.Code)
	assert.Equal(t, "https://client.home", allowed.Header().Get("Access-Control-Allow-Origin"))
	assert.Contains(t, allowed.Body.String(), `"scope":"lan-service"`)

	disallowed := httptest.NewRecorder()
	disallowedRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	disallowedRequest.Header.Set("Origin", "https://untrusted.home")
	engine.ServeHTTP(disallowed, disallowedRequest)
	assert.Equal(t, http.StatusForbidden, disallowed.Code)
}

func TestLocalCORSOnlyAllowsLoopbackOrigins(t *testing.T) {
	engine := gin.New()
	engine.Use(localSecurityHeaders())
	engine.GET("/ok", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	local := httptest.NewRecorder()
	localRequest := httptest.NewRequest(http.MethodOptions, "/ok", nil)
	localRequest.Header.Set("Origin", "http://localhost:5173")
	localRequest.Header.Set("Access-Control-Request-Method", "GET")
	engine.ServeHTTP(local, localRequest)
	assert.Equal(t, http.StatusNoContent, local.Code)
	assert.Equal(t, "http://localhost:5173", local.Header().Get("Access-Control-Allow-Origin"))

	external := httptest.NewRecorder()
	externalRequest := httptest.NewRequest(http.MethodOptions, "/ok", nil)
	externalRequest.Header.Set("Origin", "https://example.com")
	externalRequest.Header.Set("Access-Control-Request-Method", "GET")
	engine.ServeHTTP(external, externalRequest)
	assert.Equal(t, http.StatusForbidden, external.Code)
	assert.Empty(t, external.Header().Get("Access-Control-Allow-Origin"))
}

func TestLocalAdminAuth(t *testing.T) {
	engine := gin.New()
	store := newAdminTokenStore("expected")
	auth := &adminAuthStore{enabled: true}
	tokenPath := filepath.Join(t.TempDir(), "admin-token")
	engine.GET("/protected", localAdminAuth(auth, store, localUser{
		ID:       7,
		Username: "local",
		Role:     localRootRole,
	}), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"id": c.GetInt("id"), "group": c.GetString("group")})
	})

	unauthorized := httptest.NewRecorder()
	engine.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/protected", nil))
	assert.Equal(t, http.StatusUnauthorized, unauthorized.Code)

	authorized := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set(adminTokenHeader, "expected")
	engine.ServeHTTP(authorized, request)
	assert.Equal(t, http.StatusOK, authorized.Code)
	assert.JSONEq(t, `{"id":7,"group":"default"}`, authorized.Body.String())

	require.NoError(t, store.replacePersisted("expected", "replacement", tokenPath))
	oldToken := httptest.NewRecorder()
	oldRequest := httptest.NewRequest(http.MethodGet, "/protected", nil)
	oldRequest.Header.Set(adminTokenHeader, "expected")
	engine.ServeHTTP(oldToken, oldRequest)
	assert.Equal(t, http.StatusUnauthorized, oldToken.Code)

	newToken := httptest.NewRecorder()
	newRequest := httptest.NewRequest(http.MethodGet, "/protected", nil)
	newRequest.Header.Set(adminTokenHeader, "replacement")
	engine.ServeHTTP(newToken, newRequest)
	assert.Equal(t, http.StatusOK, newToken.Code)

	auth.enabled = false
	withoutAuthentication := httptest.NewRecorder()
	engine.ServeHTTP(withoutAuthentication, httptest.NewRequest(http.MethodGet, "/protected", nil))
	assert.Equal(t, http.StatusOK, withoutAuthentication.Code)
}

func TestAdminAuthSettingDefaultsOffAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "admin-auth.json")
	store, err := newAdminAuthStore(path)
	require.NoError(t, err)
	assert.False(t, store.isEnabled())

	require.NoError(t, store.setEnabled(true))
	reloaded, err := newAdminAuthStore(path)
	require.NoError(t, err)
	assert.True(t, reloaded.isEnabled())
	require.NoError(t, requirePrivateRegularFile(path, "administrator authentication setting"))
}

func TestAdminTokenChangePersistsAndRotatesImmediately(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "admin-token")
	require.NoError(t, os.WriteFile(tokenPath, []byte("old-admin-token-value\n"), 0o600))
	store := newAdminTokenStore("old-admin-token-value")
	runtime := localRuntime{
		config:     runtimeConfig{AdminTokenFile: tokenPath},
		adminAuth:  &adminAuthStore{enabled: true},
		adminToken: store,
		rootUser:   localUser{ID: 7, Username: "local", Role: localRootRole},
	}

	engine := gin.New()
	group := engine.Group("/local/api")
	group.Use(localAdminAuth(runtime.adminAuth, store, runtime.rootUser))
	group.PUT("/admin-token", handleAdminTokenChange(runtime))

	change := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/local/api/admin-token", strings.NewReader(`{"token":"my-custom-admin-token-2026"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(adminTokenHeader, "old-admin-token-value")
	engine.ServeHTTP(change, request)
	require.Equal(t, http.StatusOK, change.Code)
	assert.NotContains(t, change.Body.String(), "my-custom-admin-token-2026")

	stored, err := os.ReadFile(tokenPath)
	require.NoError(t, err)
	assert.Equal(t, "my-custom-admin-token-2026\n", string(stored))
	info, err := os.Stat(tokenPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	oldToken := httptest.NewRecorder()
	oldRequest := httptest.NewRequest(http.MethodPut, "/local/api/admin-token", strings.NewReader(`{"token":"another-custom-token-2026"}`))
	oldRequest.Header.Set("Content-Type", "application/json")
	oldRequest.Header.Set(adminTokenHeader, "old-admin-token-value")
	engine.ServeHTTP(oldToken, oldRequest)
	assert.Equal(t, http.StatusUnauthorized, oldToken.Code)

	newToken := httptest.NewRecorder()
	newRequest := httptest.NewRequest(http.MethodPut, "/local/api/admin-token", strings.NewReader(`{"token":"another-custom-token-2026"}`))
	newRequest.Header.Set("Content-Type", "application/json")
	newRequest.Header.Set(adminTokenHeader, "my-custom-admin-token-2026")
	engine.ServeHTTP(newToken, newRequest)
	assert.Equal(t, http.StatusOK, newToken.Code)
}

func TestAdminAuthCanBeEnabledWithCustomTokenAndDisabled(t *testing.T) {
	root := t.TempDir()
	tokenPath := filepath.Join(root, "admin-token")
	authPath := filepath.Join(root, "admin-auth.json")
	require.NoError(t, os.WriteFile(tokenPath, []byte("generated-but-not-required\n"), 0o600))
	auth, err := newAdminAuthStore(authPath)
	require.NoError(t, err)
	runtime := localRuntime{
		config:     runtimeConfig{AdminAuthFile: authPath, AdminTokenFile: tokenPath},
		adminAuth:  auth,
		adminToken: newAdminTokenStore("generated-but-not-required"),
		rootUser:   localUser{ID: 7, Username: "local", Role: localRootRole},
	}

	engine := gin.New()
	group := engine.Group("/local/api")
	group.Use(localAdminAuth(runtime.adminAuth, runtime.adminToken, runtime.rootUser))
	group.GET("/summary", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	group.PUT("/admin-auth", handleAdminAuthChange(runtime))

	openResponse := httptest.NewRecorder()
	engine.ServeHTTP(openResponse, httptest.NewRequest(http.MethodGet, "/local/api/summary", nil))
	assert.Equal(t, http.StatusNoContent, openResponse.Code)

	enableResponse := performJSON(engine, http.MethodPut, "/local/api/admin-auth", `{"enabled":true,"token":"my-custom-console-password"}`)
	require.Equal(t, http.StatusOK, enableResponse.Code, enableResponse.Body.String())
	assert.True(t, auth.isEnabled())
	storedToken, err := os.ReadFile(tokenPath)
	require.NoError(t, err)
	assert.Equal(t, "my-custom-console-password\n", string(storedToken))

	lockedResponse := httptest.NewRecorder()
	engine.ServeHTTP(lockedResponse, httptest.NewRequest(http.MethodGet, "/local/api/summary", nil))
	assert.Equal(t, http.StatusUnauthorized, lockedResponse.Code)

	disableRequest := httptest.NewRequest(http.MethodPut, "/local/api/admin-auth", strings.NewReader(`{"enabled":false}`))
	disableRequest.Header.Set("Content-Type", "application/json")
	disableRequest.Header.Set(adminTokenHeader, "my-custom-console-password")
	disableResponse := httptest.NewRecorder()
	engine.ServeHTTP(disableResponse, disableRequest)
	require.Equal(t, http.StatusOK, disableResponse.Code, disableResponse.Body.String())
	assert.False(t, auth.isEnabled())

	reopenedResponse := httptest.NewRecorder()
	engine.ServeHTTP(reopenedResponse, httptest.NewRequest(http.MethodGet, "/local/api/summary", nil))
	assert.Equal(t, http.StatusNoContent, reopenedResponse.Code)
}

func TestAdminTokenValidation(t *testing.T) {
	for _, token := range []string{"short", " leading-or-trailing-token", "trailing-or-leading-token ", "contains\nnewline-value", "包含非 ASCII 字符的密钥"} {
		assert.Error(t, validateAdminToken(token), token)
	}
	assert.NoError(t, validateAdminToken("correct horse battery staple"))
}

func TestLocalTokenCreationUsesUnlimitedDefaultGroup(t *testing.T) {
	store, err := openLocalStore(filepath.Join(t.TempDir(), "localrouter.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	root, err := store.ensureRootUser()
	require.NoError(t, err)
	runtime := localRuntime{store: store, rootUser: root}
	engine := gin.New()
	engine.POST("/token", handleAddToken(runtime))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(`{"name":"client","agent_code":"agent-007","agent_name":"Build Agent","workspace":"/workspace/project","runtime":"codex","group":"paid","unlimited_quota":false}`))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	tokens, _, err := store.listTokens(root.ID, 1, 20)
	require.NoError(t, err)
	require.Len(t, tokens, 1)
	assert.Equal(t, "default", tokens[0].Group)
	assert.True(t, tokens[0].UnlimitedQuota)
	assert.Equal(t, int64(-1), tokens[0].ExpiredTime)
	assert.Equal(t, "agent-007", tokens[0].AgentCode)
	assert.Equal(t, "/workspace/project", tokens[0].Workspace)
}

func TestLocalAPITokenAuthorizationAcceptsIssuedRootToken(t *testing.T) {
	validator := func(key string) (*localToken, error) {
		switch key {
		case "issued-root-token":
			return &localToken{UserID: 7, Status: localStatusEnabled}, nil
		case "other-user-token":
			return &localToken{UserID: 8, Status: localStatusEnabled}, nil
		default:
			return nil, errors.New("invalid token")
		}
	}

	authorized := func(rootUserID int, provided string) bool {
		_, ok := resolveLocalAPIToken(rootUserID, provided, validator)
		return ok
	}
	assert.True(t, authorized(7, "sk-issued-root-token"))
	assert.False(t, authorized(7, "sk-bootstrap"))
	assert.False(t, authorized(7, "sk-other-user-token"))
	assert.False(t, authorized(7, "sk-invalid"))
	assert.False(t, authorized(0, "sk-issued-root-token"))
}

func TestRelaySurfaceKeepsProtocolsAndOmitsPlatformOAuth(t *testing.T) {
	engine := gin.New()
	registerRelayRoutesWithRuntime(engine, localRuntime{})

	routes := make(map[string]bool)
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = true
	}

	assert.True(t, routes["POST /v1/chat/completions"])
	assert.True(t, routes["POST /v1/responses"])
	assert.True(t, routes["POST /v1/messages"])
	assert.True(t, routes["POST /v1beta/models/*path"])
	assert.True(t, routes["POST /v1/images/generations"])
	assert.False(t, routes["GET /api/oauth/:provider"])
	assert.False(t, routes["POST /api/user/register"])
	assert.False(t, routes["POST /api/stripe/webhook"])
	assert.False(t, routes["GET /api/subscription/plans"])
}

func TestProviderCatalogOnlyAdvertisesNativeAdapters(t *testing.T) {
	engine := gin.New()
	profiles := testChannelProfiles(t)
	engine.GET("/providers", listProviders(localRuntime{channelProfiles: profiles}))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/providers", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), `"id":100`) // legacy Codex subscription adapter
	assert.NotContains(t, recorder.Body.String(), "ChatGPT Subscription (Codex)")
	assert.Contains(t, recorder.Body.String(), "OpenAI compatible")
	assert.Contains(t, recorder.Body.String(), `"key":"anthropic-messages"`)
}

func TestConsoleServesCompiledWebAssets(t *testing.T) {
	engine := gin.New()
	registerConsoleRoutes(engine, localRuntime{config: runtimeConfig{
		Host:           "127.0.0.1",
		Port:           8317,
		AdminTokenFile: "/private-host/LocalRouter/data/admin-token",
		APITokenFile:   "/private-host/LocalRouter/data/api-token",
		DatabasePath:   "/private-host/LocalRouter/data/localrouter.db",
		ProtocolDir:    "/private-host/LocalRouter/protocols",
		StateDir:       "/private-host/LocalRouter/state",
		CacheDir:       "/private-host/LocalRouter/cache",
	}})

	statusRecorder := httptest.NewRecorder()
	engine.ServeHTTP(statusRecorder, httptest.NewRequest(http.MethodGet, "/local/status", nil))
	require.Equal(t, http.StatusOK, statusRecorder.Code)
	assert.NotContains(t, statusRecorder.Body.String(), "/private-host")
	assert.Contains(t, statusRecorder.Body.String(), `$XDG_DATA_HOME/localrouter/admin-token`)
	assert.Contains(t, statusRecorder.Body.String(), `$XDG_CONFIG_HOME/localrouter/protocols`)
	assert.Contains(t, statusRecorder.Body.String(), `"path_layout":"xdg-v1"`)

	indexRecorder := httptest.NewRecorder()
	engine.ServeHTTP(indexRecorder, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusOK, indexRecorder.Code)
	assert.Contains(t, indexRecorder.Header().Get("Content-Type"), "text/html")
	assert.Contains(t, indexRecorder.Body.String(), `id="root"`)

	assetPattern := regexp.MustCompile(`(?:src|href)="(/assets/[^"]+)"`)
	assetMatch := assetPattern.FindStringSubmatch(indexRecorder.Body.String())
	require.Len(t, assetMatch, 2, "compiled console should reference a bundled asset")

	assetRecorder := httptest.NewRecorder()
	engine.ServeHTTP(assetRecorder, httptest.NewRequest(http.MethodGet, assetMatch[1], nil))
	assert.Equal(t, http.StatusOK, assetRecorder.Code)
	assert.NotEmpty(t, assetRecorder.Body.Bytes())
}
