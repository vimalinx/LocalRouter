package main

// In ordinary mode an Agent may connect a brand-new public HTTPS service for
// itself without a human click: it supplies the credential file and the
// published prices, its budget limits the spend, and the human can undo the
// connection at any time, and the Agent can undo its own connection. Anything that changes an existing service, targets
// a private network, grants another Token or lacks prices waits for review.
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func (runtime localRuntime) autoApplyServices() bool {
	settings, ok := runtime.budgetSettings()
	if !ok || !settings.AutoApplyServices {
		return false
	}
	strict, err := runtime.strictMode()
	return err == nil && !strict
}

func publicHTTPSTarget(raw string) bool {
	target, err := url.Parse(raw)
	if err != nil || target.Scheme != "https" {
		return false
	}
	host := strings.ToLower(target.Hostname())
	if host == "" || net.ParseIP(host) != nil || !strings.Contains(host, ".") {
		return false
	}
	for _, suffix := range []string{".localhost", ".local", ".lan", ".internal", ".home.arpa", ".test", ".invalid", ".example"} {
		if strings.HasSuffix(host, suffix) {
			return false
		}
	}
	return true
}

func (hub *serviceWorkspace) autoApplyBlocker(owner localToken, proposal serviceProposal) string {
	if !hub.runtime.autoApplyServices() {
		return "automatic service connection is off or strict mode is on"
	}
	if policy, exists := hub.policies.policyFor(owner.ID); !exists || !budgetEnabled(policy) {
		return "only an Agent identity with a trial or formal budget can connect services automatically"
	}
	if proposal.MaintainerID != 0 || (proposal.GrantTokenID != 0 && proposal.GrantTokenID != owner.ID) {
		return "maintenance delegation or grants to another Token need human review"
	}
	switch proposal.Kind {
	case "bundle":
		return ""
	case "connection":
	default:
		return "only connection and bundle proposals apply automatically"
	}
	definition := proposal.Connection.Definition
	if _, exists := hub.registry.get(definition.ID); exists && !hub.ownsService(definition.ID, owner) {
		return "changing a service the human or another Agent connected needs human review"
	}
	if !publicHTTPSTarget(definition.BaseURL) {
		return "only public HTTPS hosts apply automatically; private, loopback and IP targets need human review"
	}
	if definition.Pricing == nil || len(definition.Pricing.Entries) == 0 {
		return "add definition.pricing entries with the provider's published prices and source_url to apply automatically"
	}
	if definition.Auth.Type != "none" {
		if _, err := hub.registry.readSecret(definition.Auth); err != nil {
			return "supply the service credential with lr setup prepare --credential-file to apply automatically"
		}
	}
	return ""
}

// An Agent owns a live service it connected automatically, as long as no other
// Token is granted it. It may then repair that service without review.
func (hub *serviceWorkspace) ownsService(pack string, owner localToken) bool {
	var latest *serviceProposal
	for _, item := range hub.proposals(0) {
		if item.State == "applied" && item.Connection != nil && item.Connection.Definition.ID == pack && (latest == nil || item.UpdatedAt.After(latest.UpdatedAt)) {
			current := item
			latest = &current
		}
	}
	return latest != nil && latest.OwnerTokenID == owner.ID && latest.DecidedBy == "auto:"+owner.AgentCode && !hub.packGrantedToOthers(pack, owner.ID)
}

// The Agent's credential is stored at preparation under the proposal's own
// binding, so the human approves without typing it. Existing bindings are
// never overwritten.
func (hub *serviceWorkspace) provisionCredential(proposal *serviceProposal, credential string) (string, error) {
	if credential == "" {
		return "", nil
	}
	if proposal.Connection == nil || proposal.Connection.Definition.Auth.Type == "none" {
		return "", errors.New("credential applies only to an authenticated service connection")
	}
	if len(credential) > 32<<10 || strings.ContainsAny(credential, "\r\n\x00") {
		return "", errors.New("credential must be one line up to 32 KiB")
	}
	path, err := hub.registry.secretPath(proposal.Connection.Definition.Auth.SecretFile)
	if err != nil {
		return "", err
	}
	if exists, err := inspectPrivateRegularFile(path, "service credential"); err != nil {
		return "", err
	} else if exists {
		return "retained", nil
	}
	if err = ensurePrivateDirectory(filepath.Dir(path)); err == nil {
		err = writeMaintenanceFileAtomic(path, []byte(credential+"\n"))
	}
	if err != nil {
		return "", err
	}
	proposal.CredentialProvisioned = true
	id := proposal.ID
	return "stored", hub.update(func(doc *serviceWorkspaceDocument) error {
		item := doc.Proposals[id]
		item.CredentialProvisioned = true
		doc.Proposals[id] = item
		return nil
	})
}

func (hub *serviceWorkspace) discardProvisionedCredential(proposal serviceProposal) {
	if !proposal.CredentialProvisioned || proposal.Connection == nil {
		return
	}
	if path, err := hub.registry.secretPath(proposal.Connection.Definition.Auth.SecretFile); err == nil {
		_ = os.Remove(path)
	}
}

