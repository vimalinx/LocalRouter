package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const defaultGraphWorkflowAttempts = 1000

func validateProtocolGraphWorkflow(workflow *protocolWorkflow, operations map[string]protocolRoute) error {
	if len(workflow.Steps) == 0 {
		return errors.New("steps are required")
	}
	if workflow.InitialStep == "" {
		workflow.InitialStep = workflow.Steps[0].ID
	}
	if workflow.MaxPollAttempts == 0 {
		workflow.MaxPollAttempts = defaultGraphWorkflowAttempts
	}
	if workflow.MaxPollAttempts < 1 || workflow.MaxPollAttempts > 10000 {
		return errors.New("max_poll_attempts must be between 1 and 10000")
	}
	steps := make(map[string]protocolWorkflowStep, len(workflow.Steps))
	for index := range workflow.Steps {
		step := &workflow.Steps[index]
		if !protocolOperationIDPattern.MatchString(step.ID) || steps[step.ID].ID != "" {
			return fmt.Errorf("step %d has an invalid or duplicate id", index+1)
		}
		if step.Kind == "" {
			step.Kind = "operation"
		}
		if step.WaitMS < 0 || step.WaitMS > 24*60*60*1000 {
			return fmt.Errorf("step %s: wait_ms is out of range", step.ID)
		}
		switch step.Kind {
		case "operation":
			if len(step.Parallel) > 0 {
				return fmt.Errorf("step %s: operation steps cannot define parallel calls", step.ID)
			}
			if err := validateProtocolWorkflowCall(protocolWorkflowCall{
				ID: step.ID, Operation: step.Operation, Method: step.Method,
				PathParams: step.PathParams, Query: step.Query, BodyExpr: step.BodyExpr,
			}, operations); err != nil {
				return fmt.Errorf("step %s: %w", step.ID, err)
			}
		case "parallel":
			if step.Operation != "" || len(step.Parallel) == 0 || len(step.Parallel) > 32 {
				return fmt.Errorf("step %s: parallel steps require 1-32 calls and no operation", step.ID)
			}
			seenCalls := make(map[string]bool)
			for callIndex, call := range step.Parallel {
				if !protocolOperationIDPattern.MatchString(call.ID) || seenCalls[call.ID] {
					return fmt.Errorf("step %s call %d has an invalid or duplicate id", step.ID, callIndex+1)
				}
				seenCalls[call.ID] = true
				if err := validateProtocolWorkflowCall(call, operations); err != nil {
					return fmt.Errorf("step %s call %s: %w", step.ID, call.ID, err)
				}
			}
		case "callback":
			if step.Operation != "" || len(step.Parallel) > 0 || step.BodyExpr != "" || len(step.PathParams) > 0 || len(step.Query) > 0 {
				return fmt.Errorf("step %s: callback steps cannot issue operations", step.ID)
			}
		default:
			return fmt.Errorf("step %s: kind must be operation, parallel, or callback", step.ID)
		}
		if len(step.Transitions) == 0 {
			return fmt.Errorf("step %s: at least one transition is required", step.ID)
		}
		for transitionIndex, transition := range step.Transitions {
			if transition.When != "" {
				if err := validateProtocolExpression(transition.When); err != nil {
					return fmt.Errorf("step %s transition %d when: %w", step.ID, transitionIndex+1, err)
				}
			}
			if (transition.To == "") == (transition.Terminal == "") {
				return fmt.Errorf("step %s transition %d must define exactly one of to or terminal", step.ID, transitionIndex+1)
			}
			if transition.Terminal != "" && transition.Terminal != "succeeded" && transition.Terminal != "failed" && transition.Terminal != "cancelled" {
				return fmt.Errorf("step %s transition %d has an invalid terminal state", step.ID, transitionIndex+1)
			}
			if transition.ResultExpr != "" {
				if err := validateProtocolExpression(transition.ResultExpr); err != nil {
					return fmt.Errorf("step %s transition %d result_expr: %w", step.ID, transitionIndex+1, err)
				}
			}
		}
		steps[step.ID] = *step
	}
	if _, ok := steps[workflow.InitialStep]; !ok {
		return fmt.Errorf("initial_step references unknown step %q", workflow.InitialStep)
	}
	if workflow.CancelStep != "" {
		if _, ok := steps[workflow.CancelStep]; !ok {
			return fmt.Errorf("cancel_step references unknown step %q", workflow.CancelStep)
		}
	}
	for _, step := range workflow.Steps {
		for _, transition := range step.Transitions {
			if transition.To != "" {
				if _, ok := steps[transition.To]; !ok {
					return fmt.Errorf("step %s references unknown step %q", step.ID, transition.To)
				}
			}
		}
	}
	if !graphWorkflowCanReachTerminal(workflow.InitialStep, steps, make(map[string]bool)) {
		return errors.New("initial_step cannot reach a terminal transition")
	}
	if workflow.CancelStep != "" && !graphWorkflowCanReachTerminal(workflow.CancelStep, steps, make(map[string]bool)) {
		return errors.New("cancel_step cannot reach a terminal transition")
	}
	return nil
}

