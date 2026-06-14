import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { useEffect, useRef, useState } from "react";
import { createTerminalSession } from "../../lib/api";
import { terminalWebSocketURL } from "../../lib/terminal";
import { Icon, Icons } from "../shared/ui";

type TerminalStatus = "connecting" | "connected" | "closed" | "error";

type TerminalMessage =
  | { type: "output"; data?: string }
  | { type: "exit"; code?: number }
  | { type: "error"; message?: string };

export function ChatTerminalPanel({
  workspace,
  onClose,
}: {
  workspace: string;
  onClose: () => void;
}) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const terminalRef = useRef<Terminal | null>(null);
  const socketRef = useRef<WebSocket | null>(null);
  const fitRef = useRef<FitAddon | null>(null);
  const [status, setStatus] = useState<TerminalStatus>("connecting");

  useEffect(() => {
    const container = containerRef.current;
    if (!container || !workspace.trim()) return;

    const terminal = new Terminal({
      cursorBlink: false,
      convertEol: true,
      fontFamily: "var(--font-mono)",
      fontSize: 13,
      lineHeight: 1.25,
      scrollback: 5000,
      theme: terminalTheme(),
    });
    const fit = new FitAddon();
    terminal.loadAddon(fit);
    terminal.open(container);
    fit.fit();
    terminalRef.current = terminal;
    fitRef.current = fit;

    let disposed = false;
    let socket: WebSocket | null = null;
    setStatus("connecting");

    const sendResize = () => {
      if (!socket || socket.readyState !== WebSocket.OPEN) return;
      socket.send(JSON.stringify({ type: "resize", cols: terminal.cols, rows: terminal.rows }));
    };

    const resizeObserver = new ResizeObserver(() => {
      fit.fit();
      sendResize();
    });
    resizeObserver.observe(container);

    const dataDisposable = terminal.onData((data) => {
      if (socket?.readyState === WebSocket.OPEN) {
        socket.send(JSON.stringify({ type: "input", data }));
      }
    });

    void createTerminalSession(workspace)
      .then((response) => {
        if (disposed) return;
        socket = new WebSocket(
          terminalWebSocketURL(
            response.data.workspace,
            response.data.token,
            terminal.cols,
            terminal.rows,
          ),
        );
        socketRef.current = socket;
        socket.addEventListener("open", () => {
          setStatus("connected");
          fit.fit();
          sendResize();
          terminal.focus();
        });
        socket.addEventListener("message", (event) => {
          const message = parseTerminalMessage(event.data);
          if (!message) return;
          switch (message.type) {
            case "output":
              terminal.write(message.data ?? "");
              break;
            case "error":
              setStatus("error");
              terminal.writeln(`\r\n\x1b[31m${message.message || "Terminal error"}\x1b[0m`);
              break;
            case "exit":
              setStatus("closed");
              terminal.writeln(`\r\n\x1b[90mTerminal exited (${message.code ?? 0}).\x1b[0m`);
              break;
          }
        });
        socket.addEventListener("close", () =>
          setStatus((current) => (current === "error" ? current : "closed")),
        );
        socket.addEventListener("error", () => setStatus("error"));
      })
      .catch((error: unknown) => {
        if (disposed) return;
        setStatus("error");
        const message = error instanceof Error ? error.message : "Failed to start terminal";
        terminal.writeln(`\r\n\x1b[31m${message}\x1b[0m`);
      });

    const themeObserver = new MutationObserver(() => {
      terminal.options.theme = terminalTheme();
    });
    themeObserver.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["data-theme"],
    });

    return () => {
      disposed = true;
      themeObserver.disconnect();
      dataDisposable.dispose();
      resizeObserver.disconnect();
      if (socket?.readyState === WebSocket.OPEN) {
        socket.send(JSON.stringify({ type: "close" }));
      }
      socket?.close();
      terminal.dispose();
      terminalRef.current = null;
      socketRef.current = null;
      fitRef.current = null;
    };
  }, [workspace]);

  return (
    <section
      aria-label="Embedded terminal"
      style={{
        borderTop: "1px solid var(--border)",
        background: "color-mix(in srgb, var(--bg0) 94%, var(--bg2))",
        flex: "0 0 clamp(220px, 34vh, 420px)",
        minHeight: 220,
        display: "flex",
        flexDirection: "column",
        padding: "10px 12px 12px",
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 10,
          marginBottom: 8,
          flexShrink: 0,
        }}
      >
        <div
          style={{
            display: "inline-flex",
            alignItems: "center",
            gap: 8,
            minWidth: 0,
            maxWidth: "min(42vw, 360px)",
            border: "1px solid var(--border)",
            borderRadius: 13,
            background: "var(--bg2)",
            padding: "8px 12px",
            boxShadow: "0 1px 0 color-mix(in srgb, var(--t0) 8%, transparent) inset",
          }}
        >
          <Icon d={Icons.terminal} size={14} />
          <strong
            title={workspace}
            style={{
              minWidth: 0,
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
              fontSize: 13,
              color: "var(--t0)",
            }}
          >
            {terminalTitle(workspace)}
          </strong>
        </div>
        <span
          style={{
            flex: 1,
          }}
        />
        <span
          style={{
            color: terminalStatusColor(status),
            fontFamily: "var(--font-mono)",
            fontSize: 10,
            textTransform: "uppercase",
          }}
        >
          {terminalStatusLabel(status)}
        </span>
        <button
          className="btn btn-ghost btn-sm"
          type="button"
          aria-label="Close terminal"
          title="Close terminal"
          onClick={onClose}
          style={{ width: 28, height: 28, padding: 0, justifyContent: "center" }}
        >
          <Icon d={Icons.x} size={12} />
        </button>
      </div>
      <div
        ref={containerRef}
        style={{
          flex: 1,
          minHeight: 0,
          border: "1px solid color-mix(in srgb, var(--border) 72%, transparent)",
          borderRadius: 16,
          background: "var(--terminal-bg, #171717)",
          padding: "12px 14px",
          overflow: "hidden",
        }}
      />
    </section>
  );
}

