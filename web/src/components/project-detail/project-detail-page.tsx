import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import * as ContextMenu from "@radix-ui/react-context-menu";
import { Layers2, Link2, Pencil, Star, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { fetchProjectIssueSummary, type IssueRecordSummary } from '@/lib/api';
import { projectPulseSubscribed } from '@/lib/pulse-subscriptions';
import { ViewGlyph } from "@/components/views/view-icon-picker";
import { normalizeProjectIcon } from "@/components/views/project-icon";
import { ProjectOverview } from "./project-overview";
import { useLabelSelection } from "@/components/property/use-label-selection";
import { ProjectActivity } from "./project-activity";
import {
  ProjectIssueDisplayMenu,
  ProjectIssueFilterMenu,
  ProjectIssues,
  ProjectNewView,
  type ProjectIssueFilters,
} from "./project-issues";
import { DEFAULT_PROJECT_ISSUE_DISPLAY } from "./project-issue-display";
import { ProjectDetailsSidebar } from "./project-details-sidebar";
import { ProjectInsights } from "./project-insights";
import { IssueAgentTasks } from '@/components/agent/issue-agent-tasks';
import { ProjectCustomerRequestsLauncher, ProjectCustomerRequestsPage } from "@/components/customer/project-customer-requests";
import { projectCustomerRequests } from "@/components/customer/customer-request-events";
import {
  ProjectActionsMenu,
  ProjectDescriptionHistoryDialog,
  ProjectNotificationMenu,
} from "./project-header-menus";
import type {
  ProjectDetailProps,
  ProjectDetailTab,
} from "./project-detail-types";
import { labelsForProject, labelsForResource } from "@/lib/labels";
import {
  AddViewIcon,
  InsightsIcon,
  SidebarIcon,
} from "@/components/ui/view-action-icons";
import {
  confirmAction,
  promptAction,
} from "@/components/ui/action-dialog-service";
import "./project-detail-page.css";
import "@/components/editor/rich-text-body.css";
import { ProjectSlackDialog } from './project-slack-dialog';
import { FlowTooltip } from "@/components/ui/tooltip";
import { useI18n } from "@/i18n/i18n";
import { useRegisterCommandContext } from "@/components/command/command-context";
import {
  PROJECT_PICKER_SEQUENCES,
  PROJECT_SEQUENCE_TIMEOUT,
  hasOpenProjectOverlay,
  isCopyProjectUrlShortcut,
  isEditableShortcutTarget,
  projectDateShortcut,
  projectShortcutLabels,
  type ProjectPickerKind,
  type ProjectPickerRequest,
} from "./project-detail-shortcuts";

export type { ProjectDetailTab } from "./project-detail-types";

export function ProjectDetailPage(props: ProjectDetailProps) {
  const { t } = useI18n();
  const [slackOpen,setSlackOpen] = useState(false);
  const {
    project,
    projects,
    projectUpdates,
    issues,
    users,
    labels,
    labelGroups,
    viewer,
    tab,
    onTabChange,
    onUpdate,
    onDelete,
    onOpenSidebar,
    onToggleFavorite,
    savedView,
  } = props;
  useRegisterCommandContext({ kind: "project", project, onUpdate: (input) => onUpdate(project.id, input) });
  const [detailsOpen, setDetailsOpen] = useStoredBoolean(
    `flow:project:${project.id}:details`,
    true,
  );
  const [insightsOpen, setInsightsOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [notificationOpen, setNotificationOpen] = useState(false);
  const pulseSubscribed = useMemo(() => projectPulseSubscribed(project, {
    viewerId: viewer.id,
    subscriptions: props.issueData?.subscriptions ?? (props.subscription ? [props.subscription] : []),
    teamMembers: props.issueData?.teamMembers,
    initiatives: props.initiatives,
  }), [project, viewer.id, props.issueData?.subscriptions, props.issueData?.teamMembers, props.subscription, props.initiatives]);
  const labelSelection = useLabelSelection(project.labelIds ?? []);
  const [issueSummary, setIssueSummary] = useState<IssueRecordSummary>();
  useEffect(() => {
    if (!props.issueData?.issueCollectionPaged) return;
    const abort = new AbortController();
    void fetchProjectIssueSummary(project.id, abort.signal).then(setIssueSummary).catch(error => {
      if (!abort.signal.aborted) toast.error('Could not load project issue totals', { description: error.message });
    });
    return () => abort.abort();
  }, [project.id, props.issueData?.issueCollectionPaged, props.issueData?.issueCollectionRevision, issues]);
  const displayedProject = { ...project, labelIds: labelSelection.selectedIds, ...(issueSummary ? { issueCount: issueSummary.total, progress: issueSummary.total ? issueSummary.completed / issueSummary.total * 100 : 0 } : {}) };
  const issueStateKey = `flow:project:${project.id}:issues`;
  const [issueFilters, setIssueFilters] = useState<ProjectIssueFilters>(() =>
    readIssueFilters(issueStateKey),
  );
  const [issueDisplay, setIssueDisplay] = useState(() =>
    readIssueDisplay(issueStateKey),
  );
  const [activeSavedViewId, setActiveSavedViewId] = useState<
    string | undefined
  >(savedView?.id);
  const [milestoneScopeId, setMilestoneScopeId] = useState<string>(
    () => new URLSearchParams(location.search).get("projectMilestoneId") ?? "",
  );
  const projectIssues = useMemo(
    () => issues.filter((issue) => issue.project?.id === project.id),
    [issues, project.id],
  );
  // Linear shows the Customers tab once the project (or one of its issues) has a request.
  const customerRequestsEnabled = props.issueData?.workspaceSettings.featureFlags["customer-requests"] !== false && props.issueData?.viewerRole !== "guest";
  const projectIssueIdSet = useMemo(() => new Set(projectIssues.map((issue) => issue.id)), [projectIssues]);
  const customerRequestsTab = customerRequestsEnabled && (tab === "requests" || projectCustomerRequests(props.issueData?.customerRequests ?? [], project, projectIssueIdSet).length > 0);
  const openCustomerRequests = useCallback(() => onTabChange("requests"), [onTabChange]);
  const scopedProjectIssues = useMemo(
    () =>
      milestoneScopeId
        ? projectIssues.filter(
            (issue) => issue.projectMilestoneId === milestoneScopeId,
          )
        : projectIssues,
    [milestoneScopeId, projectIssues],
  );
  const milestoneScope = (project.milestones ?? []).find(
    (milestone) => milestone.id === milestoneScopeId,
  );
  const issueLabels = useMemo(
    () => labelsForResource(labels, "issue", labelGroups),
    [labelGroups, labels],
  );
  const projectLabels = useMemo(
    () => labelsForProject(labels, project.teamIds ?? [], labelGroups, labelSelection.selectedIds),
    [labelGroups, labels, project.teamIds, labelSelection.selectedIds],
  );
  const projectSavedViews = useMemo(
    () =>
      props.savedViews.filter(
        (view) => view.resource === "issues" && view.projectId === project.id,
      ),
    [project.id, props.savedViews],
  );
  const activeSavedView =
    savedView ??
    projectSavedViews.find((view) => view.id === activeSavedViewId);
  const favorited = Boolean(props.favorite);
  const changeIssueFilters = (next: ProjectIssueFilters) => {
    setIssueFilters(next);
    localStorage.setItem(`${issueStateKey}:filters`, JSON.stringify(next));
  };
  const changeIssueDisplay = (next: typeof issueDisplay) => {
    setIssueDisplay(next);
    localStorage.setItem(
      `${issueStateKey}:display`,
      JSON.stringify({ ...next, properties: [...next.properties] }),
    );
  };
  const openSavedView = (view: (typeof projectSavedViews)[number]) => {
    setActiveSavedViewId(view.id);
    changeIssueFilters(filtersFromSavedView(view.filters, issueLabels));
    changeIssueDisplay(displayFromSavedView(view.display));
    if (props.onOpenSavedView) props.onOpenSavedView(view);
    else onTabChange("issues");
  };
  const openIssueFilter = (
    field: "assignee" | "labels",
    value: string,
    valueLabel: string,
  ) => {
    setMilestoneScopeId("");
    setActiveSavedViewId(undefined);
    changeIssueFilters([
      {
        id: `progress-${field}-${value || "none"}`,
        field,
        fieldLabel: field === "assignee" ? "Assignee" : "Labels",
        operator: "is",
        value,
        valueLabel,
        values: [{ value, valueLabel }],
      },
    ]);
    onTabChange("issues");
  };
  const openMilestoneIssues = (milestoneId = "") => {
    setActiveSavedViewId(undefined);
    setMilestoneScopeId(milestoneId);
    onTabChange("issues");
    window.setTimeout(() => {
      const url = new URL(location.href);
      if (milestoneId) url.searchParams.set("projectMilestoneId", milestoneId);
      else url.searchParams.delete("projectMilestoneId");
      history.replaceState(history.state, "", url);
    }, 0);
  };

  const shortcutLabels = projectShortcutLabels();
  const [pickerRequest, setPickerRequest] = useState<
    ProjectPickerRequest & { target: "overview" | "sidebar" }
  >();
  const pickerRequestCount = useRef(0);
  const pickerSequenceAt = useRef(0);
  const clearPickerRequest = useCallback(() => setPickerRequest(undefined), []);
  const overviewMemberCount = users.filter((user) =>
    (project.memberIds ?? []).includes(user.id),
  ).length;
  const overviewLabelCount = labelSelection.selectedIds.filter((id) =>
    projectLabels.some((label) => label.id === id),
  ).length;
  // The overview Properties row only renders Members, Start date and Labels chips once they have a value.
  const overviewHasPicker = (kind: ProjectPickerKind) =>
    kind === "members"
      ? overviewMemberCount > 0
      : kind === "startDate"
        ? Boolean(project.startDate)
        : kind === "labels"
          ? overviewLabelCount > 0
          : true;
  const requestPicker = (kind: ProjectPickerKind) => {
    const target =
      !detailsOpen && tab === "overview" && overviewHasPicker(kind)
        ? "overview"
        : "sidebar";
    if (target === "sidebar" && !detailsOpen) {
      setInsightsOpen(false);
      setDetailsOpen(true);
    }
    pickerRequestCount.current += 1;
    setPickerRequest({ kind, target, id: pickerRequestCount.current });
  };
  const copyProjectUrl = () =>
    void navigator.clipboard
      .writeText(location.href)
      .then(() => toast.success("Project URL copied"));

  useEffect(() => {
    const toggle = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.isComposing) return;
      const target = event.target instanceof Element ? event.target : null;
      if (isEditableShortcutTarget(target)) return;
      const key = event.key.toLowerCase();
      if (
        (event.metaKey || event.ctrlKey) &&
        !event.shiftKey &&
        !event.altKey &&
        key === "i"
      ) {
        if (hasOpenProjectOverlay()) return;
        event.preventDefault();
        setInsightsOpen(false);
        setDetailsOpen((value) => !value);
        return;
      }
      if ((event.metaKey || event.ctrlKey) && key === "u") {
        event.preventDefault();
        onTabChange("activity");
        return;
      }
      if (isCopyProjectUrlShortcut(event)) {
        if (hasOpenProjectOverlay()) return;
        event.preventDefault();
        copyProjectUrl();
        return;
      }
      const dateKind = projectDateShortcut(event);
      if (dateKind) {
        if (hasOpenProjectOverlay()) return;
        event.preventDefault();
        requestPicker(dateKind);
        return;
      }
      if (
        !event.metaKey &&
        !event.ctrlKey &&
        !event.altKey &&
        !event.shiftKey &&
        !event.repeat &&
        !hasOpenProjectOverlay()
      ) {
        const armed =
          Date.now() - pickerSequenceAt.current < PROJECT_SEQUENCE_TIMEOUT;
        pickerSequenceAt.current = 0;
        const kind = armed ? PROJECT_PICKER_SEQUENCES[key] : undefined;
        if (kind) {
          event.preventDefault();
          requestPicker(kind);
          return;
        }
        // Don't claim "P": the app-wide "N then P" sequence still needs it.
        if (!armed && key === "p") {
          pickerSequenceAt.current = Date.now();
          return;
        }
      }
      if (
        !event.metaKey &&
        !event.ctrlKey &&
        !event.altKey &&
        !event.shiftKey &&
        !event.repeat &&
        !target?.closest(
          'select,[contenteditable]:not([contenteditable="false"]),[role=menu],[role=listbox],[role=dialog]',
        ) &&
        !document.querySelector('[role="dialog"],[role="menu"]') &&
        PROJECT_TAB_SHORTCUTS[event.key]
      ) {
        event.preventDefault();
        onTabChange(PROJECT_TAB_SHORTCUTS[event.key]);
        return;
      }
      if (event.altKey && event.key.toLowerCase() === "f") {
        event.preventDefault();
        void onToggleFavorite(project.id, !favorited).catch((error) =>
          toast.error("Could not update favorite", {
            description: error instanceof Error ? error.message : undefined,
          }),
        );
      }
    };
    window.addEventListener("keydown", toggle);
    return () => window.removeEventListener("keydown", toggle);
  });

  useEffect(() => {
    if (!savedView || savedView.projectId !== project.id) return;
    setActiveSavedViewId(savedView.id);
    setIssueFilters(filtersFromSavedView(savedView.filters, issueLabels));
    setIssueDisplay(displayFromSavedView(savedView.display));
  }, [issueLabels, project.id, savedView]);

  const save = async (input: Parameters<ProjectDetailProps["onUpdate"]>[1]) => {
    try {
      if (input.labelIds !== undefined) await labelSelection.save(input.labelIds, () => onUpdate(project.id, input));
      else await onUpdate(project.id, input);
    } catch (error) {
      toast.error("Could not update project", {
        description: error instanceof Error ? error.message : undefined,
      });
    }
  };
  const toggleFavorite = async () => {
    try {
      await props.onToggleFavorite(project.id, !favorited);
    } catch (error) {
      toast.error("Could not update favorite", {
        description: error instanceof Error ? error.message : undefined,
      });
    }
  };
  const setEvents = async (events: string[]) => {
    try {
      await props.onSetSubscriptionEvents(project.id, events);
    } catch (error) {
      toast.error("Could not update project notifications", {
        description: error instanceof Error ? error.message : undefined,
      });
      throw error;
    }
  };

  return (
    <main className="project-detail-page">
      <header className="project-detail-page__header">
        <button
          aria-label="Open workspace sidebar"
          className="project-detail-page__mobile-menu"
          data-sidebar-trigger onClick={onOpenSidebar}
          type="button"
        >
          <span />
          <span />
          <span />
        </button>
        {props.projectsOriginPath && (
          <>
            <a
              className="project-detail-page__all-projects"
              href={props.projectsOriginPath}
              onClick={(event) => {
                if (!props.onOpenProjects || event.metaKey || event.ctrlKey || event.shiftKey) return;
                event.preventDefault();
                props.onOpenProjects();
              }}
            >
              Projects
            </a>
            <span aria-hidden="true" className="project-detail-page__crumb-separator">
              ›
            </span>
          </>
        )}
        <a
          className="project-detail-page__crumb"
          href={`/${location.pathname.split("/")[1]}/project/${project.slugId}/overview`}
          onClick={(event) => {
            event.preventDefault();
            onTabChange("overview");
          }}
        >
          <ViewGlyph
            color={project.color}
            icon={normalizeProjectIcon(project.icon)}
          />
          <span data-i18n-ignore>{project.name}</span>
        </a>
        <button
          aria-checked={favorited}
          aria-label={favorited ? "Remove from favorites" : "Add to favorites"}
          className="project-detail-page__header-action"
          data-active={favorited}
          onClick={() => void toggleFavorite()}
          role="switch"
          type="button"
        >
          <Star fill={favorited ? "currentColor" : "none"} size={14} />
        </button>
        <ProjectActionsMenu
          favorited={favorited}
          onDelete={() => setDeleteOpen(true)}
          onFavorite={() => void toggleFavorite()}
          onRemind={(remindAt) =>
            props.onCreateReminder(project.id, remindAt).then(() => undefined)
          }
          onSetEvents={setEvents}
          onShowActivity={() => onTabChange("activity")}
          onShowHistory={() => setHistoryOpen(true)}
          onShowNotifications={() => setNotificationOpen(true)}
          onShowSlack={() => setSlackOpen(true)}
          onUpdateSchedule={updateSchedule => save({ updateSchedule })}
          project={project}
          subscription={props.subscription}
          pulseSubscribed={pulseSubscribed}
        />
        <div className="project-detail-page__header-spacer" />
        <FlowTooltip label={t("Copy project URL")} shortcut={shortcutLabels.copyUrl}>
          <button
            aria-keyshortcuts={shortcutLabels.copyUrl.startsWith("⌘") ? "Meta+Shift+C" : "Control+Shift+C"}
            aria-label="Copy page URL"
            className="project-detail-page__header-action"
            onClick={copyProjectUrl}
            type="button"
          >
            <Link2 size={14} />
          </button>
        </FlowTooltip>
        <ProjectNotificationMenu
          onShowSlack={() => { setNotificationOpen(false); setSlackOpen(true) }}
          onOpenChange={setNotificationOpen}
          open={notificationOpen}
          onSetEvents={setEvents}
          onUpdate={save}
          project={project}
          subscription={props.subscription}
          pulseSubscribed={pulseSubscribed}
        />
      </header>
      <ProjectSlackDialog open={slackOpen} onOpenChange={setSlackOpen} project={project} connections={props.integrationConnections} onSave={save}/>

      <div className="project-detail-page__toolbar">
        <nav aria-label="Project views" className="project-detail-page__tabs">
          <ProjectTab
            active={tab === "overview"}
            id="overview"
            onChange={onTabChange}
            shortcut="1"
            tooltip="View Overview"
          >
            Overview
          </ProjectTab>
          <ProjectTab
            active={tab === "activity"}
            id="activity"
            onChange={onTabChange}
            shortcut="2"
            tooltip="View updates and activity"
          >
            Activity
          </ProjectTab>
          {customerRequestsTab && (
            <ProjectTab
              active={tab === "requests"}
              id="requests"
              onChange={onTabChange}
            >
              Customers
            </ProjectTab>
          )}
          <ProjectTab
            active={tab === "issues"}
            id="issues"
            onChange={onTabChange}
            shortcut="3"
            tooltip="View Issues"
          >
            Issues
          </ProjectTab>
          {projectSavedViews.map((view) => (
            <ContextMenu.Root key={view.id}>
              <ContextMenu.Trigger asChild>
                <button
                  aria-current={
                    tab === "issues" && activeSavedViewId === view.id
                      ? "page"
                      : undefined
                  }
                  className="project-detail-page__saved-view-tab"
                  data-active={
                    tab === "issues" && activeSavedViewId === view.id
                  }
                  onClick={() => openSavedView(view)}
                  title={`${view.description || view.name} · Right-click for view actions`}
                  type="button"
                >
                  <ViewGlyph
                    color={view.color || "#8a8f98"}
                    icon={view.icon || "CustomView"}
                  />
                  <span>{view.name}</span>
                </button>
              </ContextMenu.Trigger>
              <ContextMenu.Portal>
                <ContextMenu.Content data-flow-motion="floating" className="project-detail-page__menu">
                  <ContextMenu.Item
                    onSelect={() => {
                      if (props.onEditSavedView) props.onEditSavedView(view);
                      else
                        void promptAction("Rename view", view.name).then(
                          (name) => {
                            if (name && name !== view.name)
                              return props.onUpdateSavedView(view.id, { name });
                          },
                        );
                    }}
                  >
                    <Pencil size={13} />
                    <span>
                      {props.onEditSavedView ? "Edit view…" : "Rename view…"}
                    </span>
                  </ContextMenu.Item>
                  <ContextMenu.Separator />
                  <ContextMenu.Item
                    className="is-danger"
                    onSelect={() => {
                      void confirmAction(`Delete view “${view.name}”?`, {
                        confirmLabel: "Delete view",
                      }).then((confirmed) => {
                        if (confirmed)
                          return props.onDeleteSavedView(view).then(() => {
                            if (activeSavedViewId === view.id)
                              setActiveSavedViewId(undefined);
                          });
                      });
                    }}
                  >
                    <Trash2 size={13} />
                    <span>Delete view</span>
                  </ContextMenu.Item>
                </ContextMenu.Content>
              </ContextMenu.Portal>
            </ContextMenu.Root>
          ))}
          {tab === "new" ? (
            <button
              aria-current="page"
              className="project-detail-page__new-view-tab"
              type="button"
            >
              <Layers2 size={13} />
              <span>New view</span>
              <Pencil size={10} />
            </button>
          ) : (
            <FlowTooltip label={t("Create new view")}>
              <button
                aria-label="Add new view"
                className="project-detail-page__add-view"
                onClick={() => onTabChange("new")}
                type="button"
              >
                <AddViewIcon />
              </button>
            </FlowTooltip>
          )}
        </nav>
        <div className="project-detail-page__toolbar-actions">
          {tab === "issues" && (
            <>
              <ProjectIssueFilterMenu
                issueData={props.issueData}
                filters={issueFilters}
                issues={projectIssues}
                onChange={changeIssueFilters}
              />
              <ProjectIssueDisplayMenu
                display={issueDisplay}
                onChange={changeIssueDisplay}
                issueData={props.issueData}
                issues={projectIssues}
                filters={issueFilters}
              />
            </>
          )}
          <FlowTooltip label={t(insightsOpen ? "Close project insights" : "Open project insights")}>
            <button
              aria-label={t(insightsOpen ? "Close project insights" : "Open project insights")}
              aria-pressed={insightsOpen}
              className="project-detail-page__toolbar-button ui-pill"
              onClick={() => {
                setInsightsOpen((value) => !value);
                setDetailsOpen(false);
              }}
              type="button"
            >
              <InsightsIcon height={14} width={14} />
            </button>
          </FlowTooltip>
          <FlowTooltip
            label={t(detailsOpen ? "Close project details" : "Open project details")}
            shortcut={shortcutLabels.toggleDetails}
          >
            <button
              aria-label={
                detailsOpen ? "Close project details" : "Open project details"
              }
              aria-pressed={detailsOpen}
              className="project-detail-page__toolbar-button ui-pill"
              onClick={() => {
                setDetailsOpen((value) => !value);
                setInsightsOpen(false);
              }}
              type="button"
            >
              <SidebarIcon />
            </button>
          </FlowTooltip>
        </div>
      </div>

      <div
        className={`project-detail-page__workspace ${detailsOpen || insightsOpen ? "has-details" : ""} ${tab === "new" ? "is-new-view" : ""}`}
      >
        <div className="project-detail-page__main">
          {customerRequestsEnabled && props.issueData && <ProjectCustomerRequestsLauncher data={props.issueData} project={displayedProject} onOpenRequests={openCustomerRequests} />}
          {tab === "overview" && (
            <ProjectOverview
              {...props}
              issueSummary={issueSummary}
              project={displayedProject}
              labels={projectLabels}
              onOpenMilestoneIssues={openMilestoneIssues}
              onPickerRequestHandled={clearPickerRequest}
              pickerRequest={
                pickerRequest?.target === "overview" ? pickerRequest : undefined
              }
              projectIssues={projectIssues}
              save={save}
            />
          )}
          {tab === "requests" && props.issueData && (
            <ProjectCustomerRequestsPage data={props.issueData} project={displayedProject} issues={projectIssues} />
          )}
          {tab === "activity" && <><ProjectActivity {...props} />{props.issueData&&<IssueAgentTasks resourceType="project" issue={{id:project.id}} data={props.issueData}/>}</>}
          {tab === "issues" &&
            (props.editingSavedView && activeSavedView ? (
              <ProjectNewView
                {...props}
                editingView
                savedView={activeSavedView}
                labels={issueLabels}
                display={issueDisplay}
                filters={issueFilters}
                onDisplayChange={changeIssueDisplay}
                onFiltersChange={changeIssueFilters}
                projectIssues={projectIssues}
              />
            ) : (
              <ProjectIssues
                {...props}
                issueSummary={issueSummary}
                labels={issueLabels}
                display={issueDisplay}
                filters={issueFilters}
                milestoneScope={milestoneScope}
                onClearMilestoneScope={() => openMilestoneIssues()}
                onFiltersChange={changeIssueFilters}
                projectIssues={scopedProjectIssues}
              />
            ))}
          {tab === "new" && (
            <ProjectNewView
              {...props}
              labels={issueLabels}
              display={issueDisplay}
              filters={issueFilters}
              onDisplayChange={changeIssueDisplay}
              onFiltersChange={changeIssueFilters}
              projectIssues={projectIssues}
            />
          )}
        </div>
        {detailsOpen && (
          <ProjectDetailsSidebar
            featureFlags={props.issueData?.workspaceSettings.featureFlags}
            issueSummary={issueSummary}
            availableIssueLabels={issueLabels}
            onCreateLabel={props.onCreateLabel}
            initiatives={props.initiatives}
            integrationConnections={props.integrationConnections}
            labelGroups={labelGroups}
            labels={projectLabels}
            onConvertMilestone={props.onConvertMilestone}
            onCreateMilestone={props.onCreateMilestone}
            onDeleteMilestone={props.onDeleteMilestone}
            onMoveMilestone={props.onMoveMilestone}
            onOpenIssueFilter={openIssueFilter}
            onOpenMilestoneIssues={openMilestoneIssues}
            onPickerRequestHandled={clearPickerRequest}
            pickerRequest={
              pickerRequest?.target === "sidebar" ? pickerRequest : undefined
            }
            onReorderMilestones={props.onReorderMilestones}
            onTabChange={onTabChange}
            onUpdate={save}
            onUpdateProject={props.onUpdate}
            onUpdateMilestone={props.onUpdateMilestone}
            project={displayedProject}
            projectRelations={props.projectRelations}
            projectIssues={projectIssues}
            projects={projects}
            projectStatuses={props.projectStatuses}
            projectUpdates={projectUpdates}
            tab={tab}
            teams={props.teams}
            users={users}
            viewer={viewer}
          />
        )}
        {insightsOpen && (
          <ProjectInsights
            issues={projectIssues}
            labels={issueLabels}
            users={users}
          />
        )}
      </div>

      <Dialog.Root onOpenChange={setDeleteOpen} open={deleteOpen}>
        <Dialog.Portal>
          <Dialog.Overlay data-flow-motion="backdrop" className="project-detail-page__dialog-overlay" />
          <Dialog.Content data-flow-motion="dialog"
            aria-describedby="project-delete-description"
            className="project-detail-page__delete-dialog"
          >
            <Dialog.Title>Delete “{project.name}”?</Dialog.Title>
            <Dialog.Description id="project-delete-description">
              All of its {projectIssues.length} issues will be archived and
              unassociated from the project.
              <br />
              <br />
              Deleted projects are available in the “Recently deleted” view for
              30 days, before they are permanently deleted.
            </Dialog.Description>
            <footer>
              <Dialog.Close asChild>
                <button type="button">Cancel</button>
              </Dialog.Close>
              <button
                autoFocus
                className="is-danger"
                onClick={() => void onDelete(project.id)}
                type="button"
              >
                Delete
              </button>
            </footer>
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
      <ProjectDescriptionHistoryDialog
        onOpenChange={setHistoryOpen}
        open={historyOpen}
        project={project}
      />
    </main>
  );
}

