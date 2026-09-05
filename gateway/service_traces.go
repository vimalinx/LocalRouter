package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

var serviceCorrelationPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,95}$`)
var serviceTraceIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var serviceSpanIDPattern = regexp.MustCompile(`^[a-f0-9]{16}$`)

type serviceMetering struct {
	ResourceIDPath string               `json:"resource_id_path,omitempty"`
	StatePath      string               `json:"state_path,omitempty"`
	Units          []serviceUnitMapping `json:"units,omitempty"`
}

type serviceUnitMapping struct {
	Unit   string `json:"unit"`
	Path   string `json:"path"`
	Source string `json:"source"`
	Mode   string `json:"mode"` // delta, or snapshot keyed by provider resource
}

type serviceUnitObservation struct {
	Unit     string  `json:"unit"`
	Quantity float64 `json:"quantity"`
	Source   string  `json:"source"`
	Mode     string  `json:"mode"`
}

type serviceTrace struct {
	SpanID         string                   `json:"span_id"`
	TraceID        string                   `json:"trace_id"`
	ParentSpanID   string                   `json:"parent_span_id,omitempty"`
	TaskID         string                   `json:"task_id,omitempty"`
	TokenID        int                      `json:"token_id"`
	Kind           string                   `json:"kind"`
	Surface        string                   `json:"surface"`
	Pack           string                   `json:"pack,omitempty"`
	Operation      string                   `json:"operation,omitempty"`
	PackDigest     string                   `json:"pack_digest,omitempty"`
	ContractDigest string                   `json:"contract_digest,omitempty"`
	GrantRevisions []string                 `json:"grant_revisions,omitempty"`
	Model          string                   `json:"model,omitempty"`
	JobID          string                   `json:"job_id,omitempty"`
	ResourceRef    string                   `json:"resource_ref,omitempty"`
	ResourceState  string                   `json:"resource_state,omitempty"`
	Method         string                   `json:"method"`
	StartedAt      time.Time                `json:"started_at"`
	FinishedAt     *time.Time               `json:"finished_at,omitempty"`
	LatencyMS      int64                    `json:"latency_ms"`
	HTTPStatus     int                      `json:"http_status"`
	Outcome        string                   `json:"outcome"`
	UpstreamCalled bool                     `json:"upstream_called"`
	Attempt        int                      `json:"attempt,omitempty"`
	CredentialRef  string                   `json:"credential_ref,omitempty"`
	Units          []serviceUnitObservation `json:"units,omitempty"`
	Usage          *usageMetrics            `json:"usage,omitempty"`
	Cost           *usageCost               `json:"cost,omitempty"`
}

func (store *localStore) initializeServiceTraces() error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS service_traces (span_id TEXT PRIMARY KEY, trace_id TEXT NOT NULL, token_id INTEGER NOT NULL, task_id TEXT NOT NULL, started_at INTEGER NOT NULL, finished INTEGER NOT NULL DEFAULT 0, payload TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS service_traces_owner_trace ON service_traces(token_id, trace_id, started_at)`,
		`CREATE INDEX IF NOT EXISTS service_traces_owner_task ON service_traces(token_id, task_id, started_at)`,
		`CREATE INDEX IF NOT EXISTS service_traces_started ON service_traces(started_at)`,
	} {
		if _, err := store.db.Exec(statement); err != nil {
			return err
		}
	}
	// A previous process cannot prove the outcome of an unfinished network call.
	_, err := store.db.Exec(`UPDATE service_traces SET payload = json_set(payload, '$.outcome', 'outcome_unknown'), finished = 1 WHERE finished = 0`)
	return err
}

func (store *localStore) saveServiceTrace(trace serviceTrace) error {
	data, err := json.Marshal(trace)
	if err != nil {
		return err
	}
	finished := trace.FinishedAt != nil
	_, err = store.db.Exec(`INSERT INTO service_traces(span_id, trace_id, token_id, task_id, started_at, finished, payload) VALUES(?,?,?,?,?,?,?) ON CONFLICT(span_id) DO UPDATE SET finished=excluded.finished, payload=excluded.payload`, trace.SpanID, trace.TraceID, trace.TokenID, trace.TaskID, trace.StartedAt.UnixNano(), finished, string(data))
	return err
}

