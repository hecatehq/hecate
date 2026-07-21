export function safeExternalURL(value: string): string | null {
  try {
    const url = new URL(value);
    if (url.protocol === "http:" || url.protocol === "https:" || url.protocol === "mailto:") {
      return url.href;
    }
  } catch {
    // Invalid and relative URLs stay inside the inert Markdown fallback.
  }
  return null;
}

export async function openDesktopExternalURL(value: string): Promise<void> {
  const url = safeExternalURL(value);
  if (!url) throw new Error("Unsupported external URL");
  const { openUrl } = await import("@tauri-apps/plugin-opener");
  await openUrl(url);
}
