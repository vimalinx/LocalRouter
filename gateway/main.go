package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

const (
	defaultHost       = "127.0.0.1"
	defaultPort       = 8317
	defaultLANHost    = "0.0.0.0"
	defaultLANPort    = 8318
	localTokenName    = "LocalRouter default"
	adminTokenHeader  = "X-Local-Admin"
	credentialDirMode = 0o700
	credentialMode    = 0o600
	serverScopeKey    = "localrouter.server-scope"
	loopbackScope     = "loopback"
	lanServiceScope   = "lan-service"
)

// The web console is a compiled local-only React application.
//
//go:embed web/* protocols channel-profiles.json
var webFiles embed.FS

type runtimeConfig struct {
	Host                string
	Port                int
	LANEnabled          bool
	LANHost             string
	LANPort             int
	LANAllowedOrigins   []string
	LANPublicBaseURL    string
	ConfigDir           string
	DataDir             string
	StateDir            string
	CacheDir            string
	ProtocolDir         string
	ChannelProfilesPath string
	DatabasePath        string
	AdminAuthFile       string
	AdminTokenFile      string
	APITokenFile        string
}

type localRuntime struct {
	config          runtimeConfig
	adminAuth       *adminAuthStore
	adminToken      *adminTokenStore
	apiToken        string
	rootUser        localUser
	store           *localStore
	relayClient     *http.Client
	balancer        *localRelayBalancer
	protocols       *protocolRegistry
	policies        *tokenPolicyStore
	events          *protocolEventStore
	channelProfiles *localChannelProfileRegistry
}

type adminTokenStore struct {
	mu    sync.RWMutex
	token string
}

type adminAuthDocument struct {
	SchemaVersion string `json:"schema_version"`
	Enabled       bool   `json:"enabled"`
}

type adminAuthStore struct {
	mu      sync.RWMutex
	path    string
	enabled bool
}

func newAdminAuthStore(path string) (*adminAuthStore, error) {
	store := &adminAuthStore{path: path}
	exists, err := inspectPrivateRegularFile(path, "administrator authentication setting")
	if err != nil {
		return nil, err
	}
	if !exists {
		return store, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read administrator authentication setting: %w", err)
	}
	var document adminAuthDocument
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil || document.SchemaVersion != "1" {
		return nil, errors.New("administrator authentication setting is invalid")
	}
	store.enabled = document.Enabled
	return store, nil
}

func (store *adminAuthStore) isEnabled() bool {
	if store == nil {
		return false
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.enabled
}

func (store *adminAuthStore) setEnabled(enabled bool) error {
	if store == nil {
		return errors.New("administrator authentication setting is unavailable")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	document, err := json.Marshal(adminAuthDocument{SchemaVersion: "1", Enabled: enabled})
	if err != nil {
		return err
	}
	if err := writeSecretAtomic(store.path, string(document)); err != nil {
		return fmt.Errorf("persist administrator authentication setting: %w", err)
	}
	store.enabled = enabled
	return nil
}

func newAdminTokenStore(token string) *adminTokenStore {
	return &adminTokenStore{token: token}
}

func (store *adminTokenStore) matches(provided string) bool {
	store.mu.RLock()
	expected := store.token
	store.mu.RUnlock()
	return len(provided) == len(expected) && subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func (store *adminTokenStore) replacePersisted(current string, replacement string, path string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(current) != len(store.token) || subtle.ConstantTimeCompare([]byte(current), []byte(store.token)) != 1 {
		return errors.New("admin token changed before this request completed")
	}
	if err := writeSecretAtomic(path, replacement); err != nil {
		return err
	}
	store.token = replacement
	return nil
}

func (store *adminTokenStore) replacePersistedWithoutCurrent(replacement string, path string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := writeSecretAtomic(path, replacement); err != nil {
		return err
	}
	store.token = replacement
	return nil
}

func main() {
	if handled, err := handleLocalCommand(os.Args[1:]); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, "LocalRouter command error:", err)
			os.Exit(1)
		}
		return
	}
	config, err := loadRuntimeConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "LocalRouter configuration error:", err)
		os.Exit(1)
	}

	runtime, err := initializeRuntime(config)
	if err != nil {
		fmt.Fprintln(os.Stderr, "LocalRouter initialization error:", err)
		os.Exit(1)
	}
	defer func() {
		if closeErr := runtime.store.Close(); closeErr != nil {
			fmt.Fprintln(os.Stderr, "LocalRouter database close error:", closeErr)
		}
	}()

	if os.Getenv("GIN_MODE") != "debug" {
		gin.SetMode(gin.ReleaseMode)
	}
	servers := []namedHTTPServer{newHTTPServer("local", config.Host, config.Port, buildServer(runtime))}
	if config.LANEnabled {
		servers = append(servers, newHTTPServer("lan-service", config.LANHost, config.LANPort, buildLANServer(runtime)))
	}
	serverErrors := make(chan namedServerError, len(servers))
	for _, configured := range servers {
		configured := configured
		go func() {
			fmt.Fprintf(os.Stderr, "LocalRouter %s listener on http://%s\n", configured.name, configured.server.Addr)
			serverErrors <- namedServerError{name: configured.name, err: configured.server.ListenAndServe()}
		}()
	}
	runtime.protocols.startWorkflowScheduler(runtime)
	defer runtime.protocols.stopWorkflowScheduler()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-stop:
		fmt.Fprintln(os.Stderr, "received signal "+sig.String()+", shutting down")
	case serveFailure := <-serverErrors:
		if !errors.Is(serveFailure.err, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "LocalRouter %s listener failed: %v\n", serveFailure.name, serveFailure.err)
		}
	}
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelShutdown()
	for _, configured := range servers {
		if err := configured.server.Shutdown(shutdownContext); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "LocalRouter %s shutdown failed: %v\n", configured.name, err)
		}
	}
}

