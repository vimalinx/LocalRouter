package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type serviceProposalInput struct {
	ID              string             `json:"id,omitempty"`
	Kind            string             `json:"kind"`
	Reason          string             `json:"reason"`
	Bundle          *serviceBundle     `json:"bundle,omitempty"`
	Connection      *serviceConnection `json:"connection,omitempty"`
	Template        *serviceTemplate   `json:"template,omitempty"`
	GrantTokenID    int                `json:"grant_token_id,omitempty"`
	GrantExpiresAt  int64              `json:"grant_expires_at,omitempty"`
	MaintainerID    int                `json:"maintainer_token_id,omitempty"`
	MaintenanceMode string             `json:"maintenance_mode,omitempty"`
}

func proposalReviewDigest(proposal serviceProposal) string {
	// Mutable state and execution results never change the reviewed authority.
	return serviceDigest(struct {
		Owner                                   int
		Kind, Reason                            string
		Bundle                                  *serviceBundle
		Connection                              *serviceConnection
		Template                                *serviceTemplate
		GrantTokenID                            int
		GrantExpiresAt                          int64
		MaintainerID                            int
		MaintenanceMode, PackDigest, BaseDigest string
		Impact                                  *protocolDraftImpact
	}{proposal.OwnerTokenID, proposal.Kind, proposal.Reason, proposal.Bundle, proposal.Connection, proposal.Template, proposal.GrantTokenID, proposal.GrantExpiresAt, proposal.MaintainerID, proposal.MaintenanceMode, proposal.PackDigest, proposal.BaseDigest, proposal.Impact})
}

