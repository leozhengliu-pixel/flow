import type { AppRoute, SettingsPageId } from "@/lib/app-routes";
import { SETTINGS_SEARCH_PAGES } from "@/components/settings/settings-search";
import type { BootstrapData, Issue, Project } from "@/types/flow";

export type RouteInfo = {
  title: string;
  pinnedTitle?: string;
  icon?: string;
};

const SETTINGS_TITLE = new Map(
  SETTINGS_SEARCH_PAGES.map((page) => [page.id, page.title] as const),
);

/** Explicitly omit billing / usage settings titles (OUT_OF_SCOPE_BILLING). */
const BILLING_SETTINGS = new Set<string>([
  "billing",
  "usage",
  "spend-limits",
  "plans",
]);

export type RouteInfoData = Pick<
  BootstrapData,
  "workspace" | "issues" | "projects" | "teams" | "notifications"
> & {
  issue?: Issue;
  project?: Project;
};

/**
 * LS-0538 — centralized match → chrome info for document.title / future tabs.
 * First slice: issue / project / inbox / my-issues / settings (non-billing).
 */
export function routeInfo(
  route: AppRoute,
  data?: Partial<RouteInfoData> | null,
  /** Translates UI labels (the app's i18n `t`); record names are never translated. */
  t: (source: string) => string = (source) => source,
): RouteInfo {
  const workspaceName = data?.workspace?.name || data?.workspace?.urlKey || "Flow";

  if (route.kind === "inbox") {
    const unread = data?.notifications?.filter((item) => !item.readAt).length ?? 0;
    const suffix = unread > 0 ? ` (${unread > 99 ? "99+" : unread})` : "";
    return {
      title: `${t("Inbox")}${suffix}`,
      pinnedTitle: t("Inbox"),
      icon: "inbox",
    };
  }

  if (route.kind === "my-issues") {
    const viewLabel =
      route.view === "assigned"
        ? "Assigned"
        : route.view === "created"
          ? "Created"
          : route.view === "subscribed"
            ? "Subscribed"
            : route.view === "activity"
              ? "Activity"
              : String(route.view);
    const isDefault = route.view === "assigned";
    return {
      title: isDefault ? t("My issues") : `${t("My issues")} › ${t(viewLabel)}`,
      pinnedTitle: isDefault ? t("My issues") : t(viewLabel),
      icon: "my-issues",
    };
  }

  if (route.kind === "issue") {
    const issue =
      data?.issue ||
      data?.issues?.find(
        (item) =>
          item.id === route.identifier ||
          item.identifier.toLowerCase() === route.identifier.toLowerCase() ||
          item.identifier.toLowerCase().endsWith(`-${route.identifier.toLowerCase()}`),
      );
    if (issue) {
      return {
        title: `${issue.identifier} ${issue.title}`,
        pinnedTitle: issue.identifier,
        icon: "issue",
      };
    }
    return { title: t("Issue"), pinnedTitle: t("Issue"), icon: "issue" };
  }

  if (route.kind === "project") {
    const project =
      data?.project ||
      data?.projects?.find(
        (item) =>
          item.id === route.projectSlugId ||
          item.slugId === route.projectSlugId,
      );
    if (project) {
      const tab =
        route.tab && route.tab !== "overview"
          ? ` › ${t(capitalize(route.tab))}`
          : "";
      return {
        title: `${project.name}${tab}`,
        pinnedTitle: project.name,
        icon: "project",
      };
    }
    return { title: t("Project"), pinnedTitle: t("Project"), icon: "project" };
  }

  if (route.kind === "settings") {
    if (route.page === "shortcuts") {
      return { title: t("Not found"), pinnedTitle: t("Not found"), icon: "settings" };
    }
    if (BILLING_SETTINGS.has(route.page)) {
      return { title: t("Settings"), pinnedTitle: t("Settings"), icon: "settings" };
    }
    if (route.page === "team" && route.teamKey) {
      const team = data?.teams?.find(
        (item) => item.key.toLowerCase() === route.teamKey?.toLowerCase(),
      );
      const section = route.teamSection
        ? ` › ${t(teamSectionLabel(route.teamSection))}`
        : "";
      return {
        title: `${team?.name || route.teamKey}${section}`,
        pinnedTitle: team?.name || route.teamKey,
        icon: "settings",
      };
    }
    const label = t(
      SETTINGS_TITLE.get(route.page as SettingsPageId) ||
        SETTINGS_EXTRA_TITLES[route.page] ||
        settingsFallbackLabel(route.page),
    );
    return {
      title: label,
      pinnedTitle: label,
      icon: "settings",
    };
  }

  if (route.kind === "workspace-root") {
    return { title: workspaceName, pinnedTitle: workspaceName };
  }

  if (route.kind === "not-found") {
    return { title: t("Not found"), pinnedTitle: t("Not found") };
  }

  const team =
    "teamKey" in route && typeof route.teamKey === "string"
      ? data?.teams?.find((item) => item.key.toLowerCase() === (route.teamKey as string).toLowerCase())
      : undefined;
  if ((route.kind === "team-overview" || route.kind === "team-issues") && team) {
    return { title: team.name, pinnedTitle: team.name };
  }
  const pageTitle = PAGE_TITLES[route.kind];
  if (pageTitle) {
    const title = team ? `${team.name} › ${t(pageTitle)}` : t(pageTitle);
    return { title, pinnedTitle: t(pageTitle) };
  }

  return {
    title: workspaceName,
    pinnedTitle: workspaceName,
  };
}

