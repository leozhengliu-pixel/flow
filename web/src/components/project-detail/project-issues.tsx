import { useMemo, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { Plus, Trash2, X } from "lucide-react";
import { MilestoneProgressIcon } from "@/components/issue/milestone-progress-icon";
import { isMilestoneDateOverdue, milestoneIssueProgress } from "@/components/issue/milestone-progress";
import { toast } from "sonner";
import { MyIssuesDisplayMenu } from "@/components/my-issues/my-issues-display-menu";
import { MyIssuesFilterMenu } from "@/components/my-issues/my-issues-filter-menu";
import { FilterIcon as Filter } from "@/components/ui/view-action-icons";
import { MyIssuesFilterBar } from "@/components/my-issues/my-issues-filter-bar";
import {
  filterValues,
  issueFiltersToQueryAst,
  toggleFilterOption,
  updateFilterOperator,
  updateFilterValues,
  type MyIssuesAppliedFilter,
} from "@/components/my-issues/my-issues-filter-types";
import {
  ISSUE_FILTER_LABELS,
  applyExplorerFilters,
  explorerFilterOptions,
  executeExplorerBulkAction,
  explorerBoardGroupUpdate,
  explorerBulkOptions,
  explorerPropertyOptions,
  explorerUpdateForProperty,
  issueHierarchyFields,
  issueToExplorerRow,
} from "@/components/issue-explorer/issue-explorer-model";
import { buildIssueGroups, groupMoveUpdate, pagedDisplayQuery } from "@/components/issue-explorer/issue-grouping";
import { MyIssuesBulkActionBar } from "@/components/my-issues/my-issues-bulk-action-bar";
import {
  MyIssuesList,
  type MyIssuesContextAction,
  type MyIssuesEditableProperty,
  type MyIssuesRowData,
  type MyIssuesRowPropertyOptions,
} from "@/components/my-issues/my-issues-list";
import type {
  MyIssuesDisplayOptions,
  MyIssuesFilterKey,
  MyIssuesFilterOption,
  MyIssuesProperty,
} from "@/components/my-issues/my-issues-surface";
import { IssueBoard } from "@/components/issue-explorer/issue-board";
import { PagedIssueList } from "@/components/issue-explorer/paged-issue-list";
import type { IssueQueryInput } from "@/lib/api";
import { ViewIconPicker } from "@/components/views/view-icon-picker";
import type {
  BootstrapData,
  Issue,
  ProjectMilestone,
  SavedView,
} from "@/types/flow";
import type { ProjectDetailProps } from "./project-detail-types";
import { DEFAULT_PROJECT_ISSUE_DISPLAY } from "./project-issue-display";
import { PRIORITY_LABELS } from "./project-detail-types";

export type ProjectIssueFilters = MyIssuesAppliedFilter[];
export type ProjectIssueProperty = MyIssuesProperty;

export function ProjectIssueFilterMenu({
  issueData,
  filters,
  issues,
  onChange,
}: {
  issueData?: BootstrapData;
  filters: ProjectIssueFilters;
  issues: Issue[];
  onChange: (filters: ProjectIssueFilters) => void;
}) {
  const [open, setOpen] = useState(false);
  const options = useMemo(
    () => (issueData ? explorerPropertyOptions(issueData, issues) : undefined),
    [issueData, issues],
  );
  const toggle = (field: MyIssuesFilterKey, option: MyIssuesFilterOption) => {
    const label = ISSUE_FILTER_LABELS[field];
    if (label) onChange(toggleFilterOption(filters, field, label, option));
  };
  return (
    <MyIssuesFilterMenu
      scope="project"
      filters={filters}
      onOpenChange={setOpen}
      onToggle={toggle}
      open={open}
      options={(field) => (options ? explorerFilterOptions(field, options) : undefined)}
      trigger={
        <button
          aria-label="Add filter"
          className="project-detail-page__toolbar-button ui-pill"
          data-active={filters.length > 0}
          type="button"
        >
          <Filter size={14} />
          {filters.length > 0 && <i>{filters.length}</i>}
        </button>
      }
    />
  );
}

export function ProjectIssueDisplayMenu({
  display,
  onChange,
}: {
  display: MyIssuesDisplayOptions;
  onChange: (display: MyIssuesDisplayOptions) => void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <MyIssuesDisplayMenu
      hiddenProperties={["project"]}
      toggles={["triage", "archived"]}
      onReset={() => onChange(DEFAULT_PROJECT_ISSUE_DISPLAY)}
      resetLabel="Reset to default"
      onChange={onChange}
      onOpenChange={setOpen}
      open={open}
      options={display}
    />
  );
}

export function ProjectIssueFilterBar({
  issueData,
  filters,
  issues,
  onChange,
}: {
  issueData?: BootstrapData;
  filters: ProjectIssueFilters;
  issues: Issue[];
  onChange: (filters: ProjectIssueFilters) => void;
}) {
  const options = useMemo(
    () => (issueData ? explorerPropertyOptions(issueData, issues) : undefined),
    [issueData, issues],
  );
  return (
    <MyIssuesFilterBar
      filters={filters}
      filterOptions={(filter) => (options ? explorerFilterOptions(filter.field, options) : undefined)}
      onClear={() => onChange([])}
      onOperatorChange={(id, operator) =>
        onChange(updateFilterOperator(filters, id, operator))
      }
      onRemove={(id) => onChange(filters.filter((filter) => filter.id !== id))}
      onValuesChange={(id, values) =>
        onChange(updateFilterValues(filters, id, values))
      }
    />
  );
}

export function ProjectNewView({
  display,
  filters,
  onCreateSavedView,
  onDisplayChange,
  onFiltersChange,
  onTabChange,
  onOpenSavedView,
  onUpdateSavedView,
  project,
  projectIssues,
  savedView,
  editingView,
  ...props
}: ProjectDetailProps & {
  projectIssues: Issue[];
  filters: ProjectIssueFilters;
  display: MyIssuesDisplayOptions;
  onFiltersChange: (filters: ProjectIssueFilters) => void;
  onDisplayChange: (display: MyIssuesDisplayOptions) => void;
  savedView?: SavedView;
  editingView?: boolean;
}) {
  const [name, setName] = useState(savedView?.name ?? "");
  const [saving, setSaving] = useState(false);
  const [visual, setVisual] = useState({
    icon: savedView?.icon || "CustomView",
    color: savedView?.color || "#8a8f98",
  });
  const save = async () => {
    if (saving) return;
    setSaving(true);
    try {
      const input = {
        name: name.trim() || "All issues",
        description: savedView?.description ?? "",
        resource: "issues" as const,
        projectId: project.id,
        scope: "workspace" as const,
        view: "all" as const,
        ...visual,
        filters: filters.map(filterSnapshot),
        display: displaySnapshot(display),
      };
      const created =
        savedView && onUpdateSavedView
          ? await onUpdateSavedView(savedView.id, input)
          : await onCreateSavedView(input);
      toast.success("Project view saved");
      if (onOpenSavedView) onOpenSavedView(created);
      else onTabChange("issues");
    } catch (error) {
      toast.error("Could not save project view", {
        description: error instanceof Error ? error.message : undefined,
      });
    } finally {
      setSaving(false);
    }
  };
  return (
    <div className="project-new-view" data-paged={props.issueData?.issueCollectionPaged || undefined}>
      <div className="project-new-view__name">
        <ViewIconPicker
          color={visual.color}
          icon={visual.icon}
          onChange={setVisual}
          triggerClassName="project-new-view__icon"
        />
        <input
          autoFocus
          aria-label="View name"
          onChange={(event) => setName(event.target.value)}
          onKeyDown={(event) => {
            if ((event.metaKey || event.ctrlKey) && event.key === "Enter")
              void save();
          }}
          placeholder="All issues"
          value={name}
        />
        <button
          onClick={() => {
            if (savedView && onOpenSavedView) onOpenSavedView(savedView);
            else onTabChange("issues");
          }}
          type="button"
        >
          Cancel
        </button>
        <button
          className="is-save"
          disabled={saving}
          onClick={() => void save()}
          type="button"
        >
          {saving ? "Saving…" : editingView ? "Save changes" : "Save"}
        </button>
      </div>
      <div className="project-new-view__tools">
        <ProjectIssueFilterMenu
          issueData={props.issueData}
          filters={filters}
          issues={projectIssues}
          onChange={onFiltersChange}
        />
        <ProjectIssueDisplayMenu display={display} onChange={onDisplayChange} />
      </div>
      <ProjectIssues
        {...props}
        onUpdateSavedView={onUpdateSavedView}
        onOpenSavedView={onOpenSavedView}
        project={project}
        projectIssues={projectIssues}
        filters={filters}
        display={display}
        onCreateSavedView={onCreateSavedView}
        onFiltersChange={onFiltersChange}
        onTabChange={onTabChange}
      />
    </div>
  );
}

export function ProjectIssues({
  issueData,
  workflowStates,
  cycles,
  display,
  filters,
  issues,
  labels,
  labelGroups,
  milestoneScope,
  onClearMilestoneScope,
  onCreateIssue,
  onDeleteIssues,
  onFiltersChange,
  onOpenIssue,
  onUpdateIssue,
  project,
  projects,
  projectIssues,
  users,
}: ProjectDetailProps & {
  projectIssues: Issue[];
  filters: ProjectIssueFilters;
  display: MyIssuesDisplayOptions;
  milestoneScope?: ProjectMilestone;
  onClearMilestoneScope?: () => void;
  onFiltersChange: (filters: ProjectIssueFilters) => void;
}) {
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const [deleteTarget, setDeleteTarget] = useState<Issue>();
  const [loadedIssues, setLoadedIssues] = useState<Issue[]>([]);
  const pagedQuery = useMemo<IssueQueryInput>(() => {
    const { sort, direction, groupBy, archived, conditions } = pagedDisplayQuery(display);
    if (milestoneScope) conditions.push({ field: 'projectMilestoneId', values: [milestoneScope.id] });
    return { projectId: project.id, archived, groupBy, sort, direction, filter: { and: [issueFiltersToQueryAst(filters), ...conditions] } };
  }, [project.id, milestoneScope, filters, display]);
  const visible = useMemo(
    () => applyExplorerFilters(projectIssues, filters, issueData),
    [filters, issueData, projectIssues],
  );
  const allStates = useMemo(
    () =>
      uniqueById(workflowStates ?? issues.map((issue) => issue.state)).filter(state => !state.teamId || project.teamIds.includes(state.teamId)).sort(
        (left, right) => left.position - right.position,
      ),
    [issues, workflowStates, project.teamIds],
  );
  const groups = useMemo(() => {
    const cycleNames = new Map((cycles ?? []).map(cycle => [cycle.id, cycle.name]));
    const rows = visible.map(issue => issueData ? issueToExplorerRow(issue, issueData.workspace.urlKey, issueData.issues, issueData) : { ...toRowData(issue, visible), cycleName: cycleNames.get(issue.cycleId ?? '') });
    return buildIssueGroups(rows, display, { data: issueData, states: allStates });
  }, [allStates, cycles, display, issueData, visible]);
  const rowIssues = useMemo(
    () => new Map([...projectIssues, ...loadedIssues].map((issue) => [issue.id, issue])),
    [projectIssues, loadedIssues],
  );
  const labelGroupNames = useMemo(
    () =>
      new Map(
        labelGroups
          .filter((group) => group.resourceType === "issue")
          .map((group) => [group.id, group.name]),
      ),
    [labelGroups],
  );
  const propertyOptions = useMemo<MyIssuesRowPropertyOptions>(
    () => ({
      status: allStates.map((state) => ({
        id: state.id,
        teamId: state.teamId,
        label: state.name,
        kind: "status",
        stateType: state.type,
        color: state.color,
      })),
      priority: [0, 1, 2, 3, 4].map((priority) => ({
        id: String(priority),
        label: PRIORITY_LABELS[priority],
        kind: "priority" as const,
        priority: priority as 0 | 1 | 2 | 3 | 4,
      })),
      assignee: [
        { id: "", label: "No assignee", kind: "assignee" as const },
        ...users
          .filter((user) => user.active)
          .map((user) => ({
            id: user.id,
            label: user.displayName,
            avatarUrl: user.avatarUrl,
            kind: "assignee" as const,
          })),
      ],
      dueDate: dueDateOptions(),
      cycle: [{ id: '', label: 'No cycle', kind: 'cycle' as const }, ...(cycles ?? []).map(cycle => ({ id: cycle.id, teamId: cycle.teamId, label: cycle.name, kind: 'cycle' as const }))],
      labels: labels.map((label) => ({
        id: label.id,
        label: label.name,
        kind: "labels" as const,
        color: label.color,
        description: label.description,
        issueCount: label.issueCount,
        scope: label.scope,
        resourceType: label.resourceType,
        groupId: label.groupId,
        groupLabel: label.groupId
          ? labelGroupNames.get(label.groupId)
          : undefined,
      })),
      project: [
        { id: "", label: "No project", kind: "project" as const },
        ...projects.map((item) => ({
          id: item.id,
          label: item.name,
          kind: "project" as const,
          color: item.color,
        })),
      ],
    }),
    [allStates, cycles, labelGroupNames, labels, projects, users],
  );
  const changeProperty = async (
    row: MyIssuesRowData,
    property: MyIssuesEditableProperty,
    value: string | string[],
  ) => {
    const input = explorerUpdateForProperty(property, value);
    if (input) await onUpdateIssue(row.id, input);
  };
  const contextAction = (
    row: MyIssuesRowData,
    action: MyIssuesContextAction,
  ) => {
    const issue = rowIssues.get(row.id);
    if (!issue) return;
    if (action === "delete") setDeleteTarget(issue);
    else if (action === "copy")
      void navigator.clipboard.writeText(
        `${location.origin}/${location.pathname.split("/")[1]}/issue/${issue.identifier}`,
      );
    else if (action === "openIn") onOpenIssue(issue);
  };
  const selectedRows = useMemo(() => groups.flatMap(group => group.issues).filter((row, index, all) => selected.has(row.id) && all.findIndex(item => item.id === row.id) === index), [groups, selected]);
  const select = (issueId: string, isSelected: boolean) =>
    setSelected((current) => {
      const next = new Set(current);
      if (isSelected) next.add(issueId);
      else next.delete(issueId);
      return next;
    });
  const move = (
    row: MyIssuesRowData,
    sourceGroupId: string,
    targetGroupId: string,
    targetIndex: number,
  ) => {
    const target = groups.find((group) => group.id === targetGroupId);
    const before = target?.issues[targetIndex];
    const after = target?.issues[targetIndex - 1];
    const sortOrder =
      before && after
        ? ((before.sortOrder ?? 0) + (after.sortOrder ?? 0)) / 2
        : before
          ? (before.sortOrder ?? 1) - 1
          : (after?.sortOrder ?? 0) + 1;
    const groupUpdate =
      sourceGroupId === targetGroupId
        ? {}
        : issueData
          ? explorerBoardGroupUpdate(row, display.grouping, targetGroupId, issueData)
          : groupMoveUpdate(row, display.grouping, targetGroupId, { states: allStates }) ?? {};
    void onUpdateIssue(row.id, { sortOrder, ...groupUpdate });
  };

  return (
    <div className="project-issues" data-layout={display.layout} data-paged={issueData?.issueCollectionPaged || undefined}>
      {milestoneScope && (
        <div className="project-issues__milestone-scope">
          <MilestoneProgressIcon overdue={isMilestoneDateOverdue(milestoneScope.targetDate)} progress={milestoneIssueProgress(projectIssues, project.id, milestoneScope.id)} size={13} />
          <span data-i18n-ignore>{milestoneScope.name}</span>
          <button
            aria-label="Clear milestone filter"
            onClick={onClearMilestoneScope}
            type="button"
          >
            <X size={12} />
          </button>
        </div>
      )}
      <ProjectIssueFilterBar
        issueData={issueData}
        filters={filters}
        issues={projectIssues}
        onChange={onFiltersChange}
      />
      {issueData?.issueCollectionPaged ? <PagedIssueList
        data={issueData} query={pagedQuery} layout={display.layout}
        onLoadedIssuesChange={setLoadedIssues} onOpenIssueRecord={onOpenIssue}
        onMoveIssueRecord={(issue, input) => onUpdateIssue(issue.id, input)}
        collapsedGroupIds={collapsed} displayProperties={display.properties}
        propertyOptions={propertyOptions} selectedIds={selected}
        onContextAction={contextAction} onPropertyChange={changeProperty} onSelectIssue={select}
        onCreateIssue={group => onCreateIssue(project.id, milestoneScope?.id, group.createContext)}
        onGroupCollapsedChange={(id, value) => setCollapsed(current => { const next = new Set(current); if (value) next.add(id); else next.delete(id); return next })}
      /> : groups.length > 0 &&
        (display.layout === "list" ? (
          <MyIssuesList
            collapsedGroupIds={collapsed}
            displayProperties={display.properties}
            groups={groups}
            nestedSubIssues={display.nestedSubIssues}
            propertyOptions={propertyOptions}
            selectedIds={selected}
            onContextAction={contextAction}
            onCreateIssue={(group) => onCreateIssue(project.id, milestoneScope?.id, group.createContext)}
            onGroupCollapsedChange={(groupId, isCollapsed) =>
              setCollapsed((current) => {
                const next = new Set(current);
                if (isCollapsed) next.add(groupId);
                else next.delete(groupId);
                return next;
              })
            }
            onOpenIssue={(row) => {
              const issue = rowIssues.get(row.id);
              if (issue) onOpenIssue(issue);
            }}
            onPropertyChange={changeProperty}
            onSelectIssue={select}
          />
        ) : (
          <IssueBoard
            groups={groups}
            onCreateIssue={(group) => onCreateIssue(project.id, milestoneScope?.id, group.createContext)}
            onMove={move}
            onOpenIssue={(row) => {
              const issue = rowIssues.get(row.id);
              if (issue) onOpenIssue(issue);
            }}
            onPropertyChange={changeProperty}
            onSelectIssue={select}
            properties={display.properties}
            propertyOptions={propertyOptions}
            selectedIds={selected}
          />
        ))}
      {!issueData?.issueCollectionPaged && !groups.length && (
        <div className="project-issues__empty">
          <strong>No matching issues</strong>
          <span>Change the filters or create a new issue.</span>
          <button
            onClick={() => onCreateIssue(project.id, milestoneScope?.id)}
            type="button"
          >
            <Plus size={13} />
            Create issue
          </button>
        </div>
      )}
      {issueData ? (
        <MyIssuesBulkActionBar
          selectedIssues={selectedRows}
          destructiveActions={["archive", "delete"]}
          actionOptions={(action) => explorerBulkOptions(action, explorerPropertyOptions(issueData, projectIssues))}
          onAction={(action, _issues, value) => {
            void executeExplorerBulkAction({
              action,
              ids: selectedRows.map((row) => row.id),
              value,
              data: issueData,
              issuesById: rowIssues,
              onUpdateIssue,
              onUpdateIssues: (ids, input) => Promise.all(ids.map((id) => onUpdateIssue(id, input))),
              onDeleteIssues,
            }).then(() => setSelected(new Set()));
          }}
          onClear={() => setSelected(new Set())}
        />
      ) : selected.size > 0 && (
        <div className="project-issues__bulk">
          <span>{selected.size} selected</span>
          <button onClick={() => setSelected(new Set())} type="button">
            Clear
          </button>
          <button
            className="is-danger"
            onClick={() =>
              void onDeleteIssues([...selected]).then(() =>
                setSelected(new Set()),
              )
            }
            type="button"
          >
            <Trash2 size={13} />
            Delete
          </button>
        </div>
      )}
      <Dialog.Root
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(undefined);
        }}
        open={Boolean(deleteTarget)}
      >
        <Dialog.Portal>
          <Dialog.Overlay data-flow-motion="backdrop" className="project-detail-page__dialog-overlay" />
          <Dialog.Content data-flow-motion="dialog"
            aria-describedby={undefined}
            className="project-detail-page__delete-dialog"
          >
            <Dialog.Title>Delete {deleteTarget?.identifier}?</Dialog.Title>
            <p>This issue will be permanently deleted.</p>
            <footer>
              <Dialog.Close asChild>
                <button type="button">Cancel</button>
              </Dialog.Close>
              <button
                autoFocus
                className="is-danger"
                onClick={() =>
                  deleteTarget &&
                  void onDeleteIssues([deleteTarget.id]).then(() =>
                    setDeleteTarget(undefined),
                  )
                }
                type="button"
              >
                Delete
              </button>
            </footer>
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
    </div>
  );
}

