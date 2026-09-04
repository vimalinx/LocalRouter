package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const maxProtocolDraftFileBytes = 2 << 20
const protocolDraftBaseDigestFile = ".localrouter-base-digest"

var (
	errProtocolDraftBaseUnknown = errors.New("draft base digest is missing or invalid")
	errProtocolDraftStale       = errors.New("draft base digest no longer matches live configuration")
)

type protocolDraftView struct {
	ID         string                     `json:"id"`
	UpdatedAt  time.Time                  `json:"updated_at"`
	Digest     string                     `json:"digest,omitempty"`
	BaseDigest string                     `json:"base_digest,omitempty"`
	LiveDigest string                     `json:"live_digest"`
	Stale      bool                       `json:"stale"`
	Valid      bool                       `json:"valid"`
	Error      string                     `json:"error,omitempty"`
	Files      []string                   `json:"files"`
	Protocols  []protocolCandidateSummary `json:"protocols,omitempty"`
	Impact     protocolDraftImpact        `json:"impact"`
}

type protocolDraftFileChange struct {
	Path       string `json:"path"`
	Change     string `json:"change"`
	Area       string `json:"area"`
	ProtocolID string `json:"protocol_id,omitempty"`
}

type protocolDraftProtocolImpact struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name,omitempty"`
	Change             string   `json:"change"`
	Sections           []string `json:"sections"`
	OperationsAdded    []string `json:"operations_added,omitempty"`
	OperationsModified []string `json:"operations_modified,omitempty"`
	OperationsRemoved  []string `json:"operations_removed,omitempty"`
	PoolModeBefore     string   `json:"pool_mode_before,omitempty"`
	PoolModeAfter      string   `json:"pool_mode_after,omitempty"`
}

type protocolDraftImpact struct {
	ChangedFiles int                           `json:"changed_files"`
	Files        []protocolDraftFileChange     `json:"files"`
	Protocols    []protocolDraftProtocolImpact `json:"protocols"`
	PoolIDs      []string                      `json:"pool_ids"`
}

func (registry *protocolRegistry) draftBase() string {
	return filepath.Join(registry.stateDir, "protocol-drafts")
}

func readProtocolDraftBaseDigest(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, protocolDraftBaseDigestFile))
	if err != nil {
		return "", errProtocolDraftBaseUnknown
	}
	digest := strings.TrimSpace(string(data))
	if !validProtocolDigest(digest) {
		return "", errProtocolDraftBaseUnknown
	}
	return digest, nil
}

func writeProtocolDraftBaseDigest(root, digest string) error {
	if !validProtocolDigest(digest) {
		return errProtocolDraftBaseUnknown
	}
	temporary, err := os.CreateTemp(root, ".localrouter-base-digest-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.WriteString(digest + "\n"); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, filepath.Join(root, protocolDraftBaseDigestFile))
}

func (registry *protocolRegistry) seedProtocolDraft(root string) error {
	baseDigest := registry.currentDigest()
	if err := copyProtocolDraftTree(registry.dir, root); err != nil {
		return err
	}
	return writeProtocolDraftBaseDigest(root, baseDigest)
}

func (registry *protocolRegistry) currentProtocolDraftBase(root string) (string, string, error) {
	liveDigest := registry.currentDigest()
	baseDigest, err := readProtocolDraftBaseDigest(root)
	if err != nil {
		return "", liveDigest, errProtocolDraftBaseUnknown
	}
	if baseDigest != liveDigest {
		return baseDigest, liveDigest, errProtocolDraftStale
	}
	return baseDigest, liveDigest, nil
}

func (registry *protocolRegistry) invalidateDraftPlanLocked(id string) {
	if registry.pendingPlan != nil && registry.pendingPlan.DraftID == id {
		registry.pendingPlan = nil
	}
}

func (registry *protocolRegistry) protocolDraftRoot(id string) (string, bool) {
	if !protocolIDPattern.MatchString(id) {
		return "", false
	}
	root := filepath.Join(registry.draftBase(), id)
	info, err := os.Stat(root)
	return root, err == nil && info.IsDir()
}

func copyProtocolDraftTree(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil || relative == "." {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("protocol source contains a symlink")
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o700)
		}
		return copyProtocolFile(path, destination, 0o600)
	})
}

