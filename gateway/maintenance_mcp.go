package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

type maintenanceToolDefinition struct {
	Name        string
	Description string
	InputSchema gin.H
	ReadOnly    bool
	Destructive bool
}

type maintenanceToolFailure struct {
	Code           string                       `json:"code"`
	Stage          string                       `json:"stage"`
	Message        string                       `json:"message"`
	NextAction     string                       `json:"next_action,omitempty"`
	Retryable      bool                         `json:"retryable"`
	DraftPreserved bool                         `json:"draft_preserved,omitempty"`
	BaseDigest     string                       `json:"base_digest,omitempty"`
	LiveDigest     string                       `json:"live_digest,omitempty"`
	Issues         []maintenanceValidationIssue `json:"issues,omitempty"`
	Rollback       *struct {
		Attempted bool   `json:"attempted"`
		Succeeded bool   `json:"succeeded"`
		Digest    string `json:"digest,omitempty"`
	} `json:"rollback,omitempty"`
}

type maintenanceGuideInput struct {
	DraftID      string   `json:"draft_id"`
	PackID       string   `json:"pack_id"`
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Summary      string   `json:"summary"`
	Status       string   `json:"status"`
	Operations   []string `json:"operations"`
	LastVerified string   `json:"last_verified,omitempty"`
	Visibility   string   `json:"visibility,omitempty"`
	Markdown     string   `json:"markdown"`
}

