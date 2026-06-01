import { expect, test, mockGatewayAPIs, MOCK_SETTINGS_CONFIG_WITH_PROVIDERS } from "./fixtures";
import type { Page } from "@playwright/test";

// Most specs use the default empty-providers fixture and exercise the
// add-provider flow end-to-end. A few opt into a pre-populated state so
// they can pin row-level interactions (delete, edit) without first having
// to run the create flow.

test.beforeEach(async ({ page }) => {
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
  await page.locator(".hecate-activitybar [aria-label^='Connections']").click();
  await page.waitForSelector("text=Connections");
});

// Locator helpers — the preset cards' accessible names include the brand
// initial and the description, so name-based regex matches are brittle.
// Click via the inner text label (an exact-match span) instead, scoped to
// the open dialog so the same card text in the providers table doesn't
// collide.
function dialog(page: Page) {
  return page.getByRole("dialog");
}
function pickPreset(page: Page, name: string) {
  return dialog(page).getByText(name, { exact: true }).click();
}
async function pickCloudPreset(page: Page, name: string) {
  await dialog(page).getByRole("button", { name: "Cloud", exact: true }).click();
  await pickPreset(page, name);
}

test("empty state shows the placeholder and an Add provider CTA", async ({ page }) => {
  await expect(page.getByText("No model providers configured")).toBeVisible();
  await expect(page.getByRole("button", { name: /add provider/i }).first()).toBeVisible();
});

test("readiness repair card opens a blocked provider from Connections", async ({ page }) => {
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await mockGatewayAPIs(page, {
    settingsConfig: {
      providers: [
        {
          id: "anthropic",
          name: "Anthropic",
          preset_id: "anthropic",
          kind: "cloud",
          protocol: "anthropic",
          base_url: "https://api.anthropic.com/v1",
          enabled: true,
          credential_configured: false,
        },
      ],
      tenants: [],
      api_keys: [],
      policy_rules: [],
    },
  });
  await page.route("/hecate/v1/providers/status*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "provider_status",
        data: [
          {
            name: "anthropic",
            kind: "cloud",
            healthy: true,
            status: "healthy",
            credential_ready: false,
            credential_state: "missing",
            routing_ready: false,
            routing_blocked_reason: "credential_missing",
            models: ["claude-sonnet-4-6"],
            model_count: 1,
            readiness: {
              status: "blocked",
              reason: "credential_missing",
              message: "Anthropic needs an API key before Hecate can route requests.",
              operator_action: "Add or rotate the provider API key in Connections.",
            },
            readiness_checks: [
              {
                name: "credentials",
                status: "blocked",
                reason: "credential_missing",
                message: "Add credentials before Hecate can route requests to this provider.",
              },
              {
                name: "routing",
                status: "blocked",
                reason: "credential_missing",
                message: "Routing is blocked until credentials are configured.",
              },
            ],
          },
        ],
      }),
    }),
  );

  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
  await page.locator(".hecate-activitybar [aria-label^='Connections']").click();

  await expect(page.getByTestId("connections-provider-readiness-meaning")).toContainText(
    "1 provider needs attention",
  );
  await expect(
    page.getByText("Next: Add or rotate the provider API key in Connections."),
  ).toBeVisible();
  await page.getByRole("button", { name: "Open provider" }).click();
  await expect(page.getByRole("dialog")).toContainText("Anthropic · cloud");
});

test("Add provider modal opens on the Local tab by default", async ({ page }) => {
  await page
    .getByRole("button", { name: /add provider/i })
    .first()
    .click();
  // Ollama is a Local preset — its visibility proves the Local tab is
  // active without depending on a tab-specific aria attribute.
  await expect(dialog(page).getByText("Ollama", { exact: true })).toBeVisible();
  await expect(dialog(page).getByText("Running")).toBeVisible();
});

test("switching to the Cloud tab swaps the preset list", async ({ page }) => {
  await page
    .getByRole("button", { name: /add provider/i })
    .first()
    .click();
  await dialog(page).getByRole("button", { name: "Cloud", exact: true }).click();
  await expect(dialog(page).getByText("Anthropic", { exact: true })).toBeVisible();
  // Ollama is a Local preset — should not be visible on the Cloud tab.
  await expect(dialog(page).getByText("Ollama", { exact: true })).not.toBeVisible();
});

test("adding an Anthropic preset surfaces the row in the Cloud table", async ({ page }) => {
  await page
    .getByRole("button", { name: /add provider/i })
    .first()
    .click();
  await dialog(page).getByRole("button", { name: "Cloud", exact: true }).click();
  await pickPreset(page, "Anthropic");

  // Form pre-fills name from the preset.
  const nameInput = dialog(page).locator("input[type='text']").first();
  await expect(nameInput).toHaveValue("Anthropic");

  await dialog(page).locator("input[type='password']").fill("sk-test-key");
  await dialog(page).getByRole("button", { name: "Add provider", exact: true }).click();

  // Modal closes and the row appears in the providers table.
  await expect(page.getByText("Cloud providers")).toBeVisible();
  await expect(page.locator("tbody tr", { hasText: "Anthropic" })).toBeVisible();
});

