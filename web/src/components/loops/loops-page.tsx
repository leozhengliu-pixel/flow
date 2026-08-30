import { useMemo, useState } from "react";
import {
  Check,
  ChevronDown,
  Clock3,
  MoreHorizontal,
  Plus,
  Repeat2,
  Settings2,
  Sparkles,
  Trash2,
  X,
} from "lucide-react";
import { DisplayIcon, FilterIcon, PlusIcon } from "@/components/ui/view-action-icons";
import {
  createLoop,
  deleteLoop,
  sendAgentMessage,
  updateLoop,
  type LoopMutation,
} from "@/lib/api";
import { loopPath, loopsPath, newLoopPath } from "@/lib/app-routes";
import type { BootstrapData, Loop } from "@/types/flow";
import { useI18n } from "@/i18n/i18n";
import { toast } from "sonner";
import "./loops-page.css";

type Props = {
  data: BootstrapData;
  loopId?: string;
  editing: boolean;
  onOpenSidebar: () => void;
  onNavigate: (path: string) => void;
  onReload: () => Promise<void>;
};
const triggerLabels: Record<Loop["triggerType"], string> = {
  schedule: "Schedule",
  issue: "An issue",
  project: "A project",
  initiative: "An initiative",
  cycle: "A cycle",
};
const iconChoices = ["repeat", "sparkles", "circle", "zap", "bot"];

export function LoopsPage({
  data,
  loopId,
  editing,
  onOpenSidebar,
  onNavigate,
  onReload,
}: Props) {
  const { t } = useI18n();
  const loop = loopId
    ? data.loops.find((item) => item.id === loopId)
    : undefined;
  if (editing && loopId && !loop)
    return (
      <main className="main-panel loops-page" aria-label={t("Loop not found")}>
        <div className="loops-empty">
          <h2>{t("Loop not found")}</h2>
          <p>{t("The requested loop is not available.")}</p>
          <button
            className="loops-primary-button"
            onClick={() => onNavigate(loopsPath(data.workspace.urlKey))}
          >
            {t("Back to loops")}
          </button>
        </div>
      </main>
    );
  return editing ? (
    <LoopEditor
      data={data}
      loop={loop}
      onOpenSidebar={onOpenSidebar}
      onNavigate={onNavigate}
      onReload={onReload}
    />
  ) : (
    <LoopList
      data={data}
      onOpenSidebar={onOpenSidebar}
      onNavigate={onNavigate}
      onReload={onReload}
    />
  );
}

