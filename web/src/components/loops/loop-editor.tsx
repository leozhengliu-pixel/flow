import { useCallback, useEffect, useRef, useState } from "react";
import { ChevronDown, Link2, Plus, Settings2, Sparkles, X } from "lucide-react";
import { toast } from "sonner";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { SelectControl } from "@/components/ui/select-control";
import { Toggle } from "@/components/ui/toggle";
import { ViewGlyph, ViewIconPicker } from "@/components/views/view-icon-picker";
import { TeamIcon } from "@/components/issue/issue-icons";
import { createLoop, deleteDraft, deleteLoop, getLoop, updateLoop, type LoopMutation } from "@/lib/api";
import { loopPath, loopsPath, newLoopPath } from "@/lib/app-routes";
import { useI18n } from "@/i18n/i18n";
import type { BootstrapData, Loop, LoopCodeAccess, LoopTriggerType } from "@/types/flow";
import { LoopAgentPanel } from "./loop-agent-panel";
import { LoopBreadcrumb } from "./loop-breadcrumb";
import { LoopIcon, loopIconColor } from "./loop-glyph";
import { LoopInstructionsEditor } from "./loop-instructions-editor";
import { activeTeams, copyText, loopBuilderFirstMessage, loopUrl, markLoopAgentHandoff, takeLoopAgentAutostart, useLoopConfig } from "./loop-data";
import { ENTITY_NAMES, LOOP_PERMISSION_COPY, configStrings, defaultScheduleConfig, instructionsPlaceholder, isLoopDraft, loopTeamId } from "./loop-model";
import { LoopTriggerEditor } from "./loop-trigger";
import { LoopVersionsDialog } from "./loop-versions";

const DEFAULT_COLOR = "#d9b84b";

type Form = {
  name: string;
  icon: string;
  color: string;
  level: Loop["level"];
  teamId?: string;
  triggerType: LoopTriggerType;
  triggerConfig: Record<string, unknown>;
  instructions: string;
  instructionsData?: Record<string, unknown>;
  connectorIds: string[];
  teamAccess: Loop["teamAccess"];
  allowChangesOutsideTrigger: boolean;
  allowExternalSync: boolean;
  webSearch: boolean;
  codeAccess: LoopCodeAccess;
};

function formOf(loop: Partial<Loop>): Form {
  return {
    name: loop.name ?? "",
    icon: loop.icon || "Automation",
    color: loop.color || DEFAULT_COLOR,
    level: loop.level ?? "workspace",
    teamId: loop.level === "team" ? loopTeamId(loop as Loop) : undefined,
    triggerType: loop.triggerType ?? "schedule",
    triggerConfig: loop.triggerConfig ?? defaultScheduleConfig(),
    instructions: loop.instructions ?? "",
    instructionsData: loop.instructionsData,
    connectorIds: loop.connectorIds ?? [],
    teamAccess: loop.teamAccess ?? "allPublic",
    allowChangesOutsideTrigger: loop.allowChangesOutsideTrigger ?? true,
    allowExternalSync: loop.allowExternalSync ?? false,
    webSearch: loop.webSearch ?? false,
    codeAccess: loop.codeAccess ?? "read",
  };
}

function mutationOf(form: Form): LoopMutation {
  return {
    name: form.name.trim(),
    icon: form.icon,
    color: form.color,
    level: form.level,
    teamId: form.level === "team" ? form.teamId : undefined,
    triggerType: form.triggerType,
    // Schedules run in the editor's timezone.
    triggerConfig: form.triggerType === "schedule" ? { ...form.triggerConfig, timezone: Intl.DateTimeFormat().resolvedOptions().timeZone } : form.triggerConfig,
    instructions: form.instructions,
    ...(form.instructionsData ? { instructionsData: form.instructionsData } : {}),
    connectorIds: form.connectorIds,
    teamAccess: form.teamAccess,
    allowChangesOutsideTrigger: form.triggerType === "schedule" ? false : form.allowChangesOutsideTrigger,
    allowExternalSync: form.allowExternalSync,
    webSearch: form.webSearch,
    codeAccess: form.codeAccess,
  };
}

