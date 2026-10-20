package main

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/gin-gonic/gin"
)

type serviceReprepareInput struct {
	ID     string `json:"id,omitempty"`
	Digest string `json:"digest"`
	Enable bool   `json:"enable,omitempty"`
}

// Repreparation never applies a Pack or transfers approval. The original review
// remains available, and a deterministic replacement makes response loss safe.
func (hub *serviceWorkspace) reprepare(id, digest string, actor int, enable bool) (serviceProposal, error) {
	hub.applyMu.Lock()
	defer hub.applyMu.Unlock()
	old, ok := hub.proposal(id, actor)
	if !ok {
		return old, errors.New("unknown proposal")
	}
	if old.Digest != digest || !validProtocolDigest(digest) {
		return old, errors.New("proposal digest changed; inspect before repreparing")
	}
	if old.SupersededBy != "" {
		item, exists := hub.proposal(old.SupersededBy, actor)
		if !exists {
			return old, errors.New("replacement proposal unavailable; inspect history")
		}
		return item, nil
	}
	if old.State != "awaiting_approval" {
		return old, errors.New("only an unapplied awaiting_approval proposal can be reprepared; reconcile interrupted applies first")
	}
	owner, err := hub.store.tokenByID(hub.userID, old.OwnerTokenID, false)
	if err != nil || owner.Status != localStatusEnabled || (owner.ExpiredTime >= 0 && owner.ExpiredTime <= time.Now().Unix()) {
		return old, errors.New("proposal owner is unavailable, disabled or expired")
	}
	if policy, exists := hub.policies.policyFor(owner.ID); exists && policy.ExpiresAt > 0 && policy.ExpiresAt <= time.Now().Unix() {
		return old, errors.New("proposal owner policy has expired")
	}
	// Deep copy: normalization must not alter the original reviewed document.
	data, _ := json.Marshal(old)
	var copied serviceProposal
	if err := json.Unmarshal(data, &copied); err != nil {
		return old, err
	}
	if copied.Connection != nil {
		copied.Connection.Definition.Auth.SecretFile = ""
		if enable {
			copied.Connection.Definition.Enabled = true
		}
	}
	replacementID := "sp-" + serviceDigest(struct {
		ID, Digest string
		Enable     bool
	}{id, digest, enable})[:32]
	item, exists := hub.proposal(replacementID, old.OwnerTokenID)
	if !exists {
		item, err = hub.prepare(owner, serviceProposalInput{
			ID: replacementID, supersedes: id, Kind: old.Kind, Reason: old.Reason,
			Connection: copied.Connection, Bundle: copied.Bundle, Template: copied.Template,
			GrantTokenID: old.GrantTokenID, GrantExpiresAt: old.GrantExpiresAt,
			MaintainerID: old.MaintainerID, MaintenanceMode: old.MaintenanceMode,
		})
		if err != nil {
			return item, err
		}
	}
	if item.Supersedes != id || item.State != "awaiting_approval" {
		return item, errors.New("replacement is not ready for review; inspect its retained preparation result")
	}
	err = hub.update(func(doc *serviceWorkspaceDocument) error {
		current := doc.Proposals[id]
		if current.Digest != digest || current.State != "awaiting_approval" {
			return errors.New("original proposal changed during repreparation")
		}
		current.State, current.SupersededBy, current.UpdatedAt = "superseded", item.ID, time.Now().UTC()
		doc.Proposals[id] = current
		return nil
	})
	if err == nil {
		hub.runtime.humanEvent(gin.H{"event": "service_proposal_decided", "id": id, "state": "superseded"})
		hub.requestDecision(owner, item, "替代 "+id+"，需要重新审阅")
	}
	return item, err
}

func proposalReviewBlocker(p serviceProposal) string {
	if p.Bundle != nil && p.GrantTokenID > 0 && p.GrantPolicy == nil {
		return "legacy proposal lacks reviewed effective policy; use lr setup reprepare <id> <digest> and review the replacement"
	}
	if p.Connection != nil && !p.Connection.Definition.Enabled {
		return "connection is disabled; explicitly reprepare with --enable and review the replacement"
	}
	return ""
}
