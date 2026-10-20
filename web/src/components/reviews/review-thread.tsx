import { AlertCircle, ArrowUp, ChevronsUpDown, CircleCheck, LoaderCircle, RotateCcw } from "lucide-react";
import { useState } from "react";

import { MentionBody } from "@/components/editor/mentions/mention-body";
import { UserAvatar } from "@/components/ui/user-avatar";
import { useI18n } from "@/i18n/i18n";
import { commentOnReview, resolveReviewThread, syncReview } from "@/lib/api";
import { targetCommentHash } from "@/lib/review-comment-navigation";
import { toast } from "@/lib/toast";
import type { CodeReview, ReviewEvent, User } from "@/types/flow";

import { PullRequestCommentActions } from "./pull-request-comment-actions";
import { reviewTimeAgo } from "./review-list-model";
import { splitSuggestionBody, type ReviewThreadModel } from "./pull-request-review-model";
import { ReviewCode } from "./review-code";
import { PostedAsCaption } from "./posted-as-caption";

type SuggestionTarget = { path: string; line: number; content?: string };

/** An inline code comment thread: comments, suggestions, reply composer and resolve toggle. */
export function ReviewThread({
  review,
  thread,
  originalLine,
  viewer,
  onReload,
}: {
  review: CodeReview;
  thread: ReviewThreadModel;
  /** The person replying (shown next to the reply box). */
  viewer?: Pick<User, "displayName" | "avatarUrl">;
  /** The commented line's current content, shown as the removed side of suggestions. */
  originalLine?: string;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  const [expanded, setExpanded] = useState(!thread.resolved);
  const [reply, setReply] = useState("");
  const [busy, setBusy] = useState(false);
  const comments = [thread.root, ...thread.replies];
  const target: SuggestionTarget | undefined = thread.root.path && thread.root.line ? { path: thread.root.path, line: thread.root.line, content: originalLine } : undefined;
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
  const toggleResolved = () => void run(() => resolveReviewThread(review.id, thread.root.id, !thread.resolved), "Could not update thread");
  const authors = [...new Set(comments.map((comment) => comment.actor.displayName || comment.actor.name))];
  return (
    <div className="review-thread" data-resolved={thread.resolved || undefined}>
      {thread.resolved && (
        <button type="button" className="review-thread-resolved" aria-expanded={expanded} onClick={() => setExpanded((value) => !value)}>
          <CircleCheck />
          <span>
            {t(comments.length === 1 ? "{count} resolved comment from {names}" : "{count} resolved comments from {names}")
              .replace("{count}", String(comments.length))
              .replace("{names}", authors.join(", "))}
          </span>
          <ChevronsUpDown className="review-thread-expand" />
        </button>
      )}
      {expanded && (
        <>
          {comments.map((comment, index) => (
            <ReviewThreadComment
              key={comment.id}
              review={review}
              comment={comment}
              reply={index > 0}
              target={target}
              resolve={index === 0 ? { resolved: thread.resolved, busy, onToggle: toggleResolved } : undefined}
              onReload={onReload}
            />
          ))}
          <div className="review-thread-reply">
            {viewer && <UserAvatar className="avatar review-thread-avatar" name={viewer.displayName} avatarUrl={viewer.avatarUrl} />}
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
        </>
      )}
    </div>
  );
}

function ReviewThreadComment({
  review,
  comment,
  reply,
  target,
  resolve,
  onReload,
}: {
  review: CodeReview;
  comment: ReviewEvent;
  reply: boolean;
  target?: SuggestionTarget;
  resolve?: { resolved: boolean; busy: boolean; onToggle: () => void };
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  const actor = comment.actor.displayName || comment.actor.name;
  const age = reviewTimeAgo(comment.createdAt);
  return (
    <div className={`review-thread-comment${reply ? " is-reply" : ""}`} id={targetCommentHash(comment.id)} data-review-comment-id={comment.id}>
      <header>
        <UserAvatar className="avatar review-thread-avatar" name={actor} avatarUrl={comment.actor.avatarUrl} />
        <strong data-i18n-ignore>{actor}</strong>
        <time dateTime={comment.createdAt} title={new Date(comment.createdAt).toLocaleString()}>
          {age === "just now" ? t("just now") : age.replace(/ ago$/, ` ${t("ago")}`)}
        </time>
        {!reply && comment.outdated && <span className="review-thread-outdated">{t("Outdated")}</span>}
        <ReviewSyncState review={review} event={comment} onReload={onReload} />
        <span className="review-thread-actions">
          {resolve && (
            <button
              type="button"
              className="review-ghost-button"
              aria-label={t(resolve.resolved ? "Unresolve thread" : "Resolve thread")}
              title={t(resolve.resolved ? "Unresolve thread" : "Resolve thread")}
              disabled={resolve.busy}
              onClick={resolve.onToggle}
            >
              {resolve.resolved ? <RotateCcw /> : <CircleCheck />}
            </button>
          )}
          <PullRequestCommentActions event={comment} reviewId={review.id} issueIds={review.issueIds} reviewUrl={review.url} />
        </span>
      </header>
      <PostedAsCaption postedAs={comment.postedAs} />
      <ReviewCommentBody body={comment.body ?? ""} target={target} />
    </div>
  );
}

/** Comment markdown with ```suggestion blocks rendered as suggested changes. */
export function ReviewCommentBody({ body, target, className = "review-thread-body" }: { body: string; target?: SuggestionTarget; className?: string }) {
  const { t } = useI18n();
  return (
    <>
      {splitSuggestionBody(body).map((part, index) =>
        part.kind === "text" ? (
          <MentionBody key={index} className={className} body={part.text} />
        ) : (
          <div key={index} className="review-suggestion">
            <div className="review-suggestion-title">{t("Suggested change")}</div>
            <div className="review-suggestion-lines">
              {target?.content !== undefined && (
                <div className="review-suggestion-line is-remove">
                  <span className="review-suggestion-number">{target.line}</span>
                  <ReviewCode content={target.content} path={target.path} />
                </div>
              )}
              {part.lines.map((line, lineIndex) => (
                <div className="review-suggestion-line is-add" key={lineIndex}>
                  <span className="review-suggestion-number">{target ? target.line + lineIndex : ""}</span>
                  <ReviewCode content={line} path={target?.path ?? ""} />
                </div>
              ))}
            </div>
          </div>
        ),
      )}
    </>
  );
}

/**
 * Shows a local change the code host has not confirmed: "Syncing…" while it is
 * sent, or "Not synced to GitHub" with the error and a Retry action.
 */
export function ReviewSyncState({ review, event, onReload }: { review: CodeReview; event: ReviewEvent; onReload: () => Promise<void> }) {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  const sync = event.sync;
  if (!sync) return null;
  const host = review.provider === "gitlab" ? "GitLab" : "GitHub";
  if (sync.state === "pending") {
    return (
      <span className="review-sync-state is-pending">
        <LoaderCircle />
        {t("Syncing…")}
      </span>
    );
  }
  const retry = async () => {
    setBusy(true);
    try {
      const updated = await syncReview(review.id, true);
      await onReload();
      const after = updated.events.find((item) => item.id === event.id);
      if (after?.sync?.state === "failed") toast.error(after.sync.error ?? t("Could not sync with the code host"));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not sync with the code host"));
    } finally {
      setBusy(false);
    }
  };
  const label = t(sync.op === "create" ? "Not synced to {host}" : "Thread state not synced to {host}").replace("{host}", host);
  return (
    <span className="review-sync-state is-failed" title={sync.error}>
      <AlertCircle />
      <span>{label}</span>
      <button type="button" className="review-sync-retry" disabled={busy} onClick={() => void retry()}>
        {t("Retry")}
      </button>
    </span>
  );
}