type namedHTTPServer struct {
	name   string
	server *http.Server
}

type namedServerError struct {
	name string
	err  error
}

func newHTTPServer(name, host string, port int, handler http.Handler) namedHTTPServer {
	return namedHTTPServer{name: name, server: &http.Server{
		Addr:              net.JoinHostPort(host, strconv.Itoa(port)),
		Handler:           h2c.NewHandler(handler, &http2.Server{}),
		ReadHeaderTimeout: 10 * time.Second,
	}}
}

func loadRuntimeConfig() (runtimeConfig, error) {
	xdg, err := resolveXDGDirectories()
	if err != nil {
		return runtimeConfig{}, err
	}
	configDir, err := resolveDirectoryOverride("LOCAL_GATEWAY_CONFIG_DIR", xdg.ConfigDir)
	if err != nil {
		return runtimeConfig{}, err
	}
	_ = godotenv.Load(filepath.Join(configDir, "config.env"))
	_ = godotenv.Load(".env")

	host, err := normalizeLoopbackHost(strings.TrimSpace(os.Getenv("LOCAL_GATEWAY_HOST")))
	if err != nil {
		return runtimeConfig{}, err
	}
	port := defaultPort
	rawPort := strings.TrimSpace(os.Getenv("LOCAL_GATEWAY_PORT"))
	if rawPort == "" {
		rawPort = strings.TrimSpace(os.Getenv("PORT"))
	}
	if rawPort != "" {
		port, err = strconv.Atoi(rawPort)
		if err != nil || port < 1 || port > 65535 {
			return runtimeConfig{}, fmt.Errorf("LOCAL_GATEWAY_PORT must be between 1 and 65535")
		}
	}
	lanEnabled, err := parseEnvironmentBoolean("LOCAL_GATEWAY_LAN_ENABLED")
	if err != nil {
		return runtimeConfig{}, err
	}
	lanHost := ""
	lanPort := 0
	var lanAllowedOrigins []string
	lanPublicBaseURL := ""
	if lanEnabled {
		lanHost, err = normalizeLANHost(strings.TrimSpace(os.Getenv("LOCAL_GATEWAY_LAN_HOST")))
		if err != nil {
			return runtimeConfig{}, err
		}
		lanPort = defaultLANPort
		rawLANPort := strings.TrimSpace(os.Getenv("LOCAL_GATEWAY_LAN_PORT"))
		if rawLANPort != "" {
			lanPort, err = strconv.Atoi(rawLANPort)
			if err != nil || lanPort < 1 || lanPort > 65535 {
				return runtimeConfig{}, errors.New("LOCAL_GATEWAY_LAN_PORT must be between 1 and 65535")
			}
		}
		if lanPort == port {
			return runtimeConfig{}, errors.New("LOCAL_GATEWAY_LAN_PORT must differ from LOCAL_GATEWAY_PORT")
		}
		lanAllowedOrigins, err = parseAllowedOrigins(os.Getenv("LOCAL_GATEWAY_LAN_ALLOWED_ORIGINS"))
		if err != nil {
			return runtimeConfig{}, err
		}
		lanPublicBaseURL, err = normalizePublicBaseURL(os.Getenv("LOCAL_GATEWAY_LAN_PUBLIC_BASE_URL"))
		if err != nil {
			return runtimeConfig{}, err
		}
	}

	dataOverride := strings.TrimSpace(os.Getenv("LOCAL_GATEWAY_DATA_DIR")) != ""
	dataDir, err := resolveDirectoryOverride("LOCAL_GATEWAY_DATA_DIR", xdg.DataDir)
	if err != nil {
		return runtimeConfig{}, err
	}
	stateDefault := xdg.StateDir
	if dataOverride && strings.TrimSpace(os.Getenv("LOCAL_GATEWAY_STATE_DIR")) == "" {
		stateDefault = dataDir
	}
	stateDir, err := resolveDirectoryOverride("LOCAL_GATEWAY_STATE_DIR", stateDefault)
	if err != nil {
		return runtimeConfig{}, err
	}
	cacheDir, err := resolveDirectoryOverride("LOCAL_GATEWAY_CACHE_DIR", xdg.CacheDir)
	if err != nil {
		return runtimeConfig{}, err
	}
	for label, path := range map[string]string{
		"configuration": configDir,
		"data":          dataDir,
		"state":         stateDir,
		"cache":         cacheDir,
	} {
		if err := ensurePrivateDirectory(path); err != nil {
			return runtimeConfig{}, fmt.Errorf("prepare %s directory: %w", label, err)
		}
	}
	if err := ensurePrivateDirectory(filepath.Join(dataDir, "protocol-secrets")); err != nil {
		return runtimeConfig{}, fmt.Errorf("create protocol secret directory: %w", err)
	}
	protocolOverride := strings.TrimSpace(os.Getenv("LOCAL_GATEWAY_PROTOCOL_DIR")) != ""
	protocolDir, err := resolveDirectoryOverride("LOCAL_GATEWAY_PROTOCOL_DIR", filepath.Join(configDir, "protocols"))
	if err != nil {
		return runtimeConfig{}, err
	}
	if err := ensurePrivateDirectory(protocolDir); err != nil {
		return runtimeConfig{}, fmt.Errorf("create protocol directory: %w", err)
	}
	if !protocolOverride {
		if err := seedEmbeddedProtocols(protocolDir); err != nil {
			return runtimeConfig{}, fmt.Errorf("seed protocol authoring assets: %w", err)
		}
	}
	channelProfilesPath := strings.TrimSpace(os.Getenv("LOCAL_GATEWAY_CHANNEL_PROFILES_FILE"))
	if channelProfilesPath == "" {
		channelProfilesPath = filepath.Join(configDir, "channel-profiles.json")
		if err := seedEmbeddedChannelProfiles(channelProfilesPath); err != nil {
			return runtimeConfig{}, fmt.Errorf("seed channel protocol profiles: %w", err)
		}
	} else if !filepath.IsAbs(channelProfilesPath) {
		return runtimeConfig{}, errors.New("LOCAL_GATEWAY_CHANNEL_PROFILES_FILE must be absolute")
	}

	return runtimeConfig{
		Host:                host,
		Port:                port,
		LANEnabled:          lanEnabled,
		LANHost:             lanHost,
		LANPort:             lanPort,
		LANAllowedOrigins:   lanAllowedOrigins,
		LANPublicBaseURL:    lanPublicBaseURL,
		ConfigDir:           configDir,
		DataDir:             dataDir,
		StateDir:            stateDir,
		CacheDir:            cacheDir,
		ProtocolDir:         protocolDir,
		ChannelProfilesPath: channelProfilesPath,
		DatabasePath:        filepath.Join(dataDir, "localrouter.db"),
		AdminAuthFile:       filepath.Join(dataDir, "admin-auth.json"),
		AdminTokenFile:      filepath.Join(dataDir, "admin-token"),
		APITokenFile:        filepath.Join(dataDir, "api-token"),
	}, nil
}

