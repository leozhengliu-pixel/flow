import { AlertCircle, GitMerge, GitPullRequest, PanelRightClose, Search, Zap } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { toast } from "@/lib/toast";

import { Avatar } from "@/components/issue/issue-row";
import { usePropertyCommand } from "@/components/property/use-property-command";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { CheckboxMark } from "@/components/ui/checkbox-mark";
import { useI18n } from "@/i18n/i18n";
import { toggleFavoriteFor } from "@/lib/favorites";
import { submitReview, syncReview, updateIntegrationConnection, updateReview, updateUserSettings } from "@/lib/api";
import { integrationSettingsPath, reviewAgentPath, reviewPath, reviewsPath, type ReviewRouteTab } from "@/lib/app-routes";
import type { BootstrapData, CodeReview } from "@/types/flow";

import "./reviews-page.css";
import { ReviewFilters, ReviewDisplayMenu } from "./review-list-controls";
import {
  compareReviews,
  groupReviews,
  matchesReviewFilters,
  reviewBaseItems,
  reviewGroupCollapsedByDefault,
  reviewShortAge,
  reviewStatus,
  reviewStatusLabels,
  type ReviewDisplay,
} from "./review-list-model";
import { useReviewListControls } from "./use-review-list-controls";
import { UserAvatar } from "@/components/ui/user-avatar";
import { PersonHover } from "@/components/property/person-info";
import { CodeReviewAccessActions } from "./code-review-access-actions";
import { resolveCodeReviewAccess } from "@/lib/code-access";
import { DiffView } from "./diff-view";
import { EditableDiffView } from "./editable-diff-view";
import { PullRequestHeader } from "./pull-request-header";
import { PullRequestActionsMenu } from "./pull-request-actions-menu";
import { PullRequestReviewActions } from "./pull-request-review-actions";
import { reviewPrimaryAction, type ReviewDecision } from "./pull-request-review-model";
// coding sessions (track G)
import { ReviewCodingSessionActions } from "@/components/coding-sessions/review-coding-actions";
import { ReviewAgentPanel, ReviewAgentToggle } from "@/components/coding-sessions/review-agent-panel";
import { resolvePreferredMergeMethod, type MergeMethod } from "./pull-request-merge-method";
import { PullRequestReviewShortcutsProvider } from "./pull-request-review-shortcuts-context";
import { useOptionalPullRequestReviewShortcuts } from "./pull-request-review-shortcuts";
import { reviewProviderIdentifier } from "./review-provider-model";
import { ReviewStateIcon, ReviewTitle } from "./review-state-icon";
import { ReviewOverview } from "./review-overview";
import { getAnchoredCommentEvent, scrollToAnchoredComment } from "@/lib/review-comment-navigation";
import { AiBurstGlyph } from "@/components/ui/agent-glyph";

export function ReviewsPage({
  data,
  view,
  review,
  tab = "overview",
  onNavigate,
  onReload,
  onOpenSidebar,
  returnPath,
  agentSessionId,
  showAgent = true,
}: {
  data: BootstrapData;
  view: "for-you" | "created";
  review?: CodeReview;
  tab?: ReviewRouteTab;
  onNavigate: (path: string) => void;
  onReload: () => Promise<void>;
  onOpenSidebar: () => void;
  returnPath?: string;
  /** A coding session of this pull request, opened in the agent panel (`?agentSessionId=…`). */
  agentSessionId?: string;
  /** `?showAgent=false` hides the agent panel. */
  showAgent?: boolean;
}) {
  const { t } = useI18n();
  const [fullWindow, setFullWindow] = useState(false);
  const [busy, setBusy] = useState(false);
  const [picker, setPicker] = useState<"reviewers" | "issues" | null>(null);
  // A coding session opened on the review (the agent panel) shares the page with the review.
  const agentOpen = Boolean(review && agentSessionId && showAgent);
  const mutate = async (input: Parameters<typeof updateReview>[1]) => {
    if (!review) return;
    const pending = Object.entries(input).filter(([, value]) => value !== undefined);
    if (input.favorite !== undefined && pending.length === 1) {
      void toggleFavoriteFor(data, "review", review.id, input.favorite, review.favorite);
      return;
    }
    setBusy(true);
    try {
      await updateReview(review.id, input);
      await onReload();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not update review"));
    } finally {
      setBusy(false);
    }
  };
  return (
    <PullRequestReviewShortcutsProvider
      handlers={{
        toggleReviewLayout: () => {
          if (review) setFullWindow((value) => !value);
        },
      }}
    >
      <main className={`flow-framed-workspace reviews-workspace ${review ? "has-detail" : ""} ${fullWindow ? "is-full-window" : ""} ${agentOpen ? "has-agent-panel" : ""}`}>
        {review ? (
          <section className="reviews-detail-pane">
            <ReviewDetail
              returnPath={returnPath}
              data={data}
              review={review}
              tab={tab}
              busy={busy}
              setBusy={setBusy}
              fullWindow={fullWindow}
              onToggleFull={setFullWindow}
              onNavigate={onNavigate}
              onMutate={mutate}
              onReload={onReload}
              onOpenPicker={setPicker}
              onOpenSidebar={onOpenSidebar}
              agentSessionId={agentSessionId}
              showAgent={showAgent}
            />
          </section>
        ) : (
          <ReviewList data={data} view={view} onNavigate={onNavigate} onReload={onReload} onOpenSidebar={onOpenSidebar} />
        )}
        {review && picker && (
          <PeopleIssuePicker
            type={picker}
            data={data}
            review={review}
            onClose={() => setPicker(null)}
            onSave={async (ids) => {
              await mutate(picker === "reviewers" ? { reviewerIds: ids } : { issueIds: ids });
              setPicker(null);
            }}
          />
        )}
      </main>
    </PullRequestReviewShortcutsProvider>
  );
}

