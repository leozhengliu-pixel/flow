/**
 * PullRequestHeader — the pull request page chrome: breadcrumb row (issue ›
 * state icon, title, diff stat, favorite, actions) with the repository pill,
 * copy-link and full-window controls, then the Overview / Guide / Diff tab
 * pills with the primary review actions on the right.
 */
import type { ReactNode } from "react";
import { Expand, FileDiff, Link2, PanelLeft, Shrink, Star } from "lucide-react";

import { useI18n } from "@/i18n/i18n";
import { issuePath, reviewPath, reviewsPath, type ReviewRouteTab } from "@/lib/app-routes";
import { resolveCodeReviewAccess } from "@/lib/code-access";
import { toast } from "@/lib/toast";
import type { BootstrapData, CodeReview, Issue } from "@/types/flow";

import { reviewDocsOnly, reviewHeaderStat } from "./pull-request-review-model";
import { CodeHostIcon } from "./review-provider";
import { reviewProviderIdentifier } from "./review-provider-model";
import { ReviewStateIcon, ReviewTitle } from "./review-state-icon";

const REVIEW_TABS: { id: ReviewRouteTab; label: string }[] = [
  { id: "overview", label: "Overview" },
  { id: "review", label: "Guide" },
  { id: "changes", label: "Diff" },
];

export function PullRequestHeader({
  data,
  review,
  issues,
  tab,
  fullWindow,
  onToggleFull,
  onNavigate,
  onMutate,
  onOpenPicker,
  onOpenSidebar,
  returnPath,
  menu,
  actions,
}: {
  data: BootstrapData;
  review: CodeReview;
  issues: Issue[];
  tab: ReviewRouteTab;
  fullWindow: boolean;
  onToggleFull: (value: boolean) => void;
  onNavigate: (path: string) => void;
  onMutate: (input: { favorite?: boolean }) => Promise<void>;
  onOpenPicker: (value: "reviewers" | "issues") => void;
  onOpenSidebar?: () => void;
  returnPath?: string;
  /** The "Pull request actions" menu trigger. */
  menu?: ReactNode;
  /** Primary review actions shown at the right of the tab row. */
  actions?: ReactNode;
}) {
  const { t } = useI18n();
  const access = resolveCodeReviewAccess(data);
  const workspace = data.workspace.urlKey;
  const stat = reviewHeaderStat(review);
  const go = (path: string) => (event: React.MouseEvent) => {
    event.preventDefault();
    onNavigate(path);
  };
  const copyUrl = async () => {
    try {
      await navigator.clipboard.writeText(location.href.split("#")[0]);
      toast.success(t("Copied URL to clipboard"));
    } catch {
      toast.error(t("Could not copy to clipboard"));
    }
  };

  return (
    <>
      <header className="review-detail-top" data-code-access={access.reason}>
        <button
          className="review-ghost-button review-menu-button"
          aria-label={t("Menu")}
          data-sidebar-trigger
          onClick={() => (fullWindow ? onToggleFull(false) : onOpenSidebar ? onOpenSidebar() : onNavigate(returnPath ?? reviewsPath(workspace)))}
          type="button"
        >
          <PanelLeft />
        </button>
        <nav className="review-breadcrumb" aria-label={t("Breadcrumb")}>
          {issues[0] ? (
            <a className="review-breadcrumb-issue" href={issuePath(workspace, issues[0])} onClick={go(issuePath(workspace, issues[0]))} data-i18n-ignore>
              {issues[0].identifier}
            </a>
          ) : (
            <button className="review-breadcrumb-issue" type="button" onClick={() => onOpenPicker("issues")}>
              {t("No issue")}
            </button>
          )}
          <span className="review-breadcrumb-separator" aria-hidden="true">
            ›
          </span>
          <ReviewStateIcon review={review} className="review-breadcrumb-state" />
          <a className="review-breadcrumb-title" href={reviewPath(workspace, review)} onClick={go(reviewPath(workspace, review))}>
            <h2 data-i18n-ignore>
              <ReviewTitle title={review.title} />
            </h2>
          </a>
          <button
            className="review-diff-stat"
            type="button"
            aria-label={t("View diff")}
            onClick={() => onNavigate(reviewPath(workspace, review, "changes"))}
          >
            {reviewDocsOnly(review) ? (
              <span className="review-diff-docs">{t("Docs only")}</span>
            ) : (
              <>
                <span className="review-diff-added">+{stat.additions}</span>
                {(stat.deletions > 0 || review.deletions > 0) && <span className="review-diff-removed">-{stat.deletions}</span>}
                <FileDiff />
              </>
            )}
          </button>
          <button
            className="review-ghost-button"
            aria-label={t(review.favorite ? "Remove from favorites" : "Add to favorites")}
            aria-pressed={review.favorite}
            onClick={() => void onMutate({ favorite: !review.favorite })}
            type="button"
          >
            <Star fill={review.favorite ? "currentColor" : "none"} />
          </button>
          {menu}
        </nav>
        <div className="review-detail-top-actions">
          <a className="review-repository-pill" href={review.url} target="_blank" rel="noreferrer" data-i18n-ignore>
            <CodeHostIcon provider={review.provider} />
            <span>
              {review.repositoryName}
              {reviewProviderIdentifier(review)}
            </span>
          </a>
          <button className="review-ghost-button" aria-label={t("Copy URL")} onClick={() => void copyUrl()} type="button">
            <Link2 />
          </button>
          <button
            className="review-ghost-button"
            aria-label={t(fullWindow ? "Exit full window" : "Review diff in full window")}
            aria-pressed={fullWindow}
            title={t(fullWindow ? "Exit full window" : "Review diff in full window")}
            onClick={() => onToggleFull(!fullWindow)}
            type="button"
          >
            {fullWindow ? <Shrink /> : <Expand />}
          </button>
        </div>
      </header>
      <div className="review-detail-tabs">
        <nav aria-label={t("Pull request sections")}>
          {REVIEW_TABS.map((item, index) => (
            <a
              className={tab === item.id ? "active" : ""}
              aria-current={tab === item.id ? "page" : undefined}
              title={`${t(item.id === "overview" ? "View overview" : item.id === "review" ? "View guide" : "View diff")}  ${index + 1}`}
              key={item.id}
              href={reviewPath(workspace, review, item.id)}
              onClick={go(reviewPath(workspace, review, item.id))}
            >
              {t(item.label)}
            </a>
          ))}
        </nav>
        <div className="review-detail-tab-actions">{actions}</div>
      </div>
    </>
  );
}