func parseEnvironmentBoolean(name string) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return false, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", name)
	}
	return value, nil
}

func normalizeLoopbackHost(host string) (string, error) {
	if host == "" || strings.EqualFold(host, "localhost") {
		return defaultHost, nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", fmt.Errorf("LOCAL_GATEWAY_HOST must be a loopback IP, got %q", host)
	}
	return ip.String(), nil
}

func normalizeLANHost(host string) (string, error) {
	if host == "" {
		host = defaultLANHost
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "", fmt.Errorf("LOCAL_GATEWAY_LAN_HOST must be an IP address, got %q", host)
	}
	if ip.IsLoopback() {
		return "", fmt.Errorf("LOCAL_GATEWAY_LAN_HOST must not be loopback, got %q", host)
	}
	if !ip.IsUnspecified() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() {
		return "", fmt.Errorf("LOCAL_GATEWAY_LAN_HOST must be unspecified, private, or link-local, got %q", host)
	}
	return ip.String(), nil
}

func parseAllowedOrigins(raw string) ([]string, error) {
	seen := make(map[string]bool)
	origins := make([]string, 0)
	for _, value := range strings.Split(raw, ",") {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		parsed, err := url.Parse(value)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
			return nil, fmt.Errorf("LOCAL_GATEWAY_LAN_ALLOWED_ORIGINS contains an invalid origin %q", value)
		}
		normalized := parsed.Scheme + "://" + parsed.Host
		if !seen[normalized] {
			seen[normalized] = true
			origins = append(origins, normalized)
		}
	}
	return origins, nil
}

func normalizePublicBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", fmt.Errorf("LOCAL_GATEWAY_LAN_PUBLIC_BASE_URL must be an http(s) origin, got %q", raw)
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

func initializeRuntime(config runtimeConfig) (localRuntime, error) {
	store, err := openLocalStore(config.DatabasePath)
	if err != nil {
		return localRuntime{}, fmt.Errorf("initialize database: %w", err)
	}
	complete := false
	defer func() {
		if !complete {
			_ = store.Close()
		}
	}()
	if err := protectDatabaseFiles(config.DatabasePath); err != nil {
		return localRuntime{}, err
	}
	rootUser, err := store.ensureRootUser()
	if err != nil {
		return localRuntime{}, fmt.Errorf("initialize local operator: %w", err)
	}
	adminToken, err := readOrCreateSecret(config.AdminTokenFile, "lr-admin-")
	if err != nil {
		return localRuntime{}, err
	}
	adminAuth, err := newAdminAuthStore(config.AdminAuthFile)
	if err != nil {
		return localRuntime{}, err
	}
	apiToken, err := readOrCreateSecret(config.APITokenFile, "sk-")
	if err != nil {
		return localRuntime{}, err
	}
	if err := store.ensureDefaultToken(rootUser.ID, apiToken); err != nil {
		return localRuntime{}, err
	}
	protocols, err := newProtocolRegistryWithState(config.ProtocolDir, config.DataDir, config.StateDir)
	if err != nil {
		return localRuntime{}, fmt.Errorf("load protocol templates: %w", err)
	}
	channelProfiles, err := loadLocalChannelProfiles(config.ChannelProfilesPath)
	if err != nil {
		return localRuntime{}, fmt.Errorf("load channel protocol profiles: %w", err)
	}
	policies, err := newTokenPolicyStore(config.DataDir)
	if err != nil {
		return localRuntime{}, fmt.Errorf("load token policies: %w", err)
	}
	bootstrapToken, err := store.validateToken(apiToken)
	if err != nil {
		return localRuntime{}, fmt.Errorf("resolve bootstrap service token: %w", err)
	}
	policies.protectedServiceTokenID = bootstrapToken.ID
	events, err := newProtocolEventStore(config.StateDir)
	if err != nil {
		return localRuntime{}, fmt.Errorf("initialize protocol event store: %w", err)
	}
	protocols.policies = policies
	protocols.events = events

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 128
	transport.MaxIdleConnsPerHost = 32
	transport.IdleConnTimeout = 90 * time.Second
	complete = true
	return localRuntime{
		config: config, adminAuth: adminAuth, adminToken: newAdminTokenStore(adminToken), apiToken: apiToken,
		rootUser: rootUser, store: store, relayClient: &http.Client{Transport: transport},
		balancer: newLocalRelayBalancer(), protocols: protocols, policies: policies, events: events, channelProfiles: channelProfiles,
	}, nil
}