function displaySnapshot(display: MyIssuesDisplayOptions) {
  return { ...display, properties: [...display.properties] };
}
function filterSnapshot(filter: MyIssuesAppliedFilter): MyIssuesAppliedFilter {
  const values = filterValues(filter);
  return { ...filter, ...values[0], values };
}
function dueDateOptions() {
  const today = new Date();
  const iso = (date: Date) => date.toISOString().slice(0, 10);
  return [
    { id: "", label: "No due date", kind: "dueDate" as const },
    { id: iso(today), label: "Today", kind: "dueDate" as const },
    {
      id: iso(new Date(today.getTime() + 86_400_000)),
      label: "Tomorrow",
      kind: "dueDate" as const,
    },
  ];
}
function uniqueById<T extends { id: string }>(items: T[]) {
  return [...new Map(items.map((item) => [item.id, item])).values()];
}
function toRowData(issue: Issue, issues: Issue[]): MyIssuesRowData {
  return {
    id: issue.id,
    teamId: issue.team.id,
    cycleId: issue.cycleId,
    identifier: issue.identifier,
    title: issue.title,
    href: `/${location.pathname.split("/")[1]}/issue/${issue.identifier}`,
    priority: issue.priority as 0 | 1 | 2 | 3 | 4,
    state: issue.state,
    labels: issue.labels,
    project: issue.project,
    projectMilestoneId: issue.projectMilestoneId,
    assignee: issue.assignee
      ? {
          id: issue.assignee.id,
          name: issue.assignee.displayName,
          avatarUrl: issue.assignee.avatarUrl,
        }
      : undefined,
    estimate: issue.estimate,
    dueDate: issue.dueDate,
    createdAt: issue.createdAt,
    updatedAt: issue.updatedAt,
    parentId: issue.parentId,
    ...issueHierarchyFields(issue, issues),
    sortOrder: issue.sortOrder,
  };
}