test("adding a custom local provider surfaces the row with the entered URL", async ({ page }) => {
  await page
    .getByRole("button", { name: /add provider/i })
    .first()
    .click();
  await dialog(page).getByRole("button", { name: "Local", exact: true }).click();
  await pickPreset(page, "Custom");

  // Custom on Local: Name + Endpoint URL are editable, no API key.
  const inputs = dialog(page).locator("input[type='text']");
  await inputs.nth(0).fill("My Local");
  await inputs.nth(1).fill("http://127.0.0.1:9000/v1");

  await dialog(page).getByRole("button", { name: "Add provider", exact: true }).click();

  await expect(page.getByText("Local inference")).toBeVisible();
  await expect(page.locator("tbody tr", { hasText: "My Local" })).toBeVisible();
});

test("duplicate preset prompts for custom name but still blocks duplicate endpoint", async ({
  page,
}) => {
  const created: Array<{
    id: string;
    name: string;
    custom_name?: string;
    kind: string;
    protocol: string;
    base_url: string;
    enabled: boolean;
    credential_configured: boolean;
  }> = [];
  const slug = (name: string, customName?: string) => {
    const src = customName ? `${name} ${customName}` : name;
    return src
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "");
  };

  await page.route("/hecate/v1/settings*", async (route) => {
    const url = route.request().url();
    if (url.includes("/hecate/v1/settings/providers")) {
      await route.fallback();
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "settings",
        data: { providers: created, tenants: [], api_keys: [], policy_rules: [] },
      }),
    });
  });

  await page.route("/hecate/v1/settings/providers", async (route) => {
    if (route.request().method() !== "POST") {
      await route.fallback();
      return;
    }
    const body = JSON.parse(route.request().postData() ?? "{}") as {
      name?: string;
      custom_name?: string;
      base_url?: string;
      api_key?: string;
      kind?: string;
      protocol?: string;
    };
    const id = slug(body.name ?? "", body.custom_name);
    if (created.some((p) => p.id === id)) {
      await route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({ error: { message: `provider with id "${id}" already exists` } }),
      });
      return;
    }
    const record = {
      id,
      name: body.name ?? id,
      custom_name: body.custom_name,
      kind: body.kind || "cloud",
      protocol: body.protocol || "openai",
      base_url: body.base_url ?? "",
      enabled: true,
      credential_configured: !!body.api_key,
    };
    created.push(record);
    await route.fulfill({
      status: 201,
      contentType: "application/json",
      body: JSON.stringify({ object: "settings_provider", data: record }),
    });
  });

  // First instance — preset Name is locked and no custom name is needed.
  await page
    .getByRole("button", { name: /add provider/i })
    .first()
    .click();
  await pickCloudPreset(page, "Anthropic");
  await dialog(page).locator("input[type='password']").fill("sk-prod");
  await dialog(page).getByRole("button", { name: "Add provider", exact: true }).click();
  await expect(page.locator("tbody tr", { hasText: "Anthropic" })).toBeVisible();

  // Second instance — same preset. Without custom_name the id collision
  // tells the operator how to disambiguate the name.
  await page
    .getByRole("button", { name: /add provider/i })
    .first()
    .click();
  await pickCloudPreset(page, "Anthropic");
  await expect(dialog(page).getByText(/Anthropic is already configured/)).toBeVisible();

  // A different custom name resolves the id collision, but the default
  // Anthropic endpoint is still already taken. The modal should keep the
  // save button disabled and explain the endpoint collision even though
  // preset endpoint fields are hidden.
  await dialog(page).locator("input[type='text']").nth(1).fill("Dev");
  await expect(dialog(page).getByText(/Endpoint .* already used by/i)).toBeVisible();
  await dialog(page).locator("input[type='password']").fill("sk-dev");
  await expect(
    dialog(page).getByRole("button", { name: "Add provider", exact: true }),
  ).toBeDisabled();
  await expect(page.locator("tbody tr", { hasText: "Dev" })).toHaveCount(0);
});

test("conflict response surfaces the inline error inside the modal", async ({ page }) => {
  // Override the create route to return 409 unconditionally — the stateful
  // fixture would only return 409 on a real duplicate, and we want to pin
  // the inline-error path without juggling two adds.
  await page.route("/hecate/v1/settings/providers", async (route) => {
    if (route.request().method() === "POST") {
      await route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({
          error: {
            type: "invalid_request",
            message: 'base URL already used by provider "Primary"',
          },
        }),
      });
      return;
    }
    await route.continue();
  });

  await page
    .getByRole("button", { name: /add provider/i })
    .first()
    .click();
  await dialog(page).getByRole("button", { name: "Local", exact: true }).click();
  await pickPreset(page, "Ollama");
  await dialog(page).getByRole("button", { name: "Add provider", exact: true }).click();

  await expect(dialog(page).getByText(/base URL already used by provider/)).toBeVisible();
});

