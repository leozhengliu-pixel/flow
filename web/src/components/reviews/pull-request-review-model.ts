import type { DiffLine } from "@/lib/diff-computer";
import type { CodeReview, ReviewCommentSide, ReviewFile } from "@/types/flow";

import type { MergeMethod } from "./pull-request-merge-method";
import { reviewReadyToMerge } from "./review-list-model";

export type ReviewDecision = "approve" | "comment" | "requestChanges";

export const MERGE_LABELS: Record<MergeMethod, string> = {
  squash: "Squash & merge",
  merge: "Merge",
  rebase: "Rebase & merge",
};

/** What the primary action of the pull request page is for the viewer. */
export function reviewPrimaryAction(review: CodeReview, viewerId: string) {
  const closed = review.status === "merged" || review.status === "closed";
  const author = review.author.id === viewerId;
  const readyToMerge = !closed && reviewReadyToMerge(review);
  // Authors merge as soon as the branch is mergeable; other people once it is approved.
  const mergeable = readyToMerge && (author || review.status === "approved");
  if (closed) return { kind: "none" as const, mergeable: false };
  if (author) {
    if (review.draft) return { kind: review.reviewerIds.length ? ("markReady" as const) : ("readyForReview" as const), mergeable };
    if (mergeable) return { kind: "merge" as const, mergeable };
    if (!review.reviewerIds.length && !review.teamReviewers.length) return { kind: "requestReview" as const, mergeable };
    return { kind: "none" as const, mergeable };
  }
  const asked = review.reviewerIds.includes(viewerId);
  const reviewed = review.events.some((event) => event.actor.id === viewerId && ["approved", "changes_requested", "review_commented"].includes(event.type));
  return { kind: reviewed && !asked ? ("reviewAgain" as const) : ("submitReview" as const), primary: asked, mergeable };
}

const DOC_FILE = /\.(md|mdx|markdown|txt|rst|adoc)$/i;

/** True when every changed file is documentation (header shows "Docs only" instead of a diff stat). */
export function reviewDocsOnly(review: CodeReview) {
  return review.files.length > 0 && review.files.every((file) => DOC_FILE.test(file.path));
}

/** Sidebar/summary text for checks, mirroring the provider: failures by name, pending counts, or "All passed". */
export function reviewChecksSummary(checks: CodeReview["checks"]): { tone: "success" | "danger" | "muted"; text: string; failed: string[] } | null {
  if (!checks.length) return null;
  const failed = checks.filter((check) => check.status === "failed").map((check) => check.name);
  const pending = checks.filter((check) => check.status === "pending" || check.status === "running");
  const pendingSuffix = pending.length ? ` · ${pending.length} pending` : "";
  if (failed.length) {
    const names = failed.length <= 3 ? failed.join(", ") : `${failed.slice(0, 2).join(", ")}, and ${failed.length - 2} more`;
    return { tone: "danger", text: names + pendingSuffix, failed };
  }
  if (pending.length) return { tone: "muted", text: pending.length === 1 ? `${pending[0].name} pending` : `${pending.length} checks pending`, failed };
  const passed = checks.filter((check) => check.status === "passed").length;
  if (passed === checks.length) return { tone: "success", text: "All passed", failed };
  const skipped = checks.length - passed;
  return { tone: "muted", text: [passed && `${passed} passed`, skipped && `${skipped} skipped`].filter(Boolean).join(" · "), failed };
}

const TEST_FILE = /(^|\/)(__tests__|tests?|spec)\/|[._-](test|spec)\.[a-z0-9]+$/i;

/** Groups changed files the way the provider's sidebar does: implementation first, then tests. */
export function reviewFileGroups(files: ReviewFile[]) {
  const groups = [
    { id: "implementation", label: "Implementation", files: files.filter((file) => !TEST_FILE.test(file.path)) },
    { id: "tests", label: "Tests", files: files.filter((file) => TEST_FILE.test(file.path)) },
  ];
  return groups
    .filter((group) => group.files.length)
    .map((group) => ({
      ...group,
      additions: group.files.reduce((sum, file) => sum + file.additions, 0),
      deletions: group.files.reduce((sum, file) => sum + file.deletions, 0),
    }));
}

