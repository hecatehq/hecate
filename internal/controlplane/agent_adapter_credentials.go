package controlplane

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func applyUpsertAgentAdapterCredential(ctx context.Context, state *State, credential AgentAdapterCredential) (AgentAdapterCredential, error) {
	if state == nil {
		return AgentAdapterCredential{}, fmt.Errorf("control plane state is required")
	}
	credential.AdapterID = strings.TrimSpace(credential.AdapterID)
	credential.Name = strings.TrimSpace(credential.Name)
	if credential.AdapterID == "" {
		return AgentAdapterCredential{}, fmt.Errorf("agent adapter id is required")
	}
	if credential.Name == "" {
		return AgentAdapterCredential{}, fmt.Errorf("agent adapter credential name is required")
	}
	if strings.TrimSpace(credential.ValueEncrypted) == "" {
		return AgentAdapterCredential{}, fmt.Errorf("agent adapter credential ciphertext is required")
	}

	now := time.Now().UTC()
	index := agentAdapterCredentialIndex(state.AgentAdapterCredentials, credential.AdapterID, credential.Name)
	if index >= 0 {
		existing := state.AgentAdapterCredentials[index]
		if credential.CreatedAt.IsZero() {
			credential.CreatedAt = existing.CreatedAt
		}
	} else if credential.CreatedAt.IsZero() {
		credential.CreatedAt = now
	}
	credential.RotatedAt = now

	if index >= 0 {
		state.AgentAdapterCredentials[index] = credential
	} else {
		state.AgentAdapterCredentials = append(state.AgentAdapterCredentials, credential)
	}
	appendAuditEvent(state, newAuditEvent(ctx, "agent_adapter.credential_saved", "agent_adapter", credential.AdapterID, credential.Name))
	return credential, nil
}

func applyDeleteAgentAdapterCredential(ctx context.Context, state *State, adapterID, name string) error {
	adapterID = strings.TrimSpace(adapterID)
	name = strings.TrimSpace(name)
	if adapterID == "" {
		return fmt.Errorf("agent adapter id is required")
	}
	if name == "" {
		return fmt.Errorf("agent adapter credential name is required")
	}
	index := agentAdapterCredentialIndex(state.AgentAdapterCredentials, adapterID, name)
	if index < 0 {
		return fmt.Errorf("agent adapter credential %q for %q not found", name, adapterID)
	}
	state.AgentAdapterCredentials = append(state.AgentAdapterCredentials[:index], state.AgentAdapterCredentials[index+1:]...)
	appendAuditEvent(state, newAuditEvent(ctx, "agent_adapter.credential_deleted", "agent_adapter", adapterID, name))
	return nil
}

func agentAdapterCredentialIndex(items []AgentAdapterCredential, adapterID, name string) int {
	for i := range items {
		if items[i].AdapterID == adapterID && items[i].Name == name {
			return i
		}
	}
	return -1
}
