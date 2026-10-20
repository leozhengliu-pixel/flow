/**
 * LS-0511 PullRequestReviewShortcutsContext — review shortcut map (list + detail + inbox).
 */
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  type ReactNode,
} from "react";

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
    let prefix: string | null = null;
    let prefixTimer = 0;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.repeat) return;
      if (isEditableTarget(event.target)) return;
      // Ignore keys typed into menus and dialogs layered over the page.
      if (event.target instanceof HTMLElement && event.target.closest("[role=dialog],[role=menu],[role=listbox]")) return;
      const result = resolveReviewShortcut(event, prefix);
      window.clearTimeout(prefixTimer);
      prefix = result.prefix;
      if (prefix) {
        prefixTimer = window.setTimeout(() => (prefix = null), 1200);
        return;
      }
      if (!result.handler) return;
      const handler = resolve(result.handler);
      if (!handler) return;
      event.preventDefault();
      handler();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => {
      window.clearTimeout(prefixTimer);
      window.removeEventListener("keydown", onKeyDown);
    };
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
