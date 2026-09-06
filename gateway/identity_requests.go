package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Enrollment proves possession of a private claim, not authority. Only the
// loopback human decision endpoint activates a service identity.
type identityRequest struct {
	ID               string           `json:"id"`
	AgentCode        string           `json:"agent_code"`
	AgentName        string           `json:"agent_name"`
	Workspace        string           `json:"workspace"`
	Runtime          string           `json:"runtime"`
	Policy           localTokenPolicy `json:"policy"`
	Digest           string           `json:"digest"`
	State            string           `json:"state"`
	TokenID          int              `json:"token_id"`
	CreatedAt        int64            `json:"created_at"`
	ExpiresAt        int64            `json:"expires_at"`
	OwnerTokenID     int              `json:"owner_token_id,omitempty"`
	BasePolicyDigest string           `json:"base_policy_digest,omitempty"`
}

func identityHash(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}

func identityClaim(c *gin.Context) (string, bool) {
	claim := bearerToken(c.GetHeader("Authorization"))
	decoded, err := hex.DecodeString(claim)
	if err != nil || len(decoded) != 32 {
		writeAgentError(c, 401, "identity_claim_required", "private enrollment claim required", "use the claim saved by lr identity request", false, "agent", "resume lr identity request; never supply an administrator credential", nil, nil, nil)
		return "", false
	}
	c.Header("Cache-Control", "no-store")
	return identityHash(claim), true
}

func registerIdentityRequestRoutes(engine *gin.Engine, runtime localRuntime) {
	// Deliberately absent from buildLANServer: enrollment is a loopback handshake.
	engine.POST("/agent/identity-requests", handleIdentityRequest(runtime))
	engine.POST("/agent/identity-requests/:id/claim", handleIdentityClaim(runtime))
	engine.DELETE("/agent/identity-requests/:id", handleIdentityWithdraw(runtime))
}

func handleIdentityWithdraw(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		hash, ok := identityClaim(c)
		if !ok {
			return
		}
		runtime.store.identityMu.Lock()
		defer runtime.store.identityMu.Unlock()
		request, expected, err := loadIdentityRequest(runtime.store, c.Param("id"))
		if err != nil || subtle.ConstantTimeCompare([]byte(hash), []byte(expected)) != 1 {
			localFailure(c, 404, "identity request not found")
			return
		}
		if request.State == "approved" {
			localFailure(c, 409, "identity already approved; revoke the issued identity instead")
			return
		}
		if _, err = runtime.store.db.Exec(`UPDATE identity_requests SET state='withdrawn' WHERE id=?`, request.ID); err != nil {
			localFailure(c, 500, "cannot withdraw request")
			return
		}
		localSuccess(c, gin.H{"withdrawn": true, "authority_changed": false})
	}
}

func registerIdentityAdminRoutes(admin *gin.RouterGroup, runtime localRuntime) {
	human := admin.Group("/identity-requests")
	human.Use(humanAllowanceAccess)
	human.GET("", handleIdentityList(runtime))
	human.POST("/:id/decision", handleIdentityDecision(runtime))
}

func identityPolicyDigest(store *tokenPolicyStore, id int) string {
	policy, exists := store.policyFor(id)
	data, _ := json.Marshal(struct {
		Exists bool
		Policy localTokenPolicy
	}{exists, policy})
	return identityHash(string(data))
}

// Existing Agents may request a replacement scope, but never apply it. The
// current policy remains in force until the human reviews the complete scope.
func handleIdentityAccessRequest(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		value, _ := c.Get("local_token")
		token, ok := value.(*localToken)
		if !ok || token.AgentCode == "localrouter-system" || token.Name == localTokenName {
			localFailure(c, 403, "independent service identity required")
			return
		}
		if c.Request.Method == "GET" {
			request, _, err := loadIdentityRequest(runtime.store, c.Param("id"))
			if err != nil || request.OwnerTokenID != token.ID {
				localFailure(c, 404, "owned access request not found")
				return
			}
			localSuccess(c, request)
			return
		}
		var input struct {
			Policy localTokenPolicy `json:"policy"`
		}
		if err := decodeAgentRequest(c, &input); err != nil {
			localFailure(c, 400, "invalid access request")
			return
		}
		request := identityRequest{AgentCode: token.AgentCode, AgentName: token.AgentName, Workspace: token.Workspace, Runtime: token.Runtime, Policy: input.Policy, OwnerTokenID: token.ID, BasePolicyDigest: identityPolicyDigest(runtime.policies, token.ID)}
		if err := validateIdentityRequest(runtime, &request); err != nil {
			localFailure(c, 400, err.Error())
			return
		}
		data, _ := json.Marshal(request)
		request.Digest = identityHash(string(data))
		request.ID = "access-" + request.Digest[:24]
		runtime.store.identityMu.Lock()
		defer runtime.store.identityMu.Unlock()
		if previous, _, err := loadIdentityRequest(runtime.store, request.ID); err == nil {
			localSuccess(c, previous)
			return
		} else if err != sql.ErrNoRows {
			localFailure(c, 500, "cannot inspect access request")
			return
		}
		var count int
		if err := runtime.store.db.QueryRow(`SELECT count(*) FROM identity_requests WHERE created_at>?`, time.Now().Add(-time.Hour).Unix()).Scan(&count); err != nil {
			localFailure(c, 500, "cannot inspect request capacity")
			return
		}
		if count >= 100 {
			localFailure(c, 429, "too many requests; resume an existing request or wait")
			return
		}
		request.State, request.CreatedAt = "pending", time.Now().Unix()
		request.ExpiresAt = request.CreatedAt + 24*60*60
		data, _ = json.Marshal(request)
		if _, err := runtime.store.db.Exec(`INSERT INTO identity_requests (id,claim_hash,document,state,created_at,expires_at) VALUES (?,'',?,'pending',?,?)`, request.ID, string(data), request.CreatedAt, request.ExpiresAt); err != nil {
			localFailure(c, 500, "cannot save access request")
			return
		}
		localSuccess(c, request)
	}
}