func graphWorkflowCanReachTerminal(stepID string, steps map[string]protocolWorkflowStep, visiting map[string]bool) bool {
	if visiting[stepID] {
		return false
	}
	visiting[stepID] = true
	defer delete(visiting, stepID)
	for _, transition := range steps[stepID].Transitions {
		if transition.Terminal != "" || graphWorkflowCanReachTerminal(transition.To, steps, visiting) {
			return true
		}
	}
	return false
}

func validateProtocolWorkflowCall(call protocolWorkflowCall, operations map[string]protocolRoute) error {
	route, ok := operations[call.Operation]
	if !ok {
		return fmt.Errorf("references unknown operation %q", call.Operation)
	}
	method := strings.ToUpper(call.Method)
	if method == "" && len(route.Methods) > 0 {
		method = route.Methods[0]
	}
	if !routeSupportsMethod(route, method) {
		return fmt.Errorf("method %s is not supported by operation %s", method, call.Operation)
	}
	params := protocolPathParameterNames(route.Path)
	for name := range params {
		if call.PathParams[name] == "" {
			return fmt.Errorf("path_params.%s is required", name)
		}
	}
	for name, expression := range call.PathParams {
		if !params[name] {
			return fmt.Errorf("path_params.%s is not present in operation path", name)
		}
		if err := validateProtocolExpression(expression); err != nil {
			return fmt.Errorf("path_params.%s: %w", name, err)
		}
	}
	for name, expression := range call.Query {
		if strings.TrimSpace(name) == "" || strings.ContainsAny(name, "&#=?") {
			return fmt.Errorf("query key %q is unsafe", name)
		}
		if err := validateProtocolExpression(expression); err != nil {
			return fmt.Errorf("query.%s: %w", name, err)
		}
	}
	if call.BodyExpr != "" {
		if err := validateProtocolExpression(call.BodyExpr); err != nil {
			return fmt.Errorf("body_expr: %w", err)
		}
	}
	return nil
}

func (registry *protocolRegistry) handleGraphWorkflowCreate(c *gin.Context, runtime localRuntime, definition protocolDefinition, workflow protocolWorkflow) {
	ready, status := registry.readiness(definition)
	if !ready {
		view, _ := registry.viewFor(definition.ID)
		registry.writeReadinessError(c, view, "workflow."+workflow.ID+".create", status)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxProtocolBodyBytes))
	if err != nil {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"success": false, "message": "workflow request body is too large"})
		return
	}
	if len(body) == 0 {
		body = []byte("{}")
	}
	if !json.Valid(body) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "graph workflow input must be JSON"})
		return
	}
	jobID, err := newWorkflowJobID()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot allocate workflow job"})
		return
	}
	callbackToken, err := newWorkflowCallbackToken()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot allocate callback token"})
		return
	}
	callbackBaseURL, err := workflowCallbackBaseURL(c, runtime)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"success": false, "message": err.Error()})
		return
	}
	now := time.Now().UTC()
	job := protocolWorkflowJob{
		ID: jobID, ProtocolID: definition.ID, WorkflowID: workflow.ID,
		State: "running", Attempts: 0, MaxAttempts: workflow.MaxPollAttempts,
		CreatedAt: now, UpdatedAt: now, NextPollAt: now,
		Input: append(json.RawMessage(nil), body...), Context: make(map[string]json.RawMessage),
		CurrentStep: workflow.InitialStep, CallbackToken: callbackToken,
		CallbackURL:  fmt.Sprintf("%s/w/callback/%s/%s/%s/%s", callbackBaseURL, definition.ID, workflow.ID, jobID, callbackToken),
		OwnerTokenID: c.GetInt(tokenPolicyContextID),
	}
	registry.workflowMu.Lock()
	defer registry.workflowMu.Unlock()
	stateLock, lockErr := registry.lockProtocolState("workflow", definition.ID)
	if lockErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot lock workflow state"})
		return
	}
	defer unlockProtocolState(stateLock)
	state, err := registry.loadWorkflowState(definition.ID)
	if err == nil {
		state.Jobs[job.ID] = job
		err = registry.saveWorkflowState(definition.ID, state)
	}
	if err == nil {
		job = registry.advanceGraphWorkflow(runtime, definition, workflow, job, nil, 0)
		state.Jobs[job.ID] = job
		err = registry.saveWorkflowState(definition.ID, state)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot persist workflow job"})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"object": "workflow.job", "data": publicWorkflowJob(job)})
}

