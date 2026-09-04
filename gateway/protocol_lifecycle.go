package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type protocolCandidate struct {
	Definitions map[string]protocolDefinition
	Guides      map[string][]protocolGuide
	Digest      string
}

type protocolPackPlan struct {
	Digest     string    `json:"digest"`
	BaseDigest string    `json:"base_digest,omitempty"`
	LiveDigest string    `json:"live_digest"`
	CreatedAt  time.Time `json:"created_at"`
	Protocols  []string  `json:"protocols"`
	DraftID    string    `json:"draft_id,omitempty"`
	Files      []string  `json:"files,omitempty"`
}

type protocolRevisionInfo struct {
	Digest    string    `json:"digest"`
	CreatedAt time.Time `json:"created_at"`
	Live      bool      `json:"live,omitempty"`
}

type protocolApplyRequest struct {
	Digest string `json:"digest"`
}

type protocolCandidateSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Routes    int    `json:"routes"`
	Guides    int    `json:"guides"`
	Workflows int    `json:"workflows"`
	PoolMode  string `json:"pool_mode"`
}

func (registry *protocolRegistry) readProtocolCandidate(root string) (protocolCandidate, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return protocolCandidate{}, err
	}
	definitions := make(map[string]protocolDefinition)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(root, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return protocolCandidate{}, fmt.Errorf("read %s: %w", entry.Name(), err)
		}
		var definition protocolDefinition
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&definition); err != nil {
			return protocolCandidate{}, fmt.Errorf("decode %s: %w", entry.Name(), err)
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			return protocolCandidate{}, fmt.Errorf("decode %s: trailing JSON data is not allowed", entry.Name())
		}
		definition.SourceFile = path
		if err := registry.validate(&definition); err != nil {
			return protocolCandidate{}, fmt.Errorf("validate %s: %w", entry.Name(), err)
		}
		if _, exists := definitions[definition.ID]; exists {
			return protocolCandidate{}, fmt.Errorf("duplicate protocol id %q", definition.ID)
		}
		definitions[definition.ID] = definition
	}
	guides, err := registry.loadGuidesAt(root, definitions)
	if err != nil {
		return protocolCandidate{}, err
	}
	if err := validateProtocolPoolCatalog(root); err != nil {
		return protocolCandidate{}, fmt.Errorf("validate pool catalog: %w", err)
	}
	digest, err := protocolCandidateDigest(root, definitions)
	if err != nil {
		return protocolCandidate{}, err
	}
	return protocolCandidate{Definitions: definitions, Guides: guides, Digest: digest}, nil
}

func protocolCandidateDigest(root string, definitions map[string]protocolDefinition) (string, error) {
	files, err := protocolManagedFiles(root, definitions)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	for _, relative := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			return "", err
		}
		_, _ = hash.Write([]byte(relative))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(data)
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func protocolManagedFiles(root string, definitions map[string]protocolDefinition) ([]string, error) {
	files := make([]string, 0)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			files = append(files, filepath.ToSlash(entry.Name()))
		}
	}
	for protocolID := range definitions {
		for subdir, extension := range map[string]string{"guides": ".md", "modules": ".wasm"} {
			dir := filepath.Join(root, protocolID, subdir)
			entries, err := os.ReadDir(dir)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			for _, entry := range entries {
				if !entry.IsDir() && filepath.Ext(entry.Name()) == extension {
					files = append(files, filepath.ToSlash(filepath.Join(protocolID, subdir, entry.Name())))
				}
			}
		}
	}
	catalogDir := filepath.Join(root, "catalogs")
	catalogEntries, err := os.ReadDir(catalogDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, entry := range catalogEntries {
		if entry.IsDir() || (filepath.Ext(entry.Name()) != ".json" && filepath.Ext(entry.Name()) != ".md") {
			continue
		}
		files = append(files, filepath.ToSlash(filepath.Join("catalogs", entry.Name())))
	}
	sort.Strings(files)
	return files, nil
}

