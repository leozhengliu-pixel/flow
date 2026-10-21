import type { ReviewRouteTab } from "@/lib/app-routes";
import { reviewPath } from "@/lib/app-routes";
import type { CodeReview } from "@/types/flow";

/** Up to this pane width the agent panel replaces the review body instead of sitting beside it (see reviews-page.css). */
export const EMBEDDED_REVIEW_NARROW_WIDTH = 1000;

/**
 * The tab and agent state a path within this review names (`/review/<slug>/changes?showAgent=false&agentSessionId=…`), or
 * `null` for a path to anywhere else.
 */
export function parseEmbeddedReviewPath(
  path: string,
  workspaceKey: string,
  review: Pick<CodeReview, "slugId">,
): { tab: ReviewRouteTab; showAgent?: boolean } | null {
  const [pathname, query = ""] = path.split("#")[0].split("?");
  const base = reviewPath(workspaceKey, review);
  if (pathname !== base && !pathname.startsWith(`${base}/`)) return null;
  const segment = pathname.slice(base.length + 1);
  const tab: ReviewRouteTab = segment === "changes" || segment === "review" ? segment : "overview";
  const show = new URLSearchParams(query).get("showAgent");
  return { tab, showAgent: show === null ? undefined : show !== "false" };
}

/**
 * The agent panel's state after a navigation within the review. In a narrow pane the open panel hides the tab, so any
 * navigation other than an explicit hide brings the tab into view.
 */
export function nextEmbeddedAgentShown(current: boolean, requested: boolean | undefined, narrow: boolean) {
  if (requested === false) return false;
  if (current && narrow) return false;
  return requested ?? current;
}
