package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	tokenPolicyContextID          = "localrouter_token_id"
	maxTokenPolicyBody            = 128 << 10
	localRouterMaintainCapability = "localrouter.maintain"
)

type localTokenPolicy struct {
	TokenID           int      `json:"token_id"`
	Surfaces          []string `json:"surfaces,omitempty"`
	Packs             []string `json:"packs,omitempty"`
	Operations        []string `json:"operations,omitempty"`
	Models            []string `json:"models,omitempty"`
	Capabilities      []string `json:"capabilities,omitempty"`
	RequestsPerMinute int      `json:"requests_per_minute,omitempty"`
	DailyRequestLimit int      `json:"daily_request_limit,omitempty"`
	MaxInFlight       int      `json:"max_in_flight,omitempty"`
	ExpiresAt         int64    `json:"expires_at,omitempty"`
}

type tokenPolicyDocument struct {
	SchemaVersion           string                   `json:"schema_version"`
	AgentMaintenanceEnabled bool                     `json:"agent_maintenance_enabled,omitempty"`
	Policies                map[int]localTokenPolicy `json:"policies"`
}

type tokenPolicyUsage struct {
	MinuteStart time.Time
	MinuteCount int
	DayStart    time.Time
	DayCount    int
	InFlight    int `json:"-"`
}

type tokenPolicyStore struct {
	workspace               *serviceWorkspace
	path                    string
	mu                      sync.Mutex
	agentMaintenanceEnabled bool
	protectedServiceTokenID int
	policies                map[int]localTokenPolicy
	usage                   map[int]*tokenPolicyUsage
}

func newTokenPolicyStore(dataDir string) (*tokenPolicyStore, error) {
	store := &tokenPolicyStore{
		path:     filepath.Join(dataDir, "token-policies.json"),
		policies: make(map[int]localTokenPolicy), usage: make(map[int]*tokenPolicyUsage),
	}
	exists, err := inspectPrivateRegularFile(store.path, "token policy file")
	if err != nil {
		return nil, err
	}
	if !exists {
		return store, nil
	}
	data, err := os.ReadFile(store.path)
	if err != nil {
		return nil, err
	}
	var document tokenPolicyDocument
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil || document.SchemaVersion != "1" {
		return nil, errors.New("token policy file is invalid")
	}
	if document.Policies != nil {
		store.policies = document.Policies
	}
	store.agentMaintenanceEnabled = document.AgentMaintenanceEnabled
	usagePath := store.usagePath()
	exists, err = inspectPrivateRegularFile(usagePath, "token usage file")
	if err != nil {
		return nil, err
	}
	if exists {
		data, err := os.ReadFile(usagePath)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &store.usage); err != nil || store.usage == nil {
			return nil, errors.New("token usage file is invalid")
		}
	}
	return store, nil
}

