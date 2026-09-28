/**
 * LS-0505 PullRequestHeader — title / status / branch / draft / approve / merge chrome.
 * Adds squash&merge method picker + merge-queue affordance without requiring full GitHub ACL.
 */
import * as Popover from "@radix-ui/react-popover";
import type { ReactNode } from "react";
import {
  ChevronLeft,
  ChevronRight,
  GitBranch,
  GitMerge,
  Link2,
  Maximize2,
  PanelRightClose,
  Star,
} from "lucide-react";

import { useI18n } from "@/i18n/i18n";
import { issuePath, reviewPath, reviewsPath, type ReviewRouteTab } from "@/lib/app-routes";
import { resolveCodeReviewAccess } from "@/lib/code-access";
import type { BootstrapData, CodeReview, Issue } from "@/types/flow";

import { ReviewProviderIcon, reviewProviderIdentifier } from "./review-provider";

export type MergeMethod = "squash" | "merge" | "rebase";

const MERGE_LABELS: Record<MergeMethod, string> = {
  squash: "Squash and merge",
  merge: "Create a merge commit",
  rebase: "Rebase and merge",
};

export function resolvePreferredMergeMethod(
  preference: string | undefined,
): MergeMethod {
  const value = (preference ?? "").toLowerCase();
  if (value.includes("rebase")) return "rebase";
  if (value.includes("merge commit") || value === "merge") return "merge";
  return "squash";
}

export function PullRequestHeader({
  data,
  review,
  issues,
  tab,
  busy,
  fullWindow,
  mergeMethod,
  onMergeMethodChange,
  onToggleFull,
  onNavigate,
  onMutate,
  onOpenSubmit,
  onOpenPicker,
  onMerge,
  returnPath,
  actions,
}: {
  data: BootstrapData;
  review: CodeReview;
  issues: Issue[];
  tab: ReviewRouteTab;
  busy: boolean;
  fullWindow: boolean;
  mergeMethod: MergeMethod;
  onMergeMethodChange: (method: MergeMethod) => void;
  onToggleFull: (value: boolean) => void;
  onNavigate: (path: string) => void;
  onMutate: (input: { favorite?: boolean; status?: CodeReview["status"] }) => Promise<void>;
  onOpenSubmit: () => void;
  onOpenPicker: (value: "reviewers" | "issues") => void;
  onMerge: (method: MergeMethod) => Promise<void>;
  returnPath?: string;
  actions?: ReactNode;
}) {
  const { t } = useI18n();
  const passed = review.checks.every(
    (check) => check.status === "passed" || check.status === "skipped",
  );
  const closed = review.status === "merged" || review.status === "closed";
  const access = resolveCodeReviewAccess(data);
  const queueEnabled = Boolean(data.userSettings[data.viewer.id]?.checksMergeQueue);
  const showQueue =
    queueEnabled &&
    !closed &&
    (review.branchState === "behind" || review.status === "approved");

  return (
    <>
      <header className="review-detail-top" data-code-access={access.reason}>
        {fullWindow && (
          <button
            className="review-full-menu"
            aria-label={t("Menu")}
            onClick={() => onToggleFull(false)}
          >
            <PanelRightClose />
          </button>
        )}
        <button
          className="review-mobile-back"
          aria-label={t("Back to reviews")}
          onClick={() => onNavigate(returnPath ?? reviewsPath(data.workspace.urlKey))}
        >
          <ChevronLeft />
        </button>
        <div>
          {issues[0] ? (
            <a
              href={issuePath(data.workspace.urlKey, issues[0])}
              onClick={(event) => {
                event.preventDefault();
                onNavigate(issuePath(data.workspace.urlKey, issues[0]));
              }}
              data-i18n-ignore
            >
              {issues[0].identifier}
            </a>
          ) : (
            <button type="button" onClick={() => onOpenPicker("issues")}>
              {t("No issue")}
            </button>
          )}
          <ChevronRight />
          <ReviewProviderIcon provider={review.provider} />
          <strong data-i18n-ignore>
            {reviewProviderIdentifier(review)} {review.title}
          </strong>
          {review.draft ? <em className="review-draft-pill">{t("Draft")}</em> : null}
        </div>
        <span className="review-diff-count">
          <b>+{review.additions}</b>
          <i>-{review.deletions}</i>
        </span>
        <button
          aria-label={t(review.favorite ? "Remove from favorites" : "Add to favorites")}
          onClick={() => void onMutate({ favorite: !review.favorite })}
          type="button"
        >
          <Star fill={review.favorite ? "currentColor" : "none"} />
        </button>
        {actions}
        <button
          aria-label={t("Copy URL")}
          onClick={() => void navigator.clipboard.writeText(location.href)}
          type="button"
        >
          <Link2 />
        </button>
        <button
          aria-label={t("Copy branch name")}
          onClick={() => void navigator.clipboard.writeText(review.headBranch)}
          type="button"
        >
          <GitBranch />
        </button>
        <button
          aria-label={t(fullWindow ? "Exit full window" : "Review diff in full window")}
          aria-pressed={fullWindow}
          onClick={() => onToggleFull(!fullWindow)}
          type="button"
        >
          <Maximize2 />
        </button>
      </header>
      <div className="review-detail-tabs">
        <nav>
          {(["overview", "review", "changes"] as const).map((value) => (
            <a
              className={tab === value ? "active" : ""}
              key={value}
              href={reviewPath(data.workspace.urlKey, review, value)}
              onClick={(event) => {
                event.preventDefault();
                onNavigate(reviewPath(data.workspace.urlKey, review, value));
              }}
            >
              {t(value === "overview" ? "Overview" : value === "review" ? "Guide" : "Diff")}
            </a>
          ))}
        </nav>
        {showQueue ? (
          <span className="review-merge-queue" title={t("Checks & merge queue")}>
            {t("Merge queue")}
          </span>
        ) : null}
        <Popover.Root>
          <Popover.Trigger asChild>
            <button
              disabled={busy || closed || !passed}
              type="button"
              aria-label={t("Merge options")}
            >
              <GitMerge />
              {t(MERGE_LABELS[mergeMethod])}
            </button>
          </Popover.Trigger>
          <Popover.Portal>
            <Popover.Content
              data-flow-motion="floating"
              className="review-merge-method-menu"
              align="end"
              sideOffset={5}
            >
              {(Object.keys(MERGE_LABELS) as MergeMethod[]).map((method) => (
                <button
                  key={method}
                  type="button"
                  aria-checked={mergeMethod === method}
                  role="menuitemradio"
                  onClick={() => {
                    onMergeMethodChange(method);
                    void onMerge(method);
                  }}
                >
                  <GitMerge />
                  {t(MERGE_LABELS[method])}
                </button>
              ))}
            </Popover.Content>
          </Popover.Portal>
        </Popover.Root>
        <button
          className="review-submit"
          disabled={busy || closed}
          onClick={onOpenSubmit}
          type="button"
        >
          {t("Submit review")}
        </button>
      </div>
    </>
  );
}
