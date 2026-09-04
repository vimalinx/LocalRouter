package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	localRootRole       = 100
	localStatusEnabled  = 1
	localLogTypeConsume = 2
	localLogTypeError   = 5
	localQuotaPerUnit   = 500_000.0
)

type localUser struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Role     int    `json:"role"`
}

type localToken struct {
	ID             int    `json:"id"`
	UserID         int    `json:"user_id"`
	Key            string `json:"key,omitempty"`
	Status         int    `json:"status"`
	Name           string `json:"name"`
	AgentCode      string `json:"agent_code"`
	AgentName      string `json:"agent_name"`
	Workspace      string `json:"workspace"`
	Runtime        string `json:"runtime"`
	CreatedTime    int64  `json:"created_time"`
	AccessedTime   int64  `json:"accessed_time"`
	ExpiredTime    int64  `json:"expired_time"`
	UnlimitedQuota bool   `json:"unlimited_quota"`
	Group          string `json:"group"`
}

type localChannel struct {
	ID              int                     `json:"id"`
	Type            int                     `json:"type"`
	Key             string                  `json:"-"`
	Status          int                     `json:"status"`
	Name            string                  `json:"name"`
	Weight          int                     `json:"weight"`
	CreatedTime     int64                   `json:"created_time"`
	TestTime        int64                   `json:"test_time,omitempty"`
	ResponseTime    int                     `json:"response_time,omitempty"`
	BaseURL         string                  `json:"base_url,omitempty"`
	Models          string                  `json:"models"`
	Group           string                  `json:"group"`
	Priority        int                     `json:"priority"`
	AutoBan         int                     `json:"auto_ban"`
	Balance         float64                 `json:"balance,omitempty"`
	UpstreamProfile protocolUpstreamProfile `json:"upstream_profile,omitempty"`
}

type localRequestLog struct {
	ID                    int     `json:"id"`
	UserID                int     `json:"user_id"`
	CreatedAt             int64   `json:"created_at"`
	Type                  int     `json:"type"`
	Content               string  `json:"content"`
	Username              string  `json:"username"`
	TokenName             string  `json:"token_name"`
	ModelName             string  `json:"model_name"`
	Quota                 int64   `json:"quota"`
	PromptTokens          int64   `json:"prompt_tokens"`
	CompletionTokens      int64   `json:"completion_tokens"`
	CachedInputTokens     int64   `json:"cached_input_tokens"`
	CacheWriteInputTokens int64   `json:"cache_write_input_tokens"`
	ReasoningTokens       int64   `json:"reasoning_tokens"`
	TotalTokens           int64   `json:"total_tokens"`
	CostUSD               float64 `json:"cost_usd"`
	CostStatus            string  `json:"cost_status"`
	UseTime               int64   `json:"use_time"`
	IsStream              bool    `json:"is_stream"`
	ChannelID             int     `json:"channel_id"`
	ChannelName           string  `json:"channel_name"`
	TokenID               int     `json:"token_id"`
	Group                 string  `json:"group"`
	RequestID             string  `json:"request_id"`
}

type localStore struct {
	db *sql.DB
}

