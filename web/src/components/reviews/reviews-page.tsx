import * as Popover from "@radix-ui/react-popover";
import {
  ArrowUp,
  Check,
  CheckCircle2,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  CircleDot,
  Copy,
  ExternalLink,
  FileCode2,
  GitBranch,
  GitMerge,
  GitPullRequest,
  Link2,
  Maximize2,
  MoreHorizontal,
  PanelRightClose,
  Paperclip,
  Plus,
  Search,
  Sparkles,
  Star,
  Users,
  X,
} from "lucide-react";
import { useMemo, useState, type ComponentProps, type ReactNode } from "react";
import { toast } from "sonner";

import { Avatar } from "@/components/issue/issue-row";
import { usePropertyCommand } from "@/components/property/use-property-command";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import {
  DisplayIcon as Settings2,
  FilterIcon as Filter,
} from "@/components/ui/view-action-icons";
import { Toggle } from "@/components/ui/toggle";
import { CheckboxMark } from "@/components/ui/checkbox-mark";
import { SelectControl } from "@/components/ui/select-control";
import { useI18n } from "@/i18n/i18n";
import { commentOnReview, submitReview, updateReview } from "@/lib/api";
import {
  integrationSettingsPath,
  issuePath,
  reviewPath,
  reviewsPath,
  type ReviewRouteTab,
} from "@/lib/app-routes";
import type { BootstrapData, CodeReview } from "@/types/flow";

import "./reviews-page.css";

type Display = {
  grouping: "focus" | "status" | "repository";
  ordering: "importance" | "updated" | "opened";
  closed: "day" | "week" | "month" | "all";
  showDrafts: boolean;
  showTeams: boolean;
  badge: "all" | "requested";
  properties: string[];
};
type FilterState = {
  status?: string;
  query?: string;
  author?: string;
  reviewer?: string;
  repository?: string;
  quick?: boolean;
  missingIssue?: boolean;
};
const DEFAULT_DISPLAY: Display = {
  grouping: "focus",
  ordering: "importance",
  closed: "day",
  showDrafts: true,
  showTeams: true,
  badge: "all",
  properties: ["repository", "author", "opened", "status", "quick"],
};

export function ReviewsPage({
  data,
  view,
  review,
  tab = "overview",
  onNavigate,
  onReload,
  onOpenSidebar,
}: {
  data: BootstrapData;
  view: "for-you" | "created";
  review?: CodeReview;
  tab?: ReviewRouteTab;
  onNavigate: (path: string) => void;
  onReload: () => Promise<void>;
  onOpenSidebar: () => void;
}) {
  const { t } = useI18n(),
    [filter, setFilter] = useState<FilterState>({}),
    [display, setDisplay] = useState(DEFAULT_DISPLAY),
    [fullWindow, setFullWindow] = useState(false);
  const [submitOpen, setSubmitOpen] = useState(false),
    [picker, setPicker] = useState<"reviewers" | "issues" | null>(null),
    [busy, setBusy] = useState(false),
    [groupExpanded, setGroupExpanded] = useState(true);
  const visible = useMemo(
    () =>
      data.reviews
        .filter((item) =>
          view === "created"
            ? item.author.id === data.viewer.id
            : item.reviewerIds.includes(data.viewer.id),
        )
        .filter((item) => display.showDrafts || !item.draft)
        .filter((item) =>
          filter.status ? item.status === filter.status : true,
        )
        .filter((item) =>
          filter.author ? item.author.id === filter.author : true,
        )
        .filter((item) =>
          filter.reviewer ? item.reviewerIds.includes(filter.reviewer) : true,
        )
        .filter((item) =>
          filter.repository
            ? `${item.repositoryOwner}/${item.repositoryName}` ===
              filter.repository
            : true,
        )
        .filter((item) => (filter.quick ? item.quickToReview : true))
        .filter((item) => (filter.missingIssue ? !item.issueIds.length : true))
        .filter((item) =>
          filter.query
            ? `${item.title} ${item.repositoryOwner} ${item.repositoryName}`
                .toLowerCase()
                .includes(filter.query.toLowerCase())
            : true,
        )
        .sort((a, b) =>
          display.ordering === "opened"
            ? b.createdAt.localeCompare(a.createdAt)
            : b.updatedAt.localeCompare(a.updatedAt),
        ),
    [data.reviews, data.viewer.id, display, filter, view],
  );
  const mutate = async (input: Parameters<typeof updateReview>[1]) => {
    if (!review) return;
    setBusy(true);
    try {
      await updateReview(review.id, input);
      await onReload();
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t("Could not update review"),
      );
    } finally {
      setBusy(false);
    }
  };
  return (
    <main
      className={`flow-framed-workspace reviews-workspace ${review ? "has-detail" : ""} ${fullWindow ? "is-full-window" : ""}`}
    >
      {!fullWindow && (
        <section className="reviews-list-pane">
          <header className="reviews-topbar">
            <button
              className="reviews-mobile-sidebar"
              aria-label={t("Open sidebar")}
              onClick={onOpenSidebar}
            >
              <PanelRightClose />
            </button>
            <h1>{t("Reviews")}</h1>
            <div>
              <ReviewFilterMenu
                filter={filter}
                data={data}
                onChange={setFilter}
              />
              <ReviewDisplayMenu display={display} onChange={setDisplay} />
            </div>
          </header>
          <nav className="reviews-tabs">
            <a
              className={view === "for-you" ? "active" : ""}
              href={reviewsPath(data.workspace.urlKey)}
              onClick={(event) => {
                event.preventDefault();
                onNavigate(reviewsPath(data.workspace.urlKey));
              }}
            >
              {t("For you")}
            </a>
            <a
              className={view === "created" ? "active" : ""}
              href={reviewsPath(data.workspace.urlKey, "created")}
              onClick={(event) => {
                event.preventDefault();
                onNavigate(reviewsPath(data.workspace.urlKey, "created"));
              }}
            >
              {t("Created")}
            </a>
          </nav>
          {visible.length ? (
            <div className="review-groups">
              <button
                aria-expanded={groupExpanded}
                className="review-group-heading"
                onClick={() => setGroupExpanded((value) => !value)}
              >
                {t(view === "created" ? "Created by you" : "Needs your review")}
                <ChevronDown />
              </button>
              {groupExpanded &&
                visible.map((item) => (
                  <ReviewRow
                    key={item.id}
                    item={item}
                    active={review?.id === item.id}
                    data={data}
                    onOpen={() =>
                      onNavigate(reviewPath(data.workspace.urlKey, item))
                    }
                  />
                ))}
            </div>
          ) : !data.integrationConnections.some(
              (item) =>
                item.provider === "github" || item.provider === "gitlab",
            ) ? (
            <ReviewEmpty
              connected={false}
              onConnect={(provider) =>
                onNavigate(
                  integrationSettingsPath(data.workspace.urlKey, provider),
                )
              }
            />
          ) : null}
        </section>
      )}
      <section className="reviews-detail-pane">
        {review ? (
          <ReviewDetail
            data={data}
            review={review}
            tab={tab}
            busy={busy}
            fullWindow={fullWindow}
            onToggleFull={setFullWindow}
            onNavigate={onNavigate}
            onMutate={mutate}
            onReload={onReload}
            onOpenSubmit={() => setSubmitOpen(true)}
            onOpenPicker={setPicker}
          />
        ) : (
          <div className="reviews-selection-empty">
            <ReviewEmptyArt />
            <span>{t("Nothing to review")}</span>
          </div>
        )}
      </section>
      {review && (
        <SubmitReviewDialog
          open={submitOpen}
          busy={busy}
          review={review}
          onClose={() => setSubmitOpen(false)}
          onSubmit={async (decision, body) => {
            setBusy(true);
            try {
              await submitReview(review.id, { decision, body });
              await onReload();
              setSubmitOpen(false);
            } catch (error) {
              toast.error(
                error instanceof Error
                  ? error.message
                  : t("Could not submit review"),
              );
            } finally {
              setBusy(false);
            }
          }}
        />
      )}{" "}
      {review && picker && (
        <PeopleIssuePicker
          type={picker}
          data={data}
          review={review}
          onClose={() => setPicker(null)}
          onSave={async (ids) => {
            await mutate(
              picker === "reviewers" ? { reviewerIds: ids } : { issueIds: ids },
            );
            setPicker(null);
          }}
        />
      )}
    </main>
  );
}

