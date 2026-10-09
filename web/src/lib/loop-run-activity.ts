/**
 * Loop run progress arrives as lightweight `loop_run.*` realtime signals
 * ({ loopId, runId, status }) that change no workspace data. The app forwards
 * them as this window event so an open run page refetches the run at once
 * instead of reloading the workspace.
 */
export const LOOP_RUN_ACTIVITY_EVENT = "flow:loop-run-activity";

export type LoopRunActivity = { type: string; loopId?: string; runId?: string; status?: string };

export function isLoopRunSignal(type: string) {
  return type.startsWith("loop_run.");
}

export function loopRunActivity(event: { type: string; payload?: unknown }): LoopRunActivity {
  const payload = (event.payload ?? {}) as Record<string, unknown>;
  const text = (value: unknown) => (typeof value === "string" ? value : undefined);
  return { type: event.type, loopId: text(payload.loopId), runId: text(payload.runId), status: text(payload.status) };
}
