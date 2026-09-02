package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

type protocolHMACSecret struct {
	Key   string `json:"key"`
	KeyID string `json:"key_id,omitempty"`
}

type protocolAWSCredentials struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	SessionToken    string `json:"session_token,omitempty"`
}

type protocolOAuthSecret struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

type protocolOAuthResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    any    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

func validateProtocolHMAC(config protocolHMACConfig) error {
	if config.Header == "" || !protocolHeaderPattern.MatchString(http.CanonicalHeaderKey(config.Header)) {
		return errors.New("hmac header is required and must be safe")
	}
	if config.TimestampHeader != "" && !protocolHeaderPattern.MatchString(http.CanonicalHeaderKey(config.TimestampHeader)) {
		return errors.New("hmac timestamp_header is unsafe")
	}
	if config.KeyIDHeader != "" && !protocolHeaderPattern.MatchString(http.CanonicalHeaderKey(config.KeyIDHeader)) {
		return errors.New("hmac key_id_header is unsafe")
	}
	if config.Algorithm != "" && config.Algorithm != "hmac-sha256" {
		return errors.New("hmac algorithm must be hmac-sha256")
	}
	if config.Encoding != "" && config.Encoding != "hex" && config.Encoding != "base64" && config.Encoding != "base64url" {
		return errors.New("hmac encoding must be hex, base64, or base64url")
	}
	if config.MessageTemplate == "" || len(config.MessageTemplate) > 8192 {
		return errors.New("hmac message_template is required and bounded")
	}
	for _, token := range protocolHMACTemplateTokens(config.MessageTemplate) {
		switch token {
		case "method", "path", "query", "body_sha256", "timestamp":
		default:
			return fmt.Errorf("hmac message_template contains unsupported token %q", token)
		}
	}
	return nil
}

func protocolHMACTemplateTokens(value string) []string {
	result := make([]string, 0)
	for {
		start := strings.Index(value, "{{")
		if start < 0 {
			return result
		}
		end := strings.Index(value[start+2:], "}}")
		if end < 0 {
			return append(result, "invalid")
		}
		result = append(result, value[start+2:start+2+end])
		value = value[start+2+end+2:]
	}
}

func validateProtocolOAuth2(config protocolOAuth2Config) error {
	parsed, err := url.Parse(config.TokenURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopbackHostname(parsed.Hostname()))) {
		return errors.New("oauth2 token_url must use https or loopback http")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return errors.New("oauth2 token_url cannot contain credentials or fragment")
	}
	grant := config.GrantType
	if grant == "" {
		grant = "client_credentials"
	}
	if grant != "client_credentials" && grant != "refresh_token" {
		return errors.New("oauth2 grant_type must be client_credentials or refresh_token")
	}
	if config.ClientAuth != "" && config.ClientAuth != "basic" && config.ClientAuth != "body" {
		return errors.New("oauth2 client_auth must be basic or body")
	}
	if config.ExpirySkewSecs < 0 || config.ExpirySkewSecs > 600 {
		return errors.New("oauth2 expiry_skew_seconds must be between 0 and 600")
	}
	return nil
}

func isLoopbackHostname(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func (registry *protocolRegistry) injectProtocolCredential(request *http.Request, definition protocolDefinition, acquired acquiredProtocolCredential, body []byte) error {
	auth := definition.Auth
	secret := acquired.Credential.Secret
	value := secret
	if auth.ValueTemplate != "" {
		value = strings.ReplaceAll(auth.ValueTemplate, "{{secret}}", secret)
	}
	switch auth.Type {
	case "none":
		return nil
	case "bearer":
		request.Header.Set("Authorization", "Bearer "+value)
	case "header", "cookie":
		request.Header.Set(auth.Header, value)
	case "dual":
		request.Header.Set("Authorization", "Bearer "+value)
		request.Header.Set(auth.Header, value)
	case "hmac-sha256":
		return injectProtocolHMAC(request, *auth.HMAC, secret, body)
	case "aws-sigv4":
		return injectProtocolAWSSigV4(request, *auth.AWSSigV4, secret, body)
	case "oauth2":
		token, err := registry.protocolOAuthAccessToken(request.Context(), definition, acquired)
		if err != nil {
			return err
		}
		tokenType := token.TokenType
		if tokenType == "" {
			tokenType = "Bearer"
		}
		request.Header.Set("Authorization", tokenType+" "+token.AccessToken)
	default:
		return errors.New("unsupported auth type")
	}
	return nil
}

func injectProtocolHMAC(request *http.Request, config protocolHMACConfig, rawSecret string, body []byte) error {
	secret := protocolHMACSecret{Key: rawSecret}
	if strings.HasPrefix(strings.TrimSpace(rawSecret), "{") {
		if err := json.Unmarshal([]byte(rawSecret), &secret); err != nil {
			return errors.New("invalid HMAC secret document")
		}
	}
	if secret.Key == "" {
		return errors.New("HMAC key is empty")
	}
	timestamp := time.Now().UTC().Format(time.RFC3339)
	bodyDigest := sha256.Sum256(body)
	message := config.MessageTemplate
	replacements := map[string]string{
		"method":      request.Method,
		"path":        request.URL.EscapedPath(),
		"query":       request.URL.RawQuery,
		"body_sha256": hex.EncodeToString(bodyDigest[:]),
		"timestamp":   timestamp,
	}
	for key, replacement := range replacements {
		message = strings.ReplaceAll(message, "{{"+key+"}}", replacement)
	}
	mac := hmac.New(sha256.New, []byte(secret.Key))
	_, _ = mac.Write([]byte(message))
	signature := mac.Sum(nil)
	encoded := hex.EncodeToString(signature)
	switch config.Encoding {
	case "base64":
		encoded = base64.StdEncoding.EncodeToString(signature)
	case "base64url":
		encoded = base64.RawURLEncoding.EncodeToString(signature)
	}
	request.Header.Set(config.Header, encoded)
	if config.TimestampHeader != "" {
		request.Header.Set(config.TimestampHeader, timestamp)
	}
	if config.KeyIDHeader != "" && secret.KeyID != "" {
		request.Header.Set(config.KeyIDHeader, secret.KeyID)
	}
	return nil
}

func injectProtocolAWSSigV4(request *http.Request, config protocolAWSSigV4Config, rawSecret string, body []byte) error {
	var secret protocolAWSCredentials
	decoder := json.NewDecoder(strings.NewReader(rawSecret))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&secret); err != nil || secret.AccessKeyID == "" || secret.SecretAccessKey == "" {
		return errors.New("invalid AWS SigV4 credential document")
	}
	payloadDigest := sha256.Sum256(body)
	credentials := aws.Credentials{AccessKeyID: secret.AccessKeyID, SecretAccessKey: secret.SecretAccessKey, SessionToken: secret.SessionToken}
	return v4.NewSigner().SignHTTP(request.Context(), credentials, request, hex.EncodeToString(payloadDigest[:]), config.Service, config.Region, time.Now().UTC())
}

