package main

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// Model search runs where the contract and catalogues already live. Clients
// only pass the query; they never download the whole contract to filter it.

var modelSearchWordSplit = regexp.MustCompile(`[^[:alnum:]]+`)

type modelSearchRow struct {
	fields         map[string]any
	ID             string
	Pack           string
	PackName       string
	PackDesc       string
	OwnedBy        string
	OperationKey   string
	SourceKind     string
	ModelKey       string
	identityScore  int
	score          int
	preferred      bool
	compatibleOps  []gin.H
	originalOffset int
}

type modelCatalogSource struct {
	pack, packName, packDesc, operationKey, callURL string
}

func modelSearchWords(text string) []string {
	words := []string{}
	for _, word := range strings.Fields(modelSearchWordSplit.ReplaceAllString(strings.ToLower(text), " ")) {
		switch word {
		case "for", "the", "with", "agent", "model":
			continue
		}
		if len(word) > 1 {
			words = append(words, word)
		}
	}
	return words
}

func handleAgentModelSearch(engine *gin.Engine, runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		query := strings.TrimSpace(c.Query("q"))
		exactOnly := c.Query("exact") == "true" || c.Query("exact") == "1"
		limit := 20
		if raw := c.Query("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 0 || parsed > 200 {
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "invalid_limit", "message": "limit must be 0 (all) or 1..200", "next_action": "use limit=1..200 or limit=0 for all"})
				return
			}
			limit = parsed
		}
		timeout := 10 * time.Second
		if raw := c.Query("timeout"); raw != "" {
			seconds, err := strconv.Atoi(raw)
			if err != nil || seconds < 1 || seconds > 120 {
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "invalid_timeout", "message": "timeout must be 1..120 seconds", "next_action": "use timeout=1..120"})
				return
			}
			timeout = time.Duration(seconds) * time.Second
		}
		requestedPack := ""
		if exactOnly && strings.Contains(query, ":") {
			requestedPack = query[:strings.Index(query, ":")]
			if !modelPreferencePackPattern.MatchString(requestedPack) || strings.TrimSpace(query[len(requestedPack)+1:]) == "" {
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "invalid_exact_selector", "message": "exact model selection must use <pack>:<model-id>", "next_action": "pass q=<pack>:<model-id> with exact=true"})
				return
			}
		}
		c.JSON(http.StatusOK, runtime.searchModels(engine, c, query, exactOnly, limit, requestedPack, timeout))
	}
}

