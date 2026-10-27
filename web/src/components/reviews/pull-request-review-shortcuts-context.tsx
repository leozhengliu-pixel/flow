/**
 * LS-0511 PullRequestReviewShortcutsContext — review shortcut map (list + detail + inbox).
 */
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  type ReactNode,
  type RefObject,
} from "react";

import { useLatestRef } from "@/hooks/use-latest-ref";
import {
  PullRequestReviewShortcutsContext,
  resolveReviewShortcut,
  type PullRequestReviewShortcutHandlers,
  type PullRequestReviewShortcutsContextValue,
} from "./pull-request-review-shortcuts";

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
  scope,
}: {
  children: ReactNode;
  handlers?: PullRequestReviewShortcutHandlers;
  enabled?: boolean;
  /**
   * A review embedded in another page (the inbox pane): the shortcuts listen first (capture phase) and only for keys typed
   * on the page body or inside this element, and a handled key does not reach the host page's own shortcuts.
   */
  scope?: RefObject<HTMLElement | null>;
}) {
  const registered = useRef<PullRequestReviewShortcutHandlers>({});
  const base = useLatestRef(handlers);

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
    [base],
  );

  useEffect(() => {
    if (!enabled) return;
    let prefix: string | null = null;
    let prefixTimer = 0;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.repeat) return;
      if (isEditableTarget(event.target)) return;
      if (scope && !(event.target === document.body || event.target === document.documentElement || (event.target instanceof Node && scope.current?.contains(event.target)))) return;
      // Ignore keys typed into menus and dialogs layered over the page.
      if (event.target instanceof HTMLElement && event.target.closest("[role=dialog],[role=menu],[role=listbox]")) return;
      const result = resolveReviewShortcut(event, prefix);
      window.clearTimeout(prefixTimer);
      prefix = result.prefix;
      if (prefix) {
        if (scope) event.stopPropagation();
        prefixTimer = window.setTimeout(() => (prefix = null), 1200);
        return;
      }
      if (!result.handler) return;
      const handler = resolve(result.handler);
      if (!handler) return;
      event.preventDefault();
      if (scope) event.stopPropagation();
      handler();
    };
    const capture = Boolean(scope);
    window.addEventListener("keydown", onKeyDown, capture);
    return () => {
      window.clearTimeout(prefixTimer);
      window.removeEventListener("keydown", onKeyDown, capture);
    };
  }, [enabled, resolve, scope]);

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
      openDisplayOptions: () => resolve("openDisplayOptions")?.(),
      toggleLineWrapping: () => resolve("toggleLineWrapping")?.(),
      toggleAllFiles: () => resolve("toggleAllFiles")?.(),
      markAllFilesReviewed: () => resolve("markAllFilesReviewed")?.(),
      openInProvider: () => resolve("openInProvider")?.(),
      selectReviewers: () => resolve("selectReviewers")?.(),
      toggleFavorite: () => resolve("toggleFavorite")?.(),
      openOverview: () => resolve("openOverview")?.(),
      openGuide: () => resolve("openGuide")?.(),
      openDiff: () => resolve("openDiff")?.(),
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
