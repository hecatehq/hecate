// Test render helper. Wraps view-tests in the real slice providers
// seeded with fixture state plus a coordinator-overrides context
// that intercepts action calls so tests can keep asserting on
// stubbed action vi.fn() invocations.
//
// Production code never uses any of this — App.tsx mounts slice
// providers directly, and views consume slice + coordinator hooks
// without a viewmodel facade. The helper preserves the legacy
// `withRuntimeConsole(ui, {state, actions})` ergonomics so per-view
// test files don't have to be rewritten end-to-end.

import { useEffect, useRef, type ReactElement, type ReactNode } from "react";

import { ApprovalsProvider, useApprovals } from "../app/state/approvals";
import { ChatProvider, useChat } from "../app/state/chat";
import {
  CoordinatorOverridesProvider,
  type CoordinatorOverrides,
} from "../app/state/coordinators/overrides";
import { ProvidersAndModelsProvider, useProvidersAndModels } from "../app/state/providersAndModels";
import { ProjectsProvider, useProjects } from "../app/state/projects";
import { RetentionProvider, useRetention } from "../app/state/retention";
import { RuntimeProvider, useRuntime } from "../app/state/runtime";
import { SettingsProvider, useSettings } from "../app/state/settings";
import { UsageProvider, useUsage } from "../app/state/usage";

import type {
  RuntimeConsoleFixtureActions,
  RuntimeConsoleFixtureState,
} from "./runtime-console-fixture";

// Slice initial-state derivations from the fixture bag. The fixture
// shape is a denormalized snapshot of the previous viewmodel; we
// fan its fields back out to the slices that own them.
function runtimeInitialState(fixture: RuntimeConsoleFixtureState) {
  return {
    health: fixture.health,
    sessionInfo: null,
    loading: fixture.loading,
    error: fixture.error,
    message: fixture.message,
    runtimeHeaders: fixture.runtimeHeaders,
    copiedCommand: fixture.copiedCommand,
    hecateRTKEnabled: fixture.hecateRTKEnabled,
    hecateRTKAvailable: fixture.hecateRTKAvailable,
    hecateRTKPath: fixture.hecateRTKPath,
  };
}

function usageInitialState(fixture: RuntimeConsoleFixtureState) {
  return {
    summary: fixture.usageSummary,
    events: fixture.usageEvents,
    // Tests that mount UsageView via this wrapper pretend the slice
    // is already hydrated — without this flag, useEnsureUsageLoaded
    // would fire the (unmocked) fetch on every UsageView render and
    // spam usage.ensureLoaded.failed warnings into the test output.
    loaded: true,
  };
}

function providersAndModelsInitialState(fixture: RuntimeConsoleFixtureState) {
  return {
    providers: fixture.providers,
    providerPresets: fixture.providerPresets,
    // Same dedup reason as usageInitialState: presets are considered
    // pre-loaded under the fixture so useEnsureProviderPresetsLoaded
    // doesn't trigger a fetch on every AddProviderModal / TasksView
    // mount inside a test render.
    providerPresetsLoaded: true,
    // Same compat shim as the syncer: legacy tests fill
    // providerScopedModels but leave models empty.
    models: fixture.models.length > 0 ? fixture.models : fixture.providerScopedModels,
    agentAdapters: fixture.agentAdapters,
    agentAdapterApprovalMode: fixture.agentAdapterApprovalMode,
    agentAdapterHealthByID: fixture.agentAdapterHealthByID,
    agentAdapterHealthLoadingByID: fixture.agentAdapterHealthLoadingByID,
  };
}

