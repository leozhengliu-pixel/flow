import * as Popover from "@radix-ui/react-popover";
import * as RadixMenu from "@radix-ui/react-dropdown-menu";
import { Check, ChevronDown, CircleCheck, CircleX, GitMerge, MessageSquare, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";

import { useI18n } from "@/i18n/i18n";
import type { CodeReview } from "@/types/flow";

import type { MergeMethod } from "./pull-request-merge-method";
import { MERGE_LABELS, reviewPrimaryAction, type ReviewDecision } from "./pull-request-review-model";
import "@/components/project-detail/project-action-menu.css";

/** Right side of the tab row: ready/request review, merge, and the submit review popover. */
export function PullRequestReviewActions({
  review,
  viewerId,
  busy,
  mergeMethod,
  submitOpen,
  onSubmitOpenChange,
  onMergeMethodChange,
  onMerge,
  onMutate,
  onOpenPicker,
  onSubmit,
}: {
  review: CodeReview;
  viewerId: string;
  busy: boolean;
  mergeMethod: MergeMethod;
  submitOpen: boolean;
  onSubmitOpenChange: (open: boolean) => void;
  onMergeMethodChange: (method: MergeMethod) => void;
  onMerge: (method: MergeMethod) => Promise<void>;
  onMutate: (input: { draft?: boolean }) => Promise<void>;
  onOpenPicker: (value: "reviewers" | "issues") => void;
  onSubmit: (decision: ReviewDecision, body: string) => Promise<boolean>;
}) {
  const { t } = useI18n();
  const action = reviewPrimaryAction(review, viewerId);
  return (
    <>
      {action.mergeable && (
        <div className="review-merge-split" role="group" aria-label={t("Merge")}>
          <button type="button" className="review-pill-button" disabled={busy} onClick={() => void onMerge(mergeMethod)}>
            <GitMerge />
            {t(MERGE_LABELS[mergeMethod])}
          </button>
          <RadixMenu.Root>
            <RadixMenu.Trigger asChild>
              <button type="button" className="review-pill-button is-icon" aria-label={t("Merge options")} disabled={busy}>
                <ChevronDown />
              </button>
            </RadixMenu.Trigger>
            <RadixMenu.Portal>
              <RadixMenu.Content data-flow-motion="floating" className="project-action-menu" align="end" sideOffset={4} collisionPadding={16}>
                {(Object.keys(MERGE_LABELS) as MergeMethod[]).map((method) => (
                  <RadixMenu.Item key={method} onSelect={() => onMergeMethodChange(method)}>
                    <span className="project-menu-icon">{mergeMethod === method ? <Check /> : null}</span>
                    <span className="project-menu-label">{t(MERGE_LABELS[method])}</span>
                  </RadixMenu.Item>
                ))}
              </RadixMenu.Content>
            </RadixMenu.Portal>
          </RadixMenu.Root>
        </div>
      )}
      {action.kind === "readyForReview" || action.kind === "markReady" ? (
        <button type="button" className="review-pill-button is-primary" disabled={busy} onClick={() => void onMutate({ draft: false })}>
          {t(action.kind === "readyForReview" ? "Ready for review" : "Mark as ready")}
        </button>
      ) : action.kind === "requestReview" ? (
        <button type="button" className="review-pill-button" disabled={busy} onClick={() => onOpenPicker("reviewers")}>
          {t("Request review")}
        </button>
      ) : action.kind === "submitReview" || action.kind === "reviewAgain" ? (
        <SubmitReviewPopover
          label={t(action.kind === "reviewAgain" ? "Review again" : "Submit review")}
          primary={Boolean(action.primary)}
          busy={busy}
          open={submitOpen}
          onOpenChange={onSubmitOpenChange}
          onSubmit={onSubmit}
        />
      ) : null}
    </>
  );
}

function SubmitReviewPopover({
  label,
  primary,
  busy,
  open,
  onOpenChange,
  onSubmit,
}: {
  label: string;
  primary: boolean;
  busy: boolean;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSubmit: (decision: ReviewDecision, body: string) => Promise<boolean>;
}) {
  const { t } = useI18n();
  const [body, setBody] = useState("");
  const input = useRef<HTMLTextAreaElement>(null);
  useEffect(() => {
    if (open) requestAnimationFrame(() => input.current?.focus());
  }, [open]);
  const submit = async (decision: ReviewDecision) => {
    if (busy || (decision !== "approve" && !body.trim())) return;
    if (await onSubmit(decision, body.trim())) setBody("");
  };
  return (
    <Popover.Root open={open} onOpenChange={onOpenChange}>
      <Popover.Trigger asChild>
        <button type="button" className={`review-pill-button review-submit${primary ? " is-primary" : ""}`} disabled={busy}>
          {label}
        </button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content
          data-flow-motion="floating"
          className="review-submit-popover"
          align="end"
          sideOffset={6}
          collisionPadding={16}
          aria-label={t("Submit review")}
          onKeyDown={(event) => {
            if (event.key !== "Enter" || !(event.metaKey || event.ctrlKey)) return;
            event.preventDefault();
            void submit(event.shiftKey ? "comment" : event.altKey ? "requestChanges" : "approve");
          }}
        >
          <button type="button" className="review-ghost-button review-submit-dismiss" aria-label={t("Dismiss pending review")} onClick={() => onOpenChange(false)}>
            <X />
          </button>
          <textarea
            ref={input}
            aria-label={t("Review summary")}
            placeholder={t("Leave a comment…")}
            value={body}
            onChange={(event) => setBody(event.target.value)}
          />
          <footer>
            <button type="button" className="review-pill-button" disabled={busy || !body.trim()} onClick={() => void submit("comment")} title={t("Submit review as a comment")}>
              <MessageSquare />
              {t("Only comment")}
            </button>
            <button type="button" className="review-pill-button" disabled={busy || !body.trim()} onClick={() => void submit("requestChanges")} title={t("Submit review requesting changes")}>
              <CircleX className="review-icon-danger" />
              {t("Request changes")}
            </button>
            <button type="button" className="review-pill-button" disabled={busy} onClick={() => void submit("approve")} title={t("Submit review approving")}>
              <CircleCheck className="review-icon-success" />
              {t("Approve")}
            </button>
          </footer>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}