/** Loads the loop behind the editor: a draft (`?draftId=`) or a published loop being edited. */
function useEditorLoop(data: BootstrapData, id: string | undefined) {
  const cached = id ? data.loops.find((item) => item.id === id) : undefined;
  const [loop, setLoop] = useState<Loop | undefined>(cached);
  const [missing, setMissing] = useState(false);
  const refetch = useCallback(async () => {
    if (!id) return undefined;
    try {
      const next = await getLoop(id);
      setLoop(next);
      setMissing(false);
      return next;
    } catch {
      if (!cached) setMissing(true);
      return undefined;
    }
  }, [cached, id]);
  useEffect(() => {
    void refetch();
  }, [refetch]);
  return { loop, setLoop, missing, refetch };
}

export function LoopEditor({
  data,
  draftId,
  loopId,
  onOpenSidebar,
  onNavigate,
  onReload,
}: {
  data: BootstrapData;
  draftId?: string;
  loopId?: string;
  onOpenSidebar: () => void;
  onNavigate: (path: string) => void;
  onReload: (changed?: Loop) => Promise<void>;
}) {
  const { t } = useI18n();
  const legacyDraft = draftId ? data.drafts.find((item) => item.id === draftId && item.type === "loop") : undefined;
  const [id, setId] = useState(legacyDraft ? undefined : (draftId ?? loopId));
  const { loop, setLoop, missing, refetch } = useEditorLoop(data, id);
  const migrating = useRef(false);

  // Drafts saved by older builds live in the Drafts table; move them onto a draft loop.
  useEffect(() => {
    if (!legacyDraft || migrating.current) return;
    migrating.current = true;
    const values = (legacyDraft.metadata ?? {}) as Partial<Loop>;
    const level = values.level === "team" && loopTeamId(values as Loop) ? "team" : "workspace";
    void createLoop({
      ...mutationOf(formOf({ ...values, name: values.name ?? legacyDraft.title, instructions: values.instructions ?? legacyDraft.body })),
      level,
      teamId: level === "team" ? loopTeamId(values as Loop) : undefined,
      status: "draft",
    })
      .then(async (created) => {
        await deleteDraft(legacyDraft.id).catch(() => undefined);
        window.history.replaceState({}, "", `${newLoopPath(data.workspace.urlKey)}?draftId=${encodeURIComponent(created.id)}`);
        setId(created.id);
        setLoop(created);
      })
      .catch((error) => toast.error(error instanceof Error ? error.message : t("Could not open draft")));
  }, [data.workspace.urlKey, legacyDraft, setLoop, t]);

  if (missing)
    return (
      <main className="main-panel loops-page" aria-label={t("Loop not found")}>
        <div className="loops-not-found">
          <h2>{t("Loop not found")}</h2>
          <p>{t("The requested loop is not available.")}</p>
          <button className="loops-primary-button" onClick={() => onNavigate(loopsPath(data.workspace.urlKey))}>
            {t("Back to loops")}
          </button>
        </div>
      </main>
    );
  if (!loop)
    return (
      <main className="main-panel loops-editor-page" aria-label={t("New loop")} aria-busy="true">
        <header className="loops-editor-topbar">
          <span className="loops-breadcrumb">{t("Loops")}</span>
        </header>
      </main>
    );
  return <LoopEditorForm key={loop.id} data={data} loop={loop} onLoopChange={setLoop} refetch={refetch} onOpenSidebar={onOpenSidebar} onNavigate={onNavigate} onReload={onReload} />;
}

