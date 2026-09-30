import { useEffect, useState } from "react";
import { ChevronRight, History, Link2, MoreHorizontal, Pencil, Play, Settings2 } from "lucide-react";
import { toast } from "sonner";
import { Toggle } from "@/components/ui/toggle";
import { UserAvatar } from "@/components/ui/user-avatar";
import { TeamIcon } from "@/components/issue/issue-icons";
import { ViewGlyph } from "@/components/views/view-icon-picker";
import { getLoop, runLoopNow, updateLoop } from "@/lib/api";
import { editLoopPath, loopPath, loopRunPath, loopsPath, newLoopPath } from "@/lib/app-routes";
import { useI18n } from "@/i18n/i18n";
import type { BootstrapData, Loop, LoopTriggerType } from "@/types/flow";
import { LoopActionsMenu } from "./loop-actions";
import { LoopBreadcrumb } from "./loop-breadcrumb";
import { copyText, loopOwner, loopUrl, takeLoopAgentHandoff, useLoopRecord } from "./loop-data";
import { LoopAgentPanel } from "./loop-agent-panel";
import { LoopIcon, loopIconColor } from "./loop-glyph";
import { LoopInstructionsEditor } from "./loop-instructions-editor";
import { ENTITY_NAMES, LOOP_PERMISSION_COPY, configStrings, isLoopDraft, loopTeam, relativeTime } from "./loop-model";
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
      return data.teams.map((item) => ({ id: item.id, label: item.name, detail: item.key, detailPosition: "after", entity: true, icon: <TeamIcon team={item} size={14} /> }));
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
  onReload: (changed?: Loop) => Promise<void>;
}) {
  const { t } = useI18n();
  const { loop, setLoop, missing } = useLoopRecord(data, loopId);
  const [expanded, setExpanded] = useState(false);
  const [versionsOpen, setVersionsOpen] = useState(() => new URLSearchParams(window.location.search).get("versions") === "1");
  // Published from the loop builder: keep its conversation docked while it finishes the reply.
  const [agentOpen, setAgentOpen] = useState(() => takeLoopAgentHandoff(loopId));
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
      const next = await updateLoop(loop.id, { enabled });
      setLoop(next);
      void onReload(next).catch(() => undefined);
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
        <LoopBreadcrumb
          data={data}
          loop={loop}
          loopId={loop.id}
          onNavigate={onNavigate}
          actions={
          <LoopActionsMenu
            align="start"
            data={data}
            loop={loop}
            onNavigate={onNavigate}
            onChanged={(next) => {
              setLoop(next);
              void onReload(next).catch(() => undefined);
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
          }
        />
        <div className="loops-topbar-actions">
          <span className="loops-access-note">
            <ViewGlyph color="currentColor" icon="Team" />
            {t(team ? "All team members can edit" : "All workspace members can edit")}
          </span>
          <button className="loops-icon-button is-plain" aria-label={t("Copy link")} title={t("Copy link")} onClick={() => void copyText(loopUrl(workspace, loop), t("Link copied"))}>
            <Link2 size={14} />
          </button>
        </div>
      </header>
      <div className="loops-detail-body">
      <div className="loops-detail-scroll">
        <div className="loops-detail">
          <div className="loops-detail-actions">
            <button className="loops-detail-button" onClick={() => onNavigate(editLoopPath(workspace, loop.id))}>
              <Pencil size={14} />
              {t("Edit")}
            </button>
            {loop.enabled && (
              <button className="loops-detail-button is-primary" disabled={busy} onClick={run}>
                <Play size={14} />
                {t("Run now")}
              </button>
            )}
          </div>
          <div className="loops-detail-icon" style={{ color: loopIconColor(loop) }}>
            <LoopIcon source={loop} size={16} />
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
              <span>{t("Enabled")}</span>
              <Toggle checked={loop.enabled} label={t("Enabled")} onChange={(checked) => void toggle(checked)} />
            </label>
            {owner && (
              <span className="loops-detail-owner">
                {t("Owned by")}
                <span className="loops-detail-owner-chip">
                  <UserAvatar avatarUrl={owner.avatarUrl} className="avatar loops-avatar" name={owner.displayName || owner.name} />
                  <span data-i18n-ignore>{owner.displayName || owner.name}</span>
                </span>
              </span>
            )}
            <span className="loops-detail-dot" aria-hidden="true">
              ·
            </span>
            <span className="loops-detail-updated" title={new Date(loop.updatedAt).toLocaleString()}>
              {t("Last update")} {relativeTime(loop.updatedAt, undefined, t)}
            </span>
            {loop.version !== undefined && (
              <>
                <span className="loops-detail-dot" aria-hidden="true">
                  ·
                </span>
                <button className="loops-detail-version" type="button" onClick={() => setVersionsOpen(true)}>
                  {t("Version {version}").replace("{version}", String(loop.version))}
                </button>
              </>
            )}
          </div>

          <button className="loops-card loops-run-summary" onClick={() => onNavigate(loopRunPath(workspace, loop.id))}>
            <span className="loops-run-summary-icon" aria-hidden="true">
              <History size={16} />
            </span>
            <span className="loops-run-summary-copy">
              <strong>{t("Run history")}</strong>
              <small>
                {t("Ran")} {runs} {t(runs === 1 ? "time over the last 30 days" : "times over the last 30 days")}
              </small>
            </span>
            <ChevronRight size={16} />
          </button>

          <section className="loops-section" aria-label={t("Trigger")}>
            <h3 className="loops-section-title">{t("Trigger")}</h3>
            <div className="loops-card is-trigger">
              <LoopTriggerEditor data={data} level={loop.level} triggerType={loop.triggerType} config={loop.triggerConfig ?? {}} readOnly />
            </div>
          </section>

          <section className="loops-section" aria-label={t("Instructions")}>
            <h3 className="loops-section-title">{t("Instructions")}</h3>
            <div className={`loops-card is-instructions${expanded || !longInstructions ? "" : " is-collapsed"}`}>
              <div className={`loops-instructions-text${expanded || !longInstructions ? "" : " is-collapsed"}`} data-i18n-ignore>
                {loop.instructions ? (
                  <LoopInstructionsEditor readOnly ariaLabel={t("Instructions")} data={data} value={loop.instructions} valueData={loop.instructionsData} onNavigate={onNavigate} />
                ) : (
                  <span className="loops-muted">{t("No instructions")}</span>
                )}
              </div>
              {longInstructions && (
                <button className="loops-expand-button" type="button" aria-expanded={expanded} onClick={() => setExpanded((value) => !value)}>
                  {t(expanded ? "Collapse" : "Expand")}
                </button>
              )}
            </div>
          </section>

          <LoopPermissionsSummary data={data} loop={loop} onNavigate={onNavigate} />
        </div>
      </div>
      {agentOpen && (
        <LoopAgentPanel
          data={data}
          loopId={loop.id}
          title={loop.name || undefined}
          visual={{ templateId: loop.templateId, icon: loop.icon, color: loop.color }}
          open={agentOpen}
          onClose={() => setAgentOpen(false)}
          onLoopSaved={() => {
            void getLoop(loop.id)
              .then(setLoop)
              .catch(() => undefined);
          }}
          onNavigateLoop={(result) => result.id && result.id !== loop.id && onNavigate(loopPath(workspace, result.id))}
        />
      )}
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
          void onReload(next).catch(() => undefined);
        }}
      />
    </main>
  );
}