function terminalTitle(workspace: string): string {
  const clean = workspace.trim().replace(/\/+$/, "");
  const name = clean.split("/").filter(Boolean).at(-1) || "workspace";
  return `${name}`;
}

function parseTerminalMessage(value: unknown): TerminalMessage | null {
  if (typeof value !== "string") return null;
  try {
    const parsed = JSON.parse(value) as TerminalMessage;
    return typeof parsed?.type === "string" ? parsed : null;
  } catch {
    return null;
  }
}

function terminalTheme() {
  const styles = getComputedStyle(document.documentElement);
  const read = (name: string, fallback: string) => styles.getPropertyValue(name).trim() || fallback;
  return {
    background: read("--bg0", "#050808"),
    foreground: read("--t0", "#e5ecec"),
    cursor: read("--teal", "#00c7b7"),
    selectionBackground: read("--teal-bg", "#003d38"),
    black: read("--bg0", "#050808"),
    brightBlack: read("--t4", "#5b6668"),
    white: read("--t1", "#cbd4d4"),
    brightWhite: read("--t0", "#f5f7f7"),
    green: read("--green", "#63d471"),
    red: read("--red", "#ff6961"),
    yellow: read("--amber", "#f0c34a"),
    cyan: read("--teal", "#00c7b7"),
  };
}

function terminalStatusLabel(status: TerminalStatus): string {
  switch (status) {
    case "connected":
      return "live";
    case "connecting":
      return "connecting";
    case "error":
      return "error";
    case "closed":
      return "closed";
  }
}

function terminalStatusColor(status: TerminalStatus): string {
  switch (status) {
    case "connected":
      return "var(--green)";
    case "connecting":
      return "var(--amber)";
    case "error":
      return "var(--red)";
    case "closed":
      return "var(--t3)";
  }
}
