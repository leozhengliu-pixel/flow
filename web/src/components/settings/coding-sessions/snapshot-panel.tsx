import { useCallback, useEffect, useRef, useState } from "react";
import { RefreshCw } from "lucide-react";
import { useI18n } from "@/i18n/i18n";
import { cancelCodingSnapshot, fetchCodingSnapshotLogs, fetchCodingSnapshots, refreshCodingSnapshot } from "@/lib/api";
import { toast } from "@/lib/toast";
import type { CodingEnvironmentSnapshot } from "@/types/flow";
import { SNAPSHOT_STATUS_LABEL, formatBytes, snapshotInProgress } from "./coding-sessions-model";
import { CodingButton } from "./coding-settings-primitives";
import { errorMessage } from "./use-coding-settings";

export const SNAPSHOT_POLL_MS = 3000;

function when(value?: string) {
  if (!value) return "";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "" : date.toLocaleString();
}

/** Snapshot status, logs, Refresh and Cancel (§10). */
export function SnapshotPanel({ environmentId, initial, disabled, onFixWithAgent, onChange }: { environmentId: string; initial?: CodingEnvironmentSnapshot; disabled?: boolean; onFixWithAgent: (snapshotId: string) => void; onChange?: (snapshot?: CodingEnvironmentSnapshot) => void }) {
  const { t } = useI18n();
  const [snapshots, setSnapshots] = useState<CodingEnvironmentSnapshot[]>(initial ? [initial] : []);
  const [busy, setBusy] = useState(false);
  const [showLogs, setShowLogs] = useState(false);
  const [logs, setLogs] = useState<{ logs: string; complete: boolean }>();
  const latest = snapshots[0];
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;

  const load = useCallback(async () => {
    try {
      const list = await fetchCodingSnapshots(environmentId);
      const sorted = [...(list ?? [])].sort((a, b) => b.createdAt.localeCompare(a.createdAt));
      setSnapshots(sorted);
      onChangeRef.current?.(sorted[0]);
    } catch {
      // Keep the snapshot embedded in the environment when the history can't be read.
    }
  }, [environmentId]);
  useEffect(() => {
    void load();
  }, [load]);
  const inProgress = snapshotInProgress(latest?.status);
  useEffect(() => {
    if (!inProgress) return;
    const timer = setInterval(() => void load(), SNAPSHOT_POLL_MS);
    return () => clearInterval(timer);
  }, [inProgress, load]);

  const latestId = latest?.id;
  useEffect(() => {
    if (!showLogs || !latestId) return;
    let active = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const tick = async () => {
      try {
        const response = await fetchCodingSnapshotLogs(latestId);
        if (!active) return;
        // Logs are lines posted by the runner; accept a joined string or the raw line list.
        const raw = (response as { logs: string | string[] }).logs;
        const next = { logs: Array.isArray(raw) ? raw.join("\n") : raw ?? "", complete: Boolean(response?.complete) };
        setLogs(next);
        if (!next.complete) timer = setTimeout(() => void tick(), SNAPSHOT_POLL_MS);
      } catch (reason) {
        if (active) setLogs({ logs: errorMessage(reason, t("Could not load logs")), complete: true });
      }
    };
    void tick();
    return () => {
      active = false;
      if (timer) clearTimeout(timer);
    };
  }, [showLogs, latestId, t]);

  const refresh = async () => {
    setBusy(true);
    try {
      const snapshot = await refreshCodingSnapshot(environmentId);
      setSnapshots((current) => [snapshot, ...current.filter((item) => item.id !== snapshot.id)]);
      onChange?.(snapshot);
    } catch (reason) {
      toast.error(errorMessage(reason, "Could not refresh the snapshot"));
    } finally {
      setBusy(false);
    }
  };
  const cancel = async () => {
    if (!latest) return;
    setBusy(true);
    try {
      const snapshot = await cancelCodingSnapshot(latest.id);
      setSnapshots((current) => current.map((item) => (item.id === snapshot.id ? snapshot : item)));
      onChange?.(snapshot);
    } catch (reason) {
      toast.error(errorMessage(reason, "Could not cancel the snapshot"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="cs-snapshot" data-testid="snapshot-panel">
      <div className="feature-row cs-row">
        <div>
          <strong>
            {t("Snapshot")}
            {latest && <span className={`cs-badge is-${latest.status}`} data-testid="snapshot-status">{t(SNAPSHOT_STATUS_LABEL[latest.status])}</span>}
          </strong>
          <span>
            {latest
              ? [latest.baseRef && `${latest.baseRef}${latest.baseSha ? ` @ ${latest.baseSha.slice(0, 7)}` : ""}`, formatBytes(latest.sizeBytes), when(latest.finishedAt || latest.startedAt || latest.createdAt)].filter(Boolean).join(" · ")
              : t("No snapshot yet. Sessions run setup until the first snapshot is ready.")}
          </span>
          {latest?.status === "failed" && latest.error && <span className="cs-inline-error" data-i18n-ignore>{latest.error}</span>}
        </div>
        <aside>
          {latest && <CodingButton onClick={() => setShowLogs((value) => !value)} aria-expanded={showLogs}>{showLogs ? "Hide logs" : "View logs"}</CodingButton>}
          {latest?.status === "failed" && <CodingButton disabled={disabled} onClick={() => onFixWithAgent(latest.id)}>Fix with agent</CodingButton>}
          {inProgress ? (
            <CodingButton disabled={disabled || busy} onClick={() => void cancel()}>Cancel</CodingButton>
          ) : (
            <CodingButton disabled={disabled || busy} onClick={() => void refresh()}>
              <RefreshCw size={13} />
              Refresh
            </CodingButton>
          )}
        </aside>
      </div>
      {showLogs && (
        <pre className="cs-logs" data-testid="snapshot-logs" aria-live="polite">
          {logs ? logs.logs || t("No output yet") : t("Loading…")}
        </pre>
      )}
      {snapshots.length > 1 && (
        <details className="cs-snapshot-history">
          <summary>{t("Previous snapshots")}</summary>
          <ul>
            {snapshots.slice(1, 6).map((snapshot) => (
              <li key={snapshot.id}>
                <span className={`cs-badge is-${snapshot.status}`}>{t(SNAPSHOT_STATUS_LABEL[snapshot.status])}</span>
                <span data-i18n-ignore>{when(snapshot.finishedAt || snapshot.createdAt)}</span>
                {snapshot.error && <span className="cs-history-error" data-i18n-ignore>{snapshot.error}</span>}
              </li>
            ))}
          </ul>
        </details>
      )}
    </div>
  );
}