func maintenanceObjectSchema(properties gin.H, required ...string) gin.H {
	schema := gin.H{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func maintenanceString(description string) gin.H {
	return gin.H{"type": "string", "description": description}
}

func maintenanceTools() []maintenanceToolDefinition {
	return []maintenanceToolDefinition{
		{Name: "localrouter_status", Description: "Inspect live Pack, pool and draft status without revealing secrets or private locators.", InputSchema: maintenanceObjectSchema(gin.H{}), ReadOnly: true},
		{Name: "localrouter_draft_open", Description: "Open an idempotent draft seeded from and version-bound to the live configuration. LocalRouter owns paths and file layout.", InputSchema: maintenanceObjectSchema(gin.H{"draft_id": maintenanceString("Stable lowercase draft id")}, "draft_id")},
		{Name: "localrouter_draft_get_pack", Description: "Read the editable semantic content of one Pack in a draft. Protected operational locators are omitted and preserved by patches.", InputSchema: maintenanceObjectSchema(gin.H{"draft_id": maintenanceString("Draft id"), "pack_id": maintenanceString("Pack id")}, "draft_id", "pack_id"), ReadOnly: true},
		{Name: "localrouter_draft_put_pack", Description: "Create or update a canonical v3 Pack core. Omitted authentication defaults explicitly to none; existing operations, profiles, workflows and pricing are preserved.", InputSchema: maintenancePutPackSchema()},
		{Name: "localrouter_draft_put_operation", Description: "Create or replace one operation by operation_id. LocalRouter supplies safe transport, retry and draft-availability defaults and validates the complete Pack before writing.", InputSchema: maintenancePutOperationSchema()},
		{Name: "localrouter_draft_put_upstream_profile", Description: "Create or replace one reusable supplier request profile for headers, User-Agent and query handling. Protected authentication headers cannot be configured here.", InputSchema: maintenancePutUpstreamProfileSchema()},
		{Name: "localrouter_draft_lint_pack", Description: "Validate one draft Pack and return a structured issue with path, allowed values and next repair action without changing the draft.", InputSchema: maintenanceObjectSchema(gin.H{"draft_id": maintenanceString("Draft id"), "pack_id": maintenanceString("Pack id")}, "draft_id", "pack_id"), ReadOnly: true},
		{Name: "localrouter_draft_patch_pack", Description: "Apply an RFC 7396 style semantic merge patch to a Pack. Null removes a field; formatting and atomic file replacement are handled by LocalRouter.", InputSchema: maintenanceObjectSchema(gin.H{"draft_id": maintenanceString("Draft id"), "pack_id": maintenanceString("Pack id"), "patch": gin.H{"type": "object", "description": "Pack content changes", "additionalProperties": true}}, "draft_id", "pack_id", "patch")},
		{Name: "localrouter_draft_put_guide", Description: "Create or replace an Agent guide from metadata and Markdown body. LocalRouter generates and validates front matter.", InputSchema: maintenanceObjectSchema(gin.H{
			"draft_id": maintenanceString("Draft id"), "pack_id": maintenanceString("Pack id"), "id": maintenanceString("Guide id"),
			"title": maintenanceString("Guide title"), "summary": maintenanceString("One sentence purpose"),
			"status":     gin.H{"type": "string", "enum": []string{"draft", "observed", "verified", "deprecated"}},
			"operations": gin.H{"type": "array", "items": gin.H{"type": "string"}}, "last_verified": maintenanceString("YYYY-MM-DD when status is verified"),
			"visibility": gin.H{"type": "string", "enum": []string{"local", "public"}}, "markdown": maintenanceString("Guide body without YAML front matter"),
		}, "draft_id", "pack_id", "id", "title", "summary", "status", "operations", "markdown")},
		{Name: "localrouter_draft_delete_guide", Description: "Remove one authored Agent guide from a draft without constructing its file path.", InputSchema: maintenanceObjectSchema(gin.H{"draft_id": maintenanceString("Draft id"), "pack_id": maintenanceString("Pack id"), "guide_id": maintenanceString("Guide id")}, "draft_id", "pack_id", "guide_id"), Destructive: true},
		{Name: "localrouter_draft_delete_pack", Description: "Remove a Pack definition and its authored guides from a draft.", InputSchema: maintenanceObjectSchema(gin.H{"draft_id": maintenanceString("Draft id"), "pack_id": maintenanceString("Pack id")}, "draft_id", "pack_id"), Destructive: true},
		{Name: "localrouter_draft_review", Description: "Validate a non-stale draft and return exact file, protocol section and pool impact plus the immutable digest to review.", InputSchema: maintenanceObjectSchema(gin.H{"draft_id": maintenanceString("Draft id")}, "draft_id"), ReadOnly: true},
		{Name: "localrouter_draft_plan", Description: "Create a release plan only when the supplied reviewed digest still exactly matches the draft and its base still matches live.", InputSchema: maintenanceObjectSchema(gin.H{"draft_id": maintenanceString("Draft id"), "reviewed_digest": maintenanceString("Digest returned by localrouter_draft_review")}, "draft_id", "reviewed_digest")},
		{Name: "localrouter_draft_apply", Description: "Apply the exact planned digest, verify the installed live tree, and automatically restore the previous revision if local verification fails.", InputSchema: maintenanceObjectSchema(gin.H{"digest": maintenanceString("Exact digest returned by localrouter_draft_plan")}, "digest"), Destructive: true},
		{Name: "localrouter_draft_abort", Description: "Discard a draft without changing the live configuration.", InputSchema: maintenanceObjectSchema(gin.H{"draft_id": maintenanceString("Draft id")}, "draft_id"), Destructive: true},
		{Name: "localrouter_history", Description: "List immutable Pack revisions and identify the live digest.", InputSchema: maintenanceObjectSchema(gin.H{}), ReadOnly: true},
		{Name: "localrouter_rollback", Description: "Restore an immutable prior revision and locally verify its digest.", InputSchema: maintenanceObjectSchema(gin.H{"digest": maintenanceString("Revision digest from localrouter_history")}, "digest"), Destructive: true},
	}
}

func (registry *protocolRegistry) handleMaintenanceMCP() gin.HandlerFunc {
	return func(c *gin.Context) {
		var request mcpRequest
		decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || request.JSONRPC != "2.0" || request.Method == "" {
			mcpError(c, request.ID, -32600, "invalid JSON-RPC request")
			return
		}
		c.Header("MCP-Protocol-Version", mcpProtocolVersion)
		switch request.Method {
		case "initialize":
			mcpResult(c, request.ID, gin.H{"protocolVersion": mcpProtocolVersion, "capabilities": gin.H{"tools": gin.H{"listChanged": false}}, "serverInfo": gin.H{"name": "LocalRouter Maintenance", "version": "1"}})
		case "notifications/initialized":
			c.Status(http.StatusAccepted)
		case "ping":
			mcpResult(c, request.ID, gin.H{})
		case "tools/list":
			items := make([]gin.H, 0, len(maintenanceTools()))
			for _, tool := range maintenanceTools() {
				items = append(items, gin.H{"name": tool.Name, "description": tool.Description, "inputSchema": tool.InputSchema, "annotations": gin.H{"readOnlyHint": tool.ReadOnly, "destructiveHint": tool.Destructive}})
			}
			mcpResult(c, request.ID, gin.H{"tools": items})
		case "tools/call":
			registry.handleMaintenanceToolCall(c, request)
		default:
			mcpError(c, request.ID, -32601, "method not found")
		}
	}
}

func maintenanceResult(c *gin.Context, id json.RawMessage, data any) {
	payload := gin.H{"success": true, "data": data}
	encoded, _ := json.Marshal(payload)
	mcpResult(c, id, gin.H{"content": []gin.H{{"type": "text", "text": string(encoded)}}, "structuredContent": payload, "isError": false})
}

func maintenanceFailureResult(c *gin.Context, id json.RawMessage, failure maintenanceToolFailure) {
	payload := gin.H{"success": false, "error": failure}
	encoded, _ := json.Marshal(payload)
	mcpResult(c, id, gin.H{"content": []gin.H{{"type": "text", "text": string(encoded)}}, "structuredContent": payload, "isError": true})
}

func (registry *protocolRegistry) currentDigest() string {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	return registry.liveDigest
}

func maintenanceFailure(registry *protocolRegistry, code, stage, message string, retryable, draftPreserved bool) maintenanceToolFailure {
	return maintenanceToolFailure{Code: code, Stage: stage, Message: message, Retryable: retryable, DraftPreserved: draftPreserved, LiveDigest: registry.currentDigest()}
}

func decodeMaintenanceArguments(raw json.RawMessage, destination any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("arguments must contain one JSON object")
	}
	return nil
}

func (registry *protocolRegistry) handleMaintenanceToolCall(c *gin.Context, request mcpRequest) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(request.Params, &params); err != nil || params.Name == "" {
		mcpError(c, request.ID, -32602, "tool name is required")
		return
	}
	known := false
	for _, tool := range maintenanceTools() {
		if tool.Name == params.Name {
			known = true
			break
		}
	}
	if !known {
		mcpError(c, request.ID, -32602, "unknown maintenance tool")
		return
	}

	switch params.Name {
	case "localrouter_status":
		var args struct{}
		if err := decodeMaintenanceArguments(params.Arguments, &args); err != nil {
			maintenanceFailureResult(c, request.ID, maintenanceFailure(registry, "invalid_arguments", "input", err.Error(), false, false))
			return
		}
		drafts, _ := registry.maintenanceDrafts()
		maintenanceResult(c, request.ID, gin.H{"live_digest": registry.currentDigest(), "protocols": registry.views(), "drafts": drafts, "resource_guards": []string{"pool concurrency", "lease expiry", "cooldown", "health", "quota eligibility"}})
	case "localrouter_draft_open":
		var args struct {
			DraftID string `json:"draft_id"`
		}
		if err := decodeMaintenanceArguments(params.Arguments, &args); err != nil {
			maintenanceFailureResult(c, request.ID, maintenanceFailure(registry, "invalid_arguments", "input", err.Error(), false, false))
			return
		}
		view, failure := registry.maintenanceOpenDraft(args.DraftID)
		if failure != nil {
			maintenanceFailureResult(c, request.ID, *failure)
			return
		}
		maintenanceResult(c, request.ID, view)
	case "localrouter_draft_get_pack":
		var args struct {
			DraftID string `json:"draft_id"`
			PackID  string `json:"pack_id"`
		}
		if err := decodeMaintenanceArguments(params.Arguments, &args); err != nil {
			maintenanceFailureResult(c, request.ID, maintenanceFailure(registry, "invalid_arguments", "input", err.Error(), false, false))
			return
		}
		pack, failure := registry.maintenanceGetPack(args.DraftID, args.PackID)
		if failure != nil {
			maintenanceFailureResult(c, request.ID, *failure)
			return
		}
		maintenanceResult(c, request.ID, pack)
	case "localrouter_draft_patch_pack":
		var args struct {
			DraftID string         `json:"draft_id"`
			PackID  string         `json:"pack_id"`
			Patch   map[string]any `json:"patch"`
		}
		if err := decodeMaintenanceArguments(params.Arguments, &args); err != nil {
			maintenanceFailureResult(c, request.ID, maintenanceFailure(registry, "invalid_arguments", "input", err.Error(), false, true))
			return
		}
		view, failure := registry.maintenancePatchPack(args.DraftID, args.PackID, args.Patch)
		if failure != nil {
			maintenanceFailureResult(c, request.ID, *failure)
			return
		}
		maintenanceResult(c, request.ID, view)
	case "localrouter_draft_put_pack":
		var args maintenancePackInput
		if err := decodeMaintenanceArguments(params.Arguments, &args); err != nil {
			maintenanceFailureResult(c, request.ID, maintenanceFailure(registry, "invalid_arguments", "input", err.Error(), false, true))
			return
		}
		result, failure := registry.maintenancePutPack(args)
		if failure != nil {
			maintenanceFailureResult(c, request.ID, *failure)
			return
		}
		maintenanceResult(c, request.ID, result)
	case "localrouter_draft_put_operation":
		var args maintenanceOperationInput
		if err := decodeMaintenanceArguments(params.Arguments, &args); err != nil {
			maintenanceFailureResult(c, request.ID, maintenanceFailure(registry, "invalid_arguments", "input", err.Error(), false, true))
			return
		}
		result, failure := registry.maintenancePutOperation(args)
		if failure != nil {
			maintenanceFailureResult(c, request.ID, *failure)
			return
		}
		maintenanceResult(c, request.ID, result)
	case "localrouter_draft_put_upstream_profile":
		var args maintenanceUpstreamProfileInput
		if err := decodeMaintenanceArguments(params.Arguments, &args); err != nil {
			maintenanceFailureResult(c, request.ID, maintenanceFailure(registry, "invalid_arguments", "input", err.Error(), false, true))
			return
		}
		result, failure := registry.maintenancePutUpstreamProfile(args)
		if failure != nil {
			maintenanceFailureResult(c, request.ID, *failure)
			return
		}
		maintenanceResult(c, request.ID, result)
	case "localrouter_draft_lint_pack":
		var args struct {
			DraftID string `json:"draft_id"`
			PackID  string `json:"pack_id"`
		}
		if err := decodeMaintenanceArguments(params.Arguments, &args); err != nil {
			maintenanceFailureResult(c, request.ID, maintenanceFailure(registry, "invalid_arguments", "input", err.Error(), false, true))
			return
		}
		result, failure := registry.maintenanceLintPack(args.DraftID, args.PackID)
		if failure != nil {
			maintenanceFailureResult(c, request.ID, *failure)
			return
		}
		maintenanceResult(c, request.ID, result)
	case "localrouter_draft_put_guide":
		var args maintenanceGuideInput
		if err := decodeMaintenanceArguments(params.Arguments, &args); err != nil {
			maintenanceFailureResult(c, request.ID, maintenanceFailure(registry, "invalid_arguments", "input", err.Error(), false, true))
			return
		}
		view, failure := registry.maintenancePutGuide(args)
		if failure != nil {
			maintenanceFailureResult(c, request.ID, *failure)
			return
		}
		maintenanceResult(c, request.ID, view)
	case "localrouter_draft_delete_guide":
		var args struct {
			DraftID string `json:"draft_id"`
			PackID  string `json:"pack_id"`
			GuideID string `json:"guide_id"`
		}
		if err := decodeMaintenanceArguments(params.Arguments, &args); err != nil {
			maintenanceFailureResult(c, request.ID, maintenanceFailure(registry, "invalid_arguments", "input", err.Error(), false, true))
			return
		}
		view, failure := registry.maintenanceDeleteGuide(args.DraftID, args.PackID, args.GuideID)
		if failure != nil {
			maintenanceFailureResult(c, request.ID, *failure)
			return
		}
		maintenanceResult(c, request.ID, view)
	case "localrouter_draft_delete_pack":
		var args struct {
			DraftID string `json:"draft_id"`
			PackID  string `json:"pack_id"`
		}
		if err := decodeMaintenanceArguments(params.Arguments, &args); err != nil {
			maintenanceFailureResult(c, request.ID, maintenanceFailure(registry, "invalid_arguments", "input", err.Error(), false, true))
			return
		}
		view, failure := registry.maintenanceDeletePack(args.DraftID, args.PackID)
		if failure != nil {
			maintenanceFailureResult(c, request.ID, *failure)
			return
		}
		maintenanceResult(c, request.ID, view)
	case "localrouter_draft_review":
		var args struct {
			DraftID string `json:"draft_id"`
		}
		if err := decodeMaintenanceArguments(params.Arguments, &args); err != nil {
			maintenanceFailureResult(c, request.ID, maintenanceFailure(registry, "invalid_arguments", "input", err.Error(), false, true))
			return
		}
		result, failure := registry.maintenanceReviewDraft(args.DraftID)
		if failure != nil {
			maintenanceFailureResult(c, request.ID, *failure)
			return
		}
		maintenanceResult(c, request.ID, result)
	case "localrouter_draft_plan":
		var args struct {
			DraftID        string `json:"draft_id"`
			ReviewedDigest string `json:"reviewed_digest"`
		}
		if err := decodeMaintenanceArguments(params.Arguments, &args); err != nil {
			maintenanceFailureResult(c, request.ID, maintenanceFailure(registry, "invalid_arguments", "input", err.Error(), false, true))
			return
		}
		plan, failure := registry.maintenancePlanDraft(args.DraftID, args.ReviewedDigest)
		if failure != nil {
			maintenanceFailureResult(c, request.ID, *failure)
			return
		}
		maintenanceResult(c, request.ID, plan)
	case "localrouter_draft_apply":
		var args struct {
			Digest string `json:"digest"`
		}
		if err := decodeMaintenanceArguments(params.Arguments, &args); err != nil {
			maintenanceFailureResult(c, request.ID, maintenanceFailure(registry, "invalid_arguments", "input", err.Error(), false, true))
			return
		}
		result, failure := registry.applyPlannedProtocolDigest(args.Digest)
		if failure != nil {
			maintenanceFailureResult(c, request.ID, *failure)
			return
		}
		maintenanceResult(c, request.ID, result)
	case "localrouter_draft_abort":
		var args struct {
			DraftID string `json:"draft_id"`
		}
		if err := decodeMaintenanceArguments(params.Arguments, &args); err != nil {
			maintenanceFailureResult(c, request.ID, maintenanceFailure(registry, "invalid_arguments", "input", err.Error(), false, true))
			return
		}
		if failure := registry.maintenanceAbortDraft(args.DraftID); failure != nil {
			maintenanceFailureResult(c, request.ID, *failure)
			return
		}
		maintenanceResult(c, request.ID, gin.H{"draft_id": args.DraftID, "discarded": true, "live_digest": registry.currentDigest()})
	case "localrouter_history":
		history, failure := registry.maintenanceHistory()
		if failure != nil {
			maintenanceFailureResult(c, request.ID, *failure)
			return
		}
		maintenanceResult(c, request.ID, gin.H{"live_digest": registry.currentDigest(), "revisions": history})
	case "localrouter_rollback":
		var args struct {
			Digest string `json:"digest"`
		}
		if err := decodeMaintenanceArguments(params.Arguments, &args); err != nil {
			maintenanceFailureResult(c, request.ID, maintenanceFailure(registry, "invalid_arguments", "input", err.Error(), false, false))
			return
		}
		result, failure := registry.maintenanceRollback(args.Digest)
		if failure != nil {
			maintenanceFailureResult(c, request.ID, *failure)
			return
		}
		maintenanceResult(c, request.ID, result)
	}
}