function ReviewRow({
  item,
  active,
  onOpen,
}: {
  item: CodeReview;
  active: boolean;
  data: BootstrapData;
  onOpen: () => void;
}) {
  const { t } = useI18n();
  return (
    <button className={`review-row ${active ? "active" : ""}`} onClick={onOpen}>
      <ReviewProviderIcon provider={item.provider} />
      <span data-i18n-ignore>{item.title}</span>
      {item.quickToReview && <Sparkles aria-label={t("Quick to review")} />}
      <small>{relative(item.updatedAt, t)}</small>
    </button>
  );
}

function ReviewProviderIcon({
  provider,
  ...props
}: { provider: CodeReview["provider"] } & ComponentProps<"svg">) {
  const Icon = provider === "gitlab" ? GitMerge : GitPullRequest;
  return <Icon {...props} />;
}

function reviewProviderNoun(provider: CodeReview["provider"]) {
  return provider === "gitlab" ? "merge request" : "pull request";
}

function reviewProviderIdentifier(
  review: Pick<CodeReview, "provider" | "number">,
) {
  return `${review.provider === "gitlab" ? "!" : "#"}${review.number}`;
}

function ReviewEmpty({
  connected,
  onConnect,
}: {
  connected: boolean;
  onConnect: (provider: "github" | "gitlab") => void;
}) {
  const { t } = useI18n();
  return (
    <div className="reviews-empty">
      <GitPullRequest />
      <strong>
        {t(connected ? "Nothing to review" : "Connect a code host")}
      </strong>
      <span>
        {t(
          connected
            ? "Reviews assigned to you will appear here."
            : "Connect GitHub or GitLab to sync pull and merge requests.",
        )}
      </span>
      {!connected && (
        <div>
          <button onClick={() => onConnect("github")}>
            <GitPullRequest />
            GitHub
          </button>
          <button onClick={() => onConnect("gitlab")}>
            <GitMerge />
            GitLab
          </button>
        </div>
      )}
    </div>
  );
}

