import { afterEach, expect, it, vi } from "vitest";
import { steerTaskRun } from "./api";

afterEach(() => vi.unstubAllGlobals());

it("sends live guidance as one JSON object with an idempotency key", async () => {
  const fetch = vi.fn(
    async () =>
      new Response(
        JSON.stringify({
          object: "task.run.steering",
          data: { id: "guidance", status: "pending" },
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
  );
  vi.stubGlobal("fetch", fetch);
  await steerTaskRun("task/one", "run/one", "Inspect first", "delivery-id");
  expect(fetch).toHaveBeenCalledWith(
    "/hecate/v1/tasks/task%2Fone/runs/run%2Fone/steer",
    expect.objectContaining({
      method: "POST",
      body: JSON.stringify({ client_request_id: "delivery-id", content: "Inspect first" }),
    }),
  );
});
