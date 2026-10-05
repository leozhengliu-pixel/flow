import { useCallback, useEffect, useMemo, useRef, useState, type MouseEvent, type ReactNode } from "react";
import { ArrowDown, ArrowUp, Check, Search, Settings2, UserRound, X } from "lucide-react";
import { PlusIcon } from "@/components/ui/view-action-icons";
import { UserAvatar } from "@/components/ui/user-avatar";
import { TeamIcon } from "@/components/issue/issue-icons";
import { ViewGlyph } from "@/components/views/view-icon-picker";
import { DirectoryFilterMenu } from "@/components/workspace-directory/directory-menus";
import { TeamDateFilterDialog } from "@/components/workspace-directory/team-directory-controls";
import { loopPath, newLoopPath } from "@/lib/app-routes";
import { useI18n } from "@/i18n/i18n";
import type { BootstrapData, Loop, LoopTriggerType, User } from "@/types/flow";
import { LoopActionsMenu, LoopBulkActionsMenu } from "./loop-actions";
import { LoopCreateDialog, LoopCreateHub } from "./loop-create-hub";
import { LoopIcon, loopIconColor } from "./loop-glyph";
import { loopOwner, useLoops } from "./loop-data";
import { ENTITY_NAMES, compactAge, isLoopDraft, loopTeam, loopTeamId, triggerSummary } from "./loop-model";
import { TriggerIcon } from "./loop-trigger";
import { LoopDisplayMenu, LoopFilterBar } from "./loop-list-controls";
import {
  LOOP_FILTER_FIELDS,
  defaultDescending,
  emptyLoopFilters,
  loopDisplayKey,
  loopFilterGroups,
  loopFiltersActive,
  matchesLoopFilters,
  readLoopDisplay,
  writeLoopDisplay,
  type LoopDisplaySettings,
  type LoopListFilterField,
  type LoopListFilters,
  type LoopOrdering,
} from "./loop-list-model";