/** "math.js" and "src" for "src/math.js". */
export function splitReviewPath(path: string) {
  const index = path.lastIndexOf("/");
  return index < 0 ? { name: path, directory: "" } : { name: path.slice(index + 1), directory: path.slice(0, index) };
}

/** Splits a comment body into markdown text and ```suggestion blocks (the replacement lines). */
export function splitSuggestionBody(body: string) {
  const parts: ({ kind: "text"; text: string } | { kind: "suggestion"; lines: string[] })[] = [];
  const pattern = /```suggestion[^\n]*\n([\s\S]*?)```/g;
  let last = 0;
  for (const match of body.matchAll(pattern)) {
    const before = body.slice(last, match.index).trim();
    if (before) parts.push({ kind: "text", text: before });
    parts.push({ kind: "suggestion", lines: match[1].replace(/\n$/, "").split("\n") });
    last = (match.index ?? 0) + match[0].length;
  }
  const rest = body.slice(last).trim();
  if (rest) parts.push({ kind: "text", text: rest });
  return parts;
}

export type ReviewThreadModel = { root: CodeReview["events"][number]; replies: CodeReview["events"][number][]; resolved: boolean };

/** Thread map key of a code location: "path:SIDE:line" (RIGHT when the side is unknown). */
export function reviewThreadKey(path: string, side: ReviewCommentSide | undefined, line: number) {
  return `${path}:${side ?? "RIGHT"}:${line}`;
}

/** Code comment threads (root + replies) keyed by reviewThreadKey (path, diff side, line). */
export function reviewCodeThreads(review: CodeReview) {
  const comments = review.events.filter((event) => event.path && event.line && (event.type === "commented" || event.type === "review_commented"));
  const roots = comments.filter((event) => !event.parentId || !comments.some((item) => item.id === event.parentId));
  const threads = new Map<string, ReviewThreadModel[]>();
  for (const root of roots) {
    const replies = comments.filter((event) => event.parentId === root.id);
    const key = reviewThreadKey(root.path ?? "", root.side, root.line ?? 0);
    threads.set(key, [...(threads.get(key) ?? []), { root, replies, resolved: Boolean(root.resolved) }]);
  }
  return threads;
}

/** Splits two strings into a shared prefix, the differing middles and a shared suffix (rename highlights). */
export function changedSegments(before: string, after: string) {
  let start = 0;
  while (start < before.length && start < after.length && before[start] === after[start]) start += 1;
  let end = 0;
  while (end < before.length - start && end < after.length - start && before[before.length - 1 - end] === after[after.length - 1 - end]) end += 1;
  return {
    prefix: before.slice(0, start),
    before: before.slice(start, before.length - end),
    after: after.slice(start, after.length - end),
    suffix: before.slice(before.length - end),
  };
}

/** Header diff stat: like the provider, test files are left out of the +/- totals. */
export function reviewHeaderStat(review: CodeReview) {
  if (!review.files.length) return { additions: review.additions, deletions: review.deletions };
  const implementation = reviewFileGroups(review.files).find((group) => group.id === "implementation");
  return { additions: implementation?.additions ?? 0, deletions: implementation?.deletions ?? 0 };
}

/** The comment anchor of a diff cell: removed lines and the old column comment on the LEFT (old) side. */
export function diffCommentAnchor(line: DiffLine | undefined, column: "old" | "new" | "unified"): { line: number; side: ReviewCommentSide } | null {
  if (!line) return null;
  if (column === "old" || (column === "unified" && line.kind === "remove")) return line.oldNumber ? { line: line.oldNumber, side: "LEFT" } : null;
  if (column === "new" && line.kind === "remove") return null;
  return line.newNumber ? { line: line.newNumber, side: "RIGHT" } : null;
}
