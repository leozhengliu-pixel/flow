import { useEffect, useMemo, useState } from "react";
import { ArrowDown, ArrowUp, MoreHorizontal, Search, Settings2 } from "lucide-react";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { DisplayIcon, FilterIcon, PlusIcon } from "@/components/ui/view-action-icons";
import { UserAvatar } from "@/components/ui/user-avatar";
import { TeamIcon } from "@/components/issue/issue-icons";
import { ViewGlyph } from "@/components/views/view-icon-picker";
import { loopPath, newLoopPath } from "@/lib/app-routes";
import { useI18n } from "@/i18n/i18n";
import type { BootstrapData, Loop, LoopTriggerType, Team } from "@/types/flow";
import { LoopActionsMenu } from "./loop-actions";
import { LoopCreateDialog, LoopCreateHub } from "./loop-create-hub";
import { LoopIcon, loopIconColor } from "./loop-glyph";
import { loopOwner, useLoops } from "./loop-data";
import { ENTITY_NAMES, isLoopDraft, loopTeamId, relativeTime, triggerSummary } from "./loop-model";

type SortKey = "name" | "trigger" | "owner" | "runs" | "lastRun";
type Column = Exclude<SortKey, "name">;
const COLUMNS: { id: Column; label: string }[] = [
  { id: "trigger", label: "Trigger" },
  { id: "owner", label: "Owner" },
  { id: "runs", label: "Runs (30d)" },
  { id: "lastRun", label: "Last executed" },
];
type StatusFilter = "enabled" | "disabled" | "draft";

