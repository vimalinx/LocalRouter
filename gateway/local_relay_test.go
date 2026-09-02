package main

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testChannelProfiles(t *testing.T) *localChannelProfileRegistry {
	t.Helper()
	raw, err := webFiles.ReadFile("channel-profiles.json")
	require.NoError(t, err)
	profiles, err := loadLocalChannelProfilesJSON(raw)
	require.NoError(t, err)
	return profiles
}

func testChannelProfile(t *testing.T, key string) localChannelProfile {
	t.Helper()
	for _, profile := range testChannelProfiles(t).profiles {
		if profile.Key == key {
			return profile
		}
	}
	t.Fatalf("channel profile %q not found", key)
	return localChannelProfile{}
}

func TestRelayTargetURLDoesNotDuplicateProtocolVersion(t *testing.T) {
	target, err := relayTargetURL("https://example.test/v1", "/v1/chat/completions", "trace=1")
	require.NoError(t, err)
	assert.Equal(t, "https://example.test/v1/chat/completions?trace=1", target)

	target, err = relayTargetURL("https://example.test/gateway", "/v1/chat/completions", "")
	require.NoError(t, err)
	assert.Equal(t, "https://example.test/gateway/v1/chat/completions", target)
}

func TestUpstreamRequestReplacesLocalCredentials(t *testing.T) {
	incoming, err := http.NewRequest(http.MethodPost, "http://local.test/v1/chat/completions", nil)
	require.NoError(t, err)
	incoming.Header.Set("Authorization", "Bearer local-secret")
	incoming.Header.Set("X-Api-Key", "local-secret")
	incoming.Header.Set("X-Trace", "preserved")

	profile := testChannelProfile(t, "openai-compatible")
	request, err := makeUpstreamRequest(incoming, localChannel{
		Type: profile.ID, Key: "upstream-secret", BaseURL: "https://upstream.test",
	}, profile, []byte(`{"model":"m"}`))
	require.NoError(t, err)
	assert.Equal(t, "Bearer upstream-secret", request.Header.Get("Authorization"))
	assert.Empty(t, request.Header.Get("X-Api-Key"))
	assert.Equal(t, "preserved", request.Header.Get("X-Trace"))
}

func TestUpstreamRequestNeverForwardsLocalGeminiQueryToken(t *testing.T) {
	incoming, err := http.NewRequest(http.MethodPost, "http://local.test/v1beta/models/gemini:generateContent?key=local-secret&alt=sse", nil)
	require.NoError(t, err)
	profile := testChannelProfile(t, "gemini-native")
	request, err := makeUpstreamRequest(incoming, localChannel{
		Type: profile.ID, Key: "upstream-secret", BaseURL: "https://upstream.test",
	}, profile, []byte(`{"contents":[]}`))
	require.NoError(t, err)
	assert.Empty(t, request.URL.Query().Get("key"))
	assert.Equal(t, "sse", request.URL.Query().Get("alt"))
	assert.Equal(t, "upstream-secret", request.Header.Get("X-Goog-Api-Key"))
}

func TestChannelUpstreamProfileAppliesSupplierRequestPolicy(t *testing.T) {
	incoming, err := http.NewRequest(http.MethodPost, "http://local.test/v1/chat/completions?z=%2F&a=1&key=local-secret", nil)
	require.NoError(t, err)
	incoming.Header.Set("Authorization", "Bearer local-secret")
	incoming.Header.Set("User-Agent", "caller/1")
	incoming.Header.Set("X-Remove-Me", "gone")
	incoming.Header.Set("X-Trace", "trace-1")

	profile := protocolUpstreamProfile{
		SetHeaders:    map[string]string{"X-Provider-Mode": "silent"},
		RemoveHeaders: []string{"X-Remove-Me"},
		UserAgent:     "omit",
		Query:         "preserve-raw",
	}
	require.NoError(t, normalizeAndValidateProtocolUpstreamProfile(&profile))
	protocolProfile := testChannelProfile(t, "openai-compatible")
	request, err := makeUpstreamRequest(incoming, localChannel{
		Type: protocolProfile.ID, Key: "upstream-secret", BaseURL: "https://upstream.test", UpstreamProfile: profile,
	}, protocolProfile, []byte(`{"model":"m"}`))
	require.NoError(t, err)

	assert.Equal(t, "z=%2F&a=1", request.URL.RawQuery)
	assert.Equal(t, "silent", request.Header.Get("X-Provider-Mode"))
	assert.Equal(t, "trace-1", request.Header.Get("X-Trace"))
	assert.Empty(t, request.Header.Get("X-Remove-Me"))
	assert.Empty(t, request.Header.Values("User-Agent"))
	assert.Equal(t, "Bearer upstream-secret", request.Header.Get("Authorization"))
}