func protectDatabaseFiles(databasePath string) error {
	for _, path := range []string{databasePath, databasePath + "-wal", databasePath + "-shm"} {
		exists, err := inspectOwnedRegularFile(path, "database file")
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if err := os.Chmod(path, credentialMode); err != nil {
			return fmt.Errorf("protect database file %s: %w", path, err)
		}
	}
	return nil
}

func readOrCreateSecret(path string, prefix string) (string, error) {
	exists, err := inspectPrivateRegularFile(path, "credential file")
	if err != nil {
		return "", err
	}
	if !exists {
		secret, err := randomSecret(32, prefix)
		if err != nil {
			return "", err
		}
		file, err := createPrivateFile(path, "credential file")
		if err != nil {
			return "", err
		}
		complete := false
		defer func() {
			_ = file.Close()
			if !complete {
				_ = os.Remove(path)
			}
		}()
		if _, err := file.WriteString(secret + "\n"); err != nil {
			return "", fmt.Errorf("write credential file %s: %w", path, err)
		}
		if err := file.Sync(); err != nil {
			return "", fmt.Errorf("sync credential file %s: %w", path, err)
		}
		if err := file.Close(); err != nil {
			return "", fmt.Errorf("close credential file %s: %w", path, err)
		}
		complete = true
		return secret, nil
	}
	data, err := os.ReadFile(path)
	if err == nil {
		secret := strings.TrimSpace(string(data))
		if secret == "" {
			return "", fmt.Errorf("credential file is empty: %s", path)
		}
		return secret, nil
	}
	return "", fmt.Errorf("read credential file %s: %w", path, err)
}

func randomSecret(byteCount int, prefix string) (string, error) {
	buffer := make([]byte, byteCount)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate credential: %w", err)
	}
	return prefix + hex.EncodeToString(buffer), nil
}

func validateAdminToken(token string) error {
	if token != strings.TrimSpace(token) {
		return errors.New("admin token must not start or end with whitespace")
	}
	if len(token) < 16 {
		return errors.New("admin token must contain at least 16 bytes")
	}
	if len(token) > 512 {
		return errors.New("admin token must not exceed 512 bytes")
	}
	for _, character := range token {
		if character < 0x20 || character > 0x7e {
			return errors.New("admin token must contain printable ASCII characters only")
		}
	}
	return nil
}

func writeSecretAtomic(path string, secret string) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".localrouter-secret-*")
	if err != nil {
		return fmt.Errorf("create temporary credential file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()

	if err := temporary.Chmod(credentialMode); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect temporary credential file: %w", err)
	}
	if _, err := temporary.WriteString(secret + "\n"); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary credential file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync temporary credential file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary credential file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace credential file: %w", err)
	}
	if err := os.Chmod(path, credentialMode); err != nil {
		return fmt.Errorf("protect credential file: %w", err)
	}
	return nil
}

func buildServer(runtime localRuntime) *gin.Engine {
	engine := gin.New()
	engine.Use(gin.Recovery(), serverScope(loopbackScope), localSecurityHeaders())

	registerConsoleRoutes(engine, runtime)
	registerLocalAdminRoutes(engine, runtime)
	registerProtocolRoutes(engine, runtime)
	registerRelayRoutesWithRuntime(engine, runtime)
	return engine
}

