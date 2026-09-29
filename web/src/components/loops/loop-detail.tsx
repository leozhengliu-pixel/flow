import { useEffect, useState } from "react";
import { ChevronRight, Link2, MoreHorizontal, Pencil, Play, Settings2 } from "lucide-react";
import { toast } from "sonner";
import { Toggle } from "@/components/ui/toggle";
import { UserAvatar } from "@/components/ui/user-avatar";
import { TeamIcon } from "@/components/issue/issue-icons";
import { ViewGlyph } from "@/components/views/view-icon-picker";
import { runLoopNow, updateLoop } from "@/lib/api";
import { editLoopPath, loopRunPath, loopsPath, newLoopPath, teamLoopsPath } from "@/lib/app-routes";
import { useI18n } from "@/i18n/i18n";
import type { BootstrapData, Loop, LoopTriggerType } from "@/types/flow";
import { LoopActionsMenu } from "./loop-actions";
import { copyText, loopOwner, loopUrl, useLoopRecord } from "./loop-data";
import { LoopGlyph } from "./loop-glyph";
import { LoopInstructionsEditor } from "./loop-instructions-editor";
import { configStrings, isLoopDraft, loopTeam, relativeTime } from "./loop-model";
import { LoopCommandPicker, RunLoopOnPicker, type PickerItem } from "./loop-pickers";
import { LoopTriggerEditor } from "./loop-trigger";
import { LoopVersionsDialog } from "./loop-versions";

/** Starts a run: schedules immediately, event loops after picking the entity. Resolves to the run page path. */
function useRunLoop(data: BootstrapData, loop: Loop | undefined, onNavigate: (path: string) => void) {
  const { t } = useI18n();
  const [picking, setPicking] = useState(false);
  const [busy, setBusy] = useState(false);
  const start = async (target?: { entityType: string; entityId: string }) => {
    if (!loop || busy) return;
    setBusy(true);
    try {
      const run = await runLoopNow(loop.id, target);
      setPicking(false);
      onNavigate(loopRunPath(data.workspace.urlKey, loop.id, run.id));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not start the loop"));
    } finally {
      setBusy(false);
    }
  };
  const run = () => {
    if (!loop) return;
    if (loop.triggerType === "schedule") void start();
    else setPicking(true);
  };
  const scope = loop ? configStrings(loop.triggerConfig, "teamIds") : [];
  const picker = loop && loop.triggerType !== "schedule" && (
    loop.triggerType === "issue" ? (
      <RunLoopOnPicker data={data} open={picking} teamIds={scope} onOpenChange={setPicking} onSelect={(issue) => void start({ entityType: "issue", entityId: issue.id })} />
    ) : (
      <LoopCommandPicker
        open={picking}
        title={`Search for ${loop.triggerType} to run loop on…`}
        placeholder={`Search for ${loop.triggerType} to run loop on…`}
        items={entityItems(data, loop.triggerType)}
        onOpenChange={setPicking}
        onSelect={(item) => void start({ entityType: loop.triggerType, entityId: item.id })}
        footer={
          <span>
            <kbd>↵</kbd> {t("Run loop on")}
          </span>
        }
      />
    )
  );
  return { run, picker, busy };
}

function entityItems(data: BootstrapData, type: LoopTriggerType): PickerItem[] {
  switch (type) {
    case "project":
      return data.projects.map((item) => ({ id: item.id, label: item.name, entity: true }));
    case "initiative":
      return (data.initiatives ?? []).map((item) => ({ id: item.id, label: item.name, entity: true }));
    case "release":
      return (data.releases ?? []).map((item) => ({ id: item.id, label: item.name, detail: item.version, entity: true }));
    case "team":
      return data.teams.map((item) => ({ id: item.id, label: item.name, detail: item.key, entity: true, icon: <TeamIcon team={item} size={14} /> }));
    case "cycle":
      return (data.cycles ?? []).map((item) => ({ id: item.id, label: item.name || `Cycle ${item.number}`, entity: true }));
    default:
      return [];
  }
}

