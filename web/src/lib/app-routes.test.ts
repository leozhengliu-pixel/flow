import { describe, expect, it } from "vitest";
import {
  initiativePath,
  settingsPath,
  usageSettingsPath,
  issuePath,
  automationNewPath,
  automationPath,
  automationRunsPath,
  apiKeyPath,
  apiKeyEditPath,
  diaryPath,
  welcomePath,
  meetingsPath,
  inboxPath,
  labelPath,
  meetingPath,
  editLoopPath,
  loopPath,
  loopRunPath,
  loopsViewPath,
  memberProfileHandle,
  memberProfilePath,
  newAPIKeyPath,
  newLoopPath,
  newIssuePath,
  newSigningKeyPath,
  parseAppRoute,
  releasePipelinePath,
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
    expect(parseAppRoute("/acme/team/ENG/loops/new")).toEqual({ kind: "loop-editor", workspaceSlug: "acme", teamKey: "ENG" });
    expect(parseAppRoute("/acme/team/ENG/loops/new", "?draftId=draft-loop-1")).toEqual({
      kind: "loop-editor",
      workspaceSlug: "acme",
      teamKey: "ENG",
      draftId: "draft-loop-1",
    });
    expect(parseAppRoute("/acme/loops")).toEqual({ kind: "loops", workspaceSlug: "acme" });
    expect(parseAppRoute("/acme/loops/my-loops")).toEqual({ kind: "loops", workspaceSlug: "acme", view: "mine" });
    expect(parseAppRoute("/acme/loops/all")).toEqual({ kind: "loops", workspaceSlug: "acme", view: "all" });
    expect(parseAppRoute("/acme/loops/constructor")).toEqual({ kind: "loop", workspaceSlug: "acme", loopId: "constructor" });
    expect(loopsViewPath("acme", "mine")).toBe("/acme/loops/my-loops");
    expect(loopsViewPath("acme", "all")).toBe("/acme/loops/all");
    // Loop pages use the slug; a loop without one (or a bare reference) keeps its id.
    const loop = { id: "loop_1", slugId: "triage-agent-96b66416bc6a" };
    expect(loopPath("acme", loop)).toBe("/acme/loop/triage-agent-96b66416bc6a");
    expect(editLoopPath("acme", loop)).toBe("/acme/loop/triage-agent-96b66416bc6a/edit");
    expect(loopRunPath("acme", loop, "run-9")).toBe("/acme/loop/triage-agent-96b66416bc6a/run/run-9");
    expect(loopRunPath("acme", loop)).toBe("/acme/loop/triage-agent-96b66416bc6a/runs");
    expect(loopPath("acme", { id: "loop_1" })).toBe("/acme/loop/loop_1");
    expect(parseAppRoute("/acme/loop/triage-agent-96b66416bc6a")).toEqual({ kind: "loop", workspaceSlug: "acme", loopId: "triage-agent-96b66416bc6a" });
    expect(parseAppRoute("/acme/loop/loop-1")).toEqual({ kind: "loop", workspaceSlug: "acme", loopId: "loop-1" });
    expect(parseAppRoute("/acme/loops/loop-1")).toEqual({ kind: "loop", workspaceSlug: "acme", loopId: "loop-1" });
    expect(parseAppRoute("/acme/loop/loop-1/edit")).toEqual({ kind: "loop-editor", workspaceSlug: "acme", loopId: "loop-1" });
    expect(parseAppRoute("/acme/loop/loop-1/run/run-9")).toEqual({ kind: "loop-run", workspaceSlug: "acme", loopId: "loop-1", runId: "run-9" });
    expect(parseAppRoute("/acme/loop/loop-1/runs")).toEqual({ kind: "loop-run", workspaceSlug: "acme", loopId: "loop-1" });
    expect(parseAppRoute("/acme/team/ENG/members")).toEqual({
      kind: "team-members",
      workspaceSlug: "acme",
      teamKey: "ENG",
    });
    expect(parseAppRoute("/acme/settings/authentication")).toEqual({
      kind: "settings",
      workspaceSlug: "acme",
      page: "authentication",
    });
    expect(parseAppRoute("/acme/settings/account/preferences")).toEqual({
      kind: "settings",
      workspaceSlug: "acme",
      page: "preferences",
    });
    expect(parseAppRoute("/acme/settings/account/shortcuts")).toEqual({
      kind: "settings",
      workspaceSlug: "acme",
      page: "shortcuts",
    });

    expect(parseAppRoute("/acme/settings/asks")).toEqual({
      kind: "settings",
      workspaceSlug: "acme",
      page: "asks",
    });
    expect(parseAppRoute("/acme/settings/asks/email-intake/new")).toEqual({
      kind: "settings",
      workspaceSlug: "acme",
      page: "asks",
      asksEmailIntakeMode: "new",
    });
    expect(parseAppRoute("/acme/settings/asks/slack-conn-1")).toEqual({
      kind: "settings",
      workspaceSlug: "acme",
      page: "asks",
      asksIntegrationId: "slack-conn-1",
    });

    expect(
      parseAppRoute("/acme/settings/account/security/api-keys/new"),
    ).toEqual({
      kind: "settings",
      workspaceSlug: "acme",
      page: "account-security",
      apiKeyMode: "new",
    });
    expect(
      parseAppRoute("/acme/settings/account/security/commit-signing-key"),
    ).toEqual({
      kind: "settings",
      workspaceSlug: "acme",
      page: "account-security",
      signingKeyMode: "new",
    });
    expect(
      parseAppRoute("/acme/settings/account/security/api-keys/key%2F1"),
    ).toEqual({
      kind: "settings",
      workspaceSlug: "acme",
      page: "account-security",
      apiKeyMode: "detail",
      apiKeyId: "key/1",
    });
    expect(
      parseAppRoute("/acme/settings/account/security/api-keys/key%2F1/edit"),
    ).toEqual({
      kind: "settings",
      workspaceSlug: "acme",
      page: "account-security",
      apiKeyMode: "edit",
      apiKeyId: "key/1",
    });
    expect(parseAppRoute("/")).toEqual({ kind: "root" });
    expect(parseAppRoute("/acme/inbox/priority")).toEqual({ kind: "inbox", workspaceSlug: "acme", tab: "priority" });
    expect(parseAppRoute("/acme/inbox/other")).toEqual({ kind: "inbox", workspaceSlug: "acme", tab: "other" });
  });

  it("generates encoded entity and view paths", () => {
    expect(inboxPath("acme", "priority")).toBe("/acme/inbox/priority");
    expect(inboxPath("acme", "other")).toBe("/acme/inbox/other");
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
    expect(newAPIKeyPath("acme")).toBe(
      "/acme/settings/account/security/api-keys/new",
    );
    expect(newSigningKeyPath("acme")).toBe(
      "/acme/settings/account/security/commit-signing-key",
    );
    expect(apiKeyPath("acme", "key/1")).toBe(
      "/acme/settings/account/security/api-keys/key%2F1",
    );
    expect(apiKeyEditPath("acme", "key/1")).toBe(
      "/acme/settings/account/security/api-keys/key%2F1/edit",
    );
    expect(teamViewsNewPath("acme", "ENG", "issues")).toBe(
      "/acme/team/ENG/views/issues/new",
    );
    expect(teamLoopsPath("acme", "ENG")).toBe("/acme/team/ENG/loops");
    expect(newLoopPath("acme")).toBe("/acme/loops/new");
    expect(newLoopPath("acme", "ENG")).toBe("/acme/team/ENG/loops/new");
    expect(teamMembersPath("acme", "ENG")).toBe("/acme/team/ENG/members");
    expect(memberProfilePath("acme", memberProfileHandle({ name: "Jane Doe", username: "jane.doe" }))).toBe("/acme/profiles/jane.doe");
    expect(memberProfilePath("acme", memberProfileHandle({ name: "Jane Doe" }), "created")).toBe("/acme/profiles/Jane%20Doe/created");
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
    expect(welcomePath("acme")).toBe("/acme/welcome");
    expect(meetingsPath("acme")).toBe("/acme/meetings");
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
    expect(parseAppRoute("/acme/welcome")).toEqual({
      kind: "welcome",
      workspaceSlug: "acme",
    });
    expect(parseAppRoute("/acme/meetings")).toEqual({
      kind: "meetings",
      workspaceSlug: "acme",
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
  it("parses pipeline deleted releases and archive tabs", () => {
    expect(parseAppRoute("/acme/pipeline/app/releases/deleted")).toEqual({
      kind: "release-pipeline",
      workspaceSlug: "acme",
      pipelineSlug: "app",
      tab: "deleted",
    });
    expect(parseAppRoute("/acme/pipeline/app/releases/archived")).toEqual({
      kind: "release-pipeline",
      workspaceSlug: "acme",
      pipelineSlug: "app",
      tab: "archive",
    });
    expect(releasePipelinePath("acme", "app", "deleted")).toBe(
      "/acme/pipeline/app/releases/deleted",
    );
  });

});

describe('settings aliases and deep links', () => {
  it('maps reference settings URLs onto Flow pages', () => {
    expect(parseAppRoute('/acme/settings/ai/coding-sessions')).toMatchObject({ kind: 'settings', page: 'coding-sessions' })
    expect(parseAppRoute('/acme/settings/ai/coding-sessions/environments')).toMatchObject({ kind: 'settings', page: 'coding-environments' })
    expect(parseAppRoute('/acme/settings/skill/skill_1/edit')).toMatchObject({ kind: 'settings', page: 'agents', agentSkillMode: 'edit', agentSkillId: 'skill_1' })
    expect(parseAppRoute('/acme/settings/workspace/welcome-message')).toMatchObject({ kind: 'settings', page: 'workspace', workspaceView: 'welcome-message' })
    expect(parseAppRoute('/acme/settings/billing')).toEqual({ kind: 'settings', workspaceSlug: 'acme', page: 'billing' })
    expect(parseAppRoute('/acme/settings/usage')).toEqual({ kind: 'settings', workspaceSlug: 'acme', page: 'usage' })
    expect(parseAppRoute('/acme/settings/usage/analytics')).toMatchObject({ page: 'usage', usageView: 'analytics' })
    expect(parseAppRoute('/acme/settings/usage/spend-limits')).toMatchObject({ page: 'usage', usageView: 'spend-limits' })
    expect(parseAppRoute('/acme/settings/usage/spend-limits/users')).toMatchObject({ page: 'usage', usageView: 'user-limits' })
    expect(parseAppRoute('/acme/settings/usage/spend-limits/loops')).toMatchObject({ page: 'usage', usageView: 'loop-limits' })
    expect(usageSettingsPath('acme', 'user-limits')).toBe('/acme/settings/usage/spend-limits/users')
    expect(usageSettingsPath('acme')).toBe('/acme/settings/usage')
    expect(parseAppRoute('/acme/settings/asks/email-intake/addr-1/edit')).toMatchObject({ kind: 'settings', page: 'asks', asksEmailIntakeId: 'addr-1', asksEmailIntakeMode: 'edit' })
    expect(parseAppRoute('/acme/settings/asks/email-intake/new')).toMatchObject({ page: 'asks', asksEmailIntakeMode: 'new' })
    expect(parseAppRoute('/acme/settings/asks/web-forms/form-1/pages/new')).toEqual({ kind: 'settings', workspaceSlug: 'acme', page: 'asks' })
    expect(parseAppRoute('/acme/settings/teams/ENG/retire')).toMatchObject({ page: 'team', teamKey: 'ENG', teamSection: 'overview', teamSubPath: 'retire' })
    expect(parseAppRoute('/acme/settings/teams/ENG/triage/memories')).toMatchObject({ page: 'team', teamSection: 'triage' })
    expect(settingsPath('acme', 'team', 'ENG', 'overview', 'set-parent')).toBe('/acme/settings/teams/ENG/set-parent')
    expect(parseAppRoute('/acme/settings/account/code-and-reviews/coding-tools')).toMatchObject({ kind: 'settings', page: 'code-and-reviews', accountView: 'coding-tools' })
    expect(parseAppRoute('/acme/settings/labels')).toMatchObject({ kind: 'settings', page: 'issue-labels' })
    expect(parseAppRoute('/acme/settings/teams/ENG/labels')).toMatchObject({ kind: 'settings', page: 'team', teamKey: 'ENG', teamSection: 'issue-labels' })
  })

  it('parses API, webhook, loops, and notification sub-pages', () => {
    expect(parseAppRoute('/acme/settings/api/keys')).toMatchObject({ kind: 'settings', page: 'api', apiView: 'keys' })
    expect(parseAppRoute('/acme/settings/api/webhooks/new')).toMatchObject({ kind: 'settings', page: 'api', webhookId: 'new' })
    expect(parseAppRoute('/acme/settings/api/webhooks/hook_1/edit')).toMatchObject({ kind: 'settings', page: 'api', webhookId: 'hook_1' })
    expect(parseAppRoute('/acme/settings/loops')).toMatchObject({ kind: 'settings', page: 'loops' })
    expect(parseAppRoute('/acme/settings/account/notifications/priority-filter')).toMatchObject({ kind: 'settings', page: 'notifications', notificationChannel: 'priority-filter' })
    expect(parseAppRoute('/acme/settings/teams/ENG/project-labels')).toMatchObject({ kind: 'settings', page: 'team', teamSection: 'project-labels' })
    expect(settingsPath('acme', 'coding-sessions')).toBe('/acme/settings/ai/coding-sessions')
    expect(parseAppRoute('/acme/settings/teams/ENG/templates/issue/new')).toMatchObject({ page: 'team', teamSection: 'templates', teamSubPath: 'issue/new' })
    expect(parseAppRoute('/acme/settings/teams/ENG/templates/project/tpl_1/edit')).toMatchObject({ teamSection: 'templates', teamSubPath: 'project/tpl_1/edit' })
    expect(parseAppRoute('/acme/settings/teams/ENG/recurring-issues/new')).toMatchObject({ teamSection: 'recurring-issues', teamSubPath: 'new' })
    expect(settingsPath('acme', 'team', 'ENG', 'templates', 'issue/new')).toBe('/acme/settings/teams/ENG/templates/issue/new')
  })

  it('opens bare views directories on the issue views tab', () => {
    expect(parseAppRoute('/acme/views')).toEqual({ kind: 'workspace-views', workspaceSlug: 'acme', resource: 'issues' })
    expect(parseAppRoute('/acme/team/ENG/views')).toEqual({ kind: 'team-views', workspaceSlug: 'acme', teamKey: 'ENG', resource: 'issues' })
    expect(parseAppRoute('/acme/views/projects')).toEqual({ kind: 'workspace-views', workspaceSlug: 'acme', resource: 'projects' })
  })
})

describe('prefilled new-issue routes', () => {
  it('parses workspace and team scoped /new links', () => {
    expect(parseAppRoute('/acme/new', '?title=Hi')).toEqual({ kind: 'new-issue', workspaceSlug: 'acme' })
    expect(parseAppRoute('/acme/team/ENG/new')).toEqual({ kind: 'new-issue', workspaceSlug: 'acme', teamKey: 'ENG' })
    expect(parseAppRoute('/acme/team/ENG/new/')).toEqual({ kind: 'new-issue', workspaceSlug: 'acme', teamKey: 'ENG' })
  })

  it('does not capture other paths', () => {
    expect(parseAppRoute('/acme/new/extra').kind).not.toBe('new-issue')
    expect(parseAppRoute('/acme/team/ENG/new/extra').kind).not.toBe('new-issue')
    expect(parseAppRoute('/acme/team/ENG')).toMatchObject({ kind: 'team-overview' })
  })

  it('builds paths and keeps the route in its workspace', () => {
    expect(newIssuePath('acme')).toBe('/acme/new')
    expect(newIssuePath('acme', 'ENG')).toBe('/acme/team/ENG/new')
    expect(routeBelongsToWorkspace(parseAppRoute(newIssuePath('acme', 'ENG')), 'acme')).toBe(true)
  })
})

describe("new OAuth application route", () => {
  it("parses both settings locations to the create page and keeps pre-fill params in the path", async () => {
    const { applicationNewPath, parseAppRoute: parse } = await import("./app-routes");
    // Like the reference app, OAuth application developer pages live under Settings › API; the old location still parses.
    expect(parse("/acme/settings/api/applications/new")).toMatchObject({ kind: "settings", page: "api", applicationId: "new", applicationMode: "edit" });
    expect(parse("/acme/settings/applications/new")).toMatchObject({ kind: "settings", page: "applications", applicationId: "new" });
    expect(applicationNewPath("acme")).toBe("/acme/settings/api/applications/new");
    expect(applicationNewPath("acme", "oauth.client_name=A")).toBe("/acme/settings/api/applications/new?oauth.client_name=A");
    expect(parse("/acme/settings/api/applications/app-1/edit")).toMatchObject({ page: "api", applicationId: "app-1", applicationMode: "edit" });
    expect(parse("/acme/settings/applications/app-1/edit")).toMatchObject({ applicationId: "app-1", applicationMode: "edit" });
  });
});
