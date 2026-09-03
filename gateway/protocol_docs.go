package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"gopkg.in/yaml.v3"
)

const maxProtocolGuideBytes = 1 << 20

type protocolGuideFrontMatter struct {
	ID           string   `yaml:"id"`
	Title        string   `yaml:"title"`
	Summary      string   `yaml:"summary"`
	Status       string   `yaml:"status"`
	Operations   []string `yaml:"operations"`
	LastVerified string   `yaml:"last_verified,omitempty"`
	Visibility   string   `yaml:"visibility,omitempty"`
}

type protocolGuide struct {
	protocolGuideFrontMatter
	Markdown string
	HTML     template.HTML
}

type protocolGuideSummary struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Summary      string   `json:"summary"`
	Status       string   `json:"status"`
	Operations   []string `json:"operations"`
	LastVerified string   `json:"last_verified,omitempty"`
	Visibility   string   `json:"visibility"`
	MarkdownURL  string   `json:"markdown_url"`
}

type protocolDocLinks struct {
	HTML     string `json:"html"`
	Manifest string `json:"manifest"`
	Markdown string `json:"markdown"`
	Examples string `json:"examples"`
}

type protocolExample struct {
	OperationKey       string                          `json:"operation_key"`
	OperationID        string                          `json:"operation_id"`
	OperationIDRole    string                          `json:"operation_id_role"`
	OperationIDIsURL   bool                            `json:"operation_id_is_url"`
	Methods            []string                        `json:"methods"`
	Path               string                          `json:"path"`
	CallURL            string                          `json:"call_url"`
	Call               protocolCallContract            `json:"call"`
	RequestExampleRole string                          `json:"request_example_role,omitempty"`
	DynamicInputs      map[string]protocolDynamicInput `json:"dynamic_inputs,omitempty"`
	Streaming          bool                            `json:"streaming"`
	RequestBody        json.RawMessage                 `json:"request_example,omitempty"`
}

var protocolMarkdown = goldmark.New(goldmark.WithExtensions(extension.GFM))

func protocolDocLinksFor(protocolID string) protocolDocLinks {
	base := "/docs/packs/" + protocolID
	return protocolDocLinks{
		HTML: base, Manifest: base + "/manifest.json", Markdown: base + "/guide.md",
		Examples: base + "/examples.json",
	}
}

func summarizeProtocolGuides(protocolID string, guides []protocolGuide) []protocolGuideSummary {
	result := make([]protocolGuideSummary, 0, len(guides))
	for _, guide := range guides {
		result = append(result, protocolGuideSummary{
			ID: guide.ID, Title: guide.Title, Summary: guide.Summary, Status: guide.Status,
			Operations: append([]string(nil), guide.Operations...), LastVerified: guide.LastVerified,
			Visibility:  guide.Visibility,
			MarkdownURL: "/docs/packs/" + protocolID + "/guides/" + guide.ID + ".md",
		})
	}
	return result
}

func (registry *protocolRegistry) loadGuidesAt(root string, definitions map[string]protocolDefinition) (map[string][]protocolGuide, error) {
	loaded := make(map[string][]protocolGuide, len(definitions))
	for protocolID, definition := range definitions {
		guideDir := filepath.Join(root, protocolID, "guides")
		entries, err := os.ReadDir(guideDir)
		if errors.Is(err, os.ErrNotExist) {
			loaded[protocolID] = []protocolGuide{}
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read guides for %s: %w", protocolID, err)
		}
		operations := make(map[string]bool, len(definition.Routes))
		for _, route := range definition.Routes {
			operations[route.OperationID] = true
		}
		seen := make(map[string]bool)
		guides := make([]protocolGuide, 0, len(entries))
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
				continue
			}
			guide, err := loadProtocolGuide(filepath.Join(guideDir, entry.Name()), operations)
			if err != nil {
				return nil, fmt.Errorf("load guide %s/%s: %w", protocolID, entry.Name(), err)
			}
			if seen[guide.ID] {
				return nil, fmt.Errorf("load guides for %s: duplicate guide id %q", protocolID, guide.ID)
			}
			if strings.TrimSuffix(entry.Name(), ".md") != guide.ID {
				return nil, fmt.Errorf("load guide %s/%s: filename must match guide id %q", protocolID, entry.Name(), guide.ID)
			}
			seen[guide.ID] = true
			guides = append(guides, guide)
		}
		sort.Slice(guides, func(i, j int) bool { return guides[i].ID < guides[j].ID })
		loaded[protocolID] = guides
	}
	return loaded, nil
}