func protocolDraftRelativePath(raw string, write bool) (string, bool) {
	relative := filepath.ToSlash(strings.TrimPrefix(raw, "/"))
	clean := filepath.ToSlash(filepath.Clean(relative))
	if relative == "" || clean != relative || strings.HasPrefix(clean, "../") || filepath.IsAbs(relative) {
		return "", false
	}
	parts := strings.Split(clean, "/")
	for _, part := range parts {
		lower := strings.ToLower(strings.TrimSuffix(part, filepath.Ext(part)))
		if part == "" || strings.HasPrefix(part, ".") {
			return "", false
		}
		switch lower {
		case "secret", "secrets", "token", "tokens", "api-token", "admin-token", "credential", "credentials":
			return "", false
		}
	}
	if !write && strings.HasPrefix(clean, "schema/") && strings.HasSuffix(clean, ".json") {
		return clean, true
	}
	if len(parts) == 1 && strings.HasSuffix(parts[0], ".json") && protocolIDPattern.MatchString(strings.TrimSuffix(parts[0], ".json")) {
		return clean, true
	}
	if len(parts) == 3 && protocolIDPattern.MatchString(parts[0]) && parts[1] == "guides" && strings.HasSuffix(parts[2], ".md") {
		return clean, true
	}
	if len(parts) == 3 && protocolIDPattern.MatchString(parts[0]) && parts[1] == "modules" && strings.HasSuffix(parts[2], ".wasm") {
		return clean, true
	}
	if len(parts) == 2 && parts[0] == "catalogs" && (strings.HasSuffix(parts[1], ".json") || strings.HasSuffix(parts[1], ".md")) {
		return clean, true
	}
	return "", false
}

func (registry *protocolRegistry) draftView(id, root string) protocolDraftView {
	view := protocolDraftView{ID: id, LiveDigest: registry.currentDigest(), Files: []string{}, Impact: registry.protocolDraftImpact(root)}
	if baseDigest, err := readProtocolDraftBaseDigest(root); err == nil {
		view.BaseDigest = baseDigest
		view.Stale = baseDigest != view.LiveDigest
	} else {
		view.Stale = true
	}
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err == nil {
			relative = filepath.ToSlash(relative)
			if _, allowed := protocolDraftRelativePath(relative, false); allowed {
				view.Files = append(view.Files, relative)
			}
		}
		if info, err := entry.Info(); err == nil && info.ModTime().After(view.UpdatedAt) {
			view.UpdatedAt = info.ModTime().UTC()
		}
		return nil
	})
	sort.Strings(view.Files)
	if candidate, err := registry.readProtocolCandidate(root); err == nil {
		view.Valid = true
		view.Digest = candidate.Digest
		view.Protocols = summarizeProtocolCandidate(candidate)
	} else {
		view.Error = err.Error()
	}
	return view
}

func protocolDraftFiles(root string) map[string][]byte {
	files := make(map[string][]byte)
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if _, ok := protocolDraftRelativePath(relative, false); !ok {
			return nil
		}
		if data, err := os.ReadFile(path); err == nil {
			files[relative] = data
		}
		return nil
	})
	return files
}

func protocolDraftFileArea(path string) (area, protocolID string) {
	parts := strings.Split(path, "/")
	if len(parts) == 1 && strings.HasSuffix(path, ".json") {
		return "definition", strings.TrimSuffix(parts[0], ".json")
	}
	if len(parts) >= 2 && protocolIDPattern.MatchString(parts[0]) {
		switch parts[1] {
		case "guides":
			return "guide", parts[0]
		case "modules":
			return "module", parts[0]
		}
	}
	if len(parts) > 0 && parts[0] == "catalogs" {
		return "catalog", ""
	}
	if len(parts) > 0 && parts[0] == "schema" {
		return "schema", ""
	}
	return "other", ""
}