func (store *localStore) serviceTraces(owner int, traceID, taskID string, page, size int) ([]serviceTrace, int64, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 50
	}
	where := ` WHERE (? <= 0 OR token_id = ?) AND (? = '' OR trace_id = ?) AND (? = '' OR task_id = ?)`
	args := []any{owner, owner, traceID, traceID, taskID, taskID}
	var total int64
	if err := store.db.QueryRow(`SELECT count(*) FROM service_traces`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := store.db.Query(`SELECT payload FROM service_traces`+where+` ORDER BY started_at DESC, span_id LIMIT ? OFFSET ?`, append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []serviceTrace{}
	for rows.Next() {
		var raw string
		var trace serviceTrace
		if err := rows.Scan(&raw); err != nil {
			return nil, 0, err
		}
		if err := json.Unmarshal([]byte(raw), &trace); err != nil {
			return nil, 0, err
		}
		items = append(items, trace)
	}
	return items, total, rows.Err()
}

func serviceTraceContext(c *gin.Context) *serviceTrace {
	value, _ := c.Get("localrouter_service_trace")
	trace, _ := value.(*serviceTrace)
	return trace
}

func serviceTraceMiddleware(runtime localRuntime, surface string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if runtime.workspace == nil || runtime.store == nil {
			c.Next()
			return
		}
		traceID := strings.ToLower(c.GetHeader("X-LocalRouter-Trace-Id"))
		parentID := ""
		parts := strings.Split(c.GetHeader("traceparent"), "-")
		if len(parts) == 4 && parts[0] == "00" && serviceTraceIDPattern.MatchString(parts[1]) && serviceSpanIDPattern.MatchString(parts[2]) && parts[1] != strings.Repeat("0", 32) && parts[2] != strings.Repeat("0", 16) {
			traceID, parentID = parts[1], parts[2]
		}
		if traceID == "" {
			traceID = serviceID("")
		}
		taskID := c.GetHeader("X-LocalRouter-Task-Id")
		if !serviceTraceIDPattern.MatchString(traceID) || traceID == strings.Repeat("0", 32) || (taskID != "" && !serviceCorrelationPattern.MatchString(taskID)) {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"success": false, "code": "invalid_trace_context", "message": "use a 32-character lowercase hexadecimal trace ID and a bounded task ID"})
			return
		}
		kind := "request"
		if surface == "w" || surface == "mcp" {
			kind = "projection"
		}
		tokenID := c.GetInt(tokenPolicyContextID)
		if tokenID == 0 {
			tokenID = c.GetInt("token_id")
			c.Set(tokenPolicyContextID, tokenID)
		}
		trace := &serviceTrace{SpanID: serviceID("")[:16], TraceID: traceID, ParentSpanID: parentID, TaskID: taskID, TokenID: tokenID, Kind: kind, Surface: surface, Method: c.Request.Method, StartedAt: time.Now().UTC(), Outcome: "running", GrantRevisions: runtime.workspace.grantRevisions(tokenID)}
		if runtime.protocols != nil {
			trace.ContractDigest = runtime.protocols.currentDigest()
		}
		if err := runtime.store.saveServiceTrace(*trace); err != nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"success": false, "code": "trace_unavailable", "message": "cannot record this request; no upstream call was made", "retryable": true})
			return
		}
		c.Set("localrouter_service_trace", trace)
		c.Header("X-LocalRouter-Trace-Id", trace.TraceID)
		c.Header("X-LocalRouter-Span-Id", trace.SpanID)
		defer func() {
			now := time.Now().UTC()
			trace.FinishedAt = &now
			trace.LatencyMS = now.Sub(trace.StartedAt).Milliseconds()
			trace.HTTPStatus = c.Writer.Status()
			trace.JobID = c.GetString("localrouter_workflow_job")
			trace.Model = c.GetString("localrouter_usage_model")
			trace.CredentialRef = protocolCredentialRef(c.GetString("localrouter_protocol_credential"))
			if c.GetString("localrouter_protocol_credential") == "" {
				trace.CredentialRef = ""
			}
			if trace.Outcome == "running" {
				trace.Outcome = "response_received"
				if trace.HTTPStatus >= 400 {
					trace.Outcome = "rejected"
					if trace.UpstreamCalled {
						trace.Outcome = "upstream_error"
					}
				}
			}
			if c.GetString("localrouter_protocol_outcome") == "unknown" {
				trace.Outcome = "outcome_unknown"
			}
			if err := runtime.store.saveServiceTrace(*trace); err != nil {
				log.Print("LocalRouter: trace completion could not be persisted; initial request record is retained")
			}
		}()
		c.Next()
	}
}

