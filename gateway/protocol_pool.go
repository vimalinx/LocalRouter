package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type protocolPoolConfig struct {
	Mode                        string                    `json:"mode"`
	CredentialsFile             string                    `json:"credentials_file,omitempty"`
	Strategy                    string                    `json:"strategy,omitempty"`
	MaxAttempts                 int                       `json:"max_attempts,omitempty"`
	MaxInFlightPerCredential    int                       `json:"max_inflight_per_credential,omitempty"`
	MinBalance                  float64                   `json:"min_balance,omitempty"`
	UnauthorizedAction          string                    `json:"unauthorized_action,omitempty"`
	UnauthorizedCooldownSeconds int                       `json:"unauthorized_cooldown_seconds,omitempty"`
	RateLimitCooldownSeconds    int                       `json:"rate_limit_cooldown_seconds,omitempty"`
	TransientCooldownSeconds    int                       `json:"transient_cooldown_seconds,omitempty"`
	InFlightLeaseSeconds        int                       `json:"inflight_lease_seconds,omitempty"`
	QuotaStaleAfterSeconds      int                       `json:"quota_stale_after_seconds,omitempty"`
	Source                      *protocolPoolSourceConfig `json:"source,omitempty"`
}

type protocolPoolSourceConfig struct {
	Format             string            `json:"format"`
	LocatorFile        string            `json:"locator_file"`
	RecordsPath        string            `json:"records_path,omitempty"`
	IDPath             string            `json:"id_path"`
	SecretPath         string            `json:"secret_path"`
	SecretCodec        string            `json:"secret_codec,omitempty"`
	SecretSelector     string            `json:"secret_selector,omitempty"`
	SecretFields       map[string]string `json:"secret_fields,omitempty"`
	DisabledPath       string            `json:"disabled_path,omitempty"`
	EligiblePath       string            `json:"eligible_path,omitempty"`
	BalancePath        string            `json:"balance_path,omitempty"`
	QuotaTotalPath     string            `json:"quota_total_path,omitempty"`
	QuotaRemainingPath string            `json:"quota_remaining_path,omitempty"`
	QuotaUsedPath      string            `json:"quota_used_path,omitempty"`
	QuotaUnitPath      string            `json:"quota_unit_path,omitempty"`
	QuotaStatusPath    string            `json:"quota_status_path,omitempty"`
	QuotaCheckedAtPath string            `json:"quota_checked_at_path,omitempty"`
	WeightPath         string            `json:"weight_path,omitempty"`
	PriorityPath       string            `json:"priority_path,omitempty"`
	ExpiresAtPath      string            `json:"expires_at_path,omitempty"`
	MetadataPaths      map[string]string `json:"metadata_paths,omitempty"`
}

type protocolPoolSourceLocator struct {
	SchemaVersion string `json:"schema_version"`
	Path          string `json:"path"`
}

type protocolCredentialFile struct {
	SchemaVersion string               `json:"schema_version"`
	Credentials   []protocolCredential `json:"credentials"`
}

type protocolCredential struct {
	ID        string                   `json:"id"`
	Secret    string                   `json:"secret"`
	Disabled  bool                     `json:"disabled,omitempty"`
	Priority  int                      `json:"priority,omitempty"`
	Weight    int                      `json:"weight,omitempty"`
	Balance   float64                  `json:"balance,omitempty"`
	Quota     *protocolCredentialQuota `json:"quota,omitempty"`
	ExpiresAt string                   `json:"expires_at,omitempty"`
	Metadata  map[string]string        `json:"metadata,omitempty"`
}

type protocolCredentialQuota struct {
	Total     *float64 `json:"total,omitempty"`
	Remaining *float64 `json:"remaining,omitempty"`
	Used      *float64 `json:"used,omitempty"`
	Unit      string   `json:"unit,omitempty"`
	Status    string   `json:"status,omitempty"`
	CheckedAt string   `json:"checked_at,omitempty"`
}

type protocolCredentialRuntime struct {
	LastUsed            string           `json:"last_used,omitempty"`
	CooldownUntil       int64            `json:"cooldown_until,omitempty"`
	ConsecutiveFailures int              `json:"consecutive_failures,omitempty"`
	InFlight            int              `json:"in_flight,omitempty"`
	DisabledReason      string           `json:"disabled_reason,omitempty"`
	CurrentWeight       int              `json:"current_weight,omitempty"`
	Leases              map[string]int64 `json:"leases,omitempty"`
}

type protocolAffinityRuntime struct {
	CredentialID string `json:"credential_id"`
	ExpiresAt    int64  `json:"expires_at"`
}

type protocolPoolState struct {
	Credentials map[string]*protocolCredentialRuntime `json:"credentials"`
	Affinity    map[string]protocolAffinityRuntime    `json:"affinity"`
	Cursor      int                                   `json:"cursor,omitempty"`
}

type protocolPoolView struct {
	Mode       string `json:"mode"`
	Strategy   string `json:"strategy,omitempty"`
	Source     string `json:"source,omitempty"`
	Total      int    `json:"total,omitempty"`
	Eligible   int    `json:"eligible,omitempty"`
	InFlight   int    `json:"in_flight,omitempty"`
	Affinities int    `json:"affinities,omitempty"`
}

type protocolPoolAccountView struct {
	Ref                 string                   `json:"ref"`
	Label               string                   `json:"label"`
	Status              string                   `json:"status"`
	StatusLabel         string                   `json:"status_label"`
	Balance             *float64                 `json:"balance,omitempty"`
	Quota               protocolQuotaAccountView `json:"quota"`
	InFlight            int                      `json:"in_flight"`
	ConsecutiveFailures int                      `json:"consecutive_failures,omitempty"`
	CooldownUntil       string                   `json:"cooldown_until,omitempty"`
	LastUsed            string                   `json:"last_used,omitempty"`
	Targets             []string                 `json:"targets,omitempty"`
}

type protocolQuotaAccountView struct {
	Tracked        bool                             `json:"tracked"`
	Status         string                           `json:"status"`
	Total          *float64                         `json:"total,omitempty"`
	Remaining      *float64                         `json:"remaining,omitempty"`
	Used           *float64                         `json:"used,omitempty"`
	UsedPercent    *float64                         `json:"used_percent,omitempty"`
	Unit           string                           `json:"unit,omitempty"`
	CheckedAt      string                           `json:"checked_at,omitempty"`
	Stale          bool                             `json:"stale"`
	ReferenceValue *protocolQuotaReferenceValueView `json:"reference_value,omitempty"`
}

type protocolQuotaRuntimeView struct {
	Status            string                           `json:"status"`
	TrackedAccounts   int                              `json:"tracked_accounts"`
	ConfirmedAccounts int                              `json:"confirmed_accounts"`
	UnknownAccounts   int                              `json:"unknown_accounts"`
	StaleAccounts     int                              `json:"stale_accounts"`
	Total             *float64                         `json:"total,omitempty"`
	Remaining         *float64                         `json:"remaining,omitempty"`
	Used              *float64                         `json:"used,omitempty"`
	UsedPercent       *float64                         `json:"used_percent,omitempty"`
	Unit              string                           `json:"unit,omitempty"`
	ReferenceValue    *protocolQuotaReferenceValueView `json:"reference_value,omitempty"`
}

// protocolQuotaReferenceValueView is a rate-derived estimate, not an account
// invoice or proof of payment. It is emitted only when a single pricing rate
// has the same denominator unit as the quota telemetry.
type protocolQuotaReferenceValueView struct {
	Status    string   `json:"status"`
	Currency  string   `json:"currency,omitempty"`
	PricingID string   `json:"pricing_id,omitempty"`
	Total     *float64 `json:"total,omitempty"`
	Remaining *float64 `json:"remaining,omitempty"`
	Used      *float64 `json:"used,omitempty"`
}

