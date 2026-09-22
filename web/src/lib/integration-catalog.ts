/**
 * LS-0337 — Integration catalog schema + subtype sections.
 * REST-facing catalog only (no GraphQL). Availability labels Coming soon /
 * Not supported are product availability — not engineering-gap banners.
 * P1-D adds product patterns for long-tail OAuth shells (LS-0340 / LS-0685…).
 * P2-D ships browser OAuth for Microsoft Teams / PagerDuty / Front (LS-0409 / LS-0451 / LS-0278).
 */

export type IntegrationAvailability = "supported" | "coming_soon" | "not_supported";

export type IntegrationCategory =
  | "Essentials"
  | "Engineering"
  | "Design"
  | "Support"
  | "Productivity"
  | "Automation"
  | "Other";

/** Primary catalog slug (settings path segment). */
export type IntegrationSlug = string;

export interface IntegrationProductPattern {
  headline: string;
  summary: string;
  capabilities: string[];
}

export interface IntegrationCatalogEntry {
  slug: IntegrationSlug;
  /** Linear-aligned service / subtype key when applicable. */
  service: string;
  name: string;
  description: string;
  category: IntegrationCategory;
  availability: IntegrationAvailability;
  /** Parent slug when this is a subtype (e.g. githubPersonal → github). */
  parentSlug?: IntegrationSlug;
  /** Hide subtype rows from the main All Integrations grid. */
  subtype?: boolean;
  /** Providers that currently have REST connect / manage flows. */
  connectProvider?: "github" | "gitlab" | "slack" | "jira" | "microsoftteams" | "pagerduty" | "front";
  /** Optional product-capability copy for Coming soon / Not supported detail pages. */
  productPattern?: IntegrationProductPattern;
}