func (hub *serviceWorkspace) prepare(owner localToken, input serviceProposalInput) (serviceProposal, error) {
	if owner.AgentCode == "localrouter-system" || owner.Name == localTokenName {
		return serviceProposal{}, errors.New("agent_identity_required: run lr init and use a dedicated registered Agent Token before preparing a proposal")
	}
	if owner.ID <= 0 || len(input.Reason) == 0 || len(input.Reason) > 2000 {
		return serviceProposal{}, errors.New("a registered identity and a short reason are required")
	}
	if input.ID != "" {
		if !protocolOperationIDPattern.MatchString(input.ID) {
			return serviceProposal{}, errors.New("invalid proposal id")
		}
		if previous, exists := hub.proposal(input.ID, 0); exists {
			if previous.OwnerTokenID != owner.ID {
				return serviceProposal{}, errors.New("proposal id already exists")
			}
			return serviceProposal{}, fmt.Errorf("proposal already exists; inspect %s instead of replaying preparation", input.ID)
		}
	} else {
		input.ID = serviceID("sp-")
	}
	if input.GrantExpiresAt < 0 || (input.GrantExpiresAt > 0 && input.GrantExpiresAt <= time.Now().Unix()) {
		return serviceProposal{}, errors.New("grant expiry must be in the future")
	}
	if input.MaintainerID != 0 {
		if input.MaintenanceMode != "compatible" || !hub.policies.hasCapability(input.MaintainerID, localRouterMaintainCapability) {
			return serviceProposal{}, errors.New("maintenance delegation requires a distinct maintenance-only Token and mode compatible")
		}
		if input.MaintainerID == owner.ID && !hub.policies.hasCapability(owner.ID, localRouterMaintainCapability) {
			return serviceProposal{}, errors.New("service and maintenance Tokens must remain separate")
		}
	} else if input.MaintenanceMode != "" {
		return serviceProposal{}, errors.New("maintenance mode requires maintainer_token_id")
	}
	if input.GrantTokenID != 0 {
		if _, err := hub.store.tokenByID(owner.UserID, input.GrantTokenID, false); err != nil || hub.policies.hasCapability(input.GrantTokenID, localRouterMaintainCapability) {
			return serviceProposal{}, errors.New("grant target must be an existing service Token")
		}
	}
	now := time.Now().UTC()
	proposal := serviceProposal{ID: input.ID, OwnerTokenID: owner.ID, AgentCode: owner.AgentCode, Kind: input.Kind, Reason: input.Reason, CreatedAt: now, UpdatedAt: now, State: "preparing", Verification: "contract-only", GrantTokenID: input.GrantTokenID, GrantExpiresAt: input.GrantExpiresAt, MaintainerID: input.MaintainerID, MaintenanceMode: input.MaintenanceMode}
	switch input.Kind {
	case "bundle":
		if input.Bundle == nil || input.Connection != nil || input.Template != nil || input.MaintainerID != 0 {
			return proposal, errors.New("bundle proposal requires only a bundle and optional service grant")
		}
		bundle, err := hub.resolveBundle(*input.Bundle)
		if err != nil {
			return proposal, err
		}
		proposal.Bundle = &bundle
		if proposal.GrantTokenID == 0 && !hub.policies.hasCapability(owner.ID, localRouterMaintainCapability) {
			proposal.GrantTokenID = owner.ID
		}
	case "template":
		if input.Template == nil || input.Bundle != nil || input.Connection != nil || input.GrantTokenID != 0 || input.MaintainerID != 0 {
			return proposal, errors.New("template proposal requires only a shareable template")
		}
		if err := hub.validateTemplate(input.Template); err != nil {
			return proposal, err
		}
		proposal.Template = input.Template
	case "connection":
		if input.Connection == nil || input.Template != nil {
			return proposal, errors.New("connection proposal requires a connection and optional explicit capability bundle")
		}
		if input.GrantTokenID != 0 && input.Bundle == nil {
			return proposal, errors.New("granting calls requires an explicit capability bundle")
		}
		if _, exists := hub.template(input.Connection.TemplateID, input.Connection.TemplateVersion); !exists {
			return proposal, errors.New("select one exact template id and version")
		}
		connection := *input.Connection
		if err := hub.normalizeConnection(&connection, input.ID); err != nil {
			return proposal, err
		}
		proposal.Connection = &connection
		if input.Bundle != nil {
			bundle, err := hub.resolveBundleWithPack(*input.Bundle, &connection.Definition)
			if err != nil {
				return proposal, err
			}
			proposal.Bundle = &bundle
			if proposal.GrantTokenID == 0 && !hub.policies.hasCapability(owner.ID, localRouterMaintainCapability) {
				proposal.GrantTokenID = owner.ID
			}
		}
		proposal.DraftID = "setup-" + strings.TrimPrefix(serviceID(""), "")[:20]
	default:
		return proposal, errors.New("kind must be connection, bundle, or template")
	}
	// Reserve ownership before touching the isolated draft. A consumer can only
	// prepare a new server-named draft, never select or overwrite a human draft.
	if err := hub.update(func(doc *serviceWorkspaceDocument) error {
		if _, exists := doc.Proposals[proposal.ID]; exists {
			return errors.New("proposal id already exists")
		}
		pending := 0
		for _, item := range doc.Proposals {
			if item.OwnerTokenID == owner.ID && (item.State == "preparing" || item.State == "awaiting_approval") {
				pending++
			}
		}
		if pending >= 20 {
			return errors.New("too many pending proposals; withdraw or finish an existing proposal")
		}
		doc.Proposals[proposal.ID] = proposal
		return nil
	}); err != nil {
		return proposal, err
	}
	if proposal.Connection != nil {
		if err := hub.prepareConnectionDraft(&proposal); err != nil {
			_ = hub.update(func(doc *serviceWorkspaceDocument) error {
				item := doc.Proposals[proposal.ID]
				item.State, item.Error = "preparation_failed", err.Error()
				doc.Proposals[item.ID] = item
				return nil
			})
			return proposal, err
		}
	}
	proposal.State = "awaiting_approval"
	proposal.Digest = proposalReviewDigest(proposal)
	proposal.UpdatedAt = time.Now().UTC()
	err := hub.update(func(doc *serviceWorkspaceDocument) error { doc.Proposals[proposal.ID] = proposal; return nil })
	return proposal, err
}