func bindServiceTrace(c *gin.Context, definition protocolDefinition, operation string) {
	if trace := serviceTraceContext(c); trace != nil {
		trace.Pack, trace.Operation = definition.ID, operation
		trace.PackDigest = serviceDigest(definition)
	}
}

func serviceCallRuntime(runtime localRuntime, c *gin.Context) localRuntime {
	runtime.callOwner = c.GetInt(tokenPolicyContextID)
	if trace := serviceTraceContext(c); trace != nil {
		runtime.callSurface = trace.Surface
	}
	runtime.workflowContext = c.Request.Context()
	if trace := serviceTraceContext(c); trace != nil {
		runtime.callTraceID, runtime.callParentID, runtime.callTaskID = trace.TraceID, trace.SpanID, trace.TaskID
	}
	return runtime
}

func (registry *protocolRegistry) startServiceAttempt(c *gin.Context, attempt int) (func(int, bool, error), error) {
	trace := serviceTraceContext(c)
	if trace == nil || registry.workspace == nil {
		return func(int, bool, error) {}, nil
	}
	child := *trace
	child.UpstreamCalled = true
	child.SpanID, child.ParentSpanID, child.Kind = serviceID("")[:16], trace.SpanID, "attempt"
	child.Attempt, child.StartedAt, child.FinishedAt = attempt, time.Now().UTC(), nil
	child.Cost, child.Usage, child.Units = nil, nil, nil
	child.CredentialRef = protocolCredentialRef(c.GetString("localrouter_protocol_credential"))
	if err := registry.workspace.store.saveServiceTrace(child); err != nil {
		return nil, err
	}
	trace.UpstreamCalled = true
	return func(status int, wrote bool, err error) {
		now := time.Now().UTC()
		child.FinishedAt = &now
		child.LatencyMS = now.Sub(child.StartedAt).Milliseconds()
		child.HTTPStatus = status
		child.Outcome = "response_headers_received"
		if err != nil {
			child.Outcome = "transport_failed"
			if wrote {
				child.Outcome = "outcome_unknown"
			}
		}
		if saveErr := registry.workspace.store.saveServiceTrace(child); saveErr != nil {
			log.Print("LocalRouter: upstream attempt completion could not be persisted")
		}
	}, nil
}

func validateServiceMetering(metering *serviceMetering) error {
	if metering == nil {
		return nil
	}
	if len(metering.Units) > 16 {
		return errors.New("metering allows at most 16 unit mappings")
	}
	for _, path := range []string{metering.ResourceIDPath, metering.StatePath} {
		if len(path) > 200 || strings.ContainsAny(path, "\r\n") {
			return errors.New("invalid metering extraction path")
		}
	}
	for _, mapping := range metering.Units {
		if !serviceCorrelationPattern.MatchString(mapping.Unit) || len(mapping.Path) == 0 || len(mapping.Path) > 200 || (mapping.Source != "request" && mapping.Source != "response") || (mapping.Mode != "delta" && mapping.Mode != "snapshot") {
			return errors.New("metering units require unit, path, request/response source and delta/snapshot mode")
		}
		if mapping.Mode == "snapshot" && metering.ResourceIDPath == "" {
			return errors.New("snapshot metering requires a stable resource_id_path")
		}
	}
	return nil
}

