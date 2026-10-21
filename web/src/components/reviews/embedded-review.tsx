import { useCallback, useEffect, useRef, useState } from "react";

import type { ReviewRouteTab } from "@/lib/app-routes";
import { reviewAgentPath, reviewPath } from "@/lib/app-routes";
import type { BootstrapData, CodeReview } from "@/types/flow";

import { EMBEDDED_REVIEW_NARROW_WIDTH, nextEmbeddedAgentShown, parseEmbeddedReviewPath } from "./embedded-review-model";
import { ReviewsPage } from "./reviews-page";

/**
 * Flow's review page (header, Overview / Guide / Diff tabs) hosted in the inbox detail pane with the coding session's
 * agent panel. Opens on the Diff tab with the agent shown; "open in full page" goes to the review route.
 */
export function EmbeddedReview({
  data,
  review,
  sessionId,
  onReload,
  onNavigate,
}: {
  data: BootstrapData;
  review: CodeReview;
  /** The coding session of this pull request; without one (a review request) the review has no agent panel. */
  sessionId?: string;
  onReload: () => Promise<void>;
  /** Paths outside this review (the issue breadcrumb) and the full page. */
  onNavigate: (path: string) => void;
}) {
  const [state, setState] = useState<{ tab: ReviewRouteTab; showAgent: boolean }>({ tab: sessionId ? "changes" : "overview", showAgent: Boolean(sessionId) });
  const rootRef = useRef<HTMLDivElement>(null);
  const [narrow, setNarrow] = useState(false);
  useEffect(() => {
    const element = rootRef.current;
    if (!element || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(([entry]) => setNarrow(entry.contentRect.width <= EMBEDDED_REVIEW_NARROW_WIDTH));
    observer.observe(element);
    return () => observer.disconnect();
  }, []);
  const workspaceKey = data.workspace.urlKey;
  const navigate = useCallback(
    (path: string) => {
      const next = parseEmbeddedReviewPath(path, workspaceKey, review);
      if (!next) {
        onNavigate(path);
        return;
      }
      setState((current) => ({ tab: next.tab, showAgent: nextEmbeddedAgentShown(current.showAgent, next.showAgent, narrow) }));
    },
    [workspaceKey, review, narrow, onNavigate],
  );
  return (
    <div ref={rootRef} className="reviews-embedded-host" style={{ height: "100%", minHeight: 0 }}>
      <ReviewsPage
        data={data}
        view="for-you"
        review={review}
        tab={state.tab}
        agentSessionId={sessionId}
        showAgent={state.showAgent}
        onNavigate={navigate}
        onReload={onReload}
        embedded={{ onOpenFull: () => onNavigate(sessionId ? reviewAgentPath(workspaceKey, review, sessionId, { tab: state.tab, showAgent: state.showAgent }) : reviewPath(workspaceKey, review, state.tab)) }}
      />
    </div>
  );
}
