import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import { useEffect, useMemo, useRef, useState } from "react";
import {
  BarChart3,
  ArrowDown,
  ArrowUp,
  Bell,
  BellOff,
  Check,
  ChevronDown,
  Copy,
  Download,
  Expand,
  Filter,
  LayoutDashboard,
  Link2,
  LoaderCircle,
  MoreHorizontal,
  Plus,
  RefreshCw,
  Share2,
  SlidersHorizontal,
  Star,
  Table2,
  Trash2,
  UsersRound,
} from "lucide-react";
import { toast } from "sonner";

import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { confirmAction } from "@/components/ui/action-dialog-service";
import { PropertyMenu } from "@/components/property/property-menu";
import { LabelIcon, NoAssigneeIcon, StatusIcon, TeamIcon } from "@/components/issue/issue-icons";
import { ViewsDirectoryHeader } from "@/components/views-page/views-directory-header";
import {
  LinearInsightBar,
  LinearInsightLine,
  type LinearInsightPoint,
} from "@/components/analytics/linear-insight-graph";
import { useI18n } from "@/i18n/i18n";
import {
  addFavorite,
  createDashboard,
  dashboardExportURL,
  deleteDashboard,
  fetchDashboardResults,
  fetchDashboards,
  previewDashboardWidget,
  removeFavorite,
  shareDashboard,
  subscribeDashboard,
  updateDashboard,
} from "@/lib/api";
import type {
  BootstrapData,
  Dashboard,
  DashboardInsightConfig,
  DashboardWidget,
  DashboardWidgetResult,
  IssueLabel,
  Team,
  User,
  WorkflowState,
} from "@/types/flow";

import "./dashboards-page.css";

type DashboardFilters = NonNullable<Dashboard["filters"]>;
type InsightDisplay = "chart" | "table" | "metric";
type DashboardOrdering = "name" | "owner" | "updatedAt" | "createdAt";
type DashboardColumn = "owner" | "updatedAt" | "createdAt";

