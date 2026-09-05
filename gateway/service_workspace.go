package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Service workspace state contains no credential values. Grants and proposals
// share one atomic document so an approved revision cannot be partially granted.
type serviceWorkspace struct {
	mu       sync.RWMutex
	applyMu  sync.Mutex
	path     string
	doc      serviceWorkspaceDocument
	registry *protocolRegistry
	policies *tokenPolicyStore
	store    *localStore
	userID   int
}

type serviceWorkspaceDocument struct {
	SchemaVersion string                       `json:"schema_version"`
	Bundles       map[string]serviceBundle     `json:"bundles"`
	Grants        map[int][]serviceBundleGrant `json:"grants"`
	Proposals     map[string]serviceProposal   `json:"proposals"`
	Delegations   map[int][]serviceDelegation  `json:"delegations"`
	Templates     map[string]serviceTemplate   `json:"templates"`
}

type serviceBundleMember struct {
	Pack       string   `json:"pack"`
	Operations []string `json:"operations"`
	Workflows  []string `json:"workflows,omitempty"`
}

type serviceBundle struct {
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	Description string                `json:"description,omitempty"`
	Guide       string                `json:"guide,omitempty"`
	Includes    []string              `json:"includes,omitempty"`
	Members     []serviceBundleMember `json:"members"`
	Revision    string                `json:"revision,omitempty"`
}

type serviceBundleGrant struct {
	Revision  string    `json:"revision"`
	GrantedAt time.Time `json:"granted_at"`
	ExpiresAt int64     `json:"expires_at,omitempty"`
}

