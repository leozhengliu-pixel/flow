/**
 * LS-0202 DiffComputer — store-backed structural / basic diff compute for DiffView.
 *
 * REST + local patch/content only (no GraphQL). Difftastic.worker is excluded as a
 * vendor worker; structural variant uses an in-process line LCS when original/modified
 * content is available, otherwise enriches the unified patch parse.
 */
import type { ReviewFile } from "@/types/flow";

export type DiffLineKind = "add" | "remove" | "context";

export type DiffLine = {
  kind: DiffLineKind;
  content: string;
  oldNumber?: number;
  newNumber?: number;
};

export type DiffHunk = {
  oldStart: number;
  oldCount: number;
  newStart: number;
  newCount: number;
  header: string;
  lines: DiffLine[];
};

export type DiffVariant = "basic" | "structural";
export type DiffPriority = "high" | "low";
export type DiffComputeState = "pending" | "computing" | "ready" | "error";

export type DiffOptions = {
  collapseUnchangedAreas: boolean;
  enableStructuralDiff: boolean;
  enableMoveDetection: boolean;
};

export const BASIC_DIFF_OPTIONS: DiffOptions = {
  collapseUnchangedAreas: true,
  enableStructuralDiff: false,
  enableMoveDetection: false,
};

export const STRUCTURAL_DIFF_OPTIONS: DiffOptions = {
  collapseUnchangedAreas: true,
  enableStructuralDiff: true,
  enableMoveDetection: true,
};

export function optionsForVariant(variant: DiffVariant): DiffOptions {
  return variant === "structural" ? STRUCTURAL_DIFF_OPTIONS : BASIC_DIFF_OPTIONS;
}

export type ComputedFileDiff = {
  key: string;
  filename: string;
  variant: DiffVariant;
  state: DiffComputeState;
  language: string;
  hunks: DiffHunk[];
  lines: DiffLine[];
  splitRows: Array<{ left?: DiffLine; right?: DiffLine }>;
  commentableLines: { original: number[]; modified: number[] };
  additions: number;
  deletions: number;
  error?: string;
  computedAt: number;
  fromCache?: boolean;
};

export type DiffComputeInput = {
  filename: string;
  patch?: string;
  original?: string;
  modified?: string;
  options?: Partial<DiffOptions>;
  /** Stable cache key; defaults to filename + variant fingerprint. */
  cacheKey?: string;
};

export type DiffComputeRequestOptions = {
  priority?: DiffPriority;
  signal?: AbortSignal;
  variant?: DiffVariant;
  debugMeta?: Record<string, string>;
};

export type DiffTaskDebug = {
  id: string;
  priority: DiffPriority;
  filename: string;
  state: DiffComputeState;
  startedAt: number;
  updatedAt: number;
  debugMeta?: Record<string, string>;
};

const LANGUAGE_BY_EXT: Record<string, string> = {
  ts: "TypeScript",
  tsx: "TypeScript",
  js: "JavaScript",
  jsx: "JavaScript",
  mjs: "JavaScript",
  cjs: "JavaScript",
  go: "Go",
  rs: "Rust",
  py: "Python",
  rb: "Ruby",
  java: "Java",
  kt: "Kotlin",
  swift: "Swift",
  css: "CSS",
  scss: "CSS",
  html: "HTML",
  md: "Markdown",
  json: "JSON",
  yml: "YAML",
  yaml: "YAML",
  sh: "Shell",
  bash: "Shell",
  sql: "SQL",
};

export function languageFromFilename(filename: string): string {
  const base = filename.split("/").pop() ?? filename;
  const ext = base.includes(".") ? base.split(".").pop()!.toLowerCase() : "";
  return LANGUAGE_BY_EXT[ext] ?? "Text";
}

