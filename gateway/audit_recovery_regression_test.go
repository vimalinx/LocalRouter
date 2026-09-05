package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type auditBrokenBody struct{}

func (auditBrokenBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (auditBrokenBody) Close() error             { return nil }

func TestRecoveryBrokenResponseIsNotSuccess(t *testing.T) {
	rt := auditRuntime(t)
	rt.relayClient = &http.Client{Transport: auditTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: auditBrokenBody{}, Request: r}, nil
	})}
	engine := gin.New()
	engine.POST("/v1/chat/completions", func(c *gin.Context) {
		relayAcrossChannels(c, rt, []localChannel{{ID: 1, Type: 1, Name: "fixture", Key: "fixture-only", BaseURL: "https://fixture.example.invalid"}}, "fixture", []byte(`{"model":"fixture"}`))
	})
	out := httptest.NewRecorder()
	engine.ServeHTTP(out, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"fixture"}`)))
	t.Logf("upstream 200 headers, response read returned unexpected EOF: downstream HTTP=%d body bytes=%d", out.Code, out.Body.Len())
	require.Equal(t, 502, out.Code)
}

func recoveryRuntime(server *httptest.Server, registry *protocolRegistry) localRuntime {
	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())
	return localRuntime{config: runtimeConfig{Host: u.Hostname(), Port: port}, apiToken: "fixture-only", protocols: registry}
}

func recoveryContext(method, jobID string) (*gin.Context, *httptest.ResponseRecorder) {
	out := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(out)
	c.Request = httptest.NewRequest(method, "/w/fixture/flow/"+jobID, nil)
	c.Params = gin.Params{{Key: "job", Value: jobID}}
	c.Set(tokenPolicyContextID, 7)
	return c, out
}

func TestRecoveryWorkflowCancelAfterAttemptBudget(t *testing.T) {
	registry, err := newProtocolRegistry(t.TempDir(), t.TempDir())
	require.NoError(t, err)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"cancelled":true}`)
	}))
	defer server.Close()
	workflow := protocolWorkflow{ID: "flow", CancelStep: "cancel", Steps: []protocolWorkflowStep{{ID: "cancel", Operation: "cancel", Transitions: []protocolWorkflowTransition{{Terminal: "cancelled"}}}}}
	def := protocolDefinition{ID: "fixture", Routes: []protocolRoute{{OperationID: "cancel", Path: "/cancel", Methods: []string{"POST"}}}, Workflows: []protocolWorkflow{workflow}}
	job := protocolWorkflowJob{ID: "job", ProtocolID: def.ID, WorkflowID: workflow.ID, State: "running", CurrentStep: "poll", OwnerTokenID: 7, Attempts: 1, MaxAttempts: 1}
	require.NoError(t, registry.saveWorkflowState(def.ID, protocolWorkflowState{Jobs: map[string]protocolWorkflowJob{job.ID: job}}))
	c, out := recoveryContext("DELETE", job.ID)
	registry.handleGraphWorkflowCancel(c, recoveryRuntime(server, registry), def, workflow)
	var response struct {
		Data protocolWorkflowJob `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out.Body.Bytes(), &response))
	t.Logf("cancel running job at attempt limit: state=%s, cancellation calls=%d", response.Data.State, calls.Load())
	require.Equal(t, "cancelled", response.Data.State)
	require.Equal(t, int32(1), calls.Load())
}

func TestRecoverySlowWorkflowDoesNotBlockOtherPackCancel(t *testing.T) {
	registry, err := newProtocolRegistry(t.TempDir(), t.TempDir())
	require.NoError(t, err)
	entered := make(chan struct{})
	unblock := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-unblock
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()
	rt := recoveryRuntime(server, registry)
	wfA := protocolWorkflow{ID: "flow", Steps: []protocolWorkflowStep{{ID: "slow", Operation: "slow", Transitions: []protocolWorkflowTransition{{Terminal: "succeeded"}}}}}
	defA := protocolDefinition{ID: "packa", Routes: []protocolRoute{{OperationID: "slow", Path: "/slow", Methods: []string{"POST"}}}, Workflows: []protocolWorkflow{wfA}}
	wfB := protocolWorkflow{ID: "flow", Steps: []protocolWorkflowStep{{ID: "wait", Kind: "callback"}}}
	defB := protocolDefinition{ID: "packb", Workflows: []protocolWorkflow{wfB}}
	a := protocolWorkflowJob{ID: "a", ProtocolID: defA.ID, WorkflowID: "flow", State: "running", CurrentStep: "slow", MaxAttempts: 10, OwnerTokenID: 7}
	b := protocolWorkflowJob{ID: "b", ProtocolID: defB.ID, WorkflowID: "flow", State: "waiting_callback", CurrentStep: "wait", MaxAttempts: 10, WaitingCallback: true, OwnerTokenID: 7}
	require.NoError(t, registry.saveWorkflowState(defA.ID, protocolWorkflowState{Jobs: map[string]protocolWorkflowJob{"a": a}}))
	require.NoError(t, registry.saveWorkflowState(defB.ID, protocolWorkflowState{Jobs: map[string]protocolWorkflowJob{"b": b}}))
	ca, _ := recoveryContext("GET", "a")
	cb, _ := recoveryContext("DELETE", "b")
	aDone := make(chan struct{})
	bStarted := make(chan struct{})
	bDone := make(chan struct{})
	go func() { registry.handleGraphWorkflowGet(ca, rt, defA, wfA); close(aDone) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		close(unblock)
		t.Fatal("slow fixture did not start")
	}
	go func() { close(bStarted); registry.handleGraphWorkflowCancel(cb, rt, defB, wfB); close(bDone) }()
	<-bStarted
	blocked := false
	select {
	case <-bDone:
	case <-time.After(150 * time.Millisecond):
		blocked = true
	}
	close(unblock)
	<-aDone
	<-bDone
	t.Logf("pack A upstream held open; cancelling independent pack B blocked until A released=%t", blocked)
	require.False(t, blocked)
}

func TestRecoveryCancelInterruptsActiveJobAndPreservesSibling(t *testing.T) {
	registry, err := newProtocolRegistry(t.TempDir(), t.TempDir())
	require.NoError(t, err)
	entered := make(chan struct{})
	release := make(chan struct{})
	var cleanupCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/slow") {
			close(entered)
			select {
			case <-r.Context().Done():
			case <-release:
			}
			return
		}
		cleanupCalls.Add(1)
		io.WriteString(w, `{"cancelled":true}`)
	}))
	defer server.Close()
	defer close(release)
	rt := recoveryRuntime(server, registry)
	wf := protocolWorkflow{ID: "flow", CancelStep: "cancel", Steps: []protocolWorkflowStep{
		{ID: "slow", Operation: "slow", Transitions: []protocolWorkflowTransition{{Terminal: "succeeded"}}},
		{ID: "cancel", Operation: "cancel", Transitions: []protocolWorkflowTransition{{Terminal: "cancelled"}}},
	}}
	def := protocolDefinition{ID: "fixture", Routes: []protocolRoute{
		{OperationID: "slow", Path: "/slow", Methods: []string{"POST"}},
		{OperationID: "cancel", Path: "/cancel", Methods: []string{"POST"}},
	}, Workflows: []protocolWorkflow{wf}}
	a := protocolWorkflowJob{ID: "a", ProtocolID: def.ID, WorkflowID: wf.ID, State: "running", CurrentStep: "slow", OwnerTokenID: 7, MaxAttempts: 10}
	b := a
	b.ID = "b"
	b.State = "waiting_callback"
	b.WaitingCallback = true
	require.NoError(t, registry.saveWorkflowState(def.ID, protocolWorkflowState{Jobs: map[string]protocolWorkflowJob{"a": a, "b": b}}))
	ca, _ := recoveryContext("GET", "a")
	done := make(chan struct{})
	go func() { registry.handleGraphWorkflowGet(ca, rt, def, wf); close(done) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("fixture never started")
	}
	// A different owner cannot cancel or disclose the active job.
	bad, badOut := recoveryContext("DELETE", "a")
	bad.Set(tokenPolicyContextID, 8)
	registry.handleGraphWorkflowCancel(bad, rt, def, wf)
	require.Equal(t, 404, badOut.Code)
	cb, out := recoveryContext("DELETE", "a")
	cancelDone := make(chan struct{})
	go func() { registry.handleGraphWorkflowCancel(cb, rt, def, wf); close(cancelDone) }()
	select {
	case <-cancelDone:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel blocked behind active network request")
	}
	<-done
	require.Equal(t, 200, out.Code)
	state, err := registry.loadWorkflowState(def.ID)
	require.NoError(t, err)
	require.Equal(t, "cancelled", state.Jobs["a"].State)
	require.Equal(t, "waiting_callback", state.Jobs["b"].State)
	require.EqualValues(t, 1, cleanupCalls.Load())
}

func TestRecoveryInterruptedExecutionIsNotReplayed(t *testing.T) {
	registry, err := newProtocolRegistry(t.TempDir(), t.TempDir())
	require.NoError(t, err)
	wf := protocolWorkflow{ID: "flow", Steps: []protocolWorkflowStep{{ID: "create", Operation: "create"}}}
	def := protocolDefinition{ID: "fixture", Workflows: []protocolWorkflow{wf}}
	registry.templates[def.ID] = def
	job := protocolWorkflowJob{ID: "interrupted", ProtocolID: def.ID, WorkflowID: wf.ID, State: "running", CurrentStep: "create", ExecutionID: "persisted-claim", MaxAttempts: 10}
	require.NoError(t, registry.saveWorkflowState(def.ID, protocolWorkflowState{Jobs: map[string]protocolWorkflowJob{job.ID: job}}))
	registry.recoverInterruptedWorkflows()
	state, err := registry.loadWorkflowState(def.ID)
	require.NoError(t, err)
	require.Equal(t, "outcome_unknown", state.Jobs[job.ID].State)
	require.Empty(t, state.Jobs[job.ID].ExecutionID)
	require.True(t, workflowJobTerminal(state.Jobs[job.ID].State))
}
