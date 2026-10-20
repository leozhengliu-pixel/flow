import * as RadixMenu from "@radix-ui/react-dropdown-menu";
import { CheckCircle2, Copy, ExternalLink, GitBranch, GitMerge, Link2, MoreHorizontal, RotateCcw, Star, Users, X } from "lucide-react";
import { useState, type ReactNode } from "react";

import { useI18n } from "@/i18n/i18n";
import { toast } from "@/lib/toast";
import type { CodeReview } from "@/types/flow";

import { reviewProviderNoun } from "./review-provider-model";
import { ReviewStateIcon } from "./review-state-icon";
import "@/components/project-detail/project-action-menu.css";

type ReviewMutation = { favorite?: boolean; draft?: boolean; status?: CodeReview["status"]; branchState?: CodeReview["branchState"] };

/** "Pull request actions" (…) menu: open, favorite, copy, link, reviewers, state changes. */
export function PullRequestActionsMenu({
  review,
  busy,
  onMutate,
  onOpenPicker,
  onQuickApprove,
  onMerge,
  canQuickApprove,
}: {
  review: CodeReview;
  busy?: boolean;
  onMutate: (input: ReviewMutation) => Promise<void>;
  onOpenPicker: (value: "reviewers" | "issues") => void;
  onQuickApprove: () => Promise<void>;
  onMerge: () => Promise<void>;
  canQuickApprove: boolean;
}) {
  const { t } = useI18n();
  const [approving, setApproving] = useState(false);
  const closed = review.status === "merged" || review.status === "closed";
  const noun = reviewProviderNoun(review.provider);
  const host = review.provider === "gitlab" ? "GitLab" : "GitHub";
  const copy = async (value: string, message: string) => {
    try {
      await navigator.clipboard.writeText(value);
      toast.success(t(message));
    } catch {
      toast.error(t("Could not copy to clipboard"));
    }
  };
  const quickApprove = async () => {
    setApproving(true);
    try {
      await onQuickApprove();
    } finally {
      setApproving(false);
    }
  };
  return (
    <RadixMenu.Root>
      <RadixMenu.Trigger asChild>
        <button className="review-ghost-button" type="button" aria-label={t(`${noun} actions`)}>
          <MoreHorizontal />
        </button>
      </RadixMenu.Trigger>
      <RadixMenu.Portal>
        <RadixMenu.Content data-flow-motion="floating" className="project-action-menu review-actions-menu" align="start" sideOffset={4} collisionPadding={16}>
          <MenuItem icon={<ExternalLink />} label={t(`Open in ${host}`)} shortcut={["O", "then", "G"]} onSelect={() => window.open(review.url, "_blank", "noopener,noreferrer")} />
          <MenuItem icon={<Star />} label={t(review.favorite ? "Unfavorite" : "Favorite")} shortcut={["Alt", "F"]} onSelect={() => void onMutate({ favorite: !review.favorite })} />
          <RadixMenu.Separator />
          <RadixMenu.Sub>
            <RadixMenu.SubTrigger>
              <span className="project-menu-icon">
                <Copy />
              </span>
              <span className="project-menu-label">{t("Copy")}</span>
              <span className="project-menu-chevron" aria-hidden="true">
                ▶
              </span>
            </RadixMenu.SubTrigger>
            <RadixMenu.Portal>
              <RadixMenu.SubContent data-flow-motion="floating" className="project-action-menu project-action-submenu" sideOffset={4} collisionPadding={16}>
                <MenuItem icon={<Link2 />} label={t("Copy URL")} onSelect={() => void copy(location.href.split("#")[0], "Copied URL to clipboard")} />
                <MenuItem icon={<GitBranch />} label={t("Copy branch name")} onSelect={() => void copy(review.headBranch, "Copied branch name to clipboard")} />
                <MenuItem icon={<Copy />} label={t("Copy title")} onSelect={() => void copy(review.title, "Copied title to clipboard")} />
              </RadixMenu.SubContent>
            </RadixMenu.Portal>
          </RadixMenu.Sub>
          <RadixMenu.Separator />
          <MenuItem icon={<Link2 />} label={t("Link issue")} shortcut={["Ctrl", "Alt", "L"]} onSelect={() => onOpenPicker("issues")} />
          <MenuItem icon={<Users />} label={t("Select reviewers")} shortcut={["⇧", "R"]} onSelect={() => onOpenPicker("reviewers")} />
          {canQuickApprove && (
            <MenuItem
              icon={<CheckCircle2 />}
              label={t("Quick approve")}
              shortcut={["R", "then", "A"]}
              disabled={closed || busy || approving}
              onSelect={() => void quickApprove()}
            />
          )}
          <RadixMenu.Separator />
          {closed ? (
            <MenuItem icon={<RotateCcw />} label={t(`Reopen ${noun}`)} onSelect={() => void onMutate({ status: "open" })} />
          ) : (
            <>
              {review.branchState === "behind" && <MenuItem icon={<GitBranch />} label={t("Update branch")} onSelect={() => void onMutate({ branchState: "upToDate" })} />}
              <MenuItem
                icon={review.draft ? <CheckCircle2 /> : <ReviewStateIcon review={{ ...review, draft: true }} />}
                label={t(review.draft ? "Mark ready for review" : "Convert to draft")}
                onSelect={() => void onMutate({ draft: !review.draft })}
              />
              {!review.draft && <MenuItem icon={<GitMerge />} label={t(`Merge ${noun}`)} onSelect={() => void onMerge()} />}
              <RadixMenu.Separator />
              <MenuItem icon={<X />} label={t(`Close ${noun}`)} onSelect={() => void onMutate({ status: "closed" })} />
            </>
          )}
        </RadixMenu.Content>
      </RadixMenu.Portal>
    </RadixMenu.Root>
  );
}

function MenuItem({
  icon,
  label,
  shortcut,
  disabled,
  onSelect,
}: {
  icon: ReactNode;
  label: string;
  shortcut?: string[];
  disabled?: boolean;
  onSelect: () => void;
}) {
  return (
    <RadixMenu.Item disabled={disabled} onSelect={onSelect}>
      <span className="project-menu-icon">{icon}</span>
      <span className="project-menu-label">{label}</span>
      {shortcut && (
        <span className="project-menu-shortcut" aria-hidden="true">
          {shortcut.map((key, index) => (key === "then" ? <span key={index}>then</span> : <kbd key={index}>{key}</kbd>))}
        </span>
      )}
    </RadixMenu.Item>
  );
}