func (hub *serviceWorkspace) normalizeConnection(connection *serviceConnection, proposalID string) error {
	definition := &connection.Definition
	if err := validateServiceSource(connection.SourceURL); err != nil {
		return err
	}
	if len(connection.Guide) > 32<<10 {
		return errors.New("maintenance guide is too large")
	}
	target, err := url.Parse(definition.BaseURL)
	if err != nil || target.User != nil || target.RawQuery != "" || target.Fragment != "" {
		return errors.New("base_url must not contain credentials, query or fragment")
	}
	if definition.Auth.SecretFile != "" {
		return errors.New("Agents select authentication type; credential bindings are owned by LocalRouter")
	}
	if definition.Pool != nil || len(definition.Targets) != 0 || len(definition.UpstreamProfiles) != 0 || definition.Auth.HMAC != nil || definition.Auth.OAuth2 != nil || definition.Auth.AWSSigV4 != nil {
		return errors.New("the onboarding lane supports none/bearer/header auth and one target; advanced pools/adapters use delegated Pack authoring")
	}
	switch definition.Auth.Type {
	case "", "none":
		definition.Auth = protocolAuth{Type: "none"}
	case "bearer", "header":
		definition.Auth.SecretFile = "service-credentials/" + definition.ID + "-" + serviceDigest(proposalID)[:16] + ".secret"
		if previous, exists := hub.registry.get(definition.ID); exists && previous.Auth.Type == definition.Auth.Type && previous.Auth.Header == definition.Auth.Header && previous.Auth.ValueTemplate == definition.Auth.ValueTemplate {
			definition.Auth.SecretFile = previous.Auth.SecretFile
		}
	default:
		return errors.New("onboarding authentication must be none, bearer or header")
	}
	if definition.Readiness != nil && definition.Readiness.Mode == "probe" {
		return errors.New("onboarding preparation does not run provider probes; use credential readiness, then verify explicitly")
	}
	for i := range definition.Routes {
		route := &definition.Routes[i]
		if route.Transport == "adapter" || route.Adapter != nil || route.TargetSelector != nil {
			return errors.New("adapters and dynamic target selection require delegated Pack authoring")
		}
		if route.Retry.Mode == "always" {
			return errors.New("onboarding cannot enable unconditional replay")
		}
		// The proposal proves a checked contract, not an upstream operation.
		route.Availability = protocolRouteAvailability{Status: "observed", Level: "contract"}
	}
	if err := validateMaintenanceDefinition(hub.registry, definition, false); err != nil {
		return err
	}
	return nil
}

func (hub *serviceWorkspace) prepareConnectionDraft(proposal *serviceProposal) error {
	if _, failure := hub.registry.maintenanceOpenDraft(proposal.DraftID); failure != nil {
		return errors.New(failure.Message)
	}
	definition := proposal.Connection.Definition
	// The complete definition uses the existing Pack validator and server-owned
	// canonical file writer. No client-supplied filesystem path is accepted.
	hub.registry.lifecycleMu.Lock()
	root, exists := hub.registry.protocolDraftRoot(proposal.DraftID)
	if !exists {
		hub.registry.lifecycleMu.Unlock()
		return errors.New("prepared draft is missing")
	}
	if err := writeMaintenanceDefinition(filepath.Join(root, definition.ID+".json"), definition); err != nil {
		hub.registry.lifecycleMu.Unlock()
		return err
	}
	hub.registry.lifecycleMu.Unlock()
	if proposal.Connection.Guide != "" {
		_, failure := hub.registry.maintenancePutGuide(maintenanceGuideInput{DraftID: proposal.DraftID, PackID: definition.ID, ID: "service-maintenance", Title: "Service maintenance", Summary: "Provider contract and repair guidance", Status: "observed", Markdown: proposal.Connection.Guide})
		if failure != nil {
			return errors.New(failure.Message)
		}
	}
	result, failure := hub.registry.maintenanceReviewDraft(proposal.DraftID)
	if failure != nil {
		return errors.New(failure.Message)
	}
	data, _ := json.Marshal(result)
	var review struct {
		Digest     string              `json:"digest"`
		BaseDigest string              `json:"base_digest"`
		Impact     protocolDraftImpact `json:"impact"`
	}
	if err := json.Unmarshal(data, &review); err != nil {
		return err
	}
	for _, impact := range review.Impact.Protocols {
		if impact.ID != definition.ID {
			return errors.New("draft affects another Pack")
		}
	}
	proposal.PackDigest, proposal.BaseDigest, proposal.Impact = review.Digest, review.BaseDigest, &review.Impact
	return nil
}

