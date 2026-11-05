import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";

const workflow = Bun.YAML.parse(
  readFileSync(new URL("../.github/workflows/test.yml", import.meta.url), "utf8"),
) as {
  jobs: Record<
    string,
    {
      needs: string[] | string;
      if: string;
      outputs?: Record<string, string>;
      steps?: { id?: string; run?: string; if?: string; with?: { filters?: string } }[];
    }
  >;
};

const changes = workflow.jobs.changes;
const filterStep = changes.steps?.find((step) => step.id === "filter");
const filters = Bun.YAML.parse(filterStep?.with?.filters ?? "{}") as Record<string, string[]>;
const matches = (filter: string, path: string) =>
  (filters[filter] ?? []).some((pattern) => new Bun.Glob(pattern).match(path));

for (const path of ["ui/package.json", "ui/bun.lock"]) {
  test(`${path} selects the updater guard without selecting native packages`, () => {
    expect(matches("updater_deps", path)).toBe(true);
    expect(changes.outputs?.updater_deps).toBe("${{ steps.filter.outputs.updater_deps }}");
    expect(workflow.jobs["test-tauri-rust"].if.trim().split(/\s*\|\|\s*/)).toContain(
      "needs.changes.outputs.updater_deps == 'true'",
    );
    for (const filter of ["tauri", "mobile", "desktop", "workflow"]) {
      expect(matches(filter, path)).toBe(false);
    }
  });
}

for (const path of [
  "ui/src/lib/desktop-update.ts",
  "ui/src/styles.css",
  "docs/operator/security.md",
]) {
  test(`${path} does not select the dependency-only filter`, () => {
    expect(matches("updater_deps", path)).toBe(false);
  });
}

test("the selected Rust job runs the guard unconditionally and blocks required checks", () => {
  const rust = workflow.jobs["test-tauri-rust"];
  expect(rust.needs).toBe("changes");
  const guard = rust.steps?.find(
    (step) => step.run === "bun tauri/scripts/check-updater-dependencies.ts",
  );
  expect(guard).toBeDefined();
  expect(guard?.if).toBeUndefined();
  expect(workflow.jobs.required.needs).toContain("test-tauri-rust");
  for (const name of ["tauri-mobile", "tauri-desktop"]) {
    expect(workflow.jobs[name].if).not.toContain("needs.changes.outputs.updater_deps");
  }
});

test("updater path-gating regressions run before change filtering", () => {
  const steps = changes.steps ?? [];
  const validation = steps.findIndex((step) =>
    step.run?.includes("scripts/updater-workflow.test.ts"),
  );
  expect(validation).toBeGreaterThanOrEqual(0);
  expect(validation).toBeLessThan(steps.findIndex((step) => step.id === "filter"));
  expect(steps[validation]?.if).toBeUndefined();
});
