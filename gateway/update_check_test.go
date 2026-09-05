package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSemanticVersionComparison(t *testing.T) {
	t.Parallel()
	tests := []struct {
		left, right string
		want        int
	}{
		{"0.1.0-alpha.5", "0.1.0-alpha.4", 1},
		{"v0.1.0", "0.1.0-alpha.5", 1},
		{"0.1.1", "0.1.0", 1},
		{"1.0.0-rc.2", "1.0.0-rc.10", -1},
		{"1.0.0", "1.0.0", 0},
	}
	for _, test := range tests {
		left, leftValid := parseSemanticVersion(test.left)
		right, rightValid := parseSemanticVersion(test.right)
		require.True(t, leftValid, test.left)
		require.True(t, rightValid, test.right)
		assert.Equal(t, test.want, compareSemanticVersions(left, right), test.left+" vs "+test.right)
	}
	for _, invalid := range []string{"dev", "1.0", "01.0.0", "1.0.0-alpha.01", "1.0.0+"} {
		_, valid := parseSemanticVersion(invalid)
		assert.False(t, valid, invalid)
	}
}

func TestSelectLatestReleaseRespectsChannel(t *testing.T) {
	t.Parallel()
	releases := []githubRelease{
		{TagName: "v0.2.0-beta.1", Prerelease: true},
		{TagName: "v0.1.1", Prerelease: false},
		{TagName: "v9.0.0", Draft: true},
	}
	stable, found := selectLatestRelease("0.1.0", releases)
	require.True(t, found)
	assert.Equal(t, "v0.1.1", stable.TagName)
	prerelease, found := selectLatestRelease("0.1.0-alpha.5", releases)
	require.True(t, found)
	assert.Equal(t, "v0.2.0-beta.1", prerelease.TagName)
}

func TestUpdateCheckerCachesETagAndReportsAvailability(t *testing.T) {
	t.Parallel()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		assert.Equal(t, "application/vnd.github+json", request.Header.Get("Accept"))
		assert.Equal(t, "2026-03-10", request.Header.Get("X-GitHub-Api-Version"))
		if request.Header.Get("If-None-Match") == `"release-v1"` {
			writer.WriteHeader(http.StatusNotModified)
			return
		}
		writer.Header().Set("ETag", `"release-v1"`)
		_ = json.NewEncoder(writer).Encode([]githubRelease{{
			TagName: "v0.1.1", PublishedAt: "2026-09-10T00:00:00Z",
		}})
	}))
	defer server.Close()

	checker := newUpdateChecker(true, "0.1.0", server.Client(), server.URL)
	checkedAt := time.Date(2026, 9, 5, 1, 2, 3, 0, time.UTC)
	checker.now = func() time.Time { return checkedAt }
	first, err := checker.check(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "available", first.Status)
	assert.Equal(t, "v0.1.1", first.LatestVersion)
	assert.Equal(t, githubReleaseBase+"v0.1.1", first.ReleaseURL)

	checkedAt = checkedAt.Add(time.Hour)
	second, err := checker.check(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "available", second.Status)
	assert.Equal(t, checkedAt.Format(time.RFC3339), second.CheckedAt)
	assert.Equal(t, 2, requests)
}

func TestUpdateCheckerFailureIsSanitizedAndNonFatal(t *testing.T) {
	t.Parallel()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests++
		writer.Header().Set("Retry-After", "120")
		writer.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	checker := newUpdateChecker(true, "0.1.0", server.Client(), server.URL)
	status, err := checker.check(context.Background())
	require.Error(t, err)
	assert.Equal(t, "error", status.Status)
	assert.Equal(t, "rate-limited", status.ErrorCode)
	assert.Empty(t, status.LatestVersion)
	assert.NotEmpty(t, status.RetryAfter)

	status, err = checker.check(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "rate-limited", status.ErrorCode)
	assert.Equal(t, 1, requests, "a manual recheck must not hammer GitHub during the retry window")
}

func TestUpdateCheckerRestoresCachedStateAfterTransientFailure(t *testing.T) {
	t.Parallel()
	requestNumber := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requestNumber++
		switch requestNumber {
		case 1:
			writer.Header().Set("ETag", `"release-v1"`)
			_ = json.NewEncoder(writer).Encode([]githubRelease{{TagName: "v0.1.1"}})
		case 2:
			writer.WriteHeader(http.StatusServiceUnavailable)
		default:
			writer.WriteHeader(http.StatusNotModified)
		}
	}))
	defer server.Close()

	checker := newUpdateChecker(true, "0.1.0", server.Client(), server.URL)
	first, err := checker.check(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "available", first.Status)
	failed, err := checker.check(context.Background())
	require.Error(t, err)
	assert.Equal(t, "error", failed.Status)
	restored, err := checker.check(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "available", restored.Status)
	assert.Equal(t, "v0.1.1", restored.LatestVersion)
}

func TestUpdateCheckConfigurationDefaultsOnAndCanBeDisabled(t *testing.T) {
	t.Setenv("LOCAL_GATEWAY_UPDATE_CHECK_ENABLED", "")
	enabled, err := parseEnvironmentBooleanDefault("LOCAL_GATEWAY_UPDATE_CHECK_ENABLED", true)
	require.NoError(t, err)
	assert.True(t, enabled)

	t.Setenv("LOCAL_GATEWAY_UPDATE_CHECK_ENABLED", "false")
	enabled, err = parseEnvironmentBooleanDefault("LOCAL_GATEWAY_UPDATE_CHECK_ENABLED", true)
	require.NoError(t, err)
	assert.False(t, enabled)

	t.Setenv("LOCAL_GATEWAY_UPDATE_CHECK_ENABLED", "sometimes")
	_, err = parseEnvironmentBooleanDefault("LOCAL_GATEWAY_UPDATE_CHECK_ENABLED", true)
	assert.EqualError(t, err, "LOCAL_GATEWAY_UPDATE_CHECK_ENABLED must be true or false")
}