func protocolDefinitionFromBytes(data []byte) *protocolDefinition {
	if len(data) == 0 {
		return nil
	}
	var definition protocolDefinition
	if json.Unmarshal(data, &definition) != nil || definition.ID == "" {
		return nil
	}
	return &definition
}

func protocolPoolMode(definition *protocolDefinition) string {
	if definition == nil {
		return ""
	}
	if definition.Pool == nil {
		return "static"
	}
	return definition.Pool.Mode
}

func protocolOperationIDs(definition *protocolDefinition) []string {
	if definition == nil {
		return nil
	}
	ids := make([]string, 0, len(definition.Routes))
	for _, route := range definition.Routes {
		if route.OperationID != "" {
			ids = append(ids, route.OperationID)
		}
	}
	sort.Strings(ids)
	return ids
}

func protocolOperationMap(definition *protocolDefinition) map[string]protocolRoute {
	routes := make(map[string]protocolRoute)
	if definition == nil {
		return routes
	}
	for _, route := range definition.Routes {
		key := route.OperationID
		if key == "" {
			key = strings.Join(route.Methods, ",") + " " + route.Path
		}
		routes[key] = route
	}
	return routes
}

func protocolModifiedOperations(before, after *protocolDefinition) []string {
	beforeRoutes := protocolOperationMap(before)
	afterRoutes := protocolOperationMap(after)
	modified := make([]string, 0)
	for id, beforeRoute := range beforeRoutes {
		if afterRoute, ok := afterRoutes[id]; ok && !protocolJSONEqual(beforeRoute, afterRoute) {
			modified = append(modified, id)
		}
	}
	sort.Strings(modified)
	return modified
}

func protocolStringDifference(left, right []string) []string {
	rightSet := make(map[string]struct{}, len(right))
	for _, value := range right {
		rightSet[value] = struct{}{}
	}
	result := make([]string, 0)
	for _, value := range left {
		if _, ok := rightSet[value]; !ok {
			result = append(result, value)
		}
	}
	return result
}

func protocolJSONEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	var leftValue, rightValue any
	if json.Unmarshal(leftJSON, &leftValue) != nil || json.Unmarshal(rightJSON, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func protocolDefinitionSections(before, after *protocolDefinition) []string {
	if before == nil || after == nil {
		return []string{"definition"}
	}
	sections := make([]string, 0)
	if !protocolJSONEqual(before.Routes, after.Routes) {
		sections = append(sections, "routes")
	}
	if !protocolJSONEqual(before.Pool, after.Pool) {
		sections = append(sections, "pool")
	}
	if !protocolJSONEqual(before.Workflows, after.Workflows) {
		sections = append(sections, "workflows")
	}
	if !protocolJSONEqual(before.Auth, after.Auth) {
		sections = append(sections, "auth")
	}
	if before.BaseURL != after.BaseURL || before.Timeout != after.Timeout || !protocolJSONEqual(before.Targets, after.Targets) || !protocolJSONEqual(before.UpstreamProfiles, after.UpstreamProfiles) {
		sections = append(sections, "upstream")
	}
	if before.Enabled != after.Enabled || !protocolJSONEqual(before.Readiness, after.Readiness) {
		sections = append(sections, "availability")
	}
	if before.Name != after.Name || before.Description != after.Description || !protocolJSONEqual(before.ForwardHeaders, after.ForwardHeaders) || !protocolJSONEqual(before.ResponseHeaders, after.ResponseHeaders) {
		sections = append(sections, "metadata")
	}
	if len(sections) == 0 {
		sections = append(sections, "definition")
	}
	return sections
}

func appendProtocolSection(sections []string, section string) []string {
	for _, current := range sections {
		if current == section {
			return sections
		}
	}
	return append(sections, section)
}

func protocolSectionsAffectRuntime(sections []string) bool {
	for _, section := range sections {
		if section != "guides" && section != "metadata" {
			return true
		}
	}
	return false
}

func (registry *protocolRegistry) protocolDraftImpact(root string) protocolDraftImpact {
	liveFiles := protocolDraftFiles(registry.dir)
	draftFiles := protocolDraftFiles(root)
	paths := make(map[string]struct{}, len(liveFiles)+len(draftFiles))
	for path := range liveFiles {
		paths[path] = struct{}{}
	}
	for path := range draftFiles {
		paths[path] = struct{}{}
	}
	orderedPaths := make([]string, 0, len(paths))
	for path := range paths {
		orderedPaths = append(orderedPaths, path)
	}
	sort.Strings(orderedPaths)

	impact := protocolDraftImpact{Files: []protocolDraftFileChange{}, Protocols: []protocolDraftProtocolImpact{}, PoolIDs: []string{}}
	protocols := make(map[string]*protocolDraftProtocolImpact)
	poolIDs := make(map[string]struct{})
	for _, path := range orderedPaths {
		before, beforeExists := liveFiles[path]
		after, afterExists := draftFiles[path]
		if beforeExists && afterExists && bytes.Equal(before, after) {
			continue
		}
		change := "modified"
		if !beforeExists {
			change = "added"
		} else if !afterExists {
			change = "removed"
		}
		area, protocolID := protocolDraftFileArea(path)
		impact.Files = append(impact.Files, protocolDraftFileChange{Path: path, Change: change, Area: area, ProtocolID: protocolID})
		if protocolID == "" {
			continue
		}
		entry := protocols[protocolID]
		if entry == nil {
			entry = &protocolDraftProtocolImpact{ID: protocolID, Change: "modified", Sections: []string{}}
			protocols[protocolID] = entry
		}
		switch area {
		case "guide":
			entry.Sections = appendProtocolSection(entry.Sections, "guides")
		case "module":
			entry.Sections = appendProtocolSection(entry.Sections, "adapter")
		case "definition":
			beforeDefinition := protocolDefinitionFromBytes(before)
			afterDefinition := protocolDefinitionFromBytes(after)
			if beforeDefinition == nil {
				entry.Change = "added"
			} else if afterDefinition == nil {
				entry.Change = "removed"
			}
			if afterDefinition != nil {
				entry.ID = afterDefinition.ID
				entry.Name = afterDefinition.Name
			} else if beforeDefinition != nil {
				entry.ID = beforeDefinition.ID
				entry.Name = beforeDefinition.Name
			}
			entry.Sections = protocolDefinitionSections(beforeDefinition, afterDefinition)
			beforeOperations := protocolOperationIDs(beforeDefinition)
			afterOperations := protocolOperationIDs(afterDefinition)
			entry.OperationsAdded = protocolStringDifference(afterOperations, beforeOperations)
			entry.OperationsModified = protocolModifiedOperations(beforeDefinition, afterDefinition)
			entry.OperationsRemoved = protocolStringDifference(beforeOperations, afterOperations)
			entry.PoolModeBefore = protocolPoolMode(beforeDefinition)
			entry.PoolModeAfter = protocolPoolMode(afterDefinition)
			if !protocolJSONEqual(func() any {
				if beforeDefinition == nil {
					return nil
				}
				return beforeDefinition.Pool
			}(), func() any {
				if afterDefinition == nil {
					return nil
				}
				return afterDefinition.Pool
			}()) {
				poolIDs[entry.ID] = struct{}{}
			}
		}
	}

	ids := make([]string, 0, len(protocols))
	for id := range protocols {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		entry := protocols[id]
		if entry.Name == "" {
			if definition := protocolDefinitionFromBytes(draftFiles[id+".json"]); definition != nil {
				entry.Name = definition.Name
			} else if definition := protocolDefinitionFromBytes(liveFiles[id+".json"]); definition != nil {
				entry.Name = definition.Name
			}
		}
		sort.Strings(entry.Sections)
		beforeDefinition := protocolDefinitionFromBytes(liveFiles[id+".json"])
		afterDefinition := protocolDefinitionFromBytes(draftFiles[id+".json"])
		if protocolSectionsAffectRuntime(entry.Sections) && (protocolPoolMode(beforeDefinition) != "static" || protocolPoolMode(afterDefinition) != "static") {
			poolIDs[id] = struct{}{}
		}
		impact.Protocols = append(impact.Protocols, *entry)
	}
	for id := range poolIDs {
		impact.PoolIDs = append(impact.PoolIDs, id)
	}
	sort.Strings(impact.PoolIDs)
	impact.ChangedFiles = len(impact.Files)
	return impact
}

func (registry *protocolRegistry) handleDraftList(c *gin.Context) {
	entries, err := os.ReadDir(registry.draftBase())
	if errors.Is(err, os.ErrNotExist) {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": []protocolDraftView{}})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot read drafts"})
		return
	}
	views := make([]protocolDraftView, 0)
	for _, entry := range entries {
		if entry.IsDir() && protocolIDPattern.MatchString(entry.Name()) {
			views = append(views, registry.draftView(entry.Name(), filepath.Join(registry.draftBase(), entry.Name())))
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": views})
}

func (registry *protocolRegistry) handleDraftCreate(c *gin.Context) {
	registry.lifecycleMu.Lock()
	defer registry.lifecycleMu.Unlock()
	var request struct {
		ID string `json:"id"`
	}
	decoder := json.NewDecoder(io.LimitReader(c.Request.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || !protocolIDPattern.MatchString(request.ID) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "draft id must match the protocol id format"})
		return
	}
	if err := os.MkdirAll(registry.draftBase(), 0o700); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot create draft storage"})
		return
	}
	root := filepath.Join(registry.draftBase(), request.ID)
	if err := os.Mkdir(root, 0o700); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, os.ErrExist) {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"success": false, "message": "draft already exists or cannot be created"})
		return
	}
	if err := registry.seedProtocolDraft(root); err != nil {
		_ = os.RemoveAll(root)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot seed draft"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": registry.draftView(request.ID, root)})
}