func (registry *protocolRegistry) revisionRoot() string {
	return filepath.Join(registry.stateDir, "protocol-revisions")
}

func (registry *protocolRegistry) captureProtocolRevision(sourceRoot, digest string) error {
	revisionDir := filepath.Join(registry.revisionRoot(), digest)
	if info, err := os.Stat(revisionDir); err == nil && info.IsDir() {
		return nil
	}
	candidate, err := registry.readProtocolCandidate(sourceRoot)
	if err != nil {
		return err
	}
	if candidate.Digest != digest {
		return errors.New("candidate changed while capturing revision")
	}
	if err := os.MkdirAll(registry.revisionRoot(), 0o700); err != nil {
		return err
	}
	if err := os.Chmod(registry.revisionRoot(), 0o700); err != nil {
		return err
	}
	temporary, err := os.MkdirTemp(registry.revisionRoot(), ".capture-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	if err := os.Chmod(temporary, 0o700); err != nil {
		return err
	}
	files, err := protocolManagedFiles(sourceRoot, candidate.Definitions)
	if err != nil {
		return err
	}
	for _, relative := range files {
		source := filepath.Join(sourceRoot, filepath.FromSlash(relative))
		target := filepath.Join(temporary, "files", filepath.FromSlash(relative))
		if err := copyProtocolFile(source, target, 0o600); err != nil {
			return err
		}
	}
	receipt := protocolRevisionInfo{Digest: digest, CreatedAt: time.Now().UTC()}
	receiptData, _ := json.MarshalIndent(receipt, "", "  ")
	if err := os.WriteFile(filepath.Join(temporary, "receipt.json"), receiptData, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, revisionDir)
}

func copyProtocolFile(source, target string, mode fs.FileMode) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("managed protocol file %s is not regular", source)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	return os.WriteFile(target, data, mode)
}

func summarizeProtocolCandidate(candidate protocolCandidate) []protocolCandidateSummary {
	ids := make([]string, 0, len(candidate.Definitions))
	for id := range candidate.Definitions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	summaries := make([]protocolCandidateSummary, 0, len(ids))
	for _, id := range ids {
		definition := candidate.Definitions[id]
		poolMode := "static"
		if definition.Pool != nil {
			poolMode = definition.Pool.Mode
		}
		summaries = append(summaries, protocolCandidateSummary{
			ID: id, Name: definition.Name, Routes: len(definition.Routes), Guides: len(candidate.Guides[id]),
			Workflows: len(definition.Workflows), PoolMode: poolMode,
		})
	}
	return summaries
}

func (registry *protocolRegistry) handleAdminValidate(c *gin.Context) {
	candidate, err := registry.readProtocolCandidate(registry.dir)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"digest": candidate.Digest, "protocols": summarizeProtocolCandidate(candidate)}})
}

func (registry *protocolRegistry) handleAdminPlan(c *gin.Context) {
	candidate, err := registry.readProtocolCandidate(registry.dir)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	registry.mu.RLock()
	liveDigest := registry.liveDigest
	registry.mu.RUnlock()
	ids := make([]string, 0, len(candidate.Definitions))
	for id := range candidate.Definitions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	plan := protocolPackPlan{Digest: candidate.Digest, LiveDigest: liveDigest, CreatedAt: time.Now().UTC(), Protocols: ids}
	registry.lifecycleMu.Lock()
	registry.pendingPlan = &plan
	registry.lifecycleMu.Unlock()
	c.JSON(http.StatusOK, gin.H{"success": true, "data": plan})
}

