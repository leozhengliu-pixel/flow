/**
 * LS-0511 PullRequestReviewShortcutsContext — review shortcut map (list + detail + inbox).
 */
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  type ReactNode,
} from "react";

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

const PullRequestReviewShortcutsContext = createContext<PullRequestReviewShortcutsContextValue | null>(
  null,
);

function isEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  const tag = target.tagName;
  return (
    target.isContentEditable ||
    tag === "INPUT" ||
    tag === "TEXTAREA" ||
    tag === "SELECT" ||
    Boolean(target.closest("[contenteditable=true]"))
  );
}

export function PullRequestReviewShortcutsProvider({
  children,
  handlers,
  enabled = true,
}: {
  children: ReactNode;
  handlers?: PullRequestReviewShortcutHandlers;
  enabled?: boolean;
}) {
  const registered = useRef<PullRequestReviewShortcutHandlers>({});
  const base = useRef(handlers);
  base.current = handlers;

  const register = useCallback((partial: Partial<PullRequestReviewShortcutHandlers>) => {
    registered.current = { ...registered.current, ...partial };
    return () => {
      for (const key of Object.keys(partial) as (keyof PullRequestReviewShortcutHandlers)[]) {
        if (registered.current[key] === partial[key]) delete registered.current[key];
      }
    };
  }, []);

  const resolve = useCallback(
    (key: keyof PullRequestReviewShortcutHandlers) =>
      registered.current[key] ?? base.current?.[key],
    [],
  );

  useEffect(() => {
    if (!enabled) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.metaKey || event.ctrlKey || event.altKey) return;
      if (isEditableTarget(event.target)) return;
      const map: Record<string, keyof PullRequestReviewShortcutHandlers> = {
        f: "focusFileFilter",
        "/": "searchInFiles",
        o: "toggleHoveredFile",
        v: "markHoveredFileReviewed",
        c: "markHoveredSectionReviewed",
        s: "submitReview",
        l: "toggleReviewLayout",
        j: "nextFile",
        k: "previousFile",
        a: "approve",
      };
      const handlerKey = map[event.key];
      if (!handlerKey) return;
      const handler = resolve(handlerKey);
      if (!handler) return;
      event.preventDefault();
      handler();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [enabled, resolve]);

  const value = useMemo<PullRequestReviewShortcutsContextValue>(
    () => ({
      focusFileFilter: () => resolve("focusFileFilter")?.(),
      searchInFiles: () => resolve("searchInFiles")?.(),
      toggleHoveredFile: () => resolve("toggleHoveredFile")?.(),
      markHoveredFileReviewed: () => resolve("markHoveredFileReviewed")?.(),
      markHoveredSectionReviewed: () => resolve("markHoveredSectionReviewed")?.(),
      markTourFileReviewed: () => resolve("markTourFileReviewed")?.(),
      submitReview: () => resolve("submitReview")?.(),
      toggleReviewLayout: () => resolve("toggleReviewLayout")?.(),
      nextFile: () => resolve("nextFile")?.(),
      previousFile: () => resolve("previousFile")?.(),
      approve: () => resolve("approve")?.(),
      register,
    }),
    [register, resolve],
  );

  return (
    <PullRequestReviewShortcutsContext.Provider value={value}>
      {children}
    </PullRequestReviewShortcutsContext.Provider>
  );
}

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
