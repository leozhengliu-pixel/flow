/**
 * LS-0097 BackgroundReviewProcessor — prefetch / multi-file / variant queue
 * on top of DiffComputer (REST + local ReviewFile.patch; no GraphQL).
 */
import type { CodeReview, ReviewFile } from "@/types/flow";

import {
  defaultDiffComputer,
  type ComputedFileDiff,
  type DiffComputer,
  type DiffPriority,
  type DiffVariant,
  reviewFileToInput,
} from "./diff-computer";

export type ReviewDiffKey = {
  reviewId: string;
  path: string;
  variant: DiffVariant;
};

export function diffStoreKey({ reviewId, path, variant }: ReviewDiffKey): string {
  return `${reviewId}::${path}::${variant}`;
}

export type ProcessorJobHandle = {
  id: number;
  promise: Promise<void>;
  cancel: () => void;
  setPriority: (priority: DiffPriority) => void;
};

export type PendingDiffUpdate = {
  key: string;
  diff: ComputedFileDiff;
  reviewId: string;
  path: string;
  variant: DiffVariant;
};

export type BackgroundReviewProcessorOptions = {
  computer?: DiffComputer;
  /** Max concurrent file diffs (foreground engages higher). */
  concurrency?: number;
  /** Prefer structural when original/modified content is supplied via contentResolver. */
  preferStructural?: boolean;
  contentResolver?: (args: {
    review: CodeReview;
    file: ReviewFile;
    side: "original" | "modified";
  }) => Promise<string | undefined>;
};

type QueueJob = {
  id: number;
  parentId: string;
  key: string;
  priority: DiffPriority;
  review: CodeReview;
  file: ReviewFile;
  variant: DiffVariant;
  resolve: () => void;
  reject: (error: unknown) => void;
  cancelled: boolean;
  controller: AbortController;
};

/**
 * Prefetch and multi-file variant queue. Stores computed diffs so DiffView can
 * read ready results without re-parsing patches on every render.
 */
export class BackgroundReviewProcessor {
  private static instance: BackgroundReviewProcessor | null = null;

  static initialize(options: BackgroundReviewProcessorOptions = {}): BackgroundReviewProcessor {
    BackgroundReviewProcessor.instance = new BackgroundReviewProcessor(options);
    return BackgroundReviewProcessor.instance;
  }

  static getInstance(): BackgroundReviewProcessor {
    if (!BackgroundReviewProcessor.instance) {
      throw new Error("BackgroundReviewProcessor must be initialized before use.");
    }
    return BackgroundReviewProcessor.instance;
  }

  static resetForTests() {
    BackgroundReviewProcessor.instance?.dispose();
    BackgroundReviewProcessor.instance = null;
  }

  private computer: DiffComputer;
  private concurrency: number;
  private preferStructural: boolean;
  private contentResolver?: BackgroundReviewProcessorOptions["contentResolver"];
  private store = new Map<string, ComputedFileDiff>();
  private pendingUpdates: PendingDiffUpdate[] = [];
  private listeners = new Set<() => void>();
  private pendingJobs: QueueJob[] = [];
  private active = 0;
  private lastId = 0;
  private processedReviewIds = new Set<string>();
  private inFlightFile = new Map<string, Promise<void>>();
  private disposed = false;
  private engagedReviewId: string | null = null;

  constructor(options: BackgroundReviewProcessorOptions = {}) {
    this.computer = options.computer ?? defaultDiffComputer;
    this.concurrency = Math.max(1, options.concurrency ?? 2);
    this.preferStructural = options.preferStructural ?? true;
    this.contentResolver = options.contentResolver;
  }

  dispose() {
    this.disposed = true;
    for (const job of this.pendingJobs) {
      job.cancelled = true;
      job.controller.abort();
      job.resolve();
    }
    this.pendingJobs = [];
    this.listeners.clear();
    this.store.clear();
    this.pendingUpdates = [];
  }

