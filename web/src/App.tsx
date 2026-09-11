import {
  Suspense,
  lazy,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { Bot, History } from "lucide-react";
import { setRuntimePreferences, setWorkspaceRuntimePreferences } from '@/lib/runtime-preferences';
import { AuthenticationPolicyPage } from '@/components/auth/authentication-policy-page';
import {
  addFavorite,
  addSubscription,
  batchUpdateIssues,
  completeCycle as completeCycleRequest,
  createComment,
  createCustomer,
  createCustomerRequest,
  createDocument,
  createInitiative,
  createInitiativeComment,
  createInitiativeResource,
  createInitiativeReminder,
  createInitiativeUpdate,
  createInitiativeUpdateComment,
  createIssue,
  createIssueLink,
  createIssueLoopRun,
  createIssueReminder,
  createProject,
  createProjectReminder,
  createProjectComment,
  createProjectMilestone,
  createProjectResource,
  createProjectUpdate,
  createProjectUpdateComment,
  createRelation,
  createSavedView,
  shareSavedView,
  createTeam,
  createWorkspace,
  createWorkspaceLabel,
  createWorkspaceIssueTemplate,
  deleteAttachment,
  deleteComment,
  deleteCustomer,
  deleteInitiative,
  deleteInitiativeComment,
  deleteInitiativeResource,
  deleteInitiativeUpdate,
  deleteIssue,
  deleteProject,
  deleteProjectMilestone,
  deleteProjectResource,
  deleteProjectUpdate,
  deleteRelation,
  deleteSavedView,
  unshareSavedView,
  deleteWorkspace,
  fetchAccountBootstrap,
  fetchAuthSession,
  fetchBootstrap,
  fetchIssueRecord,
  fetchIssueHistory,
  fetchIssueRelated,
  fetchVisibleIssueIds,
  logoutAccount,
  listProjectRelations,
  recordRecentResource,
  removeFavorite,
  removeSubscription,
  reorderProjectMilestones,
  setProjectDisplayDefault,
  setLastWorkspace,
  startCycle as startCycleRequest,
  toggleCommentReaction,
  toggleInitiativeCommentReaction,
  toggleInitiativeUpdateReaction,
  toggleIssueReaction,
  toggleProjectUpdateReaction,
  updateComment,
  updateAgentSession as updateAgentSessionRequest,
  updateCustomer,
  updateCycle as updateCycleRequest,
  updateCycleSettings,
  updateInitiative,
  updateInitiativeComment,
  updateInitiativeResource,
  updateInitiativeUpdate,
  updateIssue,
  updateProject,
  updateProjectMilestone,
  updateProjectResource,
  updateProjectUpdate,
  updateReview,
  updateRelease,
  updateSavedView,
  updateWorkspace,
  updateWorkspaceSettings,
  updateUserSettings,
  uploadProjectUpdateAttachment,
  deleteProjectUpdateAttachment,
  uploadInitiativeUpdateAttachment,
  deleteInitiativeUpdateAttachment,
  uploadAttachment,
  ApiError,
} from "@/lib/api";
import type {
  AccountBootstrap,
  AuthSession,
  BootstrapData,
  Comment,
  Customer,
  CustomerMutationInput,
  Cycle,
  CycleMutationInput,
  CycleSettingsMutationInput,
  Dashboard,
  Draft,
  Initiative,
  InitiativeMutationInput,
  InitiativeUpdate,
  Issue,
  IssueLabel,
  IssueRelationType,
  IssueUpdateInput,
  Project,
  ProjectRelation,
  ProjectMilestone,
  ProjectUpdate,
  SavedView,
  SavedViewMutationInput,
  Workspace,
  WorkspaceMutationInput,
  SearchResult,
  SearchResourceType,
  UserSettings,
} from "@/types/flow";
import { deriveResourceCounts } from "@/lib/resource-counts";
import { Sidebar, type PageId } from "@/components/layout/sidebar";
import { issueReturnPath } from '@/lib/issue-navigation-context';
import { fetchWorkspacePreferences } from '@/lib/api';
import { navigationReturnPath, navigationLabel, sidebarOriginPath, reviewsOriginView, issueSequenceIDs } from '@/lib/navigation-context';
import type {
  IssueOptionsActions,
  IssueConversionKind,
  RelatedIssueCreationKind,
} from "@/components/issue/issue-options-menu";
import { ErrorState, SkeletonRows } from "@/components/state/state-view";
import { confirmAction } from "@/components/ui/action-dialog-service";
import { toast } from "sonner";
import type {
  ProjectCreateInput,
  ProjectMutationInput,
} from "@/components/projects-page/projects-page";
import type { NewProjectDraft } from "@/components/projects-page/new-project-dialog";
import { WorkspaceOnboarding, WorkspaceDirectoryPage, MemberProfilePage, TeamCreatePage, TeamOverviewPage, SettingsPage, AuthPage, OAuthAuthorizePage, WorkspaceSearchPage, WorkspaceOperationsPage, DocumentPage, DocumentsIndexPage, WorkspaceSecondaryPage, AnalyticsDashboardPage, DashboardsPage, CustomerDetailPage, InboxAppPage, ProjectsPage, ProjectDetailPage, MyIssuesPage, IssueExplorerPage, ViewsPage, InitiativesPage, InitiativeDetailPage, CyclesPage, CycleDetailPage, PulsePage, TeamArchivePage, ReviewsPage, AgentPage, AgentChatPanel, LoopsPage, DetailPane, CommandMenu, BulkActionBar, CreateIssueDialog } from "@/lib/route-pages";
import { issueToExplorerRow } from "@/components/issue-explorer/issue-explorer-model";
import type { MyIssuesCreateContext } from "@/components/my-issues/my-issues-list";
import { useLocation } from "react-router-dom";
import { useRouteNavigation } from "@/hooks/use-route-navigation";
import {
  agentPath,
  apiKeyPath,
  apiKeyEditPath,
  dashboardWidgetPath,
  dashboardsPath,
  dashboardsNewPath,
  customersPath,
  customerPath,
  cyclePath,
  documentPath,
  documentsPath,
  loopsPath,
  inboxPath,
  initiativePath,
  initiativesPath,
  teamInitiativesPath,
  issuePath,
  issueTemplateEditPath,
  integrationSettingsPath,
  membersPath,
  memberProfilePath,
  myIssuesPath,
  newReleasePipelinePath,
  newIssueTemplatePath,
  newAPIKeyPath,
  newSigningKeyPath,
  newProjectTemplatePath,
  newTeamPath,
  parseAppRoute,
  projectPath,
  projectSavedViewPath,
  projectSavedViewEditPath,
  projectTemplateEditPath,
  projectsNewViewPath,
  projectsPath,
  projectsSavedViewPath,
  projectsSavedViewEditPath,
  pulsePath,
  pulseViewPath,
  releasePipelinesPath,
  releasePipelineSettingsPath,
  reviewPath,
  reviewsPath,
  searchPath,
  routeBelongsToWorkspace,
  savedViewPathId,
  teamCyclesPath,
  teamDocumentsPath,
  teamDashboardsPath,
  teamDashboardsNewPath,
  teamHomePath,
  teamIssuesPath,
  teamProjectsNewViewPath,
  teamProjectsPath,
  teamProjectsSavedViewPath,
  teamProjectsSavedViewEditPath,
  teamSavedViewPath,
  teamSavedViewEditPath,
  teamsPath,
  teamViewsNewPath,
  teamViewsPath,
  upcomingCyclePath,
  workspaceIssuesPath,
  workspaceOnboardingPath,
  workspaceSavedViewPath,
  workspaceSavedViewEditPath,
  workspaceViewsNewPath,
  workspaceViewsPath,
  settingsPath,
  type AppRoute,
} from "@/lib/app-routes";
import type { CreateIssueInput } from "@/components/create-issue/create-issue-dialog";

function mergeProjectRelations(data: BootstrapData, projectId: string, relations: ProjectRelation[]) {
  const unrelated = (data.projectRelations ?? []).filter(relation => relation.projectId !== projectId && relation.relatedProjectId !== projectId)
  return [...unrelated, ...relations]
}

import { useWorkspaceRealtime } from "@/hooks/use-workspace-realtime";
import { fetchResourcePreferences, mergeResourcePreferences, RESOURCE_PREFERENCES_UPDATED, type ResourcePreferences } from '@/lib/resource-preferences';
import { useDesktopNotifications } from "@/hooks/use-desktop-notifications";
import { labelsForResource, setGroupedLabelSelected } from "@/lib/labels";
import { applyTheme } from "@/lib/theme";
import { useExitPresence } from '@/components/ui/motion';

import { PeopleProvider } from '@/components/property/people-provider'
import { searchResultLink } from '@/lib/search-result-link'
import { mergeIssueRecords, mergeWorkspaceDirectory, requiresIssueVisibilityCheck } from '@/lib/issue-detail-cache'

const IssueLoadingPreview = lazy(() => import('@/components/issue/issue-loading-preview').then(module => ({default:module.IssueLoadingPreview})))

function App() {
  const location = useLocation(),
    navigateTo = useRouteNavigation(),
    route = useMemo(
      () => parseAppRoute(location.pathname, location.search),
      [location.pathname, location.search],
    );
  const [account, setAccount] = useState<AccountBootstrap | null>(null),
    [data, setData] = useState<BootstrapData | null>(null),
    [error, setError] = useState(false);
  const [session, setSession] = useState<AuthSession | null>(null);
  const sessionViewerRef=useRef(session?.user.id);
  sessionViewerRef.current=session?.user.id;
  const acceptBootstrap = useCallback((next:BootstrapData)=>setData(current=>next.viewer.id===sessionViewerRef.current?mergeWorkspaceDirectory(current,next):current),[]);
  const bootstrapRequest = useRef<{ key: string; promise: Promise<BootstrapData> } | null>(null);
  const initialIssueRef = useRef<{key:string;viewerId:string;issue:Issue} | null>(null);
  const [authReady, setAuthReady] = useState(false);
  const [authenticationPolicy,setAuthenticationPolicy] = useState<string>();
  useEffect(()=>{const listener=(event:Event)=>{const detail=(event as CustomEvent<{code:string;workspaceKey:string}>).detail;if(detail.workspaceKey===decodeURIComponent(window.location.pathname.split('/').filter(Boolean)[0]??''))setAuthenticationPolicy(detail.code)};window.addEventListener('flow:authentication-policy',listener);return()=>window.removeEventListener('flow:authentication-policy',listener)},[]);
  const [mobileSidebarOpen, setMobileSidebarOpen] = useState(false);
  const [closedAgentSessionIds, setClosedAgentSessionIds] = useState<
    Set<string>
  >(new Set());
  const [selected, setSelected] = useState(new Set<string>()),
    [commandOpen, setCommandOpen] = useState(false),
    [createOpen, setCreateOpen] = useState(false),
    [createDraftId, setCreateDraftId] = useState<string>(),
    [createTeamId, setCreateTeamId] = useState<string>(),
    [createStateId, setCreateStateId] = useState<string>(),
    [createProjectId, setCreateProjectId] = useState<string>(),
    [createProjectMilestoneId, setCreateProjectMilestoneId] =
      useState<string>(),
    [createContext, setCreateContext] = useState<MyIssuesCreateContext>();
  const createStateRef = useRef<string | undefined>(undefined);
  const createPresent = useExitPresence(createOpen);
  const openCreateIssue = useCallback(
    (contextOrState?: MyIssuesCreateContext | string) => {
      const context: MyIssuesCreateContext = typeof contextOrState === "string"
        ? { stateId: contextOrState }
        : contextOrState ?? {};
      const next =
        context.stateId || data?.states.find((state) => state.type === "unstarted")?.id;
      createStateRef.current = next;
      setCreateContext({ ...context, stateId: next });
      setCreateDraftId(undefined);
      setCreateTeamId(context.teamId);
      setCreateStateId(next);
      setCreateProjectId(context.projectId);
      setCreateProjectMilestoneId(context.projectMilestoneId);
      setCreateOpen(true);
    },
    [data],
  );
  const shortcutSequence = useRef<{ key: string; at: number }>({
    key: "",
    at: 0,
  });
  const recordedResource = useRef("");
  const requestedWorkspaceKey =
    "workspaceSlug" in route ? route.workspaceSlug : "";
  const loadedWorkspaceKey = data?.workspace.urlKey;
  useEffect(() => {
    if (!loadedWorkspaceKey) return;
    const warmDetails = () => {
      void DetailPane.preload().catch(() => undefined);
      void ProjectDetailPage.preload().catch(() => undefined);
    };
    const onIntent = (event: Event) => {
      const target = event.target;
      if (!(target instanceof Element)) return;
      if (target.closest('a[href*="/issue/"], .flow-inbox-row')) void DetailPane.preload().catch(() => undefined);
      if (target.closest('a[href*="/project/"], .lp-project-row, .lp-project-card')) void ProjectDetailPage.preload().catch(() => undefined);
    };
    const idle = 'requestIdleCallback' in window;
    const timer = idle ? window.requestIdleCallback(warmDetails, { timeout: 1500 }) : window.setTimeout(warmDetails, 500);
    document.addEventListener('pointerover', onIntent, { passive: true });
    document.addEventListener('focusin', onIntent);
    return () => {
      if (idle) window.cancelIdleCallback(timer); else window.clearTimeout(timer);
      document.removeEventListener('pointerover', onIntent);
      document.removeEventListener('focusin', onIntent);
    };
  }, [loadedWorkspaceKey]);
  const loadAccount = async () => {
    setError(false);
    try {
      setAccount(await fetchAccountBootstrap());
    } catch {
      setError(true);
    }
  };
  const load = async () => {
    const key = data?.workspace.urlKey || requestedWorkspaceKey;
    if (!key) return;
    setError(false);
    try {
      acceptBootstrap(await fetchBootstrap(key));
    } catch {
      setError(true);
    }
  };
  useEffect(() => {
    fetchAuthSession()
      .then(async (authenticated) => {
        setSession(authenticated);
        setAccount(await fetchAccountBootstrap());
      })
      .catch(() => {
        setSession(null);
        setAccount(null);
      })
      .finally(() => setAuthReady(true));
  }, []);
  useEffect(() => {
    if (!data) return;
    const settings = data.userSettings[data.viewer.id] ?? {};
    setRuntimePreferences(settings);
    setWorkspaceRuntimePreferences(data.workspaceSettings);
    const root = document.documentElement;
    applyTheme(settings);
    root.style.fontSize =
      settings.fontSize === "Small"
        ? "14px"
        : settings.fontSize === "Large"
          ? "18px"
          : "";
    root.classList.toggle(
      "settings-pointer-cursor",
      Boolean(settings.pointerCursor),
    );
    root.classList.toggle(
      "settings-underline-links",
      Boolean(settings.underlineLinks),
    );
    root.classList.toggle(
      "settings-reduce-animated-media",
      Boolean(settings.disableAnimatedImages),
    );
  }, [data]);
  const oauthPath = location.pathname === "/oauth/authorize";
  const authPath =
    [
      "/login",
      "/signup",
      "/verify-email",
      "/forgot-password",
      "/reset-password",
    ].some((path) => location.pathname === path) ||
    location.pathname.startsWith("/invite/");
  useEffect(() => {
    if (authReady && !session && oauthPath) {
      const returnTo = `${location.pathname}${location.search}`;
      navigateTo(`/login?returnTo=${encodeURIComponent(returnTo)}`, {
        replace: true,
      });
    }
  }, [
    authReady,
    location.pathname,
    location.search,
    navigateTo,
    oauthPath,
    session,
  ]);
  useEffect(() => {
    if (
      authReady &&
      session &&
      authPath &&
      !location.pathname.startsWith("/invite/")
    ) {
      navigateTo("/", { replace: true });
    }
  }, [authPath, authReady, location.pathname, navigateTo, session]);
  useEffect(() => {
    if (!account) return;
    if (oauthPath) return;
    if (
      account.workspaces.length === 0 &&
      route.kind !== "workspace-onboarding"
    ) {
      setData(null);
      navigateTo(workspaceOnboardingPath(), { replace: true });
      return;
    }
    if (route.kind === "root") {
      const next =
        account.workspaces.find(
          (item) => item.workspace.urlKey === account.lastWorkspaceKey,
        )?.workspace ?? account.workspaces[0]?.workspace;
      navigateTo(
        next
          ? `/${encodeURIComponent(next.urlKey)}`
          : workspaceOnboardingPath(),
        {
          replace: true,
        },
      );
      return;
    }
    if (route.kind === "workspace-onboarding") {
      setData(null);
      return;
    }
    if (!requestedWorkspaceKey) return;
    if (
      !account.workspaces.some(
        (item) => item.workspace.urlKey === requestedWorkspaceKey,
      )
    ) {
      setData(null);
      setError(true);
      return;
    }
    if (loadedWorkspaceKey === requestedWorkspaceKey) return;
    let cancelled = false;
    setData((current) =>
      current?.workspace.urlKey === requestedWorkspaceKey ? current : null,
    );
    setError(false);
    // Clearing the previous workspace reruns this effect; share its in-flight request.
    const requestKey = `${account.viewer.id}:${requestedWorkspaceKey}`;
    if (bootstrapRequest.current?.key !== requestKey) {
      const request = { key: requestKey, promise: fetchBootstrap(requestedWorkspaceKey) };
      bootstrapRequest.current = request;
      const clear = () => { if (bootstrapRequest.current === request) bootstrapRequest.current = null; };
      void request.promise.then(clear, clear);
    }
    bootstrapRequest.current.promise
      .then(next => { if (!cancelled && next.viewer.id===sessionViewerRef.current) setData(current=>{
        const merged=mergeWorkspaceDirectory(current,next), preview=initialIssueRef.current;
        return preview?.viewerId===account.viewer.id && preview?.key.startsWith(`${requestedWorkspaceKey}:`) ? {...merged,issues:mergeIssueRecords(merged.issues,[preview.issue])}:merged;
      }); })
      .catch(() => { if (!cancelled) setError(true); });
    return () => { cancelled = true; };
  }, [account, loadedWorkspaceKey, navigateTo, oauthPath, requestedWorkspaceKey, route.kind]);
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.isComposing) return;
      const targetEditable = isEditableTarget(e.target);
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setCommandOpen((open) => !open);
        return;
      }
      if (
        e.metaKey ||
        e.ctrlKey ||
        e.altKey ||
        targetEditable ||
        hasOpenShortcutScope()
      )
        return;
      const pressed = e.key.toLowerCase();
      const now = Date.now();
      const sequence = shortcutSequence.current;
      const inSequence = now - sequence.at < 1100;
      if (inSequence && sequence.key === "n" && pressed === "p" && data) {
        e.preventDefault();
        shortcutSequence.current = { key: "", at: 0 };
        navigateTo(`${projectsPath(data.workspace.urlKey)}?create=1`);
        return;
      }
      if (inSequence && sequence.key === "n" && pressed === "i" && data) {
        e.preventDefault();
        shortcutSequence.current = { key: "", at: 0 };
        navigateTo(`${initiativesPath(data.workspace.urlKey)}?create=1`);
        return;
      }
      if (inSequence && sequence.key === "g" && pressed === "i" && data) {
        e.preventDefault();
        shortcutSequence.current = { key: "", at: 0 };
        navigateTo(inboxPath(data.workspace.urlKey));
        return;
      }
      if (inSequence && sequence.key === "g" && pressed === "m" && data) {
        e.preventDefault();
        shortcutSequence.current = { key: "", at: 0 };
        navigateTo(myIssuesPath(data.workspace.urlKey));
        return;
      }
      if (inSequence && sequence.key === "g" && pressed === "j" && data) {
        e.preventDefault();
        shortcutSequence.current = { key: "", at: 0 };
        navigateTo(agentPath(data.workspace.urlKey));
        return;
      }
      if (inSequence && sequence.key === "g" && pressed === "r" && data) {
        e.preventDefault();
        shortcutSequence.current = { key: "", at: 0 };
        navigateTo(reviewsPath(data.workspace.urlKey));
        return;
      }
      if (pressed === "n" || pressed === "g") {
        shortcutSequence.current = { key: pressed, at: now };
        return;
      }
      shortcutSequence.current = { key: "", at: 0 };
      if (pressed === "/" && data) {
        e.preventDefault();
        navigateTo(searchPath(data.workspace.urlKey));
        return;
      }
      if (pressed === "q" || pressed === "c") {
        e.preventDefault();
        openCreateIssue();
      }
    };
    addEventListener("keydown", key);
    return () => removeEventListener("keydown", key);
  }, [data, navigateTo, openCreateIssue]);
  useEffect(() => {
    const params = new URLSearchParams(location.search);
    if (
      location.pathname.includes("/issues/") &&
      params.get("create") === "1" &&
      params.get("template")
    )
      setCreateOpen(true);
  }, [location.pathname, location.search]);
  const selectedIssue =
    route.kind === "issue"
      ? data?.issues.find(
          (i) => i.identifier.toUpperCase() === route.identifier.toUpperCase(),
        ) || null
      : null;
  const [issueContextLoading, setIssueContextLoading] = useState(false);
  const [initialIssue, setInitialIssue] = useState<{key:string;viewerId:string;issue:Issue} | null>(null);
  const [detailAccessPending,setDetailAccessPending]=useState('');
  useEffect(()=>{initialIssueRef.current=null;setInitialIssue(null);setDetailAccessPending('');},[session?.user.id]);
  const [issueHistoryState, setIssueHistoryState] = useState<{key:string;loading:boolean;error:boolean}>({key:'',loading:false,error:false});
  const issueContextKey = useRef('');
  const historyRefreshSequence = useRef(0);
  const recordIdentifier = route.kind === 'issue' ? route.identifier : '';
  const detailWorkspaceKey = route.kind === 'issue' ? route.workspaceSlug : '';
  const detailKey = `${detailWorkspaceKey}:${recordIdentifier}`;
  const detailViewerId=session?.user.id;
  const detailLookupId=selectedIssue?.id ?? recordIdentifier;
  const previewIssue = initialIssue?.key === detailKey && initialIssue.viewerId===session?.user.id ? initialIssue.issue : undefined;
  const missingIssueRecord = Boolean(recordIdentifier && (!selectedIssue || selectedIssue.isSummary || selectedIssue.needsDetailRefresh));
  useEffect(() => {
    if (!authReady || !detailViewerId || !detailWorkspaceKey || !recordIdentifier || !missingIssueRecord || detailAccessPending===detailWorkspaceKey) return;
    const viewerId=detailViewerId;
    const key = `${detailWorkspaceKey}:${recordIdentifier}`;
    const controller = new AbortController();
    setIssueContextLoading(true);
    void fetchIssueRecord(detailLookupId, controller.signal, detailWorkspaceKey).then(issue => {
      if (controller.signal.aborted || sessionViewerRef.current!==viewerId) return;
      issueContextKey.current = key;
      initialIssueRef.current={key,viewerId,issue};
      setInitialIssue({key,viewerId,issue});
      setData(current => current?.workspace.urlKey === detailWorkspaceKey && current.viewer.id===viewerId ? {...current,issues:mergeIssueRecords(current.issues,[issue])}:current);
    }).catch(error => { if (!controller.signal.aborted && sessionViewerRef.current===viewerId) {
      issueContextKey.current = key;
      if(initialIssueRef.current?.key===key)initialIssueRef.current=null;
      setInitialIssue(current=>current?.key===key?null:current);
      if (error instanceof ApiError && [403,404].includes(error.status)) setData(current=>current?.workspace.urlKey===detailWorkspaceKey?{...current,issues:current.issues.filter(issue=>issue.identifier.toUpperCase()!==recordIdentifier.toUpperCase())}:current);
      toast.error('Could not load issue', { description: error.message });
    } })
      .finally(() => { if (!controller.signal.aborted) setIssueContextLoading(false); });
    return () => controller.abort();
  }, [authReady,detailViewerId,detailLookupId,detailWorkspaceKey,recordIdentifier,missingIssueRecord,detailAccessPending]);
  const historyIssueId=selectedIssue?.id;
  const historyIssueSummary=Boolean(selectedIssue?.isSummary);
  const historyLoaded=Boolean(historyIssueId && Object.hasOwn(data?.issueHistoryCursors ?? {},historyIssueId));
  const relatedIssueKey=JSON.stringify([selectedIssue?.parentId,...(selectedIssue?.subIssueIds ?? [])].filter(Boolean));
  useEffect(() => {
    if (!data?.issueCollectionPaged || !historyIssueId || historyIssueSummary || historyLoaded) return;
    const id=historyIssueId, workspace=data.workspace.urlKey, viewerId=data.viewer.id, key=`${workspace}:${id}`;
    const controller=new AbortController();
    const sequence=++historyRefreshSequence.current;
    setIssueHistoryState({key,loading:true,error:false});
    void fetchIssueHistory(id,controller.signal).then(history=>{
      if(controller.signal.aborted || sequence!==historyRefreshSequence.current)return;
      setData(current=>current?.workspace.urlKey===workspace && current.viewer.id===viewerId?{...current,comments:{...current.comments,[id]:history.comments},activities:{...current.activities,[id]:history.activities},issueHistoryCursors:{...current.issueHistoryCursors,[id]:{commentsCursor:history.commentsCursor,activitiesCursor:history.activitiesCursor}}}:current);
      setIssueHistoryState({key,loading:false,error:false});
    }).catch(()=>{if(!controller.signal.aborted && sequence===historyRefreshSequence.current)setIssueHistoryState({key,loading:false,error:true});});
    return()=>controller.abort();
  },[data?.issueCollectionPaged,data?.workspace.urlKey,data?.viewer.id,historyIssueId,historyIssueSummary,historyLoaded]);
  useEffect(()=>{
    if(!data?.issueCollectionPaged || !historyIssueId || historyIssueSummary || relatedIssueKey==='[]')return;
    const controller=new AbortController(), workspace=data.workspace.urlKey, viewerId=data.viewer.id;
    void fetchIssueRelated(historyIssueId,controller.signal).then(related=>{
      if(!controller.signal.aborted)setData(current=>current?.workspace.urlKey===workspace && current.viewer.id===viewerId?{...current,issues:mergeIssueRecords(current.issues,related)}:current);
    }).catch(()=>undefined);
    return()=>controller.abort();
  },[data?.issueCollectionPaged,data?.workspace.urlKey,data?.viewer.id,historyIssueId,historyIssueSummary,relatedIssueKey]);
  const selectedProject =
    route.kind === "project" || route.kind === "project-saved-view"
      ? data?.projects.find(
          (project) => project.slugId === route.projectSlugId,
        ) || null
      : null;
  useEffect(() => {
    let active:AbortController|undefined;
    const refresh = async (event: Event) => {
      const issueId = (event as CustomEvent<string>).detail;
      if (historyIssueId !== issueId) return;
      active?.abort();
      const controller=new AbortController();active=controller;
      const sequence = ++historyRefreshSequence.current;
      const context = await fetchIssueHistory(issueId,controller.signal);
      if (controller.signal.aborted || sequence !== historyRefreshSequence.current) return;
      setData(current => current && current.workspace.urlKey === data?.workspace.urlKey && current.viewer.id===data?.viewer.id ? { ...current,
        comments: { ...current.comments, [issueId]: context.comments }, activities: { ...current.activities, [issueId]: context.activities },
        issueHistoryCursors: { ...current.issueHistoryCursors, [issueId]: { commentsCursor: context.commentsCursor, activitiesCursor: context.activitiesCursor } },
      } : current);
      setIssueHistoryState({key:`${data?.workspace.urlKey}:${issueId}`,loading:false,error:false});
    };
    const listener = (event: Event) => { void refresh(event).catch(() => undefined) };
    window.addEventListener('flow-issue-history-changed', listener);
    return () => { active?.abort(); window.removeEventListener('flow-issue-history-changed', listener) };
  }, [historyIssueId, data?.workspace.urlKey, data?.viewer.id]);
  const selectedDocument =
    route.kind === "document"
      ? data?.documents.find(
          (document) =>
            document.slugId === route.documentSlugId ||
            document.id === route.documentSlugId,
        ) || null
      : null;
  const selectedCustomer =
    route.kind === "customer"
      ? data?.customers.find((customer) =>
          route.customerSlugId.endsWith(customer.id.slice(-12)),
        ) || null
      : null;
  const selectedInitiative =
    route.kind === "initiative"
      ? data?.initiatives.find(
          (initiative) => initiative.slugId === route.initiativeSlugId,
        ) || null
      : null;
  const selectedCycle =
    route.kind === "cycle"
      ? route.cycleId === "active"
        ? ([...(data?.cycles ?? [])]
            .filter(
              (cycle) =>
                cycle.status === "current" &&
                data?.teams.find(
                  (team) =>
                    team.key.toLowerCase() === route.teamKey.toLowerCase(),
                )?.id === cycle.teamId,
            )
            .sort((a, b) => a.startsAt.localeCompare(b.startsAt))[0] ?? null)
        : data?.cycles.find(
            (cycle) => String(cycle.number) === route.cycleId,
          ) || null
      : route.kind === "cycle-upcoming"
        ? ([...(data?.cycles ?? [])]
            .filter(
              (cycle) =>
                cycle.status === "upcoming" &&
                data?.teams.find(
                  (team) =>
                    team.key.toLowerCase() === route.teamKey.toLowerCase(),
                )?.id === cycle.teamId,
            )
            .sort((a, b) => a.startsAt.localeCompare(b.startsAt))[0] ?? null)
        : null;
  const selectedReview =
    route.kind === "review"
      ? data?.reviews.find((item) => item.slugId === route.reviewSlug) || null
      : null;
  const availableSavedViews = data?.savedViews ?? [];
  const issueSavedViews = availableSavedViews.filter(
    (view) =>
      view.resource !== "projects" &&
      view.resource !== "initiativeProjects" &&
      !view.projectId,
  );
  const projectSavedViews = availableSavedViews.filter(
    (view) => view.resource === "projects",
  );
  const selectedSavedView =
    route.kind === "workspace-saved-view" || route.kind === "team-saved-view"
      ? issueSavedViews.find(
          (view) => view.id === route.viewId || view.slugId === route.viewId,
        ) || null
      : null;
  const selectedProjectSavedView =
    route.kind === "projects-saved-view" ||
    route.kind === "team-projects-saved-view"
      ? projectSavedViews.find(
          (view) => view.id === route.viewId || view.slugId === route.viewId,
        ) || null
      : null;
  const projectFacetViews = availableSavedViews.filter(
    (view) => view.resource === "issues" && view.projectId,
  );
  const selectedProjectFacetView =
    route.kind === "project-saved-view"
      ? projectFacetViews.find(
          (view) => view.id === route.viewId || view.slugId === route.viewId,
        ) || null
      : null;
  const viewedResourceType: SearchResourceType | undefined = selectedIssue
    ? "issue"
    : selectedProjectFacetView
      ? "view"
      : selectedProject
        ? "project"
        : selectedInitiative
          ? "initiative"
          : selectedDocument
            ? "document"
            : selectedCustomer
              ? "customer"
              : selectedSavedView || selectedProjectSavedView
                ? "view"
                : undefined;
  const viewedResourceId =
    selectedIssue?.id ??
    selectedProjectFacetView?.id ??
    selectedProject?.id ??
    selectedInitiative?.id ??
    selectedDocument?.id ??
    selectedCustomer?.id ??
    selectedSavedView?.id ??
    selectedProjectSavedView?.id;
  useEffect(() => {
    if (!data || !viewedResourceType || !viewedResourceId) return;
    const key = `${data.workspace.id}:${viewedResourceType}:${viewedResourceId}`;
    if (recordedResource.current === key) return;
    recordedResource.current = key;
    void recordRecentResource(viewedResourceType, viewedResourceId).catch(
      () => {
        if (recordedResource.current === key) recordedResource.current = "";
      },
    );
  }, [data, viewedResourceId, viewedResourceType]);
  useEffect(() => {
    const update = (event: Event) => {
      const { workspaceKey, preferences } = (event as CustomEvent<{ workspaceKey: string; preferences: ResourcePreferences }>).detail;
      setData(current => current?.workspace.urlKey === workspaceKey ? mergeResourcePreferences(current, preferences) : current);
    };
    window.addEventListener(RESOURCE_PREFERENCES_UPDATED, update);
    return () => window.removeEventListener(RESOURCE_PREFERENCES_UPDATED, update);
  }, []);
  const realtime = useWorkspaceRealtime({
    workspaceKey: data?.workspace.urlKey,
    snapshot: data,
    issueId: selectedIssue?.id,
    route: location.pathname,
    onRemoteSync: async (event) => {
      const workspace = data?.workspace.urlKey;
      if (!workspace) return;
      const viewerId=data.viewer.id;
      if (event.type === 'workspace_preferences.updated') {
        const settings = await fetchWorkspacePreferences(workspace);
        setData(current => current?.workspace.urlKey === workspace && current.viewer.id === viewerId ? { ...current, workspaceSettings: settings } : current);
        return;
      }
      const entity = event.payload?.entity;
      if (/^(favorite\.|favorite_folder\.|subscription\.)/.test(event.type)) {
        const preferences = await fetchResourcePreferences(workspace);
        setData(current => current?.workspace.urlKey === workspace && current.viewer.id === viewerId ? mergeResourcePreferences(current, preferences) : current);
        return;
      }
      if (event.type.startsWith('notification.') && entity && typeof entity === 'object' && 'recipientId' in entity) {
        const notification = entity as BootstrapData['notifications'][number];
        if (notification.recipientId !== data.viewer.id) return;
        setData(current => current?.workspace.urlKey === workspace && current.viewer.id === viewerId ? {
          ...current, notifications: current.notifications.some(item => item.id === notification.id)
            ? current.notifications.map(item => item.id === notification.id ? notification : item)
            : [...current.notifications, notification],
        } : current);
        return;
      }
      const issue = event.payload?.issue ?? (
        entity && typeof entity === "object" && "identifier" in entity
          ? (entity as Issue)
          : undefined
      );
      if (event.type.startsWith('comment.') && issue) {
        issueContextKey.current = '';
        window.dispatchEvent(new CustomEvent('flow-issue-history-changed', { detail: issue.id }));
        return;
      }
      if ((event.type === "issue.updated" || event.type === "issue.created") && issue) {
        setData((current) =>
          current?.workspace.urlKey === workspace && current.viewer.id === viewerId
            ? deriveResourceCounts({
                ...current,
                issues: current.issueCollectionPaged
                  ? mergeIssueRecords(current.issues,[issue])
                  : current.issues.some((item) => item.id === issue.id)
                  ? current.issues.map((item) => item.id === issue.id ? issue : item)
                  : [issue, ...current.issues],
              })
            : current,
        );
        return;
      }
      if (event.type === "issue.deleted" && event.aggregateId) {
        setData((current) =>
          current?.workspace.urlKey === workspace && current.viewer.id === viewerId
            ? deriveResourceCounts({
                ...current,
                issues: current.issues.filter((item) => item.id !== event.aggregateId),
              })
            : current,
        );
        return;
      }
      const checkedIds=data.issues.map(issue=>issue.id);
      const eventFields=event.payload as Record<string,unknown>|undefined;
      const cosmeticTeamChange=(event.type==='team.updated'||event.type==='team.settings_updated') && eventFields && ['name','icon','color','description','timezone','showInitiatives'].some(key=>Object.hasOwn(eventFields,key)) && !['access','private','retired','parentTeamId','membershipRestriction'].some(key=>Object.hasOwn(eventFields,key));
      const checkVisibility=data.issueCollectionPaged && requiresIssueVisibilityCheck(event.type) && !cosmeticTeamChange;
      if(checkVisibility && /resync|member|permission|shared|unshared/.test(event.type))setDetailAccessPending(workspace);
      let next:BootstrapData, visibility:{ids:string[]}|undefined;
      try {
        [next,visibility]=await Promise.all([fetchBootstrap(workspace),checkVisibility ? fetchVisibleIssueIds(workspace,checkedIds) : Promise.resolve(undefined)]);
      } catch(error) {
        if(checkVisibility && sessionViewerRef.current===viewerId)setDetailAccessPending(workspace);
        throw error;
      }
      if(sessionViewerRef.current!==viewerId || next.viewer.id!==viewerId)return;
      const checked=new Set(checkedIds), visible=visibility ? new Set(visibility.ids):undefined;
      if(checkVisibility)setDetailAccessPending(workspace);
      if (visible && initialIssueRef.current?.key.startsWith(`${workspace}:`) && !visible.has(initialIssueRef.current.issue.id)) {
        initialIssueRef.current=null;
        setInitialIssue(null);
      }
      const payload=event.payload as Record<string,unknown> | undefined;
      const deletedLabelIds = Array.isArray(payload?.labelIds) ? new Set(payload.labelIds as string[]) : undefined;
      const deletedWorkflowState=event.type==='workflow_state.deleted' ? payload : undefined;
      setData(current=>{
        if(current?.workspace.urlKey!==workspace || current.viewer.id!==viewerId)return current;
        const authorized=visible?new Set([...visible,...current.issues.filter(issue=>!checked.has(issue.id)).map(issue=>issue.id)]):undefined;
        const merged=mergeWorkspaceDirectory(current,next,authorized);
        if(deletedLabelIds)merged.issues=merged.issues.map(issue=>issue.labels.some(label=>deletedLabelIds.has(label.id))?{...issue,labels:issue.labels.filter(label=>!deletedLabelIds.has(label.id))}:issue);
        if(deletedWorkflowState){
          const replacement=next.states.find(state=>state.id===deletedWorkflowState.replacementStateId);
          if(replacement)merged.issues=merged.issues.map(issue=>issue.team.id===deletedWorkflowState.teamId&&issue.state.id===deletedWorkflowState.stateId?{...issue,state:replacement,needsDetailRefresh:!issue.isSummary}:issue);
        }
        return merged;
      });
      if(deletedWorkflowState)window.dispatchEvent(new CustomEvent('flow-issue-query-invalidated',{detail:{workspaceKey:workspace}}));
      if(visible && checkedIds.some(id=>!visible.has(id))) window.dispatchEvent(new CustomEvent('flow-issue-query-invalidated',{detail:{workspaceKey:workspace,force:true}}));
      if(checkVisibility && selectedIssue && visible?.has(selectedIssue.id)) {
        const refreshed=await fetchIssueRecord(selectedIssue.id,undefined,workspace);
        setData(current=>current?.workspace.urlKey===workspace && current.viewer.id===viewerId?{...current,issues:mergeIssueRecords(current.issues,[refreshed])}:current);
      }
      if(checkVisibility && sessionViewerRef.current===viewerId)setDetailAccessPending(current=>current===workspace?'':current);
    },
  });
  useDesktopNotifications(data);
  const replaceIssue = (issue: Issue) =>
    setData((current) =>
      current && current.workspace.id===data?.workspace.id && current.viewer.id===data?.viewer.id
        ? deriveResourceCounts({
            ...current,
            issues: current.issueCollectionPaged
              ? mergeIssueRecords(current.issues,[issue])
              : current.issues.map((i) => (i.id === issue.id ? issue : i)),
          })
        : current,
    );
  const updateIssueComments = (
    issueId: string,
    updater: (comments: Comment[]) => Comment[],
  ) =>
    setData((current) =>
      current
        ? {
            ...current,
            comments: {
              ...current.comments,
              [issueId]: updater(current.comments[issueId] ?? []),
            },
          }
        : current,
    );
  const createCommentOptimistically = async (
    issue: Issue,
    body: string,
    bodyData?: Record<string, unknown>,
    parentId?: string,
  ) => {
    if (!data) throw new Error("Workspace is not loaded");
    const temporary: Comment = {
      id: `optimistic-${crypto.randomUUID()}`,
      version: 0,
      body,
      bodyData,
      parentId,
      reactions: {},
      createdAt: new Date().toISOString(),
      user: data.viewer,
    };
    updateIssueComments(issue.id, (comments) => [...comments, temporary]);
    try {
      const saved = await createComment(issue.id, body, bodyData, parentId);
      updateIssueComments(issue.id, (comments) =>
        comments.map((comment) =>
          comment.id === temporary.id ? saved : comment,
        ),
      );
      return saved;
    } catch (error) {
      updateIssueComments(issue.id, (comments) =>
        comments.filter((comment) => comment.id !== temporary.id),
      );
      toast.error("Could not post comment", {
        description: error instanceof Error ? error.message : undefined,
      });
      throw error;
    }
  };
  const editCommentOptimistically = async (
    issue: Issue,
    id: string,
    body: string,
    bodyData?: Record<string, unknown>,
  ) => {
    const current = data?.comments[issue.id]?.find(
      (comment) => comment.id === id,
    );
    if (!current) throw new Error("Comment not found");
    updateIssueComments(issue.id, (comments) =>
      comments.map((comment) =>
        comment.id === id
          ? { ...comment, body, bodyData, editedAt: new Date().toISOString() }
          : comment,
      ),
    );
    try {
      const saved = await updateComment(
        issue.id,
        id,
        body,
        bodyData,
        current.version,
      );
      updateIssueComments(issue.id, (comments) =>
        comments.map((comment) => (comment.id === id ? saved : comment)),
      );
      return saved;
    } catch (error) {
      const conflict =
        error instanceof ApiError && error.code === "VERSION_CONFLICT"
          ? (error.current as Comment | undefined)
          : undefined;
      updateIssueComments(issue.id, (comments) =>
        comments.map((comment) =>
          comment.id === id ? (conflict ?? current) : comment,
        ),
      );
      toast.error(
        conflict
          ? "Comment changed in another session"
          : "Could not edit comment",
        { description: error instanceof Error ? error.message : undefined },
      );
      throw error;
    }
  };
  const deleteCommentOptimistically = async (issue: Issue, id: string) => {
    const comments = data?.comments[issue.id] ?? [];
    const index = comments.findIndex((comment) => comment.id === id);
    const removed = comments[index];
    if (!removed) return;
    updateIssueComments(issue.id, (current) =>
      current.filter((comment) => comment.id !== id && comment.parentId !== id),
    );
    try {
      await deleteComment(issue.id, id);
    } catch (error) {
      updateIssueComments(issue.id, (current) => {
        const next = [...current];
        next.splice(Math.min(index, next.length), 0, removed);
        return next;
      });
      toast.error("Could not delete comment", {
        description: error instanceof Error ? error.message : undefined,
      });
      throw error;
    }
  };
  const reactToCommentOptimistically = async (
    issue: Issue,
    id: string,
    emoji: string,
  ) => {
    const current = data?.comments[issue.id]?.find(
      (comment) => comment.id === id,
    );
    if (!current || !data) return;
    const users = current.reactions[emoji] ?? [];
    const reactions = {
      ...current.reactions,
      [emoji]: users.includes(data.viewer.id)
        ? users.filter((userId) => userId !== data.viewer.id)
        : [...users, data.viewer.id],
    };
    updateIssueComments(issue.id, (comments) =>
      comments.map((comment) =>
        comment.id === id ? { ...comment, reactions } : comment,
      ),
    );
    try {
      const saved = await toggleCommentReaction(issue.id, id, emoji);
      updateIssueComments(issue.id, (comments) =>
        comments.map((comment) => (comment.id === id ? saved : comment)),
      );
    } catch (error) {
      updateIssueComments(issue.id, (comments) =>
        comments.map((comment) => (comment.id === id ? current : comment)),
      );
      toast.error("Could not update reaction", {
        description: error instanceof Error ? error.message : undefined,
      });
      throw error;
    }
  };
  const run = async <T,>(work: () => Promise<T>, message: string) => {
    try {
      return await work();
    } catch (error) {
      toast.error(message, {
        description: error instanceof Error ? error.message : undefined,
      });
      throw error;
    }
  };
  const refreshActivity = async (includeIssue = true) => {
    if (!data) return;
    if (data.issueCollectionPaged && selectedIssue) {
      const sequence=++historyRefreshSequence.current;
      const [issue,context] = await Promise.all([includeIssue ? fetchIssueRecord(selectedIssue.id) : Promise.resolve(undefined), fetchIssueHistory(selectedIssue.id)]);
      if(sequence!==historyRefreshSequence.current)return;
      setData(current => current?.workspace.id === data.workspace.id && current.viewer.id===data.viewer.id ? {
        ...current,
        issues: issue ? mergeIssueRecords(current.issues,[issue]) : current.issues,
        comments: { ...current.comments, [selectedIssue.id]: context.comments ?? [] },
        issueHistoryCursors: { ...current.issueHistoryCursors, [selectedIssue.id]: { commentsCursor: context.commentsCursor, activitiesCursor: context.activitiesCursor } },
        activities: { ...current.activities, [selectedIssue.id]: context.activities ?? [] },
      } : current);
      setIssueHistoryState({key:`${data.workspace.urlKey}:${selectedIssue.id}`,loading:false,error:false});
      return;
    }
    const next = await fetchBootstrap(data.workspace.urlKey);
    setData(current=>mergeWorkspaceDirectory(current,next));
  };
  const updateSelected = async (input: IssueUpdateInput) => {
    if (!selectedIssue) return;
    await updateIssueById(selectedIssue, input);
    if (input.description !== undefined) void refreshActivity(false).catch(()=>setIssueHistoryState({key:`${data?.workspace.urlKey}:${selectedIssue.id}`,loading:false,error:true}));
  };
  const removeSelected = async () => {
    if (!data || !selectedIssue) return;
    await run(() => deleteIssue(selectedIssue.id), "Could not delete issue");
    setData((current) =>
      current
        ? deriveResourceCounts({
            ...current,
            issues: current.issues.filter((i) => i.id !== selectedIssue.id),
          })
        : current,
    );
    navigateTo(issueReturnPath(location.state, data.workspace.urlKey, selectedIssue), { replace: true });
  };
  const addComment = async (
    body: string,
    bodyData?: Record<string, unknown>,
    parentId?: string,
  ) => {
    if (!selectedIssue) return;
    await createCommentOptimistically(selectedIssue, body, bodyData, parentId);
  };
  const editComment = async (
    id: string,
    body: string,
    bodyData?: Record<string, unknown>,
  ) => {
    if (!selectedIssue) return;
    await editCommentOptimistically(selectedIssue, id, body, bodyData);
  };
  const removeComment = async (id: string) => {
    if (!selectedIssue) return;
    await deleteCommentOptimistically(selectedIssue, id);
  };
  const reactComment = async (id: string, emoji: string) => {
    if (!selectedIssue) return;
    await reactToCommentOptimistically(selectedIssue, id, emoji);
  };
  const reactIssue = async (emoji: string) => {
    if (!selectedIssue) return;
    const issue = await run(
      () => toggleIssueReaction(selectedIssue.id, emoji),
      "Could not update reaction",
    );
    replaceIssue(issue);
  };
  const addIssue = async (input: CreateIssueInput): Promise<Issue> => {
    if (!data) throw new Error("Workspace is not loaded");
    const issue = await run(
      () =>
        createIssue({
          ...input,
          teamId: input.teamId || data.teams[0].id,
          ...(input.recurrence
            ? {
                nextOccurrenceAt: nextOccurrence(
                  input.recurrence,
                ).toISOString(),
              }
            : {}),
        }),
      "Could not create issue",
    );
    setData((current) =>
      current
        ? deriveResourceCounts({
            ...current,
            issues: [issue, ...current.issues],
            activities: { ...current.activities, [issue.id]: [] },
          })
        : current,
    );
    if (!input.createMore) navigateTo(issuePath(data.workspace.urlKey, issue));
    return issue;
  };
  const addSubIssue = async (input: {
    title: string;
    description: string;
    stateId: string;
    priority: number;
    assigneeId?: string;
    projectId?: string;
    cycleId?: string;
    dueDate?: string;
    labelIds: string[];
    attachments: File[];
  }) => {
    if (!data || !selectedIssue) return;
    const { attachments, ...fields } = input;
    const child = await createIssue({
      ...fields,
      teamId: selectedIssue.team.id,
      parentId: selectedIssue.id,
    });
    const uploaded = await Promise.all(
      attachments.map((file) => uploadAttachment(child.id, file)),
    );
    const complete = { ...child, attachments: uploaded };
    setData((current) =>
      current
        ? deriveResourceCounts({
            ...current,
            issues: [
              complete,
              ...current.issues.map((i) =>
                i.id === selectedIssue.id
                  ? { ...i, subIssueIds: [...i.subIssueIds, child.id] }
                  : i,
              ),
            ],
            activities: { ...current.activities, [child.id]: [] },
          })
        : current,
    );
    await refreshActivity();
  };
  const addRelation = async (
    type: IssueRelationType,
    relatedIssueId: string,
  ) => {
    if (!selectedIssue) return;
    await run(
      () => createRelation(selectedIssue.id, type, relatedIssueId),
      "Could not add relation",
    );
    await refreshActivity();
  };
  const removeRelation = async (relationId: string) => {
    if (!selectedIssue) return;
    await run(
      () => deleteRelation(selectedIssue.id, relationId),
      "Could not remove relation",
    );
    await refreshActivity();
  };
  const addAttachment = async (file: File) => {
    if (!selectedIssue) return;
    await run(
      () => uploadAttachment(selectedIssue.id, file),
      "Could not upload attachment",
    );
    await refreshActivity();
  };
  const removeAttachment = async (id: string) => {
    if (!selectedIssue) return;
    await run(
      () => deleteAttachment(selectedIssue.id, id),
      "Could not remove attachment",
    );
    await refreshActivity();
  };
  const updateIssueById = async (issue: Issue, input: IssueUpdateInput) => {
    const current = data?.issues.find((item) => item.id === issue.id) ?? issue;
    const optimistic = applyOptimisticIssue(current, input, data);
    replaceIssue(optimistic);
    try {
      const updated = await updateIssue(issue.id, {
        ...input,
        expectedVersion: current.version,
      });
      replaceIssue(updated);
      return updated;
    } catch (error) {
      const conflict =
        error instanceof ApiError && error.code === "VERSION_CONFLICT"
          ? (error.current as Issue | undefined)
          : undefined;
      replaceIssue(conflict ?? current);
      if (!(conflict && input.expectedDocumentVersion !== undefined)) {
        toast.error(
          conflict
            ? "Issue changed in another session"
            : "Could not update issue",
          { description: error instanceof Error ? error.message : undefined },
        );
      }
      throw error;
    }
  };
  const addSelectedIssueLink = async (input: {
    url: string;
    title?: string;
  }) => {
    if (!selectedIssue) return;
    const attachment = await run(
      () => createIssueLink(selectedIssue.id, input),
      "Could not add link",
    );
    replaceIssue({
      ...selectedIssue,
      attachments: [...selectedIssue.attachments, attachment],
    });
  };
  const addSelectedCustomerRequest = async (input: {
    customerId?: string;
    customerName?: string;
    body: string;
  }) => {
    if (!selectedIssue) return;
    let customer = data?.customers.find((item) => item.id === input.customerId);
    if (!customer && input.customerName) {
      customer = await run(
        () => createCustomer({ name: input.customerName! }),
        "Could not create customer",
      );
    }
    if (!customer) throw new Error("Select or create a customer");
    const request = await run(
      () =>
        createCustomerRequest({
          customerId: customer.id,
          body: input.body,
          source: "manual",
          issueId: selectedIssue.id,
        }),
      "Could not add customer request",
    );
    setData((current) =>
      current
        ? {
            ...current,
            customers: current.customers.some(
              (item) => item.id === customer!.id,
            )
              ? current.customers
              : [customer!, ...current.customers],
            customerRequests: [request, ...current.customerRequests],
          }
        : current,
    );
  };
  const addSelectedDocument = async () => {
    if (!data || !selectedIssue) return;
    const document = await run(
      () =>
        createDocument({
          title: "New document",
          teamIds: [selectedIssue.team.id],
          issueId: selectedIssue.id,
        }),
      "Could not create document",
    );
    setData((current) =>
      current
        ? { ...current, documents: [document, ...current.documents] }
        : current,
    );
    navigateTo(documentPath(data.workspace.urlKey, document));
  };
  const toggleSelectedRelease = async (releaseId: string) => {
    if (!selectedIssue) return;
    const release = data?.releases.find((item) => item.id === releaseId);
    if (!release) throw new Error("Release not found");
    const issueIds = release.issueIds.includes(selectedIssue.id)
      ? release.issueIds.filter((id) => id !== selectedIssue.id)
      : [...release.issueIds, selectedIssue.id];
    const updated = await run(
      () => updateRelease(release.id, { issueIds }),
      "Could not update release",
    );
    setData((current) =>
      current
        ? {
            ...current,
            releases: current.releases.map((item) =>
              item.id === updated.id ? updated : item,
            ),
          }
        : current,
    );
  };
  const createSelectedRelated = async (
    kind: RelatedIssueCreationKind,
    title: string,
  ) => {
    if (!selectedIssue) return;
    const related = await run(
      () =>
        createIssue({
          title,
          description: kind === "copy" ? selectedIssue.description : "",
          descriptionState: kind === "copy" ? selectedIssue.descriptionState : undefined,
          descriptionData: kind === "copy" ? selectedIssue.documentContent?.contentData : undefined,
          estimate: kind === "copy" ? selectedIssue.estimate : undefined,
          cycleId: kind === "copy" ? selectedIssue.cycleId : undefined,
          dueDate: kind === "copy" ? selectedIssue.dueDate : undefined,
          projectMilestoneId: kind === "copy" ? selectedIssue.projectMilestoneId : undefined,
          teamId: selectedIssue.team.id,
          parentId: kind === "sub-issue" ? selectedIssue.id : undefined,
          stateId: selectedIssue.state.id,
          priority: selectedIssue.priority,
          assigneeId: selectedIssue.assignee?.id,
          projectId: selectedIssue.project?.id,
          labelIds: selectedIssue.labels.map((label) => label.id),
        }),
      "Could not create related issue",
    );
    if (kind === "parent") {
      await updateIssueById(selectedIssue, { parentId: related.id });
    } else if (kind !== "sub-issue" && kind !== "copy") {
      const relationType: IssueRelationType =
        kind === "blocked"
          ? "blocks"
          : kind === "blocking"
            ? "blocked_by"
            : "related";
      await run(
        () => createRelation(selectedIssue.id, relationType, related.id),
        "Could not relate issue",
      );
    }
    await refreshActivity();
  };
  const convertSelectedIssue = async (kind: IssueConversionKind) => {
    if (!data || !selectedIssue) return;
    if (kind === "template") {
      const template = await run(
        () =>
          createWorkspaceIssueTemplate({
            name: selectedIssue.title,
            description: `Created from ${selectedIssue.identifier}`,
            body: selectedIssue.description,
            teamId: selectedIssue.team.id,
            stateId: selectedIssue.state.id,
            priority: selectedIssue.priority,
            assigneeId: selectedIssue.assignee?.id,
            projectId: selectedIssue.project?.id,
            labelIds: selectedIssue.labels.map((label) => label.id),
          }),
        "Could not create issue template",
      );
      setData((current) =>
        current
          ? {
              ...current,
              issueTemplates: [template, ...current.issueTemplates],
            }
          : current,
      );
      return;
    }
    const project = await run(
      () =>
        createProject({
          name: selectedIssue.title,
          summary: selectedIssue.description,
          description: selectedIssue.description,
          priority: selectedIssue.priority,
          leadId: selectedIssue.assignee?.id,
          teamIds: [selectedIssue.team.id],
          labelIds: selectedIssue.labels.map((label) => label.id),
        }),
      "Could not convert issue to project",
    );
    await run(
      () => deleteIssue(selectedIssue.id),
      "Project was created, but the original issue could not be removed",
    );
    setData((current) =>
      current
        ? {
            ...current,
            projects: [project, ...current.projects],
            issues: current.issues.filter(
              (item) => item.id !== selectedIssue.id,
            ),
          }
        : current,
    );
    navigateTo(projectPath(data.workspace.urlKey, project), { replace: true });
  };
  const toggleSelectedFavorite = async () => {
    if (!data || !selectedIssue) return;
    const favorite = data.favorites.find(
      (item) =>
        item.resourceType === "issue" && item.resourceId === selectedIssue.id,
    );
    if (favorite) {
      await run(
        () => removeFavorite("issue", selectedIssue.id),
        "Could not remove favorite",
      );
      setData((current) =>
        current
          ? {
              ...current,
              favorites: current.favorites.filter(
                (item) => item.id !== favorite.id,
              ),
            }
          : current,
      );
    } else {
      const created = await run(
        () => addFavorite("issue", selectedIssue.id),
        "Could not add favorite",
      );
      setData((current) =>
        current
          ? { ...current, favorites: [created, ...current.favorites] }
          : current,
      );
    }
  };
  const remindSelectedIssue = async (remindAt: string) => {
    if (!selectedIssue) return;
    await run(
      () => createIssueReminder(selectedIssue.id, remindAt),
      "Could not set reminder",
    );
    await refreshActivity();
  };
  const runSelectedIssueLoop = async (prompt: string) => {
    if (!selectedIssue) return;
    await run(
      () => createIssueLoopRun(selectedIssue.id, prompt),
      "Could not run loop",
    );
    await refreshActivity();
  };
  const restoreSelectedDescription = async (
    description: string,
    descriptionState?: string,
  ) => {
    if (!selectedIssue) return;
    await updateIssueById(selectedIssue, { description, descriptionState });
    await refreshActivity();
  };
  const linkSelectedReview = async (reviewId: string) => {
    if (!selectedIssue) return;
    const review = data?.reviews.find((item) => item.id === reviewId);
    if (!review) throw new Error("Pull request not found");
    const issueIds = [...new Set([...review.issueIds, selectedIssue.id])];
    await updateReview(review.id, { issueIds });
    setData((current) =>
      current
        ? {
            ...current,
            reviews: current.reviews.map((item) =>
              item.id === reviewId ? { ...item, issueIds } : item,
            ),
          }
        : current,
    );
    await refreshActivity();
  };
  const unlinkSelectedReview = async (reviewId: string) => {
    if (!selectedIssue) return;
    const review = data?.reviews.find((item) => item.id === reviewId);
    if (!review) throw new Error("Pull request not found");
    const issueIds = review.issueIds.filter((id) => id !== selectedIssue.id);
    await updateReview(review.id, { issueIds });
    setData((current) =>
      current
        ? {
            ...current,
            reviews: current.reviews.map((item) =>
              item.id === reviewId ? { ...item, issueIds } : item,
            ),
          }
        : current,
    );
    await refreshActivity();
  };
  const selectedIssueOptionsActions: IssueOptionsActions | undefined =
    selectedIssue
      ? {
          addLink: addSelectedIssueLink,
          addCustomerRequest: addSelectedCustomerRequest,
          addDocument: addSelectedDocument,
          linkReview: linkSelectedReview,
          unlinkReview: unlinkSelectedReview,
          toggleRelease: toggleSelectedRelease,
          createRelated: createSelectedRelated,
          configureRecurring: () => navigateTo(`${settingsPath(data!.workspace.urlKey, 'team', selectedIssue.team.key, 'recurring-issues')}?fromIssue=${encodeURIComponent(selectedIssue.id)}`),
          convert: convertSelectedIssue,
          setRecurring: (recurrence) =>
            updateIssueById(selectedIssue, {
              recurrence,
              nextOccurrenceAt: nextOccurrence(recurrence).toISOString(),
            }).then(() => undefined),
          toggleFavorite: toggleSelectedFavorite,
          remind: remindSelectedIssue,
          runLoop: runSelectedIssueLoop,
          restoreDescription: restoreSelectedDescription,
        }
      : undefined;
  const updateInboxIssue = async (issue: Issue, input: IssueUpdateInput) => {
    await updateIssueById(issue, input);
    await refreshActivity();
  };
  const deleteInboxIssue = async (issue: Issue) => {
    await run(() => deleteIssue(issue.id), "Could not delete issue");
    setData((current) =>
      current
        ? {
            ...current,
            issues: current.issues.filter((item) => item.id !== issue.id),
          }
        : current,
    );
  };
  const createInboxSubIssue = async (
    issue: Issue,
    input: {
      title: string;
      description: string;
      stateId: string;
      priority: number;
      assigneeId?: string;
      projectId?: string;
      cycleId?: string;
      dueDate?: string;
      labelIds: string[];
      attachments: File[];
    },
  ) => {
    const { attachments, ...fields } = input;
    const child = await run(
      () =>
        createIssue({
          ...fields,
          teamId: issue.team.id,
          parentId: issue.id,
        }),
      "Could not create sub-issue",
    );
    const uploaded = await Promise.all(
      attachments.map((file) => uploadAttachment(child.id, file)),
    );
    const complete = { ...child, attachments: uploaded };
    setData((current) =>
      current
        ? {
            ...current,
            issues: [
              complete,
              ...current.issues.map((item) =>
                item.id === issue.id
                  ? { ...item, subIssueIds: [...item.subIssueIds, child.id] }
                  : item,
              ),
            ],
            activities: { ...current.activities, [child.id]: [] },
          }
        : current,
    );
    await refreshActivity();
  };
  const reactToInboxIssue = async (issue: Issue, emoji: string) => {
    const updated = await run(
      () => toggleIssueReaction(issue.id, emoji),
      "Could not update reaction",
    );
    replaceIssue(updated);
  };
  const createInboxComment = async (
    issue: Issue,
    body: string,
    bodyData?: Record<string, unknown>,
    parentId?: string,
  ) => {
    await createCommentOptimistically(issue, body, bodyData, parentId);
  };
  const editInboxComment = async (
    issue: Issue,
    id: string,
    body: string,
    bodyData?: Record<string, unknown>,
  ) => {
    await editCommentOptimistically(issue, id, body, bodyData);
  };
  const deleteInboxComment = async (issue: Issue, id: string) => {
    await deleteCommentOptimistically(issue, id);
  };
  const reactToInboxComment = async (
    issue: Issue,
    id: string,
    emoji: string,
  ) => {
    await reactToCommentOptimistically(issue, id, emoji);
  };
  const createInboxRelation = async (
    issue: Issue,
    type: IssueRelationType,
    relatedIssueId: string,
  ) => {
    await run(
      () => createRelation(issue.id, type, relatedIssueId),
      "Could not add relation",
    );
    await refreshActivity();
  };
  const deleteInboxRelation = async (issue: Issue, relationId: string) => {
    await run(
      () => deleteRelation(issue.id, relationId),
      "Could not remove relation",
    );
    await refreshActivity();
  };
  const uploadInboxAttachment = async (issue: Issue, file: File) => {
    await run(
      () => uploadAttachment(issue.id, file),
      "Could not upload attachment",
    );
    await refreshActivity();
  };
  const deleteInboxAttachment = async (issue: Issue, id: string) => {
    await run(
      () => deleteAttachment(issue.id, id),
      "Could not remove attachment",
    );
    await refreshActivity();
  };
  const addProject = async (input: ProjectCreateInput) => {
    const project = await run(
      () => createProject(input),
      "Could not create project",
    );
    let relations: ProjectRelation[] | undefined;
    if (input.templateId !== undefined || input.dependencyIds !== undefined || input.dependencyRelations !== undefined) {
      try {
        relations = (await listProjectRelations(project.id)).nodes;
      } catch {
        relations = undefined;
      }
    }
    setData((current) =>
      current
        ? {
            ...current,
            projects: [project, ...current.projects],
            projectRelations: relations
              ? mergeProjectRelations(current, project.id, relations)
              : current.projectRelations,
          }
        : current,
    );
    return project;
  };
  const changeProject = async (id: string, input: ProjectMutationInput) => {
    const project = await run(
      () => updateProject(id, input),
      "Could not update project",
    );
    let relations: ProjectRelation[] | undefined;
    if (input.dependencyIds !== undefined || input.dependencyRelations !== undefined) {
      try {
        relations = (await listProjectRelations(id)).nodes;
      } catch {
        relations = undefined;
      }
    }
    setData((current) =>
      current
        ? {
            ...current,
            projects: current.projects.map((item) =>
              item.id === id ? project : item,
            ),
            projectRelations: relations
              ? mergeProjectRelations(current, id, relations)
              : current.projectRelations,
          }
        : current,
    );
    return project;
  };
  const removeProject = async (id: string) => {
    await run(() => deleteProject(id), "Could not delete project");
    setData((current) =>
      current
        ? {
            ...current,
            projects: current.projects
              .filter((project) => project.id !== id)
              .map((project) => ({
                ...project,
                dependencyIds: project.dependencyIds?.filter((dependencyId) => dependencyId !== id),
              })),
            projectRelations: (current.projectRelations ?? []).filter(
              (relation) => relation.projectId !== id && relation.relatedProjectId !== id,
            ),
            projectUpdates: Object.fromEntries(
              Object.entries(current.projectUpdates ?? {}).filter(
                ([projectId]) => projectId !== id,
              ),
            ),
            issues: current.issues.map((issue) =>
              issue.project?.id === id
                ? { ...issue, project: undefined }
                : issue,
            ),
          }
        : current,
    );
    if (data && selectedProject?.id === id)
      navigateTo(navigationReturnPath(location.state, data.workspace.urlKey, projectsPath(data.workspace.urlKey)), { replace: true });
  };
  const toggleProjectFavorite = async (
    projectId: string,
    favorite: boolean,
  ) => {
    const current = data?.favorites.find(
      (item) =>
        item.userId === data.viewer.id &&
        item.resourceType === "project" &&
        item.resourceId === projectId,
    );
    if (!favorite) {
      if (current)
        await run(
          () => removeFavorite("project", projectId),
          "Could not update favorite",
        );
      setData((state) =>
        state
          ? {
              ...state,
              favorites: state.favorites.filter(
                (item) =>
                  !(
                    item.userId === state.viewer.id &&
                    item.resourceType === "project" &&
                    item.resourceId === projectId
                  ),
              ),
            }
          : state,
      );
      return;
    }
    const created = await run(
      () => addFavorite("project", projectId),
      "Could not update favorite",
    );
    setData((state) =>
      state
        ? {
            ...state,
            favorites: [
              created,
              ...state.favorites.filter((item) => item.id !== created.id),
            ],
          }
        : state,
    );
  };
  const toggleTeamFavorite = async (teamId: string, favorite: boolean) => {
    if (!favorite) {
      await run(
        () => removeFavorite("team", teamId),
        "Could not update favorite",
      );
      setData((state) =>
        state
          ? {
              ...state,
              favorites: state.favorites.filter(
                (item) =>
                  !(
                    item.userId === state.viewer.id &&
                    item.resourceType === "team" &&
                    item.resourceId === teamId
                  ),
              ),
            }
          : state,
      );
      return;
    }
    const created = await run(
      () => addFavorite("team", teamId),
      "Could not update favorite",
    );
    setData((state) =>
      state
        ? {
            ...state,
            favorites: [
              created,
              ...state.favorites.filter((item) => item.id !== created.id),
            ],
          }
        : state,
    );
  };
  const setProjectSubscriptionEvents = async (
    projectId: string,
    events: string[],
  ) => {
    if (!events.length) {
      await run(
        () => removeSubscription("project", projectId),
        "Could not update project notifications",
      );
      setData((state) =>
        state
          ? {
              ...state,
              subscriptions: state.subscriptions.filter(
                (item) =>
                  !(
                    item.userId === state.viewer.id &&
                    item.resourceType === "project" &&
                    item.resourceId === projectId
                  ),
              ),
            }
          : state,
      );
      return;
    }
    const updated = await run(
      () => addSubscription("project", projectId, events),
      "Could not update project notifications",
    );
    setData((state) =>
      state
        ? {
            ...state,
            subscriptions: [
              updated,
              ...state.subscriptions.filter((item) => item.id !== updated.id),
            ],
          }
        : state,
    );
  };
  const addProjectReminder = async (projectId: string, remindAt: string) => {
    const notification = await run(
      () => createProjectReminder(projectId, remindAt),
      "Could not create project reminder",
    );
    setData((state) =>
      state
        ? {
            ...state,
            notifications: [
              notification,
              ...state.notifications.filter(
                (item) => item.id !== notification.id,
              ),
            ],
          }
        : state,
    );
    return notification;
  };
  const addInitiative = async (
    input: InitiativeMutationInput & { name: string },
  ) => {
    const initiative = await run(
      () => createInitiative(input),
      "Could not create initiative",
    );
    setData((current) =>
      current
        ? {
            ...current,
            initiatives: [initiative, ...current.initiatives],
            initiativeUpdates: {
              ...current.initiativeUpdates,
              [initiative.id]: [],
            },
            projects: current.projects.map((project) =>
              initiative.projectIds.includes(project.id)
                ? {
                    ...project,
                    initiatives: [
                      ...new Set([
                        ...(project.initiatives ?? []),
                        initiative.id,
                      ]),
                    ],
                  }
                : project,
            ),
          }
        : current,
    );
    return initiative;
  };
  const addInitiativeLabel = async (name: string): Promise<IssueLabel> => {
    const label = await run(
      () => createWorkspaceLabel({ name, resourceType: "initiative" }),
      "Could not create initiative label",
    );
    setData((current) =>
      current ? { ...current, labels: [...current.labels, label] } : current,
    );
    return label;
  };
  const addProjectLabel = async (name: string, groupId?: string): Promise<IssueLabel> => {
    const label = await run(
      () => createWorkspaceLabel({ name, resourceType: "project", groupId }),
      "Could not create project label",
    );
    setData(current => current ? { ...current, labels: [...current.labels, label] } : current);
    return label;
  };
  const addInitiativeReminder = async (
    initiativeId: string,
    remindAt: string,
  ) => {
    const notification = await run(
      () => createInitiativeReminder(initiativeId, remindAt),
      "Could not create initiative reminder",
    );
    setData((current) =>
      current
        ? {
            ...current,
            notifications: [
              notification,
              ...current.notifications.filter(
                (item) => item.id !== notification.id,
              ),
            ],
          }
        : current,
    );
    return notification;
  };
  const changeInitiative = async (
    id: string,
    input: InitiativeMutationInput,
  ) => {
    const initiative = await run(
      () => updateInitiative(id, input),
      "Could not update initiative",
    );
    setData((current) =>
      current
        ? {
            ...current,
            initiatives: current.initiatives.map((item) =>
              item.id === id ? initiative : item,
            ),
            projects: current.projects.map((project) => ({
              ...project,
              initiatives: initiative.projectIds.includes(project.id)
                ? [...new Set([...(project.initiatives ?? []), id])]
                : (project.initiatives ?? []).filter((item) => item !== id),
            })),
          }
        : current,
    );
    return initiative;
  };
  const removeInitiative = async (id: string) => {
    await run(() => deleteInitiative(id), "Could not delete initiative");
    setData((current) =>
      current
        ? {
            ...current,
            initiatives: current.initiatives.filter((item) => item.id !== id).map(item => ({ ...item, parentInitiativeIds: (item.parentInitiativeIds ?? []).filter(parent => parent !== id) })),
            initiativeUpdates: Object.fromEntries(
              Object.entries(current.initiativeUpdates ?? {}).filter(
                ([initiativeId]) => initiativeId !== id,
              ),
            ),
            projects: current.projects.map((project) => ({
              ...project,
              initiatives: (project.initiatives ?? []).filter(
                (item) => item !== id,
              ),
            })),
          }
        : current,
    );
  };
  const addInitiativeUpdate = async (
    id: string,
    input: {
      body: string;
      bodyData?: Record<string, unknown>;
      health?: Project["health"];
    },
  ) => {
    const update = await run(
      () => createInitiativeUpdate(id, input),
      "Could not post initiative update",
    );
    setData((current) =>
      current
        ? {
            ...current,
            initiativeUpdates: {
              ...current.initiativeUpdates,
              [id]: [update, ...(current.initiativeUpdates[id] ?? [])],
            },
            initiatives: current.initiatives.map((item) =>
              item.id === id
                ? {
                    ...item,
                    health: update.health,
                    updatedAt: update.createdAt,
                  }
                : item,
            ),
          }
        : current,
    );
    return update;
  };
  const removeInitiativeUpdate = async (id: string, updateId: string) => {
    await run(
      () => deleteInitiativeUpdate(id, updateId),
      "Could not delete initiative update",
    );
    setData((current) => {
      if (!current) return current;
      const remaining = (current.initiativeUpdates[id] ?? []).filter(
        (item) => item.id !== updateId,
      );
      return {
        ...current,
        initiativeUpdates: { ...current.initiativeUpdates, [id]: remaining },
        initiatives: current.initiatives.map((item) =>
          item.id === id
            ? { ...item, health: remaining[0]?.health ?? "noUpdate" }
            : item,
        ),
      };
    });
  };
  const commentOnInitiative = async (id: string, body: string) => {
    const comment = await run(
      () => createInitiativeComment(id, body),
      "Could not post initiative comment",
    );
    setData((current) =>
      current
        ? {
            ...current,
            initiatives: current.initiatives.map((item) =>
              item.id === id
                ? { ...item, comments: [...(item.comments ?? []), comment] }
                : item,
            ),
          }
        : current,
    );
  };
  const editInitiativeComment = async (
    id: string,
    commentId: string,
    body: string,
  ) => {
    const comment = await run(
      () => updateInitiativeComment(id, commentId, body),
      "Could not edit initiative comment",
    );
    setData((current) =>
      current
        ? {
            ...current,
            initiatives: current.initiatives.map((item) =>
              item.id === id
                ? {
                    ...item,
                    comments: (item.comments ?? []).map((existing) =>
                      existing.id === commentId ? comment : existing,
                    ),
                  }
                : item,
            ),
          }
        : current,
    );
    return comment;
  };
  const removeInitiativeComment = async (id: string, commentId: string) => {
    await run(
      () => deleteInitiativeComment(id, commentId),
      "Could not delete initiative comment",
    );
    setData((current) =>
      current
        ? {
            ...current,
            initiatives: current.initiatives.map((item) =>
              item.id === id
                ? {
                    ...item,
                    comments: (item.comments ?? []).filter(
                      (comment) => comment.id !== commentId,
                    ),
                  }
                : item,
            ),
          }
        : current,
    );
  };
  const reactToInitiativeComment = async (
    id: string,
    commentId: string,
    emoji: string,
  ) => {
    const comment = await run(
      () => toggleInitiativeCommentReaction(id, commentId, emoji),
      "Could not update initiative reaction",
    );
    setData((current) =>
      current
        ? {
            ...current,
            initiatives: current.initiatives.map((item) =>
              item.id === id
                ? {
                    ...item,
                    comments: (item.comments ?? []).map((existing) =>
                      existing.id === commentId ? comment : existing,
                    ),
                  }
                : item,
            ),
          }
        : current,
    );
    return comment;
  };
  const addInitiativeResource = async (
    id: string,
    input: {
      type?: "link" | "document";
      title?: string;
      url?: string;
      documentId?: string;
    },
  ) => {
    const resource = await run(
      () => createInitiativeResource(id, input),
      "Could not add initiative resource",
    );
    setData((current) =>
      current
        ? {
            ...current,
            initiatives: current.initiatives.map((item) =>
              item.id === id
                ? { ...item, resources: [...(item.resources ?? []), resource] }
                : item,
            ),
          }
        : current,
    );
    return resource;
  };
  const changeInitiativeResource = async (
    id: string,
    resourceId: string,
    input: { type?: "link" | "document"; title?: string; url?: string },
  ) => {
    const resource = await run(
      () => updateInitiativeResource(id, resourceId, input),
      "Could not update initiative resource",
    );
    setData((current) =>
      current
        ? {
            ...current,
            initiatives: current.initiatives.map((item) =>
              item.id === id
                ? {
                    ...item,
                    resources: (item.resources ?? []).map((existing) =>
                      existing.id === resourceId ? resource : existing,
                    ),
                  }
                : item,
            ),
          }
        : current,
    );
    return resource;
  };
  const removeInitiativeResource = async (id: string, resourceId: string) => {
    await run(
      () => deleteInitiativeResource(id, resourceId),
      "Could not delete initiative resource",
    );
    setData((current) =>
      current
        ? {
            ...current,
            initiatives: current.initiatives.map((item) =>
              item.id === id
                ? {
                    ...item,
                    resources: (item.resources ?? []).filter(
                      (resource) => resource.id !== resourceId,
                    ),
                  }
                : item,
            ),
          }
        : current,
    );
  };
  const changeInitiativeUpdate = async (
    id: string,
    updateId: string,
    input: {
      body?: string;
      bodyData?: Record<string, unknown>;
      health?: Project["health"];
    },
  ) => {
    const update = await run(
      () => updateInitiativeUpdate(id, updateId, input),
      "Could not edit initiative update",
    );
    setData((current) =>
      current
        ? {
            ...current,
            initiativeUpdates: {
              ...current.initiativeUpdates,
              [id]: (current.initiativeUpdates[id] ?? []).map((item) =>
                item.id === updateId ? update : item,
              ),
            },
            initiatives: current.initiatives.map((item) =>
              item.id === id &&
              (current.initiativeUpdates[id] ?? [])[0]?.id === updateId
                ? { ...item, health: update.health }
                : item,
            ),
          }
        : current,
    );
    return update;
  };
  const replaceInitiativeUpdate = (
    initiativeId: string,
    update: InitiativeUpdate,
  ) =>
    setData((current) =>
      current
        ? {
            ...current,
            initiativeUpdates: {
              ...current.initiativeUpdates,
              [initiativeId]: (
                current.initiativeUpdates[initiativeId] ?? []
              ).map((item) => (item.id === update.id ? update : item)),
            },
          }
        : current,
    );
  const commentOnInitiativeUpdate = async (
    initiativeId: string,
    updateId: string,
    body: string,
  ) => {
    const update = await run(
      () => createInitiativeUpdateComment(initiativeId, updateId, body),
      "Could not post comment",
    );
    replaceInitiativeUpdate(initiativeId, update);
    return update;
  };
  const reactToInitiativeUpdate = async (
    initiativeId: string,
    updateId: string,
    emoji: string,
  ) => {
    const update = await run(
      () => toggleInitiativeUpdateReaction(initiativeId, updateId, emoji),
      "Could not update reaction",
    );
    replaceInitiativeUpdate(initiativeId, update);
    return update;
  };
  const addInitiativeUpdateAttachment = async (
    initiativeId: string,
    updateId: string,
    file: File,
  ) => {
    const update = await run(
      () => uploadInitiativeUpdateAttachment(initiativeId, updateId, file),
      "Could not upload update attachment",
    );
    replaceInitiativeUpdate(initiativeId, update);
    return update;
  };
  const removeInitiativeUpdateAttachment = async (
    initiativeId: string,
    updateId: string,
    attachmentId: string,
  ) => {
    const update = await run(
      () =>
        deleteInitiativeUpdateAttachment(initiativeId, updateId, attachmentId),
      "Could not delete update attachment",
    );
    replaceInitiativeUpdate(initiativeId, update);
    return update;
  };
  const addProjectUpdate = async (
    id: string,
    input: {
      body: string;
      bodyData?: Record<string, unknown>;
      health?: Project["health"];
    },
  ) => {
    const update = await run(
      () => createProjectUpdate(id, input),
      "Could not post project update",
    );
    setData((current) =>
      current
        ? {
            ...current,
            projectUpdates: {
              ...(current.projectUpdates ?? {}),
              [id]: [update, ...(current.projectUpdates?.[id] ?? [])],
            },
            projects: current.projects.map((project) =>
              project.id === id
                ? {
                    ...project,
                    health: update.health,
                    updatedAt: update.createdAt,
                  }
                : project,
            ),
          }
        : current,
    );
    return update;
  };
  const changeProjectUpdate = async (
    projectId: string,
    updateId: string,
    input: {
      body?: string;
      bodyData?: Record<string, unknown>;
      health?: Project["health"];
    },
  ) => {
    const update = await run(
      () => updateProjectUpdate(projectId, updateId, input),
      "Could not edit project update",
    );
    setData((current) =>
      current
        ? {
            ...current,
            projectUpdates: {
              ...(current.projectUpdates ?? {}),
              [projectId]: (current.projectUpdates?.[projectId] ?? []).map(
                (item) => (item.id === updateId ? update : item),
              ),
            },
            projects: current.projects.map((project) =>
              project.id === projectId &&
              current.projectUpdates?.[projectId]?.[0]?.id === updateId
                ? {
                    ...project,
                    health: update.health,
                    updatedAt: update.editedAt ?? project.updatedAt,
                  }
                : project,
            ),
          }
        : current,
    );
    return update;
  };
  const removeProjectUpdate = async (projectId: string, updateId: string) => {
    await run(
      () => deleteProjectUpdate(projectId, updateId),
      "Could not delete project update",
    );
    setData((current) => {
      if (!current) return current;
      const remaining = (current.projectUpdates?.[projectId] ?? []).filter(
        (update) => update.id !== updateId,
      );
      return {
        ...current,
        projectUpdates: {
          ...(current.projectUpdates ?? {}),
          [projectId]: remaining,
        },
        projects: current.projects.map((project) =>
          project.id === projectId
            ? {
                ...project,
                health: remaining[0]?.health ?? "noUpdate",
                updatedAt: remaining[0]?.createdAt ?? project.updatedAt,
              }
            : project,
        ),
      };
    });
  };
  const replaceProjectUpdate = (projectId: string, update: ProjectUpdate) =>
    setData((current) =>
      current
        ? {
            ...current,
            projectUpdates: {
              ...(current.projectUpdates ?? {}),
              [projectId]: (current.projectUpdates?.[projectId] ?? []).map(
                (item) => (item.id === update.id ? update : item),
              ),
            },
          }
        : current,
    );
  const commentOnProjectUpdate = async (
    projectId: string,
    updateId: string,
    body: string,
  ) => {
    const update = await run(
      () => createProjectUpdateComment(projectId, updateId, body),
      "Could not post comment",
    );
    replaceProjectUpdate(projectId, update);
    return update;
  };
  const reactToProjectUpdate = async (
    projectId: string,
    updateId: string,
    emoji: string,
  ) => {
    const update = await run(
      () => toggleProjectUpdateReaction(projectId, updateId, emoji),
      "Could not update reaction",
    );
    replaceProjectUpdate(projectId, update);
    return update;
  };
  const addProjectUpdateAttachment = async (
    projectId: string,
    updateId: string,
    file: File,
  ) => {
    const update = await run(
      () => uploadProjectUpdateAttachment(projectId, updateId, file),
      "Could not upload update attachment",
    );
    replaceProjectUpdate(projectId, update);
    return update;
  };
  const removeProjectUpdateAttachment = async (
    projectId: string,
    updateId: string,
    attachmentId: string,
  ) => {
    const update = await run(
      () => deleteProjectUpdateAttachment(projectId, updateId, attachmentId),
      "Could not delete update attachment",
    );
    replaceProjectUpdate(projectId, update);
    return update;
  };
  const addProjectResource = async (
    projectId: string,
    input: { type?: "link" | "document"; title?: string; url?: string },
  ) => {
    if (input.type === "document") {
      const document = await run(
        () =>
          createDocument({
            title: input.title || "New document",
            projectIds: [projectId],
          }),
        "Could not create document",
      );
      const next = await fetchBootstrap(data?.workspace.urlKey);
      acceptBootstrap(next);
      const resource = next.projects
        .find((project) => project.id === projectId)
        ?.resources.find((item) => item.id === document.id);
      if (!resource) throw new Error("Document resource was not linked");
      navigateTo(documentPath(next.workspace.urlKey, document));
      return resource;
    }
    if (!input.url) throw new Error("A link URL is required");
    const resource = await run(
      () => createProjectResource(projectId, { ...input, url: input.url! }),
      "Could not add project resource",
    );
    setData((current) =>
      current
        ? {
            ...current,
            projects: current.projects.map((project) =>
              project.id === projectId
                ? {
                    ...project,
                    resources: [...(project.resources ?? []), resource],
                  }
                : project,
            ),
          }
        : current,
    );
    return resource;
  };
  const changeProjectResource = async (
    projectId: string,
    resourceId: string,
    input: { type?: "link" | "document"; title?: string; url?: string },
  ) => {
    const resource = await run(
      () => updateProjectResource(projectId, resourceId, input),
      "Could not update project resource",
    );
    setData((current) =>
      current
        ? {
            ...current,
            projects: current.projects.map((project) =>
              project.id === projectId
                ? {
                    ...project,
                    resources: (project.resources ?? []).map((item) =>
                      item.id === resourceId ? resource : item,
                    ),
                  }
                : project,
            ),
          }
        : current,
    );
    return resource;
  };
  const removeProjectResource = async (
    projectId: string,
    resourceId: string,
  ) => {
    await run(
      () => deleteProjectResource(projectId, resourceId),
      "Could not delete project resource",
    );
    setData((current) =>
      current
        ? {
            ...current,
            projects: current.projects.map((project) =>
              project.id === projectId
                ? {
                    ...project,
                    resources: (project.resources ?? []).filter(
                      (item) => item.id !== resourceId,
                    ),
                  }
                : project,
            ),
          }
        : current,
    );
  };
  const addProjectMilestone = async (
    projectId: string,
    input: { name: string; description?: string; targetDate?: string },
  ) => {
    const milestone = await run(
      () => createProjectMilestone(projectId, input),
      "Could not add project milestone",
    );
    setData((current) =>
      current
        ? {
            ...current,
            projects: current.projects.map((project) =>
              project.id === projectId
                ? {
                    ...project,
                    milestones: [...(project.milestones ?? []), milestone],
                  }
                : project,
            ),
          }
        : current,
    );
    return milestone;
  };
  const addIssueProject = async (draft: NewProjectDraft) => {
    if (!data) throw new Error("Workspace is not loaded");
    const project = await addProject({
      templateId: draft.templateId,
      color: draft.color,
      description: draft.description,
      icon: draft.icon,
      leadId: draft.leadId,
      memberIds: draft.memberIds,
      labelIds: draft.labelIds,
      dependencyIds: draft.dependencyIds,
      dependencyRelations: draft.dependencyRelations,
      milestones: draft.milestones,
      milestoneDetails: draft.milestoneDetails,
      initiatives: draft.initiativeIds,
      name: draft.name,
      priority: Math.max(
        0,
        ["No priority", "Urgent", "High", "Medium", "Low"].indexOf(
          draft.priority,
        ),
      ),
      startDate: draft.startDate,
      startDateResolution: draft.startDateResolution,
      statusId:
        data.projectStatuses.find((status) => status.name === draft.status)
          ?.id ??
        data.projects.find((project) => project.status.name === draft.status)
          ?.status.id,
      summary: draft.summary,
      targetDate: draft.targetDate,
      targetDateResolution: draft.targetDateResolution,
      teamIds: draft.teamIds,
    });
    return project;
  };
  const changeProjectMilestone = async (
    projectId: string,
    milestoneId: string,
    input: { name?: string; description?: string; targetDate?: string },
  ) => {
    const milestone = await run(
      () => updateProjectMilestone(projectId, milestoneId, input),
      "Could not update project milestone",
    );
    setData((current) =>
      current
        ? {
            ...current,
            projects: current.projects.map((project) =>
              project.id === projectId
                ? {
                    ...project,
                    milestones: (project.milestones ?? []).map((item) =>
                      item.id === milestoneId ? milestone : item,
                    ),
                  }
                : project,
            ),
          }
        : current,
    );
    return milestone;
  };
  const removeProjectMilestone = async (
    projectId: string,
    milestoneId: string,
  ) => {
    await run(
      () => deleteProjectMilestone(projectId, milestoneId),
      "Could not delete project milestone",
    );
    setData((current) =>
      current
        ? {
            ...current,
            projects: current.projects.map((project) =>
              project.id === projectId
                ? {
                    ...project,
                    milestones: (project.milestones ?? []).filter(
                      (item) => item.id !== milestoneId,
                    ),
                  }
                : project,
            ),
          }
        : current,
    );
  };
  const reorderMilestones = async (projectId: string, ids: string[]) => {
    const original =
      data?.projects.find((project) => project.id === projectId)?.milestones ??
      [];
    const byId = new Map(
      original.map((milestone) => [milestone.id, milestone]),
    );
    const optimistic = ids.flatMap((id) => {
      const milestone = byId.get(id);
      return milestone ? [milestone] : [];
    });
    const setMilestones = (milestones: ProjectMilestone[]) =>
      setData((current) =>
        current
          ? {
              ...current,
              projects: current.projects.map((project) =>
                project.id === projectId ? { ...project, milestones } : project,
              ),
            }
          : current,
      );
    setMilestones(optimistic);
    try {
      const milestones = await reorderProjectMilestones(projectId, ids);
      setMilestones(milestones);
      return milestones;
    } catch (error) {
      setMilestones(original);
      toast.error("Could not reorder project milestones", {
        description: error instanceof Error ? error.message : undefined,
      });
      throw error;
    }
  };
  const moveProjectMilestone = async (
    projectId: string,
    milestoneId: string,
    targetProjectId: string,
  ) => {
    const source = data?.projects.find((project) => project.id === projectId);
    const milestone = source?.milestones?.find(
      (item) => item.id === milestoneId,
    );
    if (!milestone) throw new Error("Milestone not found");
    await addProjectMilestone(targetProjectId, {
      name: milestone.name,
      description: milestone.description,
      targetDate: milestone.targetDate,
    });
    await removeProjectMilestone(projectId, milestoneId);
  };
  const convertProjectMilestone = async (
    projectId: string,
    milestoneId: string,
  ) => {
    const source = data?.projects.find((project) => project.id === projectId);
    const milestone = source?.milestones?.find(
      (item) => item.id === milestoneId,
    );
    if (!source || !milestone) throw new Error("Milestone not found");
    const created = await addProject({
      name: milestone.name,
      summary: `Converted from milestone in ${source.name}`,
      teamIds: source.teamIds,
      targetDate: milestone.targetDate,
    });
    await removeProjectMilestone(projectId, milestoneId);
    return created;
  };
  const commentOnProject = async (projectId: string, body: string) => {
    const comment = await run(
      () => createProjectComment(projectId, body),
      "Could not post project comment",
    );
    setData((current) =>
      current
        ? {
            ...current,
            projects: current.projects.map((project) =>
              project.id === projectId
                ? {
                    ...project,
                    comments: [...(project.comments ?? []), comment],
                  }
                : project,
            ),
          }
        : current,
    );
    return comment;
  };
  const changeProjectDisplayDefault = async (
    display: Record<string, unknown>,
  ) => {
    const saved = await run(
      () => setProjectDisplayDefault(display),
      "Could not save project view default",
    );
    setData((current) =>
      current ? { ...current, projectDisplayDefault: saved } : current,
    );
  };
  const batchUpdate = async (input: IssueUpdateInput) => {
    const updated = await run(
      () => batchUpdateIssues([...selected], input),
      "Could not update selected issues",
    );
    setData((current) =>
      current
        ? {
            ...current,
            issues: current.issues.map(
              (i) => updated.find((x) => x.id === i.id) || i,
            ),
          }
        : current,
    );
    setSelected(new Set());
  };
  const batchToggleLabel = async (labelId: string) => {
    if (!data) return;
    const issues = data.issues.filter((issue) => selected.has(issue.id));
    const shouldSelect = !issues.every((issue) =>
      issue.labels.some((label) => label.id === labelId),
    );
    const updated = await run(
      () =>
        Promise.all(
          issues.map((issue) =>
            updateIssue(issue.id, {
              expectedVersion: issue.version,
              labelIds: setGroupedLabelSelected(
                issue.labels.map((label) => label.id),
                labelId,
                data.labels,
                shouldSelect,
              ),
            }),
          ),
        ),
      "Could not update selected issue labels",
    );
    setData((current) =>
      current
        ? {
            ...current,
            issues: current.issues.map(
              (issue) => updated.find((item) => item.id === issue.id) || issue,
            ),
          }
        : current,
    );
    setSelected(new Set());
  };
  const batchDelete = async () => {
    if (
      !(await confirmAction(`Delete ${selected.size} selected issues?`, {
        description: "This cannot be undone.",
        confirmLabel: "Delete",
      }))
    )
      return;
    await run(
      () => Promise.all([...selected].map(deleteIssue)),
      "Could not delete selected issues",
    );
    setData((current) =>
      current
        ? {
            ...current,
            issues: current.issues.filter((i) => !selected.has(i.id)),
          }
        : current,
    );
    setSelected(new Set());
  };
  const updateIssueFromPage = async (id: string, input: IssueUpdateInput) => {
    const issue = data?.issues.find((item) => item.id === id) ?? (data?.issueCollectionPaged ? await fetchIssueRecord(id) : undefined);
    if (!issue) throw new Error("Issue not found");
    return updateIssueById(issue, input);
  };
  const changeCycle = async (id: string, input: CycleMutationInput) => {
    const cycle = await run(
      () => updateCycleRequest(id, input),
      "Could not update cycle",
    );
    setData((current) =>
      current
        ? {
            ...current,
            cycles: current.cycles.map((item) =>
              item.id === id ? cycle : item,
            ),
          }
        : current,
    );
    return cycle;
  };
  const startCycle = async (cycle: Cycle) => {
    await run(() => startCycleRequest(cycle.id), "Could not start cycle");
    await refreshActivity();
  };
  const finishCycle = async (cycle: Cycle) => {
    await run(() => completeCycleRequest(cycle.id), "Could not complete cycle");
    await refreshActivity();
  };
  const changeCycleSettings = async (
    teamId: string,
    input: CycleSettingsMutationInput,
  ) => {
    const settings = await run(
      () => updateCycleSettings(teamId, input),
      "Could not update cycle settings",
    );
    setData((current) =>
      current
        ? {
            ...current,
            cycleSettings: { ...current.cycleSettings, [teamId]: settings },
          }
        : current,
    );
    return settings;
  };
  const updateIssuesFromPage = async (
    ids: string[],
    input: IssueUpdateInput,
  ) => {
    const issues = await run(
      () => batchUpdateIssues(ids, input),
      "Could not update issues",
    );
    setData((current) =>
      current
        ? {
            ...current,
            issues: current.issueCollectionPaged ? [...issues, ...current.issues.filter(issue => !issues.some(updated => updated.id === issue.id))].slice(0, 2000) : current.issues.map(
              (issue) =>
                issues.find((updated) => updated.id === issue.id) ?? issue,
            ),
          }
        : current,
    );
    return issues;
  };
  const deleteIssuesFromPage = async (ids: string[]) => {
    await run(
      () => Promise.all(ids.map(deleteIssue)),
      "Could not delete issues",
    );
    setData((current) =>
      current
        ? {
            ...current,
            issueCollectionRevision: (current.issueCollectionRevision ?? 0) + 1,
            issues: current.issues.filter((issue) => !ids.includes(issue.id)),
          }
        : current,
    );
  };
  const addSavedView = async (input: SavedViewMutationInput) => {
    const view = await run(
      () => createSavedView(input),
      "Could not create view",
    );
    setData((current) =>
      current
        ? { ...current, savedViews: [...(current.savedViews ?? []), view] }
        : current,
    );
    return view;
  };
  const changeSavedView = async (id: string, input: SavedViewMutationInput) => {
    const view = await run(
      () => updateSavedView(id, input),
      "Could not update view",
    );
    setData((current) =>
      current
        ? {
            ...current,
            savedViews: (current.savedViews ?? []).map((item) =>
              item.id === id ? view : item,
            ),
          }
        : current,
    );
    return view;
  };
  const shareView = async (view: SavedView) => {
    if (view.shareToken) {
      await run(
        () => unshareSavedView(view.id),
        "Could not disable view sharing",
      );
      setData((current) =>
        current
          ? {
              ...current,
              savedViews: (current.savedViews ?? []).map((item) =>
                item.id === view.id
                  ? { ...item, shareToken: undefined, sharedAt: undefined }
                  : item,
              ),
            }
          : current,
      );
      return undefined;
    }
    const shared = await run(
      () => shareSavedView(view.id),
      "Could not share view",
    );
    setData((current) =>
      current
        ? {
            ...current,
            savedViews: (current.savedViews ?? []).map((item) =>
              item.id === view.id ? shared.view : item,
            ),
          }
        : current,
    );
    return shared.url;
  };
  useEffect(() => {
    const sync = (event: Event) => {
      const {workspaceKey,settings} = (event as CustomEvent<{workspaceKey:string;settings:UserSettings}>).detail;
      setData(current => current && current.workspace.urlKey === workspaceKey && current.viewer.id === settings.userId ? {...current,userSettings:{...current.userSettings,[settings.userId]:settings}} : current);
    };
    window.addEventListener('flow:user-settings-updated',sync);
    const syncWorkspace = (event: Event) => {
      const {workspaceKey,settings} = (event as CustomEvent<{workspaceKey:string;settings:BootstrapData['workspaceSettings']}>).detail;
      setData(current => current?.workspace.urlKey === workspaceKey ? { ...current, workspaceSettings: settings } : current);
    };
    window.addEventListener('flow:workspace-preferences-updated',syncWorkspace);
    return () => { window.removeEventListener('flow:user-settings-updated',sync); window.removeEventListener('flow:workspace-preferences-updated',syncWorkspace); };
  }, []);
  const changeCurrentUserSettings = async (input: UserSettings) => {
    const settings = await run(
      () => updateUserSettings(input),
      "Could not update preferences",
    );
    setData((current) =>
      current
        ? {
            ...current,
            userSettings: {
              ...current.userSettings,
              [current.viewer.id]: settings,
            },
          }
        : current,
    );
    return settings;
  };
  const toggleSavedViewFavorite = async (view: SavedView) => {
    const current = data?.favorites.find(
      (item) =>
        item.userId === data.viewer.id &&
        item.resourceType === "view" &&
        item.resourceId === view.id,
    );
    if (current || view.favorite) {
      if (current)
        await run(
          () => removeFavorite("view", view.id),
          "Could not remove favorite",
        );
      if (view.favorite) await changeSavedView(view.id, { favorite: false });
      setData((state) =>
        state
          ? {
              ...state,
              favorites: state.favorites.filter(
                (item) =>
                  !(
                    item.userId === state.viewer.id &&
                    item.resourceType === "view" &&
                    item.resourceId === view.id
                  ),
              ),
            }
          : state,
      );
      return;
    }
    const created = await run(
      () => addFavorite("view", view.id),
      "Could not add favorite",
    );
    setData((state) =>
      state
        ? {
            ...state,
            favorites: [
              created,
              ...state.favorites.filter((item) => item.id !== created.id),
            ],
          }
        : state,
    );
  };
  const setSavedViewSubscriptionEvents = async (
    view: SavedView,
    events: string[],
  ) => {
    const current = data?.subscriptions.find(
      (item) =>
        item.userId === data.viewer.id &&
        item.resourceType === "view" &&
        item.resourceId === view.id,
    );
    if (!events.length) {
      if (current)
        await run(
          () => removeSubscription("view", view.id),
          "Could not unsubscribe from view",
        );
      setData((state) =>
        state
          ? {
              ...state,
              subscriptions: state.subscriptions.filter(
                (item) =>
                  !(
                    item.userId === state.viewer.id &&
                    item.resourceType === "view" &&
                    item.resourceId === view.id
                  ),
              ),
            }
          : state,
      );
      return;
    }
    const updated = await run(
      () => addSubscription("view", view.id, events),
      "Could not update view subscription",
    );
    setData((state) =>
      state
        ? {
            ...state,
            subscriptions: [
              updated,
              ...state.subscriptions.filter((item) => item.id !== updated.id),
            ],
          }
        : state,
    );
  };
  const removeSavedViewOnly = async (view: SavedView) => {
    await run(() => deleteSavedView(view.id), "Could not delete view");
    setData((current) =>
      current
        ? {
            ...current,
            savedViews: (current.savedViews ?? []).filter(
              (item) => item.id !== view.id,
            ),
          }
        : current,
    );
  };
  const removeSavedView = async (view: SavedView) => {
    await run(() => deleteSavedView(view.id), "Could not delete view");
    setData((current) =>
      current
        ? {
            ...current,
            savedViews: (current.savedViews ?? []).filter(
              (item) => item.id !== view.id,
            ),
          }
        : current,
    );
    if (!data) return;
    navigateTo(
      view.scope === "team" && view.teamId
        ? teamIssuesPath(
            data.workspace.urlKey,
            data.teams.find((team) => team.id === view.teamId)?.key ??
              data.teams[0].key,
            "all",
          )
        : workspaceIssuesPath(data.workspace.urlKey, "all"),
      { replace: true },
    );
  };
  const removeProjectSavedView = async (view: SavedView) => {
    await run(() => deleteSavedView(view.id), "Could not delete view");
    setData((current) =>
      current
        ? {
            ...current,
            savedViews: (current.savedViews ?? []).filter(
              (item) => item.id !== view.id,
            ),
          }
        : current,
    );
    if (!data) return;
    const team = data.teams.find((item) => item.id === view.teamId);
    navigateTo(
      view.scope === "team" && team
        ? teamProjectsPath(data.workspace.urlKey, team.key)
        : projectsPath(data.workspace.urlKey),
      { replace: true },
    );
  };
  const renderIssuePreview = (issue: Issue, onClose: () => void) => {
    const refresh = async () => {
      const next = await fetchBootstrap(data!.workspace.urlKey);
      acceptBootstrap(next);
    };
    return (
      <IssueDetails
        key={issue.id}
        issue={issue}
        data={data!}
        workspacePresence={realtime.presence}
        onClose={onClose}
        onNavigateIssue={openIssue}
        onUpdate={async (input) => {
          await updateIssueById(issue, input);
        }}
        onDelete={async () => {
          await run(() => deleteIssue(issue.id), "Could not delete issue");
          setData((current) =>
            current
              ? {
                  ...current,
                  issues: current.issues.filter((item) => item.id !== issue.id),
                }
              : current,
          );
          onClose();
        }}
        onCreateSubIssue={async (input) => {
          const { attachments, ...fields } = input;
          const child = await run(
            () =>
              createIssue({
                ...fields,
                teamId: issue.team.id,
                parentId: issue.id,
              }),
            "Could not create sub-issue",
          );
          const uploaded = await Promise.all(
            attachments.map((file) => uploadAttachment(child.id, file)),
          );
          setData((current) =>
            current
              ? {
                  ...current,
                  issues: [
                    { ...child, attachments: uploaded },
                    ...current.issues.map((item) =>
                      item.id === issue.id
                        ? {
                            ...item,
                            subIssueIds: [...item.subIssueIds, child.id],
                          }
                        : item,
                    ),
                  ],
                  activities: { ...current.activities, [child.id]: [] },
                }
              : current,
          );
        }}
        onCreateProject={addIssueProject}
        onCreateProjectMilestone={addProjectMilestone}
        onReactIssue={async (emoji) => {
          const updated = await run(
            () => toggleIssueReaction(issue.id, emoji),
            "Could not update reaction",
          );
          replaceIssue(updated);
        }}
        onComment={async (body, bodyData, parentId) => {
          await createCommentOptimistically(issue, body, bodyData, parentId);
        }}
        onEditComment={async (id, body, bodyData) => {
          await editCommentOptimistically(issue, id, body, bodyData);
        }}
        onDeleteComment={async (id) => {
          await deleteCommentOptimistically(issue, id);
        }}
        onReactComment={async (id, emoji) => {
          await reactToCommentOptimistically(issue, id, emoji);
        }}
        onRelation={async (type, relatedIssueId) => {
          await run(
            () => createRelation(issue.id, type, relatedIssueId),
            "Could not add relation",
          );
          await refresh();
        }}
        onDeleteRelation={async (relationId) => {
          await run(
            () => deleteRelation(issue.id, relationId),
            "Could not remove relation",
          );
          await refresh();
        }}
        onUpload={async (file) => {
          await run(
            () => uploadAttachment(issue.id, file),
            "Could not upload attachment",
          );
          await refresh();
        }}
        onDeleteAttachment={async (id) => {
          await run(
            () => deleteAttachment(issue.id, id),
            "Could not remove attachment",
          );
          await refresh();
        }}
      />
    );
  };
  const addWorkspace = async (input: {
    name: string;
    urlKey: string;
    region: string;
  }) => {
    const created = await run(
      () => createWorkspace(input),
      "Could not create workspace",
    );
    const nextAccount = await fetchAccountBootstrap();
    setAccount(nextAccount);
    setData(created);
    navigateTo(`/${encodeURIComponent(created.workspace.urlKey)}`, {
      replace: true,
    });
    return created;
  };
  const switchWorkspace = (workspace: Workspace) => {
    if (workspace.id === data?.workspace.id) return;
    void setLastWorkspace(workspace.urlKey).catch(() =>
      toast.error("Could not remember workspace"),
    );
    setData(null);
    navigateTo(`/${encodeURIComponent(workspace.urlKey)}`);
  };
  const changeWorkspace = async (input: WorkspaceMutationInput) => {
    if (!data) return;
    const previousKey = data.workspace.urlKey;
    const updated = await run(
      () => updateWorkspace(previousKey, input),
      "Could not update workspace",
    );
    setData(updated);
    setAccount(await fetchAccountBootstrap());
    if (updated.workspace.urlKey !== previousKey)
      navigateTo(`/${encodeURIComponent(updated.workspace.urlKey)}`, {
        replace: true,
      });
  };
  const removeWorkspace = async () => {
    if (
      !data ||
      !(await confirmAction(`Delete ${data.workspace.name}?`, {
        description: "This permanently deletes all workspace data.",
        confirmLabel: "Delete workspace",
      }))
    )
      return;
    await run(
      () => deleteWorkspace(data.workspace.urlKey),
      "Could not delete workspace",
    );
    const next = await fetchAccountBootstrap();
    setAccount(next);
    setData(null);
    const workspace =
      next.workspaces.find(
        (item) => item.workspace.urlKey === next.lastWorkspaceKey,
      )?.workspace ?? next.workspaces[0]?.workspace;
    navigateTo(
      workspace ? myIssuesPath(workspace.urlKey) : workspaceOnboardingPath(),
      { replace: true },
    );
  };
  const addTeam = async (input: {
    name: string;
    key: string;
    color?: string;
    icon?: string;
    private?: boolean;
    parentTeamId?: string;
    copyFromTeamId?: string;
    timezone?: string;
  }) => {
    if (!data) return;
    await run(
      () => createTeam(data.workspace.urlKey, input),
      "Could not create team",
    );
    acceptBootstrap(await fetchBootstrap(data.workspace.urlKey));
  };
  const addCustomer = async (
    input: CustomerMutationInput & { name: string },
  ) => {
    const customer = await run(
      () => createCustomer(input),
      "Could not create customer",
    );
    setData((current) =>
      current
        ? { ...current, customers: [...(current.customers ?? []), customer] }
        : current,
    );
  };
  const changeCustomer = async (
    customer: Customer,
    input: CustomerMutationInput,
  ) => {
    const updated = await run(
      () => updateCustomer(customer.id, input),
      "Could not update customer",
    );
    setData((current) =>
      current
        ? {
            ...current,
            customers: (current.customers ?? []).map((item) =>
              item.id === customer.id ? updated : item,
            ),
          }
        : current,
    );
  };
  const removeCustomer = async (customer: Customer) => {
    if (
      !(await confirmAction(`Delete ${customer.name}?`, {
        confirmLabel: "Delete customer",
      }))
    )
      return;
    await run(() => deleteCustomer(customer.id), "Could not delete customer");
    setData((current) =>
      current
        ? {
            ...current,
            customers: (current.customers ?? []).filter(
              (item) => item.id !== customer.id,
            ),
          }
        : current,
    );
  };
  useEffect(() => {
    if (!data) return;
    const workspace = data.workspace.urlKey;
    if (route.kind === "root" || route.kind === "workspace-root") {
      const preference = data.userSettings[data.viewer.id]?.homeView;
      const homeView = preference && !preference.includes('(default)') ? preference : data.workspaceSettings.defaultHomeView ?? 'agent';
      navigateTo(
        homeView === "Inbox" || homeView === 'inbox'
          ? inboxPath(workspace)
          : homeView === "My issues" || homeView === 'my-issues'
            ? myIssuesPath(workspace)
            : homeView === 'projects' || homeView === 'Projects' ? projectsPath(workspace) : agentPath(workspace),
        { replace: true },
      );
      return;
    }
    const canonicalize = (to: string, options?: { replace?: boolean; state?: unknown }) => {
      if (!to.includes("?") && !to.includes("#")) navigateTo(to + location.search + location.hash, { ...options, state: location.state });
      else navigateTo(to, options);
    };
    if (!routeBelongsToWorkspace(route, workspace)) return;
    if (
      route.kind === "dashboards" &&
      data.workspaceSettings.featureFlags.dashboards === false
    ) {
      canonicalize(workspaceViewsPath(workspace, "issues"), { replace: true });
      return;
    }
    if (route.kind === "inbox" && location.pathname !== inboxPath(workspace, route.tab))
      canonicalize(inboxPath(workspace, route.tab), { replace: true });
    if (route.kind === "search" && location.pathname !== searchPath(workspace))
      canonicalize(searchPath(workspace), { replace: true });
    if (
      route.kind === "pulse" &&
      location.pathname !==
        (route.viewId
          ? pulseViewPath(workspace, route.viewId)
          : pulsePath(workspace, route.view))
    )
      canonicalize(
        route.viewId
          ? pulseViewPath(workspace, route.viewId)
          : pulsePath(workspace, route.view),
        { replace: true },
      );
    if (
      route.kind === "my-issues" &&
      location.pathname !== myIssuesPath(workspace, route.view)
    )
      canonicalize(myIssuesPath(workspace, route.view), { replace: true });
    if (
      route.kind === "reviews" &&
      location.pathname !== reviewsPath(workspace, route.view)
    )
      canonicalize(reviewsPath(workspace, route.view), { replace: true });
    if (route.kind === "review" && selectedReview) {
      const canonical = reviewPath(workspace, selectedReview, route.tab);
      if (location.pathname !== canonical)
        canonicalize(canonical, { replace: true });
    }
    if (
      route.kind === "workspace-issues" &&
      location.pathname !== workspaceIssuesPath(workspace, route.view)
    )
      canonicalize(workspaceIssuesPath(workspace, route.view), { replace: true });
    if (
      route.kind === "workspace-members" &&
      location.pathname !== membersPath(workspace)
    )
      canonicalize(membersPath(workspace), { replace: true });
    if (route.kind === "member-profile") {
      const user = data.users.find(
        (item) => item.name === route.username || item.id === route.username,
      );
      if (user) {
        const canonical = memberProfilePath(workspace, user.name, route.view);
        if (location.pathname !== canonical)
          canonicalize(canonical, { replace: true });
      }
    }
    if (
      route.kind === "workspace-customers" &&
      location.pathname !== customersPath(workspace)
    )
      canonicalize(customersPath(workspace), { replace: true });
    if (
      route.kind === "workspace-teams" &&
      location.pathname !== teamsPath(workspace)
    )
      canonicalize(teamsPath(workspace), { replace: true });
    if (
      route.kind === "new-team" &&
      location.pathname !== newTeamPath(workspace)
    )
      canonicalize(newTeamPath(workspace), { replace: true });
    if (route.kind === "team-issues") {
      const canonical = teamIssuesPath(workspace, route.teamKey, route.view);
      if (location.pathname !== canonical)
        canonicalize(canonical, { replace: true });
    }
    if (
      route.kind === "team-cycles" &&
      location.pathname !== teamCyclesPath(workspace, route.teamKey)
    )
      canonicalize(teamCyclesPath(workspace, route.teamKey), {
        replace: true,
      });
    if (
      route.kind === "cycle-upcoming" &&
      location.pathname !== upcomingCyclePath(workspace, route.teamKey)
    )
      canonicalize(upcomingCyclePath(workspace, route.teamKey), {
        replace: true,
      });
    if (
      route.kind === "workspace-saved-view" &&
      selectedSavedView &&
      location.pathname !==
        (route.editing
          ? workspaceSavedViewEditPath(
              workspace,
              savedViewPathId(selectedSavedView),
            )
          : workspaceSavedViewPath(
              workspace,
              savedViewPathId(selectedSavedView),
            ))
    )
      canonicalize(
        route.editing
          ? workspaceSavedViewEditPath(
              workspace,
              savedViewPathId(selectedSavedView),
            )
          : workspaceSavedViewPath(
              workspace,
              savedViewPathId(selectedSavedView),
            ),
        { replace: true },
      );
    if (
      route.kind === "team-saved-view" &&
      selectedSavedView &&
      location.pathname !==
        (route.editing
          ? teamSavedViewEditPath(
              workspace,
              route.teamKey,
              savedViewPathId(selectedSavedView),
            )
          : teamSavedViewPath(
              workspace,
              route.teamKey,
              savedViewPathId(selectedSavedView),
            ))
    )
      canonicalize(
        route.editing
          ? teamSavedViewEditPath(
              workspace,
              route.teamKey,
              savedViewPathId(selectedSavedView),
            )
          : teamSavedViewPath(
              workspace,
              route.teamKey,
              savedViewPathId(selectedSavedView),
            ),
        { replace: true },
      );
    if (
      route.kind === "workspace-views" &&
      location.pathname !== workspaceViewsPath(workspace, route.resource)
    )
      canonicalize(workspaceViewsPath(workspace, route.resource), {
        replace: true,
      });
    if (
      route.kind === "workspace-views-new" &&
      location.pathname !== workspaceViewsNewPath(workspace, route.resource)
    )
      canonicalize(workspaceViewsNewPath(workspace, route.resource), {
        replace: true,
      });
    if (
      route.kind === "team-views" &&
      location.pathname !==
        teamViewsPath(workspace, route.teamKey, route.resource)
    )
      canonicalize(teamViewsPath(workspace, route.teamKey, route.resource), {
        replace: true,
      });
    if (
      route.kind === "team-views-new" &&
      location.pathname !==
        teamViewsNewPath(workspace, route.teamKey, route.resource)
    )
      canonicalize(teamViewsNewPath(workspace, route.teamKey, route.resource), {
        replace: true,
      });
    if (
      route.kind === "projects" &&
      location.pathname !== projectsPath(workspace)
    )
      canonicalize(projectsPath(workspace), { replace: true });
    if (
      route.kind === "initiatives" &&
      location.pathname !== initiativesPath(workspace, route.view)
    )
      canonicalize(initiativesPath(workspace, route.view), { replace: true });
    if (
      route.kind === "team-initiatives" &&
      location.pathname !==
        teamInitiativesPath(workspace, route.teamKey, route.view)
    )
      canonicalize(teamInitiativesPath(workspace, route.teamKey, route.view), {
        replace: true,
      });
    if (
      route.kind === "team-projects" &&
      location.pathname !== teamProjectsPath(workspace, route.teamKey)
    )
      canonicalize(teamProjectsPath(workspace, route.teamKey), { replace: true });
    if (
      route.kind === "projects-saved-view" &&
      selectedProjectSavedView &&
      location.pathname !==
        (route.editing
          ? projectsSavedViewEditPath(
              workspace,
              savedViewPathId(selectedProjectSavedView),
            )
          : projectsSavedViewPath(
              workspace,
              savedViewPathId(selectedProjectSavedView),
            ))
    )
      canonicalize(
        route.editing
          ? projectsSavedViewEditPath(
              workspace,
              savedViewPathId(selectedProjectSavedView),
            )
          : projectsSavedViewPath(
              workspace,
              savedViewPathId(selectedProjectSavedView),
            ),
        { replace: true },
      );
    if (
      route.kind === "team-projects-saved-view" &&
      selectedProjectSavedView &&
      location.pathname !==
        (route.editing
          ? teamProjectsSavedViewEditPath(
              workspace,
              route.teamKey,
              savedViewPathId(selectedProjectSavedView),
            )
          : teamProjectsSavedViewPath(
              workspace,
              route.teamKey,
              savedViewPathId(selectedProjectSavedView),
            ))
    )
      canonicalize(
        route.editing
          ? teamProjectsSavedViewEditPath(
              workspace,
              route.teamKey,
              savedViewPathId(selectedProjectSavedView),
            )
          : teamProjectsSavedViewPath(
              workspace,
              route.teamKey,
              savedViewPathId(selectedProjectSavedView),
            ),
        { replace: true },
      );
    if (route.kind === "issue" && selectedIssue) {
      const canonical = issuePath(workspace, selectedIssue);
      if (location.pathname !== canonical)
        canonicalize(`${canonical}${location.search}${location.hash}`, { replace: true, state: location.state });
    }
    if (route.kind === "cycle" && route.cycleId !== "active" && selectedCycle) {
      const canonical = cyclePath(workspace, route.teamKey, selectedCycle);
      if (location.pathname !== canonical)
        canonicalize(canonical, { replace: true });
    }
    if (route.kind === "project" && selectedProject) {
      const canonical = projectPath(workspace, selectedProject, route.tab);
      if (location.pathname !== canonical)
        canonicalize(canonical, { replace: true });
    }
    if (
      route.kind === "project-saved-view" &&
      selectedProject &&
      selectedProjectFacetView
    ) {
      const canonical = route.editing
        ? projectSavedViewEditPath(
            workspace,
            selectedProject.slugId,
            savedViewPathId(selectedProjectFacetView),
          )
        : projectSavedViewPath(
            workspace,
            selectedProject.slugId,
            savedViewPathId(selectedProjectFacetView),
          );
      if (location.pathname !== canonical)
        canonicalize(canonical, { replace: true });
    }
    if (route.kind === "document" && selectedDocument) {
      const canonical = documentPath(workspace, selectedDocument);
      if (location.pathname !== canonical)
        canonicalize(canonical, { replace: true });
    }
    if (route.kind === "customer" && selectedCustomer) {
      const canonical = customerPath(workspace, selectedCustomer);
      if (location.pathname !== canonical)
        canonicalize(canonical, { replace: true });
    }
    if (route.kind === "initiative" && selectedInitiative) {
      const canonical = initiativePath(
        workspace,
        selectedInitiative,
        route.tab,
        route.viewId,
      );
      if (location.pathname !== canonical)
        canonicalize(canonical, { replace: true });
    }
  }, [
    data,
    location.pathname,
    navigateTo,
    route,
    selectedCycle,
    selectedInitiative,
    selectedIssue,
    selectedProject,
    selectedDocument,
    selectedCustomer,
    selectedProjectSavedView,
    selectedProjectFacetView,
    selectedSavedView,
    selectedReview,
  ]);
  if (authenticationPolicy && !authPath) return <AuthenticationPolicyPage code={authenticationPolicy}/>;
  if (!authReady)
    return (
      <div className="auth-page">
        <div className="auth-brand">
          <span className="auth-brand-mark" />
          Flow
        </div>
      </div>
    );
  if (!session || authPath)
    return (
      <AuthPage
        session={session}
        onAuthenticated={async (authenticated, returnTo) => {
          setSession(authenticated);
          const nextAccount = await fetchAccountBootstrap();
          setAccount(nextAccount);
          setError(false);
          navigateTo(returnTo || "/", { replace: true });
        }}
        onInvitationAccepted={async (workspaceKey) => {
          setAccount(await fetchAccountBootstrap());
          setData(null);
          navigateTo(myIssuesPath(workspaceKey), { replace: true });
        }}
      />
    );
  if (!account)
    return (
      <div className="app loading-app">
        <aside className="sidebar" />
        <main className="main-panel">
          {error ? (
            <ErrorState retry={loadAccount} />
          ) : (
            <SkeletonRows count={9} />
          )}
        </main>
      </div>
    );
  if (oauthPath) return <OAuthAuthorizePage account={account} />;
  if (route.kind === "workspace-onboarding" || account.workspaces.length === 0)
    return (
      <Suspense
        fallback={
          <main className="main-panel">
            <SkeletonRows count={7} />
          </main>
        }
      >
        <WorkspaceOnboarding
          account={account}
          onCreate={addWorkspace}
          onBack={() => {
            const workspace =
              account.workspaces.find(
                (item) => item.workspace.urlKey === account.lastWorkspaceKey,
              )?.workspace ?? account.workspaces[0]?.workspace;
            navigateTo(
              workspace ? `/${encodeURIComponent(workspace.urlKey)}` : "/",
            );
          }}
          onLogout={async () => {
            await logoutAccount();
            setSession(null);
            setAccount(null);
            setData(null);
            navigateTo("/login", { replace: true });
          }}
        />
      </Suspense>
    );
  if (!data)
    return (
      <div className="app loading-app">
        <aside className="sidebar" />
        <main className="main-panel">
          {previewIssue && !previewIssue.isSummary ? <Suspense fallback={<SkeletonRows count={9}/>}><IssueLoadingPreview issue={previewIssue} onBack={()=>navigateTo(workspaceIssuesPath(detailWorkspaceKey,'all'))}/></Suspense> : error ? <ErrorState retry={load} /> : <SkeletonRows count={9} />}
        </main>
      </div>
    );
  if (route.kind === "settings")
    return (
      <PeopleProvider users={data.users} workspaceName={data.workspace.name} members={data.members} teams={data.teams} teamMembers={data.teamMembers} projects={data.projects}>
      <Suspense
        fallback={
          <div className="app loading-app">
            <aside className="sidebar" />
            <main className="main-panel">
              <SkeletonRows count={9} />
            </main>
          </div>
        }
      >
        <SettingsPage
          data={data}
          page={route.page}
          notificationChannel={route.notificationChannel}
          onNavigateNotification={channel => navigateTo(`${settingsPath(data.workspace.urlKey,'notifications')}${channel ? `/${channel}` : ''}`)}
          apiKeyMode={route.apiKeyMode}
          apiKeyId={route.apiKeyId}
          signingKeyMode={route.signingKeyMode}
          teamKey={route.teamKey}
          teamSection={route.teamSection}
          releasePipelineMode={route.releasePipelineMode}
          releasePipelineSlug={route.releasePipelineSlug}
          integrationProvider={route.integrationProvider}
          issueTemplateMode={route.issueTemplateMode}
          issueTemplateId={route.issueTemplateId}
          projectTemplateMode={route.projectTemplateMode}
          projectTemplateId={route.projectTemplateId}
          agentSkillMode={route.agentSkillMode}
          agentSkillId={route.agentSkillId}
          onBack={() =>
            navigateTo(
              navigationReturnPath(location.state, data.workspace.urlKey, route.releasePipelineMode
                ? releasePipelinesPath(data.workspace.urlKey)
                : route.agentSkillMode
                  ? agentPath(data.workspace.urlKey)
                  : myIssuesPath(data.workspace.urlKey)),
            )
          }
          onNavigate={(page, teamKey, teamSection) =>
            navigateTo(
              settingsPath(data.workspace.urlKey, page, teamKey, teamSection),
            )
          }
          onCreateAPIKey={() =>
            navigateTo(newAPIKeyPath(data.workspace.urlKey))
          }
          onCreateSigningKey={() =>
            navigateTo(newSigningKeyPath(data.workspace.urlKey))
          }
          onOpenAPIKey={(key) =>
            navigateTo(apiKeyPath(data.workspace.urlKey, key.id))
          }
          onEditAPIKey={(key) =>
            navigateTo(apiKeyEditPath(data.workspace.urlKey, key.id))
          }
          onLogout={async () => {
            await logoutAccount();
            setSession(null);
            setAccount(null);
            setData(null);
            navigateTo("/login", { replace: true });
          }}
          onNavigateAgent={() => navigateTo(agentPath(data.workspace.urlKey))}
          onOpenAgentHistory={() =>
            navigateTo(`${agentPath(data.workspace.urlKey)}?history=1`)
          }
          onCreateReleasePipeline={() =>
            navigateTo(newReleasePipelinePath(data.workspace.urlKey))
          }
          onOpenReleasePipeline={(pipeline) =>
            navigateTo(
              releasePipelineSettingsPath(
                data.workspace.urlKey,
                pipeline.slugId || pipeline.id,
              ),
            )
          }
          onOpenIntegration={(provider) =>
            navigateTo(integrationSettingsPath(data.workspace.urlKey, provider))
          }
          onCreateIssueTemplate={(form) =>
            navigateTo(newIssueTemplatePath(data.workspace.urlKey, form))
          }
          onOpenIssueTemplate={(template) =>
            navigateTo(
              issueTemplateEditPath(data.workspace.urlKey, template.id),
            )
          }
          onDuplicateIssueTemplate={(template) =>
            navigateTo(
              `${newIssueTemplatePath(data.workspace.urlKey, template.templateType === "customForm")}?duplicate=${encodeURIComponent(template.id)}`,
            )
          }
          onCreateProjectTemplate={() =>
            navigateTo(newProjectTemplatePath(data.workspace.urlKey))
          }
          onOpenProjectTemplate={(template) =>
            navigateTo(
              projectTemplateEditPath(data.workspace.urlKey, template.id),
            )
          }
          onDuplicateProjectTemplate={(template) =>
            navigateTo(
              `${newProjectTemplatePath(data.workspace.urlKey)}?duplicate=${encodeURIComponent(template.id)}`,
            )
          }
          onCreateTeam={() => navigateTo(newTeamPath(data.workspace.urlKey))}
          onWorkspaceUpdate={changeWorkspace}
          onWorkspaceDelete={removeWorkspace}
          onSettingsUpdate={updateWorkspaceSettings}
          onReload={async () => {
            acceptBootstrap(await fetchBootstrap(data.workspace.urlKey));
          }}
        />
      </Suspense></PeopleProvider>
    );
  const workspaceValid = routeBelongsToWorkspace(route, data.workspace.urlKey);
  const routeTeamKey = "teamKey" in route ? route.teamKey : undefined;
  const teamValid =
    !routeTeamKey ||
    data.teams.some(
      (team) => team.key.toLowerCase() === routeTeamKey.toLowerCase(),
    );
  const routeScopeValid = workspaceValid && teamValid;
  const page = routeScopeValid ? pageForRoute(route) : "not-found";
  const toolbarAgentSession = data.agentSessions?.find(
    (item) =>
      item.location === "toolbar" && !closedAgentSessionIds.has(item.id),
  );
  const toolbarAgentIssues = toolbarAgentSession
    ? toolbarAgentSession.issueIds
        .map((id) => data.issues.find((issue) => issue.id === id))
        .filter((issue): issue is Issue => Boolean(issue))
        .map((issue) =>
          issueToExplorerRow(issue, data.workspace.urlKey, data.issues, data),
        )
    : [];
  const rememberResult = (type: SearchResourceType, id: string) => {
    void recordRecentResource(type, id).catch(() => undefined);
  };
  const openIssue = (issue: Issue, sequence?: string[]) => {
    if (data.issueCollectionPaged) setData(current => current ? { ...current, issues: [issue, ...current.issues.filter(item => item.id !== issue.id)].slice(0, 2000) } : current);
    rememberResult("issue", issue.id);
    navigateTo(issuePath(data.workspace.urlKey, issue), {
      state: sequence ? { issueSequence: sequence.slice(0, 1000) } : undefined,
    });
  };
  const openProject = (project: Project) => {
    rememberResult("project", project.id);
    navigateTo(projectPath(data.workspace.urlKey, project));
  };
  const openInitiative = (
    initiative: Initiative,
    tab: import("@/lib/app-routes").InitiativeRouteTab = "overview",
  ) => {
    rememberResult("initiative", initiative.id);
    navigateTo(initiativePath(data.workspace.urlKey, initiative, tab));
  };
  const openSearchResult = (result: SearchResult) => {
    rememberResult(result.type, result.id);
    navigateTo(searchResultLink(data,result),{state:{returnTo:location.pathname+location.search,originPath:location.pathname+location.search}});
  };
  const cycleTeam =
    "teamKey" in route &&
    (route.kind === "team-cycles" ||
      route.kind === "cycle" ||
      route.kind === "cycle-upcoming")
      ? data.teams.find(
          (team) => team.key.toLowerCase() === route.teamKey.toLowerCase(),
        )
      : undefined;
  const openCycle = (cycle: Cycle) =>
    cycleTeam &&
    navigateTo(cyclePath(data.workspace.urlKey, cycleTeam.key, cycle));
  const projectTeamKey =
    "teamKey" in route &&
    [
      "team-projects",
      "team-projects-new-view",
      "team-projects-saved-view",
    ].includes(route.kind)
      ? route.teamKey
      : undefined;
  const projectTeam = projectTeamKey
    ? data.teams.find(
        (team) => team.key.toLowerCase() === projectTeamKey.toLowerCase(),
      )
    : undefined;
  const scopedProjectSavedViews = projectSavedViews.filter((view) =>
    projectTeam
      ? view.scope === "team" && view.teamId === projectTeam.id
      : view.scope !== "team",
  );
  const isProjectsSavedRoute =
    route.kind === "projects-saved-view" ||
    route.kind === "team-projects-saved-view";
  const isViewsRoute =
    route.kind === "workspace-views" ||
    route.kind === "workspace-views-new" ||
    route.kind === "team-views" ||
    route.kind === "team-views-new";
  const viewsTeam =
    isViewsRoute && "teamKey" in route
      ? data.teams.find(
          (team) => team.key.toLowerCase() === route.teamKey.toLowerCase(),
        )
      : undefined;
  const viewsScope =
    isViewsRoute && viewsTeam
      ? { kind: "team" as const, team: viewsTeam }
      : { kind: "workspace" as const };
  const viewsResource = isViewsRoute ? route.resource : "issues";
  const directoryViews = availableSavedViews.filter(
    (view) =>
      (view.resource ?? "issues") === viewsResource &&
      !view.projectId &&
      (viewsTeam
        ? view.scope === "team" && view.teamId === viewsTeam.id
        : view.scope === "personal" || view.scope === "workspace"),
  );
  const duplicateSavedView = availableSavedViews.find(
    (view) => view.id === new URLSearchParams(location.search).get("duplicate"),
  );
  const savedIssuePathFor = (view: SavedView) => {
    const project = view.projectId
      ? data.projects.find((item) => item.id === view.projectId)
      : undefined;
    return project
      ? projectSavedViewPath(
          data.workspace.urlKey,
          project.slugId,
          savedViewPathId(view),
        )
      : workspaceSavedViewPath(data.workspace.urlKey, savedViewPathId(view));
  };
  const savedProjectPathFor = (view: SavedView) => {
    return projectsSavedViewPath(data.workspace.urlKey, savedViewPathId(view));
  };
  const savedIssueEditPathFor = (view: SavedView) => {
    const project = view.projectId
      ? data.projects.find((item) => item.id === view.projectId)
      : undefined;
    return project
      ? projectSavedViewEditPath(
          data.workspace.urlKey,
          project.slugId,
          savedViewPathId(view),
        )
      : workspaceSavedViewEditPath(
          data.workspace.urlKey,
          savedViewPathId(view),
        );
  };
  const savedProjectEditPathFor = (view: SavedView) => {
    return projectsSavedViewEditPath(
      data.workspace.urlKey,
      savedViewPathId(view),
    );
  };
  const selectedSavedViewTeam =
    selectedSavedView?.scope === "team" && selectedSavedView.teamId
      ? data.teams.find((team) => team.id === selectedSavedView.teamId)
      : undefined;
  const selectedSavedViewScope = selectedSavedViewTeam
    ? { kind: "team" as const, team: selectedSavedViewTeam }
    : { kind: "workspace" as const };
  const selectedSavedViewList = selectedSavedViewTeam
    ? issueSavedViews.filter(
        (view) =>
          view.scope === "team" && view.teamId === selectedSavedViewTeam.id,
      )
    : issueSavedViews.filter((view) => view.scope !== "team");
  return (
    <PeopleProvider users={data.users} workspaceName={data.workspace.name} members={data.members} teams={data.teams} teamMembers={data.teamMembers} projects={data.projects}><div className="app">
      <Sidebar
        account={account}
        data={data}
        page={page}
        sourcePath={sidebarOriginPath(location.state, location.pathname, data.workspace.urlKey) ?? (page === 'issue-detail' && selectedIssue ? issueReturnPath(location.state, data.workspace.urlKey, selectedIssue) : undefined)}
        open={mobileSidebarOpen}
        onOpenChange={setMobileSidebarOpen}
        onSearch={() => navigateTo(searchPath(data.workspace.urlKey))}
        onCreate={() => openCreateIssue()}
        onOpenSettings={(page = "workspace") =>
          navigateTo(
            settingsPath(
              data.workspace.urlKey,
              data.viewerRole === "admin" ? page : "preferences",
            ),
          )
        }
        onSwitchWorkspace={switchWorkspace}
        onCreateWorkspace={() => navigateTo(workspaceOnboardingPath())}
        onReload={async () =>
          acceptBootstrap(await fetchBootstrap(data.workspace.urlKey))
        }
        onLogout={async () => {
          await logoutAccount();
          setSession(null);
          setAccount(null);
          setData(null);
          navigateTo("/login", { replace: true });
        }}
      />
      <Suspense
        fallback={
          <main className="main-panel">
            <SkeletonRows />
          </main>
        }
      >
        {page === "search" && route.kind === "search" && (
          <WorkspaceSearchPage
            onOpenSidebar={() => setMobileSidebarOpen(true)}
            onOpenResult={openSearchResult}
            getResultHref={result=>searchResultLink(data,result)}
            users={data.users}
          />
        )}
        {page === "documents" && route.kind === "documents" && (
          <DocumentsIndexPage
            data={data}
            search={location.search}
            onFiltersChange={search => navigateTo(`${documentsPath(data.workspace.urlKey)}${search ? `?${search}` : ''}`, { replace: true })}
            onNavigate={navigateTo}
            onReload={async () =>
              acceptBootstrap(await fetchBootstrap(data.workspace.urlKey))
            }
          />
        )}
        {page === "team-overview" &&
          (route.kind === "team-overview" ||
            route.kind === "team-documents" ||
            route.kind === "team-loops" ||
            route.kind === "team-members") && (
            <TeamOverviewPage
              data={data}
              team={data.teams.find(
                (team) =>
                  team.key.toLowerCase() === route.teamKey.toLowerCase(),
              )!}
              view={
                route.kind === "team-documents"
                  ? "documents"
                  : route.kind === "team-loops"
                    ? "loops"
                    : route.kind === "team-members"
                      ? "members"
                      : "overview"
              }
              onNavigate={navigateTo}
              onOpenSidebar={() => setMobileSidebarOpen(true)}
              onReload={async () =>
                acceptBootstrap(await fetchBootstrap(data.workspace.urlKey))
              }
            />
          )}
        {(
          route.kind === "diary" ||
          route.kind === "meeting" ||
          route.kind === "automations" ||
          route.kind === "automation-new" ||
          route.kind === "automation-detail" ||
          route.kind === "automation-runs" ||
          route.kind === "team-board" ||
          route.kind === "team-triage" ||
          route.kind === "team-updates" ||
          route.kind === "team-update" ||
          route.kind === "team-resources" ||
          route.kind === "team-links" ||
          route.kind === "release-note" ||
          route.kind === "label"
        ) && (
          <WorkspaceSecondaryPage
            data={data}
            kind={
              route.kind === "automation-detail"
                ? "automation-detail"
                : route.kind === "automation-runs"
                  ? "automation-runs"
                  : route.kind
            }
            team={
              "teamKey" in route
                ? data.teams.find(
                    (team) =>
                      team.key.toLowerCase() === route.teamKey.toLowerCase(),
                  )
                : undefined
            }
            workflowId={
              route.kind === "automation-detail" ||
              route.kind === "automation-runs"
                ? route.automationId
                : undefined
            }
            workflowRunId={
              route.kind === "automation-runs" ? route.runId : undefined
            }
            editing={route.kind === "automation-detail" ? route.editing : undefined}
            resourceName={route.kind === "label" ? route.resourceName : undefined}
            resourceType={route.kind === "label" ? route.resourceType : undefined}
            releaseNote={
              route.kind === "release-note"
                ? data.releaseNotes.find(
                    (item) => item.id === route.releaseNoteId,
                  )
                : undefined
            }
            onNavigate={navigateTo}
            onReload={async () => {
              acceptBootstrap(await fetchBootstrap(data.workspace.urlKey));
            }}
          />
        )}
        {page === "analytics" && route.kind === "analytics" && (
          <AnalyticsDashboardPage />
        )}
        {page === "analytics" && route.kind === "dashboards" && (
          <main className="main-panel">
            <DashboardsPage
              dashboardId={route.dashboardId}
              creating={route.creating}
              widgetId={route.widgetId}
              data={data}
              dashboardsHref={
                route.teamKey
                  ? teamDashboardsPath(data.workspace.urlKey, route.teamKey)
                  : dashboardsPath(data.workspace.urlKey)
              }
              onNavigate={(dashboardId) =>
                navigateTo(
                  dashboardId
                    ? dashboardsPath(data.workspace.urlKey, dashboardId)
                    : route.teamKey
                      ? teamDashboardsPath(data.workspace.urlKey, route.teamKey)
                      : dashboardsPath(data.workspace.urlKey),
                )
              }
              onOpenResource={(resource) =>
                navigateTo(
                  route.teamKey
                    ? teamViewsPath(
                        data.workspace.urlKey,
                        route.teamKey,
                        resource,
                      )
                    : workspaceViewsPath(data.workspace.urlKey, resource),
                )
              }
              onOpenSidebar={() => setMobileSidebarOpen(true)}
              onOpenCreate={() =>
                navigateTo(
                  route.teamKey
                    ? teamDashboardsNewPath(
                        data.workspace.urlKey,
                        route.teamKey,
                      )
                    : dashboardsNewPath(data.workspace.urlKey),
                )
              }
              onExploreIssues={(filters) =>
                navigateTo(
                  `${workspaceIssuesPath(data.workspace.urlKey, "all")}?insightFilter=${encodeURIComponent(JSON.stringify(filters))}`,
                )
              }
              onOpenWidget={(dashboardId, widgetId) =>
                navigateTo(
                  dashboardWidgetPath(
                    data.workspace.urlKey,
                    dashboardId,
                    widgetId,
                  ),
                )
              }
              resourceHref={(resource) =>
                route.teamKey
                  ? teamViewsPath(
                      data.workspace.urlKey,
                      route.teamKey,
                      resource,
                    )
                  : workspaceViewsPath(data.workspace.urlKey, resource)
              }
              teamKey={route.teamKey}
            />
          </main>
        )}
        {page === "agent" && route.kind === "agent" && (
          <AgentPage
            chatSlug={route.chatSlug}
            data={data}
            onNavigate={navigateTo}
            onOpenSidebar={() => setMobileSidebarOpen(true)}
            onReload={async () =>
              acceptBootstrap(await fetchBootstrap(data.workspace.urlKey))
            }
          />
        )}
        {page === "loops" &&
          (route.kind === "loops" || route.kind === "loop-editor") && (
            <LoopsPage
              data={data}
              draftId={route.kind === "loop-editor" ? route.draftId : undefined}
              loopId={route.kind === "loop-editor" ? route.loopId : undefined}
              editing={route.kind === "loop-editor"}
              onOpenSidebar={() => setMobileSidebarOpen(true)}
              onNavigate={path => navigateTo(route.kind === 'loop-editor' && path === loopsPath(data.workspace.urlKey) ? navigationReturnPath(location.state, data.workspace.urlKey, path) : path)}
              onReload={async () =>
                acceptBootstrap(await fetchBootstrap(data.workspace.urlKey))
              }
            />
          )}
        {page === "reviews" &&
          (route.kind === "reviews" || route.kind === "review") && (
            <ReviewsPage
              data={data}
              view={route.kind === "reviews" ? route.view : reviewsOriginView(location.state, data.workspace.urlKey)}
              returnPath={navigationReturnPath(location.state, data.workspace.urlKey, reviewsPath(data.workspace.urlKey))}
              review={
                route.kind === "review"
                  ? (selectedReview ?? undefined)
                  : undefined
              }
              tab={route.kind === "review" ? route.tab : undefined}
              onNavigate={navigateTo}
              onReload={async () =>
                acceptBootstrap(await fetchBootstrap(data.workspace.urlKey))
              }
              onOpenSidebar={() => setMobileSidebarOpen(true)}
            />
          )}
        {((page === "drafts" && route.kind === "drafts") ||
          (page === "asks" && route.kind === "asks") ||
          (page === "releases" &&
            (route.kind === "releases" ||
              route.kind === "release-pipeline" ||
              route.kind === "release"))) && (
          <WorkspaceOperationsPage
            data={data}
            view={
              route.kind === "release-pipeline" || route.kind === "release"
                ? "releases"
                : (route.kind as "drafts" | "releases" | "asks")
            }
            pipelineSlug={
              route.kind === "release-pipeline" || route.kind === "release"
                ? route.pipelineSlug
                : undefined
            }
            releaseSlug={
              route.kind === "release" ? route.releaseSlug : undefined
            }
            pipelineTab={
              route.kind === "release-pipeline" ? route.tab : undefined
            }
            releaseTab={route.kind === "release" ? route.tab : undefined}
            onOpenSidebar={() => setMobileSidebarOpen(true)}
            onReload={async () =>
              acceptBootstrap(await fetchBootstrap(data.workspace.urlKey))
            }
            onNavigate={(path) => navigateTo(path)}
            onResumeDraft={(draft: Draft) => {
              // Keep local drafts addressable so the dialog only restores a
              // draft when the user explicitly chooses it.
              setCreateDraftId(draft.id);
              setCreateTeamId(
                draft.id.startsWith("local:") &&
                  typeof draft.metadata?.teamId === "string"
                  ? draft.metadata.teamId
                  : undefined,
              );
              setCreateOpen(true);
            }}
          />
        )}
        {page === "team-archive" &&
          route.kind === "team-archive" &&
          data.teams.find(
            (team) => team.key.toLowerCase() === route.teamKey.toLowerCase(),
          ) && (
            <TeamArchivePage
              data={data}
              team={data.teams.find(
                (team) =>
                  team.key.toLowerCase() === route.teamKey.toLowerCase(),
              )!}
              tab={route.tab}
              onNavigate={navigateTo}
              onOpenSidebar={() => setMobileSidebarOpen(true)}
              onReload={async () =>
                acceptBootstrap(await fetchBootstrap(data.workspace.urlKey))
              }
            />
          )}
        {page === "document-detail" && selectedDocument && (
          <DocumentPage
            data={data}
            origin={navigationLabel(navigationReturnPath(location.state, data.workspace.urlKey, ''), data)}
            document={selectedDocument}
            onReload={async () =>
              acceptBootstrap(await fetchBootstrap(data.workspace.urlKey))
            }
            onBack={() => {
              const source = navigationReturnPath(location.state, data.workspace.urlKey, '');
              if (source) { navigateTo(source); return; }
              const issue = data.issues.find(
                (item) => item.id === selectedDocument.issueId,
              );
              const project = data.projects.find((item) =>
                selectedDocument.projectIds.includes(item.id),
              );
              const team = data.teams.find((item) =>
                selectedDocument.teamIds.includes(item.id),
              );
              navigateTo(
                issue
                  ? issuePath(data.workspace.urlKey, issue)
                  : project
                    ? projectPath(data.workspace.urlKey, project)
                    : team
                      ? teamDocumentsPath(data.workspace.urlKey, team.key)
                      : documentsPath(data.workspace.urlKey),
              );
            }}
          />
        )}
        {page === "customer-detail" && selectedCustomer && (
          <CustomerDetailPage
            data={data}
            customer={selectedCustomer}
            onBack={() => navigateTo(navigationReturnPath(location.state, data.workspace.urlKey, customersPath(data.workspace.urlKey)))}
            onReload={async () =>
              acceptBootstrap(await fetchBootstrap(data.workspace.urlKey))
            }
            onOpenResource={(type, id) => {
              if (type === "issue") {
                const issue = data.issues.find((item) => item.id === id);
                if (issue) navigateTo(issuePath(data.workspace.urlKey, issue));
              } else {
                const project = data.projects.find((item) => item.id === id);
                if (project)
                  navigateTo(projectPath(data.workspace.urlKey, project));
              }
            }}
          />
        )}
        {!workspaceValid && (
          <RouteNotFound
            title="Workspace not found"
            description={`This app is connected to ${data.workspace.name}.`}
          />
        )}
        {workspaceValid && !teamValid && (
          <RouteNotFound
            title="Team not found"
            description="This team does not exist in the current workspace."
          />
        )}
        {(route.kind === "workspace-members" ||
          page === "customers" ||
          page === "teams") && (
          <WorkspaceDirectoryPage
            kind={
              route.kind === "workspace-members"
                ? "members"
                : page === "customers"
                  ? "customers"
                  : "teams"
            }
            data={data}
            inviteOnOpen={
              page === "members" &&
              new URLSearchParams(location.search).get("invite") === "1"
            }
            customerOnOpen={
              page === "customers" &&
              new URLSearchParams(location.search).get("create") === "1"
            }
            onOpenSidebar={() => setMobileSidebarOpen(true)}
            onNavigateTeamMembers={(team) =>
              navigateTo(
                settingsPath(
                  data.workspace.urlKey,
                  "team",
                  team.key,
                  "members",
                ),
              )
            }
            onNavigateMember={(user) =>
              navigateTo(memberProfilePath(data.workspace.urlKey, user.name))
            }
            onNavigateTeam={(team) =>
              navigateTo(teamHomePath(data.workspace.urlKey, team.key))
            }
            onNavigateTeamProjects={(team) =>
              navigateTo(teamProjectsPath(data.workspace.urlKey, team.key))
            }
            onNavigateTeamCycles={(team) =>
              navigateTo(teamCyclesPath(data.workspace.urlKey, team.key))
            }
            onNavigateTeamsSettings={() =>
              navigateTo(settingsPath(data.workspace.urlKey, "teams"))
            }
            onNewTeam={() => navigateTo(newTeamPath(data.workspace.urlKey))}
            onCreateCustomer={addCustomer}
            onUpdateCustomer={changeCustomer}
            onDeleteCustomer={removeCustomer}
            onOpenCustomer={(customer) =>
              navigateTo(customerPath(data.workspace.urlKey, customer))
            }
            onReload={async () =>
              acceptBootstrap(await fetchBootstrap(data.workspace.urlKey))
            }
          />
        )}
        {route.kind === "member-profile" &&
          (() => {
            const user = data.users.find(
              (item) =>
                item.name === route.username || item.id === route.username,
            );
            return user ? (
              <MemberProfilePage
                data={data}
                user={user}
                view={route.view}
                onNavigate={(view) =>
                  navigateTo(
                    memberProfilePath(data.workspace.urlKey, user.name, view),
                  )
                }
                onOpenIssue={openIssue}
                onUpdateIssue={updateIssueFromPage}
              />
            ) : (
              <RouteNotFound
                title="Member not found"
                description="This member is not available in the workspace."
              />
            );
          })()}
        {page === "new-team" && (
          <TeamCreatePage
            teams={data.teams}
            teamSettings={data.teamSettings}
            initialParentTeamId={new URLSearchParams(location.search).get('parentTeamId') ?? ''}
            canCreateSubTeam={data.viewerRole === 'admin' || data.viewerRole === 'owner'}
            onBack={() => navigateTo(teamsPath(data.workspace.urlKey))}
            onNavigateSettings={(settingsPage, teamKey) =>
              navigateTo(
                settingsPath(data.workspace.urlKey, settingsPage, teamKey),
              )
            }
            onCreate={addTeam}
          />
        )}
        {page === "pulse" && route.kind === "pulse" && (
          <PulsePage
            data={data}
            view={route.view}
            viewId={route.viewId}
            onNavigateView={(view) =>
              navigateTo(pulsePath(data.workspace.urlKey, view))
            }
            onNavigateSavedView={(viewId) =>
              navigateTo(pulseViewPath(data.workspace.urlKey, viewId))
            }
            onOpenSidebar={() => setMobileSidebarOpen(true)}
            onCreateSavedView={addSavedView}
            onUpdateSavedView={changeSavedView}
            onDeleteSavedView={removeSavedViewOnly}
            onUpdateUserSettings={changeCurrentUserSettings}
            onCreateProject={addProjectUpdate}
            onUpdateProject={changeProjectUpdate}
            onDeleteProject={removeProjectUpdate}
            onCommentProject={commentOnProjectUpdate}
            onReactProject={reactToProjectUpdate}
            onUploadProjectAttachment={addProjectUpdateAttachment}
            onDeleteProjectAttachment={removeProjectUpdateAttachment}
            onCreateInitiative={addInitiativeUpdate}
            onUpdateInitiative={changeInitiativeUpdate}
            onDeleteInitiative={removeInitiativeUpdate}
            onCommentInitiative={commentOnInitiativeUpdate}
            onReactInitiative={reactToInitiativeUpdate}
            onUploadInitiativeAttachment={addInitiativeUpdateAttachment}
            onDeleteInitiativeAttachment={removeInitiativeUpdateAttachment}
          />
        )}
        {page === "cycles" && route.kind === "team-cycles" && cycleTeam && (
          <CyclesPage
            cycles={data.cycles.filter(
              (cycle) => cycle.teamId === cycleTeam.id,
            )}
            issues={data.issues}
            settings={
              data.cycleSettings[cycleTeam.id] ?? {
                enabled: false,
                durationWeeks: 2,
                cooldownWeeks: 0,
                upcomingCount: 2,
              }
            }
            team={cycleTeam}
            onOpen={openCycle}
            onUpdateCycle={(cycle, input) => changeCycle(cycle.id, input)}
            onStartCycle={startCycle}
            onCompleteCycle={finishCycle}
            onUpdateSettings={(input) =>
              changeCycleSettings(cycleTeam.id, input)
            }
            onReload={refreshActivity}
            onOpenSidebar={() => setMobileSidebarOpen(true)}
          />
        )}
        {page === "cycle-detail" &&
          (route.kind === "cycle" || route.kind === "cycle-upcoming") &&
          selectedCycle &&
          cycleTeam && (
            <CycleDetailPage
              key={selectedCycle.id}
              cycle={selectedCycle}
              team={cycleTeam}
              data={data}
              onBack={() =>
                navigateTo(navigationReturnPath(location.state, data.workspace.urlKey, teamCyclesPath(data.workspace.urlKey, cycleTeam.key)))
              }
              onUpdateCycle={(input) => changeCycle(selectedCycle.id, input)}
              onStartCycle={() => startCycle(selectedCycle)}
              onCompleteCycle={() => finishCycle(selectedCycle)}
              onUpdateIssue={updateIssueById}
              renderIssuePreview={renderIssuePreview}
              onOpenSidebar={() => setMobileSidebarOpen(true)}
              onCreateIssue={() => openCreateIssue({ teamId: cycleTeam.id, cycleId: selectedCycle.id })}
              onReload={refreshActivity}
              onNavigate={navigateTo}
            />
          )}
        {page === "initiatives" &&
          (route.kind === "initiatives" ||
            route.kind === "team-initiatives") && (
            <InitiativesPage
              key={route.kind === 'team-initiatives' ? route.teamKey : 'workspace-initiatives'}
              createOnMount={
                new URLSearchParams(location.search).get("create") === "1"
              }
              initiatives={data.initiatives}
              teamSettings={data.teamSettings}
              teamContext={route.kind === 'team-initiatives' ? data.teams.find(team => team.key.toLowerCase() === route.teamKey.toLowerCase()) : undefined}
              initiativeUpdates={data.initiativeUpdates}
              projects={data.projects}
              projectUpdates={data.projectUpdates}
              teams={data.teams}
              users={data.users}
              labels={labelsForResource(
                data.labels,
                "initiative",
                data.labelGroups,
              )}
              viewer={data.viewer}
              displayDefault={
                data.settings?.initiativeDisplay as
                  | {
                      grouping?: string;
                      ordering?: string;
                      properties?: string[];
                      showTeamInitiatives?: boolean;
                    }
                  | undefined
              }
              view={route.view}
              onViewChange={(view) =>
                navigateTo(
                  route.kind === "team-initiatives"
                    ? teamInitiativesPath(
                        data.workspace.urlKey,
                        route.teamKey,
                        view,
                      )
                    : initiativesPath(data.workspace.urlKey, view),
                )
              }
              onOpen={openInitiative}
              onCreate={(input) =>
                route.kind === "team-initiatives"
                  ? addInitiative({
                      ...input,
                      leadTeamId:
                        input.leadTeamId ??
                        data.teams.find(
                          (team) =>
                            team.key.toLowerCase() ===
                            route.teamKey.toLowerCase(),
                        )?.id,
                    })
                  : addInitiative(input)
              }
              onCreateLabel={addInitiativeLabel}
              onCreateUpdate={addInitiativeUpdate}
              onUpdate={changeInitiative}
              onDelete={removeInitiative}
              onCreateReminder={addInitiativeReminder}
              onSetDefault={async (initiativeDisplay) => {
                await updateWorkspaceSettings({
                  ...(data.settings ?? {}),
                  initiativeDisplay,
                });
                await load();
              }}
              onOpenSidebar={() => setMobileSidebarOpen(true)}
            />
          )}
        {page === "initiative-detail" &&
          route.kind === "initiative" &&
          selectedInitiative && (
            <InitiativeDetailPage
              key={selectedInitiative.id}
              onCreateInitiative={addInitiative}
              onOpenInitiative={openInitiative}
              initiative={selectedInitiative}
              initiatives={data.initiatives}
              documents={data.documents}
              initiativeUpdates={
                data.initiativeUpdates[selectedInitiative.id] ?? []
              }
              drafts={data.drafts}
              projects={data.projects}
              projectStatuses={data.projectStatuses}
              projectTemplates={data.projectTemplates}
              projectUpdates={data.projectUpdates}
              teams={data.teams}
              invitations={data.invitations}
              agentSkills={data.agentSkills}
              users={data.users}
              labels={data.labels}
              labelGroups={data.labelGroups}
              presence={realtime.presence}
              viewer={data.viewer}
              savedViews={availableSavedViews.filter(
                (view) => view.resource === "initiativeProjects",
              )}
              tab={route.tab}
              viewId={route.viewId}
              onBack={() => navigateTo(navigationReturnPath(location.state, data.workspace.urlKey, initiativesPath(data.workspace.urlKey)))}
              onTabChange={(tab) =>
                navigateTo(
                  initiativePath(
                    data.workspace.urlKey,
                    selectedInitiative,
                    tab,
                  ),
                )
              }
              onOpenView={(viewId) =>
                navigateTo(
                  initiativePath(
                    data.workspace.urlKey,
                    selectedInitiative,
                    "view",
                    viewId,
                  ),
                )
              }
              onOpenProject={openProject}
              onCreateProject={addProject}
              onCreateLabel={addInitiativeLabel}
              onUpdateProject={changeProject}
              onCreateProjectUpdate={addProjectUpdate}
              onUpdateProjectUpdate={changeProjectUpdate}
              onDeleteProjectUpdate={removeProjectUpdate}
              onCommentProjectUpdate={commentOnProjectUpdate}
              onReactProjectUpdate={reactToProjectUpdate}
              onUpdate={changeInitiative}
              onDelete={removeInitiative}
              onCreateReminder={addInitiativeReminder}
              onCreateUpdate={addInitiativeUpdate}
              onUpdateInitiativeUpdate={changeInitiativeUpdate}
              onDeleteUpdate={removeInitiativeUpdate}
              onComment={commentOnInitiative}
              onUpdateComment={editInitiativeComment}
              onDeleteComment={removeInitiativeComment}
              onReactComment={reactToInitiativeComment}
              onCreateResource={addInitiativeResource}
              onUpdateResource={changeInitiativeResource}
              onDeleteResource={removeInitiativeResource}
              onCreateSavedView={addSavedView}
              onUpdateSavedView={changeSavedView}
              onDeleteSavedView={removeSavedViewOnly}
              onOpenSidebar={() => setMobileSidebarOpen(true)}
            />
          )}
        {page === "inbox" && (
          <InboxAppPage
            data={data}
            activeTab={route.kind === "inbox" && route.tab ? route.tab : "all"}
            onTabChange={(tab) => navigateTo(inboxPath(data.workspace.urlKey, tab === "all" ? undefined : tab))}
            presence={realtime.presence}
            onReload={load}
            onOpenSidebar={() => setMobileSidebarOpen(true)}
            onOpenSettings={() => navigateTo(settingsPath(data.workspace.urlKey, data.viewerRole === "admin" ? "workspace" : "preferences"))}
            onOpenIssue={openIssue}
            onOpenProject={openProject}
            onOpenReview={(review) =>
              navigateTo(reviewPath(data.workspace.urlKey, review))
            }
            onSubscriberChange={async (issue, subscribed) => {
              await updateIssueById(issue, {
                subscriberIds: subscribed
                  ? [...new Set([...issue.subscriberIds, data.viewer.id])]
                  : issue.subscriberIds.filter((id) => id !== data.viewer.id),
              });
            }}
            onUpdateIssue={updateInboxIssue}
            onDeleteIssue={deleteInboxIssue}
            onCreateRelation={createInboxRelation}
            onDeleteRelation={deleteInboxRelation}
            onCreateSubIssue={createInboxSubIssue}
            onReactIssue={reactToInboxIssue}
            onCreateComment={createInboxComment}
            onEditComment={editInboxComment}
            onDeleteComment={deleteInboxComment}
            onReactComment={reactToInboxComment}
            onUploadAttachment={uploadInboxAttachment}
            onDeleteAttachment={deleteInboxAttachment}
            onCopyIssueLink={(issue) =>
              navigator.clipboard.writeText(
                `${window.location.origin}${issuePath(data.workspace.urlKey, issue)}`,
              )
            }
          />
        )}
        {page === "my-issues" && route.kind === "my-issues" && (
          <MyIssuesPage
            key={route.view}
            data={data}
            initialView={route.view}
            onNavigateView={(_view, href) => navigateTo(href)}
            onOpenSidebar={() => setMobileSidebarOpen(true)}
            onOpenIssue={openIssue}
            onCreateIssue={(context) => openCreateIssue(context)}
            onUpdateIssue={updateIssueFromPage}
            onUpdateIssues={updateIssuesFromPage}
            onDeleteIssues={deleteIssuesFromPage}
          />
        )}
        {page === "workspace-issues" && route.kind === "workspace-issues" && (
          <IssueExplorerPage
            key={`workspace-${route.view}-${location.search}`}
            data={data}
            initialInsightFilters={readInsightDrillFilters(location.search)}
            initialLabelId={
              new URLSearchParams(location.search).get("label") ?? undefined
            }
            initialStatusId={
              new URLSearchParams(location.search).get("status") ?? undefined
            }
            scope={{ kind: "workspace" }}
            view={route.view}
            viewHref={(view) =>
              workspaceIssuesPath(data.workspace.urlKey, view)
            }
            savedViews={issueSavedViews.filter((view) => view.scope !== "team")}
            savedViewHref={(view) =>
              workspaceSavedViewPath(
                data.workspace.urlKey,
                savedViewPathId(view),
              )
            }
            onNavigateView={(view) =>
              navigateTo(workspaceIssuesPath(data.workspace.urlKey, view))
            }
            onNavigateSavedView={(view) =>
              navigateTo(
                workspaceSavedViewPath(
                  data.workspace.urlKey,
                  savedViewPathId(view),
                ),
              )
            }
            onCreateSavedView={addSavedView}
            onUpdateSavedView={changeSavedView}
            onDeleteSavedView={removeSavedView}
            onOpenSidebar={() => setMobileSidebarOpen(true)}
            onOpenIssue={openIssue}
            renderIssuePreview={renderIssuePreview}
            onCreateIssue={(context) => {
              openCreateIssue(context);
            }}
            onUpdateIssue={updateIssueFromPage}
            onUpdateIssues={updateIssuesFromPage}
            onDeleteIssues={deleteIssuesFromPage}
          />
        )}
        {page === "team-issues" && route.kind === "team-issues" && (
          <IssueExplorerPage
            key={`${route.teamKey}-${route.view}`}
            data={data}
            initialStatusId={
              new URLSearchParams(location.search).get("status") ?? undefined
            }
            scope={{
              kind: "team",
              team: data.teams.find(
                (team) =>
                  team.key.toLowerCase() === route.teamKey.toLowerCase(),
              )!,
            }}
            view={route.view}
            viewHref={(view) =>
              teamIssuesPath(data.workspace.urlKey, route.teamKey, view)
            }
            savedViews={issueSavedViews.filter(
              (view) =>
                view.scope === "team" &&
                view.teamId ===
                  data.teams.find(
                    (team) =>
                      team.key.toLowerCase() === route.teamKey.toLowerCase(),
                  )!.id,
            )}
            savedViewHref={(view) =>
              teamSavedViewPath(
                data.workspace.urlKey,
                route.teamKey,
                savedViewPathId(view),
              )
            }
            onNavigateView={(view) =>
              navigateTo(
                teamIssuesPath(data.workspace.urlKey, route.teamKey, view),
              )
            }
            onNavigateSavedView={(view) =>
              navigateTo(
                teamSavedViewPath(
                  data.workspace.urlKey,
                  route.teamKey,
                  savedViewPathId(view),
                ),
              )
            }
            onCreateSavedView={addSavedView}
            onUpdateSavedView={changeSavedView}
            onDeleteSavedView={removeSavedView}
            onOpenSidebar={() => setMobileSidebarOpen(true)}
            onOpenIssue={openIssue}
            renderIssuePreview={renderIssuePreview}
            onCreateIssue={(context) => {
              openCreateIssue(context);
            }}
            onUpdateIssue={updateIssueFromPage}
            onUpdateIssues={updateIssuesFromPage}
            onDeleteIssues={deleteIssuesFromPage}
          />
        )}
        {page === "workspace-issues" &&
          route.kind === "workspace-saved-view" &&
          selectedSavedView && (
            <IssueExplorerPage
              key={`${selectedSavedView.id}:${route.editing ? "edit" : "view"}`}
              data={data}
              scope={selectedSavedViewScope}
              view={selectedSavedView.view}
              savedView={selectedSavedView}
              editingView={route.editing}
              viewHref={(view) =>
                selectedSavedViewTeam
                  ? teamIssuesPath(
                      data.workspace.urlKey,
                      selectedSavedViewTeam.key,
                      view,
                    )
                  : workspaceIssuesPath(data.workspace.urlKey, view)
              }
              savedViews={selectedSavedViewList}
              savedViewHref={savedIssuePathFor}
              onNavigateView={(view) =>
                navigateTo(
                  selectedSavedViewTeam
                    ? teamIssuesPath(
                        data.workspace.urlKey,
                        selectedSavedViewTeam.key,
                        view,
                      )
                    : workspaceIssuesPath(data.workspace.urlKey, view),
                )
              }
              onNavigateSavedView={(view) =>
                navigateTo(savedIssuePathFor(view))
              }
              onCreateSavedView={addSavedView}
              onUpdateSavedView={changeSavedView}
              onDeleteSavedView={removeSavedView}
              onToggleSavedViewFavorite={toggleSavedViewFavorite}
              onSetSavedViewSubscriptionEvents={setSavedViewSubscriptionEvents}
              onShareSavedView={shareView}
              onDuplicateSavedView={(view) =>
                navigateTo(
                  `${
                    selectedSavedViewTeam
                      ? teamViewsNewPath(
                          data.workspace.urlKey,
                          selectedSavedViewTeam.key,
                          "issues",
                        )
                      : workspaceViewsNewPath(data.workspace.urlKey, "issues")
                  }?duplicate=${encodeURIComponent(view.id)}`,
                )
              }
              onBeginEditSavedView={() =>
                navigateTo(savedIssueEditPathFor(selectedSavedView))
              }
              onFinishEditSavedView={() =>
                navigateTo(savedIssuePathFor(selectedSavedView))
              }
              onOpenSidebar={() => setMobileSidebarOpen(true)}
              onOpenIssue={openIssue}
              renderIssuePreview={renderIssuePreview}
              onCreateIssue={(context) => {
                openCreateIssue(context);
              }}
              onUpdateIssue={updateIssueFromPage}
              onUpdateIssues={updateIssuesFromPage}
              onDeleteIssues={deleteIssuesFromPage}
            />
          )}
        {page === "team-issues" &&
          route.kind === "team-saved-view" &&
          selectedSavedView &&
          selectedSavedView.scope === "team" &&
          selectedSavedView.teamId ===
            data.teams.find(
              (team) => team.key.toLowerCase() === route.teamKey.toLowerCase(),
            )!.id && (
            <IssueExplorerPage
              key={`${selectedSavedView.id}:${route.editing ? "edit" : "view"}`}
              data={data}
              scope={{
                kind: "team",
                team: data.teams.find(
                  (team) =>
                    team.key.toLowerCase() === route.teamKey.toLowerCase(),
                )!,
              }}
              view={selectedSavedView.view}
              savedView={selectedSavedView}
              editingView={route.editing}
              viewHref={(view) =>
                teamIssuesPath(data.workspace.urlKey, route.teamKey, view)
              }
              savedViews={issueSavedViews.filter(
                (view) =>
                  view.scope === "team" &&
                  view.teamId === selectedSavedView.teamId,
              )}
              savedViewHref={(view) =>
                teamSavedViewPath(
                  data.workspace.urlKey,
                  route.teamKey,
                  savedViewPathId(view),
                )
              }
              onNavigateView={(view) =>
                navigateTo(
                  teamIssuesPath(data.workspace.urlKey, route.teamKey, view),
                )
              }
              onNavigateSavedView={(view) =>
                navigateTo(
                  teamSavedViewPath(
                    data.workspace.urlKey,
                    route.teamKey,
                    savedViewPathId(view),
                  ),
                )
              }
              onCreateSavedView={addSavedView}
              onUpdateSavedView={changeSavedView}
              onDeleteSavedView={removeSavedView}
              onToggleSavedViewFavorite={toggleSavedViewFavorite}
              onSetSavedViewSubscriptionEvents={setSavedViewSubscriptionEvents}
              onShareSavedView={shareView}
              onDuplicateSavedView={(view) =>
                navigateTo(
                  `${teamViewsNewPath(data.workspace.urlKey, route.teamKey, "issues")}?duplicate=${encodeURIComponent(view.id)}`,
                )
              }
              onBeginEditSavedView={() =>
                navigateTo(
                  teamSavedViewEditPath(
                    data.workspace.urlKey,
                    route.teamKey,
                    savedViewPathId(selectedSavedView),
                  ),
                )
              }
              onFinishEditSavedView={() =>
                navigateTo(
                  teamSavedViewPath(
                    data.workspace.urlKey,
                    route.teamKey,
                    savedViewPathId(selectedSavedView),
                  ),
                )
              }
              onOpenSidebar={() => setMobileSidebarOpen(true)}
              onOpenIssue={openIssue}
              renderIssuePreview={renderIssuePreview}
              onCreateIssue={(context) => {
                openCreateIssue(context);
              }}
              onUpdateIssue={updateIssueFromPage}
              onUpdateIssues={updateIssuesFromPage}
              onDeleteIssues={deleteIssuesFromPage}
            />
          )}
        {page === "views" &&
          (route.kind === "workspace-views" || route.kind === "team-views") && (
            <main className="main-panel">
              <ViewsPage
                data={data}
                resource={viewsResource}
                scope={viewsScope}
                views={directoryViews}
                viewHref={(view) =>
                  (view.resource ?? "issues") === "projects"
                    ? savedProjectPathFor(view)
                    : savedIssuePathFor(view)
                }
                onCreate={() =>
                  navigateTo(
                    viewsTeam
                      ? teamViewsNewPath(
                          data.workspace.urlKey,
                          viewsTeam.key,
                          viewsResource,
                        )
                      : workspaceViewsNewPath(
                          data.workspace.urlKey,
                          viewsResource,
                        ),
                  )
                }
                onDelete={removeSavedViewOnly}
                onDuplicate={(view) =>
                  navigateTo(
                    `${
                      viewsTeam
                        ? teamViewsNewPath(
                            data.workspace.urlKey,
                            viewsTeam.key,
                            viewsResource,
                          )
                        : workspaceViewsNewPath(
                            data.workspace.urlKey,
                            viewsResource,
                          )
                    }?duplicate=${encodeURIComponent(view.id)}`,
                  )
                }
                onEdit={(view) =>
                  navigateTo(
                    (view.resource ?? "issues") === "projects"
                      ? savedProjectEditPathFor(view)
                      : savedIssueEditPathFor(view),
                  )
                }
                onOpen={(view) =>
                  navigateTo(
                    (view.resource ?? "issues") === "projects"
                      ? savedProjectPathFor(view)
                      : savedIssuePathFor(view),
                  )
                }
                onOpenSidebar={() => setMobileSidebarOpen(true)}
                onResourceChange={(resource) =>
                  navigateTo(
                    viewsTeam
                      ? teamViewsPath(
                          data.workspace.urlKey,
                          viewsTeam.key,
                          resource,
                        )
                      : workspaceViewsPath(data.workspace.urlKey, resource),
                  )
                }
                resourceHref={(resource) =>
                  viewsTeam
                    ? teamViewsPath(
                        data.workspace.urlKey,
                        viewsTeam.key,
                        resource,
                      )
                    : workspaceViewsPath(data.workspace.urlKey, resource)
                }
                dashboardsHref={
                  viewsTeam
                    ? teamDashboardsPath(data.workspace.urlKey, viewsTeam.key)
                    : dashboardsPath(data.workspace.urlKey)
                }
                onOpenDashboards={() =>
                  navigateTo(
                    viewsTeam
                      ? teamDashboardsPath(data.workspace.urlKey, viewsTeam.key)
                      : dashboardsPath(data.workspace.urlKey),
                  )
                }
                onUpdate={changeSavedView}
                onToggleFavorite={toggleSavedViewFavorite}
                onToggleScopeFavorite={toggleTeamFavorite}
                onSetSubscriptionEvents={setSavedViewSubscriptionEvents}
                onShare={shareView}
              />
            </main>
          )}
        {page === "views" &&
          (route.kind === "workspace-views-new" ||
            route.kind === "team-views-new") &&
          route.resource === "issues" && (
            <IssueExplorerPage
              key={`${route.kind}:issues-new:${duplicateSavedView?.id ?? "blank"}`}
              data={data}
              scope={viewsScope}
              view="all"
              creatingView
              duplicateFrom={
                duplicateSavedView?.resource !== "projects"
                  ? duplicateSavedView
                  : undefined
              }
              defaultSaveScope={viewsTeam ? "team" : "personal"}
              viewHref={(view) =>
                viewsTeam
                  ? teamIssuesPath(data.workspace.urlKey, viewsTeam.key, view)
                  : workspaceIssuesPath(data.workspace.urlKey, view)
              }
              savedViews={issueSavedViews.filter((view) =>
                viewsTeam
                  ? view.scope === "team" && view.teamId === viewsTeam.id
                  : view.scope === "personal" || view.scope === "workspace",
              )}
              savedViewHref={savedIssuePathFor}
              onNavigateView={(view) =>
                navigateTo(
                  viewsTeam
                    ? teamIssuesPath(data.workspace.urlKey, viewsTeam.key, view)
                    : workspaceIssuesPath(data.workspace.urlKey, view),
                )
              }
              onNavigateSavedView={(view) =>
                navigateTo(savedIssuePathFor(view))
              }
              onCreateSavedView={addSavedView}
              onUpdateSavedView={changeSavedView}
              onDeleteSavedView={removeSavedView}
              onCancelCreateSavedView={() =>
                navigateTo(
                  viewsTeam
                    ? teamViewsPath(
                        data.workspace.urlKey,
                        viewsTeam.key,
                        "issues",
                      )
                    : workspaceViewsPath(data.workspace.urlKey, "issues"),
                )
              }
              onNewViewResourceChange={(resource) =>
                navigateTo(
                  viewsTeam
                    ? teamViewsNewPath(
                        data.workspace.urlKey,
                        viewsTeam.key,
                        resource,
                      )
                    : workspaceViewsNewPath(data.workspace.urlKey, resource),
                )
              }
              onOpenSidebar={() => setMobileSidebarOpen(true)}
              onOpenIssue={openIssue}
              renderIssuePreview={renderIssuePreview}
              onCreateIssue={(context) => {
                openCreateIssue(context);
              }}
              onUpdateIssue={updateIssueFromPage}
              onUpdateIssues={updateIssuesFromPage}
              onDeleteIssues={deleteIssuesFromPage}
            />
          )}
        {page === "views" &&
          (route.kind === "workspace-views-new" ||
            route.kind === "team-views-new") &&
          route.resource === "projects" && (
            <main className="main-panel">
              <ProjectsPage
                key={`${route.kind}:projects-new:${duplicateSavedView?.id ?? "blank"}`}
                projects={data.projects}
                projectUpdates={data.projectUpdates}
                projectStatuses={data.projectStatuses}
                projectTemplates={data.projectTemplates}
                initiatives={data.initiatives}
                users={data.users}
                teams={data.teams}
                invitations={data.invitations}
                presence={realtime.presence}
                agentSkills={data.agentSkills}
                labels={data.labels}
                labelGroups={data.labelGroups}
                workspaceKey={data.workspace.urlKey}
                scopeTeamId={viewsTeam?.id}
                teamSettings={data.teamSettings}
                viewerId={data.viewer.id}
                viewer={data.viewer}
                favoriteProjectIds={data.favorites
                  .filter(
                    (item) =>
                      item.userId === data.viewer.id &&
                      item.resourceType === "project",
                  )
                  .map((item) => item.resourceId)}
                projectSubscriptions={data.subscriptions.filter(
                  (item) =>
                    item.userId === data.viewer.id &&
                    item.resourceType === "project",
                )}
                onToggleProjectFavorite={toggleProjectFavorite}
                onSetProjectSubscriptionEvents={setProjectSubscriptionEvents}
                onCreateProjectReminder={addProjectReminder}
                creatingView
                duplicateFrom={
                  duplicateSavedView?.resource === "projects"
                    ? duplicateSavedView
                    : undefined
                }
                defaultSaveScope={viewsTeam ? "team" : "personal"}
                savedViews={projectSavedViews.filter((view) =>
                  viewsTeam
                    ? view.scope === "team" && view.teamId === viewsTeam.id
                    : view.scope === "personal" || view.scope === "workspace",
                )}
                projectDisplayDefault={data.projectDisplayDefault}
                projectHref={(project) =>
                  projectPath(data.workspace.urlKey, project)
                }
                onOpenSidebar={() => setMobileSidebarOpen(true)}
                onRetry={load}
                onOpenProject={openProject}
                onOpenProjectIssues={(project) =>
                  navigateTo(
                    projectPath(data.workspace.urlKey, project, "issues"),
                  )
                }
                onCreateProject={addProject}
                onCreateProjectUpdate={addProjectUpdate}
                onUpdateProjectUpdate={changeProjectUpdate}
                onDeleteProjectUpdate={removeProjectUpdate}
                onCommentProjectUpdate={commentOnProjectUpdate}
                onReactProjectUpdate={reactToProjectUpdate}
                onUpdateProject={changeProject}
                onDeleteProject={removeProject}
                onSetDisplayDefault={changeProjectDisplayDefault}
                onCreateSavedView={addSavedView}
                onUpdateSavedView={changeSavedView}
                onDeleteSavedView={removeProjectSavedView}
                onNavigateAllViews={() =>
                  navigateTo(
                    viewsTeam
                      ? teamViewsPath(
                          data.workspace.urlKey,
                          viewsTeam.key,
                          "projects",
                        )
                      : workspaceViewsPath(data.workspace.urlKey, "projects"),
                  )
                }
                onNavigateNewView={() =>
                  navigateTo(
                    viewsTeam
                      ? teamViewsNewPath(
                          data.workspace.urlKey,
                          viewsTeam.key,
                          "projects",
                        )
                      : workspaceViewsNewPath(
                          data.workspace.urlKey,
                          "projects",
                        ),
                  )
                }
                onNavigateSavedView={(view) =>
                  navigateTo(savedProjectPathFor(view))
                }
                onNewViewResourceChange={(resource) =>
                  navigateTo(
                    viewsTeam
                      ? teamViewsNewPath(
                          data.workspace.urlKey,
                          viewsTeam.key,
                          resource,
                        )
                      : workspaceViewsNewPath(data.workspace.urlKey, resource),
                  )
                }
              />
            </main>
          )}
        {page === "projects" &&
          (!isProjectsSavedRoute || selectedProjectSavedView) && (
            <main className="main-panel">
              <ProjectsPage
                key={`${route.kind}:${selectedProjectSavedView?.id ?? "all"}:${route.kind === "projects-saved-view" || route.kind === "team-projects-saved-view" ? (route.editing ? "edit" : "view") : "base"}`}
                createOnMount={
                  new URLSearchParams(location.search).get("create") === "1"
                }
                initialTemplateId={
                  new URLSearchParams(location.search).get("template") ??
                  undefined
                }
                projects={data.projects}
                projectUpdates={data.projectUpdates}
                projectStatuses={data.projectStatuses}
                projectTemplates={data.projectTemplates}
                initiatives={data.initiatives}
                users={data.users}
                teams={data.teams}
                invitations={data.invitations}
                presence={realtime.presence}
                agentSkills={data.agentSkills}
                labels={data.labels}
                labelGroups={data.labelGroups}
                workspaceKey={data.workspace.urlKey}
                scopeTeamId={projectTeam?.id}
                teamSettings={data.teamSettings}
                viewerId={data.viewer.id}
                viewer={data.viewer}
                favoriteProjectIds={data.favorites
                  .filter(
                    (item) =>
                      item.userId === data.viewer.id &&
                      item.resourceType === "project",
                  )
                  .map((item) => item.resourceId)}
                projectSubscriptions={data.subscriptions.filter(
                  (item) =>
                    item.userId === data.viewer.id &&
                    item.resourceType === "project",
                )}
                onToggleProjectFavorite={toggleProjectFavorite}
                onSetProjectSubscriptionEvents={setProjectSubscriptionEvents}
                onCreateProjectReminder={addProjectReminder}
                creatingView={
                  route.kind === "projects-new-view" ||
                  route.kind === "team-projects-new-view"
                }
                savedView={selectedProjectSavedView ?? undefined}
                savedViewSubscription={
                  selectedProjectSavedView
                    ? data.subscriptions.find(
                        (item) =>
                          item.userId === data.viewer.id &&
                          item.resourceType === "view" &&
                          item.resourceId === selectedProjectSavedView.id,
                      )
                    : undefined
                }
                editingView={
                  (route.kind === "projects-saved-view" ||
                    route.kind === "team-projects-saved-view") &&
                  route.editing
                }
                savedViews={scopedProjectSavedViews}
                projectDisplayDefault={data.projectDisplayDefault}
                projectHref={(project) =>
                  projectPath(data.workspace.urlKey, project)
                }
                onOpenSidebar={() => setMobileSidebarOpen(true)}
                onRetry={load}
                onOpenProject={openProject}
                onOpenProjectIssues={(project) =>
                  navigateTo(
                    projectPath(data.workspace.urlKey, project, "issues"),
                  )
                }
                onCreateProject={addProject}
                onCreateProjectUpdate={addProjectUpdate}
                onUpdateProjectUpdate={changeProjectUpdate}
                onDeleteProjectUpdate={removeProjectUpdate}
                onCommentProjectUpdate={commentOnProjectUpdate}
                onReactProjectUpdate={reactToProjectUpdate}
                onUpdateProject={changeProject}
                onDeleteProject={removeProject}
                onSetDisplayDefault={changeProjectDisplayDefault}
                onCreateSavedView={addSavedView}
                onUpdateSavedView={changeSavedView}
                onDeleteSavedView={removeProjectSavedView}
                onSetSavedViewSubscriptionEvents={
                  setSavedViewSubscriptionEvents
                }
                onShareSavedView={shareView}
                onDuplicateSavedView={(view) =>
                  navigateTo(
                    `${
                      projectTeamKey
                        ? teamViewsNewPath(
                            data.workspace.urlKey,
                            projectTeamKey,
                            "projects",
                          )
                        : workspaceViewsNewPath(
                            data.workspace.urlKey,
                            "projects",
                          )
                    }?duplicate=${encodeURIComponent(view.id)}`,
                  )
                }
                onBeginEditSavedView={() => {
                  if (selectedProjectSavedView)
                    navigateTo(
                      savedProjectEditPathFor(selectedProjectSavedView),
                    );
                }}
                onFinishEditSavedView={() => {
                  if (selectedProjectSavedView)
                    navigateTo(savedProjectPathFor(selectedProjectSavedView));
                }}
                onNavigateAllViews={() =>
                  navigateTo(
                    projectTeamKey
                      ? teamProjectsPath(data.workspace.urlKey, projectTeamKey)
                      : projectsPath(data.workspace.urlKey),
                  )
                }
                onNavigateNewView={() =>
                  navigateTo(
                    projectTeamKey
                      ? teamProjectsNewViewPath(
                          data.workspace.urlKey,
                          projectTeamKey,
                        )
                      : projectsNewViewPath(data.workspace.urlKey),
                  )
                }
                onNavigateSavedView={(view) =>
                  navigateTo(
                    projectTeamKey
                      ? teamProjectsSavedViewPath(
                          data.workspace.urlKey,
                          projectTeamKey,
                          savedViewPathId(view),
                        )
                      : projectsSavedViewPath(
                          data.workspace.urlKey,
                          savedViewPathId(view),
                        ),
                  )
                }
              />
            </main>
          )}
        {page === "project-detail" &&
          (route.kind === "project" ||
            (route.kind === "project-saved-view" &&
              selectedProjectFacetView)) &&
          selectedProject && (
            <ProjectDetailPage
              issueData={data.issueCollectionPaged ? data : undefined}
              onCreateLabel={addProjectLabel}
              key={`${selectedProject.id}:${selectedProjectFacetView?.id ?? "base"}`}
              project={selectedProject}
              workflowStates={data.states}
              cycles={data.cycles}
              projects={data.projects}
              projectRelations={data.projectRelations}
              initiatives={data.initiatives}
              documents={data.documents}
              integrationConnections={data.integrationConnections}
              projectStatuses={data.projectStatuses}
              projectUpdates={data.projectUpdates?.[selectedProject.id] ?? []}
              drafts={data.drafts}
              issues={data.issues}
              users={data.users}
              teams={data.teams}
              labels={data.labels}
              labelGroups={data.labelGroups}
              viewer={data.viewer}
              activities={data.activities?.[selectedProject.id] ?? []}
              favorite={data.favorites.find(
                (item) =>
                  item.userId === data.viewer.id &&
                  item.resourceType === "project" &&
                  item.resourceId === selectedProject.id,
              )}
              subscription={data.subscriptions.find(
                (item) =>
                  item.userId === data.viewer.id &&
                  item.resourceType === "project" &&
                  item.resourceId === selectedProject.id,
              )}
              tab={route.kind === "project-saved-view" ? "issues" : route.tab}
              savedView={selectedProjectFacetView ?? undefined}
              editingSavedView={
                route.kind === "project-saved-view" && route.editing
              }
              savedViews={data.savedViews}
              onOpenSavedView={(view) =>
                navigateTo(
                  projectSavedViewPath(
                    data.workspace.urlKey,
                    selectedProject.slugId,
                    savedViewPathId(view),
                  ),
                )
              }
              onEditSavedView={(view) =>
                navigateTo(
                  projectSavedViewEditPath(
                    data.workspace.urlKey,
                    selectedProject.slugId,
                    savedViewPathId(view),
                  ),
                )
              }
              onTabChange={(tab) =>
                navigateTo(
                  projectPath(data.workspace.urlKey, selectedProject, tab),
                )
              }
              onUpdate={changeProject}
              onCreateUpdate={addProjectUpdate}
              onUpdateProjectUpdate={changeProjectUpdate}
              onDeleteUpdate={removeProjectUpdate}
              onCommentProjectUpdate={commentOnProjectUpdate}
              onReactProjectUpdate={reactToProjectUpdate}
              onUploadProjectUpdateAttachment={addProjectUpdateAttachment}
              onDeleteProjectUpdateAttachment={removeProjectUpdateAttachment}
              onCommentProject={commentOnProject}
              onCreateResource={addProjectResource}
              onUpdateResource={changeProjectResource}
              onDeleteResource={removeProjectResource}
              onCreateMilestone={addProjectMilestone}
              onUpdateMilestone={changeProjectMilestone}
              onDeleteMilestone={removeProjectMilestone}
              onMoveMilestone={moveProjectMilestone}
              onConvertMilestone={convertProjectMilestone}
              onReorderMilestones={reorderMilestones}
              onDelete={removeProject}
              onToggleFavorite={toggleProjectFavorite}
              onSetSubscriptionEvents={setProjectSubscriptionEvents}
              onCreateReminder={addProjectReminder}
              onCreateSavedView={addSavedView}
              onUpdateSavedView={changeSavedView}
              onDeleteSavedView={async (view) => {
                await removeSavedViewOnly(view);
                navigateTo(
                  projectPath(data.workspace.urlKey, selectedProject, "issues"),
                  { replace: true },
                );
              }}
              onOpenIssue={openIssue}
              onUpdateIssue={updateIssueFromPage}
              onDeleteIssues={deleteIssuesFromPage}
              onCreateIssue={(projectId, projectMilestoneId, context) => {
                const projectTeamId = data.projects.find((item) => item.id === projectId)?.teamIds[0];
                setCreateProjectId(projectId);
                setCreateProjectMilestoneId(projectMilestoneId);
                openCreateIssue({ ...context, projectId, projectMilestoneId, teamId: context?.teamId ?? projectTeamId });
              }}
              onOpenSidebar={() => setMobileSidebarOpen(true)}
            />
          )}
        {page === "issue-detail" && detailAccessPending===data.workspace.urlKey && <main className="main-panel issue-panel" role="status">Checking issue access…</main>}
        {page === "issue-detail" && selectedIssue && !selectedIssue.isSummary && detailAccessPending!==data.workspace.urlKey && (
          <main className="main-panel issue-panel">
            <IssueDetails
              key={selectedIssue.id}
              issue={selectedIssue}
              data={data}
              full
              historyLoading={issueHistoryState.key===`${data.workspace.urlKey}:${selectedIssue.id}`&&issueHistoryState.loading}
              historyError={issueHistoryState.key===`${data.workspace.urlKey}:${selectedIssue.id}`&&issueHistoryState.error ? 'Could not load issue history' : undefined}
              onRetryHistory={()=>{void refreshActivity(false).catch(()=>undefined)}}
              returnPath={issueReturnPath(location.state, data.workspace.urlKey, selectedIssue)}
              navigationIssueIds={issueSequenceIDs(location.state)}
              workspacePresence={realtime.presence}
              issueOptionsActions={selectedIssueOptionsActions}
              presence={realtime.presence.filter(
                (item) =>
                  item.issueId === selectedIssue.id &&
                  item.clientId !== realtime.clientId,
              )}
              onClose={() =>
                navigateTo(
                  issueReturnPath(location.state, data.workspace.urlKey, selectedIssue),
                )
              }
              onNavigateRoot={() =>
                navigateTo(issueReturnPath(location.state, data.workspace.urlKey, selectedIssue))
              }
              onNavigateIssue={openIssue}
              onUpdate={updateSelected}
              onDelete={removeSelected}
              onCreateSubIssue={addSubIssue}
              onCreateProject={addIssueProject}
              onCreateProjectMilestone={addProjectMilestone}
              onReactIssue={reactIssue}
              onComment={addComment}
              onEditComment={editComment}
              onDeleteComment={removeComment}
              onReactComment={reactComment}
              onRelation={addRelation}
              onDeleteRelation={removeRelation}
              onUpload={addAttachment}
              onDeleteAttachment={removeAttachment}
            />
          </main>
        )}
        {routeScopeValid && page === "not-found" && <RouteNotFound />}
        {routeScopeValid && page === "issue-detail" && detailAccessPending!==data.workspace.urlKey && (!selectedIssue || selectedIssue.isSummary) && (
          issueContextLoading || (missingIssueRecord && issueContextKey.current !== `${data.workspace.urlKey}:${recordIdentifier}`) ? <main className="main-panel issue-panel" role="status">Loading issue…</main> : <RouteNotFound
            title="Issue not found"
            description="This issue does not exist or is no longer available."
          />
        )}
        {routeScopeValid && page === "cycle-detail" && !selectedCycle && (
          <RouteNotFound
            title="Cycle not found"
            description="This cycle does not exist or is no longer available."
          />
        )}
        {routeScopeValid && page === "project-detail" && !selectedProject && (
          <RouteNotFound
            title="Project not found"
            description="This project does not exist or is no longer available."
          />
        )}
        {routeScopeValid &&
          route.kind === "project-saved-view" &&
          selectedProject &&
          !selectedProjectFacetView && (
            <RouteNotFound
              title="Project view not found"
              description="This project view does not exist or is no longer available."
            />
          )}
        {routeScopeValid &&
          page === "initiative-detail" &&
          !selectedInitiative && (
            <RouteNotFound
              title="Initiative not found"
              description="This initiative does not exist or is no longer available."
            />
          )}
        {routeScopeValid &&
          (route.kind === "workspace-saved-view" ||
            route.kind === "team-saved-view") &&
          !selectedSavedView && (
            <RouteNotFound
              title="View not found"
              description="This saved view does not exist or is no longer available."
            />
          )}
        {routeScopeValid &&
          isProjectsSavedRoute &&
          !selectedProjectSavedView && (
            <RouteNotFound
              title="Project view not found"
              description="This project view does not exist or is no longer available."
            />
          )}
      </Suspense>
      <Suspense fallback={null}>
        {selected.size > 0 && (
          <BulkActionBar
            count={selected.size}
            data={data}
            onUpdate={batchUpdate}
            onToggleLabel={batchToggleLabel}
            onDelete={batchDelete}
            onClear={() => setSelected(new Set())}
          />
        )}
      </Suspense>
      <Suspense fallback={null}>
        {commandOpen && (
          <CommandMenu
            open={commandOpen}
            onOpenChange={setCommandOpen}
            onCreateIssue={() => openCreateIssue()}
            onCreateDocument={() =>
              void run(
                () => createDocument({ title: "Untitled document" }),
                "Could not create document",
              ).then(async (document) => {
                acceptBootstrap(await fetchBootstrap(data.workspace.urlKey));
                navigateTo(documentPath(data.workspace.urlKey, document));
              })
            }
            onCreateIssueTemplate={() =>
              navigateTo(newIssueTemplatePath(data.workspace.urlKey))
            }
            onCreateProject={() =>
              navigateTo(`${projectsPath(data.workspace.urlKey)}?create=1`)
            }
            onCreateView={() =>
              navigateTo(workspaceViewsNewPath(data.workspace.urlKey, "issues"))
            }
            onCreateInitiative={() =>
              navigateTo(`${initiativesPath(data.workspace.urlKey)}?create=1`)
            }
            onSearchWorkspace={() =>
              navigateTo(searchPath(data.workspace.urlKey))
            }
            onNavigateInbox={() => navigateTo(inboxPath(data.workspace.urlKey))}
            onNavigateMyIssues={() =>
              navigateTo(myIssuesPath(data.workspace.urlKey))
            }
            onNavigateProjects={() =>
              navigateTo(projectsPath(data.workspace.urlKey))
            }
            onNavigateInitiatives={() =>
              navigateTo(initiativesPath(data.workspace.urlKey))
            }
            onNavigateViews={() =>
              navigateTo(workspaceViewsPath(data.workspace.urlKey))
            }
            onNavigateMembers={() =>
              navigateTo(membersPath(data.workspace.urlKey))
            }
            onNavigateCustomers={() =>
              navigateTo(`${customersPath(data.workspace.urlKey)}?create=1`)
            }
            onNavigateAgent={() => navigateTo(agentPath(data.workspace.urlKey))}
            onNavigateReviews={() => navigateTo(reviewsPath(data.workspace.urlKey))}
            onOpenResult={openSearchResult}
          />
        )}
      </Suspense>
      <Suspense fallback={null}>
        {createPresent && (
          <CreateIssueDialog
            open={createOpen}
            draftId={createDraftId}
            initialContext={createContext}
            initialTeamId={createTeamId}
            initialProjectId={createProjectId}
            initialProjectMilestoneId={createProjectMilestoneId}
            initialStateId={createStateId ?? createStateRef.current}
            initialTemplateId={
              new URLSearchParams(location.search).get("template") ?? undefined
            }
            onOpenChange={(open) => {
              setCreateOpen(open);
              if (!open) {
                setCreateDraftId(undefined);
                setCreateContext(undefined);
                setCreateTeamId(undefined);
                setCreateProjectId(undefined);
                setCreateProjectMilestoneId(undefined);
              }
            }}
            data={data}
            onCreate={addIssue}
            onDraftSaved={async () =>
              acceptBootstrap(await fetchBootstrap(data.workspace.urlKey))
            }
            onDraftDeleted={async () =>
              acceptBootstrap(await fetchBootstrap(data.workspace.urlKey))
            }
            onUpload={async (issueId, file) => {
              const attachment = await run(
                () => uploadAttachment(issueId, file),
                "Could not upload attachment",
              );
              setData((current) =>
                current
                  ? {
                      ...current,
                      issues: current.issues.map((issue) =>
                        issue.id === issueId
                          ? {
                              ...issue,
                              attachments: [...issue.attachments, attachment],
                            }
                          : issue,
                      ),
                    }
                  : current,
              );
            }}
          />
        )}
      </Suspense>
      {toolbarAgentSession && (
        <AgentChatPanel
          initialSession={toolbarAgentSession}
          issues={toolbarAgentIssues}
          open
          onClose={() =>
            setClosedAgentSessionIds((current) =>
              new Set(current).add(toolbarAgentSession.id),
            )
          }
          onSessionChange={(next) =>
            setData((current) =>
              current
                ? {
                    ...current,
                    agentSessions: current.agentSessions.map((item) =>
                      item.id === next.id ? next : item,
                    ),
                  }
                : current,
            )
          }
          onOpenFullPage={(session) => {
            if (!session) {
              navigateTo(agentPath(data.workspace.urlKey));
              return;
            }
            void updateAgentSessionRequest(session.id, {
              location: "page",
            }).then((next) => {
              setData((current) =>
                current
                  ? {
                      ...current,
                      agentSessions: current.agentSessions.map((item) =>
                        item.id === next.id ? next : item,
                      ),
                    }
                  : current,
              );
              navigateTo(agentPath(data.workspace.urlKey, next.slugId));
            });
          }}
        />
      )}
      <div className="bottom-agent">
        <button
          aria-label="Agent"
          onClick={() => navigateTo(agentPath(data.workspace.urlKey))}
          type="button"
        >
          <Bot />
          <span>Agent</span>
        </button>
        <button
          aria-label="Chat history"
          onClick={() =>
            navigateTo(`${agentPath(data.workspace.urlKey)}?history=1`)
          }
          type="button"
        >
          <History />
        </button>
      </div>
    </div></PeopleProvider>
  );
}

