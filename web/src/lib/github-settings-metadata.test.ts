import { describe, expect, it } from "vitest";

import {
  GITHUB_ENTERPRISE_CLOUD,
  gitHubEnterpriseCloudSettingsPageMetadata,
  gitHubSettingsPageMetadata,
  githubMetadataItemEnabled,
  isGithubEnterpriseCloudConnection,
  pullRequestGuidesSettingsMetadata,
  pullRequestIssueLinkingSettingsMetadata,
} from "./github-settings-metadata";

describe("GitHubSettingsMetadata (LS-0282)", () => {
  it("exposes Linear-aligned page + PR metadata ids", () => {
    expect(gitHubSettingsPageMetadata.id).toBe("github");
    expect(gitHubEnterpriseCloudSettingsPageMetadata.id).toBe(
      "github-enterprise-cloud",
    );
    expect(gitHubEnterpriseCloudSettingsPageMetadata.title).toBe(
      GITHUB_ENTERPRISE_CLOUD.title,
    );
    const section = gitHubSettingsPageMetadata.sections.pullRequests;
    expect(section.id).toBe("pull-requests");
    expect(section.items.pullRequestGuides).toEqual(
      pullRequestGuidesSettingsMetadata,
    );
    expect(section.items.pullRequestIssueLinking).toEqual(
      pullRequestIssueLinkingSettingsMetadata,
    );
  });

  it("defaults PR guides on and auto-link off when unset", () => {
    expect(githubMetadataItemEnabled(pullRequestGuidesSettingsMetadata, {})).toBe(
      true,
    );
    expect(
      githubMetadataItemEnabled(pullRequestIssueLinkingSettingsMetadata, {}),
    ).toBe(false);
    expect(
      githubMetadataItemEnabled(pullRequestGuidesSettingsMetadata, {
        reviewGuides: "false",
      }),
    ).toBe(false);
    expect(
      githubMetadataItemEnabled(pullRequestIssueLinkingSettingsMetadata, {
        autoLink: "true",
      }),
    ).toBe(true);
  });

  it("detects Enterprise Cloud connections", () => {
    expect(isGithubEnterpriseCloudConnection({ organization: "acme" })).toBe(
      false,
    );
    expect(
      isGithubEnterpriseCloudConnection({
        enterpriseCloud: "true",
        enterpriseUrl: "company.ghe.com",
      }),
    ).toBe(true);
    expect(
      isGithubEnterpriseCloudConnection({ enterpriseUrl: "company.ghe.com" }),
    ).toBe(true);
  });
});