func (registry *protocolRegistry) maintenanceDrafts() ([]protocolDraftView, error) {
	entries, err := os.ReadDir(registry.draftBase())
	if errors.Is(err, os.ErrNotExist) {
		return []protocolDraftView{}, nil
	}
	if err != nil {
		return nil, err
	}
	views := make([]protocolDraftView, 0)
	for _, entry := range entries {
		if entry.IsDir() && protocolIDPattern.MatchString(entry.Name()) {
			views = append(views, registry.draftView(entry.Name(), filepath.Join(registry.draftBase(), entry.Name())))
		}
	}
	sort.Slice(views, func(i, j int) bool { return views[i].UpdatedAt.After(views[j].UpdatedAt) })
	return views, nil
}

func (registry *protocolRegistry) maintenanceOpenDraft(id string) (protocolDraftView, *maintenanceToolFailure) {
	if !protocolIDPattern.MatchString(id) {
		failure := maintenanceFailure(registry, "invalid_draft_id", "input", "draft_id must match the safe Pack id format", false, false)
		return protocolDraftView{}, &failure
	}
	registry.lifecycleMu.Lock()
	defer registry.lifecycleMu.Unlock()
	if root, ok := registry.protocolDraftRoot(id); ok {
		view := registry.draftView(id, root)
		return view, nil
	}
	if err := os.MkdirAll(registry.draftBase(), 0o700); err != nil {
		failure := maintenanceFailure(registry, "draft_storage_failed", "draft", "cannot create draft storage", true, false)
		return protocolDraftView{}, &failure
	}
	root := filepath.Join(registry.draftBase(), id)
	if err := os.Mkdir(root, 0o700); err != nil {
		failure := maintenanceFailure(registry, "draft_create_failed", "draft", "cannot create draft", true, false)
		return protocolDraftView{}, &failure
	}
	if err := registry.seedProtocolDraft(root); err != nil {
		_ = os.RemoveAll(root)
		failure := maintenanceFailure(registry, "draft_seed_failed", "draft", "cannot seed draft from live configuration", true, false)
		return protocolDraftView{}, &failure
	}
	view := registry.draftView(id, root)
	return view, nil
}

