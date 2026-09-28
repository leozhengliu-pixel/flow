/**
 * LS-0094 AutomationTrustedSourceEditorOptions
 * Editor options layer: hydrateSources + sourcesForEditor + sourceKey normalize.
 */
import type { User } from "@/types/flow";
import {
  EMAIL_TRUSTED_SOURCE_KEY,
  appUserIdFromSourceKey,
  getTrustedSourcesMode,
  integrationDisplayName,
  integrationServiceFromSourceKey,
  isOauthClientSourceKey,
  matcherToAllowlistSourceKey,
  normalizeSourceKey,
  specialSourceName,
  trustedSourcesAllowlist,
  type IntegrationSourceService,
  type TrustedSourceMatcher,
  type TrustedSourcesMode,
} from "./automation-source-key";

export type TrustedSourceKind =
  | "agent"
  | "appUser"
  | "integration"
  | "oauthClient"
  | "special";

export type TrustedSourceEditorOption = {
  id: string;
  name: string;
  iconUrl?: string;
  kind?: TrustedSourceKind;
  allowed: boolean;
};

export type CatalogSource = {
  id: string;
  name: string;
  iconUrl?: string;
};

export type TrustedSourcesSettings = {
  trustedSourcesMode?: TrustedSourcesMode | string | null;
  trustedSourcesAllowlist?: string[] | null;
};

export type HydrateSourcesContext = {
  users: User[];
  /** Active integration service ids present in the workspace (e.g. slack). */
  integrationServices: string[];
  /** Optional OAuth / marketplace catalog entries keyed as oauthClient:<id>. */
  catalogSources?: CatalogSource[];
  /** Policy matchers attached to the automation / loop being edited. */
  policyMatchers?: TrustedSourceMatcher[];
  /** Explicit source keys already on the automation. */
  policySourceKeys?: string[];
  settings: TrustedSourcesSettings;
};

const KIND_LABEL: Record<TrustedSourceKind, string> = {
  agent: "agent",
  appUser: "app user",
  integration: "integration",
  oauthClient: "OAuth app",
  special: "source",
};

function isSlackAsksAppUser(user: User): boolean {
  return !user.oauthClientId && user.name === "Linear Asks";
}

function isLinearAppUser(user: User): boolean {
  return Boolean(user.builtinAgent);
}

