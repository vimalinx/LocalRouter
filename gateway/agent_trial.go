package main

// Trial budgets replace per-identity human review in ordinary mode. Every new
// identity starts with a small lifetime allowance drawn from a shared monthly
// pool; money above that needs one human decision on an Agent-written budget
// request. The human sets every amount. Agents can never raise their own budget.
import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type agentBudgetSettings struct {
	AutoApproveIdentities bool  `json:"auto_approve_identities"`
	TrialPerAgent         int64 `json:"trial_per_agent_usd_micros"`
	TrialPoolMonthly      int64 `json:"trial_pool_monthly_usd_micros"`
	TrialRequestReserve   int64 `json:"trial_request_reserve_usd_micros"`
	Revision              int64 `json:"revision"`
}

func defaultBudgetSettings() agentBudgetSettings {
	return agentBudgetSettings{AutoApproveIdentities: true, TrialPerAgent: 2_000_000, TrialPoolMonthly: 5_000_000, TrialRequestReserve: 20_000}
}

func readBudgetSettings(q budgetQuerier) (agentBudgetSettings, error) {
	settings := defaultBudgetSettings()
	var raw string
	err := q.QueryRow(`SELECT document FROM agent_budget_settings WHERE name='trial'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	return settings, json.Unmarshal([]byte(raw), &settings)
}

func (s *agentBudgetStore) settings() (agentBudgetSettings, error) {
	return readBudgetSettings(s.store.db)
}

func validateBudgetSettings(settings agentBudgetSettings) error {
	for _, amount := range []int64{settings.TrialPerAgent, settings.TrialPoolMonthly, settings.TrialRequestReserve} {
		if amount <= 0 || amount > maxAllowanceAmount {
			return errors.New("trial amounts must be positive USD micros")
		}
	}
	if settings.TrialRequestReserve > settings.TrialPerAgent || settings.TrialPerAgent > settings.TrialPoolMonthly {
		return errors.New("require request reservation <= per-Agent trial <= monthly trial pool")
	}
	return nil
}

func (s *agentBudgetStore) saveSettings(next agentBudgetSettings) (agentBudgetSettings, error) {
	if err := validateBudgetSettings(next); err != nil {
		return next, err
	}
	tx, err := s.store.db.Begin()
	if err != nil {
		return next, err
	}
	defer tx.Rollback()
	current, err := readBudgetSettings(tx)
	if err != nil {
		return next, err
	}
	if next.Revision != current.Revision {
		return current, errBudgetRevision
	}
	next.Revision = current.Revision + 1
	raw, _ := json.Marshal(next)
	if _, err = tx.Exec(`INSERT INTO agent_budget_settings(name,document) VALUES('trial',?) ON CONFLICT(name) DO UPDATE SET document=excluded.document`, string(raw)); err != nil {
		return next, err
	}
	return next, tx.Commit()
}

var errBudgetRevision = errors.New("settings changed; reload and retry")

// Agent-supplied money fields are discarded: a new identity always starts on
// the trial tier and only a human decision moves it.
func applyTrialBudget(policy *localTokenPolicy, settings agentBudgetSettings) {
	policy.DailyBudgetUSDMicros, policy.MonthlyBudgetUSDMicros = 0, 0
	policy.BudgetTier, policy.TrialBudgetUSDMicros = budgetTierTrial, settings.TrialPerAgent
	policy.RequestReserveUSDMicros = settings.TrialRequestReserve
}

func copyBudgetFields(target *localTokenPolicy, source localTokenPolicy) {
	target.BudgetTier, target.TrialBudgetUSDMicros = source.BudgetTier, source.TrialBudgetUSDMicros
	target.DailyBudgetUSDMicros, target.MonthlyBudgetUSDMicros = source.DailyBudgetUSDMicros, source.MonthlyBudgetUSDMicros
	target.RequestReserveUSDMicros = source.RequestReserveUSDMicros
}

func (runtime localRuntime) strictMode() (bool, error) {
	strict := false
	if runtime.policies == nil || runtime.policies.allowances == nil {
		return false, nil
	}
	err := runtime.policies.allowances.transaction(false, func(doc *serviceAllowanceDocument) error {
		strict = allowanceSettings(doc).Enabled
		return nil
	})
	return strict, err
}

// Automatic enrollment needs the ledger that enforces the trial; without it
// every request falls back to human review.
func (runtime localRuntime) budgetSettings() (agentBudgetSettings, bool) {
	if runtime.policies == nil || runtime.policies.budgets == nil {
		return agentBudgetSettings{}, false
	}
	settings, err := runtime.policies.budgets.settings()
	return settings, err == nil
}

func (runtime localRuntime) autoApproveIdentities() bool {
	settings, ok := runtime.budgetSettings()
	if !ok || !settings.AutoApproveIdentities {
		return false
	}
	strict, err := runtime.strictMode()
	return err == nil && !strict
}

type agentBudgetRequest struct {
	ID             string `json:"id"`
	TokenID        int    `json:"token_id"`
	AgentCode      string `json:"agent_code"`
	AgentName      string `json:"agent_name"`
	Kind           string `json:"kind"`
	Daily          int64  `json:"daily_usd_micros,omitempty"`
	Monthly        int64  `json:"monthly_usd_micros,omitempty"`
	RequestReserve int64  `json:"request_reserve_usd_micros,omitempty"`
	Reason         string `json:"reason"`
	State          string `json:"state"`
	CreatedAt      int64  `json:"created_at"`
	DecidedAt      int64  `json:"decided_at,omitempty"`
	DecidedBy      string `json:"decided_by,omitempty"`
	TrialSpent     int64  `json:"trial_spent_usd_micros"`
}

func registerBudgetAgentRoutes(agent *gin.RouterGroup, runtime localRuntime) {
	agent.POST("/budget-requests", handleBudgetRequestCreate(runtime))
	agent.GET("/budget-requests/:id", handleBudgetRequestGet(runtime))
}

func registerBudgetAdminRoutes(admin *gin.RouterGroup, runtime localRuntime) {
	human := admin.Group("")
	human.Use(humanAllowanceAccess)
	human.GET("/agent-budget-settings", handleBudgetSettings(runtime))
	human.PUT("/agent-budget-settings", handleBudgetSettings(runtime))
	human.GET("/budget-requests", handleBudgetRequestList(runtime))
	human.POST("/budget-requests/:id/decision", handleBudgetRequestDecision(runtime))
}

func handleBudgetSettings(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		if runtime.policies == nil || runtime.policies.budgets == nil {
			localFailure(c, 503, "billing unavailable")
			return
		}
		if c.Request.Method == "GET" {
			settings, err := runtime.policies.budgets.settings()
			if err != nil {
				localFailure(c, 500, "cannot read budget settings")
				return
			}
			localSuccess(c, settings)
			return
		}
		var input agentBudgetSettings
		if err := decodeAllowanceRequest(c, &input); err != nil {
			localFailure(c, 400, "send the complete settings with the revision you read")
			return
		}
		saved, err := runtime.policies.budgets.saveSettings(input)
		if errors.Is(err, errBudgetRevision) {
			localFailure(c, 409, err.Error())
			return
		}
		if err != nil {
			localFailure(c, 400, err.Error())
			return
		}
		localSuccess(c, saved)
	}
}

func budgetRequestOwner(c *gin.Context, runtime localRuntime) (*localToken, localTokenPolicy, bool) {
	value, _ := c.Get("local_token")
	token, ok := value.(*localToken)
	if !ok || token.AgentCode == "" || token.AgentCode == "localrouter-system" || token.Name == localTokenName {
		writeAgentError(c, 403, "independent_identity_required", "budget requests belong to an independent Agent identity", "shared or bootstrap Token", false, "agent", "run lr identity request <agent-code> <policy-json> first", nil, nil, nil)
		return nil, localTokenPolicy{}, false
	}
	policy, exists := runtime.policies.policyFor(token.ID)
	if !exists || policy.SetupOnly || policyHasCapability(policy, localRouterMaintainCapability) {
		writeAgentError(c, 403, "service_identity_required", "only service-calling identities have a budget", "setup-only, maintenance or unrestricted identity", false, "agent", "use the service identity that makes the calls", nil, nil, nil)
		return nil, localTokenPolicy{}, false
	}
	return token, policy, true
}

func usdField(value *float64) (int64, error) {
	if value == nil {
		return 0, nil
	}
	amount, ok := usdMicros(*value)
	if !ok {
		return 0, errors.New("amounts must be finite non-negative USD")
	}
	return amount, nil
}

func handleBudgetRequestCreate(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		if runtime.policies == nil || runtime.policies.budgets == nil {
			writeAgentError(c, 503, "agent_budget_unavailable", "billing unavailable", "budget ledger is not available", false, "localrouter", "ask the operator to repair LocalRouter storage", nil, nil, nil)
			return
		}
		if requestServerScope(c) == lanServiceScope {
			writeAgentError(c, 403, "loopback_required", "budget requests are available only on the loopback listener", "LAN service scope", false, "agent", "submit the request from this machine", nil, nil, nil)
			return
		}
		token, policy, ok := budgetRequestOwner(c, runtime)
		if !ok {
			return
		}
		var input struct {
			Kind           string   `json:"kind"`
			DailyUSD       *float64 `json:"daily_usd"`
			MonthlyUSD     *float64 `json:"monthly_usd"`
			RequestReserve *float64 `json:"request_reserve_usd"`
			Reason         string   `json:"reason"`
		}
		if err := decodeAgentRequest(c, &input); err != nil {
			writeAgentError(c, 400, "invalid_arguments", "send {kind, monthly_usd?, daily_usd?, request_reserve_usd?, reason}", err.Error(), false, "agent", "see lr budget request --help", nil, nil, nil)
			return
		}
		request := agentBudgetRequest{TokenID: token.ID, AgentCode: token.AgentCode, AgentName: token.AgentName, Kind: input.Kind, Reason: strings.TrimSpace(input.Reason), State: "pending", CreatedAt: time.Now().Unix()}
		var err error
		if request.Daily, err = usdField(input.DailyUSD); err == nil {
			if request.Monthly, err = usdField(input.MonthlyUSD); err == nil {
				request.RequestReserve, err = usdField(input.RequestReserve)
			}
		}
		switch {
		case err != nil:
		case len(request.Reason) < 3 || len(request.Reason) > 512:
			err = errors.New("explain the need in 3 to 512 characters")
		case request.Kind == budgetTierTrial:
			if policy.BudgetTier != budgetTierTrial {
				err = errors.New("another trial round is only for Agents on the trial tier")
			} else if request.Daily+request.Monthly+request.RequestReserve != 0 {
				err = errors.New("the human sets the trial amount; omit amounts")
			}
		case request.Kind == "formal":
			if request.Daily == 0 && request.Monthly == 0 {
				err = errors.New("propose monthly_usd and/or daily_usd from the expected workload and the provider's published prices")
			}
		default:
			err = errors.New("kind must be trial or formal")
		}
		if err == nil {
			candidate := policy
			candidate.BudgetTier, candidate.TrialBudgetUSDMicros = "", 0
			candidate.DailyBudgetUSDMicros, candidate.MonthlyBudgetUSDMicros = request.Daily, request.Monthly
			if request.RequestReserve > 0 {
				candidate.RequestReserveUSDMicros = request.RequestReserve
			}
			if request.Kind == "formal" {
				err = validateLocalTokenPolicy(&candidate)
			}
		}
		if err != nil {
			writeAgentError(c, 400, "budget_request_invalid", err.Error(), err.Error(), false, "agent", "correct the request and submit it again", nil, nil, nil)
			return
		}
		if view, viewErr := runtime.policies.budgets.view(token.ID, policy, time.Now()); viewErr == nil {
			request.TrialSpent = view.TrialSpent
		}
		canonical, _ := json.Marshal([]any{request.TokenID, request.Kind, request.Daily, request.Monthly, request.RequestReserve, request.Reason})
		request.ID = "budget-" + identityHash(string(canonical))[:24]
		created, err := runtime.policies.budgets.submitRequest(request)
		if err != nil {
			writeAgentError(c, 500, "agent_budget_unavailable", "cannot save budget request", err.Error(), false, "localrouter", "inspect LocalRouter storage; nothing was approved", nil, nil, nil)
			return
		}
		if created {
			runtime.policies.budgets.notify(runtime, request)
		}
		stored, _ := runtime.policies.budgets.request(request.ID)
		localSuccess(c, budgetRequestResponse(runtime, stored))
	}
}

func budgetRequestResponse(runtime localRuntime, request agentBudgetRequest) gin.H {
	next := "wait for the human decision; lr budget status " + request.ID + " shows progress"
	switch request.State {
	case "approved":
		next = "budget is active; preflight again before the next paid call"
	case "denied":
		next = "the human declined; do not resubmit the same request, ask the human or stop"
	case "superseded":
		next = "a newer request from this Agent replaced this one"
	}
	return gin.H{"request": request, "console_url": fmt.Sprintf("http://%s:%d/#tokens", runtime.config.Host, runtime.config.Port), "next_action": next}
}

// One pending request per Agent: a new request supersedes the previous one.
// Re-submitting the identical request resumes it.
func (s *agentBudgetStore) submitRequest(request agentBudgetRequest) (bool, error) {
	tx, err := s.store.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var existing int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM agent_budget_requests WHERE id=?`, request.ID).Scan(&existing); err != nil {
		return false, err
	}
	if existing > 0 {
		return false, nil
	}
	if _, err = tx.Exec(`UPDATE agent_budget_requests SET state='superseded', document=json_set(document,'$.state','superseded') WHERE token_id=? AND state='pending'`, request.TokenID); err != nil {
		return false, err
	}
	raw, _ := json.Marshal(request)
	if _, err = tx.Exec(`INSERT INTO agent_budget_requests(id,token_id,state,created_at,document) VALUES(?,?,?,?,?)`, request.ID, request.TokenID, request.State, request.CreatedAt, string(raw)); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (s *agentBudgetStore) request(id string) (agentBudgetRequest, error) {
	var raw string
	var request agentBudgetRequest
	if err := s.store.db.QueryRow(`SELECT document FROM agent_budget_requests WHERE id=?`, id).Scan(&raw); err != nil {
		return request, err
	}
	return request, json.Unmarshal([]byte(raw), &request)
}

func handleBudgetRequestGet(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		if runtime.policies == nil || runtime.policies.budgets == nil {
			localFailure(c, 503, "billing unavailable")
			return
		}
		value, _ := c.Get("local_token")
		token, _ := value.(*localToken)
		request, err := runtime.policies.budgets.request(c.Param("id"))
		if err != nil || token == nil || request.TokenID != token.ID {
			writeAgentError(c, 404, "budget_request_not_found", "owned budget request not found", "unknown id or another Agent's request", false, "agent", "use the id returned by lr budget request", nil, nil, nil)
			return
		}
		localSuccess(c, budgetRequestResponse(runtime, request))
	}
}

