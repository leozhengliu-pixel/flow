/**
 * LS-0086 AutomationBlockingSourceHelper
 * Resolves blocking / untrusted issue sources for automation gates.
 */
import {
  EMAIL_TRUSTED_SOURCE_KEY,
  integrationDisplayName,
  integrationServiceFromSourceKey,
  isIntegrationSourceService,
  issueSourceMetadataToSourceKey,
  specialSourceName,
  type IssueSourceMetadata,
} from "./automation-source-key";

export type BlockingSourceIssue = {
  sourceMetadata?: IssueSourceMetadata | null;
  creator?: {
    id: string;
    name?: string;
    displayName?: string;
    app?: boolean;
    builtinAgent?: boolean;
  } | null;
};

export type BlockingSource = {
  key: string;
  name: string;
};

export const AutomationBlockingSourceHelper = {
  forIssue(issue: BlockingSourceIssue | null | undefined): BlockingSource | undefined {
    if (!issue) return undefined;
    const metadata = issue.sourceMetadata ?? undefined;
    const key = issueSourceMetadataToSourceKey(metadata);
    if (key && metadata) {
      return { key, name: this.sourceName(key, metadata) };
    }
    const creator = issue.creator;
    if (creator?.app && !creator.builtinAgent) {
      return {
        key: `appUser:${creator.id}`,
        name: creator.displayName || creator.name || creator.id,
      };
    }
    return undefined;
  },

  isUnauthenticatedEmail(
    issue: BlockingSourceIssue | null | undefined,
  ): boolean {
    const metadata = issue?.sourceMetadata;
    return (
      metadata?.type === "email" &&
      metadata.emailIntakeMetadata?.trusted !== true
    );
  },

  sourceName(key: string, metadata?: IssueSourceMetadata | null): string {
    if (key === EMAIL_TRUSTED_SOURCE_KEY) {
      return specialSourceName(EMAIL_TRUSTED_SOURCE_KEY);
    }
    if (
      metadata?.type === "integration" &&
      metadata.subType &&
      isIntegrationSourceService(metadata.subType)
    ) {
      return integrationDisplayName(metadata.subType);
    }
    const service = integrationServiceFromSourceKey(key);
    if (service) return integrationDisplayName(service);
    return metadata?.displayName ?? "an OAuth application";
  },
};
