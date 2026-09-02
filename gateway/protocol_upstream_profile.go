package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// protocolUpstreamProfile is an operator-owned request policy. It lets one
// protocol operation adapt to a provider without exposing provider selection
// or protected authentication headers to callers.
type protocolUpstreamProfile struct {
	ForwardHeaders []string          `json:"forward_headers,omitempty"`
	SetHeaders     map[string]string `json:"set_headers,omitempty"`
	RemoveHeaders  []string          `json:"remove_headers,omitempty"`
	UserAgent      string            `json:"user_agent,omitempty"`
	Query          string            `json:"query,omitempty"`
}

func channelHasUpstreamProfile(profile protocolUpstreamProfile) bool {
	return len(profile.ForwardHeaders) > 0 || len(profile.SetHeaders) > 0 || len(profile.RemoveHeaders) > 0 || profile.UserAgent != "" || profile.Query != ""
}

func protocolProfileReservedHeader(header string) bool {
	canonical := http.CanonicalHeaderKey(strings.TrimSpace(header))
	lower := strings.ToLower(canonical)
	return canonical == "" || !protocolHeaderPattern.MatchString(canonical) ||
		isHopByHopHeader(canonical) ||
		strings.EqualFold(canonical, "Host") ||
		strings.EqualFold(canonical, "Authorization") ||
		strings.EqualFold(canonical, "Proxy-Authorization") ||
		strings.EqualFold(canonical, "Cookie") ||
		strings.EqualFold(canonical, "X-Api-Key") ||
		strings.EqualFold(canonical, "Content-Length") ||
		strings.Contains(lower, "token") || strings.Contains(lower, "secret") ||
		strings.Contains(lower, "api-key") || strings.Contains(lower, "apikey") || strings.Contains(lower, "api_key")
}

func validateProtocolUpstreamProfiles(definition *protocolDefinition) error {
	if len(definition.UpstreamProfiles) == 0 {
		return nil
	}
	if len(definition.UpstreamProfiles) > 128 {
		return errors.New("upstream_profiles cannot contain more than 128 profiles")
	}
	if definition.SchemaVersion != protocolSchemaVersionV3 {
		return errors.New("upstream_profiles require schema_version 3")
	}
	profileNames := make([]string, 0, len(definition.UpstreamProfiles))
	for name := range definition.UpstreamProfiles {
		profileNames = append(profileNames, name)
	}
	sort.Strings(profileNames)
	for _, name := range profileNames {
		if !protocolOperationIDPattern.MatchString(name) {
			return fmt.Errorf("upstream profile %q must use a safe identifier", name)
		}
		profile := definition.UpstreamProfiles[name]
		if err := normalizeAndValidateProtocolUpstreamProfile(&profile); err != nil {
			return fmt.Errorf("upstream profile %q: %w", name, err)
		}
		definition.UpstreamProfiles[name] = profile
	}
	return nil
}