function ProjectTab({
  active,
  children,
  id,
  onChange,
  shortcut,
  tooltip,
}: {
  active: boolean;
  children: string;
  id: ProjectDetailTab;
  onChange: (tab: ProjectDetailTab) => void;
  shortcut?: string;
  tooltip?: string;
}) {
  const { t } = useI18n();
  const projectBase = location.pathname.replace(
    /\/(overview|activity|requests|issues|view\/new|view\/[^/]+(?:\/edit)?)$/,
    "",
  );
  return (
    <FlowTooltip label={tooltip ? t(tooltip) : undefined} shortcut={shortcut}>
    <a
      aria-current={active ? "page" : undefined}
      aria-keyshortcuts={shortcut}
      className="project-detail-page__tab"
      data-active={active}
      href={`${projectBase}/${id === "new" ? "view/new" : id}`}
      onClick={(event) => {
        if (event.metaKey || event.ctrlKey || event.shiftKey) return;
        event.preventDefault();
        onChange(id);
      }}
    >
      {children}
    </a>
    </FlowTooltip>
  );
}

const PROJECT_TAB_SHORTCUTS: Record<string, ProjectDetailTab> = {
  "1": "overview",
  "2": "activity",
  "3": "issues",
};

function useStoredBoolean(key: string, fallback: boolean) {
  const [value, setValue] = useState(() =>
    localStorage.getItem(key) === null
      ? fallback
      : localStorage.getItem(key) === "true",
  );
  const update = (next: boolean | ((current: boolean) => boolean)) =>
    setValue((current) => {
      const resolved = typeof next === "function" ? next(current) : next;
      localStorage.setItem(key, String(resolved));
      return resolved;
    });
  return [value, update] as const;
}

