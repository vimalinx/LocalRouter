package main

// Service allowances are human-owned, additive authorization. They never grant
// token/bundle access and are intentionally absent from maintenance MCP tools.
import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
)

const maxAllowanceAmount int64 = 1_000_000_000_000

var errAllowanceNoChange = errors.New("allowance state unchanged")

type serviceAllowanceRule struct {
	OperationLimits    map[string]int64 `json:"operation_limits,omitempty"`
	Enabled            bool             `json:"enabled"`
	Mode               string           `json:"mode"`   // quota, approval, deny
	Unit               string           `json:"unit"`   // requests or usd_micros
	Period             string           `json:"period"` // once, day, month (UTC)
	Limit              int64            `json:"limit"`
	ApprovalOperations []string         `json:"approval_operations"`
	DeniedOperations   []string         `json:"denied_operations"`
	Revision           int64            `json:"revision"`
}
type serviceAllowanceReceipt struct {
	ReconciliationReason string `json:"reconciliation_reason,omitempty"`
	SettledAt            int64  `json:"settled_at,omitempty"`
	ID                   string `json:"id"`
	Service              string `json:"service"`
	Operation            string `json:"operation"`
	TokenID              int    `json:"token_id"`
	Period               string `json:"period"`
	Amount               int64  `json:"amount"`
	Pending              bool   `json:"pending"`
	Approved             bool   `json:"approved"`
	CreatedAt            int64  `json:"created_at"`
}
type serviceAllowanceGrant struct {
	ID        string `json:"id"`
	Service   string `json:"service"`
	Operation string `json:"operation"`
	TokenID   int    `json:"token_id"`
	ExpiresAt int64  `json:"expires_at"`
	Used      bool   `json:"used"`
}
type serviceAllowanceDocument struct {
	Version  int                                `json:"version"`
	Rules    map[string]serviceAllowanceRule    `json:"rules"`
	Receipts map[string]serviceAllowanceReceipt `json:"receipts"`
	Grants   map[string]serviceAllowanceGrant   `json:"grants"`
}
type serviceAllowanceStore struct{ path string }
type serviceAllowanceDecision struct {
	OperationLimit     *int64 `json:"operation_limit,omitempty"`
	OperationRemaining *int64 `json:"operation_remaining,omitempty"`
	Enabled            bool   `json:"enabled"`
	Allowed            bool   `json:"allowed"`
	Code               string `json:"code,omitempty"`
	Message            string `json:"message"`
	Remaining          int64  `json:"remaining"`
	Required           int64  `json:"required"`
	Unit               string `json:"unit,omitempty"`
	GrantID            string `json:"-"`
}

func newServiceAllowanceStore(dir string) *serviceAllowanceStore {
	return &serviceAllowanceStore{path: filepath.Join(dir, "service-allowances.json")}
}

