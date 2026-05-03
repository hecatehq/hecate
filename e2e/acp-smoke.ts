import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { mkdtemp } from "node:fs/promises";
import http, { type IncomingMessage, type Server, type ServerResponse } from "node:http";
import net from "node:net";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import readline from "node:readline";
import { fileURLToPath } from "node:url";

type JSONRPCID = number | string;

type RPCRequest = {
  jsonrpc: "2.0";
  id?: JSONRPCID;
  method?: string;
  params?: unknown;
  result?: unknown;
  error?: { code: number; message: string };
};

type RPCResponse<T = unknown> = {
  jsonrpc: "2.0";
  id: JSONRPCID;
  result?: T;
  error?: { code: number; message: string };
};

type InitializeResult = { availableModels?: Array<{ id: string }>; agentCapabilities?: { permissions?: boolean } };
type SessionNewResult = { session_id: string };
type SessionPromptResult = { session_id: string; task_id: string; run_id: string };
type SessionCancelResult = { session_id: string; task_id?: string; run_id?: string; cancelled: boolean };

type ScenarioResult = Record<string, unknown> & { scenario: string };

type FakeMode = "final" | "tool";

type Stack = {
  children: ChildProcessWithoutNullStreams[];
  fakeUpstream?: Server;
};

type StartGatewayOptions = {
  fakePort: number;
  gatewayPort?: number;
  dataDir?: string;
  approvalPolicies?: string;
};

type StartACPOptions = {
  gatewayURL?: string | null;
  dataDir?: string;
  approvalRoute?: "editor" | "operator";
  autoPermissionDecision?: "allow" | "deny" | null;
};

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const modelID = "gpt-4o-mini";

const results: ScenarioResult[] = [];

try {
  results.push(await scenarioEditorApprovalRoundTrip());
  results.push(await scenarioOperatorApprovalDoesNotAskEditor());
  results.push(await scenarioCancelAwaitingApprovalRun());
  results.push(await scenarioGatewayStateDiscovery());
  console.log(JSON.stringify({ scenarios: results }, null, 2));
} catch (error) {
  console.error(error instanceof Error ? error.stack ?? error.message : String(error));
  process.exitCode = 1;
}

async function scenarioEditorApprovalRoundTrip(): Promise<ScenarioResult> {
  return withStack(async (stack) => {
    const fakePort = await freePort();
    const gatewayPort = await freePort();
    stack.fakeUpstream = await startFakeOpenAI(fakePort, "tool");
    const gateway = await startGateway({ fakePort, gatewayPort, approvalPolicies: "all_tools" });
    stack.children.push(gateway);

    const acp = startACP({
      gatewayURL: `http://127.0.0.1:${gatewayPort}`,
      approvalRoute: "editor",
      autoPermissionDecision: "allow",
    });
    stack.children.push(acp.process);

    const init = await acp.request<InitializeResult>("initialize", initializeParams(true));
    assert(init.availableModels?.some((model) => model.id === modelID), "initialize should advertise fake model");
    assert(init.agentCapabilities?.permissions === true, "editor approval mode should advertise permissions");

    const session = await acp.request<SessionNewResult>("session/new", {
      model: modelID,
      cwd: repoRoot,
    });
    assert(session.session_id !== "", "session/new should return a session id");

    const first = await acp.request<SessionPromptResult>("session/prompt", {
      session_id: session.session_id,
      prompt: "Run pwd, then say smoke complete.",
    });
    assert(first.task_id !== "" && first.run_id !== "", "first prompt should create task/run ids");

    await waitFor(() => acp.permissionRequests.length === 1, "editor permission request");
    await waitFor(() => hasTerminalUpdate(acp.updates), "terminal update after approval");

    const second = await acp.request<SessionPromptResult>("session/prompt", {
      session_id: session.session_id,
      prompt: "Continue with one more sentence.",
    });

    assert(second.task_id === first.task_id, "second prompt should continue the same Hecate task");
    assert(second.run_id !== first.run_id, "second prompt should start a new Hecate run");
    assert(acp.updates.length > 0, "bridge should emit session/update notifications");

    return {
      scenario: "editor approval round-trip",
      taskID: first.task_id,
      firstRunID: first.run_id,
      secondRunID: second.run_id,
      updateCount: acp.updates.length,
      permissionRequestCount: acp.permissionRequests.length,
    };
  });
}

