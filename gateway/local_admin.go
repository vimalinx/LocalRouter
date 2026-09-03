package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

func validateAgentRegistration(token *localToken) error {
	token.AgentCode = strings.TrimSpace(token.AgentCode)
	token.AgentName = strings.TrimSpace(token.AgentName)
	token.Workspace = strings.TrimSpace(token.Workspace)
	token.Runtime = strings.TrimSpace(token.Runtime)
	if len(token.AgentCode) < 2 || len(token.AgentCode) > 48 {
		return errors.New("agent_code must contain 2 to 48 characters")
	}
	if token.AgentCode == "localrouter-system" {
		return errors.New("agent_code localrouter-system is reserved")
	}
	for _, character := range []byte(token.AgentCode) {
		if !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') && !(character >= '0' && character <= '9') && character != '-' && character != '_' && character != '.' {
			return errors.New("agent_code may contain only letters, numbers, dot, underscore, or hyphen")
		}
	}
	if token.AgentName == "" || len([]rune(token.AgentName)) > 80 {
		return errors.New("agent_name must contain 1 to 80 characters")
	}
	if token.Workspace == "" || len(token.Workspace) > 512 || strings.ContainsAny(token.Workspace, "\r\n\x00") {
		return errors.New("workspace must contain 1 to 512 safe characters")
	}
	if len(token.Runtime) > 64 || strings.ContainsAny(token.Runtime, "\r\n\x00") {
		return errors.New("runtime must contain at most 64 safe characters")
	}
	return nil
}

type localProvider struct {
	ID          int    `json:"id"`
	Key         string `json:"key"`
	Name        string `json:"name"`
	BaseURL     string `json:"base_url"`
	RequiresKey bool   `json:"requires_key"`
}

type localChannelInput struct {
	ID              int                     `json:"id"`
	Type            int                     `json:"type"`
	Key             string                  `json:"key"`
	Status          int                     `json:"status"`
	Name            string                  `json:"name"`
	Weight          int                     `json:"weight"`
	BaseURL         string                  `json:"base_url"`
	Models          string                  `json:"models"`
	Priority        int                     `json:"priority"`
	AutoBan         int                     `json:"auto_ban"`
	UpstreamProfile protocolUpstreamProfile `json:"upstream_profile"`
}

func (input localChannelInput) channel() localChannel {
	return localChannel{
		ID: input.ID, Type: input.Type, Key: input.Key, Status: input.Status, Name: input.Name,
		Weight: input.Weight, BaseURL: input.BaseURL, Models: input.Models, Priority: input.Priority,
		AutoBan: input.AutoBan, Group: "default", UpstreamProfile: input.UpstreamProfile,
	}
}

func localSuccess(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

func localFailure(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"success": false, "message": message})
}

func localPage(page, pageSize int, total int64, items any) gin.H {
	return gin.H{"page": page, "page_size": pageSize, "total": total, "items": items}
}

func listProviders(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		localSuccess(c, runtime.channelProfiles.providers())
	}
}

func handleAdminModels(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		channels, err := runtime.store.enabledChannels()
		if err != nil {
			localFailure(c, http.StatusInternalServerError, "cannot load model catalogue")
			return
		}
		seen := make(map[string]bool)
		models := make([]string, 0)
		for _, channel := range channels {
			for _, model := range strings.Split(channel.Models, ",") {
				model = strings.TrimSpace(model)
				if model == "" || model == "*" || seen[model] {
					continue
				}
				seen[model] = true
				models = append(models, model)
			}
		}
		localSuccess(c, models)
	}
}

