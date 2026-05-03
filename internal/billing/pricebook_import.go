package billing

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"sync/atomic"
	"time"

	"github.com/hecate/agent-runtime/internal/billing/litellm"
	"github.com/hecate/agent-runtime/internal/config"
	"github.com/hecate/agent-runtime/internal/controlplane"
)

// pricebookImporter is the seam tests use to substitute a fixture
// loader for the real LiteLLM HTTP fetch. Production code calls
// `litellm.Fetch`; tests reassign this to return a hand-built slice.
type pricebookImporter func(ctx context.Context) ([]config.ModelPriceConfig, error)

// PricebookImportStore is the subset of controlplane.Store the importer
// needs. Pulled out so the auto-import scheduler can be tested with a
// thin fake instead of a full durable control-plane stack.
type PricebookImportStore interface {
	Snapshot(ctx context.Context) (controlplane.State, error)
	UpsertPricebookEntry(ctx context.Context, entry config.ModelPriceConfig) (config.ModelPriceConfig, error)
}

// PricebookImportSummary is the result of one import run — added rows,
// updated rows, unchanged count, and any per-row failures. It mirrors
// the wire shape the HTTP handler returns to the UI so both surfaces
// can be reasoned about identically.
type PricebookImportSummary struct {
	FetchedAt string
	Added     []config.ModelPriceConfig
	Updated   []PricebookImportUpdate
	Skipped   []PricebookImportUpdate
	Unchanged int
	Applied   []config.ModelPriceConfig
	Failed    []PricebookImportFailure
}

// PricebookImportUpdate pairs a proposed (imported) row with its
// previous (currently-stored) value so the UI can show a before/after
// diff. The auto-import scheduler doesn't render these but the type is
// shared with the handler.
type PricebookImportUpdate struct {
	Entry    config.ModelPriceConfig
	Previous config.ModelPriceConfig
}

// PricebookImportFailure is one row that failed to apply. Best-effort
// import keeps going past failures so the operator (or scheduler logs)
// see exactly which rows didn't land.
type PricebookImportFailure struct {
	Entry config.ModelPriceConfig
	Error string
}

// PricebookImportOptions controls one Run() call.
//   - Apply: when true, also persists Added+Updated rows via the store.
//     False = preview only (HTTP /preview path; not used by the
//     scheduler, which always applies).
//   - Keys: optional explicit allowlist of "provider/model" keys to
//     restrict the apply to. Empty list means "everything". An explicit
//     non-empty list ALSO unlocks Skipped (manual-rows) for replacement
//     — the operator-protection contract.
type PricebookImportOptions struct {
	Apply bool
	Keys  []string
}

// PricebookImporter carries the dependencies needed to compute and
// apply a LiteLLM diff. Construct once; call Run repeatedly. Safe for
// concurrent use only when the underlying store is.
type PricebookImporter struct {
	store    PricebookImportStore
	fetcher  pricebookImporter
	clientHT *http.Client
}

// NewPricebookImporter wires the production importer: the supplied
// store for current state + UpsertPricebookEntry, and the real LiteLLM
// HTTP fetcher. Tests use NewPricebookImporterWithFetcher to substitute
// the fetcher for fixture data.
func NewPricebookImporter(store PricebookImportStore, client *http.Client) *PricebookImporter {
	if client == nil {
		client = http.DefaultClient
	}
	return &PricebookImporter{
		store:    store,
		clientHT: client,
		fetcher:  func(ctx context.Context) ([]config.ModelPriceConfig, error) { return litellm.Fetch(ctx, client) },
	}
}

// NewPricebookImporterWithFetcher is the test seam — substitutes the
// upstream fetcher with a hand-built slice. Used by handler_pricebook_import.go's
// existing test override and by pricebook_import_test.go.
func NewPricebookImporterWithFetcher(store PricebookImportStore, fetcher pricebookImporter) *PricebookImporter {
	return &PricebookImporter{store: store, fetcher: fetcher}
}