func loadProtocolGuide(path string, operations map[string]bool) (protocolGuide, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return protocolGuide{}, err
	}
	if len(data) > maxProtocolGuideBytes {
		return protocolGuide{}, fmt.Errorf("guide exceeds %d bytes", maxProtocolGuideBytes)
	}
	frontMatter, markdownBody, err := splitProtocolGuide(data)
	if err != nil {
		return protocolGuide{}, err
	}
	var metadata protocolGuideFrontMatter
	decoder := yaml.NewDecoder(strings.NewReader(frontMatter))
	decoder.KnownFields(true)
	if err := decoder.Decode(&metadata); err != nil {
		return protocolGuide{}, fmt.Errorf("decode front matter: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return protocolGuide{}, errors.New("front matter must contain one YAML document")
	}
	if !protocolOperationIDPattern.MatchString(metadata.ID) {
		return protocolGuide{}, errors.New("id must use the same safe format as operation_id")
	}
	if strings.TrimSpace(metadata.Title) == "" || strings.TrimSpace(metadata.Summary) == "" {
		return protocolGuide{}, errors.New("title and summary are required")
	}
	switch metadata.Status {
	case "draft", "observed", "verified", "deprecated":
	default:
		return protocolGuide{}, errors.New("status must be draft, observed, verified, or deprecated")
	}
	if metadata.Visibility == "" {
		metadata.Visibility = "local"
	}
	if metadata.Visibility != "local" && metadata.Visibility != "public" {
		return protocolGuide{}, errors.New("visibility must be local or public")
	}
	if metadata.Status == "verified" && strings.TrimSpace(metadata.LastVerified) == "" {
		return protocolGuide{}, errors.New("verified guides require last_verified")
	}
	seenOperations := make(map[string]bool)
	for _, operationID := range metadata.Operations {
		if !operations[operationID] {
			return protocolGuide{}, fmt.Errorf("unknown operation reference %q", operationID)
		}
		if seenOperations[operationID] {
			return protocolGuide{}, fmt.Errorf("duplicate operation reference %q", operationID)
		}
		seenOperations[operationID] = true
	}
	if strings.TrimSpace(markdownBody) == "" {
		return protocolGuide{}, errors.New("guide body cannot be empty")
	}
	var rendered bytes.Buffer
	if err := protocolMarkdown.Convert([]byte(markdownBody), &rendered); err != nil {
		return protocolGuide{}, fmt.Errorf("render markdown: %w", err)
	}
	return protocolGuide{protocolGuideFrontMatter: metadata, Markdown: markdownBody, HTML: template.HTML(rendered.String())}, nil
}

func splitProtocolGuide(data []byte) (string, string, error) {
	normalized := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return "", "", errors.New("guide must start with YAML front matter")
	}
	remainder := strings.TrimPrefix(normalized, "---\n")
	separator := strings.Index(remainder, "\n---\n")
	if separator < 0 {
		return "", "", errors.New("guide front matter is not closed")
	}
	return remainder[:separator], strings.TrimSpace(remainder[separator+5:]) + "\n", nil
}

func (registry *protocolRegistry) guidesFor(protocolID string) ([]protocolGuide, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	if _, exists := registry.templates[protocolID]; !exists {
		return nil, false
	}
	guides := registry.guides[protocolID]
	return append([]protocolGuide(nil), guides...), true
}

func (registry *protocolRegistry) viewFor(protocolID string) (protocolView, bool) {
	for _, view := range registry.views() {
		if view.ID == protocolID {
			return view, true
		}
	}
	return protocolView{}, false
}

