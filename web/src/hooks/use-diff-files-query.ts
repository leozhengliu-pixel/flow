/**
 * LS-0728 useDiffFilesQuery — Diff files query pack over BackgroundReviewProcessor.
 *
 * REST + local ReviewFile.patch (no GraphQL). Includes PDF / binary stubs and
 * agent-context selection helpers used by DiffView / EditableDiffView.
 */
import { useEffect, useMemo, useState, useSyncExternalStore } from "react";

import {
  ensureBackgroundReviewProcessor,
  type BackgroundReviewProcessor,
} from "@/lib/background-review-processor";
import { hasParseableDiff, parseDiffFile } from "@/lib/diff-parser";
import type { ComputedFileDiff, DiffVariant } from "@/lib/diff-computer";
import type { CodeReview, ReviewFile } from "@/types/flow";

export type DiffFileKind = "text" | "pdf" | "binary" | "empty";

export type DiffFileEntry = {
  path: string;
  status: ReviewFile["status"];
  kind: DiffFileKind;
  additions: number;
  deletions: number;
  language: string;
  computed?: ComputedFileDiff;
  parseable: boolean;
};

const PDF_EXT = /\.pdf$/i;
const BINARY_EXT = /\.(png|jpe?g|gif|webp|ico|zip|gz|woff2?|ttf|eot|mp4|mov|bin)$/i;

export function classifyDiffFile(file: ReviewFile): DiffFileKind {
  if (PDF_EXT.test(file.path)) return "pdf";
  if (BINARY_EXT.test(file.path)) return "binary";
  if (!file.patch?.trim() && file.additions === 0 && file.deletions === 0) return "empty";
  if (!hasParseableDiff(file.patch) && (file.additions > 0 || file.deletions > 0)) return "binary";
  return "text";
}

function subscribeProcessor(processor: BackgroundReviewProcessor, onStoreChange: () => void) {
  return processor.subscribe(onStoreChange);
}

export function useDiffFilesQuery(
  review: CodeReview | undefined,
  options?: { variant?: DiffVariant; enableAgentContext?: boolean },
) {
  const variant = options?.variant ?? "basic";
  const processor = ensureBackgroundReviewProcessor();
  const [, setTick] = useState(0);

  useEffect(() => {
    if (!review) return;
    processor.engage(review.id);
    const handle = processor.prefetchReview(review, {
      priority: "high",
      variants: variant === "structural" ? ["basic", "structural"] : ["basic"],
    });
    return () => {
      handle.cancel();
      processor.disengage(review.id);
    };
  }, [processor, review, variant]);

  const revision = useSyncExternalStore(
    (onChange) => subscribeProcessor(processor, onChange),
    () => {
      if (!review) return 0;
      return processor.getReviewDiffs(review.id).reduce((sum, diff) => sum + diff.computedAt, 0);
    },
    () => 0,
  );

  useEffect(() => processor.subscribe(() => setTick((value) => value + 1)), [processor]);

  const files = useMemo<DiffFileEntry[]>(() => {
    if (!review) return [];
    return review.files.map((file) => {
      const kind = classifyDiffFile(file);
      const parsed = parseDiffFile(file.path, file.patch);
      const structural = processor.getComputedDiff(review.id, file.path, "structural");
      const basic = processor.getComputedDiff(review.id, file.path, "basic");
      const preferred =
        variant === "structural"
          ? structural?.state === "ready"
            ? structural
            : (basic ?? structural)
          : (basic ?? structural);
      return {
        path: file.path,
        status: file.status,
        kind,
        additions: preferred?.additions ?? (parsed.additions || file.additions),
        deletions: preferred?.deletions ?? (parsed.deletions || file.deletions),
        language: preferred?.language ?? parsed.language,
        computed: preferred,
        parseable: kind === "text" && (preferred?.lines.length ?? parsed.lines.length) > 0,
      };
    });
  }, [processor, review, revision, variant]);

  const diffsByPath = useMemo(() => {
    const map = new Map<string, ComputedFileDiff>();
    for (const file of files) {
      if (file.computed) map.set(file.path, file.computed);
    }
    return map;
  }, [files]);

  const agentContextPaths = useMemo(
    () => (options?.enableAgentContext === false ? [] : files.filter((f) => f.kind === "text").map((f) => f.path)),
    [files, options?.enableAgentContext],
  );

  return {
    processor,
    revision,
    files,
    diffsByPath,
    ready: review ? processor.areAllDiffsReady(review, "basic") : true,
    agentContextPaths,
    pdfCount: files.filter((f) => f.kind === "pdf").length,
    binaryCount: files.filter((f) => f.kind === "binary").length,
  };
}