export function DashboardsPage({
  dashboardId,
  creating = false,
  widgetId,
  data,
  dashboardsHref,
  onNavigate,
  onExploreIssues,
  onOpenResource,
  onOpenSidebar,
  onOpenCreate,
  onOpenWidget,
  resourceHref,
  teamKey,
}: {
  dashboardId?: string;
  creating?: boolean;
  widgetId?: string;
  data: BootstrapData;
  dashboardsHref: string;
  onNavigate: (dashboardId?: string) => void;
  onExploreIssues: (filters: DashboardFilters) => void;
  onOpenResource: (resource: "issues" | "projects") => void;
  onOpenSidebar: () => void;
  onOpenCreate: () => void;
  onOpenWidget: (dashboardId: string, widgetId: string) => void;
  resourceHref: (resource: "issues" | "projects") => string;
  teamKey?: string;
}) {
  const { t } = useI18n();
  const routeTeam = teamKey
    ? data.teams.find(
        (team) => team.key.toLowerCase() === teamKey.toLowerCase(),
      )
    : undefined;
  const [items, setItems] = useState<Dashboard[]>([]);
  const [selectedId, setSelectedId] = useState(dashboardId ?? "");
  const [results, setResults] = useState<DashboardWidgetResult[]>([]);
  const [loading, setLoading] = useState(true);
  const [createOpen, setCreateOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [filtersOpen, setFiltersOpen] = useState(false);
  const [draftName, setDraftName] = useState("");
  const [draftDescription, setDraftDescription] = useState("");
  const [draftVisibility, setDraftVisibility] = useState<
    Dashboard["visibility"]
  >(routeTeam ? "team" : "workspace");
  const [draftTeamId, setDraftTeamId] = useState(
    routeTeam?.id ?? data.teams[0]?.id ?? "",
  );
  const [busy, setBusy] = useState(false);
  const [favorite, setFavorite] = useState(false);
  const [query, setQuery] = useState("");
  const [ordering, setOrdering] = useState<DashboardOrdering>("name");
  const [direction, setDirection] = useState<"asc" | "desc">("asc");
  const [columns, setColumns] = useState<Set<DashboardColumn>>(
    () => new Set(["owner"]),
  );
  const resultsRequest = useRef(0);
  const selected = useMemo(
    () => items.find((item) => item.id === selectedId),
    [items, selectedId],
  );
  useEffect(() => {
    setFavorite(Boolean(selectedId && data.favorites.some((item) => item.userId === data.viewer.id && item.resourceType === "dashboard" && item.resourceId === selectedId)));
  }, [data.favorites, data.viewer.id, selectedId]);
  useEffect(() => setSelectedId(dashboardId ?? ""), [dashboardId]);
  useEffect(() => setCreateOpen(creating), [creating]);

  const load = async () => {
    setLoading(true);
    try {
      const loaded: Dashboard[] = [];
      let cursor = "";
      do {
        const page = await fetchDashboards(cursor);
        loaded.push(...page.items);
        cursor = page.hasMore ? page.nextCursor : "";
      } while (cursor);
      setItems(loaded);
      setSelectedId((current) =>
        loaded.some((item) => item.id === current) ? current : "",
      );
    } finally {
      setLoading(false);
    }
  };
  useEffect(() => {
    void load().catch(() => toast.error(t("Could not load dashboards")));
  }, [t]);
  useEffect(() => {
    const request = ++resultsRequest.current;
    if (!selected) {
      setResults([]);
      return;
    }
    setLoading(true);
    void fetchDashboardResults(selected.id)
      .then((value) => { if (resultsRequest.current === request) setResults(value.results); })
      .catch(() => toast.error(t("Could not load dashboard data")))
      .finally(() => { if (resultsRequest.current === request) setLoading(false); });
  }, [selected, t]);

  const replace = (item: Dashboard) =>
    setItems((current) =>
      current.map((value) => (value.id === item.id ? item : value)),
    );
  const patchDashboard = async (
    input: Parameters<typeof updateDashboard>[1],
  ) => {
    if (!selected) return undefined;
    try {
      const item = await updateDashboard(selected.id, input);
      replace(item);
      return item;
    } catch (error) {
      toast.error(t("Could not update dashboard"));
      throw error;
    }
  };
  const create = async () => {
    if (
      !draftName.trim() ||
      busy ||
      (draftVisibility === "team" && !draftTeamId)
    )
      return;
    setBusy(true);
    try {
      const item = await createDashboard({
        name: draftName.trim(),
        description: draftDescription.trim(),
        visibility: draftVisibility,
        teamIds: draftVisibility === "team" ? [draftTeamId] : [],
        filters: {},
        hideFilters: false,
        widgets: [],
      });
      setItems((current) => [item, ...current]);
      setSelectedId(item.id);
      onNavigate(item.id);
      setCreateOpen(false);
      setDraftName("");
      setDraftDescription("");
      setDraftVisibility(routeTeam ? "team" : "workspace");
    } catch {
      toast.error(t("Could not create dashboard"));
    } finally {
      setBusy(false);
    }
  };
  const remove = async () => {
    if (!selected || busy) return;
    setBusy(true);
    try {
      await deleteDashboard(selected.id);
      setItems((current) => current.filter((item) => item.id !== selected.id));
      setSelectedId("");
      onNavigate();
      setDeleteOpen(false);
    } finally {
      setBusy(false);
    }
  };
  const copyShareLink = async () => {
    if (!selected) return;
    try {
      const shared = selected.shareToken ? selected : await shareDashboard(selected.id, true);
      replace(shared);
      await navigator.clipboard.writeText(`${location.origin}/api/shared/dashboards/${shared.shareToken}`);
      toast.success(t("Public dashboard link copied"));
    } catch {
      toast.error(t("Could not share dashboard"));
    }
  };
  const toggleFavorite = async () => {
    if (!selected) return;
    const next = !favorite;
    setFavorite(next);
    try {
      if (next) await addFavorite("dashboard", selected.id);
      else await removeFavorite("dashboard", selected.id);
    } catch {
      setFavorite(!next);
      toast.error(t("Could not update favorite"));
    }
  };
  const openInsight = (widgetId?: string) => {
    if (selected) onOpenWidget(selected.id, widgetId ?? "new");
  };
  const saveInsight = async (draft: InsightDraft) => {
    if (!selected) return;
    const existing = selected.widgets.find(
      (item) => item.id === widgetId && widgetId !== "new",
    );
    const next: DashboardWidget = {
      id: existing?.id ?? "",
      type: "insight",
      title: draft.title.trim() || insightDefaultTitle(draft),
      description: draft.description.trim(),
      position: existing?.position ?? selected.widgets.length,
      width: existing?.width ?? 1,
      config: {
        display: draft.display,
        measure: draft.measure,
        aggregation: draft.aggregation,
        slice: draft.slice,
        segment: draft.segment,
        dateAggregation: draft.dateAggregation,
        ...draft.filters,
      },
    };
    await patchDashboard({
      widgets: existing
        ? selected.widgets.map((item) =>
            item.id === existing.id ? next : item,
          )
        : [...selected.widgets, next],
    });
    clearInsightURLState();
    onNavigate(selected.id);
  };
  const reorderWidget = async (widgetId: string, targetIndex: number) => {
    if (!selected) return;
    const currentIndex = selected.widgets.findIndex((item) => item.id === widgetId);
    const bounded = Math.max(0, Math.min(targetIndex, selected.widgets.length - 1));
    if (currentIndex < 0 || currentIndex === bounded) return;
    const widgets = [...selected.widgets];
    const [moved] = widgets.splice(currentIndex, 1);
    widgets.splice(bounded, 0, moved);
    await patchDashboard({ widgets: widgets.map((item, position) => ({ ...item, position })) });
  };
  const subscribed = Boolean(selected?.subscriberIds.includes(data.viewer.id));
  const visibleItems = routeTeam
    ? items.filter(
        (item) =>
          item.visibility === "team" && item.teamIds.includes(routeTeam.id),
      )
    : items;
  const directoryItems = [...visibleItems]
    .filter((item) =>
      `${item.name} ${item.description ?? ""}`
        .toLowerCase()
        .includes(query.trim().toLowerCase()),
    )
    .sort((left, right) =>
      compareDashboards(left, right, ordering, direction, data),
    );
  const workspace = directoryItems.filter(
    (item) => item.visibility === "workspace",
  );
  const personal = directoryItems.filter(
    (item) => item.visibility === "private",
  );
  const crossTeam = directoryItems.filter((item) => item.visibility === "team");
  const openDashboard = (id: string) => {
    setSelectedId(id);
    onNavigate(id);
  };

  return (
    <div className="dashboards-shell">
      {selected && <DashboardHeader
          selected={selected}
          sectionTitle={widgetId ? (selected.widgets.find((item) => item.id === widgetId)?.title ?? t("Add insight")) : undefined}
          favorite={favorite}
          subscribed={subscribed}
          onBack={() => {
            setSelectedId("");
            onNavigate();
          }}
          onDelete={() => setDeleteOpen(true)}
          onDisableShare={() =>
            void shareDashboard(selected.id, false).then(replace)
          }
          onFavorite={() => void toggleFavorite()}
          onOpenSidebar={onOpenSidebar}
          onOwner={(ownerId) => void patchDashboard({ ownerId })}
          onRefresh={() => void load()}
          onShare={() => void copyShareLink()}
          onSubscribe={() =>
            void subscribeDashboard(selected.id, !subscribed).then(replace)
          }
          onVisibility={(visibility, teamIds) =>
            void patchDashboard({ visibility, teamIds })
          }
          data={data}
        />}
      {!selected ? (
        <section className="dashboard-index">
          <ViewsDirectoryHeader
            activeResource="dashboards"
            actionLabel={t("New dashboard")}
            onAction={onOpenCreate}
            onOpenSidebar={onOpenSidebar}
            tabs={[
              { resource: "issues", label: t("Issues"), href: resourceHref("issues"), onSelect: () => onOpenResource("issues") },
              { resource: "projects", label: t("Projects"), href: resourceHref("projects"), onSelect: () => onOpenResource("projects") },
              { resource: "dashboards", label: t("Dashboards"), href: dashboardsHref },
            ]}
            title={t("Views")}
            toolbarEnd={<>
              <input
                className="dashboard-search"
                aria-label={t("Find a dashboard…")}
                placeholder={t("Find a dashboard…")}
                value={query}
                onChange={(event) => setQuery(event.target.value)}
              />
              <DashboardDirectoryMenu
                columns={columns}
                direction={direction}
                ordering={ordering}
                onColumns={setColumns}
                onDirection={setDirection}
                onOrdering={setOrdering}
              />
            </>}
          />
          <div className="dashboard-directory-content">
            <div
              className="dashboard-table-head"
              style={
                {
                  "--dashboard-columns": dashboardColumns(columns),
                } as React.CSSProperties
              }
            >
              <span>{t("Name")}</span>
              {columns.has("createdAt") && <span>{t("Created")}</span>}
              {columns.has("updatedAt") && <span>{t("Updated")}</span>}
              {columns.has("owner") && <span>{t("Owner")}</span>}
            </div>
            {routeTeam ? (
              <DashboardSection
                hideHeader
                title={routeTeam.name}
                items={directoryItems}
                data={data}
                columns={columns}
                onOpen={openDashboard}
                onCreate={() => {
                  setDraftVisibility("team");
                  setDraftTeamId(routeTeam.id);
                  onOpenCreate();
                }}
              />
            ) : (
              <>
                <DashboardSection
                  title={t("Personal dashboards")}
                  scope={t("Only visible to you")}
                  items={personal}
                  data={data}
                  columns={columns}
                  onOpen={openDashboard}
                  onCreate={() => {
                    setDraftVisibility("private");
                    onOpenCreate();
                  }}
                />
                <DashboardSection
                  title={data.workspace.name}
                  scope={t("Workspace")}
                  items={workspace}
                  data={data}
                  columns={columns}
                  onOpen={openDashboard}
                  onCreate={() => {
                    setDraftVisibility("workspace");
                    onOpenCreate();
                  }}
                />
                <DashboardSection
                  title={t("Cross-team dashboards")}
                  items={crossTeam}
                  data={data}
                  columns={columns}
                  canCreate={false}
                  onOpen={openDashboard}
                />
              </>
            )}
            {!loading && directoryItems.length === 0 && (
              <div className="dashboard-zero">
                <LayoutDashboard />
                <strong>{t("Dashboards")}</strong>
                <p>
                  {t(
                    "Group Insights charts together into dashboards to see trends and metrics across your organization.",
                  )}
                </p>
                {!query && (
                  <button type="button" onClick={onOpenCreate}>
                    <Plus />
                    {t("Create new dashboard")}
                  </button>
                )}
              </div>
            )}
          </div>
        </section>
      ) : widgetId ? (
        <InsightEditorPage
          dashboard={selected}
          data={data}
          widget={selected.widgets.find((item) => item.id === widgetId)}
          isNew={widgetId === "new"}
          onClose={() => {
            clearInsightURLState();
            onNavigate(selected.id);
          }}
          onExplore={onExploreIssues}
          onSave={(draft) => void saveInsight(draft)}
        />
      ) : (
        <section className="dashboard-detail">
          <div className="dashboard-title">
            <DashboardGlyph dashboard={selected} />
            <div>
              <input
                aria-label={t("Dashboard name")}
                value={selected.name}
                onChange={(event) =>
                  setItems((current) =>
                    current.map((item) =>
                      item.id === selected.id
                        ? { ...item, name: event.target.value }
                        : item,
                    ),
                  )
                }
                onBlur={(event) =>
                  void patchDashboard({ name: event.target.value })
                }
              />
              <input
                className="dashboard-description"
                aria-label={t("Description")}
                placeholder={t("Add a description…")}
                value={selected.description ?? ""}
                onChange={(event) =>
                  setItems((current) =>
                    current.map((item) =>
                      item.id === selected.id
                        ? { ...item, description: event.target.value }
                        : item,
                    ),
                  )
                }
                onBlur={(event) =>
                  void patchDashboard({ description: event.target.value })
                }
              />
            </div>
          </div>
          <div className="dashboard-filter-row">
            <button
              className="dashboard-filter-button"
              type="button"
              onClick={() => setFiltersOpen(true)}
            >
              <Filter />
              {t("Filter")}
            </button>
            <DashboardFilterSummary dashboard={selected} data={data} onOpen={() => setFiltersOpen(true)} />
            <span />
            <button
              className="dashboard-add-insight"
              type="button"
              onClick={() => openInsight()}
            >
              <Plus />
              {t("Add insight")}
            </button>
          </div>
          {loading ? (
            <LoaderCircle className="dashboard-spin" />
          ) : selected.widgets.length ? (
            <div className="dashboard-grid">
              {results.map((result, index) => (
                <DashboardCard
                  key={result.widget.id}
                  result={result}
                  onEdit={() => openInsight(result.widget.id)}
                  onExplore={() => onExploreIssues(readFilters(result.widget.config))}
                  onMove={(direction) => void reorderWidget(result.widget.id, index + direction)}
                  onDropWidget={(sourceId) => void reorderWidget(sourceId, index)}
                  onRemove={() =>
                    void patchDashboard({
                      widgets: selected.widgets.filter(
                        (widget) => widget.id !== result.widget.id,
                      ),
                    })
                  }
                  onResize={() =>
                    void patchDashboard({
                      widgets: selected.widgets.map((widget) =>
                        widget.id === result.widget.id
                          ? { ...widget, width: widget.width === 2 ? 1 : 2 }
                          : widget,
                      ),
                    })
                  }
                />
              ))}
            </div>
          ) : (
            <div className="dashboard-zero detail">
              <LayoutDashboard />
              <strong>{t("Add your first insight")}</strong>
              <p>{t("Choose a metric to start building this dashboard.")}</p>
              <button type="button" onClick={() => openInsight()}>
                <Plus />
                {t("Add insight")}
              </button>
            </div>
          )}
        </section>
      )}
      <CreateDashboardDialog
        open={createOpen}
        busy={busy}
        name={draftName}
        description={draftDescription}
        visibility={draftVisibility}
        teamId={draftTeamId}
        teams={data.teams}
        onName={setDraftName}
        onDescription={setDraftDescription}
        onVisibility={setDraftVisibility}
        onTeam={setDraftTeamId}
        onClose={() => { setCreateOpen(false); onNavigate(); }}
        onCreate={() => void create()}
      />
      <Dialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <DialogContent className="dashboard-dialog">
          <DialogTitle>{t("Delete dashboard?")}</DialogTitle>
          <p>{t("This action cannot be undone.")}</p>
          <footer>
            <button type="button" onClick={() => setDeleteOpen(false)}>
              {t("Cancel")}
            </button>
            <button
              className="danger"
              disabled={busy}
              type="button"
              onClick={() => void remove()}
            >
              {t("Delete")}
            </button>
          </footer>
        </DialogContent>
      </Dialog>
      {selected && (
        <FilterDialog
          open={filtersOpen}
          data={data}
          filters={selected.filters ?? {}}
          hideFilters={Boolean(selected.hideFilters)}
          onClose={() => setFiltersOpen(false)}
          onSave={(filters, hideFilters) =>
            void patchDashboard({ filters, hideFilters }).then(() =>
              setFiltersOpen(false),
            )
          }
        />
      )}
    </div>
  );
}

function DashboardHeader({
  selected,
  sectionTitle,
  favorite,
  subscribed,
  data,
  onBack,
  onDelete,
  onDisableShare,
  onFavorite,
  onOpenSidebar,
  onOwner,
  onRefresh,
  onShare,
  onSubscribe,
  onVisibility,
}: {
  selected?: Dashboard;
  sectionTitle?: string;
  favorite: boolean;
  subscribed: boolean;
  data: BootstrapData;
  onBack: () => void;
  onDelete: () => void;
  onDisableShare: () => void;
  onFavorite: () => void;
  onOpenSidebar: () => void;
  onOwner: (id: string) => void;
  onRefresh: () => void;
  onShare: () => void;
  onSubscribe: () => void;
  onVisibility: (
    visibility: Dashboard["visibility"],
    teamIds: string[],
  ) => void;
}) {
  const { t } = useI18n();
  return (
    <header className="dashboards-header">
      <button className="mobile-menu" type="button" onClick={onOpenSidebar}>
        Menu
      </button>
      {selected ? (
        <>
          <button className="dashboard-crumb" type="button" onClick={onBack}>
            {t("Dashboards")}
          </button>
          <span>›</span>
          <DashboardGlyph dashboard={selected} />
          <strong data-i18n-ignore>{selected.name}{sectionTitle && <small>› {sectionTitle}</small>}</strong>
          <DashboardMenu
            dashboard={selected}
            data={data}
            onDelete={onDelete}
            onDisableShare={onDisableShare}
            onOwner={onOwner}
            onRefresh={onRefresh}
            onVisibility={onVisibility}
          />
          <button
            className="dashboard-favorite"
            aria-label={t(favorite ? "Remove from favorites" : "Add to favorites")}
            type="button"
            onClick={onFavorite}
          >
            <Star fill={favorite ? "currentColor" : "none"} />
          </button>
          <i />
          <small>
            {t("Refreshed")} {relative(selected.updatedAt)}
          </small>
          <button
            className="dashboard-header-icon"
            aria-label={t(subscribed ? "Unsubscribe" : "Subscribe")}
            type="button"
            onClick={onSubscribe}
          >
            {subscribed ? <BellOff /> : <Bell />}
          </button>
          <button
            className="dashboard-header-icon"
            aria-label={t("Share")}
            type="button"
            onClick={onShare}
          >
            <Link2 />
          </button>
        </>
      ) : null}
    </header>
  );
}

function DashboardSection({
  title,
  scope,
  items,
  data,
  columns,
  canCreate = true,
  hideHeader = false,
  onOpen,
  onCreate,
}: {
  title: string;
  scope?: string;
  items: Dashboard[];
  data: BootstrapData;
  columns: Set<DashboardColumn>;
  canCreate?: boolean;
  hideHeader?: boolean;
  onOpen: (id: string) => void;
  onCreate?: () => void;
}) {
  if (!items.length && title !== data.workspace.name && !hideHeader)
    return null;
  return (
    <section className="dashboard-section" data-plain={hideHeader || undefined}>
      {!hideHeader && (
        <header>
          <WorkspaceMark label={title} />
          <strong data-i18n-ignore>{title}</strong>
          {scope && <span>· {scope}</span>}
          {canCreate && onCreate && (
            <button
              aria-label={`Add dashboard to ${title}`}
              type="button"
              onClick={onCreate}
            >
              <Plus />
            </button>
          )}
        </header>
      )}
      {items.map((item) => {
        const owner = data.users.find((user) => user.id === item.ownerId);
        return (
          <button
            className="dashboard-row"
            style={
              {
                "--dashboard-columns": dashboardColumns(columns),
              } as React.CSSProperties
            }
            key={item.id}
            onClick={() => onOpen(item.id)}
            type="button"
          >
            <DashboardGlyph dashboard={item} />
            <strong data-i18n-ignore>{item.name}</strong>
            {columns.has("createdAt") && (
              <time>{shortDate(item.createdAt)}</time>
            )}
            {columns.has("updatedAt") && (
              <time>{shortDate(item.updatedAt)}</time>
            )}
            {columns.has("owner") && <Owner owner={owner} />}
          </button>
        );
      })}
    </section>
  );
}

function DashboardGlyph({ dashboard }: { dashboard: Dashboard }) {
  return (
    <span className={`dashboard-glyph color-${hash(dashboard.id) % 5}`}>
      <BarChart3 />
    </span>
  );
}
function WorkspaceMark({ label }: { label: string }) {
  return (
    <span className="dashboard-workspace-mark">
      {label.slice(0, 1).toUpperCase()}
    </span>
  );
}
function Owner({ owner }: { owner?: User }) {
  return (
    <span className="dashboard-owner">
      <span>{initials(owner?.displayName ?? "?")}</span>
      <b data-i18n-ignore>{owner?.displayName ?? "Unknown"}</b>
    </span>
  );
}

function DashboardFilterSummary({
  dashboard,
  data,
  onOpen,
}: {
  dashboard: Dashboard;
  data: BootstrapData;
  onOpen: () => void;
}) {
  if (dashboard.hideFilters)
    return dashboard.filters &&
      Object.values(dashboard.filters).some(Boolean) ? (
      <button className="dashboard-saved-filters" type="button" onClick={onOpen}>
        <Filter />
        Saved filters
      </button>
    ) : null;
  const chips = filterChips(dashboard.filters ?? {}, data);
  return (
    <>
      {chips.map((chip) => (
        <span className="dashboard-filter-chip" key={chip}>
          {chip}
        </span>
      ))}
    </>
  );
}

function DashboardDirectoryMenu({
  columns,
  direction,
  ordering,
  onColumns,
  onDirection,
  onOrdering,
}: {
  columns: Set<DashboardColumn>;
  direction: "asc" | "desc";
  ordering: DashboardOrdering;
  onColumns: (value: Set<DashboardColumn>) => void;
  onDirection: (value: "asc" | "desc") => void;
  onOrdering: (value: DashboardOrdering) => void;
}) {
  const { t } = useI18n();
  const toggle = (column: DashboardColumn) => {
    const next = new Set(columns);
    if (next.has(column)) next.delete(column);
    else next.add(column);
    onColumns(next);
  };
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <button
          className="dashboard-directory-options"
          aria-label={t("Display options")}
          type="button"
        >
          <SlidersHorizontal />
        </button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          align="end"
          className="dashboard-menu"
          sideOffset={4}
        >
          <DropdownMenu.Label>{t("Ordering")}</DropdownMenu.Label>
          {(["name", "owner", "updatedAt", "createdAt"] as const).map(
            (value) => (
              <DropdownMenu.Item key={value} onSelect={() => onOrdering(value)}>
                {t(
                  value === "updatedAt"
                    ? "Updated"
                    : value === "createdAt"
                      ? "Created"
                      : value === "owner"
                        ? "Owner"
                        : "Name",
                )}
                {ordering === value && <Check className="end" />}
              </DropdownMenu.Item>
            ),
          )}
          <DropdownMenu.Separator />
          <DropdownMenu.Item
            onSelect={() => onDirection(direction === "asc" ? "desc" : "asc")}
          >
            {t(direction === "asc" ? "Ascending" : "Descending")}
          </DropdownMenu.Item>
          <DropdownMenu.Separator />
          <DropdownMenu.Label>{t("Display properties")}</DropdownMenu.Label>
          {(["createdAt", "updatedAt", "owner"] as const).map((value) => (
            <DropdownMenu.CheckboxItem
              checked={columns.has(value)}
              key={value}
              onCheckedChange={() => toggle(value)}
              onSelect={(event) => event.preventDefault()}
            >
              {columns.has(value) && <Check />}
              {t(
                value === "updatedAt"
                  ? "Updated"
                  : value === "createdAt"
                    ? "Created"
                    : "Owner",
              )}
            </DropdownMenu.CheckboxItem>
          ))}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}

function DashboardMenu({
  dashboard,
  data,
  onDelete,
  onDisableShare,
  onOwner,
  onRefresh,
  onVisibility,
}: {
  dashboard: Dashboard;
  data: BootstrapData;
  onDelete: () => void;
  onDisableShare: () => void;
  onOwner: (id: string) => void;
  onRefresh: () => void;
  onVisibility: (value: Dashboard["visibility"], teamIds: string[]) => void;
}) {
  const { t } = useI18n();
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <button
          className="dashboard-menu-trigger"
          aria-label={t("Open menu")}
          type="button"
        >
          <MoreHorizontal />
        </button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          align="start"
          className="dashboard-menu"
          sideOffset={5}
        >
          <DropdownMenu.Sub>
            <DropdownMenu.SubTrigger>
              <UsersRound />
              {t("Change owner")}
              <ChevronDown className="end" />
            </DropdownMenu.SubTrigger>
            <DropdownMenu.Portal>
              <DropdownMenu.SubContent
                className="dashboard-menu"
                sideOffset={5}
              >
                {data.users.map((user) => (
                  <DropdownMenu.Item
                    key={user.id}
                    onSelect={() => onOwner(user.id)}
                  >
                    <Owner owner={user} />
                    {dashboard.ownerId === user.id && <Check className="end" />}
                  </DropdownMenu.Item>
                ))}
              </DropdownMenu.SubContent>
            </DropdownMenu.Portal>
          </DropdownMenu.Sub>
          <DropdownMenu.Sub>
            <DropdownMenu.SubTrigger>
              <Share2 />
              {t("Move to…")}
              <ChevronDown className="end" />
            </DropdownMenu.SubTrigger>
            <DropdownMenu.Portal>
              <DropdownMenu.SubContent
                className="dashboard-menu"
                sideOffset={5}
              >
                <DropdownMenu.Item
                  onSelect={() => onVisibility("workspace", [])}
                >
                  {t("Workspace")}
                  {dashboard.visibility === "workspace" && (
                    <Check className="end" />
                  )}
                </DropdownMenu.Item>
                <DropdownMenu.Item onSelect={() => onVisibility("private", [])}>
                  {t("Personal")}
                  {dashboard.visibility === "private" && (
                    <Check className="end" />
                  )}
                </DropdownMenu.Item>
                {data.teams.map((team) => (
                  <DropdownMenu.Item
                    key={team.id}
                    onSelect={() => onVisibility("team", [team.id])}
                  >
                    <span data-i18n-ignore>{team.name}</span>
                    {dashboard.visibility === "team" &&
                      dashboard.teamIds.includes(team.id) && (
                        <Check className="end" />
                      )}
                  </DropdownMenu.Item>
                ))}
              </DropdownMenu.SubContent>
            </DropdownMenu.Portal>
          </DropdownMenu.Sub>
          <DropdownMenu.Item
            onSelect={() => void navigator.clipboard.writeText(location.href)}
          >
            <Copy />
            {t("Copy link")}
          </DropdownMenu.Item>
          <DropdownMenu.Item asChild>
            <a href={dashboardExportURL(dashboard.id)} download>
              <Download />
              {t("Export insights as CSV…")}
            </a>
          </DropdownMenu.Item>
          {dashboard.shareToken && (
            <DropdownMenu.Item onSelect={onDisableShare}>
              <Link2 />
              {t("Disable public link")}
            </DropdownMenu.Item>
          )}
          <DropdownMenu.Separator />
          <DropdownMenu.Item onSelect={onRefresh}>
            <RefreshCw />
            {t("Refresh data")}
          </DropdownMenu.Item>
          <DropdownMenu.Separator />
          <DropdownMenu.Item className="danger" onSelect={onDelete}>
            <Trash2 />
            {t("Delete dashboard")}
          </DropdownMenu.Item>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}

function DashboardCard({
  result,
  onEdit,
  onExplore,
  onMove,
  onDropWidget,
  onRemove,
  onResize,
}: {
  result: DashboardWidgetResult;
  onEdit: () => void;
  onExplore: () => void;
  onMove: (direction: -1 | 1) => void;
  onDropWidget: (sourceId: string) => void;
  onRemove: () => void;
  onResize: () => void;
}) {
  const { t } = useI18n();
  const display = String(
    result.widget.config?.display ??
      (result.widget.type === "issue_count" ? "metric" : "chart"),
  ) as InsightDisplay;
  return (
    <article
      className={result.widget.width === 2 ? "wide" : ""}
      draggable
      onDragStart={(event) => {
        event.dataTransfer.effectAllowed = "move";
        event.dataTransfer.setData("text/dashboard-widget", result.widget.id);
        event.currentTarget.dataset.dragging = "true";
      }}
      onDragEnd={(event) => delete event.currentTarget.dataset.dragging}
      onDragOver={(event) => {
        event.preventDefault();
        event.dataTransfer.dropEffect = "move";
      }}
      onDrop={(event) => {
        event.preventDefault();
        const sourceId = event.dataTransfer.getData("text/dashboard-widget");
        if (sourceId && sourceId !== result.widget.id) onDropWidget(sourceId);
      }}
    >
      <header>
        <strong>{result.widget.title}</strong>
        <DropdownMenu.Root>
          <DropdownMenu.Trigger asChild>
            <button aria-label={t("Open insight menu")} type="button">
              <MoreHorizontal />
            </button>
          </DropdownMenu.Trigger>
          <DropdownMenu.Portal>
            <DropdownMenu.Content
              align="end"
              className="dashboard-menu"
              sideOffset={4}
            >
              <DropdownMenu.Item onSelect={onEdit}>
                <Filter />
                {t("Edit insight")}
              </DropdownMenu.Item>
              <DropdownMenu.Item onSelect={onExplore}>
                <Expand />
                {t("Explore issues")}
              </DropdownMenu.Item>
              <DropdownMenu.Item onSelect={() => onMove(-1)}>
                <ArrowUp />
                {t("Move earlier")}
              </DropdownMenu.Item>
              <DropdownMenu.Item onSelect={() => onMove(1)}>
                <ArrowDown />
                {t("Move later")}
              </DropdownMenu.Item>
              <DropdownMenu.Item onSelect={onResize}>
                <LayoutDashboard />
                {t(result.widget.width === 2 ? "Half width" : "Full width")}
              </DropdownMenu.Item>
              <DropdownMenu.Item className="danger" onSelect={onRemove}>
                <Trash2 />
                {t("Remove from dashboard")}
              </DropdownMenu.Item>
            </DropdownMenu.Content>
          </DropdownMenu.Portal>
        </DropdownMenu.Root>
      </header>
      <WidgetValue result={result} display={display} />
    </article>
  );
}

function WidgetValue({
  result,
  display,
}: {
  result: DashboardWidgetResult;
  display: InsightDisplay;
}) {
  if (result.widget.type === "issue_count")
    return (
      <div className="metric-number">
        {String((result.value as { count: number }).count)}
      </div>
    );
  if (Array.isArray(result.value)) {
    const rows = result.value.slice(0, 10);
    if (display === "chart") {
      const points: LinearInsightPoint[] = rows.map((row, index) => ({
        id: String((row as { id?: string }).id ?? index),
        label: String((row as { name?: string }).name ?? "Item"),
        value:
          "progress" in (row as object)
            ? Math.round(Number((row as { progress: number }).progress) * 100)
            : Number((row as { completed?: number }).completed ?? 0),
      }));
      return <LinearInsightBar points={points} />;
    }
    return (
      <div className="metric-list">
        {rows.map((row, index) => (
          <div key={String((row as { id?: string }).id ?? index)}>
            <span>{String((row as { name?: string }).name ?? "Item")}</span>
            <strong>
              {"progress" in (row as object)
                ? `${Math.round(Number((row as { progress: number }).progress) * 100)}%`
                : `${String((row as { completed?: number }).completed ?? 0)} / ${String((row as { total?: number }).total ?? 0)}`}
            </strong>
          </div>
        ))}
      </div>
    );
  }
  const entries = Object.entries(
    (result.value ?? {}) as Record<string, unknown>,
  );
  const total = entries.reduce(
    (sum, [, value]) => sum + (Number(value) || 0),
    0,
  );
  if (display === "metric") return <div className="metric-number">{total}</div>;
  if (!entries.length) return <span className="metric-muted">No data</span>;
  if (display === "table")
    return (
      <div className="metric-list">
        {entries.map(([label, value]) => (
          <div key={label}>
            <span>{label}</span>
            <strong>{String(value)}</strong>
          </div>
        ))}
      </div>
    );
  const points: LinearInsightPoint[] = entries
    .slice(0, 10)
    .map(([label, value]) => ({ id: label, label, value: Number(value) || 0 }));
  return (
    <>
      {result.widget.type === "throughput" ? (
        <LinearInsightLine points={points} />
      ) : (
        <LinearInsightBar points={points} />
      )}
      <div className="metric-chart-table">
        {entries.slice(0, 6).map(([label, value]) => (
          <div key={label}>
            <span>{label}</span>
            <strong>{String(value)}</strong>
          </div>
        ))}
      </div>
    </>
  );
}

type InsightDraft = {
  title: string;
  description: string;
  display: InsightDisplay;
  measure: NonNullable<DashboardInsightConfig["measure"]>;
  aggregation: NonNullable<DashboardInsightConfig["aggregation"]>;
  slice: NonNullable<DashboardInsightConfig["slice"]>;
  segment: NonNullable<DashboardInsightConfig["segment"]>;
  dateAggregation: NonNullable<DashboardInsightConfig["dateAggregation"]>;
  filters: DashboardFilters;
};
function InsightEditorPage({
  dashboard,
  data,
  widget,
  isNew,
  onClose,
  onExplore,
  onSave,
}: {
  dashboard: Dashboard;
  data: BootstrapData;
  widget?: DashboardWidget;
  isNew: boolean;
  onClose: () => void;
  onExplore: (filters: DashboardFilters) => void;
  onSave: (draft: InsightDraft) => void;
}) {
  const { t } = useI18n();
  const initialDraft = useMemo(() => insightDraftFromWidget(widget), [widget]);
  const [draft, setDraft] = useState(initialDraft);
  const [preview, setPreview] = useState<DashboardWidgetResult>();
  const [previewing, setPreviewing] = useState(false);
  const [previewError, setPreviewError] = useState("");
  const initialSerialized = JSON.stringify(initialDraft);
  const dirty = JSON.stringify(draft) !== initialSerialized;
  const allowNavigationRef = useRef(false);
  useEffect(() => {
    const restored = readInsightURLState();
    setDraft(restored ?? initialDraft);
  }, [initialDraft, initialSerialized]);
  useEffect(() => {
    const timeout = window.setTimeout(() => {
      setPreviewing(true);
      setPreviewError("");
      void previewDashboardWidget(dashboard.id, insightWidgetFromDraft(draft, widget))
        .then(setPreview)
        .catch(() => setPreviewError(t("Could not load insight preview")))
        .finally(() => setPreviewing(false));
    }, 180);
    return () => window.clearTimeout(timeout);
  }, [dashboard.id, draft, t, widget]);
  useEffect(() => {
    const params = new URLSearchParams(location.search);
    params.set("insight", JSON.stringify({
      display: draft.display,
      measure: draft.measure,
      aggregation: draft.aggregation,
      slice: draft.slice,
      segment: draft.segment,
      dateAggregation: draft.dateAggregation,
      title: draft.title,
      description: draft.description,
    }));
    params.set("insightFilter", JSON.stringify(draft.filters));
    history.replaceState(history.state, "", `${location.pathname}?${params.toString()}`);
  }, [draft]);
  useEffect(() => {
    const protect = (event: BeforeUnloadEvent) => {
      if (!dirty) return;
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", protect);
    return () => window.removeEventListener("beforeunload", protect);
  }, [dirty]);
  useEffect(() => {
    if (!dirty) return;
    const currentURL = location.href;
    const guardLinks = (event: MouseEvent) => {
      const link = (event.target as Element | null)?.closest("a[href]");
      if (!link || allowNavigationRef.current) return;
      event.preventDefault();
      event.stopPropagation();
      void confirmAction(t("Discard unsaved insight changes?"),{confirmLabel:t("Discard")}).then(confirmed=>{if(confirmed){allowNavigationRef.current=true;location.assign((link as HTMLAnchorElement).href)}});
    };
    const guardHistory = () => {
      if (allowNavigationRef.current) return;
      history.pushState(history.state, "", currentURL);
      void confirmAction(t("Discard unsaved insight changes?"),{confirmLabel:t("Discard")}).then(confirmed=>{if(confirmed){allowNavigationRef.current=true;history.back()}});
    };
    document.addEventListener("click", guardLinks, true);
    window.addEventListener("popstate", guardHistory);
    return () => {
      document.removeEventListener("click", guardLinks, true);
      window.removeEventListener("popstate", guardHistory);
    };
  }, [dirty, t]);
  const update = <K extends keyof InsightDraft>(key: K, value: InsightDraft[K]) =>
    setDraft((current) => ({ ...current, [key]: value }));
  const close = () => { if(!dirty){onClose();return} void confirmAction(t("Discard unsaved insight changes?"),{confirmLabel:t("Discard")}).then(confirmed=>{if(confirmed)onClose()}) };
  return (
    <section className="dashboard-insight-editor">
      <header className="dashboard-insight-editor-title">
        <div>
          <input aria-label={t("Insight name")} autoFocus={isNew} value={draft.title} placeholder={t(insightDefaultTitle(draft))} onChange={(event) => update("title", event.target.value)} />
          <input aria-label={t("Insight description")} value={draft.description} placeholder={t("Add a description…")} onChange={(event) => update("description", event.target.value)} />
        </div>
        <nav>
          {!isNew && dirty && <button type="button" onClick={() => setDraft(initialDraft)}>{t("Reset")}</button>}
          <button type="button" onClick={close}>{t("Cancel")}</button>
          <button className="primary" type="button" onClick={() => onSave(draft)}>{t(isNew ? "Add to dashboard" : "Save")}</button>
        </nav>
      </header>
      <div className="dashboard-insight-editor-controls">
        <InsightControl label={t("Measure")} value={draft.measure} options={insightMeasureOptions(t)} onChange={(value) => setDraft((current) => ({ ...current, measure: value, aggregation: defaultInsightAggregation(value) }))} />
        <InsightControl label={t("Aggregation")} value={draft.aggregation} options={insightAggregationOptions(t)} onChange={(value) => update("aggregation", value)} />
        <InsightControl label={t("Slice")} value={draft.slice} options={insightSliceOptions(t)} onChange={(value) => update("slice", value)} />
        <InsightControl label={t("Segment")} value={draft.segment} options={insightSegmentOptions(t)} onChange={(value) => update("segment", value)} />
        {(draft.slice === "created_at" || draft.slice === "completed_at") && <InsightControl label={t("Interval")} value={draft.dateAggregation} options={insightDateOptions(t)} onChange={(value) => update("dateAggregation", value)} />}
      </div>
      <div className="dashboard-insight-editor-body">
        <aside>
          <fieldset>
            <legend>{t("Display as")}</legend>
            <div className="dashboard-segments">
              {(["chart", "table", "metric"] as const).map((display) => <button className={draft.display === display ? "active" : ""} key={display} type="button" onClick={() => update("display", display)}>{display === "chart" ? <BarChart3 /> : display === "table" ? <Table2 /> : <LayoutDashboard />}{t(display === "chart" ? "Chart" : display === "table" ? "Table" : "Metric")}</button>)}
            </div>
          </fieldset>
          <InsightFilterBuilder data={data} filters={draft.filters} onChange={(filters) => update("filters", filters)} />
        </aside>
        <section className="dashboard-insight-preview" aria-label={t("Insight preview")}>
          <header><strong>{draft.title.trim() || t(insightDefaultTitle(draft))}</strong>{previewing && <LoaderCircle className="dashboard-spin" />}<button aria-label={t("Explore issues")} type="button" onClick={() => onExplore(draft.filters)}><Expand /></button></header>
          <div>{previewError ? <span className="metric-muted">{previewError}</span> : preview ? <WidgetValue result={preview} display={draft.display} /> : <LoaderCircle className="dashboard-spin" />}</div>
        </section>
      </div>
    </section>
  );
}

function InsightControl<T extends string>({ label, value, options, onChange }: { label: string; value: T; options: DashboardSelectOption<T>[]; onChange: (value: T) => void }) {
  return <label><span>{label}</span><DashboardSelect ariaLabel={label} options={options} value={value} onChange={onChange}/></label>;
}

function InsightFilterBuilder({ data, filters, onChange }: { data: BootstrapData; filters: DashboardFilters; onChange: (filters: DashboardFilters) => void }) {
  const { t } = useI18n();
  const toggle = (key: keyof DashboardFilters, id: string) => {
    const selected = filters[key] ?? [];
    onChange({ ...filters, [key]: selected.includes(id) ? selected.filter((value) => value !== id) : [...selected, id] });
  };
  const groupNames = new Map(data.labelGroups.map((group) => [group.id, group]));
  return <section className="dashboard-insight-filter-builder">
    <header><Filter/><strong>{t("Filters")}</strong></header>
    <div>
      <PropertyMenu compact multiple label={t("Teams")} value={(filters.teamIds?.length ?? 0) ? `${filters.teamIds!.length} ${t("Teams")}` : t("All teams")} selectedIds={filters.teamIds ?? []} triggerClassName="dashboard-filter-property" icon={<TeamIcon size={14}/>} options={data.teams.map((team) => ({ id: team.id, label: team.name, color: team.color, icon: <TeamIcon size={14}/>, i18nIgnore: true }))} onChange={(id) => toggle("teamIds", id)}/>
      <PropertyMenu compact multiple label={t("Statuses")} value={(filters.stateIds?.length ?? 0) ? `${filters.stateIds!.length} ${t("Statuses")}` : t("All statuses")} selectedIds={filters.stateIds ?? []} triggerClassName="dashboard-filter-property" icon={<Filter size={14}/>} options={data.states.map((state) => ({ id: state.id, label: state.name, color: state.color, icon: <StatusIcon state={state}/>, i18nIgnore: true }))} onChange={(id) => toggle("stateIds", id)}/>
      <PropertyMenu compact multiple label={t("Assignees")} value={(filters.assigneeIds?.length ?? 0) ? `${filters.assigneeIds!.length} ${t("Assignees")}` : t("All assignees")} selectedIds={filters.assigneeIds ?? []} triggerClassName="dashboard-filter-property" icon={<NoAssigneeIcon size={14}/>} options={data.users.map((user) => ({ id: user.id, label: user.displayName, i18nIgnore: true }))} onChange={(id) => toggle("assigneeIds", id)}/>
      <PropertyMenu compact multiple kind="labels" label={t("Labels")} value={(filters.labelIds?.length ?? 0) ? `${filters.labelIds!.length} ${t("Labels")}` : t("All labels")} selectedIds={filters.labelIds ?? []} triggerClassName="dashboard-filter-property" icon={<LabelIcon size={14}/>} options={data.labels.filter((label) => label.resourceType === "issue").map((label) => { const group = label.groupId ? groupNames.get(label.groupId) : undefined; return { id: label.id, label: label.name, color: label.color, description: label.description, issueCount: label.issueCount, groupId: label.groupId, groupLabel: group?.name, groupColor: group?.color, i18nIgnore: true }; })} onChange={(id) => toggle("labelIds", id)}/>
    </div>
  </section>;
}

function insightDraftFromWidget(widget?: DashboardWidget): InsightDraft {
  const config = widget?.config ?? {};
  const legacy = widget?.type;
  return {
    title: widget?.title ?? "",
    description: widget?.description ?? "",
    display: (config.display as InsightDisplay | undefined) ?? (legacy === "issue_count" ? "metric" : "chart"),
    measure: config.measure ?? (legacy === "sla_health" ? "sla_breaches" : "issue_count"),
    aggregation: config.aggregation ?? "count",
    slice: config.slice ?? (legacy === "status_breakdown" ? "status" : legacy === "assignee_workload" ? "assignee" : legacy === "cycle_progress" ? "cycle" : legacy === "project_progress" ? "project" : legacy === "throughput" ? "completed_at" : "none"),
    segment: config.segment ?? "none",
    dateAggregation: config.dateAggregation ?? (legacy === "throughput" ? "day" : "month"),
    filters: readFilters(config),
  };
}

function insightWidgetFromDraft(draft: InsightDraft, existing?: DashboardWidget): DashboardWidget {
  return {
    id: existing?.id ?? "preview",
    type: "insight",
    title: draft.title.trim() || insightDefaultTitle(draft),
    description: draft.description.trim(),
    position: existing?.position ?? 0,
    width: existing?.width ?? 1,
    config: {
      display: draft.display,
      measure: draft.measure,
      aggregation: draft.aggregation,
      slice: draft.slice,
      segment: draft.segment,
      dateAggregation: draft.dateAggregation,
      ...draft.filters,
    },
  };
}

function readInsightURLState(): InsightDraft | undefined {
  try {
    const params = new URLSearchParams(location.search);
    const settings = JSON.parse(params.get("insight") ?? "null") as Partial<InsightDraft> | null;
    const filters = JSON.parse(params.get("insightFilter") ?? "null") as DashboardFilters | null;
    if (!settings) return undefined;
    return { ...insightDraftFromWidget(), ...settings, filters: filters ?? {} };
  } catch {
    return undefined;
  }
}

function clearInsightURLState() {
  const params = new URLSearchParams(location.search);
  params.delete("insight");
  params.delete("insightFilter");
  const search = params.toString();
  history.replaceState(history.state, "", `${location.pathname}${search ? `?${search}` : ""}`);
}

function insightDefaultTitle(draft: Pick<InsightDraft, "measure" | "slice" | "segment">) {
  const measure = draft.measure === "issue_count" ? "Issue count" : draft.measure === "cycle_time" ? "Cycle time" : draft.measure === "lead_time" ? "Lead time" : draft.measure === "sla_breaches" ? "SLA breaches" : "Estimate";
  const slice = draft.slice === "none" ? "" : ` by ${draft.slice.replace("_at", " date").replaceAll("_", " ")}`;
  const segment = draft.segment === "none" ? "" : ` and ${draft.segment.replaceAll("_", " ")}`;
  return `${measure}${slice}${segment}`;
}

function defaultInsightAggregation(measure: InsightDraft["measure"]): InsightDraft["aggregation"] {
  return measure === "cycle_time" || measure === "lead_time" ? "average" : measure === "estimate" || measure === "sla_breaches" ? "sum" : "count";
}

type InsightTranslator = (value: string) => string;
function insightMeasureOptions(t: InsightTranslator): DashboardSelectOption<InsightDraft["measure"]>[] { return [
  { value: "issue_count", label: t("Issue count"), description: t("Number of issues matching the filters") },
  { value: "estimate", label: t("Estimate"), description: t("Issue estimate points") },
  { value: "cycle_time", label: t("Cycle time"), description: t("Time from started to completed") },
  { value: "lead_time", label: t("Lead time"), description: t("Time from created to completed") },
  { value: "sla_breaches", label: t("SLA breaches"), description: t("Issues that breached their SLA") },
]; }
function insightAggregationOptions(t: InsightTranslator): DashboardSelectOption<InsightDraft["aggregation"]>[] { return ["count", "sum", "average", "minimum", "maximum"].map((value) => ({ value: value as InsightDraft["aggregation"], label: t(value[0].toUpperCase() + value.slice(1)) })); }
function insightSliceOptions(t: InsightTranslator): DashboardSelectOption<InsightDraft["slice"]>[] { return ["none", "status", "team", "assignee", "label", "project", "cycle", "priority", "created_at", "completed_at"].map((value) => ({ value: value as InsightDraft["slice"], label: t(value === "none" ? "No slice" : value === "created_at" ? "Created date" : value === "completed_at" ? "Completed date" : value[0].toUpperCase() + value.slice(1)) })); }
function insightSegmentOptions(t: InsightTranslator): DashboardSelectOption<InsightDraft["segment"]>[] { return ["none", "status", "team", "assignee", "project", "priority"].map((value) => ({ value: value as InsightDraft["segment"], label: t(value === "none" ? "No segment" : value[0].toUpperCase() + value.slice(1)) })); }
function insightDateOptions(t: InsightTranslator): DashboardSelectOption<InsightDraft["dateAggregation"]>[] { return ["day", "week", "month", "quarter", "year"].map((value) => ({ value: value as InsightDraft["dateAggregation"], label: t(value[0].toUpperCase() + value.slice(1)) })); }

function FilterDialog({
  open,
  data,
  filters,
  hideFilters,
  onClose,
  onSave,
}: {
  open: boolean;
  data: BootstrapData;
  filters: DashboardFilters;
  hideFilters: boolean;
  onClose: () => void;
  onSave: (filters: DashboardFilters, hide: boolean) => void;
}) {
  const { t } = useI18n();
  const [draft, setDraft] = useState(filters);
  const [hide, setHide] = useState(hideFilters);
  useEffect(() => {
    if (open) {
      setDraft(filters);
      setHide(hideFilters);
    }
  }, [open, filters, hideFilters]);
  return (
    <Dialog open={open} onOpenChange={(value) => !value && onClose()}>
      <DialogContent className="dashboard-dialog dashboard-filter-dialog">
        <DialogTitle>{t("Dashboard filters")}</DialogTitle>
        <p>{t("These filters apply to every insight on this dashboard.")}</p>
        <FilterFields data={data} filters={draft} onChange={setDraft} />
        <label className="dashboard-hide-filters">
          <input
            type="checkbox"
            checked={hide}
            onChange={(event) => setHide(event.target.checked)}
          />
          <span>
            <strong>{t("Hide saved filters")}</strong>
            <small>{t("Filters remain active when hidden.")}</small>
          </span>
        </label>
        <footer>
          <button type="button" onClick={onClose}>
            {t("Cancel")}
          </button>
          <button
            className="primary"
            type="button"
            onClick={() => onSave(draft, hide)}
          >
            {t("Save filters")}
          </button>
        </footer>
      </DialogContent>
    </Dialog>
  );
}

function FilterFields({
  data,
  filters,
  onChange,
}: {
  data: BootstrapData;
  filters: DashboardFilters;
  onChange: (value: DashboardFilters) => void;
}) {
  const { t } = useI18n();
  return (
    <div className="dashboard-filter-fields">
      <FilterGroup
        title={t("Teams")}
        values={data.teams}
        selected={filters.teamIds ?? []}
        label={(item) => item.name}
        onChange={(teamIds) => onChange({ ...filters, teamIds })}
      />
      <FilterGroup
        title={t("Statuses")}
        values={data.states}
        selected={filters.stateIds ?? []}
        label={(item) => item.name}
        onChange={(stateIds) => onChange({ ...filters, stateIds })}
      />
      <FilterGroup
        title={t("Assignees")}
        values={data.users}
        selected={filters.assigneeIds ?? []}
        label={(item) => item.displayName}
        onChange={(assigneeIds) => onChange({ ...filters, assigneeIds })}
      />
      <FilterGroup
        title={t("Labels")}
        values={data.labels.filter(
          (label) => label.resourceType === "issue" && !label.groupId,
        )}
        selected={filters.labelIds ?? []}
        label={(item) => item.name}
        onChange={(labelIds) => onChange({ ...filters, labelIds })}
      />
    </div>
  );
}
function FilterGroup<T extends Team | WorkflowState | User | IssueLabel>({
  title,
  values,
  selected,
  label,
  onChange,
}: {
  title: string;
  values: T[];
  selected: string[];
  label: (item: T) => string;
  onChange: (ids: string[]) => void;
}) {
  return (
    <fieldset>
      <legend>{title}</legend>
      <div>
        {values.map((item) => (
          <label key={item.id}>
            <input
              type="checkbox"
              checked={selected.includes(item.id)}
              onChange={(event) =>
                onChange(
                  event.target.checked
                    ? [...selected, item.id]
                    : selected.filter((id) => id !== item.id),
                )
              }
            />
            <span data-i18n-ignore>{label(item)}</span>
          </label>
        ))}
      </div>
    </fieldset>
  );
}

type DashboardSelectOption<T extends string> = {
  value: T;
  label: string;
  description?: string;
  i18nIgnore?: boolean;
};

function DashboardSelect<T extends string>({
  ariaLabel,
  options,
  value,
  onChange,
}: {
  ariaLabel: string;
  options: DashboardSelectOption<T>[];
  value: T;
  onChange: (value: T) => void;
}) {
  const selected = options.find((option) => option.value === value);
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <button
          aria-label={ariaLabel}
          className="dashboard-select-trigger"
          type="button"
        >
          <span data-i18n-ignore={selected?.i18nIgnore || undefined}>
            {selected?.label ?? value}
          </span>
          <ChevronDown />
        </button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          align="start"
          className="dashboard-menu dashboard-select-menu"
          collisionPadding={8}
          sideOffset={4}
        >
          <DropdownMenu.RadioGroup
            value={value}
            onValueChange={(next) => onChange(next as T)}
          >
            {options.map((option) => (
              <DropdownMenu.RadioItem
                className="dashboard-select-option"
                key={option.value}
                value={option.value}
              >
                <span className="dashboard-select-copy">
                  <strong data-i18n-ignore={option.i18nIgnore || undefined}>
                    {option.label}
                  </strong>
                  {option.description && <small>{option.description}</small>}
                </span>
                <DropdownMenu.ItemIndicator className="dashboard-select-check">
                  <Check />
                </DropdownMenu.ItemIndicator>
              </DropdownMenu.RadioItem>
            ))}
          </DropdownMenu.RadioGroup>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}

function CreateDashboardDialog({
  open,
  busy,
  name,
  description,
  visibility,
  teamId,
  teams,
  onName,
  onDescription,
  onVisibility,
  onTeam,
  onClose,
  onCreate,
}: {
  open: boolean;
  busy: boolean;
  name: string;
  description: string;
  visibility: Dashboard["visibility"];
  teamId: string;
  teams: Team[];
  onName: (value: string) => void;
  onDescription: (value: string) => void;
  onVisibility: (value: Dashboard["visibility"]) => void;
  onTeam: (value: string) => void;
  onClose: () => void;
  onCreate: () => void;
}) {
  const { t } = useI18n();
  return (
    <Dialog open={open} onOpenChange={(value) => !value && onClose()}>
      <DialogContent className="dashboard-dialog">
        <DialogTitle>{t("New dashboard")}</DialogTitle>
        <label>
          <span>{t("Name")}</span>
          <input
            autoFocus
            placeholder={t("Dashboard name")}
            value={name}
            onChange={(event) => onName(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") onCreate();
            }}
          />
        </label>
        <label>
          <span>{t("Description")}</span>
          <input
            placeholder={t("Add a description…")}
            value={description}
            onChange={(event) => onDescription(event.target.value)}
          />
        </label>
        <label>
          <span>{t("Location")}</span>
          <DashboardSelect
            ariaLabel={t("Location")}
            options={[
              { value: "workspace", label: t("Workspace") },
              { value: "team", label: t("Team") },
              { value: "private", label: t("Personal") },
            ]}
            value={visibility}
            onChange={onVisibility}
          />
        </label>
        {visibility === "team" && (
          <label>
            <span>{t("Team")}</span>
            <DashboardSelect
              ariaLabel={t("Team")}
              options={teams.map((team) => ({
                value: team.id,
                label: team.name,
                i18nIgnore: true,
              }))}
              value={teamId}
              onChange={onTeam}
            />
          </label>
        )}
        <footer>
          <button type="button" onClick={onClose}>
            {t("Cancel")}
          </button>
          <button
            className="primary"
            disabled={
              !name.trim() || busy || (visibility === "team" && !teamId)
            }
            type="button"
            onClick={onCreate}
          >
            {t(busy ? "Creating…" : "Create dashboard")}
          </button>
        </footer>
      </DialogContent>
    </Dialog>
  );
}

function readFilters(config?: Record<string, unknown>): DashboardFilters {
  return {
    teamIds: stringArray(config?.teamIds),
    stateIds: stringArray(config?.stateIds),
    assigneeIds: stringArray(config?.assigneeIds),
    labelIds: stringArray(config?.labelIds),
  };
}
function stringArray(value: unknown) {
  return Array.isArray(value)
    ? value.filter((item): item is string => typeof item === "string")
    : [];
}
function filterChips(filters: DashboardFilters, data: BootstrapData) {
  return [
    ...(filters.teamIds ?? []).map(
      (id) => data.teams.find((item) => item.id === id)?.name,
    ),
    ...(filters.stateIds ?? []).map(
      (id) => data.states.find((item) => item.id === id)?.name,
    ),
    ...(filters.assigneeIds ?? []).map(
      (id) => data.users.find((item) => item.id === id)?.displayName,
    ),
    ...(filters.labelIds ?? []).map(
      (id) => data.labels.find((item) => item.id === id)?.name,
    ),
  ].filter((value): value is string => Boolean(value));
}
function dashboardColumns(columns: Set<DashboardColumn>) {
  return `28px minmax(0,1fr)${columns.has("createdAt") ? " 75px" : ""}${columns.has("updatedAt") ? " 75px" : ""}${columns.has("owner") ? " minmax(120px,140px)" : ""}`;
}
function compareDashboards(
  left: Dashboard,
  right: Dashboard,
  ordering: DashboardOrdering,
  direction: "asc" | "desc",
  data: BootstrapData,
) {
  const factor = direction === "asc" ? 1 : -1;
  const owner = (item: Dashboard) =>
    data.users.find((user) => user.id === item.ownerId)?.displayName ?? "";
  const a =
    ordering === "name"
      ? left.name
      : ordering === "owner"
        ? owner(left)
        : ordering === "updatedAt"
          ? left.updatedAt
          : left.createdAt;
  const b =
    ordering === "name"
      ? right.name
      : ordering === "owner"
        ? owner(right)
        : ordering === "updatedAt"
          ? right.updatedAt
          : right.createdAt;
  return a.localeCompare(b) * factor;
}
function shortDate(value: string) {
  return new Intl.DateTimeFormat(undefined, {
    month: "short",
    day: "numeric",
  }).format(new Date(value));
}
function hash(value: string) {
  return [...value].reduce((total, char) => total + char.charCodeAt(0), 0);
}
function initials(value: string) {
  return (
    value
      .split(/\s+/)
      .filter(Boolean)
      .slice(0, 2)
      .map((part) => part[0]?.toUpperCase())
      .join("") || "?"
  );
}
function relative(value: string) {
  const seconds = Math.max(
    0,
    Math.floor((Date.now() - Date.parse(value)) / 1000),
  );
  if (seconds < 60) return "now";
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
}
