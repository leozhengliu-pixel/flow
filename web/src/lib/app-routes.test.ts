import { describe, expect, it } from "vitest";
import {
  initiativePath,
  issuePath,
  automationNewPath,
  automationPath,
  automationRunsPath,
  diaryPath,
  labelPath,
  meetingPath,
  parseAppRoute,
  projectPath,
  projectSavedViewEditPath,
  projectSavedViewPath,
  releaseNotePath,
  routeBelongsToWorkspace,
  teamBoardPath,
  teamLinksPath,
  teamLoopsPath,
  teamMembersPath,
  teamResourcesPath,
  teamTriagePath,
  teamUpdatePath,
  teamUpdatesPath,
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

  it("generates canonical secondary routes", () => {
    expect(diaryPath("acme")).toBe("/acme/diary");
    expect(meetingPath("acme", "meeting 1")).toBe(
      "/acme/meeting/meeting%201",
    );
    expect(automationNewPath("acme")).toBe("/acme/automations/new");
    expect(automationPath("acme", "rule/1", true)).toBe(
      "/acme/automation/rule%2F1/edit",
    );
    expect(automationRunsPath("acme", "rule-1")).toBe(
      "/acme/automation/rule-1/runs",
    );
    expect(automationRunsPath("acme", "rule-1", "run-1")).toBe(
      "/acme/automation/rule-1/run/run-1",
    );
    expect(teamBoardPath("acme", "ENG")).toBe("/acme/team/ENG/board");
    expect(teamTriagePath("acme", "ENG")).toBe("/acme/team/ENG/triage");
    expect(teamUpdatesPath("acme", "ENG")).toBe("/acme/team/ENG/updates");
    expect(teamUpdatePath("acme", "ENG", "post 1")).toBe(
      "/acme/team/ENG/update/post%201",
    );
    expect(teamResourcesPath("acme", "ENG")).toBe(
      "/acme/team/ENG/resources",
    );
    expect(teamLinksPath("acme", "ENG")).toBe("/acme/team/ENG/links");
    expect(releaseNotePath("acme", "note 1")).toBe(
      "/acme/release-note/note%201",
    );
    expect(labelPath("acme", "issue", "bug report")).toBe(
      "/acme/issue-label/bug%20report",
    );
  });

  it("does not throw on malformed encoded route segments", () => {
    expect(parseAppRoute("/acme/document/%E0%A4%A").kind).toBe("document");
  });

  it("parses workspace and team secondary routes", () => {
    expect(parseAppRoute("/acme/diary")).toEqual({
      kind: "diary",
      workspaceSlug: "acme",
    });
    expect(parseAppRoute("/acme/meeting/meeting-1")).toEqual({
      kind: "meeting",
      workspaceSlug: "acme",
      meetingId: "meeting-1",
    });
    expect(parseAppRoute("/acme/automations/new")).toEqual({
      kind: "automation-new",
      workspaceSlug: "acme",
    });
    expect(parseAppRoute("/acme/automation/auto-1/runs")).toEqual({
      kind: "automation-runs",
      workspaceSlug: "acme",
      automationId: "auto-1",
    });
    expect(parseAppRoute("/acme/team/ENG/board")).toEqual({
      kind: "team-board",
      workspaceSlug: "acme",
      teamKey: "ENG",
    });
    expect(parseAppRoute("/acme/team/ENG/update/post-1")).toEqual({
      kind: "team-update",
      workspaceSlug: "acme",
      teamKey: "ENG",
      updateId: "post-1",
    });
    expect(parseAppRoute("/acme/release-note/note-1")).toEqual({
      kind: "release-note",
      workspaceSlug: "acme",
      releaseNoteId: "note-1",
    });
    expect(parseAppRoute("/acme/issue-label/bug")).toEqual({
      kind: "label",
      workspaceSlug: "acme",
      resourceName: "bug",
      resourceType: "issue",
    });
  });
});