func (registry *protocolRegistry) maintenanceDraftBaseFailure(root, stage string) *maintenanceToolFailure {
	baseDigest, liveDigest, err := registry.currentProtocolDraftBase(root)
	if err == nil {
		return nil
	}
	code := "draft_base_unknown"
	message := "draft predates version-bound drafts and cannot be safely released"
	if errors.Is(err, errProtocolDraftStale) {
		code = "stale_draft"
		message = "live configuration changed after this draft was opened"
	}
	failure := maintenanceFailure(registry, code, stage, message, false, true)
	failure.BaseDigest = baseDigest
	failure.LiveDigest = liveDigest
	failure.NextAction = "Open a new draft from current live configuration and reapply only the intended changes."
	return &failure
}

func maintenanceProtectedKey(key string) bool {
	switch strings.ToLower(key) {
	case "base_url", "targets", "secret_file", "credentials_file", "locator_file", "client_secret_file", "token_url":
		return true
	default:
		return false
	}
}

func sanitizeMaintenanceValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		clean := make(map[string]any, len(typed))
		for key, item := range typed {
			if maintenanceProtectedKey(key) {
				continue
			}
			clean[key] = sanitizeMaintenanceValue(item)
		}
		return clean
	case []any:
		clean := make([]any, len(typed))
		for index, item := range typed {
			clean[index] = sanitizeMaintenanceValue(item)
		}
		return clean
	default:
		return value
	}
}