// Reload under an OS lock for every transaction: multiple gateway processes
// cannot admit against a stale balance. Atomic replacement precedes dispatch.
func (s *serviceAllowanceStore) transaction(write bool, fn func(*serviceAllowanceDocument) error) error {
	if s == nil {
		return errors.New("allowance store unavailable")
	}
	lockPath := s.path + ".lock"
	if _, err := inspectPrivateRegularFile(lockPath, "allowance lock"); err != nil {
		return err
	}
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	doc := serviceAllowanceDocument{Version: 1, Rules: map[string]serviceAllowanceRule{}, Receipts: map[string]serviceAllowanceReceipt{}, Grants: map[string]serviceAllowanceGrant{}}
	exists, err := inspectPrivateRegularFile(s.path, "service allowances")
	if err != nil {
		return err
	}
	if exists {
		data, err := os.ReadFile(s.path)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(data, &doc); err != nil {
			return errors.New("invalid allowance state")
		}
		if doc.Version != 1 || doc.Rules == nil || doc.Receipts == nil || doc.Grants == nil {
			return errors.New("invalid allowance state")
		}
	}
	for _, rule := range doc.Rules {
		if err := validateAllowanceRule(&rule); err != nil {
			return errors.New("invalid allowance rule in storage")
		}
	}
	for _, receipt := range doc.Receipts {
		if receipt.Amount < 0 || receipt.Amount > maxAllowanceAmount || receipt.TokenID <= 0 {
			return errors.New("invalid allowance receipt in storage")
		}
	}
	if err = fn(&doc); err != nil {
		if errors.Is(err, errAllowanceNoChange) {
			return nil
		}
		return err
	}
	if !write {
		return nil
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	if err := (&tokenPolicyStore{}).writeAtomicLocked(s.path, data); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(s.path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
func allowancePeriod(rule serviceAllowanceRule, now time.Time) string {
	switch rule.Period {
	case "day":
		return now.UTC().Format("2006-01-02")
	case "month":
		return now.UTC().Format("2006-01")
	default:
		return "once"
	}
}
func allowanceMatches(ops []string, operation string) bool {
	for _, op := range ops {
		if op == operation || op == "*" {
			return true
		}
	}
	return false
}
func validateAllowanceRule(r *serviceAllowanceRule) error {
	if r.Mode != "quota" && r.Mode != "approval" && r.Mode != "deny" {
		return errors.New("mode must be quota, approval or deny")
	}
	if r.Unit != "requests" && r.Unit != "usd_micros" {
		return errors.New("unit must be requests or usd_micros")
	}
	if r.Period != "once" && r.Period != "day" && r.Period != "month" {
		return errors.New("period must be once, day or month")
	}
	if r.Limit < 0 || r.Limit > maxAllowanceAmount {
		return errors.New("limit is out of range")
	}
	if len(r.OperationLimits) > 1024 {
		return errors.New("too many operation limits")
	}
	for operation, limit := range r.OperationLimits {
		if strings.TrimSpace(operation) == "" || strings.Contains(operation, "*") || limit < 0 || limit > maxAllowanceAmount {
			return errors.New("invalid operation limit")
		}
	}
	r.ApprovalOperations = normalizePolicyValues(r.ApprovalOperations)
	r.DeniedOperations = normalizePolicyValues(r.DeniedOperations)
	return nil
}
func defaultAllowanceRule() serviceAllowanceRule {
	return serviceAllowanceRule{Mode: "quota", Unit: "usd_micros", Limit: 3000000, Period: "month", ApprovalOperations: []string{}, DeniedOperations: []string{}}
}
func allowanceDecision(doc *serviceAllowanceDocument, service, operation string, tokenID int, quote int64, now time.Time) serviceAllowanceDecision {
	r := doc.Rules[service]
	d := serviceAllowanceDecision{Enabled: r.Enabled, Allowed: true, Message: "shared allowance is disabled; existing authorization applies", Unit: r.Unit}
	if !r.Enabled {
		return d
	}
	d.Allowed = false
	d.Code = "service_approval_required"
	d.Message = "ask the human to approve this operation once or adjust the service allowance"
	period := allowancePeriod(r, now)
	d.Remaining = r.Limit
	canonical := canonicalAllowanceOperation(service, operation)
	if limit, ok := r.OperationLimits[canonical]; ok {
		remaining := limit
		d.OperationLimit = &limit
		d.OperationRemaining = &remaining
	}
	for _, receipt := range doc.Receipts {
		if receipt.Service == service && !receipt.Approved && (receipt.Period == period || receipt.Pending) {
			d.Remaining -= receipt.Amount
			if d.OperationRemaining != nil && canonicalAllowanceOperation(service, receipt.Operation) == canonical {
				*d.OperationRemaining -= receipt.Amount
			}
		}
	}
	if r.Mode == "deny" || (allowanceMatches(r.DeniedOperations, operation) || allowanceMatches(r.DeniedOperations, canonical)) {
		d.Code = "service_use_denied"
		d.Message = "human policy prohibits this service operation"
		return d
	}
	if tokenID <= 0 {
		d.Message = "an independent registered Agent identity is required"
		return d
	}
	// Human one-shot grants are scoped to service, operation, token and expiry.
	ids := make([]string, 0, len(doc.Grants))
	for id := range doc.Grants {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		g := doc.Grants[id]
		if g.Service == service && (g.Operation == operation || g.Operation == canonical) && g.TokenID == tokenID && !g.Used && g.ExpiresAt > now.Unix() {
			d.Allowed = true
			d.Code = ""
			d.GrantID = id
			d.Message = "human approved one call"
			return d
		}
	}
	if r.Mode == "approval" || (allowanceMatches(r.ApprovalOperations, operation) || allowanceMatches(r.ApprovalOperations, canonical)) {
		return d
	}
	d.Required = 1
	if r.Unit == "usd_micros" {
		d.Required = quote
		if quote < 0 {
			d.Message = "cost is not bounded by confirmed fixed request pricing; human approval is required"
			return d
		}
	}
	if d.Required > d.Remaining {
		d.Message = "shared service allowance exhausted; ask the human to approve once or increase the limit"
		return d
	}
	if d.OperationRemaining != nil && d.Required > *d.OperationRemaining {
		d.Message = "operation sub-allowance exhausted; ask the human to approve once or increase its limit"
		return d
	}
	d.Allowed = true
	d.Code = ""
	d.Message = "within human-configured shared allowance"
	return d
}
func (s *serviceAllowanceStore) evaluate(service, operation string, tokenID int, quote int64, reserve bool) (serviceAllowanceDecision, string, error) {
	var d serviceAllowanceDecision
	var id string
	err := s.transaction(reserve, func(doc *serviceAllowanceDocument) error {
		now := time.Now().UTC()
		d = allowanceDecision(doc, service, operation, tokenID, quote, now)
		if !reserve || !d.Enabled || !d.Allowed {
			return errAllowanceNoChange
		}
		id = newRelayRequestID()
		r := doc.Rules[service]
		receipt := serviceAllowanceReceipt{ID: id, Service: service, Operation: operation, TokenID: tokenID, Period: allowancePeriod(r, now), Amount: d.Required, Pending: r.Unit == "usd_micros", CreatedAt: now.Unix()}
		if d.GrantID != "" {
			g := doc.Grants[d.GrantID]
			g.Used = true
			doc.Grants[d.GrantID] = g
			receipt.Approved = true
			receipt.Amount = 0
			receipt.Pending = false
		}
		doc.Receipts[id] = receipt
		return nil
	})
	return d, id, err
}

// Unknown outcomes retain their reservation across restarts and period changes.
func (s *serviceAllowanceStore) settle(id string, amount int64) error {
	if id == "" || amount < 0 || amount > maxAllowanceAmount {
		return nil
	}
	return s.transaction(true, func(doc *serviceAllowanceDocument) error {
		r, ok := doc.Receipts[id]
		if !ok || !r.Pending {
			return nil
		}
		r.Amount = amount
		r.Pending = false
		r.SettledAt = time.Now().Unix()
		doc.Receipts[id] = r
		return nil
	})
}

// Variable/token/duration prices are deliberately not treated as a safe ceiling.
// Such calls can use request-count allowances or explicit human approval.
func fixedAllowanceQuote(def protocolDefinition, operation string) int64 {
	if def.Pricing == nil {
		return -1
	}
	found := false
	var rate float64
	for _, p := range def.Pricing.Entries {
		if p.Scope == "model" {
			return -1
		}
		if p.Scope != "operation" || p.ID != operation {
			continue
		}
		quantity, unit, ok := parseProtocolPricingUnit(p.Unit)
		if !ok || canonicalProtocolQuotaUnit(unit) != "request" || p.Amount == nil || p.Currency != "USD" || p.Status != "confirmed" || quantity <= 0 {
			return -1
		}
		value := *p.Amount / quantity
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return -1
		}
		if found && !sameProtocolPricingRate(rate, value) {
			return -1
		}
		rate = value
		found = true
	}
	if !found || rate*1e6 > float64(maxAllowanceAmount) {
		return -1
	}
	return int64(math.Ceil(rate * 1e6))
}
func (p *tokenPolicyStore) allowanceIdentity(tokenID int) int {
	if tokenID == p.protectedServiceTokenID {
		return 0
	}
	return tokenID
}
func (p *tokenPolicyStore) reserveAllowance(c *gin.Context, service, operation string, quote int64) (string, bool) {
	if p == nil || p.allowances == nil {
		return "", true
	}
	d, id, err := p.allowances.evaluate(service, operation, p.allowanceIdentity(c.GetInt(tokenPolicyContextID)), quote, true)
	if err != nil {
		writeAgentError(c, 503, "service_allowance_unavailable", "cannot safely read or persist service allowance", "request was not admitted", false, "localrouter", "ask the operator to repair allowance storage", nil, nil, nil)
		c.Abort()
		return "", false
	}
	if !d.Allowed {
		writeAgentError(c, 403, d.Code, d.Message, d.Message, false, "localrouter", "ask the human in /#protocols to approve this operation once or adjust the allowance; do not retry automatically", nil, nil, gin.H{"service": service, "operation": operation, "allowance": d})
		c.Abort()
		return "", false
	}
	if id != "" {
		c.Header("X-LocalRouter-Allowance-Receipt", id)
		c.Set("localrouter_allowance_single_attempt", true)
	}
	return id, true
}
func (p *tokenPolicyStore) finishAllowance(c *gin.Context, id string, def protocolDefinition, operation string) {
	if id == "" || p == nil || p.allowances == nil {
		return
	}
	// Only non-streaming successful, fully observed fixed-price responses settle.
	if c.Writer.Status() < 200 || c.Writer.Status() >= 300 || c.GetString("localrouter_protocol_outcome") == "unknown" || strings.Contains(c.Writer.Header().Get("Content-Type"), "text/event-stream") {
		return
	}
	metrics, _ := c.Get("localrouter_usage_metrics")
	usage, _ := metrics.(usageMetrics)
	cost := protocolUsageCost(def, operation, c.GetString("localrouter_usage_model"), usage, true)
	if cost == nil || (cost.Status != "confirmed" && cost.Status != "reported") || cost.AmountUSD < 0 || math.IsNaN(cost.AmountUSD) || math.IsInf(cost.AmountUSD, 0) {
		return
	}
	// Persistence failure leaves the original reservation intact (fail closed).
	_ = p.allowances.settle(id, int64(math.Ceil(cost.AmountUSD*1e6)))
}

func decodeAllowanceRequest(c *gin.Context, v any) error {
	decoder := json.NewDecoder(io.LimitReader(c.Request.Body, 128<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("expected one JSON object")
	}
	return nil
}
func allowanceAdminError(c *gin.Context, err error) {
	c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
}
func allowanceServices(runtime localRuntime) map[string]string {
	result := map[string]string{}
	for _, v := range runtime.protocols.views() {
		result[v.ID] = v.Name
	}
	for _, v := range runtime.channelProfiles.compatibilityViews(runtime.store) {
		result[v.PackKey] = v.Name
	}
	return result
}
func handleAllowanceList(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		services := allowanceServices(runtime)
		items := []gin.H{}
		err := runtime.policies.allowances.transaction(false, func(doc *serviceAllowanceDocument) error {
			for key := range doc.Rules {
				if _, ok := services[key]; !ok {
					services[key] = key
				}
			}
			keys := make([]string, 0, len(services))
			for key := range services {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				rule, ok := doc.Rules[key]
				if !ok {
					rule = defaultAllowanceRule()
				}
				pending := []serviceAllowanceReceipt{}
				var spent, reserved int64
				for _, r := range doc.Receipts {
					if r.Service != key || r.Approved {
						continue
					}
					if r.Pending {
						reserved += r.Amount
						pending = append(pending, r)
					} else if r.Period == allowancePeriod(rule, time.Now()) {
						spent += r.Amount
					}
				}
				sort.Slice(pending, func(i, j int) bool { return pending[i].CreatedAt < pending[j].CreatedAt })
				items = append(items, gin.H{"service": key, "name": services[key], "rule": rule, "spent": spent, "reserved": reserved, "remaining": rule.Limit - spent - reserved, "pending": pending, "operations": allowanceOperationViews(runtime, key, rule, doc)})
			}
			return nil
		})
		if err != nil {
			c.JSON(503, gin.H{"success": false, "message": "cannot read allowance state"})
			return
		}
		c.JSON(200, gin.H{"success": true, "data": items})
	}
}
func handleAllowancePut(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		service := c.Param("service")
		if _, ok := allowanceServices(runtime)[service]; !ok {
			allowanceAdminError(c, errors.New("unknown service"))
			return
		}
		var rule serviceAllowanceRule
		if err := decodeAllowanceRequest(c, &rule); err != nil {
			allowanceAdminError(c, err)
			return
		}
		if err := validateAllowanceConfig(runtime, service, &rule); err != nil {
			allowanceAdminError(c, err)
			return
		}
		err := runtime.policies.allowances.transaction(true, func(doc *serviceAllowanceDocument) error { return applyAllowanceRule(doc, service, &rule) })
		if err != nil {
			allowanceAdminError(c, err)
			return
		}
		c.JSON(200, gin.H{"success": true, "data": rule})
	}
}
func handleAllowanceGrant(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input struct {
			TokenID   int    `json:"token_id"`
			Operation string `json:"operation"`
		}
		if err := decodeAllowanceRequest(c, &input); err != nil {
			allowanceAdminError(c, err)
			return
		}
		service := c.Param("service")
		input.Operation = strings.TrimSpace(input.Operation)
		if input.TokenID <= 0 || runtime.policies.allowanceIdentity(input.TokenID) == 0 || input.Operation == "" || strings.Contains(input.Operation, "*") {
			allowanceAdminError(c, errors.New("independent token and exact operation required"))
			return
		}
		if !allowanceOperationExists(runtime, service, input.Operation) {
			allowanceAdminError(c, errors.New("unknown operation"))
			return
		}
		token, err := runtime.store.tokenByID(runtime.rootUser.ID, input.TokenID, false)
		if err != nil || token.Status != localStatusEnabled || (token.ExpiredTime >= 0 && token.ExpiredTime <= time.Now().Unix()) || token.AgentCode == "" || token.Workspace == "" || runtime.policies.hasCapability(input.TokenID, localRouterMaintainCapability) {
			allowanceAdminError(c, errors.New("active independent service token required"))
			return
		}
		grant := serviceAllowanceGrant{ID: newRelayRequestID(), Service: service, Operation: input.Operation, TokenID: input.TokenID, ExpiresAt: time.Now().Add(time.Hour).Unix()}
		err = runtime.policies.allowances.transaction(true, func(doc *serviceAllowanceDocument) error {
			r := doc.Rules[service]
			if !r.Enabled || r.Mode == "deny" || (allowanceMatches(r.DeniedOperations, input.Operation) || allowanceMatches(r.DeniedOperations, canonicalAllowanceOperation(service, input.Operation))) {
				return errors.New("service allowance is disabled or operation is prohibited")
			}
			doc.Grants[grant.ID] = grant
			return nil
		})
		if err != nil {
			allowanceAdminError(c, err)
			return
		}
		c.JSON(200, gin.H{"success": true, "data": grant})
	}
}
func handleAllowanceReconcile(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input struct {
			Amount int64  `json:"amount"`
			Reason string `json:"reason"`
		}
		if err := decodeAllowanceRequest(c, &input); err != nil {
			allowanceAdminError(c, err)
			return
		}
		if input.Amount < 0 || input.Amount > maxAllowanceAmount || strings.TrimSpace(input.Reason) == "" {
			allowanceAdminError(c, errors.New("confirmed nonnegative amount and reconciliation reason required"))
			return
		}
		err := runtime.policies.allowances.transaction(true, func(doc *serviceAllowanceDocument) error {
			id := c.Param("receipt")
			r, ok := doc.Receipts[id]
			if !ok || r.Service != c.Param("service") || !r.Pending {
				return fmt.Errorf("pending receipt not found")
			}
			r.Amount = input.Amount
			r.Pending = false
			r.SettledAt = time.Now().Unix()
			r.ReconciliationReason = strings.TrimSpace(input.Reason)
			doc.Receipts[id] = r
			return nil
		})
		if err != nil {
			allowanceAdminError(c, err)
			return
		}
		c.JSON(200, gin.H{"success": true})
	}
}