/** Primary (non-subtype) catalog used by All Integrations. */
export const INTEGRATION_CATALOG: IntegrationCatalogEntry[] = [
  {
    slug: "github",
    service: "github",
    name: "GitHub",
    description: "Automate pull request workflows and link code to issues",
    category: "Essentials",
    availability: "supported",
    connectProvider: "github",
  },
  {
    slug: "slack",
    service: "slack",
    name: "Slack",
    description: "Create issues from Slack messages and sync threads",
    category: "Essentials",
    availability: "supported",
    connectProvider: "slack",
  },
  {
    slug: "gitlab",
    service: "gitlab",
    name: "GitLab",
    description: "Automate your merge request workflow",
    category: "Engineering",
    availability: "supported",
    connectProvider: "gitlab",
  },
  {
    slug: "jira",
    service: "jira",
    name: "Jira",
    description: "Sync issues and status between Jira and Flow",
    category: "Essentials",
    availability: "supported",
    connectProvider: "jira",
  },
  {
    slug: "figma",
    service: "figma",
    name: "Figma",
    description: "Embed designs and keep design context on issues",
    category: "Design",
    availability: "coming_soon",
  },
  {
    slug: "sentry",
    service: "sentry",
    name: "Sentry",
    description: "Link errors and create issues from Sentry alerts",
    category: "Engineering",
    availability: "coming_soon",
  },
  {
    slug: "zendesk",
    service: "zendesk",
    name: "Zendesk",
    description: "Turn support tickets into tracked product work",
    category: "Support",
    availability: "coming_soon",
    productPattern: {
      headline: "Support tickets in Flow",
      summary: "Sync Zendesk tickets with Flow issues so support and product stay aligned.",
      capabilities: [
        "Create Flow issues from Zendesk tickets",
        "Reopen Zendesk tickets when linked issues complete",
        "Keep customer context on the issue",
      ],
    },
  },
  {
    slug: "intercom",
    service: "intercom",
    name: "Intercom",
    description: "Capture customer conversations as issues",
    category: "Support",
    availability: "coming_soon",
    productPattern: {
      headline: "Conversations to issues",
      summary: "Turn Intercom conversations into tracked Flow issues without leaving support.",
      capabilities: [
        "Create issues from Intercom conversations",
        "Sync conversation status with issue state",
        "Attach customer context to requests",
      ],
    },
  },
  {
    slug: "discord",
    service: "discord",
    name: "Discord",
    description: "Post workspace updates to Discord channels",
    category: "Productivity",
    availability: "coming_soon",
    productPattern: {
      headline: "Community updates",
      summary: "Broadcast project and initiative updates to Discord channels your community follows.",
      capabilities: [
        "Post project updates to a Discord channel",
        "Post initiative updates to a Discord channel",
        "Keep a single source of truth in Flow",
      ],
    },
  },
  {
    slug: "microsoft-teams",
    service: "microsoftTeams",
    name: "Microsoft Teams",
    description: "Collaborate on issues from Microsoft Teams",
    category: "Productivity",
    availability: "supported",
    connectProvider: "microsoftteams",
    productPattern: {
      headline: "Teams alongside Slack",
      summary: "Connect a Microsoft Teams tenant so workspace updates and issue collaboration can reach your channels.",
      capabilities: [
        "Authorize a Teams tenant with browser OAuth",
        "Keep connection status honest when credentials are missing",
        "Reuse the shared OAuth complete surface after redirect",
      ],
    },
  },
  {
    slug: "notion",
    service: "notion",
    name: "Notion",
    description: "Link Notion docs to issues and projects",
    category: "Productivity",
    availability: "coming_soon",
  },
  {
    slug: "pagerduty",
    service: "pagerDuty",
    name: "PagerDuty",
    description: "Create issues from incidents and on-call events",
    category: "Engineering",
    availability: "supported",
    connectProvider: "pagerduty",
    productPattern: {
      headline: "Incidents into Flow",
      summary: "Link PagerDuty so on-call incidents can become tracked Flow issues.",
      capabilities: [
        "Start browser OAuth against PagerDuty Identity",
        "Complete via the shared oauth/complete flash",
        "No fake connected state without a successful token exchange",
      ],
    },
  },
  {
    slug: "opsgenie",
    service: "opsgenie",
    name: "Opsgenie",
    description: "Route Opsgenie alerts into Flow issues",
    category: "Engineering",
    availability: "coming_soon",
  },
  {
    slug: "front",
    service: "front",
    name: "Front",
    description: "Turn Front conversations into issues",
    category: "Support",
    availability: "supported",
    connectProvider: "front",
    productPattern: {
      headline: "Support desk long-tail",
      summary: "Connect Front so conversations can reopen when linked Flow issues complete.",
      capabilities: [
        "Browser OAuth start/complete with existing REST routes",
        "Reopen Front tickets when linked issues complete (follow-on)",
        "Catalog stays honest until OAuth credentials are configured",
      ],
    },
  },
  {
    slug: "gong",
    service: "gong",
    name: "Gong",
    description: "Attach call insights to customer requests",
    category: "Support",
    availability: "not_supported",
  },
  {
    slug: "salesforce",
    service: "salesforce",
    name: "Salesforce",
    description: "Sync CRM accounts and opportunities",
    category: "Support",
    availability: "not_supported",
  },
  {
    slug: "google-sheets",
    service: "googleSheets",
    name: "Google Sheets",
    description: "Export and sync spreadsheet data",
    category: "Productivity",
    availability: "not_supported",
  },
  {
    slug: "google-calendar",
    service: "googleCalendarPersonal",
    name: "Google Calendar",
    description: "Personal calendar integration for scheduling",
    category: "Productivity",
    availability: "not_supported",
  },
  {
    slug: "loom",
    service: "loom",
    name: "Loom",
    description: "Embed Loom recordings on issues",
    category: "Design",
    availability: "not_supported",
  },
  {
    slug: "airbyte",
    service: "airbyte",
    name: "Airbyte",
    description: "Warehouse sync for Flow data",
    category: "Automation",
    availability: "not_supported",
  },
  {
    slug: "launchdarkly",
    service: "launchDarkly",
    name: "LaunchDarkly",
    description: "Link feature flags to issues and releases",
    category: "Engineering",
    availability: "not_supported",
  },
  {
    slug: "zapier",
    service: "zapier",
    name: "Zapier",
    description: "Automate Flow with thousands of apps",
    category: "Automation",
    availability: "coming_soon",
  },
  {
    slug: "mcp-server",
    service: "mcpServer",
    name: "MCP Server",
    description: "Connect Model Context Protocol servers to Flow Agent",
    category: "Automation",
    availability: "coming_soon",
  },
];