func validateChannel(channel *localChannel, requireKey bool, profiles *localChannelProfileRegistry) error {
	channel.Name = strings.TrimSpace(channel.Name)
	channel.Key = strings.TrimSpace(channel.Key)
	channel.BaseURL = strings.TrimRight(strings.TrimSpace(channel.BaseURL), "/")
	channel.Models = normalizeModelList(channel.Models)
	if channel.Name == "" {
		return errors.New("channel name is required")
	}
	profile, supported := profiles.profile(channel.Type)
	if !supported {
		return errors.New("unknown channel protocol profile")
	}
	if requireKey && profile.Auth.Type != "none" && channel.Key == "" {
		return errors.New("channel key is required by the selected protocol profile")
	}
	if channel.BaseURL == "" {
		channel.BaseURL = profile.BaseURL
	}
	parsed, err := url.Parse(channel.BaseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return errors.New("base_url must be an absolute HTTP or HTTPS URL without user info")
	}
	if channel.Status == 0 {
		channel.Status = localStatusEnabled
	}
	if channel.Weight <= 0 {
		channel.Weight = 100
	}
	if channel.AutoBan == 0 {
		channel.AutoBan = 1
	}
	if channelHasUpstreamProfile(channel.UpstreamProfile) {
		if err := normalizeAndValidateProtocolUpstreamProfile(&channel.UpstreamProfile); err != nil {
			return fmt.Errorf("upstream_profile: %w", err)
		}
		for header, value := range channel.UpstreamProfile.SetHeaders {
			if strings.Contains(value, "{{") || strings.Contains(value, "}}") {
				return fmt.Errorf("upstream_profile.set_headers %q must be literal for model channels", header)
			}
		}
	}
	channel.Group = "default"
	return nil
}

func normalizeModelList(models string) string {
	seen := make(map[string]bool)
	result := make([]string, 0)
	for _, model := range strings.Split(models, ",") {
		model = strings.TrimSpace(model)
		if model == "" || seen[model] {
			continue
		}
		seen[model] = true
		result = append(result, model)
	}
	return strings.Join(result, ",")
}

func handleListChannels(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, pageSize := parsePage(c.Request.URL.Query(), 50)
		items, total, err := runtime.store.listChannels(page, pageSize)
		if err != nil {
			localFailure(c, http.StatusInternalServerError, "cannot list channels")
			return
		}
		localSuccess(c, localPage(page, pageSize, total, items))
	}
}

func handleGetChannel(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			localFailure(c, http.StatusBadRequest, "invalid channel id")
			return
		}
		channel, err := runtime.store.channel(id)
		if errors.Is(err, sql.ErrNoRows) {
			localFailure(c, http.StatusNotFound, "channel not found")
			return
		}
		if err != nil {
			localFailure(c, http.StatusInternalServerError, "cannot read channel")
			return
		}
		localSuccess(c, channel)
	}
}

func handleAddChannel(runtime localRuntime) gin.HandlerFunc {
	type request struct {
		Mode    string            `json:"mode"`
		Channel localChannelInput `json:"channel"`
	}
	return func(c *gin.Context) {
		var payload request
		if err := c.ShouldBindJSON(&payload); err != nil {
			localFailure(c, http.StatusBadRequest, "invalid channel document")
			return
		}
		channel := payload.Channel.channel()
		if err := validateChannel(&channel, true, runtime.channelProfiles); err != nil {
			localFailure(c, http.StatusUnprocessableEntity, err.Error())
			return
		}
		id, err := runtime.store.insertChannel(channel)
		if err != nil {
			localFailure(c, http.StatusInternalServerError, "cannot save channel")
			return
		}
		localSuccess(c, gin.H{"id": id})
	}
}

func handleUpdateChannel(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input localChannelInput
		if err := c.ShouldBindJSON(&input); err != nil || input.ID <= 0 {
			localFailure(c, http.StatusBadRequest, "invalid channel document")
			return
		}
		channel := input.channel()
		if err := validateChannel(&channel, false, runtime.channelProfiles); err != nil {
			localFailure(c, http.StatusUnprocessableEntity, err.Error())
			return
		}
		if err := runtime.store.updateChannel(channel); err != nil {
			localFailure(c, http.StatusInternalServerError, "cannot update channel")
			return
		}
		localSuccess(c, channel)
	}
}

