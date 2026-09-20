/** Shared automation sourceKey normalization (LS-0094 / LS-0086). */

export const EMAIL_TRUSTED_SOURCE_KEY = "issueSource:email-trusted" as const;
export const APP_USER_SOURCE_PREFIX = "appUser:" as const;
export const INTEGRATION_SOURCE_PREFIX = "integration:" as const;
export const OAUTH_CLIENT_SOURCE_PREFIX = "oauthClient:" as const;

export type TrustedSourcesMode = "none" | "allowlist";

export type IssueSourceMetadata = {
  type?: string;
  subType?: string;
  displayName?: string;
  id?: string;
  emailIntakeMetadata?: { trusted?: boolean };
};

export type TrustedSourceMatcher =
  | { type: "appUser"; userId: string }
  | { type: "sourceMetadata"; sourceMetadata: IssueSourceMetadata };

/** Catalog integration services that can appear as trusted issue sources. */
export const INTEGRATION_SOURCE_SERVICES = [
  "slack",
  "github",
  "gitlab",
  "jira",
  "email",
  "asksWeb",
  "discord",
  "intercom",
  "zendesk",
  "front",
] as const;

export type IntegrationSourceService =
  (typeof INTEGRATION_SOURCE_SERVICES)[number];

const INTEGRATION_DISPLAY_NAMES: Record<string, string> = {
  slack: "Slack",
  github: "GitHub",
  gitlab: "GitLab",
  jira: "Jira",
  email: "Email",
  asksWeb: "Asks web",
  discord: "Discord",
  intercom: "Intercom",
  zendesk: "Zendesk",
  front: "Front",
};

const SPECIAL_SOURCE_NAMES: Record<string, string> = {
  [EMAIL_TRUSTED_SOURCE_KEY]: "Trusted email",
};

export function isOauthClientSourceKey(key: string | undefined | null): boolean {
  return Boolean(key?.startsWith(OAUTH_CLIENT_SOURCE_PREFIX));
}

export function isAppUserSourceKey(key: string | undefined | null): boolean {
  return Boolean(key?.startsWith(APP_USER_SOURCE_PREFIX));
}

export function isIntegrationSourceKey(key: string | undefined | null): boolean {
  return Boolean(key?.startsWith(INTEGRATION_SOURCE_PREFIX));
}

export function appUserIdFromSourceKey(
  key: string | undefined | null,
): string | undefined {
  if (!key?.startsWith(APP_USER_SOURCE_PREFIX)) return undefined;
  const id = key.slice(APP_USER_SOURCE_PREFIX.length);
  return id || undefined;
}

export function integrationServiceFromSourceKey(
  key: string | undefined | null,
): IntegrationSourceService | undefined {
  if (!key?.startsWith(INTEGRATION_SOURCE_PREFIX)) return undefined;
  const service = key.slice(INTEGRATION_SOURCE_PREFIX.length);
  return isIntegrationSourceService(service) ? service : undefined;
}

export function isIntegrationSourceService(
  value: string | undefined | null,
): value is IntegrationSourceService {
  return Boolean(
    value &&
      (INTEGRATION_SOURCE_SERVICES as readonly string[]).includes(value),
  );
}

export function integrationDisplayName(service: string): string {
  return INTEGRATION_DISPLAY_NAMES[service] ?? service;
}

export function specialSourceName(key: string): string {
  return SPECIAL_SOURCE_NAMES[key] ?? key;
}

/** Normalize any raw key to a canonical allowlist sourceKey (or undefined). */
export function normalizeSourceKey(
  key: string | undefined | null,
): string | undefined {
  const trimmed = String(key ?? "").trim();
  if (!trimmed) return undefined;
  if (trimmed === EMAIL_TRUSTED_SOURCE_KEY) return EMAIL_TRUSTED_SOURCE_KEY;
  if (trimmed.startsWith(APP_USER_SOURCE_PREFIX)) {
    const id = trimmed.slice(APP_USER_SOURCE_PREFIX.length).trim();
    return id ? `${APP_USER_SOURCE_PREFIX}${id}` : undefined;
  }
  if (trimmed.startsWith(INTEGRATION_SOURCE_PREFIX)) {
    const service = trimmed.slice(INTEGRATION_SOURCE_PREFIX.length).trim();
    return isIntegrationSourceService(service)
      ? `${INTEGRATION_SOURCE_PREFIX}${service}`
      : undefined;
  }
  if (trimmed.startsWith(OAUTH_CLIENT_SOURCE_PREFIX)) {
    const id = trimmed.slice(OAUTH_CLIENT_SOURCE_PREFIX.length).trim();
    return id ? `${OAUTH_CLIENT_SOURCE_PREFIX}${id}` : undefined;
  }
  return undefined;
}

export function externalSourceMetadataToSourceKey(
  metadata: IssueSourceMetadata | undefined | null,
): string | undefined {
  if (!metadata) return undefined;
  if (
    metadata.type === "integration" &&
    metadata.subType &&
    isIntegrationSourceService(metadata.subType)
  ) {
    return `${INTEGRATION_SOURCE_PREFIX}${metadata.subType}`;
  }
  if (metadata.type === "oauthClient" && metadata.id) {
    return `${OAUTH_CLIENT_SOURCE_PREFIX}${metadata.id}`;
  }
  return undefined;
}

/** Map issue.sourceMetadata → allowlist sourceKey (Linear Xm.issueSourceMetadataToSourceKey). */
export function issueSourceMetadataToSourceKey(
  metadata: IssueSourceMetadata | undefined | null,
): string | undefined {
  if (!metadata) return undefined;
  if (metadata.type === "email" && metadata.subType === "email") {
    return EMAIL_TRUSTED_SOURCE_KEY;
  }
  return externalSourceMetadataToSourceKey(metadata);
}

/** Map a trusted-source matcher/policy → allowlist sourceKey. */
export function matcherToAllowlistSourceKey(
  matcher: TrustedSourceMatcher | undefined | null,
): string | undefined {
  if (!matcher) return undefined;
  if (matcher.type === "appUser") {
    const id = String(matcher.userId ?? "").trim();
    return id ? `${APP_USER_SOURCE_PREFIX}${id}` : undefined;
  }
  const meta = matcher.sourceMetadata;
  if (
    meta?.type === "email" &&
    meta.subType === "email" &&
    meta.emailIntakeMetadata?.trusted === true
  ) {
    return EMAIL_TRUSTED_SOURCE_KEY;
  }
  return externalSourceMetadataToSourceKey(meta);
}

export function getTrustedSourcesMode(
  settings:
    | { trustedSourcesMode?: string | null }
    | null
    | undefined,
): TrustedSourcesMode {
  return settings?.trustedSourcesMode === "allowlist" ? "allowlist" : "none";
}

export function trustedSourcesAllowlist(
  settings:
    | { trustedSourcesAllowlist?: string[] | null }
    | null
    | undefined,
): string[] {
  const raw = settings?.trustedSourcesAllowlist ?? [];
  const keys = raw
    .map((key) => normalizeSourceKey(key))
    .filter((key): key is string => Boolean(key));
  return [...new Set(keys)];
}