function LoopList({
  data,
  onOpenSidebar,
  onNavigate,
  onReload,
}: Omit<Props, "editing" | "loopId">) {
  const { t } = useI18n();
  const [filterOpen, setFilterOpen] = useState(false);
  const [displayOpen, setDisplayOpen] = useState(false);
  const [activeOnly, setActiveOnly] = useState(false);
  const [showTrigger, setShowTrigger] = useState(true);
  const [showLastRun, setShowLastRun] = useState(true);
  const [confirm, setConfirm] = useState<Loop>();
  const loops = useMemo(
    () => data.loops.filter((item) => !activeOnly || item.enabled),
    [activeOnly, data.loops],
  );
  const remove = async () => {
    if (!confirm) return;
    await deleteLoop(confirm.id);
    setConfirm(undefined);
    await onReload();
  };
  return (
    <main className="main-panel loops-page" aria-label={t("Loops")}>
      <header className="loops-topbar">
        <button
          className="loops-mobile-menu"
          aria-label={t("Open sidebar")}
          onClick={onOpenSidebar}
        >
          <Settings2 />
        </button>
        <div className="loops-title">
          <h2>{t("Loops")}</h2>
        </div>
        <div className="loops-topbar-actions">
          <button
            className="loops-new-button"
            onClick={() => onNavigate(newLoopPath(data.workspace.urlKey))}
          >
            <PlusIcon />
            {t("New loop")}
          </button>
        </div>
      </header>
      <div className="loops-toolbar">
        <div className="loops-toolbar-left">
          <button
            className={`loops-icon-button ${filterOpen ? "is-open" : ""}`}
            aria-label={t("Add filter")}
            aria-expanded={filterOpen}
            onClick={() => setFilterOpen((value) => !value)}
          >
            <FilterIcon />
          </button>
          {filterOpen && (
            <div className="loops-popover loops-filter-popover">
              <button
                onClick={() => {
                  setActiveOnly((value) => !value);
                  setFilterOpen(false);
                }}
              >
                <span className="loops-check">
                  {activeOnly && <Check size={12} />}
                </span>
                {t("Enabled")}
              </button>
              <button
                onClick={() => {
                  setActiveOnly(false);
                  setFilterOpen(false);
                }}
              >
                <X size={12} />
                {t("Clear filters")}
              </button>
            </div>
          )}
          {activeOnly && (
            <div className="loops-filter-chip">
              <span>{t("Enabled")}</span>
              <button
                aria-label={t("Clear filters")}
                onClick={() => setActiveOnly(false)}
              >
                <X size={12} />
              </button>
            </div>
          )}
        </div>
        <button
          className={`loops-icon-button ${displayOpen ? "is-open" : ""}`}
          aria-label={t("Display options")}
          aria-expanded={displayOpen}
          onClick={() => setDisplayOpen((value) => !value)}
        >
          <DisplayIcon />
        </button>
        {displayOpen && (
          <div className="loops-popover loops-display-popover">
            <strong>{t("Display properties")}</strong>
            <button onClick={() => setShowTrigger((value) => !value)}>
              <span className="loops-check">
                {showTrigger && <Check size={12} />}
              </span>
              {t("Trigger")}
            </button>
            <button onClick={() => setShowLastRun((value) => !value)}>
              <span className="loops-check">
                {showLastRun && <Check size={12} />}
              </span>
              {t("Last run")}
            </button>
          </div>
        )}
      </div>
      {loops.length ? (
        <div className="loops-list">
          {loops.map((item) => (
            <LoopRow
              key={item.id}
              loop={item}
              showTrigger={showTrigger}
              showLastRun={showLastRun}
              onOpen={() =>
                onNavigate(loopPath(data.workspace.urlKey, item.id))
              }
              onToggle={async () => {
                await updateLoop(item.id, { enabled: !item.enabled });
                await onReload();
              }}
              onDelete={() => setConfirm(item)}
            />
          ))}
        </div>
      ) : (
        <LoopEmpty
          onCreate={() => onNavigate(newLoopPath(data.workspace.urlKey))}
        />
      )}
      {confirm && (
        <div className="loops-modal-backdrop">
          <section
            className="loops-confirm"
            role="dialog"
            aria-modal="true"
            aria-labelledby="loop-delete-title"
          >
            <h2 id="loop-delete-title">{t("Delete loop?")}</h2>
            <p>
              {t(
                "This loop and its configuration will be permanently deleted.",
              )}
            </p>
            <footer>
              <button onClick={() => setConfirm(undefined)}>
                {t("Cancel")}
              </button>
              <button className="is-danger" onClick={() => void remove()}>
                {t("Delete")}
              </button>
            </footer>
          </section>
        </div>
      )}
    </main>
  );
}

function LoopRow({
  loop,
  showTrigger,
  showLastRun,
  onOpen,
  onToggle,
  onDelete,
}: {
  loop: Loop;
  showTrigger: boolean;
  showLastRun: boolean;
  onOpen: () => void;
  onToggle: () => Promise<void>;
  onDelete: () => void;
}) {
  const { t } = useI18n();
  const [menu, setMenu] = useState(false);
  return (
    <article className={`loops-row ${!loop.enabled ? "is-disabled" : ""}`}>
      <button className="loops-row-main" onClick={onOpen}>
        <span className="loops-row-icon">
          <Repeat2 size={16} />
        </span>
        <span className="loops-row-copy">
          <strong>{loop.name}</strong>
          {showTrigger && (
            <small>
              {triggerLabels[loop.triggerType]} ·{" "}
              {loop.level === "workspace" ? t("Workspace") : t("Team")}
            </small>
          )}
        </span>
        <span className={`loops-status ${loop.enabled ? "is-enabled" : ""}`}>
          <i />
          {loop.enabled ? t("Enabled") : t("Disabled")}
        </span>
        {showLastRun && (
          <span className="loops-row-updated">
            <Clock3 size={13} />
            {new Date(loop.updatedAt).toLocaleDateString()}
          </span>
        )}
      </button>
      <button
        className="loops-row-menu"
        aria-label={t("Open actions")}
        aria-expanded={menu}
        onClick={() => setMenu((value) => !value)}
      >
        <MoreHorizontal size={16} />
      </button>
      {menu && (
        <div className="loops-popover loops-row-popover">
          <button
            onClick={() => {
              setMenu(false);
              onOpen();
            }}
          >
            <Settings2 size={14} />
            {t("Edit loop")}
          </button>
          <button
            onClick={() => {
              setMenu(false);
              void onToggle();
            }}
          >
            {loop.enabled ? <X size={14} /> : <Check size={14} />}
            {loop.enabled ? t("Disable loop") : t("Enable loop")}
          </button>
          <button
            className="is-danger"
            onClick={() => {
              setMenu(false);
              onDelete();
            }}
          >
            <Trash2 size={14} />
            {t("Delete loop")}
          </button>
        </div>
      )}
    </article>
  );
}

