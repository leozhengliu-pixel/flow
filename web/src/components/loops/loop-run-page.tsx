import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { AlertTriangle, ArrowDown, ArrowUp, CheckCircle2, Info, Link2, LoaderCircle, Pencil, Search, Settings2, ThumbsDown, ThumbsUp, XCircle } from "lucide-react";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { DisplayIcon, FilterIcon } from "@/components/ui/view-action-icons";
import { AgentWorkGroup } from "@/components/agent/agent-work-group";
import { AgentAnswerText } from "@/components/agent/agent-answer";
import { toast } from "sonner";
import { getLoopRun, listLoopRuns, rateLoopRun, replyToLoopRun } from "@/lib/api";
import { UserAvatar } from "@/components/ui/user-avatar";
import { editLoopPath, loopPath, loopRunPath, loopsPath } from "@/lib/app-routes";
import { useI18n } from "@/i18n/i18n";
import type { BootstrapData, LoopRun, LoopRunReply } from "@/types/flow";
import { copyText, loopUrl, runParts, useLoopRecord } from "./loop-data";
import { LoopBreadcrumb } from "./loop-breadcrumb";
import { LoopInstructionsEditor } from "./loop-instructions-editor";
import { dayLabel, runDuration, runTriggerLabel } from "./loop-model";

const POLL_MS = 1500;
/** Loop runs never wait for approvals in the transcript. */
const ignoreApproval = () => undefined;

function RunStatusIcon({ status }: { status: LoopRun["status"] }) {
  if (status === "running") return <LoaderCircle aria-label="Running" className="loops-run-status is-running" size={14} />;
  if (status === "failed") return <AlertTriangle aria-label="Failed" className="loops-run-status is-failed" size={14} />;
  return <CheckCircle2 aria-label="Completed" className="loops-run-status is-completed" size={14} />;
}

