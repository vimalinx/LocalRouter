package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

var errWorkflowNotFound = errors.New("workflow job not found")

type workflowExecution struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (registry *protocolRegistry) recoverInterruptedWorkflows() {
	registry.mu.RLock()
	packs := make([]string, 0, len(registry.templates))
	for id := range registry.templates {
		packs = append(packs, id)
	}
	registry.mu.RUnlock()
	for _, pack := range packs {
		_ = registry.editWorkflowState(pack, func(state *protocolWorkflowState) (bool, error) {
			changed := false
			for id, job := range state.Jobs {
				if job.ExecutionID == "" || registry.workflowRunning[pack+"/"+id] != nil {
					continue
				}
				job.State, job.ExecutionID, job.CancelRequested = "outcome_unknown", "", false
				job.Error = "previous execution was interrupted; reconcile upstream state or cancel explicitly"
				job.UpdatedAt = time.Now().UTC()
				state.Jobs[id] = job
				changed = true
			}
			return changed, nil
		})
	}
}

// Only state IO holds the registry/file locks. Network execution is claimed
// per job and committed by execution ID, so other jobs can progress/cancel.
func (registry *protocolRegistry) editWorkflowState(pack string, edit func(*protocolWorkflowState) (bool, error)) error {
	registry.workflowMu.Lock()
	defer registry.workflowMu.Unlock()
	lock, err := registry.lockProtocolState("workflow", pack)
	if err != nil {
		return err
	}
	defer unlockProtocolState(lock)
	state, err := registry.loadWorkflowState(pack)
	if err != nil {
		return err
	}
	changed, err := edit(&state)
	if err != nil {
		return err
	}
	if changed {
		return registry.saveWorkflowState(pack, state)
	}
	return nil
}

func (registry *protocolRegistry) runStoredWorkflow(ctx context.Context, runtime localRuntime, definition protocolDefinition, workflow protocolWorkflow, jobID string, owner int, mode string, callback []byte, callbackStatus int) (protocolWorkflowJob, error) {
	key := definition.ID + "/" + jobID
	var job protocolWorkflowJob
	var execution *workflowExecution
	for {
		var wait <-chan struct{}
		claimed := false
		err := registry.editWorkflowState(definition.ID, func(state *protocolWorkflowState) (bool, error) {
			var exists bool
			job, exists = state.Jobs[jobID]
			if !exists || job.WorkflowID != workflow.ID || (owner >= 0 && job.OwnerTokenID != 0 && job.OwnerTokenID != owner) {
				return false, errWorkflowNotFound
			}
			if registry.workflowRunning == nil {
				registry.workflowRunning = make(map[string]*workflowExecution)
			}
			if mode == "cancel" {
				if job.State == "succeeded" || job.State == "cancelled" {
					return false, nil
				}
				if active := registry.workflowRunning[key]; active != nil {
					job.CancelRequested = true
					state.Jobs[jobID] = job
					active.cancel()
					wait = active.done
					return true, nil
				}
			} else {
				if job.ExecutionID != "" || workflowJobTerminal(job.State) || job.CancelRequested {
					return false, nil
				}
				if mode == "callback" {
					if !job.WaitingCallback {
						return false, errors.New("workflow is not waiting for callback")
					}
				} else if job.WaitingCallback || time.Now().UTC().Before(job.NextPollAt) {
					return false, nil
				}
			}
			if len(registry.workflowRunning) >= 64 {
				return false, errors.New("workflow execution capacity reached")
			}
			id, err := newWorkflowJobID()
			if err != nil {
				return false, err
			}
			job.ExecutionID = id
			if mode == "cancel" {
				job.CancelRequested = true
			}
			state.Jobs[jobID] = job
			// Registration and durable claim happen under the same short lock.
			// Clean up registration below if the atomic state write fails.
			runCtx, cancel := context.WithCancel(ctx)
			runtime.workflowContext = runCtx
			execution = &workflowExecution{cancel: cancel, done: make(chan struct{})}
			registry.workflowRunning[key] = execution
			claimed = true
			return true, nil
		})
		if err != nil {
			if claimed {
				registry.finishWorkflowExecution(key, execution)
			}
			return job, err
		}
		if wait != nil {
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return job, ctx.Err()
			}
		}
		if !claimed {
			return job, nil
		}
		break
	}
	defer registry.finishWorkflowExecution(key, execution)
	claimID := job.ExecutionID
	if mode == "cancel" {
		job.State, job.WaitingCallback = "running", false
		job.Cancelling, job.CancelAttempts = true, 0
		if len(workflow.Steps) > 0 {
			if workflow.CancelStep == "" {
				job.State = "cancelled"
			} else {
				job.CurrentStep = workflow.CancelStep
				job = registry.advanceGraphWorkflow(runtime, definition, workflow, job, nil, 0)
			}
		} else {
			job = advancePollingWorkflow(runtime, definition, workflow, job, true)
		}
	} else if len(workflow.Steps) > 0 {
		job = registry.advanceGraphWorkflow(runtime, definition, workflow, job, callback, callbackStatus)
	} else {
		job = advancePollingWorkflow(runtime, definition, workflow, job, false)
	}
	if runtime.workflowContext.Err() != nil {
		job.State = "outcome_unknown"
		job.Error = "workflow request interrupted; reconcile upstream state before retrying or cancel explicitly"
	}
	job.ExecutionID = ""
	job.UpdatedAt = time.Now().UTC()
	err := registry.editWorkflowState(definition.ID, func(state *protocolWorkflowState) (bool, error) {
		current, ok := state.Jobs[jobID]
		if !ok || current.ExecutionID != claimID {
			return false, errors.New("workflow execution changed before commit")
		}
		job.CancelRequested = current.CancelRequested && mode != "cancel"
		state.Jobs[jobID] = job
		return true, nil
	})
	return job, err
}

