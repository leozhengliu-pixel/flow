import * as RadixMenu from "@radix-ui/react-dropdown-menu";
import { ArrowUp, ChevronDown, CircleCheck, CircleDashed, CircleX, CornerDownRight, FileCode2, GitBranch, MessageSquare, Plus } from "lucide-react";
import { useState, type ReactNode } from "react";

import { MentionBody } from "@/components/editor/mentions/mention-body";
import { PersonHover } from "@/components/property/person-info";
import { UserAvatar } from "@/components/ui/user-avatar";
import { useI18n } from "@/i18n/i18n";
import { issuePath, reviewPath } from "@/lib/app-routes";
import { commentOnReview } from "@/lib/api";
import { targetCommentHash } from "@/lib/review-comment-navigation";
import { toast } from "@/lib/toast";
import type { BootstrapData, CodeReview, Issue, ReviewEvent, User } from "@/types/flow";

import { PullRequestCommentActions } from "./pull-request-comment-actions";
import { ReviewCommentBody } from "./review-thread";
import { reviewChecksSummary, reviewCodeThreads, reviewFileGroups, splitReviewPath } from "./pull-request-review-model";
import { reviewStatus, reviewStatusLabels, reviewTimeAgo } from "./review-list-model";
import { reviewProviderNoun } from "./review-provider-model";
import { PostedAsCaption } from "./posted-as-caption";
import { ReviewStateIcon, ReviewTitle } from "./review-state-icon";
import "@/components/project-detail/project-action-menu.css";

type Mutation = { draft?: boolean; status?: CodeReview["status"]; branchState?: CodeReview["branchState"] };