func (registry *protocolRegistry) protocolOAuthAccessToken(ctx context.Context, definition protocolDefinition, acquired acquiredProtocolCredential) (protocolOAuthToken, error) {
	cacheKey := definition.ID + ":" + acquired.Credential.ID
	registry.oauthMu.Lock()
	defer registry.oauthMu.Unlock()
	if cached := registry.oauthTokens[cacheKey]; cached.AccessToken != "" && time.Now().Before(cached.ExpiresAt) {
		return cached, nil
	}
	var secret protocolOAuthSecret
	decoder := json.NewDecoder(strings.NewReader(acquired.Credential.Secret))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&secret); err != nil || secret.ClientID == "" || secret.ClientSecret == "" {
		return protocolOAuthToken{}, errors.New("invalid OAuth2 credential document")
	}
	config := *definition.Auth.OAuth2
	grant := config.GrantType
	if grant == "" {
		grant = "client_credentials"
	}
	form := url.Values{"grant_type": []string{grant}}
	if len(config.Scopes) > 0 {
		form.Set("scope", strings.Join(config.Scopes, " "))
	}
	if config.Audience != "" {
		form.Set("audience", config.Audience)
	}
	for key, value := range config.Extra {
		form.Set(key, value)
	}
	if grant == "refresh_token" {
		if secret.RefreshToken == "" {
			return protocolOAuthToken{}, errors.New("OAuth2 refresh_token grant requires a refresh token")
		}
		form.Set("refresh_token", secret.RefreshToken)
	}
	if config.ClientAuth == "body" {
		form.Set("client_id", secret.ClientID)
		form.Set("client_secret", secret.ClientSecret)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, config.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return protocolOAuthToken{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	if config.ClientAuth != "body" {
		request.SetBasicAuth(secret.ClientID, secret.ClientSecret)
	}
	response, err := registry.client.Do(request)
	if err != nil {
		return protocolOAuthToken{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		return protocolOAuthToken{}, fmt.Errorf("OAuth2 token endpoint returned HTTP %d", response.StatusCode)
	}
	var tokenResponse protocolOAuthResponse
	if err := json.Unmarshal(body, &tokenResponse); err != nil || tokenResponse.AccessToken == "" {
		return protocolOAuthToken{}, errors.New("OAuth2 token endpoint returned an invalid token document")
	}
	expiresIn := oauthExpiresIn(tokenResponse.ExpiresIn)
	skew := config.ExpirySkewSecs
	if skew == 0 {
		skew = 30
	}
	if expiresIn <= skew {
		expiresIn = skew + 1
	}
	token := protocolOAuthToken{AccessToken: tokenResponse.AccessToken, TokenType: tokenResponse.TokenType, ExpiresAt: time.Now().Add(time.Duration(expiresIn-skew) * time.Second)}
	registry.oauthTokens[cacheKey] = token
	return token, nil
}

func oauthExpiresIn(value any) int {
	switch value := value.(type) {
	case float64:
		return int(value)
	case string:
		parsed, _ := strconv.Atoi(value)
		return parsed
	case json.Number:
		parsed, _ := strconv.Atoi(value.String())
		return parsed
	default:
		return 3600
	}
}

func (registry *protocolRegistry) invalidateOAuthToken(definition protocolDefinition, acquired acquiredProtocolCredential) {
	registry.oauthMu.Lock()
	delete(registry.oauthTokens, definition.ID+":"+acquired.Credential.ID)
	registry.oauthMu.Unlock()
}