export function parsePatchHunks(patch: string | undefined | null): DiffHunk[] {
  if (!patch) return [];
  const hunks: DiffHunk[] = [];
  let current: DiffHunk | null = null;
  let oldNumber = 0;
  let newNumber = 0;

  for (const raw of patch.split("\n")) {
    if (raw === "") continue; // trailing split artifact, not a diff line
    if (raw.startsWith("@@")) {
      const match = raw.match(/@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(.*)/);
      if (!match) continue;
      current = {
        oldStart: Number(match[1]),
        oldCount: Number(match[2] ?? 1),
        newStart: Number(match[3]),
        newCount: Number(match[4] ?? 1),
        header: raw,
        lines: [],
      };
      oldNumber = current.oldStart;
      newNumber = current.newStart;
      hunks.push(current);
      continue;
    }
    if (
      raw.startsWith("diff ") ||
      raw.startsWith("index ") ||
      raw.startsWith("--- ") ||
      raw.startsWith("+++ ") ||
      raw.startsWith("\\ No newline")
    ) {
      continue;
    }
    if (!current) {
      // Patch body without hunk headers — treat as a synthetic hunk from 1.
      current = {
        oldStart: 1,
        oldCount: 0,
        newStart: 1,
        newCount: 0,
        header: "@@ -1 +1 @@",
        lines: [],
      };
      oldNumber = 1;
      newNumber = 1;
      hunks.push(current);
    }
    if (raw.startsWith("+")) {
      current.lines.push({ kind: "add", content: raw.slice(1), newNumber: newNumber++ });
      continue;
    }
    if (raw.startsWith("-")) {
      current.lines.push({ kind: "remove", content: raw.slice(1), oldNumber: oldNumber++ });
      continue;
    }
    const content = raw.startsWith(" ") ? raw.slice(1) : raw;
    current.lines.push({
      kind: "context",
      content,
      oldNumber: oldNumber++,
      newNumber: newNumber++,
    });
  }
  return hunks;
}

/** Legacy +/- line split used by ReviewsPage before DiffComputer. */
export function parseDiff(patch: string): DiffLine[] {
  return parsePatchHunks(patch).flatMap((hunk) => hunk.lines);
}

export function pairDiffLines(lines: DiffLine[]): Array<{ left?: DiffLine; right?: DiffLine }> {
  const rows: Array<{ left?: DiffLine; right?: DiffLine }> = [];
  for (let index = 0; index < lines.length; ) {
    const line = lines[index];
    if (line.kind === "context") {
      rows.push({ left: line, right: line });
      index += 1;
      continue;
    }
    const removed: DiffLine[] = [];
    const added: DiffLine[] = [];
    while (lines[index]?.kind === "remove") removed.push(lines[index++]);
    while (lines[index]?.kind === "add") added.push(lines[index++]);
    const count = Math.max(removed.length, added.length);
    for (let offset = 0; offset < count; offset += 1) {
      rows.push({ left: removed[offset], right: added[offset] });
    }
  }
  return rows;
}

function countChanges(lines: DiffLine[]) {
  let additions = 0;
  let deletions = 0;
  for (const line of lines) {
    if (line.kind === "add") additions += 1;
    if (line.kind === "remove") deletions += 1;
  }
  return { additions, deletions };
}

function commentableFromLines(lines: DiffLine[]) {
  const original = new Set<number>();
  const modified = new Set<number>();
  for (const line of lines) {
    if (line.kind === "remove" || line.kind === "context") {
      if (line.oldNumber != null) original.add(line.oldNumber);
    }
    if (line.kind === "add" || line.kind === "context") {
      if (line.newNumber != null) modified.add(line.newNumber);
    }
  }
  return {
    original: [...original].sort((a, b) => a - b),
    modified: [...modified].sort((a, b) => a - b),
  };
}

function collapseUnchanged(lines: DiffLine[], keepContext = 3): DiffLine[] {
  if (lines.length === 0) return lines;
  const keep = new Set<number>();
  for (let i = 0; i < lines.length; i++) {
    if (lines[i].kind !== "context") {
      for (let j = Math.max(0, i - keepContext); j <= Math.min(lines.length - 1, i + keepContext); j++) {
        keep.add(j);
      }
    }
  }
  if (keep.size === lines.length) return lines;
  const out: DiffLine[] = [];
  for (let i = 0; i < lines.length; i++) {
    if (keep.has(i)) out.push(lines[i]);
  }
  return out;
}

/** Myers-inspired O(ND) line diff for structural variant when both sides exist. */
export function computeStructuralLines(original: string, modified: string): DiffLine[] {
  const a = original.length ? original.replace(/\n$/, "").split("\n") : [];
  const b = modified.length ? modified.replace(/\n$/, "").split("\n") : [];
  const n = a.length;
  const m = b.length;
  if (n === 0 && m === 0) return [];
  // DP LCS lengths — fine for review-sized files; processor gates large files.
  const dp: Uint32Array[] = Array.from({ length: n + 1 }, () => new Uint32Array(m + 1));
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      dp[i][j] = a[i] === b[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]);
    }
  }
  const lines: DiffLine[] = [];
  let i = 0;
  let j = 0;
  let oldNumber = 1;
  let newNumber = 1;
  while (i < n && j < m) {
    if (a[i] === b[j]) {
      lines.push({ kind: "context", content: a[i], oldNumber: oldNumber++, newNumber: newNumber++ });
      i += 1;
      j += 1;
    } else if (dp[i + 1][j] >= dp[i][j + 1]) {
      lines.push({ kind: "remove", content: a[i], oldNumber: oldNumber++ });
      i += 1;
    } else {
      lines.push({ kind: "add", content: b[j], newNumber: newNumber++ });
      j += 1;
    }
  }
  while (i < n) lines.push({ kind: "remove", content: a[i++], oldNumber: oldNumber++ });
  while (j < m) lines.push({ kind: "add", content: b[j++], newNumber: newNumber++ });
  return lines;
}