/** Overview tab: title, byline, description and activity, with the properties sidebar. */
export function ReviewOverview({
  data,
  review,
  issues,
  reviewers,
  busy,
  onMutate,
  onNavigate,
  onOpenPicker,
  onReload,
}: {
  data: BootstrapData;
  review: CodeReview;
  issues: Issue[];
  reviewers: User[];
  busy: boolean;
  onMutate: (input: Mutation) => Promise<void>;
  onNavigate: (path: string) => void;
  onOpenPicker: (value: "reviewers" | "issues") => void;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  const author = review.author.displayName || review.author.name;
  return (
    <div className="review-overview">
      <div className="review-overview-main">
        <h1 className="review-overview-title" data-i18n-ignore>
          <ReviewTitle title={review.title} />
        </h1>
        <div className="review-byline">
          <UserAvatar className="avatar review-byline-avatar" name={author} avatarUrl={review.author.avatarUrl} />
          <PersonHover person={review.author}>
            <span className="review-byline-author" data-i18n-ignore>
              {author}
            </span>
          </PersonHover>
          <span className="review-byline-dot" aria-hidden="true">
            ⋅
          </span>
          <span className="review-byline-branch" data-i18n-ignore>
            {review.baseBranch}
          </span>
          <span className="review-byline-arrow" aria-label={t("from")}>
            ←
          </span>
          <span className="review-byline-branch" data-i18n-ignore title={review.headBranch}>
            {review.headBranch}
          </span>
        </div>
        <section className="review-description">
          <h3>{t("Description")}</h3>
          {review.description.trim() ? <MentionBody body={review.description} /> : <p className="review-muted">{t("No description provided.")}</p>}
        </section>
        <ReviewActivity data={data} review={review} onNavigate={onNavigate} onReload={onReload} />
        <ReviewCommentComposer data={data} review={review} onReload={onReload} />
      </div>
      <aside aria-label={t("Review details")} className="review-overview-sidebar">
        <SidebarSection label={t("Status")}>
          <ReviewStatusMenu review={review} busy={busy} onMutate={onMutate} />
        </SidebarSection>
        <SidebarSection label={t("Resolves")}>
          {issues.map((issue) => (
            <a
              key={issue.id}
              className="review-sidebar-issue"
              href={issuePath(data.workspace.urlKey, issue)}
              onClick={(event) => {
                event.preventDefault();
                onNavigate(issuePath(data.workspace.urlKey, issue));
              }}
            >
              <span data-i18n-ignore>{issue.identifier}</span>
              <span data-i18n-ignore>{issue.title}</span>
            </a>
          ))}
          <button type="button" className="review-sidebar-button is-muted" onClick={() => onOpenPicker("issues")}>
            <Plus />
            <span>{t("Link issue")}</span>
          </button>
        </SidebarSection>
        <SidebarSection label={t("Reviewers")}>
          {reviewers.map((user) => (
            <button type="button" key={user.id} className="review-sidebar-button" onClick={() => onOpenPicker("reviewers")}>
              <UserAvatar className="avatar review-sidebar-avatar" name={user.displayName} avatarUrl={user.avatarUrl} />
              <span data-i18n-ignore>{user.displayName}</span>
            </button>
          ))}
          {review.teamReviewers.map((team) => (
            <span key={team} className="review-sidebar-text" data-i18n-ignore>
              {team}
            </span>
          ))}
          <button type="button" className="review-sidebar-button is-muted" onClick={() => onOpenPicker("reviewers")}>
            <Plus />
            <span>{t("Add reviewers")}</span>
          </button>
        </SidebarSection>
        <ReviewChecks checks={review.checks} />
        <SidebarSection label={t("Branch")}>
          <ReviewBranchState review={review} busy={busy} onMutate={onMutate} />
        </SidebarSection>
        <ReviewSidebarFiles data={data} review={review} onNavigate={onNavigate} />
      </aside>
    </div>
  );
}

function SidebarSection({ label, children }: { label: string; children: ReactNode }) {
  return (
    <section className="review-sidebar-section">
      <h3>{label}</h3>
      <div>{children}</div>
    </section>
  );
}

function ReviewStatusMenu({ review, busy, onMutate }: { review: CodeReview; busy: boolean; onMutate: (input: Mutation) => Promise<void> }) {
  const { t } = useI18n();
  const closed = review.status === "merged" || review.status === "closed";
  const noun = reviewProviderNoun(review.provider);
  return (
    <RadixMenu.Root>
      <RadixMenu.Trigger asChild>
        <button type="button" className="review-sidebar-button is-strong" disabled={busy}>
          <ReviewStateIcon review={review} />
          <span>{t(reviewStatusLabels[reviewStatus(review)])}</span>
        </button>
      </RadixMenu.Trigger>
      <RadixMenu.Portal>
        <RadixMenu.Content data-flow-motion="floating" className="project-action-menu" align="start" sideOffset={4} collisionPadding={16}>
          {closed ? (
            <RadixMenu.Item onSelect={() => void onMutate({ status: "open" })}>
              <span className="project-menu-label">{t(`Reopen ${noun}`)}</span>
            </RadixMenu.Item>
          ) : (
            <>
              <RadixMenu.Item onSelect={() => void onMutate({ draft: !review.draft })}>
                <span className="project-menu-label">{t(review.draft ? "Mark ready for review" : "Convert to draft")}</span>
              </RadixMenu.Item>
              <RadixMenu.Item onSelect={() => void onMutate({ status: "closed" })}>
                <span className="project-menu-label">{t(`Close ${noun}`)}</span>
              </RadixMenu.Item>
            </>
          )}
        </RadixMenu.Content>
      </RadixMenu.Portal>
    </RadixMenu.Root>
  );
}

function ReviewChecks({ checks }: { checks: CodeReview["checks"] }) {
  const { t } = useI18n();
  const summary = reviewChecksSummary(checks);
  if (!summary) return null;
  const Icon = summary.tone === "danger" ? CircleX : summary.tone === "success" ? CircleCheck : CircleDashed;
  return (
    <SidebarSection label={t("Checks")}>
      <div className="review-sidebar-text is-strong" data-tone={summary.tone}>
        <Icon className="review-check-icon" />
        <span data-i18n-ignore={summary.failed.length > 0 || undefined}>{summary.failed.length ? summary.text : t(summary.text)}</span>
      </div>
      <ul className="review-sidebar-checks">
        {checks.map((check) => (
          <li key={check.id} data-status={check.status}>
            <i aria-hidden="true" />
            {check.url ? (
              <a href={check.url} target="_blank" rel="noreferrer" data-i18n-ignore>
                {check.name}
              </a>
            ) : (
              <span data-i18n-ignore>{check.name}</span>
            )}
            <small>{t(check.status === "passed" ? "Passed" : check.status === "failed" ? "Failed" : check.status === "skipped" ? "Skipped" : "Pending")}</small>
          </li>
        ))}
      </ul>
    </SidebarSection>
  );
}

function ReviewBranchState({ review, busy, onMutate }: { review: CodeReview; busy: boolean; onMutate: (input: Mutation) => Promise<void> }) {
  const { t } = useI18n();
  const label = review.branchState === "upToDate" ? "Up to date with main" : review.branchState === "conflicted" ? "Conflicts with main" : "Behind main";
  return (
    <div className="review-sidebar-text is-strong" data-branch-state={review.branchState}>
      <GitBranch className="review-branch-icon" />
      <span>{t(label)}</span>
      {review.branchState === "behind" && !["merged", "closed"].includes(review.status) && (
        <button type="button" className="review-pill-button is-small" disabled={busy} onClick={() => void onMutate({ branchState: "upToDate" })}>
          {t("Update branch")}
        </button>
      )}
    </div>
  );
}

function ReviewSidebarFiles({ data, review, onNavigate }: { data: BootstrapData; review: CodeReview; onNavigate: (path: string) => void }) {
  const { t } = useI18n();
  const groups = reviewFileGroups(review.files);
  const [collapsed, setCollapsed] = useState<Set<string>>(() => new Set(groups.length > 1 ? ["tests"] : []));
  if (!review.files.length) return null;
  const open = (path: string) => onNavigate(`${reviewPath(data.workspace.urlKey, review, "changes")}#file=${encodeURIComponent(path)}`);
  return (
    <section className="review-sidebar-section review-sidebar-files">
      <h3>
        {review.files.length} {t(review.files.length === 1 ? "file changed" : "files changed")}
      </h3>
      {groups.length === 1 ? (
        <ul className="review-file-flat">
          {groups[0].files.map((file) => {
            const { name, directory } = splitReviewPath(file.path);
            return (
              <li key={file.path}>
                <a
                  href={`${reviewPath(data.workspace.urlKey, review, "changes")}#file=${encodeURIComponent(file.path)}`}
                  onClick={(event) => {
                    event.preventDefault();
                    open(file.path);
                  }}
                >
                  <FileCode2 />
                  <span className="review-file-name" data-i18n-ignore>
                    {name}
                  </span>
                  <span className="review-file-directory" data-i18n-ignore>
                    {directory}
                  </span>
                  <DiffCounts additions={file.additions} deletions={file.deletions} />
                </a>
              </li>
            );
          })}
        </ul>
      ) : null}
      {groups.length > 1 && groups.map((group) => {
        const isCollapsed = collapsed.has(group.id);
        return (
          <div key={group.id} className="review-file-group">
            <button
              type="button"
              className="review-file-group-heading"
              aria-expanded={!isCollapsed}
              onClick={() =>
                setCollapsed((current) => {
                  const next = new Set(current);
                  if (next.has(group.id)) next.delete(group.id);
                  else next.add(group.id);
                  return next;
                })
              }
            >
              <span>{t(group.label)}</span>
              <small>{group.files.length}</small>
              <ChevronDown className="review-disclosure" />
              <DiffCounts additions={group.additions} deletions={group.deletions} />
            </button>
            {!isCollapsed && (
              <ul>
                {group.files.map((file) => {
                  const { name, directory } = splitReviewPath(file.path);
                  return (
                    <li key={file.path}>
                      <a
                        href={`${reviewPath(data.workspace.urlKey, review, "changes")}#file=${encodeURIComponent(file.path)}`}
                        onClick={(event) => {
                          event.preventDefault();
                          open(file.path);
                        }}
                      >
                        <FileCode2 />
                        <span className="review-file-name" data-i18n-ignore>
                          {name}
                        </span>
                        <span className="review-file-directory" data-i18n-ignore>
                          {directory}
                        </span>
                      </a>
                    </li>
                  );
                })}
              </ul>
            )}
          </div>
        );
      })}
    </section>
  );
}

export function DiffCounts({ additions, deletions }: { additions: number; deletions: number }) {
  return (
    <span className="review-diff-counts" data-i18n-ignore>
      {additions > 0 && <span className="review-diff-added">+{additions}</span>}
      {deletions > 0 && <span className="review-diff-removed">-{deletions}</span>}
    </span>
  );
}

function ReviewActivity({ data, review, onNavigate, onReload }: { data: BootstrapData; review: CodeReview; onNavigate: (path: string) => void; onReload: () => Promise<void> }) {
  const { t } = useI18n();
  // Code comments live on the Diff tab; the activity shows events, conversation
  // comments and reviews (with a resolved-threads summary on the latest review).
  const events = review.events.filter((event) => !event.parentId && !event.path);
  const threads = [...reviewCodeThreads(review).values()].flat();
  const latestReview = [...events].reverse().find((event) => REVIEW_DECISIONS.has(event.type));
  return (
    <section className="review-activity">
      <h3>{t("Activity")}</h3>
      <ol>
        {events.map((event) =>
          isConversationCard(event) ? (
            <ReviewActivityCard
              key={event.id}
              data={data}
              review={review}
              event={event}
              replies={review.events.filter((item) => item.parentId === event.id)}
              summary={event === latestReview && threads.length ? { resolved: threads.filter((thread) => thread.resolved).length, total: threads.length } : undefined}
              onNavigate={onNavigate}
              onReload={onReload}
            />
          ) : (
            <ReviewActivityItem key={event.id} data={data} review={review} event={event} />
          ),
        )}
      </ol>
    </section>
  );
}

const REVIEW_DECISIONS = new Set(["review_commented", "approved", "changes_requested"]);

function isConversationCard(event: ReviewEvent) {
  return event.type === "commented" || event.type === "review_commented" || (REVIEW_DECISIONS.has(event.type) && Boolean(event.body));
}

function useTimeAgo() {
  const { t } = useI18n();
  return (value: string) => {
    const label = reviewTimeAgo(value);
    return label === "just now" ? t("just now") : label.replace(/ ago$/, ` ${t("ago")}`);
  };
}

function ReviewActivityCard({
  data,
  review,
  event,
  replies,
  summary,
  onNavigate,
  onReload,
}: {
  data: BootstrapData;
  review: CodeReview;
  event: ReviewEvent;
  replies: ReviewEvent[];
  summary?: { resolved: number; total: number };
  onNavigate: (path: string) => void;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  const timeAgo = useTimeAgo();
  const [reply, setReply] = useState("");
  const [busy, setBusy] = useState(false);
  const chip = event.type === "approved" ? "Approved" : event.type === "changes_requested" ? "Changes requested" : event.type === "review_commented" ? "Reviewed" : null;
  const send = async () => {
    if (!reply.trim() || busy) return;
    setBusy(true);
    try {
      await commentOnReview(review.id, reply.trim(), { parentId: event.id });
      setReply("");
      await onReload();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not submit comment"));
    } finally {
      setBusy(false);
    }
  };
  return (
    <li className="review-activity-comment">
      {[event, ...replies].map((comment, index) => {
        const actor = comment.actor.displayName || comment.actor.name;
        return (
          <div key={comment.id} className={`review-activity-comment-entry${index ? " is-reply" : ""}`} id={targetCommentHash(comment.id)} data-review-comment-id={comment.id}>
            <header>
              <UserAvatar className="avatar review-activity-avatar" name={actor} avatarUrl={comment.actor.avatarUrl} />
              <strong data-i18n-ignore>{actor}</strong>
              <time dateTime={comment.createdAt} title={new Date(comment.createdAt).toLocaleString()}>
                {timeAgo(comment.createdAt)}
              </time>
              {index === 0 && chip && (
                <span className={`review-activity-chip is-${event.type}`}>
                  {event.type === "approved" ? <CircleCheck /> : event.type === "changes_requested" ? <CircleX /> : <MessageSquare />}
                  {t(chip)}
                </span>
              )}
              <PullRequestCommentActions event={comment} reviewId={review.id} issueIds={review.issueIds} reviewUrl={review.url} />
            </header>
            <PostedAsCaption postedAs={comment.postedAs} />
            {comment.body ? <ReviewCommentBody className="review-activity-body" body={comment.body} /> : null}
            {index === 0 && summary && (
              <a
                className="review-activity-summary"
                href={reviewPath(data.workspace.urlKey, review, "changes")}
                onClick={(clickEvent) => {
                  clickEvent.preventDefault();
                  onNavigate(reviewPath(data.workspace.urlKey, review, "changes"));
                }}
              >
                <CornerDownRight />
                {t("{resolved} of {total} code comments resolved").replace("{resolved}", String(summary.resolved)).replace("{total}", String(summary.total))}
              </a>
            )}
          </div>
        );
      })}
      <div className="review-activity-reply">
        <UserAvatar className="avatar review-activity-avatar" name={data.viewer.displayName} avatarUrl={data.viewer.avatarUrl} />
        <textarea
          aria-label={t("Reply")}
          placeholder={t("Leave a reply…")}
          rows={1}
          value={reply}
          onChange={(changeEvent) => setReply(changeEvent.target.value)}
          onKeyDown={(keyEvent) => {
            if (keyEvent.key === "Enter" && !keyEvent.shiftKey) {
              keyEvent.preventDefault();
              void send();
            }
          }}
        />
        <button type="button" className="review-round-button" aria-label={t("Submit reply")} disabled={busy || !reply.trim()} onClick={() => void send()}>
          <ArrowUp />
        </button>
      </div>
    </li>
  );
}

function ReviewActivityItem({ data, review, event }: { data: BootstrapData; review: CodeReview; event: CodeReview["events"][number] }) {
  const { t } = useI18n();
  const timeAgo = useTimeAgo();
  const actor = event.actor.displayName || event.actor.name;
  const age = timeAgo(event.createdAt);
  const noun = reviewProviderNoun(review.provider);
  const reviewer = event.body ? data.users.find((user) => user.displayName === event.body || user.name === event.body) : undefined;
  let content: ReactNode;
  if (event.type === "opened")
    content = (
      <>
        {t("Opened by")} <strong data-i18n-ignore>{actor}</strong>
        {review.commitCount > 0 && (
          <>
            {" "}
            {t("with")} {review.commitCount} {t(review.commitCount === 1 ? "commit" : "commits")}
          </>
        )}
      </>
    );
  else if (event.type === "review_requested")
    content = (
      <>
        <strong data-i18n-ignore>{actor}</strong> {t("requested review from")} <strong data-i18n-ignore>{reviewer?.displayName || event.body || t("a reviewer")}</strong>
      </>
    );
  else if (event.type === "merged" || event.type === "closed")
    content = <>{t(event.type === "merged" ? "Merged" : "Closed")}</>;
  else
    content = (
      <>
        <strong data-i18n-ignore>{actor}</strong> {t(activityText(event.type, noun))}
        {event.body && <span data-i18n-ignore> {event.body}</span>}
      </>
    );
  return (
    <li className={`review-activity-event is-${event.type}`}>
      <span className="review-activity-icon">
        <ActivityIcon review={review} type={event.type} />
      </span>
      <p>
        {content}
        <span className="review-activity-time">
          {" · "}
          <time dateTime={event.createdAt} title={new Date(event.createdAt).toLocaleString()}>
            {age}
          </time>
        </span>
        {event.postedAs && (
          <>
            {" · "}
            <PostedAsCaption postedAs={event.postedAs} />
          </>
        )}
      </p>
    </li>
  );
}

function ActivityIcon({ review, type }: { review: CodeReview; type: string }) {
  if (type === "approved") return <CircleCheck className="review-icon-success" />;
  if (type === "changes_requested") return <CircleX className="review-icon-danger" />;
  if (type === "merged") return <ReviewStateIcon review={{ ...review, status: "merged" }} />;
  if (type === "closed") return <ReviewStateIcon review={{ ...review, status: "closed" }} />;
  if (type === "opened") return <ReviewStateIcon review={{ ...review, status: "open", draft: false }} />;
  return <CircleDashed />;
}

function activityText(type: string, noun: string) {
  return type === "approved"
    ? "approved these changes"
    : type === "changes_requested"
      ? "requested changes"
      : type === "commented" || type === "review_commented"
        ? "commented"
        : type === "updated"
          ? `updated the ${noun}`
          : "updated the review";
}

function ReviewCommentComposer({ data, review, onReload }: { data: BootstrapData; review: CodeReview; onReload: () => Promise<void> }) {
  const { t } = useI18n();
  const [comment, setComment] = useState("");
  const [busy, setBusy] = useState(false);
  const sendComments = data.userSettings[data.viewer.id]?.sendComments ?? "Enter";
  const submit = async () => {
    if (!comment.trim() || busy) return;
    setBusy(true);
    try {
      await commentOnReview(review.id, comment.trim());
      setComment("");
      await onReload();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not submit comment"));
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="review-comment">
      <UserAvatar className="avatar review-comment-avatar" name={data.viewer.displayName} avatarUrl={data.viewer.avatarUrl} />
      <textarea
        aria-label={t("Comment")}
        placeholder={t("Leave a comment…")}
        rows={1}
        value={comment}
        onChange={(event) => setComment(event.target.value)}
        onKeyDown={(event) => {
          if (event.key !== "Enter" || event.shiftKey) return;
          const commandMode = sendComments === "⌘ Enter";
          if (commandMode !== Boolean(event.metaKey || event.ctrlKey)) return;
          event.preventDefault();
          void submit();
        }}
      />
      <button type="button" className="review-round-button" aria-label={t("Submit comment")} disabled={busy || !comment.trim()} onClick={() => void submit()}>
        <ArrowUp />
      </button>
    </div>
  );
}