func normalizePolicyValues(values []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func validateLocalTokenPolicy(policy *localTokenPolicy) error {
	policy.Surfaces = normalizePolicyValues(policy.Surfaces)
	policy.Packs = normalizePolicyValues(policy.Packs)
	policy.Operations = normalizePolicyValues(policy.Operations)
	policy.Models = normalizePolicyValues(policy.Models)
	policy.Capabilities = normalizePolicyValues(policy.Capabilities)
	for _, surface := range policy.Surfaces {
		if surface != "v1" && surface != "v1beta" && surface != "p" && surface != "w" && surface != "mcp" {
			return errors.New("surface must be v1, v1beta, p, w, or mcp")
		}
	}
	for _, capability := range policy.Capabilities {
		if capability != localRouterMaintainCapability {
			return errors.New("capability must be localrouter.maintain")
		}
	}
	if policy.RequestsPerMinute < 0 || policy.RequestsPerMinute > 100000 {
		return errors.New("requests_per_minute is out of range")
	}
	if policy.DailyRequestLimit < 0 || policy.DailyRequestLimit > 10000000 {
		return errors.New("daily_request_limit is out of range")
	}
	if policy.MaxInFlight < 0 || policy.MaxInFlight > 10000 {
		return errors.New("max_in_flight is out of range")
	}
	return nil
}

// hasCapability checks the per-token half of Agent maintenance authorization.
// The route middleware separately enforces the global switch; an absent policy
// never grants maintenance.
func (store *tokenPolicyStore) hasCapability(tokenID int, capability string) bool {
	if store == nil || tokenID <= 0 || capability == "" {
		return false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	policy, exists := store.policies[tokenID]
	if !exists || (policy.ExpiresAt > 0 && time.Now().UTC().Unix() >= policy.ExpiresAt) {
		return false
	}
	for _, candidate := range policy.Capabilities {
		if candidate == capability {
			return true
		}
	}
	return false
}

func policyHasCapability(policy localTokenPolicy, capability string) bool {
	for _, candidate := range policy.Capabilities {
		if candidate == capability {
			return true
		}
	}
	return false
}

func (store *tokenPolicyStore) isAgentMaintenanceEnabled() bool {
	if store == nil {
		return false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.agentMaintenanceEnabled
}

func (store *tokenPolicyStore) policyFor(tokenID int) (localTokenPolicy, bool) {
	if store == nil || tokenID <= 0 {
		return localTokenPolicy{}, false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	policy, exists := store.policies[tokenID]
	if !exists {
		return localTokenPolicy{}, false
	}
	policy.Surfaces = append([]string(nil), policy.Surfaces...)
	policy.Packs = append([]string(nil), policy.Packs...)
	policy.Operations = append([]string(nil), policy.Operations...)
	policy.Models = append([]string(nil), policy.Models...)
	policy.Capabilities = append([]string(nil), policy.Capabilities...)
	return policy, true
}

// preview evaluates the current Token policy without consuming request or
// concurrency counters. Agent resolution and preflight use it to avoid
// recommending an operation the supplied service Token cannot call.
func (store *tokenPolicyStore) preview(tokenID int, surface, pack, operation, modelName string) (bool, int, string) {
	if store == nil || tokenID <= 0 {
		return true, 0, ""
	}
	if !store.workspace.bundleAllows(tokenID, surface, pack, operation) {
		return false, http.StatusForbidden, "capability bundle does not grant this service operation"
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	policy, exists := store.policies[tokenID]
	if !exists {
		return true, 0, ""
	}
	if policyHasCapability(policy, localRouterMaintainCapability) {
		return false, http.StatusForbidden, "maintenance token cannot call service surfaces"
	}
	now := time.Now().UTC()
	if policy.ExpiresAt > 0 && now.Unix() >= policy.ExpiresAt {
		return false, http.StatusForbidden, "token policy has expired"
	}
	if !containsPolicyValue(policy.Surfaces, surface) {
		return false, http.StatusForbidden, "token policy does not allow this surface"
	}
	if pack != "" && !containsPolicyValue(policy.Packs, pack) {
		return false, http.StatusForbidden, "token policy does not allow this Pack"
	}
	if operation != "" && !containsPolicyValue(policy.Operations, pack+"."+operation) && !containsPolicyValue(policy.Operations, operation) {
		return false, http.StatusForbidden, "token policy does not allow this operation"
	}
	if modelName != "" && !containsPolicyValue(policy.Models, modelName) {
		return false, http.StatusForbidden, "token policy does not allow this model"
	}
	usage := store.usage[tokenID]
	if usage == nil {
		return true, 0, ""
	}
	minuteCount := usage.MinuteCount
	if !usage.MinuteStart.Equal(now.Truncate(time.Minute)) {
		minuteCount = 0
	}
	dayCount := usage.DayCount
	if !usage.DayStart.Equal(now.Truncate(24 * time.Hour)) {
		dayCount = 0
	}
	if policy.RequestsPerMinute > 0 && minuteCount >= policy.RequestsPerMinute {
		return false, http.StatusTooManyRequests, "token minute request limit exceeded"
	}
	if policy.DailyRequestLimit > 0 && dayCount >= policy.DailyRequestLimit {
		return false, http.StatusTooManyRequests, "token daily request limit exceeded"
	}
	if policy.MaxInFlight > 0 && usage.InFlight >= policy.MaxInFlight {
		return false, http.StatusTooManyRequests, "token concurrency limit exceeded"
	}
	return true, 0, ""
}

func (store *tokenPolicyStore) maintenanceAccessViewLocked() gin.H {
	return gin.H{
		"agent_tokens_enabled": store.agentMaintenanceEnabled,
		"default_auth":         "admin",
		"admin_header":         adminTokenHeader,
		"agent_auth":           "bearer",
		"agent_capability":     localRouterMaintainCapability,
		"service_tokens":       "call-only",
		"maintenance_tokens":   "maintenance-only",
	}
}

func (store *tokenPolicyStore) handleMaintenanceAccessGet(c *gin.Context) {
	store.mu.Lock()
	view := store.maintenanceAccessViewLocked()
	store.mu.Unlock()
	c.JSON(http.StatusOK, gin.H{"success": true, "data": view})
}

func (store *tokenPolicyStore) handleMaintenanceAccessPut(c *gin.Context) {
	var request struct {
		AgentTokensEnabled bool `json:"agent_tokens_enabled"`
	}
	decoder := json.NewDecoder(io.LimitReader(c.Request.Body, maxTokenPolicyBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid maintenance access setting"})
		return
	}
	store.mu.Lock()
	previous := store.agentMaintenanceEnabled
	store.agentMaintenanceEnabled = request.AgentTokensEnabled
	err := store.saveLocked()
	if err != nil {
		store.agentMaintenanceEnabled = previous
	}
	view := store.maintenanceAccessViewLocked()
	store.mu.Unlock()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot persist maintenance access setting"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": view})
}

// Counters are separate from the version-1 policy contract so older binaries
// can still load policy settings during rollback. Active leases never persist.
func (store *tokenPolicyStore) usagePath() string {
	return filepath.Join(filepath.Dir(store.path), "token-policy-usage.json")
}

func (store *tokenPolicyStore) saveUsageLocked() error {
	data, err := json.Marshal(store.usage)
	if err != nil {
		return err
	}
	return store.writeAtomicLocked(store.usagePath(), data)
}

func (store *tokenPolicyStore) saveLocked() error {
	document := tokenPolicyDocument{
		SchemaVersion:           "1",
		AgentMaintenanceEnabled: store.agentMaintenanceEnabled,
		Policies:                store.policies,
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	return store.writeAtomicLocked(store.path, data)
}

func (store *tokenPolicyStore) writeAtomicLocked(path string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".token-policies-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(credentialMode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func containsPolicyValue(values []string, value string) bool {
	if len(values) == 0 {
		return true
	}
	for _, candidate := range values {
		if candidate == "*" || candidate == value {
			return true
		}
	}
	return false
}

func (store *tokenPolicyStore) begin(tokenID int, surface, pack, operation, modelName string) (func(), int, string) {
	if store != nil && !store.workspace.bundleAllows(tokenID, surface, pack, operation) {
		return nil, http.StatusForbidden, "capability bundle does not grant this service operation"
	}
	if store == nil || tokenID <= 0 {
		return func() {}, 0, ""
	}
	store.mu.Lock()
	policy, exists := store.policies[tokenID]
	if !exists {
		store.mu.Unlock()
		return func() {}, 0, ""
	}
	if policyHasCapability(policy, localRouterMaintainCapability) {
		store.mu.Unlock()
		return nil, http.StatusForbidden, "maintenance token cannot call service surfaces"
	}
	now := time.Now().UTC()
	if policy.ExpiresAt > 0 && now.Unix() >= policy.ExpiresAt {
		store.mu.Unlock()
		return nil, http.StatusForbidden, "token policy has expired"
	}
	if !containsPolicyValue(policy.Surfaces, surface) {
		store.mu.Unlock()
		return nil, http.StatusForbidden, "token policy does not allow this surface"
	}
	if pack != "" && !containsPolicyValue(policy.Packs, pack) {
		store.mu.Unlock()
		return nil, http.StatusForbidden, "token policy does not allow this Pack"
	}
	if operation != "" && !containsPolicyValue(policy.Operations, pack+"."+operation) && !containsPolicyValue(policy.Operations, operation) {
		store.mu.Unlock()
		return nil, http.StatusForbidden, "token policy does not allow this operation"
	}
	if modelName != "" && !containsPolicyValue(policy.Models, modelName) {
		store.mu.Unlock()
		return nil, http.StatusForbidden, "token policy does not allow this model"
	}
	usage := store.usage[tokenID]
	if usage == nil {
		usage = &tokenPolicyUsage{}
		store.usage[tokenID] = usage
	}
	minute := now.Truncate(time.Minute)
	if !usage.MinuteStart.Equal(minute) {
		usage.MinuteStart, usage.MinuteCount = minute, 0
	}
	day := now.Truncate(24 * time.Hour)
	if !usage.DayStart.Equal(day) {
		usage.DayStart, usage.DayCount = day, 0
	}
	if policy.RequestsPerMinute > 0 && usage.MinuteCount >= policy.RequestsPerMinute {
		store.mu.Unlock()
		return nil, http.StatusTooManyRequests, "token minute request limit exceeded"
	}
	if policy.DailyRequestLimit > 0 && usage.DayCount >= policy.DailyRequestLimit {
		store.mu.Unlock()
		return nil, http.StatusTooManyRequests, "token daily request limit exceeded"
	}
	if policy.MaxInFlight > 0 && usage.InFlight >= policy.MaxInFlight {
		store.mu.Unlock()
		return nil, http.StatusTooManyRequests, "token concurrency limit exceeded"
	}
	usage.MinuteCount++
	usage.DayCount++
	usage.InFlight++
	if policy.DailyRequestLimit > 0 || policy.RequestsPerMinute > 0 {
		if err := store.saveUsageLocked(); err != nil {
			usage.MinuteCount--
			usage.DayCount--
			usage.InFlight--
			store.mu.Unlock()
			return nil, http.StatusServiceUnavailable, "cannot persist token usage; request was not admitted"
		}
	}
	store.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			store.mu.Lock()
			if current := store.usage[tokenID]; current != nil && current.InFlight > 0 {
				current.InFlight--
			}
			store.mu.Unlock()
		})
	}, 0, ""
}

func abortTokenPolicy(c *gin.Context, status int, message string) {
	c.Abort()
	retryable := status == http.StatusTooManyRequests
	writeAgentError(c, status, "token_policy_denied", message, message, retryable, "localrouter", "wait for the local limit to reset or ask the operator to adjust this service Token policy", nil, nil, nil)
}

func (store *tokenPolicyStore) authorizeRequest(c *gin.Context, surface, pack, operation, modelName string) (func(), bool) {
	release, status, message := store.begin(c.GetInt(tokenPolicyContextID), surface, pack, operation, modelName)
	if status != 0 {
		abortTokenPolicy(c, status, message)
		return nil, false
	}
	return release, true
}

func (store *tokenPolicyStore) middleware(surface string) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenID := c.GetInt("token_id")
		c.Set(tokenPolicyContextID, tokenID)
		body, err := compatibilityBody(c)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": gin.H{"message": "request body exceeds 64 MiB or cannot be read"}})
			return
		}
		modelName := relayModel(c, body)
		if c.Request.Method == http.MethodPost && modelName == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "model is required"}})
			return
		}
		release, status, message := store.begin(tokenID, surface, "", "", modelName)
		if status != 0 {
			abortTokenPolicy(c, status, message)
			return
		}
		defer release()
		c.Next()
	}
}

