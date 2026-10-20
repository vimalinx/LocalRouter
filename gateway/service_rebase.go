package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A connection proposal is reviewed as a change to its own Pack files. The
// live digest covers every Pack, so an unrelated apply would otherwise make
// each pending proposal stale. BaseFiles records the live bytes of exactly the
// reviewed paths; while they are unchanged the same change is rebuilt on the
// current live tree at approval, and the reviewed digest stays authoritative.

const (
	freshnessCurrent  = "current"
	freshnessRebase   = "rebase"
	freshnessConflict = "conflict"
	freshnessUnknown  = "unknown"
)

func (hub *serviceWorkspace) liveFileDigests(paths []string) map[string]string {
	digests := make(map[string]string, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(filepath.Join(hub.registry.dir, filepath.FromSlash(path)))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			digests[path] = ""
		case err != nil:
			digests[path] = "unreadable"
		default:
			sum := sha256.Sum256(data)
			digests[path] = hex.EncodeToString(sum[:])
		}
	}
	return digests
}

// captureBaseFiles leaves BaseFiles empty when live changed while hashing, so
// such a proposal falls back to explicit repreparation.
func (hub *serviceWorkspace) captureBaseFiles(proposal *serviceProposal) {
	proposal.BaseFiles = nil
	if proposal.Impact == nil || hub.registry.currentDigest() != proposal.BaseDigest {
		return
	}
	paths := make([]string, 0, len(proposal.Impact.Files))
	for _, file := range proposal.Impact.Files {
		paths = append(paths, file.Path)
	}
	digests := hub.liveFileDigests(paths)
	if hub.registry.currentDigest() == proposal.BaseDigest {
		proposal.BaseFiles = digests
	}
}

func (hub *serviceWorkspace) freshness(proposal serviceProposal) string {
	if proposal.Connection == nil || proposal.State != "awaiting_approval" {
		return ""
	}
	if hub.registry.currentDigest() == proposal.BaseDigest {
		return freshnessCurrent
	}
	if len(proposal.BaseFiles) == 0 {
		return freshnessUnknown
	}
	paths := make([]string, 0, len(proposal.BaseFiles))
	for path := range proposal.BaseFiles {
		paths = append(paths, path)
	}
	live := hub.liveFileDigests(paths)
	for path, digest := range proposal.BaseFiles {
		if live[path] != digest {
			return freshnessConflict
		}
	}
	return freshnessRebase
}

// rebaseForApproval runs under applyMu. It never widens the change: the new
// draft must produce the identical reviewed impact.
func (hub *serviceWorkspace) rebaseForApproval(proposal *serviceProposal) error {
	pack := proposal.Connection.Definition.ID
	switch hub.freshness(*proposal) {
	case freshnessCurrent:
		return nil
	case freshnessConflict:
		return fmt.Errorf("stale_proposal: %s changed after this review; reprepare to review the change against its current version", pack)
	case freshnessUnknown:
		return errors.New("stale_proposal: live Packs changed and this proposal predates safe rebasing; reprepare it")
	}
	rebased := *proposal
	rebased.DraftID = "setup-" + serviceID("")[:20]
	if err := hub.prepareConnectionDraft(&rebased); err != nil {
		_ = hub.registry.maintenanceAbortDraft(rebased.DraftID)
		return fmt.Errorf("stale_proposal: rebuilding the reviewed change on current live Packs failed (%v); reprepare it", err)
	}
	if serviceDigest(rebased.Impact) != serviceDigest(proposal.Impact) {
		_ = hub.registry.maintenanceAbortDraft(rebased.DraftID)
		return errors.New("stale_proposal: on current live Packs this change would differ from the reviewed one; reprepare it")
	}
	err := hub.update(func(doc *serviceWorkspaceDocument) error {
		current := doc.Proposals[proposal.ID]
		if current.Digest != proposal.Digest || current.State != "awaiting_approval" {
			return errors.New("proposal changed before decision")
		}
		current.DraftID, current.PackDigest, current.BaseDigest = rebased.DraftID, rebased.PackDigest, rebased.BaseDigest
		now := time.Now().UTC()
		current.BaseFiles, current.RebasedAt, current.UpdatedAt = rebased.BaseFiles, &now, now
		doc.Proposals[proposal.ID] = current
		return nil
	})
	if err != nil {
		_ = hub.registry.maintenanceAbortDraft(rebased.DraftID)
		return err
	}
	_ = hub.registry.maintenanceAbortDraft(proposal.DraftID)
	proposal.DraftID, proposal.PackDigest, proposal.BaseDigest, proposal.BaseFiles = rebased.DraftID, rebased.PackDigest, rebased.BaseDigest, rebased.BaseFiles
	return nil
}

// recheck repeats the live check for an applied connection, for example after
// the provider recovered or the credential was replaced.
func (hub *serviceWorkspace) recheck(id string, owner int) (serviceProposal, error) {
	hub.applyMu.Lock()
	defer hub.applyMu.Unlock()
	proposal, exists := hub.proposal(id, owner)
	if !exists {
		return proposal, errors.New("unknown proposal")
	}
	if proposal.State != "applied" || proposal.Connection == nil {
		return proposal, errors.New("only an applied connection can be checked")
	}
	verification := hub.liveCheck(proposal.Connection.Definition.ID)
	if err := hub.update(func(doc *serviceWorkspaceDocument) error {
		item := doc.Proposals[id]
		item.Verification = verification
		doc.Proposals[id] = item
		return nil
	}); err != nil {
		return proposal, err
	}
	item, _ := hub.proposal(id, owner)
	return item, nil
}

// liveCheck makes one credentialed request after apply, only to a declared
// readiness probe or a parameterless model listing: both are free and free of
// side effects. Business operations, even safe GETs, may be billed and are
// never called by setup.
func (hub *serviceWorkspace) liveCheck(pack string) string {
	definition, ok := hub.registry.get(pack)
	if !ok || !definition.Enabled {
		return "contract-only"
	}
	var config protocolReadinessConfig
	if definition.Readiness != nil && definition.Readiness.Mode == "probe" {
		config = *definition.Readiness
	} else {
		for _, route := range definition.Routes {
			path := route.Path
			if route.UpstreamPath != "" {
				path = route.UpstreamPath
			}
			if len(route.Methods) == 1 && route.Methods[0] == "GET" && route.TargetSelector == nil &&
				(route.Transport == "" || route.Transport == "http") && (path == "/models" || strings.HasSuffix(path, "/models")) {
				config = protocolReadinessConfig{Method: "GET", Path: path, Target: route.Target, TimeoutSeconds: 15}
				break
			}
		}
	}
	if config.Path == "" {
		return "contract-only"
	}
	if ready, status := hub.registry.executeReadinessProbe(definition, config); !ready {
		return "live-failed:" + status
	}
	return "live-ok"
}
