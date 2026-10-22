package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Model preferences are operator choices, not provider contracts: the default
// LLM, each service's preferred model and which full Packs also answer on /v1.
// They live beside channel-profiles.json so no Pack release is needed to change
// them, and every consumer (gateway, lr, programs) reads the same document.
type modelPreferenceDocument struct {
	SchemaVersion string                   `json:"schema_version"`
	DefaultModel  string                   `json:"default_model"`
	Services      []modelServicePreference `json:"services"`
}

type modelServicePreference struct {
	Pack           string            `json:"pack"`
	PreferredModel string            `json:"preferred_model,omitempty"`
	V1             bool              `json:"v1,omitempty"`
	V1Aliases      map[string]string `json:"v1_aliases,omitempty"`
}

type modelPreferenceStore struct {
	mu       sync.RWMutex
	path     string
	modified time.Time
	document modelPreferenceDocument
}

type packModelTarget struct {
	Pack  string
	Model string
	// Path is the /v1 path that serves the model; set only by v1PackModelsAll.
	Path string
}

var modelPreferencePackPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

func emptyModelPreferences() modelPreferenceDocument {
	return modelPreferenceDocument{SchemaVersion: "1", Services: []modelServicePreference{}}
}

func newModelPreferenceStore(path string) (*modelPreferenceStore, error) {
	store := &modelPreferenceStore{path: path, document: emptyModelPreferences()}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (store *modelPreferenceStore) load() error {
	info, err := os.Stat(store.path)
	if errors.Is(err, os.ErrNotExist) {
		store.document, store.modified = emptyModelPreferences(), time.Time{}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read model preferences: %w", err)
	}
	data, err := os.ReadFile(store.path)
	if err != nil {
		return fmt.Errorf("read model preferences: %w", err)
	}
	document, err := parseModelPreferences(data)
	if err != nil {
		return fmt.Errorf("model preferences %s: %w", store.path, err)
	}
	store.document, store.modified = document, info.ModTime()
	return nil
}

// refresh picks up a hand edit of models.json without a restart. An invalid
// edit keeps the last valid document in force.
func (store *modelPreferenceStore) refresh() {
	info, err := os.Stat(store.path)
	store.mu.RLock()
	current := store.modified
	store.mu.RUnlock()
	if (err == nil && info.ModTime().Equal(current)) || (errors.Is(err, os.ErrNotExist) && current.IsZero()) {
		return
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if loadErr := store.load(); loadErr != nil {
		log.Printf("keeping previous model preferences: %v", loadErr)
		if info != nil {
			store.modified = info.ModTime()
		}
	}
}

func parseModelPreferences(data []byte) (modelPreferenceDocument, error) {
	var document modelPreferenceDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return document, fmt.Errorf("invalid JSON: %w", err)
	}
	if document.SchemaVersion != "1" {
		return document, errors.New("schema_version must be \"1\"")
	}
	document.DefaultModel = strings.TrimSpace(document.DefaultModel)
	if len(document.DefaultModel) > 256 {
		return document, errors.New("default_model is too long")
	}
	if document.Services == nil {
		document.Services = []modelServicePreference{}
	}
	if len(document.Services) > 200 {
		return document, errors.New("at most 200 services may be listed")
	}
	seen := map[string]bool{}
	for index := range document.Services {
		service := &document.Services[index]
		service.Pack = strings.TrimSpace(service.Pack)
		service.PreferredModel = strings.TrimSpace(service.PreferredModel)
		if !modelPreferencePackPattern.MatchString(service.Pack) {
			return document, fmt.Errorf("services[%d].pack is not a Pack ID", index)
		}
		if seen[service.Pack] {
			return document, fmt.Errorf("services[%d].pack %q is listed twice", index, service.Pack)
		}
		seen[service.Pack] = true
		if len(service.PreferredModel) > 256 {
			return document, fmt.Errorf("services[%d].preferred_model is too long", index)
		}
		if len(service.V1Aliases) > 50 {
			return document, fmt.Errorf("services[%d].v1_aliases has more than 50 entries", index)
		}
		for alias, model := range service.V1Aliases {
			if strings.TrimSpace(alias) == "" || strings.TrimSpace(model) == "" || len(alias) > 256 || len(model) > 256 {
				return document, fmt.Errorf("services[%d].v1_aliases entries must map a non-empty name to a non-empty model", index)
			}
		}
		if !service.V1 && len(service.V1Aliases) > 0 {
			return document, fmt.Errorf("services[%d].v1_aliases requires v1=true", index)
		}
	}
	return document, nil
}

func (store *modelPreferenceStore) snapshot() modelPreferenceDocument {
	if store == nil {
		return emptyModelPreferences()
	}
	store.refresh()
	store.mu.RLock()
	defer store.mu.RUnlock()
	document := store.document
	document.Services = make([]modelServicePreference, len(store.document.Services))
	for index, service := range store.document.Services {
		copied := service
		if service.V1Aliases != nil {
			copied.V1Aliases = make(map[string]string, len(service.V1Aliases))
			for alias, model := range service.V1Aliases {
				copied.V1Aliases[alias] = model
			}
		}
		document.Services[index] = copied
	}
	return document
}

func (store *modelPreferenceStore) replace(document modelPreferenceDocument) error {
	if store == nil {
		return errors.New("model preferences are unavailable")
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := writeSecretAtomic(store.path, string(encoded)); err != nil {
		return fmt.Errorf("persist model preferences: %w", err)
	}
	store.document = document
	if info, err := os.Stat(store.path); err == nil {
		store.modified = info.ModTime()
	}
	return nil
}

func (store *modelPreferenceStore) defaultModel() string {
	return store.snapshot().DefaultModel
}

func (store *modelPreferenceStore) preferredModel(pack string) string {
	for _, service := range store.snapshot().Services {
		if service.Pack == pack {
			return service.PreferredModel
		}
	}
	return ""
}

func (store *modelPreferenceStore) digest() string {
	encoded, _ := json.Marshal(store.snapshot())
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// v1PackModels lists every public /v1 model name answered by a ready Pack, in
// the operator's service order. A name is either a model the Pack's request
// schema declares or an operator alias for one of the Pack's models.
func (runtime localRuntime) v1PackModels(requestPath string) []packModelTarget {
	if runtime.models == nil || runtime.protocols == nil {
		return nil
	}
	routePath := strings.TrimPrefix(requestPath, "/v1")
	result := []packModelTarget{}
	for _, service := range runtime.models.snapshot().Services {
		if !service.V1 {
			continue
		}
		route, ok := runtime.v1PackRoute(service.Pack, routePath)
		if !ok {
			continue
		}
		for _, model := range protocolSchemaStringEnum(route.RequestSchema, "model") {
			result = append(result, packModelTarget{Pack: service.Pack, Model: model})
		}
		for alias := range service.V1Aliases {
			result = append(result, packModelTarget{Pack: service.Pack, Model: alias})
		}
	}
	return result
}

// v1PackModelsAll lists every Pack model on /v1 with the path that serves it,
// for example chat models on /v1/chat/completions and embedding models on
// /v1/embeddings. Aliases are listed on /v1/chat/completions.
func (runtime localRuntime) v1PackModelsAll() []packModelTarget {
	if runtime.models == nil || runtime.protocols == nil {
		return nil
	}
	result := []packModelTarget{}
	for _, service := range runtime.models.snapshot().Services {
		if !service.V1 {
			continue
		}
		definition, ok := runtime.protocols.get(service.Pack)
		if !ok {
			continue
		}
		seen := map[string]bool{}
		for _, route := range definition.Routes {
			if seen[route.Path] {
				continue
			}
			seen[route.Path] = true
			if _, ok := runtime.v1PackRoute(service.Pack, route.Path); !ok {
				continue
			}
			for _, model := range protocolSchemaStringEnum(route.RequestSchema, "model") {
				result = append(result, packModelTarget{Pack: service.Pack, Model: model, Path: "/v1" + route.Path})
			}
		}
		for alias := range service.V1Aliases {
			result = append(result, packModelTarget{Pack: service.Pack, Model: alias, Path: "/v1/chat/completions"})
		}
	}
	return result
}

// v1PackTargets resolves one public /v1 model name to the Packs that serve it,
// rewriting aliases to the Pack's own model ID.
func (runtime localRuntime) v1PackTargets(requestPath, model string) []packModelTarget {
	if runtime.models == nil || runtime.protocols == nil {
		return nil
	}
	routePath := strings.TrimPrefix(requestPath, "/v1")
	targets := []packModelTarget{}
	for _, service := range runtime.models.snapshot().Services {
		if !service.V1 {
			continue
		}
		route, ok := runtime.v1PackRoute(service.Pack, routePath)
		if !ok {
			continue
		}
		if upstream, aliased := service.V1Aliases[model]; aliased {
			targets = append(targets, packModelTarget{Pack: service.Pack, Model: upstream})
			continue
		}
		if containsString(protocolSchemaStringEnum(route.RequestSchema, "model"), model) {
			targets = append(targets, packModelTarget{Pack: service.Pack, Model: model})
		}
	}
	return targets
}

func (runtime localRuntime) v1PackRoute(pack, routePath string) (protocolRoute, bool) {
	definition, ok := runtime.protocols.get(pack)
	if !ok || !runtime.protocols.serviceEnabled(definition) {
		return protocolRoute{}, false
	}
	if ready, _ := runtime.protocols.readiness(definition); !ready {
		return protocolRoute{}, false
	}
	for _, route := range definition.Routes {
		if route.Path == routePath && routeSupportsMethod(route, http.MethodPost) && runtime.protocols.operationEnabled(pack, route.OperationID) {
			return route, true
		}
	}
	return protocolRoute{}, false
}

type packDispatchSignalKey struct{}

// markPackDispatch tells an enclosing /v1 Pack relay that this attempt reached
// the upstream, so a later failure must not be replayed on another Pack.
func markPackDispatch(c *gin.Context) {
	if signal, ok := c.Request.Context().Value(packDispatchSignalKey{}).(*atomic.Bool); ok {
		signal.Store(true)
	}
}

// v1PackRelay runs before /v1 authentication. A request whose model no
// Channel serves, but an operator-listed Pack does, is replayed through the
// full /p pipeline (authentication, policy, budget, trace) for each candidate
// Pack in order. Missing models fall back to the default LLM.
func v1PackRelay(engine *gin.Engine, runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodPost || runtime.models == nil {
			c.Next()
			return
		}
		body, err := compatibilityBody(c)
		if err != nil {
			c.Next()
			return
		}
		model := relayModel(c, body)
		if model == "" && gjson.ValidBytes(body) && gjson.ParseBytes(body).IsObject() {
			if fallback := runtime.models.defaultModel(); fallback != "" {
				if updated, setErr := sjson.SetBytes(body, "model", fallback); setErr == nil {
					body, model = updated, fallback
					c.Set("localrouter_compatibility_body", body)
					c.Header("X-LocalRouter-Default-Model", fallback)
				}
			}
		}
		if model == "" {
			c.Next()
			return
		}
		if profile, ok := runtime.channelProfiles.matchRequestPath(c.Request.URL.Path); ok {
			if channels, err := matchingChannels(runtime.store, model, profile.ID); err == nil && len(channels) > 0 {
				c.Next()
				return
			}
		}
		targets := runtime.v1PackTargets(c.Request.URL.Path, model)
		if len(targets) == 0 {
			c.Next()
			return
		}
		c.Abort()
		routePath := strings.TrimPrefix(c.Request.URL.Path, "/v1")
		// Every Pack attempt joins one trace, so /agent/traces shows the whole
		// request including attempts that failed over.
		sharedTrace := ""
		if c.GetHeader("X-LocalRouter-Trace-Id") == "" && c.GetHeader("traceparent") == "" {
			sharedTrace = serviceID("")
		}
		for index, target := range targets {
			attemptBody, setErr := sjson.SetBytes(body, "model", target.Model)
			if setErr != nil {
				attemptBody = body
			}
			dispatched := &atomic.Bool{}
			request := c.Request.Clone(context.WithValue(c.Request.Context(), packDispatchSignalKey{}, dispatched))
			request.URL.Path, request.URL.RawPath, request.RequestURI = "/p/"+target.Pack+routePath, "", ""
			request.Body, request.ContentLength = io.NopCloser(bytes.NewReader(attemptBody)), int64(len(attemptBody))
			if sharedTrace != "" {
				request.Header.Set("X-LocalRouter-Trace-Id", sharedTrace)
			}
			writer := &packFailoverWriter{destination: c.Writer, header: make(http.Header), canRetry: index+1 < len(targets), dispatched: dispatched}
			writer.header.Set("X-LocalRouter-Pack", target.Pack)
			engine.ServeHTTP(writer, request)
			if !writer.swallowed {
				return
			}
		}
	}
}

// packFailoverWriter holds back one candidate's error so the next Pack can be
// tried, but only while that error cannot mean the upstream accepted work:
// nothing was dispatched, or the provider refused with 429.
type packFailoverWriter struct {
	destination gin.ResponseWriter
	header      http.Header
	canRetry    bool
	dispatched  *atomic.Bool
	committed   bool
	swallowed   bool
}

func (writer *packFailoverWriter) Header() http.Header { return writer.header }

func (writer *packFailoverWriter) WriteHeader(status int) {
	if writer.committed || writer.swallowed {
		return
	}
	if writer.canRetry && status >= 400 && (!writer.dispatched.Load() || status == http.StatusTooManyRequests) {
		writer.swallowed = true
		return
	}
	for name, values := range writer.header {
		writer.destination.Header()[name] = values
	}
	writer.committed = true
	writer.destination.WriteHeader(status)
}

func (writer *packFailoverWriter) Write(data []byte) (int, error) {
	if !writer.committed && !writer.swallowed {
		writer.WriteHeader(http.StatusOK)
	}
	if writer.swallowed {
		return len(data), nil
	}
	return writer.destination.Write(data)
}

func (writer *packFailoverWriter) Flush() {
	if writer.committed {
		writer.destination.Flush()
	}
}

func (writer *packFailoverWriter) CloseNotify() <-chan bool {
	return writer.destination.CloseNotify()
}

func (writer *packFailoverWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("bidirectional transports require their direct Pack endpoint")
}

// discoveryModels is the one published statement of how to pick a model: the
// default LLM, the /v1 names answered by Packs, and where to search.
func discoveryModels(runtime localRuntime) gin.H {
	v1 := []gin.H{}
	for _, target := range runtime.v1PackModelsAll() {
		v1 = append(v1, gin.H{"id": target.Model, "pack": target.Pack, "path": target.Path})
	}
	return gin.H{
		"default":        runtime.models.defaultModel(),
		"default_rule":   "a /v1 request without model uses the default; every service may publish preferred_model",
		"v1_pack_models": v1,
		"search":         "/agent/models?q=<text>&exact=<bool>&limit=<n>",
		"search_cli":     "lr find model [--exact <pack>:<model-id>] [text]",
		"preferences":    "$XDG_CONFIG_HOME/localrouter/models.json",
	}
}

func registerModelPreferenceAdminRoutes(admin *gin.RouterGroup, runtime localRuntime) {
	admin.GET("/model-preferences", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": runtime.models.snapshot(), "path": "$XDG_CONFIG_HOME/localrouter/models.json"})
	})
	admin.PUT("/model-preferences", func(c *gin.Context) {
		raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "cannot read model preferences"})
			return
		}
		document, err := parseModelPreferences(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
			return
		}
		if err := runtime.models.replace(document); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": runtime.models.snapshot()})
	})
}