func handleBudgetRequestList(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		if runtime.policies == nil || runtime.policies.budgets == nil {
			localFailure(c, 503, "billing unavailable")
			return
		}
		rows, err := runtime.store.db.Query(`SELECT document FROM agent_budget_requests ORDER BY created_at DESC, rowid DESC LIMIT 100`)
		if err != nil {
			localFailure(c, 500, "cannot list budget requests")
			return
		}
		defer rows.Close()
		requests := []agentBudgetRequest{}
		for rows.Next() {
			var raw string
			var request agentBudgetRequest
			if err = rows.Scan(&raw); err == nil {
				err = json.Unmarshal([]byte(raw), &request)
			}
			if err != nil {
				localFailure(c, 500, "cannot read budget requests")
				return
			}
			requests = append(requests, request)
		}
		settings, _ := runtime.policies.budgets.settings()
		localSuccess(c, gin.H{"items": requests, "settings": settings})
	}
}

func handleBudgetRequestDecision(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input struct {
			Approve        *bool  `json:"approve"`
			Daily          *int64 `json:"daily_usd_micros"`
			Monthly        *int64 `json:"monthly_usd_micros"`
			RequestReserve *int64 `json:"request_reserve_usd_micros"`
		}
		if err := decodeAllowanceRequest(c, &input); err != nil || input.Approve == nil {
			localFailure(c, 400, "explicit approve is required")
			return
		}
		overrides := budgetOverrides{daily: input.Daily, monthly: input.Monthly, reserve: input.RequestReserve}
		request, status, err := decideBudgetRequest(runtime, c.Param("id"), *input.Approve, overrides, "console")
		if err != nil {
			localFailure(c, status, err.Error())
			return
		}
		localSuccess(c, request)
	}
}

