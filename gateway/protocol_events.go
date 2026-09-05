package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const maxProtocolEventFileBytes = 16 << 20

type protocolEvent struct {
	AccountingVersion int           `json:"accounting_version,omitempty"`
	ID                string        `json:"id"`
	CreatedAt         time.Time     `json:"created_at"`
	RequestID         string        `json:"request_id,omitempty"`
	TokenID           int           `json:"token_id,omitempty"`
	Surface           string        `json:"surface"`
	ProtocolID        string        `json:"protocol_id,omitempty"`
	OperationID       string        `json:"operation_id,omitempty"`
	Model             string        `json:"model,omitempty"`
	WorkflowID        string        `json:"workflow_id,omitempty"`
	JobID             string        `json:"job_id,omitempty"`
	Method            string        `json:"method"`
	Path              string        `json:"path"`
	Status            int           `json:"status"`
	LatencyMS         int64         `json:"latency_ms"`
	ResponseBytes     int           `json:"response_bytes"`
	Attempts          int           `json:"attempts,omitempty"`
	CredentialRef     string        `json:"credential_ref,omitempty"`
	Target            string        `json:"target,omitempty"`
	Outcome           string        `json:"outcome,omitempty"`
	Usage             *usageMetrics `json:"usage,omitempty"`
	Cost              *usageCost    `json:"cost,omitempty"`
}

type protocolEventStore struct {
	path string
	mu   sync.Mutex
}

func newProtocolEventStore(stateDir string) (*protocolEventStore, error) {
	store := &protocolEventStore{path: filepath.Join(stateDir, "protocol-events.jsonl")}
	file, err := openPrivateAppendFile(store.path, "protocol event file")
	if err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	return store, nil
}

func (store *protocolEventStore) append(event protocolEvent) {
	if store == nil {
		return
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if exists, err := inspectPrivateRegularFile(store.path, "protocol event file"); err != nil {
		return
	} else if exists {
		info, statErr := os.Lstat(store.path)
		if statErr != nil {
			return
		}
		if info.Size() >= maxProtocolEventFileBytes {
			rotated := store.path + ".1"
			_ = os.Remove(rotated)
			if os.Rename(store.path, rotated) != nil {
				return
			}
		}
	}
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	file, err := openPrivateAppendFile(store.path, "protocol event file")
	if err != nil {
		return
	}
	_, _ = file.Write(append(data, '\n'))
	_ = file.Close()
}

func eventRequestID(c *gin.Context) string {
	for _, key := range []string{"request_id", "id", "requestId"} {
		if value := c.GetString(key); value != "" {
			return value
		}
	}
	return c.Writer.Header().Get("X-Request-Id")
}

func (store *protocolEventStore) recordRequest(c *gin.Context, surface, protocolID, operationID string, started time.Time) {
	store.recordRequestWithDefinition(c, surface, protocolDefinition{ID: protocolID}, operationID, started)
}

func (store *protocolEventStore) recordRequestWithDefinition(c *gin.Context, surface string, definition protocolDefinition, operationID string, started time.Time) {
	status := c.Writer.Status()
	if status == 0 {
		status = http.StatusOK
	}
	attempts := c.GetInt("localrouter_protocol_attempts")
	if attempts == 0 {
		attempts = 1
	}
	credentialRef := ""
	if credentialID := c.GetString("localrouter_protocol_credential"); credentialID != "" {
		credentialRef = protocolCredentialRef(credentialID)
	}
	model := strings.TrimSpace(c.GetString("localrouter_usage_model"))
	usage, _ := c.Get("localrouter_usage_metrics")
	metrics, _ := usage.(usageMetrics)
	metrics = metrics.normalized()
	var usageView *usageMetrics
	if metrics.hasTokens() || metrics.ReportedCostUSD != nil {
		copy := metrics
		usageView = &copy
	}
	event := protocolEvent{
		AccountingVersion: 1,
		ID:                fmt.Sprintf("PVE-%s-%06d", time.Now().UTC().Format("20060102T150405.000000000Z"), time.Now().UnixNano()%1000000),
		CreatedAt:         time.Now().UTC(), RequestID: eventRequestID(c), TokenID: c.GetInt(tokenPolicyContextID),
		Surface: surface, ProtocolID: definition.ID, OperationID: operationID, Model: model,
		Method: c.Request.Method, Path: c.Request.URL.Path, Status: status,
		LatencyMS: time.Since(started).Milliseconds(), ResponseBytes: c.Writer.Size(), Attempts: attempts,
		CredentialRef: credentialRef, Target: c.GetString("localrouter_protocol_target"), Outcome: c.GetString("localrouter_protocol_outcome"),
		WorkflowID: c.Param("workflow"), JobID: c.GetString("localrouter_workflow_job"),
		Usage: usageView,
	}
	event.Cost = protocolUsageCost(definition, operationID, model, metrics, status > 0 && status < http.StatusBadRequest)
	if trace := serviceTraceContext(c); trace != nil && trace.Kind == "request" {
		trace.Usage, trace.Cost = usageView, event.Cost
	}
	store.append(event)
}

func (store *protocolEventStore) read(limit int) ([]protocolEvent, error) {
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	exists, err := inspectPrivateRegularFile(store.path, "protocol event file")
	if err != nil {
		return nil, err
	}
	if !exists {
		return []protocolEvent{}, nil
	}
	file, err := os.Open(store.path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	events := make([]protocolEvent, 0, limit)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		var event protocolEvent
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		if event.CredentialRef != "" && !isProtocolCredentialRef(event.CredentialRef) {
			event.CredentialRef = protocolCredentialRef(event.CredentialRef)
		}
		if event.Target != "" && !protocolOperationIDPattern.MatchString(event.Target) {
			event.Target = ""
		}
		if len(events) == limit {
			copy(events, events[1:])
			events[len(events)-1] = event
		} else {
			events = append(events, event)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	for left, right := 0, len(events)-1; left < right; left, right = left+1, right-1 {
		events[left], events[right] = events[right], events[left]
	}
	return events, nil
}

func isProtocolCredentialRef(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func (store *protocolEventStore) handleList(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	events, err := store.read(limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot read protocol events"})
		return
	}
	protocolID := strings.TrimSpace(c.Query("protocol"))
	operationID := strings.TrimSpace(c.Query("operation"))
	if protocolID != "" || operationID != "" {
		filtered := events[:0]
		for _, event := range events {
			if protocolID != "" && event.ProtocolID != protocolID {
				continue
			}
			if operationID != "" && event.OperationID != operationID {
				continue
			}
			filtered = append(filtered, event)
		}
		events = filtered
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": events})
}
