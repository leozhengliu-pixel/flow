import { useCallback, useEffect, useRef, useState } from "react";
import { ChevronRight, CircleCheck, CircleMinus, CircleX, Copy } from "lucide-react";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { GridLoader } from "@/components/ui/grid-loader";
import { useI18n } from "@/i18n/i18n";
import { cancelCodingSnapshot, fetchCodingSnapshotLogs, fetchCodingSnapshots, refreshCodingSnapshot } from "@/lib/api";
import { toast } from "@/lib/toast";
import type { CodingEnvironmentSnapshot, CodingSnapshotStatus } from "@/types/flow";
import { SNAPSHOT_STATUS_LABEL, formatDuration, snapshotDuration, snapshotInProgress } from "./coding-sessions-model";
import { CodingButton, CodingSection } from "./coding-settings-primitives";
import { stripAnsi } from "./coding-xterm";
import { SnapshotLogTerminal } from "./snapshot-log-terminal";
import { errorMessage } from "./use-coding-settings";

export const SNAPSHOT_POLL_MS = 3000;
/** The reference app lists the latest builds only. */
const MAX_SNAPSHOTS = 10;

/** 14px status mark: the dotted grid loader while queued or building, a check, an x, or a dash. */
export function SnapshotStatusIcon({ status }: { status: CodingSnapshotStatus }) {
  if (snapshotInProgress(status)) return <span className="cs-snapshot-icon is-running"><GridLoader size={14} /></span>;
  if (status === "ready") return <CircleCheck size={14} className="cs-snapshot-icon is-ready" aria-hidden="true" />;
  if (status === "failed") return <CircleX size={14} className="cs-snapshot-icon is-failed" aria-hidden="true" />;
  return <CircleMinus size={14} className="cs-snapshot-icon is-muted" aria-hidden="true" />;
}

const OUTCOME: Partial<Record<CodingSnapshotStatus, string>> = { ready: "Succeeded", failed: "Failed", canceled: "Canceled", expired: "Expired", superseded: "Superseded" };

/**
 * Snapshots (§10): "Refresh environment" in the section header, then one 60px row per build
 * (status mark, date, status or duration, chevron). A row opens the build's logs in a modal.
 */
