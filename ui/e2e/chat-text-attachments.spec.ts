import { Buffer } from "node:buffer";
import type { Page } from "@playwright/test";

import {
  expect,
  test,
  mockGatewayAPIs,
  MOCK_MODELS,
  MOCK_SETTINGS_CONFIG_WITH_PROVIDERS,
} from "./fixtures";

async function openNonvisionChat(page: Page, toolsEnabled: boolean) {
  await page.unrouteAll({ behavior: "ignoreErrors" });
  const gateway = await mockGatewayAPIs(page, {
    settingsConfig: MOCK_SETTINGS_CONFIG_WITH_PROVIDERS,
    models: MOCK_MODELS.map((model) =>
      model.id === "gpt-4o"
        ? {
            ...model,
            metadata: {
              ...model.metadata,
              capabilities: {
                tool_calling: "basic",
                image_input: "none",
                streaming: true,
                source: "provider",
              },
            },
          }
        : model,
    ),
  });
  await page.addInitScript((enabled) => {
    window.localStorage.setItem("hecate.chatTarget", "agent");
    window.localStorage.setItem("hecate.chatToolsEnabled", String(enabled));
    window.localStorage.setItem("hecate.providerFilter", "openai");
    window.localStorage.setItem("hecate.model", "gpt-4o");
    window.localStorage.setItem("hecate.project", "proj_e2e");
  }, toolsEnabled);
  await page.goto("/");
  await page.getByRole("button", { name: "New Hecate chat", exact: true }).click();
  const providerPicker = page.getByRole("button", { name: /provider picker/i });
  await providerPicker.click();
  await page.getByRole("option").filter({ hasText: "OpenAI" }).first().click();
  const modelPicker = page.getByRole("button", { name: /model picker/i });
  await modelPicker.click();
  await page.getByRole("option").filter({ hasText: "gpt-4o" }).first().click();
  await expect(modelPicker).toContainText("gpt-4o");
  await expect(
    page.getByText(`Tools ${toolsEnabled ? "on" : "off"} · /tmp/hecate-e2e`, { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText(
      toolsEnabled ? "Private files · read/search on demand" : "Whole text · if model context fits",
      { exact: true },
    ),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "Files" })).toHaveAttribute(
    "title",
    toolsEnabled
      ? /private files are read or searched on demand/
      : /whole text\/code files are included only if they fit the selected model context/,
  );
  await expect(page.getByLabel("Choose files")).toBeEnabled();
  return gateway;
}

for (const toolsEnabled of [false, true]) {
  for (const selection of ["picker", "drop"] as const) {
    test(`nonvision Hecate Chat sends exact text bytes with Tools ${toolsEnabled ? "on" : "off"} via ${selection}`, async ({
      page,
    }) => {
      const gateway = await openNonvisionChat(page, toolsEnabled);
      const sourceBody = `  const privateBrowserText = "世界";\r\n\t// preserve whitespace\n${"x".repeat(96 * 1024)}`;
      const file = {
        name: "browser-source.ts",
        mimeType: "application/octet-stream",
        buffer: Buffer.from(sourceBody),
      };
      const uploadedBodies: Buffer[] = [];
      await page.route(/\/hecate\/v1\/chat\/sessions\/[^/]+\/attachments$/, async (route) => {
        const request = route.request();
        const body = request.postDataBuffer();
        if (!body) throw new Error("attachment upload has no body");
        const form = await new Response(new Uint8Array(body).buffer, {
          headers: { "Content-Type": request.headers()["content-type"] },
        }).formData();
        const part = form.get("file");
        if (!part || typeof part === "string") throw new Error("attachment file part is missing");
        expect(part.name).toBe(file.name);
        expect(part.type).toBe("text/plain");
        uploadedBodies.push(Buffer.from(await part.arrayBuffer()));
        await route.fallback();
      });

      if (selection === "picker") {
        await page.getByLabel("Choose files").setInputFiles(file);
      } else {
        const dataTransfer = await page.evaluateHandle(
          ({ name, bytes }) => {
            const transfer = new DataTransfer();
            transfer.items.add(new File([new Uint8Array(bytes)], name, { type: "text/plain" }));
            return transfer;
          },
          { name: file.name, bytes: [...file.buffer] },
        );
        await page
          .getByRole("group", { name: "File attachments", exact: true })
          .dispatchEvent("drop", {
            dataTransfer,
          });
        await dataTransfer.dispose();
      }

      await expect(page.getByRole("button", { name: `Remove ${file.name}` })).toBeVisible();
      expect(gateway.chatAttachmentUploads).toHaveLength(0);
      await page.getByRole("textbox", { name: "Message" }).fill("Review this source file");
      await page.getByRole("button", { name: "Send message" }).click();

      await expect.poll(() => gateway.chatMessagePayloads.length).toBe(1);
      expect(uploadedBodies).toEqual([file.buffer]);
      expect(gateway.chatAttachmentUploads).toHaveLength(1);
      const uploaded = gateway.chatAttachmentUploads[0];
      expect(uploaded).toMatchObject({
        filename: file.name,
        media_type: "text/plain",
        size_bytes: file.buffer.byteLength,
      });
      expect(gateway.chatMessagePayloads[0]).toMatchObject({
        content: "Review this source file",
        tools_enabled: toolsEnabled,
        attachment_ids: [uploaded.attachment_id],
      });
      expect(JSON.stringify(gateway.chatMessagePayloads)).not.toContain("privateBrowserText");
      await expect(page.getByRole("group", { name: "Attached files" })).toBeVisible();
      await expect(page.getByRole("button", { name: `Download ${file.name}` })).toBeVisible();
      await expect(page.getByRole("button", { name: `Remove ${file.name}` })).toHaveCount(0);
      await expect(page.locator("body")).not.toContainText("privateBrowserText");
      expect(gateway.chatAttachmentContentRequests).toHaveLength(0);
    });
  }
}