func workflowCallbackBaseURL(c *gin.Context, runtime localRuntime) (string, error) {
	if requestServerScope(c) == lanServiceScope {
		if runtime.config.LANPublicBaseURL != "" {
			return runtime.config.LANPublicBaseURL, nil
		}
		ip := net.ParseIP(runtime.config.LANHost)
		if ip == nil || ip.IsUnspecified() {
			return "", errors.New("LAN workflows require LOCAL_GATEWAY_LAN_PUBLIC_BASE_URL when the LAN listener uses an unspecified address")
		}
		return "http://" + net.JoinHostPort(runtime.config.LANHost, strconv.Itoa(runtime.config.LANPort)), nil
	}
	return "http://" + net.JoinHostPort(runtime.config.Host, strconv.Itoa(runtime.config.Port)), nil
}

func (registry *protocolRegistry) handleGraphWorkflowGet(c *gin.Context, runtime localRuntime, definition protocolDefinition, workflow protocolWorkflow) {
	registry.workflowMu.Lock()
	defer registry.workflowMu.Unlock()
	stateLock, lockErr := registry.lockProtocolState("workflow", definition.ID)
	if lockErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot lock workflow state"})
		return
	}
	defer unlockProtocolState(stateLock)
	state, err := registry.loadWorkflowState(definition.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot read workflow state"})
		return
	}
	job, ok := state.Jobs[c.Param("job")]
	if !ok || job.WorkflowID != workflow.ID {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "workflow job not found"})
		return
	}
	if !workflowJobOwnedBy(c, job) {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "workflow job not found"})
		return
	}
	if !workflowJobTerminal(job.State) && !job.WaitingCallback && !time.Now().UTC().Before(job.NextPollAt) {
		job = registry.advanceGraphWorkflow(runtime, definition, workflow, job, nil, 0)
		state.Jobs[job.ID] = job
		if err := registry.saveWorkflowState(definition.ID, state); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot persist workflow state"})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"object": "workflow.job", "data": publicWorkflowJob(job)})
}

func (registry *protocolRegistry) handleGraphWorkflowCancel(c *gin.Context, runtime localRuntime, definition protocolDefinition, workflow protocolWorkflow) {
	registry.workflowMu.Lock()
	defer registry.workflowMu.Unlock()
	stateLock, lockErr := registry.lockProtocolState("workflow", definition.ID)
	if lockErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot lock workflow state"})
		return
	}
	defer unlockProtocolState(stateLock)
	state, err := registry.loadWorkflowState(definition.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot read workflow state"})
		return
	}
	job, ok := state.Jobs[c.Param("job")]
	if !ok || job.WorkflowID != workflow.ID {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "workflow job not found"})
		return
	}
	if !workflowJobOwnedBy(c, job) {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "workflow job not found"})
		return
	}
	if !workflowJobTerminal(job.State) {
		job.WaitingCallback = false
		if workflow.CancelStep == "" {
			job.State = "cancelled"
			job.UpdatedAt = time.Now().UTC()
		} else {
			job.CurrentStep = workflow.CancelStep
			job.NextPollAt = time.Now().UTC()
			job = registry.advanceGraphWorkflow(runtime, definition, workflow, job, nil, 0)
		}
		state.Jobs[job.ID] = job
		if err := registry.saveWorkflowState(definition.ID, state); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot persist workflow state"})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"object": "workflow.job", "data": publicWorkflowJob(job)})
}

