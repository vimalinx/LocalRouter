package main

import (
	"errors"
	"fmt"
)

type serviceGrantPolicy struct {
	BaseAuthority string           `json:"base_authority"`
	BeforeExists  bool             `json:"before_exists"`
	BaseDigest    string           `json:"base_digest"`
	Before        localTokenPolicy `json:"before"`
	After         localTokenPolicy `json:"after"`
}

func policySnapshotDigest(policy localTokenPolicy, exists bool) string {
	return serviceDigest(struct {
		Exists bool
		Policy localTokenPolicy
	}{exists, policy})
}

// A proposal reviews the complete resulting policy, including dependencies.
// Existing limits, models and expiry survive the scope extension unchanged.
func (hub *serviceWorkspace) prepareGrantPolicy(proposal *serviceProposal) error {
	if proposal.Bundle == nil || proposal.GrantTokenID <= 0 {
		return nil
	}
	hub.mu.RLock()
	authorityBase := serviceAuthorityDigest(&hub.doc)
	hub.mu.RUnlock()
	previous, exists := hub.policies.policyFor(proposal.GrantTokenID)
	next := previous
	next.TokenID = proposal.GrantTokenID
	next.Surfaces = append([]string(nil), previous.Surfaces...)
	next.Packs = append([]string(nil), previous.Packs...)
	next.Operations = append([]string(nil), previous.Operations...)
	for _, member := range proposal.Bundle.Members {
		if next.SetupOnly || len(next.Packs) > 0 {
			next.Packs = append(next.Packs, member.Pack)
		}
		if next.SetupOnly || len(next.Surfaces) > 0 {
			next.Surfaces = append(next.Surfaces, "p", "mcp")
			if len(member.Workflows) > 0 {
				next.Surfaces = append(next.Surfaces, "w")
			}
		}
		if next.SetupOnly || len(next.Operations) > 0 {
			for _, operation := range member.Operations {
				next.Operations = append(next.Operations, member.Pack+"."+operation)
			}
			for _, workflow := range member.Workflows {
				for _, action := range workflowScopeActions {
					next.Operations = append(next.Operations, member.Pack+".workflow."+workflow+"."+action)
				}
			}
		}
	}
	if next.SetupOnly && len(next.Operations) == 0 {
		return errors.New("setup-only identity requires a nonempty explicit service scope")
	}
	next.SetupOnly = false
	if err := validateLocalTokenPolicy(&next); err != nil {
		return err
	}
	proposal.GrantPolicy = &serviceGrantPolicy{BaseAuthority: authorityBase, BeforeExists: exists, BaseDigest: policySnapshotDigest(previous, exists), Before: previous, After: next}
	// Adding a connection must not make the first bundle silently remove the
	// previously usable service set. Freeze that set as a separate reviewed grant.
	if proposal.Kind != "connection" {
		return nil
	}
	hub.mu.RLock()
	_, restricted := hub.doc.Grants[proposal.GrantTokenID]
	for _, grant := range hub.doc.Grants[proposal.GrantTokenID] {
		if hub.doc.Bundles[grant.Revision].ID == proposal.Bundle.ID {
			hub.mu.RUnlock()
			return errors.New("use a new bundle id when extending a connection; replace existing bundles through an explicit bundle proposal")
		}
	}
	hub.mu.RUnlock()
	if restricted || previous.SetupOnly {
		return nil
	}
	retained := serviceBundle{ID: "retained-" + serviceDigest(proposal.ID)[:16], Name: "Previously authorized services", Members: []serviceBundleMember{}}
	hub.registry.mu.RLock()
	definitions := make([]protocolDefinition, 0, len(hub.registry.templates))
	for _, definition := range hub.registry.templates {
		definitions = append(definitions, definition)
	}
	hub.registry.mu.RUnlock()
	for _, def := range definitions {
		member := serviceBundleMember{Pack: def.ID}
		for _, route := range def.Routes {
			p, _, _ := hub.policies.previewScope(proposal.GrantTokenID, "p", def.ID, route.OperationID, "")
			m, _, _ := hub.policies.previewScope(proposal.GrantTokenID, "mcp", def.ID, route.OperationID, "")
			if p || m {
				member.Operations = append(member.Operations, route.OperationID)
			}
		}
		for _, workflow := range def.Workflows {
			for _, action := range workflowScopeActions {
				if allowed, _, _ := hub.policies.previewScope(proposal.GrantTokenID, "w", def.ID, "workflow."+workflow.ID+"."+action, ""); allowed {
					member.Workflows = append(member.Workflows, workflow.ID)
					break
				}
			}
		}
		if len(member.Operations)+len(member.Workflows) > 0 {
			retained.Members = append(retained.Members, member)
		}
	}
	if len(retained.Members) > 0 {
		var err error
		retained, err = hub.resolveBundle(retained)
		if err != nil {
			return fmt.Errorf("cannot freeze existing service permissions: %w", err)
		}
		proposal.RetainedBundle = &retained
	}
	return nil
}