type protocolPoolRuntimeView struct {
	Ownership        string                    `json:"ownership"`
	Status           string                    `json:"status"`
	Total            int                       `json:"total"`
	Ready            int                       `json:"ready"`
	Cooling          int                       `json:"cooling"`
	Disabled         int                       `json:"disabled"`
	Expired          int                       `json:"expired"`
	Busy             int                       `json:"busy"`
	BalanceLow       int                       `json:"balance_low"`
	Unroutable       int                       `json:"unroutable"`
	BalanceTracked   bool                      `json:"balance_tracked"`
	BalanceRemaining float64                   `json:"balance_remaining,omitempty"`
	BalanceEmpty     int                       `json:"balance_empty"`
	Quota            protocolQuotaRuntimeView  `json:"quota"`
	InFlight         int                       `json:"in_flight"`
	Accounts         []protocolPoolAccountView `json:"accounts"`
}

type acquiredProtocolCredential struct {
	Credential protocolCredential
	Pooled     bool
	LeaseID    string
}

func (registry *protocolRegistry) startCredentialLeaseHeartbeat(definition protocolDefinition, acquired acquiredProtocolCredential) func() {
	if !acquired.Pooled || acquired.LeaseID == "" || definition.Pool == nil {
		return func() {}
	}
	interval := time.Duration(definition.Pool.InFlightLeaseSeconds) * time.Second / 3
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_ = registry.renewCredentialLease(definition, acquired)
			case <-stop:
				return
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() { close(stop) })
		<-done
	}
}

func (registry *protocolRegistry) renewCredentialLease(definition protocolDefinition, acquired acquiredProtocolCredential) error {
	if !acquired.Pooled || acquired.LeaseID == "" || definition.Pool == nil {
		return nil
	}
	registry.poolMu.Lock()
	defer registry.poolMu.Unlock()
	stateLock, err := registry.lockProtocolState("pool", definition.ID)
	if err != nil {
		return err
	}
	defer unlockProtocolState(stateLock)
	state, err := registry.loadPoolState(definition.ID)
	if err != nil {
		return err
	}
	runtime := state.Credentials[acquired.Credential.ID]
	if runtime == nil {
		runtime = &protocolCredentialRuntime{}
		state.Credentials[acquired.Credential.ID] = runtime
	}
	if runtime.Leases == nil {
		runtime.Leases = make(map[string]int64)
	}
	pruneProtocolCredentialLeases(runtime, time.Now())
	runtime.Leases[acquired.LeaseID] = time.Now().Add(time.Duration(definition.Pool.InFlightLeaseSeconds) * time.Second).Unix()
	runtime.InFlight = len(runtime.Leases)
	return registry.savePoolState(definition.ID, state)
}

func (registry *protocolRegistry) validatePool(definition *protocolDefinition) error {
	pool := definition.Pool
	if pool == nil {
		return nil
	}
	if pool.Mode != "local" && pool.Mode != "external" {
		return errors.New("pool mode must be local or external")
	}
	if pool.Mode == "external" {
		if pool.CredentialsFile != "" || pool.Source != nil {
			return errors.New("external pool cannot define credentials_file or source")
		}
		if definition.Auth.Type != "none" && definition.Auth.SecretFile == "" {
			return errors.New("external pool authentication requires a stable secret_file")
		}
		pool.Strategy = "delegated"
		return nil
	}
	if (pool.CredentialsFile == "") == (pool.Source == nil) {
		return errors.New("local pool requires exactly one credentials_file or source")
	}
	if definition.Auth.Type == "none" {
		return errors.New("local pool requires an authentication type")
	}
	if definition.Auth.SecretFile != "" {
		return errors.New("local pool auth must use credentials_file instead of secret_file")
	}
	if pool.CredentialsFile != "" {
		if _, err := registry.secretPath(pool.CredentialsFile); err != nil {
			return fmt.Errorf("credentials_file: %w", err)
		}
	} else if err := registry.validatePoolSource(pool.Source); err != nil {
		return fmt.Errorf("source: %w", err)
	}
	if pool.Strategy == "" {
		pool.Strategy = "round-robin"
	}
	switch pool.Strategy {
	case "round-robin", "lru", "least-inflight", "balance-aware", "smooth-weighted":
	default:
		return errors.New("pool strategy must be round-robin, lru, least-inflight, balance-aware, or smooth-weighted")
	}
	if pool.MaxAttempts == 0 {
		pool.MaxAttempts = 3
	}
	if pool.MaxAttempts < 1 || pool.MaxAttempts > 20 {
		return errors.New("pool max_attempts must be between 1 and 20")
	}
	if pool.MaxInFlightPerCredential < 0 || pool.MaxInFlightPerCredential > 10000 {
		return errors.New("pool max_inflight_per_credential must be between 0 and 10000")
	}
	if pool.UnauthorizedAction == "" {
		pool.UnauthorizedAction = "disable"
	}
	if pool.UnauthorizedAction != "disable" && pool.UnauthorizedAction != "cooldown" {
		return errors.New("pool unauthorized_action must be disable or cooldown")
	}
	if pool.UnauthorizedCooldownSeconds == 0 {
		pool.UnauthorizedCooldownSeconds = 600
	}
	if pool.RateLimitCooldownSeconds == 0 {
		pool.RateLimitCooldownSeconds = 120
	}
	if pool.TransientCooldownSeconds == 0 {
		pool.TransientCooldownSeconds = 30
	}
	for name, duration := range map[string]int{
		"unauthorized_cooldown_seconds": pool.UnauthorizedCooldownSeconds,
		"rate_limit_cooldown_seconds":   pool.RateLimitCooldownSeconds,
		"transient_cooldown_seconds":    pool.TransientCooldownSeconds,
	} {
		if duration < 1 || duration > 7*24*60*60 {
			return fmt.Errorf("pool %s must be between 1 and 604800", name)
		}
	}
	if pool.InFlightLeaseSeconds == 0 {
		pool.InFlightLeaseSeconds = definition.Timeout + 60
	}
	if pool.InFlightLeaseSeconds < 30 || pool.InFlightLeaseSeconds > 86400 {
		return errors.New("pool inflight_lease_seconds must be between 30 and 86400")
	}
	if pool.QuotaStaleAfterSeconds == 0 {
		pool.QuotaStaleAfterSeconds = 24 * 60 * 60
	}
	if pool.QuotaStaleAfterSeconds < 60 || pool.QuotaStaleAfterSeconds > 365*24*60*60 {
		return errors.New("pool quota_stale_after_seconds must be between 60 and 31536000")
	}
	return nil
}