function ReviewList({
  data,
  view,
  onNavigate,
  onReload,
  onOpenSidebar,
}: {
  data: BootstrapData;
  view: "for-you" | "created";
  onNavigate: (path: string) => void;
  onReload: () => Promise<void>;
  onOpenSidebar: () => void;
}) {
  const { t } = useI18n();
  const { filters, setFilters, display, setDisplay } = useReviewListControls(data.workspace.id, data.viewer.id, view);
  const [filterHost, setFilterHost] = useState<HTMLDivElement | null>(null);
  const [toggledGroups, setToggledGroups] = useState<Set<string>>(new Set());
  const baseItems = useMemo(() => reviewBaseItems(data, view, display), [data, view, display]);
  const visible = useMemo(() => baseItems.filter((item) => matchesReviewFilters(item, filters)).sort((a, b) => compareReviews(a, b, display)), [baseItems, filters, display]);
  const groups = useMemo(() => groupReviews(visible, display, data.viewer.id), [visible, display, data.viewer.id]);
  const isCollapsed = (id: string) => reviewGroupCollapsedByDefault(id) !== toggledGroups.has(id);
  const access = resolveCodeReviewAccess(data);
  const tabs = [
    { id: "for-you" as const, label: "For you", path: reviewsPath(data.workspace.urlKey) },
    { id: "created" as const, label: "Created", path: reviewsPath(data.workspace.urlKey, "created") },
  ];
  return (
    <section className="reviews-list-pane">
      <header className="reviews-topbar">
        <button className="reviews-mobile-sidebar" aria-label={t("Open sidebar")} data-sidebar-trigger onClick={onOpenSidebar} type="button">
          <PanelRightClose />
        </button>
        <h1>{t("Reviews")}</h1>
        <div>
          <ReviewFilters filters={filters} data={data} items={baseItems} onChange={setFilters} filterBarHost={filterHost} />
          <ReviewDisplayMenu display={display} view={view} onChange={setDisplay} />
        </div>
      </header>
      <nav className="reviews-tabs" aria-label={t("Reviews")}>
        {tabs.map((item) => (
          <a
            key={item.id}
            className={view === item.id ? "active" : ""}
            aria-current={view === item.id ? "page" : undefined}
            href={item.path}
            onClick={(event) => {
              event.preventDefault();
              onNavigate(item.path);
            }}
          >
            {t(item.label)}
          </a>
        ))}
      </nav>
      <div className="review-filter-host" ref={setFilterHost} />
      {visible.length ? (
        <div className="review-groups">
          {groups.map((group) => {
            const collapsed = display.grouping !== "none" && isCollapsed(group.id);
            return (
              <section key={group.id}>
                {display.grouping !== "none" && (
                  <button
                    type="button"
                    aria-expanded={!collapsed}
                    className="review-group-heading"
                    title={t("Toggle group")}
                    onClick={() =>
                      setToggledGroups((current) => {
                        const next = new Set(current);
                        if (next.has(group.id)) next.delete(group.id);
                        else next.add(group.id);
                        return next;
                      })
                    }
                  >
                    {display.grouping === "author" || display.grouping === "repository" ? <span data-i18n-ignore>{group.label}</span> : <span>{t(group.label)}</span>}
                    {collapsed && <small>{group.items.length}</small>}
                    <i className="review-group-disclosure" aria-hidden="true" />
                  </button>
                )}
                {!collapsed &&
                  group.items.map((item) => (
                    <ReviewRow key={item.id} item={item} display={display} href={reviewPath(data.workspace.urlKey, item)} onOpen={() => onNavigate(reviewPath(data.workspace.urlKey, item))} />
                  ))}
              </section>
            );
          })}
        </div>
      ) : !filters.length && access.reason !== "granted" ? (
        <CodeReviewAccessActions
          data={data}
          variant="empty"
          onAction={(target) => {
            if (target.kind === "grant-code-access") {
              const connection = data.integrationConnections.find((item) => item.provider === "github" && (item.status === "connected" || item.status === "configured"));
              if (!connection) {
                onNavigate(integrationSettingsPath(data.workspace.urlKey, "github"));
                return;
              }
              void updateIntegrationConnection(connection.provider, connection.id, {
                config: { ...(connection.config ?? {}), codeAccess: "true" },
              })
                .then(onReload)
                .catch((error: unknown) => toast.error(error instanceof Error ? error.message : t("Could not enable code access")));
              return;
            }
            onNavigate(integrationSettingsPath(data.workspace.urlKey, target.provider));
          }}
        />
      ) : !filters.length && !data.integrationConnections.some((item) => item.provider === "github" || item.provider === "gitlab") ? (
        <ReviewEmpty onConnect={(provider) => onNavigate(integrationSettingsPath(data.workspace.urlKey, provider))} />
      ) : filters.length ? (
        <div className="reviews-filter-empty">
          <strong>{t("No reviews matching the filters")}</strong>
          <button type="button" onClick={() => setFilters([])}>
            {t("Clear filters")}
          </button>
        </div>
      ) : (
        <div className="reviews-selection-empty">
          <span>{t(view === "created" ? "No open reviews" : "Nothing to review")}</span>
        </div>
      )}
    </section>
  );
}

