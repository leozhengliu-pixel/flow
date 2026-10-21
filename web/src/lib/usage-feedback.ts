import { useContext } from "react";

import { ApplicationStoreContext } from "@/store/application-store-contexts";
import type { WorkspaceSettings } from "@/types/flow";

/** The workspace feature flag behind Settings › AI & agents › "Enable usage feedback". On until an admin turns it off. */
export const USAGE_FEEDBACK_FLAG = "usage-feedback";

export function usageFeedbackEnabled(settings?: Pick<WorkspaceSettings, "featureFlags"> | null): boolean {
  return settings?.featureFlags?.[USAGE_FEEDBACK_FLAG] ?? true;
}

/**
 * Whether agent answers offer feedback (thumbs up / down, "Leave feedback…"). Outside the application store (a bare
 * component in a test or story) feedback stays on, like the server's default.
 */
export function useUsageFeedbackEnabled(): boolean {
  const app = useContext(ApplicationStoreContext);
  return usageFeedbackEnabled(app?.workspaceSettings ?? app?.data?.workspaceSettings);
}