func validateIdentityRequest(runtime localRuntime, request *identityRequest) error {
	token := localToken{AgentCode: request.AgentCode, AgentName: request.AgentName, Workspace: request.Workspace, Runtime: request.Runtime}
	if err := validateAgentRegistration(&token); err != nil {
		return err
	}
	request.AgentCode, request.AgentName, request.Workspace, request.Runtime = token.AgentCode, token.AgentName, token.Workspace, token.Runtime
	policy := &request.Policy
	if policy.TokenID != 0 || len(policy.Capabilities) != 0 {
		return errors.New("enrollment grants service access only")
	}
	if err := validateLocalTokenPolicy(policy); err != nil {
		return err
	}
	if len(policy.Packs) == 0 || len(policy.Packs) > 50 || len(policy.Operations) == 0 || len(policy.Operations) > 200 || len(policy.Models) > 200 {
		return errors.New("select explicit Packs and Pack-qualified operations")
	}
	// Freeze explicit operation membership. New Pack operations require a new decision.
	for _, pack := range policy.Packs {
		if pack == "*" || !protocolIDPattern.MatchString(pack) {
			return errors.New("select exact Pack IDs")
		}
	}
	for _, key := range policy.Operations {
		pack, op, found := strings.Cut(key, ".")
		if !found || !containsString(policy.Packs, pack) {
			return errors.New("each operation must belong to a selected Pack")
		}
		if _, ok := runtime.protocols.describeOperation(pack, op); !ok {
			return errors.New("select a published exact operation_key")
		}
	}
	if len(policy.Surfaces) == 0 {
		policy.Surfaces = []string{"p", "mcp"}
	}
	for _, surface := range policy.Surfaces {
		if surface != "p" && surface != "mcp" {
			return errors.New("enrollment supports Protocol Pack calls through p and mcp")
		}
	}
	if policy.ExpiresAt < 0 {
		policy.ExpiresAt = 0
	}
	if policy.ExpiresAt > 0 && policy.ExpiresAt <= time.Now().Unix() {
		return errors.New("authorization expiry must be in the future")
	}
	return nil
}

func loadIdentityRequest(store *localStore, id string) (identityRequest, string, error) {
	var request identityRequest
	var document, hash string
	err := store.db.QueryRow(`SELECT document, claim_hash, state, token_id FROM identity_requests WHERE id = ?`, id).Scan(&document, &hash, &request.State, &request.TokenID)
	if err != nil {
		return request, "", err
	}
	state, tokenID := request.State, request.TokenID
	err = json.Unmarshal([]byte(document), &request)
	request.State, request.TokenID = state, tokenID
	if request.State == "pending" && request.ExpiresAt <= time.Now().Unix() {
		request.State = "expired"
	}
	return request, hash, err
}