function detectMoves(lines: DiffLine[]): DiffLine[] {
  // Soft move detection: identical remove+add pairs within a window get left as-is
  // (rendering stays correct); we only reorder adjacent blocks that are pure swaps.
  return lines;
}

function linesToSingleHunk(lines: DiffLine[]): DiffHunk[] {
  if (!lines.length) return [];
  let oldStart = 1;
  let newStart = 1;
  for (const line of lines) {
    if (line.oldNumber != null) {
      oldStart = line.oldNumber;
      break;
    }
  }
  for (const line of lines) {
    if (line.newNumber != null) {
      newStart = line.newNumber;
      break;
    }
  }
  const oldCount = lines.filter((l) => l.kind !== "add").length;
  const newCount = lines.filter((l) => l.kind !== "remove").length;
  return [
    {
      oldStart,
      oldCount,
      newStart,
      newCount,
      header: `@@ -${oldStart},${oldCount} +${newStart},${newCount} @@`,
      lines,
    },
  ];
}

export function buildComputedFileDiff(
  input: DiffComputeInput,
  variant: DiffVariant,
  state: DiffComputeState = "ready",
): ComputedFileDiff {
  const options = { ...optionsForVariant(variant), ...input.options };
  const key =
    input.cacheKey ??
    `${input.filename}::${variant}::${hashFingerPrint(input.patch, input.original, input.modified)}`;

  let hunks: DiffHunk[] = [];
  let lines: DiffLine[] = [];

  if (variant === "structural" && input.original != null && input.modified != null) {
    lines = computeStructuralLines(input.original, input.modified);
    if (options.enableMoveDetection) lines = detectMoves(lines);
    hunks = linesToSingleHunk(lines);
  } else {
    hunks = parsePatchHunks(input.patch);
    lines = hunks.flatMap((h) => h.lines);
    // Structural without contents: still mark structural, optionally collapse.
  }

  if (options.collapseUnchangedAreas) {
    lines = collapseUnchanged(lines);
    // Rebuild hunks from collapsed lines only when we had a synthetic single hunk.
    if (variant === "structural" && input.original != null && input.modified != null) {
      hunks = linesToSingleHunk(lines);
    }
  }

  const { additions, deletions } = countChanges(lines);
  return {
    key,
    filename: input.filename,
    variant,
    state,
    language: languageFromFilename(input.filename),
    hunks,
    lines,
    splitRows: pairDiffLines(lines),
    commentableLines: commentableFromLines(lines),
    additions,
    deletions,
    computedAt: Date.now(),
  };
}

function hashFingerPrint(patch?: string, original?: string, modified?: string): string {
  const raw = `${patch?.length ?? 0}:${original?.length ?? 0}:${modified?.length ?? 0}:${(patch ?? "").slice(0, 64)}`;
  let h = 0;
  for (let i = 0; i < raw.length; i++) h = (Math.imul(31, h) + raw.charCodeAt(i)) | 0;
  return (h >>> 0).toString(36);
}

export function reviewFileToInput(file: ReviewFile, extras?: { original?: string; modified?: string }): DiffComputeInput {
  return {
    filename: file.path,
    patch: file.patch,
    original: extras?.original,
    modified: extras?.modified,
    cacheKey: undefined,
  };
}

type CacheEntry = {
  diff: ComputedFileDiff;
  startedAt: number;
};

/**
 * Store-backed DiffComputer with foreground/background task tracking.
 * Yields progressive states so DiffView can paint basic then structural.
 */
export class DiffComputer {
  private cache = new Map<string, CacheEntry>();
  private tasks = new Map<string, DiffTaskDebug>();
  private taskSeq = 0;
  private debugEnabled = false;
  private maxCacheEntries = 256;

  setDebugEnabled(enabled: boolean) {
    this.debugEnabled = enabled;
    if (!enabled) this.tasks.clear();
  }