func buildLANServer(runtime localRuntime) *gin.Engine {
	engine := gin.New()
	engine.Use(gin.Recovery(), serverScope(lanServiceScope), lanSecurityHeaders(runtime.config.LANAllowedOrigins))

	registerLANLandingRoutes(engine)
	registerProtocolDocumentationRoutes(engine, runtime)
	registerProtocolConsumerRoutes(engine, runtime)
	registerRelayRoutesWithRuntime(engine, runtime)
	return engine
}

func serverScope(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(serverScopeKey, scope)
		c.Next()
	}
}

func requestServerScope(c *gin.Context) string {
	if scope := c.GetString(serverScopeKey); scope != "" {
		return scope
	}
	return loopbackScope
}

func localSecurityHeaders() gin.HandlerFunc {
	return securityHeaders(loopbackOrigin, false)
}

func lanSecurityHeaders(allowedOrigins []string) gin.HandlerFunc {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowed[origin] = true
	}
	return securityHeaders(func(origin string) bool { return allowed[origin] }, true)
}

func securityHeaders(originAllowed func(string) bool, rejectDisallowed bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "no-referrer")
		origin := strings.TrimSpace(c.GetHeader("Origin"))
		allowed := origin == "" || originAllowed(origin)
		if origin != "" && allowed {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Api-Key, X-Goog-Api-Key, Anthropic-Version")
			c.Header("Access-Control-Max-Age", "600")
		}
		if origin != "" && !allowed && rejectDisallowed {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		if c.Request.Method == http.MethodOptions && c.GetHeader("Access-Control-Request-Method") != "" {
			if !allowed {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func loopbackOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return false
	}
	if strings.EqualFold(parsed.Hostname(), "localhost") {
		return true
	}
	ip := net.ParseIP(parsed.Hostname())
	return ip != nil && ip.IsLoopback()
}

func registerConsoleRoutes(engine *gin.Engine, runtime localRuntime) {
	indexPage, err := webFiles.ReadFile("web/index.html")
	if err != nil {
		panic(err)
	}
	webRoot, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(err)
	}
	assetServer := http.FileServer(http.FS(webRoot))
	engine.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"ok":     true,
			"mode":   "local-self-use",
			"engine": "localrouter-native",
			"oauth":  false,
		})
	})
	engine.GET("/local/status", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"success":            true,
			"mode":               "local-self-use",
			"listen":             net.JoinHostPort(runtime.config.Host, strconv.Itoa(runtime.config.Port)),
			"admin_auth_enabled": runtime.adminAuth.isEnabled(),
			"admin_token_file":   "$XDG_DATA_HOME/localrouter/admin-token",
			"api_token_file":     "$XDG_DATA_HOME/localrouter/api-token",
			"database_path":      "$XDG_DATA_HOME/localrouter/localrouter.db",
			"protocol_dir":       "$XDG_CONFIG_HOME/localrouter/protocols",
			"channel_profiles":   "$XDG_CONFIG_HOME/localrouter/channel-profiles.json",
			"state_dir":          "$XDG_STATE_HOME/localrouter",
			"cache_dir":          "$XDG_CACHE_HOME/localrouter",
			"path_layout":        "xdg-v1",
			"engine":             "localrouter-native",
			"oauth":              "external-maintainer",
			"lan_service":        lanServiceStatus(runtime.config),
		})
	})
	engine.GET("/", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexPage)
	})
	engine.GET("/assets/*filepath", gin.WrapH(assetServer))
}

func lanServiceStatus(config runtimeConfig) gin.H {
	status := gin.H{"enabled": config.LANEnabled, "scope": lanServiceScope, "management_exposed": false}
	if config.LANEnabled {
		status["listen"] = net.JoinHostPort(config.LANHost, strconv.Itoa(config.LANPort))
	}
	return status
}

func registerLANLandingRoutes(engine *gin.Engine) {
	engine.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true, "mode": lanServiceScope, "engine": "localrouter-native", "oauth": false})
	})
	engine.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"name": "LocalRouter", "scope": lanServiceScope, "console": "loopback-only",
			"discovery": "/.well-known/localrouter.json", "authentication": "service-token-required",
		})
	})
}

