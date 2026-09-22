/**
 * LS-0506 PullRequestInboxView — inbox split hosting header/diff instead of teaser-only.
 * Does not block the whole panel on full GitHub ACL; shows access actions when needed.
 */
import { GitBranch, GitMerge, GitPullRequest } from "lucide-react";

import { CodeReviewAccessActions } from "@/components/reviews/code-review-access-actions";
import { DiffView } from "@/components/reviews/diff-view";
import { useI18n } from "@/i18n/i18n";
import { resolveCodeReviewAccess } from "@/lib/code-access";
import type { BootstrapData, CodeReview } from "@/types/flow";

import { reviewProviderIdentifier } from "./review-provider";

export function PullRequestInboxView({
  data,
  review,
  onOpen,
  onReload,
  onAccessAction,
}: {
  data: BootstrapData;
  review: CodeReview;
  onOpen: () => void;
  onReload?: () => Promise<void> | void;
  onAccessAction?: Parameters<typeof CodeReviewAccessActions>[0]["onAction"];
}) {
  const { t } = useI18n();
  const access = resolveCodeReviewAccess(data);
  const checksPassed = review.checks.every(
    (check) => check.status === "passed" || check.status === "skipped",
  );

  return (
    <div className="flow-inbox-review-detail pull-request-inbox-view">
      <header>
        <span className={`flow-inbox-review-provider ${review.provider}`}>
          {review.provider === "github" ? "GH" : "GL"}
        </span>
        <div>
          <small>
            {review.provider === "github" ? t("GitHub pull request") : t("GitLab merge request")}
          </small>
          <h2 data-i18n-ignore>{review.title}</h2>
        </div>
      </header>
      <p>
        {review.author.displayName} · {review.repositoryOwner}/{review.repositoryName}{" "}
        {reviewProviderIdentifier(review)}
      </p>
      <dl>
        <div>
          <dt>{t("Status")}</dt>
          <dd>{review.draft ? t("Draft") : t(review.status)}</dd>
        </div>
        <div>
          <dt>{t("Branches")}</dt>
          <dd>
            <GitBranch size={12} /> {review.headBranch} → {review.baseBranch}
          </dd>
        </div>
        <div>
          <dt>{t("Reviewers")}</dt>
          <dd>{review.reviewerIds.length || t("None")}</dd>
        </div>
        <div>
          <dt>{t("Checks")}</dt>
          <dd>
            {review.checks.length
              ? checksPassed
                ? t("All passed")
                : t("Checks need attention")
              : t("No checks reported")}
          </dd>
        </div>
        <div>
          <dt>{t("Diff")}</dt>
          <dd>
            <b>+{review.additions}</b> / <i>-{review.deletions}</i> · {review.files.length}{" "}
            {t("files")}
          </dd>
        </div>
      </dl>

      {access.reason !== "granted" && onAccessAction ? (
        <CodeReviewAccessActions data={data} variant="inline" onAction={onAccessAction} />
      ) : null}

      {review.files.length > 0 && onReload ? (
        <div className="pull-request-inbox-diff">
          <DiffView review={review} onReload={async () => { await onReload(); }} />
        </div>
      ) : (
        <div className="pull-request-inbox-diff-empty">
          <GitPullRequest size={18} />
          <span>{t("Open the full review for discussion and merge actions.")}</span>
        </div>
      )}

      <div className="pull-request-inbox-actions">
        <button type="button" className="primary" onClick={onOpen}>
          {t("Open review")}
        </button>
        {review.url ? (
          <a href={review.url} target="_blank" rel="noreferrer">
            <GitMerge size={14} />
            {t(review.provider === "github" ? "Open in GitHub" : "Open in GitLab")}
          </a>
        ) : null}
      </div>
    </div>
  );
}
