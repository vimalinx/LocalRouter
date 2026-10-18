package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type protocolWorkflowJob struct {
	TraceID         string                     `json:"trace_id,omitempty"`
	ParentSpanID    string                     `json:"parent_span_id,omitempty"`
	TaskID          string                     `json:"task_id,omitempty"`
	ExecutionID     string                     `json:"execution_id,omitempty"`
	CancelRequested bool                       `json:"cancel_requested,omitempty"`
	Cancelling      bool                       `json:"cancelling,omitempty"`
	CancelAttempts  int                        `json:"cancel_attempts,omitempty"`
	Cancellable     bool                       `json:"cancellable,omitempty"`
	ID              string                     `json:"id"`
	ProtocolID      string                     `json:"protocol_id"`
	WorkflowID      string                     `json:"workflow_id"`
	ResourceID      string                     `json:"resource_id"`
	State           string                     `json:"state"`
	UpstreamState   string                     `json:"upstream_state,omitempty"`
	Result          json.RawMessage            `json:"result,omitempty"`
	Attempts        int                        `json:"attempts"`
	MaxAttempts     int                        `json:"max_attempts"`
	CreatedAt       time.Time                  `json:"created_at"`
	UpdatedAt       time.Time                  `json:"updated_at"`
	NextPollAt      time.Time                  `json:"next_poll_at,omitempty"`
	LastHTTPCode    int                        `json:"last_http_code,omitempty"`
	Error           string                     `json:"error,omitempty"`
	Input           json.RawMessage            `json:"input,omitempty"`
	Context         map[string]json.RawMessage `json:"context,omitempty"`
	CurrentStep     string                     `json:"current_step,omitempty"`
	CallbackToken   string                     `json:"callback_token,omitempty"`
	CallbackURL     string                     `json:"callback_url,omitempty"`
	WaitingCallback bool                       `json:"waiting_callback,omitempty"`
	OwnerTokenID    int                        `json:"owner_token_id,omitempty"`
}

type protocolWorkflowState struct {
	Jobs map[string]protocolWorkflowJob `json:"jobs"`
}

func (registry *protocolRegistry) workflowStatePath(protocolID string) string {
	return filepath.Join(registry.stateDir, "workflow-state", protocolID+".json")
}

func (registry *protocolRegistry) loadWorkflowState(protocolID string) (protocolWorkflowState, error) {
	state := protocolWorkflowState{Jobs: make(map[string]protocolWorkflowJob)}
	path := registry.workflowStatePath(protocolID)
	exists, err := inspectPrivateRegularFile(path, "protocol workflow state file")
	if err != nil {
		return state, err
	}
	if !exists {
		return state, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, err
	}
	if state.Jobs == nil {
		state.Jobs = make(map[string]protocolWorkflowJob)
	}
	return state, nil
}