func (registry *protocolRegistry) maintenanceGetPack(draftID, packID string) (any, *maintenanceToolFailure) {
	root, ok := registry.protocolDraftRoot(draftID)
	if !ok || !protocolIDPattern.MatchString(packID) {
		failure := maintenanceFailure(registry, "unknown_draft_or_pack", "read", "unknown draft or invalid pack_id", false, true)
		return nil, &failure
	}
	data, err := os.ReadFile(filepath.Join(root, packID+".json"))
	if err != nil {
		failure := maintenanceFailure(registry, "pack_not_found", "read", "Pack does not exist in this draft", false, true)
		return nil, &failure
	}
	var pack map[string]any
	if json.Unmarshal(data, &pack) != nil {
		failure := maintenanceFailure(registry, "pack_invalid_json", "read", "Pack JSON is invalid; use a semantic patch to repair it", false, true)
		return nil, &failure
	}
	return gin.H{"draft_id": draftID, "pack_id": packID, "pack": sanitizeMaintenanceValue(pack), "protected_fields": []string{"base_url", "targets", "auth.secret_file", "auth.oauth2.token_url", "auth.oauth2.client_secret_file", "pool.credentials_file", "pool.source.locator_file"}}, nil
}

func mergeMaintenancePatch(target map[string]any, patch map[string]any) map[string]any {
	if target == nil {
		target = make(map[string]any)
	}
	for key, value := range patch {
		if value == nil {
			delete(target, key)
			continue
		}
		patchObject, patchIsObject := value.(map[string]any)
		if !patchIsObject {
			target[key] = value
			continue
		}
		currentObject, _ := target[key].(map[string]any)
		target[key] = mergeMaintenancePatch(currentObject, patchObject)
	}
	return target
}

func writeMaintenanceFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".maintenance-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
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

func (registry *protocolRegistry) maintenancePatchPack(draftID, packID string, patch map[string]any) (protocolDraftView, *maintenanceToolFailure) {
	if !protocolIDPattern.MatchString(packID) || patch == nil {
		failure := maintenanceFailure(registry, "invalid_pack_patch", "input", "pack_id and patch object are required", false, true)
		return protocolDraftView{}, &failure
	}
	registry.lifecycleMu.Lock()
	defer registry.lifecycleMu.Unlock()
	root, ok := registry.protocolDraftRoot(draftID)
	if !ok {
		failure := maintenanceFailure(registry, "unknown_draft", "draft", "unknown draft", false, false)
		return protocolDraftView{}, &failure
	}
	path := filepath.Join(root, packID+".json")
	pack := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(data, &pack) != nil {
			failure := maintenanceFailure(registry, "pack_invalid_json", "edit", "existing Pack JSON is invalid", false, true)
			return protocolDraftView{}, &failure
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		failure := maintenanceFailure(registry, "pack_read_failed", "edit", "cannot read Pack", true, true)
		return protocolDraftView{}, &failure
	}
	pack = mergeMaintenancePatch(pack, patch)
	if id, _ := pack["id"].(string); id == "" {
		pack["id"] = packID
	} else if id != packID {
		failure := maintenanceFailure(registry, "pack_id_mismatch", "edit", "Pack id cannot differ from pack_id", false, true)
		return protocolDraftView{}, &failure
	}
	encoded, err := json.MarshalIndent(pack, "", "  ")
	if err != nil || len(encoded) > maxProtocolDraftFileBytes {
		failure := maintenanceFailure(registry, "pack_encode_failed", "format", "Pack content cannot be encoded within the size limit", false, true)
		return protocolDraftView{}, &failure
	}
	encoded = append(encoded, '\n')
	if err := writeMaintenanceFileAtomic(path, encoded); err != nil {
		failure := maintenanceFailure(registry, "pack_write_failed", "edit", "cannot atomically write Pack draft", true, true)
		return protocolDraftView{}, &failure
	}
	registry.invalidateDraftPlanLocked(draftID)
	view := registry.draftView(draftID, root)
	return view, nil
}