type budgetOverrides struct{ daily, monthly, reserve *int64 }

// The human may adjust the proposed amounts while approving. Approval rewrites
// only the budget fields of the current policy; scope stays untouched.
func decideBudgetRequest(runtime localRuntime, id string, approve bool, overrides budgetOverrides, by string) (agentBudgetRequest, int, error) {
	if runtime.policies == nil || runtime.policies.budgets == nil {
		return agentBudgetRequest{}, 503, errors.New("billing unavailable")
	}
	budgets := runtime.policies.budgets
	request, err := budgets.request(id)
	if err != nil {
		return request, 404, errors.New("budget request not found")
	}
	if request.State != "pending" {
		return request, 409, errors.New("request is no longer pending")
	}
	request.DecidedAt, request.DecidedBy = time.Now().Unix(), by
	if !approve {
		request.State = "denied"
		return request, 0, budgets.storeDecision(request)
	}
	for _, value := range []struct {
		override *int64
		target   *int64
	}{{overrides.daily, &request.Daily}, {overrides.monthly, &request.Monthly}, {overrides.reserve, &request.RequestReserve}} {
		if value.override != nil {
			*value.target = *value.override
		}
	}
	settings, err := budgets.settings()
	if err != nil {
		return request, 500, errors.New("cannot read budget settings")
	}
	runtime.policies.mu.Lock()
	defer runtime.policies.mu.Unlock()
	previous, exists := runtime.policies.policies[request.TokenID]
	if !exists {
		return request, 409, errors.New("Agent identity no longer has a service policy")
	}
	next := previous
	if request.Kind == budgetTierTrial {
		if next.BudgetTier != budgetTierTrial {
			return request, 409, errors.New("Agent already left the trial tier")
		}
		next.TrialBudgetUSDMicros += settings.TrialPerAgent
	} else {
		next.BudgetTier, next.TrialBudgetUSDMicros = "", 0
		next.DailyBudgetUSDMicros, next.MonthlyBudgetUSDMicros = request.Daily, request.Monthly
		if request.RequestReserve > 0 {
			next.RequestReserveUSDMicros = request.RequestReserve
		}
		if next.DailyBudgetUSDMicros == 0 && next.MonthlyBudgetUSDMicros == 0 {
			return request, 400, errors.New("a formal budget needs a daily or monthly limit")
		}
	}
	if err = validateLocalTokenPolicy(&next); err != nil {
		return request, 400, err
	}
	runtime.policies.policies[request.TokenID] = next
	if err = runtime.policies.saveLocked(); err == nil {
		request.State = "approved"
		err = budgets.storeDecision(request)
	}
	if err != nil {
		runtime.policies.policies[request.TokenID] = previous
		_ = runtime.policies.saveLocked()
		return request, 500, errors.New("approval did not complete; the previous budget remains")
	}
	return request, 0, nil
}