func (registry *protocolRegistry) loadPoolCredentials(definition protocolDefinition) ([]protocolCredential, error) {
	if definition.Pool == nil || definition.Pool.Mode != "local" {
		return nil, errors.New("protocol does not use a local pool")
	}
	if definition.Pool.Source != nil {
		return registry.loadMappedPoolCredentials(*definition.Pool.Source)
	}
	path, err := registry.secretPath(definition.Pool.CredentialsFile)
	if err != nil {
		return nil, err
	}
	if err := requirePrivateRegularFile(path, "credential pool file"); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file protocolCredentialFile
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("credential pool has trailing JSON data")
	}
	if file.SchemaVersion != "1" || len(file.Credentials) == 0 {
		return nil, errors.New("credential pool requires schema_version 1 and at least one credential")
	}
	seen := make(map[string]bool)
	for index := range file.Credentials {
		credential := &file.Credentials[index]
		if !protocolOperationIDPattern.MatchString(credential.ID) || credential.Secret == "" {
			return nil, fmt.Errorf("credential %d requires a safe id and non-empty secret", index+1)
		}
		if seen[credential.ID] {
			return nil, fmt.Errorf("duplicate credential id %q", credential.ID)
		}
		seen[credential.ID] = true
		if credential.Weight == 0 {
			credential.Weight = 1
		}
		if credential.Weight < 1 || credential.Weight > 10000 {
			return nil, fmt.Errorf("credential %s weight must be between 1 and 10000", credential.ID)
		}
		if credential.ExpiresAt != "" {
			if _, err := time.Parse(time.RFC3339, credential.ExpiresAt); err != nil {
				return nil, fmt.Errorf("credential %s expires_at must be RFC3339", credential.ID)
			}
		}
		credential.Quota = normalizeProtocolQuota(credential.Quota, time.Now(), definition.Pool.QuotaStaleAfterSeconds)
		if credential.Quota != nil && credential.Quota.Remaining != nil {
			credential.Balance = *credential.Quota.Remaining
		} else if (definition.Pool.MinBalance > 0 || definition.Pool.Strategy == "balance-aware") && credential.Quota == nil {
			balance := credential.Balance
			credential.Quota = normalizeProtocolQuota(&protocolCredentialQuota{Remaining: &balance, Status: "confirmed"}, time.Now(), definition.Pool.QuotaStaleAfterSeconds)
		}
	}
	return file.Credentials, nil
}

