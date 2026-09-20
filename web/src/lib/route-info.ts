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
): RouteInfo {
  const workspaceName = data?.workspace?.name || data?.workspace?.urlKey || "Flow";

  if (route.kind === "inbox") {
    const unread = data?.notifications?.filter((item) => !item.readAt).length ?? 0;
    const suffix = unread > 0 ? ` (${unread > 99 ? "99+" : unread})` : "";
    return {
      title: `Inbox${suffix}`,
      pinnedTitle: "Inbox",
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
      title: isDefault ? "My issues" : `My issues › ${viewLabel}`,
      pinnedTitle: isDefault ? "My issues" : viewLabel,
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
    return { title: "Issue", pinnedTitle: "Issue", icon: "issue" };
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
          ? ` › ${capitalize(route.tab)}`
          : "";
      return {
        title: `${project.name}${tab}`,
        pinnedTitle: project.name,
        icon: "project",
      };
    }
    return { title: "Project", pinnedTitle: "Project", icon: "project" };
  }

  if (route.kind === "settings") {
    if (BILLING_SETTINGS.has(route.page)) {
      return { title: "Settings", pinnedTitle: "Settings", icon: "settings" };
    }
    if (route.page === "team" && route.teamKey) {
      const team = data?.teams?.find(
        (item) => item.key.toLowerCase() === route.teamKey?.toLowerCase(),
      );
      const section = route.teamSection
        ? ` › ${teamSectionLabel(route.teamSection)}`
        : "";
      return {
        title: `${team?.name || route.teamKey}${section}`,
        pinnedTitle: team?.name || route.teamKey,
        icon: "settings",
      };
    }
    const label =
      SETTINGS_TITLE.get(route.page as SettingsPageId) ||
      settingsFallbackLabel(route.page);
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
    return { title: "Not found", pinnedTitle: "Not found" };
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
  return section
    .split("-")
    .map((part) => part[0]?.toUpperCase() + part.slice(1))
    .join(" ");
}

function settingsFallbackLabel(page: string) {
  return page
    .split("-")
    .map((part) => part[0]?.toUpperCase() + part.slice(1))
    .join(" ");
}