/** Apply `document.title` from routeInfo (browser tab chrome). */
export function applyDocumentTitle(info: RouteInfo, workspaceName?: string) {
  const suffix = workspaceName ? ` · ${workspaceName}` : " · Flow";
  document.title = `${info.title}${suffix}`;
}

function capitalize(value: string) {
  return value ? value[0].toUpperCase() + value.slice(1).replace(/-/g, " ") : value;
}

function teamSectionLabel(section: string) {
  const label = section.replace(/^ai-/, "AI-").replace(/-/g, " ");
  return label[0]?.toUpperCase() + label.slice(1);
}

/** Settings pages that are not in the settings search index. */
const SETTINGS_EXTRA_TITLES: Record<string, string> = {
  loops: "Loops",
  "coding-sessions": "Coding sessions",
  "coding-environments": "Coding environments",
};

/** Page names for the browser tab, like Linear's (the page name only). */
const PAGE_TITLES: Partial<Record<AppRoute["kind"], string>> = {
  agent: "Agent",
  asks: "Asks",
  automations: "Automations",
  "automation-new": "Automations",
  "automation-detail": "Automations",
  "automation-runs": "Automations",
  dashboards: "Dashboards",
  diary: "Diary",
  documents: "Documents",
  drafts: "Drafts",
  initiatives: "Initiatives",
  loops: "Loops",
  meetings: "Meetings",
  "new-team": "Create a new team",
  projects: "Projects",
  pulse: "Pulse",
  releases: "Releases",
  reviews: "Reviews",
  "team-archive": "Archive",
  "team-cycles": "Cycles",
  "team-documents": "Documents",
  "team-initiatives": "Initiatives",
  "team-loops": "Loops",
  "team-members": "Members",
  "team-projects": "Projects",
  "team-triage": "Triage",
  "team-updates": "Updates",
  "team-views": "Views",
  "workspace-customers": "Customers",
  "workspace-issues": "Issues",
  "workspace-members": "Members",
  "workspace-teams": "Teams",
  "workspace-views": "Views",
};

function settingsFallbackLabel(page: string) {
  const label = page.replace(/-/g, " ");
  return label ? label[0].toUpperCase() + label.slice(1) : label;
}
