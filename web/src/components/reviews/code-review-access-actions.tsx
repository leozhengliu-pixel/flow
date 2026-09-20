/**
 * LS-0111 CodeReviewAccessActions — contextual ACL actions for connect / grant / denied.
 */
import { GitMerge, GitPullRequest, ShieldAlert } from "lucide-react";

import { useI18n } from "@/i18n/i18n";
import {
  resolveCodeReviewAccess,
  type CodeReviewAccessReason,
} from "@/lib/code-access";
import type { BootstrapData } from "@/types/flow";

export type CodeReviewAccessActionTarget =
  | { kind: "connect"; provider: "github" | "gitlab" }
  | { kind: "grant-code-access"; provider: "github" }
  | { kind: "reconnect"; provider: "github" };

type Props = {
  data: BootstrapData;
  variant?: "empty" | "banner" | "inline";
  onAction: (target: CodeReviewAccessActionTarget) => void;
};

export function codeReviewAccessCopy(reason: CodeReviewAccessReason): {
  title: string;
  description: string;
  primary?: { label: string; target: CodeReviewAccessActionTarget };
  secondary?: { label: string; target: CodeReviewAccessActionTarget };
} {
  switch (reason) {
    case "workspace_connection_missing":
      return {
        title: "Connect a code host",
        description: "Connect GitHub or GitLab to sync pull and merge requests.",
        primary: { label: "Connect GitHub", target: { kind: "connect", provider: "github" } },
        secondary: { label: "Connect GitLab", target: { kind: "connect", provider: "gitlab" } },
      };
    case "workspace_code_access_missing":
      return {
        title: "Code access required",
        description: "Reviews and diffs need a GitHub integration with code access.",
        primary: {
          label: "Enable code access",
          target: { kind: "grant-code-access", provider: "github" },
        },
      };
    case "personal_connection_missing":
      return {
        title: "Personal GitHub connection required",
        description: "Connect your personal GitHub account to review private repositories.",
        primary: { label: "Connect GitHub", target: { kind: "connect", provider: "github" } },
      };
    case "personal_connection_reconnect_required":
      return {
        title: "Reconnect GitHub",
        description: "Your GitHub connection needs to be re-authorized before reviews can sync.",
        primary: { label: "Reconnect GitHub", target: { kind: "reconnect", provider: "github" } },
      };
    default:
      return {
        title: "Nothing to review",
        description: "Reviews assigned to you will appear here.",
      };
  }
}

export function CodeReviewAccessActions({ data, variant = "empty", onAction }: Props) {
  const { t } = useI18n();
  const { reason } = resolveCodeReviewAccess(data);
  if (reason === "granted") return null;
  const copy = codeReviewAccessCopy(reason);
  const className =
    variant === "banner"
      ? "code-review-access is-banner"
      : variant === "inline"
        ? "code-review-access is-inline"
        : "code-review-access is-empty";

  return (
    <div className={className} role="status">
      {variant === "empty" ? <GitPullRequest /> : <ShieldAlert size={18} />}
      <strong>{t(copy.title)}</strong>
      <span>{t(copy.description)}</span>
      {(copy.primary || copy.secondary) && (
        <div className="code-review-access-actions">
          {copy.primary && (
            <button type="button" className="primary" onClick={() => onAction(copy.primary!.target)}>
              {copy.primary.target.provider === "gitlab" ? <GitMerge size={14} /> : <GitPullRequest size={14} />}
              {t(copy.primary.label)}
            </button>
          )}
          {copy.secondary && (
            <button type="button" onClick={() => onAction(copy.secondary!.target)}>
              {copy.secondary.target.provider === "gitlab" ? <GitMerge size={14} /> : <GitPullRequest size={14} />}
              {t(copy.secondary.label)}
            </button>
          )}
        </div>
      )}
    </div>
  );
}
