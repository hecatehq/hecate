#!/usr/bin/env node

import readline from "node:readline";

const appURI = "ui://demo/weather";

const appHTML = `<!doctype html>
<html>
  <head>
    <meta charset="utf-8" />
    <style>
      :root {
        color-scheme: dark light;
        font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      }
      body {
        margin: 0;
        background: #111827;
        color: #f9fafb;
      }
      .wrap {
        display: grid;
        gap: 12px;
        padding: 18px;
      }
      .top {
        align-items: baseline;
        display: flex;
        gap: 10px;
        justify-content: space-between;
      }
      .place {
        font-size: 15px;
        font-weight: 650;
      }
      .temp {
        color: #5eead4;
        font-size: 34px;
        font-weight: 750;
      }
      .grid {
        display: grid;
        gap: 8px;
        grid-template-columns: repeat(3, minmax(0, 1fr));
      }
      .cell {
        background: rgba(255, 255, 255, 0.08);
        border: 1px solid rgba(255, 255, 255, 0.12);
        border-radius: 6px;
        padding: 9px;
      }
      .label {
        color: #9ca3af;
        font-size: 11px;
      }
      .value {
        font-size: 13px;
        margin-top: 4px;
      }
    </style>
  </head>
  <body>
    <main class="wrap">
      <div class="top">
        <div>
          <div class="label">MCP App demo</div>
          <div class="place" id="place">Waiting for tool input</div>
        </div>
        <div class="temp" id="temp">--</div>
      </div>
      <div class="grid">
        <section class="cell">
          <div class="label">Conditions</div>
          <div class="value" id="conditions">--</div>
        </section>
        <section class="cell">
          <div class="label">Humidity</div>
          <div class="value" id="humidity">--</div>
        </section>
        <section class="cell">
          <div class="label">Wind</div>
          <div class="value" id="wind">--</div>
        </section>
      </div>
    </main>
    <script>
      let nextID = 1;
      let toolInput = {};

      function request(method, params) {
        const id = nextID++;
        window.parent.postMessage({ jsonrpc: "2.0", id, method, params }, "*");
      }

      function notify(method, params) {
        window.parent.postMessage({ jsonrpc: "2.0", method, params }, "*");
      }

      window.addEventListener("message", (event) => {
        const msg = event.data || {};
        if (msg.method === "ui/notifications/tool-input") {
          toolInput = msg.params?.arguments || {};
          document.getElementById("place").textContent = toolInput.location || "Somewhere nice";
        }
        if (msg.method === "ui/notifications/tool-result") {
          const data = msg.params?.structuredContent || {};
          document.getElementById("temp").textContent = data.temperature ?? "72F";
          document.getElementById("conditions").textContent = data.conditions || "clear";
          document.getElementById("humidity").textContent = data.humidity || "45%";
          document.getElementById("wind").textContent = data.wind || "8 mph";
          notify("ui/notifications/size-changed", {
            width: document.documentElement.scrollWidth,
            height: document.documentElement.scrollHeight
          });
        }
      });

      request("ui/initialize", {
        protocolVersion: "2026-01-26",
        clientInfo: { name: "hecate-weather-demo", version: "0.0.0" },
        appCapabilities: { availableDisplayModes: ["inline"] }
      });
      notify("ui/notifications/initialized", {});
    </script>
  </body>
</html>`;

function response(id, result) {
  process.stdout.write(`${JSON.stringify({ jsonrpc: "2.0", id, result })}\n`);
}

function errorResponse(id, code, message) {
  process.stdout.write(`${JSON.stringify({ jsonrpc: "2.0", id, error: { code, message } })}\n`);
}

function handle(req) {
  if (!req.id && req.method?.startsWith("notifications/")) return;

  switch (req.method) {
    case "initialize":
      response(req.id, {
        protocolVersion: req.params?.protocolVersion || "2025-11-25",
        capabilities: { tools: {}, resources: {} },
        serverInfo: { name: "hecate-weather-app-demo", version: "0.0.0" },
      });
      return;
    case "tools/list":
      response(req.id, {
        tools: [
          {
            name: "show_weather",
            description: "Render a tiny weather dashboard MCP App.",
            inputSchema: {
              type: "object",
              properties: {
                location: { type: "string", description: "City or place to render." },
              },
              required: ["location"],
            },
            _meta: {
              ui: {
                resourceUri: appURI,
                visibility: ["model", "app"],
              },
            },
          },
        ],
      });
      return;
    case "tools/call": {
      const location = req.params?.arguments?.location || "Barcelona";
      response(req.id, {
        content: [{ type: "text", text: `Weather app rendered for ${location}.` }],
        structuredContent: {
          location,
          temperature: "72F",
          conditions: "Sunny",
          humidity: "45%",
          wind: "8 mph",
        },
        _meta: { demo: true },
      });
      return;
    }
    case "resources/read":
      if (req.params?.uri !== appURI) {
        errorResponse(req.id, -32602, `unknown resource: ${req.params?.uri || ""}`);
        return;
      }
      response(req.id, {
        contents: [
          {
            uri: appURI,
            mimeType: "text/html;profile=mcp-app",
            text: appHTML,
            _meta: {
              ui: {
                csp: {},
                prefersBorder: true,
              },
            },
          },
        ],
      });
      return;
    case "ping":
      response(req.id, {});
      return;
    default:
      errorResponse(req.id, -32601, `method not found: ${req.method}`);
  }
}

readline
  .createInterface({ input: process.stdin, crlfDelay: Infinity })
  .on("line", (line) => {
    try {
      handle(JSON.parse(line));
    } catch (err) {
      errorResponse(null, -32700, err instanceof Error ? err.message : "parse error");
    }
  });