/** Subtype / personal / specialty rows (LS-0337) — not shown as primary cards. */
export const INTEGRATION_SUBTYPES: IntegrationCatalogEntry[] = [
  { slug: "github-enterprise-server", service: "githubEnterpriseServer", name: "GitHub Enterprise Server", description: "Self-hosted GitHub Enterprise Server", category: "Engineering", availability: "coming_soon", parentSlug: "github", subtype: true },
  { slug: "github-enterprise-cloud", service: "githubEnterpriseCloud", name: "GitHub Enterprise Cloud", description: "GitHub Enterprise Cloud with data residency", category: "Engineering", availability: "coming_soon", parentSlug: "github", subtype: true },
  { slug: "github-commit", service: "githubCommit", name: "GitHub Commits", description: "Commit linking and status", category: "Engineering", availability: "coming_soon", parentSlug: "github", subtype: true },
  { slug: "github-import", service: "githubImport", name: "GitHub Import", description: "Import repositories and issues", category: "Engineering", availability: "coming_soon", parentSlug: "github", subtype: true },
  { slug: "github-personal", service: "githubPersonal", name: "GitHub (personal)", description: "Personal GitHub authorization", category: "Engineering", availability: "coming_soon", parentSlug: "github", subtype: true },
  { slug: "github-code-access-personal", service: "githubCodeAccessPersonal", name: "GitHub code access (personal)", description: "Personal code access for Code Intelligence and Reviews", category: "Engineering", availability: "coming_soon", parentSlug: "github", subtype: true },
  { slug: "jira-personal", service: "jiraPersonal", name: "Jira (personal)", description: "Personal Jira authorization", category: "Essentials", availability: "coming_soon", parentSlug: "jira", subtype: true },
  { slug: "figma-plugin", service: "figmaPlugin", name: "Figma Plugin", description: "Figma plugin companion", category: "Design", availability: "coming_soon", parentSlug: "figma", subtype: true },
  { slug: "slack-asks", service: "slackAsks", name: "Slack Asks", description: "Asks intake from Slack", category: "Essentials", availability: "coming_soon", parentSlug: "slack", subtype: true },
  { slug: "slack-personal", service: "slackPersonal", name: "Slack (personal)", description: "Personal Slack authorization", category: "Essentials", availability: "coming_soon", parentSlug: "slack", subtype: true },
  { slug: "mcp-server-personal", service: "mcpServerPersonal", name: "MCP Server (personal)", description: "Personal MCP connectors", category: "Automation", availability: "coming_soon", parentSlug: "mcp-server", subtype: true },
];

const BY_SLUG = new Map(
  [...INTEGRATION_CATALOG, ...INTEGRATION_SUBTYPES].map((entry) => [entry.slug, entry]),
);

export function getIntegrationCatalogEntry(slug: string): IntegrationCatalogEntry | undefined {
  return BY_SLUG.get(slug);
}

export function isIntegrationCatalogSlug(slug: string): boolean {
  return BY_SLUG.has(slug);
}

export function primaryIntegrations(): IntegrationCatalogEntry[] {
  return INTEGRATION_CATALOG.filter((entry) => !entry.subtype);
}

export function integrationCategories(entries: IntegrationCatalogEntry[] = primaryIntegrations()): IntegrationCategory[] {
  const seen = new Set<IntegrationCategory>();
  for (const entry of entries) seen.add(entry.category);
  return ["Essentials", "Engineering", "Design", "Support", "Productivity", "Automation", "Other"].filter(
    (category): category is IntegrationCategory => seen.has(category as IntegrationCategory),
  );
}

export function availabilityLabel(availability: IntegrationAvailability): string {
  if (availability === "supported") return "Available";
  if (availability === "coming_soon") return "Coming soon";
  return "Not supported";
}

export function subtypesFor(parentSlug: string): IntegrationCatalogEntry[] {
  return INTEGRATION_SUBTYPES.filter((entry) => entry.parentSlug === parentSlug);
}

const DEFAULT_PRODUCT_PATTERNS: Partial<Record<IntegrationAvailability, IntegrationProductPattern>> = {
  coming_soon: {
    headline: "Coming soon",
    summary: "This integration will connect Flow with the tools your team already uses.",
    capabilities: [
      "Connect your workspace account",
      "Sync relevant activity into Flow",
      "Manage the connection from Settings",
    ],
  },
  not_supported: {
    headline: "Not supported",
    summary: "This catalog entry is listed for completeness and is not available in Flow.",
    capabilities: [
      "Browse related integrations that are available",
      "Use webhooks or Zapier for custom automation",
    ],
  },
};

export function getIntegrationProductPattern(
  entry: IntegrationCatalogEntry,
): IntegrationProductPattern | undefined {
  if (entry.availability === "supported") return undefined;
  return entry.productPattern ?? DEFAULT_PRODUCT_PATTERNS[entry.availability];
}