function LoopEmpty({ onCreate }: { onCreate: () => void }) {
  const { t } = useI18n();
  return (
    <div className="loops-empty">
      <LoopEmptyIllustration />
      <div className="loops-empty-body">
        <div className="loops-empty-copy">
          <h2>{t("Loops")}</h2>
          <div className="loops-empty-paragraphs">
            <p>{t("Loops let Flow Agent take action based on an event or a schedule. Use loops to automate manual work for your team and keep your process moving.")}</p>
            <p>{t("See documentation for details and examples.")}</p>
          </div>
        </div>
        <div className="loops-empty-actions">
        <button className="loops-primary-button" onClick={onCreate}>
          {t("Create new loop")}
        </button>
        <a
          href="https://flow.app/docs/loops"
          target="_blank"
          rel="noreferrer"
          className="loops-secondary-button"
        >
          {t("Docs and Examples")}
        </a>
        </div>
      </div>
    </div>
  );
}

function LoopEmptyIllustration() {
  return <svg aria-label="No loops illustration" className="loops-empty-illustration" fill="none" viewBox="0 0 120 120">
    <defs><linearGradient id="loops-empty-gradient" gradientUnits="userSpaceOnUse" x1="0" x2="120" y1="0" y2="120" spreadMethod="repeat"><stop offset="0" stopColor="var(--theme-text-primary)"/><stop offset=".38" stopColor="var(--theme-text-primary)"/><stop offset=".5" stopColor="var(--theme-text-secondary)"/><stop offset=".62" stopColor="var(--theme-text-primary)"/><stop offset="1" stopColor="var(--theme-text-primary)"/><animateTransform attributeName="gradientTransform" dur="1.8s" from="0 0" repeatCount="3" to="120 120" type="translate"/></linearGradient></defs>
    <ellipse cx="60" cy="60" rx="51" ry="22" stroke="url(#loops-empty-gradient)" strokeWidth="1.5" transform="rotate(45 60 60)"/>
    <ellipse cx="60" cy="60" rx="51" ry="22" stroke="var(--theme-text-secondary)" strokeWidth="1.5" transform="rotate(-45 60 60)"/>
    <ellipse cx="60" cy="60" rx="50" ry="21" stroke="var(--theme-border-strong)" strokeWidth="1.5" transform="rotate(90 60 60)"/>
  </svg>
}

