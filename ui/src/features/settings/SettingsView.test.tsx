import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { getPlugins, getProjectCoordinationBackendStatus, resetSystemData } from "../../lib/api";
import { ConnectionsPanel } from "../connections/ConnectionsPanel";
import { SettingsView } from "./SettingsView";
import {
  createRuntimeConsoleActions,
  createRuntimeConsoleFixture,
} from "../../test/runtime-console-fixture";
import { withRuntimeConsole } from "../../test/runtime-console-render";

vi.mock("../../lib/api", async (importOriginal) => ({
  ...((await importOriginal()) as Record<string, unknown>),
  getPlugins: vi.fn(),
  getProjectCoordinationBackendStatus: vi.fn(),
  resetSystemData: vi.fn(),
}));

function setup(stateOverrides = {}, actionOverrides = {}) {
  const state = createRuntimeConsoleFixture(stateOverrides);
  const actions = { ...createRuntimeConsoleActions(), ...actionOverrides };
  const user = userEvent.setup();
  return { state, actions, user };
}

function capability(id: string, name: string, status = "supported") {
  return {
    id,
    name,
    status,
    description: `${name} support`,
  };
}

beforeEach(() => {
  vi.mocked(getPlugins).mockReset();
  vi.mocked(getPlugins).mockResolvedValue({ object: "plugins", data: [] });
  vi.mocked(getProjectCoordinationBackendStatus).mockReset();
  vi.mocked(getProjectCoordinationBackendStatus).mockResolvedValue({
    object: "project_coordination_backend_status",
    data: {
      configured_backend: "hecate",
      authoritative_backend: "hecate",
      storage_backend: "sqlite",
      cairnline_connector: "embedded",
      cairnline_connector_ready: true,
      cairnline_bridge_ready: true,
      cairnline_authoritative: false,
      read_model_switch_ready: false,
      write_adapter_ready: false,
      replacement_ready: false,
      replacement_mode: "disabled",
      replacement_mode_armed: false,
      status: "hecate_authoritative",
      detail:
        "Hecate-native project stores are authoritative. Cairnline bridge endpoints are available for replacement-readiness checks.",
      next_replacement_action: {
        id: "enable-cairnline-dogfood",
        label: "Enable Cairnline dogfood",
        detail:
          "Configure Cairnline as the project coordination backend in a local dogfood runtime before moving any authority.",
        target: "configuration",
      },
    },
  });
  vi.mocked(resetSystemData).mockReset();
  sessionStorage.removeItem("hecate.settingsFocus");
  sessionStorage.removeItem("hecate.connectionsFocus");
});

