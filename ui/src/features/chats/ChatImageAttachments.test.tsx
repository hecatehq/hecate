import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { getChatAttachmentContentBlob } from "../../lib/api";
import {
  appendChatFiles,
  ChatAttachmentDrafts,
  ChatAttachmentGallery,
  ChatImageAttachmentDrafts,
  ChatImageAttachmentGallery,
  MAX_CHAT_IMAGE_BYTES,
  MAX_CHAT_IMAGE_MESSAGE_BYTES,
  MAX_CHAT_TEXT_BYTES,
  MAX_CHAT_TEXT_MESSAGE_BYTES,
} from "./ChatImageAttachments";

vi.mock("../../lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../lib/api")>();
  return { ...actual, getChatAttachmentContentBlob: vi.fn() };
});

const originalCreateObjectURL = Object.getOwnPropertyDescriptor(URL, "createObjectURL");
const originalRevokeObjectURL = Object.getOwnPropertyDescriptor(URL, "revokeObjectURL");
const originalIntersectionObserver = Object.getOwnPropertyDescriptor(
  globalThis,
  "IntersectionObserver",
);

afterEach(() => {
  vi.clearAllMocks();
  restoreURLMethod("createObjectURL", originalCreateObjectURL);
  restoreURLMethod("revokeObjectURL", originalRevokeObjectURL);
  restoreGlobalProperty("IntersectionObserver", originalIntersectionObserver);
});