export const AutomationTrustedSourceEditorOptions = {
  normalizeSourceKey,
  getTrustedSourcesMode,
  trustedSourcesAllowlist,
  matcherToAllowlistSourceKey,

  async hydrateSources(ctx: HydrateSourcesContext): Promise<void> {
    // REST bootstrap already carries users + integrations; keep async for
    // Linear hydrateSources parity (asksWebPages / app users).
    void this.appUserIdsForSources(ctx);
    await Promise.resolve();
  },

  allowedSourceKeys(settings: TrustedSourcesSettings): Set<string> {
    if (getTrustedSourcesMode(settings) === "none") return new Set();
    return new Set(trustedSourcesAllowlist(settings));
  },

  sourceKeysFromPolicies(
    matchers: TrustedSourceMatcher[] | undefined,
    explicitKeys: string[] | undefined,
  ): string[] {
    const fromMatchers = (matchers ?? [])
      .map((matcher) => matcherToAllowlistSourceKey(matcher))
      .filter((key): key is string => Boolean(key));
    const fromKeys = (explicitKeys ?? [])
      .map((key) => normalizeSourceKey(key))
      .filter((key): key is string => Boolean(key));
    return [...new Set([...fromMatchers, ...fromKeys])];
  },

  sourceKeysForEditor(ctx: HydrateSourcesContext): string[] {
    return [
      ...new Set([
        ...trustedSourcesAllowlist(ctx.settings),
        ...this.sourceKeysFromPolicies(ctx.policyMatchers, ctx.policySourceKeys),
        // Always surface trusted-email so editors can enable it.
        EMAIL_TRUSTED_SOURCE_KEY,
        ...ctx.integrationServices
          .filter((service) => Boolean(integrationServiceFromSourceKey(`integration:${service}`) || service))
          .map((service) => `integration:${service}`),
      ]),
    ];
  },

  appUserIdsForSources(ctx: HydrateSourcesContext): Set<string> {
    const ids = new Set<string>();
    for (const key of this.sourceKeysForEditor(ctx)) {
      const id = appUserIdFromSourceKey(key);
      if (id) ids.add(id);
    }
    return ids;
  },

  appUserSource(ctx: HydrateSourcesContext, key: string) {
    const id = appUserIdFromSourceKey(key);
    if (!id) return undefined;
    const user = ctx.users.find((item) => item.id === id);
    if (
      !user ||
      !user.app ||
      !user.active ||
      isLinearAppUser(user) ||
      isSlackAsksAppUser(user)
    ) {
      return undefined;
    }
    const agent =
      user.appScopes?.some(
        (scope) => scope === "app:mentionable" || scope === "app:assignable",
      ) ?? false;
    return {
      id: `appUser:${user.id}`,
      name: user.displayName || user.name,
      iconUrl: user.avatarUrl,
      kind: (agent ? "agent" : "appUser") as TrustedSourceKind,
    };
  },

  integrationSource(service: IntegrationSourceService | string) {
    return {
      id: `integration:${service}`,
      name: integrationDisplayName(service),
      kind: "integration" as const,
    };
  },

  integrationSourceForKey(ctx: HydrateSourcesContext, key: string) {
    const service = integrationServiceFromSourceKey(key);
    if (!service) return undefined;
    if (service === "asksWeb") {
      // Asks web pages are optional; keep option when service is listed.
      if (!ctx.integrationServices.includes("asksWeb")) return undefined;
      return this.integrationSource(service);
    }
    if (!ctx.integrationServices.includes(service)) return undefined;
    return this.integrationSource(service);
  },

  specialSourceForKey(key: string) {
    if (key === EMAIL_TRUSTED_SOURCE_KEY) {
      return {
        id: EMAIL_TRUSTED_SOURCE_KEY,
        name: specialSourceName(EMAIL_TRUSTED_SOURCE_KEY),
        kind: "special" as const,
      };
    }
    return undefined;
  },

  oauthClientSourceForKey(key: string, catalog?: CatalogSource[]) {
    if (!isOauthClientSourceKey(key)) return undefined;
    const catalogHit = catalog?.find((item) => item.id === key);
    if (catalogHit) {
      return {
        id: key,
        name: catalogHit.name,
        iconUrl: catalogHit.iconUrl,
        kind: "oauthClient" as const,
      };
    }
    const id = key.slice("oauthClient:".length);
    if (!id) return undefined;
    return {
      id: key,
      name: `OAuth app (${id})`,
      kind: "oauthClient" as const,
    };
  },

  sourceForKey(ctx: HydrateSourcesContext, key: string) {
    return (
      this.appUserSource(ctx, key) ||
      this.integrationSourceForKey(ctx, key) ||
      this.oauthClientSourceForKey(key, ctx.catalogSources) ||
      this.specialSourceForKey(key)
    );
  },

  disambiguateDuplicateNames<T extends { name: string; kind?: TrustedSourceKind }>(
    sources: T[],
    catalogNames: string[],
  ): T[] {
    const counts = new Map<string, number>();
    for (const name of [...sources.map((s) => s.name), ...catalogNames]) {
      const key = name.trim().toLocaleLowerCase();
      counts.set(key, (counts.get(key) ?? 0) + 1);
    }
    return sources.map((source) => {
      const key = source.name.trim().toLocaleLowerCase();
      if ((counts.get(key) ?? 0) === 1 || !source.kind) return source;
      return {
        ...source,
        name: `${source.name} (${KIND_LABEL[source.kind]})`,
      };
    });
  },

  sourcesForEditor(ctx: HydrateSourcesContext): TrustedSourceEditorOption[] {
    const allowed = this.allowedSourceKeys(ctx.settings);
    const catalog = new Map(
      (ctx.catalogSources ?? []).map((item) => [item.id, item]),
    );
    const catalogHits: Array<{
      id: string;
      name: string;
      iconUrl?: string;
      kind?: TrustedSourceKind;
    }> = [];
    const resolved: Array<{
      id: string;
      name: string;
      iconUrl?: string;
      kind?: TrustedSourceKind;
    }> = [];

    for (const key of this.sourceKeysForEditor(ctx)) {
      if (isOauthClientSourceKey(key) && catalog.has(key)) {
        const hit = catalog.get(key)!;
        catalogHits.push({
          id: key,
          name: hit.name,
          iconUrl: hit.iconUrl,
          kind: "oauthClient",
        });
        continue;
      }
      const source = this.sourceForKey(ctx, key);
      if (source) resolved.push(source);
    }

    return [
      ...catalogHits,
      ...this.disambiguateDuplicateNames(resolved, catalogHits.map((c) => c.name)),
    ]
      .sort((a, b) => a.name.localeCompare(b.name))
      .map((source) => ({
        id: source.id,
        name: source.name,
        iconUrl: source.iconUrl,
        kind: source.kind,
        allowed: allowed.has(source.id),
      }));
  },
};
