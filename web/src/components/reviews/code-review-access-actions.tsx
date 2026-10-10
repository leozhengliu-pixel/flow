/**
 * LS-0111 CodeReviewAccessActions — contextual ACL actions for connect / grant / denied.
 */
import { GitMerge, GitPullRequest, ShieldAlert } from "lucide-react";

import { useI18n } from "@/i18n/i18n";
import { resolveCodeReviewAccess } from "@/lib/code-access";
import type { BootstrapData } from "@/types/flow";

import { codeReviewAccessCopy, type CodeReviewAccessActionTarget } from "./code-review-access-copy";

type Props = {
  data: BootstrapData;
  variant?: "empty" | "banner" | "inline";
  onAction: (target: CodeReviewAccessActionTarget) => void;
};

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