// prepareService is shared by the HTTP and MCP preparation entry points.
func (hub *serviceWorkspace) prepareService(owner localToken, input serviceProposalInput) (gin.H, error) {
	runtime := hub.runtime
	credential := input.Credential
	input.Credential = ""
	proposal, err := hub.prepare(owner, input)
	if err != nil {
		return nil, err
	}
	stored, err := hub.provisionCredential(&proposal, credential)
	if err != nil {
		return nil, fmt.Errorf("proposal %s is prepared but the credential was not stored: %w", proposal.ID, err)
	}
	auto := gin.H{"applied": false, "credential": stored}
	if blocker := hub.autoApplyBlocker(owner, proposal); blocker != "" {
		auto["reason"] = blocker
		runtime.humanEvent(serviceProposalEvent(owner, proposal, blocker))
	} else {
		applied, err := hub.decideAs(proposal.ID, proposal.Digest, "approve", "", 0, "auto:"+owner.AgentCode)
		if err != nil {
			auto["reason"] = "automatic apply failed: " + err.Error()
		} else {
			proposal = applied
			auto["applied"] = true
			hub.announceAppliedService(owner, proposal)
		}
	}
	view := serviceProposalView(proposal)
	view["auto_apply"] = auto
	if auto["applied"] == true {
		view["next_action"] = "the service is live for this Agent; preflight, then call within your budget. The human may undo it."
	}
	return view, nil
}

func proposalService(proposal serviceProposal) string {
	switch {
	case proposal.Connection != nil:
		return proposal.Connection.Definition.ID
	case proposal.Bundle != nil:
		return proposal.Bundle.ID
	case proposal.Template != nil:
		return proposal.Template.ID
	}
	return proposal.ID
}

func serviceProposalEvent(owner localToken, proposal serviceProposal, why string) gin.H {
	body := proposal.Reason
	if why != "" {
		body += "\n需要你处理：" + why
	}
	if proposal.Connection != nil {
		body = "目标：" + proposal.Connection.Definition.BaseURL + "\n" + body
	}
	return gin.H{"event": "service_proposal", "id": proposal.ID, "agent_code": owner.AgentCode, "agent_name": owner.AgentName,
		"title": "LocalRouter · " + owner.AgentName + " 申请接入 " + proposalService(proposal), "body": body}
}

func (hub *serviceWorkspace) announceAppliedService(owner localToken, proposal serviceProposal) {
	runtime := hub.runtime
	service, host := "", ""
	if proposal.Connection != nil {
		service = proposal.Connection.Definition.ID
		if target, err := url.Parse(proposal.Connection.Definition.BaseURL); err == nil {
			host = target.Hostname()
		}
	} else if proposal.Bundle != nil {
		service = proposal.Bundle.ID
	}
	title := fmt.Sprintf("LocalRouter · %s 接入了 %s", owner.AgentName, service)
	body := proposal.Reason
	if host != "" {
		body = "目标：" + host + "\n" + body
	}
	runtime.humanEvent(gin.H{"event": "service_applied", "id": proposal.ID, "agent_code": owner.AgentCode, "agent_name": owner.AgentName, "service": service, "host": host, "title": title, "body": body})
	if runtime.policies == nil || runtime.policies.budgets == nil {
		return
	}
	desktopNotify(runtime, runtime.policies.budgets.notifySlots, "normal", title, body, [][2]string{{"undo", "撤销"}, {"open", "打开控制台"}}, func(action string) {
		switch action {
		case "undo":
			_, _ = hub.undoApplied(proposal.ID, "desktop", 0)
		case "open":
			openConsole(runtime, "setup")
		}
	})
}

// undoApplied reverses an applied proposal: the Pack files it added are
// removed through a reviewed draft, its grant is withdrawn and a credential the
// Agent supplied is deleted. History stays. An Agent (actor > 0) may undo only
// its own proposal, and only while no other Token is granted that service.
func (hub *serviceWorkspace) undoApplied(id, by string, actor int) (serviceProposal, error) {
	hub.applyMu.Lock()
	defer hub.applyMu.Unlock()
	proposal, exists := hub.proposal(id, 0)
	if !exists || (actor > 0 && proposal.OwnerTokenID != actor) {
		return proposal, errors.New("unknown proposal; an Agent can undo only its own proposals")
	}
	if proposal.State != "applied" {
		return proposal, fmt.Errorf("proposal is %s; only applied proposals can be undone", proposal.State)
	}
	if proposal.MaintainerID != 0 || (proposal.GrantTokenID != 0 && proposal.GrantTokenID != proposal.OwnerTokenID) {
		return proposal, errors.New("needs_approval: this proposal delegated maintenance or granted another Token; ask the human to undo it")
	}
	if proposal.Connection != nil {
		pack := proposal.Connection.Definition.ID
		added := []string{}
		if proposal.Impact != nil {
			for _, file := range proposal.Impact.Files {
				if file.ProtocolID == pack && file.Change == "added" {
					added = append(added, file.Path)
				}
			}
		}
		if len(added) == 0 {
			return proposal, errors.New("needs_approval: this proposal changed an existing service; ask the human to edit it")
		}
		if actor > 0 && hub.packGrantedToOthers(pack, proposal.OwnerTokenID) {
			return proposal, errors.New("needs_approval: other Agents are granted this service; ask the human to remove it")
		}
		if _, live := hub.registry.get(pack); live {
			if err := hub.removeDraftFiles(added); err != nil {
				return proposal, err
			}
		}
	}
	err := hub.update(func(doc *serviceWorkspaceDocument) error {
		item := doc.Proposals[id]
		if item.Bundle != nil && item.GrantTokenID > 0 {
			kept := []serviceBundleGrant{}
			for _, grant := range doc.Grants[item.GrantTokenID] {
				if grant.Revision != item.Bundle.Revision {
					kept = append(kept, grant)
				}
			}
			doc.Grants[item.GrantTokenID] = kept
		}
		item.State, item.UpdatedAt, item.UndoneBy = "undone", time.Now().UTC(), by
		doc.Proposals[id] = item
		return nil
	})
	if err == nil {
		hub.discardProvisionedCredential(proposal)
		hub.runtime.humanEvent(gin.H{"event": "service_undone", "id": id})
	}
	item, _ := hub.proposal(id, 0)
	return item, err
}