func serviceMeteringSchema() gin.H {
	return maintenanceObjectSchema(gin.H{
		"resource_id_path": maintenanceString("Response JSON path to a stable resource identifier, stored only as a hashed reference"),
		"state_path":       maintenanceString("Response JSON path to provider task state"),
		"units":            gin.H{"type": "array", "maxItems": 16, "items": maintenanceObjectSchema(gin.H{"unit": maintenanceString("Resource unit, e.g. video-second or page"), "path": maintenanceString("Numeric JSON extraction path"), "source": maintenanceEnum("Evidence source", "request", "response"), "mode": maintenanceEnum("Delta per request or snapshot per stable resource", "delta", "snapshot")}, "unit", "path", "source", "mode")},
	})
}

func observeServiceUnits(c *gin.Context, route protocolRoute, body []byte, source string) {
	trace := serviceTraceContext(c)
	if trace == nil || route.Metering == nil || !json.Valid(body) {
		return
	}
	metering := route.Metering
	if source == "response" {
		if resource := gjson.GetBytes(body, metering.ResourceIDPath); metering.ResourceIDPath != "" && resource.Exists() && resource.Type == gjson.String && resource.String() != "" {
			// Provider resource identifiers can contain private data. A stable
			// Pack-scoped reference supports deduplication without exposing it.
			trace.ResourceRef = "resource-" + serviceDigest([]string{trace.Pack, resource.String()})[:24]
		}
		if state := gjson.GetBytes(body, metering.StatePath); metering.StatePath != "" && state.Type == gjson.String && serviceCorrelationPattern.MatchString(state.String()) {
			trace.ResourceState = state.String()
		}
	}
	for _, mapping := range metering.Units {
		if mapping.Source != source {
			continue
		}
		value := gjson.GetBytes(body, mapping.Path)
		if value.Type != gjson.Number || value.Num < 0 || math.IsInf(value.Num, 0) || math.IsNaN(value.Num) {
			continue
		}
		trace.Units = append(trace.Units, serviceUnitObservation{Unit: mapping.Unit, Quantity: value.Num, Source: source, Mode: mapping.Mode})
	}
}

func traceResponseHeaders(trace serviceTrace) string {
	return fmt.Sprintf("00-%s-%s-00", trace.TraceID, trace.SpanID)
}

func attachWorkflowTrace(c *gin.Context, job *protocolWorkflowJob) {
	if trace := serviceTraceContext(c); trace != nil {
		job.TraceID, job.ParentSpanID, job.TaskID = trace.TraceID, trace.SpanID, trace.TaskID
	}
}

func (hub *serviceWorkspace) recordWorkflowTrace(job protocolWorkflowJob, mode string) {
	if !serviceTraceIDPattern.MatchString(job.TraceID) {
		return
	}
	now := time.Now().UTC()
	trace := serviceTrace{SpanID: serviceID("")[:16], TraceID: job.TraceID, ParentSpanID: job.ParentSpanID, TaskID: job.TaskID, TokenID: job.OwnerTokenID, Kind: "workflow", Surface: "w", Pack: job.ProtocolID, Operation: "workflow." + job.WorkflowID + "." + mode, JobID: job.ID, ResourceState: job.State, ResourceRef: "resource-" + serviceDigest([]string{job.ProtocolID, job.ResourceID})[:24], StartedAt: now, FinishedAt: &now, HTTPStatus: job.LastHTTPCode, Outcome: job.State, ContractDigest: hub.registry.currentDigest(), GrantRevisions: hub.grantRevisions(job.OwnerTokenID)}
	if err := hub.store.saveServiceTrace(trace); err != nil {
		log.Print("LocalRouter: workflow trace completion could not be persisted")
	}
}