function readInsightDrillFilters(
  search: string,
): NonNullable<Dashboard["filters"]> | undefined {
  try {
    const value = JSON.parse(
      new URLSearchParams(search).get("insightFilter") ?? "null",
    ) as Dashboard["filters"] | null;
    return value && typeof value === "object" ? value : undefined;
  } catch {
    return undefined;
  }
}

function pageForRoute(route: AppRoute): PageId | "not-found" {
  if (route.kind === "inbox") return "inbox";
  if (route.kind === "search") return "search";
  if (
    route.kind === "diary" ||
    route.kind === "meeting" ||
    route.kind === "automations" ||
    route.kind === "automation-new" ||
    route.kind === "automation-detail" ||
    route.kind === "automation-runs" ||
    route.kind === "label"
  )
    return "workspace-issues";
  if (route.kind === "pulse") return "pulse";
  if (route.kind === "my-issues") return "my-issues";
  if (route.kind === "reviews" || route.kind === "review") return "reviews";
  if (route.kind === "team-issues") return "team-issues";
  if (
    route.kind === "team-overview" ||
    route.kind === "team-documents" ||
    route.kind === "team-loops" ||
    route.kind === "team-members" ||
    route.kind === "team-board" ||
    route.kind === "team-triage" ||
    route.kind === "team-updates" ||
    route.kind === "team-update" ||
    route.kind === "team-resources" ||
    route.kind === "team-links"
  )
    return "team-overview";
  if (route.kind === "team-cycles") return "cycles";
  if (route.kind === "cycle" || route.kind === "cycle-upcoming")
    return "cycle-detail";
  if (route.kind === "workspace-issues") return "workspace-issues";
  if (route.kind === "workspace-members") return "members";
  if (route.kind === "member-profile") return "members";
  if (route.kind === "workspace-customers") return "customers";
  if (route.kind === "customer") return "customer-detail";
  if (route.kind === "documents") return "documents";
  if (route.kind === "analytics") return "analytics";
  if (route.kind === "dashboards") return "analytics";
  if (route.kind === "document") return "document-detail";
  if (route.kind === "drafts") return "drafts";
  if (route.kind === "agent") return "agent";
  if (route.kind === "loops" || route.kind === "loop-editor") return "loops";
  if (
    route.kind === "releases" ||
    route.kind === "release-pipeline" ||
    route.kind === "release" ||
    route.kind === "release-note"
  )
    return "releases";
  if (route.kind === "asks") return "asks";
  if (route.kind === "team-archive") return "team-archive";
  if (route.kind === "workspace-teams") return "teams";
  if (route.kind === "new-team") return "new-team";
  if (route.kind === "team-saved-view") return "team-issues";
  if (route.kind === "workspace-saved-view") return "workspace-issues";
  if (
    route.kind === "workspace-views" ||
    route.kind === "workspace-views-new" ||
    route.kind === "team-views" ||
    route.kind === "team-views-new"
  )
    return "views";
  if (
    route.kind === "projects" ||
    route.kind === "team-projects" ||
    route.kind === "projects-new-view" ||
    route.kind === "projects-saved-view" ||
    route.kind === "team-projects-new-view" ||
    route.kind === "team-projects-saved-view"
  )
    return "projects";
  if (route.kind === "initiatives" || route.kind === "team-initiatives")
    return "initiatives";
  if (route.kind === "initiative") return "initiative-detail";
  if (route.kind === "project" || route.kind === "project-saved-view")
    return "project-detail";
  if (route.kind === "issue") return "issue-detail";
  return "not-found";
}
function RouteNotFound({
  title = "Page not found",
  description = "The requested Flow route is not available.",
}: {
  title?: string;
  description?: string;
}) {
  return (
    <main className="main-panel">
      <div className="state-fill">
        <strong>{title}</strong>
        <span>{description}</span>
      </div>
    </main>
  );
}