// The applying proposal is the durable activation journal. Admissions fail
// closed until BOTH policy and grant documents commit; reconciliation accepts
// only the reviewed base or the exact planned policy after an interrupted write.
func (hub *serviceWorkspace) commitGrantPolicy(item serviceProposal, change func(*serviceWorkspaceDocument) error) error {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if item.GrantPolicy == nil {
		return hub.updateLocked(change)
	}
	policies := hub.policies
	policies.mu.Lock()
	defer policies.mu.Unlock()
	previous, existed := policies.policies[item.GrantTokenID]
	planned := item.GrantPolicy.After
	base := policySnapshotDigest(previous, existed)
	if base != item.GrantPolicy.BaseDigest && base != policySnapshotDigest(planned, true) {
		return errors.New("target policy changed since review; preserve the current policy and prepare a fresh proposal")
	}
	current := hub.doc.Proposals[item.ID]
	if current.State != "applying" || current.AuthorityBase != serviceAuthorityDigest(&hub.doc) {
		return errors.New("authority changed since approval; operator inspection required")
	}
	policies.policies[item.GrantTokenID] = planned
	if err := policies.saveLocked(); err != nil {
		if existed {
			policies.policies[item.GrantTokenID] = previous
		} else {
			delete(policies.policies, item.GrantTokenID)
		}
		return err
	}
	if err := hub.updateLocked(change); err != nil {
		if existed {
			policies.policies[item.GrantTokenID] = previous
		} else {
			delete(policies.policies, item.GrantTokenID)
		}
		if rollbackErr := policies.saveLocked(); rollbackErr != nil {
			return errors.Join(err, errors.New("policy rollback requires reconciliation; applying identity remains blocked"))
		}
		return err
	}
	return nil
}

// Only the human operator can end an interrupted activation. Restore the base
// policy if our exact write is still present; never overwrite a later policy.
// Installed Pack files and upstream resources are deliberately not rolled back.
func (hub *serviceWorkspace) abandonGrantPolicy(proposal serviceProposal) error {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.policies.mu.Lock()
	defer hub.policies.mu.Unlock()
	current := hub.doc.Proposals[proposal.ID]
	if current.State != "applying" || current.Digest != proposal.Digest {
		return errors.New("proposal changed before abandonment")
	}
	previous, existed := hub.policies.policies[proposal.GrantTokenID]
	restore := proposal.GrantPolicy != nil && existed && serviceDigest(previous) == serviceDigest(proposal.GrantPolicy.After)
	if restore {
		if proposal.GrantPolicy.BeforeExists {
			hub.policies.policies[proposal.GrantTokenID] = proposal.GrantPolicy.Before
		} else {
			delete(hub.policies.policies, proposal.GrantTokenID)
		}
		if err := hub.policies.saveLocked(); err != nil {
			hub.policies.policies[proposal.GrantTokenID] = previous
			return err
		}
	}
	err := hub.updateLocked(func(doc *serviceWorkspaceDocument) error {
		item := doc.Proposals[proposal.ID]
		item.State = "abandoned"
		item.Error = "unfinished grant abandoned by operator; installed services and later policy edits retained"
		doc.Proposals[item.ID] = item
		return nil
	})
	if err != nil && restore {
		hub.policies.policies[proposal.GrantTokenID] = previous
		return errors.Join(err, hub.policies.saveLocked())
	}
	return err
}