func (registry *protocolRegistry) handleDiscovery(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		agentMaintenanceEnabled := registry.policies != nil && registry.policies.isAgentMaintenanceEnabled()
		compatibilityPacks := runtime.channelProfiles.compatibilityViews(runtime.store)
		topologyDigest := localServiceTopologyDigest(registry.currentDigest(), runtime.channelProfiles)
		c.JSON(http.StatusOK, gin.H{
			"schema_version": "1",
			"name":           "LocalRouter",
			"scope":          "loopback",
			"contract": gin.H{
				"digest": registry.currentDigest(), "schema_version": agentContractSchemaVersion,
				"cache": "reuse cached operation contracts until the digest or schema_version changes",
			},
			"topology": gin.H{
				"digest": topologyDigest, "schema_version": agentContractSchemaVersion,
				"cache": "reuse the service tree until this digest or schema_version changes; readiness remains live state",
			},
			"agent": gin.H{
				"catalog": "/agent/operations", "resolve": "/agent/resolve", "describe": "/agent/operations/{pack}/{operation}",
				"compare": "/agent/compare", "preflight": "/agent/preflight", "whoami": "/agent/whoami",
				"workflow": "/w/{pack}/{workflow}", "docs": "/docs/agent.json",
				"selection_mode": "agent", "merged": false,
			},
			"invocation": gin.H{
				"operation_key":       "stable Pack and operation selector: <pack>.<operation_id>",
				"operation_id":        "semantic selector for Agent APIs, lr and MCP; never an HTTP path",
				"operation_id_is_url": false,
				"http":                "use each route.call_url with one of route.call.methods",
				"cli":                 "prefer route.call.cli or lr call <pack> <operation_id> '<json>'",
			},
			"pack_model": gin.H{
				"unit": "service-pack",
				"rule": "all callable services are exposed as Packs; choose the smallest authoring form that preserves the observed contract",
				"kinds": gin.H{
					"compatibility": "lightweight standard API Pack backed by Channel Profiles and Channels",
					"protocol":      "full Pack for isolated paths, custom transports, authentication, pools, adapters or workflows",
				},
				"projections": gin.H{"workflow": "/w/{pack}", "mcp": "/mcp"},
			},
			"surfaces": []gin.H{
				{"id": "compatibility", "paths": []string{"/v1/*", "/v1beta/*"}, "role": "standard API Packs"},
				{"id": "protocol", "paths": []string{"/p/{pack}/*"}, "role": "isolated Protocol Pack calls"},
				{"id": "workflow", "paths": []string{"/w/{pack}/{workflow}"}, "role": "persistent Pack workflow projection"},
				{"id": "mcp", "paths": []string{"/mcp"}, "role": "ready Pack operations projected as Agent tools"},
			},
			"documentation": gin.H{
				"html": "/docs", "index": "/docs/index.json", "openapi": "/docs/openapi.json",
				"agent": "/docs/agent.json", "pool_catalog": "/docs/pools/index.json", "pool_guide": "/docs/pools/catalog.md", "mcp": "/mcp",
			},
			"maintenance": gin.H{
				"console": "/#control", "grant_console": "/#tokens", "mcp": "/manage/mcp",
				"drafts": "/local/api/protocol-drafts", "history": "/local/api/protocols/history",
				"console_auth": gin.H{
					"enabled": runtime.adminAuth.isEnabled(), "default_enabled": false,
					"enable_or_disable": "/local/api/admin-auth", "change_token": "/local/api/admin-token",
				},
				"auth": gin.H{
					"default":       "administrator",
					"administrator": gin.H{"type": "header", "header": adminTokenHeader, "required_for_maintenance_mcp": true},
					"agent_token": gin.H{
						"enabled": agentMaintenanceEnabled, "type": "bearer", "header": "Authorization",
						"required_capability": localRouterMaintainCapability, "service_access": false,
					},
				},
				"grant":        "Service Tokens are call-only. Agent maintenance Tokens are separate, maintenance-only, and accepted only while the operator switch is enabled.",
				"lifecycle":    []string{"draft", "edit", "validate", "review-impact", "plan", "apply", "verify", "rollback"},
				"editing":      "Semantic MCP tools own paths, JSON/YAML formatting, atomic writes and exact digests.",
				"failure":      "Drafts are preserved and local post-apply verification failures automatically restore the previous revision.",
				"operator_api": gin.H{"path": "/local/api", "auth": gin.H{"type": "header", "header": "X-Local-Admin", "required": runtime.adminAuth.isEnabled()}},
			},
			"compatibility_packs": compatibilityPacks,
			"protocols":           registry.views(),
			"authentication": gin.H{
				"type": "bearer", "header": "Authorization", "applies_to": []string{"/agent/*", "/p/*", "/w/*", "/mcp", "/v1/*", "/v1beta/*"},
			},
			"protocol_pack_versions": []string{"1", "2", "3"},
		})
	}
}