// Run is the single entry point used by both the HTTP handlers and the
// scheduler. With Apply=false it returns a diff-only summary (preview);
// with Apply=true it persists the diff and returns an applied summary.
// The "manual rows are operator-protected" contract is enforced here:
// blanket apply (empty Keys) leaves Skipped alone; explicit Keys allow
// Skipped replacements.
func (p *PricebookImporter) Run(ctx context.Context, opts PricebookImportOptions) (PricebookImportSummary, error) {
	imported, err := p.fetcher(ctx)
	if err != nil {
		return PricebookImportSummary{}, err
	}

	state, err := p.store.Snapshot(ctx)
	if err != nil {
		return PricebookImportSummary{}, err
	}

	type currentRow struct {
		entry  config.ModelPriceConfig
		source string
	}
	current := make(map[string]currentRow, len(state.Pricebook))
	for _, entry := range state.Pricebook {
		key := PricebookKey(entry.Provider, entry.Model)
		source := entry.Source
		if source == "" {
			source = config.PricebookSourceManual
		}
		current[key] = currentRow{entry: entry, source: source}
	}

	summary := PricebookImportSummary{
		FetchedAt: time.Now().UTC().Format(time.RFC3339),
	}
	for _, entry := range imported {
		key := PricebookKey(entry.Provider, entry.Model)
		existing, ok := current[key]
		if !ok {
			summary.Added = append(summary.Added, entry)
			continue
		}
		// Manual rows are operator-protected — we never overwrite them
		// in a blanket apply. Surface as Skipped (paired with the
		// proposed price) so the UI / log shows what was held back.
		if existing.source == config.PricebookSourceManual {
			if pricebookPricesEqual(existing.entry, entry) {
				summary.Unchanged++
				continue
			}
			summary.Skipped = append(summary.Skipped, PricebookImportUpdate{
				Entry: entry, Previous: existing.entry,
			})
			continue
		}
		if pricebookPricesEqual(existing.entry, entry) {
			summary.Unchanged++
			continue
		}
		summary.Updated = append(summary.Updated, PricebookImportUpdate{
			Entry: entry, Previous: existing.entry,
		})
	}

	// Sort each section so output is deterministic — important both for
	// the UI (no jump on re-render) and the scheduler logs (diffable
	// across runs).
	sortPricebookEntries(summary.Added)
	sortPricebookUpdates(summary.Updated)
	sortPricebookUpdates(summary.Skipped)

	if !opts.Apply {
		return summary, nil
	}

	keyFilter := newPricebookKeyFilter(opts.Keys)
	applied := make([]config.ModelPriceConfig, 0, len(summary.Added)+len(summary.Updated))
	failed := make([]PricebookImportFailure, 0)
	applyOne := func(entry config.ModelPriceConfig) {
		entry.Source = config.PricebookSourceImported
		saved, upsertErr := p.store.UpsertPricebookEntry(ctx, entry)
		if upsertErr != nil {
			failed = append(failed, PricebookImportFailure{Entry: entry, Error: upsertErr.Error()})
			return
		}
		applied = append(applied, saved)
	}

	for _, entry := range summary.Added {
		if !keyFilter.allows(entry.Provider, entry.Model) {
			continue
		}
		applyOne(entry)
	}
	for _, update := range summary.Updated {
		if !keyFilter.allows(update.Entry.Provider, update.Entry.Model) {
			continue
		}
		applyOne(update.Entry)
	}
	if keyFilter.explicit() {
		for _, skip := range summary.Skipped {
			if !keyFilter.allows(skip.Entry.Provider, skip.Entry.Model) {
				continue
			}
			applyOne(skip.Entry)
		}
	}

	summary.Applied = applied
	summary.Failed = failed
	return summary, nil
}

// PricebookKey is the canonical "provider/model" identifier used both
// in the importer's diff bookkeeping and in audit-event target_ids.
// Exported so other packages (audit-event consumers) can reproduce the
// same shape without cross-package leaking.
func PricebookKey(provider, model string) string { return provider + "/" + model }

func pricebookPricesEqual(a, b config.ModelPriceConfig) bool {
	return a.InputMicrosUSDPerMillionTokens == b.InputMicrosUSDPerMillionTokens &&
		a.OutputMicrosUSDPerMillionTokens == b.OutputMicrosUSDPerMillionTokens &&
		a.CachedInputMicrosUSDPerMillionTokens == b.CachedInputMicrosUSDPerMillionTokens
}

func sortPricebookEntries(items []config.ModelPriceConfig) {
	sort.Slice(items, func(i, j int) bool {
		return PricebookKey(items[i].Provider, items[i].Model) <
			PricebookKey(items[j].Provider, items[j].Model)
	})
}