func (registry *protocolRegistry) validatePoolSource(source *protocolPoolSourceConfig) error {
	if source == nil {
		return errors.New("source is required")
	}
	if source.Format != "json" && source.Format != "jsonl" {
		return errors.New("format must be json or jsonl")
	}
	if source.Format == "jsonl" && source.RecordsPath != "" {
		return errors.New("records_path is only valid for json sources")
	}
	if _, err := registry.secretPath(source.LocatorFile); err != nil {
		return fmt.Errorf("locator_file: %w", err)
	}
	if source.IDPath == "" || source.SecretPath == "" {
		return errors.New("id_path and secret_path are required")
	}
	if source.SecretCodec == "" {
		source.SecretCodec = "plain"
	}
	if source.SecretCodec != "plain" && source.SecretCodec != "cookie-list-json" && source.SecretCodec != "json-fields" {
		return errors.New("secret_codec must be plain, cookie-list-json, or json-fields")
	}
	if source.SecretCodec == "cookie-list-json" && strings.TrimSpace(source.SecretSelector) == "" {
		return errors.New("cookie-list-json requires secret_selector")
	}
	if source.SecretCodec == "json-fields" {
		if len(source.SecretFields) == 0 {
			return errors.New("json-fields requires secret_fields")
		}
		for name, path := range source.SecretFields {
			if !protocolOperationIDPattern.MatchString(name) {
				return fmt.Errorf("secret_fields key %q is unsafe", name)
			}
			if err := validateJSONTransformPath(path); err != nil {
				return fmt.Errorf("secret_fields.%s: %w", name, err)
			}
		}
	}
	for name, path := range map[string]string{
		"records_path": source.RecordsPath, "id_path": source.IDPath, "secret_path": source.SecretPath,
		"disabled_path": source.DisabledPath, "eligible_path": source.EligiblePath, "balance_path": source.BalancePath,
		"quota_total_path": source.QuotaTotalPath, "quota_remaining_path": source.QuotaRemainingPath,
		"quota_used_path": source.QuotaUsedPath, "quota_unit_path": source.QuotaUnitPath,
		"quota_status_path": source.QuotaStatusPath, "quota_checked_at_path": source.QuotaCheckedAtPath,
		"weight_path": source.WeightPath, "priority_path": source.PriorityPath, "expires_at_path": source.ExpiresAtPath,
	} {
		if path != "" {
			if err := validateJSONTransformPath(path); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	for name, path := range source.MetadataPaths {
		if !protocolOperationIDPattern.MatchString(name) {
			return fmt.Errorf("metadata_paths key %q is unsafe", name)
		}
		if err := validateJSONTransformPath(path); err != nil {
			return fmt.Errorf("metadata_paths.%s: %w", name, err)
		}
	}
	return nil
}

func (registry *protocolRegistry) loadMappedPoolCredentials(source protocolPoolSourceConfig) ([]protocolCredential, error) {
	path, err := registry.resolvePoolSource(source.LocatorFile)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) > 32<<20 {
		return nil, errors.New("external pool source exceeds 32 MiB")
	}
	records, err := mappedPoolRecords(data, source)
	if err != nil {
		return nil, err
	}
	credentials := make([]protocolCredential, 0, len(records))
	seen := make(map[string]bool)
	for _, record := range records {
		identity := strings.TrimSpace(gjson.GetBytes(record, source.IDPath).String())
		secretRaw := gjson.GetBytes(record, source.SecretPath)
		if identity == "" || (source.SecretCodec != "json-fields" && !secretRaw.Exists()) {
			continue
		}
		secret, codecExpiry, err := decodeMappedPoolSecret(record, secretRaw.String(), source)
		if err != nil || secret == "" {
			continue
		}
		digest := sha256.Sum256([]byte(identity))
		id := "src-" + hex.EncodeToString(digest[:16])
		if seen[id] {
			return nil, errors.New("external pool source contains duplicate identities")
		}
		seen[id] = true
		credential := protocolCredential{ID: id, Secret: secret, Weight: 1, ExpiresAt: codecExpiry}
		if source.DisabledPath != "" && gjson.GetBytes(record, source.DisabledPath).Bool() {
			credential.Disabled = true
		}
		if source.EligiblePath != "" && !gjson.GetBytes(record, source.EligiblePath).Bool() {
			credential.Disabled = true
		}
		if protocolPoolSourceHasQuota(source) {
			quota := &protocolCredentialQuota{
				Total:     mappedPoolNumber(record, source.QuotaTotalPath),
				Remaining: mappedPoolNumber(record, source.QuotaRemainingPath),
				Used:      mappedPoolNumber(record, source.QuotaUsedPath),
			}
			if quota.Remaining == nil {
				quota.Remaining = mappedPoolNumber(record, source.BalancePath)
				if quota.Remaining != nil {
					quota.Status = "estimated"
				}
			}
			if source.QuotaUnitPath != "" {
				quota.Unit = strings.TrimSpace(gjson.GetBytes(record, source.QuotaUnitPath).String())
			}
			if source.QuotaStatusPath != "" {
				if status := strings.TrimSpace(gjson.GetBytes(record, source.QuotaStatusPath).String()); status != "" {
					quota.Status = status
				}
			}
			if source.QuotaCheckedAtPath != "" {
				quota.CheckedAt = strings.TrimSpace(gjson.GetBytes(record, source.QuotaCheckedAtPath).String())
			}
			credential.Quota = normalizeProtocolQuota(quota, time.Now(), 0)
			if credential.Quota.Remaining != nil {
				credential.Balance = *credential.Quota.Remaining
			}
		}
		if source.WeightPath != "" {
			credential.Weight = int(gjson.GetBytes(record, source.WeightPath).Int())
			if credential.Weight == 0 {
				credential.Weight = 1
			}
		}
		if credential.Weight < 1 || credential.Weight > 10000 {
			return nil, fmt.Errorf("mapped credential %s weight must be between 1 and 10000", credential.ID)
		}
		if source.PriorityPath != "" {
			credential.Priority = int(gjson.GetBytes(record, source.PriorityPath).Int())
		}
		if source.ExpiresAtPath != "" {
			credential.ExpiresAt = mappedPoolExpiry(gjson.GetBytes(record, source.ExpiresAtPath))
		}
		if len(source.MetadataPaths) > 0 {
			credential.Metadata = make(map[string]string, len(source.MetadataPaths))
			for name, path := range source.MetadataPaths {
				value := gjson.GetBytes(record, path)
				if value.Exists() {
					credential.Metadata[name] = value.String()
				}
			}
		}
		credentials = append(credentials, credential)
	}
	if len(credentials) == 0 {
		return nil, errors.New("external pool source has no usable credentials")
	}
	return credentials, nil
}

func (registry *protocolRegistry) resolvePoolSource(locatorRelative string) (string, error) {
	locatorPath, err := registry.secretPath(locatorRelative)
	if err != nil {
		return "", err
	}
	locatorInfo, err := os.Lstat(locatorPath)
	if err != nil {
		return "", err
	}
	if !locatorInfo.Mode().IsRegular() || locatorInfo.Mode().Perm()&0o077 != 0 {
		return "", errors.New("pool source locator must be a private regular file")
	}
	if stat, ok := locatorInfo.Sys().(*syscall.Stat_t); !ok || int(stat.Uid) != os.Geteuid() {
		return "", errors.New("pool source locator must be owned by the LocalRouter user")
	}
	locatorData, err := os.ReadFile(locatorPath)
	if err != nil {
		return "", err
	}
	var locator protocolPoolSourceLocator
	decoder := json.NewDecoder(io.LimitReader(strings.NewReader(string(locatorData)), 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&locator); err != nil || locator.SchemaVersion != "1" || !filepath.IsAbs(locator.Path) {
		return "", errors.New("pool source locator requires schema_version 1 and an absolute path")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "", errors.New("pool source locator has trailing JSON data")
	}
	resolved, err := filepath.EvalSymlinks(locator.Path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("external pool source must be a private regular file")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || int(stat.Uid) != os.Geteuid() {
		return "", errors.New("external pool source must be owned by the LocalRouter user")
	}
	return resolved, nil
}

func mappedPoolRecords(data []byte, source protocolPoolSourceConfig) ([]json.RawMessage, error) {
	if source.Format == "json" {
		payload := data
		if source.RecordsPath != "" {
			result := gjson.GetBytes(data, source.RecordsPath)
			if !result.Exists() {
				return nil, errors.New("external pool records_path does not exist")
			}
			payload = []byte(result.Raw)
		}
		var records []json.RawMessage
		if err := json.Unmarshal(payload, &records); err != nil {
			return nil, errors.New("external JSON pool must resolve to an array")
		}
		return records, nil
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	records := make([]json.RawMessage, 0)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if !json.Valid([]byte(line)) {
			return nil, errors.New("external JSONL pool contains invalid JSON")
		}
		records = append(records, json.RawMessage(line))
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func protocolPoolSourceHasQuota(source protocolPoolSourceConfig) bool {
	return source.BalancePath != "" || source.QuotaTotalPath != "" || source.QuotaRemainingPath != "" ||
		source.QuotaUsedPath != "" || source.QuotaUnitPath != "" || source.QuotaStatusPath != "" || source.QuotaCheckedAtPath != ""
}

func mappedPoolNumber(record []byte, path string) *float64 {
	if path == "" {
		return nil
	}
	result := gjson.GetBytes(record, path)
	if !result.Exists() || result.Type == gjson.Null {
		return nil
	}
	value := result.Float()
	if result.Type == gjson.String {
		parsed, err := strconv.ParseFloat(strings.TrimSpace(result.String()), 64)
		if err != nil {
			return nil
		}
		value = parsed
	}
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return nil
	}
	copy := value
	return &copy
}

func normalizeProtocolQuota(input *protocolCredentialQuota, now time.Time, staleAfterSeconds int) *protocolCredentialQuota {
	if input == nil {
		return nil
	}
	quota := *input
	quota.Unit = strings.TrimSpace(quota.Unit)
	quota.Status = strings.ToLower(strings.TrimSpace(quota.Status))
	quota.CheckedAt = strings.TrimSpace(quota.CheckedAt)
	inconsistent := quota.Total != nil && quota.Remaining != nil && quota.Used != nil &&
		math.Abs((*quota.Remaining+*quota.Used)-*quota.Total) > math.Max(1e-9, *quota.Total*1e-9)
	for _, value := range []*float64{quota.Total, quota.Remaining, quota.Used} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0) {
			return &protocolCredentialQuota{Status: "unknown", Unit: quota.Unit, CheckedAt: quota.CheckedAt}
		}
	}
	if quota.Total != nil && quota.Remaining == nil && quota.Used != nil {
		remaining := math.Max(0, *quota.Total-*quota.Used)
		quota.Remaining = &remaining
	}
	if quota.Total != nil && quota.Used == nil && quota.Remaining != nil {
		used := math.Max(0, *quota.Total-*quota.Remaining)
		quota.Used = &used
	}
	if quota.Total != nil {
		if quota.Remaining != nil && *quota.Remaining > *quota.Total {
			remaining := *quota.Total
			quota.Remaining = &remaining
		}
		if quota.Used != nil && *quota.Used > *quota.Total {
			used := *quota.Total
			quota.Used = &used
		}
	}
	tracked := quota.Total != nil || quota.Remaining != nil || quota.Used != nil
	if !tracked {
		quota.Status = "unknown"
	} else if inconsistent {
		quota.Status = "estimated"
	} else if quota.Status != "estimated" && quota.Status != "stale" && quota.Status != "unknown" {
		quota.Status = "confirmed"
	}
	if staleAfterSeconds > 0 && quota.CheckedAt != "" {
		checkedAt, err := time.Parse(time.RFC3339, quota.CheckedAt)
		if err != nil || now.Sub(checkedAt) > time.Duration(staleAfterSeconds)*time.Second {
			quota.Status = "stale"
		}
	}
	return &quota
}

func protocolQuotaAccount(quota *protocolCredentialQuota, now time.Time, staleAfterSeconds int) protocolQuotaAccountView {
	normalized := normalizeProtocolQuota(quota, now, staleAfterSeconds)
	view := protocolQuotaAccountView{Status: "unknown"}
	if normalized == nil {
		return view
	}
	view.Total, view.Remaining, view.Used = normalized.Total, normalized.Remaining, normalized.Used
	view.Unit, view.Status, view.CheckedAt = normalized.Unit, normalized.Status, normalized.CheckedAt
	view.Tracked = normalized.Total != nil || normalized.Remaining != nil || normalized.Used != nil
	view.Stale = normalized.Status == "stale"
	if normalized.Total != nil && *normalized.Total > 0 && normalized.Used != nil {
		percent := math.Max(0, math.Min(100, (*normalized.Used / *normalized.Total)*100))
		view.UsedPercent = &percent
	}
	return view
}

func aggregateProtocolQuota(accounts []protocolQuotaAccountView, configured bool) protocolQuotaRuntimeView {
	view := protocolQuotaRuntimeView{Status: "untracked"}
	if configured {
		view.Status = "unknown"
	}
	units := map[string]bool{}
	allRemaining, allComplete, allCompleteUsable := true, true, true
	var total, remaining, used float64
	var totalCorrection, remainingCorrection, usedCorrection float64
	for _, account := range accounts {
		if !account.Tracked {
			view.UnknownAccounts++
			continue
		}
		view.TrackedAccounts++
		units[account.Unit] = true
		if account.Status == "confirmed" {
			view.ConfirmedAccounts++
		}
		if account.Status != "confirmed" && account.Status != "estimated" {
			allCompleteUsable = false
		}
		if account.Stale {
			view.StaleAccounts++
		}
		if account.Remaining == nil {
			allRemaining = false
		} else {
			compensatedQuotaAdd(&remaining, &remainingCorrection, *account.Remaining)
		}
		if account.Total == nil || account.Used == nil || account.Remaining == nil {
			allComplete = false
		} else {
			compensatedQuotaAdd(&total, &totalCorrection, *account.Total)
			compensatedQuotaAdd(&used, &usedCorrection, *account.Used)
		}
	}
	if view.TrackedAccounts == 0 {
		return view
	}
	if len(units) > 1 {
		view.Status = "mixed-unit"
		return view
	}
	for unit := range units {
		view.Unit = unit
	}
	if allRemaining && view.TrackedAccounts == len(accounts) {
		view.Remaining = &remaining
	}
	if allComplete && view.TrackedAccounts == len(accounts) {
		view.Total, view.Used = &total, &used
		if total > 0 {
			percent := math.Max(0, math.Min(100, used/total*100))
			view.UsedPercent = &percent
		}
	}
	if view.StaleAccounts > 0 {
		view.Status = "stale"
	} else if allComplete && allCompleteUsable && view.TrackedAccounts == len(accounts) {
		if view.ConfirmedAccounts == len(accounts) {
			view.Status = "confirmed"
		} else {
			view.Status = "estimated"
		}
	} else if allRemaining && view.TrackedAccounts == len(accounts) {
		view.Status = "remaining-only"
	} else {
		view.Status = "partial"
	}
	return view
}

type protocolQuotaPricingRate struct {
	amountPerUnit float64
	currency      string
	pricingID     string
	status        string
}

func protocolQuotaReferenceValue(total, remaining, used *float64, quotaUnit, quotaStatus string, pricing *protocolPricing) *protocolQuotaReferenceValueView {
	rate, status := protocolQuotaRate(quotaUnit, pricing)
	if status == "unavailable" {
		return nil
	}
	if status == "ambiguous" {
		return &protocolQuotaReferenceValueView{Status: status}
	}
	view := &protocolQuotaReferenceValueView{
		Status:    rate.status,
		Currency:  rate.currency,
		PricingID: rate.pricingID,
	}
	if quotaStatus == "estimated" || quotaStatus == "stale" || quotaStatus == "partial" || quotaStatus == "remaining-only" {
		view.Status = quotaStatus
	}
	view.Total = scaledProtocolQuotaValue(total, rate.amountPerUnit)
	view.Remaining = scaledProtocolQuotaValue(remaining, rate.amountPerUnit)
	view.Used = scaledProtocolQuotaValue(used, rate.amountPerUnit)
	return view
}

func protocolQuotaRate(quotaUnit string, pricing *protocolPricing) (protocolQuotaPricingRate, string) {
	canonicalQuotaUnit := canonicalProtocolQuotaUnit(quotaUnit)
	if canonicalQuotaUnit == "" || pricing == nil {
		return protocolQuotaPricingRate{}, "unavailable"
	}
	var selected *protocolQuotaPricingRate
	for _, entry := range pricing.Entries {
		if entry.Amount == nil || (entry.Status != "confirmed" && entry.Status != "estimated") {
			continue
		}
		currency, ok := protocolMonetaryCurrency(entry.Currency)
		if !ok {
			continue
		}
		quantity, unit, ok := parseProtocolPricingUnit(entry.Unit)
		if !ok || canonicalProtocolQuotaUnit(unit) != canonicalQuotaUnit {
			continue
		}
		amountPerUnit := *entry.Amount / quantity
		if math.IsNaN(amountPerUnit) || math.IsInf(amountPerUnit, 0) || amountPerUnit < 0 {
			continue
		}
		candidate := protocolQuotaPricingRate{
			amountPerUnit: amountPerUnit,
			currency:      currency,
			pricingID:     entry.ID,
			status:        entry.Status,
		}
		if selected == nil {
			selected = &candidate
			continue
		}
		if selected.currency != candidate.currency || !sameProtocolPricingRate(selected.amountPerUnit, candidate.amountPerUnit) {
			return protocolQuotaPricingRate{}, "ambiguous"
		}
		if selected.status == "estimated" && candidate.status == "confirmed" {
			selected = &candidate
		}
	}
	if selected == nil {
		return protocolQuotaPricingRate{}, "unavailable"
	}
	return *selected, selected.status
}

func parseProtocolPricingUnit(input string) (float64, string, bool) {
	remainder := strings.TrimSpace(input)
	if !strings.HasPrefix(strings.ToLower(remainder), "per-") {
		return 0, "", false
	}
	remainder = remainder[4:]
	if remainder == "" {
		return 0, "", false
	}
	quantity := 1.0
	unit := remainder
	if separator := strings.IndexByte(remainder, '-'); separator > 0 {
		if parsed, ok := parseProtocolPricingQuantity(remainder[:separator]); ok {
			quantity = parsed
			unit = remainder[separator+1:]
		}
	}
	if quantity <= 0 || strings.TrimSpace(unit) == "" {
		return 0, "", false
	}
	return quantity, unit, true
}

func parseProtocolPricingQuantity(input string) (float64, bool) {
	normalized := strings.ToLower(strings.TrimSpace(input))
	multiplier := 1.0
	if strings.HasSuffix(normalized, "k") {
		multiplier = 1_000
		normalized = strings.TrimSuffix(normalized, "k")
	} else if strings.HasSuffix(normalized, "m") {
		multiplier = 1_000_000
		normalized = strings.TrimSuffix(normalized, "m")
	}
	value, err := strconv.ParseFloat(normalized, 64)
	if err != nil || value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return value * multiplier, true
}

func canonicalProtocolQuotaUnit(input string) string {
	normalized := strings.ToLower(strings.Trim(strings.TrimSpace(input), "-_ "))
	normalized = strings.NewReplacer("_", "-", " ", "-").Replace(normalized)
	for strings.Contains(normalized, "--") {
		normalized = strings.ReplaceAll(normalized, "--", "-")
	}
	parts := strings.Split(normalized, "-")
	for index, part := range parts {
		if len(part) > 3 && strings.HasSuffix(part, "s") {
			parts[index] = strings.TrimSuffix(part, "s")
		}
	}
	return strings.Join(parts, "-")
}

func protocolMonetaryCurrency(input string) (string, bool) {
	currency := strings.TrimSpace(input)
	if len(currency) != 3 || currency != strings.ToUpper(currency) {
		return "", false
	}
	for _, character := range currency {
		if character < 'A' || character > 'Z' {
			return "", false
		}
	}
	return currency, true
}

func sameProtocolPricingRate(left, right float64) bool {
	return math.Abs(left-right) <= math.Max(1e-12, math.Max(math.Abs(left), math.Abs(right))*1e-9)
}

func scaledProtocolQuotaValue(value *float64, rate float64) *float64 {
	if value == nil {
		return nil
	}
	scaled := *value * rate
	if math.IsNaN(scaled) || math.IsInf(scaled, 0) {
		return nil
	}
	return &scaled
}

func compensatedQuotaAdd(sum, correction *float64, value float64) {
	y := value - *correction
	t := *sum + y
	*correction = (t - *sum) - y
	*sum = t
}

func decodeMappedPoolSecret(record []byte, raw string, source protocolPoolSourceConfig) (string, string, error) {
	if source.SecretCodec == "plain" || source.SecretCodec == "" {
		return strings.TrimSpace(raw), "", nil
	}
	if source.SecretCodec == "json-fields" {
		document := make(map[string]string, len(source.SecretFields))
		for name, path := range source.SecretFields {
			value := strings.TrimSpace(gjson.GetBytes(record, path).String())
			if value == "" {
				return "", "", fmt.Errorf("json-fields value %s is missing", name)
			}
			document[name] = value
		}
		encoded, err := json.Marshal(document)
		if err != nil {
			return "", "", err
		}
		return string(encoded), "", nil
	}
	var cookies []struct {
		Name    string  `json:"name"`
		Value   string  `json:"value"`
		Expires float64 `json:"expires"`
	}
	if err := json.Unmarshal([]byte(raw), &cookies); err != nil {
		return "", "", err
	}
	for _, cookie := range cookies {
		if cookie.Name == source.SecretSelector && cookie.Value != "" {
			expires := ""
			if cookie.Expires > 0 {
				expires = time.Unix(int64(cookie.Expires), 0).UTC().Format(time.RFC3339)
			}
			return cookie.Value, expires, nil
		}
	}
	return "", "", errors.New("selected cookie is missing")
}

func mappedPoolExpiry(result gjson.Result) string {
	if !result.Exists() {
		return ""
	}
	if result.Type == gjson.Number {
		return time.Unix(result.Int(), 0).UTC().Format(time.RFC3339)
	}
	value := strings.TrimSpace(result.String())
	if _, err := time.Parse(time.RFC3339, value); err == nil {
		return value
	}
	return ""
}

func (registry *protocolRegistry) poolStatePath(protocolID string) string {
	return filepath.Join(registry.stateDir, "protocol-state", protocolID+".json")
}

func (registry *protocolRegistry) loadPoolState(protocolID string) (protocolPoolState, error) {
	state := protocolPoolState{
		Credentials: make(map[string]*protocolCredentialRuntime),
		Affinity:    make(map[string]protocolAffinityRuntime),
	}
	path := registry.poolStatePath(protocolID)
	exists, err := inspectPrivateRegularFile(path, "protocol pool state file")
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
	if state.Credentials == nil {
		state.Credentials = make(map[string]*protocolCredentialRuntime)
	}
	if state.Affinity == nil {
		state.Affinity = make(map[string]protocolAffinityRuntime)
	}
	return state, nil
}

func (registry *protocolRegistry) savePoolState(protocolID string, state protocolPoolState) error {
	dir := filepath.Dir(registry.poolStatePath(protocolID))
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
	return os.Rename(temporaryPath, registry.poolStatePath(protocolID))
}

func (registry *protocolRegistry) acquireCredential(definition protocolDefinition, route protocolRoute, affinityKey string, exclude map[string]bool) (acquiredProtocolCredential, error) {
	if definition.Pool == nil || definition.Pool.Mode == "external" {
		secret, err := registry.readSecret(definition.Auth)
		if err != nil {
			return acquiredProtocolCredential{}, err
		}
		return acquiredProtocolCredential{Credential: protocolCredential{ID: "static", Secret: secret}}, nil
	}
	registry.poolMu.Lock()
	defer registry.poolMu.Unlock()
	stateLock, err := registry.lockProtocolState("pool", definition.ID)
	if err != nil {
		return acquiredProtocolCredential{}, err
	}
	defer unlockProtocolState(stateLock)
	credentials, err := registry.loadPoolCredentials(definition)
	if err != nil {
		return acquiredProtocolCredential{}, err
	}
	state, err := registry.loadPoolState(definition.ID)
	if err != nil {
		return acquiredProtocolCredential{}, err
	}
	now := time.Now()
	credentialByID := make(map[string]protocolCredential, len(credentials))
	for _, credential := range credentials {
		credentialByID[credential.ID] = credential
		if state.Credentials[credential.ID] == nil {
			state.Credentials[credential.ID] = &protocolCredentialRuntime{Leases: make(map[string]int64)}
		}
		pruneProtocolCredentialLeases(state.Credentials[credential.ID], now)
	}
	for key, affinity := range state.Affinity {
		if affinity.ExpiresAt <= now.Unix() {
			delete(state.Affinity, key)
		}
	}
	if affinityKey != "" {
		if affinity, ok := state.Affinity[affinityKey]; ok && !exclude[affinity.CredentialID] {
			if credential, exists := credentialByID[affinity.CredentialID]; exists && protocolCredentialEligible(credential, state.Credentials[credential.ID], definition.Pool, now) && protocolCredentialMatchesRoute(definition, route, credential) {
				runtime := state.Credentials[credential.ID]
				leaseID, leaseErr := allocateProtocolLease(runtime, definition.Pool, now)
				if leaseErr != nil {
					return acquiredProtocolCredential{}, leaseErr
				}
				runtime.LastUsed = now.UTC().Format(time.RFC3339Nano)
				if err := registry.savePoolState(definition.ID, state); err != nil {
					return acquiredProtocolCredential{}, err
				}
				return acquiredProtocolCredential{Credential: credential, Pooled: true, LeaseID: leaseID}, nil
			}
		}
	}
	candidates := make([]protocolCredential, 0, len(credentials))
	bestPriority := 0
	prioritySet := false
	for _, credential := range credentials {
		if exclude[credential.ID] || !protocolCredentialEligible(credential, state.Credentials[credential.ID], definition.Pool, now) || !protocolCredentialMatchesRoute(definition, route, credential) {
			continue
		}
		if !prioritySet || credential.Priority < bestPriority {
			bestPriority = credential.Priority
			prioritySet = true
			candidates = candidates[:0]
		}
		if credential.Priority == bestPriority {
			candidates = append(candidates, credential)
		}
	}
	if len(candidates) == 0 {
		return acquiredProtocolCredential{}, errors.New("no eligible pool credential")
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
	selected := selectProtocolCredential(candidates, &state, definition.Pool)
	runtime := state.Credentials[selected.ID]
	leaseID, err := allocateProtocolLease(runtime, definition.Pool, now)
	if err != nil {
		return acquiredProtocolCredential{}, err
	}
	runtime.LastUsed = now.UTC().Format(time.RFC3339Nano)
	if err := registry.savePoolState(definition.ID, state); err != nil {
		return acquiredProtocolCredential{}, err
	}
	return acquiredProtocolCredential{Credential: selected, Pooled: true, LeaseID: leaseID}, nil
}

func pruneProtocolCredentialLeases(runtime *protocolCredentialRuntime, now time.Time) {
	if runtime.Leases == nil {
		runtime.Leases = make(map[string]int64)
		if runtime.InFlight > 0 {
			runtime.InFlight = 0
		}
	}
	for leaseID, expiresAt := range runtime.Leases {
		if expiresAt <= now.Unix() {
			delete(runtime.Leases, leaseID)
		}
	}
	runtime.InFlight = len(runtime.Leases)
}

func allocateProtocolLease(runtime *protocolCredentialRuntime, pool *protocolPoolConfig, now time.Time) (string, error) {
	pruneProtocolCredentialLeases(runtime, now)
	leaseID, err := randomSecret(12, "lease-")
	if err != nil {
		return "", err
	}
	runtime.Leases[leaseID] = now.Add(time.Duration(pool.InFlightLeaseSeconds) * time.Second).Unix()
	runtime.InFlight = len(runtime.Leases)
	return leaseID, nil
}

func protocolCredentialMatchesSelector(credential protocolCredential, selector string) bool {
	if selector == "" {
		return true
	}
	value, err := evalProtocolExpression(selector, map[string]any{
		"id": credential.ID, "metadata": credential.Metadata, "balance": credential.Balance,
		"priority": credential.Priority, "weight": credential.Weight,
	})
	if err != nil {
		return false
	}
	matched, ok := value.(bool)
	return ok && matched
}

func protocolCredentialMatchesRoute(definition protocolDefinition, route protocolRoute, credential protocolCredential) bool {
	if !protocolCredentialMatchesSelector(credential, route.PoolSelector) {
		return false
	}
	if route.TargetSelector == nil {
		return true
	}
	_, _, ok := protocolTargetForCredential(definition, route, credential)
	return ok
}

func protocolCredentialEligible(credential protocolCredential, runtime *protocolCredentialRuntime, pool *protocolPoolConfig, now time.Time) bool {
	if credential.Disabled || runtime == nil || runtime.DisabledReason != "" || runtime.CooldownUntil > now.Unix() || credential.Balance < pool.MinBalance {
		return false
	}
	if pool.MaxInFlightPerCredential > 0 && runtime.InFlight >= pool.MaxInFlightPerCredential {
		return false
	}
	if credential.ExpiresAt != "" {
		expires, _ := time.Parse(time.RFC3339, credential.ExpiresAt)
		if !expires.After(now) {
			return false
		}
	}
	return true
}

func selectProtocolCredential(candidates []protocolCredential, state *protocolPoolState, pool *protocolPoolConfig) protocolCredential {
	switch pool.Strategy {
	case "lru":
		return *minCredential(candidates, func(candidate protocolCredential) string { return state.Credentials[candidate.ID].LastUsed })
	case "least-inflight":
		sort.SliceStable(candidates, func(i, j int) bool {
			left, right := state.Credentials[candidates[i].ID], state.Credentials[candidates[j].ID]
			if left.InFlight == right.InFlight {
				return left.LastUsed < right.LastUsed
			}
			return left.InFlight < right.InFlight
		})
		return candidates[0]
	case "balance-aware":
		sort.SliceStable(candidates, func(i, j int) bool {
			if candidates[i].Balance == candidates[j].Balance {
				return state.Credentials[candidates[i].ID].LastUsed < state.Credentials[candidates[j].ID].LastUsed
			}
			return candidates[i].Balance > candidates[j].Balance
		})
		return candidates[0]
	case "smooth-weighted":
		total := 0
		selected := candidates[0]
		for _, candidate := range candidates {
			runtime := state.Credentials[candidate.ID]
			runtime.CurrentWeight += candidate.Weight
			total += candidate.Weight
			if runtime.CurrentWeight > state.Credentials[selected.ID].CurrentWeight {
				selected = candidate
			}
		}
		state.Credentials[selected.ID].CurrentWeight -= total
		return selected
	default:
		selected := candidates[state.Cursor%len(candidates)]
		state.Cursor = (state.Cursor + 1) % len(candidates)
		return selected
	}
}

func minCredential(candidates []protocolCredential, value func(protocolCredential) string) *protocolCredential {
	selected := &candidates[0]
	for index := 1; index < len(candidates); index++ {
		if value(candidates[index]) < value(*selected) {
			selected = &candidates[index]
		}
	}
	return selected
}

func (registry *protocolRegistry) releaseCredential(definition protocolDefinition, acquired acquiredProtocolCredential, status int, retryAfter string) error {
	if !acquired.Pooled {
		return nil
	}
	registry.poolMu.Lock()
	defer registry.poolMu.Unlock()
	stateLock, err := registry.lockProtocolState("pool", definition.ID)
	if err != nil {
		return err
	}
	defer unlockProtocolState(stateLock)
	state, err := registry.loadPoolState(definition.ID)
	if err != nil {
		return err
	}
	runtime := state.Credentials[acquired.Credential.ID]
	if runtime == nil {
		runtime = &protocolCredentialRuntime{}
		state.Credentials[acquired.Credential.ID] = runtime
	}
	if acquired.LeaseID != "" && runtime.Leases != nil {
		delete(runtime.Leases, acquired.LeaseID)
	}
	pruneProtocolCredentialLeases(runtime, time.Now())
	now := time.Now()
	switch {
	case status >= 200 && status < 400:
		runtime.ConsecutiveFailures = 0
		runtime.CooldownUntil = 0
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		runtime.ConsecutiveFailures++
		if definition.Pool.UnauthorizedAction == "disable" {
			runtime.DisabledReason = fmt.Sprintf("upstream-%d", status)
		} else {
			runtime.CooldownUntil = now.Add(time.Duration(definition.Pool.UnauthorizedCooldownSeconds) * time.Second).Unix()
		}
	case status == http.StatusPaymentRequired:
		runtime.ConsecutiveFailures++
		runtime.CooldownUntil = now.Add(time.Duration(definition.Pool.UnauthorizedCooldownSeconds) * time.Second).Unix()
	case status == http.StatusTooManyRequests:
		runtime.ConsecutiveFailures++
		duration := time.Duration(definition.Pool.RateLimitCooldownSeconds) * time.Second
		if seconds, err := time.ParseDuration(strings.TrimSpace(retryAfter) + "s"); err == nil && seconds > 0 && seconds <= 7*24*time.Hour {
			duration = seconds
		}
		runtime.CooldownUntil = now.Add(duration).Unix()
	case status >= 500:
		runtime.ConsecutiveFailures++
		runtime.CooldownUntil = now.Add(time.Duration(definition.Pool.TransientCooldownSeconds) * time.Second).Unix()
	}
	return registry.savePoolState(definition.ID, state)
}

func (registry *protocolRegistry) bindAffinity(definition protocolDefinition, key, credentialID string, ttlSeconds int) error {
	if definition.Pool == nil || definition.Pool.Mode != "local" || key == "" || credentialID == "" {
		return nil
	}
	registry.poolMu.Lock()
	defer registry.poolMu.Unlock()
	stateLock, err := registry.lockProtocolState("pool", definition.ID)
	if err != nil {
		return err
	}
	defer unlockProtocolState(stateLock)
	state, err := registry.loadPoolState(definition.ID)
	if err != nil {
		return err
	}
	state.Affinity[key] = protocolAffinityRuntime{CredentialID: credentialID, ExpiresAt: time.Now().Add(time.Duration(ttlSeconds) * time.Second).Unix()}
	return registry.savePoolState(definition.ID, state)
}

func protocolShouldRetry(status int) bool {
	return status == http.StatusUnauthorized || status == http.StatusPaymentRequired || status == http.StatusForbidden || status == http.StatusTooManyRequests || status >= 500
}

func (registry *protocolRegistry) poolView(definition protocolDefinition) protocolPoolView {
	if definition.Pool == nil {
		return protocolPoolView{Mode: "static"}
	}
	view := protocolPoolView{Mode: definition.Pool.Mode, Strategy: definition.Pool.Strategy}
	if definition.Pool.Source != nil {
		view.Source = "external-readonly"
	}
	if definition.Pool.Mode != "local" {
		return view
	}
	registry.poolMu.Lock()
	defer registry.poolMu.Unlock()
	stateLock, lockErr := registry.lockProtocolState("pool", definition.ID)
	if lockErr != nil {
		return view
	}
	defer unlockProtocolState(stateLock)
	credentials, err := registry.loadPoolCredentials(definition)
	if err != nil {
		return view
	}
	state, _ := registry.loadPoolState(definition.ID)
	now := time.Now()
	view.Total = len(credentials)
	for _, credential := range credentials {
		runtime := state.Credentials[credential.ID]
		if runtime == nil {
			runtime = &protocolCredentialRuntime{}
		}
		pruneProtocolCredentialLeases(runtime, now)
		view.InFlight += runtime.InFlight
		if protocolCredentialEligible(credential, runtime, definition.Pool, now) && protocolCredentialRoutableForAnyRoute(definition, credential) {
			view.Eligible++
		}
	}
	for _, affinity := range state.Affinity {
		if affinity.ExpiresAt > now.Unix() {
			view.Affinities++
		}
	}
	return view
}

func (registry *protocolRegistry) poolRuntimeView(definition protocolDefinition) protocolPoolRuntimeView {
	view := protocolPoolRuntimeView{Ownership: "static", Status: "static", Quota: protocolQuotaRuntimeView{Status: "untracked"}, Accounts: []protocolPoolAccountView{}}
	if definition.Pool == nil {
		return view
	}
	if definition.Pool.Mode == "external" {
		view.Ownership = "upstream"
		view.Status = "delegated"
		return view
	}
	view.Ownership = "local"
	view.Status = "ready"

	registry.poolMu.Lock()
	defer registry.poolMu.Unlock()
	stateLock, err := registry.lockProtocolState("pool", definition.ID)
	if err != nil {
		view.Status = "unavailable"
		return view
	}
	defer unlockProtocolState(stateLock)
	credentials, err := registry.loadPoolCredentials(definition)
	if err != nil {
		view.Status = "unavailable"
		return view
	}
	state, err := registry.loadPoolState(definition.ID)
	if err != nil {
		view.Status = "unavailable"
		return view
	}
	sort.Slice(credentials, func(i, j int) bool { return credentials[i].ID < credentials[j].ID })
	now := time.Now()
	quotaConfigured := (definition.Pool.Source != nil && protocolPoolSourceHasQuota(*definition.Pool.Source)) ||
		definition.Pool.MinBalance > 0 || definition.Pool.Strategy == "balance-aware"
	view.Total = len(credentials)
	quotaAccounts := make([]protocolQuotaAccountView, 0, len(credentials))
	for index, credential := range credentials {
		runtime := state.Credentials[credential.ID]
		if runtime == nil {
			runtime = &protocolCredentialRuntime{}
		}
		pruneProtocolCredentialLeases(runtime, now)
		status, label := protocolCredentialDisplayStatus(credential, runtime, definition.Pool, now)
		if status == "ready" && !protocolCredentialRoutableForAnyRoute(definition, credential) {
			status, label = "unroutable", "未映射接口"
		}
		account := protocolPoolAccountView{
			Ref: protocolCredentialRef(credential.ID), Label: protocolCredentialDisplayLabel(index, credential.ID), Status: status, StatusLabel: label,
			InFlight: runtime.InFlight, ConsecutiveFailures: runtime.ConsecutiveFailures,
			LastUsed: runtime.LastUsed, Targets: protocolCredentialTargetNames(definition, credential),
		}
		account.Quota = protocolQuotaAccount(credential.Quota, now, definition.Pool.QuotaStaleAfterSeconds)
		account.Quota.ReferenceValue = protocolQuotaReferenceValue(
			account.Quota.Total, account.Quota.Remaining, account.Quota.Used,
			account.Quota.Unit, account.Quota.Status, definition.Pricing,
		)
		quotaAccounts = append(quotaAccounts, account.Quota)
		if account.Quota.Remaining != nil {
			balance := *account.Quota.Remaining
			account.Balance = &balance
			view.BalanceRemaining += balance
			if balance <= 0 {
				view.BalanceEmpty++
			}
		}
		if runtime.CooldownUntil > now.Unix() {
			account.CooldownUntil = time.Unix(runtime.CooldownUntil, 0).UTC().Format(time.RFC3339)
		}
		view.InFlight += runtime.InFlight
		switch status {
		case "ready":
			view.Ready++
		case "cooldown":
			view.Cooling++
		case "disabled":
			view.Disabled++
		case "expired":
			view.Expired++
		case "busy":
			view.Busy++
		case "balance-low":
			view.BalanceLow++
		case "unroutable":
			view.Unroutable++
		}
		view.Accounts = append(view.Accounts, account)
	}
	view.Quota = aggregateProtocolQuota(quotaAccounts, quotaConfigured)
	view.Quota.ReferenceValue = protocolQuotaReferenceValue(
		view.Quota.Total, view.Quota.Remaining, view.Quota.Used,
		view.Quota.Unit, view.Quota.Status, definition.Pricing,
	)
	view.BalanceTracked = view.Quota.TrackedAccounts > 0
	if view.Ready == 0 {
		view.Status = "degraded"
	}
	return view
}

func protocolCredentialTargetNames(definition protocolDefinition, credential protocolCredential) []string {
	seen := make(map[string]bool)
	targets := make([]string, 0)
	for _, route := range definition.Routes {
		if route.TargetSelector == nil || !protocolCredentialMatchesSelector(credential, route.PoolSelector) {
			continue
		}
		_, name, ok := protocolTargetForCredential(definition, route, credential)
		if ok && name != "" && !seen[name] {
			seen[name] = true
			targets = append(targets, name)
		}
	}
	sort.Strings(targets)
	return targets
}

func protocolCredentialRoutableForAnyRoute(definition protocolDefinition, credential protocolCredential) bool {
	if len(definition.Routes) == 0 {
		return true
	}
	for _, route := range definition.Routes {
		if protocolCredentialMatchesRoute(definition, route, credential) {
			return true
		}
	}
	return false
}

func protocolCredentialDisplayLabel(index int, credentialID string) string {
	digest := sha256.Sum256([]byte(credentialID))
	return fmt.Sprintf("账号 %02d · %s", index+1, hex.EncodeToString(digest[:3]))
}

func protocolCredentialRef(credentialID string) string {
	digest := sha256.Sum256([]byte(credentialID))
	return hex.EncodeToString(digest[:])
}

func protocolCredentialDisplayStatus(credential protocolCredential, runtime *protocolCredentialRuntime, pool *protocolPoolConfig, now time.Time) (string, string) {
	if credential.Disabled {
		return "disabled", "配置停用"
	}
	if runtime.DisabledReason != "" {
		return "disabled", "调度停用"
	}
	if credential.ExpiresAt != "" {
		expires, err := time.Parse(time.RFC3339, credential.ExpiresAt)
		if err == nil && !expires.After(now) {
			return "expired", "已过期"
		}
	}
	if runtime.CooldownUntil > now.Unix() {
		return "cooldown", "冷却中"
	}
	if credential.Balance < pool.MinBalance {
		return "balance-low", "余额不足"
	}
	if pool.MaxInFlightPerCredential > 0 && runtime.InFlight >= pool.MaxInFlightPerCredential {
		return "busy", "占用中"
	}
	return "ready", "可调度"
}

type protocolPoolResetRequest struct {
	CredentialID  string `json:"credential_id,omitempty"`
	CredentialRef string `json:"credential_ref,omitempty"`
}

func (registry *protocolRegistry) handleAdminPoolReset(c *gin.Context) {
	definition, ok := registry.get(c.Param("id"))
	if !ok || definition.Pool == nil || definition.Pool.Mode != "local" {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "local protocol pool not found"})
		return
	}
	var request protocolPoolResetRequest
	decoder := json.NewDecoder(io.LimitReader(c.Request.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid reset request"})
		return
	}
	registry.poolMu.Lock()
	stateLock, lockErr := registry.lockProtocolState("pool", definition.ID)
	if lockErr != nil {
		registry.poolMu.Unlock()
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot lock pool state"})
		return
	}
	state, err := registry.loadPoolState(definition.ID)
	if err != nil {
		unlockProtocolState(stateLock)
		registry.poolMu.Unlock()
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot read pool state"})
		return
	}
	reset := 0
	for credentialID, runtime := range state.Credentials {
		if request.CredentialID != "" && request.CredentialID != credentialID {
			continue
		}
		if request.CredentialRef != "" && request.CredentialRef != protocolCredentialRef(credentialID) {
			continue
		}
		runtime.CooldownUntil = 0
		runtime.ConsecutiveFailures = 0
		runtime.DisabledReason = ""
		runtime.InFlight = 0
		runtime.Leases = make(map[string]int64)
		reset++
	}
	if (request.CredentialID != "" || request.CredentialRef != "") && reset == 0 {
		unlockProtocolState(stateLock)
		registry.poolMu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "credential state not found"})
		return
	}
	if err := registry.savePoolState(definition.ID, state); err != nil {
		unlockProtocolState(stateLock)
		registry.poolMu.Unlock()
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "cannot persist pool state"})
		return
	}
	unlockProtocolState(stateLock)
	registry.poolMu.Unlock()
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"reset": reset, "pool": registry.poolView(definition)}})
}