// Compatible repairs preserve authority-bearing fields. Request/response JSON
// mappings, schemas, metering, pricing, summaries and timeouts may be repaired
// within the explicitly delegated service contract.
func compatibleServiceRepair(before, after protocolDefinition) bool {
	strip := func(definition protocolDefinition) protocolDefinition {
		definition.Description, definition.Name = "", ""
		definition.Timeout = 0
		definition.Pricing = nil
		definition.SourceFile, definition.ParsedBaseURL, definition.ParsedTargets = "", nil, nil
		definition.Routes = append([]protocolRoute(nil), definition.Routes...)
		for i := range definition.Routes {
			route := &definition.Routes[i]
			route.Summary = ""
			route.RequestBody, route.RequestSchema, route.ResponseSchema = nil, nil, nil
			route.RequestTransform.JSON = protocolJSONTransform{}
			route.ResponseTransform = protocolJSONTransform{}
			route.Metering = nil
		}
		sort.Slice(definition.Routes, func(i, j int) bool { return definition.Routes[i].OperationID < definition.Routes[j].OperationID })
		return definition
	}
	return serviceDigest(strip(before)) == serviceDigest(strip(after))
}

func (hub *serviceWorkspace) scopedMaintainer(tokenID int) bool {
	if hub == nil || tokenID <= 0 {
		return false
	}
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	_, exists := hub.doc.Delegations[tokenID]
	return exists
}

func (hub *serviceWorkspace) mayRepair(tokenID int, proposal serviceProposal) bool {
	if proposal.Connection == nil || proposal.Bundle != nil || proposal.Kind != "connection" || proposal.MaintainerID != 0 || proposal.GrantTokenID != 0 {
		return false
	}
	if !hub.policies.isAgentMaintenanceEnabled() || !hub.policies.hasCapability(tokenID, localRouterMaintainCapability) {
		return false
	}
	pack := proposal.Connection.Definition.ID
	hub.mu.RLock()
	delegated := false
	for _, grant := range hub.doc.Delegations[tokenID] {
		if grant.Pack == pack && grant.Mode == "compatible" && (grant.ExpiresAt == 0 || grant.ExpiresAt > time.Now().Unix()) {
			delegated = true
		}
	}
	hub.mu.RUnlock()
	previous, exists := hub.registry.get(pack)
	return delegated && exists && compatibleServiceRepair(previous, proposal.Connection.Definition)
}