// Connections is now a top-level workspace; Settings keeps
// configuration that does not belong to a runtime connection surface.
// Policy and MCP Cache were removed (single-user mode dropped tenant/role
// gating and the MCP cache was pure informational stats). Usage lives
// in the Usage workspace.
describe("SettingsView", () => {
  it("renders project coordination backend status", async () => {
    vi.mocked(getProjectCoordinationBackendStatus).mockResolvedValue({
      object: "project_coordination_backend_status",
      data: {
        configured_backend: "cairnline",
        authoritative_backend: "hecate",
        storage_backend: "sqlite",
        cairnline_connector: "embedded",
        cairnline_connector_ready: true,
        cairnline_read_source: "embedded",
        cairnline_bridge_ready: true,
        cairnline_authoritative: false,
        read_model_switch_ready: true,
        write_adapter_ready: false,
        replacement_ready: false,
        replacement_target: "embedded_cairnline_first",
        replacement_target_detail:
          "Hecate's Projects replacement path targets embedded Cairnline as the first source of truth; the standalone sidecar remains an interoperability and future external-server boundary.",
        replacement_mode: "disabled",
        replacement_mode_armed: false,
        replacement_mode_detail:
          "Embedded Cairnline replacement mode is disabled; Hecate will not report Projects as replaceable without an explicit operator cutover-mode opt-in.",
        read_routes: ["project-list", "project-detail"],
        portable_write_gaps: ["agent-profiles", "memory-candidates"],
        orchestrator_capabilities: ["assignment-start"],
        migration_blockers: ["migration-cutover"],
        migration_rehearsal: {
          operation: "mirror_parity",
          import_mode: "cairnline_snapshot_import",
          snapshot_version: 3,
          source_authority: "hecate_authoritative_stores",
          target: "embedded_cairnline_sqlite",
          refreshes_target: false,
          authoritative: false,
          cutover_ready: false,
          status: "verified",
          checklist: [
            {
              id: "native-snapshot-import",
              status: "complete",
              detail: "Imported through Cairnline's versioned snapshot contract.",
            },
            {
              id: "strict-embedded-read-smoke",
              status: "complete",
              detail: "Exercised Projects routes before cutover.",
            },
            {
              id: "rollback-plan",
              status: "documented",
              detail: "Rollback is switching reads back to Hecate.",
            },
          ],
          rollback: [
            "Unset HECATE_PROJECTS_CAIRNLINE_REPLACEMENT_MODE.",
            "Switch HECATE_PROJECTS_CAIRNLINE_READ_SOURCE back to auto.",
          ],
          embedded_smoke: {
            status: "passed",
            project_count: 2,
            read_route_checks: 38,
            read_model_count: 2,
            launch_packet_count: 1,
            launch_packet_warning_count: 0,
            launch_packet_error_count: 0,
          },
        },
        write_switchpoints: [
          {
            name: "agent-profiles",
            current_authority: "hecate",
            cairnline_state: "live_mirror_non_authoritative",
            live_mirror: true,
            blocks_authority: true,
            seams: ["agent-profiles-live-mirror"],
            gap: "agent-profiles",
            detail:
              "Agent profile mutations still commit to Hecate first, then mirror portable profile metadata and execution posture into Cairnline.",
          },
          {
            name: "assignment-start-dispatch",
            current_authority: "hecate",
            cairnline_state: "result_mirror_only",
            live_mirror: true,
            blocks_authority: false,
            seams: ["project-assignment-start-result-live-mirror"],
            gap: "assignment-start",
            detail:
              "Assignment start stays Hecate-owned and mirrors committed runtime refs into Cairnline.",
          },
        ],
        next_replacement_action: {
          id: "move-portable-write-authority",
          label: "Move the next portable write authority",
          detail:
            "Close the next portable project-state gap by adding a Cairnline-authoritative switchpoint while keeping Hecate as compatibility shadow.",
          target: "agent-profiles",
          config_hints: [
            {
              env: "HECATE_PROJECTS_CAIRNLINE_WRITE_AUTHORITY",
              value: "agent-profiles",
              detail: "Enable the profile switchpoint before moving profile writes.",
            },
          ],
          probe_urls: ["/hecate/v1/projects/{id}/cairnline/read-model"],
        },
        replacement_gates: [
          {
            id: "read-routes",
            ready: true,
            status: "ready",
            detail:
              "Configured live project read families can be served from Cairnline's projected read model.",
            probe_urls: ["/hecate/v1/projects/{id}/cairnline/read-model"],
          },
          {
            id: "write-authority-switchpoints",
            ready: false,
            status: "partial",
            detail:
              "Some portable project-state mutation switchpoints are Cairnline-authoritative; remaining portable write gaps: agent-profiles, memory-candidates.",
          },
          {
            id: "migration-and-rollback",
            ready: false,
            status: "rehearsal_available",
            detail:
              "Embedded sync and project export return structured migration rehearsal evidence with rollback notes.",
            probe_urls: [
              "/hecate/v1/projects/cairnline/sync",
              "/hecate/v1/projects/cairnline/mirror-parity",
            ],
          },
          {
            id: "embedded-replacement-mode",
            ready: false,
            status: "disabled",
            detail:
              "Embedded Cairnline replacement mode is disabled until the operator arms the explicit cutover contract.",
          },
        ],
        status: "cairnline_read_routes_ready",
        detail: "Cairnline read routes are served from the read model.",
      },
    });
    const { state, actions } = setup();
    render(withRuntimeConsole(<SettingsView />, { state, actions }));

    expect(await screen.findByText("Project coordination")).toBeTruthy();
    expect(screen.getByText("Cairnline dogfood active")).toBeTruthy();
    expect(screen.getByText("reads: embedded")).toBeTruthy();
    expect(screen.getByText("cairnline configured · hecate authoritative")).toBeTruthy();
    expect(screen.getByText("cairnline read routes ready")).toBeTruthy();
    expect(screen.getByText(/2 read routes use Cairnline/i)).toBeTruthy();
    expect(screen.getByText(/Target: embedded cairnline first/i)).toBeTruthy();
    expect(screen.getByText(/standalone sidecar remains an interoperability/i)).toBeTruthy();
    expect(screen.getByText(/Mode: disabled not armed/i)).toBeTruthy();
    expect(screen.getByText(/explicit operator cutover-mode opt-in/i)).toBeTruthy();
    expect(screen.getByText("Portable write gaps")).toBeTruthy();
    expect(screen.getByText("Hecate orchestrator capabilities")).toBeTruthy();
    expect(screen.getByText("Next action")).toBeTruthy();
    expect(screen.getByText("Move the next portable write authority")).toBeTruthy();
    expect(screen.getByText("Probe checklist")).toBeTruthy();
    expect(screen.getByText("Inspect read model")).toBeTruthy();
    expect(screen.getByText("Replacement gates")).toBeTruthy();
    expect(screen.getByText("Migration rehearsal")).toBeTruthy();
    expect(screen.getByText("mirror parity")).toBeTruthy();
    expect(screen.getByText("snapshot v3")).toBeTruthy();
    expect(screen.getByText(/cairnline snapshot import/i)).toBeTruthy();
    expect(screen.getByText("native snapshot import")).toBeTruthy();
    expect(screen.getByText("strict embedded read smoke")).toBeTruthy();
    expect(screen.getByText("Rollback")).toBeTruthy();
    expect(screen.getByText(/Unset HECATE_PROJECTS_CAIRNLINE_REPLACEMENT_MODE/i)).toBeTruthy();
    expect(screen.getByText("embedded smoke passed")).toBeTruthy();
    expect(screen.getByText("38 route checks")).toBeTruthy();
    expect(screen.getByText("Write switchpoints")).toBeTruthy();
    expect(screen.getByText("agent profiles")).toBeTruthy();
    expect(screen.getByText("live mirror non authoritative")).toHaveClass("badge-amber");
    expect(screen.getByText("blocks authority")).toBeTruthy();
    expect(screen.getByText("gap agent-profiles")).toBeTruthy();
    expect(screen.getByText("agent-profiles-live-mirror")).toBeTruthy();
    expect(screen.getByText("assignment start dispatch")).toBeTruthy();
    expect(screen.getByText("result mirror only")).toBeTruthy();
    expect(screen.getByText("non-blocking")).toBeTruthy();
    expect(screen.getByText("project-assignment-start-result-live-mirror")).toBeTruthy();
    expect(screen.getByText("read routes")).toBeTruthy();
    expect(screen.getByText("write authority switchpoints")).toBeTruthy();
    expect(screen.getByText("migration and rollback")).toBeTruthy();
    expect(screen.getByText("partial")).toBeTruthy();
    expect(screen.getByText("rehearsal available")).toBeTruthy();
    expect(screen.getAllByText("agent-profiles").length).toBeGreaterThanOrEqual(1);
    expect(
      screen.getByText("HECATE_PROJECTS_CAIRNLINE_WRITE_AUTHORITY=agent-profiles"),
    ).toBeTruthy();
    expect(screen.getByText("Configuration hints")).toBeTruthy();
    expect(
      screen.getByText("Enable the profile switchpoint before moving profile writes."),
    ).toBeTruthy();
    expect(screen.getAllByText("1 probe").length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("/hecate/v1/projects/{id}/cairnline/read-model").length).toBe(2);
    expect(screen.getByText("/hecate/v1/projects/cairnline/sync")).toBeTruthy();
    expect(screen.getByText("/hecate/v1/projects/cairnline/mirror-parity")).toBeTruthy();
    expect(screen.getByText("POST")).toBeTruthy();
    expect(screen.getByText("memory-candidates")).toBeTruthy();
    expect(screen.getByText("assignment-start")).toBeTruthy();
    expect(screen.getByText("migration-cutover")).toBeTruthy();
  });

  it("labels backend-status probes in the project backend checklist", async () => {
    vi.mocked(getProjectCoordinationBackendStatus).mockResolvedValue({
      object: "project_coordination_backend_status",
      data: {
        configured_backend: "hecate",
        authoritative_backend: "hecate",
        storage_backend: "sqlite",
        cairnline_connector: "embedded",
        cairnline_connector_ready: true,
        cairnline_bridge_ready: true,
        cairnline_authoritative: false,
        read_model_switch_ready: false,
        write_adapter_ready: false,
        replacement_ready: false,
        replacement_mode: "disabled",
        replacement_mode_armed: false,
        status: "hecate_authoritative",
        detail:
          "Hecate-native project stores are authoritative. Cairnline bridge endpoints are available for replacement-readiness checks.",
        next_replacement_action: {
          id: "enable-cairnline-dogfood",
          label: "Enable Cairnline dogfood",
          detail:
            "Configure Cairnline as the project coordination backend in a local dogfood runtime before moving any authority.",
          target: "configuration",
          probes: [
            {
              method: "GET",
              url: "/hecate/v1/projects/backend-status",
            },
          ],
        },
      },
    });
    const { state, actions } = setup();
    render(withRuntimeConsole(<SettingsView />, { state, actions }));

    expect(await screen.findByText("Enable Cairnline dogfood")).toBeTruthy();
    expect(screen.getByText("Probe checklist")).toBeTruthy();
    expect(screen.getByText("Verify backend status")).toBeTruthy();
    expect(screen.getByText(/reports the expected connector and next action/i)).toBeTruthy();
    expect(screen.getByText("/hecate/v1/projects/backend-status")).toBeTruthy();
  });

  it("shows strict embedded smoke probe routes in the next project backend action", async () => {
    vi.mocked(getProjectCoordinationBackendStatus).mockResolvedValue({
      object: "project_coordination_backend_status",
      data: {
        configured_backend: "cairnline",
        authoritative_backend: "hecate",
        storage_backend: "sqlite",
        cairnline_connector: "embedded",
        cairnline_connector_ready: true,
        cairnline_read_source: "embedded",
        cairnline_bridge_ready: true,
        cairnline_authoritative: false,
        read_model_switch_ready: true,
        write_adapter_ready: true,
        replacement_ready: false,
        replacement_mode: "disabled",
        replacement_mode_armed: false,
        portable_write_gaps: [],
        orchestrator_capabilities: ["assignment-start"],
        migration_blockers: ["migration-cutover"],
        next_replacement_action: {
          id: "run-strict-embedded-read-smoke",
          label: "Run strict embedded read smoke",
          detail:
            "Portable write authority is clear; verify the embedded Cairnline mirror and strict read-smoke evidence before treating migration cutover as the next blocker.",
          target: "strict-embedded-read-smoke",
          config_hints: [
            {
              env: "HECATE_PROJECTS_CAIRNLINE_READ_SOURCE",
              value: "embedded",
            },
          ],
          probes: [
            {
              method: "POST",
              url: "/hecate/v1/projects/cairnline/sync",
            },
            {
              method: "GET",
              url: "/hecate/v1/projects/cairnline/mirror-parity",
            },
          ],
          probe_urls: [
            "/hecate/v1/projects/cairnline/sync",
            "/hecate/v1/projects/cairnline/mirror-parity",
          ],
        },
        replacement_gates: [
          {
            id: "strict-embedded-read-smoke",
            ready: false,
            status: "operator_probe_required",
            detail:
              "Run the embedded sync/parity/smoke probes with HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=embedded before treating the mirror database as a cutover candidate.",
            probes: [
              {
                method: "POST",
                url: "/hecate/v1/projects/cairnline/sync",
              },
              {
                method: "GET",
                url: "/hecate/v1/projects/cairnline/mirror-parity",
              },
            ],
            probe_urls: [
              "/hecate/v1/projects/cairnline/sync",
              "/hecate/v1/projects/cairnline/mirror-parity",
            ],
          },
        ],
        status: "cairnline_read_routes_ready",
        detail: "Cairnline read routes are served from the read model.",
      },
    });
    const { state, actions } = setup();
    render(withRuntimeConsole(<SettingsView />, { state, actions }));

    expect(await screen.findByText("Run strict embedded read smoke")).toBeTruthy();
    expect(screen.getByText("strict-embedded-read-smoke")).toBeTruthy();
    expect(screen.getByText("Probe checklist")).toBeTruthy();
    expect(screen.getByText(/Run these routes in order/i)).toBeTruthy();
    expect(screen.getByText("Step 1")).toBeTruthy();
    expect(screen.getByText("Rebuild embedded mirror")).toBeTruthy();
    expect(screen.getByText(/Refresh the Cairnline mirror/i)).toBeTruthy();
    expect(screen.getByText("Step 2")).toBeTruthy();
    expect(screen.getByText("Verify mirror parity")).toBeTruthy();
    expect(screen.getByText(/Compare the existing embedded mirror/i)).toBeTruthy();
    expect(screen.getAllByText("Probe routes").length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("POST").length).toBe(2);
    expect(screen.getAllByText("GET").length).toBe(2);
    expect(screen.getAllByText("/hecate/v1/projects/cairnline/sync").length).toBe(2);
    expect(screen.getAllByText("/hecate/v1/projects/cairnline/mirror-parity").length).toBe(2);
    expect(screen.getAllByRole("button", { name: "copy" }).length).toBeGreaterThanOrEqual(2);
    expect(screen.getByText("HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=embedded")).toBeTruthy();
  });

  it("renders authoritative Cairnline status with runtime boundary warnings", async () => {
    vi.mocked(getProjectCoordinationBackendStatus).mockResolvedValue({
      object: "project_coordination_backend_status",
      data: {
        configured_backend: "cairnline",
        authoritative_backend: "cairnline",
        storage_backend: "sqlite",
        cairnline_connector: "embedded",
        cairnline_connector_ready: true,
        cairnline_read_source: "embedded",
        cairnline_bridge_ready: true,
        cairnline_authoritative: true,
        read_model_switch_ready: true,
        write_adapter_ready: true,
        replacement_ready: true,
        replacement_target: "embedded_cairnline_first",
        replacement_mode: "embedded",
        replacement_mode_armed: true,
        read_routes: ["project-list"],
        portable_write_gaps: [],
        orchestrator_capabilities: ["assignment-start"],
        migration_blockers: [],
        warnings: [
          "Hecate still owns runtime/workspace side effects such as task/chat execution.",
          "Remaining Hecate-owned orchestrator capabilities: assignment-start.",
        ],
        next_replacement_action: {
          id: "monitor-cairnline-backend",
          label: "Monitor Cairnline backend",
          detail:
            "All Cairnline replacement gates are ready and embedded replacement mode is armed; Projects are reporting Cairnline as authoritative.",
          target: "cairnline",
        },
        replacement_gates: [
          {
            id: "read-routes",
            ready: true,
            status: "ready",
            detail: "Configured live project read families are served from Cairnline.",
          },
        ],
        status: "cairnline_authoritative",
        detail:
          "All Cairnline replacement gates are ready and embedded replacement mode is armed; Hecate is reporting Cairnline as authoritative for portable Projects coordination state.",
      },
    });
    const { state, actions } = setup();
    render(withRuntimeConsole(<SettingsView />, { state, actions }));

    expect(await screen.findByText("Cairnline owns portable project state")).toBeTruthy();
    expect(screen.getByText("cairnline configured · cairnline authoritative")).toBeTruthy();
    expect(screen.getByText("cairnline authoritative")).toBeTruthy();
    expect(screen.getByText(/Hecate is reporting Cairnline as authoritative/i)).toBeTruthy();
    expect(screen.getByText("Runtime boundary")).toBeTruthy();
    expect(screen.getByText(/Hecate still owns runtime\/workspace side effects/i)).toBeTruthy();
    expect(screen.getByText(/Remaining Hecate-owned orchestrator capabilities/i)).toBeTruthy();
    expect(screen.getByText("Monitor Cairnline backend")).toBeTruthy();
  });

  it("shows project backend load failures without hiding maintenance", async () => {
    vi.mocked(getProjectCoordinationBackendStatus).mockRejectedValue(
      new Error("backend status unavailable"),
    );
    const { state, actions } = setup();
    render(withRuntimeConsole(<SettingsView />, { state, actions }));

    expect(await screen.findByText("backend status unavailable")).toBeTruthy();
    expect(screen.getByText("Maintenance")).toBeTruthy();
  });

  it("renders maintenance cleanup without legacy tabs", () => {
    const { state, actions } = setup();
    render(withRuntimeConsole(<SettingsView />, { state, actions }));
    expect(screen.getByText("Maintenance")).toBeTruthy();
    expect(screen.getByText(/Clean up old local runtime data/i)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Retention" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Pricing" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Connections" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Model capabilities" })).toBeNull();
  });

  it("starts on the cleanup controls", () => {
    const { state, actions } = setup();
    render(withRuntimeConsole(<SettingsView />, { state, actions }));
    expect(screen.getByText(/Run cleanup/i)).toBeTruthy();
    expect(screen.getByRole("button", { name: /Clean up now/i })).toBeTruthy();
  });

  it("triggers a retention-runs fetch on mount", () => {
    // Retention runs are no longer in the boot-time dashboard
    // snapshot — the view is responsible for asking once it's on
    // screen. Without this effect the list stays empty forever.
    const loadRetentionRuns = vi.fn().mockResolvedValue(undefined);
    const { state, actions } = setup({}, { loadRetentionRuns });
    render(withRuntimeConsole(<SettingsView />, { state, actions }));
    expect(loadRetentionRuns).toHaveBeenCalledTimes(1);
  });

  it("renders plugin registry records for operator inspection", async () => {
    vi.mocked(getPlugins).mockResolvedValue({
      object: "plugins",
      data: [
        {
          id: "github",
          name: "GitHub",
          description: "Read and link GitHub work.",
          version: "0.1.0",
          source_kind: "local_path",
          source_ref: "/plugins/github/plugin.json",
          manifest_schema_version: "hecate.plugin.v0",
          manifest_digest: "sha256:abc",
          requested_permissions: [{ value: "network:github.com", classification: "advisory" }],
          registry_state: "valid",
          enabled: false,
          warnings: [],
          capabilities: [
            {
              id: "issues",
              kind: "connector",
              display_name: "Issues",
              requested_permissions: [{ value: "secret:github_token", classification: "advisory" }],
              enabled: true,
            },
            {
              id: "github",
              kind: "mcp_server",
              display_name: "GitHub MCP",
              enabled: true,
              mcp_server: {
                name: "github",
                transport: "stdio",
                command: "npx",
                args: ["-y", "@modelcontextprotocol/server-github"],
                env: { GITHUB_TOKEN: "$GITHUB_TOKEN" },
                approval_policy: "require_approval",
              },
            },
          ],
          auth: [
            {
              capability_id: "issues",
              requested_name: "github_token",
              kind: "token",
              status: "unknown",
            },
          ],
          installed_at: "2026-06-18T10:00:00Z",
          updated_at: "2026-06-18T10:00:00Z",
        },
      ],
    });
    const { state, actions } = setup();
    render(withRuntimeConsole(<SettingsView />, { state, actions }));

    expect(await screen.findByText("GitHub")).toBeTruthy();
    expect(screen.getByText("github@0.1.0")).toBeTruthy();
    expect(screen.getByText(/2 capabilities/i)).toBeTruthy();
    expect(
      screen.getByText(/MCP github · stdio: npx -y @modelcontextprotocol\/server-github/i),
    ).toBeTruthy();
    expect(screen.getByText(/Unresolved auth: github_token/i)).toBeTruthy();
  });
});