func (registry *protocolRegistry) handleOpenAPI(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, registry.openAPIDocument())
}

func (registry *protocolRegistry) openAPIDocument() gin.H {
	paths := make(map[string]any)
	tags := make([]gin.H, 0)
	for _, view := range registry.views() {
		tags = append(tags, gin.H{"name": view.ID, "description": view.Description})
		for _, route := range view.Routes {
			fullPath := view.Mount + route.Path
			published := publishProtocolRoute(view.ID, view.Mount, view.Routes, route)
			parameterDefinitions := make([]gin.H, 0)
			pathParameterNames := make([]string, 0)
			for name := range protocolPathParameterNames(route.Path) {
				pathParameterNames = append(pathParameterNames, name)
			}
			sort.Strings(pathParameterNames)
			for _, name := range pathParameterNames {
				parameterDefinitions = append(parameterDefinitions, gin.H{"name": name, "in": "path", "required": true, "schema": gin.H{"type": "string"}})
			}
			for _, name := range route.QueryParameters {
				parameterDefinitions = append(parameterDefinitions, gin.H{"name": name, "in": "query", "required": true, "schema": gin.H{"type": "string"}})
			}
			pathItem, _ := paths[fullPath].(map[string]any)
			if pathItem == nil {
				pathItem = make(map[string]any)
				paths[fullPath] = pathItem
			}
			for _, method := range route.Methods {
				operationID := view.ID + "." + route.OperationID
				if len(route.Methods) > 1 {
					operationID += "." + strings.ToLower(method)
				}
				responseContent := gin.H{"application/json": gin.H{"schema": protocolOpenAPISchema(route.ResponseSchema, nil)}}
				if route.Streaming {
					responseContent = gin.H{"text/event-stream": gin.H{"schema": gin.H{"type": "string"}}}
				}
				operation := gin.H{
					"operationId": operationID, "summary": route.Summary, "tags": []string{view.ID},
					"security":                          []gin.H{{"LocalRouterBearer": []string{}}},
					"responses":                         gin.H{"default": gin.H{"description": "Upstream response", "content": responseContent}},
					"x-localrouter-operation-id":        route.OperationID,
					"x-localrouter-operation-id-role":   "semantic-selector",
					"x-localrouter-operation-id-is-url": false,
					"x-localrouter-call-url":            fullPath,
					"x-localrouter-streaming":           route.Streaming,
					"x-localrouter-transport":           route.Transport,
					"x-localrouter-availability":        route.Availability.Status,
					"x-localrouter-capabilities":        route.Capabilities,
				}
				if published.RequestExampleRole != "" {
					operation["x-localrouter-request-example-role"] = published.RequestExampleRole
				}
				if len(published.DynamicInputs) > 0 {
					operation["x-localrouter-dynamic-inputs"] = published.DynamicInputs
				}
				if len(parameterDefinitions) > 0 {
					operation["parameters"] = parameterDefinitions
				}
				if len(route.RequestBody) > 0 && method != http.MethodGet {
					var example any
					_ = json.Unmarshal(route.RequestBody, &example)
					operation["requestBody"] = gin.H{
						"required": true,
						"content":  gin.H{"application/json": gin.H{"schema": protocolOpenAPISchema(route.RequestSchema, route.RequestBody), "example": example}},
					}
				}
				pathItem[strings.ToLower(method)] = operation
			}
		}
		for _, workflow := range view.Workflows {
			createPath := view.WorkflowMount + "/" + workflow.ID
			jobPath := createPath + "/{job}"
			paths[createPath] = map[string]any{
				"post": gin.H{
					"operationId": view.ID + ".workflow." + workflow.ID + ".create",
					"summary":     "Start " + workflow.Name, "tags": []string{view.ID},
					"security":    []gin.H{{"LocalRouterBearer": []string{}}},
					"requestBody": gin.H{"required": true, "content": gin.H{"application/json": gin.H{"schema": gin.H{"type": "object"}}}},
					"responses":   gin.H{"202": gin.H{"description": "Local workflow job created"}},
				},
			}
			jobOperations := map[string]any{
				"get": gin.H{
					"operationId": view.ID + ".workflow." + workflow.ID + ".get",
					"summary":     "Poll " + workflow.Name, "tags": []string{view.ID},
					"security":  []gin.H{{"LocalRouterBearer": []string{}}},
					"responses": gin.H{"200": gin.H{"description": "Local workflow job state"}},
				},
			}
			if workflow.CancelOperation != "" || workflow.CancelStep != "" {
				jobOperations["delete"] = gin.H{
					"operationId": view.ID + ".workflow." + workflow.ID + ".cancel",
					"summary":     "Cancel " + workflow.Name, "tags": []string{view.ID},
					"security":  []gin.H{{"LocalRouterBearer": []string{}}},
					"responses": gin.H{"200": gin.H{"description": "Workflow cancelled"}},
				}
			}
			paths[jobPath] = jobOperations
		}
	}
	return gin.H{
		"openapi":    "3.1.0",
		"info":       gin.H{"title": "LocalRouter Protocol Packs", "version": "3.0.0", "description": "Generated from validated LocalRouter protocol contracts."},
		"servers":    []gin.H{{"url": "/"}},
		"tags":       tags,
		"paths":      paths,
		"components": gin.H{"securitySchemes": gin.H{"LocalRouterBearer": gin.H{"type": "http", "scheme": "bearer"}}},
	}
}

