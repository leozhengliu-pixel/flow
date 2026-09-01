import { describe, expect, it } from "vitest";
import {
  initiativePath,
  issuePath,
  parseAppRoute,
  projectPath,
  projectSavedViewEditPath,
  projectSavedViewPath,
  routeBelongsToWorkspace,
  teamLoopsPath,
  teamMembersPath,
  teamViewsNewPath,
  workspaceSavedViewPath,
  workspaceViewsNewPath,
} from "./app-routes";
import type { Initiative, Issue, Project } from "@/types/flow";

describe("application routes", () => {
  it("parses workspace, team, detail, and settings routes", () => {
    expect(parseAppRoute("/acme/projects/all")).toMatchObject({
      kind: "projects",
      workspaceSlug: "acme",
    });
    expect(parseAppRoute("/acme/team/ENG/backlog")).toEqual({
      kind: "team-issues",
      workspaceSlug: "acme",
      teamKey: "ENG",
      view: "backlog",
    });
    expect(parseAppRoute("/acme/team/ENG/loops")).toEqual({
      kind: "team-loops",
      workspaceSlug: "acme",
      teamKey: "ENG",
    });
    expect(parseAppRoute("/acme/loops/new", "?draftId=draft-loop-1")).toEqual({
      kind: "loop-editor",
      workspaceSlug: "acme",
      draftId: "draft-loop-1",
    });
    expect(parseAppRoute("/acme/team/ENG/members")).toEqual({
      kind: "team-members",
      workspaceSlug: "acme",
      teamKey: "ENG",
    });
    expect(parseAppRoute("/acme/settings/account/preferences")).toEqual({
      kind: "settings",
      workspaceSlug: "acme",
      page: "preferences",
    });
    expect(parseAppRoute("/")).toEqual({ kind: "root" });
  });

  it("generates encoded entity and view paths", () => {
    expect(
      issuePath("acme", {
        identifier: "ENG-42",
        title: "Repair login flow",
      } as Issue),
    ).toBe("/acme/issue/ENG-42/repair-login-flow");
    expect(
      projectPath("acme", { slugId: "platform migration" } as Project),
    ).toBe("/acme/project/platform%20migration/overview");
    expect(
      initiativePath(
        "acme",
        { slugId: "north-star" } as Initiative,
        "projects",
      ),
    ).toBe("/acme/initiative/north-star/projects");
    expect(workspaceViewsNewPath("acme", "projects")).toBe(
      "/acme/views/projects/new",
    );
    expect(teamViewsNewPath("acme", "ENG", "issues")).toBe(
      "/acme/team/ENG/views/issues/new",
    );
    expect(teamLoopsPath("acme", "ENG")).toBe("/acme/team/ENG/loops");
    expect(teamMembersPath("acme", "ENG")).toBe("/acme/team/ENG/members");
    expect(projectSavedViewPath("acme", "platform", "view-1")).toBe(
      "/acme/project/platform/view/view-1",
    );
    expect(projectSavedViewEditPath("acme", "platform", "view-1")).toBe(
      "/acme/project/platform/view/view-1/edit",
    );
    expect(workspaceSavedViewPath("acme", "view-1")).toBe("/acme/view/view-1");
    expect(parseAppRoute("/acme/project/platform/view/view-1")).toEqual({
      kind: "project-saved-view",
      workspaceSlug: "acme",
      projectSlugId: "platform",
      viewId: "view-1",
    });
    expect(parseAppRoute("/acme/project/platform/view/view-1/edit")).toEqual({
      kind: "project-saved-view",
      workspaceSlug: "acme",
      projectSlugId: "platform",
      viewId: "view-1",
      editing: true,
    });
  });

  it("rejects routes from another workspace", () => {
    expect(routeBelongsToWorkspace(parseAppRoute("/acme/inbox"), "acme")).toBe(
      true,
    );
    expect(routeBelongsToWorkspace(parseAppRoute("/other/inbox"), "acme")).toBe(
      false,
    );
  });
});