export function SnapshotPanel({ environmentId, initial, disabled, onFixWithAgent, onChange }: { environmentId: string; initial?: CodingEnvironmentSnapshot; disabled?: boolean; onFixWithAgent: (snapshotId: string) => void; onChange?: (snapshot?: CodingEnvironmentSnapshot) => void }) {
  const { t, formatDate } = useI18n();
  const [snapshots, setSnapshots] = useState<CodingEnvironmentSnapshot[]>(initial ? [initial] : []);
  const [refreshing, setRefreshing] = useState(false);
  const [openId, setOpenId] = useState<string>();
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
  const inProgress = snapshots.some((snapshot) => snapshotInProgress(snapshot.status));
  useEffect(() => {
    if (!inProgress) return;
    const timer = setInterval(() => void load(), SNAPSHOT_POLL_MS);
    return () => clearInterval(timer);
  }, [inProgress, load]);

  const replace = (snapshot: CodingEnvironmentSnapshot) => {
    setSnapshots((current) => [snapshot, ...current.filter((item) => item.id !== snapshot.id)].sort((a, b) => b.createdAt.localeCompare(a.createdAt)));
    if (!latest || latest.id === snapshot.id || snapshot.createdAt >= latest.createdAt) onChange?.(snapshot);
  };
  const refresh = async () => {
    setRefreshing(true);
    try {
      replace(await refreshCodingSnapshot(environmentId));
    } catch (reason) {
      toast.error(errorMessage(reason, "Could not refresh the snapshot"));
    } finally {
      setRefreshing(false);
    }
  };

  const open = snapshots.find((snapshot) => snapshot.id === openId);
  const when = (value: string) => {
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? "" : formatDate(date, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" });
  };

  return (
    <CodingSection
      title="Snapshots"
      className="cs-snapshots"
      action={
        <CodingButton small className="cs-refresh-button" title={t("Prepare a new snapshot from saved settings")} disabled={disabled || refreshing} onClick={() => void refresh()}>
          {refreshing ? "Refreshing environment…" : "Refresh environment"}
        </CodingButton>
      }
    >
      <div className="feature-card cs-snapshot-list" data-testid="snapshot-panel">
        {snapshots.slice(0, MAX_SNAPSHOTS).map((snapshot) => {
          const duration = snapshotDuration(snapshot);
          const finished = !snapshotInProgress(snapshot.status);
          return (
            <button key={snapshot.id} type="button" className="cs-snapshot-row" data-testid="snapshot-row" onClick={() => setOpenId(snapshot.id)}>
              <SnapshotStatusIcon status={snapshot.status} />
              <strong data-i18n-ignore>{when(snapshot.createdAt)}</strong>
              <span className="cs-snapshot-meta">
                <span data-testid="snapshot-status" data-status={snapshot.status} {...(finished && duration !== undefined && snapshot.status === "ready" ? { "data-i18n-ignore": true } : {})}>
                  {finished && duration !== undefined && snapshot.status === "ready" ? formatDuration(duration) : t(SNAPSHOT_STATUS_LABEL[snapshot.status])}
                </span>
                {finished && <ChevronRight size={16} aria-hidden="true" />}
              </span>
            </button>
          );
        })}
        {!snapshots.length && <p className="cs-card-text cs-snapshot-empty">{t("No snapshots yet.")}</p>}
      </div>
      {open && (
        <SnapshotLogsDialog
          snapshot={open}
          disabled={disabled}
          onClose={() => setOpenId(undefined)}
          onChange={replace}
          onFixWithAgent={(id) => {
            setOpenId(undefined);
            onFixWithAgent(id);
          }}
        />
      )}
    </CodingSection>
  );
}

/** "Snapshot logs": a 960px modal with the build log in a read-only xterm (polled until complete), outcome, Copy logs. */
function SnapshotLogsDialog({ snapshot, disabled, onClose, onChange, onFixWithAgent }: { snapshot: CodingEnvironmentSnapshot; disabled?: boolean; onClose: () => void; onChange: (snapshot: CodingEnvironmentSnapshot) => void; onFixWithAgent: (snapshotId: string) => void }) {
  const { t, formatRelative } = useI18n();
  const [logs, setLogs] = useState<{ logs: string; complete: boolean }>();
  const [canceling, setCanceling] = useState(false);
  const id = snapshot.id;

  useEffect(() => {
    let active = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const tick = async () => {
      try {
        const response = await fetchCodingSnapshotLogs(id);
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
  }, [id, t]);

  const cancel = async () => {
    setCanceling(true);
    try {
      onChange(await cancelCodingSnapshot(snapshot.id));
    } catch (reason) {
      toast.error(errorMessage(reason, "Could not cancel the snapshot"));
    } finally {
      setCanceling(false);
    }
  };
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(stripAnsi(logs?.logs ?? ""));
      toast.success(t("Logs copied"));
    } catch {
      toast.error(t("Could not copy logs"));
    }
  };

  const running = snapshotInProgress(snapshot.status);
  const duration = snapshotDuration(snapshot);
  const outcome = running ? t(SNAPSHOT_STATUS_LABEL[snapshot.status]) : t(OUTCOME[snapshot.status] ?? SNAPSHOT_STATUS_LABEL[snapshot.status]);
  const summary = [
    outcome,
    snapshot.startedAt || snapshot.createdAt ? t("started {time}").replace("{time}", formatRelative(snapshot.startedAt || snapshot.createdAt)) : "",
    duration !== undefined ? t("took {duration}").replace("{duration}", formatDuration(duration)) : "",
  ].filter(Boolean).join(" · ");

  return (
    <Dialog open onOpenChange={(value) => !value && onClose()}>
      <DialogContent className="feature-dialog cs-logs-dialog" aria-describedby={undefined} closeLabel={t("Close")}>
        <header className="cs-logs-header">
          <SnapshotStatusIcon status={snapshot.status} />
          <DialogTitle>{t("Snapshot logs")}</DialogTitle>
        </header>
        <SnapshotLogTerminal text={logs?.logs ?? ""} placeholder={logs ? t("No output yet") : t("Loading…")} />
        {snapshot.status === "failed" && snapshot.error && <p className="cs-inline-error cs-logs-error" data-i18n-ignore>{snapshot.error}</p>}
        <footer className="cs-logs-footer">
          <span className="cs-logs-summary" data-testid="snapshot-summary" data-i18n-ignore>{summary}</span>
          {running && <CodingButton small disabled={disabled || canceling} onClick={() => void cancel()}>Cancel</CodingButton>}
          {snapshot.status === "failed" && <CodingButton small disabled={disabled} onClick={() => onFixWithAgent(snapshot.id)}>Fix with agent</CodingButton>}
          <CodingButton small disabled={!logs?.logs} onClick={() => void copy()}>
            <Copy size={14} />
            Copy logs
          </CodingButton>
        </footer>
      </DialogContent>
    </Dialog>
  );
}