func protocolOpenAPISchema(explicit, example json.RawMessage) any {
	if len(explicit) > 0 {
		var schema any
		if json.Unmarshal(explicit, &schema) == nil {
			return schema
		}
	}
	if len(example) > 0 {
		var value any
		if json.Unmarshal(example, &value) == nil {
			return inferProtocolSchema(value)
		}
	}
	return gin.H{}
}

func inferProtocolSchema(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		properties := make(map[string]any, len(typed))
		required := make([]string, 0, len(typed))
		for key, child := range typed {
			properties[key] = inferProtocolSchema(child)
			required = append(required, key)
		}
		sort.Strings(required)
		return gin.H{"type": "object", "properties": properties, "required": required}
	case []any:
		items := any(gin.H{})
		if len(typed) > 0 {
			items = inferProtocolSchema(typed[0])
		}
		return gin.H{"type": "array", "items": items}
	case string:
		return gin.H{"type": "string"}
	case float64:
		return gin.H{"type": "number"}
	case bool:
		return gin.H{"type": "boolean"}
	case nil:
		return gin.H{"type": []string{"null"}}
	default:
		return gin.H{}
	}
}

func (registry *protocolRegistry) handleExamples(c *gin.Context) {
	view, ok := registry.viewFor(c.Param("id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"message": "unknown protocol"})
		return
	}
	examples := make([]protocolExample, 0, len(view.Routes))
	for _, route := range view.Routes {
		published := publishProtocolRoute(view.ID, view.Mount, view.Routes, route)
		examples = append(examples, protocolExample{
			OperationKey: published.OperationKey, OperationID: route.OperationID,
			OperationIDRole: published.OperationIDRole, OperationIDIsURL: false,
			Methods: route.Methods, Path: route.Path, CallURL: published.CallURL,
			Call: published.Call, RequestExampleRole: published.RequestExampleRole, DynamicInputs: published.DynamicInputs,
			Streaming: route.Streaming, RequestBody: route.RequestBody,
		})
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"object": "protocol.examples", "protocol_id": view.ID, "data": examples})
}