describe("native chat attachment selection", () => {
  it("accepts supported images and UTF-8 text without an extension allowlist", async () => {
    const png = imageFile("map.png", "image/png");
    const json = new File(['{"answer":42}'], "answer.json", { type: "application/json" });
    const source = new File(["export const answer = 42;"], "main.ts", {
      type: "video/mp2t",
    });

    const result = await appendChatFiles([], [png, json, source], "native", {
      imageInputEnabled: true,
    });

    expect(result.attachments).toEqual([
      expect.objectContaining({ file: png, kind: "image", canonicalMediaType: "image/png" }),
      expect.objectContaining({ file: json, kind: "text", canonicalMediaType: "text/plain" }),
      expect.objectContaining({ file: source, kind: "text", canonicalMediaType: "text/plain" }),
    ]);
    expect(result.error).toBe("");
  });

  it("enforces the per-image size and shared message count limits", async () => {
    const oversized = imageFile("large.webp", "image/webp");
    Object.defineProperty(oversized, "size", {
      value: MAX_CHAT_IMAGE_BYTES + 1,
    });

    await expect(
      appendChatFiles([], [oversized], "native", { imageInputEnabled: true }),
    ).resolves.toMatchObject({ error: "large.webp exceeds the 5 MiB image limit." });

    const current = ["1", "2", "3", "4"].map((id) => ({
      id,
      file: imageFile(`${id}.png`, "image/png"),
      kind: "image" as const,
    }));
    await expect(
      appendChatFiles(current, [imageFile("5.png", "image/png")], "native", {
        imageInputEnabled: true,
      }),
    ).resolves.toMatchObject({ error: "A message can include up to 4 files." });
  });

  it("enforces independent image and text message budgets", async () => {
    const current = ["1", "2"].map((id) => {
      const file = imageFile(`${id}.png`, "image/png");
      Object.defineProperty(file, "size", { value: 5 * 1024 * 1024 });
      return { id, file, kind: "image" as const };
    });
    const next = imageFile("3.png", "image/png");
    Object.defineProperty(next, "size", {
      value: MAX_CHAT_IMAGE_MESSAGE_BYTES - 10 * 1024 * 1024 + 1,
    });

    const imageResult = await appendChatFiles(current, [next], "native", {
      imageInputEnabled: true,
    });

    expect(imageResult.attachments).toHaveLength(2);
    expect(imageResult.error).toBe("Images in one message can total up to 12 MiB.");

    const firstText = new File(["a"], "first.txt", { type: "text/plain" });
    Object.defineProperty(firstText, "size", { value: MAX_CHAT_TEXT_MESSAGE_BYTES / 2 });
    const secondText = new File(["b"], "second.txt", { type: "text/plain" });
    Object.defineProperty(secondText, "size", { value: MAX_CHAT_TEXT_MESSAGE_BYTES / 2 });
    const extraText = new File(["c"], "extra.txt", { type: "text/plain" });
    const textResult = await appendChatFiles(
      [
        { id: "text-1", file: firstText, kind: "text" },
        { id: "text-2", file: secondText, kind: "text" },
      ],
      [extraText],
      "native",
    );
    expect(textResult.attachments).toHaveLength(2);
    expect(textResult.error).toBe("Text and code files in one message can total up to 64 KiB.");

    const nearlyFullImage = imageFile("full.png", "image/png");
    Object.defineProperty(nearlyFullImage, "size", {
      value: MAX_CHAT_IMAGE_MESSAGE_BYTES,
    });
    const mixedResult = await appendChatFiles(
      [{ id: "image", file: nearlyFullImage, kind: "image" }],
      [new File(["x"], "note.txt", { type: "text/plain" })],
      "native",
      { imageInputEnabled: true },
    );
    expect(mixedResult.attachments).toHaveLength(1);
    expect(mixedResult.error).toBe("Files in one message can total up to 12 MiB.");
  });

  it("stops classifying after four accepted candidates", async () => {
    const unreadable = new File(["ignored"], "fifth.txt", { type: "text/plain" });
    const slice = vi.fn(() => {
      throw new Error("must not read beyond the message count bound");
    });
    Object.defineProperty(unreadable, "slice", { value: slice });
    const result = await appendChatFiles(
      [],
      [
        new File(["1"], "1.txt", { type: "text/plain" }),
        new File(["2"], "2.txt", { type: "text/plain" }),
        new File(["3"], "3.txt", { type: "text/plain" }),
        new File(["4"], "4.txt", { type: "text/plain" }),
        unreadable,
      ],
      "native",
    );

    expect(result.attachments).toHaveLength(4);
    expect(result.error).toBe("A message can include up to 4 files.");
    expect(slice).not.toHaveBeenCalled();
  });

  it("sniffs an image whose browser MIME declaration is empty", async () => {
    const image = new File(
      [new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])],
      "clipboard.png",
      { type: "" },
    );

    const result = await appendChatFiles([], [image], "native", { imageInputEnabled: true });

    expect(result.attachments).toHaveLength(1);
    expect(result.attachments[0].file).toBe(image);
    expect(result.attachments[0].kind).toBe("image");
    expect(result.error).toBe("");
  });

  it("rejects invalid UTF-8, control characters, archives, and oversized text", async () => {
    const invalidUTF8 = new File([new Uint8Array([0xc3, 0x28])], "invalid.txt", {
      type: "text/plain",
    });
    const control = new File([new Uint8Array([0x6f, 0x6b, 0x00])], "control.txt", {
      type: "text/plain",
    });
    const archive = new File(["PK\u0003\u0004"], "source.zip", { type: "application/zip" });
    const oversized = new File(["x"], "large.ts", { type: "text/plain" });
    Object.defineProperty(oversized, "size", { value: MAX_CHAT_TEXT_BYTES + 1 });

    await expect(appendChatFiles([], [invalidUTF8], "native")).resolves.toMatchObject({
      attachments: [],
      error: "invalid.txt is not valid UTF-8 text or a supported PNG, JPEG, or WebP image.",
    });
    await expect(appendChatFiles([], [control], "native")).resolves.toMatchObject({
      attachments: [],
      error: "control.txt contains unsupported control characters.",
    });
    await expect(appendChatFiles([], [archive], "native")).resolves.toMatchObject({
      attachments: [],
      error:
        "source.zip is not supported in Hecate Chat. Attach UTF-8 text/code or PNG, JPEG, or WebP.",
    });
    await expect(appendChatFiles([], [oversized], "native")).resolves.toMatchObject({
      attachments: [],
      error: "large.ts exceeds the 32 KiB text/code limit.",
    });
  });

  it("accepts text but rejects an image when the selected model lacks vision", async () => {
    const text = new File(["package main"], "main.go", { type: "text/plain" });
    const image = imageFile("map.png", "image/png");

    const result = await appendChatFiles([], [text, image], "native", {
      imageInputEnabled: false,
      imageInputDisabledReason: "Selected model does not support image input.",
    });

    expect(result.attachments).toEqual([expect.objectContaining({ file: text, kind: "text" })]);
    expect(result.error).toBe("Selected model does not support image input.");
  });

  it("exposes the disabled reason and keeps the picker keyboard-visible", () => {
    render(
      <ChatImageAttachmentDrafts
        attachments={[]}
        enabled={false}
        disabledReason="Wait before attaching files."
        onAddFiles={vi.fn()}
        onRemove={vi.fn()}
      />,
    );

    expect(screen.getByRole("group", { name: "File attachments" })).toBeVisible();
    expect(screen.getByRole("button", { name: "Files" })).toBeDisabled();
    expect(screen.getByText("Wait before attaching files.")).toBeVisible();
  });

  it("passes selected files to the composer and removes drafts by accessible name", async () => {
    defineURLMethod(
      "createObjectURL",
      vi.fn(() => "blob:draft"),
    );
    defineURLMethod("revokeObjectURL", vi.fn());
    const onAddFiles = vi.fn();
    const onRemove = vi.fn();
    const file = imageFile("street.webp", "image/webp");
    const user = userEvent.setup();
    const { rerender } = render(
      <ChatImageAttachmentDrafts
        attachments={[]}
        enabled
        disabledReason=""
        onAddFiles={onAddFiles}
        onRemove={onRemove}
      />,
    );

    await user.upload(screen.getByLabelText("Choose files"), file);
    expect(onAddFiles).toHaveBeenCalledWith([file]);

    rerender(
      <ChatImageAttachmentDrafts
        attachments={[{ id: "draft-1", file }]}
        enabled
        disabledReason=""
        onAddFiles={onAddFiles}
        onRemove={onRemove}
      />,
    );
    expect(screen.getByRole("group", { name: "Files ready to attach" })).toBeVisible();
    expect(screen.getByRole("status")).toHaveTextContent(
      "street.webp added. 1 file ready to attach.",
    );
    await user.click(screen.getByRole("button", { name: "Remove street.webp" }));
    expect(onRemove).toHaveBeenCalledWith("draft-1");
    await waitFor(() => expect(screen.getByRole("button", { name: "Files" })).toHaveFocus());
    rerender(
      <ChatImageAttachmentDrafts
        attachments={[]}
        enabled
        disabledReason=""
        onAddFiles={onAddFiles}
        onRemove={onRemove}
      />,
    );
    expect(screen.getByRole("status")).toHaveTextContent(
      "street.webp removed. No files ready to attach.",
    );
  });

  it("associates selection errors with the picker controls", () => {
    render(
      <ChatImageAttachmentDrafts
        attachments={[]}
        enabled
        disabledReason=""
        error="source.zip is not supported in Hecate Chat."
        onAddFiles={vi.fn()}
        onRemove={vi.fn()}
      />,
    );

    expect(screen.getByLabelText("Choose files")).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByLabelText("Choose files")).toHaveAccessibleDescription(
      "source.zip is not supported in Hecate Chat.",
    );
    expect(screen.getByRole("button", { name: "Files" })).toHaveAccessibleDescription(
      "source.zip is not supported in Hecate Chat.",
    );
  });

  it("moves focus to the next draft after removing an image", async () => {
    defineURLMethod(
      "createObjectURL",
      vi.fn((file: File) => `blob:${file.name}`),
    );
    defineURLMethod("revokeObjectURL", vi.fn());
    const onRemove = vi.fn();
    const user = userEvent.setup();

    render(
      <ChatImageAttachmentDrafts
        attachments={[
          { id: "draft-1", file: imageFile("first.png", "image/png") },
          { id: "draft-2", file: imageFile("second.png", "image/png") },
        ]}
        enabled
        disabledReason=""
        onAddFiles={vi.fn()}
        onRemove={onRemove}
      />,
    );

    await user.click(screen.getByRole("button", { name: "Remove first.png" }));

    expect(onRemove).toHaveBeenCalledWith("draft-1");
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Remove second.png" })).toHaveFocus(),
    );
  });

  it("moves focus to the visible disabled reason after removing the last draft", async () => {
    defineURLMethod(
      "createObjectURL",
      vi.fn(() => "blob:disabled-draft"),
    );
    defineURLMethod("revokeObjectURL", vi.fn());
    const user = userEvent.setup();

    function DisabledDraftHarness() {
      const [attachments, setAttachments] = useState([
        { id: "draft-1", file: imageFile("blocked.png", "image/png") },
      ]);
      return (
        <ChatImageAttachmentDrafts
          attachments={attachments}
          enabled={false}
          disabledReason="Wait before attaching files."
          onAddFiles={vi.fn()}
          onRemove={(id) =>
            setAttachments((current) => current.filter((attachment) => attachment.id !== id))
          }
        />
      );
    }

    render(<DisabledDraftHarness />);
    await user.click(screen.getByRole("button", { name: "Remove blocked.png" }));

    await waitFor(() => expect(screen.getByText("Wait before attaching files.")).toHaveFocus());
  });

  it("accepts files dropped on the attachment control", () => {
    const onAddFiles = vi.fn();
    const file = imageFile("aerial.jpg", "image/jpeg");
    const { rerender } = render(
      <ChatImageAttachmentDrafts
        attachments={[]}
        enabled
        disabledReason=""
        onAddFiles={onAddFiles}
        onRemove={vi.fn()}
      />,
    );

    fireEvent.drop(screen.getByLabelText("File attachments"), {
      dataTransfer: { files: [file], types: ["Files"] },
    });

    expect(onAddFiles).toHaveBeenCalledWith([file]);
    rerender(
      <ChatImageAttachmentDrafts
        attachments={[{ id: "draft-1", file }]}
        enabled
        disabledReason=""
        onAddFiles={onAddFiles}
        onRemove={vi.fn()}
      />,
    );
    expect(screen.getByRole("status")).toHaveTextContent(
      "aerial.jpg added. 1 file ready to attach.",
    );
  });
});

