package controlplane

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func normalizeModelCapabilityRecord(record ModelCapabilityRecord, source string) (ModelCapabilityRecord, error) {
	record.Provider = strings.TrimSpace(record.Provider)
	record.Model = strings.TrimSpace(record.Model)
	record.ToolCalling = strings.TrimSpace(record.ToolCalling)
	record.Source = strings.TrimSpace(record.Source)
	record.Note = strings.TrimSpace(record.Note)
	if record.Provider == "" || record.Model == "" {
		return ModelCapabilityRecord{}, fmt.Errorf("model capability provider and model are required")
	}
	if record.Source == "" {
		record.Source = source
	}
	record.UpdatedAt = time.Now().UTC()
	return record, nil
}

func upsertModelCapabilityRecord(items []ModelCapabilityRecord, record ModelCapabilityRecord) ([]ModelCapabilityRecord, string) {
	for i := range items {
		if sameModelCapabilityKey(items[i], record.Provider, record.Model) {
			items[i] = cloneModelCapabilityRecord(record)
			return items, "updated"
		}
	}
	return append(items, cloneModelCapabilityRecord(record)), "created"
}

func deleteModelCapabilityRecord(items []ModelCapabilityRecord, provider, model string) ([]ModelCapabilityRecord, bool) {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	for i := range items {
		if sameModelCapabilityKey(items[i], provider, model) {
			next := append(items[:i], items[i+1:]...)
			return append([]ModelCapabilityRecord(nil), next...), true
		}
	}
	return items, false
}

func sameModelCapabilityKey(record ModelCapabilityRecord, provider, model string) bool {
	return strings.EqualFold(strings.TrimSpace(record.Provider), strings.TrimSpace(provider)) &&
		strings.EqualFold(strings.TrimSpace(record.Model), strings.TrimSpace(model))
}

func applyModelCapabilityOverride(ctx context.Context, state *State, record ModelCapabilityRecord) (ModelCapabilityRecord, error) {
	if state == nil {
		return ModelCapabilityRecord{}, fmt.Errorf("control plane state is required")
	}
	record, err := normalizeModelCapabilityRecord(record, "operator_override")
	if err != nil {
		return ModelCapabilityRecord{}, err
	}
	var action string
	state.ModelCapabilityOverrides, action = upsertModelCapabilityRecord(state.ModelCapabilityOverrides, record)
	appendAuditEvent(state, newAuditEvent(ctx, "model_capability_override."+action, "model_capability", record.Provider+"/"+record.Model, record.ToolCalling))
	return record, nil
}

func applyDeleteModelCapabilityOverride(ctx context.Context, state *State, provider, model string) error {
	if state == nil {
		return fmt.Errorf("control plane state is required")
	}
	var deleted bool
	state.ModelCapabilityOverrides, deleted = deleteModelCapabilityRecord(state.ModelCapabilityOverrides, provider, model)
	if !deleted {
		return fmt.Errorf("model capability override %q not found", provider+"/"+model)
	}
	appendAuditEvent(state, newAuditEvent(ctx, "model_capability_override.deleted", "model_capability", provider+"/"+model, ""))
	return nil
}

func applyModelCapabilityProbe(ctx context.Context, state *State, record ModelCapabilityRecord) (ModelCapabilityRecord, error) {
	if state == nil {
		return ModelCapabilityRecord{}, fmt.Errorf("control plane state is required")
	}
	record, err := normalizeModelCapabilityRecord(record, "probe")
	if err != nil {
		return ModelCapabilityRecord{}, err
	}
	var action string
	state.ModelCapabilityProbeState, action = upsertModelCapabilityRecord(state.ModelCapabilityProbeState, record)
	appendAuditEvent(state, newAuditEvent(ctx, "model_capability_probe."+action, "model_capability", record.Provider+"/"+record.Model, record.ToolCalling))
	return record, nil
}
