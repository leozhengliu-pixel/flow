import { createContext, useContext } from "react";

export type PullRequestReviewShortcutHandlers = {
  focusFileFilter?: () => void;
  searchInFiles?: () => void;
  toggleHoveredFile?: () => void;
  markHoveredFileReviewed?: () => void;
  markHoveredSectionReviewed?: () => void;
  markTourFileReviewed?: () => void;
  submitReview?: () => void;
  toggleReviewLayout?: () => void;
  nextFile?: () => void;
  previousFile?: () => void;
  approve?: () => void;
  openDisplayOptions?: () => void;
  toggleLineWrapping?: () => void;
  toggleAllFiles?: () => void;
  markAllFilesReviewed?: () => void;
  openInProvider?: () => void;
  selectReviewers?: () => void;
  toggleFavorite?: () => void;
};

type ShortcutKey = keyof PullRequestReviewShortcutHandlers;

/** Two-key sequences ("R then S") used by the reference shortcuts. */
export const REVIEW_SHORTCUT_SEQUENCES: Record<string, ShortcutKey> = {
  "r s": "submitReview",
  "r a": "approve",
  "o g": "openInProvider",
  "w l": "toggleLineWrapping",
};

/** Resolves a keydown (plus the pending sequence prefix) to a review shortcut. */
export function resolveReviewShortcut(
  event: Pick<KeyboardEvent, "key" | "code" | "shiftKey" | "altKey" | "metaKey" | "ctrlKey">,
  prefix: string | null,
): { handler?: ShortcutKey; prefix: string | null } {
  if (event.metaKey || event.ctrlKey) return { prefix: null };
  const letter = event.code.startsWith("Key") ? event.code.slice(3).toLowerCase() : event.key.toLowerCase();
  if (event.altKey) {
    if (event.shiftKey && letter === "t") return { handler: "toggleAllFiles", prefix: null };
    if (!event.shiftKey && letter === "a") return { handler: "markAllFilesReviewed", prefix: null };
    if (!event.shiftKey && letter === "f") return { handler: "toggleFavorite", prefix: null };
    return { prefix: null };
  }
  if (prefix) {
    const handler = REVIEW_SHORTCUT_SEQUENCES[`${prefix} ${letter}`];
    if (handler) return { handler, prefix: null };
  }
  if (event.shiftKey) {
    if (letter === "v") return { handler: "openDisplayOptions", prefix: null };
    if (letter === "r") return { handler: "selectReviewers", prefix: null };
    return { prefix: null };
  }
  if (Object.keys(REVIEW_SHORTCUT_SEQUENCES).some((sequence) => sequence.startsWith(`${letter} `))) return { prefix: letter };
  const single: Record<string, ShortcutKey> = {
    ".": "toggleReviewLayout",
    t: "focusFileFilter",
    "/": "searchInFiles",
    v: "markHoveredFileReviewed",
    j: "nextFile",
    k: "previousFile",
  };
  return { handler: single[event.key === "." || event.key === "/" ? event.key : letter], prefix: null };
}

export type PullRequestReviewShortcutsContextValue = PullRequestReviewShortcutHandlers & {
  register: (partial: Partial<PullRequestReviewShortcutHandlers>) => () => void;
};

export const PullRequestReviewShortcutsContext = createContext<PullRequestReviewShortcutsContextValue | null>(
  null,
);

export function usePullRequestReviewShortcuts() {
  const ctx = useContext(PullRequestReviewShortcutsContext);
  if (!ctx) {
    throw new Error("usePullRequestReviewShortcuts requires PullRequestReviewShortcutsProvider");
  }
  return ctx;
}

export function useOptionalPullRequestReviewShortcuts() {
  return useContext(PullRequestReviewShortcutsContext);
}
