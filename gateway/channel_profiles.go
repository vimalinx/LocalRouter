package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/tidwall/gjson"
)

type localChannelProfileDocument struct {
	SchemaVersion string                `json:"schema_version"`
	Profiles      []localChannelProfile `json:"profiles"`
}

type localChannelProfile struct {
	ID             int                      `json:"id"`
	Key            string                   `json:"key"`
	Name           string                   `json:"name"`
	BaseURL        string                   `json:"base_url"`
	RequestPaths   localProfileRequestPaths `json:"request_paths"`
	Auth           localProfileAuth         `json:"auth"`
	ModelCatalogue localProfileModelCatalog `json:"model_catalog"`
}

type localProfileRequestPaths struct {
	Exact    []string `json:"exact,omitempty"`
	Prefixes []string `json:"prefixes,omitempty"`
	Default  bool     `json:"default,omitempty"`
}

type localProfileAuth struct {
	Type           string            `json:"type"`
	Header         string            `json:"header,omitempty"`
	Prefix         string            `json:"prefix,omitempty"`
	QueryParameter string            `json:"query_parameter,omitempty"`
	DefaultHeaders map[string]string `json:"default_headers,omitempty"`
}

type localProfileModelCatalog struct {
	Path   string                         `json:"path"`
	Arrays []localProfileModelResultArray `json:"arrays"`
}

type localProfileModelResultArray struct {
	Path       string `json:"path"`
	IDField    string `json:"id_field"`
	TrimPrefix string `json:"trim_prefix,omitempty"`
}

type localChannelProfileRegistry struct {
	profiles  []localChannelProfile
	byID      map[int]localChannelProfile
	defaultID int
}

func loadLocalChannelProfiles(path string) (*localChannelProfileRegistry, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("channel profile config must be a regular non-symlink file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("channel profile config must not be accessible by group or other users")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return loadLocalChannelProfilesJSON(raw)
}

func loadLocalChannelProfilesJSON(raw []byte) (*localChannelProfileRegistry, error) {
	var document localChannelProfileDocument
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("channel profile config must contain one JSON object")
	}
	if document.SchemaVersion != "1" || len(document.Profiles) == 0 {
		return nil, errors.New("channel profile config requires schema_version 1 and at least one profile")
	}
	registry := &localChannelProfileRegistry{profiles: append([]localChannelProfile(nil), document.Profiles...), byID: make(map[int]localChannelProfile, len(document.Profiles))}
	seenKeys := make(map[string]bool, len(document.Profiles))
	seenExact := make(map[string]bool)
	seenPrefixes := make(map[string]bool)
	for index := range registry.profiles {
		profile := &registry.profiles[index]
		profile.Key = strings.TrimSpace(profile.Key)
		profile.Name = strings.TrimSpace(profile.Name)
		profile.BaseURL = strings.TrimRight(strings.TrimSpace(profile.BaseURL), "/")
		if profile.ID < 1 || profile.Key == "" || profile.Name == "" {
			return nil, fmt.Errorf("profile %d requires a positive id, key and name", index)
		}
		if _, exists := registry.byID[profile.ID]; exists || seenKeys[profile.Key] {
			return nil, fmt.Errorf("profile %d duplicates id or key", profile.ID)
		}
		seenKeys[profile.Key] = true
		parsed, err := url.Parse(profile.BaseURL)
		if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return nil, fmt.Errorf("profile %s base_url must be absolute HTTP or HTTPS without user info", profile.Key)
		}
		if profile.RequestPaths.Default {
			if registry.defaultID != 0 {
				return nil, errors.New("channel profiles can define only one default request path profile")
			}
			registry.defaultID = profile.ID
		}
		for _, path := range profile.RequestPaths.Exact {
			if err := validateProfilePath(path); err != nil || seenExact[path] {
				return nil, fmt.Errorf("profile %s has invalid or duplicate exact path %q", profile.Key, path)
			}
			seenExact[path] = true
		}
		for _, prefix := range profile.RequestPaths.Prefixes {
			if err := validateProfilePath(prefix); err != nil || seenPrefixes[prefix] {
				return nil, fmt.Errorf("profile %s has invalid or duplicate path prefix %q", profile.Key, prefix)
			}
			seenPrefixes[prefix] = true
		}
		if err := validateLocalProfileAuth(profile.Auth); err != nil {
			return nil, fmt.Errorf("profile %s auth: %w", profile.Key, err)
		}
		if err := validateProfilePath(profile.ModelCatalogue.Path); err != nil || len(profile.ModelCatalogue.Arrays) == 0 {
			return nil, fmt.Errorf("profile %s requires a model_catalog path and result arrays", profile.Key)
		}
		for _, array := range profile.ModelCatalogue.Arrays {
			if strings.TrimSpace(array.Path) == "" || strings.TrimSpace(array.IDField) == "" {
				return nil, fmt.Errorf("profile %s model result arrays require path and id_field", profile.Key)
			}
		}
		registry.byID[profile.ID] = *profile
	}
	if registry.defaultID == 0 {
		return nil, errors.New("channel profiles require one default request path profile")
	}
	sort.SliceStable(registry.profiles, func(i, j int) bool { return registry.profiles[i].ID < registry.profiles[j].ID })
	return registry, nil
}