func (registry *protocolRegistry) handlePackMarkdown(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	view, ok := registry.viewFor(c.Param("id"))
	if !ok {
		c.String(http.StatusNotFound, "unknown protocol\n")
		return
	}
	guides, _ := registry.guidesFor(view.ID)
	var output strings.Builder
	fmt.Fprintf(&output, "# %s\n\n%s\n\n", view.Name, view.Description)
	fmt.Fprintf(&output, "- 调用前缀：`%s`\n- 状态：`%s`\n- Manifest：`%s`\n- OpenAPI：`/docs/openapi.json`\n\n", view.Mount, view.Status, view.Docs.Manifest)
	if view.OpenAIBaseURL != "" {
		fmt.Fprintf(&output, "- OpenAI v1 Base URL：`%s`（客户端在其后追加 `/models` 或 `/chat/completions`）\n\n", view.OpenAIBaseURL)
	}
	output.WriteString("> `operation_id` 是给 Agent API、`lr` 和 MCP 使用的语义选择标识，不是 URL。直接发送 HTTP 时只使用下方的 **实际调用地址**（也就是 Manifest 中的 `call_url`）。\n\n")
	output.WriteString("> `request_example` 只说明请求形状，不证明其中的动态值当前可用；存在 `dynamic_inputs` 时，必须先按其来源解析模型或资源 ID。\n\n")
	output.WriteString("## 操作参考\n\n")
	for _, route := range view.Routes {
		fmt.Fprintf(&output, "- **实际调用**：`%s %s`\n  - `operation_key`：`%s.%s`\n  - `operation_id`：`%s`（语义选择标识，不是 URL）\n  - CLI：`lr call %s %s '<json>'`\n  - 用途：%s", strings.Join(route.Methods, "/"), view.Mount+route.Path, view.ID, route.OperationID, route.OperationID, view.ID, route.OperationID, route.Summary)
		if route.Streaming {
			output.WriteString("（流式）")
		}
		output.WriteString("\n")
	}
	if len(view.Workflows) > 0 {
		output.WriteString("\n## 异步工作流\n\n")
		for _, workflow := range view.Workflows {
			mode := "创建/轮询"
			if len(workflow.Steps) > 0 {
				mode = "持久化图状态机（后台推进，可查询）"
			}
			fmt.Fprintf(&output, "- `%s` — %s；%s：`POST %s/%s`，查询：`GET %s/%s/{job}`\n", workflow.ID, workflow.Name, mode, view.WorkflowMount, workflow.ID, view.WorkflowMount, workflow.ID)
		}
	}
	for _, guide := range guides {
		fmt.Fprintf(&output, "\n---\n\n## %s\n\n> %s · 状态：`%s`", guide.Title, guide.Summary, guide.Status)
		if guide.LastVerified != "" {
			fmt.Fprintf(&output, " · 最近验证：`%s`", guide.LastVerified)
		}
		output.WriteString("\n\n")
		output.WriteString(guide.Markdown)
	}
	c.Data(http.StatusOK, "text/markdown; charset=utf-8", []byte(output.String()))
}

func (registry *protocolRegistry) handleGuideMarkdown(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	protocolID := c.Param("id")
	guideID := strings.TrimSuffix(c.Param("guide"), ".md")
	guides, ok := registry.guidesFor(protocolID)
	if !ok {
		c.String(http.StatusNotFound, "unknown protocol\n")
		return
	}
	for _, guide := range guides {
		if guide.ID == guideID {
			c.Data(http.StatusOK, "text/markdown; charset=utf-8", []byte(guide.Markdown))
			return
		}
	}
	c.String(http.StatusNotFound, "unknown guide\n")
}