func openLocalStore(path string) (*localStore, error) {
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		exists, err := inspectPrivateRegularFile(candidate, "database file")
		if err != nil {
			return nil, err
		}
		if candidate == path && !exists {
			file, err := createPrivateFile(path, "database file")
			if err != nil {
				return nil, err
			}
			if err := file.Close(); err != nil {
				_ = os.Remove(path)
				return nil, fmt.Errorf("close database file %s: %w", path, err)
			}
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &localStore{db: db}
	if err := store.initialize(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (store *localStore) Close() error {
	if store == nil || store.db == nil {
		return nil
	}
	return store.db.Close()
}

func (store *localStore) initialize() error {
	statements := []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA busy_timeout=5000`,
		`PRAGMA foreign_keys=ON`,
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY,
			username TEXT UNIQUE,
			password TEXT NOT NULL DEFAULT 'local-only',
			display_name TEXT,
			role INTEGER DEFAULT 1,
			status INTEGER DEFAULT 1,
			quota INTEGER DEFAULT 0,
			used_quota INTEGER DEFAULT 0,
			request_count INTEGER DEFAULT 0,
			"group" TEXT DEFAULT 'default',
			created_at INTEGER,
			deleted_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS tokens (
			id INTEGER PRIMARY KEY,
			user_id INTEGER NOT NULL,
			key VARCHAR(128) UNIQUE NOT NULL,
			status INTEGER DEFAULT 1,
			name TEXT,
			created_time INTEGER,
			accessed_time INTEGER,
			expired_time INTEGER DEFAULT -1,
			remain_quota INTEGER DEFAULT 0,
			unlimited_quota NUMERIC DEFAULT 1,
			model_limits_enabled NUMERIC DEFAULT 0,
			model_limits TEXT,
			allow_ips TEXT DEFAULT '',
			used_quota INTEGER DEFAULT 0,
			"group" TEXT DEFAULT 'default',
			deleted_at DATETIME
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tokens_user_id ON tokens(user_id)`,
		`CREATE TABLE IF NOT EXISTS channels (
			id INTEGER PRIMARY KEY,
			type INTEGER DEFAULT 1,
			key TEXT NOT NULL,
			status INTEGER DEFAULT 1,
			name TEXT,
			weight INTEGER DEFAULT 100,
			created_time INTEGER,
			test_time INTEGER DEFAULT 0,
			response_time INTEGER DEFAULT 0,
			base_url TEXT DEFAULT '',
			balance REAL DEFAULT 0,
			models TEXT,
			"group" TEXT DEFAULT 'default',
			used_quota INTEGER DEFAULT 0,
			priority INTEGER DEFAULT 0,
			auto_ban INTEGER DEFAULT 1
		)`,
		`CREATE TABLE IF NOT EXISTS logs (
			id INTEGER PRIMARY KEY,
			user_id INTEGER,
			created_at INTEGER,
			type INTEGER,
			content TEXT,
			username TEXT DEFAULT '',
			token_name TEXT DEFAULT '',
			model_name TEXT DEFAULT '',
			quota INTEGER DEFAULT 0,
			prompt_tokens INTEGER DEFAULT 0,
			completion_tokens INTEGER DEFAULT 0,
			use_time INTEGER DEFAULT 0,
			is_stream NUMERIC,
			channel_id INTEGER,
			channel_name TEXT,
			token_id INTEGER DEFAULT 0,
			"group" TEXT,
			request_id TEXT DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_user_id ON logs(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_created_at ON logs(created_at)`,
	}
	for _, statement := range statements {
		if _, err := store.db.Exec(statement); err != nil {
			return fmt.Errorf("initialize local database: %w", err)
		}
	}
	if err := store.ensureColumn("channels", "upstream_profile", `TEXT NOT NULL DEFAULT '{}'`); err != nil {
		return err
	}
	for _, column := range []struct {
		name       string
		definition string
	}{
		{name: "cached_input_tokens", definition: `INTEGER NOT NULL DEFAULT 0`},
		{name: "cache_write_input_tokens", definition: `INTEGER NOT NULL DEFAULT 0`},
		{name: "reasoning_tokens", definition: `INTEGER NOT NULL DEFAULT 0`},
		{name: "total_tokens", definition: `INTEGER NOT NULL DEFAULT 0`},
		{name: "cost_usd", definition: `REAL NOT NULL DEFAULT 0`},
		{name: "cost_status", definition: `TEXT NOT NULL DEFAULT ''`},
	} {
		if err := store.ensureColumn("logs", column.name, column.definition); err != nil {
			return err
		}
	}
	for _, column := range []struct {
		name       string
		definition string
	}{
		{name: "agent_code", definition: `TEXT NOT NULL DEFAULT ''`},
		{name: "agent_name", definition: `TEXT NOT NULL DEFAULT ''`},
		{name: "workspace", definition: `TEXT NOT NULL DEFAULT ''`},
		{name: "runtime", definition: `TEXT NOT NULL DEFAULT ''`},
	} {
		if err := store.ensureColumn("tokens", column.name, column.definition); err != nil {
			return err
		}
	}
	if _, err := store.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_tokens_agent_code ON tokens(agent_code) WHERE agent_code <> '' AND deleted_at IS NULL`); err != nil {
		return fmt.Errorf("initialize Agent registry index: %w", err)
	}
	return nil
}

func (store *localStore) ensureColumn(table, column, definition string) error {
	rows, err := store.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return fmt.Errorf("inspect %s schema: %w", table, err)
	}
	found := false
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return err
		}
		found = found || name == column
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	if _, err := store.db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + definition); err != nil {
		return fmt.Errorf("add %s.%s: %w", table, column, err)
	}
	return nil
}

func (store *localStore) ensureRootUser() (localUser, error) {
	var user localUser
	err := store.db.QueryRow(`SELECT id, username, role FROM users WHERE role = ? AND deleted_at IS NULL ORDER BY id LIMIT 1`, localRootRole).
		Scan(&user.ID, &user.Username, &user.Role)
	if err == nil {
		return user, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return localUser{}, err
	}
	now := time.Now().Unix()
	result, err := store.db.Exec(`INSERT INTO users (username, password, display_name, role, status, quota, "group", created_at) VALUES (?, ?, ?, ?, ?, ?, 'default', ?)`,
		"local", "local-only-no-login", "Local operator", localRootRole, localStatusEnabled, int64(math.MaxInt64), now)
	if err != nil {
		return localUser{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return localUser{}, err
	}
	return localUser{ID: int(id), Username: "local", Role: localRootRole}, nil
}

func normalizeStoredToken(value string) string {
	return strings.TrimPrefix(strings.TrimSpace(value), "sk-")
}

func (store *localStore) ensureDefaultToken(userID int, visibleToken string) error {
	key := normalizeStoredToken(visibleToken)
	if key == "" {
		return errors.New("local API token is empty")
	}
	var id int
	err := store.db.QueryRow(`SELECT id FROM tokens WHERE user_id = ? AND name = ? AND deleted_at IS NULL`, userID, localTokenName).Scan(&id)
	now := time.Now().Unix()
	if errors.Is(err, sql.ErrNoRows) {
		_, err = store.db.Exec(`INSERT INTO tokens (user_id, key, status, name, agent_code, agent_name, workspace, runtime, created_time, accessed_time, expired_time, remain_quota, unlimited_quota, "group") VALUES (?, ?, ?, ?, 'localrouter-system', 'LocalRouter system', 'operator', 'bootstrap', ?, ?, -1, 0, 1, 'default')`,
			userID, key, localStatusEnabled, localTokenName, now, now)
		return err
	}
	if err != nil {
		return err
	}
	_, err = store.db.Exec(`UPDATE tokens SET key = ?, status = ?, agent_code = 'localrouter-system', agent_name = 'LocalRouter system', workspace = 'operator', runtime = 'bootstrap', expired_time = -1, unlimited_quota = 1, "group" = 'default', deleted_at = NULL WHERE id = ?`, key, localStatusEnabled, id)
	return err
}

func (store *localStore) validateToken(key string) (*localToken, error) {
	key = normalizeStoredToken(key)
	if key == "" {
		return nil, errors.New("empty token")
	}
	var token localToken
	var unlimited int
	err := store.db.QueryRow(`SELECT id, user_id, key, status, COALESCE(name, ''), COALESCE(agent_code, ''), COALESCE(agent_name, ''), COALESCE(workspace, ''), COALESCE(runtime, ''), COALESCE(created_time, 0), COALESCE(accessed_time, 0), COALESCE(expired_time, -1), COALESCE(unlimited_quota, 1), COALESCE("group", 'default') FROM tokens WHERE key = ? AND deleted_at IS NULL`, key).
		Scan(&token.ID, &token.UserID, &token.Key, &token.Status, &token.Name, &token.AgentCode, &token.AgentName, &token.Workspace, &token.Runtime, &token.CreatedTime, &token.AccessedTime, &token.ExpiredTime, &unlimited, &token.Group)
	if err != nil {
		return nil, err
	}
	token.UnlimitedQuota = unlimited != 0
	if token.Status != localStatusEnabled || (token.ExpiredTime >= 0 && token.ExpiredTime <= time.Now().Unix()) {
		return nil, errors.New("token is disabled or expired")
	}
	if token.Name != localTokenName && (strings.TrimSpace(token.AgentCode) == "" || strings.TrimSpace(token.Workspace) == "") {
		return nil, errors.New("Agent registration is required for this token")
	}
	_, _ = store.db.Exec(`UPDATE tokens SET accessed_time = ? WHERE id = ?`, time.Now().Unix(), token.ID)
	return &token, nil
}

func parsePage(query map[string][]string, defaultSize int) (int, int) {
	page, _ := strconv.Atoi(firstQuery(query, "page"))
	pageSize, _ := strconv.Atoi(firstQuery(query, "page_size"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = defaultSize
	}
	if pageSize > 200 {
		pageSize = 200
	}
	return page, pageSize
}

func firstQuery(query map[string][]string, key string) string {
	values := query[key]
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func (store *localStore) count(table string, where string, args ...any) (int64, error) {
	allowed := map[string]bool{"channels": true, "tokens": true, "logs": true}
	if !allowed[table] {
		return 0, errors.New("unsupported count table")
	}
	query := `SELECT COUNT(*) FROM ` + table
	if where != "" {
		query += ` WHERE ` + where
	}
	var count int64
	err := store.db.QueryRow(query, args...).Scan(&count)
	return count, err
}

func (store *localStore) listChannels(page, pageSize int) ([]localChannel, int64, error) {
	total, err := store.count("channels", "")
	if err != nil {
		return nil, 0, err
	}
	rows, err := store.db.Query(`SELECT id, COALESCE(type, 1), COALESCE(status, 1), COALESCE(name, ''), COALESCE(weight, 100), COALESCE(created_time, 0), COALESCE(test_time, 0), COALESCE(response_time, 0), COALESCE(base_url, ''), COALESCE(models, ''), COALESCE("group", 'default'), COALESCE(priority, 0), COALESCE(auto_ban, 1), COALESCE(balance, 0), COALESCE(upstream_profile, '{}') FROM channels ORDER BY priority DESC, id ASC LIMIT ? OFFSET ?`, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	channels := make([]localChannel, 0)
	for rows.Next() {
		var channel localChannel
		var profileJSON string
		if err := rows.Scan(&channel.ID, &channel.Type, &channel.Status, &channel.Name, &channel.Weight, &channel.CreatedTime, &channel.TestTime, &channel.ResponseTime, &channel.BaseURL, &channel.Models, &channel.Group, &channel.Priority, &channel.AutoBan, &channel.Balance, &profileJSON); err != nil {
			return nil, 0, err
		}
		if err := json.Unmarshal([]byte(profileJSON), &channel.UpstreamProfile); err != nil {
			return nil, 0, fmt.Errorf("decode channel %d upstream profile: %w", channel.ID, err)
		}
		channels = append(channels, channel)
	}
	return channels, total, rows.Err()
}

func (store *localStore) channel(id int) (localChannel, error) {
	var channel localChannel
	var profileJSON string
	err := store.db.QueryRow(`SELECT id, COALESCE(type, 1), key, COALESCE(status, 1), COALESCE(name, ''), COALESCE(weight, 100), COALESCE(created_time, 0), COALESCE(test_time, 0), COALESCE(response_time, 0), COALESCE(base_url, ''), COALESCE(models, ''), COALESCE("group", 'default'), COALESCE(priority, 0), COALESCE(auto_ban, 1), COALESCE(balance, 0), COALESCE(upstream_profile, '{}') FROM channels WHERE id = ?`, id).
		Scan(&channel.ID, &channel.Type, &channel.Key, &channel.Status, &channel.Name, &channel.Weight, &channel.CreatedTime, &channel.TestTime, &channel.ResponseTime, &channel.BaseURL, &channel.Models, &channel.Group, &channel.Priority, &channel.AutoBan, &channel.Balance, &profileJSON)
	if err == nil {
		err = json.Unmarshal([]byte(profileJSON), &channel.UpstreamProfile)
	}
	return channel, err
}

func (store *localStore) enabledChannels() ([]localChannel, error) {
	rows, err := store.db.Query(`SELECT id, COALESCE(type, 1), key, COALESCE(status, 1), COALESCE(name, ''), COALESCE(weight, 100), COALESCE(created_time, 0), COALESCE(test_time, 0), COALESCE(response_time, 0), COALESCE(base_url, ''), COALESCE(models, ''), COALESCE("group", 'default'), COALESCE(priority, 0), COALESCE(auto_ban, 1), COALESCE(balance, 0), COALESCE(upstream_profile, '{}') FROM channels WHERE status = ? ORDER BY priority DESC, weight DESC, id ASC`, localStatusEnabled)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	channels := make([]localChannel, 0)
	for rows.Next() {
		var channel localChannel
		var profileJSON string
		if err := rows.Scan(&channel.ID, &channel.Type, &channel.Key, &channel.Status, &channel.Name, &channel.Weight, &channel.CreatedTime, &channel.TestTime, &channel.ResponseTime, &channel.BaseURL, &channel.Models, &channel.Group, &channel.Priority, &channel.AutoBan, &channel.Balance, &profileJSON); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(profileJSON), &channel.UpstreamProfile); err != nil {
			return nil, fmt.Errorf("decode channel %d upstream profile: %w", channel.ID, err)
		}
		channels = append(channels, channel)
	}
	return channels, rows.Err()
}

func (store *localStore) enabledChannelCountsByType() (map[int]int, error) {
	rows, err := store.db.Query(`SELECT COALESCE(type, 1), COUNT(*) FROM channels WHERE status = ? GROUP BY COALESCE(type, 1)`, localStatusEnabled)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := make(map[int]int)
	for rows.Next() {
		var profileID, count int
		if err := rows.Scan(&profileID, &count); err != nil {
			return nil, err
		}
		counts[profileID] = count
	}
	return counts, rows.Err()
}

func (store *localStore) insertChannel(channel localChannel) (int64, error) {
	now := time.Now().Unix()
	profileJSON, err := json.Marshal(channel.UpstreamProfile)
	if err != nil {
		return 0, err
	}
	result, err := store.db.Exec(`INSERT INTO channels (type, key, status, name, weight, created_time, base_url, models, "group", priority, auto_ban, upstream_profile) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'default', ?, ?, ?)`,
		channel.Type, channel.Key, channel.Status, channel.Name, channel.Weight, now, channel.BaseURL, channel.Models, channel.Priority, channel.AutoBan, string(profileJSON))
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (store *localStore) updateChannel(channel localChannel) error {
	profileJSON, err := json.Marshal(channel.UpstreamProfile)
	if err != nil {
		return err
	}
	if strings.TrimSpace(channel.Key) == "" {
		_, err := store.db.Exec(`UPDATE channels SET type = ?, status = ?, name = ?, weight = ?, base_url = ?, models = ?, "group" = 'default', priority = ?, auto_ban = ?, upstream_profile = ? WHERE id = ?`,
			channel.Type, channel.Status, channel.Name, channel.Weight, channel.BaseURL, channel.Models, channel.Priority, channel.AutoBan, string(profileJSON), channel.ID)
		return err
	}
	_, err = store.db.Exec(`UPDATE channels SET type = ?, key = ?, status = ?, name = ?, weight = ?, base_url = ?, models = ?, "group" = 'default', priority = ?, auto_ban = ?, upstream_profile = ? WHERE id = ?`,
		channel.Type, channel.Key, channel.Status, channel.Name, channel.Weight, channel.BaseURL, channel.Models, channel.Priority, channel.AutoBan, string(profileJSON), channel.ID)
	return err
}

func (store *localStore) deleteChannel(id int) error {
	result, err := store.db.Exec(`DELETE FROM channels WHERE id = ?`, id)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (store *localStore) updateChannelTest(id int, status int, responseTime int) error {
	_, err := store.db.Exec(`UPDATE channels SET status = ?, test_time = ?, response_time = ? WHERE id = ?`, status, time.Now().Unix(), responseTime, id)
	return err
}

func (store *localStore) listTokens(userID, page, pageSize int) ([]localToken, int64, error) {
	total, err := store.count("tokens", "user_id = ? AND deleted_at IS NULL", userID)
	if err != nil {
		return nil, 0, err
	}
	rows, err := store.db.Query(`SELECT id, user_id, key, COALESCE(status, 1), COALESCE(name, ''), COALESCE(agent_code, ''), COALESCE(agent_name, ''), COALESCE(workspace, ''), COALESCE(runtime, ''), COALESCE(created_time, 0), COALESCE(accessed_time, 0), COALESCE(expired_time, -1), COALESCE(unlimited_quota, 1), COALESCE("group", 'default') FROM tokens WHERE user_id = ? AND deleted_at IS NULL ORDER BY id DESC LIMIT ? OFFSET ?`, userID, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	tokens := make([]localToken, 0)
	for rows.Next() {
		var token localToken
		var unlimited int
		if err := rows.Scan(&token.ID, &token.UserID, &token.Key, &token.Status, &token.Name, &token.AgentCode, &token.AgentName, &token.Workspace, &token.Runtime, &token.CreatedTime, &token.AccessedTime, &token.ExpiredTime, &unlimited, &token.Group); err != nil {
			return nil, 0, err
		}
		token.UnlimitedQuota = unlimited != 0
		token.Key = maskToken(token.Key)
		tokens = append(tokens, token)
	}
	return tokens, total, rows.Err()
}

func maskToken(key string) string {
	if len(key) <= 8 {
		return "sk-********"
	}
	return "sk-" + key[:4] + "****" + key[len(key)-4:]
}

func (store *localStore) tokenByID(userID, tokenID int, reveal bool) (localToken, error) {
	var token localToken
	var unlimited int
	err := store.db.QueryRow(`SELECT id, user_id, key, COALESCE(status, 1), COALESCE(name, ''), COALESCE(agent_code, ''), COALESCE(agent_name, ''), COALESCE(workspace, ''), COALESCE(runtime, ''), COALESCE(created_time, 0), COALESCE(accessed_time, 0), COALESCE(expired_time, -1), COALESCE(unlimited_quota, 1), COALESCE("group", 'default') FROM tokens WHERE id = ? AND user_id = ? AND deleted_at IS NULL`, tokenID, userID).
		Scan(&token.ID, &token.UserID, &token.Key, &token.Status, &token.Name, &token.AgentCode, &token.AgentName, &token.Workspace, &token.Runtime, &token.CreatedTime, &token.AccessedTime, &token.ExpiredTime, &unlimited, &token.Group)
	token.UnlimitedQuota = unlimited != 0
	if err == nil && !reveal {
		token.Key = maskToken(token.Key)
	}
	return token, err
}

func (store *localStore) createToken(userID int, token localToken) (int64, error) {
	secret, err := randomSecret(32, "")
	if err != nil {
		return 0, err
	}
	if token.ExpiredTime == 0 {
		token.ExpiredTime = -1
	}
	now := time.Now().Unix()
	result, err := store.db.Exec(`INSERT INTO tokens (user_id, key, status, name, agent_code, agent_name, workspace, runtime, created_time, accessed_time, expired_time, remain_quota, unlimited_quota, "group") VALUES (?, ?, 1, ?, ?, ?, ?, ?, ?, 0, ?, 0, 1, 'default')`,
		userID, secret, token.Name, token.AgentCode, token.AgentName, token.Workspace, token.Runtime, now, token.ExpiredTime)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (store *localStore) updateToken(userID int, token localToken) error {
	result, err := store.db.Exec(`UPDATE tokens SET status = ?, name = ?, agent_code = ?, agent_name = ?, workspace = ?, runtime = ?, expired_time = ? WHERE id = ? AND user_id = ? AND deleted_at IS NULL`,
		token.Status, token.Name, token.AgentCode, token.AgentName, token.Workspace, token.Runtime, token.ExpiredTime, token.ID, userID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (store *localStore) updateTokenKey(userID, tokenID int, key string) error {
	result, err := store.db.Exec(`UPDATE tokens SET key = ? WHERE id = ? AND user_id = ? AND deleted_at IS NULL`, normalizeStoredToken(key), tokenID, userID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (store *localStore) deleteToken(userID, tokenID int) error {
	result, err := store.db.Exec(`UPDATE tokens SET status = 2, deleted_at = CURRENT_TIMESTAMP WHERE id = ? AND user_id = ? AND deleted_at IS NULL`, tokenID, userID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (store *localStore) logRequest(ctx context.Context, entry localRequestLog) error {
	_, err := store.db.ExecContext(ctx, `INSERT INTO logs (user_id, created_at, type, content, username, token_name, model_name, quota, prompt_tokens, completion_tokens, cached_input_tokens, cache_write_input_tokens, reasoning_tokens, total_tokens, cost_usd, cost_status, use_time, is_stream, channel_id, channel_name, token_id, "group", request_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'default', ?)`,
		entry.UserID, entry.CreatedAt, entry.Type, entry.Content, entry.Username, entry.TokenName, entry.ModelName, entry.Quota, entry.PromptTokens, entry.CompletionTokens, entry.CachedInputTokens, entry.CacheWriteInputTokens, entry.ReasoningTokens, entry.TotalTokens, entry.CostUSD, entry.CostStatus, entry.UseTime, entry.IsStream, entry.ChannelID, entry.ChannelName, entry.TokenID, entry.RequestID)
	return err
}

func (store *localStore) listLogs(userID, page, pageSize int) ([]localRequestLog, int64, error) {
	total, err := store.count("logs", "user_id = ?", userID)
	if err != nil {
		return nil, 0, err
	}
	rows, err := store.db.Query(`SELECT id, COALESCE(user_id, 0), COALESCE(created_at, 0), COALESCE(type, 0), COALESCE(content, ''), COALESCE(username, ''), COALESCE(token_name, ''), COALESCE(model_name, ''), COALESCE(quota, 0), COALESCE(prompt_tokens, 0), COALESCE(completion_tokens, 0), COALESCE(cached_input_tokens, 0), COALESCE(cache_write_input_tokens, 0), COALESCE(reasoning_tokens, 0), COALESCE(total_tokens, 0), COALESCE(cost_usd, 0), COALESCE(cost_status, ''), COALESCE(use_time, 0), COALESCE(is_stream, 0), COALESCE(channel_id, 0), COALESCE(channel_name, ''), COALESCE(token_id, 0), COALESCE("group", 'default'), COALESCE(request_id, '') FROM logs WHERE user_id = ? ORDER BY id DESC LIMIT ? OFFSET ?`, userID, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	logs := make([]localRequestLog, 0)
	for rows.Next() {
		var entry localRequestLog
		if err := rows.Scan(&entry.ID, &entry.UserID, &entry.CreatedAt, &entry.Type, &entry.Content, &entry.Username, &entry.TokenName, &entry.ModelName, &entry.Quota, &entry.PromptTokens, &entry.CompletionTokens, &entry.CachedInputTokens, &entry.CacheWriteInputTokens, &entry.ReasoningTokens, &entry.TotalTokens, &entry.CostUSD, &entry.CostStatus, &entry.UseTime, &entry.IsStream, &entry.ChannelID, &entry.ChannelName, &entry.TokenID, &entry.Group, &entry.RequestID); err != nil {
			return nil, 0, err
		}
		logs = append(logs, entry)
	}
	return logs, total, rows.Err()
}