test("nonvision Hecate Chat explains image rejection without disabling text files", async ({
  page,
}) => {
  const gateway = await openNonvisionChat(page, true);
  const picker = page.getByLabel("Choose files");
  await picker.setInputFiles({
    name: "blocked-image.png",
    mimeType: "image/png",
    buffer: Buffer.from(
      "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
      "base64",
    ),
  });
  await expect(
    page.getByText("gpt-4o does not support image input.", { exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "Remove blocked-image.png" })).toHaveCount(0);
  expect(gateway.chatAttachmentUploads).toHaveLength(0);
  expect(gateway.chatMessagePayloads).toHaveLength(0);

  await picker.setInputFiles({
    name: "allowed-notes.md",
    mimeType: "text/markdown",
    buffer: Buffer.from("# Still usable\n"),
  });
  await expect(page.getByRole("button", { name: "Remove allowed-notes.md" })).toBeVisible();
  await expect(page.getByText("gpt-4o does not support image input.", { exact: true })).toHaveCount(
    0,
  );
});

test("native file selection enforces the shared 5 MiB and 12 MiB limits", async ({ page }) => {
  const gateway = await openNonvisionChat(page, true);
  const picker = page.getByLabel("Choose files");

  await picker.setInputFiles({
    name: "too-large.txt",
    mimeType: "text/plain",
    buffer: Buffer.alloc(5 * 1024 * 1024 + 1, "x"),
  });
  await expect(
    page.getByText("too-large.txt exceeds the 5 MiB limit.", { exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "Remove too-large.txt" })).toHaveCount(0);

  const fourMiB = Buffer.alloc(4 * 1024 * 1024, "a");
  await picker.setInputFiles([
    { name: "one.txt", mimeType: "text/plain", buffer: fourMiB },
    { name: "two.txt", mimeType: "text/plain", buffer: fourMiB },
    { name: "three.txt", mimeType: "text/plain", buffer: fourMiB },
    { name: "over.txt", mimeType: "text/plain", buffer: Buffer.from("x") },
  ]);
  await expect(
    page.getByText("Files in one message can total up to 12 MiB.", { exact: true }),
  ).toBeVisible();
  for (const name of ["one.txt", "two.txt", "three.txt"]) {
    await expect(page.getByRole("button", { name: `Remove ${name}` })).toBeVisible();
  }
  await expect(page.getByRole("button", { name: "Remove over.txt" })).toHaveCount(0);
  expect(gateway.chatAttachmentUploads).toHaveLength(0);
  expect(gateway.chatMessagePayloads).toHaveLength(0);
});

test("invalid UTF-8 leaves an existing valid text draft intact", async ({ page }) => {
  const gateway = await openNonvisionChat(page, false);
  const picker = page.getByLabel("Choose files");

  await picker.setInputFiles({
    name: "keep.txt",
    mimeType: "text/plain",
    buffer: Buffer.from("keep this draft"),
  });
  await expect(page.getByRole("button", { name: "Remove keep.txt" })).toBeVisible();

  await picker.setInputFiles({
    name: "invalid.txt",
    mimeType: "text/plain",
    buffer: Buffer.from([0xc3, 0x28]),
  });
  await expect(
    page.getByText("invalid.txt is not valid UTF-8 text or a supported PNG, JPEG, or WebP image.", {
      exact: true,
    }),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "Remove keep.txt" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Remove invalid.txt" })).toHaveCount(0);
  expect(gateway.chatAttachmentUploads).toHaveLength(0);
  expect(gateway.chatMessagePayloads).toHaveLength(0);
});

test("model-context rejection restores text and shows recovery guidance", async ({ page }) => {
  const gateway = await openNonvisionChat(page, false);
  await page.route(/\/hecate\/v1\/chat\/sessions\/[^/]+\/messages$/, async (route) => {
    if (route.request().method() !== "POST") {
      await route.fallback();
      return;
    }
    await route.fulfill({
      status: 413,
      contentType: "application/json",
      body: JSON.stringify({
        error: {
          type: "chat.text_context_too_large",
          message: "text attachment context too large",
          user_message: "These files do not fit the selected model's inline context budget.",
          operator_action:
            "Turn Tools on to read and search files in parts, or attach a smaller excerpt. No file text was sent.",
        },
      }),
    });
  });
  const file = {
    name: "large-source.ts",
    mimeType: "text/plain",
    buffer: Buffer.from("x".repeat(96 * 1024)),
  };
  await page.getByLabel("Choose files").setInputFiles(file);
  await page.getByRole("textbox", { name: "Message" }).fill("Review the full source");
  await page.getByRole("button", { name: "Send message" }).click();

  await expect(
    page.getByText("These files do not fit the selected model's inline context budget.", {
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    page.getByText(
      "Turn Tools on to read and search files in parts, or attach a smaller excerpt. No file text was sent.",
      { exact: true },
    ),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "Remove large-source.ts" })).toBeVisible();
  await expect(page.getByRole("textbox", { name: "Message" })).toHaveValue(
    "Review the full source",
  );
  expect(gateway.chatAttachmentUploads).toHaveLength(1);
  expect(gateway.chatMessagePayloads).toHaveLength(0);
});