async function scenarioOperatorApprovalDoesNotAskEditor(): Promise<ScenarioResult> {
  return withStack(async (stack) => {
    const fakePort = await freePort();
    const gatewayPort = await freePort();
    stack.fakeUpstream = await startFakeOpenAI(fakePort, "tool");
    const gateway = await startGateway({ fakePort, gatewayPort, approvalPolicies: "all_tools" });
    stack.children.push(gateway);

    const acp = startACP({
      gatewayURL: `http://127.0.0.1:${gatewayPort}`,
      approvalRoute: "operator",
      autoPermissionDecision: null,
    });
    stack.children.push(acp.process);

    const init = await acp.request<InitializeResult>("initialize", initializeParams(false));
    assert(init.agentCapabilities?.permissions === false, "operator approval mode should not advertise editor permissions");

    const session = await acp.request<SessionNewResult>("session/new", {
      model: modelID,
      cwd: repoRoot,
    });
    await acp.request<SessionPromptResult>("session/prompt", {
      session_id: session.session_id,
      prompt: "Run pwd and wait for operator approval.",
    });

    await waitFor(() => hasPendingApprovalUpdate(acp.updates), "approval.requested update");
    assert(acp.permissionRequests.length === 0, "operator approval route must not emit session/request_permission");

    return {
      scenario: "operator approval route",
      updateCount: acp.updates.length,
      permissionRequestCount: acp.permissionRequests.length,
    };
  });
}

async function scenarioCancelAwaitingApprovalRun(): Promise<ScenarioResult> {
  return withStack(async (stack) => {
    const fakePort = await freePort();
    const gatewayPort = await freePort();
    stack.fakeUpstream = await startFakeOpenAI(fakePort, "tool");
    const gateway = await startGateway({ fakePort, gatewayPort, approvalPolicies: "all_tools" });
    stack.children.push(gateway);

    const acp = startACP({
      gatewayURL: `http://127.0.0.1:${gatewayPort}`,
      approvalRoute: "operator",
      autoPermissionDecision: null,
    });
    stack.children.push(acp.process);

    await acp.request<InitializeResult>("initialize", initializeParams(false));
    const session = await acp.request<SessionNewResult>("session/new", {
      model: modelID,
      cwd: repoRoot,
    });
    const prompt = await acp.request<SessionPromptResult>("session/prompt", {
      session_id: session.session_id,
      prompt: "Run pwd and then stay awaiting approval.",
    });

    await waitFor(() => hasPendingApprovalUpdate(acp.updates), "approval.requested update before cancel");
    const cancelled = await acp.request<SessionCancelResult>("session/cancel", {
      session_id: session.session_id,
      reason: "e2e cancel",
    });

    assert(cancelled.cancelled === true, "session/cancel should cancel the active Hecate run");
    assert(cancelled.task_id === prompt.task_id, "cancel response should include task id");
    assert(cancelled.run_id === prompt.run_id, "cancel response should include run id");

    return {
      scenario: "cancel awaiting approval run",
      taskID: prompt.task_id,
      runID: prompt.run_id,
      cancelled: cancelled.cancelled,
    };
  });
}

async function scenarioGatewayStateDiscovery(): Promise<ScenarioResult> {
  return withStack(async (stack) => {
    const fakePort = await freePort();
    const gatewayPort = await freePort();
    const dataDir = await mkdtemp(join(tmpdir(), "hecate-acp-discovery-"));
    stack.fakeUpstream = await startFakeOpenAI(fakePort, "final");
    const gateway = await startGateway({ fakePort, gatewayPort, dataDir });
    stack.children.push(gateway);

    const acp = startACP({
      gatewayURL: null,
      dataDir,
      approvalRoute: "editor",
      autoPermissionDecision: null,
    });
    stack.children.push(acp.process);

    const init = await acp.request<InitializeResult>("initialize", initializeParams(true));
    assert(init.availableModels?.some((model) => model.id === modelID), "state discovery should find gateway models");

    return {
      scenario: "runtime state discovery",
      modelCount: init.availableModels?.length ?? 0,
    };
  });
}

async function withStack<T>(fn: (stack: Stack) => Promise<T>): Promise<T> {
  const stack: Stack = { children: [] };
  try {
    return await fn(stack);
  } finally {
    await Promise.all(stack.children.reverse().map(stopChild));
    await new Promise<void>((resolve) => stack.fakeUpstream?.close(() => resolve()) ?? resolve());
  }
}

class ACPClient {
  readonly updates: unknown[] = [];
  readonly permissionRequests: unknown[] = [];
  readonly process: ChildProcessWithoutNullStreams;
  private readonly pending = new Map<string, (msg: RPCResponse) => void>();
  private nextID = 1;