func (registry *protocolRegistry) handleGraphWorkflowCallback(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		definition, workflow, ok := registry.workflowDefinition(c.Param("protocol"), c.Param("workflow"))
		if !ok || len(workflow.Steps) == 0 {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "unknown workflow"})
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxProtocolBodyBytes))
		if err != nil {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"success": false, "message": "callback body is too large"})
			return
		}
		if len(body) == 0 {
			body = []byte("null")
		}
		if !json.Valid(body) {
			body, _ = json.Marshal(string(body))
		}
		registry.workflowMu.Lock()
		defer registry.workflowMu.Unlock()
		stateLock, lockErr := registry.lockProtocolState("workflow", definition.ID)
		if lockErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot lock workflow state"})
			return
		}
		defer unlockProtocolState(stateLock)
		state, err := registry.loadWorkflowState(definition.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot read workflow state"})
			return
		}
		job, exists := state.Jobs[c.Param("job")]
		provided := c.Param("token")
		if !exists || job.WorkflowID != workflow.ID || len(provided) != len(job.CallbackToken) || subtle.ConstantTimeCompare([]byte(provided), []byte(job.CallbackToken)) != 1 {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "workflow callback not found"})
			return
		}
		if workflowJobTerminal(job.State) {
			c.JSON(http.StatusConflict, gin.H{"success": false, "message": "workflow job is already terminal"})
			return
		}
		if !job.WaitingCallback {
			c.JSON(http.StatusConflict, gin.H{"success": false, "message": "workflow is not waiting for a callback"})
			return
		}
		job.WaitingCallback = false
		job = registry.advanceGraphWorkflow(runtime, definition, workflow, job, body, http.StatusOK)
		state.Jobs[job.ID] = job
		if err := registry.saveWorkflowState(definition.ID, state); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot persist workflow state"})
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"object": "workflow.callback.receipt", "accepted": true, "job_id": job.ID, "state": job.State})
	}
}

func (registry *protocolRegistry) advanceGraphWorkflow(runtime localRuntime, definition protocolDefinition, workflow protocolWorkflow, job protocolWorkflowJob, callbackBody []byte, callbackStatus int) protocolWorkflowJob {
	if workflowJobTerminal(job.State) || (job.WaitingCallback && callbackBody == nil) {
		return job
	}
	now := time.Now().UTC()
	if job.Attempts >= job.MaxAttempts {
		job.State, job.Error, job.UpdatedAt = "timed_out", "workflow exceeded max_poll_attempts", now
		return job
	}
	step, ok := graphWorkflowStep(workflow, job.CurrentStep)
	if !ok {
		job.State, job.Error, job.UpdatedAt = "failed", "current workflow step is missing", now
		return job
	}
	job.Attempts++
	job.UpdatedAt = now
	job.Error = ""
	var responseBody []byte
	var status int
	var callErr error
	switch step.Kind {
	case "callback":
		if callbackBody == nil {
			job.WaitingCallback = true
			job.State = "waiting_callback"
			return job
		}
		responseBody, status = callbackBody, callbackStatus
	case "parallel":
		responseBody, status, callErr = registry.executeParallelWorkflowCalls(runtime, definition, job, step.Parallel)
	default:
		responseBody, status, callErr = registry.executeWorkflowCall(runtime, definition, job, protocolWorkflowCall{
			ID: step.ID, Operation: step.Operation, Method: step.Method,
			PathParams: step.PathParams, Query: step.Query, BodyExpr: step.BodyExpr,
		})
	}
	job.LastHTTPCode = status
	if responseBody == nil {
		responseBody = []byte("null")
	}
	if job.Context == nil {
		job.Context = make(map[string]json.RawMessage)
	}
	job.Context[step.ID] = append(json.RawMessage(nil), responseBody...)
	env := graphWorkflowExpressionEnv(job, responseBody, status)
	if callErr != nil {
		env["error"] = callErr.Error()
		job.Error = callErr.Error()
	} else {
		env["error"] = ""
	}
	transition, transitionErr := selectWorkflowTransition(step.Transitions, env)
	if transitionErr != nil {
		job.State, job.Error = "failed", transitionErr.Error()
		return job
	}
	if transition.ResultExpr != "" {
		value, err := evalProtocolExpression(transition.ResultExpr, env)
		if err != nil {
			job.State, job.Error = "failed", "result expression failed: "+err.Error()
			return job
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			job.State, job.Error = "failed", "cannot encode workflow result"
			return job
		}
		job.Result = encoded
	}
	job.WaitingCallback = false
	if transition.Terminal != "" {
		job.State = transition.Terminal
		job.CurrentStep = ""
		job.NextPollAt = time.Time{}
		if transition.Terminal == "succeeded" {
			job.Error = ""
		}
		return job
	}
	job.State = "running"
	job.CurrentStep = transition.To
	job.NextPollAt = now.Add(time.Duration(step.WaitMS) * time.Millisecond)
	return job
}