function ReviewEmptyArt() {
  return (
    <svg
      aria-hidden="true"
      className="reviews-selection-art"
      fill="none"
      viewBox="0 0 130 131"
    >
      <path
        className="ring ring-one"
        d="M109.401 62.5693C134.2 77.995 134.2 103.005 109.401 118.4307C84.603 133.8564 44.397 133.8564 19.5987 118.4307C-5.19957 103.005 -5.19957 77.995 19.5987 62.5693C44.397 47.14357 84.603 47.14357 109.401 62.5693Z"
      />
      <path
        className="ring ring-two"
        d="M106.573 63.5754C129.809 77.676 129.809 100.5376 106.573 114.6382C83.3367 128.7388 45.6633 128.7388 22.4271 114.6382C-0.809048 100.5376 -0.809048 77.676 22.4271 63.5754C45.6633 49.47485 83.3367 49.47485 106.573 63.5754Z"
      />
      <path
        className="ring ring-three"
        d="M102.33 65.2526C123.223 77.5895 123.223 97.5915 102.33 109.9283C81.4372 122.2652 47.5628 122.2652 26.6698 109.9283C5.77674 97.5915 5.77674 77.5895 26.6698 65.2526C47.5628 52.91579 81.4372 52.91579 102.33 65.2526Z"
      />
      <g>
        <path
          className="shade-one"
          d="M41.3059 67.7349C35.2448 70.9047 31.75 75.1527 31.75 79.6433H30.25C30.25 74.3138 34.3664 69.6713 40.6108 66.4056C46.8911 63.1213 55.5152 61.1123 65 61.1123C74.4848 61.1123 83.1089 63.1213 89.3892 66.4056C95.6336 69.6713 99.75 74.3138 99.75 79.6433H98.25C98.25 75.1527 94.7552 70.9047 88.6941 67.7349C82.6687 64.5838 74.2928 62.6123 65 62.6123C55.7072 62.6123 47.3313 64.5838 41.3059 67.7349Z"
        />
        <path
          className="shade-three"
          fillRule="evenodd"
          d="M78.4652 23.2585C79.0742 22.6782 79.2361 22.2111 79.2361 21.87673C79.2361 21.54241 79.0742 21.07526 78.4652 20.49494C77.8527 19.91118 76.8865 19.31634 75.5687 18.78517C72.9411 17.72606 69.2081 17.03711 65.0082 17.03711C60.8082 17.03711 57.0752 17.72606 54.4476 18.78517C53.1299 19.31634 52.1636 19.91118 51.5511 20.49494C50.9421 21.07526 50.7803 21.54241 50.7803 21.87673C50.7803 22.2111 50.9421 22.6782 51.5511 23.2585C52.1636 23.8423 53.1299 24.4371 54.4476 24.9683C57.0752 26.0274 60.8082 26.7164 65.0082 26.7164C69.2081 26.7164 72.9411 26.0274 75.5687 24.9683C76.8865 24.4371 77.8527 23.8423 78.4652 23.2585ZM65.0082 28.2164C73.6944 28.2164 80.7361 25.378 80.7361 21.87673C80.7361 18.37546 73.6944 15.53711 65.0082 15.53711C56.3219 15.53711 49.2803 18.37546 49.2803 21.87673C49.2803 25.378 56.3219 28.2164 65.0082 28.2164Z"
        />
        <path
          className="shade-one"
          fillRule="evenodd"
          d="M51.642 43.8755C55.0256 45.7975 59.7502 47.0101 65.0096 47.0101C70.2691 47.0101 74.9937 45.7975 78.3773 43.8755C81.7829 41.9409 83.6882 39.3837 83.6882 36.7236H85.1882C85.1882 40.1588 82.7443 43.1199 79.1181 45.1797C75.47 47.2521 70.4803 48.5101 65.0096 48.5101C59.539 48.5101 54.5493 47.2521 50.9011 45.1797C47.275 43.1199 44.8311 40.1588 44.8311 36.7236H46.3311C46.3311 39.3837 48.2364 41.9409 51.642 43.8755Z"
        />
        <path
          className="shade-two"
          fillRule="evenodd"
          d="M51.6096 27.6639C55.0003 29.3759 59.7368 30.4567 65.0096 30.4567C70.2825 30.4567 75.019 29.3759 78.4096 27.6639C81.8431 25.9302 83.6882 23.6723 83.6882 21.39648H85.1882C85.1882 24.5387 82.6841 27.1859 79.0857 29.0029C75.4446 30.8414 70.4669 31.9567 65.0096 31.9567C59.5524 31.9567 54.5746 30.8414 50.9335 29.0029C47.3352 27.1859 44.8311 24.5387 44.8311 21.39648H46.3311C46.3311 23.6723 48.1762 25.9302 51.6096 27.6639Z"
        />
        <path
          className="flask"
          fillRule="evenodd"
          d="M46.2011 21.93625C46.2011 17.70481 52.5669 14.75924 61.3376 13.72671C61.4004 13.71852 61.4663 13.71038 61.5348 13.70231C63.8409 13.43061 66.1803 13.42656 68.4828 13.72671C77.2535 14.75924 83.8193 17.70481 83.8193 21.93625V36.9143C83.8193 37.1571 83.8598 37.3969 83.9379 37.6168L97.3711 75.4416C97.992 76.7931 98.3163 78.1719 98.3163 79.5893C98.3163 89.1358 83.6051 96.9529 65.1102 97.4244C46.6154 96.9529 31.7041 89.1358 31.7041 79.5893C31.7041 78.1719 32.0284 76.7931 32.6494 75.4416L46.0825 37.6168C46.1606 37.3969 46.2011 37.1571 46.2011 36.9143V21.93625ZM44.7011 21.93625C44.7011 18.96286 46.9404 16.77854 49.7896 15.3004C52.697 13.79205 56.6752 12.765987 61.3593 12.212613C63.7769 11.92777 66.2375 11.92216 68.6674 12.238079C73.1444 12.765897 77.1679 13.79129 80.1208 15.29526C83.0015 16.76241 85.3193 18.94261 85.3193 21.93625V36.9143C85.3193 36.993 85.3328 37.0625 85.3514 37.1148L98.781 74.9295C99.4519 76.4068 99.8163 77.9695 99.8163 79.5893C99.8163 85.1953 95.5245 89.9306 89.3568 93.2324C83.1185 96.572 74.5767 98.6836 65.1485 98.9239L65.1102 98.9249L65.072 98.9239C55.644 98.6836 47.0543 96.5722 40.7682 93.2348C34.5581 89.9378 30.2041 85.2042 30.2041 79.5893C30.2041 77.9695 30.5685 76.4068 31.2394 74.9296L44.669 37.1148C44.6876 37.0625 44.7011 36.993 44.7011 36.9143V21.93625Z"
        />
      </g>
    </svg>
  );
}