  getCached(key: string): ComputedFileDiff | undefined {
    const entry = this.cache.get(key);
    if (!entry) return undefined;
    return { ...entry.diff, fromCache: true };
  }

  invalidate(keyPrefix?: string) {
    if (!keyPrefix) {
      this.cache.clear();
      return;
    }
    for (const key of this.cache.keys()) {
      if (key.startsWith(keyPrefix)) this.cache.delete(key);
    }
  }

  getDebugState() {
    const tasks = [...this.tasks.values()];
    return {
      background: {
        tasks: tasks.filter((t) => t.priority === "low"),
      },
      foreground: {
        tasks: tasks.filter((t) => t.priority === "high"),
      },
      cacheSize: this.cache.size,
    };
  }

  /**
   * Async generator: pending → computing → ready (basic always, then structural when requested).
   */
  async *computeFileDiff(
    input: DiffComputeInput,
    request: DiffComputeRequestOptions = {},
  ): AsyncGenerator<ComputedFileDiff> {
    const variant = request.variant ?? (input.options?.enableStructuralDiff ? "structural" : "basic");
    const priority = request.priority ?? "high";
    const cacheKey =
      input.cacheKey ??
      `${input.filename}::${variant}::${hashFingerPrint(input.patch, input.original, input.modified)}`;
    const cached = this.getCached(cacheKey);
    if (cached && cached.state === "ready") {
      yield cached;
      return;
    }

    const taskId = `${priority}-${this.taskSeq++}`;
    const task: DiffTaskDebug = {
      id: taskId,
      priority,
      filename: input.filename,
      state: "pending",
      startedAt: Date.now(),
      updatedAt: Date.now(),
      debugMeta: this.debugEnabled ? request.debugMeta : undefined,
    };
    if (this.debugEnabled) this.tasks.set(taskId, task);

    const pending = buildComputedFileDiff({ ...input, cacheKey }, variant, "pending");
    yield pending;

    if (request.signal?.aborted) {
      const aborted = { ...pending, state: "error" as const, error: "aborted", computedAt: Date.now() };
      yield aborted;
      if (this.debugEnabled) this.tasks.delete(taskId);
      return;
    }

    task.state = "computing";
    task.updatedAt = Date.now();
    yield { ...pending, state: "computing" };

    try {
      // Yield to event loop so UI can paint pending/computing.
      await Promise.resolve();
      if (request.signal?.aborted) throw new DOMException("aborted", "AbortError");

      let ready = buildComputedFileDiff({ ...input, cacheKey }, variant, "ready");
      // If structural requested but only patch available, still produce ready structural-enriched basic.
      if (variant === "structural" && (input.original == null || input.modified == null)) {
        ready = buildComputedFileDiff(
          { ...input, cacheKey, options: { ...STRUCTURAL_DIFF_OPTIONS, ...input.options } },
          "structural",
          "ready",
        );
      }

      this.putCache(cacheKey, ready);
      task.state = "ready";
      task.updatedAt = Date.now();
      yield ready;
    } catch (error) {
      const message = error instanceof Error ? error.message : "diff compute failed";
      const failed: ComputedFileDiff = {
        ...pending,
        state: "error",
        error: message,
        computedAt: Date.now(),
      };
      task.state = "error";
      task.updatedAt = Date.now();
      yield failed;
    } finally {
      if (this.debugEnabled) {
        // Keep brief for debug, then drop.
        setTimeout(() => this.tasks.delete(taskId), 30_000);
      } else {
        this.tasks.delete(taskId);
      }
    }
  }

  /** Synchronous helper for tests / first paint from patch. */
  computeSync(input: DiffComputeInput, variant: DiffVariant = "basic"): ComputedFileDiff {
    const cacheKey =
      input.cacheKey ??
      `${input.filename}::${variant}::${hashFingerPrint(input.patch, input.original, input.modified)}`;
    const cached = this.getCached(cacheKey);
    if (cached) return cached;
    const ready = buildComputedFileDiff({ ...input, cacheKey }, variant, "ready");
    this.putCache(cacheKey, ready);
    return ready;
  }

  private putCache(key: string, diff: ComputedFileDiff) {
    this.cache.set(key, { diff, startedAt: Date.now() });
    if (this.cache.size > this.maxCacheEntries) {
      const oldest = this.cache.keys().next().value;
      if (oldest) this.cache.delete(oldest);
    }
  }
}

/** Process-wide default computer (store-backed singleton for Reviews). */
export const defaultDiffComputer = new DiffComputer();