func (registry *protocolRegistry) executeWorkflowCall(runtime localRuntime, definition protocolDefinition, job protocolWorkflowJob, call protocolWorkflowCall) ([]byte, int, error) {
	route, _ := operationRoute(definition, call.Operation)
	env := graphWorkflowExpressionEnv(job, nil, 0)
	params := make(map[string]string, len(call.PathParams))
	for name, expression := range call.PathParams {
		value, err := evalProtocolExpression(expression, env)
		if err != nil {
			return nil, 0, fmt.Errorf("path expression %s failed: %w", name, err)
		}
		params[name], err = expressionScalar(value)
		if err != nil {
			return nil, 0, fmt.Errorf("path expression %s failed: %w", name, err)
		}
	}
	path, err := renderProtocolPath(route.Path, params)
	if err != nil {
		return nil, 0, err
	}
	query := make(url.Values)
	for name, expression := range call.Query {
		value, err := evalProtocolExpression(expression, env)
		if err != nil {
			return nil, 0, fmt.Errorf("query expression %s failed: %w", name, err)
		}
		scalar, err := expressionScalar(value)
		if err != nil {
			return nil, 0, fmt.Errorf("query expression %s failed: %w", name, err)
		}
		query.Set(name, scalar)
	}
	body := []byte(job.Input)
	if call.BodyExpr != "" {
		value, err := evalProtocolExpression(call.BodyExpr, env)
		if err != nil {
			return nil, 0, fmt.Errorf("body expression failed: %w", err)
		}
		body, err = json.Marshal(value)
		if err != nil {
			return nil, 0, err
		}
	}
	method := strings.ToUpper(call.Method)
	if method == "" {
		method = route.Methods[0]
	}
	status, responseBody, _, err := callLocalProtocol(runtime, definition.ID, method, path, query.Encode(), "application/json", body)
	if err != nil {
		return responseBody, status, err
	}
	return responseBody, status, nil
}

func (registry *protocolRegistry) executeParallelWorkflowCalls(runtime localRuntime, definition protocolDefinition, job protocolWorkflowJob, calls []protocolWorkflowCall) ([]byte, int, error) {
	type result struct {
		id     string
		body   []byte
		status int
		err    error
	}
	results := make(chan result, len(calls))
	var wait sync.WaitGroup
	for _, call := range calls {
		call := call
		wait.Add(1)
		go func() {
			defer wait.Done()
			body, status, err := registry.executeWorkflowCall(runtime, definition, job, call)
			results <- result{id: call.ID, body: body, status: status, err: err}
		}()
	}
	wait.Wait()
	close(results)
	encoded := make(map[string]any, len(calls))
	overallStatus := http.StatusOK
	var firstErr error
	for item := range results {
		var body any
		if json.Unmarshal(item.body, &body) != nil {
			body = string(item.body)
		}
		entry := map[string]any{"status": item.status, "body": body}
		if item.err != nil {
			entry["error"] = item.err.Error()
			if firstErr == nil {
				firstErr = item.err
			}
		}
		if item.status < 200 || item.status >= 300 {
			overallStatus = item.status
		}
		encoded[item.id] = entry
	}
	body, err := json.Marshal(encoded)
	if err != nil {
		return nil, 0, err
	}
	return body, overallStatus, firstErr
}