func handleDeleteChannel(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			localFailure(c, http.StatusBadRequest, "invalid channel id")
			return
		}
		if err := runtime.store.deleteChannel(id); errors.Is(err, sql.ErrNoRows) {
			localFailure(c, http.StatusNotFound, "channel not found")
			return
		} else if err != nil {
			localFailure(c, http.StatusInternalServerError, "cannot delete channel")
			return
		}
		localSuccess(c, gin.H{"deleted": true})
	}
}

func handleTestChannel(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			localFailure(c, http.StatusBadRequest, "invalid channel id")
			return
		}
		models, latency, err := fetchChannelModels(c.Request.Context(), runtime.relayClient, runtime.store, runtime.channelProfiles, id)
		if err != nil {
			_ = runtime.store.updateChannelTest(id, 2, latency)
			localFailure(c, http.StatusBadGateway, "upstream test failed: "+err.Error())
			return
		}
		_ = runtime.store.updateChannelTest(id, localStatusEnabled, latency)
		localSuccess(c, gin.H{"models": models, "response_time": latency})
	}
}

func handleFetchChannelModels(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			localFailure(c, http.StatusBadRequest, "invalid channel id")
			return
		}
		models, _, err := fetchChannelModels(c.Request.Context(), runtime.relayClient, runtime.store, runtime.channelProfiles, id)
		if err != nil {
			localFailure(c, http.StatusBadGateway, "cannot fetch upstream models: "+err.Error())
			return
		}
		localSuccess(c, models)
	}
}

func handleFixChannels(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		channels, err := runtime.store.enabledChannels()
		if err != nil {
			localFailure(c, http.StatusInternalServerError, "cannot inspect channels")
			return
		}
		passed := 0
		for _, channel := range channels {
			_, latency, testErr := fetchChannelModels(c.Request.Context(), runtime.relayClient, runtime.store, runtime.channelProfiles, channel.ID)
			status := localStatusEnabled
			if testErr != nil {
				status = 2
			} else {
				passed++
			}
			_ = runtime.store.updateChannelTest(channel.ID, status, latency)
		}
		localSuccess(c, gin.H{"tested": len(channels), "passed": passed})
	}
}

func handleListTokens(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, pageSize := parsePage(c.Request.URL.Query(), 50)
		items, total, err := runtime.store.listTokens(runtime.rootUser.ID, page, pageSize)
		if err != nil {
			localFailure(c, http.StatusInternalServerError, "cannot list tokens")
			return
		}
		localSuccess(c, localPage(page, pageSize, total, items))
	}
}

func handleGetToken(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			localFailure(c, http.StatusBadRequest, "invalid token id")
			return
		}
		token, err := runtime.store.tokenByID(runtime.rootUser.ID, id, false)
		if errors.Is(err, sql.ErrNoRows) {
			localFailure(c, http.StatusNotFound, "token not found")
			return
		}
		if err != nil {
			localFailure(c, http.StatusInternalServerError, "cannot read token")
			return
		}
		localSuccess(c, token)
	}
}

func handleAddToken(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		var token localToken
		if err := c.ShouldBindJSON(&token); err != nil {
			localFailure(c, http.StatusBadRequest, "invalid token document")
			return
		}
		token.Name = strings.TrimSpace(token.Name)
		if token.Name == "" || len(token.Name) > 64 {
			localFailure(c, http.StatusUnprocessableEntity, "token name must contain 1 to 64 characters")
			return
		}
		if token.Name == localTokenName {
			localFailure(c, http.StatusConflict, "token name is reserved for the system token")
			return
		}
		if err := validateAgentRegistration(&token); err != nil {
			localFailure(c, http.StatusUnprocessableEntity, err.Error())
			return
		}
		id, err := runtime.store.createToken(runtime.rootUser.ID, token)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				localFailure(c, http.StatusConflict, "agent_code is already registered")
				return
			}
			localFailure(c, http.StatusInternalServerError, "cannot issue Agent token")
			return
		}
		localSuccess(c, gin.H{"id": id})
	}
}

