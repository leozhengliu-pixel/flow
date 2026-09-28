import { useCallback, useEffect, useState } from "react";
import { Play } from "lucide-react";
import { toast } from "sonner";

import { useI18n } from "@/i18n/i18n";
import { listLoopRuns, runLoopNow } from "@/lib/api";
import type { Loop, LoopRun } from "@/types/flow";

const TRIGGER_LABELS: Record<LoopRun["trigger"], string> = {
  manual: "Manual run",
  schedule: "Scheduled",
  event: "Triggered",
};

/** Run history for a loop, with Run now for scheduled loops. */
export function LoopRuns({ loop }: { loop: Loop }) {
  const { t, formatDate } = useI18n();
  const [runs, setRuns] = useState<LoopRun[]>();
  const [starting, setStarting] = useState(false);
  const load = useCallback(async () => {
    try {
      setRuns(await listLoopRuns(loop.id));
    } catch (error) {
      setRuns([]);
      toast.error(error instanceof Error ? error.message : t("Could not load loop runs"));
    }
  }, [loop.id, t]);
  useEffect(() => {
    void load();
  }, [load]);
  // Poll while a run is in progress.
  useEffect(() => {
    if (!runs?.some((run) => run.status === "running")) return;
    const timer = window.setTimeout(() => void load(), 2000);
    return () => window.clearTimeout(timer);
  }, [runs, load]);
  const start = async () => {
    setStarting(true);
    try {
      await runLoopNow(loop.id);
      await load();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not start the loop"));
    } finally {
      setStarting(false);
    }
  };
  const date = (value: string) => formatDate(value, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" });
  return (
    <section className="loops-editor-section loops-runs">
      <div className="loops-section-heading">
        <h2>{t("Runs")}</h2>
        {loop.triggerType === "schedule" && (
          <button className="loops-compose-button" disabled={starting || !loop.enabled} onClick={() => void start()}>
            <Play size={13} />
            {t("Run now")}
          </button>
        )}
      </div>
      {loop.triggerType === "schedule" && loop.enabled && loop.nextRunAt && (
        <p className="loops-runs-next">
          {t("Next run")} {date(loop.nextRunAt)}
        </p>
      )}
      {runs && runs.length === 0 && <p className="loops-runs-empty">{t("This loop has not run yet.")}</p>}
      {runs && runs.length > 0 && (
        <ol className="loops-runs-list">
          {runs.map((run) => {
            const blocked = run.toolCalls?.filter((call) => call.status === "blocked").length ?? 0;
            return (
              <li key={run.id} className={`loops-run is-${run.status}`}>
                <header>
                  <i aria-hidden="true" />
                  <strong>{t(run.status === "running" ? "Running" : run.status === "failed" ? "Failed" : "Completed")}</strong>
                  <span>
                    {t(TRIGGER_LABELS[run.trigger])}
                    {run.entityIdentifier && <span data-i18n-ignore> · {run.entityIdentifier}</span>}
                  </span>
                  <time dateTime={run.startedAt}>{date(run.startedAt)}</time>
                </header>
                {run.error && <p className="loops-run-error">{run.error}</p>}
                {run.output && <p className="loops-run-output">{run.output}</p>}
                {(run.toolCalls?.length ?? 0) > 0 && (
                  <small>
                    {run.toolCalls!.length} {t(run.toolCalls!.length === 1 ? "tool call" : "tool calls")}
                    {blocked > 0 && ` · ${blocked} ${t("blocked by loop permissions")}`}
                  </small>
                )}
              </li>
            );
          })}
        </ol>
      )}
    </section>
  );
}