describe("External Agent file attachment selection", () => {
  it("accepts arbitrary non-empty files while enforcing the shared limits", async () => {
    const archive = new File(["archive"], "evidence.zip", { type: "application/zip" });
    const empty = new File([], "empty.txt", { type: "text/plain" });

    const result = await appendChatFiles([], [archive, empty], "files");

    expect(result.attachments).toHaveLength(1);
    expect(result.attachments[0]?.file).toBe(archive);
    expect(result.error).toBe("empty.txt is empty.");

    const source = new File(["export const value = 1;"], "source.ts", {
      type: "video/mp2t",
    });
    await expect(appendChatFiles([], [source], "files")).resolves.toMatchObject({
      attachments: [
        expect.objectContaining({
          file: source,
          kind: "text",
          canonicalMediaType: "text/plain",
        }),
      ],
      error: "",
    });

    const oversized = new File(["large"], "large.bin", {
      type: "application/octet-stream",
    });
    Object.defineProperty(oversized, "size", { value: MAX_CHAT_IMAGE_BYTES + 1 });
    expect((await appendChatFiles([], [oversized], "files")).error).toBe(
      "large.bin exceeds the 5 MiB limit.",
    );

    const combined = ["1", "2"].map((id) => {
      const file = new File([id], `${id}.bin`, { type: "application/octet-stream" });
      Object.defineProperty(file, "size", { value: 5 * 1024 * 1024 });
      return { id, file };
    });
    const beyondCombinedLimit = new File(["3"], "3.bin", {
      type: "application/octet-stream",
    });
    Object.defineProperty(beyondCombinedLimit, "size", {
      value: MAX_CHAT_IMAGE_MESSAGE_BYTES - 10 * 1024 * 1024 + 1,
    });
    expect((await appendChatFiles(combined, [beyondCombinedLimit], "files")).error).toBe(
      "Files in one message can total up to 12 MiB.",
    );

    const current = ["1", "2", "3", "4"].map((id) => ({
      id,
      file: new File([id], `${id}.bin`, { type: "application/octet-stream" }),
    }));
    expect(
      (
        await appendChatFiles(
          current,
          [new File(["5"], "5.bin", { type: "application/octet-stream" })],
          "files",
        )
      ).error,
    ).toBe("A message can include up to 4 files.");
  });

  it("uses file-specific picker, drop, chip, and live-announcement semantics", async () => {
    const onAddFiles = vi.fn();
    const onRemove = vi.fn();
    const file = new File(["report"], "report.pdf", { type: "application/pdf" });
    const user = userEvent.setup();
    const { rerender } = render(
      <ChatAttachmentDrafts
        attachments={[]}
        acceptance="files"
        enabled
        disabledReason=""
        onAddFiles={onAddFiles}
        onRemove={onRemove}
      />,
    );

    expect(screen.getByRole("group", { name: "File attachments" })).toBeVisible();
    expect(screen.getByRole("button", { name: "Files" })).toBeEnabled();
    await user.upload(screen.getByLabelText("Choose files"), file);
    expect(onAddFiles).toHaveBeenCalledWith([file]);

    rerender(
      <ChatAttachmentDrafts
        attachments={[{ id: "draft-file", file }]}
        acceptance="files"
        enabled
        disabledReason=""
        onAddFiles={onAddFiles}
        onRemove={onRemove}
      />,
    );
    expect(screen.getByRole("group", { name: "Files ready to attach" })).toBeVisible();
    expect(screen.getByText("report.pdf")).toBeVisible();
    expect(screen.queryByRole("img", { name: "report.pdf" })).toBeNull();
    expect(screen.getByRole("status")).toHaveTextContent(
      "report.pdf added. 1 file ready to attach.",
    );

    fireEvent.drop(screen.getByLabelText("File attachments"), {
      dataTransfer: { files: [file], types: ["Files"] },
    });
    expect(onAddFiles).toHaveBeenLastCalledWith([file]);
  });
});

