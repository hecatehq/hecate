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
      strategy?: { matrix: { toolchain: string[] } };
      services?: Record<string, { image: string; options: string }>;
      steps?: {
        run?: string;
        if?: string;
        with?: Record<string, unknown>;
        env?: Record<string, string>;
      }[];
    }
  >;
};

test("Postgres checks use a disposable service and both supported toolchains", () => {
  const job = workflow.jobs["test-postgres"];
  expect(job.if).toContain("needs.changes.outputs.go == 'true'");
  expect(job.if).toContain("needs.changes.outputs.workflow == 'true'");
  expect(job.strategy?.matrix.toolchain).toEqual(["minimum", "stable"]);
  expect(job.services?.postgres.image).toBe("postgres:17-alpine");
  expect(job.services?.postgres.options).toContain("pg_isready");
  expect(job.steps).toContainEqual(
    expect.objectContaining({
      if: "matrix.toolchain == 'minimum'",
      with: expect.objectContaining({ "go-version-file": "go.mod" }),
    }),
  );
  expect(job.steps).toContainEqual(
    expect.objectContaining({
      if: "matrix.toolchain == 'stable'",
      with: expect.objectContaining({ "go-version": "stable" }),
    }),
  );
  const run = job.steps?.find((step) => step.run === "just test-postgres");
  expect(run?.env?.HECATE_POSTGRES_TEST_URL).toContain("${{ job.services.postgres.ports['5432'] }}");
});

test("Postgres failures block the required check and expensive native builds", () => {
  for (const name of ["required", "tauri-desktop", "tauri-mobile"]) {
    expect(workflow.jobs[name].needs).toContain("test-postgres");
    if (name !== "required") {
      expect(workflow.jobs[name].if).toContain("needs.test-postgres.result");
    }
  }
});
