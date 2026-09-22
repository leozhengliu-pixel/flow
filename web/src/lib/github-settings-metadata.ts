/**
 * LS-0282 GitHubSettingsMetadata — Linear-aligned settings metadata pack.
 * REST/bootstrap only (no GraphQL). Used by CodeIntegrationSettings sections.
 */

export type GitHubSettingsMetadataItem = {
  id: string;
  title: string;
  description: string;
  /** IntegrationConnection.config key when the item is a persisted toggle. */
  configKey?: string;
  /** Default when config key is unset. */
  defaultEnabled?: boolean;
};

export type GitHubSettingsMetadataSection = {
  id: string;
  title: string;
  items: Record<string, GitHubSettingsMetadataItem>;
};

export type GitHubSettingsPageMetadata = {
  id: string;
  title: string;
  sections: Record<string, GitHubSettingsMetadataSection>;
};

/** PR guides item — Linear `pullRequestGuidesSettingsMetadata`. */
export const pullRequestGuidesSettingsMetadata: GitHubSettingsMetadataItem = {
  id: "pull-request-tour",
  title: "Generate Pull Request guides",
  description: "Generate guided reviews for new pull requests",
  configKey: "reviewGuides",
  defaultEnabled: true,
};

/** Issue linking item — Linear `pullRequestIssueLinkingSettingsMetadata`. */
export const pullRequestIssueLinkingSettingsMetadata: GitHubSettingsMetadataItem =
  {
    id: "pull-request-issue-mode",
    title: "Automatically link Flow issues",
    description:
      "Link a matching issue automatically, or generate one on merge when none is linked.",
    configKey: "autoLink",
    defaultEnabled: false,
  };

/** Primary GitHub settings page metadata. */
export const gitHubSettingsPageMetadata: GitHubSettingsPageMetadata = {
  id: "github",
  title: "GitHub",
  sections: {
    pullRequests: {
      id: "pull-requests",
      title: "Pull requests",
      items: {
        pullRequestGuides: pullRequestGuidesSettingsMetadata,
        pullRequestIssueLinking: pullRequestIssueLinkingSettingsMetadata,
      },
    },
  },
};

/**
 * GitHub Enterprise Cloud settings page metadata.
 * Same pull-request sections; title/id distinguish the GHE Cloud surface.
 */
export const gitHubEnterpriseCloudSettingsPageMetadata: GitHubSettingsPageMetadata =
  {
    ...gitHubSettingsPageMetadata,
    id: "github-enterprise-cloud",
    title: "GitHub Enterprise Cloud",
  };

export const GITHUB_ENTERPRISE_CLOUD = {
  id: "github-enterprise-cloud",
  title: "GitHub Enterprise Cloud",
  description:
    "Connect a GitHub Enterprise Cloud organization with data residency. Use the hostname of your GHE Cloud instance.",
  hostnameLabel: "GitHub Enterprise Cloud hostname",
  hostnamePlaceholder: "company.ghe.com",
  connectLabel: "Connect GitHub Enterprise Cloud",
} as const;

export function isGithubEnterpriseCloudConnection(config?: Record<
  string,
  string
> | null): boolean {
  if (!config) return false;
  return (
    config.enterpriseCloud === "true" ||
    config.kind === "enterprise-cloud" ||
    Boolean(config.enterpriseUrl?.trim())
  );
}

export function githubMetadataItemEnabled(
  item: GitHubSettingsMetadataItem,
  config?: Record<string, string> | null,
): boolean {
  if (!item.configKey) return Boolean(item.defaultEnabled);
  const raw = config?.[item.configKey];
  if (raw === undefined || raw === "") {
    return Boolean(item.defaultEnabled);
  }
  return raw === "true";
}
