/**
 * Hook bridge: BackgroundReviewProcessor ↔ Reviews DiffView.
 */
import { useEffect, useState, useSyncExternalStore } from "react";

import {
  ensureBackgroundReviewProcessor,
  type BackgroundReviewProcessor,
} from "@/lib/background-review-processor";
import type { ComputedFileDiff, DiffVariant } from "@/lib/diff-computer";
import type { CodeReview } from "@/types/flow";

function subscribeProcessor(processor: BackgroundReviewProcessor, onStoreChange: () => void) {
  return processor.subscribe(onStoreChange);
}

export function useReviewDiffs(review: CodeReview | undefined, variant: DiffVariant = "basic") {
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
      // Derive a cheap revision from store contents.
      return processor.getReviewDiffs(review.id).reduce((sum, diff) => sum + diff.computedAt, 0);
    },
    () => 0,
  );

  // Keep a local tick so consumers re-render even if revision math collides.
  useEffect(() => {
    return processor.subscribe(() => setTick((value) => value + 1));
  }, [processor]);

  const diffsByPath = new Map<string, ComputedFileDiff>();
  if (review) {
    for (const file of review.files) {
      const structural = processor.getComputedDiff(review.id, file.path, "structural");
      const basic = processor.getComputedDiff(review.id, file.path, "basic");
      const preferred =
        variant === "structural"
          ? structural?.state === "ready"
            ? structural
            : basic ?? structural
          : basic ?? structural;
      if (preferred) diffsByPath.set(file.path, preferred);
    }
  }

  return {
    processor,
    revision,
    diffsByPath,
    ready: review ? processor.areAllDiffsReady(review, "basic") : true,
  };
}