func (registry *protocolRegistry) saveWorkflowState(protocolID string, state protocolWorkflowState) error {
	dir := filepath.Dir(registry.workflowStatePath(protocolID))
	if err := ensurePrivateDirectory(dir); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(dir, protocolID+"-*.tmp")
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
	if err := os.Rename(temporaryPath, registry.workflowStatePath(protocolID)); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func (registry *protocolRegistry) workflowDefinition(protocolID, workflowID string) (protocolDefinition, protocolWorkflow, bool) {
	definition, ok := registry.get(protocolID)
	if !ok {
		return protocolDefinition{}, protocolWorkflow{}, false
	}
	for _, workflow := range definition.Workflows {
		if workflow.ID == workflowID {
			return definition, workflow, true
		}
	}
	return protocolDefinition{}, protocolWorkflow{}, false
}

func operationRoute(definition protocolDefinition, operationID string) (protocolRoute, bool) {
	for _, route := range definition.Routes {
		if route.OperationID == operationID {
			return route, true
		}
	}
	return protocolRoute{}, false
}

func (registry *protocolRegistry) handleWorkflowCreate(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		definition, workflow, ok := registry.workflowDefinition(c.Param("protocol"), c.Param("workflow"))
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "unknown workflow"})
			return
		}
		operation := "workflow." + workflow.ID + ".create"
		bindServiceTrace(c, definition, operation)
		if registry.policies != nil {
			release, allowed := registry.policies.authorizeProjection(c, "w", definition.ID, operation, "")
			if !allowed {
				return
			}
			defer release()
		}
		if registry.events != nil {
			started := time.Now()
			defer registry.events.recordRequest(c, "w", definition.ID, operation, started)
		}
		if len(workflow.Steps) > 0 {
			registry.handleGraphWorkflowCreate(c, runtime, definition, workflow)
			return
		}
		ready, status := registry.readiness(definition)
		if !ready {
			view, _ := registry.viewFor(definition.ID)
			registry.writeReadinessError(c, view, workflow.CreateOperation, status)
			return
		}
		createRoute, _ := operationRoute(definition, workflow.CreateOperation)
		body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxProtocolBodyBytes))
		if err != nil {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"success": false, "message": "workflow request body is too large"})
			return
		}
		jobID, err := newWorkflowJobID()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot allocate workflow job"})
			return
		}
		now := time.Now().UTC()
		job := protocolWorkflowJob{
			ID: jobID, ProtocolID: definition.ID, WorkflowID: workflow.ID,
			State: "creating", ExecutionID: jobID, MaxAttempts: workflow.MaxPollAttempts,
			CreatedAt: now, UpdatedAt: now, OwnerTokenID: c.GetInt(tokenPolicyContextID),
		}
		attachWorkflowTrace(c, &job)
		// Persist intent before dispatch. Interrupted claims must be reconciled,
		// never treated as permission to create a replacement resource.
		if err := registry.editWorkflowState(definition.ID, func(state *protocolWorkflowState) (bool, error) {
			state.Jobs[job.ID] = job
			return true, nil
		}); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "code": "workflow_intent_unavailable", "retryable": true, "message": "cannot persist workflow intent; no upstream call was made"})
			return
		}
		c.Set("localrouter_workflow_job", job.ID)
		responseCode, responseBody, contentType, callErr := callLocalProtocol(serviceCallRuntime(runtime, c), definition.ID, http.MethodPost, createRoute.Path, c.Request.URL.RawQuery, c.GetHeader("Content-Type"), body)
		job.ExecutionID, job.LastHTTPCode = "", responseCode
		job.UpdatedAt = time.Now().UTC()
		job.ResourceID = gjson.GetBytes(responseBody, workflow.ResourceIDPath).String()
		switch {
		case errors.Is(callErr, errWorkflowOutcomeUnknown):
			job.State, job.Error = "outcome_unknown", errWorkflowOutcomeUnknown.Error()
		case callErr != nil || responseCode < 200 || responseCode >= 300:
			job.State, job.Error = "failed", "workflow create operation failed"
		case job.ResourceID == "":
			job.State, job.Error = "outcome_unknown", "create was accepted without a resource id; reconcile upstream state"
		default:
			job.State = "pending"
			job.NextPollAt = job.UpdatedAt.Add(time.Duration(workflow.PollIntervalMS) * time.Millisecond)
		}
		stateErr := registry.editWorkflowState(definition.ID, func(state *protocolWorkflowState) (bool, error) {
			current, ok := state.Jobs[job.ID]
			if !ok || current.ExecutionID != jobID {
				return false, errors.New("workflow intent changed before commit")
			}
			state.Jobs[job.ID] = job
			return true, nil
		})
		if stateErr != nil || job.State == "outcome_unknown" {
			job.State = "outcome_unknown"
			c.JSON(http.StatusBadGateway, gin.H{"success": false, "code": "workflow_outcome_unknown", "retryable": false, "next_actor": "human", "next_action": "reconcile this existing job with the provider; do not recreate it", "data": publicWorkflowJob(job)})
			return
		}
		if job.State == "failed" {
			if responseCode >= 400 && len(responseBody) > 0 {
				c.Data(responseCode, contentType, responseBody)
			} else {
				c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": job.Error, "data": publicWorkflowJob(job)})
			}
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"object": "workflow.job", "data": publicWorkflowJob(job), "create_response": json.RawMessage(responseBody)})
	}
}