// Service and maintenance credentials are never an authority to edit human
// allowance policy, including when the local human console is unlocked.
func humanAllowanceAccess(c *gin.Context) {
	if c.GetHeader("Authorization") != "" || c.GetHeader("X-Api-Key") != "" || c.GetHeader("X-Goog-Api-Key") != "" || c.Query("key") != "" {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"success": false, "message": "service allowances are configured by the human console, not Agent credentials"})
		return
	}
	c.Next()
}

func (p *tokenPolicyStore) beginProtocolAllowance(c *gin.Context, def protocolDefinition, operation string) (func(), bool) {
	receipt, ok := p.reserveAllowance(c, def.ID, operation, fixedAllowanceQuote(def, operation))
	return func() { p.finishAllowance(c, receipt, def, operation) }, ok
}

func allowanceOperationExists(runtime localRuntime, service, operation string) bool {
	if strings.HasPrefix(service, "compatibility:") {
		parts := strings.SplitN(operation, " ", 2)
		if len(parts) != 2 || !containsString([]string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}, parts[0]) || strings.ContainsAny(parts[1], "? #") || unsafeProtocolPath(parts[1]) {
			return false
		}
		profile, ok := runtime.channelProfiles.matchRequestPath(parts[1])
		return ok && service == "compatibility:"+profile.Key
	}
	def, ok := runtime.protocols.get(service)
	if !ok {
		return false
	}
	for _, route := range def.Routes {
		if route.OperationID == operation {
			return true
		}
	}
	return false
}

