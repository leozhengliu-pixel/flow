/**
 * LS-0535 ReviewCommentNavigation — anchored comment hash deep-links.
 */
import type { ReviewEvent } from "@/types/flow";

export const COMMENT_HASH_PREFIX = "comment-";

export function targetCommentHash(commentId: string): string {
  return `${COMMENT_HASH_PREFIX}${commentId}`;
}

export function commentIdFromHash(hash: string | undefined | null): string | undefined {
  if (!hash) return undefined;
  const value = hash.startsWith("#") ? hash.slice(1) : hash;
  if (!value.startsWith(COMMENT_HASH_PREFIX)) return undefined;
  const id = value.slice(COMMENT_HASH_PREFIX.length);
  return id || undefined;
}

export function isCommentIdInHash(hash: string | undefined | null, commentId: string): boolean {
  return commentIdFromHash(hash) === commentId;
}

export function getAnchoredCommentEvent(
  events: ReviewEvent[],
  hash: string | undefined | null,
): ReviewEvent | undefined {
  const id = commentIdFromHash(hash);
  if (!id) return undefined;
  return events.find((event) => event.id === id);
}

export function getAnchoredCommentFilePath(
  events: ReviewEvent[],
  hash: string | undefined | null,
): string | undefined {
  return getAnchoredCommentEvent(events, hash)?.path;
}

export type AnchoredCommentRedirectInput = {
  reviewPath: string;
  events: ReviewEvent[];
  targetCommentHash: string;
  search?: string;
};

/** Prefer Diff tab when the anchored comment has a file path; else Overview. */
export function getAnchoredCommentRedirectPath({
  reviewPath,
  events,
  targetCommentHash: hash,
  search,
}: AnchoredCommentRedirectInput): string {
  const filePath = getAnchoredCommentFilePath(events, hash);
  const base = filePath
    ? reviewPath.replace(/\/(overview|review|changes)?\/?$/, "/changes")
    : reviewPath.replace(/\/(overview|review|changes)?\/?$/, "/overview");
  const withSearch = search ? `${base}${search.startsWith("?") ? search : `?${search}`}` : base;
  return `${withSearch}#${hash.startsWith("#") ? hash.slice(1) : hash}`;
}

export function scrollToAnchoredComment(
  hash: string | undefined | null,
  root: ParentNode | Document = document,
): boolean {
  const id = commentIdFromHash(hash);
  if (!id) return false;
  const el =
    root.querySelector<HTMLElement>(`[data-review-comment-id="${CSS.escape(id)}"]`) ??
    root.querySelector<HTMLElement>(`#${CSS.escape(targetCommentHash(id))}`);
  if (!el) return false;
  el.scrollIntoView({ behavior: "smooth", block: "center" });
  el.setAttribute("data-anchored-active", "true");
  return true;
}