function readIssueFilters(key: string): ProjectIssueFilters {
  try {
    const value = JSON.parse(localStorage.getItem(`${key}:filters`) ?? "[]");
    return Array.isArray(value) ? value : [];
  } catch {
    return [];
  }
}
function readIssueDisplay(key: string) {
  try {
    const value = JSON.parse(localStorage.getItem(`${key}:display`) ?? "null");
    return value
      ? {
          ...DEFAULT_PROJECT_ISSUE_DISPLAY,
          ...value,
          properties: new Set(
            value.properties ?? [...DEFAULT_PROJECT_ISSUE_DISPLAY.properties],
          ),
        }
      : DEFAULT_PROJECT_ISSUE_DISPLAY;
  } catch {
    return DEFAULT_PROJECT_ISSUE_DISPLAY;
  }
}
function displayFromSavedView(value: Record<string, unknown>) {
  return {
    ...DEFAULT_PROJECT_ISSUE_DISPLAY,
    ...value,
    properties: new Set(
      Array.isArray(value.properties)
        ? value.properties
        : [...DEFAULT_PROJECT_ISSUE_DISPLAY.properties],
    ),
  };
}
function filtersFromSavedView(
  value: unknown[],
  labels: ProjectDetailProps["labels"],
): ProjectIssueFilters {
  const labelsById = new Map(labels.map((label) => [label.id, label]));
  return value.flatMap((item, index) => {
    if (!item || typeof item !== "object") return [];
    const raw = item as {
      field?: string;
      operator?: string;
      valueLabel?: string;
      color?: string;
      values?: unknown[];
    };
    if (raw.field === "project") return [];
    const field = raw.field === "label" ? "labels" : raw.field;
    if (!["status", "assignee", "priority", "labels"].includes(field ?? ""))
      return [];
    const values = (raw.values ?? []).flatMap((entry) => {
      const stored =
        typeof entry === "string"
          ? { value: entry }
          : entry && typeof entry === "object"
            ? (entry as {
                value?: unknown;
                id?: unknown;
                valueLabel?: unknown;
                label?: unknown;
                color?: unknown;
              })
            : undefined;
      const id = stored?.value ?? stored?.id;
      if (typeof id !== "string" || !id) return [];
      const label = field === "labels" ? labelsById.get(id) : undefined;
      return [
        {
          value: id,
          valueLabel:
            label?.name ??
            (typeof stored?.valueLabel === "string"
              ? stored.valueLabel
              : typeof stored?.label === "string"
                ? stored.label
                : (raw.valueLabel ?? id)),
          color:
            label?.color ??
            (typeof stored?.color === "string" ? stored.color : raw.color),
        },
      ];
    });
    if (!values.length) return [];
    return [
      {
        id: `saved-${index}`,
        field: field as ProjectIssueFilters[number]["field"],
        fieldLabel: (
          {
            status: "Status",
            assignee: "Assignee",
            priority: "Priority",
            labels: "Labels",
          } as Record<string, string>
        )[field!],
        operator:
          raw.operator === "isNot" ? ("isNot" as const) : ("is" as const),
        ...values[0],
        values,
      },
    ];
  });
}