func (registry *protocolRegistry) maintenancePutGuide(input maintenanceGuideInput) (protocolDraftView, *maintenanceToolFailure) {
	if !protocolIDPattern.MatchString(input.PackID) || !protocolOperationIDPattern.MatchString(input.ID) {
		failure := maintenanceFailure(registry, "invalid_guide_id", "input", "pack_id or guide id is invalid", false, true)
		return protocolDraftView{}, &failure
	}
	if strings.TrimSpace(input.Markdown) == "" {
		failure := maintenanceFailure(registry, "empty_guide", "input", "guide markdown cannot be empty", false, true)
		return protocolDraftView{}, &failure
	}
	if input.Visibility == "" {
		input.Visibility = "local"
	}
	metadata := protocolGuideFrontMatter{ID: input.ID, Title: strings.TrimSpace(input.Title), Summary: strings.TrimSpace(input.Summary), Status: input.Status, Operations: normalizePolicyValues(input.Operations), LastVerified: strings.TrimSpace(input.LastVerified), Visibility: input.Visibility}
	frontMatter, err := yaml.Marshal(metadata)
	if err != nil {
		failure := maintenanceFailure(registry, "guide_format_failed", "format", "cannot format guide metadata", false, true)
		return protocolDraftView{}, &failure
	}
	data := []byte("---\n" + string(frontMatter) + "---\n\n" + strings.TrimSpace(input.Markdown) + "\n")
	if len(data) > maxProtocolGuideBytes {
		failure := maintenanceFailure(registry, "guide_too_large", "format", "guide exceeds the size limit", false, true)
		return protocolDraftView{}, &failure
	}
	registry.lifecycleMu.Lock()
	defer registry.lifecycleMu.Unlock()
	root, ok := registry.protocolDraftRoot(input.DraftID)
	if !ok {
		failure := maintenanceFailure(registry, "unknown_draft", "draft", "unknown draft", false, false)
		return protocolDraftView{}, &failure
	}
	if _, err := os.Stat(filepath.Join(root, input.PackID+".json")); err != nil {
		failure := maintenanceFailure(registry, "pack_not_found", "edit", "guide Pack does not exist in this draft", false, true)
		return protocolDraftView{}, &failure
	}
	path := filepath.Join(root, input.PackID, "guides", input.ID+".md")
	if err := writeMaintenanceFileAtomic(path, data); err != nil {
		failure := maintenanceFailure(registry, "guide_write_failed", "edit", "cannot atomically write guide", true, true)
		return protocolDraftView{}, &failure
	}
	registry.invalidateDraftPlanLocked(input.DraftID)
	view := registry.draftView(input.DraftID, root)
	return view, nil
}

func (registry *protocolRegistry) maintenanceDeletePack(draftID, packID string) (protocolDraftView, *maintenanceToolFailure) {
	if !protocolIDPattern.MatchString(packID) {
		failure := maintenanceFailure(registry, "invalid_pack_id", "input", "invalid pack_id", false, true)
		return protocolDraftView{}, &failure
	}
	registry.lifecycleMu.Lock()
	defer registry.lifecycleMu.Unlock()
	root, ok := registry.protocolDraftRoot(draftID)
	if !ok {
		failure := maintenanceFailure(registry, "unknown_draft", "draft", "unknown draft", false, false)
		return protocolDraftView{}, &failure
	}
	if err := os.Remove(filepath.Join(root, packID+".json")); err != nil {
		failure := maintenanceFailure(registry, "pack_not_found", "edit", "Pack does not exist in this draft", false, true)
		return protocolDraftView{}, &failure
	}
	if err := os.RemoveAll(filepath.Join(root, packID)); err != nil {
		failure := maintenanceFailure(registry, "pack_cleanup_failed", "edit", "Pack definition was removed but its authored files could not be removed", true, true)
		return protocolDraftView{}, &failure
	}
	registry.invalidateDraftPlanLocked(draftID)
	view := registry.draftView(draftID, root)
	return view, nil
}

func (registry *protocolRegistry) maintenanceDeleteGuide(draftID, packID, guideID string) (protocolDraftView, *maintenanceToolFailure) {
	if !protocolIDPattern.MatchString(packID) || !protocolOperationIDPattern.MatchString(guideID) {
		failure := maintenanceFailure(registry, "invalid_guide_id", "input", "pack_id or guide_id is invalid", false, true)
		return protocolDraftView{}, &failure
	}
	registry.lifecycleMu.Lock()
	defer registry.lifecycleMu.Unlock()
	root, ok := registry.protocolDraftRoot(draftID)
	if !ok {
		failure := maintenanceFailure(registry, "unknown_draft", "draft", "unknown draft", false, false)
		return protocolDraftView{}, &failure
	}
	if err := os.Remove(filepath.Join(root, packID, "guides", guideID+".md")); err != nil {
		failure := maintenanceFailure(registry, "guide_not_found", "edit", "guide does not exist in this draft", false, true)
		return protocolDraftView{}, &failure
	}
	registry.invalidateDraftPlanLocked(draftID)
	view := registry.draftView(draftID, root)
	return view, nil
}

func (registry *protocolRegistry) maintenanceReviewDraft(draftID string) (any, *maintenanceToolFailure) {
	registry.lifecycleMu.Lock()
	defer registry.lifecycleMu.Unlock()
	root, ok := registry.protocolDraftRoot(draftID)
	if !ok {
		failure := maintenanceFailure(registry, "unknown_draft", "validation", "unknown draft", false, false)
		return nil, &failure
	}
	if failure := registry.maintenanceDraftBaseFailure(root, "review"); failure != nil {
		return nil, failure
	}
	candidate, err := registry.readProtocolCandidate(root)
	if err != nil {
		failure := maintenanceFailure(registry, "draft_invalid", "validation", err.Error(), false, true)
		attachMaintenanceValidationIssue(&failure, err)
		return nil, &failure
	}
	baseDigest, _, _ := registry.currentProtocolDraftBase(root)
	return gin.H{"draft_id": draftID, "digest": candidate.Digest, "base_digest": baseDigest, "protocols": summarizeProtocolCandidate(candidate), "impact": registry.protocolDraftImpact(root), "next_action": "Review impact.files, impact.protocols and impact.pool_ids, then pass this exact digest to localrouter_draft_plan."}, nil
}