  constructor(options: StartACPOptions) {
    const env = { ...process.env };
    if (options.gatewayURL === null) delete env.HECATE_GATEWAY_URL;
    else if (options.gatewayURL !== undefined) env.HECATE_GATEWAY_URL = options.gatewayURL;
    if (options.dataDir !== undefined) env.GATEWAY_DATA_DIR = options.dataDir;
    if (options.approvalRoute !== undefined) env.HECATE_APPROVAL_ROUTE = options.approvalRoute;

    this.process = spawn("go", ["run", "./cmd/hecate-acp"], {
      cwd: repoRoot,
      env,
      stdio: ["pipe", "pipe", "pipe"],
    });
    this.process.stderr.on("data", (chunk: Buffer) => process.stderr.write(chunk));

    const rl = readline.createInterface({ input: this.process.stdout });
    rl.on("line", (line: string) => {
      const msg = JSON.parse(line) as RPCRequest;
      if (msg.method === "session/update") {
        this.updates.push(msg.params);
        return;
      }
      if (msg.method === "session/request_permission") {
        this.permissionRequests.push(msg.params);
        if (options.autoPermissionDecision !== null) {
          this.process.stdin.write(JSON.stringify({
            jsonrpc: "2.0",
            id: msg.id,
            result: { decision: options.autoPermissionDecision ?? "allow", note: "e2e smoke" },
          }) + "\n");
        }
        return;
      }
      if (msg.id !== undefined) {
        const waiter = this.pending.get(JSON.stringify(msg.id));
        if (waiter) {
          this.pending.delete(JSON.stringify(msg.id));
          waiter(msg as RPCResponse);
        }
      }
    });
  }

  request<T>(method: string, params: unknown): Promise<T> {
    const id = this.nextID++;
    this.process.stdin.write(JSON.stringify({ jsonrpc: "2.0", id, method, params }) + "\n");
    return new Promise((resolve, reject) => {
      const timeout = setTimeout(() => {
        this.pending.delete(JSON.stringify(id));
        reject(new Error(`timeout waiting for ${method}`));
      }, 20_000);
      this.pending.set(JSON.stringify(id), (msg) => {
        clearTimeout(timeout);
        if (msg.error) reject(new Error(`${method}: ${msg.error.message}`));
        else resolve(msg.result as T);
      });
    });
  }
}

function startACP(options: StartACPOptions): ACPClient {
  return new ACPClient(options);
}

async function startGateway(options: StartGatewayOptions): Promise<ChildProcessWithoutNullStreams> {
  const gatewayPort = options.gatewayPort ?? await freePort();
  const gateway = spawn("go", ["run", "./cmd/hecate"], {
    cwd: repoRoot,
    env: {
      ...process.env,
      GATEWAY_ADDRESS: `127.0.0.1:${gatewayPort}`,
      GATEWAY_AUTH_DISABLED: "true",
      GATEWAY_DATA_DIR: options.dataDir ?? await mkdtemp(join(tmpdir(), "hecate-acp-smoke-")),
      GATEWAY_DEFAULT_MODEL: modelID,
      GATEWAY_TASK_APPROVAL_POLICIES: options.approvalPolicies ?? "",
      PROVIDER_FAKE_PRECONFIGURED: "1",
      PROVIDER_FAKE_API_KEY: "dummy",
      PROVIDER_FAKE_BASE_URL: `http://127.0.0.1:${options.fakePort}/v1`,
      PROVIDER_FAKE_DEFAULT_MODEL: modelID,
      PROVIDER_FAKE_KIND: "local",
      PROVIDER_FAKE_MODELS: modelID,
    },
    stdio: ["pipe", "pipe", "pipe"],
  });
  gateway.stderr.on("data", (chunk: Buffer) => process.stderr.write(chunk));
  await waitHealthy(`http://127.0.0.1:${gatewayPort}`);
  return gateway;
}