function ReviewDetail({
  data,
  review,
  tab,
  busy,
  fullWindow,
  onToggleFull,
  onNavigate,
  onMutate,
  onReload,
  onOpenSubmit,
  onOpenPicker,
}: {
  data: BootstrapData;
  review: CodeReview;
  tab: ReviewRouteTab;
  busy: boolean;
  fullWindow: boolean;
  onToggleFull: (value: boolean) => void;
  onNavigate: (path: string) => void;
  onMutate: (input: Parameters<typeof updateReview>[1]) => Promise<void>;
  onReload: () => Promise<void>;
  onOpenSubmit: () => void;
  onOpenPicker: (value: "reviewers" | "issues") => void;
}) {
  const { t } = useI18n(),
    [comment, setComment] = useState("");
  const issues = data.issues.filter((item) =>
      review.issueIds.includes(item.id),
    ),
    reviewers = data.users.filter((user) =>
      review.reviewerIds.includes(user.id),
    );
  const passed = review.checks.every(
    (check) => check.status === "passed" || check.status === "skipped",
  );
  const closed = review.status === "merged" || review.status === "closed",
    sendComments = data.userSettings[data.viewer.id]?.sendComments ?? "Enter";
  const commentSubmit = async () => {
    if (!comment.trim()) return;
    try {
      await commentOnReview(review.id, comment);
      setComment("");
      await onReload();
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t("Could not submit review"),
      );
    }
  };
  return (
    <>
      <header className="review-detail-top">
        {fullWindow && (
          <button
            className="review-full-menu"
            aria-label={t("Menu")}
            onClick={() => onToggleFull(false)}
          >
            <PanelRightClose />
          </button>
        )}
        <button
          className="review-mobile-back"
          aria-label={t("Back to reviews")}
          onClick={() => onNavigate(reviewsPath(data.workspace.urlKey))}
        >
          <ChevronLeft />
        </button>
        <div>
          {issues[0] ? (
            <a
              href={issuePath(data.workspace.urlKey, issues[0])}
              onClick={(event) => {
                event.preventDefault();
                onNavigate(issuePath(data.workspace.urlKey, issues[0]));
              }}
              data-i18n-ignore
            >
              {issues[0].identifier}
            </a>
          ) : (
            <button onClick={() => onOpenPicker("issues")}>
              {t("No issue")}
            </button>
          )}
          <ChevronRight />
          <ReviewProviderIcon provider={review.provider} />
          <strong data-i18n-ignore>
            {reviewProviderIdentifier(review)} {review.title}
          </strong>
        </div>
        <span className="review-diff-count">
          <b>+{review.additions}</b>
          <i>-{review.deletions}</i>
        </span>
        <button
          aria-label={t(
            review.favorite ? "Remove from favorites" : "Add to favorites",
          )}
          onClick={() => void onMutate({ favorite: !review.favorite })}
        >
          <Star fill={review.favorite ? "currentColor" : "none"} />
        </button>
        <ReviewActions
          review={review}
          busy={busy}
          onMutate={onMutate}
          onOpenPicker={onOpenPicker}
          onQuickApprove={async () => {
            try {
              await submitReview(review.id, { decision: "approve", body: "" });
              await onReload();
            } catch (error) {
              toast.error(
                error instanceof Error
                  ? error.message
                  : t("Could not submit review"),
              );
            }
          }}
        />
        <button
          aria-label={t("Copy URL")}
          onClick={() => void navigator.clipboard.writeText(location.href)}
        >
          <Link2 />
        </button>
        <button
          aria-label={t("Copy branch name")}
          onClick={() => void navigator.clipboard.writeText(review.headBranch)}
        >
          <GitBranch />
        </button>
        <button
          aria-label={t(
            fullWindow ? "Exit full window" : "Review diff in full window",
          )}
          aria-pressed={fullWindow}
          onClick={() => onToggleFull(!fullWindow)}
        >
          <Maximize2 />
        </button>
      </header>
      <div className="review-detail-tabs">
        <nav>
          {(["overview", "review", "changes"] as const).map((value) => (
            <a
              className={tab === value ? "active" : ""}
              key={value}
              href={reviewPath(data.workspace.urlKey, review, value)}
              onClick={(event) => {
                event.preventDefault();
                onNavigate(reviewPath(data.workspace.urlKey, review, value));
              }}
            >
              {t(
                value === "overview"
                  ? "Overview"
                  : value === "review"
                    ? "Guide"
                    : "Diff",
              )}
            </a>
          ))}
        </nav>
        <button
          disabled={busy || closed || !passed}
          onClick={() => void onMutate({ status: "merged" })}
        >
          <GitMerge />
          {t("Squash & merge")}
        </button>
        <button
          className="review-submit"
          disabled={busy || closed}
          onClick={onOpenSubmit}
        >
          {t("Submit review")}
        </button>
      </div>
      {tab === "overview" ? (
        <div className={fullWindow ? "review-full-layout" : "review-overview"}>
          <div
            className={
              fullWindow ? "review-full-main" : "review-overview-inner"
            }
          >
            <h2 data-i18n-ignore>{review.title}</h2>
            <div className="review-byline">
              <Avatar name={review.author.displayName} />
              <span data-i18n-ignore>{review.author.displayName}</span>
              <b>·</b>
              <a
                href={review.url}
                target="_blank"
                rel="noreferrer"
                data-i18n-ignore
              >
                {review.repositoryName}
                {reviewProviderIdentifier(review)}
              </a>
              <b>·</b>
              <code data-i18n-ignore>{review.baseBranch}</code>
              <span>←</span>
              <code data-i18n-ignore>{review.headBranch}</code>
            </div>
            <dl>
              <dt>{t("Status")}</dt>
              <dd>
                <ReviewProviderIcon provider={review.provider} />
                <button onClick={() => void onMutate({ draft: !review.draft })}>
                  {t(review.draft ? "Draft" : statusLabel(review.status))}
                </button>
                {!closed && (
                  <em>
                    {t(
                      review.status === "approved"
                        ? "Approved"
                        : "Waiting for your review",
                    )}
                  </em>
                )}
              </dd>
              <dt>{t("Reviewers")}</dt>
              <dd>
                <button
                  className="review-people"
                  onClick={() => onOpenPicker("reviewers")}
                >
                  {reviewers.map((user) => (
                    <Avatar key={user.id} name={user.displayName} />
                  ))}
                  <span>
                    {reviewers.map((user) => user.displayName).join(", ") ||
                      t("Select reviewers")}
                  </span>
                </button>
                <button
                  className="review-add"
                  aria-label={t("Add reviewer")}
                  onClick={() => onOpenPicker("reviewers")}
                >
                  <Plus />
                </button>
              </dd>
              <dt>{t("Checks")}</dt>
              <dd>
                <ChecksMenu checks={review.checks} />
              </dd>
              <dt>{t("Branch")}</dt>
              <dd>
                <GitBranch />
                <button
                  disabled={review.branchState === "upToDate"}
                  onClick={() => void onMutate({ branchState: "upToDate" })}
                >
                  {t(
                    review.branchState === "upToDate"
                      ? "Up to date with main"
                      : review.branchState === "conflicted"
                        ? "Conflicts"
                        : "Behind main",
                  )}
                </button>
              </dd>
              <dt>{t("Issues")}</dt>
              <dd className="review-issues">
                {issues.map((issue) => (
                  <a
                    data-i18n-ignore
                    href={issuePath(data.workspace.urlKey, issue)}
                    onClick={(event) => {
                      event.preventDefault();
                      onNavigate(issuePath(data.workspace.urlKey, issue));
                    }}
                    key={issue.id}
                  >
                    {issue.identifier}
                  </a>
                ))}
                <button onClick={() => onOpenPicker("issues")}>
                  <Plus />
                  {t(issues.length ? "Change issues" : "Link issue")}
                </button>
              </dd>
            </dl>
            <section className="review-description">
              <h3>{t("Description")}</h3>
              <p data-i18n-ignore>{review.description}</p>
            </section>
            <section className="review-activity">
              <h3>{t("Activity")}</h3>
              {review.events.map((event) => (
                <ReviewEvent
                  event={event}
                  key={event.id}
                  review={review}
                  data={data}
                  timeLabel={relative(event.createdAt, t)}
                />
              ))}
            </section>
            <div className="review-comment">
              <Avatar name={data.viewer.displayName} />
              <textarea
                aria-label={t("Comment")}
                placeholder={t("Leave a comment…")}
                value={comment}
                onChange={(event) => setComment(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key !== "Enter" || event.shiftKey) return;
                  const commandMode = sendComments === "⌘ Enter";
                  if (commandMode !== Boolean(event.metaKey || event.ctrlKey))
                    return;
                  event.preventDefault();
                  void commentSubmit();
                }}
              />
              <div className="review-comment-actions">
                <button
                  aria-label={t("Attach images, files, or videos")}
                  type="button"
                >
                  <Paperclip />
                </button>
                <button
                  aria-label={t("Submit comment")}
                  disabled={!comment.trim()}
                  onClick={() => void commentSubmit()}
                  type="button"
                >
                  <ArrowUp />
                </button>
              </div>
            </div>
          </div>
          {fullWindow && (
            <ReviewSidebar
              data={data}
              review={review}
              reviewers={reviewers}
              issues={issues}
              onMutate={onMutate}
              onNavigate={onNavigate}
              onOpenPicker={onOpenPicker}
            />
          )}
        </div>
      ) : tab === "review" ? (
        <ReviewGuide review={review} />
      ) : (
        <ReviewChanges review={review} />
      )}
    </>
  );
}