describe("SettingsView maintenance cleanup", () => {
  it("shows known subsystems as toggle chips", async () => {
    const { state, actions } = setup();
    render(withRuntimeConsole(<SettingsView />, { state, actions }));
    for (const sub of ["Trace snapshots", "Usage events", "Audit events"]) {
      expect(await screen.findByText(sub)).toBeTruthy();
    }
  });

  it("clicking a chip calls setRetentionSubsystems", async () => {
    const setRetentionSubsystems = vi.fn();
    const { state, actions, user } = setup({}, { setRetentionSubsystems });
    render(withRuntimeConsole(<SettingsView />, { state, actions }));
    await user.click(await screen.findByText("Audit events"));
    expect(setRetentionSubsystems).toHaveBeenCalledWith("audit_events");
  });

  it("'Clean up now' button triggers runRetention action", async () => {
    const runRetention = vi.fn(async () => undefined);
    const { state, actions, user } = setup({}, { runRetention });
    render(withRuntimeConsole(<SettingsView />, { state, actions }));
    await user.click(await screen.findByRole("button", { name: /Clean up now/i }));
    expect(runRetention).toHaveBeenCalled();
  });

  it("handles partial retention run payloads without results", async () => {
    const { state, actions } = setup({
      retentionLastRun: {
        finished_at: new Date().toISOString(),
        trigger: "manual",
      },
    });
    render(withRuntimeConsole(<SettingsView />, { state, actions }));

    expect(await screen.findByText(/Last run/i)).toBeTruthy();
    expect(screen.getByText("0 removed")).toBeTruthy();
  });

  it("labels the reset affordance as runtime-only on memory backend", async () => {
    const { state, actions, user } = setup({
      settingsConfig: { backend: "memory", providers: [], policy_rules: [], events: [] },
    });
    render(withRuntimeConsole(<SettingsView />, { state, actions }));

    expect(screen.getByText("Reset runtime state")).toBeTruthy();
    expect(screen.getByText(/current in-memory state/i)).toBeTruthy();
    await user.click(await screen.findByRole("button", { name: /Reset/i }));

    expect(screen.getByText(/memory storage/i)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Reset runtime state" })).toBeDisabled();
  });

  it("labels the reset affordance as local data cleanup on sqlite backend", async () => {
    const { state, actions } = setup({
      settingsConfig: { backend: "sqlite", providers: [], policy_rules: [], events: [] },
    });
    render(withRuntimeConsole(<SettingsView />, { state, actions }));

    expect(screen.getByText("Reset local data")).toBeTruthy();
    expect(screen.getByText(/remaining Hecate database rows/i)).toBeTruthy();
  });

  it("resets local data after typed confirmation and refreshes dashboard state", async () => {
    vi.mocked(resetSystemData).mockResolvedValue({
      object: "system_reset",
      data: {
        projects_deleted: 1,
        project_skills_deleted: 1,
        project_work_rows_deleted: 2,
        project_assistant_proposals_deleted: 1,
        plugins_deleted: 1,
        agent_profiles_deleted: 1,
        chat_sessions_deleted: 2,
        tasks_deleted: 1,
        providers_deleted: 1,
        policy_rules_deleted: 1,
        agent_approval_grants_deleted: 1,
        database_rows_deleted: 3,
        cairnline_mirror_files_deleted: 1,
      },
    });
    const loadDashboard = vi.fn(async () => undefined);
    const { state, actions, user } = setup(
      { settingsConfig: { backend: "sqlite", providers: [], policy_rules: [], events: [] } },
      { loadDashboard },
    );
    render(withRuntimeConsole(<SettingsView />, { state, actions }));

    await user.click(await screen.findByRole("button", { name: /Reset/i }));
    const confirm = screen.getByRole("button", { name: "Reset local data" });
    expect(confirm).toBeDisabled();

    await user.type(screen.getByLabelText(/Type RESET/i), "RESET");
    expect(confirm).toBeEnabled();
    await user.click(confirm);

    await waitFor(() => expect(resetSystemData).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(loadDashboard).toHaveBeenCalledTimes(1));
  });
});

// Usage rendering lives in the Usage workspace; Settings intentionally stays
// focused on retention.

describe("Connections external-agent panel", () => {
  it("summarizes model provider connections and links to Connections when requested", async () => {
    const onNavigate = vi.fn();
    const { state, actions, user } = setup({
      settingsConfig: {
        backend: "memory",
        providers: [
          {
            id: "ollama",
            name: "Ollama",
            preset_id: "ollama",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:11434/v1",
            credential_configured: false,
          },
          {
            id: "anthropic",
            name: "Anthropic",
            preset_id: "anthropic",
            kind: "cloud",
            protocol: "anthropic",
            base_url: "https://api.anthropic.com",
            credential_configured: true,
          },
        ],
        policy_rules: [],
        events: [],
      },
      providers: [
        {
          name: "ollama",
          kind: "local",
          healthy: true,
          status: "healthy",
          routing_ready: true,
          model_count: 3,
        },
        {
          name: "anthropic",
          kind: "cloud",
          healthy: false,
          status: "unhealthy",
          routing_ready: false,
          readiness: { status: "blocked", reason: "missing_credential" },
        },
      ],
      models: [
        { id: "llama3", owned_by: "ollama" },
        { id: "claude-sonnet", owned_by: "anthropic" },
      ],
    });
    render(withRuntimeConsole(<ConnectionsPanel onNavigate={onNavigate} />, { state, actions }));

    const card = await screen.findByTestId("connections-model-providers");
    expect(within(card).getByText("Model providers")).toBeTruthy();
    expect(within(card).getByText("2 configured")).toBeTruthy();
    expect(within(card).getByText("Ready")).toBeTruthy();
    expect(within(card).getByText("Needs attention")).toBeTruthy();
    expect(within(card).getByTestId("connections-provider-repair")).toHaveTextContent(
      "Next repair",
    );
    expect(within(card).getByTestId("connections-provider-repair")).toHaveTextContent(
      "Provider blocked",
    );

    await user.click(within(card).getByRole("button", { name: "Open Connections" }));
    expect(onNavigate).toHaveBeenCalledWith("connections");
  });

  it("fires listChatGrants when the tab opens", async () => {
    const listChatGrants = vi.fn(async () => undefined);
    const { state, actions } = setup({}, { listChatGrants });
    render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));
    expect(listChatGrants).toHaveBeenCalled();
  });

  it("renders the empty-state copy when there are no grants", async () => {
    const { state, actions } = setup();
    render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));
    expect(await screen.findByTestId("external-agents-empty")).toBeTruthy();
  });

  it("renders one row per grant with adapter / tool / decision metadata", async () => {
    const { state, actions } = setup({
      chatGrants: [
        {
          id: "g-1",
          scope: "session",
          adapter_id: "codex",
          tool_kind: "fs",
          decision: "approve",
          granted_by: "operator",
          granted_at: "2026-04-21T10:00:00Z",
        },
        {
          id: "g-2",
          scope: "adapter_tool",
          adapter_id: "claude-code",
          tool_kind: "exec",
          decision: "deny",
          granted_by: "operator",
          granted_at: "2026-04-21T10:01:00Z",
          expires_at: "2026-05-01T10:00:00Z",
        },
      ],
    });
    render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));
    expect(await screen.findByTestId("external-agents-list")).toBeTruthy();
    // Scope decision-tone assertions to row content so they don't
    // accidentally match the section description above.
    const approveRow = screen.getByTestId("external-agents-row-g-1");
    expect(within(approveRow).getByText(/always approve/i)).toBeTruthy();
    const denyRow = screen.getByTestId("external-agents-row-g-2");
    expect(within(denyRow).getByText(/always deny/i)).toBeTruthy();
  });

  it("shows logout for logout-capable local adapters", async () => {
    const logoutAgentAdapter = vi.fn(async () => true);
    const { state, actions, user } = setup(
      {
        agentAdapters: [
          {
            id: "codex",
            name: "Codex",
            kind: "acp",
            command: "codex-acp-adapter",
            available: true,
            status: "available",
            auth_status: "ok",
            supports_authenticate: true,
            supports_logout: true,
          },
          {
            id: "cursor_agent",
            name: "Cursor Agent",
            kind: "acp",
            command: "cursor-agent",
            available: true,
            status: "available",
            auth_status: "ok",
            supports_authenticate: false,
            supports_logout: false,
          },
        ],
      },
      { logoutAgentAdapter },
    );
    render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));

    await user.click(await screen.findByRole("button", { name: "Sign out Codex" }));

    expect(logoutAgentAdapter).toHaveBeenCalledWith("codex");
    expect(screen.queryByRole("button", { name: "Sign out Cursor Agent" })).toBeNull();
  });

  it("shows authenticate for local adapters that need sign-in", async () => {
    const authenticateAgentAdapter = vi.fn(async () => true);
    const { state, actions, user } = setup(
      {
        agentAdapters: [
          {
            id: "codex",
            name: "Codex",
            kind: "acp",
            command: "codex-acp-adapter",
            available: true,
            status: "available",
            auth_status: "unauthenticated",
            supports_authenticate: true,
            supports_logout: true,
          },
          {
            id: "cursor_agent",
            name: "Cursor Agent",
            kind: "acp",
            command: "cursor-agent",
            available: true,
            status: "available",
            auth_status: "unauthenticated",
            supports_authenticate: false,
            supports_logout: false,
          },
        ],
      },
      { authenticateAgentAdapter },
    );
    render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));

    await user.click(await screen.findByRole("button", { name: "Sign in Codex" }));

    expect(authenticateAgentAdapter).toHaveBeenCalledWith("codex");
    expect(screen.queryByRole("button", { name: "Sign in Cursor Agent" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Sign out Codex" })).toBeNull();
  });

  it("uses live probe capabilities for authenticate visibility", async () => {
    const authenticateAgentAdapter = vi.fn(async () => true);
    const { state, actions, user } = setup(
      {
        agentAdapters: [
          {
            id: "codex",
            name: "Codex",
            kind: "acp",
            command: "codex-acp-adapter",
            available: true,
            status: "available",
            auth_status: "unauthenticated",
            supports_authenticate: true,
            supports_logout: true,
          },
          {
            id: "cursor_agent",
            name: "Cursor Agent",
            kind: "acp",
            command: "cursor-agent",
            available: true,
            status: "available",
            auth_status: "unauthenticated",
            supports_authenticate: false,
            supports_logout: false,
          },
        ],
        agentAdapterHealthByID: new Map([
          [
            "codex",
            {
              adapter_id: "codex",
              status: "auth_required",
              stage: "initialize",
              capabilities_known: true,
              supports_authenticate: false,
              supports_logout: false,
              supports_load_session: true,
              duration_ms: 42,
            },
          ],
          [
            "cursor_agent",
            {
              adapter_id: "cursor_agent",
              status: "auth_required",
              stage: "initialize",
              capabilities_known: true,
              supports_authenticate: true,
              supports_logout: true,
              supports_load_session: true,
              duration_ms: 41,
            },
          ],
        ]),
      },
      { authenticateAgentAdapter },
    );
    render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));

    expect(screen.queryByRole("button", { name: "Sign in Codex" })).toBeNull();
    await user.click(await screen.findByRole("button", { name: "Sign in Cursor Agent" }));
    expect(authenticateAgentAdapter).toHaveBeenCalledWith("cursor_agent");
  });

  it("shows the ACP capability matrix and applies live auth capability overrides", async () => {
    const { state, actions } = setup({
      agentAdapters: [
        {
          id: "codex",
          name: "Codex",
          kind: "acp",
          command: "codex-acp-adapter",
          available: true,
          status: "available",
          auth_status: "ok",
          supports_authenticate: true,
          supports_logout: true,
          capabilities: [
            capability("prompt_session", "sessions"),
            capability("cancel", "cancel"),
            capability("permissions", "permissions"),
            capability("config_options", "config", "adapter_dependent"),
            capability("terminal_rpc", "terminal RPC", "operator_opt_in"),
            capability("authenticate", "login"),
            capability("logout", "logout"),
          ],
        },
      ],
      agentAdapterHealthByID: new Map([
        [
          "codex",
          {
            adapter_id: "codex",
            status: "ready",
            stage: "ready",
            capabilities_known: true,
            supports_authenticate: false,
            supports_logout: false,
            supports_load_session: true,
            duration_ms: 42,
          },
        ],
      ]),
    });
    render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));

    const row = await screen.findByTestId("external-agents-adapter-codex");
    const capabilities = within(row).getByTestId("external-agents-adapter-codex-capabilities");
    expect(within(capabilities).getByText("sessions")).toBeTruthy();
    expect(within(capabilities).getByText("permissions")).toBeTruthy();
    expect(within(capabilities).getByText(/config/)).toHaveTextContent("if advertised");
    expect(within(capabilities).getByText(/terminal RPC/)).toHaveTextContent("opt-in");
    expect(
      within(row).getByTestId("external-agents-adapter-codex-terminal-policy"),
    ).toHaveTextContent("HECATE_AGENT_ADAPTER_TERMINALS=1");
    expect(
      within(row).getByTestId("external-agents-adapter-codex-terminal-policy"),
    ).not.toHaveTextContent("HECATE_REMOTE_ALLOW_ACP_TERMINALS");
    expect(within(capabilities).queryByText("login")).toBeNull();
    expect(within(capabilities).queryByText("logout")).toBeNull();
  });

  it("revoke asks for inline confirmation before deleting the grant", async () => {
    const deleteChatGrant = vi.fn(async () => true);
    const { state, actions, user } = setup(
      {
        chatGrants: [
          {
            id: "g-7",
            scope: "session",
            adapter_id: "codex",
            tool_kind: "fs",
            decision: "approve",
            granted_at: "2026-04-21T10:00:00Z",
          },
        ],
      },
      { deleteChatGrant },
    );
    render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));
    await user.click(await screen.findByTestId("external-agents-revoke-g-7"));
    expect(deleteChatGrant).not.toHaveBeenCalled();
    await user.click(await screen.findByTestId("external-agents-confirm-revoke-g-7"));
    expect(deleteChatGrant).toHaveBeenCalledWith("g-7");
  });

  it("revoke confirmation can be cancelled inline", async () => {
    const deleteChatGrant = vi.fn(async () => true);
    const { state, actions, user } = setup(
      {
        chatGrants: [
          {
            id: "g-8",
            scope: "session",
            adapter_id: "codex",
            tool_kind: "fs",
            decision: "approve",
            granted_at: "2026-04-21T10:00:00Z",
          },
        ],
      },
      { deleteChatGrant },
    );
    render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));
    await user.click(await screen.findByTestId("external-agents-revoke-g-8"));
    expect(await screen.findByTestId("external-agents-confirm-revoke-g-8")).toBeTruthy();
    await user.click(await screen.findByTestId("external-agents-cancel-revoke-g-8"));
    expect(deleteChatGrant).not.toHaveBeenCalled();
    expect(screen.queryByTestId("external-agents-confirm-revoke-g-8")).toBeNull();
  });

  it("surfaces the listing error inline when the load fails", async () => {
    const { state, actions } = setup({
      chatGrantsError: "list failed: 500",
    });
    render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));
    expect(await screen.findByText(/list failed: 500/)).toBeTruthy();
  });

  it("keeps the Anthropic provider key card visible through transient settings refreshes", async () => {
    const { state, actions } = setup({
      settingsConfig: {
        backend: "memory",
        providers: [
          {
            id: "anthropic",
            name: "Anthropic",
            preset_id: "anthropic",
            kind: "cloud",
            protocol: "anthropic",
            base_url: "https://api.anthropic.com",
            credential_configured: true,
          },
        ],
        policy_rules: [],
        events: [],
      },
    });
    const { rerender } = render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));

    expect(await screen.findByTestId("anthropic-provider-key-card")).toBeTruthy();

    rerender(
      withRuntimeConsole(<ConnectionsPanel />, {
        state: { ...state, settingsConfig: { ...state.settingsConfig!, providers: [] } },
        actions,
      }),
    );

    expect(screen.getByTestId("anthropic-provider-key-card")).toBeTruthy();
  });

  it("saves and clears the Anthropic provider key from Connections settings", async () => {
    const setProviderAPIKey = vi.fn(async () => undefined);
    const { state, actions, user } = setup(
      {
        settingsConfig: {
          backend: "memory",
          providers: [
            {
              id: "anthropic",
              name: "Anthropic",
              preset_id: "anthropic",
              kind: "cloud",
              protocol: "anthropic",
              base_url: "https://api.anthropic.com",
              credential_configured: true,
            },
          ],
          policy_rules: [],
          events: [],
        },
      },
      { setProviderAPIKey },
    );
    render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));

    await user.type(await screen.findByLabelText("Anthropic API key"), "sk-ant-new");
    await user.click(screen.getByRole("button", { name: "Update key" }));
    await user.click(screen.getByRole("button", { name: "Remove" }));

    expect(setProviderAPIKey).toHaveBeenNthCalledWith(1, "anthropic", "sk-ant-new");
    expect(setProviderAPIKey).toHaveBeenNthCalledWith(2, "anthropic", "");
  });

  // External agent status panel — surfaces readiness diagnostics.
  // Direct adapter binaries can be checked quietly.
  // The section is hidden when no agents are registered (no point
  // showing an empty card); otherwise each row renders inline
  // diagnostic copy when a result exists.
  describe("adapter status panel", () => {
    function withAdapter(overrides: Record<string, unknown> = {}) {
      return {
        agentAdapters: [
          {
            id: "codex",
            name: "Codex",
            kind: "acp",
            command: "codex-acp-adapter",
            available: true,
            status: "available",
            cost_mode: "external",
          },
        ],
        ...overrides,
      };
    }

    it("hides the panel when no adapters are registered", async () => {
      const { state, actions } = setup({ agentAdapters: [] });
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));
      expect(screen.queryByTestId("external-agents-adapters")).toBeNull();
    });

    it("renders one row per adapter with a visible check action", async () => {
      const { state, actions } = setup(withAdapter());
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));
      expect(await screen.findByTestId("external-agents-adapters")).toBeTruthy();
      expect(screen.getByTestId("external-agents-adapter-codex")).toBeTruthy();
      expect(screen.getByTestId("external-agents-test-codex")).toHaveTextContent("Check");
    });

    it("checks available local adapters when Connections opens", async () => {
      const probeAgentAdapter = vi.fn(async () => null);
      const { state, actions } = setup(withAdapter(), { probeAgentAdapter });

      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));

      await waitFor(() => expect(probeAgentAdapter).toHaveBeenCalledWith("codex"));
      expect(probeAgentAdapter).toHaveBeenCalledTimes(1);
    });

    it("shows bridge and underlying agent versions separately", async () => {
      const { state, actions } = setup(
        withAdapter({
          agentAdapters: [
            {
              id: "codex",
              name: "Codex",
              kind: "acp",
              command: "codex-acp-adapter",
              available: true,
              status: "available",
              cost_mode: "external",
              adapter_version: "1.2.3",
              agent_version: "0.48.0",
            },
          ],
        }),
      );
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));

      const row = await screen.findByTestId("external-agents-adapter-codex");
      expect(row).toHaveTextContent("bridge 1.2.3");
      expect(row).toHaveTextContent("agent 0.48.0");
    });

    it("renders compact local sign-in when the cached probe says auth is missing", async () => {
      const { state, actions } = setup(
        withAdapter({
          agentAdapterHealthByID: new Map([
            [
              "codex",
              {
                adapter_id: "codex",
                status: "auth_required",
                stage: "initialize",
                path: "/usr/local/bin/codex-acp-adapter",
                error: "Authentication required",
                hint: "Run codex login",
                duration_ms: 412,
              },
            ],
          ]),
        }),
      );
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));
      const row = await screen.findByTestId("external-agents-adapter-codex");
      expect(within(row).getByText("sign in")).toBeTruthy();
      expect(within(row).getByText("Local sign-in")).toBeTruthy();
      expect(within(row).getByText("codex login")).toBeTruthy();
      expect(screen.queryByTestId("external-agents-adapter-codex-detail")).toBeNull();
      expect(screen.queryByTestId("external-agents-adapter-codex-auth-warning")).toBeNull();
      expect(row).not.toHaveTextContent("path /usr/local/bin/codex-acp-adapter");
      expect(row).not.toHaveTextContent("412 ms");
      expect(row).not.toHaveTextContent("auth unknown");
    });

    it("shows live load-session capability from a ready probe", async () => {
      const { state, actions } = setup(
        withAdapter({
          agentAdapterHealthByID: new Map([
            [
              "codex",
              {
                adapter_id: "codex",
                status: "ready",
                stage: "ready",
                path: "/usr/local/bin/codex-acp-adapter",
                capabilities_known: true,
                supports_authenticate: true,
                supports_logout: true,
                supports_load_session: true,
                duration_ms: 73,
              },
            ],
          ]),
        }),
      );
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));
      const row = await screen.findByTestId("external-agents-adapter-codex");
      expect(row).toHaveTextContent("load session yes");
    });

    it("explains local ACP auth actions are disabled in remote runtime", async () => {
      const { state, actions } = setup(
        withAdapter({
          sessionInfo: {
            role: "operator",
            remote_identity: {
              actor_id: "actor_1",
              org_id: "org_1",
              project_id: "project_1",
              runtime_id: "runtime_1",
            },
          },
          agentAdapters: [
            {
              id: "codex",
              name: "Codex",
              kind: "acp",
              command: "codex-acp-adapter",
              available: true,
              status: "available",
              cost_mode: "external",
              supports_authenticate: true,
              supports_logout: true,
              capabilities: [capability("terminal_rpc", "terminal RPC", "operator_opt_in")],
            },
          ],
        }),
      );
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));

      const row = await screen.findByTestId("external-agents-adapter-codex");
      expect(
        within(row).getByTestId("external-agents-adapter-codex-auth-policy"),
      ).toHaveTextContent("Local ACP login/logout actions are disabled in remote runtime");
      expect(
        within(row).getByTestId("external-agents-adapter-codex-terminal-policy"),
      ).toHaveTextContent("HECATE_REMOTE_ALLOW_ACP_TERMINALS=1");
      expect(within(row).queryByRole("button", { name: /Sign in Codex/i })).toBeNull();
      expect(within(row).queryByRole("button", { name: /Sign out Codex/i })).toBeNull();
    });

    it("shows missing adapters as setup notifications", async () => {
      const { state, actions } = setup(
        withAdapter({
          agentAdapters: [
            {
              id: "codex",
              name: "Codex",
              kind: "acp",
              command: "codex-acp-adapter",
              available: true,
              status: "available",
              cost_mode: "external",
              auth_status: "unknown",
            },
            {
              id: "cursor_agent",
              name: "Cursor Agent",
              kind: "acp",
              command: "cursor-agent",
              available: true,
              status: "available",
              cost_mode: "external",
              auth_status: "unknown",
            },
          ],
          agentAdapterHealthByID: new Map([
            [
              "codex",
              {
                adapter_id: "codex",
                status: "not_installed",
                stage: "lookup",
                error: "codex-acp-adapter command was not found",
                hint: "Install Codex and ensure codex-acp-adapter is on PATH.",
                duration_ms: 0,
              },
            ],
            [
              "cursor_agent",
              {
                adapter_id: "cursor_agent",
                status: "error",
                stage: "ready",
                path: "dev-override://cursor_agent",
                error: "forced app CLI missing by HECATE_AGENT_ADAPTER_DEV_OVERRIDES",
                hint: "Install Cursor with Agent support, then sign in with Cursor Agent.",
                duration_ms: 0,
              },
            ],
          ]),
        }),
      );
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));

      const codex = await screen.findByTestId("external-agents-adapter-codex");
      expect(within(codex).getByText("not configured")).toBeTruthy();
      expect(codex).toHaveTextContent(
        "Set up to use: Install Codex and ensure codex-acp-adapter is on PATH.",
      );
      expect(codex).not.toHaveTextContent("not installed");
      expect(codex).not.toHaveTextContent("auth unknown");
      expect(codex).not.toHaveTextContent("0 ms");

      const cursor = await screen.findByTestId("external-agents-adapter-cursor_agent");
      expect(within(cursor).getByText("not configured")).toBeTruthy();
      expect(cursor).toHaveTextContent("Set up to use: Install Cursor with Agent support");
      expect(cursor).not.toHaveTextContent("error");
      expect(cursor).not.toHaveTextContent("auth unknown");
      expect(cursor).not.toHaveTextContent("dev-override://cursor_agent");
    });

    it("renders local sign-in from discovery auth before a full probe has run", async () => {
      const { state, actions } = setup(
        withAdapter({
          agentAdapters: [
            {
              id: "cursor_agent",
              name: "Cursor Agent",
              kind: "acp",
              command: "cursor-agent",
              available: true,
              status: "available",
              cost_mode: "external",
              auth_status: "unauthenticated",
              auth_error: "Run cursor-agent login",
            },
          ],
        }),
      );
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));
      const row = await screen.findByTestId("external-agents-adapter-cursor_agent");
      expect(within(row).getByText("sign in")).toBeTruthy();
      expect(within(row).getByText("Local sign-in")).toBeTruthy();
      expect(within(row).getByText("cursor-agent login")).toBeTruthy();
      expect(screen.queryByTestId("external-agents-adapter-cursor_agent-auth-warning")).toBeNull();
      expect(screen.queryByTestId("external-agents-adapter-cursor_agent-auth-detail")).toBeNull();
    });

    it("shows an inline checking status while a probe is in flight", async () => {
      const { state, actions } = setup(
        withAdapter({
          agentAdapterHealthLoadingByID: new Map([["codex", true]]),
        }),
      );
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));
      expect(await screen.findByTestId("external-agents-checking-codex")).toHaveTextContent(
        /checking/i,
      );
    });

    it("does not flash Claude Code local auth guidance before readiness is verified", async () => {
      const { state, actions } = setup(
        withAdapter({
          agentAdapters: [
            {
              id: "claude_code",
              name: "Claude Code",
              kind: "acp",
              command: "claude-code-acp-adapter",
              available: true,
              status: "available",
              cost_mode: "external",
              auth_status: "unknown",
            },
          ],
        }),
      );
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));

      expect(await screen.findByTestId("external-agents-adapter-claude_code")).toBeTruthy();
      expect(screen.queryByText("Local sign-in")).toBeNull();
      expect(screen.queryByText(/does not store credentials/)).toBeNull();
      expect(screen.queryByLabelText("Claude Code credential")).toBeNull();
    });

    it("shows Claude Code local auth guidance when discovery reports missing auth", async () => {
      const { state, actions } = setup(
        withAdapter({
          agentAdapters: [
            {
              id: "claude_code",
              name: "Claude Code",
              kind: "acp",
              command: "claude-code-acp-adapter",
              available: true,
              status: "available",
              cost_mode: "external",
              auth_status: "unauthenticated",
              auth_error: "Run `claude /login` in Terminal.",
            },
          ],
        }),
      );
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));

      expect(await screen.findByText("Local sign-in")).toBeTruthy();
      expect(screen.getByText("claude /login")).toBeTruthy();
      expect(screen.getByText(/does not store credentials/)).toBeTruthy();
      expect(screen.queryByLabelText("Claude Code credential")).toBeNull();
    });

    it("does not show Claude Code local auth guidance after the adapter probe succeeds", async () => {
      const { state, actions } = setup(
        withAdapter({
          agentAdapters: [
            {
              id: "claude_code",
              name: "Claude Code",
              kind: "acp",
              command: "claude-code-acp-adapter",
              available: true,
              status: "available",
              cost_mode: "external",
              auth_status: "ok",
            },
          ],
          agentAdapterHealthByID: new Map([
            [
              "claude_code",
              { adapter_id: "claude_code", status: "ready", stage: "ready", duration_ms: 629 },
            ],
          ]),
        }),
      );
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));

      expect(await screen.findByText("ready")).toBeTruthy();
      expect(screen.queryByText("Local sign-in")).toBeNull();
      expect(screen.queryByTestId("external-agents-adapter-claude_code-auth-warning")).toBeNull();
      expect(screen.queryByLabelText("Claude Code credential")).toBeNull();
    });

    it("copies the Claude Code sign-in command", async () => {
      const copyCommand = vi.fn(async () => undefined);
      const { state, actions, user } = setup(
        withAdapter({
          agentAdapters: [
            {
              id: "claude_code",
              name: "Claude Code",
              kind: "acp",
              command: "claude-code-acp-adapter",
              available: true,
              status: "available",
              cost_mode: "external",
              auth_status: "unauthenticated",
            },
          ],
        }),
        { copyCommand },
      );
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));

      await user.click(await screen.findByRole("button", { name: "Copy command" }));
      expect(copyCommand).toHaveBeenCalledWith("claude /login");
    });

    it("can retest an adapter after local sign-in", async () => {
      const probeAgentAdapter = vi.fn(async () => null);
      const { state, actions, user } = setup(
        withAdapter({
          agentAdapters: [
            {
              id: "claude_code",
              name: "Claude Code",
              kind: "acp",
              command: "claude-code-acp-adapter",
              available: true,
              status: "available",
              cost_mode: "external",
              auth_status: "unauthenticated",
            },
          ],
        }),
        { probeAgentAdapter },
      );
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));

      await user.click(await screen.findByRole("button", { name: "Test again" }));
      expect(probeAgentAdapter).toHaveBeenCalledWith("claude_code");
    });
  });
});
