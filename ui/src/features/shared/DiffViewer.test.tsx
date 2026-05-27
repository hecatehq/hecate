import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

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
  it("renders one rich diff host per file in a multi-file git patch", () => {
    const { container } = render(<DiffViewer diff={MULTI_FILE_DIFF} />);

    expect(screen.getByTestId("diff-viewer")).toBeInTheDocument();
    expect(container.querySelectorAll("diffs-container.diff-viewer-file")).toHaveLength(2);
  });

  it("falls back to the plain diff code block when the text is not parseable as a patch", () => {
    render(<DiffViewer diff="not a patch, just diagnostic output" />);

    expect(screen.queryByTestId("diff-viewer")).toBeNull();
    expect(screen.getByText("diff")).toBeInTheDocument();
    expect(screen.getByText("not a patch, just diagnostic output")).toBeInTheDocument();
  });
});
