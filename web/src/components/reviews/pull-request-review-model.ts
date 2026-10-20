import type { CodeReview, ReviewFile } from "@/types/flow";

import type { MergeMethod } from "./pull-request-merge-method";

export type ReviewDecision = "approve" | "comment" | "requestChanges";

export const MERGE_LABELS: Record<MergeMethod, string> = {
  squash: "Squash and merge",
  merge: "Create a merge commit",
  rebase: "Rebase and merge",
};

/** What the primary action of the pull request page is for the viewer. */
export function reviewPrimaryAction(review: CodeReview, viewerId: string) {
  const closed = review.status === "merged" || review.status === "closed";
  const author = review.author.id === viewerId;
  const passed = review.checks.every((check) => check.status === "passed" || check.status === "skipped");
  const mergeable = !closed && !review.draft && review.status === "approved" && passed && review.branchState !== "conflicted";
  if (closed) return { kind: "none" as const, mergeable: false };
  if (author) {
    if (review.draft) return { kind: review.reviewerIds.length ? ("markReady" as const) : ("readyForReview" as const), mergeable };
    if (!review.reviewerIds.length && !review.teamReviewers.length) return { kind: "requestReview" as const, mergeable };
    return { kind: "none" as const, mergeable };
  }
  const asked = review.reviewerIds.includes(viewerId);
  const reviewed = review.events.some((event) => event.actor.id === viewerId && ["approved", "changes_requested", "review_commented"].includes(event.type));
  return { kind: reviewed && !asked ? ("reviewAgain" as const) : ("submitReview" as const), primary: asked, mergeable };
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

/** Code comment threads (root + replies) keyed by "path:line". */
export function reviewCodeThreads(review: CodeReview) {
  const comments = review.events.filter((event) => event.path && event.line && (event.type === "commented" || event.type === "review_commented"));
  const roots = comments.filter((event) => !event.parentId || !comments.some((item) => item.id === event.parentId));
  const threads = new Map<string, ReviewThreadModel[]>();
  for (const root of roots) {
    const replies = comments.filter((event) => event.parentId === root.id);
    const key = `${root.path}:${root.line}`;
    threads.set(key, [...(threads.get(key) ?? []), { root, replies, resolved: Boolean(root.resolved) }]);
  }
  return threads;
}