func (hub *serviceWorkspace) packGrantedToOthers(pack string, owner int) bool {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	for tokenID, grants := range hub.doc.Grants {
		if tokenID == owner {
			continue
		}
		for _, grant := range grants {
			for _, member := range hub.doc.Bundles[grant.Revision].Members {
				if member.Pack == pack {
					return true
				}
			}
		}
	}
	return false
}

// removeDraftFiles deletes exactly the files a proposal added, then applies the
// reviewed draft so validation, digests and rollback stay with LocalRouter.
func (hub *serviceWorkspace) removeDraftFiles(paths []string) error {
	draftID := "undo-" + serviceID("")[:20]
	if _, failure := hub.registry.maintenanceOpenDraft(draftID); failure != nil {
		return errors.New(failure.Message)
	}
	hub.registry.lifecycleMu.Lock()
	root, exists := hub.registry.protocolDraftRoot(draftID)
	if !exists {
		hub.registry.lifecycleMu.Unlock()
		return errors.New("undo draft is missing")
	}
	for _, relative := range paths {
		path := filepath.Join(root, filepath.Clean(relative))
		if !strings.HasPrefix(path, root+string(filepath.Separator)) {
			hub.registry.lifecycleMu.Unlock()
			return errors.New("invalid draft path")
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			hub.registry.lifecycleMu.Unlock()
			return err
		}
	}
	hub.registry.lifecycleMu.Unlock()
	result, failure := hub.registry.maintenanceReviewDraft(draftID)
	if failure != nil {
		return errors.New(failure.Message)
	}
	data, _ := json.Marshal(result)
	var review struct {
		Digest string `json:"digest"`
	}
	if err := json.Unmarshal(data, &review); err != nil {
		return err
	}
	plan, failure := hub.registry.maintenancePlanDraft(draftID, review.Digest)
	if failure != nil {
		return errors.New(failure.Message)
	}
	if _, failure = hub.registry.applyPlannedProtocolDigest(plan.Digest); failure != nil {
		return fmt.Errorf("%s: %s", failure.Code, failure.Message)
	}
	return nil
}

// humanEvent hands a request that needs the human, or a decision on one, to an
// optional operator-configured command (for example a Todo integration). The
// event is JSON on stdin; failures never affect the request path.
func (runtime localRuntime) humanEvent(event gin.H) {
	hook := runtime.config.HumanRequestHook
	if hook == "" {
		return
	}
	event["console_url"] = consoleURL(runtime, "")
	raw, _ := json.Marshal(event)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, hook)
		command.Stdin = bytes.NewReader(raw)
		_ = command.Run()
	}()
}

func consoleURL(runtime localRuntime, page string) string {
	return fmt.Sprintf("http://%s:%d/#%s", runtime.config.Host, runtime.config.Port, page)
}

func openConsole(runtime localRuntime, page string) {
	_ = exec.Command("xdg-open", consoleURL(runtime, page)).Start()
}

// desktopNotify shows an actionable notification when enabled. Without a
// display, when all slots are busy or on timeout nothing happens and the
// console stays authoritative.
func desktopNotify(runtime localRuntime, slots chan struct{}, urgency, title, body string, actions [][2]string, handle func(string)) {
	if !runtime.config.DesktopNotifications || slots == nil {
		return
	}
	select {
	case slots <- struct{}{}:
	default:
		return
	}
	go func() {
		defer func() { <-slots }()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		arguments := []string{"--app-name=LocalRouter", "--urgency=" + urgency, "--wait"}
		for _, action := range actions {
			arguments = append(arguments, "--action="+action[0]+"="+action[1])
		}
		output, err := exec.CommandContext(ctx, "notify-send", append(arguments, title, body)...).Output()
		if err == nil {
			handle(strings.TrimSpace(string(output)))
		}
	}()
}