func normalizeAndValidateProtocolUpstreamProfile(profile *protocolUpstreamProfile) error {
	if len(profile.ForwardHeaders) > 64 || len(profile.SetHeaders) > 64 || len(profile.RemoveHeaders) > 64 {
		return errors.New("header rule lists cannot contain more than 64 entries")
	}
	if profile.UserAgent == "" {
		profile.UserAgent = "inherit"
	}
	switch profile.UserAgent {
	case "inherit", "preserve", "omit", "localrouter", "configured":
	default:
		return errors.New("user_agent must be inherit, preserve, omit, localrouter, or configured")
	}
	if profile.Query == "" {
		profile.Query = "normalized"
	}
	if profile.Query != "normalized" && profile.Query != "preserve-raw" {
		return errors.New("query must be normalized or preserve-raw")
	}

	forward, err := normalizeProtocolProfileHeaderList(profile.ForwardHeaders)
	if err != nil {
		return fmt.Errorf("forward_headers: %w", err)
	}
	remove, err := normalizeProtocolProfileHeaderList(profile.RemoveHeaders)
	if err != nil {
		return fmt.Errorf("remove_headers: %w", err)
	}
	profile.ForwardHeaders = forward
	profile.RemoveHeaders = remove
	for _, headers := range [][]string{profile.ForwardHeaders, profile.RemoveHeaders} {
		for _, header := range headers {
			if strings.EqualFold(header, "User-Agent") {
				return errors.New("User-Agent must be controlled with user_agent, not a header list")
			}
		}
	}

	setHeaders := make(map[string]string, len(profile.SetHeaders))
	setHeaderNames := make([]string, 0, len(profile.SetHeaders))
	for raw := range profile.SetHeaders {
		setHeaderNames = append(setHeaderNames, raw)
	}
	sort.Strings(setHeaderNames)
	for _, raw := range setHeaderNames {
		value := profile.SetHeaders[raw]
		canonical := http.CanonicalHeaderKey(strings.TrimSpace(raw))
		if protocolProfileReservedHeader(canonical) {
			return fmt.Errorf("set_headers contains reserved header %q", raw)
		}
		if len(value) > 8192 || strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("set_headers %q contains an unsafe value", raw)
		}
		if err := validateProtocolProfileTemplateSyntax(value); err != nil {
			return fmt.Errorf("set_headers %q: %w", raw, err)
		}
		if _, duplicate := setHeaders[canonical]; duplicate {
			return fmt.Errorf("set_headers contains duplicate header %q", canonical)
		}
		setHeaders[canonical] = value
	}
	profile.SetHeaders = setHeaders
	for _, header := range profile.RemoveHeaders {
		if _, conflict := setHeaders[header]; conflict {
			return fmt.Errorf("header %q cannot be both set and removed", header)
		}
	}
	_, configuredUserAgent := setHeaders["User-Agent"]
	if profile.UserAgent == "configured" && !configuredUserAgent {
		return errors.New("user_agent configured requires set_headers.User-Agent")
	}
	if profile.UserAgent != "configured" && configuredUserAgent {
		return errors.New("set_headers.User-Agent requires user_agent configured")
	}
	return nil
}

func validateProtocolProfileTemplateSyntax(value string) error {
	withoutKnown := protocolTemplateVariable.ReplaceAllString(value, "")
	if strings.Contains(withoutKnown, "{{") || strings.Contains(withoutKnown, "}}") {
		return errors.New("contains an unsupported template variable")
	}
	return nil
}

func normalizeProtocolProfileHeaderList(headers []string) ([]string, error) {
	seen := make(map[string]bool, len(headers))
	result := make([]string, 0, len(headers))
	for _, raw := range headers {
		canonical := http.CanonicalHeaderKey(strings.TrimSpace(raw))
		if protocolProfileReservedHeader(canonical) {
			return nil, fmt.Errorf("contains reserved header %q", raw)
		}
		if seen[canonical] {
			return nil, fmt.Errorf("contains duplicate header %q", canonical)
		}
		seen[canonical] = true
		result = append(result, canonical)
	}
	sort.Strings(result)
	return result, nil
}

func protocolUpstreamProfileForRoute(definition protocolDefinition, route protocolRoute, targetName string) (string, *protocolUpstreamProfile) {
	name := route.UpstreamProfile
	if targetName != "" {
		if selected := route.UpstreamProfilesByTarget[targetName]; selected != "" {
			name = selected
		}
	}
	if name == "" {
		return "", nil
	}
	profile, exists := definition.UpstreamProfiles[name]
	if !exists {
		return name, nil
	}
	return name, &profile
}