function chatInitialState(fixture: RuntimeConsoleFixtureState) {
  // Same chatTarget mapping rule as the syncer: an active non-external
  // session needs the per-session override to land on the derived
  // chatTarget the test expects.
  let chatTargetBySessionID = fixture.chatTargetBySessionID;
  if (fixture.activeChatSessionID && fixture.chatTarget !== "external_agent") {
    chatTargetBySessionID = new Map(fixture.chatTargetBySessionID);
    chatTargetBySessionID.set(fixture.activeChatSessionID, fixture.chatTarget);
  }
  return {
    defaultChatTarget: fixture.chatTarget,
    chatTargetBySessionID,
    agentAdapterID: fixture.agentAdapterID,
    agentConfigOptions: fixture.agentConfigOptions,
    agentWorkspace: fixture.agentWorkspace,
    agentWorkspaceBranch: fixture.agentWorkspaceBranch,
    chatSessions: fixture.chatSessions,
    activeChatSessionID: fixture.activeChatSessionID,
    activeChatSession: fixture.activeChatSession,
    queuedChatMessages: fixture.queuedChatMessages,
    model: fixture.model,
    systemPrompt: fixture.systemPrompt,
    chatLoading: fixture.chatLoading,
    chatCancelling: fixture.chatCancelling,
    streamingContent: fixture.streamingContent,
    chatResult: fixture.chatResult,
    pendingToolCalls: fixture.pendingToolCalls,
    pendingThread: fixture.pendingThread,
    chatError: fixture.chatError,
    chatErrorCode: fixture.chatErrorCode,
    chatErrorStatus: fixture.chatErrorStatus,
    chatErrorAction: fixture.chatErrorAction,
    chatErrorRequestID: fixture.chatErrorRequestID,
    chatErrorTraceID: fixture.chatErrorTraceID,
    modelFilter: fixture.modelFilter,
    providerFilter: fixture.providerFilter,
  };
}

function projectsInitialState(fixture: RuntimeConsoleFixtureState) {
  return {
    projects: fixture.projects,
  };
}

function approvalsInitialState(fixture: RuntimeConsoleFixtureState) {
  return {
    pendingBySessionID: fixture.pendingApprovalsBySessionID,
    grants: fixture.chatGrants,
    grantsLoading: fixture.chatGrantsLoading,
    grantsError: fixture.chatGrantsError,
  };
}

function retentionInitialState(fixture: RuntimeConsoleFixtureState) {
  return {
    subsystems: fixture.retentionSubsystems,
    loading: fixture.retentionLoading,
    error: fixture.retentionError,
    lastRun: fixture.retentionLastRun,
    runs: fixture.retentionRuns,
  };
}

function settingsInitialState(fixture: RuntimeConsoleFixtureState) {
  return {
    config: fixture.settingsConfig,
    error: fixture.settingsError,
    notice: fixture.notice,
  };
}

// Fan the test fixture's flat `actions` bag out into the per-coordinator
// shape the slice + coordinator hooks read through the overrides context.
//
// Many slice setters use the React useState-style polymorphic signature
// (`value | (prev) => value`), but the fixture exposes the simpler
// `(value) => void` shape. We adapt by treating overrides as opaque
// records here — `applyOverride` casts the partial back to the real
// hook's return type before merging — and tests assert on the
// invocation rather than the update form.
function buildOverrides(actions: RuntimeConsoleFixtureActions): CoordinatorOverrides {
  return {
    runtimeSlice: {
      setMessage: actions.setMessage,
      copyCommand: actions.copyCommand,
    },
    chatSlice: {
      setAgentAdapterID: actions.setAgentAdapterID,
      setAgentWorkspace: actions.setAgentWorkspace,
      setSystemPrompt: actions.setSystemPrompt,
      setModel: actions.setModel,
      setModelFilter: actions.setModelFilter,
      setProviderFilter: actions.setProviderFilter,
      removeQueuedChatMessage: actions.removeQueuedChatMessage,
      updateQueuedChatMessage: actions.updateQueuedChatMessage,
    },
    settingsSlice: {
      dismissNotice: actions.dismissNotice,
    },
    retentionSlice: {
      setSubsystems: actions.setRetentionSubsystems,
      loadRuns: actions.loadRetentionRuns,
    },
    approvalsSlice: {
      loadGrants: actions.listChatGrants,
    },
    providersAndModelsSlice: {
      refreshProviders: actions.refreshProviders,
      probeAgentAdapter: actions.probeAgentAdapter,
    },
    projectsSlice: {
      setActiveProjectID: actions.setActiveProjectID,
      loadProjects: actions.loadProjects,
      createProjectFromFolder: actions.createProjectFromFolder,
      selectProject: actions.selectProject,
      renameProject: actions.renameProject,
      deleteProject: actions.deleteProject,
    },
    chat: {
      submitChat: actions.submitChat,
      submitToolResults: actions.submitToolResults,
      cancelAgentChat: actions.cancelAgentChat,
      chooseAgentWorkspace: actions.chooseAgentWorkspace,
      createChatSession: actions.createChatSession,
      deleteChatSession: actions.deleteChatSession,
      renameChatSession: actions.renameChatSession,
      selectChatSession: actions.selectChatSession,
      startNewChat: actions.startNewChat,
      setChatTarget: actions.setChatTarget,
      setChatToolsEnabled: actions.setChatToolsEnabled,
      setNewChatAgent: actions.setNewChatAgent,
      updateToolResult: actions.updateToolResult,
      getChatApproval: actions.getChatApproval,
      resolveChatApproval: actions.resolveChatApproval,
      cancelChatApproval: actions.cancelChatApproval,
      resolveTaskApproval: actions.resolveTaskApproval,
      deleteChatGrant: actions.deleteChatGrant,
      listChatMessageFiles: actions.listChatMessageFiles,
      getChatWorkspaceDiff: actions.getChatWorkspaceDiff,
      getChatWorkspaceFileDiff: actions.getChatWorkspaceFileDiff,
      revertChatWorkspaceFiles: actions.revertChatWorkspaceFiles,
      getChatMessageFileDiff: actions.getChatMessageFileDiff,
      revertChatMessageFiles: actions.revertChatMessageFiles,
      setChatConfigOption: actions.setChatConfigOption,
      setHecateRTKEnabled: actions.setHecateRTKEnabled,
    },
    dashboard: {
      loadDashboard: actions.loadDashboard,
      refreshProviders: actions.refreshProviders,
    },
    providers: {
      setProviderAPIKey: actions.setProviderAPIKey,
      createProvider: actions.createProvider,
      deleteProvider: actions.deleteProvider,
      setProviderBaseURL: actions.setProviderBaseURL,
      setProviderName: actions.setProviderName,
      setProviderCustomName: actions.setProviderCustomName,
    },
    policy: {
      upsertPolicyRule: actions.upsertPolicyRule,
      deletePolicyRule: actions.deletePolicyRule,
    },
    agentAdapters: {
      probeAgentAdapter: actions.probeAgentAdapter,
    },
    retention: {
      runRetention: actions.runRetention,
    },
  };
}