func handleIdentityRequest(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		hash, ok := identityClaim(c)
		if !ok {
			return
		}
		var input struct {
			AgentCode string           `json:"agent_code"`
			AgentName string           `json:"agent_name"`
			Workspace string           `json:"workspace"`
			Runtime   string           `json:"runtime"`
			Policy    localTokenPolicy `json:"policy"`
		}
		if err := decodeAgentRequest(c, &input); err != nil {
			localFailure(c, 400, "invalid identity request")
			return
		}
		request := identityRequest{AgentCode: input.AgentCode, AgentName: input.AgentName, Workspace: input.Workspace, Runtime: input.Runtime, Policy: input.Policy}
		if err := validateIdentityRequest(runtime, &request); err != nil {
			localFailure(c, 400, err.Error())
			return
		}
		canonical, _ := json.Marshal(request)
		request.Digest = identityHash(string(canonical))
		request.ID = "identity-" + hash[:24]
		runtime.store.identityMu.Lock()
		defer runtime.store.identityMu.Unlock()
		if previous, _, err := loadIdentityRequest(runtime.store, request.ID); err == nil {
			if previous.Digest != request.Digest {
				localFailure(c, 409, "this claim is already bound to a different request")
				return
			}
			localSuccess(c, previous)
			return
		} else if err != sql.ErrNoRows {
			localFailure(c, 500, "cannot read identity request")
			return
		}
		var pending int
		if err := runtime.store.db.QueryRow(`SELECT count(*) FROM identity_requests WHERE created_at > ?`, time.Now().Add(-time.Hour).Unix()).Scan(&pending); err != nil {
			localFailure(c, 500, "cannot inspect enrollment capacity")
			return
		}
		if pending >= 100 {
			localFailure(c, 429, "too many enrollment requests; resume an existing request or wait")
			return
		}
		request.State, request.CreatedAt = "pending", time.Now().Unix()
		request.ExpiresAt = request.CreatedAt + 24*60*60
		document, _ := json.Marshal(request)
		_, err := runtime.store.db.Exec(`INSERT INTO identity_requests (id,claim_hash,document,state,created_at,expires_at) VALUES (?,?,?,'pending',?,?)`, request.ID, hash, string(document), request.CreatedAt, request.ExpiresAt)
		if err != nil {
			localFailure(c, 500, "cannot save identity request")
			return
		}
		localSuccess(c, request)
	}
}

func handleIdentityClaim(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		hash, ok := identityClaim(c)
		if !ok {
			return
		}
		runtime.store.identityMu.Lock()
		defer runtime.store.identityMu.Unlock()
		request, expected, err := loadIdentityRequest(runtime.store, c.Param("id"))
		if err != nil || subtle.ConstantTimeCompare([]byte(hash), []byte(expected)) != 1 {
			localFailure(c, 404, "identity request not found")
			return
		}
		result := gin.H{"request": request, "ready": false, "registration_url": "/#tokens", "next_action": "review this exact request in Agent Workbench, then resume lr identity claim"}
		if request.State == "approved" {
			if request.ExpiresAt <= time.Now().Unix() {
				localFailure(c, 410, "credential delivery window expired; ask the operator to revoke the unclaimed identity")
				return
			}
			token, err := runtime.store.tokenByID(runtime.rootUser.ID, request.TokenID, true)
			if err != nil || token.Status != localStatusEnabled || (token.ExpiredTime >= 0 && token.ExpiredTime <= time.Now().Unix()) {
				localFailure(c, 410, "identity was revoked or expired")
				return
			}
			var issuedHash string
			if err := runtime.store.db.QueryRow(`SELECT issued_key_hash FROM identity_requests WHERE id=?`, request.ID).Scan(&issuedHash); err != nil || issuedHash != identityHash(token.Key) || token.AgentCode != request.AgentCode || token.Workspace != request.Workspace {
				localFailure(c, 410, "identity or credential changed; an old claim cannot retrieve a replacement")
				return
			}
			policy, exists := runtime.policies.policyFor(token.ID)
			if !exists || (policy.ExpiresAt > 0 && policy.ExpiresAt <= time.Now().Unix()) || policyHasCapability(policy, localRouterMaintainCapability) {
				localFailure(c, 409, "approved service policy is no longer available")
				return
			}
			// Re-delivery within the window returns the SAME credential. A lost
			// response never creates another identity or resets its usage.
			result["ready"], result["token"] = true, "sk-"+token.Key
			result["next_action"] = "save privately and bind; never print this credential"
		}
		localSuccess(c, result)
	}
}

func handleIdentityList(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		rows, err := runtime.store.db.Query(`SELECT id FROM identity_requests ORDER BY created_at DESC LIMIT 100`)
		if err != nil {
			localFailure(c, 500, "cannot list identity requests")
			return
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				break
			}
			ids = append(ids, id)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			localFailure(c, 500, "cannot read identity requests")
			return
		}
		requests := []identityRequest{}
		for _, id := range ids {
			request, _, err := loadIdentityRequest(runtime.store, id)
			if err != nil {
				localFailure(c, 500, "cannot read identity request")
				return
			}
			requests = append(requests, request)
		}
		localSuccess(c, requests)
	}
}