func handleUpdateToken(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		var token localToken
		if err := c.ShouldBindJSON(&token); err != nil || token.ID <= 0 {
			localFailure(c, http.StatusBadRequest, "invalid token document")
			return
		}
		token.Name = strings.TrimSpace(token.Name)
		if token.Name == "" {
			localFailure(c, http.StatusUnprocessableEntity, "token name is required")
			return
		}
		stored, err := runtime.store.tokenByID(runtime.rootUser.ID, token.ID, false)
		if err != nil {
			localFailure(c, http.StatusNotFound, "token not found")
			return
		}
		if stored.Name != localTokenName && token.Name == localTokenName {
			localFailure(c, http.StatusConflict, "token name is reserved for the system token")
			return
		}
		if stored.Name != localTokenName {
			if err := validateAgentRegistration(&token); err != nil {
				localFailure(c, http.StatusUnprocessableEntity, err.Error())
				return
			}
		} else {
			token.AgentCode, token.AgentName, token.Workspace, token.Runtime = stored.AgentCode, stored.AgentName, stored.Workspace, stored.Runtime
		}
		if token.Status == 0 {
			token.Status = localStatusEnabled
		}
		if err := runtime.store.updateToken(runtime.rootUser.ID, token); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				localFailure(c, http.StatusConflict, "agent_code is already registered")
				return
			}
			localFailure(c, http.StatusInternalServerError, "cannot update token")
			return
		}
		localSuccess(c, token)
	}
}

func handleDeleteToken(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			localFailure(c, http.StatusBadRequest, "invalid token id")
			return
		}
		token, err := runtime.store.tokenByID(runtime.rootUser.ID, id, true)
		if err != nil {
			localFailure(c, http.StatusNotFound, "token not found")
			return
		}
		if token.Name == localTokenName {
			localFailure(c, http.StatusConflict, "the bootstrap token cannot be deleted")
			return
		}
		if err := runtime.store.deleteToken(runtime.rootUser.ID, id); err != nil {
			localFailure(c, http.StatusInternalServerError, "cannot revoke token")
			return
		}
		localSuccess(c, gin.H{"deleted": true})
	}
}

func handleRevealToken(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			localFailure(c, http.StatusBadRequest, "invalid token id")
			return
		}
		token, err := runtime.store.tokenByID(runtime.rootUser.ID, id, true)
		if err != nil {
			localFailure(c, http.StatusNotFound, "token not found")
			return
		}
		localSuccess(c, gin.H{"key": "sk-" + normalizeStoredToken(token.Key)})
	}
}

func handleUpdateTokenKey(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil || id <= 0 {
			localFailure(c, http.StatusBadRequest, "invalid token id")
			return
		}
		token, err := runtime.store.tokenByID(runtime.rootUser.ID, id, false)
		if err != nil {
			localFailure(c, http.StatusNotFound, "token not found")
			return
		}
		if token.Name == localTokenName {
			localFailure(c, http.StatusConflict, "the bootstrap token key cannot be changed here")
			return
		}
		var request struct {
			Key string `json:"key"`
		}
		decoder := json.NewDecoder(io.LimitReader(c.Request.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			localFailure(c, http.StatusBadRequest, "invalid token key document")
			return
		}
		request.Key = strings.TrimSpace(request.Key)
		if err := validateAdminToken(request.Key); err != nil {
			localFailure(c, http.StatusUnprocessableEntity, err.Error())
			return
		}
		if err := runtime.store.updateTokenKey(runtime.rootUser.ID, id, request.Key); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				localFailure(c, http.StatusConflict, "token key is already in use")
				return
			}
			localFailure(c, http.StatusInternalServerError, "cannot update token key")
			return
		}
		localSuccess(c, gin.H{"updated": true})
	}
}

func handleListLogs(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, pageSize := parsePage(c.Request.URL.Query(), 50)
		items, total, err := runtime.store.listLogs(runtime.rootUser.ID, page, pageSize)
		if err != nil {
			localFailure(c, http.StatusInternalServerError, "cannot list request logs")
			return
		}
		localSuccess(c, localPage(page, pageSize, total, items))
	}
}