func validateProfilePath(path string) error {
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "://") || strings.Contains(path, "..") || strings.ContainsAny(path, "\r\n") {
		return errors.New("path must be a safe absolute HTTP path")
	}
	return nil
}

func validateLocalProfileAuth(auth localProfileAuth) error {
	if auth.Type != "header" && auth.Type != "query" && auth.Type != "none" {
		return errors.New("type must be header, query, or none")
	}
	if auth.Type == "header" {
		if auth.Header == "" || relayRequestHeaderHopByHop(auth.Header) {
			return errors.New("header auth requires a safe non-hop-by-hop header")
		}
	}
	if auth.Type == "query" && (auth.QueryParameter == "" || strings.ContainsAny(auth.QueryParameter, "&=\r\n")) {
		return errors.New("query auth requires a safe query_parameter")
	}
	if strings.ContainsAny(auth.Prefix, "\r\n") {
		return errors.New("auth prefix cannot contain newlines")
	}
	for name, value := range auth.DefaultHeaders {
		if relayRequestHeaderBlocked(name) || strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("default header %q is reserved or invalid", name)
		}
	}
	return nil
}

func (registry *localChannelProfileRegistry) profile(id int) (localChannelProfile, bool) {
	if registry == nil {
		return localChannelProfile{}, false
	}
	profile, ok := registry.byID[id]
	return profile, ok
}

func (registry *localChannelProfileRegistry) matchRequestPath(path string) (localChannelProfile, bool) {
	if registry == nil {
		return localChannelProfile{}, false
	}
	for _, profile := range registry.profiles {
		for _, exact := range profile.RequestPaths.Exact {
			if path == exact {
				return profile, true
			}
		}
	}
	bestLength, bestID := -1, 0
	for _, profile := range registry.profiles {
		for _, prefix := range profile.RequestPaths.Prefixes {
			if strings.HasPrefix(path, prefix) && len(prefix) > bestLength {
				bestLength, bestID = len(prefix), profile.ID
			}
		}
	}
	if bestID != 0 {
		return registry.profile(bestID)
	}
	return registry.profile(registry.defaultID)
}

func (registry *localChannelProfileRegistry) providers() []localProvider {
	providers := make([]localProvider, 0, len(registry.profiles))
	for _, profile := range registry.profiles {
		providers = append(providers, localProvider{ID: profile.ID, Key: profile.Key, Name: profile.Name, BaseURL: profile.BaseURL, RequiresKey: profile.Auth.Type != "none"})
	}
	return providers
}

func applyLocalProfileAuthorization(request *http.Request, key string, profile localChannelProfile) error {
	for name, value := range profile.Auth.DefaultHeaders {
		if request.Header.Get(name) == "" {
			request.Header.Set(name, value)
		}
	}
	switch profile.Auth.Type {
	case "header":
		request.Header.Set(profile.Auth.Header, profile.Auth.Prefix+key)
	case "query":
		query := request.URL.Query()
		query.Set(profile.Auth.QueryParameter, profile.Auth.Prefix+key)
		request.URL.RawQuery = query.Encode()
	case "none":
		return nil
	default:
		return errors.New("channel profile auth is invalid")
	}
	return nil
}

func parseProfileModels(body []byte, profile localChannelProfile) []string {
	result := make([]string, 0)
	seen := make(map[string]bool)
	for _, array := range profile.ModelCatalogue.Arrays {
		for _, entry := range gjson.GetBytes(body, array.Path).Array() {
			name := strings.TrimSpace(entry.Get(array.IDField).String())
			name = strings.TrimPrefix(name, array.TrimPrefix)
			if name != "" && !seen[name] {
				seen[name] = true
				result = append(result, name)
			}
		}
	}
	return result
}