func validateProtocolRouteUpstreamProfiles(route *protocolRoute, definition *protocolDefinition) error {
	if route.UpstreamProfile == "" && len(route.UpstreamProfilesByTarget) == 0 {
		return nil
	}
	if definition.SchemaVersion != protocolSchemaVersionV3 {
		return errors.New("upstream profiles require schema_version 3")
	}
	type profileReference struct {
		location string
		name     string
	}
	references := make([]profileReference, 0, len(route.UpstreamProfilesByTarget)+1)
	if route.UpstreamProfile != "" {
		references = append(references, profileReference{location: "upstream_profile", name: route.UpstreamProfile})
	}
	targetNames := make([]string, 0, len(route.UpstreamProfilesByTarget))
	for targetName := range route.UpstreamProfilesByTarget {
		targetNames = append(targetNames, targetName)
	}
	sort.Strings(targetNames)
	for _, targetName := range targetNames {
		profileName := route.UpstreamProfilesByTarget[targetName]
		if _, exists := definition.ParsedTargets[targetName]; !exists {
			return fmt.Errorf("upstream_profiles_by_target references unknown target %q", targetName)
		}
		references = append(references, profileReference{location: "upstream_profiles_by_target." + targetName, name: profileName})
	}
	for _, reference := range references {
		profile, exists := definition.UpstreamProfiles[reference.name]
		if !exists {
			return fmt.Errorf("%s references unknown upstream profile %q", reference.location, reference.name)
		}
		if profile.Query == "preserve-raw" && (len(route.RequestTransform.Query) > 0 || len(route.RequestTransform.QueryExpr) > 0) {
			return fmt.Errorf("%s uses preserve-raw query and cannot be combined with query transforms", reference.location)
		}
		headerNames := make([]string, 0, len(profile.SetHeaders))
		for header := range profile.SetHeaders {
			headerNames = append(headerNames, header)
		}
		sort.Strings(headerNames)
		for _, header := range headerNames {
			value := profile.SetHeaders[header]
			if err := validateProtocolTemplate(value, route.Path); err != nil {
				return fmt.Errorf("%s set_headers %q: %w", reference.location, header, err)
			}
		}
	}
	return nil
}

func protocolUpstreamRawQuery(profile *protocolUpstreamProfile, original string, query url.Values) string {
	if profile != nil && profile.Query == "preserve-raw" {
		return original
	}
	return query.Encode()
}

func protocolUpstreamHeaders(definition protocolDefinition, route protocolRoute, targetName string, client http.Header, defaults []string, forwardAll bool, params map[string]string, query url.Values, legacyUserAgent string) http.Header {
	headers := make(http.Header)
	if forwardAll {
		for header, values := range client {
			if protocolProfileReservedHeader(header) {
				continue
			}
			headers[http.CanonicalHeaderKey(header)] = append([]string(nil), values...)
		}
	}
	_, profile := protocolUpstreamProfileForRoute(definition, route, targetName)
	forward := append(append([]string(nil), defaults...), definition.ForwardHeaders...)
	if profile != nil {
		forward = append(forward, profile.ForwardHeaders...)
	}
	for _, header := range normalizeHeaderList(forward) {
		if protocolProfileReservedHeader(header) {
			continue
		}
		if values := client.Values(header); len(values) > 0 {
			headers[header] = append([]string(nil), values...)
		}
	}
	if profile != nil {
		for header, value := range profile.SetHeaders {
			headers.Set(header, renderProtocolTemplate(value, params, query))
		}
		for _, header := range profile.RemoveHeaders {
			headers.Del(header)
		}
		switch profile.UserAgent {
		case "inherit":
			switch legacyUserAgent {
			case "localrouter":
				headers.Set("User-Agent", "LocalRouter/2 protocol-pack")
			case "preserve":
				if values := client.Values("User-Agent"); len(values) > 0 {
					headers["User-Agent"] = append([]string(nil), values...)
				}
			}
		case "preserve":
			if values := client.Values("User-Agent"); len(values) > 0 {
				headers["User-Agent"] = append([]string(nil), values...)
			} else {
				headers["User-Agent"] = []string{}
			}
		case "omit":
			headers["User-Agent"] = []string{}
		case "localrouter":
			headers.Set("User-Agent", "LocalRouter/2 protocol-pack")
		case "configured":
			// Already materialized from SetHeaders above.
		}
	} else {
		switch legacyUserAgent {
		case "localrouter":
			headers.Set("User-Agent", "LocalRouter/2 protocol-pack")
		case "preserve":
			if values := client.Values("User-Agent"); len(values) > 0 {
				headers["User-Agent"] = append([]string(nil), values...)
			}
		}
	}
	return headers
}