func (registry *protocolRegistry) handleAdminApply(c *gin.Context) {
	request, ok := decodeProtocolLifecycleRequest(c)
	if !ok {
		return
	}
	result, failure := registry.applyPlannedProtocolDigest(request.Digest)
	if failure != nil {
		status := http.StatusConflict
		if failure.Code == "candidate_invalid" || failure.Code == "invalid_digest" {
			status = http.StatusBadRequest
		} else if failure.Code == "revision_capture_failed" || failure.Code == "apply_failed" || failure.Code == "verification_failed" {
			status = http.StatusInternalServerError
		}
		c.JSON(status, gin.H{"success": false, "error": failure, "message": failure.Message})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

func (registry *protocolRegistry) applyPlannedProtocolDigest(digest string) (any, *maintenanceToolFailure) {
	if !validProtocolDigest(digest) {
		failure := maintenanceFailure(registry, "invalid_digest", "apply", "a valid planned digest is required", false, false)
		return nil, &failure
	}
	registry.lifecycleMu.Lock()
	defer registry.lifecycleMu.Unlock()
	if registry.pendingPlan == nil || registry.pendingPlan.Digest != digest {
		failure := maintenanceFailure(registry, "plan_not_found", "apply", "no matching reviewed plan", false, false)
		return nil, &failure
	}
	plan := *registry.pendingPlan
	if registry.currentDigest() != plan.LiveDigest {
		failure := maintenanceFailure(registry, "live_changed_after_plan", "apply", "live configuration changed after plan; review and plan again", false, plan.DraftID != "")
		return nil, &failure
	}
	sourceRoot := registry.dir
	if plan.DraftID != "" {
		var ok bool
		sourceRoot, ok = registry.protocolDraftRoot(plan.DraftID)
		if !ok {
			failure := maintenanceFailure(registry, "draft_missing", "apply", "planned draft no longer exists", false, false)
			return nil, &failure
		}
		baseDigest, _, err := registry.currentProtocolDraftBase(sourceRoot)
		if err != nil || baseDigest != plan.BaseDigest {
			failure := maintenanceFailure(registry, "draft_base_changed_after_plan", "apply", "draft base changed after plan; open a new draft from current live configuration", false, true)
			failure.BaseDigest = baseDigest
			failure.LiveDigest = registry.currentDigest()
			failure.NextAction = "Open a new draft from current live configuration and reapply only the intended changes."
			return nil, &failure
		}
	}
	candidate, err := registry.readProtocolCandidate(sourceRoot)
	if err != nil {
		failure := maintenanceFailure(registry, "candidate_invalid", "validation", err.Error(), false, plan.DraftID != "")
		return nil, &failure
	}
	if candidate.Digest != digest {
		failure := maintenanceFailure(registry, "candidate_changed_after_plan", "apply", "candidate changed after plan", false, plan.DraftID != "")
		return nil, &failure
	}
	previousDigest := plan.LiveDigest
	if err := registry.captureProtocolRevision(sourceRoot, candidate.Digest); err != nil {
		failure := maintenanceFailure(registry, "revision_capture_failed", "revision", "cannot capture protocol revision", true, plan.DraftID != "")
		return nil, &failure
	}
	if sourceRoot != registry.dir {
		if err := registry.installProtocolCandidate(sourceRoot, candidate.Digest); err != nil {
			failure := maintenanceFailure(registry, "apply_failed", "install", "cannot install planned draft", true, true)
			failure.Rollback = registry.tryAutomaticProtocolRollback(previousDigest)
			return nil, &failure
		}
	} else {
		registry.mu.Lock()
		registry.templates = candidate.Definitions
		registry.guides = candidate.Guides
		registry.liveDigest = candidate.Digest
		registry.mu.Unlock()
	}
	verifyErr := registry.verifyInstalledProtocolCandidate(candidate.Digest)
	if verifyErr != nil {
		message := "installed Pack tree did not pass local verification"
		message = "installed Pack tree failed local verification: " + verifyErr.Error()
		failure := maintenanceFailure(registry, "verification_failed", "verification", message, false, plan.DraftID != "")
		failure.Rollback = registry.tryAutomaticProtocolRollback(previousDigest)
		return nil, &failure
	}
	registry.readinessMu.Lock()
	registry.readinessCache = make(map[string]protocolReadinessRuntime)
	registry.readinessMu.Unlock()
	registry.pendingPlan = nil
	result := gin.H{
		"receipt": "PAP-" + time.Now().UTC().Format("20060102T150405Z") + "-" + candidate.Digest[:8],
		"digest":  candidate.Digest, "previous_digest": previousDigest, "verified": true,
		"protocols": registry.views(),
	}
	if plan.DraftID != "" {
		draftBaseUpdated := writeProtocolDraftBaseDigest(sourceRoot, candidate.Digest) == nil
		result["draft_base_updated"] = draftBaseUpdated
		if !draftBaseUpdated {
			result["next_action"] = "The release succeeded, but this draft cannot be reused safely; open a new draft from current live configuration."
		}
	}
	return result, nil
}

func (registry *protocolRegistry) verifyInstalledProtocolCandidate(digest string) error {
	if registry.verifyInstall != nil {
		return registry.verifyInstall(registry.dir, digest)
	}
	installed, err := registry.readProtocolCandidate(registry.dir)
	if err != nil {
		return err
	}
	if installed.Digest != digest {
		return errors.New("installed protocol digest mismatch")
	}
	return nil
}

func (registry *protocolRegistry) tryAutomaticProtocolRollback(digest string) *struct {
	Attempted bool   `json:"attempted"`
	Succeeded bool   `json:"succeeded"`
	Digest    string `json:"digest,omitempty"`
} {
	result := &struct {
		Attempted bool   `json:"attempted"`
		Succeeded bool   `json:"succeeded"`
		Digest    string `json:"digest,omitempty"`
	}{Attempted: validProtocolDigest(digest), Digest: digest}
	if !result.Attempted {
		return result
	}
	result.Succeeded = registry.restoreProtocolRevision(digest) == nil
	return result
}

func (registry *protocolRegistry) installProtocolCandidate(sourceRoot, digest string) error {
	parent := filepath.Dir(registry.dir)
	temporary, err := os.MkdirTemp(parent, ".protocol-apply-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	if err := copyProtocolTree(registry.dir, temporary); err != nil {
		return err
	}
	if err := clearManagedProtocolFiles(temporary); err != nil {
		return err
	}
	candidate, err := registry.readProtocolCandidate(sourceRoot)
	if err != nil || candidate.Digest != digest {
		return errors.New("draft changed during installation")
	}
	files, err := protocolManagedFiles(sourceRoot, candidate.Definitions)
	if err != nil {
		return err
	}
	for _, relative := range files {
		if err := copyProtocolFile(filepath.Join(sourceRoot, filepath.FromSlash(relative)), filepath.Join(temporary, filepath.FromSlash(relative)), 0o644); err != nil {
			return err
		}
	}
	installed, err := registry.readProtocolCandidate(temporary)
	if err != nil || installed.Digest != digest {
		return errors.New("installed draft digest mismatch")
	}
	backup := filepath.Join(parent, ".protocol-backup-"+time.Now().UTC().Format("20060102T150405.000000000"))
	if err := os.Rename(registry.dir, backup); err != nil {
		return err
	}
	if err := os.Rename(temporary, registry.dir); err != nil {
		_ = os.Rename(backup, registry.dir)
		return err
	}
	registry.mu.Lock()
	registry.templates = installed.Definitions
	registry.guides = installed.Guides
	registry.liveDigest = installed.Digest
	registry.mu.Unlock()
	// The new tree is already committed and verified here. Backup cleanup is
	// best effort so a harmless cleanup failure can never be reported as a
	// failed apply after the live state actually changed.
	_ = os.RemoveAll(backup)
	return nil
}

func decodeProtocolLifecycleRequest(c *gin.Context) (protocolApplyRequest, bool) {
	var request protocolApplyRequest
	decoder := json.NewDecoder(io.LimitReader(c.Request.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || !validProtocolDigest(request.Digest) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "a valid digest is required"})
		return protocolApplyRequest{}, false
	}
	return request, true
}

func validProtocolDigest(digest string) bool {
	if len(digest) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

func (registry *protocolRegistry) handleAdminHistory(c *gin.Context) {
	entries, err := os.ReadDir(registry.revisionRoot())
	if errors.Is(err, os.ErrNotExist) {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": []protocolRevisionInfo{}})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot read protocol history"})
		return
	}
	history := make([]protocolRevisionInfo, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !validProtocolDigest(entry.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(registry.revisionRoot(), entry.Name(), "receipt.json"))
		if err != nil {
			continue
		}
		var info protocolRevisionInfo
		if json.Unmarshal(data, &info) == nil && info.Digest == entry.Name() {
			history = append(history, info)
		}
	}
	sort.Slice(history, func(i, j int) bool { return history[i].CreatedAt.After(history[j].CreatedAt) })
	registry.mu.RLock()
	liveDigest := registry.liveDigest
	registry.mu.RUnlock()
	for index := range history {
		history[index].Live = history[index].Digest == liveDigest
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": history, "live_digest": liveDigest})
}

func (registry *protocolRegistry) handleAdminRollback(c *gin.Context) {
	request, ok := decodeProtocolLifecycleRequest(c)
	if !ok {
		return
	}
	registry.lifecycleMu.Lock()
	defer registry.lifecycleMu.Unlock()
	if err := registry.restoreProtocolRevision(request.Digest); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	registry.pendingPlan = nil
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"receipt": "PRB-" + time.Now().UTC().Format("20060102T150405Z") + "-" + request.Digest[:8],
		"digest":  request.Digest, "protocols": registry.views(),
	}})
}

