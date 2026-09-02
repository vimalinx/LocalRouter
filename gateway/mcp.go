package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const mcpProtocolVersion = "2025-03-26"

var mcpToolNameCleaner = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)
var protocolPathParameter = regexp.MustCompile(`\{([a-zA-Z0-9_-]+)\}`)

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpToolBinding struct {
	Name       string
	ProtocolID string
	Operation  string
	Method     string
	Route      protocolRoute
}

func (registry *protocolRegistry) mcpTools() []mcpToolBinding {
	bindings := make([]mcpToolBinding, 0)
	for _, view := range registry.views() {
		if !view.Enabled || !view.Ready {
			continue
		}
		for _, route := range view.Routes {
			if available, _ := routeAvailabilityReady(route.Availability, time.Now().UTC()); !available {
				continue
			}
			for _, method := range route.Methods {
				name := mcpToolNameCleaner.ReplaceAllString(view.ID+"__"+route.OperationID, "_")
				if len(route.Methods) > 1 {
					name += "__" + strings.ToLower(method)
				}
				bindings = append(bindings, mcpToolBinding{Name: name, ProtocolID: view.ID, Operation: route.OperationID, Method: method, Route: route})
			}
		}
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].Name < bindings[j].Name })
	return bindings
}

func mcpInputSchema(route protocolRoute) gin.H {
	querySchema := gin.H{"type": "object", "additionalProperties": true}
	if len(route.QueryParameters) > 0 {
		queryProperties := gin.H{}
		for _, parameter := range route.QueryParameters {
			queryProperties[parameter] = gin.H{"type": "string"}
		}
		querySchema["properties"] = queryProperties
		querySchema["required"] = append([]string(nil), route.QueryParameters...)
	}
	properties := gin.H{"query": querySchema}
	required := make([]string, 0, 2)
	if len(route.QueryParameters) > 0 {
		required = append(required, "query")
	}
	pathProperties := gin.H{}
	for _, match := range protocolPathParameter.FindAllStringSubmatch(route.Path, -1) {
		pathProperties[match[1]] = gin.H{"type": "string"}
	}
	if len(pathProperties) > 0 {
		properties["path_params"] = gin.H{"type": "object", "properties": pathProperties, "required": sortedMapKeys(pathProperties), "additionalProperties": false}
		required = append(required, "path_params")
	}
	if route.Methods[0] != http.MethodGet || len(route.RequestBody) > 0 || len(route.RequestSchema) > 0 {
		properties["body"] = protocolOpenAPISchema(route.RequestSchema, route.RequestBody)
	}
	schema := gin.H{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func sortedMapKeys(values gin.H) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (registry *protocolRegistry) handleMCP(runtime localRuntime) gin.HandlerFunc {
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
			mcpResult(c, request.ID, gin.H{
				"protocolVersion": mcpProtocolVersion,
				"capabilities":    gin.H{"tools": gin.H{"listChanged": false}},
				"serverInfo":      gin.H{"name": "LocalRouter", "version": "1"},
			})
		case "notifications/initialized":
			c.Status(http.StatusAccepted)
		case "ping":
			mcpResult(c, request.ID, gin.H{})
		case "tools/list":
			tools := make([]gin.H, 0)
			for _, binding := range registry.mcpTools() {
				tools = append(tools, gin.H{
					"name": binding.Name, "description": binding.Route.Summary,
					"inputSchema": mcpInputSchema(binding.Route),
					"annotations": gin.H{"readOnlyHint": binding.Method == http.MethodGet, "destructiveHint": binding.Method == http.MethodDelete},
				})
			}
			mcpResult(c, request.ID, gin.H{"tools": tools})
		case "tools/call":
			registry.handleMCPToolCall(c, runtime, request)
		default:
			mcpError(c, request.ID, -32601, "method not found")
		}
	}
}

func (registry *protocolRegistry) handleMCPToolCall(c *gin.Context, runtime localRuntime, request mcpRequest) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if json.Unmarshal(request.Params, &params) != nil || params.Name == "" {
		mcpError(c, request.ID, -32602, "tool name is required")
		return
	}
	var binding *mcpToolBinding
	for _, candidate := range registry.mcpTools() {
		if candidate.Name == params.Name {
			copy := candidate
			binding = &copy
			break
		}
	}
	if binding == nil {
		mcpError(c, request.ID, -32602, "unknown or unavailable tool")
		return
	}
	if registry.policies != nil {
		release, allowed := registry.policies.authorizeRequest(c, "mcp", binding.ProtocolID, binding.Operation, "")
		if !allowed {
			return
		}
		defer release()
	}
	var arguments struct {
		PathParams map[string]string `json:"path_params"`
		Query      map[string]any    `json:"query"`
		Body       any               `json:"body"`
	}
	if len(params.Arguments) > 0 && json.Unmarshal(params.Arguments, &arguments) != nil {
		mcpError(c, request.ID, -32602, "tool arguments are invalid")
		return
	}
	path := binding.Route.Path
	for _, match := range protocolPathParameter.FindAllStringSubmatch(binding.Route.Path, -1) {
		value := strings.TrimSpace(arguments.PathParams[match[1]])
		if value == "" {
			mcpError(c, request.ID, -32602, "missing path parameter: "+match[1])
			return
		}
		path = strings.ReplaceAll(path, match[0], url.PathEscape(value))
	}
	for _, parameter := range binding.Route.QueryParameters {
		value, ok := arguments.Query[parameter]
		if !ok || value == nil {
			mcpError(c, request.ID, -32602, "missing query parameter: "+parameter)
			return
		}
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) == "" {
				mcpError(c, request.ID, -32602, "missing query parameter: "+parameter)
				return
			}
		case []any:
			if len(typed) == 0 {
				mcpError(c, request.ID, -32602, "missing query parameter: "+parameter)
				return
			}
		}
	}
	query := make(url.Values)
	for key, value := range arguments.Query {
		switch typed := value.(type) {
		case []any:
			for _, item := range typed {
				query.Add(key, fmt.Sprint(item))
			}
		default:
			query.Set(key, fmt.Sprint(value))
		}
	}
	var body []byte
	if arguments.Body != nil {
		body, _ = json.Marshal(arguments.Body)
	}
	status, response, contentType, err := callLocalProtocol(runtime, binding.ProtocolID, binding.Method, path, query.Encode(), "application/json", body)
	if err != nil {
		mcpError(c, request.ID, -32603, "tool transport failed")
		return
	}
	text := string(response)
	result := gin.H{"content": []gin.H{{"type": "text", "text": text}}, "isError": status < 200 || status >= 300}
	if strings.Contains(strings.ToLower(contentType), "json") && json.Valid(response) {
		result["structuredContent"] = json.RawMessage(response)
	}
	mcpResult(c, request.ID, result)
}

func mcpResult(c *gin.Context, id json.RawMessage, result any) {
	c.JSON(http.StatusOK, gin.H{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": result})
}

func mcpError(c *gin.Context, id json.RawMessage, code int, message string) {
	c.JSON(http.StatusOK, gin.H{"jsonrpc": "2.0", "id": json.RawMessage(id), "error": gin.H{"code": code, "message": message}})
}
