import { ArrowUp, ChevronRight, CircleCheck } from "lucide-react";
import { useState } from "react";

import { MentionBody } from "@/components/editor/mentions/mention-body";
import { UserAvatar } from "@/components/ui/user-avatar";
import { useI18n } from "@/i18n/i18n";
import { commentOnReview, resolveReviewThread } from "@/lib/api";
import { targetCommentHash } from "@/lib/review-comment-navigation";
import { toast } from "@/lib/toast";
import type { CodeReview, ReviewEvent } from "@/types/flow";

import { PullRequestCommentActions } from "./pull-request-comment-actions";
import { reviewShortAge } from "./review-list-model";
import { splitSuggestionBody, type ReviewThreadModel } from "./pull-request-review-model";

/** An inline code comment thread: comments, suggestions, reply composer and resolve toggle. */
export function ReviewThread({
  review,
  thread,
  originalLine,
  onReload,
}: {
  review: CodeReview;
  thread: ReviewThreadModel;
  /** The commented line's current content, shown as the removed side of suggestions. */
  originalLine?: string;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  const [expanded, setExpanded] = useState(!thread.resolved);
  const [reply, setReply] = useState("");
  const [busy, setBusy] = useState(false);
  const comments = [thread.root, ...thread.replies];
  const run = async (task: () => Promise<unknown>, failure: string) => {
    setBusy(true);
    try {
      await task();
      await onReload();
      return true;
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t(failure));
      return false;
    } finally {
      setBusy(false);
    }
  };
  const sendReply = async () => {
    if (!reply.trim() || busy) return;
    if (await run(() => commentOnReview(review.id, reply.trim(), { parentId: thread.root.id }), "Could not submit comment")) setReply("");
  };
  const toggleResolved = () => run(() => resolveReviewThread(review.id, thread.root.id, !thread.resolved), "Could not update thread");
  return (
    <div className="review-thread" data-resolved={thread.resolved || undefined}>
      {thread.resolved && (
        <button type="button" className="review-thread-resolved" aria-expanded={expanded} onClick={() => setExpanded((value) => !value)}>
          <ChevronRight />
          <CircleCheck />
          <span>{t("Resolved")}</span>
          <small>
            {comments.length} {t(comments.length === 1 ? "comment" : "comments")}
          </small>
        </button>
      )}
      {expanded && (
        <>
          {comments.map((comment) => (
            <ReviewThreadComment key={comment.id} review={review} comment={comment} originalLine={originalLine} />
          ))}
          <div className="review-thread-reply">
            <textarea
              aria-label={t("Reply")}
              placeholder={t("Leave a reply…")}
              rows={1}
              value={reply}
              onChange={(event) => setReply(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
                  event.preventDefault();
                  void sendReply();
                }
              }}
            />
            <button type="button" className="review-round-button" aria-label={t("Submit reply")} disabled={busy || !reply.trim()} onClick={() => void sendReply()}>
              <ArrowUp />
            </button>
          </div>
          <footer>
            <button type="button" className="review-pill-button is-small" disabled={busy} onClick={() => void toggleResolved()}>
              {t(thread.resolved ? "Unresolve" : "Resolve")}
            </button>
          </footer>
        </>
      )}
    </div>
  );
}

function ReviewThreadComment({ review, comment, originalLine }: { review: CodeReview; comment: ReviewEvent; originalLine?: string }) {
  const { t } = useI18n();
  const actor = comment.actor.displayName || comment.actor.name;
  return (
    <div className="review-thread-comment" id={targetCommentHash(comment.id)} data-review-comment-id={comment.id}>
      <header>
        <UserAvatar className="avatar review-thread-avatar" name={actor} avatarUrl={comment.actor.avatarUrl} />
        <strong data-i18n-ignore>{actor}</strong>
        <time dateTime={comment.createdAt} title={new Date(comment.createdAt).toLocaleString()}>
          {reviewShortAge(comment.createdAt)} {t("ago")}
        </time>
        <PullRequestCommentActions event={comment} reviewId={review.id} issueIds={review.issueIds} reviewUrl={review.url} />
      </header>
      <ReviewCommentBody body={comment.body ?? ""} originalLine={originalLine} />
    </div>
  );
}

/** Comment markdown with ```suggestion blocks rendered as suggested changes. */
export function ReviewCommentBody({ body, originalLine, className = "review-thread-body" }: { body: string; originalLine?: string; className?: string }) {
  const { t } = useI18n();
  return (
    <>
      {splitSuggestionBody(body).map((part, index) =>
        part.kind === "text" ? (
          <MentionBody key={index} className={className} body={part.text} />
        ) : (
          <div key={index} className="review-suggestion">
            <div className="review-suggestion-title">{t("Suggested change")}</div>
            <pre data-i18n-ignore>
              {originalLine !== undefined && <span className="is-remove">{originalLine}</span>}
              {part.lines.map((line, lineIndex) => (
                <span className="is-add" key={lineIndex}>
                  {line}
                </span>
              ))}
            </pre>
          </div>
        ),
      )}
    </>
  );
}