describe("stored chat image attachments", () => {
  it("fetches on entry, revokes on distant exit, and refetches on re-entry", async () => {
    let notify: IntersectionObserverCallback = () => {};
    const observe = vi.fn();
    const disconnect = vi.fn();
    class MockIntersectionObserver implements IntersectionObserver {
      readonly root = null;
      readonly rootMargin = "240px 0px";
      readonly scrollMargin = "0px";
      readonly thresholds = [0];

      constructor(callback: IntersectionObserverCallback) {
        notify = callback;
      }

      observe(target: Element) {
        observe(target);
      }

      disconnect() {
        disconnect();
      }

      takeRecords(): IntersectionObserverEntry[] {
        return [];
      }

      unobserve() {}
    }
    Object.defineProperty(globalThis, "IntersectionObserver", {
      configurable: true,
      value: MockIntersectionObserver,
    });
    const createObjectURL = vi
      .fn<(_: Blob) => string>()
      .mockReturnValueOnce("blob:stored-1")
      .mockReturnValueOnce("blob:stored-2");
    const revokeObjectURL = vi.fn();
    defineURLMethod("createObjectURL", createObjectURL);
    defineURLMethod("revokeObjectURL", revokeObjectURL);
    vi.mocked(getChatAttachmentContentBlob).mockResolvedValue(
      new Blob(["image"], { type: "image/png" }),
    );

    const { unmount } = render(<ChatImageAttachmentGallery attachments={[storedAttachment()]} />);

    expect(screen.getByRole("group", { name: "Attached images" })).toBeVisible();
    expect(observe).toHaveBeenCalledOnce();
    expect(getChatAttachmentContentBlob).not.toHaveBeenCalled();

    act(() => {
      notify(
        [{ isIntersecting: true, intersectionRatio: 1 } as IntersectionObserverEntry],
        {} as IntersectionObserver,
      );
    });
    await waitFor(() =>
      expect(getChatAttachmentContentBlob).toHaveBeenCalledWith(
        "session-1",
        "att-1",
        expect.any(AbortSignal),
      ),
    );
    await waitFor(() =>
      expect(screen.getByRole("img", { name: "map.png" })).toHaveAttribute("src", "blob:stored-1"),
    );
    expect(screen.getByRole("button", { name: "Open map.png" })).toHaveAttribute(
      "aria-haspopup",
      "dialog",
    );
    expect(disconnect).not.toHaveBeenCalled();

    act(() => {
      notify(
        [{ isIntersecting: false, intersectionRatio: 0 } as IntersectionObserverEntry],
        {} as IntersectionObserver,
      );
    });
    await waitFor(() => expect(screen.queryByRole("img", { name: "map.png" })).toBeNull());
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:stored-1");

    act(() => {
      notify(
        [{ isIntersecting: true, intersectionRatio: 1 } as IntersectionObserverEntry],
        {} as IntersectionObserver,
      );
    });
    await waitFor(() => expect(getChatAttachmentContentBlob).toHaveBeenCalledTimes(2));
    await waitFor(() =>
      expect(screen.getByRole("img", { name: "map.png" })).toHaveAttribute("src", "blob:stored-2"),
    );

    unmount();
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:stored-2");
    expect(disconnect).toHaveBeenCalledOnce();
  });

  it("fetches blob content through the API client and revokes its preview URL", async () => {
    const createObjectURL = vi.fn(() => "blob:stored");
    const revokeObjectURL = vi.fn();
    defineURLMethod("createObjectURL", createObjectURL);
    defineURLMethod("revokeObjectURL", revokeObjectURL);
    vi.mocked(getChatAttachmentContentBlob).mockResolvedValue(
      new Blob(["image"], { type: "image/png" }),
    );

    const { unmount } = render(<ChatImageAttachmentGallery attachments={[storedAttachment()]} />);

    await waitFor(() => expect(screen.getByRole("img", { name: "map.png" })).toBeVisible());
    expect(getChatAttachmentContentBlob).toHaveBeenCalledWith(
      "session-1",
      "att-1",
      expect.any(AbortSignal),
    );
    expect(screen.getByRole("img", { name: "map.png" })).toHaveAttribute("src", "blob:stored");

    unmount();
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:stored");
  });

  it("reserves the same geometry while loading and after the image resolves", async () => {
    let resolveBlob: ((blob: Blob) => void) | undefined;
    vi.mocked(getChatAttachmentContentBlob).mockImplementation(
      () =>
        new Promise<Blob>((resolve) => {
          resolveBlob = resolve;
        }),
    );
    defineURLMethod(
      "createObjectURL",
      vi.fn(() => "blob:stable"),
    );
    defineURLMethod("revokeObjectURL", vi.fn());

    render(<ChatImageAttachmentGallery attachments={[storedAttachment()]} />);

    const loading = screen.getByRole("status", { name: "Loading map.png" });
    expect(loading).toHaveStyle({ width: "132px", height: "96px" });

    act(() => resolveBlob?.(new Blob(["image"], { type: "image/png" })));
    const image = await screen.findByRole("img", { name: "map.png" });
    expect(image).toHaveAttribute("width", "132");
    expect(image).toHaveAttribute("height", "96");
    expect(image).toHaveStyle({ width: "100%", height: "100%" });
    expect(screen.getByRole("button", { name: "Open map.png" })).toHaveStyle({
      width: "132px",
      height: "96px",
    });
  });

  it("opens an accessible in-app preview and returns focus when dismissed", async () => {
    let notify: IntersectionObserverCallback = () => {};
    class MockIntersectionObserver implements IntersectionObserver {
      readonly root = null;
      readonly rootMargin = "240px 0px";
      readonly scrollMargin = "0px";
      readonly thresholds = [0];

      constructor(callback: IntersectionObserverCallback) {
        notify = callback;
      }

      observe() {}
      disconnect() {}
      takeRecords(): IntersectionObserverEntry[] {
        return [];
      }
      unobserve() {}
    }
    Object.defineProperty(globalThis, "IntersectionObserver", {
      configurable: true,
      value: MockIntersectionObserver,
    });
    defineURLMethod(
      "createObjectURL",
      vi.fn(() => "blob:preview"),
    );
    const revokeObjectURL = vi.fn();
    defineURLMethod("revokeObjectURL", revokeObjectURL);
    vi.mocked(getChatAttachmentContentBlob).mockResolvedValue(
      new Blob(["image"], { type: "image/png" }),
    );
    const user = userEvent.setup();

    render(<ChatImageAttachmentGallery attachments={[storedAttachment()]} />);

    act(() => {
      notify(
        [{ isIntersecting: true, intersectionRatio: 1 } as IntersectionObserverEntry],
        {} as IntersectionObserver,
      );
    });
    const trigger = await screen.findByRole("button", { name: "Open map.png" });
    await user.click(trigger);

    const dialog = screen.getByRole("dialog", { name: "Image preview: map.png" });
    expect(dialog).toBeVisible();
    expect(within(dialog).getByRole("img", { name: "map.png" })).toHaveAttribute(
      "src",
      "blob:preview",
    );
    expect(within(dialog).getByRole("button", { name: "Close" })).toHaveFocus();
    expect(trigger).toHaveAttribute("aria-expanded", "true");

    act(() => {
      notify(
        [{ isIntersecting: false, intersectionRatio: 0 } as IntersectionObserverEntry],
        {} as IntersectionObserver,
      );
    });

    await user.keyboard("{Escape}");

    expect(screen.queryByRole("dialog", { name: "Image preview: map.png" })).toBeNull();
    await waitFor(() => expect(screen.getByRole("button", { name: "Load map.png" })).toHaveFocus());
    expect(screen.queryByRole("img", { name: "map.png" })).toBeNull();
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:preview");
  });

  it("lets the operator load a deferred image before it intersects the viewport", async () => {
    class MockIntersectionObserver implements IntersectionObserver {
      readonly root = null;
      readonly rootMargin = "240px 0px";
      readonly scrollMargin = "0px";
      readonly thresholds = [0];

      observe() {}
      disconnect() {}
      takeRecords(): IntersectionObserverEntry[] {
        return [];
      }
      unobserve() {}
    }
    Object.defineProperty(globalThis, "IntersectionObserver", {
      configurable: true,
      value: MockIntersectionObserver,
    });
    defineURLMethod(
      "createObjectURL",
      vi.fn(() => "blob:manual"),
    );
    defineURLMethod("revokeObjectURL", vi.fn());
    let resolveBlob: ((blob: Blob) => void) | undefined;
    vi.mocked(getChatAttachmentContentBlob).mockImplementation(
      () =>
        new Promise<Blob>((resolve) => {
          resolveBlob = resolve;
        }),
    );
    const user = userEvent.setup();

    render(<ChatImageAttachmentGallery attachments={[storedAttachment()]} />);

    expect(screen.getByText("map.png")).toBeVisible();
    expect(getChatAttachmentContentBlob).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Load map.png" }));

    await waitFor(() => expect(getChatAttachmentContentBlob).toHaveBeenCalledTimes(1));
    expect(screen.getByRole("status", { name: "Loading map.png" })).toHaveFocus();
    act(() => resolveBlob?.(new Blob(["image"], { type: "image/png" })));
    expect(await screen.findByRole("img", { name: "map.png" })).toBeVisible();
    await waitFor(() => expect(screen.getByRole("button", { name: "Open map.png" })).toHaveFocus());
  });

  it("does not reclaim focus when the operator leaves a manually loading preview", async () => {
    class MockIntersectionObserver implements IntersectionObserver {
      readonly root = null;
      readonly rootMargin = "240px 0px";
      readonly scrollMargin = "0px";
      readonly thresholds = [0];

      observe() {}
      disconnect() {}
      takeRecords(): IntersectionObserverEntry[] {
        return [];
      }
      unobserve() {}
    }
    Object.defineProperty(globalThis, "IntersectionObserver", {
      configurable: true,
      value: MockIntersectionObserver,
    });
    defineURLMethod(
      "createObjectURL",
      vi.fn(() => "blob:no-focus-steal"),
    );
    defineURLMethod("revokeObjectURL", vi.fn());
    let resolveBlob: ((blob: Blob) => void) | undefined;
    vi.mocked(getChatAttachmentContentBlob).mockImplementation(
      () =>
        new Promise<Blob>((resolve) => {
          resolveBlob = resolve;
        }),
    );
    const user = userEvent.setup();

    render(
      <>
        <ChatImageAttachmentGallery attachments={[storedAttachment()]} />
        <button type="button">Elsewhere</button>
      </>,
    );
    await user.click(screen.getByRole("button", { name: "Load map.png" }));
    expect(screen.getByRole("status", { name: "Loading map.png" })).toHaveFocus();
    await user.click(screen.getByRole("button", { name: "Elsewhere" }));

    act(() => resolveBlob?.(new Blob(["image"], { type: "image/png" })));
    expect(await screen.findByRole("button", { name: "Open map.png" })).toBeVisible();
    expect(screen.getByRole("button", { name: "Elsewhere" })).toHaveFocus();
  });

  it("preserves focus when intersection automatically replaces a focused Load control", async () => {
    let notify: IntersectionObserverCallback = () => {};
    class MockIntersectionObserver implements IntersectionObserver {
      readonly root = null;
      readonly rootMargin = "240px 0px";
      readonly scrollMargin = "0px";
      readonly thresholds = [0];

      constructor(callback: IntersectionObserverCallback) {
        notify = callback;
      }

      observe() {}
      disconnect() {}
      takeRecords(): IntersectionObserverEntry[] {
        return [];
      }
      unobserve() {}
    }
    Object.defineProperty(globalThis, "IntersectionObserver", {
      configurable: true,
      value: MockIntersectionObserver,
    });
    vi.mocked(getChatAttachmentContentBlob).mockImplementation(() => new Promise<Blob>(() => {}));

    render(<ChatImageAttachmentGallery attachments={[storedAttachment()]} />);
    const loadButton = screen.getByRole("button", { name: "Load map.png" });
    act(() => loadButton.focus());
    act(() => {
      notify(
        [{ isIntersecting: true, intersectionRatio: 1 } as IntersectionObserverEntry],
        {} as IntersectionObserver,
      );
    });

    await waitFor(() =>
      expect(screen.getByRole("status", { name: "Loading map.png" })).toHaveFocus(),
    );
  });

  it("exposes deferred preview controls without a loading announcement and aborts distant requests", async () => {
    let notify: IntersectionObserverCallback = () => {};
    class MockIntersectionObserver implements IntersectionObserver {
      readonly root = null;
      readonly rootMargin = "240px 0px";
      readonly scrollMargin = "0px";
      readonly thresholds = [0];

      constructor(callback: IntersectionObserverCallback) {
        notify = callback;
      }

      observe() {}
      disconnect() {}
      takeRecords(): IntersectionObserverEntry[] {
        return [];
      }
      unobserve() {}
    }
    Object.defineProperty(globalThis, "IntersectionObserver", {
      configurable: true,
      value: MockIntersectionObserver,
    });
    vi.mocked(getChatAttachmentContentBlob).mockImplementation(() => new Promise<Blob>(() => {}));

    const { container, unmount } = render(
      <ChatImageAttachmentGallery attachments={[storedAttachment()]} />,
    );

    expect(screen.queryByRole("status", { name: "Loading map.png" })).toBeNull();
    expect(container.querySelector('[aria-hidden="true"]')).toBeNull();
    expect(screen.getByText("map.png")).toBeVisible();
    expect(screen.getByRole("button", { name: "Load map.png" })).toBeVisible();

    act(() => {
      notify(
        [
          {
            isIntersecting: true,
            intersectionRatio: 1,
          } as IntersectionObserverEntry,
        ],
        {} as IntersectionObserver,
      );
    });
    expect(await screen.findByRole("status", { name: "Loading map.png" })).toBeVisible();
    await waitFor(() => expect(getChatAttachmentContentBlob).toHaveBeenCalledTimes(1));
    const firstSignal = vi.mocked(getChatAttachmentContentBlob).mock.calls[0][2];
    expect(firstSignal?.aborted).toBe(false);

    act(() => {
      notify(
        [
          {
            isIntersecting: false,
            intersectionRatio: 0,
          } as IntersectionObserverEntry,
        ],
        {} as IntersectionObserver,
      );
    });
    await waitFor(() => expect(firstSignal?.aborted).toBe(true));
    expect(screen.queryByRole("status", { name: "Loading map.png" })).toBeNull();

    act(() => {
      notify(
        [
          {
            isIntersecting: true,
            intersectionRatio: 1,
          } as IntersectionObserverEntry,
        ],
        {} as IntersectionObserver,
      );
    });
    await waitFor(() => expect(getChatAttachmentContentBlob).toHaveBeenCalledTimes(2));
    const secondSignal = vi.mocked(getChatAttachmentContentBlob).mock.calls[1][2];
    expect(secondSignal?.aborted).toBe(false);

    unmount();
    expect(secondSignal?.aborted).toBe(true);
  });

  it("retries a failed stored image request on demand", async () => {
    defineURLMethod(
      "createObjectURL",
      vi.fn(() => "blob:retried"),
    );
    defineURLMethod("revokeObjectURL", vi.fn());
    let resolveRetry: ((blob: Blob) => void) | undefined;
    vi.mocked(getChatAttachmentContentBlob)
      .mockRejectedValueOnce(new Error("offline"))
      .mockImplementationOnce(
        () =>
          new Promise<Blob>((resolve) => {
            resolveRetry = resolve;
          }),
      );
    const user = userEvent.setup();
    render(<ChatImageAttachmentGallery attachments={[storedAttachment()]} />);

    await user.click(await screen.findByRole("button", { name: "Retry map.png" }));

    await waitFor(() => expect(getChatAttachmentContentBlob).toHaveBeenCalledTimes(2));
    expect(screen.getByRole("status", { name: "Loading map.png" })).toHaveFocus();
    act(() => resolveRetry?.(new Blob(["image"], { type: "image/png" })));
    expect(await screen.findByRole("img", { name: "map.png" })).toBeVisible();
    await waitFor(() => expect(screen.getByRole("button", { name: "Open map.png" })).toHaveFocus());
  });
});

