import type { OAuthAppManifest, OAuthApplication } from "@/types/flow";

/** Scopes an OAuth app can request. `userOnly` scopes are never granted to actor=app tokens. */
export const OAUTH_SCOPE_OPTIONS: {
  scope: string;
  label: string;
  description: string;
  userOnly?: boolean;
}[] = [
  { scope: "read", label: "Read", description: "Read access to the workspace data" },
  { scope: "write", label: "Write", description: "Create and update issues, comments and more" },
  { scope: "create_issues", label: "Create issues", description: "Create issues only" },
  { scope: "create_comments", label: "Create comments", description: "Create comments only" },
  { scope: "admin", label: "Admin", description: "Full administrative access", userOnly: true },
  { scope: "timeSchedule:write", label: "Write time schedules", description: "Manage on-call schedules", userOnly: true },
  { scope: "customer:read", label: "Read customers", description: "Read customers and customer requests" },
  { scope: "customer:write", label: "Write customers", description: "Create and update customers and requests" },
  { scope: "initiative:read", label: "Read initiatives", description: "Read initiatives and initiative updates" },
  { scope: "initiative:write", label: "Write initiatives", description: "Create and update initiatives" },
  { scope: "release:read", label: "Read releases", description: "Read releases and pipelines" },
  { scope: "release:create", label: "Create releases", description: "Create releases" },
  { scope: "releaseStage:update", label: "Update release stages", description: "Move releases between stages" },
];

export const AGENT_SCOPES = { assignable: "app:assignable", mentionable: "app:mentionable" } as const;

export const WEBHOOK_RESOURCE_TYPE_OPTIONS = [
  "AgentSessionEvent",
  "AppUserNotification",
  "Attachment",
  "Comment",
  "Customer",
  "CustomerNeed",
  "Cycle",
  "Document",
  "Initiative",
  "InitiativeUpdate",
  "Issue",
  "IssueLabel",
  "IssueSLA",
  "OAuthAuthorization",
  "PermissionChange",
  "Project",
  "ProjectLabel",
  "ProjectUpdate",
  "Reaction",
  "Release",
  "ReleaseNote",
  "User",
] as const;

export const MANIFEST_SCHEMA_URL = "/.well-known/oauth-app-manifest.schema.json";

const asString = (value: unknown) => (typeof value === "string" ? value : undefined);
const asBool = (value: unknown) => (typeof value === "boolean" ? value : undefined);
const asStrings = (value: unknown) =>
  Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : undefined;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function compact<T extends Record<string, unknown>>(value: T): T | undefined {
  const entries = Object.entries(value).filter(([, item]) => item !== undefined);
  return entries.length ? (Object.fromEntries(entries) as T) : undefined;
}

/** Keeps only the manifest fields Flow understands, dropping unknown keys and wrongly typed values. */
export function sanitizeManifest(input: unknown): OAuthAppManifest {
  if (!isRecord(input)) return {};
  const display = isRecord(input.display) ? input.display : {};
  const developer = isRecord(input.developer) ? input.developer : {};
  const oauth = isRecord(input.oauth) ? input.oauth : {};
  const webhook = isRecord(input.webhook) ? input.webhook : {};
  const agent = isRecord(input.agent) ? input.agent : {};
  const distribution: OAuthAppManifest["distribution"] =
    input.distribution === "private" || input.distribution === "public" ? input.distribution : undefined;
  return compact({
    $schema: asString(input.$schema),
    schemaVersion: asString(input.schemaVersion),
    distribution,
    display: compact({ description: asString(display.description), iconUrl: asString(display.iconUrl) }),
    developer: compact({ name: asString(developer.name) }),
    oauth: compact({
      client_name: asString(oauth.client_name),
      client_uri: asString(oauth.client_uri),
      redirect_uris: asStrings(oauth.redirect_uris),
      grant_types: asStrings(oauth.grant_types),
      scopes: asStrings(oauth.scopes),
    }),
    webhook: compact({
      enabled: asBool(webhook.enabled),
      url: asString(webhook.url),
      resourceTypes: asStrings(webhook.resourceTypes),
    }),
    agent: compact({ assignable: asBool(agent.assignable), mentionable: asBool(agent.mentionable) }),
  }) ?? {};
}