func graphWorkflowExpressionEnv(job protocolWorkflowJob, response []byte, status int) map[string]any {
	var input any
	_ = json.Unmarshal(job.Input, &input)
	contextValues := make(map[string]any, len(job.Context))
	for key, raw := range job.Context {
		var value any
		if json.Unmarshal(raw, &value) != nil {
			value = string(raw)
		}
		contextValues[key] = value
	}
	var responseValue any
	if len(response) > 0 && json.Unmarshal(response, &responseValue) != nil {
		responseValue = string(response)
	}
	return map[string]any{
		"input": input, "context": contextValues, "response": responseValue,
		"status": status, "callback_url": job.CallbackURL,
		"job": map[string]any{"id": job.ID, "attempts": job.Attempts, "current_step": job.CurrentStep},
	}
}

func selectWorkflowTransition(transitions []protocolWorkflowTransition, env map[string]any) (protocolWorkflowTransition, error) {
	for _, transition := range transitions {
		if transition.When == "" {
			return transition, nil
		}
		value, err := evalProtocolExpression(transition.When, env)
		if err != nil {
			return protocolWorkflowTransition{}, fmt.Errorf("transition expression failed: %w", err)
		}
		matched, ok := value.(bool)
		if !ok {
			return protocolWorkflowTransition{}, errors.New("transition expression must return boolean")
		}
		if matched {
			return transition, nil
		}
	}
	return protocolWorkflowTransition{}, errors.New("no workflow transition matched")
}

func graphWorkflowStep(workflow protocolWorkflow, id string) (protocolWorkflowStep, bool) {
	for _, step := range workflow.Steps {
		if step.ID == id {
			return step, true
		}
	}
	return protocolWorkflowStep{}, false
}

func newWorkflowCallbackToken() (string, error) {
	random := make([]byte, 24)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return hex.EncodeToString(random), nil
}

func publicWorkflowJob(job protocolWorkflowJob) protocolWorkflowJob {
	job.CallbackToken = ""
	job.Input = nil
	job.OwnerTokenID = 0
	return job
}

func (registry *protocolRegistry) startWorkflowScheduler(runtime localRuntime) {
	registry.schedulerMu.Lock()
	defer registry.schedulerMu.Unlock()
	if registry.schedulerStop != nil {
		return
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	registry.schedulerStop = stop
	registry.schedulerDone = done
	go func() {
		defer close(done)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				registry.advanceDueGraphWorkflows(runtime)
			case <-stop:
				return
			}
		}
	}()
}

func (registry *protocolRegistry) stopWorkflowScheduler() {
	registry.schedulerMu.Lock()
	stop, done := registry.schedulerStop, registry.schedulerDone
	registry.schedulerStop, registry.schedulerDone = nil, nil
	if stop != nil {
		close(stop)
	}
	registry.schedulerMu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
}

func (registry *protocolRegistry) advanceDueGraphWorkflows(runtime localRuntime) {
	registry.mu.RLock()
	definitions := make([]protocolDefinition, 0, len(registry.templates))
	for _, definition := range registry.templates {
		if len(definition.Workflows) > 0 {
			definitions = append(definitions, definition)
		}
	}
	registry.mu.RUnlock()
	now := time.Now().UTC()
	for _, definition := range definitions {
		registry.workflowMu.Lock()
		stateLock, lockErr := registry.lockProtocolState("workflow", definition.ID)
		if lockErr != nil {
			registry.workflowMu.Unlock()
			continue
		}
		state, err := registry.loadWorkflowState(definition.ID)
		changed := false
		advanced := 0
		if err == nil {
			for id, job := range state.Jobs {
				if advanced >= 16 || workflowJobTerminal(job.State) || job.WaitingCallback || now.Before(job.NextPollAt) {
					continue
				}
				_, workflow, ok := registry.workflowDefinition(definition.ID, job.WorkflowID)
				if !ok || len(workflow.Steps) == 0 {
					continue
				}
				state.Jobs[id] = registry.advanceGraphWorkflow(runtime, definition, workflow, job, nil, 0)
				changed = true
				advanced++
			}
		}
		if changed {
			_ = registry.saveWorkflowState(definition.ID, state)
		}
		unlockProtocolState(stateLock)
		registry.workflowMu.Unlock()
	}
}