function ReviewEvent({
  data,
  event,
  review,
  timeLabel,
}: {
  data: BootstrapData;
  event: CodeReview["events"][number];
  review: CodeReview;
  timeLabel: string;
}) {
  const { t } = useI18n();
  const actor = event.actor.displayName || event.actor.name;
  const reviewer = event.body
    ? data.users.find(
        (user) => user.displayName === event.body || user.name === event.body,
      )
    : undefined;
  let content: ReactNode;
  if (event.type === "opened")
    content = (
      <>
        <span>{t("Opened by")}</span> <strong data-i18n-ignore>{actor}</strong>
        {review.commitCount > 0 && (
          <span className="review-commit-count">
            {" "}
            {t("with")} {review.commitCount}{" "}
            {t(review.commitCount === 1 ? "commit" : "commits")}
          </span>
        )}
      </>
    );
  else if (event.type === "review_requested")
    content = (
      <>
        <strong data-i18n-ignore>{actor}</strong>{" "}
        <span>{t("requested review from")} </span>
        <span className="review-event-link" data-i18n-ignore>
          {reviewer?.displayName || event.body || "reviewer"}
        </span>
      </>
    );
  else
    content = (
      <>
        <strong data-i18n-ignore>{actor}</strong>{" "}
        <span>
          {t(eventText(event.type, reviewProviderNoun(review.provider)))}
        </span>
        {event.body && <span data-i18n-ignore> {event.body}</span>}
      </>
    );
  return (
    <div className={`review-event is-${event.type}`}>
      <span className="review-event-icon">
        <ReviewEventIcon type={event.type} />
      </span>
      <p>
        {content}
        <small> · {timeLabel}</small>
      </p>
    </div>
  );
}

function ReviewEventIcon({ type }: { type: string }) {
  if (type === "opened")
    return (
      <svg
        aria-hidden="true"
        className="review-event-opened-icon"
        fill="currentColor"
        viewBox="0 0 16 16"
      >
        <path
          fillRule="evenodd"
          clipRule="evenodd"
          d="M12.5 10C13.8807 10 15 11.1193 15 12.5C15 13.8807 13.8807 15 12.5 15C11.1193 15 10 13.8807 10 12.5C10 11.1193 11.1193 10 12.5 10ZM12.5 11.5C11.9477 11.5 11.5 11.9477 11.5 12.5C11.5 13.0523 11.9477 13.5 12.5 13.5C13.0523 13.5 13.5 13.0523 13.5 12.5C13.5 11.9477 13.0523 11.5 12.5 11.5Z"
        />
        <path
          fillRule="evenodd"
          clipRule="evenodd"
          d="M3.5 4.5C3.91414 4.50009 4.25 4.83584 4.25 5.25V14.249C4.24982 14.663 3.91403 14.9989 3.5 14.999C3.0859 14.999 2.75018 14.6631 2.75 14.249V5.25C2.75 4.83579 3.08579 4.5 3.5 4.5Z"
        />
        <path
          fillRule="evenodd"
          clipRule="evenodd"
          d="M10 2.75C11.7949 2.75 13.25 4.20507 13.25 6V10.75C13.25 11.1642 12.9142 11.5 12.5 11.5C12.0858 11.5 11.75 11.1642 11.75 10.75V6C11.75 5.0335 10.9665 4.25 10 4.25H8C7.58579 4.25 7.25 3.91421 7.25 3.5C7.25 3.08579 7.58579 2.75 8 2.75H10Z"
        />
        <path
          fillRule="evenodd"
          clipRule="evenodd"
          d="M3.5 1C4.88071 1 6 2.11929 6 3.5C6 4.88071 4.88071 6 3.5 6C2.11929 6 1 4.88071 1 3.5C1 2.11929 2.11929 1 3.5 1ZM3.5 2.5C2.94772 2.5 2.5 2.94772 2.5 3.5C2.5 4.05228 2.94772 4.5 3.5 4.5C4.05228 4.5 4.5 4.05228 4.5 3.5C4.5 2.94772 4.5 2.5 3.5 2.5Z"
        />
      </svg>
    );
  if (type === "review_requested")
    return (
      <svg
        aria-hidden="true"
        className="review-event-requested-icon"
        fill="currentColor"
        viewBox="0 0 16 16"
      >
        <path d="M10.73 6.709a.75.75 0 0 0-.96-1.152L7.296 7.618 6.28 6.602a.75.75 0 0 0-1.06 1.061l1.5 1.5a.75.75 0 0 0 1.01.046l3-2.5Z" />
        <path
          fillRule="evenodd"
          clipRule="evenodd"
          d="M11.879 1.383H4.12c-.395 0-.736 0-1.017.023-.297.024-.592.077-.875.222a2.25 2.25 0 0 0-.984.983c-.144.284-.198.579-.222.876C1 3.767 1 4.109 1 4.504v5.258c0 .395 0 .736.023 1.017.024.297.078.591.222.875.216.424.56.768.984.984.283.144.578.198.875.222.28.023.622.023 1.017.023h1.236l1.683 2.019c.5.6 1.42.6 1.92 0l1.683-2.02h1.236c.395 0 .736 0 1.017-.022.297-.024.592-.078.875-.222a2.25 2.25 0 0 0 .984-.984c.144-.284.198-.578.222-.875.023-.28.023-.622.023-1.017V4.504c0-.395 0-.737-.023-1.017-.024-.297-.078-.592-.222-.876a2.25 2.25 0 0 0-.984-.983c-.283-.145-.578-.198-.875-.222Zm-8.97 1.582c.038-.02.113-.048.317-.064.212-.018.492-.018.924-.018h7.7c.432 0 .712 0 .924.018.204.016.28.045.317.064a.75.75 0 0 1 .327.327c.02.037.047.113.064.317.017.212.018.491.018.924v5.2c0 .432 0 .711-.018.924-.017.204-.045.28-.064.316a.75.75 0 0 1-.327.328c-.038.019-.113.047-.317.064-.212.017-.492.018-.924.018h-1.324a1.25 1.25 0 0 0-.96.45L8 13.71l-1.566-1.878a1.25 1.25 0 0 0-.96-.45H4.15c-.432 0-.712-.018-.924-.018-.204-.017-.28-.045-.316-.064a.75.75 0 0 1-.328-.328c-.019-.037-.047-.112-.064-.316a12.753 12.753 0 0 1-.018-.924v-5.2c0-.433 0-.712.018-.924.017-.204.045-.28.064-.317a.75.75 0 0 1 .328-.327Z"
        />
      </svg>
    );
  return <CircleDot />;
}