func (registry *protocolRegistry) handleDraftGet(c *gin.Context) {
	root, ok := registry.protocolDraftRoot(c.Param("id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "unknown draft"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": registry.draftView(c.Param("id"), root)})
}

func (registry *protocolRegistry) handleDraftDelete(c *gin.Context) {
	registry.lifecycleMu.Lock()
	defer registry.lifecycleMu.Unlock()
	root, ok := registry.protocolDraftRoot(c.Param("id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "unknown draft"})
		return
	}
	if os.RemoveAll(root) != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot delete draft"})
		return
	}
	if registry.pendingPlan != nil && registry.pendingPlan.DraftID == c.Param("id") {
		registry.pendingPlan = nil
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (registry *protocolRegistry) handleDraftFileGet(c *gin.Context) {
	root, ok := registry.protocolDraftRoot(c.Param("id"))
	if !ok {
		c.Status(http.StatusNotFound)
		return
	}
	relative, ok := protocolDraftRelativePath(c.Param("path"), false)
	if !ok {
		c.Status(http.StatusBadRequest)
		return
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	contentType := "application/octet-stream"
	if strings.HasSuffix(relative, ".json") {
		contentType = "application/json"
	}
	if strings.HasSuffix(relative, ".md") {
		contentType = "text/markdown; charset=utf-8"
	}
	c.Data(http.StatusOK, contentType, data)
}

func (registry *protocolRegistry) handleDraftFilePut(c *gin.Context) {
	registry.lifecycleMu.Lock()
	defer registry.lifecycleMu.Unlock()
	root, ok := registry.protocolDraftRoot(c.Param("id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "unknown draft"})
		return
	}
	relative, ok := protocolDraftRelativePath(c.Param("path"), true)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "file path is not writable"})
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxProtocolDraftFileBytes))
	if err != nil {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"success": false, "message": "draft file is too large"})
		return
	}
	target := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	temporary := target + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil || os.Rename(temporary, target) != nil {
		_ = os.Remove(temporary)
		c.Status(http.StatusInternalServerError)
		return
	}
	registry.invalidateDraftPlanLocked(c.Param("id"))
	c.JSON(http.StatusOK, gin.H{"success": true, "data": registry.draftView(c.Param("id"), root)})
}