func handleIdentityDecision(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input struct {
			Digest  string `json:"digest"`
			Approve *bool  `json:"approve"`
		}
		if err := decodeAgentRequest(c, &input); err != nil || input.Approve == nil {
			localFailure(c, 400, "digest and explicit approve are required")
			return
		}
		runtime.store.identityMu.Lock()
		defer runtime.store.identityMu.Unlock()
		request, _, err := loadIdentityRequest(runtime.store, c.Param("id"))
		if err != nil {
			localFailure(c, 404, "identity request not found")
			return
		}
		if input.Digest != request.Digest {
			localFailure(c, 409, "request changed; review the current digest")
			return
		}
		if request.State != "pending" {
			localFailure(c, 409, "request is no longer pending")
			return
		}
		if !*input.Approve {
			_, err = runtime.store.db.Exec(`UPDATE identity_requests SET state='denied' WHERE id=? AND state='pending'`, request.ID)
			if err != nil {
				localFailure(c, 500, "cannot reject request")
				return
			}
			request.State = "denied"
			localSuccess(c, request)
			return
		}
		if err := validateIdentityRequest(runtime, &request); err != nil {
			localFailure(c, 409, err.Error())
			return
		}
		if request.OwnerTokenID > 0 {
			handleIdentityAccessDecision(c, runtime, request)
			return
		}
		secret, err := randomSecret(32, "")
		if err != nil {
			localFailure(c, 500, "cannot prepare identity")
			return
		}
		// Hold policy readers until both durable policy and token activation have
		// committed. An interrupted approval cannot expose an unrestricted token.
		runtime.policies.mu.Lock()
		defer runtime.policies.mu.Unlock()
		tx, err := runtime.store.db.Begin()
		if err != nil {
			localFailure(c, 500, "cannot begin approval")
			return
		}
		defer tx.Rollback()
		result, err := tx.Exec(`INSERT INTO tokens (user_id,key,status,name,agent_code,agent_name,workspace,runtime,created_time,accessed_time,expired_time,unlimited_quota,"group") VALUES (?,?,1,?,?,?,?,?,?,0,-1,1,'default')`, runtime.rootUser.ID, secret, request.AgentName, request.AgentCode, request.AgentName, request.Workspace, request.Runtime, time.Now().Unix())
		if err != nil {
			localFailure(c, 409, "Agent code already exists or identity cannot be issued; no authority was changed")
			return
		}
		id, err := result.LastInsertId()
		if err != nil {
			localFailure(c, 500, "cannot read issued identity")
			return
		}
		request.Policy.TokenID = int(id)
		previous, existed := runtime.policies.policies[int(id)]
		runtime.policies.policies[int(id)] = request.Policy
		if err = runtime.policies.saveLocked(); err == nil {
			_, err = tx.Exec(`UPDATE identity_requests SET state='approved', token_id=?, issued_key_hash=? WHERE id=? AND state='pending'`, id, identityHash(secret), request.ID)
			if err == nil {
				err = tx.Commit()
			}
		}
		if err != nil {
			if existed {
				runtime.policies.policies[int(id)] = previous
			} else {
				delete(runtime.policies.policies, int(id))
			}
			_ = runtime.policies.saveLocked()
			localFailure(c, 500, "approval did not complete; inspect this request before retrying")
			return
		}
		request.State, request.TokenID = "approved", int(id)
		localSuccess(c, request)
	}
}

func handleIdentityAccessDecision(c *gin.Context, runtime localRuntime, request identityRequest) {
	token, err := runtime.store.tokenByID(runtime.rootUser.ID, request.OwnerTokenID, false)
	if err != nil || (token.ExpiredTime >= 0 && token.ExpiredTime <= time.Now().Unix()) || token.Status != localStatusEnabled || token.AgentCode != request.AgentCode || token.Workspace != request.Workspace {
		localFailure(c, 409, "identity changed or was revoked; approval cannot restore it")
		return
	}
	// CAS is checked while holding the same policy lock used by all updates.
	runtime.policies.mu.Lock()
	defer runtime.policies.mu.Unlock()
	previous, exists := runtime.policies.policies[token.ID]
	data, _ := json.Marshal(struct {
		Exists bool
		Policy localTokenPolicy
	}{exists, previous})
	if identityHash(string(data)) != request.BasePolicyDigest {
		localFailure(c, 409, "current policy changed; prepare and review a new access request")
		return
	}
	request.Policy.TokenID = token.ID
	runtime.policies.policies[token.ID] = request.Policy
	err = runtime.policies.saveLocked()
	if err == nil {
		_, err = runtime.store.db.Exec(`UPDATE identity_requests SET state='approved',token_id=? WHERE id=? AND state='pending'`, token.ID, request.ID)
	}
	if err != nil {
		if exists {
			runtime.policies.policies[token.ID] = previous
		} else {
			delete(runtime.policies.policies, token.ID)
		}
		_ = runtime.policies.saveLocked()
		localFailure(c, 500, "access approval did not complete; inspect current policy and request before retrying")
		return
	}
	request.State, request.TokenID = "approved", token.ID
	localSuccess(c, request)
}
