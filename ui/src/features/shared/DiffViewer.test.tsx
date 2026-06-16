import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { DiffViewer } from "./DiffViewer";

const MULTI_FILE_DIFF = `diff --git a/src/a.ts b/src/a.ts
index 1111111..2222222 100644
--- a/src/a.ts
+++ b/src/a.ts
@@ -1 +1 @@
-old a
+new a
diff --git a/src/b.ts b/src/b.ts
index 3333333..4444444 100644
--- a/src/b.ts
+++ b/src/b.ts
@@ -1 +1 @@
-old b
+new b
`;

describe("DiffViewer", () => {
  afterEach(() => {
    document.documentElement.removeAttribute("data-theme");
  });

  it("uses the app light theme for rich diffs when the app is in light mode", () => {
    document.documentElement.setAttribute("data-theme", "light");

    render(<DiffViewer diff={MULTI_FILE_DIFF} />);

    expect(screen.getByTestId("diff-viewer")).toHaveAttribute("data-diff-theme", "light");
  });

  it("uses the app dark theme for rich diffs when the app is in dark mode", () => {
    document.documentElement.setAttribute("data-theme", "dark");

    render(<DiffViewer diff={MULTI_FILE_DIFF} />);

    expect(screen.getByTestId("diff-viewer")).toHaveAttribute("data-diff-theme", "dark");
  });

  it("renders one rich diff host per file in a multi-file git patch", () => {
    const { container } = render(<DiffViewer diff={MULTI_FILE_DIFF} />);

    expect(screen.getByTestId("diff-viewer")).toBeInTheDocument();
    expect(container.querySelectorAll("diffs-container.diff-viewer-file")).toHaveLength(2);
  });

  it("can render as an embedded preview without nested file chrome", () => {
    const { container } = render(<DiffViewer compact embedded diff={MULTI_FILE_DIFF} />);

    expect(screen.getByTestId("diff-viewer")).toHaveClass("diff-viewer-embedded");
    expect(container.querySelectorAll("diffs-container.diff-viewer-file")).toHaveLength(2);
  });

  it("keeps line numbers visible in compact previews", () => {
    render(<DiffViewer compact embedded diff={MULTI_FILE_DIFF} />);

    expect(screen.getByTestId("diff-viewer")).toHaveAttribute("data-line-numbers", "visible");
  });

  it("falls back to a lightweight diff view when the text is not parseable as a patch", () => {
    render(<DiffViewer diff="not a patch, just diagnostic output" />);

    expect(screen.queryByTestId("diff-viewer")).toBeNull();
    expect(screen.getByTestId("diff-viewer-raw")).toBeInTheDocument();
    expect(screen.getByText("not a patch, just diagnostic output")).toBeInTheDocument();
  });

  it("parses git patches even when diagnostics precede the diff header", () => {
    const { container } = render(
      <DiffViewer
        diff={`current diff · AGENTS.md
diff --git a/AGENTS.md b/AGENTS.md
index be6d4e94..256cc3b9 100644
--- a/AGENTS.md
+++ b/AGENTS.md
@@ -27,4 +27,8 @@ guidance live in [\`docs-ai/\`](docs-ai/README.md).
 
 When in doubt: read [\`docs-ai/core/project-context.md\`](docs-ai/core/project-context.md).
 
+## Local shell command policy
+
+- RTK may be used as a token-saving shell prefix.
+
 ## Codebase map
`}
      />,
    );

    expect(screen.getByTestId("diff-viewer")).toBeInTheDocument();
    expect(screen.queryByTestId("diff-viewer-raw")).toBeNull();
    expect(container.querySelectorAll("diffs-container.diff-viewer-file")).toHaveLength(1);
  });
});