function ReviewRow({ item, href, onOpen, display }: { item: CodeReview; href: string; onOpen: () => void; display: ReviewDisplay }) {
  const { t } = useI18n();
  const metadata = display.properties.some((id) => ["repository", "id", "status"].includes(id));
  return (
    <a
      className="review-row review-list-row"
      href={href}
      onClick={(event) => {
        if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return;
        event.preventDefault();
        onOpen();
      }}
    >
      <ReviewStateIcon review={item} className="review-row-icon" />
      <span className="review-list-row__content">
        <span className="review-list-row__title" data-i18n-ignore>
          <ReviewTitle title={item.title} />
        </span>
        {metadata && (
          <small className="review-list-row__metadata">
            {display.properties.includes("repository") && <span data-i18n-ignore>{item.repositoryName}</span>}
            {display.properties.includes("id") && <span data-i18n-ignore>{reviewProviderIdentifier(item)}</span>}
            {display.properties.includes("status") && <span>{t(reviewStatusLabels[reviewStatus(item)])}</span>}
          </small>
        )}
      </span>
      {display.properties.includes("quick") && item.quickToReview && <Zap className="review-row-quick" aria-label={t("Quick to review")} />}
      {display.properties.includes("author") && (
        <PersonHover person={item.author}>
          <span>
            <UserAvatar name={item.author.displayName} avatarUrl={item.author.avatarUrl} className="review-filter-avatar" />
          </span>
        </PersonHover>
      )}
      {display.properties.includes("opened") && (
        <time dateTime={item.createdAt} title={new Date(item.createdAt).toLocaleString()}>
          {reviewShortAge(item.createdAt)}
        </time>
      )}
    </a>
  );
}

function ReviewEmpty({ onConnect }: { onConnect: (provider: "github" | "gitlab") => void }) {
  const { t } = useI18n();
  return (
    <div className="reviews-empty">
      <GitPullRequest />
      <strong>{t("Connect a code host")}</strong>
      <span>{t("Connect GitHub or GitLab to sync pull and merge requests.")}</span>
      <div>
        <button type="button" onClick={() => onConnect("github")}>
          <GitPullRequest />
          GitHub
        </button>
        <button type="button" onClick={() => onConnect("gitlab")}>
          <GitMerge />
          GitLab
        </button>
      </div>
    </div>
  );
}