function LoopEditorForm({
  data,
  loop,
  onLoopChange,
  refetch,
  onOpenSidebar,
  onNavigate,
  onReload,
}: {
  data: BootstrapData;
  loop: Loop;
  onLoopChange: (loop: Loop) => void;
  refetch: () => Promise<Loop | undefined>;
  onOpenSidebar: () => void;
  onNavigate: (path: string) => void;
  onReload: (changed?: Loop) => Promise<void>;
}) {
  const { t } = useI18n();
  const draft = isLoopDraft(loop);
  const workspace = data.workspace.urlKey;
  const [form, setForm] = useState<Form>(() => formOf(loop));
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);
  const [autoMessage] = useState(() => (draft && takeLoopAgentAutostart(loop.id) ? loopBuilderFirstMessage(loop) : ""));
  const agentDraft = Boolean(loop.templateId || loop.sourcePrompt);
  const [agentOpen, setAgentOpen] = useState(() => draft && (Boolean(autoMessage) || agentDraft));
  // Bumped when the loop builder rewrites the draft so the rich editor reloads its content.
  const [instructionsRevision, setInstructionsRevision] = useState(0);
  const [versionsOpen, setVersionsOpen] = useState(false);
  const publishing = useRef(false);
  const loopConfig = useLoopConfig();

  const update = (patch: Partial<Form>) => {
    setForm((current) => ({ ...current, ...patch }));
    setDirty(true);
  };

  // Drafts save as you type, like Linear.
  useEffect(() => {
    if (!draft || !dirty) return;
    const timer = window.setTimeout(() => {
      setDirty(false);
      void updateLoop(loop.id, mutationOf(form))
        .then((next) => onLoopChange(next))
        .catch((error) => toast.error(error instanceof Error ? error.message : t("Could not save draft")));
    }, 500);
    return () => window.clearTimeout(timer);
  }, [dirty, draft, form, loop.id, onLoopChange, t]);

  // The loop builder edits the same draft; show its changes in place.
  const syncFromServer = useCallback(async () => {
    const next = await refetch();
    if (next) {
      setForm(formOf(next));
      setInstructionsRevision((value) => value + 1);
      setDirty(false);
    }
  }, [refetch]);

  const finish = async (next: Loop) => {
    onLoopChange(next);
    // The saved loop replaces its cached copy synchronously; the loops list
    // refreshes in the background so navigation never waits on it.
    void onReload(next).catch(() => undefined);
    onNavigate(loopPath(workspace, next.id));
  };

  const submit = async () => {
    if (!form.name.trim() || !form.instructions.trim() || saving) return;
    setSaving(true);
    try {
      const input = mutationOf(form);
      const next = await updateLoop(loop.id, draft ? { ...input, instructions: form.instructions.trim(), status: "published", enabled: true } : { ...input, instructions: form.instructions.trim() });
      await finish(next);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not save loop"));
    } finally {
      setSaving(false);
    }
  };

  const cancel = async () => {
    if (saving) return;
    if (!draft) {
      onNavigate(loopPath(workspace, loop.id));
      return;
    }
    // An untouched draft is discarded; anything else stays in Drafts.
    const pristine = !form.name.trim() && !form.instructions.trim() && !loop.templateId && !loop.sourcePrompt;
    if (pristine) await deleteLoop(loop.id).catch(() => undefined);
    else if (dirty) await updateLoop(loop.id, mutationOf(form)).catch(() => undefined);
    void onReload().catch(() => undefined);
    onNavigate(loopsPath(workspace));
  };

  const teams = activeTeams(data.teams);
  const levelTeam = form.level === "team" ? teams.find((team) => team.id === form.teamId) : undefined;
  const accessTeamIds = configStrings(form.triggerConfig, "teamIds");
  const entity = ENTITY_NAMES[form.triggerType].toLowerCase();
  const canSubmit = Boolean(form.name.trim() && form.instructions.trim()) && !saving;

  return (
    <main className="main-panel loops-editor-page" aria-label={draft ? t("New loop") : t("Edit loop")}>
      <header className="loops-editor-topbar">
        <button className="loops-mobile-menu" aria-label={t("Open sidebar")} data-sidebar-trigger onClick={onOpenSidebar}>
          <Settings2 />
        </button>
        {draft ? (
          <nav className="loops-breadcrumb" aria-label={t("Breadcrumb")}>
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
            <h2>{t("New loop")}</h2>
          </nav>
        ) : (
          <>
            <LoopBreadcrumb data={data} loop={loop} loopId={loop.id} current={t("Edit")} onNavigate={onNavigate} />
            <div className="loops-topbar-actions">
              <button className="loops-icon-button is-plain" aria-label={t("Copy link")} title={t("Copy link")} onClick={() => void copyText(loopUrl(workspace, loop), t("Link copied"))}>
                <Link2 size={14} />
              </button>
            </div>
          </>
        )}
      </header>
      <div className="loops-editor-body">
        <div className="loops-editor-scroll">
          <div className="loops-editor-heading">
            <ViewIconPicker
              ariaLabel={t("Loop icon")}
              color={loopIconColor({ templateId: loop.templateId, icon: form.icon, color: form.color }) ?? form.color}
              icon={form.icon}
              onChange={(visual) => update({ icon: visual.icon, color: visual.color })}
              triggerContent={loop.templateId ? <LoopIcon source={{ templateId: loop.templateId, icon: form.icon, color: form.color }} size={18} /> : undefined}
              prependIcons={["Automation", "CustomView"]}
              triggerClassName="loops-icon-picker"
            />
            <input aria-label={t("Loop name")} className="loops-name-input" value={form.name} onChange={(event) => update({ name: event.target.value })} placeholder={t("Loop name")} />
            {/* Linear picks the location while creating; published loops move from the loop menu. */}
            {draft && (
            <DropdownMenu>
              <DropdownMenuTrigger className="loops-level-button" aria-label={t("Loop level")}>
                {levelTeam ? <TeamIcon team={levelTeam} size={14} /> : <ViewGlyph color="currentColor" icon="Team" />}
                <span data-i18n-ignore={levelTeam ? true : undefined}>{levelTeam ? levelTeam.name : t("Workspace")}</span>
                <ChevronDown size={12} />
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="loops-menu">
                <DropdownMenuItem onSelect={() => update({ level: "workspace", teamId: undefined })}>
                  <ViewGlyph color="currentColor" icon="Team" />
                  {t("Workspace")}
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                {teams.map((team) => (
                  <DropdownMenuItem key={team.id} onSelect={() => update({ level: "team", teamId: team.id })}>
                    <TeamIcon team={team} size={14} />
                    <span data-i18n-ignore>{team.name}</span>
                    <small className="loops-menu-detail">{team.key}</small>
                  </DropdownMenuItem>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
            )}
          </div>

          <section className="loops-section" aria-label={t("Trigger")}>
            <h3 className="loops-section-title">{t("Trigger")}</h3>
            <div className="loops-card is-trigger">
              <LoopTriggerEditor data={data} level={form.level} triggerType={form.triggerType} config={form.triggerConfig} onChange={(triggerType, triggerConfig) => update({ triggerType, triggerConfig })} />
            </div>
          </section>

          <section className="loops-section" aria-label={t("Instructions")}>
            <div className="loops-section-heading">
              <h3 className="loops-section-title">{t("Instructions")}</h3>
              <button className="loops-compose-button" type="button" onClick={() => setAgentOpen(true)}>
                <Sparkles size={14} />
                {t(agentDraft ? "Configure with Agent" : "Compose with Agent")}
              </button>
            </div>
            <div className="loops-card is-instructions">
            <LoopInstructionsEditor
              key={instructionsRevision}
              ariaLabel={t("Instructions")}
              data={data}
              value={form.instructions}
              valueData={form.instructionsData}
              placeholder={t(instructionsPlaceholder(form.triggerType))}
              onChange={(instructions, instructionsData) => update({ instructions, instructionsData })}
            />
            </div>
          </section>

          <section className="loops-section" aria-label={t("Connectors")}>
            <h3 className="loops-section-title">{t("Connectors")}</h3>
            <div className="loops-card is-connectors">
            <div className="loops-connectors-row">
            {form.connectorIds.length ? (
              <div className="loops-connector-list">
                {form.connectorIds.map((connectorId) => {
                  const item = data.integrationConnections.find((connection) => connection.id === connectorId);
                  return (
                    <span key={connectorId}>
                      <span data-i18n-ignore>{item?.name ?? connectorId}</span>
                      <button type="button" aria-label={t("Remove connector")} onClick={() => update({ connectorIds: form.connectorIds.filter((value) => value !== connectorId) })}>
                        <X size={12} />
                      </button>
                    </span>
                  );
                })}
              </div>
            ) : (
              <p className="loops-no-connectors">{t("No connectors added")}</p>
            )}
              <DropdownMenu>
                <DropdownMenuTrigger className="loops-add-connector">
                  <Plus size={14} />
                  {t("Add connector")}
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="loops-menu">
                  {data.integrationConnections.length ? (
                    data.integrationConnections.map((item) => (
                      <DropdownMenuCheckboxItem
                        key={item.id}
                        checked={form.connectorIds.includes(item.id)}
                        onCheckedChange={(checked) => update({ connectorIds: checked ? [...form.connectorIds, item.id] : form.connectorIds.filter((value) => value !== item.id) })}
                      >
                        <span data-i18n-ignore>{item.name}</span>
                      </DropdownMenuCheckboxItem>
                    ))
                  ) : (
                    <DropdownMenuItem disabled>{t("No connected applications")}</DropdownMenuItem>
                  )}
                </DropdownMenuContent>
              </DropdownMenu>
            </div>
            </div>
          </section>

          <section className="loops-section" aria-label={t("Permissions")}>
            <h3 className="loops-section-title">{t("Permissions")}</h3>
            <div className="loops-card loops-permissions">
            <div className="loops-permission-row">
              <span>
                <strong>{t("Team access")}</strong>
                <small>{t(LOOP_PERMISSION_COPY.teamAccess)}</small>
              </span>
              <DropdownMenu>
                <DropdownMenuTrigger className="loops-select-button" aria-label={t("Team access")}>
                  <span>{form.teamAccess === "selected" && accessTeamIds.length ? t(`${accessTeamIds.length} teams`) : t("All public teams")}</span>
                  <ChevronDown size={12} />
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="loops-menu">
                  <DropdownMenuCheckboxItem checked={form.teamAccess === "allPublic"} onCheckedChange={() => update({ teamAccess: "allPublic" })}>
                    {t("All public teams")}
                  </DropdownMenuCheckboxItem>
                  <DropdownMenuSeparator />
                  {teams.map((team) => (
                    <DropdownMenuCheckboxItem
                      key={team.id}
                      checked={form.teamAccess === "selected" && accessTeamIds.includes(team.id)}
                      onSelect={(event) => event.preventDefault()}
                      onCheckedChange={(checked) => {
                        const ids = checked ? [...new Set([...accessTeamIds, team.id])] : accessTeamIds.filter((value) => value !== team.id);
                        update({ teamAccess: ids.length ? "selected" : "allPublic", triggerConfig: { ...form.triggerConfig, teamIds: ids } });
                      }}
                    >
                      <TeamIcon team={team} size={14} />
                      <span data-i18n-ignore>{team.name}</span>
                    </DropdownMenuCheckboxItem>
                  ))}
                </DropdownMenuContent>
              </DropdownMenu>
            </div>
            {form.triggerType !== "schedule" && (
              <div className="loops-permission-row">
                <span>
                  <strong>{t(`Allow changes outside triggering ${entity}`)}</strong>
                  <small>{t(LOOP_PERMISSION_COPY.outsideTrigger)}</small>
                </span>
                <Toggle size="regular" checked={form.allowChangesOutsideTrigger} label={t(`Allow changes outside triggering ${entity}`)} onChange={(checked) => update({ allowChangesOutsideTrigger: checked })} />
              </div>
            )}
            <div className="loops-permission-row">
              <span>
                <strong>{t("Web search")}</strong>
                <small>{t(LOOP_PERMISSION_COPY.webSearch)}</small>
                {loopConfig && !loopConfig.webSearchAvailable && (
                  <small className="loops-permission-hint" role="note">
                    {t("Web search isn't configured for this workspace.")}{" "}
                    <a
                      href={`/${workspace}/settings/loops#web-search`}
                      onClick={(event) => {
                        event.preventDefault();
                        onNavigate(`/${workspace}/settings/loops#web-search`);
                      }}
                    >
                      {t("Learn more")}
                    </a>
                  </small>
                )}
              </span>
              <Toggle size="regular" checked={form.webSearch} label={t("Web search")} onChange={(checked) => update({ webSearch: checked })} />
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
              <SelectControl
                className="loops-access-select"
                label={t("Access code")}
                value={form.codeAccess}
                onChange={(value) => update({ codeAccess: value as LoopCodeAccess })}
                options={[
                  { value: "disabled", label: t("Disabled") },
                  { value: "read", label: t("Read") },
                  { value: "readWrite", label: t("Read & write") },
                ]}
              />
            </div>
            <div className="loops-permission-row">
              <span>
                <strong>{t("Allow changes to externally synced issues and comments")}</strong>
                <small>{t(LOOP_PERMISSION_COPY.externalSync)}</small>
              </span>
              <Toggle size="regular" checked={form.allowExternalSync} label={t("Allow changes to externally synced issues and comments")} onChange={(checked) => update({ allowExternalSync: checked })} />
            </div>
            <p className="loops-permission-footnote">
              {t("Trusted sources for inbound content are managed in")}{" "}
              <a
                href={`/${workspace}/settings/loops`}
                onClick={(event) => {
                  event.preventDefault();
                  onNavigate(`/${workspace}/settings/loops`);
                }}
              >
                {t("Loops settings")}
              </a>
            </p>
            </div>
          </section>

          {/* Linear: "Published versions" on the left and Publish on the right, no Cancel (the breadcrumb leaves the editor). */}
          <footer className={`loops-editor-footer${draft ? "" : " is-published"}`}>
            {draft ? (
              <button className="loops-ghost-button" type="button" onClick={() => void cancel()}>
                {t("Cancel")}
              </button>
            ) : (
              <button className="loops-versions-button" type="button" onClick={() => setVersionsOpen(true)}>
                {t("Published versions")}
              </button>
            )}
            <button className="loops-primary-button" type="button" disabled={!canSubmit} onClick={() => void submit()}>
              {saving ? t("Saving…") : draft ? t("Create loop") : t("Publish")}
            </button>
          </footer>
        </div>
        {agentOpen && (
          <LoopAgentPanel
            data={data}
            loopId={loop.id}
            title={form.name || undefined}
            visual={{ templateId: loop.templateId, icon: form.icon, color: form.color }}
            open={agentOpen}
            autoMessage={autoMessage}
            onClose={() => setAgentOpen(false)}
            onLoopSaved={() => void syncFromServer()}
            onPublished={(result) => {
              if (publishing.current) return;
              publishing.current = true;
              // The builder keeps replying on the server; the loop page docks the conversation and follows it.
              markLoopAgentHandoff(result.id ?? loop.id);
              void refetch().then((next) => finish(next ?? { ...loop, status: "published", enabled: true, id: result.id ?? loop.id }));
            }}
            onNavigateLoop={(result) => onNavigate(loopPath(workspace, result.id ?? loop.id))}
          />
        )}
      </div>
      {!draft && (
        <LoopVersionsDialog
          data={data}
          loop={loop}
          open={versionsOpen}
          onOpenChange={setVersionsOpen}
          onNavigate={onNavigate}
          onRestored={(next) => {
            // The restored version replaces what is being edited.
            onLoopChange(next);
            setForm(formOf(next));
            setInstructionsRevision((value) => value + 1);
            setDirty(false);
            void onReload(next).catch(() => undefined);
          }}
        />
      )}
    </main>
  );
}