func (registry *protocolRegistry) finishWorkflowExecution(key string, execution *workflowExecution) {
	execution.cancel()
	registry.workflowMu.Lock()
	delete(registry.workflowRunning, key)
	close(execution.done)
	registry.workflowMu.Unlock()
}

func advancePollingWorkflow(runtime localRuntime, definition protocolDefinition, workflow protocolWorkflow, job protocolWorkflowJob, cancelling bool) protocolWorkflowJob {
	operation := workflow.PollOperation
	if cancelling {
		operation = workflow.CancelOperation
	}
	route, ok := operationRoute(definition, operation)
	if !ok || len(route.Methods) == 0 {
		job.State, job.Error = "failed", "workflow operation is missing"
		return job
	}
	params := map[string]string{}
	for name := range protocolPathParameterNames(route.Path) {
		params[name] = job.ResourceID
	}
	path, err := renderProtocolPath(route.Path, params)
	if err != nil {
		job.State, job.Error = "failed", "cannot render workflow operation path"
		return job
	}
	code, body, _, callErr := callLocalProtocol(runtime, definition.ID, route.Methods[0], path, "", "application/json", nil)
	job.LastHTTPCode = code
	if cancelling {
		job.CancelAttempts++
	} else {
		job.Attempts++
	}
	job.NextPollAt = time.Now().UTC().Add(time.Duration(workflow.PollIntervalMS) * time.Millisecond)
	if callErr != nil || code < 200 || code >= 300 {
		job.Error = fmt.Sprintf("workflow operation failed (HTTP %d); upstream state requires reconciliation", code)
		if cancelling {
			job.State = "outcome_unknown"
		}
	} else if cancelling {
		job.State, job.Error = "cancelled", ""
	} else {
		job.Error = ""
		job.UpstreamState = gjson.GetBytes(body, workflow.StatusPath).String()
		job.State = classifyWorkflowState(workflow, job.UpstreamState)
		if workflow.ResultPath != "" && job.State == "succeeded" {
			if value := gjson.GetBytes(body, workflow.ResultPath); value.Exists() {
				job.Result = json.RawMessage(value.Raw)
			}
		}
	}
	if !cancelling && job.Attempts >= job.MaxAttempts && !workflowJobTerminal(job.State) {
		job.State, job.Error = "timed_out", "workflow exceeded max_poll_attempts"
	}
	return job
}

func (registry *protocolRegistry) serveStoredWorkflow(c *gin.Context, runtime localRuntime, definition protocolDefinition, workflow protocolWorkflow, mode string) {
	job, err := registry.runStoredWorkflow(c.Request.Context(), runtime, definition, workflow, c.Param("job"), c.GetInt(tokenPolicyContextID), mode, nil, 0)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, errWorkflowNotFound) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"success": false, "message": "cannot execute or read workflow job"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"object": "workflow.job", "data": publicWorkflowJob(job)})
}

func (registry *protocolRegistry) handleAdminWorkflowCancel(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		definition, workflow, ok := registry.workflowDefinition(c.Param("protocol"), c.Param("workflow"))
		if !ok || (len(workflow.Steps) == 0 && workflow.CancelOperation == "") {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "workflow does not support cancellation"})
			return
		}
		// Operator authorization is provided by the local admin route group.
		job, err := registry.runStoredWorkflow(c.Request.Context(), runtime, definition, workflow, c.Param("job"), -1, "cancel", nil, 0)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": "workflow cancellation failed"})
			return
		}
		localSuccess(c, publicWorkflowJob(job))
	}
}