// Compatibility request templates group dynamic model paths into one published
// operation while preserving legacy exact-path policies and receipt records.
func canonicalAllowanceOperation(service, operation string) string {
	if !strings.HasPrefix(service, "compatibility:") {
		return operation
	}
	parts := strings.SplitN(operation, " ", 2)
	if len(parts) != 2 {
		return operation
	}
	for _, route := range localRelayRouteDefinitions() {
		if route.Status != "available" || !containsString(route.Methods, parts[0]) {
			continue
		}
		// The compatibility relay mounts {path} as a trailing wildcard.
		if strings.HasSuffix(route.Path, "/{path}") {
			prefix := strings.TrimSuffix(route.Path, "{path}")
			if strings.HasPrefix(parts[1], prefix) && len(parts[1]) > len(prefix) {
				return parts[0] + " " + route.Path
			}
			continue
		}
		if _, ok := matchProtocolPath(route.Path, parts[1]); ok {
			return parts[0] + " " + route.Path
		}
	}
	return operation
}
func allowanceOperationNames(runtime localRuntime, service string) map[string]string {
	result := map[string]string{}
	if strings.HasPrefix(service, "compatibility:") {
		for _, pack := range runtime.channelProfiles.compatibilityViews(runtime.store) {
			if pack.PackKey != service {
				continue
			}
			for _, route := range pack.Routes {
				if route.Status != "available" {
					continue
				}
				for _, method := range route.Methods {
					if method == http.MethodPost {
						result[method+" "+route.Path] = route.Path
					}
				}
			}
		}
	} else if def, ok := runtime.protocols.get(service); ok {
		for _, route := range def.Routes {
			result[route.OperationID] = route.Summary
		}
	}
	return result
}
func allowanceOperationViews(runtime localRuntime, service string, rule serviceAllowanceRule, doc *serviceAllowanceDocument) []gin.H {
	names := allowanceOperationNames(runtime, service)
	for operation := range rule.OperationLimits {
		if _, ok := names[operation]; !ok {
			names[operation] = operation
		}
	}
	keys := make([]string, 0, len(names))
	for key := range names {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := []gin.H{}
	period := allowancePeriod(rule, time.Now())
	for _, op := range keys {
		var spent, reserved int64
		for _, r := range doc.Receipts {
			if r.Service != service || r.Approved || canonicalAllowanceOperation(service, r.Operation) != op {
				continue
			}
			if r.Pending {
				reserved += r.Amount
			} else if r.Period == period {
				spent += r.Amount
			}
		}
		limit, configured := rule.OperationLimits[op]
		result = append(result, gin.H{"id": op, "name": names[op], "configured": configured, "limit": limit, "spent": spent, "reserved": reserved, "remaining": limit - spent - reserved})
	}
	return result
}
func validateAllowanceConfig(runtime localRuntime, service string, rule *serviceAllowanceRule) error {
	if _, ok := allowanceServices(runtime)[service]; !ok {
		return errors.New("unknown service")
	}
	if err := validateAllowanceRule(rule); err != nil {
		return err
	}
	for _, op := range append(append([]string{}, rule.ApprovalOperations...), rule.DeniedOperations...) {
		if op != "*" && !allowanceOperationExists(runtime, service, op) {
			return errors.New("unknown operation: " + op)
		}
	}
	names := allowanceOperationNames(runtime, service)
	for op := range rule.OperationLimits {
		if _, ok := names[op]; !ok {
			return errors.New("unknown sub-allowance operation: " + op)
		}
	}
	return nil
}
func applyAllowanceRule(doc *serviceAllowanceDocument, service string, rule *serviceAllowanceRule) error {
	old := doc.Rules[service]
	if old.Unit != "" && old.Unit != rule.Unit && len(old.OperationLimits) > 0 {
		return errors.New("clear existing operation sub-allowances before changing units")
	}
	if old.Revision != rule.Revision {
		return errors.New("configuration changed; reload before saving")
	}
	for _, r := range doc.Receipts {
		if r.Service == service && (old.Unit != rule.Unit || old.Period != rule.Period) {
			return errors.New("unit and period cannot change after use; existing usage must be preserved")
		}
	}
	rule.Revision++
	for id, grant := range doc.Grants {
		if grant.Service == service && !grant.Used {
			delete(doc.Grants, id)
		}
	}
	doc.Rules[service] = *rule
	return nil
}
func handleAllowanceBatch(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input struct {
			Services []struct {
				Service string               `json:"service"`
				Rule    serviceAllowanceRule `json:"rule"`
			} `json:"services"`
		}
		if err := decodeAllowanceRequest(c, &input); err != nil {
			allowanceAdminError(c, err)
			return
		}
		if len(input.Services) == 0 || len(input.Services) > 100 {
			allowanceAdminError(c, errors.New("select between 1 and 100 services"))
			return
		}
		seen := map[string]bool{}
		for i := range input.Services {
			item := &input.Services[i]
			if seen[item.Service] {
				allowanceAdminError(c, errors.New("duplicate service"))
				return
			}
			seen[item.Service] = true
			if err := validateAllowanceConfig(runtime, item.Service, &item.Rule); err != nil {
				allowanceAdminError(c, err)
				return
			}
		}
		err := runtime.policies.allowances.transaction(true, func(doc *serviceAllowanceDocument) error {
			for i := range input.Services {
				item := &input.Services[i]
				if err := applyAllowanceRule(doc, item.Service, &item.Rule); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			allowanceAdminError(c, err)
			return
		}
		c.JSON(200, gin.H{"success": true, "data": input.Services})
	}
}