type serviceDelegation struct {
	Pack      string `json:"pack"`
	Mode      string `json:"mode"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
}

type serviceProposal struct {
	ID              string               `json:"id"`
	OwnerTokenID    int                  `json:"owner_token_id"`
	AgentCode       string               `json:"agent_code"`
	Kind            string               `json:"kind"`
	Reason          string               `json:"reason"`
	CreatedAt       time.Time            `json:"created_at"`
	UpdatedAt       time.Time            `json:"updated_at"`
	State           string               `json:"state"`
	Digest          string               `json:"digest"`
	Bundle          *serviceBundle       `json:"bundle,omitempty"`
	Connection      *serviceConnection   `json:"connection,omitempty"`
	Template        *serviceTemplate     `json:"template,omitempty"`
	GrantTokenID    int                  `json:"grant_token_id,omitempty"`
	GrantExpiresAt  int64                `json:"grant_expires_at,omitempty"`
	MaintainerID    int                  `json:"maintainer_token_id,omitempty"`
	MaintenanceMode string               `json:"maintenance_mode,omitempty"`
	DraftID         string               `json:"draft_id,omitempty"`
	PackDigest      string               `json:"pack_digest,omitempty"`
	BaseDigest      string               `json:"base_digest,omitempty"`
	Impact          *protocolDraftImpact `json:"impact,omitempty"`
	AppliedDigest   string               `json:"applied_digest,omitempty"`
	Verification    string               `json:"verification"`
	Error           string               `json:"error,omitempty"`
	AuthorityBase   string               `json:"authority_base,omitempty"`
	DecidedBy       string               `json:"decided_by,omitempty"`
}

type serviceConnection struct {
	TemplateID      string             `json:"template_id"`
	TemplateVersion string             `json:"template_version"`
	Definition      protocolDefinition `json:"definition"`
	Guide           string             `json:"guide,omitempty"`
	SourceURL       string             `json:"source_url,omitempty"`
}

func emptyServiceWorkspace() serviceWorkspaceDocument {
	return serviceWorkspaceDocument{SchemaVersion: "1", Bundles: map[string]serviceBundle{}, Grants: map[int][]serviceBundleGrant{}, Proposals: map[string]serviceProposal{}, Delegations: map[int][]serviceDelegation{}, Templates: map[string]serviceTemplate{}}
}

func newServiceWorkspace(runtime localRuntime) (*serviceWorkspace, error) {
	hub := &serviceWorkspace{path: filepath.Join(runtime.config.DataDir, "service-workspace.json"), doc: emptyServiceWorkspace(), registry: runtime.protocols, policies: runtime.policies, store: runtime.store, userID: runtime.rootUser.ID}
	exists, err := inspectPrivateRegularFile(hub.path, "service workspace")
	if err != nil {
		return nil, err
	}
	if exists {
		data, err := os.ReadFile(hub.path)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &hub.doc); err != nil || hub.doc.SchemaVersion != "1" || hub.doc.Grants == nil || hub.doc.Proposals == nil || hub.doc.Bundles == nil || hub.doc.Delegations == nil || hub.doc.Templates == nil {
			return nil, errors.New("invalid service workspace document")
		}
	}
	return hub, nil
}

func serviceID(prefix string) string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic("system randomness unavailable")
	}
	return prefix + hex.EncodeToString(value[:])
}

func serviceDigest(value any) string {
	data, _ := json.Marshal(value)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// update installs an entirely new snapshot only after its private atomic write
// succeeds. Callers must not perform network or policy-store operations here.
func (hub *serviceWorkspace) update(change func(*serviceWorkspaceDocument) error) error {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	data, err := json.Marshal(hub.doc)
	if err != nil {
		return err
	}
	var next serviceWorkspaceDocument
	if err := json.Unmarshal(data, &next); err != nil {
		return err
	}
	if err := change(&next); err != nil {
		return err
	}
	data, err = json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > 32<<20 {
		return errors.New("service workspace is full; archive completed proposals before adding more")
	}
	if err := writeMaintenanceFileAtomic(hub.path, data); err != nil {
		return err
	}
	hub.doc = next
	return nil
}

func (hub *serviceWorkspace) proposal(id string, owner int) (serviceProposal, bool) {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	proposal, ok := hub.doc.Proposals[id]
	if !ok || (owner > 0 && owner != proposal.OwnerTokenID) {
		return serviceProposal{}, false
	}
	// Returned state must not share mutable pointers with the active snapshot.
	data, _ := json.Marshal(proposal)
	var copy serviceProposal
	_ = json.Unmarshal(data, &copy)
	return copy, true
}

func (hub *serviceWorkspace) proposals(owner int) []serviceProposal {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	items := []serviceProposal{}
	for _, proposal := range hub.doc.Proposals {
		if owner <= 0 || proposal.OwnerTokenID == owner {
			items = append(items, proposal)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items
}

func (hub *serviceWorkspace) bundleAllows(tokenID int, surface, pack, operation string) bool {
	if hub == nil || tokenID <= 0 || (surface != "p" && surface != "mcp" && surface != "w") {
		return true
	}
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	grants, restricted := hub.doc.Grants[tokenID]
	if !restricted {
		return true
	} // Existing callers retain their established policy.
	now := time.Now().Unix()
	for _, grant := range grants {
		if grant.ExpiresAt > 0 && now >= grant.ExpiresAt {
			continue
		}
		bundle, exists := hub.doc.Bundles[grant.Revision]
		if !exists {
			continue
		}
		for _, member := range bundle.Members {
			if member.Pack != pack {
				continue
			}
			if operation == "" {
				return len(member.Operations) > 0 || len(member.Workflows) > 0
			}
			if surface == "w" && strings.HasPrefix(operation, "workflow.") {
				for _, workflow := range member.Workflows {
					for _, action := range []string{"create", "get", "list", "cancel"} {
						if operation == "workflow."+workflow+"."+action {
							return true
						}
					}
				}
			} else {
				for _, allowed := range member.Operations {
					if operation == allowed {
						return true
					}
				}
			}
		}
	}
	return false
}

func (hub *serviceWorkspace) grantRevisions(tokenID int) []string {
	if hub == nil {
		return nil
	}
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	result := []string{}
	for _, grant := range hub.doc.Grants[tokenID] {
		if grant.ExpiresAt == 0 || time.Now().Unix() < grant.ExpiresAt {
			result = append(result, grant.Revision)
		}
	}
	sort.Strings(result)
	return result
}

func (hub *serviceWorkspace) resolveBundle(input serviceBundle) (serviceBundle, error) {
	return hub.resolveBundleWithPack(input, nil)
}

func (hub *serviceWorkspace) resolveBundleWithPack(input serviceBundle, prepared *protocolDefinition) (serviceBundle, error) {
	if !protocolIDPattern.MatchString(input.ID) || strings.TrimSpace(input.Name) == "" || len(input.Name) > 120 || len(input.Guide) > 16<<10 || len(input.Members) > 100 || len(input.Includes) > 20 {
		return serviceBundle{}, errors.New("bundle needs a valid id, name, and bounded members/guide")
	}
	input.Revision = ""
	// Included revisions are immutable flattened manifests. They cannot introduce
	// cycles or follow a mutable alias on the next request.
	hub.mu.RLock()
	for _, revision := range input.Includes {
		included, exists := hub.doc.Bundles[revision]
		if !exists {
			hub.mu.RUnlock()
			return serviceBundle{}, fmt.Errorf("unknown included bundle revision %s", revision)
		}
		input.Members = append(input.Members, included.Members...)
	}
	hub.mu.RUnlock()
	members := map[string]*serviceBundleMember{}
	for _, member := range input.Members {
		definition, exists := hub.registry.get(member.Pack)
		if prepared != nil && prepared.ID == member.Pack {
			definition, exists = *prepared, true
		}
		if !exists {
			return serviceBundle{}, fmt.Errorf("unknown Pack %s", member.Pack)
		}
		entry := members[member.Pack]
		if entry == nil {
			entry = &serviceBundleMember{Pack: member.Pack, Operations: []string{}}
			members[member.Pack] = entry
		}
		for _, operation := range member.Operations {
			if operation == "*" {
				for _, route := range definition.Routes {
					entry.Operations = append(entry.Operations, route.OperationID)
				}
				continue
			}
			if _, exists := operationRoute(definition, operation); !exists {
				return serviceBundle{}, fmt.Errorf("unknown operation %s.%s", member.Pack, operation)
			}
			entry.Operations = append(entry.Operations, operation)
		}
		for _, workflow := range member.Workflows {
			var spec protocolWorkflow
			exists := false
			for _, candidate := range definition.Workflows {
				if candidate.ID == workflow {
					spec, exists = candidate, true
					break
				}
			}
			if !exists {
				return serviceBundle{}, fmt.Errorf("unknown workflow %s.%s", member.Pack, workflow)
			}
			entry.Workflows = append(entry.Workflows, workflow)
			// Show and grant the workflow's complete transitive operation set. Each
			// execution still checks the owner's current policy after revocation.
			entry.Operations = append(entry.Operations, serviceWorkflowOperations(spec)...)
		}
	}
	input.Members = []serviceBundleMember{}
	for _, member := range members {
		member.Operations = normalizePolicyValues(member.Operations)
		member.Workflows = normalizePolicyValues(member.Workflows)
		input.Members = append(input.Members, *member)
	}
	sort.Slice(input.Members, func(i, j int) bool { return input.Members[i].Pack < input.Members[j].Pack })
	input.Includes = normalizePolicyValues(input.Includes)
	input.Revision = serviceDigest(input)
	return input, nil
}

func serviceWorkflowOperations(workflow protocolWorkflow) []string {
	ops := []string{workflow.CreateOperation, workflow.PollOperation, workflow.CancelOperation}
	for _, step := range workflow.Steps {
		ops = append(ops, step.Operation)
		for _, call := range step.Parallel {
			ops = append(ops, call.Operation)
		}
	}
	return normalizePolicyValues(ops)
}

func (hub *serviceWorkspace) bundleView(owner int) map[string]any {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	bundles := []serviceBundle{}
	for _, bundle := range hub.doc.Bundles {
		bundles = append(bundles, bundle)
	}
	sort.Slice(bundles, func(i, j int) bool {
		if bundles[i].ID == bundles[j].ID {
			return bundles[i].Revision < bundles[j].Revision
		}
		return bundles[i].ID < bundles[j].ID
	})
	grants := map[string][]serviceBundleGrant{}
	for id, items := range hub.doc.Grants {
		if owner <= 0 || owner == id {
			grants[strconv.Itoa(id)] = items
		}
	}
	return map[string]any{"bundles": bundles, "grants": grants, "scope": "service APIs on p/mcp/w; model relay policy remains independent"}
}