function IssueDetails(
  props: Omit<
    React.ComponentProps<typeof DetailPane>,
    "comments" | "activities"
  > & { issue: Issue; data: BootstrapData },
) {
  return (
    <DetailPane
      {...props}
      comments={props.data.comments[props.issue.id] || []}
      activities={props.data.activities[props.issue.id] || []}
    />
  );
}
function isEditableTarget(target: EventTarget | null) {
  return (
    target instanceof HTMLInputElement ||
    target instanceof HTMLTextAreaElement ||
    (target instanceof HTMLElement && target.isContentEditable)
  );
}
function hasOpenShortcutScope() {
  return Boolean(
    document.querySelector(
      '[role="dialog"], [role="menu"][data-state="open"], [data-radix-popper-content-wrapper]',
    ),
  );
}
function applyOptimisticIssue(
  issue: Issue,
  input: IssueUpdateInput,
  data: BootstrapData | null,
): Issue {
  const next: Issue = { ...issue, updatedAt: new Date().toISOString() };
  if (input.title !== undefined) next.title = input.title;
  if (input.description !== undefined) next.description = input.description;
  if (input.descriptionState !== undefined)
    next.descriptionState = input.descriptionState;
  if (input.priority !== undefined) {
    next.priority = input.priority;
    next.priorityLabel =
      ["No priority", "Urgent", "High", "Medium", "Low"][input.priority] ??
      "No priority";
  }
  if (input.stateId !== undefined)
    next.state =
      data?.states.find((item) => item.id === input.stateId) ?? next.state;
  if (input.assigneeId !== undefined)
    next.assignee = input.assigneeId
      ? data?.users.find((item) => item.id === input.assigneeId)
      : undefined;
  if (input.projectId !== undefined) {
    const project = data?.projects.find((item) => item.id === input.projectId);
    next.project = project
      ? {
          id: project.id,
          name: project.name,
          icon: project.icon,
          color: project.color,
        }
      : undefined;
  }
  if (input.projectMilestoneId !== undefined)
    next.projectMilestoneId = input.projectMilestoneId || undefined;
  if (input.cycleId !== undefined) next.cycleId = input.cycleId || undefined;
  if (input.dueDate !== undefined) next.dueDate = input.dueDate || undefined;
  if (input.labelIds !== undefined)
    next.labels = data
      ? labelsForResource(data.labels, "issue", data.labelGroups).filter(
          (item) => input.labelIds?.includes(item.id),
        )
      : next.labels;
  if (input.subscriberIds !== undefined)
    next.subscriberIds = input.subscriberIds;
  if (input.parentId !== undefined) next.parentId = input.parentId || undefined;
  if (input.recurrence !== undefined)
    next.recurrence = input.recurrence || undefined;
  if (input.nextOccurrenceAt !== undefined)
    next.nextOccurrenceAt = input.nextOccurrenceAt || undefined;
  if (input.sortOrder !== undefined) next.sortOrder = input.sortOrder;
  if (input.archived !== undefined)
    next.archivedAt = input.archived ? new Date().toISOString() : undefined;
  if (input.descriptionData !== undefined) {
    next.documentContent = {
      id: issue.documentContent?.id ?? `document_content_${issue.id}`,
      version: (issue.documentContent?.version ?? 0) + 1,
      content: input.description ?? issue.description,
      contentState:
        input.contentState ?? issue.documentContent?.contentState ?? "",
      contentData: input.descriptionData,
      updatedAt: next.updatedAt,
    };
  }
  return next;
}

function nextOccurrence(recurrence: "daily" | "weekly" | "monthly") {
  const date = new Date();
  if (recurrence === "daily") date.setDate(date.getDate() + 1);
  if (recurrence === "weekly") date.setDate(date.getDate() + 7);
  if (recurrence === "monthly") date.setMonth(date.getMonth() + 1);
  return date;
}

export default App;