type protocolPackPage struct {
	View   protocolView
	Guides []protocolGuide
}

var protocolDocsTemplate = template.Must(template.New("protocol-docs").Parse(protocolDocsPageHTML))
var protocolPackTemplate = template.Must(template.New("protocol-pack").Parse(protocolPackPageHTML))

func (registry *protocolRegistry) handleDocsHTML(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	if err := protocolDocsTemplate.Execute(c.Writer, registry.views()); err != nil {
		c.Status(http.StatusInternalServerError)
	}
}

func (registry *protocolRegistry) handlePackHTML(c *gin.Context) {
	view, ok := registry.viewFor(c.Param("id"))
	if !ok {
		c.String(http.StatusNotFound, "unknown protocol\n")
		return
	}
	guides, _ := registry.guidesFor(view.ID)
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	if err := protocolPackTemplate.Execute(c.Writer, protocolPackPage{View: view, Guides: guides}); err != nil {
		c.Status(http.StatusInternalServerError)
	}
}

const protocolDocsCSS = `:root{--bg:#f8f7fc;--card:#fff;--text:#1e1b4b;--muted:#526078;--border:#ddd6fe;--primary:#6d28d9;--ok:#047857;--warn:#92400e;font-family:Inter,ui-sans-serif,system-ui,sans-serif}*{box-sizing:border-box}body{margin:0;min-width:320px;background:var(--bg);color:var(--text);line-height:1.6}.shell{width:min(1180px,calc(100% - 32px));margin:auto;padding:28px 0 56px}a{color:var(--primary)}a:focus-visible{outline:3px solid #6d28d959;outline-offset:3px}.top{display:flex;align-items:flex-start;justify-content:space-between;gap:20px;margin-bottom:24px}h1{margin:0 0 4px;font-size:clamp(1.5rem,4vw,2rem)}h2{margin-top:0}h3{margin-top:28px}p{margin-top:0}.muted{color:var(--muted)}.grid{display:grid;gap:16px}.card,.guide{background:var(--card);border:1px solid var(--border);border-radius:14px;padding:18px}.head{display:flex;align-items:flex-start;justify-content:space-between;gap:14px}.status{border-radius:999px;padding:4px 10px;font-size:.78rem;font-weight:750;white-space:nowrap}.ready,.verified{background:#ecfdf5;color:var(--ok)}.waiting,.draft,.observed{background:#fffbeb;color:var(--warn)}code,pre{font-family:ui-monospace,SFMono-Regular,Menlo,monospace}code{overflow-wrap:anywhere}.mount{display:inline-block;padding:7px 10px;background:#f5f3ff;border-radius:8px}.routes{display:grid;gap:8px;margin-top:14px}.route{display:grid;grid-template-columns:minmax(110px,.25fr) minmax(180px,.65fr) 1fr;gap:10px;padding:10px;border-top:1px solid #eceaf6}.method{font-weight:800;color:var(--primary)}.links{display:flex;gap:12px;flex-wrap:wrap}.guide{margin-top:18px}.guide pre{padding:14px;overflow:auto;background:#111827;color:#f9fafb;border-radius:10px}.guide table{border-collapse:collapse;width:100%}.guide th,.guide td{border:1px solid var(--border);padding:8px;text-align:left}@media(max-width:700px){.shell{width:min(100% - 20px,1180px)}.top,.head{flex-direction:column}.route{grid-template-columns:1fr}.status{align-self:flex-start}}@media(prefers-reduced-motion:reduce){*{scroll-behavior:auto!important}}`

