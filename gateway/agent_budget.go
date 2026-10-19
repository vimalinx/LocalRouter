package main

// Agent budgets reserve before dispatch, then settle against observed costs.
// A reservation is an operator estimate, not a provider-enforced price ceiling.
// Unknown outcomes remain held; a provider charge above the estimate stops a
// formal Agent until the operator reconciles it. Trial Agents spend a small
// lifetime allowance from a shared monthly pool instead: a known overrun is
// simply counted. All amounts use integer USD micros.
import (
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type agentBudgetStore struct {
	store       *localStore
	notifySlots chan struct{}
}

type agentBudgetEntry struct {
	NeedsReview bool         `json:"needs_review,omitempty"`
	ID          string       `json:"id"`
	TokenID     int          `json:"token_id"`
	CreatedAt   int64        `json:"created_at"`
	Service     string       `json:"service"`
	Operation   string       `json:"operation"`
	Model       string       `json:"model"`
	State       string       `json:"state"`
	Reserved    int64        `json:"reserved_usd_micros"`
	Charged     int64        `json:"charged_usd_micros"`
	Source      string       `json:"source"`
	Reason      string       `json:"reason,omitempty"`
	Status      int          `json:"http_status"`
	Usage       usageMetrics `json:"usage"`
	Budgeted    bool         `json:"budgeted"`
	Tier        string       `json:"tier,omitempty"`
}

type agentBudgetView struct {
	DailyLimit       int64  `json:"daily_limit_usd_micros"`
	MonthlyLimit     int64  `json:"monthly_limit_usd_micros"`
	RequestReserve   int64  `json:"request_reserve_usd_micros"`
	DailySpent       int64  `json:"daily_spent_usd_micros"`
	MonthlySpent     int64  `json:"monthly_spent_usd_micros"`
	Held             int64  `json:"held_usd_micros"`
	Pending          int64  `json:"pending_requests"`
	Unpriced         int64  `json:"unpriced_requests"`
	Overruns         int64  `json:"unreconciled_overruns"`
	DailyRemaining   *int64 `json:"daily_remaining_usd_micros,omitempty"`
	MonthlyRemaining *int64 `json:"monthly_remaining_usd_micros,omitempty"`
	PeriodTimezone   string `json:"period_timezone"`
	Tier             string `json:"tier"`
	TrialLimit       int64  `json:"trial_limit_usd_micros,omitempty"`
	TrialSpent       int64  `json:"trial_spent_usd_micros,omitempty"`
	TrialHeld        int64  `json:"trial_held_usd_micros,omitempty"`
	TrialRemaining   *int64 `json:"trial_remaining_usd_micros,omitempty"`
	TrialPoolLimit   int64  `json:"trial_pool_limit_usd_micros,omitempty"`
	TrialPoolUsed    int64  `json:"trial_pool_used_usd_micros,omitempty"`
}

const budgetTierTrial = "trial"

// SQL predicates over the ledger document. Entries written before tiers
// existed have no tier and belong to the formal budget.
const (
	trialEntry  = `json_extract(document,'$.tier')='trial'`
	formalEntry = `COALESCE(json_extract(document,'$.tier'),'')!='trial'`
)

func budgetEnabled(p localTokenPolicy) bool {
	return p.BudgetTier == budgetTierTrial || p.DailyBudgetUSDMicros > 0 || p.MonthlyBudgetUSDMicros > 0
}

func usdMicros(amount float64) (int64, bool) {
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 || amount > float64(maxAllowanceAmount)/1e6 {
		return 0, false
	}
	if amount == 0 {
		return 0, true
	}
	return max(int64(1), int64(math.Ceil(amount*1e6-1e-9))), true
}

// The migration runs once, before the listener opens. It imports the retained
// historical price snapshots without re-importing modern ledger entries after
// a restart. It cannot recover protocol events already rotated out before upgrade.
func initializeAgentBudgets(runtime localRuntime) (*agentBudgetStore, error) {
	s := &agentBudgetStore{store: runtime.store, notifySlots: make(chan struct{}, 3)}
	_, err := s.store.db.Exec(`CREATE TABLE IF NOT EXISTS agent_billing (
 id TEXT PRIMARY KEY, token_id INTEGER NOT NULL, created_at INTEGER NOT NULL,
 state TEXT NOT NULL, reserved INTEGER NOT NULL, charged INTEGER NOT NULL,
 document TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS agent_billing_token_time ON agent_billing(token_id, created_at);
 CREATE TABLE IF NOT EXISTS agent_billing_meta (name TEXT PRIMARY KEY);
 CREATE TABLE IF NOT EXISTS agent_budget_settings (name TEXT PRIMARY KEY, document TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS agent_budget_requests (id TEXT PRIMARY KEY, token_id INTEGER NOT NULL,
 state TEXT NOT NULL, created_at INTEGER NOT NULL, document TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS agent_budget_requests_token ON agent_budget_requests(token_id, state);`)
	if err != nil {
		return nil, err
	}
	var migrated int
	if err = s.store.db.QueryRow(`SELECT COUNT(*) FROM agent_billing_meta WHERE name='history-v1'`).Scan(&migrated); err != nil {
		return nil, err
	}
	if migrated == 0 {
		entries := []agentBudgetEntry{}
		for page := 1; ; page++ {
			logs, total, err := s.store.listLogs(runtime.rootUser.ID, page, 200)
			if err != nil {
				return nil, err
			}
			for _, log := range logs {
				if log.TokenID <= 0 {
					continue
				}
				e := agentBudgetEntry{ID: "legacy-log-" + strconv.Itoa(log.ID), TokenID: log.TokenID, CreatedAt: log.CreatedAt, Service: log.ChannelName, Operation: "model.request", Model: log.ModelName, State: "unknown", Usage: usageMetrics{InputTokens: log.PromptTokens, OutputTokens: log.CompletionTokens, TotalTokens: log.TotalTokens}}
				if log.Type == localLogTypeConsume {
					e.Status = 200
				} else {
					e.Status = 502
				}
				e.Usage.CacheReadInputTokens = log.CachedInputTokens
				e.Usage.CacheWriteInputTokens = log.CacheWriteInputTokens
				e.Usage.ReasoningTokens = log.ReasoningTokens
				if log.CostStatus == "reported" || log.CostStatus == "confirmed" || log.Quota > 0 {
					if amount, ok := usdMicros(log.CostUSD + quotaToUSD(log.Quota)); ok {
						e.Charged, e.State, e.Source = amount, "settled", "historical"
					}
				}
				entries = append(entries, e)
			}
			if page*200 >= int(total) {
				break
			}
		}
		events, err := runtime.events.readRetained()
		if err != nil {
			return nil, err
		}
		for _, event := range events {
			if event.TokenID <= 0 {
				continue
			}
			e := agentBudgetEntry{ID: "legacy-event-" + event.ID, TokenID: event.TokenID, CreatedAt: event.CreatedAt.Unix(), Service: event.ProtocolID, Operation: event.OperationID, Model: event.Model, State: "unknown", Status: event.Status}
			if event.Usage != nil {
				e.Usage = *event.Usage
			}
			if event.Cost != nil && (event.Cost.Status == "reported" || event.Cost.Status == "confirmed") {
				if amount, ok := usdMicros(event.Cost.AmountUSD); ok {
					e.Charged, e.State, e.Source = amount, "settled", "historical"
				}
			}
			entries = append(entries, e)
		}
		tx, err := s.store.db.Begin()
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		for _, e := range entries {
			if err = insertBudgetEntry(tx, e); err != nil {
				return nil, err
			}
		}
		if _, err = tx.Exec(`INSERT INTO agent_billing_meta(name) VALUES ('history-v1')`); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
	}
	// No earlier process can still be using this database during a controlled
	// restart. Do not silently release requests that might have reached upstream.
	_, err = s.store.db.Exec(`UPDATE agent_billing SET state='unknown', document=json_set(document, '$.state', 'unknown') WHERE state='reserved'`)
	return s, err
}

func insertBudgetEntry(tx *sql.Tx, e agentBudgetEntry) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO agent_billing(id,token_id,created_at,state,reserved,charged,document) VALUES(?,?,?,?,?,?,?)`, e.ID, e.TokenID, e.CreatedAt, e.State, e.Reserved, e.Charged, string(raw))
	return err
}

type budgetQuerier interface{ QueryRow(string, ...any) *sql.Row }

func budgetView(q budgetQuerier, id int, p localTokenPolicy, settings agentBudgetSettings, now time.Time) (agentBudgetView, error) {
	now = now.UTC()
	day := now.Truncate(24 * time.Hour).Unix()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Unix()
	v := agentBudgetView{DailyLimit: p.DailyBudgetUSDMicros, MonthlyLimit: p.MonthlyBudgetUSDMicros, RequestReserve: p.RequestReserveUSDMicros, PeriodTimezone: "UTC", Tier: "formal"}
	err := q.QueryRow(`SELECT
 COALESCE(SUM(CASE WHEN `+formalEntry+` AND state IN ('settled','overrun') AND created_at >= ? THEN charged ELSE 0 END),0),
 COALESCE(SUM(CASE WHEN `+formalEntry+` AND state IN ('settled','overrun') AND created_at >= ? THEN charged ELSE 0 END),0),
 COALESCE(SUM(CASE WHEN `+formalEntry+` AND state IN ('reserved','unknown') THEN reserved ELSE 0 END),0),
 COALESCE(SUM(CASE WHEN state IN ('reserved','unknown') AND reserved > 0 THEN 1 ELSE 0 END),0),
 COALESCE(SUM(CASE WHEN state='unknown' AND reserved=0 AND created_at >= ? THEN 1 ELSE 0 END),0),
 COALESCE(SUM(CASE WHEN `+formalEntry+` AND (state='overrun' OR json_extract(document,'$.needs_review')=1) THEN 1 ELSE 0 END),0),
 COALESCE(SUM(CASE WHEN `+trialEntry+` AND state IN ('settled','overrun') THEN charged ELSE 0 END),0),
 COALESCE(SUM(CASE WHEN `+trialEntry+` AND state IN ('reserved','unknown') THEN reserved ELSE 0 END),0)
 FROM agent_billing WHERE token_id=?`, day, month, month, id).Scan(&v.DailySpent, &v.MonthlySpent, &v.Held, &v.Pending, &v.Unpriced, &v.Overruns, &v.TrialSpent, &v.TrialHeld)
	if err != nil {
		return v, err
	}
	if p.BudgetTier == budgetTierTrial {
		v.Tier, v.TrialLimit, v.TrialPoolLimit = budgetTierTrial, p.TrialBudgetUSDMicros, settings.TrialPoolMonthly
		remaining := max(int64(0), v.TrialLimit-v.TrialSpent-v.TrialHeld)
		v.TrialRemaining = &remaining
		// Held trial money stays counted until reconciled, even across months.
		err = q.QueryRow(`SELECT COALESCE(SUM(CASE WHEN state IN ('settled','overrun') AND created_at >= ? THEN charged WHEN state IN ('reserved','unknown') THEN reserved ELSE 0 END),0)
 FROM agent_billing WHERE `+trialEntry, month).Scan(&v.TrialPoolUsed)
	}
	if v.DailyLimit > 0 {
		remaining := max(int64(0), v.DailyLimit-v.DailySpent-v.Held)
		v.DailyRemaining = &remaining
	}
	if v.MonthlyLimit > 0 {
		remaining := max(int64(0), v.MonthlyLimit-v.MonthlySpent-v.Held)
		v.MonthlyRemaining = &remaining
	}
	return v, err
}

func (s *agentBudgetStore) view(id int, p localTokenPolicy, now time.Time) (agentBudgetView, error) {
	settings, err := s.settings()
	if err != nil {
		return agentBudgetView{}, err
	}
	return budgetView(s.store.db, id, p, settings, now)
}

func (s *agentBudgetStore) reserve(e agentBudgetEntry, p localTokenPolicy, quote int64, now time.Time) (string, error) {
	e.CreatedAt = now.Unix()
	e.State = "reserved"
	e.Budgeted = budgetEnabled(p)
	if p.BudgetTier == budgetTierTrial {
		e.Tier = budgetTierTrial
	}
	if e.Budgeted {
		e.Reserved = quote
		if quote < 0 {
			e.Reserved = p.RequestReserveUSDMicros
			if e.Reserved <= 0 {
				return "agent_budget_quote_required", nil
			}
		}
	}
	tx, err := s.store.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	settings, err := readBudgetSettings(tx)
	if err != nil {
		return "", err
	}
	v, err := budgetView(tx, e.TokenID, p, settings, now)
	if err != nil {
		return "", err
	}
	if code := agentBudgetBlock(v, p, e.Reserved); code != "" {
		return code, nil
	}

	if err = insertBudgetEntry(tx, e); err != nil {
		return "", err
	}
	return "", tx.Commit()
}

func agentBudgetBlock(v agentBudgetView, p localTokenPolicy, reserve int64) string {
	if !budgetEnabled(p) {
		return ""
	}
	if p.BudgetTier == budgetTierTrial {
		if v.TrialSpent+v.TrialHeld+reserve > p.TrialBudgetUSDMicros {
			return "agent_trial_exhausted"
		}
		if v.TrialPoolLimit > 0 && v.TrialPoolUsed+reserve > v.TrialPoolLimit {
			return "agent_trial_pool_exhausted"
		}
		return ""
	}
	if v.Overruns > 0 {
		return "agent_budget_reconciliation_required"
	}
	if p.DailyBudgetUSDMicros > 0 && v.DailySpent+v.Held+reserve > p.DailyBudgetUSDMicros {
		return "agent_daily_budget_exceeded"
	}
	if p.MonthlyBudgetUSDMicros > 0 && v.MonthlySpent+v.Held+reserve > p.MonthlyBudgetUSDMicros {
		return "agent_monthly_budget_exceeded"
	}
	return ""
}

func (p *tokenPolicyStore) previewAgentBudget(tokenID int, quote int64, exempt bool) (string, error) {
	if p == nil || exempt {
		return "", nil
	}
	policy, _ := p.policyFor(tokenID)
	if !budgetEnabled(policy) {
		return "", nil
	}
	if p.budgets == nil {
		return "", errors.New("budget store unavailable")
	}
	if quote < 0 {
		quote = policy.RequestReserveUSDMicros
		if quote <= 0 {
			return "agent_budget_quote_required", nil
		}
	}
	view, err := p.budgets.view(tokenID, policy, time.Now().UTC())
	if err != nil {
		return "", err
	}
	return agentBudgetBlock(view, policy, quote), nil
}

func (s *agentBudgetStore) finish(id string, cost *usageCost, metrics usageMetrics, model, service string, status int, notSent bool) error {
	tx, err := s.store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw string
	if err = tx.QueryRow(`SELECT document FROM agent_billing WHERE id=? AND state='reserved'`, id).Scan(&raw); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	var e agentBudgetEntry
	if err = json.Unmarshal([]byte(raw), &e); err != nil {
		return err
	}
	e.Model = model
	if service != "" {
		e.Service = service
	}
	e.Status = status
	e.Usage = metrics.normalized()
	e.State = "unknown"
	if notSent {
		e.State = "settled"
		e.Source = "not-sent"
	} else if cost != nil && (cost.Status == "reported" || cost.Status == "confirmed") {
		if amount, ok := usdMicros(cost.AmountUSD); ok {
			e.Charged = amount
			e.State = "settled"
			e.Source = cost.Source
			if e.Budgeted && e.Tier != budgetTierTrial && amount > e.Reserved {
				e.State = "overrun"
			}
		}
	}
	// An incomplete response may still disclose a lower bound on its charge.
	// Retain at least that much, and pause if even this observation is already
	// above the operator's estimate; never erase it with an unknown final cost.
	if !notSent && e.State == "unknown" && e.Budgeted && metrics.ReportedCostUSD != nil {
		amount, valid := usdMicros(*metrics.ReportedCostUSD)
		if valid && amount > e.Reserved {
			e.Reserved = amount
			e.NeedsReview = true
		}
		if !valid {
			e.NeedsReview = true
		}
	}
	encoded, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE agent_billing SET state=?,charged=?,reserved=?,document=? WHERE id=?`, e.State, e.Charged, e.Reserved, string(encoded), e.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func (p *tokenPolicyStore) reserveAgentBudget(c *gin.Context, service, operation string, quote int64, exempt bool) bool {
	if p == nil || exempt || c.GetString("localrouter_agent_budget") != "" {
		return true
	}
	id := c.GetInt(tokenPolicyContextID)
	if id <= 0 {
		return true
	}
	policy, _ := p.policyFor(id)
	if p.budgets == nil && !budgetEnabled(policy) {
		return true
	}
	entry := agentBudgetEntry{ID: newRelayRequestID(), TokenID: id, Service: service, Operation: operation}
	code := ""
	var err error
	if p.budgets == nil {
		err = errors.New("budget store unavailable")
	} else {
		code, err = p.budgets.reserve(entry, policy, quote, time.Now().UTC())
	}
	if err != nil {
		code = "agent_budget_unavailable"
	}
	if code != "" {
		status := http.StatusTooManyRequests
		if code == "agent_budget_quote_required" || code == "agent_budget_reconciliation_required" {
			status = http.StatusForbidden
		}
		if err != nil {
			status = http.StatusServiceUnavailable
		}
		writeAgentError(c, status, code, agentBudgetMessages[code], agentBudgetMessages[code], false, "localrouter", agentBudgetNextAction(code), nil, nil, nil)
		c.Abort()
		return false
	}
	c.Set("localrouter_agent_budget", entry.ID)
	c.Header("X-LocalRouter-Budget-Receipt", entry.ID)
	if budgetEnabled(policy) {
		c.Set("localrouter_allowance_single_attempt", true)
	}
	return true
}

var agentBudgetMessages = map[string]string{
	"agent_budget_quote_required":          "configure a per-request USD reservation for variable or unknown prices",
	"agent_budget_reconciliation_required": "a charge exceeded its reservation; reconcile it before further calls",
	"agent_daily_budget_exceeded":          "Agent daily USD budget has insufficient remaining funds",
	"agent_monthly_budget_exceeded":        "Agent monthly USD budget has insufficient remaining funds",
	"agent_budget_unavailable":             "cannot safely read or persist Agent budget; no upstream call was made",
	"agent_trial_exhausted":                "this Agent's trial budget is used up; no upstream call was made",
	"agent_trial_pool_exhausted":           "the shared monthly trial pool is used up; no upstream call was made",
}

func agentBudgetNextAction(code string) string {
	switch code {
	case "agent_trial_exhausted", "agent_trial_pool_exhausted", "agent_daily_budget_exceeded", "agent_monthly_budget_exceeded":
		return "run lr budget request formal --monthly <usd> [--daily <usd>] --reason <why>, or lr budget request trial --reason <why>; the human approves once, then preflight again"
	}
	return "ask the human to review the Agent budget, reservation or unsettled charges; preflight again after reconciliation"
}

func (p *tokenPolicyStore) finishAgentBudget(c *gin.Context, cost *usageCost, metrics usageMetrics, notSent bool) {
	if p == nil || p.budgets == nil {
		return
	}
	if id := c.GetString("localrouter_agent_budget"); id != "" {
		// On persistence failure the original reservation remains charged against
		// availability. Never allow another request by pretending settlement worked.
		_ = p.budgets.finish(id, cost, metrics, c.GetString("localrouter_usage_model"), c.GetString("localrouter_budget_service"), c.Writer.Status(), notSent)
	}
}

func (s *agentBudgetStore) entries(tokenID, page, size int) ([]agentBudgetEntry, int64, error) {
	var total int64
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM agent_billing WHERE token_id=?`, tokenID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.store.db.Query(`SELECT document FROM agent_billing WHERE token_id=? ORDER BY created_at DESC, rowid DESC LIMIT ? OFFSET ?`, tokenID, size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	entries := []agentBudgetEntry{}
	for rows.Next() {
		var raw string
		var e agentBudgetEntry
		if err = rows.Scan(&raw); err != nil {
			return nil, 0, err
		}
		if err = json.Unmarshal([]byte(raw), &e); err != nil {
			return nil, 0, err
		}
		entries = append(entries, e)
	}
	return entries, total, rows.Err()
}

func handleAgentBilling(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil || id <= 0 {
			localFailure(c, 400, "invalid token id")
			return
		}
		var exists int
		if err = runtime.store.db.QueryRow(`SELECT COUNT(*) FROM tokens WHERE id=? AND user_id=?`, id, runtime.rootUser.ID).Scan(&exists); err != nil || exists == 0 {
			localFailure(c, 404, "Agent not found")
			return
		}
		if runtime.policies == nil || runtime.policies.budgets == nil {
			localFailure(c, 503, "billing unavailable")
			return
		}
		page, size := parsePage(c.Request.URL.Query(), 50)
		entries, total, err := runtime.policies.budgets.entries(id, page, size)
		if err != nil {
			localFailure(c, 500, "cannot read Agent billing")
			return
		}
		policy, _ := runtime.policies.policyFor(id)
		view, err := runtime.policies.budgets.view(id, policy, time.Now().UTC())
		if err != nil {
			localFailure(c, 500, "cannot read Agent budget")
			return
		}
		localSuccess(c, gin.H{"items": entries, "total": total, "page": page, "page_size": size, "budget": view})
	}
}

func (s *agentBudgetStore) reconcile(tokenID int, id string, amount int64, reason string) error {
	tx, err := s.store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw string
	if err = tx.QueryRow(`SELECT document FROM agent_billing WHERE token_id=? AND id=? AND state IN ('unknown','overrun')`, tokenID, id).Scan(&raw); err != nil {
		return err
	}
	var e agentBudgetEntry
	if err = json.Unmarshal([]byte(raw), &e); err != nil {
		return err
	}
	e.State = "settled"
	e.Charged = amount
	e.Source = "operator-reconciled"
	e.NeedsReview = false
	e.Reason = reason
	encoded, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE agent_billing SET state='settled',charged=?,document=? WHERE id=?`, amount, string(encoded), id); err != nil {
		return err
	}
	return tx.Commit()
}