function LoopEditor({
  data,
  loop,
  onOpenSidebar,
  onNavigate,
  onReload,
}: {
  data: BootstrapData;
  loop?: Loop;
  onOpenSidebar: () => void;
  onNavigate: (path: string) => void;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  const editing = Boolean(loop);
  const [name, setName] = useState(loop?.name ?? "");
  const [icon, setIcon] = useState(loop?.icon ?? "repeat");
  const [level, setLevel] = useState<Loop["level"]>(loop?.level ?? "workspace");
  const [triggerType, setTriggerType] = useState<Loop["triggerType"]>(
    loop?.triggerType ?? "schedule",
  );
  const [triggerConfig, setTriggerConfig] = useState<Record<string, unknown>>(
    loop?.triggerConfig ?? { interval: 1, unit: "day", time: "10:00" },
  );
  const [instructions, setInstructions] = useState(loop?.instructions ?? "");
  const [connectorIds, setConnectorIds] = useState<string[]>(
    loop?.connectorIds ?? [],
  );
  const [teamAccess, setTeamAccess] = useState<Loop["teamAccess"]>(
    loop?.teamAccess ?? "allPublic",
  );
  const [allowOutside, setAllowOutside] = useState(
    loop?.allowChangesOutsideTrigger ?? true,
  );
  const [allowExternal, setAllowExternal] = useState(
    loop?.allowExternalSync ?? false,
  );
  const [composeOpen, setComposeOpen] = useState(false);
  const [composePrompt, setComposePrompt] = useState("");
  const [composeBusy, setComposeBusy] = useState(false);
  const [scopeOpen, setScopeOpen] = useState(false);
  const [connectorOpen, setConnectorOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const setConfig = (key: string, value: unknown) =>
    setTriggerConfig((current) => ({ ...current, [key]: value }));
  const clearConfig = (key: string) =>
    setTriggerConfig((current) => {
      const next = { ...current };
      delete next[key];
      return next;
    });
  const triggerObject =
    triggerType === "issue"
      ? "issue"
      : triggerType === "project"
        ? "project"
        : triggerType === "initiative"
          ? "initiative"
          : "cycle";
  const scopeTeamIds = Array.isArray(triggerConfig.teamIds)
    ? (triggerConfig.teamIds as string[])
    : [];
  const toggleScopeTeam = (teamId: string) =>
    setConfig(
      "teamIds",
      scopeTeamIds.includes(teamId)
        ? scopeTeamIds.filter((id) => id !== teamId)
        : [...scopeTeamIds, teamId],
    );
  const submit = async () => {
    if (!name.trim() || !instructions.trim()) return;
    setSaving(true);
    try {
      const input: LoopMutation = {
        name: name.trim(),
        icon,
        level,
        triggerType,
        triggerConfig,
        instructions: instructions.trim(),
        connectorIds,
        teamAccess,
        allowChangesOutsideTrigger:
          triggerType === "schedule" ? false : allowOutside,
        allowExternalSync: allowExternal,
        enabled: loop?.enabled ?? true,
      };
      if (loop) await updateLoop(loop.id, input);
      else await createLoop(input as LoopMutation & { name: string });
      await onReload();
      onNavigate(loopsPath(data.workspace.urlKey));
    } finally {
      setSaving(false);
    }
  };
  const compose = async () => {
    if (!composePrompt.trim() || composeBusy) return;
    setComposeBusy(true);
    try {
      const response = await sendAgentMessage({
        message: `Write only the concise, executable instructions for a Flow automation loop. Trigger: ${triggerLabels[triggerType]}. User intent: ${composePrompt.trim()}`,
        issueIds: [],
        history: [],
      });
      setInstructions(response.message.trim());
      setComposeOpen(false);
      setComposePrompt("");
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t("Could not compose instructions"),
      );
    } finally {
      setComposeBusy(false);
    }
  };
  const filter = triggerConfig.filter ? (
    <div className="loops-filter-builder">
      <select
        aria-label={t("Filter field")}
        value={String(triggerConfig.filterField ?? "status")}
        onChange={(event) => setConfig("filterField", event.target.value)}
      >
        <option value="status">{t("Status")}</option>
        <option value="priority">{t("Priority")}</option>
        <option value="label">{t("Label")}</option>
        <option value="assignee">{t("Assignee")}</option>
      </select>
      <select
        aria-label={t("Filter operator")}
        value={String(triggerConfig.filterOperator ?? "is")}
        onChange={(event) => setConfig("filterOperator", event.target.value)}
      >
        <option value="is">{t("is")}</option>
        <option value="isNot">{t("is not")}</option>
      </select>
      <input
        aria-label={t("Filter value")}
        value={String(triggerConfig.filterValue ?? "")}
        onChange={(event) => setConfig("filterValue", event.target.value)}
        placeholder={t("Value")}
      />
      <button
        aria-label={t("Remove filter")}
        onClick={() => clearConfig("filter")}
      >
        <X size={13} />
      </button>
    </div>
  ) : (
    <button
      className="loops-add-filter"
      onClick={() => setConfig("filter", "new")}
    >
      <Plus size={13} />
      {t("Add filter")}
    </button>
  );
  return (
    <main
      className="main-panel loops-editor-page"
      aria-label={editing ? t("Edit loop") : t("New loop")}
    >
      <header className="loops-editor-topbar">
        <button
          className="loops-mobile-menu"
          aria-label={t("Open sidebar")}
          onClick={onOpenSidebar}
        >
          <Settings2 />
        </button>
        <a
          href={loopsPath(data.workspace.urlKey)}
          onClick={(event) => {
            event.preventDefault();
            onNavigate(loopsPath(data.workspace.urlKey));
          }}
        >
          {t("Loops")}
        </a>
        <span>›</span>
        <h1>{editing ? t("Edit loop") : t("New loop")}</h1>
        <div className="loops-editor-actions">
          <button onClick={() => onNavigate(loopsPath(data.workspace.urlKey))}>
            {t("Cancel")}
          </button>
          <button
            className="loops-primary-button"
            disabled={saving || !name.trim() || !instructions.trim()}
            onClick={() => void submit()}
          >
            {saving
              ? t("Saving…")
              : editing
                ? t("Save changes")
                : t("Create loop")}
          </button>
        </div>
      </header>
      <div className="loops-editor-scroll">
        <div className="loops-editor-heading">
          <button
            className="loops-icon-picker"
            aria-label={t("Loop icon")}
            onClick={() =>
              setIcon(
                iconChoices[
                  (iconChoices.indexOf(icon) + 1) % iconChoices.length
                ],
              )
            }
          >
            <Repeat2 size={16} />
          </button>
          <input
            aria-label={t("Loop name")}
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder={t("Loop name")}
          />
          <select
            aria-label={t("Loop level")}
            value={level}
            onChange={(event) => setLevel(event.target.value as Loop["level"])}
          >
            <option value="workspace">{t("Workspace")}</option>
            <option value="team">{t("Team")}</option>
          </select>
        </div>
        <section className="loops-editor-section is-trigger">
          <h2>{t("Trigger")}</h2>
          <div className="loops-trigger-row">
            <select
              aria-label={t("Trigger type")}
              value={triggerType}
              onChange={(event) =>
                setTriggerType(event.target.value as Loop["triggerType"])
              }
            >
              {(Object.keys(triggerLabels) as Loop["triggerType"][]).map(
                (value) => (
                  <option value={value} key={value}>
                    {t(triggerLabels[value])}
                  </option>
                ),
              )}
            </select>
            {triggerType === "schedule" ? (
              <>
                <span>{t("Starting")}</span>
                <input
                  type="date"
                  value={String(
                    triggerConfig.starting ??
                      new Date().toISOString().slice(0, 10),
                  )}
                  onChange={(event) =>
                    setConfig("starting", event.target.value)
                  }
                />
                <span>{t("every")}</span>
                <input
                  className="loops-small-input"
                  type="number"
                  min="1"
                  value={Number(triggerConfig.interval ?? 1)}
                  onChange={(event) =>
                    setConfig("interval", Number(event.target.value))
                  }
                />
                <select
                  value={String(triggerConfig.unit ?? "day")}
                  onChange={(event) => setConfig("unit", event.target.value)}
                >
                  <option value="day">{t("day")}</option>
                  <option value="week">{t("week")}</option>
                  <option value="month">{t("month")}</option>
                </select>
                <span>{t("at")}</span>
                <input
                  type="time"
                  value={String(triggerConfig.time ?? "10:00")}
                  onChange={(event) => setConfig("time", event.target.value)}
                />
              </>
            ) : (
              <>
                <span>{t("is")}</span>
                <select
                  value={String(
                    triggerConfig.action ??
                      (triggerType === "cycle"
                        ? "created"
                        : "created or updated"),
                  )}
                  onChange={(event) => setConfig("action", event.target.value)}
                >
                  <option value="created">{t("created")}</option>
                  <option value="created or updated">
                    {t("created or updated")}
                  </option>
                </select>
                <span>
                  {triggerType === "initiative" ? t("with") : t("in")}
                </span>
                <span className="loops-scope-picker">
                  <button
                    className="loops-select-button"
                    onClick={() => setScopeOpen((value) => !value)}
                  >
                    {scopeTeamIds.length
                      ? `${scopeTeamIds.length} ${t("selected")}`
                      : t("Select teams…")}
                    <ChevronDown size={12} />
                  </button>
                  {scopeOpen && (
                    <div className="loops-popover loops-scope-popover">
                      {data.teams.map((team) => (
                        <button
                          key={team.id}
                          onClick={() => toggleScopeTeam(team.id)}
                        >
                          <span className="loops-check">
                            {scopeTeamIds.includes(team.id) && (
                              <Check size={12} />
                            )}
                          </span>
                          <span data-i18n-ignore>{team.name}</span>
                        </button>
                      ))}
                    </div>
                  )}
                </span>
              </>
            )}
          </div>
          {triggerType !== "schedule" && filter}
        </section>
        <section className="loops-editor-section is-instructions">
          <div className="loops-section-heading">
            <h2>{t("Instructions")}</h2>
            <button
              className="loops-compose-button"
              onClick={() => {
                setComposePrompt("");
                setComposeOpen(true);
              }}
            >
              <Sparkles size={13} />
              {t("Compose with Agent")}
            </button>
          </div>
          <textarea
            aria-label={t("Agent prompt")}
            value={instructions}
            onChange={(event) => setInstructions(event.target.value)}
            placeholder={t("Define your Agent behavior (e.g., 'Assign security issues to @alice, tag customer requests with the customer label')…")}
          />
        </section>
        <section className="loops-editor-section is-connectors">
          <div className="loops-section-heading">
            <h2>{t("Connectors")}</h2>
            <button
              className="loops-add-connector"
              onClick={() => setConnectorOpen((value) => !value)}
            >
              <Plus size={14} />
              {t("Add connector")}
            </button>
          </div>
          {connectorIds.length ? (
            <div className="loops-connector-list">
              {connectorIds.map((id) => {
                const item = data.integrationConnections.find(
                  (connection) => connection.id === id,
                );
                return (
                  <span key={id}>
                    {item?.name ?? id}
                    <button
                      onClick={() =>
                        setConnectorIds((current) =>
                          current.filter((value) => value !== id),
                        )
                      }
                    >
                      <X size={12} />
                    </button>
                  </span>
                );
              })}
            </div>
          ) : (
            <h3 className="loops-no-connectors">{t("No connectors added")}</h3>
          )}
          {connectorOpen && (
            <div className="loops-popover loops-connector-popover">
              {data.integrationConnections.length ? (
                data.integrationConnections.map((item) => (
                  <button
                    key={item.id}
                    onClick={() => {
                      setConnectorIds((current) =>
                        current.includes(item.id)
                          ? current
                          : [...current, item.id],
                      );
                      setConnectorOpen(false);
                    }}
                  >
                    <span className="loops-connector-dot" />
                    {item.name}
                    <Check
                      size={13}
                      style={{
                        opacity: connectorIds.includes(item.id) ? 1 : 0,
                      }}
                    />
                  </button>
                ))
              ) : (
                <p>{t("No connected applications")}</p>
              )}
            </div>
          )}
        </section>
        <section className="loops-editor-section loops-permissions">
          <h2>{t("Permissions")}</h2>
          <label>
            <span>
              <strong>{t("Team access")}</strong>
              <small>
                {t("Choose which team’s data are available to this loop")}
              </small>
            </span>
            <select
              value={teamAccess}
              onChange={(event) =>
                setTeamAccess(event.target.value as Loop["teamAccess"])
              }
            >
              <option value="allPublic">{t("All public teams")}</option>
              <option value="selected">{t("Selected teams")}</option>
            </select>
          </label>
          {triggerType !== "schedule" && (
            <label>
              <span>
                <strong>
                  {t(`Allow changes outside triggering ${triggerObject}`)}
                </strong>
                <small>
                  {t(
                    "Allow this loop to modify data beyond the item that triggered it",
                  )}
                </small>
              </span>
              <input
                type="checkbox"
                checked={allowOutside}
                onChange={(event) => setAllowOutside(event.target.checked)}
              />
            </label>
          )}
          <label>
            <span>
              <strong>{t("Externally synced issues and comments")}</strong>
              <small>
                {t(
                  "Allow posting to externally synced issues and comments. Posted content may be visible to users outside of Flow.",
                )}
              </small>
            </span>
            <input
              type="checkbox"
              checked={allowExternal}
              onChange={(event) => setAllowExternal(event.target.checked)}
            />
          </label>
          <p className="loops-settings-note">
            {t("Coding sessions can be enabled for Loops in")}{" "}
            <a href={`/${data.workspace.urlKey}/settings/ai/automation`}>
              {t("Loops settings")}
            </a>
            .
          </p>
        </section>
      </div>
      {composeOpen && (
        <div className="loops-modal-backdrop">
          <section
            className="loops-compose-dialog"
            role="dialog"
            aria-modal="true"
          >
            <h2>{t("Compose with Agent")}</h2>
            <p>
              {t(
                "Describe what this loop should do and Flow will turn it into instructions.",
              )}
            </p>
            <textarea
              autoFocus
              value={composePrompt}
              onChange={(event) => setComposePrompt(event.target.value)}
              placeholder={t("Describe the workflow…")}
            />
            <footer>
              <button
                disabled={composeBusy}
                onClick={() => setComposeOpen(false)}
              >
                {t("Cancel")}
              </button>
              <button
                className="loops-primary-button"
                disabled={composeBusy || !composePrompt.trim()}
                onClick={() => void compose()}
              >
                {composeBusy ? t("Composing…") : t("Use instructions")}
              </button>
            </footer>
          </section>
        </div>
      )}
    </main>
  );
}