/** Linear shows the loop's permissions read-only under the instructions. */
function LoopPermissionsSummary({ data, loop, onNavigate }: { data: BootstrapData; loop: Loop; onNavigate: (path: string) => void }) {
  const { t } = useI18n();
  const workspace = data.workspace.urlKey;
  const teamIds = configStrings(loop.triggerConfig, "teamIds");
  const entity = ENTITY_NAMES[loop.triggerType].toLowerCase();
  const code = loop.codeAccess ?? "disabled";
  const noop = () => undefined;
  return (
    <section className="loops-section" aria-label={t("Permissions")}>
      <h3 className="loops-section-title">{t("Permissions")}</h3>
      <div className="loops-card loops-permissions is-readonly">
        <div className="loops-permission-row">
          <span>
            <strong>{t("Team access")}</strong>
            <small>{t(LOOP_PERMISSION_COPY.teamAccess)}</small>
          </span>
          <span className="loops-permission-value">{loop.teamAccess === "selected" && teamIds.length ? t(`${teamIds.length} teams`) : t("All public teams")}</span>
        </div>
        {loop.triggerType !== "schedule" && (
          <div className="loops-permission-row">
            <span>
              <strong>{t(`Allow changes outside triggering ${entity}`)}</strong>
              <small>{t(LOOP_PERMISSION_COPY.outsideTrigger)}</small>
            </span>
            <Toggle disabled size="regular" checked={loop.allowChangesOutsideTrigger} label={t(`Allow changes outside triggering ${entity}`)} onChange={noop} />
          </div>
        )}
        <div className="loops-permission-row">
          <span>
            <strong>{t("Web search")}</strong>
            <small>{t(LOOP_PERMISSION_COPY.webSearch)}</small>
          </span>
          <Toggle disabled size="regular" checked={loop.webSearch ?? false} label={t("Web search")} onChange={noop} />
        </div>
        <div className="loops-permission-row">
          <span>
            <strong>{t("Access code")}</strong>
            <small>
              {t(LOOP_PERMISSION_COPY.codeAccess)}{" "}
              <a
                href={`/${workspace}/settings/loops`}
                onClick={(event) => {
                  event.preventDefault();
                  onNavigate(`/${workspace}/settings/loops`);
                }}
              >
                {t(LOOP_PERMISSION_COPY.codeAccessLink)}
              </a>
            </small>
          </span>
          <span className="loops-permission-value">{t(code === "readWrite" ? "Read & write" : code === "read" ? "Read" : "Disabled")}</span>
        </div>
        <div className="loops-permission-row">
          <span>
            <strong>{t("Allow changes to externally synced issues and comments")}</strong>
            <small>{t(LOOP_PERMISSION_COPY.externalSync)}</small>
          </span>
          <Toggle disabled size="regular" checked={loop.allowExternalSync} label={t("Allow changes to externally synced issues and comments")} onChange={noop} />
        </div>
      </div>
    </section>
  );
}