func handleAgentBillingReconcile(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil || id <= 0 {
			localFailure(c, 400, "invalid token id")
			return
		}
		var request struct {
			Amount *int64 `json:"amount_usd_micros"`
			Reason string `json:"reason"`
		}
		if err = decodeAllowanceRequest(c, &request); err != nil || request.Amount == nil || *request.Amount < 0 || *request.Amount > maxAllowanceAmount || len(strings.TrimSpace(request.Reason)) < 3 || len(request.Reason) > 512 {
			localFailure(c, 400, "provide confirmed USD micros and a reconciliation reason")
			return
		}
		if runtime.policies == nil || runtime.policies.budgets == nil {
			localFailure(c, 503, "billing unavailable")
			return
		}
		err = runtime.policies.budgets.reconcile(id, c.Param("receipt"), *request.Amount, strings.TrimSpace(request.Reason))
		if errors.Is(err, sql.ErrNoRows) {
			localFailure(c, 409, "only unknown or over-reservation charges can be reconciled")
			return
		}
		if err != nil {
			localFailure(c, 500, "cannot persist reconciliation")
			return
		}
		localSuccess(c, gin.H{"reconciled": true})
	}
}

// Keep counts, tokens and money in the same durable ledger. Rotating transport
// diagnostics and revoking access must not shrink historical Agent totals.
func (s *agentBudgetStore) populateUsage(items []localAgentUsage, index map[int]int, now time.Time) error {
	day := now.UTC().Truncate(24 * time.Hour).Unix()
	rows, err := s.store.db.Query(`SELECT token_id, COUNT(*),
 COALESCE(SUM(CASE WHEN json_extract(document,'$.http_status') >= 200 AND json_extract(document,'$.http_status') < 400 THEN 1 ELSE 0 END),0),
 COALESCE(SUM(CASE WHEN json_extract(document,'$.http_status') >= 400 OR (json_extract(document,'$.http_status')=0 AND state='unknown') THEN 1 ELSE 0 END),0),
 COALESCE(SUM(CASE WHEN created_at >= ? THEN 1 ELSE 0 END),0),
 COALESCE(SUM(json_extract(document,'$.usage.input_tokens')),0), COALESCE(SUM(json_extract(document,'$.usage.output_tokens')),0),
 COALESCE(SUM(json_extract(document,'$.usage.cache_read_input_tokens')),0), COALESCE(SUM(json_extract(document,'$.usage.cache_write_input_tokens')),0),
 COALESCE(SUM(json_extract(document,'$.usage.reasoning_tokens')),0),
 COALESCE(SUM(CASE WHEN state IN ('settled','overrun') THEN charged ELSE 0 END),0),
 COALESCE(SUM(CASE WHEN state IN ('settled','overrun') THEN 1 ELSE 0 END),0),
 COALESCE(SUM(CASE WHEN state IN ('reserved','unknown') THEN 1 ELSE 0 END),0),
 COALESCE(SUM(CASE WHEN state IN ('settled','overrun') AND json_extract(document,'$.source')='provider' THEN 1 ELSE 0 END),0), MAX(created_at)
 FROM agent_billing WHERE json_extract(document,'$.source')!='not-sent' GROUP BY token_id`, day)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item localAgentUsage
		var amount, known, unknown, provider int64
		if err = rows.Scan(&item.TokenID, &item.Requests, &item.Successful, &item.Failed, &item.TodayRequests, &item.PromptTokens, &item.CompletionTokens, &item.CachedInputTokens, &item.CacheWriteInputTokens, &item.ReasoningTokens, &amount, &known, &unknown, &provider, &item.LastUsedAt); err != nil {
			return err
		}
		position, ok := index[item.TokenID]
		if !ok {
			continue
		}
		original := &items[position]
		original.Requests, original.Successful, original.Failed, original.TodayRequests = item.Requests, item.Successful, item.Failed, item.TodayRequests
		original.PromptTokens, original.CompletionTokens = item.PromptTokens, item.CompletionTokens
		original.CachedInputTokens, original.CacheWriteInputTokens, original.ReasoningTokens = item.CachedInputTokens, item.CacheWriteInputTokens, item.ReasoningTokens
		original.LastUsedAt = max(original.LastUsedAt, item.LastUsedAt)
		original.CostUSD = float64(amount) / 1e6
		original.CostStatus = "unavailable"
		if known > 0 {
			original.CostStatus = "measured"
			if provider == known {
				original.CostStatus = "reported"
			}
			if unknown > 0 {
				original.CostStatus = "partial"
			}
		}
	}
	return rows.Err()
}