export function isEmptyManifest(manifest: OAuthAppManifest) {
  return Object.keys(manifest).length === 0;
}

/**
 * Reads a pre-filled manifest from the page query string "new OAuth application" link:
 * a `manifest` param holding JSON wins, otherwise dot-notation params (`oauth.client_name`, repeated
 * `oauth.redirect_uris`, ...) are collected. Unsupported params are ignored.
 */
export function manifestFromSearch(search: string): OAuthAppManifest {
  const params = new URLSearchParams(search);
  const raw = params.get("manifest");
  if (raw) {
    try {
      const parsed = sanitizeManifest(JSON.parse(raw));
      if (!isEmptyManifest(parsed)) return parsed;
    } catch {
      // Fall through to the dot-notation params.
    }
  }
  const bool = (key: string) => {
    const value = params.get(key);
    return value === "true" ? true : value === "false" ? false : undefined;
  };
  const many = (key: string) => {
    const values = params.getAll(key).filter(Boolean);
    return values.length ? values : undefined;
  };
  const distribution = params.get("distribution");
  return sanitizeManifest({
    distribution: distribution === "private" || distribution === "public" ? distribution : undefined,
    display: { description: params.get("display.description") ?? undefined, iconUrl: params.get("display.iconUrl") ?? undefined },
    developer: { name: params.get("developer.name") ?? undefined },
    oauth: {
      client_name: params.get("oauth.client_name") ?? undefined,
      client_uri: params.get("oauth.client_uri") ?? undefined,
      redirect_uris: many("oauth.redirect_uris"),
      grant_types: many("oauth.grant_types"),
      scopes: many("oauth.scopes"),
    },
    webhook: { enabled: bool("webhook.enabled"), url: params.get("webhook.url") ?? undefined, resourceTypes: many("webhook.resourceTypes") },
    agent: { assignable: bool("agent.assignable"), mentionable: bool("agent.mentionable") },
  });
}

/** Parses pasted or uploaded manifest JSON. Returns an error string for text that is not a JSON object. */
export function parseManifestText(text: string): { manifest: OAuthAppManifest } | { error: string } {
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch {
    return { error: "The manifest is not valid JSON." };
  }
  if (!isRecord(parsed)) return { error: "The manifest must be a JSON object." };
  return { manifest: sanitizeManifest(parsed) };
}

export interface ApplicationFormState {
  name: string;
  developerName: string;
  developerUrl: string;
  description: string;
  iconUrl: string;
  /** One URI per line. */
  redirectUris: string;
  distribution: "private" | "public";
  clientCredentials: boolean;
  /** Every scope except the two agent scopes, which are edited through `assignable`/`mentionable`. */
  scopes: string[];
  assignable: boolean;
  mentionable: boolean;
  webhookEnabled: boolean;
  webhookUrl: string;
  webhookResourceTypes: string[];
}

export function emptyApplicationForm(): ApplicationFormState {
  return {
    name: "",
    developerName: "",
    developerUrl: "",
    description: "",
    iconUrl: "",
    redirectUris: "",
    distribution: "private",
    clientCredentials: false,
    scopes: ["read"],
    assignable: false,
    mentionable: false,
    webhookEnabled: false,
    webhookUrl: "",
    webhookResourceTypes: [],
  };
}

const withRead = (scopes: string[]) => (scopes.includes("read") ? scopes : ["read", ...scopes]);
const unique = (values: string[]) => [...new Set(values)];