func (hub *serviceWorkspace) decide(id, digest, decision, credential string, actor int) (serviceProposal, error) {
	hub.applyMu.Lock()
	defer hub.applyMu.Unlock()
	proposal, exists := hub.proposal(id, actor)
	if !exists {
		return proposal, errors.New("unknown proposal")
	}
	if proposal.Digest != digest || !validProtocolDigest(digest) {
		return proposal, errors.New("proposal digest changed; inspect the exact proposal before deciding")
	}
	if proposal.State == "applied" {
		return proposal, nil
	}
	if proposal.State != "awaiting_approval" {
		return proposal, fmt.Errorf("proposal is %s; inspect it before proceeding", proposal.State)
	}
	if decision != "approve" && decision != "reject" && decision != "withdraw" {
		return proposal, errors.New("decision must be approve, reject or withdraw")
	}
	if actor > 0 && decision == "reject" {
		return proposal, errors.New("Agents withdraw their own proposals; rejection is an operator decision")
	}
	if actor > 0 && decision == "approve" && !hub.mayRepair(actor, proposal) {
		return proposal, errors.New("needs_approval: this change exceeds the granted maintenance boundary")
	}
	if credential != "" && (actor > 0 || proposal.Connection == nil || proposal.Connection.Definition.Auth.Type == "none") {
		return proposal, errors.New("credential provisioning is operator-only and requires an authenticated service connection")
	}
	if len(credential) > 32<<10 || strings.ContainsAny(credential, "\r\n\x00") {
		return proposal, errors.New("invalid service credential")
	}
	if credential != "" && proposal.Connection != nil {
		path, err := hub.registry.secretPath(proposal.Connection.Definition.Auth.SecretFile)
		if err != nil {
			return proposal, err
		}
		if exists, err := inspectPrivateRegularFile(path, "service credential"); err != nil || exists {
			return proposal, errors.New("an existing credential binding is preserved; omit credential to reuse it")
		}
	}
	if proposal.Connection != nil && decision == "approve" {
		if hub.registry.currentDigest() != proposal.BaseDigest {
			return proposal, errors.New("stale proposal: live Packs changed; prepare a new proposal")
		}
		if proposal.Connection.Definition.Auth.Type != "none" && credential == "" {
			if _, err := hub.registry.readSecret(proposal.Connection.Definition.Auth); err != nil {
				return proposal, errors.New("needs_credential: provision the service credential when approving")
			}
		}
	}
	if decision == "approve" {
		if err := hub.validateApprovalTargets(proposal); err != nil {
			return proposal, err
		}
	}
	state := "applying"
	if decision == "reject" {
		state = "rejected"
	}
	if decision == "withdraw" {
		state = "withdrawn"
	}
	if err := hub.update(func(doc *serviceWorkspaceDocument) error {
		current := doc.Proposals[id]
		if current.Digest != digest || current.State != "awaiting_approval" {
			return errors.New("proposal changed before decision")
		}
		current.State, current.UpdatedAt = state, time.Now().UTC()
		current.AuthorityBase = serviceAuthorityDigest(doc)
		current.DecidedBy = "operator"
		if actor > 0 {
			current.DecidedBy = fmt.Sprintf("agent:%d", actor)
		}
		doc.Proposals[id] = current
		return nil
	}); err != nil {
		return proposal, err
	}
	if decision != "approve" {
		item, _ := hub.proposal(id, actor)
		return item, nil
	}
	var applyErr error
	if proposal.Connection != nil {
		if credential != "" {
			path, err := hub.registry.secretPath(proposal.Connection.Definition.Auth.SecretFile)
			if err == nil {
				err = ensurePrivateDirectory(filepath.Dir(path))
			}
			if err == nil {
				err = writeMaintenanceFileAtomic(path, []byte(credential+"\n"))
			}
			applyErr = err
		}
		if applyErr == nil {
			plan, failure := hub.registry.maintenancePlanDraft(proposal.DraftID, proposal.PackDigest)
			if failure != nil {
				applyErr = errors.New(failure.Message)
			} else {
				_, failure = hub.registry.applyPlannedProtocolDigest(plan.Digest)
				if failure != nil {
					applyErr = fmt.Errorf("%s: %s (rollback: %v)", failure.Code, failure.Message, failure.Rollback)
				}
			}
		}
	}
	if applyErr != nil {
		_ = hub.update(func(doc *serviceWorkspaceDocument) error {
			item := doc.Proposals[id]
			item.State, item.Error = "apply_failed", applyErr.Error()
			doc.Proposals[id] = item
			return nil
		})
		item, _ := hub.proposal(id, actor)
		return item, applyErr
	}
	if err := hub.finishApprovedProposal(id); err != nil {
		return proposal, fmt.Errorf("apply receipt needs reconciliation: %w", err)
	}
	item, _ := hub.proposal(id, actor)
	return item, nil
}

