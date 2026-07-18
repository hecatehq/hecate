package chat

import "testing"

// StoreFactory builds a fresh Store for one conformance subtest.
// Each subtest gets its own factory invocation so backends with
// per-instance state (sqlite file under t.TempDir, fresh memory
// map) start clean. The factory is t.Helper()-friendly and may use
// t.Cleanup for teardown.
type StoreFactory func(t *testing.T) Store

// RunConformanceTests exercises every Store-interface contract
// against the backend the factory produces. Memory + sqlite invoke
// this with their own factory; new backends added later only need
// to supply a factory + one entry-point test, not duplicate every
// case body.
//
// Per-backend tests that exercise something the contract doesn't
// describe (sqlite file-on-disk reopen, sqlite-specific
// reconcile-across-instances) stay as standalone tests in their
// backend's _test.go.
func RunConformanceTests(t *testing.T, name string, factory StoreFactory) {
	t.Helper()
	t.Run(name+"/Lifecycle", func(t *testing.T) {
		t.Parallel()
		runStoreLifecycle(t, factory(t))
	})
	t.Run(name+"/ReconcileInterruptedRuns", func(t *testing.T) {
		t.Parallel()
		runStoreReconcileInterruptedRuns(t, factory(t))
	})
	t.Run(name+"/DoesNotHydrateTaskIDForAnonymousAgentSegment", func(t *testing.T) {
		t.Parallel()
		runStoreDoesNotHydrateTaskIDForAnonymousAgentSegment(t, factory(t))
	})
	t.Run(name+"/DeepCopiesConfigOptions", func(t *testing.T) {
		t.Parallel()
		runStoreDeepCopiesConfigOptions(t, factory(t))
	})
	t.Run(name+"/AvailableCommandsRoundTrip", func(t *testing.T) {
		t.Parallel()
		runStoreAvailableCommandsRoundTrip(t, factory(t))
	})
	t.Run(name+"/AvailableCommandsAuthorityRoundTrip", func(t *testing.T) {
		t.Parallel()
		runStoreAvailableCommandsAuthorityRoundTrip(t, factory(t))
	})
	t.Run(name+"/AgentInfoRoundTrip", func(t *testing.T) {
		t.Parallel()
		runStoreAgentInfoRoundTrip(t, factory(t))
	})
	t.Run(name+"/MCPServersRoundTrip", func(t *testing.T) {
		t.Parallel()
		runStoreMCPServersRoundTrip(t, factory(t))
	})
	t.Run(name+"/ContextSummaryRoundTrip", func(t *testing.T) {
		t.Parallel()
		runStoreContextSummaryRoundTrip(t, factory(t))
	})
	t.Run(name+"/DeleteByProjectID", func(t *testing.T) {
		t.Parallel()
		runStoreDeleteByProjectID(t, factory(t))
	})
	t.Run(name+"/ToolsEnabledRoundTrip", func(t *testing.T) {
		t.Parallel()
		runStoreToolsEnabledRoundTrip(t, factory(t))
	})
	t.Run(name+"/MessageAttachmentsRoundTrip", func(t *testing.T) {
		t.Parallel()
		runStoreMessageAttachmentsRoundTrip(t, factory(t))
	})
	t.Run(name+"/ActivityOnlyUpdateDoesNotReprojectSessionStatus", func(t *testing.T) {
		t.Parallel()
		runStoreActivityOnlyUpdateDoesNotReprojectSessionStatus(t, factory(t))
	})
	t.Run(name+"/MessageRequestIdempotency", func(t *testing.T) {
		t.Parallel()
		runStoreMessageRequestIdempotency(t, factory(t))
	})
	t.Run(name+"/MessageRequestLeaseRenewal", func(t *testing.T) {
		t.Parallel()
		runStoreMessageRequestLeaseRenewal(t, factory(t))
	})
	t.Run(name+"/TaskRunLinkAtomic", func(t *testing.T) {
		t.Parallel()
		runStoreTaskRunLinkAtomic(t, factory(t))
	})
}
