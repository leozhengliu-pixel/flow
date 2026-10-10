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
};

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
