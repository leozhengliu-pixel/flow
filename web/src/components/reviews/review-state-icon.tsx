import { GitMerge, GitPullRequest, GitPullRequestClosed, GitPullRequestDraft } from "lucide-react";
import type { ComponentProps } from "react";

import type { CodeReview } from "@/types/flow";

import { reviewTitleSegments, reviewVisualState } from "./review-list-model";

/** Pull/merge request icon tinted by state: draft, open, merged or closed. */
export function ReviewStateIcon({ review, ...props }: { review: CodeReview } & ComponentProps<"svg">) {
  const state = reviewVisualState(review);
  const Icon =
    state === "merged"
      ? GitMerge
      : state === "closed"
        ? GitPullRequestClosed
        : state === "draft"
          ? GitPullRequestDraft
          : review.provider === "gitlab"
            ? GitMerge
            : GitPullRequest;
  return <Icon data-review-state={state} {...props} className={`review-state-icon ${props.className ?? ""}`.trim()} />;
}

/** Pull request title with `inline code` rendered as code chips, like the provider. */
export function ReviewTitle({ title }: { title: string }) {
  return (
    <>
      {reviewTitleSegments(title).map((segment, index) =>
        segment.code ? (
          <code key={index} className="review-title-code">
            {segment.text}
          </code>
        ) : (
          <span key={index}>{segment.text}</span>
        ),
      )}
    </>
  );
}