function ReviewDetail({
  returnPath,
  data,
  review,
  tab,
  busy,
  setBusy,
  fullWindow,
  onToggleFull,
  onNavigate,
  onMutate,
  onReload,
  onOpenPicker,
  onOpenSidebar,
  agentSessionId,
  showAgent,
}: {
  returnPath?: string;
  data: BootstrapData;
  review: CodeReview;
  tab: ReviewRouteTab;
  busy: boolean;
  setBusy: (busy: boolean) => void;
  fullWindow: boolean;
  onToggleFull: (value: boolean) => void;
  onNavigate: (path: string) => void;
  onMutate: (input: Parameters<typeof updateReview>[1]) => Promise<void>;
  onReload: () => Promise<void>;
  onOpenPicker: (value: "reviewers" | "issues") => void;
  onOpenSidebar: () => void;
  agentSessionId?: string;
  showAgent?: boolean;
}) {
  const { t } = useI18n();
  const [mergeMethod, setMergeMethod] = useState<MergeMethod>(() => resolvePreferredMergeMethod(data.userSettings[data.viewer.id]?.mergeStrategy));
  const [editableDiff, setEditableDiff] = useState(false);
  const [submitOpen, setSubmitOpen] = useState(false);
  const issues = data.issues.filter((item) => review.issueIds.includes(item.id));
  const reviewers = data.users.filter((user) => review.reviewerIds.includes(user.id));
  const primary = reviewPrimaryAction(review, data.viewer.id);
  const canReview = primary.kind === "submitReview" || primary.kind === "reviewAgain";
  const backPath = returnPath ?? reviewsPath(data.workspace.urlKey);

  const submit = async (decision: ReviewDecision, body: string) => {
    setBusy(true);
    try {
      await submitReview(review.id, { decision, body });
      await onReload();
      setSubmitOpen(false);
      return true;
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not submit review"));
      return false;
    } finally {
      setBusy(false);
    }
  };
  const quickApprove = async () => {
    try {
      await submitReview(review.id, { decision: "approve", body: "" });
      await onReload();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not submit review"));
    }
  };
  const merge = async (method: MergeMethod) => {
    setMergeMethod(method);
    await onMutate({ status: "merged", mergeMethod: method });
    // The last merge method used becomes the default for the next pull request.
    const mergeStrategy = { squash: "Squash and merge", merge: "Merge commit", rebase: "Rebase and merge" }[method];
    if (data.userSettings[data.viewer.id]?.mergeStrategy !== mergeStrategy) void updateUserSettings({ mergeStrategy }).catch(() => undefined);
  };

  // Files and comments come from the code host: refresh them when the review
  // opens (skipped server-side when synced in the last 30 seconds).
  const hostReview = review.provider === "github" || review.provider === "gitlab";
  const codeHost = review.provider === "gitlab" ? "GitLab" : "GitHub";
  // "Sync with GitHub": a forced sync that also retries unsynced comments.
  const syncWithHost = async () => {
    try {
      const updated = await syncReview(review.id, true);
      await onReload();
      if (updated?.syncError) toast.error(updated.syncError);
      else toast.success(t(`Synced with ${codeHost}`));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not sync with the code host"));
    }
  };
  const openSync = useRef({ syncedAt: review.syncedAt, onReload });
  useEffect(() => {
    openSync.current = { syncedAt: review.syncedAt, onReload };
  });
  useEffect(() => {
    // Once per opened review.
    if (!hostReview) return;
    let cancelled = false;
    void (async () => {
      try {
        const updated = await syncReview(review.id, false);
        if (!cancelled && updated && updated.syncedAt !== openSync.current.syncedAt) await openSync.current.onReload();
      } catch {
        // The review stays as stored; the actions menu can sync again.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [review.id, hostReview]);

  const shortcuts = useOptionalPullRequestReviewShortcuts();
  const agentShown = Boolean(agentSessionId) && showAgent !== false;
  useEffect(() => {
    if (!shortcuts) return;
    return shortcuts.register({
      toggleReviewLayout: () => onToggleFull(!fullWindow),
      submitReview: () => canReview && setSubmitOpen(true),
      approve: () => canReview && void quickApprove(),
      openInProvider: () => window.open(review.url, "_blank", "noopener,noreferrer"),
      selectReviewers: () => onOpenPicker("reviewers"),
      toggleFavorite: () => void onMutate({ favorite: !review.favorite }),
      openOverview: () => onNavigate(reviewPath(data.workspace.urlKey, review)),
      openGuide: () => onNavigate(reviewPath(data.workspace.urlKey, review, "review")),
      openDiff: () => onNavigate(reviewPath(data.workspace.urlKey, review, "changes")),
    });
  });
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || event.defaultPrevented) return;
      const target = event.target as HTMLElement | null;
      if (target?.closest("input,textarea,[contenteditable=true],[role=dialog],[role=menu]")) return;
      if (fullWindow) onToggleFull(false);
      else onNavigate(backPath);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [fullWindow, onToggleFull, onNavigate, backPath]);
  useEffect(() => {
    const hash = typeof window !== "undefined" ? window.location.hash : "";
    if (!hash) return;
    const anchored = getAnchoredCommentEvent(review.events, hash);
    if (anchored?.path && tab !== "changes") {
      onNavigate(reviewPath(data.workspace.urlKey, review, "changes") + hash);
      return;
    }
    const timer = window.setTimeout(() => scrollToAnchoredComment(hash), 50);
    return () => window.clearTimeout(timer);
  }, [review.events, review.id, tab, data.workspace.urlKey, onNavigate, review]);

  return (
    <>
      <PullRequestHeader
        data={data}
        review={review}
        issues={issues}
        tab={tab}
        fullWindow={fullWindow}
        onToggleFull={onToggleFull}
        onNavigate={onNavigate}
        onMutate={onMutate}
        onOpenPicker={onOpenPicker}
        onOpenSidebar={onOpenSidebar}
        returnPath={backPath}
        tabSearch={agentSessionId ? `?showAgent=${agentShown}&agentSessionId=${encodeURIComponent(agentSessionId)}` : ""}
        tabActions={
          agentSessionId ? (
            <ReviewAgentToggle
              shown={agentShown}
              onToggle={() => onNavigate(reviewAgentPath(data.workspace.urlKey, review, agentSessionId, { tab, showAgent: !agentShown }))}
            />
          ) : undefined
        }
        menu={
          <PullRequestActionsMenu
            review={review}
            busy={busy}
            canQuickApprove={canReview}
            onMutate={onMutate}
            onOpenPicker={onOpenPicker}
            onQuickApprove={quickApprove}
            onMerge={merge}
            onSync={hostReview ? syncWithHost : undefined}
          />
        }
        actions={
          <>
            {/* coding sessions (track G): "Fix with agent" and quick actions */}
            <ReviewCodingSessionActions review={review} data={data} />
            <PullRequestReviewActions
              review={review}
              viewerId={data.viewer.id}
              busy={busy}
              mergeMethod={mergeMethod}
              submitOpen={submitOpen}
              onSubmitOpenChange={setSubmitOpen}
              onMerge={merge}
              onMutate={onMutate}
              onOpenPicker={onOpenPicker}
              onSubmit={submit}
            />
          </>
        }
      />
      <div className={agentShown ? "review-agent-layout" : "review-agent-layout is-agent-hidden"}>
      <div className="review-detail-body review-agent-layout-main">
        {review.syncError && (
          <p className="review-detail-sync-error" role="status">
            <AlertCircle />
            <span>{t(`Could not sync with ${codeHost}`)}</span>
            <span data-i18n-ignore title={review.syncError}>
              {review.syncError}
            </span>
          </p>
        )}
        {tab === "overview" ? (
          <ReviewOverview
            data={data}
            review={review}
            issues={issues}
            reviewers={reviewers}
            busy={busy}
            onMutate={onMutate}
            onNavigate={onNavigate}
            onOpenPicker={onOpenPicker}
            onReload={onReload}
          />
        ) : tab === "review" ? (
          <ReviewGuide review={review} />
        ) : editableDiff ? (
          <EditableDiffView review={review} onReload={onReload} onExit={() => setEditableDiff(false)} />
        ) : (
          <DiffView review={review} onReload={onReload} onEditableChange={setEditableDiff} viewer={data.viewer} />
        )}
      </div>
      {agentSessionId && agentShown && (
        <ReviewAgentPanel
          sessionId={agentSessionId}
          data={data}
          onClose={() => onNavigate(reviewAgentPath(data.workspace.urlKey, review, agentSessionId, { tab, showAgent: false }))}
          onOpenChanges={() => onNavigate(reviewAgentPath(data.workspace.urlKey, review, agentSessionId, { tab: "changes" }))}
        />
      )}
      </div>
    </>
  );
}

function PeopleIssuePicker({
  type,
  data,
  review,
  onClose,
  onSave,
}: {
  type: "reviewers" | "issues";
  data: BootstrapData;
  review: CodeReview;
  onClose: () => void;
  onSave: (ids: string[]) => Promise<void>;
}) {
  const { t } = useI18n();
  const initial = type === "reviewers" ? review.reviewerIds : review.issueIds,
    [selected, setSelected] = useState(initial),
    [busy, setBusy] = useState(false);
  const options =
    type === "reviewers"
      ? data.users.map((user) => ({
          id: user.id,
          label: user.displayName,
          keywords: `${user.name} ${user.email}`,
        }))
      : data.issues.map((issue) => ({
          id: issue.id,
          label: `${issue.identifier} ${issue.title}`,
        }));
  const command = usePropertyCommand({
    closeOnSelect: false,
    open: true,
    options,
    selectedIds: selected,
    onOpenChange: (open) => !open && onClose(),
    onSelect: (option) => setSelected((current) => (current.includes(option.id) ? current.filter((id) => id !== option.id) : [...current, option.id])),
  });
  return (
    <Dialog open onOpenChange={(value) => !value && onClose()}>
      <DialogContent className="review-picker-dialog" onInteractOutside={(event) => event.preventDefault()} onPointerDownOutside={(event) => event.preventDefault()}>
        <DialogTitle>{t(type === "reviewers" ? "Select reviewers" : "Link issues")}</DialogTitle>
        <label>
          <Search />
          <input
            ref={command.inputRef}
            autoFocus
            aria-label={t("Filter…")}
            placeholder={t("Filter…")}
            value={command.query}
            onChange={(event) => command.onQueryChange(event.target.value)}
            onKeyDown={command.onKeyDown}
          />
        </label>
        <div role="listbox" aria-multiselectable onKeyDown={command.onKeyDown}>
          {command.filteredOptions.map((option) => {
            const checked = command.isSelected(option.id),
              user = type === "reviewers" ? data.users.find((item) => item.id === option.id) : undefined;
            return (
              <button
                type="button"
                role="option"
                aria-selected={command.activeId === option.id}
                aria-checked={checked}
                key={option.id}
                onPointerMove={() => command.setActiveId(option.id)}
                onFocus={() => command.setActiveId(option.id)}
                onClick={(event) => {
                  event.preventDefault();
                  event.stopPropagation();
                  command.choose(option);
                }}
              >
                <span role="checkbox" aria-checked={checked}>
                  {checked && <CheckboxMark />}
                </span>
                {user ? (
                  <>
                    <Avatar name={user.displayName} />
                    <strong data-i18n-ignore>{user.displayName}</strong>
                  </>
                ) : (
                  <>
                    <GitPullRequest />
                    <strong data-i18n-ignore>{option.label}</strong>
                  </>
                )}
              </button>
            );
          })}
        </div>
        <footer>
          <button type="button" onClick={onClose}>
            {t("Cancel")}
          </button>
          <button
            type="button"
            className="primary"
            disabled={busy}
            onClick={() => {
              setBusy(true);
              void onSave(selected).finally(() => setBusy(false));
            }}
          >
            {t("Save")}
          </button>
        </footer>
      </DialogContent>
    </Dialog>
  );
}

function ReviewGuide({ review }: { review: CodeReview }) {
  const { t } = useI18n();
  return (
    <div className="review-guide">
      <AiBurstGlyph size={16} />
      <h2>{t("Review guide")}</h2>
      <p>{t("Suggested focus areas from the files in this review.")}</p>
      {review.files.map((file, index) => (
        <article key={file.path}>
          <span>{index + 1}</span>
          <div>
            <strong data-i18n-ignore>{file.path}</strong>
            <p>{t(index === 0 ? "Check interaction behavior and keyboard accessibility." : "Confirm regression coverage and failure states.")}</p>
          </div>
        </article>
      ))}
    </div>
  );
}