func TestValidateChannelRejectsProtectedSupplierHeader(t *testing.T) {
	channel := localChannel{
		Name: "unsafe", Type: testChannelProfile(t, "openai-compatible").ID, Key: "provider-key", Models: "m", BaseURL: "https://upstream.test",
		UpstreamProfile: protocolUpstreamProfile{SetHeaders: map[string]string{"X-Api-Key": "must-not-override-auth"}},
	}
	err := validateChannel(&channel, true, testChannelProfiles(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reserved header")
}

func TestChannelUpstreamProfilePersistsAcrossDatabaseUpgrade(t *testing.T) {
	store, err := openLocalStore(filepath.Join(t.TempDir(), "localrouter.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	channel := localChannel{
		Name: "profiled", Type: testChannelProfile(t, "openai-compatible").ID, Key: "provider-key", Status: 1, Weight: 100,
		Models: "m", BaseURL: "https://upstream.test", AutoBan: 1,
		UpstreamProfile: protocolUpstreamProfile{
			SetHeaders: map[string]string{"X-Provider-Mode": "silent"}, UserAgent: "omit", Query: "normalized",
		},
	}
	id, err := store.insertChannel(channel)
	require.NoError(t, err)
	stored, err := store.channel(int(id))
	require.NoError(t, err)
	assert.Equal(t, "silent", stored.UpstreamProfile.SetHeaders["X-Provider-Mode"])
	assert.Equal(t, "omit", stored.UpstreamProfile.UserAgent)
}

func TestChannelProfilesDrivePathAuthAndModelParsingWithoutProviderBranches(t *testing.T) {
	profiles := testChannelProfiles(t)
	gemini, ok := profiles.matchRequestPath("/v1beta/models/gemini-3:generateContent")
	require.True(t, ok)
	assert.Equal(t, "gemini-native", gemini.Key)
	anthropic, ok := profiles.matchRequestPath("/v1/messages")
	require.True(t, ok)
	assert.Equal(t, "anthropic-messages", anthropic.Key)
	openAI, ok := profiles.matchRequestPath("/v1/responses")
	require.True(t, ok)
	assert.Equal(t, "openai-compatible", openAI.Key)
	assert.Equal(t, []string{"gemini-3-flash"}, parseProfileModels([]byte(`{"models":[{"name":"models/gemini-3-flash"}]}`), gemini))
}

func TestChannelProfileFileRequiresPrivatePermissions(t *testing.T) {
	raw, err := webFiles.ReadFile("channel-profiles.json")
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "channel-profiles.json")
	require.NoError(t, os.WriteFile(path, raw, 0o644))
	_, err = loadLocalChannelProfiles(path)
	require.ErrorContains(t, err, "group or other")
	require.NoError(t, os.Chmod(path, 0o600))
	_, err = loadLocalChannelProfiles(path)
	require.NoError(t, err)
}

func TestBalancerKeepsPriorityGroupsAndRotatesWithinGroup(t *testing.T) {
	balancer := newLocalRelayBalancer()
	channels := []localChannel{
		{ID: 1, Priority: 10, Weight: 1},
		{ID: 2, Priority: 10, Weight: 1},
		{ID: 3, Priority: 0, Weight: 100},
	}
	first := balancer.ordered("model", channels)
	second := balancer.ordered("model", channels)
	require.Len(t, first, 3)
	require.Len(t, second, 3)
	assert.Equal(t, 3, first[2].ID)
	assert.Equal(t, 3, second[2].ID)
	assert.NotEqual(t, first[0].ID, second[0].ID)
}

func TestChannelSupportsExactWildcardAndPrefix(t *testing.T) {
	assert.True(t, channelSupportsModel("gpt-5,claude-*", "gpt-5"))
	assert.True(t, channelSupportsModel("gpt-5,claude-*", "claude-sonnet"))
	assert.True(t, channelSupportsModel("*", "anything"))
	assert.False(t, channelSupportsModel("gpt-5", "gpt-5-mini"))
}