async function startFakeOpenAI(port: number, mode: FakeMode): Promise<Server> {
  const server = http.createServer(async (req, res) => {
    const body = await readBody(req);

    if (req.method === "GET" && req.url === "/v1/models") {
      writeJSON(res, 200, {
        object: "list",
        data: [{ id: modelID, object: "model", owned_by: "fake" }],
      });
      return;
    }

    if (req.method === "POST" && req.url === "/v1/chat/completions") {
      const parsed = JSON.parse(body || "{}") as { messages?: Array<{ role?: string }> };
      const hasToolResult = parsed.messages?.some((message) => message.role === "tool") ?? false;
      if (mode === "tool" && !hasToolResult) {
        writeJSON(res, 200, {
          id: "chatcmpl-smoke-tool",
          object: "chat.completion",
          created: Math.floor(Date.now() / 1000),
          model: modelID,
          choices: [{
            index: 0,
            message: {
              role: "assistant",
              content: "",
              tool_calls: [{
                id: "call-smoke-pwd",
                type: "function",
                function: {
                  name: "shell_exec",
                  arguments: JSON.stringify({ command: "pwd" }),
                },
              }],
            },
            finish_reason: "tool_calls",
          }],
          usage: { prompt_tokens: 12, completion_tokens: 3, total_tokens: 15 },
        });
        return;
      }

      writeJSON(res, 200, {
        id: "chatcmpl-smoke-final",
        object: "chat.completion",
        created: Math.floor(Date.now() / 1000),
        model: modelID,
        choices: [{
          index: 0,
          message: { role: "assistant", content: "Smoke complete." },
          finish_reason: "stop",
        }],
        usage: { prompt_tokens: 12, completion_tokens: 3, total_tokens: 15 },
      });
      return;
    }

    writeJSON(res, 404, { error: { message: `unexpected ${req.method} ${req.url}` } });
  });

  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(port, "127.0.0.1", () => {
      server.off("error", reject);
      resolve();
    });
  });
  return server;
}

function initializeParams(permissions: boolean): unknown {
  return {
    protocolVersion: "0.1",
    clientCapabilities: permissions ? { permissions: {} } : {},
  };
}

async function waitHealthy(baseURL: string): Promise<void> {
  const deadline = Date.now() + 10_000;
  while (Date.now() < deadline) {
    try {
      const resp = await fetch(`${baseURL}/healthz`);
      if (resp.ok) return;
    } catch {
      // Keep polling until the gateway is ready.
    }
    await delay(100);
  }
  throw new Error(`gateway at ${baseURL} never became healthy`);
}

async function waitFor(predicate: () => boolean, label: string): Promise<void> {
  const deadline = Date.now() + 10_000;
  while (Date.now() < deadline) {
    if (predicate()) return;
    await delay(100);
  }
  throw new Error(`timed out waiting for ${label}`);
}

async function freePort(): Promise<number> {
  const server = net.createServer();
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => resolve());
  });
  const address = server.address();
  await new Promise<void>((resolve) => server.close(() => resolve()));
  if (address === null || typeof address === "string") {
    throw new Error("could not allocate a TCP port");
  }
  return address.port;
}

async function readBody(req: IncomingMessage): Promise<string> {
  const chunks: Buffer[] = [];
  for await (const chunk of req) chunks.push(Buffer.from(chunk));
  return Buffer.concat(chunks).toString("utf8");
}

function writeJSON(res: ServerResponse, status: number, payload: unknown): void {
  res.statusCode = status;
  res.setHeader("content-type", "application/json");
  res.end(JSON.stringify(payload));
}

function hasPendingApprovalUpdate(updates: unknown[]): boolean {
  return hasUpdateEvent(updates, "approval.requested") || updates.some((update) => {
    if (!update || typeof update !== "object") return false;
    const data = (update as { data?: { approvals?: unknown } }).data;
    if (!data || !Array.isArray(data.approvals)) return false;
    return data.approvals.some((approval) => {
      return approval && typeof approval === "object" && (approval as { status?: string }).status === "pending";
    });
  });
}

function hasTerminalUpdate(updates: unknown[]): boolean {
  return updates.some((update) => {
    if (!update || typeof update !== "object") return false;
    const candidate = update as { event_type?: string; terminal?: boolean; data?: { terminal?: boolean } };
    return candidate.terminal === true || candidate.data?.terminal === true || candidate.event_type === "run.finished";
  });
}

function hasUpdateEvent(updates: unknown[], eventType: string): boolean {
  return updates.some((update) => {
    if (!update || typeof update !== "object") return false;
    return (update as { event_type?: string }).event_type === eventType;
  });
}

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(message);
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function stopChild(child: ChildProcessWithoutNullStreams): Promise<void> {
  child.stdin.destroy();
  child.stdout.destroy();
  child.stderr.destroy();
  if (child.exitCode !== null || child.signalCode !== null) return;
  const exited = new Promise<void>((resolve) => child.once("exit", () => resolve()));
  child.kill("SIGTERM");
  await Promise.race([
    exited,
    delay(2_000).then(() => {
      if (child.exitCode === null && child.signalCode === null) child.kill("SIGKILL");
    }),
  ]);
}
