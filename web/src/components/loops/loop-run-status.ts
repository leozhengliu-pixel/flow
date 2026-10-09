import type { LoopRunFailureReason, LoopRunStatus } from "@/types/flow";

/** Run statuses in the run list filter order, and their labels (translated with t()). */
export const RUN_STATUSES: LoopRunStatus[] = ["completed", "needs_review", "failed", "cancelled", "interrupted", "running"];
export const STATUS_LABELS: Record<LoopRunStatus, string> = {
  running: "Running",
  completed: "Completed",
  needs_review: "Needs review",
  failed: "Failed",
  cancelled: "Cancelled",
  interrupted: "Interrupted",
};
/** Short explanations for why a run did not complete. */
export const REASON_LABELS: Record<LoopRunFailureReason, string> = {
  cancelled: "Cancelled",
  interrupted: "Interrupted by a server restart",
  timeout: "Ran out of time",
  provider_timeout: "The model provider timed out",
  provider_error: "The model provider failed",
  empty_response: "The model returned an empty response",
  tool_error: "A change the loop needed failed",
  budget_exhausted: "Run budget exceeded",
  no_output: "No output produced",
  incomplete: "The agent reported unfinished work",
  unavailable: "The loop could not run",
  error: "Error",
};