func registerLocalAdminRoutes(engine *gin.Engine, runtime localRuntime) {
	admin := engine.Group("/local/api")
	admin.Use(localAdminAuth(runtime.adminAuth, runtime.adminToken, runtime.rootUser))
	{
		admin.GET("/summary", localSummary(runtime))
		admin.GET("/analytics", localAnalyticsHandler(runtime))
		admin.GET("/agent-usage", localAgentUsageHandler(runtime))
		admin.PUT("/admin-auth", handleAdminAuthChange(runtime))
		admin.PUT("/admin-token", handleAdminTokenChange(runtime))
		admin.GET("/providers", listProviders(runtime))
		admin.GET("/models", handleAdminModels(runtime))

		admin.GET("/channels", handleListChannels(runtime))
		admin.GET("/channels/:id", handleGetChannel(runtime))
		admin.POST("/channels", handleAddChannel(runtime))
		admin.PUT("/channels", handleUpdateChannel(runtime))
		admin.DELETE("/channels/:id", handleDeleteChannel(runtime))
		admin.GET("/channels/:id/test", handleTestChannel(runtime))
		admin.GET("/channels/:id/fetch-models", handleFetchChannelModels(runtime))
		admin.POST("/channels/fix", handleFixChannels(runtime))

		admin.GET("/tokens", handleListTokens(runtime))
		admin.GET("/tokens/:id", handleGetToken(runtime))
		admin.POST("/tokens", handleAddToken(runtime))
		admin.PUT("/tokens", handleUpdateToken(runtime))
		admin.DELETE("/tokens/:id", handleDeleteToken(runtime))
		admin.POST("/tokens/:id/reveal", handleRevealToken(runtime))
		admin.PUT("/tokens/:id/key", handleUpdateTokenKey(runtime))
		admin.GET("/token-policies", runtime.policies.handleList)
		admin.GET("/token-policies/:id", runtime.policies.handleGet)
		admin.PUT("/token-policies/:id", runtime.policies.handlePut)
		admin.DELETE("/token-policies/:id", runtime.policies.handleDelete)
		admin.GET("/maintenance-access", runtime.policies.handleMaintenanceAccessGet)
		admin.PUT("/maintenance-access", runtime.policies.handleMaintenanceAccessPut)

		admin.GET("/logs", handleListLogs(runtime))
		admin.GET("/protocol-events", runtime.events.handleList)
		admin.GET("/workflows/jobs", runtime.protocols.handleAdminWorkflowJobs)
		admin.GET("/protocols", runtime.protocols.handleAdminList)
		admin.POST("/protocols/validate", runtime.protocols.handleAdminValidate)
		admin.POST("/protocols/plan", runtime.protocols.handleAdminPlan)
		admin.POST("/protocols/apply", runtime.protocols.handleAdminApply)
		admin.GET("/protocols/history", runtime.protocols.handleAdminHistory)
		admin.POST("/protocols/rollback", runtime.protocols.handleAdminRollback)
		admin.POST("/protocols/reload", runtime.protocols.handleAdminReload)
		admin.PUT("/protocols/:id/activation", runtime.protocols.handleAdminActivation)
		admin.PUT("/protocols/:id/operations/:operation/activation", runtime.protocols.handleAdminActivation)
		admin.POST("/protocols/:id/pool/reset", runtime.protocols.handleAdminPoolReset)
		admin.POST("/protocols/:id/readiness/refresh", runtime.protocols.handleAdminReadinessRefresh(runtime))
		admin.GET("/protocol-drafts", runtime.protocols.handleDraftList)
		admin.POST("/protocol-drafts", runtime.protocols.handleDraftCreate)
		admin.GET("/protocol-drafts/:id", runtime.protocols.handleDraftGet)
		admin.DELETE("/protocol-drafts/:id", runtime.protocols.handleDraftDelete)
		admin.POST("/protocol-drafts/:id/validate", runtime.protocols.handleDraftValidate)
		admin.POST("/protocol-drafts/:id/plan", runtime.protocols.handleDraftPlan)
		admin.GET("/protocol-drafts/:id/files/*path", runtime.protocols.handleDraftFileGet)
		admin.PUT("/protocol-drafts/:id/files/*path", runtime.protocols.handleDraftFilePut)
		admin.DELETE("/protocol-drafts/:id/files/*path", runtime.protocols.handleDraftFileDelete)
	}
}

