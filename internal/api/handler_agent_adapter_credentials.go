package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/hecate/agent-runtime/internal/agentadapters"
	"github.com/hecate/agent-runtime/internal/controlplane"
)

const claudeCodeOAuthTokenName = "CLAUDE_CODE_OAUTH_TOKEN"

func (h *Handler) HandleSetAgentAdapterCredential(w http.ResponseWriter, r *http.Request) {
	if !h.requireSettings(w, r) {
		return
	}
	if h.secretCipher == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "control plane secret storage is not configured")
		return
	}
	adapterID := strings.TrimSpace(r.PathValue("id"))
	if _, ok := agentadapters.FindAdapter(adapterID); !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "adapter not found")
		return
	}
	var req AgentAdapterCredentialSetRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name := normalizeAgentAdapterCredentialName(adapterID, req.Name)
	if err := validateAgentAdapterCredentialName(adapterID, name); err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	value := strings.TrimSpace(req.Value)
	if value == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "credential value is required")
		return
	}
	if adapterID == "claude_code" && name == claudeCodeOAuthTokenName {
		if err := validateClaudeCodeOAuthToken(value); err != nil {
			WriteErrorDetails(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error(), ErrorDetails{
				UserMessage:    "That does not look like a Claude Code setup token, so Hecate did not save it.",
				OperatorAction: "Run claude setup-token again, copy the token printed in Terminal, paste it here, and retry.",
				Fields: map[string]any{
					"adapter_id": adapterID,
					"name":       name,
				},
			})
			return
		}
		result := h.probeAgentAdapter(r.Context(), adapterID, []string{name + "=" + value})
		if result.Status != agentadapters.ProbeStatusReady {
			message := firstNonEmptyString(result.Hint, result.Error, result.Stderr, "Claude Code rejected this token")
			WriteErrorDetails(w, http.StatusConflict, errCodeConflict, message, ErrorDetails{
				UserMessage:    "Claude Code could not connect with that token, so Hecate did not save it.",
				OperatorAction: "Run claude setup-token again, copy the token printed in Terminal, paste it here, and retry.",
				Fields: map[string]any{
					"adapter_id": adapterID,
					"status":     result.Status,
					"stage":      result.Stage,
					"hint":       result.Hint,
					"error":      result.Error,
				},
			})
			return
		}
	}
	encrypted, err := h.secretCipher.Encrypt(value)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, fmt.Sprintf("encrypt adapter credential: %v", err))
		return
	}
	credential, err := h.controlPlane.UpsertAgentAdapterCredential(controlplane.WithActor(r.Context(), settingsActor(r)), controlplane.AgentAdapterCredential{
		AdapterID:      adapterID,
		Name:           name,
		ValueEncrypted: encrypted,
		ValuePreview:   previewAdapterCredential(value),
	})
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, AgentAdapterCredentialResponse{
		Object: "agent_adapter_credential",
		Data: AgentAdapterCredentialResponseItem{
			AdapterID:  credential.AdapterID,
			Name:       credential.Name,
			Configured: true,
			Preview:    credential.ValuePreview,
		},
	})
}

func validateClaudeCodeOAuthToken(value string) error {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "sk-") || len(value) < 20 || strings.ContainsAny(value, " \t\r\n") {
		return fmt.Errorf("Claude Code setup tokens start with sk- and are printed by `claude setup-token`")
	}
	return nil
}

func (h *Handler) HandleDeleteAgentAdapterCredential(w http.ResponseWriter, r *http.Request) {
	if !h.requireSettings(w, r) {
		return
	}
	adapterID := strings.TrimSpace(r.PathValue("id"))
	if _, ok := agentadapters.FindAdapter(adapterID); !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "adapter not found")
		return
	}
	name := normalizeAgentAdapterCredentialName(adapterID, r.PathValue("name"))
	if err := validateAgentAdapterCredentialName(adapterID, name); err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	if err := h.controlPlane.DeleteAgentAdapterCredential(controlplane.WithActor(r.Context(), settingsActor(r)), adapterID, name); err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, AgentAdapterCredentialResponse{
		Object: "agent_adapter_credential",
		Data: AgentAdapterCredentialResponseItem{
			AdapterID:  adapterID,
			Name:       name,
			Configured: false,
		},
	})
}

func (h *Handler) agentAdapterCredentialEnv(ctx context.Context, adapterID string) []string {
	if h == nil || h.controlPlane == nil || h.secretCipher == nil {
		return nil
	}
	state, err := h.controlPlane.Snapshot(ctx)
	if err != nil {
		return nil
	}
	env := make([]string, 0, len(state.AgentAdapterCredentials))
	for _, credential := range state.AgentAdapterCredentials {
		if credential.AdapterID != adapterID {
			continue
		}
		if err := validateAgentAdapterCredentialName(adapterID, credential.Name); err != nil {
			continue
		}
		value, err := h.secretCipher.Decrypt(credential.ValueEncrypted)
		if err != nil || strings.TrimSpace(value) == "" {
			continue
		}
		env = append(env, credential.Name+"="+value)
	}
	return env
}

func normalizeAgentAdapterCredentialName(adapterID, name string) string {
	name = strings.TrimSpace(name)
	if name == "" && adapterID == "claude_code" {
		return claudeCodeOAuthTokenName
	}
	return name
}

func validateAgentAdapterCredentialName(adapterID, name string) error {
	switch adapterID {
	case "claude_code":
		if name == claudeCodeOAuthTokenName || name == "ANTHROPIC_API_KEY" || name == "ANTHROPIC_AUTH_TOKEN" {
			return nil
		}
	case "codex":
		if name == "OPENAI_API_KEY" || name == "CODEX_AUTH_TOKEN" || name == "CODEX_API_KEY" {
			return nil
		}
	case "cursor_agent":
		if name == "CURSOR_API_KEY" {
			return nil
		}
	}
	return fmt.Errorf("credential %q is not supported for adapter %q", name, adapterID)
}

func previewAdapterCredential(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 8 {
		return "****"
	}
	return value[:4] + "..." + value[len(value)-4:]
}