func (registry *protocolRegistry) restoreProtocolRevision(digest string) error {
	revisionFiles := filepath.Join(registry.revisionRoot(), digest, "files")
	if info, err := os.Stat(revisionFiles); err != nil || !info.IsDir() {
		return errors.New("unknown protocol revision")
	}
	parent := filepath.Dir(registry.dir)
	temporary, err := os.MkdirTemp(parent, ".protocol-restore-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	if err := copyProtocolTree(registry.dir, temporary); err != nil {
		return err
	}
	if err := clearManagedProtocolFiles(temporary); err != nil {
		return err
	}
	if err := copyProtocolTree(revisionFiles, temporary); err != nil {
		return err
	}
	candidate, err := registry.readProtocolCandidate(temporary)
	if err != nil {
		return fmt.Errorf("revision validation failed: %w", err)
	}
	if candidate.Digest != digest {
		return errors.New("revision digest mismatch")
	}
	backup := filepath.Join(parent, ".protocol-backup-"+time.Now().UTC().Format("20060102T150405.000000000"))
	if err := os.Rename(registry.dir, backup); err != nil {
		return err
	}
	if err := os.Rename(temporary, registry.dir); err != nil {
		_ = os.Rename(backup, registry.dir)
		return err
	}
	restored, err := registry.readProtocolCandidate(registry.dir)
	if err != nil || restored.Digest != digest {
		_ = os.Rename(registry.dir, temporary)
		_ = os.Rename(backup, registry.dir)
		if err != nil {
			return err
		}
		return errors.New("restored protocol digest mismatch")
	}
	registry.mu.Lock()
	registry.templates = restored.Definitions
	registry.guides = restored.Guides
	registry.liveDigest = restored.Digest
	registry.mu.Unlock()
	_ = os.RemoveAll(backup)
	return nil
}

func copyProtocolTree(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil || relative == "." {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("protocol tree contains symlink %s", path)
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		return copyProtocolFile(path, destination, 0o644)
	})
}

func clearManagedProtocolFiles(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		if entry.IsDir() && entry.Name() == "catalogs" {
			if err := os.RemoveAll(path); err != nil {
				return err
			}
			continue
		}
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			if err := os.Remove(path); err != nil {
				return err
			}
			continue
		}
		if entry.IsDir() {
			guides, guideErr := os.Stat(filepath.Join(path, "guides"))
			modules, moduleErr := os.Stat(filepath.Join(path, "modules"))
			if (guideErr == nil && guides.IsDir()) || (moduleErr == nil && modules.IsDir()) {
				if err := os.RemoveAll(path); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