describe("stored chat file attachments", () => {
  it("fetches non-images only after an explicit download and defers URL revocation", async () => {
    let releaseBlob: ((blob: Blob) => void) | undefined;
    vi.mocked(getChatAttachmentContentBlob).mockImplementation(
      () =>
        new Promise<Blob>((resolve) => {
          releaseBlob = resolve;
        }),
    );
    const createObjectURL = vi.fn(() => "blob:download");
    const revokeObjectURL = vi.fn();
    defineURLMethod("createObjectURL", createObjectURL);
    defineURLMethod("revokeObjectURL", revokeObjectURL);
    const anchorClick = vi
      .spyOn(HTMLAnchorElement.prototype, "click")
      .mockImplementation(() => undefined);
    let deferredRevoke: (() => void) | undefined;
    const setTimeoutSpy = vi.spyOn(globalThis, "setTimeout").mockImplementation(((
      callback: TimerHandler,
    ) => {
      deferredRevoke = callback as () => void;
      return 1;
    }) as typeof setTimeout);

    try {
      render(<ChatAttachmentGallery attachments={[storedFileAttachment()]} />);

      expect(screen.getByRole("group", { name: "Attached files" })).toBeVisible();
      expect(screen.queryByRole("img")).toBeNull();
      expect(getChatAttachmentContentBlob).not.toHaveBeenCalled();
      fireEvent.click(screen.getByRole("button", { name: "Download report.pdf" }));
      expect(getChatAttachmentContentBlob).toHaveBeenCalledWith(
        "session-1",
        "file-1",
        expect.any(AbortSignal),
      );

      await act(async () => {
        releaseBlob?.(new Blob(["report"], { type: "application/pdf" }));
        await Promise.resolve();
      });
      expect(createObjectURL).toHaveBeenCalledOnce();
      expect(anchorClick).toHaveBeenCalledOnce();
      expect(revokeObjectURL).not.toHaveBeenCalled();

      act(() => deferredRevoke?.());
      expect(revokeObjectURL).toHaveBeenCalledWith("blob:download");
      expect(screen.getByRole("status")).toHaveTextContent("report.pdf download started.");
    } finally {
      setTimeoutSpy.mockRestore();
      anchorClick.mockRestore();
    }
  });

  it("announces a guarded download failure and exposes an accessible retry", async () => {
    vi.mocked(getChatAttachmentContentBlob).mockRejectedValue(new Error("offline"));
    const user = userEvent.setup();
    render(<ChatAttachmentGallery attachments={[storedFileAttachment()]} />);

    await user.click(screen.getByRole("button", { name: "Download report.pdf" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Download failed. Try again.");
    expect(screen.getByRole("button", { name: "Retry download report.pdf" })).toHaveAttribute(
      "aria-describedby",
    );
    expect(screen.getByRole("status")).toHaveTextContent("report.pdf download failed.");
  });
});

function imageFile(name: string, type: string) {
  const bytes =
    type === "image/png"
      ? [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]
      : type === "image/jpeg"
        ? [0xff, 0xd8, 0xff]
        : type === "image/webp"
          ? [0x52, 0x49, 0x46, 0x46, 0, 0, 0, 0, 0x57, 0x45, 0x42, 0x50]
          : [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a];
  return new File([new Uint8Array(bytes)], name, { type });
}

function storedAttachment() {
  return {
    id: "att-1",
    session_id: "session-1",
    filename: "map.png",
    media_type: "image/png",
    size_bytes: 5,
    sha256: "abc",
    created_at: "2026-07-13T10:00:00Z",
    content_url: "/hecate/v1/chat/sessions/session-1/attachments/att-1/content",
  };
}

function storedFileAttachment() {
  return {
    id: "file-1",
    session_id: "session-1",
    filename: "report.pdf",
    media_type: "application/pdf",
    size_bytes: 6,
    sha256: "def",
    created_at: "2026-07-15T10:00:00Z",
    content_url: "/hecate/v1/chat/sessions/session-1/attachments/file-1/content",
  };
}

function defineURLMethod(name: "createObjectURL" | "revokeObjectURL", value: unknown) {
  Object.defineProperty(URL, name, { configurable: true, value });
}

function restoreURLMethod(
  name: "createObjectURL" | "revokeObjectURL",
  descriptor: PropertyDescriptor | undefined,
) {
  if (descriptor) Object.defineProperty(URL, name, descriptor);
  else delete (URL as unknown as Record<string, unknown>)[name];
}

function restoreGlobalProperty(name: string, descriptor: PropertyDescriptor | undefined) {
  if (descriptor) Object.defineProperty(globalThis, name, descriptor);
  else delete (globalThis as unknown as Record<string, unknown>)[name];
}