func (runtime localRuntime) searchModels(engine *gin.Engine, c *gin.Context, query string, exactOnly bool, limit int, requestedPack string, timeout time.Duration) gin.H {
	protocols := runtime.protocols.views()
	compatibility := runtime.channelProfiles.compatibilityViews(runtime.store)
	defaultModel := runtime.models.defaultModel()
	wanted := func(pack string) bool { return requestedPack == "" || pack == requestedPack }
	failures := []gin.H{}
	sources := []gin.H{}
	rows := []*modelSearchRow{}

	if requestedPack != "" {
		found, ready, status := false, false, ""
		for _, view := range protocols {
			if view.ID == requestedPack {
				found, ready, status = true, view.Ready, view.Status
				break
			}
		}
		for _, view := range compatibility {
			if !found && view.ID == requestedPack {
				found, ready, status = true, view.Ready, view.Status
			}
		}
		if !found {
			failures = append(failures, gin.H{"pack": requestedPack, "code": "pack_not_found", "reason": "selected Pack is not published", "next_action": "use lr tree to select a published Pack"})
		} else if !ready {
			failures = append(failures, gin.H{"pack": requestedPack, "code": "pack_not_ready", "status": status, "reason": "selected Pack is not ready for model discovery", "next_action": "inspect lr status and lr show for this Pack before querying models"})
		}
	}

	catalogs := []modelCatalogSource{}
	for _, view := range protocols {
		if !view.Ready || !wanted(view.ID) {
			continue
		}
		for _, route := range view.PublicRoutes {
			for _, input := range route.DynamicInputs {
				if input.SourceKind != "request-schema-enum" || len(input.AllowedValues) == 0 {
					continue
				}
				sources = append(sources, gin.H{"pack": view.ID, "operation_key": route.OperationKey, "call_url": nil, "status": "contract", "source_kind": "request-schema-enum", "models": len(input.AllowedValues)})
				for _, value := range input.AllowedValues {
					rows = append(rows, &modelSearchRow{ID: value, Pack: view.ID, PackName: view.Name, PackDesc: view.Description, SourceKind: "request-schema-enum", ModelKey: view.ID + ":" + value})
				}
			}
			if containsAnyString(route.Capabilities, []string{"ai.models", "openai.models", "media.models"}) && routeSupportsMethod(route.protocolRoute, http.MethodGet) {
				catalogs = append(catalogs, modelCatalogSource{view.ID, view.Name, view.Description, route.OperationKey, route.CallURL})
			}
		}
	}
	for _, view := range compatibility {
		if !view.Ready || !wanted(view.ID) {
			continue
		}
		for _, route := range view.Routes {
			if route.Status == "available" && containsString(route.Methods, http.MethodGet) && strings.HasSuffix(route.Path, "/models") {
				catalogs = append(catalogs, modelCatalogSource{view.ID, view.Name, view.Description, route.OperationKey, route.CallURL})
			}
		}
	}

	type catalogResult struct {
		source  gin.H
		failure gin.H
		rows    []*modelSearchRow
	}
	results := make([]catalogResult, len(catalogs))
	var wait sync.WaitGroup
	for index, catalog := range catalogs {
		wait.Add(1)
		go func(index int, catalog modelCatalogSource) {
			defer wait.Done()
			status, body := internalServiceGet(engine, c, catalog.callURL, timeout)
			entries, ok := parseModelCatalog(body)
			if status >= 200 && status < 300 && ok {
				found := []*modelSearchRow{}
				for _, entry := range entries {
					entry.Pack, entry.PackName, entry.PackDesc = catalog.pack, catalog.packName, catalog.packDesc
					entry.OperationKey, entry.SourceKind, entry.ModelKey = catalog.operationKey, "catalog-operation", catalog.pack+":"+entry.ID
					found = append(found, entry)
				}
				results[index] = catalogResult{source: gin.H{"pack": catalog.pack, "operation_key": catalog.operationKey, "call_url": catalog.callURL, "status": "ok", "models": len(found)}, rows: found}
				return
			}
			// Keep the gateway's structured reason; never relay an arbitrary
			// upstream body, credential or private trace header.
			failure := gin.H{"pack": catalog.pack, "operation_key": catalog.operationKey, "call_url": catalog.callURL, "http_status": status}
			var detail map[string]any
			if json.Unmarshal(body, &detail) == nil && detail["success"] == false {
				if _, coded := detail["code"].(string); coded {
					for _, key := range []string{"code", "message", "reason", "retryable", "owner", "retry_after", "next_action", "alternatives"} {
						if value, exists := detail[key]; exists {
							failure[key] = value
						}
					}
					results[index] = catalogResult{failure: failure}
					return
				}
			}
			if status >= 200 && status < 300 {
				failure["code"], failure["reason"] = "model_catalog_invalid_response", "model catalog response is not a supported catalogue object"
			} else {
				failure["code"], failure["reason"] = "model_catalog_request_failed", "model catalog request failed; inspect http_status and the provider readiness"
			}
			failure["next_action"] = "resolve the catalogue failure before selecting a model; do not treat this response as model absence"
			results[index] = catalogResult{failure: failure}
		}(index, catalog)
	}
	wait.Wait()
	for _, result := range results {
		if result.failure != nil {
			failures = append(failures, result.failure)
			continue
		}
		sources = append(sources, result.source)
		rows = append(rows, result.rows...)
	}
	if requestedPack != "" && len(sources) == 0 && len(failures) == 0 {
		failures = append(failures, gin.H{"pack": requestedPack, "code": "model_catalog_unavailable", "reason": "selected Pack publishes neither a model catalogue nor a model schema enum", "next_action": "inspect lr describe for the supported dynamic inputs; no model availability was inferred"})
	}

	preferred := map[string]string{}
	for _, service := range runtime.models.snapshot().Services {
		preferred[service.Pack] = service.PreferredModel
	}
	terms := modelSearchWords(query)
	specific := []string{}
	for _, term := range terms {
		switch term {
		case "free", "quota", "coding", "omp":
			continue
		}
		specific = append(specific, term)
	}
	identityTerms := terms
	if len(specific) > 0 {
		identityTerms = specific
	}
	seen := map[string]bool{}
	unique := []*modelSearchRow{}
	for offset, row := range rows {
		if seen[row.ModelKey] {
			continue
		}
		seen[row.ModelKey] = true
		row.originalOffset = offset
		identity := strings.ToLower(row.Pack + " " + row.ID + " " + row.OwnedBy)
		context := strings.ToLower(row.PackDesc)
		contextScore := 0
		for _, term := range identityTerms {
			if strings.Contains(identity, term) {
				row.identityScore++
			}
		}
		for _, term := range terms {
			if strings.Contains(context, term) {
				contextScore++
			}
		}
		row.score = row.identityScore*10 + contextScore
		row.preferred = preferred[row.Pack] != "" && preferred[row.Pack] == row.ID
		row.compatibleOps = compatibleModelOperations(protocols, compatibility, row)
		unique = append(unique, row)
	}
	normalized := strings.ToLower(query)
	exact := []*modelSearchRow{}
	for _, row := range unique {
		if strings.ToLower(row.ID) == normalized || strings.ToLower(row.ModelKey) == normalized {
			exact = append(exact, row)
		}
	}
	matchMode := "fuzzy"
	all := []*modelSearchRow{}
	switch {
	case exactOnly:
		matchMode, all = "exact-only", exact
	case query == "":
		matchMode, all = "catalog", unique
	case len(exact) > 0:
		matchMode, all = "exact", exact
	default:
		for _, row := range unique {
			if row.identityScore > 0 {
				all = append(all, row)
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		if all[i].preferred != all[j].preferred {
			return all[i].preferred
		}
		return all[i].ModelKey < all[j].ModelKey
	})
	returned := all
	if limit > 0 && len(returned) > limit {
		returned = returned[:limit]
	}
	matches := make([]map[string]any, 0, len(returned))
	for _, row := range returned {
		match := map[string]any{}
		for key, value := range row.fields {
			match[key] = value
		}
		match["id"], match["pack"], match["pack_name"], match["pack_description"] = row.ID, row.Pack, row.PackName, row.PackDesc
		match["source_kind"], match["model_key"], match["score"], match["identity_score"] = row.SourceKind, row.ModelKey, row.score, row.identityScore
		match["preferred"], match["default"], match["compatible_operations"] = row.preferred, defaultModel != "" && row.ID == defaultModel, row.compatibleOps
		if row.OperationKey != "" {
			match["operation_key"] = row.OperationKey
		} else {
			match["source_operation_key"] = nil
		}
		if row.OwnedBy != "" {
			match["owned_by"] = row.OwnedBy
		}
		matches = append(matches, match)
	}
	complete := len(failures) == 0
	nextStep := "choose one model_key and one published compatible_operations entry; run lr describe and lr preflight, then confirm pricing/quota with lr find pool <pack>"
	switch {
	case !complete:
		nextStep = "model discovery is incomplete; inspect failures[].code/reason/next_action before retrying or selecting a model"
	case exactOnly && len(all) == 0:
		nextStep = "no exact live model matched; refresh with lr find model --exact <pack>:<model-id> or choose a model_key returned by lr find model"
	case len(returned) < len(all):
		nextStep = "refine the query, increase --limit, or use --all; then choose one model_key and compatible operation before lr describe/preflight"
	}
	var code any
	if !complete {
		code = "model_discovery_incomplete"
	}
	return gin.H{
		"object": "localrouter.model.search", "domain": "model", "query": query, "match_mode": matchMode,
		"live": true, "success": complete, "code": code, "complete": complete,
		"count": len(all), "returned": len(returned), "truncated": len(returned) < len(all),
		"default_model": defaultModel, "matches": matches, "sources": sources, "failures": failures,
		"exact_next_step": nextStep,
		"boundary":        "model discovery does not configure OMP or another Agent runtime; pass the explicit Pack endpoint and model ID to that runtime separately",
	}
}

func compatibleModelOperations(protocols []protocolView, compatibility []localCompatibilityPackView, row *modelSearchRow) []gin.H {
	operations := []gin.H{}
	for _, view := range protocols {
		if view.ID != row.Pack || !view.Ready {
			continue
		}
		for _, route := range view.PublicRoutes {
			for _, input := range route.DynamicInputs {
				if (input.SourceKind == "catalog-operation" && row.OperationKey != "" && input.SourceOperationKey == row.OperationKey) ||
					(input.SourceKind == "request-schema-enum" && containsString(input.AllowedValues, row.ID)) {
					operations = append(operations, gin.H{"operation_key": route.OperationKey, "operation_id": route.OperationID, "methods": route.Methods, "call_url": route.CallURL, "summary": route.Summary})
					break
				}
			}
		}
	}
	for _, view := range compatibility {
		if view.ID != row.Pack || !view.Ready {
			continue
		}
		for _, route := range view.Routes {
			if route.Status == "available" && containsString(route.Methods, http.MethodPost) {
				operations = append(operations, gin.H{"operation_key": route.OperationKey, "operation_id": route.OperationID, "methods": route.Methods, "call_url": route.CallURL, "summary": nil})
			}
		}
	}
	return operations
}

func parseModelCatalog(body []byte) ([]*modelSearchRow, bool) {
	var document struct {
		Data   []json.RawMessage `json:"data"`
		Models []json.RawMessage `json:"models"`
	}
	if json.Unmarshal(body, &document) != nil {
		return nil, false
	}
	entries := document.Data
	if entries == nil {
		entries = document.Models
	}
	if entries == nil {
		return nil, false
	}
	rows := []*modelSearchRow{}
	for _, raw := range entries {
		var id string
		if json.Unmarshal(raw, &id) == nil {
			if id != "" {
				rows = append(rows, &modelSearchRow{ID: id})
			}
			continue
		}
		var fields map[string]any
		if json.Unmarshal(raw, &fields) != nil {
			continue
		}
		for _, key := range []string{"id", "model_cls", "model", "name"} {
			if value, ok := fields[key].(string); ok && value != "" {
				id = value
				break
			}
		}
		if id == "" {
			continue
		}
		ownedBy := ""
		for _, key := range []string{"owned_by", "provider", "task"} {
			if value, ok := fields[key].(string); ok && value != "" {
				ownedBy = value
				break
			}
		}
		rows = append(rows, &modelSearchRow{fields: fields, ID: id, OwnedBy: ownedBy})
	}
	return rows, true
}

// internalServiceGet issues one GET through this listener's own pipeline as
// the calling Token, so policy, budget and trace rules apply exactly as if the
// caller had requested the URL itself.
func internalServiceGet(engine *gin.Engine, c *gin.Context, path string, timeout time.Duration) (int, []byte) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return 0, nil
	}
	// Server handlers expect a non-nil body, unlike client requests.
	request.Body = http.NoBody
	request.Host, request.RemoteAddr = c.Request.Host, c.Request.RemoteAddr
	for _, name := range []string{"Authorization", "traceparent", "X-LocalRouter-Task-Id"} {
		if value := c.GetHeader(name); value != "" {
			request.Header.Set(name, value)
		}
	}
	writer := &serviceResponseBuffer{header: make(http.Header), status: http.StatusOK, context: ctx}
	engine.ServeHTTP(writer, request)
	if ctx.Err() != nil && writer.body.Len() == 0 {
		return http.StatusGatewayTimeout, nil
	}
	return writer.status, writer.body.Bytes()
}