type SortKey = LoopOrdering;
type Column = "trigger" | "owner" | "runs" | "lastRun";
/** Linear's column widths; runs and last executed are right-aligned. */
const COLUMN_WIDTH: Record<Column, string> = { trigger: "100px", owner: "140px", runs: "80px", lastRun: "120px" };
const END_COLUMNS: Column[] = ["runs", "lastRun"];
const COLUMNS: { id: Column; label: string }[] = [
  { id: "trigger", label: "Trigger" },
  { id: "owner", label: "Owner" },
  { id: "runs", label: "Runs (30d)" },
  { id: "lastRun", label: "Last executed" },
];
const TRIGGER_ORDER: LoopTriggerType[] = ["schedule", "issue", "project", "initiative", "release", "team", "cycle"];
type LoopGroup = { key: string; label: string; icon: ReactNode; entityName?: boolean; loops: Loop[] };

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
  onReload: (changed?: Loop) => Promise<void>;
  /** `/loops/new` without a draft opens the hub dialog. */
  createOpen?: boolean;
}) {
  const { t } = useI18n();
  const [allLoops, setLoops] = useLoops(data);
  const [storedTab, setTab] = useState<"mine" | "all">(() => readTab());
  // The team page has no tabs; it lists every loop of the team.
  const tab = embedded ? "all" : storedTab;
  const [query, setQuery] = useState("");
  const [filters, setFilters] = useState<LoopListFilters>(emptyLoopFilters);
  const [dateOpen, setDateOpen] = useState(false);
  const [collapsed, setCollapsed] = useState<string[]>([]);
  // Display options persist per workspace and viewer, like the tab.
  const displayKey = loopDisplayKey(data);
  const [display, setDisplay] = useState<LoopDisplaySettings>(() => readLoopDisplay(displayKey));
  useEffect(() => writeLoopDisplay(displayKey, display), [display, displayKey]);
  // The team page lists only that team's loops, so it ignores "Show team loops".
  const showTeamLoops = display.showTeamLoops || Boolean(teamId);
  const grouping = display.grouping;
  const grouped = grouping !== "none";
  const sort = { key: display.ordering, desc: display.descending };
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
  // Rows the tab and display toggles leave; the filter menu counts and "hidden by filters" use these.
  const base = useMemo(
    () =>
      visible.filter((loop) => {
        if (tab === "mine" && (loop.ownerId ?? loop.creator?.id) !== data.viewer.id) return false;
        if (!showTeamLoops && loop.level === "team") return false;
        if (!display.showDisabledLoops && !isLoopDraft(loop) && !loop.enabled) return false;
        return true;
      }),
    [data.viewer.id, display.showDisabledLoops, showTeamLoops, tab, visible],
  );
  const sortKey = sort.key;
  const sortDesc = sort.desc;
  const loops = useMemo(() => {
    const needle = query.trim().toLowerCase();
    const rows = base.filter((loop) => {
      if (needle && !`${loop.name} ${loop.description ?? ""}`.toLowerCase().includes(needle)) return false;
      return matchesLoopFilters(loop, filters, data.viewer.id);
    });
    const value = (loop: Loop): string | number => {
      switch (sortKey) {
        case "trigger":
          return triggerSummary(loop).toLowerCase();
        case "owner":
          return (loopOwner(data, loop)?.displayName ?? "").toLowerCase();
        case "team":
          return (loopTeam(data, loop)?.name ?? "").toLowerCase();
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
      const result =
        (typeof left === "number" && typeof right === "number" ? left - right : String(left).localeCompare(String(right))) ||
        (a.name || "").toLowerCase().localeCompare((b.name || "").toLowerCase());
      return sortDesc ? -result : result;
    });
  }, [base, data, filters, query, sortDesc, sortKey]);

  const groups = useMemo<LoopGroup[]>(() => {
    if (!grouped) return [{ key: "all", label: "", icon: null, loops }];
    const buckets = new Map<string, Loop[]>();
    const keyOf = (loop: Loop) =>
      grouping === "trigger" ? loop.triggerType : grouping === "owner" ? (loopOwner(data, loop)?.id ?? "none") : (loopTeamId(loop) ?? "workspace");
    for (const loop of loops) {
      const key = keyOf(loop);
      buckets.set(key, [...(buckets.get(key) ?? []), loop]);
    }
    const result: LoopGroup[] = [];
    const add = (key: string, group: Omit<LoopGroup, "key" | "loops">) => {
      const items = buckets.get(key);
      if (items) result.push({ key: `${grouping}:${key}`, ...group, loops: items });
      buckets.delete(key);
    };
    if (grouping === "trigger") {
      for (const type of TRIGGER_ORDER) add(type, { label: t(ENTITY_NAMES[type]), icon: <TriggerIcon triggerType={type} /> });
    } else if (grouping === "owner") {
      const owners = new Map<string, User>();
      for (const loop of loops) {
        const owner = loopOwner(data, loop);
        if (owner) owners.set(owner.id, owner);
      }
      for (const owner of [...owners.values()].sort((left, right) => (left.displayName || left.name).localeCompare(right.displayName || right.name)))
        add(owner.id, { label: owner.displayName || owner.name, entityName: true, icon: <UserAvatar avatarUrl={owner.avatarUrl} className="avatar loops-avatar" name={owner.displayName || owner.name} /> });
      add("none", { label: t("No owner"), icon: <UserRound size={14} /> });
    } else {
      add("workspace", { label: t("Workspace"), icon: <ViewGlyph color="currentColor" icon="Team" /> });
      for (const team of data.teams) add(team.id, { label: team.name, entityName: true, icon: <TeamIcon team={team} size={14} /> });
    }
    for (const [key, items] of buckets) result.push({ key: `${grouping}:${key}`, label: key, entityName: true, icon: null, loops: items });
    return result;
  }, [data, grouped, grouping, loops, t]);

  // Linear's row selection: checkboxes, shift-click ranges, ⌘A, X on the hovered row, Escape to clear.
  const [selected, setSelected] = useState<string[]>([]);
  const anchor = useRef<string | undefined>(undefined);
  const hovered = useRef<string | undefined>(undefined);
  const rowIds = useMemo(
    () => groups.flatMap((group) => (grouped && collapsed.includes(group.key) ? [] : group.loops.map((loop) => loop.id))),
    [collapsed, grouped, groups],
  );
  useEffect(() => {
    // Rows hidden by filters or collapsed groups leave the selection.
    setSelected((current) => {
      const next = current.filter((id) => rowIds.includes(id));
      return next.length === current.length ? current : next;
    });
  }, [rowIds]);
  const toggleSelect = useCallback(
    (id: string, range: boolean) => {
      // Read the anchor now: the updater may run after it moves to `id`.
      const from = anchor.current ? rowIds.indexOf(anchor.current) : -1;
      const to = rowIds.indexOf(id);
      setSelected((current) => {
        if (range && from >= 0 && to >= 0) {
          const span = rowIds.slice(Math.min(from, to), Math.max(from, to) + 1);
          return [...new Set([...current, ...span])];
        }
        return current.includes(id) ? current.filter((item) => item !== id) : [...current, id];
      });
      anchor.current = id;
    },
    [rowIds],
  );
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      if (target?.closest("input, textarea, select, [contenteditable='true'], [role='menu'], [role='dialog']")) return;
      if ((event.metaKey || event.ctrlKey) && !event.altKey && event.key.toLowerCase() === "a" && rowIds.length) {
        event.preventDefault();
        setSelected(rowIds);
        return;
      }
      if (event.key === "Escape" && selected.length) {
        event.preventDefault();
        setSelected([]);
        anchor.current = undefined;
        return;
      }
      if (!event.metaKey && !event.ctrlKey && !event.altKey && event.key.toLowerCase() === "x" && hovered.current) {
        event.preventDefault();
        toggleSelect(hovered.current, event.shiftKey);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [rowIds, selected.length, toggleSelect]);
  const selectedLoops = useMemo(() => allLoops.filter((loop) => selected.includes(loop.id)), [allLoops, selected]);

  const replace = (next: Loop) => {
    setLoops(allLoops.map((loop) => (loop.id === next.id ? next : loop)));
    void onReload(next).catch(() => undefined);
  };
  const replaceMany = (changed: Loop[]) => {
    const byId = new Map(changed.map((loop) => [loop.id, loop]));
    setLoops(allLoops.map((loop) => byId.get(loop.id) ?? loop));
    void onReload().catch(() => undefined);
  };
  const drop = (id: string) => dropMany([id]);
  const dropMany = (ids: string[]) => {
    setLoops(allLoops.filter((loop) => !ids.includes(loop.id)));
    setSelected((current) => current.filter((id) => !ids.includes(id)));
    void onReload().catch(() => undefined);
  };
  // Duplicate opens the copy's draft; it is in the list (and the app's loops) right away.
  const added = (copy: Loop) => {
    setLoops([copy, ...allLoops.filter((loop) => loop.id !== copy.id)]);
    void onReload(copy).catch(() => undefined);
  };
  const open = (loop: Loop) =>
    onNavigate(isLoopDraft(loop) ? `${newLoopPath(data.workspace.urlKey)}?draftId=${encodeURIComponent(loop.id)}` : loopPath(data.workspace.urlKey, loop.id));
  // Column headers and the display menu's Ordering are the same setting.
  const sortBy = (key: SortKey) => setDisplay((current) => ({ ...current, ordering: key, descending: current.ordering === key ? !current.descending : defaultDescending(key) }));
  const toggle = <T,>(list: T[], value: T) => (list.includes(value) ? list.filter((item) => item !== value) : [...list, value]);
  const empty = visible.length === 0;
  const Container = embedded ? "div" : "main";
  const gridTemplate = ["minmax(0,1fr)", ...COLUMNS.map((column) => COLUMN_WIDTH[column.id])].join(" ");
  const filtersActive = loopFiltersActive(filters);
  const filtering = Boolean(query.trim()) || filtersActive;
  const clearFilters = () => {
    setQuery("");
    setFilters(emptyLoopFilters());
  };
  const changeFilter = (groupId: string, choiceId: string, checked: boolean) => {
    if (choiceId === "custom") {
      setDateOpen(true);
      return;
    }
    const field = groupId as LoopListFilterField;
    if (!LOOP_FILTER_FIELDS.includes(field)) return;
    setFilters((current) => {
      // Owner is multi-select; Last executed and Runs hold one value.
      const next = new Set(field === "owner" ? current[field] : []);
      if (checked) next.add(choiceId);
      else next.delete(choiceId);
      return { ...current, [field]: [...next] };
    });
  };
  const filterGroups = loopFilterGroups({ data, loops: base, t, onRuns: (value) => changeFilter("runs", value, true) });

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
            {/* The team page's Loops tab has no search field in Linear. */}
            {!embedded && (
            <label className="loops-search is-inline">
              <Search size={16} />
              <input type="search" aria-label={t("Find loops…")} placeholder={t("Find loops…")} value={query} onChange={(event) => setQuery(event.target.value)} onKeyDown={(event) => event.key === "Escape" && setQuery("")} />
              {query && (
                <button type="button" className="loops-search-clear" aria-label={t("Clear search")} onClick={() => setQuery("")}>
                  <X size={12} />
                </button>
              )}
            </label>
            )}
          </div>
          <div className="loops-toolbar-right">
            {embedded && (
              <button className="loops-new-button is-filled" onClick={() => setCreateOpen(true)}>
                <PlusIcon />
                {t("New loop")}
              </button>
            )}
            <DirectoryFilterMenu
              groups={filterGroups}
              selected={Object.fromEntries(LOOP_FILTER_FIELDS.map((field) => [field, new Set(filters[field])]))}
              onChoice={changeFilter}
              onAdvanced={() => setFilters((current) => ({ ...current, advanced: true }))}
              tooltip={{ label: t("Add Filter"), shortcut: "F" }}
              triggerClassName={`loops-icon-button${filtersActive ? " is-open" : ""}`}
            />
            <LoopDisplayMenu settings={display} onChange={setDisplay} hideTeamToggle={Boolean(teamId)} />
          </div>
        </div>
      )}
      {!empty && filtersActive && <LoopFilterBar data={data} filters={filters} groups={filterGroups} onChange={setFilters} onChoice={changeFilter} onDate={() => setDateOpen(true)} />}
      <TeamDateFilterDialog
        open={dateOpen}
        value={filters.lastRun[0]}
        title={t("Last executed")}
        fromLabel={t("Last executed on or after")}
        toLabel={t("Last executed through")}
        onClose={() => setDateOpen(false)}
        onApply={(value) => {
          changeFilter("lastRun", value, true);
          setDateOpen(false);
        }}
      />
      {empty ? (
        <div className="loops-hub-scroll">
          <LoopCreateHub data={data} onNavigate={onNavigate} onReload={onReload} />
        </div>
      ) : loops.length === 0 && filtering ? (
        <LoopsFilteredEmpty hiddenCount={base.length} onClear={clearFilters} />
      ) : (
        <div className={`loops-table${selected.length ? " has-selection" : ""}`} role="table" aria-label={t("Loops")} aria-multiselectable="true" style={{ ["--loops-columns" as string]: gridTemplate }}>
          <div className="loops-table-head" role="row">
            <button role="columnheader" className={sort.key === "name" ? "is-sorted" : undefined} onClick={() => sortBy("name")}>
              {t("Name")}
              {sort.key === "name" && (sort.desc ? <ArrowUp size={12} /> : <ArrowDown size={12} />)}
            </button>
            {COLUMNS.map((column) => {
              const end = END_COLUMNS.includes(column.id);
              const arrow = sort.key === column.id && (sort.desc ? <ArrowDown size={12} /> : <ArrowUp size={12} />);
              return (
                <button key={column.id} role="columnheader" className={[sort.key === column.id ? "is-sorted" : "", end ? "is-end" : ""].filter(Boolean).join(" ") || undefined} onClick={() => sortBy(column.id)}>
                  {end && arrow}
                  {t(column.label)}
                  {!end && arrow}
                </button>
              );
            })}
          </div>
          {loops.length === 0 && <p className="loops-table-empty">{t(tab === "mine" && !filtering ? "You don't own any loops yet." : "No loops match.")}</p>}
          {groups.map((group) =>
            group.loops.length ? (
              <div className="loops-group" role="rowgroup" key={group.key}>
                {grouped && (
                  <div className="loops-group-row" role="row">
                    <button
                      type="button"
                      className="loops-group-title"
                      aria-expanded={!collapsed.includes(group.key)}
                      onClick={() => setCollapsed((current) => toggle(current, group.key))}
                    >
                      <svg className="loops-group-chevron" viewBox="0 0 16 16" aria-hidden="true">
                        <path d="M4.5 6.25h7L8 10.25z" fill="currentColor" />
                      </svg>
                      {group.icon}
                      <strong data-i18n-ignore={group.entityName || undefined}>{group.label}</strong>
                    </button>
                    <button className="loops-group-add" aria-label={t("New loop")} onClick={() => setCreateOpen(true)}>
                      <PlusIcon />
                    </button>
                  </div>
                )}
                {!(grouped && collapsed.includes(group.key)) && group.loops.map((loop) => (
                  <LoopRow
                    key={loop.id}
                    data={data}
                    loop={loop}
                    columns={COLUMNS.map((column) => column.id)}
                    selected={selected.includes(loop.id)}
                    selection={selected.length > 1 && selected.includes(loop.id) ? selectedLoops : undefined}
                    onToggleSelect={(range) => toggleSelect(loop.id, range)}
                    onHover={(inside) => {
                      if (inside) hovered.current = loop.id;
                      else if (hovered.current === loop.id) hovered.current = undefined;
                    }}
                    onOpen={() => open(loop)}
                    onNavigate={onNavigate}
                    onChanged={replace}
                    onChangedMany={replaceMany}
                    onDeleted={() => drop(loop.id)}
                    onDeletedMany={dropMany}
                    onDuplicated={added}
                  />
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
  selected,
  selection,
  onToggleSelect,
  onHover,
  onOpen,
  onNavigate,
  onChanged,
  onChangedMany,
  onDeleted,
  onDeletedMany,
  onDuplicated,
}: {
  data: BootstrapData;
  loop: Loop;
  columns: Column[];
  selected: boolean;
  /** Set when this row is part of a multi-row selection: the menu acts on all of it. */
  selection?: Loop[];
  onToggleSelect: (range: boolean) => void;
  onHover: (inside: boolean) => void;
  onOpen: () => void;
  onNavigate: (path: string) => void;
  onChanged: (loop: Loop) => void;
  onChangedMany: (loops: Loop[]) => void;
  onDeleted: () => void;
  onDeletedMany: (ids: string[]) => void;
  onDuplicated: (copy: Loop) => void;
}) {
  const { t } = useI18n();
  const owner = loopOwner(data, loop);
  const draft = isLoopDraft(loop);
  // Linear has no ⋯ button on list rows; the loop menu opens where the row is right-clicked.
  const [menuAt, setMenuAt] = useState<{ x: number; y: number }>();
  const openMenu = (event: MouseEvent<HTMLElement>) => {
    // Events from the portalled menu bubble through React; only the row itself opens it.
    if (!event.currentTarget.contains(event.target as Node)) return;
    event.preventDefault();
    const rect = event.currentTarget.getBoundingClientRect();
    // The keyboard context-menu key reports no pointer position; anchor it to the row instead.
    setMenuAt(event.clientX || event.clientY ? { x: event.clientX, y: event.clientY } : { x: rect.left + 46, y: rect.bottom });
  };
  return (
    <div
      className={`loops-table-row${!draft && !loop.enabled ? " is-disabled" : ""}${menuAt ? " is-menu-open" : ""}${selected ? " is-selected" : ""}`}
      role="row"
      aria-selected={selected}
      onContextMenu={openMenu}
      onMouseEnter={() => onHover(true)}
      onMouseLeave={() => onHover(false)}
    >
      <button
        className="loops-row-link"
        onClick={(event) => {
          // Shift/⌘-click selects like Linear instead of opening the loop.
          if (event.shiftKey || event.metaKey || event.ctrlKey) {
            event.preventDefault();
            onToggleSelect(event.shiftKey);
            return;
          }
          onOpen();
        }}
        aria-label={loop.name || t("Untitled loop")}
      />
      <button
        type="button"
        role="checkbox"
        aria-checked={selected}
        aria-label={t("Select loop")}
        title={t("Select loop")}
        className="loops-row-check"
        onClick={(event) => {
          event.stopPropagation();
          onToggleSelect(event.shiftKey);
        }}
      >
        {selected && <Check size={10} strokeWidth={3} />}
      </button>
      <span className="loops-cell-name" role="cell">
        <span className="loops-row-title">
          <span className="loops-row-icon" style={{ color: loopIconColor(loop) }}>
            <LoopIcon source={loop} size={16} />
          </span>
          <strong data-i18n-ignore={loop.name ? true : undefined}>{loop.name || t("Untitled loop")}</strong>
          {draft && <span className="loops-badge">{t("Draft")}</span>}
          {!draft && !loop.enabled && <span className="loops-badge">{t("Disabled")}</span>}
        </span>
        {loop.description && <small data-i18n-ignore>{loop.description}</small>}
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
          <span key={column} role="cell" className="loops-cell-muted is-number is-end">
            {draft ? "–" : (loop.runCount30d ?? 0)}
          </span>
        ) : (
          <span key={column} role="cell" className="loops-cell-muted is-end" title={loop.lastRunAt ? new Date(loop.lastRunAt).toLocaleString() : undefined}>
            {loop.lastRunAt ? compactAge(loop.lastRunAt) : t("Never")}
          </span>
        ),
      )}
      {menuAt && selection && (
        <LoopBulkActionsMenu
          data={data}
          loops={selection}
          open
          onOpenChange={(next) => !next && setMenuAt(undefined)}
          onChanged={onChangedMany}
          onDeleted={onDeletedMany}
          trigger={<span className="loops-row-menu-anchor" aria-hidden="true" style={{ left: menuAt.x, top: menuAt.y }} />}
        />
      )}
      {menuAt && !selection && (
        <LoopActionsMenu
          align="start"
          data={data}
          loop={loop}
          open
          onOpenChange={(next) => !next && setMenuAt(undefined)}
          onNavigate={onNavigate}
          onChanged={onChanged}
          onDeleted={onDeleted}
          onDuplicated={onDuplicated}
          trigger={<span className="loops-row-menu-anchor" aria-hidden="true" style={{ left: menuAt.x, top: menuAt.y }} />}
        />
      )}
    </div>
  );
}

/** Linear's "No loops matching the filters" state with the hidden count and Clear Filters. */
function LoopsFilteredEmpty({ hiddenCount, onClear }: { hiddenCount: number; onClear: () => void }) {
  const { t } = useI18n();
  return (
    <div className="loops-filtered-empty">
      <img alt="" aria-hidden="true" className="loops-filtered-empty-art" src="/flow-filter-empty.svg" />
      <h2>{t("No loops matching the filters")}</h2>
      <div className="loops-filtered-empty-notice">
        <span>
          <strong>{t(hiddenCount === 1 ? "{count} loop" : "{count} loops").replace("{count}", String(hiddenCount))}</strong> <small>{t("hidden by filters")}</small>
        </span>
        <button type="button" onClick={onClear}>
          {t("Clear Filters")}
        </button>
        <button type="button" aria-label={t("Clear filters")} onClick={onClear}>
          <X size={12} />
        </button>
      </div>
    </div>
  );
}
