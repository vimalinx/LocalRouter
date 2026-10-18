package main

import (
	"errors"
	"net/http"
	"strings"
)

var workflowScopeActions = []string{"create", "get", "list", "advance", "cancel"}

// Selectors are shared by enrollment, the published catalogue and admission.
// Match the longest exact published Pack prefix, never split an operation ID.
func splitCapabilityKey(packs []string, key string) (string, string, bool) {
	pack := ""
	for _, candidate := range packs {
		if strings.HasPrefix(key, candidate+".") && len(candidate) > len(pack) {
			pack = candidate
		}
	}
	if pack == "" {
		return "", "", false
	}
	return pack, strings.TrimPrefix(key, pack+"."), true
}

func compatibilityOperation(method, path string) string {
	return strings.ToLower(method) + "." + strings.NewReplacer("/", ".", "{", "", "}", "").Replace(strings.Trim(path, "/"))
}

func (registry *localChannelProfileRegistry) requestCapability(method, path string) (string, string, bool) {
	if registry == nil {
		return "", "", false
	}
	profile, ok := registry.matchRequestPath(path)
	if !ok {
		return "", "", false
	}
	for _, route := range localRelayRouteDefinitions() {
		if !containsString(route.Methods, method) {
			continue
		}
		pattern := strings.ReplaceAll(route.Path, "{path}", "*")
		if _, matched := matchProtocolPath(pattern, path); matched {
			return profile.Key, compatibilityOperation(method, route.Path), true
		}
	}
	return "", "", false
}

func protocolScopeDependencies(def protocolDefinition, operation string) ([]string, bool) {
	if route, ok := operationRoute(def, operation); ok {
		deps := []string{}
		published := publishProtocolRoute(def.ID, "/p/"+def.ID, def.Routes, route)
		for _, dynamic := range published.DynamicInputs {
			if dynamic.SourceOperationKey != "" {
				pack, op, found := splitCapabilityKey([]string{def.ID}, dynamic.SourceOperationKey)
				if found && pack == def.ID {
					deps = append(deps, op)
				}
			}
		}
		return deps, true
	}
	for _, workflow := range def.Workflows {
		for _, action := range workflowScopeActions {
			if operation == "workflow."+workflow.ID+"."+action {
				deps := serviceWorkflowOperations(workflow)
				for _, companion := range workflowScopeActions {
					deps = append(deps, "workflow."+workflow.ID+"."+companion)
				}
				return deps, true
			}
		}
	}
	return nil, false
}

func (runtime localRuntime) expandIdentityScope(policy *localTokenPolicy) error {
	queue := append([]string(nil), policy.Operations...)
	seen := map[string]bool{}
	surfaces := append([]string(nil), policy.Surfaces...)
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		if seen[key] {
			continue
		}
		seen[key] = true
		if len(seen) > 200 {
			return errors.New("expanded operation scope exceeds 200 selectors")
		}
		pack, op, ok := splitCapabilityKey(policy.Packs, key)
		if !ok {
			return errors.New("each operation must belong to a selected Pack")
		}
		if def, found := runtime.protocols.get(pack); found {
			deps, valid := protocolScopeDependencies(def, op)
			if !valid {
				return &unpublishedIdentityScope{Pack: pack, Operation: op}
			}
			surfaces = append(surfaces, "p", "mcp")
			if strings.HasPrefix(op, "workflow.") {
				surfaces = append(surfaces, "w")
			}
			for _, dep := range deps {
				queue = append(queue, pack+"."+dep)
			}
			continue
		}
		found := false
		for _, view := range runtime.channelProfiles.compatibilityViews(nil) {
			if view.ID != pack {
				continue
			}
			for _, route := range view.Routes {
				if route.OperationID != op || route.Status != "available" {
					continue
				}
				found = true
				surface := strings.Split(strings.Trim(route.Path, "/"), "/")[0]
				surfaces = append(surfaces, surface)
				// Model inventory is required before selecting a dynamic model.
				for _, metadata := range view.Routes {
					if metadata.Path == "/"+surface+"/models" && containsString(metadata.Methods, http.MethodGet) {
						queue = append(queue, pack+"."+metadata.OperationID)
					}
				}
			}
		}
		if !found {
			return &unpublishedIdentityScope{Pack: pack, Operation: op}
		}
	}
	policy.Operations = policy.Operations[:0]
	for key := range seen {
		policy.Operations = append(policy.Operations, key)
	}
	policy.Operations = normalizePolicyValues(policy.Operations)
	policy.Surfaces = normalizePolicyValues(surfaces)
	return nil
}
