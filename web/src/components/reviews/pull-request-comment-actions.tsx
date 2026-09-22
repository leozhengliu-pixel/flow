/**
 * LS-0503 PullRequestCommentActions — comment menu pack including agent dispatch.
 * Maps Linear's GraphQL PullRequestCommentDispatchToAgent onto REST createAgentSession.
 */
import * as Popover from "@radix-ui/react-popover";
import { Bot, Copy, Link2, MoreHorizontal } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { useI18n } from "@/i18n/i18n";
import { createAgentSession } from "@/lib/api";
import { targetCommentHash } from "@/lib/review-comment-navigation";
import type { ReviewEvent } from "@/types/flow";

export type PullRequestCommentActionsProps = {
  event: ReviewEvent;
  reviewId: string;
  issueIds?: string[];
  reviewUrl?: string;
  onAgentSessionCreated?: (sessionId: string) => void;
};

export function formatReviewCommentMarkdown(event: ReviewEvent): string {
  const location =
    event.path != null
      ? event.line != null
        ? `\`${event.path}:${event.line}\``
        : `\`${event.path}\``
      : "";
  const body = event.body?.trim() || "(empty comment)";
  return location ? `${location}\n\n${body}` : body;
}

export function buildAgentDispatchPrompt(event: ReviewEvent, reviewId: string): string {
  const markdown = formatReviewCommentMarkdown(event);
  return [
    `Help with this pull request comment (review ${reviewId}).`,
    "",
    markdown,
  ].join("\n");
}

export function PullRequestCommentActions({
  event,
  reviewId,
  issueIds = [],
  reviewUrl,
  onAgentSessionCreated,
}: PullRequestCommentActionsProps) {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  const isCodeComment = Boolean(event.path);

  const copyLink = async () => {
    const hash = targetCommentHash(event.id);
    const url = `${reviewUrl ?? location.href.split("#")[0]}#${hash}`;
    await navigator.clipboard.writeText(url);
    toast.success(t("Comment URL copied to clipboard"));
  };

  const copyMarkdown = async () => {
    await navigator.clipboard.writeText(formatReviewCommentMarkdown(event));
    toast.success(t("Comment content copied to clipboard"));
  };

  const sendToAgent = async () => {
    setBusy(true);
    try {
      const session = await createAgentSession({
        message: buildAgentDispatchPrompt(event, reviewId),
        issueIds,
        location: "toolbar",
      });
      onAgentSessionCreated?.(session.id);
      toast.success(t("Sent comment to agent"));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not send to agent"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Popover.Root>
      <Popover.Trigger asChild>
        <button
          type="button"
          className="review-comment-actions-trigger"
          aria-label={t("Comment actions")}
        >
          <MoreHorizontal size={14} />
        </button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content
          data-flow-motion="floating"
          className="review-comment-actions-menu"
          align="end"
          sideOffset={4}
        >
          <button type="button" onClick={() => void copyLink()}>
            <Link2 size={14} />
            {t("Copy link to comment")}
          </button>
          <button type="button" onClick={() => void copyMarkdown()}>
            <Copy size={14} />
            {t("Copy comment as Markdown")}
          </button>
          {isCodeComment || event.type === "commented" || event.type === "review_commented" ? (
            <button type="button" disabled={busy} onClick={() => void sendToAgent()}>
              <Bot size={14} />
              {t("Send to agent")}
            </button>
          ) : null}
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}