func sortPricebookUpdates(items []PricebookImportUpdate) {
	sort.Slice(items, func(i, j int) bool {
		return PricebookKey(items[i].Entry.Provider, items[i].Entry.Model) <
			PricebookKey(items[j].Entry.Provider, items[j].Entry.Model)
	})
}

type pricebookKeyFilter struct{ keys map[string]struct{} }

func newPricebookKeyFilter(keys []string) pricebookKeyFilter {
	if len(keys) == 0 {
		return pricebookKeyFilter{}
	}
	set := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		if k == "" {
			continue
		}
		set[k] = struct{}{}
	}
	return pricebookKeyFilter{keys: set}
}

func (f pricebookKeyFilter) allows(provider, model string) bool {
	if f.keys == nil {
		return true
	}
	_, ok := f.keys[PricebookKey(provider, model)]
	return ok
}

func (f pricebookKeyFilter) explicit() bool { return f.keys != nil }

// ─── Auto-import scheduler ─────────────────────────────────────────────

// PricebookAutoImportConfig pins one knob, the tick interval. Zero or
// negative interval disables the scheduler.
type PricebookAutoImportConfig struct {
	Interval time.Duration
}

// AutoImportLogger is the minimum logging surface the scheduler needs.
// We pass *slog.Logger in production but accept the interface so tests
// can capture log lines without setting up a slog handler.
type AutoImportLogger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}

// RunPricebookAutoImport ticks on the configured interval and invokes
// the importer with Apply=true / Keys=nil (blanket import). It runs
// once immediately on start so a fresh deploy gets up-to-date prices
// without waiting one full interval. Returns when ctx is cancelled.
//
// Disabled (Interval ≤ 0) returns immediately — main.go calls this
// unconditionally so the disabled-path is the same shape as the
// enabled-path.
//
// The function never crashes the gateway: fetch errors, store errors,
// and per-row failures are all logged at Warn and the next tick fires
// normally. This matches the operator expectation that a temporary
// LiteLLM outage shouldn't take the gateway down.
func RunPricebookAutoImport(
	ctx context.Context,
	importer *PricebookImporter,
	cfg PricebookAutoImportConfig,
	logger AutoImportLogger,
) {
	if cfg.Interval <= 0 {
		return
	}

	runOnce := func() {
		summary, err := importer.Run(ctx, PricebookImportOptions{Apply: true})
		if err != nil {
			logger.Warn("pricebook auto-import failed", "error", err)
			return
		}
		logger.Info("pricebook auto-import",
			"added", len(summary.Applied)-countAppliedUpdates(summary),
			"updated", countAppliedUpdates(summary),
			"unchanged", summary.Unchanged,
			"skipped_manual", len(summary.Skipped),
			"failed", len(summary.Failed),
			"fetched_at", summary.FetchedAt,
		)
		for _, f := range summary.Failed {
			logger.Warn("pricebook auto-import row failed",
				"provider", f.Entry.Provider,
				"model", f.Entry.Model,
				"error", f.Error,
			)
		}
	}

	// Run once immediately so a freshly-started gateway with auto-import
	// configured doesn't sit on stale prices for a full interval.
	runOnce()

	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runOnce()
		}
	}
}

// countAppliedUpdates is a heuristic — Applied collapses Added+Updated.
// We count Updated by comparing applied rows against the original
// Added slice. Used only for log structuring; not load-bearing.
func countAppliedUpdates(s PricebookImportSummary) int {
	if len(s.Applied) == 0 {
		return 0
	}
	addedKeys := make(map[string]struct{}, len(s.Added))
	for _, a := range s.Added {
		addedKeys[PricebookKey(a.Provider, a.Model)] = struct{}{}
	}
	updates := 0
	for _, app := range s.Applied {
		if _, ok := addedKeys[PricebookKey(app.Provider, app.Model)]; !ok {
			updates++
		}
	}
	return updates
}

// AutoImportRuns is an atomic counter tests use to assert the scheduler
// fires the expected number of times. Production code doesn't read it.
type AutoImportRuns = atomic.Int64

// Sentinel error in case future callers want to special-case "auto
// import disabled" without inspecting the config field directly.
var ErrAutoImportDisabled = fmt.Errorf("pricebook auto-import: disabled")