func (registry *protocolRegistry) handleWorkflowGet(runtime localRuntime) gin.HandlerFunc {
	return registry.handleWorkflowReadOrAdvance(runtime, false)
}

func (registry *protocolRegistry) handleWorkflowAdvance(runtime localRuntime) gin.HandlerFunc {
	return registry.handleWorkflowReadOrAdvance(runtime, true)
}

func (registry *protocolRegistry) handleWorkflowReadOrAdvance(runtime localRuntime, advance bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		definition, workflow, ok := registry.workflowDefinition(c.Param("protocol"), c.Param("workflow"))
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "unknown workflow"})
			return
		}
		operation := "workflow." + workflow.ID + ".get"
		if advance {
			operation = "workflow." + workflow.ID + ".advance"
		}
		if registry.policies != nil {
			release, allowed := registry.policies.authorizeProjection(c, "w", definition.ID, operation, "")
			if !allowed {
				return
			}
			defer release()
		}
		if registry.events != nil {
			started := time.Now()
			defer registry.events.recordRequest(c, "w", definition.ID, operation, started)
		}
		c.Set("localrouter_workflow_job", c.Param("job"))
		mode := "observe"
		if advance {
			mode = "advance"
		}
		registry.serveStoredWorkflow(c, runtime, definition, workflow, mode)
	}
}

func (registry *protocolRegistry) handleWorkflowCancel(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		definition, workflow, ok := registry.workflowDefinition(c.Param("protocol"), c.Param("workflow"))
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "unknown workflow"})
			return
		}
		operation := "workflow." + workflow.ID + ".cancel"
		if registry.policies != nil {
			release, allowed := registry.policies.authorizeProjection(c, "w", definition.ID, operation, "")
			if !allowed {
				return
			}
			defer release()
		}
		if registry.events != nil {
			started := time.Now()
			defer registry.events.recordRequest(c, "w", definition.ID, operation, started)
		}
		c.Set("localrouter_workflow_job", c.Param("job"))
		if len(workflow.Steps) > 0 {
			registry.handleGraphWorkflowCancel(c, runtime, definition, workflow)
			return
		}
		if workflow.CancelOperation == "" {
			c.JSON(http.StatusMethodNotAllowed, gin.H{"success": false, "message": "workflow does not define cancellation"})
			return
		}
		registry.serveStoredWorkflow(c, runtime, definition, workflow, "cancel")
	}
}

func workflowJobOwnedBy(c *gin.Context, job protocolWorkflowJob) bool {
	return job.OwnerTokenID == 0 || job.OwnerTokenID == c.GetInt(tokenPolicyContextID)
}

func (registry *protocolRegistry) handleWorkflowList(c *gin.Context) {
	definition, workflow, ok := registry.workflowDefinition(c.Param("protocol"), c.Param("workflow"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "unknown workflow"})
		return
	}
	operation := "workflow." + workflow.ID + ".list"
	if registry.policies != nil {
		release, allowed := registry.policies.authorizeProjection(c, "w", definition.ID, operation, "")
		if !allowed {
			return
		}
		defer release()
	}
	state, err := registry.loadWorkflowState(definition.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot read workflow state"})
		return
	}
	jobs := make([]protocolWorkflowJob, 0)
	for _, job := range state.Jobs {
		if job.WorkflowID == workflow.ID && workflowJobOwnedBy(c, job) {
			jobs = append(jobs, publicWorkflowJob(job))
		}
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].CreatedAt.After(jobs[j].CreatedAt) })
	c.JSON(http.StatusOK, gin.H{"object": "list", "data": jobs})
}

