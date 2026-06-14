export function terminalWebSocketURL(workspace: string, cols = 80, rows = 24): string {
  const base = new URL("/hecate/v1/terminal", window.location.href);
  base.protocol = base.protocol === "https:" ? "wss:" : "ws:";
  base.searchParams.set("workspace", workspace);
  base.searchParams.set("cols", String(cols));
  base.searchParams.set("rows", String(rows));
  return base.toString();
}