// FixtureSyncer pushes the current fixture state into each slice on
// every render. React-Testing-Library's `rerender` reuses the same
// component instances, which means the slice providers initialize
// only once — without this syncer, a test that passes new fixture
// state via `rerender(withRuntimeConsole(ui, { state: nextState,
// actions }))` would never see the slice state update. The syncer
// reads the latest fixture and dispatches the diff into each slice
// using the slice's own action API.
function FixtureSyncer({ state }: { state: RuntimeConsoleFixtureState }) {
  const runtime = useRuntime();
  const usage = useUsage();
  const providersAndModels = useProvidersAndModels();
  const projects = useProjects();
  const chat = useChat();
  const approvals = useApprovals();
  const retention = useRetention();
  const settings = useSettings();

  // Capture latest action bags in refs so the effect deps are only
  // `state`. Without this, applyOverride re-creates the actions
  // object every render (it spreads even when overrides exist), and
  // an effect that depends on `runtime.actions` etc. would loop
  // forever: dispatch → state change → re-render → new actions
  // identity → effect re-runs → dispatch …
  const runtimeActionsRef = useRef(runtime.actions);
  runtimeActionsRef.current = runtime.actions;
  const usageActionsRef = useRef(usage.actions);
  usageActionsRef.current = usage.actions;
  const providersAndModelsActionsRef = useRef(providersAndModels.actions);
  providersAndModelsActionsRef.current = providersAndModels.actions;
  const projectsActionsRef = useRef(projects.actions);
  projectsActionsRef.current = projects.actions;
  const chatActionsRef = useRef(chat.actions);
  chatActionsRef.current = chat.actions;
  const approvalsActionsRef = useRef(approvals.actions);
  approvalsActionsRef.current = approvals.actions;
  const retentionActionsRef = useRef(retention.actions);
  retentionActionsRef.current = retention.actions;
  const settingsActionsRef = useRef(settings.actions);
  settingsActionsRef.current = settings.actions;

  // Effect runs only when the fixture state identity changes. Each
  // setter dispatches to its slice; matching values are no-ops at
  // the React state level (Object.is) so a sync of unchanged values
  // doesn't cause a re-render storm.
  useEffect(() => {
    runtimeActionsRef.current.setHealth(state.health);
    runtimeActionsRef.current.setLoading(state.loading);
    runtimeActionsRef.current.setError(state.error);
    runtimeActionsRef.current.setMessage(state.message);
    runtimeActionsRef.current.setRuntimeHeaders(state.runtimeHeaders);
    runtimeActionsRef.current.setHecateRTKEnabled(state.hecateRTKEnabled);
    runtimeActionsRef.current.setHecateRTKAvailable(state.hecateRTKAvailable);
    runtimeActionsRef.current.setHecateRTKPath(state.hecateRTKPath);

    usageActionsRef.current.setSummary(state.usageSummary);
    usageActionsRef.current.setEvents(state.usageEvents);

    providersAndModelsActionsRef.current.setProviders(state.providers);
    providersAndModelsActionsRef.current.setProviderPresets(state.providerPresets);
    // Legacy fixture compat: some view tests set providerScopedModels
    // (the *derived* models list the viewmodel used to expose) and
    // expect it to drive the visible model list. In the new derivation
    // pipeline, providerScopedModels comes out of slice models filtered
    // by chat.providerFilter. Fall back to providerScopedModels when
    // the fixture didn't set `models` directly so those tests keep
    // working without per-test rewrites.
    providersAndModelsActionsRef.current.setModels(
      state.models.length > 0 ? state.models : state.providerScopedModels,
    );
    providersAndModelsActionsRef.current.setAgentAdapters(state.agentAdapters);
    providersAndModelsActionsRef.current.setAgentAdapterApprovalMode(
      state.agentAdapterApprovalMode,
    );
    for (const [id, record] of state.agentAdapterHealthByID) {
      providersAndModelsActionsRef.current.setAgentAdapterHealth(id, record);
    }
    for (const [id, loading] of state.agentAdapterHealthLoadingByID) {
      providersAndModelsActionsRef.current.setAgentAdapterHealthLoading(id, Boolean(loading));
    }

    projectsActionsRef.current.setProjects(state.projects);
    projectsActionsRef.current.setActiveProjectID(state.activeProjectID);

    chatActionsRef.current.setActiveChatSession(state.activeChatSession);
    chatActionsRef.current.setActiveChatSessionID(state.activeChatSessionID);
    chatActionsRef.current.setAgentAdapterID(state.agentAdapterID);
    chatActionsRef.current.setAgentConfigOptions(state.agentConfigOptions);
    chatActionsRef.current.setAgentWorkspace(state.agentWorkspace);
    chatActionsRef.current.setAgentWorkspaceBranch(state.agentWorkspaceBranch);
    chatActionsRef.current.setChatSessions(state.chatSessions);
    chatActionsRef.current.setQueuedChatMessages(state.queuedChatMessages);
    chatActionsRef.current.setModel(state.model);
    chatActionsRef.current.setSystemPrompt(state.systemPrompt);
    chatActionsRef.current.setChatLoading(state.chatLoading);
    chatActionsRef.current.setChatCancelling(state.chatCancelling);
    chatActionsRef.current.setStreamingContent(state.streamingContent);
    chatActionsRef.current.setChatResult(state.chatResult);
    chatActionsRef.current.setPendingToolCalls(state.pendingToolCalls);
    chatActionsRef.current.setPendingThread(state.pendingThread);
    chatActionsRef.current.setChatError(state.chatError);
    chatActionsRef.current.setModelFilter(state.modelFilter);
    chatActionsRef.current.setProviderFilter(state.providerFilter);
    // Fixture compat: tests still provide `chatTarget` as a derived
    // field while the chat slice owns defaultChatTarget +
    // chatTargetBySessionID. The derivation rule is:
    //   - if active session is external_agent → "external_agent"
    //   - else if active session has a per-session override → that
    //   - else the message execution-mode tail
    //   - else defaultChatTarget
    // Tests pin the *derived* field. We map it back: when there's an
    // active session and the test wants "model" or "agent", set the
    // per-session override; otherwise plumb defaultChatTarget.
    chatActionsRef.current.setDefaultChatTarget(state.chatTarget);
    if (state.activeChatSessionID && state.chatTarget !== "external_agent") {
      const overrides = new Map(state.chatTargetBySessionID);
      overrides.set(state.activeChatSessionID, state.chatTarget);
      chatActionsRef.current.setChatTargetBySessionID(overrides);
    } else {
      chatActionsRef.current.setChatTargetBySessionID(state.chatTargetBySessionID);
    }
    // Tools-enabled fixture sync: usePersistedState reads localStorage on
    // mount and tests can write to chatToolsEnabled* state directly, so
    // we mirror those into the slice each render. Without this, the
    // useChatToolsEnabled hook falls back to the slice's localStorage-
    // derived default and the test's pinned `defaultChatToolsEnabled` /
    // `chatToolsEnabledBySessionID` never reach the rendered tree.
    chatActionsRef.current.setDefaultChatToolsEnabled(state.defaultChatToolsEnabled);
    chatActionsRef.current.setChatToolsEnabledBySessionID(state.chatToolsEnabledBySessionID);

    for (const [sessionID, rows] of state.pendingApprovalsBySessionID) {
      approvalsActionsRef.current.setPendingForSession(sessionID, rows);
    }
    // Grants live entirely in slice state through loadGrants in
    // production; tests provide a direct loadGrants override that
    // returns the fixture's grants, so we don't dispatch them here.
    void approvalsActionsRef;

    retentionActionsRef.current.setSubsystems(state.retentionSubsystems);
    // Retention runs/lastRun/error/loading are seeded via the slice
    // initialState only — the slice exposes no public setters for
    // them, and the tests that care about specific values don't
    // rerender these fields.

    settingsActionsRef.current.setConfig(state.settingsConfig);
    settingsActionsRef.current.setError(state.settingsError);
    settingsActionsRef.current.setNotice(state.notice);
  }, [state]);

  return null;
}