func (registry *protocolRegistry) handleAdminWorkflowJobs(c *gin.Context) {
	jobs := make([]protocolWorkflowJob, 0)
	for _, view := range registry.views() {
		state, err := registry.loadWorkflowState(view.ID)
		if err != nil {
			continue
		}
		for _, job := range state.Jobs {
			copy := job
			copy.CallbackToken = ""
			copy.CallbackURL = ""
			copy.ExecutionID = ""
			if _, workflow, ok := registry.workflowDefinition(job.ProtocolID, job.WorkflowID); ok {
				copy.Cancellable = job.State != "succeeded" && job.State != "cancelled" && (len(workflow.Steps) > 0 || workflow.CancelOperation != "")
			}
			copy.Input = nil
			jobs = append(jobs, copy)
		}
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].CreatedAt.After(jobs[j].CreatedAt) })
	c.JSON(http.StatusOK, gin.H{"success": true, "data": jobs})
}

func callLocalProtocol(runtime localRuntime, protocolID, method, path, rawQuery, contentType string, body []byte) (int, []byte, string, error) {
	if runtime.callOwner > 0 && runtime.store != nil && runtime.protocols != nil {
		return callOwnedProtocol(runtime, protocolID, method, path, rawQuery, contentType, body)
	}
	host := runtime.config.Host
	if host == "" {
		host = "127.0.0.1"
	}
	address := net.JoinHostPort(host, fmt.Sprintf("%d", runtime.config.Port))
	target := url.URL{Scheme: "http", Host: address, Path: "/p/" + protocolID + path, RawQuery: rawQuery}
	ctx := runtime.workflowContext
	if ctx == nil {
		ctx = context.Background()
	}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(body))
	if err != nil {
		return 0, nil, "", err
	}
	callToken := runtime.apiToken
	if runtime.callOwner > 0 && runtime.store != nil {
		token, err := runtime.store.tokenByID(runtime.rootUser.ID, runtime.callOwner, true)
		if err != nil || token.Status != localStatusEnabled || (token.ExpiredTime >= 0 && token.ExpiredTime <= time.Now().Unix()) {
			return http.StatusForbidden, nil, "", errors.New("workflow owner Token is unavailable")
		}
		callToken = token.Key
	}
	request.Header.Set("Authorization", "Bearer "+callToken)
	if serviceTraceIDPattern.MatchString(runtime.callTraceID) && serviceSpanIDPattern.MatchString(runtime.callParentID) {
		request.Header.Set("traceparent", "00-"+runtime.callTraceID+"-"+runtime.callParentID+"-00")
	}
	if runtime.callTaskID != "" {
		request.Header.Set("X-LocalRouter-Task-Id", runtime.callTaskID)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	client := &http.Client{Timeout: 10 * time.Minute, CheckRedirect: rejectUpstreamRedirect}
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, "", errors.Join(errWorkflowOutcomeUnknown, err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxProtocolBodyBytes+1))
	if err != nil || len(responseBody) > maxProtocolBodyBytes {
		return response.StatusCode, nil, response.Header.Get("Content-Type"), errWorkflowOutcomeUnknown
	}
	if response.StatusCode >= 400 && gjson.GetBytes(responseBody, "code").String() == "upstream_outcome_unknown" {
		return response.StatusCode, responseBody, response.Header.Get("Content-Type"), errWorkflowOutcomeUnknown
	}
	return response.StatusCode, responseBody, response.Header.Get("Content-Type"), nil
}

func newWorkflowJobID() (string, error) {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return "job-" + hex.EncodeToString(random), nil
}

func classifyWorkflowState(workflow protocolWorkflow, upstream string) string {
	for state, values := range map[string][]string{
		"pending": workflow.PendingValues, "running": workflow.RunningValues,
		"succeeded": workflow.SuccessValues, "failed": workflow.FailureValues,
	} {
		for _, value := range values {
			if strings.EqualFold(value, upstream) {
				return state
			}
		}
	}
	return "unknown"
}

func workflowJobTerminal(state string) bool {
	switch state {
	case "succeeded", "failed", "cancelled", "timed_out", "outcome_unknown":
		return true
	default:
		return false
	}
}