func localAdminAuth(setting *adminAuthStore, expected *adminTokenStore, root localUser) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !setting.isEnabled() {
			setLocalAdministrator(c, root)
			c.Next()
			return
		}
		provided := c.GetHeader(adminTokenHeader)
		if expected == nil || !expected.matches(provided) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "local administrator token required",
			})
			return
		}
		setLocalAdministrator(c, root)
		c.Next()
	}
}

func setLocalAdministrator(c *gin.Context, root localUser) {
	c.Set("id", root.ID)
	c.Set("username", root.Username)
	c.Set("role", root.Role)
	c.Set("group", "default")
	c.Set("user_group", "default")
}

func handleAdminAuthChange(runtime localRuntime) gin.HandlerFunc {
	type changeRequest struct {
		Enabled *bool  `json:"enabled"`
		Token   string `json:"token"`
	}
	return func(c *gin.Context) {
		var request changeRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "enabled is required"})
			return
		}
		if request.Enabled == nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "enabled is required"})
			return
		}
		enabled := *request.Enabled
		wasEnabled := runtime.adminAuth.isEnabled()
		if enabled && !wasEnabled {
			if err := validateAdminToken(request.Token); err != nil {
				c.JSON(http.StatusUnprocessableEntity, gin.H{"success": false, "message": err.Error()})
				return
			}
			if err := runtime.adminToken.replacePersistedWithoutCurrent(request.Token, runtime.config.AdminTokenFile); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "failed to persist admin token"})
				return
			}
		}
		if err := runtime.adminAuth.setEnabled(enabled); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "failed to persist administrator authentication setting"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"enabled": enabled, "changed": enabled != wasEnabled}})
	}
}

func handleAdminTokenChange(runtime localRuntime) gin.HandlerFunc {
	type changeRequest struct {
		Token string `json:"token"`
	}
	return func(c *gin.Context) {
		var request changeRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "token is required"})
			return
		}
		if err := validateAdminToken(request.Token); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"success": false, "message": err.Error()})
			return
		}
		var err error
		if runtime.adminAuth.isEnabled() {
			err = runtime.adminToken.replacePersisted(c.GetHeader(adminTokenHeader), request.Token, runtime.config.AdminTokenFile)
		} else {
			err = runtime.adminToken.replacePersistedWithoutCurrent(request.Token, runtime.config.AdminTokenFile)
		}
		if err != nil {
			if runtime.adminAuth.isEnabled() && !runtime.adminToken.matches(c.GetHeader(adminTokenHeader)) {
				c.JSON(http.StatusConflict, gin.H{"success": false, "message": "admin token changed; unlock again and retry"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "failed to persist admin token"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"changed": true}})
	}
}

func localSummary(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		channelCount, _ := runtime.store.count("channels", "")
		tokenCount, _ := runtime.store.count("tokens", "user_id = ? AND deleted_at IS NULL", runtime.rootUser.ID)
		protocolTotal, protocolReady := runtime.protocols.counts()
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"channels":           channelCount,
				"tokens":             tokenCount,
				"listen":             net.JoinHostPort(runtime.config.Host, strconv.Itoa(runtime.config.Port)),
				"admin_auth_enabled": runtime.adminAuth.isEnabled(),
				"admin_token_file":   runtime.config.AdminTokenFile,
				"api_token_file":     runtime.config.APITokenFile,
				"database_path":      runtime.config.DatabasePath,
				"config_dir":         runtime.config.ConfigDir,
				"state_dir":          runtime.config.StateDir,
				"cache_dir":          runtime.config.CacheDir,
				"protocol_dir":       runtime.config.ProtocolDir,
				"channel_profiles":   runtime.config.ChannelProfilesPath,
				"engine":             "localrouter-native",
				"protocols":          protocolTotal,
				"protocols_ready":    protocolReady,
				"billing":            "usage-accounting",
				"oauth":              "external-maintainer",
				"lan_service":        lanServiceStatus(runtime.config),
			},
		})
	}
}