// Mount the slice providers seeded with fixture state and wrap the
// children in an overrides context that intercepts action calls.
// View tests get the same `withRuntimeConsole(ui, {state, actions})`
// ergonomics they had before the facade was retired.
//
// FixtureSyncer mounts OUTSIDE the CoordinatorOverridesProvider so
// it reads the real slice actions (not the user-supplied overrides).
// Otherwise the syncer's dispatches would trip the test's vi.fn()
// stubs for the same slice setters and break call-count assertions.
export function withRuntimeConsole(
  ui: ReactElement,
  ctx: { state: RuntimeConsoleFixtureState; actions: RuntimeConsoleFixtureActions },
): ReactNode {
  // providerFilter is the chat slice's lone non-`usePersistedState`
  // field — it hydrates from localStorage on mount via a raw
  // `window.localStorage.getItem("hecate.providerFilter")` read. The
  // chat slice's initialState gets clobbered by that hydration if
  // the key holds a leftover from a previous test. Seed the key from
  // the fixture so the mount-time read matches what the test wants.
  if (typeof window !== "undefined") {
    window.localStorage.setItem("hecate.providerFilter", ctx.state.providerFilter);
    if (ctx.state.activeProjectID) {
      window.localStorage.setItem("hecate.project", ctx.state.activeProjectID);
    } else {
      window.localStorage.removeItem("hecate.project");
    }
  }
  const overrides: CoordinatorOverrides = {
    ...buildOverrides(ctx.actions),
    derivedChatTarget: ctx.state.chatTarget,
    derivedNewChatAgentID: ctx.state.newChatAgentID,
  };
  return (
    <RuntimeProvider initialState={runtimeInitialState(ctx.state)}>
      <UsageProvider initialState={usageInitialState(ctx.state)}>
        <ProvidersAndModelsProvider initialState={providersAndModelsInitialState(ctx.state)}>
          <ProjectsProvider initialState={projectsInitialState(ctx.state)}>
            <ChatProvider initialState={chatInitialState(ctx.state)}>
              <RetentionProvider initialState={retentionInitialState(ctx.state)}>
                <ApprovalsProvider initialState={approvalsInitialState(ctx.state)}>
                  <SettingsProvider initialState={settingsInitialState(ctx.state)}>
                    <FixtureSyncer state={ctx.state} />
                    <CoordinatorOverridesProvider value={overrides}>
                      {ui}
                    </CoordinatorOverridesProvider>
                  </SettingsProvider>
                </ApprovalsProvider>
              </RetentionProvider>
            </ChatProvider>
          </ProjectsProvider>
        </ProvidersAndModelsProvider>
      </UsageProvider>
    </RuntimeProvider>
  );
}
