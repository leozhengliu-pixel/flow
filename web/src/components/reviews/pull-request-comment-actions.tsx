/**
 * LS-0503 PullRequestCommentActions — comment menu pack including agent dispatch.
 * Maps the reference app's GraphQL PullRequestCommentDispatchToAgent onto REST createAgentSession.
 */
import * as Popover from "@radix-ui/react-popover";
import { Copy, Link2, MoreHorizontal } from "lucide-react";
import { useState } from "react";
import { toast } from "@/lib/toast";

import { useI18n } from "@/i18n/i18n";
import { createAgentSession } from "@/lib/api";
import { targetCommentHash } from "@/lib/review-comment-navigation";
import type { ReviewEvent } from "@/types/flow";
import { AgentCursorGlyph } from '@/components/ui/agent-glyph'

import { buildAgentDispatchPrompt, formatReviewCommentMarkdown } from "./pull-request-comment-markdown";
// coding sessions (track G)
import { sendReviewCommentToCodingAgent } from "@/components/coding-sessions/review-coding-model";
import { toastCodingStartError } from "@/components/coding-sessions/coding-session-start";
import { useCodingAvailability } from "@/components/coding-sessions/use-coding-sessions";

export type PullRequestCommentActionsProps = {
  event: ReviewEvent;
  reviewId: string;
  issueIds?: string[];
  reviewUrl?: string;
  onAgentSessionCreated?: (sessionId: string) => void;
};

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
  // coding sessions (track G): with coding sessions on, the comment goes to the PR's coding session as a follow-up.
  const coding = useCodingAvailability();
  const workspace = decodeURIComponent(location.pathname.split("/").filter(Boolean)[0] ?? "");

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
    if (coding) {
      try {
        await sendReviewCommentToCodingAgent(reviewId, event);
        toast.success(t("Sent comment to coding agent"));
      } catch (error) {
        toastCodingStartError(error, workspace, t);
      } finally {
        setBusy(false);
      }
      return;
    }
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
              <AgentCursorGlyph size={14} />
              {t(coding ? "Send comment to agent" : "Send to agent")}
            </button>
          ) : null}
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}
