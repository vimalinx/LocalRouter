package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	githubReleasesURL  = "https://api.github.com/repos/vimalinx/LocalRouter/releases?per_page=20"
	githubReleaseBase  = "https://github.com/vimalinx/LocalRouter/releases/tag/"
	updateCheckPeriod  = 6 * time.Hour
	updateCheckTimeout = 8 * time.Second
)

type updateStatus struct {
	Enabled        bool   `json:"enabled"`
	Automatic      bool   `json:"automatic"`
	CurrentVersion string `json:"current_version"`
	Channel        string `json:"channel"`
	Status         string `json:"status"`
	LatestVersion  string `json:"latest_version,omitempty"`
	ReleaseURL     string `json:"release_url,omitempty"`
	PublishedAt    string `json:"published_at,omitempty"`
	CheckedAt      string `json:"checked_at,omitempty"`
	NextCheckAt    string `json:"next_check_at,omitempty"`
	RetryAfter     string `json:"retry_after,omitempty"`
	ErrorCode      string `json:"error_code,omitempty"`
}

type githubRelease struct {
	TagName     string `json:"tag_name"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
	PublishedAt string `json:"published_at"`
}

type updateChecker struct {
	mu               sync.RWMutex
	checkMu          sync.Mutex
	startOnce        sync.Once
	stopOnce         sync.Once
	wait             sync.WaitGroup
	cancel           context.CancelFunc
	enabled          bool
	current          string
	client           *http.Client
	releasesURL      string
	interval         time.Duration
	now              func() time.Time
	etag             string
	state            updateStatus
	cached           updateStatus
	hasCached        bool
	rateLimitedUntil time.Time
}

func newUpdateChecker(enabled bool, currentVersion string, client *http.Client, releasesURL string) *updateChecker {
	if client == nil {
		client = &http.Client{Timeout: updateCheckTimeout}
	}
	if releasesURL == "" {
		releasesURL = githubReleasesURL
	}
	channel := updateChannel(currentVersion)
	status := "pending"
	if !enabled {
		status = "disabled"
	}
	return &updateChecker{
		enabled: enabled, current: currentVersion, client: client, releasesURL: releasesURL,
		interval: updateCheckPeriod, now: time.Now,
		state: updateStatus{
			Enabled: enabled, Automatic: enabled, CurrentVersion: currentVersion,
			Channel: channel, Status: status,
		},
	}
}

func (checker *updateChecker) start() {
	if checker == nil || !checker.enabled {
		return
	}
	checker.startOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		checker.cancel = cancel
		checker.wait.Add(1)
		go func() {
			defer checker.wait.Done()
			_, _ = checker.check(ctx)
			ticker := time.NewTicker(checker.interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					_, _ = checker.check(ctx)
				}
			}
		}()
	})
}

func (checker *updateChecker) stop() {
	if checker == nil {
		return
	}
	checker.stopOnce.Do(func() {
		if checker.cancel != nil {
			checker.cancel()
		}
		checker.wait.Wait()
	})
}

func (checker *updateChecker) status() updateStatus {
	if checker == nil {
		return updateStatus{CurrentVersion: buildVersion, Channel: updateChannel(buildVersion), Status: "unavailable"}
	}
	checker.mu.RLock()
	defer checker.mu.RUnlock()
	return checker.state
}

func (checker *updateChecker) check(ctx context.Context) (updateStatus, error) {
	if checker == nil || !checker.enabled {
		return checker.status(), nil
	}
	checker.checkMu.Lock()
	defer checker.checkMu.Unlock()

	now := checker.now().UTC()
	checker.mu.RLock()
	if now.Before(checker.rateLimitedUntil) {
		state := checker.state
		checker.mu.RUnlock()
		return state, nil
	}
	checker.mu.RUnlock()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, checker.releasesURL, nil)
	if err != nil {
		return checker.recordFailure(now, "request-invalid", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	request.Header.Set("User-Agent", "LocalRouter/"+checker.current)
	checker.mu.RLock()
	etag := checker.etag
	checker.mu.RUnlock()
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}

	response, err := checker.client.Do(request)
	if err != nil {
		return checker.recordFailure(now, "request-failed", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotModified {
		checker.mu.Lock()
		if !checker.hasCached {
			checker.mu.Unlock()
			return checker.recordFailure(now, "cache-missing", errors.New("GitHub returned not modified without a cached release response"))
		}
		checker.state = checker.cached
		checker.state.CheckedAt = now.Format(time.RFC3339)
		checker.state.NextCheckAt = now.Add(checker.interval).Format(time.RFC3339)
		checker.cached = checker.state
		state := checker.state
		checker.mu.Unlock()
		return state, nil
	}
	if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusTooManyRequests {
		retryAfter := githubRetryAfter(response.Header, now)
		checker.mu.Lock()
		checker.rateLimitedUntil = retryAfter
		checker.mu.Unlock()
		state, failure := checker.recordFailure(now, "rate-limited", fmt.Errorf("GitHub returned HTTP %d", response.StatusCode))
		checker.mu.Lock()
		checker.state.RetryAfter = retryAfter.Format(time.RFC3339)
		state = checker.state
		checker.mu.Unlock()
		return state, failure
	}
	if response.StatusCode != http.StatusOK {
		return checker.recordFailure(now, "github-unavailable", fmt.Errorf("GitHub returned HTTP %d", response.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return checker.recordFailure(now, "response-read-failed", err)
	}
	var releases []githubRelease
	// Only decode the public fields used by LocalRouter. Unknown GitHub fields
	// are intentionally ignored so additive API changes do not break checks.
	if err := json.Unmarshal(body, &releases); err != nil {
		return checker.recordFailure(now, "response-invalid", err)
	}
	latest, found := selectLatestRelease(checker.current, releases)
	state := updateStatus{
		Enabled: true, Automatic: true, CurrentVersion: checker.current,
		Channel: updateChannel(checker.current), CheckedAt: now.Format(time.RFC3339),
		NextCheckAt: now.Add(checker.interval).Format(time.RFC3339),
	}
	if !found {
		state.Status = "no-release"
	} else {
		state.LatestVersion = latest.TagName
		state.ReleaseURL = githubReleaseBase + url.PathEscape(latest.TagName)
		state.PublishedAt = latest.PublishedAt
		current, validCurrent := parseSemanticVersion(checker.current)
		latestVersion, _ := parseSemanticVersion(latest.TagName)
		switch {
		case !validCurrent:
			state.Status = "development"
		case compareSemanticVersions(latestVersion, current) > 0:
			state.Status = "available"
		case compareSemanticVersions(latestVersion, current) < 0:
			state.Status = "ahead"
		default:
			state.Status = "current"
		}
	}
	checker.mu.Lock()
	checker.etag = response.Header.Get("ETag")
	checker.rateLimitedUntil = time.Time{}
	checker.state = state
	checker.cached = state
	checker.hasCached = true
	checker.mu.Unlock()
	return state, nil
}

func githubRetryAfter(header http.Header, now time.Time) time.Time {
	if raw := strings.TrimSpace(header.Get("Retry-After")); raw != "" {
		if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds > 0 {
			return now.Add(time.Duration(seconds) * time.Second)
		}
		if retryAt, err := http.ParseTime(raw); err == nil && retryAt.After(now) {
			return retryAt.UTC()
		}
	}
	if raw := strings.TrimSpace(header.Get("X-RateLimit-Reset")); raw != "" {
		if unixSeconds, err := strconv.ParseInt(raw, 10, 64); err == nil {
			retryAt := time.Unix(unixSeconds, 0).UTC()
			if retryAt.After(now) {
				return retryAt
			}
		}
	}
	return now.Add(15 * time.Minute)
}

func (checker *updateChecker) recordFailure(now time.Time, code string, err error) (updateStatus, error) {
	checker.mu.Lock()
	checker.state.Status = "error"
	checker.state.CheckedAt = now.Format(time.RFC3339)
	checker.state.NextCheckAt = now.Add(checker.interval).Format(time.RFC3339)
	checker.state.ErrorCode = code
	state := checker.state
	checker.mu.Unlock()
	return state, err
}

func selectLatestRelease(currentVersion string, releases []githubRelease) (githubRelease, bool) {
	channel := updateChannel(currentVersion)
	var selected githubRelease
	var selectedVersion semanticVersion
	found := false
	for _, release := range releases {
		candidate, valid := parseSemanticVersion(release.TagName)
		if release.Draft || !valid || (channel == "stable" && (release.Prerelease || candidate.prerelease != "")) {
			continue
		}
		if !found || compareSemanticVersions(candidate, selectedVersion) > 0 {
			selected, selectedVersion, found = release, candidate, true
		}
	}
	return selected, found
}

func updateChannel(version string) string {
	parsed, valid := parseSemanticVersion(version)
	if !valid {
		return "development"
	}
	if parsed.prerelease != "" {
		return "prerelease"
	}
	return "stable"
}

type semanticVersion struct {
	major      uint64
	minor      uint64
	patch      uint64
	prerelease string
}

func parseSemanticVersion(raw string) (semanticVersion, bool) {
	value := strings.TrimPrefix(strings.TrimSpace(raw), "v")
	buildParts := strings.SplitN(value, "+", 2)
	if len(buildParts) == 2 && !validBuildMetadata(buildParts[1]) {
		return semanticVersion{}, false
	}
	value = buildParts[0]
	parts := strings.SplitN(value, "-", 2)
	core := strings.Split(parts[0], ".")
	if len(core) != 3 {
		return semanticVersion{}, false
	}
	numbers := make([]uint64, 3)
	for index, item := range core {
		if item == "" || (len(item) > 1 && item[0] == '0') {
			return semanticVersion{}, false
		}
		number, err := strconv.ParseUint(item, 10, 64)
		if err != nil {
			return semanticVersion{}, false
		}
		numbers[index] = number
	}
	prerelease := ""
	if len(parts) == 2 {
		prerelease = parts[1]
		if !validPrerelease(prerelease) {
			return semanticVersion{}, false
		}
	}
	return semanticVersion{major: numbers[0], minor: numbers[1], patch: numbers[2], prerelease: prerelease}, true
}

func validBuildMetadata(value string) bool {
	if value == "" {
		return false
	}
	for _, identifier := range strings.Split(value, ".") {
		if identifier == "" {
			return false
		}
		for _, character := range identifier {
			if !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') && !(character >= '0' && character <= '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func validPrerelease(value string) bool {
	if value == "" {
		return false
	}
	for _, identifier := range strings.Split(value, ".") {
		if identifier == "" || (len(identifier) > 1 && identifier[0] == '0' && isNumeric(identifier)) {
			return false
		}
		for _, character := range identifier {
			if !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') && !(character >= '0' && character <= '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func compareSemanticVersions(left, right semanticVersion) int {
	for _, values := range [][2]uint64{{left.major, right.major}, {left.minor, right.minor}, {left.patch, right.patch}} {
		if values[0] < values[1] {
			return -1
		}
		if values[0] > values[1] {
			return 1
		}
	}
	if left.prerelease == right.prerelease {
		return 0
	}
	if left.prerelease == "" {
		return 1
	}
	if right.prerelease == "" {
		return -1
	}
	leftParts, rightParts := strings.Split(left.prerelease, "."), strings.Split(right.prerelease, ".")
	for index := 0; index < len(leftParts) && index < len(rightParts); index++ {
		leftPart, rightPart := leftParts[index], rightParts[index]
		if leftPart == rightPart {
			continue
		}
		leftNumeric, rightNumeric := isNumeric(leftPart), isNumeric(rightPart)
		if leftNumeric && rightNumeric {
			if len(leftPart) < len(rightPart) || (len(leftPart) == len(rightPart) && leftPart < rightPart) {
				return -1
			}
			return 1
		}
		if leftNumeric {
			return -1
		}
		if rightNumeric {
			return 1
		}
		if leftPart < rightPart {
			return -1
		}
		return 1
	}
	if len(leftParts) < len(rightParts) {
		return -1
	}
	return 1
}

func isNumeric(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func handleUpdateCheck(runtime localRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		if runtime.updates == nil {
			localSuccess(c, updateStatus{CurrentVersion: buildVersion, Channel: updateChannel(buildVersion), Status: "unavailable"})
			return
		}
		status, _ := runtime.updates.check(c.Request.Context())
		localSuccess(c, status)
	}
}