/** Linear's loop page: header, run history summary, read-only trigger and collapsed instructions. */
export function LoopDetail({
  data,
  loopId,
  onOpenSidebar,
  onNavigate,
  onReload,
}: {
  data: BootstrapData;
  loopId: string;
  onOpenSidebar: () => void;
  onNavigate: (path: string) => void;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  const { loop, setLoop, missing } = useLoopRecord(data, loopId);
  const [expanded, setExpanded] = useState(false);
  const [versionsOpen, setVersionsOpen] = useState(() => new URLSearchParams(window.location.search).get("versions") === "1");
  const { run, picker, busy } = useRunLoop(data, loop, onNavigate);
  const workspace = data.workspace.urlKey;
  useEffect(() => {
    if (loop && isLoopDraft(loop)) onNavigate(`${newLoopPath(workspace)}?draftId=${encodeURIComponent(loop.id)}`);
  }, [loop, onNavigate, workspace]);
  if (missing)
    return (
      <main className="main-panel loops-page" aria-label={t("Loop not found")}>
        <div className="loops-not-found">
          <h2>{t("Loop not found")}</h2>
          <p>{t("The requested loop is not available.")}</p>
          <button className="loops-primary-button" onClick={() => onNavigate(loopsPath(workspace))}>
            {t("Back to loops")}
          </button>
        </div>
      </main>
    );
  if (!loop) return <main className="main-panel loops-page" aria-busy="true" aria-label={t("Loop")} />;
  const team = loopTeam(data, loop);
  const owner = loopOwner(data, loop);
  const toggle = async (enabled: boolean) => {
    setLoop({ ...loop, enabled });
    try {
      setLoop(await updateLoop(loop.id, { enabled }));
      void onReload().catch(() => undefined);
    } catch (error) {
      setLoop(loop);
      toast.error(error instanceof Error ? error.message : t("Could not update loop"));
    }
  };
  const runs = loop.runCount30d ?? 0;
  const longInstructions = loop.instructions.split("\n").length > 6 || loop.instructions.length > 480;
  return (
    <main className="main-panel loops-detail-page" aria-label={loop.name || t("Loop")}>
      <header className="loops-editor-topbar">
        <button className="loops-mobile-menu" aria-label={t("Open sidebar")} data-sidebar-trigger onClick={onOpenSidebar}>
          <Settings2 />
        </button>
        <nav className="loops-breadcrumb" aria-label={t("Breadcrumb")}>
          {team ? (
            <a
              href={teamLoopsPath(workspace, team.key)}
              onClick={(event) => {
                event.preventDefault();
                onNavigate(teamLoopsPath(workspace, team.key));
              }}
            >
              <TeamIcon team={team} size={14} />
              <span data-i18n-ignore>{team.name}</span>
            </a>
          ) : (
            <span className="loops-breadcrumb-scope">
              <ViewGlyph color="currentColor" icon="Team" />
              {t("Workspace")}
            </span>
          )}
          <span aria-hidden="true">›</span>
          <a
            href={loopsPath(workspace)}
            onClick={(event) => {
              event.preventDefault();
              onNavigate(loopsPath(workspace));
            }}
          >
            {t("Loops")}
          </a>
          <span aria-hidden="true">›</span>
          <h2 data-i18n-ignore>{loop.name || t("Untitled loop")}</h2>
          <LoopActionsMenu
            align="start"
            data={data}
            loop={loop}
            onNavigate={onNavigate}
            onChanged={(next) => {
              setLoop(next);
              void onReload().catch(() => undefined);
            }}
            onDeleted={() => void onReload().catch(() => undefined)}
            onRun={run}
            onShowVersions={() => setVersionsOpen(true)}
            trigger={
              <button className="loops-icon-button is-plain" aria-label={t("Loop actions")}>
                <MoreHorizontal size={16} />
              </button>
            }
          />
        </nav>
        <div className="loops-topbar-actions">
          <span className="loops-access-note">{t(team ? "All team members can edit" : "All workspace members can edit")}</span>
          <button className="loops-icon-button is-plain" aria-label={t("Copy link")} title={t("Copy link")} onClick={() => void copyText(loopUrl(workspace, loop), t("Link copied"))}>
            <Link2 size={14} />
          </button>
        </div>
      </header>
      <div className="loops-detail-scroll">
        <div className="loops-detail">
          <div className="loops-detail-actions">
            <button className="loops-secondary-button" onClick={() => onNavigate(editLoopPath(workspace, loop.id))}>
              <Pencil size={13} />
              {t("Edit")}
            </button>
            <button className="loops-primary-button" disabled={!loop.enabled || busy} title={loop.enabled ? undefined : t("Enable the loop to run it")} onClick={run}>
              <Play size={13} />
              {t("Run now")}
            </button>
          </div>
          <div className="loops-detail-icon" style={{ color: loop.color || undefined }}>
            <LoopGlyph icon={loop.icon || "Automation"} size={20} />
          </div>
          <h1 className="loops-detail-title" data-i18n-ignore>
            {loop.name || t("Untitled loop")}
          </h1>
          {loop.description && (
            <p className="loops-detail-description" data-i18n-ignore>
              {loop.description}
            </p>
          )}
          <div className="loops-detail-meta">
            <label className="loops-enabled">
              <Toggle checked={loop.enabled} label={t("Enabled")} onChange={(checked) => void toggle(checked)} />
              <span>{t("Enabled")}</span>
            </label>
            {owner && (
              <span className="loops-detail-owner">
                {t("Owned by")}
                <UserAvatar avatarUrl={owner.avatarUrl} className="avatar loops-avatar" name={owner.displayName || owner.name} />
                <span data-i18n-ignore>{owner.displayName || owner.name}</span>
              </span>
            )}
            <span className="loops-detail-updated" title={new Date(loop.updatedAt).toLocaleString()}>
              {t("Last update")} {relativeTime(loop.updatedAt, undefined, t)}
            </span>
            {loop.version !== undefined && (
              <button className="loops-detail-version" type="button" onClick={() => setVersionsOpen(true)}>
                {t("Version {version}").replace("{version}", String(loop.version))}
              </button>
            )}
          </div>

          <button className="loops-card loops-run-summary" onClick={() => onNavigate(loopRunPath(workspace, loop.id))}>
            <span>
              <strong>{t("Run history")}</strong>
              <small>
                {t("Ran")} {runs} {t(runs === 1 ? "time over the last 30 days" : "times over the last 30 days")}
              </small>
            </span>
            <ChevronRight size={16} />
          </button>

          <section className="loops-card is-trigger" aria-label={t("Trigger")}>
            <h3>{t("Trigger")}</h3>
            <LoopTriggerEditor data={data} triggerType={loop.triggerType} config={loop.triggerConfig ?? {}} readOnly />
          </section>

          <section className="loops-card is-instructions" aria-label={t("Instructions")}>
            <div className="loops-card-heading">
              <h3>{t("Instructions")}</h3>
              {longInstructions && (
                <button className="loops-compose-button" type="button" aria-expanded={expanded} onClick={() => setExpanded((value) => !value)}>
                  {t(expanded ? "Collapse" : "Expand")}
                </button>
              )}
            </div>
            <div className={`loops-instructions-text${expanded || !longInstructions ? "" : " is-collapsed"}`} data-i18n-ignore>
              {loop.instructions ? (
                <LoopInstructionsEditor readOnly ariaLabel={t("Instructions")} data={data} value={loop.instructions} valueData={loop.instructionsData} onNavigate={onNavigate} />
              ) : (
                <span className="loops-muted">{t("No instructions")}</span>
              )}
            </div>
          </section>
        </div>
      </div>
      {picker}
      <LoopVersionsDialog
        data={data}
        loop={loop}
        open={versionsOpen}
        onNavigate={onNavigate}
        onOpenChange={(open) => {
          setVersionsOpen(open);
          if (!open && window.location.search.includes("versions=1")) window.history.replaceState(window.history.state, "", window.location.pathname);
        }}
        onRestored={(next) => {
          setLoop(next);
          void onReload().catch(() => undefined);
        }}
      />
    </main>
  );
}