function ReviewSidebar({
  data,
  review,
  reviewers,
  issues,
  onMutate,
  onNavigate,
  onOpenPicker,
}: {
  data: BootstrapData;
  review: CodeReview;
  reviewers: BootstrapData["users"];
  issues: BootstrapData["issues"];
  onMutate: (input: Parameters<typeof updateReview>[1]) => Promise<void>;
  onNavigate: (path: string) => void;
  onOpenPicker: (value: "reviewers" | "issues") => void;
}) {
  const { t } = useI18n();
  const passed = review.checks.filter(
    (check) => check.status === "passed" || check.status === "skipped",
  ).length;
  const closed = review.status === "merged" || review.status === "closed";
  return (
    <aside aria-label={t("Review details")} className="review-full-sidebar">
      <section className="review-sidebar-section review-sidebar-status">
        <span>{t("Status")}</span>
        <div>
          <ReviewProviderIcon provider={review.provider} />
          <button onClick={() => void onMutate({ draft: !review.draft })}>
            {t(review.draft ? "Draft" : statusLabel(review.status))}
          </button>
          {!closed && (
            <em>
              {t(
                review.status === "approved"
                  ? "Approved"
                  : "Waiting for your review",
              )}
            </em>
          )}
        </div>
      </section>
      <section className="review-sidebar-section">
        <span>{t("Resolves")}</span>
        <div className="review-sidebar-link-row">
          <Plus />
          <button onClick={() => onOpenPicker("issues")}>
            {t(issues.length ? "Change issues" : "Link issue")}
          </button>
        </div>
      </section>
      <section className="review-sidebar-section review-sidebar-people">
        <header>
          <span>{t("Reviewers")}</span>
          <button
            aria-label={t("Add reviewer")}
            onClick={() => onOpenPicker("reviewers")}
          >
            <Plus />
          </button>
        </header>
        {reviewers.length ? (
          reviewers.map((user) => (
            <div className="review-sidebar-person" key={user.id}>
              <Avatar name={user.displayName} />
              <strong data-i18n-ignore>{user.displayName}</strong>
            </div>
          ))
        ) : (
          <button
            className="review-sidebar-empty"
            onClick={() => onOpenPicker("reviewers")}
          >
            {t("Add reviewer")}
          </button>
        )}
      </section>
      <section className="review-sidebar-section review-sidebar-checks">
        <span>{t("Checks")}</span>
        <div className="review-sidebar-check-summary">
          <CheckCircle2 />
          <span>
            {review.checks.length
              ? `${passed} / ${review.checks.length} ${t("passed")}`
              : t("All passed")}
          </span>
        </div>
        {review.checks.map((check) => (
          <div className="review-sidebar-check" key={check.id}>
            <i data-status={check.status} />
            <span data-i18n-ignore>{check.name}</span>
          </div>
        ))}
      </section>
      <section className="review-sidebar-section review-sidebar-branch">
        <span>{t("Branch")}</span>
        <div>
          <GitBranch />
          <button
            disabled={review.branchState === "upToDate"}
            onClick={() => void onMutate({ branchState: "upToDate" })}
          >
            {t(
              review.branchState === "upToDate"
                ? "Up to date with main"
                : review.branchState === "conflicted"
                  ? "Conflicts"
                  : "Behind main",
            )}
          </button>
        </div>
      </section>
      <section className="review-sidebar-section review-sidebar-files">
        <span>
          {review.files.length}{" "}
          {t(review.files.length === 1 ? "file changed" : "files changed")}
        </span>
        {review.files.map((file) => (
          <a
            href={reviewPath(data.workspace.urlKey, review, "changes")}
            onClick={(event) => {
              event.preventDefault();
              onNavigate(reviewPath(data.workspace.urlKey, review, "changes"));
            }}
            key={file.path}
          >
            <FileCode2 />
            <span data-i18n-ignore>{file.path}</span>
            <b>+{file.additions}</b>
            <i>-{file.deletions}</i>
          </a>
        ))}
      </section>
    </aside>
  );
}

function ReviewFilterMenu({
  filter,
  data,
  onChange,
}: {
  filter: FilterState;
  data: BootstrapData;
  onChange: (value: FilterState) => void;
}) {
  const { t } = useI18n(),
    [open, setOpen] = useState(false),
    [view, setView] = useState<
      "fields" | "status" | "author" | "reviewer" | "repository"
    >("fields");
  const repositories = [
    ...new Set(
      data.reviews.map(
        (item) => `${item.repositoryOwner}/${item.repositoryName}`,
      ),
    ),
  ];
  const users =
    view === "author"
      ? data.users.filter((user) =>
          data.reviews.some((item) => item.author.id === user.id),
        )
      : data.users;
  const options =
    view === "fields"
      ? [
          { id: "status", label: "Status" },
          { id: "author", label: "Author" },
          { id: "reviewer", label: "Reviewers" },
          { id: "repository", label: "Repository name" },
          { id: "quick", label: "Quick to review" },
          { id: "missingIssue", label: "Missing issue" },
        ]
      : view === "status"
        ? ["open", "inReview", "approved", "merged", "closed"].map((id) => ({
            id,
            label: statusLabel(id),
          }))
        : view === "repository"
          ? repositories.map((id) => ({ id, label: id }))
          : users.map((user) => ({
              id: user.id,
              label: user.displayName,
              keywords: `${user.name} ${user.email}`,
            }));
  const back = () => {
    command.onQueryChange("");
    setView("fields");
  };
  const select = (key: keyof FilterState, value: string | boolean) => {
    onChange({ ...filter, [key]: filter[key] === value ? undefined : value });
    back();
  };
  const command = usePropertyCommand({
    closeOnSelect: false,
    open,
    options,
    resetKey: view,
    selectedIds: [],
    onOpenChange: setOpen,
    onSelect: (option) => {
      if (view === "fields") {
        if (option.id === "quick" || option.id === "missingIssue")
          select(option.id, true);
        else setView(option.id as typeof view);
      } else {
        const key =
          view === "status"
            ? "status"
            : view === "author"
              ? "author"
              : view === "reviewer"
                ? "reviewer"
                : "repository";
        select(key, option.id);
      }
    },
  });
  return (
    <Popover.Root
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) back();
      }}
    >
      <Popover.Trigger asChild>
        <button className="reviews-icon-button" aria-label={t("Add filter")}>
          <Filter />
        </button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content
          className="reviews-command"
          align="end"
          sideOffset={5}
          onKeyDown={command.onKeyDown}
          onEscapeKeyDown={(event) => {
            if (view !== "fields") {
              event.preventDefault();
              back();
            }
          }}
        >
          <label>
            {view !== "fields" ? (
              <button aria-label={t("Back")} onClick={back}>
                ‹
              </button>
            ) : (
              <Search />
            )}
            <input
              ref={command.inputRef}
              key={view}
              autoFocus
              aria-label={t(view === "fields" ? "Add Filter…" : "Filter…")}
              placeholder={t(view === "fields" ? "Add Filter…" : "Filter…")}
              value={command.query}
              onChange={(event) => command.onQueryChange(event.target.value)}
            />
            {view === "fields" && <kbd>F</kbd>}
          </label>
          <div role="listbox">
            {command.filteredOptions.map((option) => {
              const selected =
                option.id === "quick"
                  ? filter.quick
                  : option.id === "missingIssue"
                    ? filter.missingIssue
                    : view === "fields"
                      ? false
                      : filter[view] === option.id;
              return (
                <button
                  role="option"
                  aria-selected={command.activeId === option.id}
                  aria-checked={selected || undefined}
                  key={option.id}
                  onPointerMove={() => command.setActiveId(option.id)}
                  onFocus={() => command.setActiveId(option.id)}
                  onClick={() => command.choose(option)}
                >
                  {option.id === "quick" ? (
                    <Sparkles />
                  ) : option.id === "missingIssue" ? (
                    <Link2 />
                  ) : (
                    <CircleDot />
                  )}
                  <span
                    data-i18n-ignore={
                      (view !== "fields" && view !== "status") || undefined
                    }
                  >
                    {view === "status" || view === "fields"
                      ? t(option.label)
                      : option.label}
                  </span>
                  {view === "fields" &&
                  option.id !== "quick" &&
                  option.id !== "missingIssue" ? (
                    <ChevronRight />
                  ) : selected ? (
                    <Check />
                  ) : null}
                </button>
              );
            })}
          </div>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}