const protocolDocsPageHTML = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>LocalRouter 协议文档</title><style>` + protocolDocsCSS + `</style></head><body><main class="shell"><div class="top"><div><h1>LocalRouter Protocol Packs</h1><p class="muted">同一端口提供运行契约、机器发现、OpenAPI，以及 Agent 维护的详细用法。</p><div class="links"><a href="/.well-known/localrouter.json">Agent 发现</a><a href="/docs/index.json">JSON 索引</a><a href="/docs/openapi.json">OpenAPI</a><a href="/docs/pools/catalog.md">号池目录</a></div></div><a href="/">返回控制台</a></div><div class="grid">{{range .}}{{$protocol := .}}<article class="card"><div class="head"><div><h2><a href="{{.Docs.HTML}}">{{.Name}}</a></h2><p class="muted">{{.Description}}</p><code class="mount">{{.Mount}}</code></div>{{if .Ready}}<span class="status ready">{{.StatusLabel}}</span>{{else}}<span class="status waiting">{{.StatusLabel}}</span>{{end}}</div><div class="routes" role="list">{{range .Routes}}<div class="route" role="listitem"><span><span class="muted">选择标识（非 URL）</span><br><code>{{$protocol.ID}}.{{.OperationID}}</code></span><span><span class="muted">实际调用</span><br><b class="method">{{range .Methods}}{{.}} {{end}}</b><code>{{$protocol.Mount}}{{.Path}}</code></span><span>{{.Summary}}{{if .Streaming}} · 流式透传{{end}}</span></div>{{end}}</div>{{if .Guides}}<p class="muted">Agent 指南：{{range .Guides}}<a href="{{.MarkdownURL}}">{{.Title}}</a> · {{end}}</p>{{end}}</article>{{else}}<section class="card"><h2>还没有 Protocol Pack</h2><p class="muted">放入通过校验的协议模板后重载。</p></section>{{end}}</div></main></body></html>`

const protocolPackPageHTML = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.View.Name}} · LocalRouter</title><style>` + protocolDocsCSS + `</style></head><body><main class="shell"><div class="top"><div><h1>{{.View.Name}}</h1><p class="muted">{{.View.Description}}</p><div class="links"><a href="{{.View.Docs.Manifest}}">Manifest</a><a href="{{.View.Docs.Markdown}}">Markdown 全文</a><a href="{{.View.Docs.Examples}}">调用示例</a><a href="/docs/openapi.json">OpenAPI</a></div></div><a href="/docs">全部 Pack</a></div><section class="card"><div class="head"><div><h2>生成的操作参考</h2><p class="muted">选择标识用于 Agent API、lr 与 MCP；直接 HTTP 只使用“实际调用”地址。</p><code class="mount">{{.View.Mount}}</code></div>{{if .View.Ready}}<span class="status ready">{{.View.StatusLabel}}</span>{{else}}<span class="status waiting">{{.View.StatusLabel}}</span>{{end}}</div><div class="routes">{{$protocol := .View}}{{range .View.Routes}}<div class="route"><span><span class="muted">选择标识（非 URL）</span><br><code>{{$protocol.ID}}.{{.OperationID}}</code></span><span><span class="muted">实际调用</span><br><b class="method">{{range .Methods}}{{.}} {{end}}</b><code>{{$protocol.Mount}}{{.Path}}</code></span><span>{{.Summary}}{{if .Streaming}} · 流式透传{{end}}</span></div>{{end}}</div>{{if .View.Workflows}}<h3>异步工作流</h3><div class="routes">{{range .View.Workflows}}<div class="route"><code>{{.ID}}</code><span><b class="method">POST</b> <code>{{$protocol.WorkflowMount}}/{{.ID}}</code></span><span>{{.Name}} · 后台推进并可查询本地 Job</span></div>{{end}}</div>{{end}}</section>{{range .Guides}}<article class="guide"><div class="head"><div><h2>{{.Title}}</h2><p class="muted">{{.Summary}}</p></div><span class="status {{.Status}}">{{.Status}}{{if .LastVerified}} · {{.LastVerified}}{{end}}</span></div>{{.HTML}}</article>{{else}}<section class="guide"><h2>还没有 Agent 指南</h2><p class="muted">在此 Pack 的 guides 目录写入带元数据的 Markdown，校验重载后会显示在这里。</p></section>{{end}}</main></body></html>`