func (registry *protocolRegistry) handleDraftFileDelete(c *gin.Context) {
	registry.lifecycleMu.Lock()
	defer registry.lifecycleMu.Unlock()
	root, ok := registry.protocolDraftRoot(c.Param("id"))
	if !ok {
		c.Status(http.StatusNotFound)
		return
	}
	relative, ok := protocolDraftRelativePath(c.Param("path"), true)
	if !ok {
		c.Status(http.StatusBadRequest)
		return
	}
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(relative))); err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	registry.invalidateDraftPlanLocked(c.Param("id"))
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (registry *protocolRegistry) handleDraftValidate(c *gin.Context) {
	registry.lifecycleMu.Lock()
	defer registry.lifecycleMu.Unlock()
	root, ok := registry.protocolDraftRoot(c.Param("id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "unknown draft"})
		return
	}
	baseDigest, liveDigest, baseErr := registry.currentProtocolDraftBase(root)
	if baseErr != nil {
		code := "draft_base_unknown"
		message := "draft predates version-bound drafts and cannot be safely released"
		if errors.Is(baseErr, errProtocolDraftStale) {
			code = "stale_draft"
			message = "live configuration changed after this draft was opened"
		}
		c.JSON(http.StatusConflict, gin.H{"success": false, "code": code, "message": message, "base_digest": baseDigest, "live_digest": liveDigest, "next_action": "Open a new draft from current live configuration and reapply only the intended changes."})
		return
	}
	candidate, err := registry.readProtocolCandidate(root)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"draft_id": c.Param("id"), "digest": candidate.Digest, "base_digest": baseDigest, "protocols": summarizeProtocolCandidate(candidate)}})
}

func (registry *protocolRegistry) handleDraftPlan(c *gin.Context) {
	registry.lifecycleMu.Lock()
	defer registry.lifecycleMu.Unlock()
	root, ok := registry.protocolDraftRoot(c.Param("id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "unknown draft"})
		return
	}
	baseDigest, liveDigest, baseErr := registry.currentProtocolDraftBase(root)
	if baseErr != nil {
		code := "draft_base_unknown"
		message := "draft predates version-bound drafts and cannot be safely released"
		if errors.Is(baseErr, errProtocolDraftStale) {
			code = "stale_draft"
			message = "live configuration changed after this draft was opened"
		}
		c.JSON(http.StatusConflict, gin.H{"success": false, "code": code, "message": message, "base_digest": baseDigest, "live_digest": liveDigest, "next_action": "Open a new draft from current live configuration and reapply only the intended changes."})
		return
	}
	candidate, err := registry.readProtocolCandidate(root)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	ids := make([]string, 0, len(candidate.Definitions))
	for id := range candidate.Definitions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	files, _ := protocolManagedFiles(root, candidate.Definitions)
	plan := protocolPackPlan{Digest: candidate.Digest, BaseDigest: baseDigest, LiveDigest: liveDigest, CreatedAt: time.Now().UTC(), Protocols: ids, DraftID: c.Param("id"), Files: files}
	registry.pendingPlan = &plan
	c.JSON(http.StatusOK, gin.H{"success": true, "data": plan})
}