func serviceAuthorityDigest(doc *serviceWorkspaceDocument) string {
	return serviceDigest([]any{doc.Grants, doc.Delegations})
}

func (hub *serviceWorkspace) validateApprovalTargets(item serviceProposal) error {
	if item.GrantExpiresAt > 0 && item.GrantExpiresAt <= time.Now().Unix() {
		return errors.New("grant has expired; prepare a fresh proposal")
	}
	for _, id := range []int{item.GrantTokenID, item.MaintainerID} {
		if id <= 0 {
			continue
		}
		token, err := hub.store.tokenByID(hub.userID, id, false)
		if err != nil || token.Status != localStatusEnabled || (token.ExpiredTime >= 0 && token.ExpiredTime <= time.Now().Unix()) {
			return errors.New("approval target Token is unavailable")
		}
		if policy, ok := hub.policies.policyFor(id); ok && policy.ExpiresAt > 0 && policy.ExpiresAt <= time.Now().Unix() {
			return errors.New("approval target policy has expired")
		}
	}
	if item.GrantTokenID > 0 && hub.policies.hasCapability(item.GrantTokenID, localRouterMaintainCapability) {
		return errors.New("grant target changed to maintenance-only")
	}
	if item.MaintainerID > 0 && !hub.policies.hasCapability(item.MaintainerID, localRouterMaintainCapability) {
		return errors.New("maintenance target capability changed")
	}
	return nil
}

func (hub *serviceWorkspace) finishApprovedProposal(id string) error {
	item, exists := hub.proposal(id, 0)
	if !exists || item.State != "applying" {
		return errors.New("proposal is not applying")
	}
	if err := hub.validateApprovalTargets(item); err != nil {
		return err
	}
	return hub.update(func(doc *serviceWorkspaceDocument) error {
		item := doc.Proposals[id]
		if item.State != "applying" || item.AuthorityBase != serviceAuthorityDigest(doc) {
			return errors.New("authority changed since approval; operator inspection required")
		}
		if item.Bundle != nil {
			doc.Bundles[item.Bundle.Revision] = *item.Bundle
			if item.GrantTokenID > 0 {
				grants := []serviceBundleGrant{}
				for _, grant := range doc.Grants[item.GrantTokenID] {
					if doc.Bundles[grant.Revision].ID != item.Bundle.ID {
						grants = append(grants, grant)
					}
				}
				doc.Grants[item.GrantTokenID] = append(grants, serviceBundleGrant{Revision: item.Bundle.Revision, GrantedAt: time.Now().UTC(), ExpiresAt: item.GrantExpiresAt})
			}
		}
		if item.Template != nil {
			key := item.Template.ID + ":" + item.Template.Version
			if _, exists := doc.Templates[key]; exists {
				return errors.New("template version already exists")
			}
			doc.Templates[key] = *item.Template
		}
		if item.MaintainerID > 0 && item.Connection != nil {
			grants := []serviceDelegation{}
			for _, grant := range doc.Delegations[item.MaintainerID] {
				if grant.Pack != item.Connection.Definition.ID {
					grants = append(grants, grant)
				}
			}
			doc.Delegations[item.MaintainerID] = append(grants, serviceDelegation{Pack: item.Connection.Definition.ID, Mode: item.MaintenanceMode, ExpiresAt: item.GrantExpiresAt})
		}
		item.State, item.UpdatedAt, item.Error = "applied", time.Now().UTC(), ""
		if item.Connection != nil {
			item.AppliedDigest = item.PackDigest
		}
		doc.Proposals[id] = item
		return nil
	})
}