func (s *agentBudgetStore) storeDecision(request agentBudgetRequest) error {
	raw, _ := json.Marshal(request)
	result, err := s.store.db.Exec(`UPDATE agent_budget_requests SET state=?, document=? WHERE id=? AND state='pending'`, request.State, string(raw), request.ID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return errors.New("request was decided concurrently")
	}
	return nil
}

func formatUSD(micros int64) string {
	return "$" + strconv.FormatFloat(float64(micros)/1e6, 'f', -1, 64)
}

// Desktop notifications are a convenience front end to the same decision; the
// console stays authoritative. Without a display or on timeout the request
// simply stays pending.
func (s *agentBudgetStore) notify(runtime localRuntime, request agentBudgetRequest) {
	if !runtime.config.DesktopNotifications {
		return
	}
	select {
	case s.notifySlots <- struct{}{}:
	default:
		return
	}
	go func() {
		defer func() { <-s.notifySlots }()
		title := fmt.Sprintf("LocalRouter · %s 申请预算", request.AgentName)
		body := fmt.Sprintf("再给一轮试用（已花 %s）\n理由：%s", formatUSD(request.TrialSpent), request.Reason)
		if request.Kind == "formal" {
			limits := []string{}
			if request.Monthly > 0 {
				limits = append(limits, formatUSD(request.Monthly)+"/月")
			}
			if request.Daily > 0 {
				limits = append(limits, formatUSD(request.Daily)+"/日")
			}
			body = fmt.Sprintf("转为正式预算：%s（试用已花 %s）\n理由：%s", strings.Join(limits, "，"), formatUSD(request.TrialSpent), request.Reason)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		output, err := exec.CommandContext(ctx, "notify-send", "--app-name=LocalRouter", "--urgency=critical", "--wait",
			"--action=approve=同意", "--action=deny=拒绝", "--action=open=打开控制台", title, body).Output()
		if err != nil {
			return
		}
		switch strings.TrimSpace(string(output)) {
		case "approve":
			_, _, _ = decideBudgetRequest(runtime, request.ID, true, budgetOverrides{}, "desktop")
		case "deny":
			_, _, _ = decideBudgetRequest(runtime, request.ID, false, budgetOverrides{}, "desktop")
		case "open":
			_ = exec.Command("xdg-open", fmt.Sprintf("http://%s:%d/#tokens", runtime.config.Host, runtime.config.Port)).Start()
		}
	}()
}
