import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { TranscriptMarkdown } from "./TranscriptMarkdown";

describe("TranscriptMarkdown", () => {
  it("renders bold inline marks", () => {
    render(<TranscriptMarkdown content="this is **bold** text" />);
    expect(screen.getByText("bold").tagName).toBe("STRONG");
  });

  it("renders inline code with monospace styling", () => {
    render(<TranscriptMarkdown content="use `useState` to track state" />);
    const code = screen.getByText("useState");
    expect(code.tagName).toBe("CODE");
  });

  it("renders inline code inside bold labels", () => {
    const { container } = render(<TranscriptMarkdown content="**UI (`ui/`)**" />);

    expect(container.textContent).toBe("UI (ui/)");
    expect(screen.getByText("ui/").tagName).toBe("CODE");
    expect(screen.getByText("ui/").closest("strong")).not.toBeNull();
  });

  it("renders fenced code blocks", () => {
    render(<TranscriptMarkdown content={"```ts\nconst x = 1;\n```"} />);
    expect(screen.getByText(/const x = 1/)).toBeInTheDocument();
  });

  it("renders indented fenced code blocks as code blocks", () => {
    const { container } = render(
      <TranscriptMarkdown content={"- Review changes:\n  ```sh\ngit diff\n  ```"} />,
    );
    const pre = container.querySelector("pre");
    expect(pre).not.toBeNull();
    expect(pre?.textContent).toContain("git diff");
    expect(screen.queryByText("`")).toBeNull();
  });

  it("renders headings of varying levels", () => {
    const { container } = render(<TranscriptMarkdown content={"# H1\n## H2\n### H3\nbody"} />);
    expect(container.textContent).toContain("H1");
    expect(container.textContent).toContain("H2");
    expect(container.textContent).toContain("H3");
  });

  it("renders unordered lists", () => {
    render(<TranscriptMarkdown content={"- one\n- two\n- three"} />);
    expect(screen.getByText("one").tagName).toBe("LI");
    expect(screen.getByText("two").tagName).toBe("LI");
    expect(screen.getByText("three").tagName).toBe("LI");
  });

  it("renders ordered lists", () => {
    const { container } = render(<TranscriptMarkdown content={"1. first\n2. second"} />);
    const ol = container.querySelector("ol");
    expect(ol).not.toBeNull();
    expect(ol?.querySelectorAll("li")).toHaveLength(2);
  });

  it("renders task lists with completed and incomplete states", () => {
    render(<TranscriptMarkdown content={"- [x] done\n- [ ] todo"} />);
    expect(screen.getByLabelText("Completed task")).toBeInTheDocument();
    expect(screen.getByLabelText("Incomplete task")).toBeInTheDocument();
  });

  it("renders horizontal rules", () => {
    const { container } = render(<TranscriptMarkdown content={"before\n\n---\n\nafter"} />);
    expect(container.querySelector("hr")).not.toBeNull();
  });

  it("rewrites unsafe link hrefs to # while keeping link text", () => {
    render(<TranscriptMarkdown content="see [docs](javascript:alert(1)) for details" />);
    const link = screen.getByText("docs") as HTMLAnchorElement;
    expect(link.tagName).toBe("A");
    expect(link.getAttribute("href")).toBe("#");
  });

  it("preserves http(s) and mailto link hrefs", () => {
    render(<TranscriptMarkdown content="see [docs](https://example.com) and [me](mailto:x@y.z)" />);
    expect((screen.getByText("docs") as HTMLAnchorElement).getAttribute("href")).toBe(
      "https://example.com",
    );
    expect((screen.getByText("me") as HTMLAnchorElement).getAttribute("href")).toBe("mailto:x@y.z");
  });

  it("renders tables with headers and rows", () => {
    const md = "| Name | Age |\n|------|-----|\n| Ada  | 36  |\n| Bob  | 27  |";
    render(<TranscriptMarkdown content={md} />);
    expect(screen.getByText("Name")).toBeInTheDocument();
    expect(screen.getByText("Ada")).toBeInTheDocument();
    expect(screen.getByText("36")).toBeInTheDocument();
  });
});