func (registry *protocolRegistry) maintenancePlanDraft(draftID, reviewedDigest string) (protocolPackPlan, *maintenanceToolFailure) {
	if !validProtocolDigest(reviewedDigest) {
		failure := maintenanceFailure(registry, "invalid_digest", "plan", "reviewed_digest must be the exact digest returned by review", false, true)
		return protocolPackPlan{}, &failure
	}
	registry.lifecycleMu.Lock()
	defer registry.lifecycleMu.Unlock()
	root, ok := registry.protocolDraftRoot(draftID)
	if !ok {
		failure := maintenanceFailure(registry, "unknown_draft", "plan", "unknown draft", false, false)
		return protocolPackPlan{}, &failure
	}
	if failure := registry.maintenanceDraftBaseFailure(root, "plan"); failure != nil {
		return protocolPackPlan{}, failure
	}
	candidate, err := registry.readProtocolCandidate(root)
	if err != nil {
		failure := maintenanceFailure(registry, "draft_invalid", "plan", err.Error(), false, true)
		attachMaintenanceValidationIssue(&failure, err)
		return protocolPackPlan{}, &failure
	}
	if candidate.Digest != reviewedDigest {
		failure := maintenanceFailure(registry, "draft_changed_after_review", "plan", "draft changed after review; review it again", false, true)
		return protocolPackPlan{}, &failure
	}
	ids := make([]string, 0, len(candidate.Definitions))
	for id := range candidate.Definitions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	files, _ := protocolManagedFiles(root, candidate.Definitions)
	baseDigest, liveDigest, _ := registry.currentProtocolDraftBase(root)
	plan := protocolPackPlan{Digest: candidate.Digest, BaseDigest: baseDigest, LiveDigest: liveDigest, CreatedAt: time.Now().UTC(), Protocols: ids, DraftID: draftID, Files: files}
	registry.pendingPlan = &plan
	return plan, nil
}

func (registry *protocolRegistry) maintenanceAbortDraft(draftID string) *maintenanceToolFailure {
	registry.lifecycleMu.Lock()
	defer registry.lifecycleMu.Unlock()
	root, ok := registry.protocolDraftRoot(draftID)
	if !ok {
		failure := maintenanceFailure(registry, "unknown_draft", "draft", "unknown draft", false, false)
		return &failure
	}
	if registry.pendingPlan != nil && registry.pendingPlan.DraftID == draftID {
		registry.pendingPlan = nil
	}
	if err := os.RemoveAll(root); err != nil {
		failure := maintenanceFailure(registry, "draft_delete_failed", "draft", "cannot discard draft", true, true)
		return &failure
	}
	return nil
}

func (registry *protocolRegistry) maintenanceHistory() ([]protocolRevisionInfo, *maintenanceToolFailure) {
	entries, err := os.ReadDir(registry.revisionRoot())
	if errors.Is(err, os.ErrNotExist) {
		return []protocolRevisionInfo{}, nil
	}
	if err != nil {
		failure := maintenanceFailure(registry, "history_read_failed", "history", "cannot read protocol history", true, false)
		return nil, &failure
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
			info.Live = info.Digest == registry.currentDigest()
			history = append(history, info)
		}
	}
	sort.Slice(history, func(i, j int) bool { return history[i].CreatedAt.After(history[j].CreatedAt) })
	return history, nil
}

func (registry *protocolRegistry) maintenanceRollback(digest string) (any, *maintenanceToolFailure) {
	if !validProtocolDigest(digest) {
		failure := maintenanceFailure(registry, "invalid_digest", "rollback", "valid revision digest required", false, false)
		return nil, &failure
	}
	registry.lifecycleMu.Lock()
	defer registry.lifecycleMu.Unlock()
	if err := registry.restoreProtocolRevision(digest); err != nil {
		failure := maintenanceFailure(registry, "rollback_failed", "rollback", err.Error(), false, false)
		return nil, &failure
	}
	registry.pendingPlan = nil
	return gin.H{"receipt": "PRB-" + time.Now().UTC().Format("20060102T150405Z") + "-" + digest[:8], "digest": digest, "protocols": registry.views()}, nil
}

func maintenanceInternalError(registry *protocolRegistry, stage string, err error, draftPreserved bool) *maintenanceToolFailure {
	failure := maintenanceFailure(registry, "internal_error", stage, fmt.Sprintf("%s", err), true, draftPreserved)
	return &failure
}
