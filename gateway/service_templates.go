package main

import (
	"embed"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strings"
)

// Templates are data, not provider-specific Go branches. Built-ins demonstrate
// wire patterns only and never install a supplier or initiate a network call.
//
//go:embed service-templates/*.json
var serviceTemplateFiles embed.FS

type serviceTemplate struct {
	ID               string             `json:"id"`
	Version          string             `json:"version"`
	Name             string             `json:"name"`
	Summary          string             `json:"summary"`
	SourceURL        string             `json:"source_url,omitempty"`
	MaintenanceGuide string             `json:"maintenance_guide"`
	Checks           []string           `json:"checks"`
	Example          protocolDefinition `json:"example"`
	Digest           string             `json:"digest,omitempty"`
}

func builtinServiceTemplates() []serviceTemplate {
	entries, _ := serviceTemplateFiles.ReadDir("service-templates")
	items := []serviceTemplate{}
	for _, entry := range entries {
		data, err := serviceTemplateFiles.ReadFile("service-templates/" + entry.Name())
		var template serviceTemplate
		if err == nil && json.Unmarshal(data, &template) == nil {
			template.Digest = serviceDigest(template)
			items = append(items, template)
		}
	}
	return items
}

func (hub *serviceWorkspace) templates() []serviceTemplate {
	items := builtinServiceTemplates()
	hub.mu.RLock()
	for _, template := range hub.doc.Templates {
		items = append(items, template)
	}
	hub.mu.RUnlock()
	sort.Slice(items, func(i, j int) bool {
		if items[i].ID == items[j].ID {
			return items[i].Version < items[j].Version
		}
		return items[i].ID < items[j].ID
	})
	return items
}

func (hub *serviceWorkspace) template(id, version string) (serviceTemplate, bool) {
	for _, template := range hub.templates() {
		if template.ID == id && template.Version == version {
			return template, true
		}
	}
	return serviceTemplate{}, false
}

func (hub *serviceWorkspace) validateTemplate(template *serviceTemplate) error {
	if !protocolIDPattern.MatchString(template.ID) || len(template.Version) == 0 || len(template.Version) > 40 || strings.ContainsAny(template.Version, "/\\\r\n") || strings.TrimSpace(template.Name) == "" || strings.TrimSpace(template.MaintenanceGuide) == "" {
		return errors.New("template requires id, version, name and maintenance_guide")
	}
	if _, exists := hub.template(template.ID, template.Version); exists {
		return errors.New("template versions are immutable; choose a new version")
	}
	if template.Example.Auth.SecretFile != "" || template.Example.Pool != nil || template.Example.Auth.Type != "none" || len(template.Example.Targets) != 0 || template.Example.BaseURL != "https://service.example.invalid" {
		return errors.New("shareable templates must use the placeholder base URL, auth none, and no private locators or pools")
	}
	if err := hub.registry.validate(&template.Example); err != nil {
		return err
	}
	template.Digest = ""
	template.Digest = serviceDigest(template)
	return nil
}

func validateServiceSource(raw string) error {
	if raw == "" {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("source_url must be a documentation HTTP URL without credentials, query or fragment")
	}
	return nil
}