func (store *tokenPolicyStore) handleList(c *gin.Context) {
	store.mu.Lock()
	items := make([]localTokenPolicy, 0, len(store.policies))
	for _, policy := range store.policies {
		items = append(items, policy)
	}
	store.mu.Unlock()
	sort.Slice(items, func(i, j int) bool { return items[i].TokenID < items[j].TokenID })
	c.JSON(http.StatusOK, gin.H{"success": true, "data": items})
}

func (store *tokenPolicyStore) handleGet(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "valid token id required"})
		return
	}
	store.mu.Lock()
	policy, ok := store.policies[id]
	store.mu.Unlock()
	if !ok {
		policy = localTokenPolicy{TokenID: id}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": policy})
}

func (store *tokenPolicyStore) handlePut(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "valid token id required"})
		return
	}
	var policy localTokenPolicy
	decoder := json.NewDecoder(io.LimitReader(c.Request.Body, maxTokenPolicyBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid token policy"})
		return
	}
	policy.TokenID = id
	if err := validateLocalTokenPolicy(&policy); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	if id == store.protectedServiceTokenID && policyHasCapability(policy, localRouterMaintainCapability) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "the bootstrap service token cannot become a maintenance token; issue a separate token"})
		return
	}
	store.mu.Lock()
	store.policies[id] = policy
	err = store.saveLocked()
	store.mu.Unlock()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot persist token policy"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": policy})
}

func (store *tokenPolicyStore) handleDelete(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "valid token id required"})
		return
	}
	store.mu.Lock()
	delete(store.policies, id)
	delete(store.usage, id)
	err = store.saveLocked()
	store.mu.Unlock()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot persist token policy"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