function ReviewDisplayMenu({
  display,
  onChange,
}: {
  display: Display;
  onChange: (value: Display) => void;
}) {
  const { t } = useI18n();
  const toggle = (property: string) =>
    onChange({
      ...display,
      properties: display.properties.includes(property)
        ? display.properties.filter((item) => item !== property)
        : [...display.properties, property],
    });
  return (
    <Popover.Root>
      <Popover.Trigger asChild>
        <button
          className="reviews-icon-button"
          aria-label={t("Display options")}
        >
          <Settings2 />
        </button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content className="reviews-display" align="end" sideOffset={5}>
          <SelectLine
            label={t("Grouping")}
            value={display.grouping}
            options={["focus", "status", "repository"]}
            onChange={(grouping) =>
              onChange({
                ...display,
                grouping: grouping as Display["grouping"],
              })
            }
          />
          <SelectLine
            label={t("Ordering")}
            value={display.ordering}
            options={["importance", "updated", "opened"]}
            onChange={(ordering) =>
              onChange({
                ...display,
                ordering: ordering as Display["ordering"],
              })
            }
          />
          <SelectLine
            label={t("Closed reviews")}
            value={display.closed}
            options={["day", "week", "month", "all"]}
            onChange={(closed) =>
              onChange({ ...display, closed: closed as Display["closed"] })
            }
          />
          <CheckLine
            label={t("Show drafts")}
            checked={display.showDrafts}
            onChange={(showDrafts) => onChange({ ...display, showDrafts })}
          />
          <CheckLine
            label={t("Show GitHub team reviews")}
            checked={display.showTeams}
            onChange={(showTeams) => onChange({ ...display, showTeams })}
          />
          <h4>{t("Display properties")}</h4>
          <div className="review-property-pills">
            {["repository", "id", "author", "opened", "status", "quick"].map(
              (value) => (
                <button
                  aria-pressed={display.properties.includes(value)}
                  key={value}
                  onClick={() => toggle(value)}
                >
                  {t(
                    value === "quick"
                      ? "Quick to review"
                      : value[0].toUpperCase() + value.slice(1),
                  )}
                </button>
              ),
            )}
          </div>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}

function SelectLine({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: string;
  options: string[];
  onChange: (value: string) => void;
}) {
  const { t } = useI18n();
  return (
    <label className="review-select-line">
      <span>{label}</span>
      <SelectControl
        label={label}
        value={value}
        onChange={onChange}
        options={options.map((option) => ({
          value: option,
          label: t(option[0].toUpperCase() + option.slice(1)),
        }))}
      />
    </label>
  );
}
function CheckLine({
  label,
  checked,
  onChange,
}: {
  label: string;
  checked: boolean;
  onChange: (value: boolean) => void;
}) {
  return (
    <label className="review-check-line">
      <span>{label}</span>
      <Toggle checked={checked} label={label} onChange={onChange} />
    </label>
  );
}

function ReviewActions({
  review,
  busy,
  onMutate,
  onOpenPicker,
  onQuickApprove,
}: {
  review: CodeReview;
  busy?: boolean;
  onMutate: (input: Parameters<typeof updateReview>[1]) => Promise<void>;
  onOpenPicker: (value: "reviewers" | "issues") => void;
  onQuickApprove: () => Promise<void>;
}) {
  const { t } = useI18n();
  const [approving, setApproving] = useState(false);
  const closed = review.status === "merged" || review.status === "closed";
  const quickApprove = async () => {
    setApproving(true);
    try {
      await onQuickApprove();
    } finally {
      setApproving(false);
    }
  };
  return (
    <Popover.Root>
      <Popover.Trigger asChild>
        <button
          aria-label={t(`${reviewProviderNoun(review.provider)} actions`)}
        >
          <MoreHorizontal />
        </button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content
          className="review-actions-menu"
          align="end"
          sideOffset={5}
          onKeyDown={commandKeyDown}
        >
          <a href={review.url} target="_blank" rel="noreferrer">
            <ExternalLink />
            {t(`Open in ${review.provider === "github" ? "GitHub" : "GitLab"}`)}
          </a>
          <button onClick={() => void onMutate({ favorite: !review.favorite })}>
            <Star />
            {t(review.favorite ? "Unfavorite" : "Favorite")}
          </button>
          <hr />
          <button
            onClick={() => void navigator.clipboard.writeText(location.href)}
          >
            <Copy />
            {t("Copy URL")}
          </button>
          <button
            onClick={() =>
              void navigator.clipboard.writeText(review.headBranch)
            }
          >
            <GitBranch />
            {t("Copy branch name")}
          </button>
          <hr />
          <button onClick={() => onOpenPicker("issues")}>
            <Link2 />
            {t("Link issue")}
          </button>
          <button onClick={() => onOpenPicker("reviewers")}>
            <Users />
            {t("Select reviewers")}
          </button>
          <button disabled={closed || busy || approving} onClick={() => void quickApprove()}>
            <CheckCircle2 />
            {t("Quick approve")}
          </button>
          <hr />
          {closed ? (
            <button onClick={() => void onMutate({ status: "open" })}>
              <ReviewProviderIcon provider={review.provider} />
              {t(`Reopen ${reviewProviderNoun(review.provider)}`)}
            </button>
          ) : (
            <>
              <button
                onClick={() => void onMutate({ branchState: "upToDate" })}
              >
                <GitBranch />
                {t("Update branch")}
              </button>
              <button onClick={() => void onMutate({ draft: !review.draft })}>
                <ReviewProviderIcon provider={review.provider} />
                {t(review.draft ? "Mark ready for review" : "Convert to draft")}
              </button>
              <button onClick={() => void onMutate({ status: "merged" })}>
                <GitMerge />
                {t(`Merge ${reviewProviderNoun(review.provider)}`)}
              </button>
              <button
                className="danger"
                onClick={() => void onMutate({ status: "closed" })}
              >
                <X />
                {t(`Close ${reviewProviderNoun(review.provider)}`)}
              </button>
            </>
          )}
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}

function ChecksMenu({ checks }: { checks: CodeReview["checks"] }) {
  const { t } = useI18n();
  const passed = checks.every(
    (item) => item.status === "passed" || item.status === "skipped",
  );
  return (
    <Popover.Root>
      <Popover.Trigger asChild>
        <button className={passed ? "passed" : "failed"}>
          <CheckCircle2 />
          {t(passed ? "All passed" : "Checks need attention")}
        </button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content
          className="review-checks-menu"
          align="start"
          sideOffset={5}
        >
          <h3>
            {t("Checks")}
            <span>{checks.length}</span>
          </h3>
          {checks.map((check) => (
            <div key={check.id}>
              <CheckCircle2 className={check.status} />
              <span data-i18n-ignore>{check.name}</span>
              <small>{t(check.status)}</small>
            </div>
          ))}
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}

function SubmitReviewDialog({
  open,
  busy,
  review,
  onClose,
  onSubmit,
}: {
  open: boolean;
  busy: boolean;
  review: CodeReview;
  onClose: () => void;
  onSubmit: (
    decision: "approve" | "comment" | "requestChanges",
    body: string,
  ) => Promise<void>;
}) {
  const { t } = useI18n();
  const [decision, setDecision] = useState<
      "approve" | "comment" | "requestChanges"
    >("comment"),
    [body, setBody] = useState("");
  return (
    <Dialog open={open} onOpenChange={(value) => !value && onClose()}>
      <DialogContent
        className="submit-review-dialog"
        onInteractOutside={(event) => event.preventDefault()}
        onPointerDownOutside={(event) => event.preventDefault()}
      >
        <DialogTitle>{t("Submit review")}</DialogTitle>
        <p data-i18n-ignore>{review.title}</p>
        <textarea
          autoFocus
          aria-label={t("Review summary")}
          placeholder={t("Leave a review summary…")}
          value={body}
          onChange={(event) => setBody(event.target.value)}
        />
        <div role="radiogroup" aria-label={t("Review decision")}>
          {(["comment", "approve", "requestChanges"] as const).map((value) => (
            <button
              type="button"
              role="radio"
              aria-checked={decision === value}
              key={value}
              onClick={(event) => {
                event.preventDefault();
                event.stopPropagation();
                setDecision(value);
              }}
            >
              <span>{decision === value && <i />}</span>
              {t(
                value === "comment"
                  ? "Comment"
                  : value === "approve"
                    ? "Approve"
                    : "Request changes",
              )}
            </button>
          ))}
        </div>
        <footer>
          <button type="button" onClick={onClose}>
            {t("Cancel")}
          </button>
          <button
            type="button"
            className="primary"
            disabled={busy}
            onClick={() => void onSubmit(decision, body)}
          >
            {t("Submit review")}
          </button>
        </footer>
      </DialogContent>
    </Dialog>
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
    onSelect: (option) =>
      setSelected((current) =>
        current.includes(option.id)
          ? current.filter((id) => id !== option.id)
          : [...current, option.id],
      ),
  });
  return (
    <Dialog open onOpenChange={(value) => !value && onClose()}>
      <DialogContent
        className="review-picker-dialog"
        onInteractOutside={(event) => event.preventDefault()}
        onPointerDownOutside={(event) => event.preventDefault()}
      >
        <DialogTitle>
          {t(type === "reviewers" ? "Select reviewers" : "Link issues")}
        </DialogTitle>
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
              user =
                type === "reviewers"
                  ? data.users.find((item) => item.id === option.id)
                  : undefined;
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
      <Sparkles />
      <h2>{t("Review guide")}</h2>
      <p>
        {t(
          "Start with the highest-impact files, then confirm tests and issue scope.",
        )}
      </p>
      {review.files.map((file, index) => (
        <article key={file.path}>
          <span>{index + 1}</span>
          <div>
            <strong data-i18n-ignore>{file.path}</strong>
            <p>
              {t(
                index === 0
                  ? "Check interaction behavior and keyboard accessibility."
                  : "Confirm regression coverage and failure states.",
              )}
            </p>
          </div>
        </article>
      ))}
    </div>
  );
}
function ReviewChanges({ review }: { review: CodeReview }) {
  const { t } = useI18n();
  return (
    <div className="review-changes">
      <header>
        <h2>{t("Files changed")}</h2>
        <span>{review.files.length}</span>
      </header>
      {review.files.map((file) => (
        <article key={file.path}>
          <header>
            <FileCode2 />
            <strong data-i18n-ignore>{file.path}</strong>
            <b>+{file.additions}</b>
            <i>-{file.deletions}</i>
          </header>
          <pre data-i18n-ignore>{file.patch}</pre>
        </article>
      ))}
    </div>
  );
}
function statusLabel(status: string) {
  return status === "inReview"
    ? "In review"
    : status[0].toUpperCase() + status.slice(1);
}
function eventText(type: string, noun = "pull request") {
  return type === "opened"
    ? "opened the pull request"
    : type === "review_requested"
      ? "requested a review from"
      : type === "approved"
        ? "approved these changes"
        : type === "changes_requested"
          ? "requested changes"
          : type === "commented" || type === "review_commented"
            ? "commented"
            : type === "merged"
              ? `merged the ${noun}`
              : type === "closed"
                ? `closed the ${noun}`
                : type === "updated"
                  ? `updated the ${noun}`
                  : "updated the review";
}
function relative(value: string, t: (source: string) => string) {
  const days = Math.max(
    0,
    Math.floor((Date.now() - new Date(value).getTime()) / 86400000),
  );
  return days < 1
    ? t("Today")
    : days < 7
      ? `${days}d`
      : `${Math.floor(days / 7)}w`;
}
function commandKeyDown(event: React.KeyboardEvent<HTMLElement>) {
  if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
  event.preventDefault();
  const items = [
    ...event.currentTarget.querySelectorAll<HTMLElement>(
      "button:not(:disabled),a[href],[role=option]:not([aria-disabled=true])",
    ),
  ].filter((item) => item.offsetParent !== null);
  if (!items.length) return;
  const current = items.indexOf(document.activeElement as HTMLElement),
    step = event.key === "ArrowDown" ? 1 : -1;
  items[(current + step + items.length) % items.length]?.focus();
}