  subscribe(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  private emit() {
    for (const listener of this.listeners) listener();
  }

  engage(reviewId: string) {
    this.engagedReviewId = reviewId;
    // Raise priority of jobs for the engaged review.
    for (const job of this.pendingJobs) {
      if (job.parentId === reviewId) job.priority = "high";
    }
    this.schedule();
  }

  disengage(reviewId?: string) {
    if (!reviewId || this.engagedReviewId === reviewId) this.engagedReviewId = null;
  }

  getComputedDiff(reviewId: string, path: string, variant: DiffVariant = "basic"): ComputedFileDiff | undefined {
    return this.store.get(diffStoreKey({ reviewId, path, variant }));
  }

  getReviewDiffs(reviewId: string): ComputedFileDiff[] {
    const prefix = `${reviewId}::`;
    return [...this.store.entries()].filter(([key]) => key.startsWith(prefix)).map(([, diff]) => diff);
  }

  areAllDiffsReady(review: CodeReview, variant: DiffVariant = "basic"): boolean {
    if (!review.files.length) return true;
    return review.files.every((file) => this.getComputedDiff(review.id, file.path, variant)?.state === "ready");
  }

  applyPendingDiffUpdate(update: PendingDiffUpdate, notify = true) {
    this.store.set(update.key, update.diff);
    this.pendingUpdates = this.pendingUpdates.filter((item) => item.key !== update.key);
    if (notify) this.emit();
  }

  flushDiffUpdates() {
    const batch = this.pendingUpdates.splice(0, this.pendingUpdates.length);
    for (const update of batch) this.store.set(update.key, update.diff);
    if (batch.length) this.emit();
    return batch;
  }

  /** Prefetch basic (and structural when contents resolve) for every file. */
  prefetchReview(review: CodeReview, options?: { priority?: DiffPriority; variants?: DiffVariant[] }): ProcessorJobHandle {
    const priority =
      options?.priority ?? (this.engagedReviewId === review.id ? "high" : "low");
    const variants = options?.variants ?? (["basic", "structural"] as DiffVariant[]);
    const childHandles: ProcessorJobHandle[] = [];
    for (const file of review.files) {
      for (const variant of variants) {
        if (variant === "structural" && !this.preferStructural) continue;
        childHandles.push(this.enqueueFile(review, file, variant, priority));
      }
    }
    this.processedReviewIds.add(review.id);
    const promise = Promise.all(childHandles.map((h) => h.promise)).then(() => undefined);
    return {
      id: ++this.lastId,
      promise,
      cancel: () => childHandles.forEach((h) => h.cancel()),
      setPriority: (next) => childHandles.forEach((h) => h.setPriority(next)),
    };
  }

  ensureFileProcessed(
    review: CodeReview,
    file: ReviewFile,
    variant: DiffVariant = "basic",
  ): Promise<ComputedFileDiff> {
    const key = diffStoreKey({ reviewId: review.id, path: file.path, variant });
    const existing = this.store.get(key);
    if (existing?.state === "ready") return Promise.resolve(existing);
    const handle = this.enqueueFile(
      review,
      file,
      variant,
      this.engagedReviewId === review.id ? "high" : "low",
    );
    return handle.promise.then(() => {
      const ready = this.store.get(key);
      if (ready) return ready;
      // Fallback sync compute if queue was cancelled mid-flight.
      return this.computer.computeSync(reviewFileToInput(file), variant);
    });
  }

  getDebugSnapshot() {
    return {
      enabled: !this.disposed,
      engagedReviewId: this.engagedReviewId,
      pendingJobs: this.pendingJobs.length,
      active: this.active,
      storeSize: this.store.size,
      processedPullRequestIds: [...this.processedReviewIds],
      computer: this.computer.getDebugState(),
    };
  }

  private enqueueFile(
    review: CodeReview,
    file: ReviewFile,
    variant: DiffVariant,
    priority: DiffPriority,
  ): ProcessorJobHandle {
    const key = diffStoreKey({ reviewId: review.id, path: file.path, variant });
    const inflight = this.inFlightFile.get(key);
    if (inflight) {
      const id = ++this.lastId;
      return {
        id,
        promise: inflight,
        cancel: () => {
          const job = this.pendingJobs.find((item) => item.key === key);
          if (job) {
            job.cancelled = true;
            job.controller.abort();
          }
        },
        setPriority: (next) => {
          const job = this.pendingJobs.find((item) => item.key === key);
          if (job) job.priority = next;
          this.schedule();
        },
      };
    }

    // Coalesce duplicate pending jobs with same key.
    const existingJob = this.pendingJobs.find((item) => item.key === key);
    if (existingJob) {
      if (priority === "high") existingJob.priority = "high";
      return {
        id: existingJob.id,
        promise: new Promise((resolve, reject) => {
          const prevResolve = existingJob.resolve;
          const prevReject = existingJob.reject;
          existingJob.resolve = () => {
            prevResolve();
            resolve();
          };
          existingJob.reject = (error) => {
            prevReject(error);
            reject(error);
          };
        }),
        cancel: () => {
          existingJob.cancelled = true;
          existingJob.controller.abort();
        },
        setPriority: (next) => {
          existingJob.priority = next;
          this.schedule();
        },
      };
    }

    const id = ++this.lastId;
    let settle!: () => void;
    let fail!: (error: unknown) => void;
    const promise = new Promise<void>((resolve, reject) => {
      settle = resolve;
      fail = reject;
    });
    this.inFlightFile.set(key, promise.finally(() => this.inFlightFile.delete(key)));

    const job: QueueJob = {
      id,
      parentId: review.id,
      key,
      priority,
      review,
      file,
      variant,
      resolve: settle,
      reject: fail,
      cancelled: false,
      controller: new AbortController(),
    };
    this.pendingJobs.push(job);
    this.schedule();
    return {
      id,
      promise,
      cancel: () => {
        job.cancelled = true;
        job.controller.abort();
        this.pendingJobs = this.pendingJobs.filter((item) => item.id !== job.id);
        settle();
      },
      setPriority: (next) => {
        job.priority = next;
        this.schedule();
      },
    };
  }

  private schedule() {
    if (this.disposed) return;
    const limit = this.engagedReviewId ? this.concurrency + 1 : this.concurrency;
    while (this.active < limit) {
      const index = this.bestPendingIndex();
      if (index < 0) return;
      const [job] = this.pendingJobs.splice(index, 1);
      if (!job || job.cancelled) continue;
      this.active += 1;
      void this.runJob(job).finally(() => {
        this.active -= 1;
        this.schedule();
      });
    }
  }

  private bestPendingIndex(): number {
    if (!this.pendingJobs.length) return -1;
    let best = 0;
    for (let i = 1; i < this.pendingJobs.length; i++) {
      const job = this.pendingJobs[i];
      const cur = this.pendingJobs[best];
      const jobScore =
        (job.priority === "high" ? 2 : 0) + (job.parentId === this.engagedReviewId ? 1 : 0);
      const curScore =
        (cur.priority === "high" ? 2 : 0) + (cur.parentId === this.engagedReviewId ? 1 : 0);
      if (jobScore > curScore) best = i;
    }
    return best;
  }

  private async runJob(job: QueueJob) {
    const { review, file, variant, key } = job;
    try {
      let original: string | undefined;
      let modified: string | undefined;
      if (variant === "structural" && this.contentResolver) {
        try {
          ;[original, modified] = await Promise.all([
            this.contentResolver({ review, file, side: "original" }),
            this.contentResolver({ review, file, side: "modified" }),
          ]);
        } catch {
          // Fall back to patch-only structural enrichment.
        }
      }

      const input = {
        ...reviewFileToInput(file, { original, modified }),
        cacheKey: key,
      };

      // Seed pending into store for honest loading UI.
      this.queueUpdate({
        key,
        reviewId: review.id,
        path: file.path,
        variant,
        diff: {
          key,
          filename: file.path,
          variant,
          state: "computing",
          language: "Text",
          hunks: [],
          lines: [],
          splitRows: [],
          commentableLines: { original: [], modified: [] },
          additions: 0,
          deletions: 0,
          computedAt: Date.now(),
        },
      });

      let latest: ComputedFileDiff | undefined;
      for await (const snapshot of this.computer.computeFileDiff(input, {
        priority: job.priority,
        signal: job.controller.signal,
        variant,
        debugMeta: { reviewId: review.id, path: file.path },
      })) {
        if (job.cancelled) break;
        latest = snapshot;
        this.queueUpdate({
          key,
          reviewId: review.id,
          path: file.path,
          variant,
          diff: snapshot,
        });
      }

      if (!latest && !job.cancelled) {
        latest = this.computer.computeSync(input, variant);
        this.applyPendingDiffUpdate(
          { key, reviewId: review.id, path: file.path, variant, diff: latest },
          true,
        );
      } else {
        this.flushDiffUpdates();
      }
      job.resolve();
    } catch (error) {
      this.applyPendingDiffUpdate(
        {
          key,
          reviewId: review.id,
          path: file.path,
          variant,
          diff: {
            key,
            filename: file.path,
            variant,
            state: "error",
            language: "Text",
            hunks: [],
            lines: [],
            splitRows: [],
            commentableLines: { original: [], modified: [] },
            additions: 0,
            deletions: 0,
            error: error instanceof Error ? error.message : "compute failed",
            computedAt: Date.now(),
          },
        },
        true,
      );
      job.resolve();
    }
  }

  private queueUpdate(update: PendingDiffUpdate) {
    const index = this.pendingUpdates.findIndex((item) => item.key === update.key);
    if (index >= 0) this.pendingUpdates[index] = update;
    else this.pendingUpdates.push(update);
    // Apply immediately for DiffView responsiveness (Linear batches; we flush per tick).
    this.applyPendingDiffUpdate(update, true);
  }
}

/** Ensure singleton exists for app/runtime use. */
export function ensureBackgroundReviewProcessor(
  options?: BackgroundReviewProcessorOptions,
): BackgroundReviewProcessor {
  try {
    return BackgroundReviewProcessor.getInstance();
  } catch {
    return BackgroundReviewProcessor.initialize(options);
  }
}
