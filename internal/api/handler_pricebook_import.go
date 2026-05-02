package api

import (
	"context"
	"net/http"

	"github.com/hecate/agent-runtime/internal/billing"
	"github.com/hecate/agent-runtime/internal/billing/litellm"
	"github.com/hecate/agent-runtime/internal/config"
	"github.com/hecate/agent-runtime/internal/controlplane"
)

// pricebookImportFetcher is the seam tests use to substitute a fixture
// loader for the real LiteLLM HTTP fetch. Production code calls
// `litellm.Fetch` via billing.NewPricebookImporter; tests reassign this
// var so the same handler path can run against a fixture.
var pricebookImportFetcher = func(ctx context.Context) ([]config.ModelPriceConfig, error) {
	return litellm.Fetch(ctx, http.DefaultClient)
}

// HandleControlPlanePricebookImportPreview fetches the upstream LiteLLM
// pricing data, diffs it against the current pricebook, and returns the
// proposed changes without applying anything.
//
// The diff has three buckets:
//   - Added:   imported rows that don't currently exist
//   - Updated: imported rows that would change a current "imported" row's price
//   - Skipped: current "manual" rows that LiteLLM also has — we never overwrite
//     manual edits, so we report them so the UI can explain
//
// Imported rows that exactly match the current pricebook are silently
// counted in Unchanged.
func (h *Handler) HandleControlPlanePricebookImportPreview(w http.ResponseWriter, r *http.Request) {
	if !h.requireControlPlane(w, r) {
		return
	}

	importer := h.newPricebookImporter()
	summary, err := importer.Run(r.Context(), billing.PricebookImportOptions{Apply: false})
	if err != nil {
		WriteError(w, http.StatusBadGateway, errCodeGatewayError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{
		"object": "control_plane_pricebook_import_diff",
		"data":   pricebookImportDiffFromSummary(summary),
	})
}

// HandleControlPlanePricebookImportApply runs the same fetch+diff as the
// preview handler and then persists the rows it would add or update via
// `controlPlane.UpsertPricebookEntry`. The optional `keys` field in the
// request body restricts the apply to a subset (e.g. just the rows the
// operator checked in the modal). Empty/missing keys means "apply
// everything".
func (h *Handler) HandleControlPlanePricebookImportApply(w http.ResponseWriter, r *http.Request) {
	if !h.requireControlPlane(w, r) {
		return
	}

	var req PricebookImportApplyRequest
	// Apply is allowed with an empty body — that's "apply everything". So we
	// only fail on a non-empty body that isn't valid JSON.
	if r.ContentLength > 0 {
		if !decodeJSON(w, r, &req) {
			return
		}
	}

	ctx := controlplane.WithActor(r.Context(), controlPlaneActor(r))
	importer := h.newPricebookImporter()
	summary, err := importer.Run(ctx, billing.PricebookImportOptions{Apply: true, Keys: req.Keys})
	if err != nil {
		WriteError(w, http.StatusBadGateway, errCodeGatewayError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{
		"object": "control_plane_pricebook_import_diff",
		"data":   pricebookImportDiffFromSummary(summary),
	})
}

// newPricebookImporter constructs a billing.PricebookImporter wired to
// the handler's control-plane store and the (test-overridable) fetcher.
// The fetcher var keeps the existing test seam working — handler tests
// reassign pricebookImportFetcher to inject fixture data.
func (h *Handler) newPricebookImporter() *billing.PricebookImporter {
	return billing.NewPricebookImporterWithFetcher(h.controlPlane, pricebookImportFetcher)
}

// pricebookImportDiffFromSummary translates the importer's internal
// representation (config.ModelPriceConfig everywhere) into the wire
// shape the UI consumes. Wire shape predates the importer extraction,
// so this is the one-way adapter.
func pricebookImportDiffFromSummary(s billing.PricebookImportSummary) PricebookImportDiff {
	out := PricebookImportDiff{
		FetchedAt: s.FetchedAt,
		Unchanged: s.Unchanged,
	}
	for _, e := range s.Added {
		out.Added = append(out.Added, renderControlPlanePricebookEntry(e))
	}
	for _, u := range s.Updated {
		out.Updated = append(out.Updated, PricebookImportUpdateRecord{
			Entry:    renderControlPlanePricebookEntry(u.Entry),
			Previous: renderControlPlanePricebookEntry(u.Previous),
		})
	}
	for _, sk := range s.Skipped {
		out.Skipped = append(out.Skipped, PricebookImportUpdateRecord{
			Entry:    renderControlPlanePricebookEntry(sk.Entry),
			Previous: renderControlPlanePricebookEntry(sk.Previous),
		})
	}
	for _, a := range s.Applied {
		out.Applied = append(out.Applied, renderControlPlanePricebookEntry(a))
	}
	for _, f := range s.Failed {
		out.Failed = append(out.Failed, PricebookImportFailureRecord{
			Entry: renderControlPlanePricebookEntry(f.Entry),
			Error: f.Error,
		})
	}
	return out
}