export function formStateFromManifest(manifest: OAuthAppManifest, base: ApplicationFormState = emptyApplicationForm()): ApplicationFormState {
  const manifestScopes = manifest.oauth?.scopes;
  const scopes = manifestScopes ? manifestScopes.filter((scope) => scope !== AGENT_SCOPES.assignable && scope !== AGENT_SCOPES.mentionable) : base.scopes;
  return {
    name: manifest.oauth?.client_name ?? base.name,
    developerName: manifest.developer?.name ?? base.developerName,
    developerUrl: manifest.oauth?.client_uri ?? base.developerUrl,
    description: manifest.display?.description ?? base.description,
    iconUrl: manifest.display?.iconUrl ?? base.iconUrl,
    redirectUris: manifest.oauth?.redirect_uris ? manifest.oauth.redirect_uris.join("\n") : base.redirectUris,
    distribution: manifest.distribution ?? base.distribution,
    clientCredentials: manifest.oauth?.grant_types ? manifest.oauth.grant_types.includes("client_credentials") : base.clientCredentials,
    scopes: withRead(unique(scopes)),
    assignable: manifest.agent?.assignable ?? (manifestScopes ? manifestScopes.includes(AGENT_SCOPES.assignable) : base.assignable),
    mentionable: manifest.agent?.mentionable ?? (manifestScopes ? manifestScopes.includes(AGENT_SCOPES.mentionable) : base.mentionable),
    webhookEnabled: manifest.webhook?.enabled ?? base.webhookEnabled,
    webhookUrl: manifest.webhook?.url ?? base.webhookUrl,
    webhookResourceTypes: manifest.webhook?.resourceTypes ?? base.webhookResourceTypes,
  };
}

export function formStateFromApplication(app: OAuthApplication): ApplicationFormState {
  return {
    name: app.name,
    developerName: app.developerName ?? "",
    developerUrl: app.developerUrl ?? "",
    description: app.description ?? "",
    iconUrl: app.logoUrl ?? "",
    redirectUris: app.redirectUris.join("\n"),
    distribution: app.publicEnabled ? "public" : "private",
    clientCredentials: Boolean(app.supportsClientCredentials),
    scopes: withRead(unique(app.scopes.filter((scope) => scope !== AGENT_SCOPES.assignable && scope !== AGENT_SCOPES.mentionable))),
    assignable: app.scopes.includes(AGENT_SCOPES.assignable),
    mentionable: app.scopes.includes(AGENT_SCOPES.mentionable),
    webhookEnabled: Boolean(app.webhookEnabled),
    webhookUrl: app.webhookUrl ?? "",
    webhookResourceTypes: app.webhookResourceTypes ?? [],
  };
}

export function manifestFromFormState(state: ApplicationFormState): OAuthAppManifest {
  const scopes = withRead(unique(state.scopes));
  if (state.assignable) scopes.push(AGENT_SCOPES.assignable);
  if (state.mentionable) scopes.push(AGENT_SCOPES.mentionable);
  return {
    schemaVersion: "1.0.0",
    distribution: state.distribution,
    display: { description: state.description.trim(), iconUrl: state.iconUrl.trim() },
    developer: { name: state.developerName.trim() },
    oauth: {
      client_name: state.name.trim(),
      client_uri: state.developerUrl.trim(),
      redirect_uris: state.redirectUris.split(/\s+/).filter(Boolean),
      grant_types: state.clientCredentials ? ["authorization_code", "client_credentials"] : ["authorization_code"],
      scopes,
    },
    webhook: { enabled: state.webhookEnabled, url: state.webhookUrl.trim(), resourceTypes: state.webhookResourceTypes },
    agent: { assignable: state.assignable, mentionable: state.mentionable },
  };
}

/** Merges the current form values with a manifest (manifest fields win, others are kept). */
export function applyManifestToForm(state: ApplicationFormState, manifest: OAuthAppManifest): ApplicationFormState {
  return formStateFromManifest(manifest, state);
}

/** Download file name for an exported manifest. */
export function manifestFileName(name: string) {
  const slug = name.trim().toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
  return `${slug || "oauth-app"}.manifest.json`;
}
