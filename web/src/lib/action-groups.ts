/**
 * LS-0009 ActionGroups — model-band priorities + path elevation.
 * Command menu sorts/filters groups via priority(ctx) instead of hard-coded insert order.
 */

export type ActionGroupBand =
  | "targetedModelActions"
  | "nonTargetedModelActions"
  | "viewActions"
  | "navigationActions"
  | "miscActions"
  | "searchActions"
  | "developerActions"
  | "searchResultActions";

export const ACTION_GROUP_BAND_PRIORITY: Record<ActionGroupBand, number> = {
  targetedModelActions: 0,
  nonTargetedModelActions: 1000,
  viewActions: 2000,
  navigationActions: 2500,
  miscActions: 3000,
  searchActions: 5000,
  developerActions: 6000,
  searchResultActions: 7000,
};

/** Model order used when lifting targeted model bands (lower = higher). */
export const ACTION_MODEL_ORDER = [
  "Notification",
  "Issue",
  "Project",
  "Document",
  "Initiative",
  "CustomView",
  "Team",
  "User",
  "Label",
  "Customer",
  "Review",
  "Release",
  "Cycle",
  "Dashboard",
] as const;

export type ActionGroupId =
  | "Issues"
  | "Projects"
  | "Documents"
  | "Views"
  | "Initiatives"
  | "Customers"
  | "Reviews"
  | "Agent chat"
  | "AI panel"
  | "Asks Page"
  | "Notifications"
  | "Filter"
  | "Templates"
  | "Navigation"
  | "Other"
  | "Search results";

export type ActionGroupDefinition = {
  id: ActionGroupId;
  /** Default band when not elevated by path/selection. */
  band: ActionGroupBand;
  /** Path prefixes (pathname segments after workspace) that lift this group. */
  pathLift?: string[];
  /** Model names that map this group into the targeted band. */
  models?: string[];
  promoteToTitle?: boolean;
  preventCollapsingInSearch?: boolean;
};

export const ACTION_GROUP_DEFINITIONS: ActionGroupDefinition[] = [
  {
    id: "Issues",
    band: "nonTargetedModelActions",
    models: ["Issue"],
    pathLift: ["issue", "my-issues", "team"],
  },
  {
    id: "Projects",
    band: "nonTargetedModelActions",
    models: ["Project"],
    pathLift: ["project", "projects"],
  },
  {
    id: "Documents",
    band: "nonTargetedModelActions",
    models: ["Document"],
    pathLift: ["document", "documents"],
  },
  {
    id: "Views",
    band: "viewActions",
    models: ["CustomView"],
    pathLift: ["view", "views"],
  },
  {
    id: "Initiatives",
    band: "nonTargetedModelActions",
    models: ["Initiative"],
    pathLift: ["initiative", "initiatives"],
  },
  {
    id: "Customers",
    band: "nonTargetedModelActions",
    models: ["Customer"],
    pathLift: ["customer", "customers"],
    promoteToTitle: true,
  },
  {
    id: "Reviews",
    band: "nonTargetedModelActions",
    models: ["Review"],
    pathLift: ["review", "reviews"],
    promoteToTitle: true,
  },
  {
    id: "Agent chat",
    band: "miscActions",
    pathLift: ["agent"],
    promoteToTitle: true,
  },
  {
    id: "AI panel",
    band: "miscActions",
    pathLift: ["ai"],
    promoteToTitle: true,
  },
  {
    id: "Asks Page",
    band: "miscActions",
    pathLift: ["asks"],
    promoteToTitle: true,
  },
  {
    id: "Notifications",
    band: "nonTargetedModelActions",
    models: ["Notification"],
    pathLift: ["inbox"],
  },
  {
    id: "Filter",
    band: "searchActions",
    pathLift: ["search"],
  },
  {
    id: "Templates",
    band: "miscActions",
  },
  {
    id: "Navigation",
    band: "navigationActions",
  },
  {
    id: "Other",
    band: "miscActions",
  },
  {
    id: "Search results",
    band: "searchResultActions",
    preventCollapsingInSearch: true,
  },
];

const definitionById = new Map(
  ACTION_GROUP_DEFINITIONS.map((definition) => [definition.id, definition]),
);

export type ActionGroupsContext = {
  /** Current location pathname (e.g. /acme/customers/…). */
  pathname?: string;
  /** Selected model type names (Issue, Project, …). */
  selectedModels?: Iterable<string>;
  /** When set, only these groups are considered “allowed” (LS-0721). */
  allowedActionGroups?: Iterable<string>;
};

function pathSegments(pathname: string | undefined): string[] {
  if (!pathname) return [];
  return pathname
    .split("/")
    .filter(Boolean)
    .slice(1)
    .map((segment) => segment.toLowerCase());
}

function isPathLifted(definition: ActionGroupDefinition, segments: string[]) {
  if (!definition.pathLift?.length || !segments.length) return false;
  return definition.pathLift.some((prefix) =>
    segments.some(
      (segment) =>
        segment === prefix ||
        segment.startsWith(`${prefix}-`) ||
        segment.startsWith(prefix),
    ),
  );
}

function modelLiftOffset(models: Iterable<string> | undefined): number {
  if (!models) return 0;
  let best = Number.POSITIVE_INFINITY;
  for (const model of models) {
    const index = ACTION_MODEL_ORDER.indexOf(
      model as (typeof ACTION_MODEL_ORDER)[number],
    );
    if (index >= 0) best = Math.min(best, index);
  }
  return Number.isFinite(best) ? best : 0;
}

/**
 * Priority for a group id in the current context. Lower sorts first.
 * Path elevation and selected models move matching groups into targetedModelActions.
 */
export function actionGroupPriority(
  groupId: string,
  ctx: ActionGroupsContext = {},
): number {
  const definition = definitionById.get(groupId as ActionGroupId);
  if (!definition) {
    return ACTION_GROUP_BAND_PRIORITY.miscActions + 500;
  }

  const segments = pathSegments(ctx.pathname);
  const selected = [...(ctx.selectedModels ?? [])];
  const modelMatch =
    definition.models?.some((model) => selected.includes(model)) ?? false;
  const pathMatch = isPathLifted(definition, segments);

  let band = definition.band;
  if (modelMatch || pathMatch) {
    band = "targetedModelActions";
  }

  const base = ACTION_GROUP_BAND_PRIORITY[band];
  if (band === "targetedModelActions") {
    return base + modelLiftOffset(definition.models ?? selected);
  }
  // Stable secondary order by definition list position.
  const index = ACTION_GROUP_DEFINITIONS.findIndex((item) => item.id === definition.id);
  return base + index;
}

/** Sort group ids by ActionGroups priority (and optionally filter by allow-list). */
export function sortActionGroups(
  groups: string[],
  ctx: ActionGroupsContext = {},
): string[] {
  const allowed = ctx.allowedActionGroups
    ? new Set([...ctx.allowedActionGroups])
    : null;
  const filtered =
    allowed && allowed.size > 0
      ? groups.filter((group) => allowed.has(group))
      : groups;
  return [...filtered].sort((left, right) => {
    const delta = actionGroupPriority(left, ctx) - actionGroupPriority(right, ctx);
    return delta !== 0 ? delta : left.localeCompare(right);
  });
}

export function getActionGroupDefinition(groupId: string) {
  return definitionById.get(groupId as ActionGroupId);
}
