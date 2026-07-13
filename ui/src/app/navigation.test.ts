import { describe, expect, it } from "vitest";

import {
  isPlainNavigationClick,
  navigationURLsEqual,
  parseNavigationURL,
  projectNavigationURL,
  workspaceNavigationURL,
  WORKSPACE_PATHS,
} from "./navigation";

const navigationClick = (
  overrides: Partial<Parameters<typeof isPlainNavigationClick>[0]> = {},
): Parameters<typeof isPlainNavigationClick>[0] => ({
  altKey: false,
  button: 0,
  ctrlKey: false,
  defaultPrevented: false,
  metaKey: false,
  shiftKey: false,
  ...overrides,
});

describe("workspace navigation URLs", () => {
  it.each([
    ["/chats", "chats"],
    ["/projects", "projects"],
    ["/tasks", "runs"],
    ["/connections", "connections"],
    ["/observability", "overview"],
    ["/usage", "usage"],
    ["/settings", "settings"],
  ] as const)("maps %s to the internal %s workspace", (path, workspace) => {
    expect(parseNavigationURL(path)).toMatchObject({ workspace, isCanonical: true });
    expect(WORKSPACE_PATHS[workspace]).toBe(path);
  });

  it("does not claim unknown application paths", () => {
    expect(parseNavigationURL("/unknown?project=opaque#section")).toEqual({
      workspace: null,
      project: null,
      canonicalURL: "/unknown?project=opaque#section",
      isCanonical: true,
    });
  });

  it("canonicalizes a trailing slash without losing query parameters or the hash", () => {
    expect(parseNavigationURL("/usage/?source=desktop#totals")).toEqual({
      workspace: "usage",
      project: null,
      canonicalURL: "/usage?source=desktop#totals",
      isCanonical: false,
    });
  });

  it("switches workspaces while preserving unrelated URL state", () => {
    expect(
      workspaceNavigationURL(
        "/projects?source=notification&project=proj_1&view=work&work=work_1#evidence",
        "runs",
      ),
    ).toBe("/tasks?source=notification#evidence");
  });

  it("removes stale project keys from parsed non-project workspaces", () => {
    expect(
      parseNavigationURL(
        "/connections?source=notification&project=proj_1&view=skills&work=work_1#providers",
      ),
    ).toEqual({
      workspace: "connections",
      project: null,
      canonicalURL: "/connections?source=notification#providers",
      isCanonical: false,
    });
  });
});

describe("native navigation clicks", () => {
  it("intercepts only unmodified primary-button navigation", () => {
    expect(isPlainNavigationClick(navigationClick())).toBe(true);
    expect(isPlainNavigationClick(navigationClick({ button: 1 }))).toBe(false);
    expect(isPlainNavigationClick(navigationClick({ ctrlKey: true }))).toBe(false);
    expect(isPlainNavigationClick(navigationClick({ metaKey: true }))).toBe(false);
    expect(isPlainNavigationClick(navigationClick({ shiftKey: true }))).toBe(false);
    expect(isPlainNavigationClick(navigationClick({ altKey: true }))).toBe(false);
    expect(isPlainNavigationClick(navigationClick({ defaultPrevented: true }))).toBe(false);
  });
});

describe("project navigation URLs", () => {
  it("parses an exact project work-item destination", () => {
    expect(parseNavigationURL("/projects?project=proj_1&view=work&work=work_1")).toEqual({
      workspace: "projects",
      project: { projectID: "proj_1", view: "work", workItemID: "work_1" },
      canonicalURL: "/projects?project=proj_1&view=work&work=work_1",
      isCanonical: true,
    });
  });

  it("makes a work-item destination the Work view", () => {
    expect(parseNavigationURL("/projects?project=proj_1&view=timeline&work=work_1")).toEqual({
      workspace: "projects",
      project: { projectID: "proj_1", view: "work", workItemID: "work_1" },
      canonicalURL: "/projects?project=proj_1&view=work&work=work_1",
      isCanonical: false,
    });
  });

  it("removes project child state when no project is addressed", () => {
    expect(parseNavigationURL("/projects?view=memory&work=orphan")).toMatchObject({
      project: { projectID: null, view: "overview", workItemID: null },
      canonicalURL: "/projects",
      isCanonical: false,
    });
    expect(
      projectNavigationURL("/projects?source=inbox", {
        projectID: null,
        view: "work",
        workItemID: "orphan",
      }),
    ).toBe("/projects?source=inbox");
  });

  it("canonicalizes an unknown or explicit Overview view to the omitted default", () => {
    expect(parseNavigationURL("/projects?project=proj_1&view=internal")).toMatchObject({
      project: { projectID: "proj_1", view: "overview", workItemID: null },
      canonicalURL: "/projects?project=proj_1",
      isCanonical: false,
    });
    expect(parseNavigationURL("/projects?project=proj_1&view=overview")).toMatchObject({
      project: { projectID: "proj_1", view: "overview", workItemID: null },
      canonicalURL: "/projects?project=proj_1",
      isCanonical: false,
    });
  });

  it("roundtrips opaque project and work-item IDs through URLSearchParams", () => {
    const projectID = " project/with?reserved&characters=and + unicode λ ";
    const workItemID = "work/#42?review=yes&owner=A+B π";
    const destination = projectNavigationURL("/chats?source=inbox#assignment", {
      projectID,
      view: "timeline",
      workItemID,
    });

    expect(destination).toContain("source=inbox");
    expect(destination.endsWith("#assignment")).toBe(true);
    expect(parseNavigationURL(destination)).toMatchObject({
      workspace: "projects",
      project: { projectID, view: "work", workItemID },
      isCanonical: true,
    });
  });

  it("serializes every supporting project view and omits Overview", () => {
    const current = "/projects?source=command&project=old&view=work&work=old#project";

    expect(projectNavigationURL(current, { projectID: "proj_2" })).toBe(
      "/projects?source=command&project=proj_2#project",
    );
    expect(projectNavigationURL(current, { projectID: "proj_2", view: "timeline" })).toBe(
      "/projects?source=command&project=proj_2&view=timeline#project",
    );
    expect(projectNavigationURL(current, { projectID: "proj_2", view: "memory" })).toBe(
      "/projects?source=command&project=proj_2&view=memory#project",
    );
    expect(projectNavigationURL(current, { projectID: "proj_2", view: "skills" })).toBe(
      "/projects?source=command&project=proj_2&view=skills#project",
    );
    expect(projectNavigationURL(current, { projectID: "proj_2", view: "work" })).toBe(
      "/projects?source=command&project=proj_2&view=work#project",
    );
  });

  it("accepts location-shaped inputs and compares normalized destinations", () => {
    const location = {
      pathname: "/projects/",
      search: "?project=proj_1",
      hash: "#overview",
    };

    expect(parseNavigationURL(location)).toMatchObject({
      workspace: "projects",
      canonicalURL: "/projects?project=proj_1#overview",
      isCanonical: false,
    });
    expect(
      navigationURLsEqual(
        projectNavigationURL(location, { projectID: "proj_1" }),
        "/projects?project=proj_1#overview",
      ),
    ).toBe(true);
  });
});