/** Run history: runs on the left, the selected run's live transcript on the right. */
export function LoopRunPage({
  data,
  loopId,
  runId,
  onOpenSidebar,
  onNavigate,
}: {
  data: BootstrapData;
  loopId: string;
  runId?: string;
  onOpenSidebar: () => void;
  onNavigate: (path: string) => void;
}) {
  const { t } = useI18n();
  const workspace = data.workspace.urlKey;
  const { loop, missing } = useLoopRecord(data, loopId);
  const [runs, setRuns] = useState<LoopRun[]>();
  const [selected, setSelected] = useState<LoopRun>();
  const [query, setQuery] = useState("");
  const [statusFilters, setStatusFilters] = useState<LoopRun["status"][]>([]);
  const [triggerFilters, setTriggerFilters] = useState<LoopRun["trigger"][]>([]);
  const [showDuration, setShowDuration] = useState(true);
  const [instructionsOpen, setInstructionsOpen] = useState(false);
  const [error, setError] = useState<string>();

  const loadRuns = useCallback(async () => {
    try {
      const items = await listLoopRuns(loopId);
      setRuns(Array.isArray(items) ? items : []);
    } catch (reason) {
      setRuns([]);
      setError(reason instanceof Error ? reason.message : t("Could not load loop runs"));
    }
  }, [loopId, t]);
  useEffect(() => {
    void loadRuns();
  }, [loadRuns]);

  const activeId = runId ?? runs?.[0]?.id;
  const runsRef = useRef(runs);
  runsRef.current = runs;
  useEffect(() => {
    if (!activeId) {
      setSelected(undefined);
      return;
    }
    let active = true;
    const fromList = runsRef.current?.find((item) => item.id === activeId);
    if (fromList) setSelected(fromList);
    getLoopRun(loopId, activeId)
      .then((run) => active && setSelected(run))
      .catch(() => undefined);
    return () => {
      active = false;
    };
    // Runs refresh on their own poll; only re-fetch when the selection changes.
  }, [activeId, loopId]);

  // Poll the selected run and the list while anything is still running (a run, or the agent answering a reply).
  const replying = Boolean(selected?.replies?.some((reply) => reply.status === "running"));
  const running = selected?.status === "running" || replying || Boolean(runs?.some((item) => item.status === "running"));
  useEffect(() => {
    if (!running) return;
    const timer = window.setInterval(() => {
      if (activeId)
        void getLoopRun(loopId, activeId)
          .then((run) => {
            setSelected(run);
            setRuns((current) => current?.map((item) => (item.id === run.id ? run : item)));
          })
          .catch(() => undefined);
      void loadRuns();
    }, POLL_MS);
    return () => window.clearInterval(timer);
  }, [activeId, loadRuns, loopId, running]);

  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return (runs ?? []).filter((run) => {
      if (statusFilters.length && !statusFilters.includes(run.status)) return false;
      if (triggerFilters.length && !triggerFilters.includes(run.trigger)) return false;
      if (needle && !`${runTriggerLabel(run)} ${run.entityIdentifier ?? ""} ${run.output ?? ""} ${run.error ?? ""}`.toLowerCase().includes(needle)) return false;
      return true;
    });
  }, [query, runs, statusFilters, triggerFilters]);

  // Linear's ↓/↑: the next (older) and previous (newer) run in the visible list.
  const position = filtered.findIndex((run) => run.id === activeId);
  const nextRun = position >= 0 ? filtered[position + 1] : undefined;
  const previousRun = position > 0 ? filtered[position - 1] : undefined;
  const goToRun = useCallback((run: LoopRun | undefined) => run && onNavigate(loopRunPath(workspace, loopId, run.id)), [loopId, onNavigate, workspace]);
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.metaKey || event.ctrlKey || event.altKey) return;
      const target = event.target as HTMLElement | null;
      if (target?.closest("input, textarea, select, [contenteditable='true'], [role='menu'], [role='dialog']")) return;
      const key = event.key.toLowerCase();
      if (key === "j" && nextRun) {
        event.preventDefault();
        goToRun(nextRun);
      } else if (key === "k" && previousRun) {
        event.preventDefault();
        goToRun(previousRun);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [goToRun, nextRun, previousRun]);

  // A new reply scrolls the conversation to its end, like a chat.
  const scroller = useRef<HTMLDivElement>(null);
  const replyCount = selected?.replies?.length ?? 0;
  useEffect(() => {
    if (!replyCount || !scroller.current) return;
    scroller.current.scrollTop = scroller.current.scrollHeight;
  }, [replyCount]);

  const applyRun = (run: LoopRun) => {
    setSelected((current) => (current?.id === run.id ? run : current));
    setRuns((current) => current?.map((item) => (item.id === run.id ? run : item)));
  };

  if (missing)
    return (
      <main className="main-panel loops-page" aria-label={t("Loop not found")}>
        <div className="loops-not-found">
          <h2>{t("Loop not found")}</h2>
          <button className="loops-primary-button" onClick={() => onNavigate(loopsPath(workspace))}>
            {t("Back to loops")}
          </button>
        </div>
      </main>
    );
  const toggle = <T,>(list: T[], value: T) => (list.includes(value) ? list.filter((item) => item !== value) : [...list, value]);
  const feedback = selected?.viewerRating ?? undefined;
  const rate = async (value: "up" | "down") => {
    if (!selected) return;
    const previous = selected;
    const next = feedback === value ? null : value;
    const apply = applyRun;
    apply({ ...selected, viewerRating: next });
    try {
      apply(await rateLoopRun(loopId, selected.id, next));
    } catch (reason) {
      apply(previous);
      toast.error(reason instanceof Error ? reason.message : t("Could not save feedback"));
    }
  };
  const parts = selected ? runParts(selected) : [];
  const started = selected ? new Date(selected.startedAt) : undefined;
  return (
    <main className="main-panel loops-run-page" aria-label={t("Run history")}>
      <header className="loops-editor-topbar">
        <button className="loops-mobile-menu" aria-label={t("Open sidebar")} data-sidebar-trigger onClick={onOpenSidebar}>
          <Settings2 />
        </button>
        <LoopBreadcrumb data={data} loop={loop} loopId={loopId} current={t("Run history")} onNavigate={onNavigate} />
        {loop && (
          <div className="loops-topbar-actions">
            <button className="loops-icon-button is-plain" aria-label={t("Copy link")} title={t("Copy link")} onClick={() => void copyText(loopUrl(workspace, loop), t("Link copied"))}>
              <Link2 size={14} />
            </button>
            <div className="loops-run-nav" role="group" aria-label={t("Runs")}>
              <button type="button" aria-label={t("Go to next run")} title={t(nextRun ? "Go to next run" : "No next run")} disabled={!nextRun} onClick={() => goToRun(nextRun)}>
                <ArrowDown size={14} />
              </button>
              <button type="button" aria-label={t("Go to previous run")} title={t(previousRun ? "Go to previous run" : "No previous run")} disabled={!previousRun} onClick={() => goToRun(previousRun)}>
                <ArrowUp size={14} />
              </button>
            </div>
          </div>
        )}
      </header>
      <div className="loops-run-layout">
        <aside className="loops-run-list" aria-label={t("Runs")}>
          <div className="loops-run-list-toolbar">
            <label className="loops-search">
              <Search size={16} />
              <input aria-label={t("Search runs…")} placeholder={t("Search runs…")} value={query} onChange={(event) => setQuery(event.target.value)} />
            </label>
            <DropdownMenu>
              <DropdownMenuTrigger className={`loops-icon-button${statusFilters.length || triggerFilters.length ? " is-open" : ""}`} aria-label={t("Filter")}>
                <FilterIcon />
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="loops-menu">
                <DropdownMenuLabel>{t("Status")}</DropdownMenuLabel>
                {(["completed", "failed", "running"] as const).map((item) => (
                  <DropdownMenuCheckboxItem key={item} checked={statusFilters.includes(item)} onSelect={(event) => event.preventDefault()} onCheckedChange={() => setStatusFilters((current) => toggle(current, item))}>
                    {t(item === "completed" ? "Completed" : item === "failed" ? "Failed" : "Running")}
                  </DropdownMenuCheckboxItem>
                ))}
                <DropdownMenuSeparator />
                <DropdownMenuLabel>{t("Trigger")}</DropdownMenuLabel>
                {(["manual", "schedule", "event"] as const).map((item) => (
                  <DropdownMenuCheckboxItem key={item} checked={triggerFilters.includes(item)} onSelect={(event) => event.preventDefault()} onCheckedChange={() => setTriggerFilters((current) => toggle(current, item))}>
                    {t(item === "manual" ? "Manual run" : item === "schedule" ? "Scheduled run" : "Triggered run")}
                  </DropdownMenuCheckboxItem>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
            <DropdownMenu>
              <DropdownMenuTrigger className="loops-icon-button" aria-label={t("Display options")}>
                <DisplayIcon />
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="loops-menu">
                <DropdownMenuCheckboxItem checked={showDuration} onSelect={(event) => event.preventDefault()} onCheckedChange={(checked) => setShowDuration(checked === true)}>
                  {t("Duration")}
                </DropdownMenuCheckboxItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
          {runs === undefined ? null : filtered.length === 0 ? (
            <p className="loops-run-list-empty">{error ?? t(runs.length ? "No runs match." : "This loop has not run yet.")}</p>
          ) : (
            <ol>
              {filtered.map((run) => (
                <li key={run.id}>
                  <button
                    aria-current={run.id === activeId ? "true" : undefined}
                    className={`loops-run-row${run.id === activeId ? " is-active" : ""}`}
                    onClick={() => onNavigate(loopRunPath(workspace, loopId, run.id))}
                  >
                    <span className="loops-run-row-label" data-i18n-ignore={run.triggerLabel ? true : undefined}>
                      {run.triggerLabel ?? t(runTriggerLabel(run))}
                    </span>
                    <RunStatusIcon status={run.status} />
                    {showDuration && <span className="loops-run-row-meta">{runDuration(run.startedAt, run.finishedAt)}</span>}
                    <time className="loops-run-row-meta" dateTime={run.startedAt} title={new Date(run.startedAt).toLocaleString()}>
                      {t(dayLabel(run.startedAt))}
                    </time>
                  </button>
                </li>
              ))}
            </ol>
          )}
        </aside>
        <section className="loops-run-detail" aria-label={t("Run")}>
          <div className="loops-run-scroll" ref={scroller}>
          {!selected ? (
            runs !== undefined && <p className="loops-run-list-empty">{t(runs.length ? "Select a run" : "This loop has not run yet.")}</p>
          ) : (
            <div className="loops-run-body">
              <header className="loops-run-header">
                <h1>
                  {t(dayLabel(selected.startedAt))} {t("at")} {started?.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" })}
                </h1>
                <button className="loops-run-edit" onClick={() => onNavigate(editLoopPath(workspace, loopId))}>
                  <Pencil size={14} />
                  {t("Edit loop")}
                </button>
              </header>
              <p className="loops-run-subtitle">
                <RunStatusIcon status={selected.status} />
                <span data-i18n-ignore={selected.triggerLabel ? true : undefined}>{selected.triggerLabel ?? t(runTriggerLabel(selected))}</span>
                <span>· {runDuration(selected.startedAt, selected.finishedAt)}</span>
                {selected.version !== undefined && (
                  <a
                    className="loops-run-version"
                    href={`${loopPath(workspace, loopId)}?versions=1`}
                    title={t("Show published versions")}
                    onClick={(event) => {
                      event.preventDefault();
                      onNavigate(`${loopPath(workspace, loopId)}?versions=1`);
                    }}
                  >
                    · {t("Ran version {version}").replace("{version}", String(selected.version))}
                  </a>
                )}
              </p>
              {(selected.notices ?? []).map((notice) => (
                <p className="loops-run-notice" role="note" key={notice}>
                  <Info size={14} />
                  <span>{t(notice)}</span>
                </p>
              ))}
              {loop && (
                <section className={`loops-run-instructions${instructionsOpen ? "" : " is-collapsed"}`} aria-label={t("Instructions")}>
                  <h3>{t("Instructions")}</h3>
                  <div className={`loops-instructions-text${instructionsOpen ? "" : " is-collapsed"}`} data-i18n-ignore>
                    <LoopInstructionsEditor readOnly ariaLabel={t("Instructions")} data={data} value={loop.instructions} valueData={loop.instructionsData} onNavigate={onNavigate} />
                  </div>
                  <button className="loops-expand-button is-small" type="button" aria-expanded={instructionsOpen} onClick={() => setInstructionsOpen((value) => !value)}>
                    {t(instructionsOpen ? "Collapse" : "Expand")}
                  </button>
                </section>
              )}
              {parts.length > 0 && (
                <AgentWorkGroup
                  className="loops-run-work"
                  message={{ durationMs: selected.finishedAt ? Math.max(1000, Date.parse(selected.finishedAt) - Date.parse(selected.startedAt)) : undefined }}
                  parts={parts}
                  running={selected.status === "running"}
                  onToolApproval={ignoreApproval}
                />
              )}
              {selected.status === "running" && parts.length === 0 && !selected.output && <p className="loops-run-working">{t("Working…")}</p>}
              {selected.output && <AgentAnswerText className="loops-run-answer" data={data} markdown={selected.output} />}
              {selected.status === "failed" && (
                <div className="loops-run-error" role="alert">
                  <XCircle size={16} />
                  <div>
                    <strong>{t("Loop couldn't run")}</strong>
                    {selected.error && <p data-i18n-ignore>{selected.error}</p>}
                  </div>
                </div>
              )}
              {selected.status !== "running" && (
                <div className="loops-run-feedback" role="group" aria-label={t("Rate this run")}>
                  <button aria-label={t("Good response")} aria-pressed={feedback === "up"} className={feedback === "up" ? "is-active" : undefined} onClick={() => void rate("up")}>
                    <ThumbsUp size={14} />
                  </button>
                  <button aria-label={t("Bad response")} aria-pressed={feedback === "down"} className={feedback === "down" ? "is-active" : undefined} onClick={() => void rate("down")}>
                    <ThumbsDown size={14} />
                  </button>
                </div>
              )}
              {(selected.replies ?? []).map((reply) => (
                <RunReply key={reply.id} data={data} reply={reply} />
              ))}
            </div>
          )}
          </div>
          {selected && <RunReplyComposer key={selected.id} loopId={loopId} run={selected} busy={selected.status === "running" || replying} onReplied={applyRun} />}
        </section>
      </div>
    </main>
  );
}

/** A reply on the run and the agent's answer to it. */
function RunReply({ data, reply }: { data: BootstrapData; reply: LoopRunReply }) {
  const { t } = useI18n();
  const author = data.users.find((user) => user.id === reply.userId);
  const parts = runParts(reply);
  return (
    <div className="loops-run-reply">
      <div className="loops-run-reply-message">
        <UserAvatar avatarUrl={author?.avatarUrl} className="avatar loops-avatar" name={author ? author.displayName || author.name : t("Unknown user")} />
        <p data-i18n-ignore>{reply.body}</p>
      </div>
      {parts.length > 0 && (
        <AgentWorkGroup
          className="loops-run-work"
          message={{ durationMs: reply.finishedAt ? Math.max(1000, Date.parse(reply.finishedAt) - Date.parse(reply.createdAt)) : undefined }}
          parts={parts}
          running={reply.status === "running"}
          onToolApproval={ignoreApproval}
        />
      )}
      {reply.status === "running" && parts.length === 0 && !reply.output && <p className="loops-run-working">{t("Working…")}</p>}
      {reply.output && <AgentAnswerText className="loops-run-answer" data={data} markdown={reply.output} />}
      {reply.status === "failed" && (
        <div className="loops-run-error" role="alert">
          <XCircle size={16} />
          <div>
            <strong>{t("The agent couldn't reply")}</strong>
            {reply.error && <p data-i18n-ignore>{reply.error}</p>}
          </div>
        </div>
      )}
    </div>
  );
}

/** Linear's "Reply…" composer under the run: continues the run's agent conversation. */
function RunReplyComposer({ loopId, run, busy, onReplied }: { loopId: string; run: LoopRun; busy: boolean; onReplied: (run: LoopRun) => void }) {
  const { t } = useI18n();
  const [body, setBody] = useState("");
  const [sending, setSending] = useState(false);
  const canSend = Boolean(body.trim()) && !busy && !sending;
  const send = async () => {
    if (!canSend) return;
    setSending(true);
    try {
      onReplied(await replyToLoopRun(loopId, run.id, body.trim()));
      setBody("");
    } catch (reason) {
      toast.error(reason instanceof Error ? reason.message : t("Could not send reply"));
    } finally {
      setSending(false);
    }
  };
  return (
    <form
      className="loops-run-composer"
      onSubmit={(event) => {
        event.preventDefault();
        void send();
      }}
    >
      <textarea
        aria-label={t("Reply…")}
        placeholder={t(busy ? "The agent is working…" : "Reply…")}
        value={body}
        rows={2}
        onChange={(event) => setBody(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
            event.preventDefault();
            void send();
          }
        }}
      />
      <div className="loops-run-composer-actions">
        <button type="submit" className="loops-run-send" aria-label={t("Send message")} title={t("Send message")} disabled={!canSend}>
          {sending ? <LoaderCircle size={14} className="loops-run-status is-running" /> : <ArrowUp size={14} />}
        </button>
      </div>
    </form>
  );
}
