import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";

function workflow(name: string) {
  return Bun.YAML.parse(
    readFileSync(new URL(`../.github/workflows/${name}.yml`, import.meta.url), "utf8"),
  ) as {
    jobs: Record<
      string,
      {
        "runs-on": string;
        steps: { name?: string; run?: string }[];
      }
    >;
  };
}

test("UI browser checks use an explicit Playwright-supported runner", () => {
  const job = workflow("test").jobs["typescript-e2e"];
  expect(job["runs-on"]).toBe("ubuntu-24.04");
  expect(job.steps).toContainEqual(
    expect.objectContaining({ run: "bunx playwright install-deps chromium" }),
  );
  expect(job.steps).toContainEqual(expect.objectContaining({ run: "bun run test:e2e" }));
});

test("nightly installs the documentation search tool before running maintenance", () => {
  const steps = workflow("maintenance").jobs.maintenance.steps;
  const install = steps.findIndex((step) => step.name === "Install maintenance tools");
  const run = steps.findIndex((step) => step.run === "just maintenance-nightly");
  expect(install).toBeGreaterThanOrEqual(0);
  expect(run).toBeGreaterThan(install);
  expect(steps[install].run).toContain("sudo apt-get install -y ripgrep");
});