test("deleting a provider removes its row after confirmation", async ({ context }) => {
  // Use a fresh page with the populated config — the default fixture starts
  // empty and we want to skip the create-flow setup.
  const populated = await context.newPage();
  await mockGatewayAPIs(populated, { settingsConfig: MOCK_SETTINGS_CONFIG_WITH_PROVIDERS });
  await populated.goto("/");
  await populated.waitForSelector(".hecate-activitybar");
  await populated.locator(".hecate-activitybar [aria-label^='Connections']").click();
  await populated.waitForSelector("text=Cloud providers");

  populated.on("dialog", (d) => void d.accept());

  let deleteCalled = false;
  await populated.route("/hecate/v1/settings/providers/anthropic", async (route) => {
    if (route.request().method() === "DELETE") {
      deleteCalled = true;
    }
    await route.fallback();
  });

  // Trash button on the Anthropic row. Title attr is "Remove Anthropic".
  await populated.getByTitle("Remove Anthropic").click();
  await expect(populated.getByRole("dialog", { name: "Remove provider?" })).toBeVisible();
  await populated
    .getByRole("dialog", { name: "Remove provider?" })
    .getByRole("button", { name: "Remove provider", exact: true })
    .click();

  await expect.poll(() => deleteCalled).toBe(true);
  await expect(populated.locator("tbody tr", { hasText: "Anthropic" })).toHaveCount(0);
});

test("editing the custom name PATCHes /providers/{id} with the new custom_name", async ({
  context,
}) => {
  // Preset providers have a fixed Name (catalog join key); the
  // disambiguation flow is via custom_name. This test pins that
  // editing the Custom name field on a preset row produces a
  // PATCH with { custom_name }.
  const populated = await context.newPage();
  await mockGatewayAPIs(populated, { settingsConfig: MOCK_SETTINGS_CONFIG_WITH_PROVIDERS });
  await populated.goto("/");
  await populated.waitForSelector(".hecate-activitybar");
  await populated.locator(".hecate-activitybar [aria-label^='Connections']").click();
  await populated.waitForSelector("text=Cloud providers");

  let patchBody = "";
  await populated.route("/hecate/v1/settings/providers/anthropic", async (route) => {
    if (route.request().method() === "PATCH") {
      patchBody = route.request().postData() ?? "";
    }
    await route.fallback();
  });

  await populated.locator("tbody tr", { hasText: "Anthropic" }).click();
  const dlg = populated.getByRole("dialog");
  // Anthropic is a preset → Name section hidden, Custom name input is the
  // first text field in the modal.
  const customNameInput = dlg.locator("input[type='text']").first();
  await customNameInput.fill("Prod");
  await dlg.getByRole("button", { name: /save custom name/i }).click();

  await expect.poll(() => patchBody).toContain("Prod");
  expect(JSON.parse(patchBody)).toEqual({ custom_name: "Prod" });
});

test("editing a local endpoint URL PATCHes /providers/{id} with the new base_url", async ({
  context,
}) => {
  const populated = await context.newPage();
  await mockGatewayAPIs(populated, { settingsConfig: MOCK_SETTINGS_CONFIG_WITH_PROVIDERS });
  await populated.goto("/");
  await populated.waitForSelector(".hecate-activitybar");
  await populated.locator(".hecate-activitybar [aria-label^='Connections']").click();
  await populated.waitForSelector("text=Local inference");

  let patchBody = "";
  await populated.route("/hecate/v1/settings/providers/ollama", async (route) => {
    if (route.request().method() === "PATCH") {
      patchBody = route.request().postData() ?? "";
    }
    await route.fallback();
  });

  await populated.locator("tbody tr", { hasText: "Ollama" }).click();
  const dlg = populated.getByRole("dialog");
  // For preset providers the editable inputs are: Custom name (nth 0)
  // and Endpoint URL (nth 1). The Name section is hidden because preset
  // names are fixed.
  const urlInput = dlg.locator("input[type='text']").nth(1);
  await urlInput.fill("http://192.168.1.10:11434/v1");
  await dlg.getByRole("button", { name: /save url/i }).click();

  await expect.poll(() => patchBody).toContain("192.168.1.10");
  expect(JSON.parse(patchBody)).toEqual({ base_url: "http://192.168.1.10:11434/v1" });
});

test("breadcrumb returns from the form step to the preset picker", async ({ page }) => {
  await page
    .getByRole("button", { name: /add provider/i })
    .first()
    .click();
  await pickCloudPreset(page, "Anthropic");

  // Form is showing — Name field is pre-filled.
  await expect(dialog(page).locator("input[type='text']").first()).toHaveValue("Anthropic");

  await dialog(page)
    .getByRole("button", { name: /all providers/i })
    .click();

  // Back at the picker — Anthropic preset card is visible again, and the
  // password input from the form is gone.
  await expect(dialog(page).getByText("Anthropic", { exact: true })).toBeVisible();
  await expect(dialog(page).locator("input[type='password']")).toHaveCount(0);
});