export function LoopList({
  data,
  embedded,
  teamId,
  onOpenSidebar,
  onNavigate,
  onReload,
  createOpen: createOpenProp,
}: {
  data: BootstrapData;
  embedded: boolean;
  teamId?: string;
  onOpenSidebar: () => void;
  onNavigate: (path: string) => void;
  onReload: () => Promise<void>;
  /** `/loops/new` without a draft opens the hub dialog. */
  createOpen?: boolean;
}) {
  const { t } = useI18n();
  const [allLoops, setLoops] = useLoops(data);
  const [storedTab, setTab] = useState<"mine" | "all">(() => readTab());
  // The team page has no tabs; it lists every loop of the team.
  const tab = embedded ? "all" : storedTab;
  const [query, setQuery] = useState("");
  const [statusFilters, setStatusFilters] = useState<StatusFilter[]>([]);
  const [typeFilters, setTypeFilters] = useState<LoopTriggerType[]>([]);
  const [hidden, setHidden] = useState<Column[]>([]);
  const [grouped, setGrouped] = useState(true);
  const [sort, setSort] = useState<{ key: SortKey; desc: boolean }>({ key: "name", desc: false });
  const [createOpen, setCreateOpen] = useState(Boolean(createOpenProp));
  useEffect(() => {
    if (createOpenProp) setCreateOpen(true);
  }, [createOpenProp]);
  useEffect(() => {
    try {
      localStorage.setItem("flow:loops:tab", storedTab);
    } catch {
      /* storage unavailable */
    }
  }, [storedTab]);

  // Drafts are private to their author until they are created.
  const visible = useMemo(
    () =>
      allLoops.filter((loop) => {
        if (isLoopDraft(loop) && loop.creator?.id !== data.viewer.id) return false;
        if (teamId && loopTeamId(loop) !== teamId) return false;
        return true;
      }),
    [allLoops, data.viewer.id, teamId],
  );
  const loops = useMemo(() => {
    const needle = query.trim().toLowerCase();
    const rows = visible.filter((loop) => {
      if (tab === "mine" && (loop.ownerId ?? loop.creator?.id) !== data.viewer.id) return false;
      if (needle && !`${loop.name} ${loop.description ?? ""}`.toLowerCase().includes(needle)) return false;
      if (statusFilters.length) {
        const status: StatusFilter = isLoopDraft(loop) ? "draft" : loop.enabled ? "enabled" : "disabled";
        if (!statusFilters.includes(status)) return false;
      }
      if (typeFilters.length && !typeFilters.includes(loop.triggerType)) return false;
      return true;
    });
    const value = (loop: Loop): string | number => {
      switch (sort.key) {
        case "trigger":
          return triggerSummary(loop).toLowerCase();
        case "owner":
          return (loopOwner(data, loop)?.displayName ?? "").toLowerCase();
        case "runs":
          return loop.runCount30d ?? 0;
        case "lastRun":
          return loop.lastRunAt ? Date.parse(loop.lastRunAt) : 0;
        default:
          return (loop.name || "").toLowerCase();
      }
    };
    return rows.sort((a, b) => {
      const left = value(a);
      const right = value(b);
      const result = typeof left === "number" && typeof right === "number" ? left - right : String(left).localeCompare(String(right));
      return sort.desc ? -result : result;
    });
  }, [data, query, sort, statusFilters, tab, typeFilters, visible]);

  const groups = useMemo(() => {
    if (!grouped) return [{ key: "all", team: undefined as Team | undefined, loops }];
    const byTeam = new Map<string, Loop[]>();
    for (const loop of loops) {
      const key = loopTeamId(loop) ?? "workspace";
      byTeam.set(key, [...(byTeam.get(key) ?? []), loop]);
    }
    const result: { key: string; team?: Team; loops: Loop[] }[] = [];
    if (byTeam.has("workspace")) result.push({ key: "workspace", loops: byTeam.get("workspace")! });
    for (const team of data.teams) if (byTeam.has(team.id)) result.push({ key: team.id, team, loops: byTeam.get(team.id)! });
    for (const [key, items] of byTeam) if (!result.some((group) => group.key === key)) result.push({ key, loops: items });
    return result;
  }, [data.teams, grouped, loops]);

  const replace = (next: Loop) => setLoops(allLoops.map((loop) => (loop.id === next.id ? next : loop)));
  const drop = (id: string) => {
    setLoops(allLoops.filter((loop) => loop.id !== id));
    void onReload().catch(() => undefined);
  };
  const open = (loop: Loop) =>
    onNavigate(isLoopDraft(loop) ? `${newLoopPath(data.workspace.urlKey)}?draftId=${encodeURIComponent(loop.id)}` : loopPath(data.workspace.urlKey, loop.id));
  const sortBy = (key: SortKey) => setSort((current) => ({ key, desc: current.key === key ? !current.desc : key === "runs" || key === "lastRun" }));
  const toggle = <T,>(list: T[], value: T) => (list.includes(value) ? list.filter((item) => item !== value) : [...list, value]);
  const shown = COLUMNS.filter((column) => !hidden.includes(column.id));
  const empty = visible.length === 0;
  const Container = embedded ? "div" : "main";
  const gridTemplate = `minmax(0,1fr) ${shown.map((column) => (column.id === "trigger" ? "150px" : column.id === "owner" ? "170px" : column.id === "runs" ? "96px" : "124px")).join(" ")} 32px`;

  return (
    <Container className={embedded ? "loops-embedded" : "main-panel loops-page"} aria-label={t("Loops")} role={embedded ? "region" : undefined}>
      {!embedded && (
        <header className="loops-topbar">
          <button className="loops-mobile-menu" aria-label={t("Open sidebar")} data-sidebar-trigger onClick={onOpenSidebar}>
            <Settings2 />
          </button>
          <div className="loops-title">
            <h2>{t("Loops")}</h2>
          </div>
          <div className="loops-topbar-actions">
            <button className="loops-new-button" onClick={() => setCreateOpen(true)}>
              <PlusIcon />
              {t("New loop")}
            </button>
          </div>
        </header>
      )}
      {!empty && (
        <div className="loops-toolbar">
          {/* Linear: tabs, then the "Find loops…" field inline; filter and display stay on the right. */}
          <div className="loops-toolbar-left">
            <div className="loops-tabs" role="tablist" aria-label={t("Loops")}>
              {(["mine", "all"] as const).map((item) => (
                <button key={item} role="tab" aria-selected={tab === item} className={tab === item ? "is-active" : undefined} onClick={() => setTab(item)}>
                  {t(item === "mine" ? "My loops" : "All")}
                </button>
              ))}
            </div>
            <label className="loops-search is-inline">
              <Search size={14} />
              <input type="search" aria-label={t("Find loops…")} placeholder={t("Find loops…")} value={query} onChange={(event) => setQuery(event.target.value)} onKeyDown={(event) => event.key === "Escape" && setQuery("")} />
            </label>
          </div>
          <div className="loops-toolbar-right">
            {embedded && (
              <button className="loops-new-button" onClick={() => setCreateOpen(true)}>
                <PlusIcon />
                {t("New loop")}
              </button>
            )}
            <DropdownMenu>
              <DropdownMenuTrigger className={`loops-icon-button${statusFilters.length || typeFilters.length ? " is-open" : ""}`} aria-label={t("Filter")}>
                <FilterIcon />
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="loops-menu">
                <DropdownMenuLabel>{t("Status")}</DropdownMenuLabel>
                {(["enabled", "disabled", "draft"] as const).map((item) => (
                  <DropdownMenuCheckboxItem key={item} checked={statusFilters.includes(item)} onSelect={(event) => event.preventDefault()} onCheckedChange={() => setStatusFilters((current) => toggle(current, item))}>
                    {t(item === "enabled" ? "Enabled" : item === "disabled" ? "Disabled" : "Draft")}
                  </DropdownMenuCheckboxItem>
                ))}
                <DropdownMenuSeparator />
                <DropdownMenuLabel>{t("Trigger")}</DropdownMenuLabel>
                {(["schedule", "issue", "project", "initiative", "release", "team"] as const).map((item) => (
                  <DropdownMenuCheckboxItem key={item} checked={typeFilters.includes(item)} onSelect={(event) => event.preventDefault()} onCheckedChange={() => setTypeFilters((current) => toggle(current, item))}>
                    {t(ENTITY_NAMES[item])}
                  </DropdownMenuCheckboxItem>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
            <DropdownMenu>
              <DropdownMenuTrigger className="loops-icon-button" aria-label={t("Display options")}>
                <DisplayIcon />
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="loops-menu">
                <DropdownMenuCheckboxItem checked={grouped} onSelect={(event) => event.preventDefault()} onCheckedChange={(checked) => setGrouped(checked === true)}>
                  {t("Group by team")}
                </DropdownMenuCheckboxItem>
                <DropdownMenuSeparator />
                <DropdownMenuLabel>{t("Display properties")}</DropdownMenuLabel>
                {COLUMNS.map((column) => (
                  <DropdownMenuCheckboxItem key={column.id} checked={!hidden.includes(column.id)} onSelect={(event) => event.preventDefault()} onCheckedChange={() => setHidden((current) => toggle(current, column.id))}>
                    {t(column.label)}
                  </DropdownMenuCheckboxItem>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </div>
      )}
      {empty ? (
        <div className="loops-hub-scroll">
          <LoopCreateHub data={data} onNavigate={onNavigate} onReload={onReload} />
        </div>
      ) : (
        <div className="loops-table" role="table" aria-label={t("Loops")} style={{ ["--loops-columns" as string]: gridTemplate }}>
          <div className="loops-table-head" role="row">
            <button role="columnheader" className={sort.key === "name" ? "is-sorted" : undefined} onClick={() => sortBy("name")}>
              {t("Name")}
              {sort.key === "name" && (sort.desc ? <ArrowUp size={12} /> : <ArrowDown size={12} />)}
            </button>
            {shown.map((column) => (
              <button key={column.id} role="columnheader" className={sort.key === column.id ? "is-sorted" : undefined} onClick={() => sortBy(column.id)}>
                {t(column.label)}
                {sort.key === column.id && (sort.desc ? <ArrowDown size={12} /> : <ArrowUp size={12} />)}
              </button>
            ))}
            <span role="columnheader" aria-label={t("Actions")} />
          </div>
          {loops.length === 0 && <p className="loops-table-empty">{t(tab === "mine" && !query && !statusFilters.length && !typeFilters.length ? "You don't own any loops yet." : "No loops match.")}</p>}
          {groups.map((group) =>
            group.loops.length ? (
              <div className="loops-group" role="rowgroup" key={group.key}>
                {grouped && (
                  <div className="loops-group-row" role="row">
                    <span className="loops-group-title">
                      {group.team ? <TeamIcon team={group.team} size={14} /> : <ViewGlyph color="currentColor" icon="Team" />}
                      <strong data-i18n-ignore={group.team ? true : undefined}>{group.team ? group.team.name : t("Workspace")}</strong>
                      <span className="loops-group-count">{group.loops.length}</span>
                    </span>
                    <button className="loops-group-add" aria-label={t("New loop")} onClick={() => setCreateOpen(true)}>
                      <PlusIcon />
                    </button>
                  </div>
                )}
                {group.loops.map((loop) => (
                  <LoopRow key={loop.id} data={data} loop={loop} columns={shown.map((column) => column.id)} onOpen={() => open(loop)} onNavigate={onNavigate} onChanged={replace} onDeleted={() => drop(loop.id)} />
                ))}
              </div>
            ) : null,
          )}
        </div>
      )}
      <LoopCreateDialog data={data} open={createOpen} onOpenChange={setCreateOpen} onNavigate={onNavigate} onReload={onReload} />
    </Container>
  );
}

function readTab(): "mine" | "all" {
  try {
    return localStorage.getItem("flow:loops:tab") === "mine" ? "mine" : "all";
  } catch {
    return "all";
  }
}

function LoopRow({
  data,
  loop,
  columns,
  onOpen,
  onNavigate,
  onChanged,
  onDeleted,
}: {
  data: BootstrapData;
  loop: Loop;
  columns: Column[];
  onOpen: () => void;
  onNavigate: (path: string) => void;
  onChanged: (loop: Loop) => void;
  onDeleted: () => void;
}) {
  const { t } = useI18n();
  const owner = loopOwner(data, loop);
  const draft = isLoopDraft(loop);
  return (
    <div className={`loops-table-row${!draft && !loop.enabled ? " is-disabled" : ""}`} role="row">
      <button className="loops-row-link" onClick={onOpen} aria-label={loop.name || t("Untitled loop")} />
      <span className="loops-cell-name" role="cell">
        <span className="loops-row-icon" style={{ color: loopIconColor(loop) }}>
          <LoopIcon source={loop} size={16} />
        </span>
        <span className="loops-row-copy">
          <strong data-i18n-ignore={loop.name ? true : undefined}>
            {loop.name || t("Untitled loop")}
            {draft && <span className="loops-badge">{t("Draft")}</span>}
            {!draft && !loop.enabled && <span className="loops-badge">{t("Disabled")}</span>}
          </strong>
          {loop.description && <small data-i18n-ignore>{loop.description}</small>}
        </span>
      </span>
      {columns.map((column) =>
        column === "trigger" ? (
          <span key={column} role="cell" className="loops-cell-muted">
            {triggerSummary(loop, t)}
          </span>
        ) : column === "owner" ? (
          <span key={column} role="cell" className="loops-cell-owner">
            {owner && <UserAvatar avatarUrl={owner.avatarUrl} className="avatar loops-avatar" name={owner.displayName || owner.name} />}
            <span data-i18n-ignore>{owner?.displayName || owner?.name}</span>
          </span>
        ) : column === "runs" ? (
          <span key={column} role="cell" className="loops-cell-muted is-number">
            {draft ? "–" : (loop.runCount30d ?? 0)}
          </span>
        ) : (
          <span key={column} role="cell" className="loops-cell-muted" title={loop.lastRunAt ? new Date(loop.lastRunAt).toLocaleString() : undefined}>
            {loop.lastRunAt ? relativeTime(loop.lastRunAt, undefined, t) : t("Never")}
          </span>
        ),
      )}
      <span role="cell" className="loops-cell-actions">
        <LoopActionsMenu
          data={data}
          loop={loop}
          onNavigate={onNavigate}
          onChanged={onChanged}
          onDeleted={onDeleted}
          trigger={
            <button className="loops-row-menu" aria-label={t("Open actions")}>
              <MoreHorizontal size={16} />
            </button>
          }
        />
      </span>
    </div>
  );
}
