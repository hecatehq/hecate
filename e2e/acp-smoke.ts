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

type InitializeResult = { availableModels?: Array<{ id: string }> };
type SessionNewResult = { session_id: string };
type SessionPromptResult = { session_id: string; task_id: string; run_id: string };

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const modelID = "gpt-4o-mini";
const children: ChildProcessWithoutNullStreams[] = [];
let fakeUpstream: Server | undefined;

try {
  const fakePort = await freePort();
  const gatewayPort = await freePort();
  fakeUpstream = await startFakeOpenAI(fakePort);
  const gateway = await startGateway(gatewayPort, fakePort);
  children.push(gateway);

  const acp = spawn("go", ["run", "./cmd/hecate-acp"], {
    cwd: repoRoot,
    env: {
      ...process.env,
      HECATE_GATEWAY_URL: `http://127.0.0.1:${gatewayPort}`,
    },
    stdio: ["pipe", "pipe", "pipe"],
  });
  children.push(acp);

  acp.stderr.on("data", (chunk: Buffer) => process.stderr.write(chunk));

  const rl = readline.createInterface({ input: acp.stdout });
  const pending = new Map<string, (msg: RPCResponse) => void>();
  const updates: unknown[] = [];
  const permissionRequests: unknown[] = [];

  rl.on("line", (line: string) => {
    const msg = JSON.parse(line) as RPCRequest;
    if (msg.method === "session/update") {
      updates.push(msg.params);
      return;
    }
    if (msg.method === "session/request_permission") {
      permissionRequests.push(msg.params);
      acp.stdin.write(JSON.stringify({
        jsonrpc: "2.0",
        id: msg.id,
        result: { decision: "allow", note: "e2e smoke" },
      }) + "\n");
      return;
    }
    if (msg.id !== undefined) {
      const waiter = pending.get(JSON.stringify(msg.id));
      if (waiter) {
        pending.delete(JSON.stringify(msg.id));
        waiter(msg as RPCResponse);
      }
    }
  });

  const request = <T>(method: string, params: unknown): Promise<T> => {
    const id = pending.size + 1;
    acp.stdin.write(JSON.stringify({ jsonrpc: "2.0", id, method, params }) + "\n");
    return new Promise((resolve, reject) => {
      const timeout = setTimeout(() => {
        pending.delete(JSON.stringify(id));
        reject(new Error(`timeout waiting for ${method}`));
      }, 20_000);
      pending.set(JSON.stringify(id), (msg) => {
        clearTimeout(timeout);
        if (msg.error) reject(new Error(`${method}: ${msg.error.message}`));
        else resolve(msg.result as T);
      });
    });
  };

  const init = await request<InitializeResult>("initialize", {
    protocolVersion: "0.1",
    clientCapabilities: { permissions: {} },
  });
  assert(init.availableModels?.some((model) => model.id === modelID), "ACP initialize should advertise fake model");

  const session = await request<SessionNewResult>("session/new", {
    model: modelID,
    cwd: repoRoot,
  });
  assert(session.session_id !== "", "session/new should return a session id");

  const first = await request<SessionPromptResult>("session/prompt", {
    session_id: session.session_id,
    prompt: "Run pwd, then say smoke complete.",
  });
  assert(first.task_id !== "" && first.run_id !== "", "first prompt should create task/run ids");

  await delay(1_500);

  const second = await request<SessionPromptResult>("session/prompt", {
    session_id: session.session_id,
    prompt: "Continue with one more sentence.",
  });

  await delay(1_500);

  assert(second.task_id === first.task_id, "second prompt should continue the same Hecate task");
  assert(second.run_id !== first.run_id, "second prompt should start a new Hecate run");
  assert(updates.length > 0, "ACP bridge should emit session/update notifications");
  assert(permissionRequests.length === 1, `expected one approval request, got ${permissionRequests.length}`);

  console.log(JSON.stringify({
    initializedModels: init.availableModels?.map((model) => model.id) ?? [],
    sessionID: session.session_id,
    taskID: first.task_id,
    firstRunID: first.run_id,
    secondRunID: second.run_id,
    sameTask: second.task_id === first.task_id,
    continuedRun: second.run_id !== first.run_id,
    updateCount: updates.length,
    permissionRequestCount: permissionRequests.length,
  }, null, 2));
} finally {
  await Promise.all(children.reverse().map(stopChild));
  await new Promise<void>((resolve) => fakeUpstream?.close(() => resolve()) ?? resolve());
}

async function startGateway(gatewayPort: number, fakePort: number): Promise<ChildProcessWithoutNullStreams> {
  const gateway = spawn("go", ["run", "./cmd/hecate"], {
    cwd: repoRoot,
    env: {
      ...process.env,
      GATEWAY_ADDRESS: `127.0.0.1:${gatewayPort}`,
      GATEWAY_AUTH_DISABLED: "true",
      GATEWAY_DATA_DIR: await mkdtemp(join(tmpdir(), "hecate-acp-smoke-")),
      GATEWAY_DEFAULT_MODEL: modelID,
      GATEWAY_TASK_APPROVAL_POLICIES: "all_tools",
      PROVIDER_FAKE_PRECONFIGURED: "1",
      PROVIDER_FAKE_API_KEY: "dummy",
      PROVIDER_FAKE_BASE_URL: `http://127.0.0.1:${fakePort}/v1`,
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

async function startFakeOpenAI(port: number): Promise<Server> {
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
      if (!hasToolResult) {
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
