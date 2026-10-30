import { render as renderUI, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from 'react-router-dom';
import type { ReactNode } from 'react';
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";

import { BackgroundReviewProcessor } from "@/lib/background-review-processor";

import { I18nProvider } from "@/i18n/i18n";
import { makeBootstrap, viewer, teammate } from "@/test/fixtures";
import type { CodeReview } from "@/types/flow";

const api = vi.hoisted(() => ({
  commentOnReview: vi.fn(),
  importReviews: vi.fn(),
  syncReview: vi.fn(),
  resolveReviewThread: vi.fn(),
  submitReview: vi.fn(),
  updateReview: vi.fn(),
}));
vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  ...api,
}));

import { ReviewsPage } from "./reviews-page";
const render = (ui: ReactNode) => renderUI(<MemoryRouter>{ui}</MemoryRouter>);
beforeEach(() => {
  localStorage.clear();
  BackgroundReviewProcessor.resetForTests();
});

const review: CodeReview = {
  id: "review-1",
  slugId: "review-one",
  provider: "github",
  externalId: "github-1",
  number: 33,
  title: "Improve release workflow",
  description: "Align the release workflow and add coverage.",
  status: "open",
  repositoryOwner: "flow",
  repositoryName: "flow",
  url: "https://github.com/flow/flow/pull/33",
  author: teammate,
  reviewerIds: [viewer.id],
  teamReviewers: [],
  issueIds: [],
  baseBranch: "main",
  headBranch: "feature/release",
  branchState: "upToDate",
  additions: 1,
  deletions: 1,
  commitCount: 1,
  checks: [],
  files: [],
  events: [
    {
      id: "opened",
      type: "opened",
      actor: teammate,
      createdAt: "2026-09-01T00:00:00.000Z",
    },
    {
      id: "requested",
      type: "review_requested",
      body: viewer.displayName,
      actor: teammate,
      createdAt: "2026-09-01T00:01:00.000Z",
    },
  ],
  favorite: false,
  draft: false,
  quickToReview: false,
  createdAt: "2026-09-01T00:00:00.000Z",
  updatedAt: "2026-09-01T00:01:00.000Z",
};

it("renders provider review activity and submits the compact comment composer", async () => {
  const user = userEvent.setup();
  api.commentOnReview.mockResolvedValue(review);
  render(
    <I18nProvider>
      <ReviewsPage
        data={makeBootstrap({
          viewer,
          users: [viewer, teammate],
          reviews: [review],
          userSettings: {},
          integrationConnections: [
            { id: "github", provider: "github" },
          ] as never[],
        })}
        view="for-you"
        review={review}
        onNavigate={vi.fn()}
        onReload={vi.fn().mockResolvedValue(undefined)}
        onOpenSidebar={vi.fn()}
      />
    </I18nProvider>,
  );

  expect(screen.getByText(/Opened by/)).toBeVisible();
  expect(screen.getByText(/with 1 commit/)).toBeVisible();
  expect(screen.getByText(/requested review from/)).toBeVisible();
  expect(screen.getByRole("complementary", { name: "Review details" })).toBeVisible();
  expect(screen.getByRole("button", { name: "Copy URL" })).toBeVisible();
  expect(
    screen.getByRole("button", { name: "Review diff in full window" }),
  ).toBeVisible();

  const comment = screen.getByRole("textbox", { name: "Comment" });
  await user.type(comment, "Looks good");
  await user.click(screen.getByRole("button", { name: "Submit comment" }));
  expect(api.commentOnReview).toHaveBeenCalledWith(review.id, "Looks good");
});

it("switches the review detail into the full-window two-column layout", async () => {
  const user = userEvent.setup();
  render(
    <I18nProvider>
      <ReviewsPage
        data={makeBootstrap({
          viewer,
          users: [viewer, teammate],
          reviews: [review],
          userSettings: {},
          integrationConnections: [
            { id: "github", provider: "github" },
          ] as never[],
        })}
        view="for-you"
        review={review}
        onNavigate={vi.fn()}
        onReload={vi.fn().mockResolvedValue(undefined)}
        onOpenSidebar={vi.fn()}
      />
    </I18nProvider>,
  );
  await user.click(
    screen.getByRole("button", { name: "Review diff in full window" }),
  );
  expect(
    document
      .querySelector(".reviews-workspace")
      ?.classList.contains("is-full-window"),
  ).toBe(true);
  expect(
    screen.getByRole("button", { name: "Exit full window" }),
  ).toHaveAttribute("aria-pressed", "true");
  expect(
    screen.getByRole("complementary", { name: "Review details" }),
  ).toBeVisible();
});

it("uses merge-request identifiers and actions for GitLab reviews", async () => {
  const gitlabReview: CodeReview = {
    ...review,
    id: "review-gitlab",
    slugId: "review-gitlab",
    provider: "gitlab",
    externalId: "44001",
    number: 27,
    repositoryOwner: "acme",
    repositoryName: "platform/api",
    url: "https://gitlab.example.com/acme/platform/api/-/merge_requests/27",
  };
  render(
    <I18nProvider>
      <ReviewsPage
        data={makeBootstrap({
          viewer,
          users: [viewer, teammate],
          reviews: [gitlabReview],
          userSettings: {},
          integrationConnections: [
            { id: "gitlab", provider: "gitlab" },
          ] as never[],
        })}
        view="for-you"
        review={gitlabReview}
        onNavigate={vi.fn()}
        onReload={vi.fn().mockResolvedValue(undefined)}
        onOpenSidebar={vi.fn()}
      />
    </I18nProvider>,
  );

  expect(screen.getByRole("heading", { level: 2, name: "Improve release workflow" })).toBeVisible();
  expect(screen.getByText("platform/api!27")).toBeVisible();
  expect(screen.getByRole("button", { name: "merge request actions" })).toBeVisible();
});

it("renders split and unified diff rows and posts an inline comment", async () => {
  const user = userEvent.setup();
  api.commentOnReview.mockResolvedValue(review);
  const reviewWithDiff: CodeReview = {
    ...review,
    files: [
      {
        path: "src/index.ts",
        status: "modified",
        additions: 1,
        deletions: 1,
        patch: "@@ -1,2 +1,2 @@\n const value = 1;\n-const answer = value;\n+const answer = value + 1;",
      },
    ],
  };
  render(
    <I18nProvider>
      <ReviewsPage
        data={makeBootstrap({
          viewer,
          users: [viewer, teammate],
          reviews: [reviewWithDiff],
          userSettings: {},
          integrationConnections: [{ id: "github", provider: "github" }] as never[],
        })}
        view="for-you"
        review={reviewWithDiff}
        tab="changes"
        onNavigate={vi.fn()}
        onReload={vi.fn().mockResolvedValue(undefined)}
        onOpenSidebar={vi.fn()}
      />
    </I18nProvider>,
  );

  // Unified is the default layout; Split lives in the diff display options.
  await user.click(screen.getByRole("button", { name: "Display options" }));
  expect(screen.getByRole("button", { name: "Unified" })).toHaveAttribute("aria-pressed", "true");
  // Beside the review list there is no room for two columns: Split is disabled and the diff stays unified.
  expect(screen.getByRole("button", { name: "Split" })).toHaveAttribute("aria-disabled", "true");
  await user.click(screen.getByRole("button", { name: "Split" }));
  expect(screen.getByRole("button", { name: "Split" })).toHaveAttribute("aria-pressed", "false");
  expect(screen.getByRole("button", { name: "Unified" })).toHaveAttribute("aria-pressed", "true");
  await waitFor(() =>
    expect(
      screen.getByText(
        (_text, element) =>
          element?.tagName === "CODE" &&
          element.textContent === "const answer = value + 1;",
      ),
    ).toBeVisible(),
  );
  await user.click(screen.getByRole("button", { name: "Unified" }));
  expect(screen.getByRole("button", { name: "Unified" })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await user.click(screen.getAllByRole("button", { name: /Comment on line/ })[0]);
  await user.type(screen.getByRole("textbox", { name: "Inline comment" }), "Please add a test");
  await user.click(screen.getByRole("button", { name: "Comment" }));
  await waitFor(() =>
    expect(api.commentOnReview).toHaveBeenCalledWith(review.id, "Please add a test", {
      path: "src/index.ts",
      line: 1,
      side: "RIGHT",
    }),
  );
});

it("reports quick-approve failures and unlocks the action", async () => {
  const user = userEvent.setup();
  api.submitReview.mockRejectedValueOnce(new Error("provider unavailable"));
  render(
    <I18nProvider>
      <ReviewsPage
        data={makeBootstrap({
          viewer,
          users: [viewer, teammate],
          reviews: [review],
          userSettings: {},
          integrationConnections: [{ id: "github", provider: "github" }] as never[],
        })}
        view="for-you"
        review={review}
        onNavigate={vi.fn()}
        onReload={vi.fn().mockResolvedValue(undefined)}
        onOpenSidebar={vi.fn()}
      />
    </I18nProvider>,
  );

  await user.click(screen.getByRole("button", { name: "pull request actions" }));
  await user.click(await screen.findByRole("menuitem", { name: /Quick approve/ }));
  await waitFor(() => expect(api.submitReview).toHaveBeenCalledWith(review.id, { decision: "approve", body: "" }));
  await user.click(screen.getByRole("button", { name: "pull request actions" }));
  expect(await screen.findByRole("menuitem", { name: /Quick approve/ })).not.toHaveAttribute("data-disabled");
});

it("keeps a review comment available when posting fails", async () => {
  const user = userEvent.setup();
  api.commentOnReview.mockRejectedValueOnce(new Error("network unavailable"));
  render(
    <I18nProvider>
      <ReviewsPage
        data={makeBootstrap({
          viewer,
          users: [viewer, teammate],
          reviews: [review],
          userSettings: {},
          integrationConnections: [{ id: "github", provider: "github" }] as never[],
        })}
        view="for-you"
        review={review}
        onNavigate={vi.fn()}
        onReload={vi.fn().mockResolvedValue(undefined)}
        onOpenSidebar={vi.fn()}
      />
    </I18nProvider>,
  );

  const comment = screen.getByRole("textbox", { name: "Comment" });
  await user.type(comment, "Please check the migration");
  await user.click(screen.getByRole("button", { name: "Submit comment" }));
  await waitFor(() => expect(api.commentOnReview).toHaveBeenCalledWith(review.id, "Please check the migration"));
  expect(comment).toHaveValue("Please check the migration");
});

function renderReviewList(reviews: CodeReview[], view: 'for-you' | 'created' = 'for-you') {
  return render(<I18nProvider><ReviewsPage data={makeBootstrap({ viewer, users: [viewer, teammate], reviews, teamMembers: [], userSettings: {}, integrationConnections: [{ id: 'github', provider: 'github' }] as never[] })} view={view} onNavigate={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)} onOpenSidebar={vi.fn()}/></I18nProvider>);
}

it('imports open pull and merge requests from the list with Sync now', async () => {
  const user = userEvent.setup();
  const onReload = vi.fn().mockResolvedValue(undefined);
  api.importReviews.mockResolvedValue({ repositories: 2, imported: 3, updated: 1, closed: 0, errors: [], startedAt: '2026-10-20T00:00:00Z', finishedAt: '2026-10-20T00:00:01Z' });
  render(<I18nProvider><ReviewsPage data={makeBootstrap({ viewer, viewerRole: 'admin', users: [viewer, teammate], reviews: [review], teamMembers: [], userSettings: {}, integrationConnections: [{ id: 'gitlab', provider: 'gitlab' }] as never[] })} view="for-you" onNavigate={vi.fn()} onReload={onReload} onOpenSidebar={vi.fn()}/></I18nProvider>);
  await user.click(screen.getByRole('button', { name: 'Sync now' }));
  await waitFor(() => expect(onReload).toHaveBeenCalled());
  expect(api.importReviews).toHaveBeenCalledTimes(1);
  expect(screen.getByRole('button', { name: 'Sync now' })).not.toBeDisabled();
});

it('offers Sync now only to workspace admins', () => {
  render(<I18nProvider><ReviewsPage data={makeBootstrap({ viewer, viewerRole: 'member', users: [viewer, teammate], reviews: [review], teamMembers: [], userSettings: {}, integrationConnections: [{ id: 'gitlab', provider: 'gitlab' }] as never[] })} view="for-you" onNavigate={vi.fn()} onReload={vi.fn()} onOpenSidebar={vi.fn()}/></I18nProvider>);
  expect(screen.queryByRole('button', { name: 'Sync now' })).toBeNull();
});

it('opens filter submenus on hover, supports multiple statuses, and clears actual list filters', async () => {
  const user = userEvent.setup({ skipHover: true });
  renderReviewList([review, { ...review, id: 'approved', title: 'Approved change', status: 'approved' }, { ...review, id: 'draft', title: 'Draft change', draft: true }]);
  await user.click(screen.getByRole('button', { name: 'Add filter' }));
  await user.hover(screen.getByRole('menuitem', { name: 'Status' }));
  await user.click(await screen.findByRole('menuitemcheckbox', { name: /^Open/ }));
  await user.click(screen.getByRole('menuitemcheckbox', { name: /^Approved/ }));
  await user.keyboard('{Escape}{Escape}');
  expect(document.querySelectorAll('.review-list-row')).toHaveLength(2);
  expect(screen.queryByText('Draft change')).not.toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: 'Clear all filters' }));
  expect(document.querySelectorAll('.review-list-row')).toHaveLength(3);
});

it('applies display properties, grouping, closed range, and resets view defaults', async () => {
  const user = userEvent.setup();
  renderReviewList([review, { ...review, id: 'draft', title: 'Draft change', draft: true }]);
  expect(document.querySelector('.review-list-row__metadata')).not.toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: 'Display options' }));
  await user.click(screen.getByRole('button', { name: 'ID' }));
  expect(document.querySelector('.review-list-row__metadata')).toHaveTextContent('#33');
  await user.click(screen.getByRole('checkbox', { name: 'Show drafts' }));
  expect(document.querySelectorAll('.review-list-row')).toHaveLength(1);
  await user.click(screen.getByRole('combobox', { name: 'Grouping' }));
  await user.click(screen.getByRole('option', { name: 'No grouping' }));
  expect(document.querySelector('.review-group-heading')).not.toBeInTheDocument();
  expect(screen.getByRole('combobox', { name: 'Ordering' })).toHaveTextContent('Opened');
  await user.click(screen.getByRole('combobox', { name: 'Closed reviews' }));
  expect(screen.getByRole('option', { name: 'None' })).toBeInTheDocument();
  await user.click(screen.getByRole('option', { name: 'None' }));
  await user.click(screen.getByRole('button', { name: 'Reset to view default' }));
  expect(document.querySelectorAll('.review-list-row')).toHaveLength(2);
  expect(screen.getByRole('combobox', { name: 'Grouping' })).toHaveTextContent('Focus');
  expect(document.querySelector('.review-list-row__metadata')).not.toBeInTheDocument();
});

it('uses Created view defaults and still offers Focus without GitHub team review controls', async () => {
  const user = userEvent.setup();
  renderReviewList([{ ...review, author: viewer }], 'created');
  await user.click(screen.getByRole('button', { name: 'Display options' }));
  expect(screen.queryByRole('checkbox', { name: 'Show GitHub team reviews' })).not.toBeInTheDocument();
  expect(screen.getByRole('combobox', { name: 'Closed reviews' })).toHaveTextContent('Past week');
  expect(screen.getByRole('combobox', { name: 'Grouping' })).toHaveTextContent('Status');
  await user.click(screen.getByRole('combobox', { name: 'Grouping' }));
  expect(screen.getByRole('option', { name: 'Focus' })).toBeInTheDocument();
});

function renderDetail(item: CodeReview, tab: "overview" | "changes" = "overview") {
  return render(<I18nProvider><ReviewsPage data={makeBootstrap({ viewer, users: [viewer, teammate], reviews: [item], userSettings: {}, integrationConnections: [{ id: 'github', provider: 'github' }] as never[] })} view="for-you" review={item} tab={tab} onNavigate={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)} onOpenSidebar={vi.fn()}/></I18nProvider>);
}

it('groups For you by focus with provider labels, collapses completed work and shows compact ages', async () => {
  const user = userEvent.setup();
  const now = Date.now();
  renderReviewList([
    { ...review, id: 'mine', title: 'Add `mod(a, b)` helper', author: viewer, reviewerIds: [], createdAt: new Date(now - 16 * 3_600_000).toISOString() },
    { ...review, id: 'done', title: 'Old change', author: viewer, status: 'closed', closedAt: new Date(now - 3_600_000).toISOString() },
  ]);
  expect(screen.getByRole('button', { name: /Ready to merge/ })).toHaveAttribute('aria-expanded', 'true');
  expect(document.querySelector('.review-title-code')).toHaveTextContent('mod(a, b)');
  expect(screen.getByText('16h')).toBeVisible();
  const completed = screen.getByRole('button', { name: /Completed/ });
  expect(completed).toHaveAttribute('aria-expanded', 'false');
  expect(completed).toHaveTextContent('1');
  expect(screen.queryByText('Old change')).not.toBeInTheDocument();
  await user.click(completed);
  expect(screen.getByText('Old change')).toBeVisible();
});

it('submits a review from the popover and requires a summary to request changes', async () => {
  const user = userEvent.setup();
  api.submitReview.mockResolvedValue(review);
  renderDetail(review);
  await user.click(screen.getByRole('button', { name: 'Submit review' }));
  expect(screen.getByRole('button', { name: /Request changes/ })).toBeDisabled();
  await user.type(screen.getByRole('textbox', { name: 'Review summary' }), 'Looks right');
  await user.click(screen.getByRole('button', { name: /Approve/ }));
  await waitFor(() => expect(api.submitReview).toHaveBeenCalledWith(review.id, { decision: 'approve', body: 'Looks right' }));
});

it('opens the submit review popover with the R then S sequence', async () => {
  const user = userEvent.setup();
  renderDetail(review);
  await user.keyboard('rs');
  expect(await screen.findByRole('textbox', { name: 'Review summary' })).toBeVisible();
});

it('shows author actions instead of review submission on a draft the viewer opened', () => {
  renderDetail({ ...review, author: viewer, reviewerIds: [], draft: true });
  expect(screen.getByRole('button', { name: 'Ready for review' })).toBeVisible();
  expect(screen.queryByRole('button', { name: 'Submit review' })).not.toBeInTheDocument();
});

it('renders inline threads with suggestions, replies to the thread and resolves it', async () => {
  const user = userEvent.setup();
  api.commentOnReview.mockResolvedValue(review);
  api.resolveReviewThread.mockResolvedValue(review);
  const withThread: CodeReview = {
    ...review,
    files: [{ path: 'src/index.ts', status: 'modified', additions: 1, deletions: 1, patch: '@@ -1,2 +1,2 @@\n const value = 1;\n-const answer = value;\n+const answer = value + 1;' }],
    events: [
      ...review.events,
      { id: 'c1', type: 'commented', body: 'Prefer a constant\n\n```suggestion\nconst answer = 2;\n```', path: 'src/index.ts', line: 2, actor: teammate, createdAt: '2026-09-01T01:00:00.000Z' },
      { id: 'c2', type: 'commented', body: 'Will do', parentId: 'c1', path: 'src/index.ts', line: 2, actor: viewer, createdAt: '2026-09-01T02:00:00.000Z' },
    ],
  };
  renderDetail(withThread, 'changes');
  expect(await screen.findByText('Suggested change')).toBeVisible();
  expect(screen.getByText((_text, element) => element?.classList.contains('review-code') === true && element.textContent === 'const answer = 2;')).toBeVisible();
  expect(screen.getByText('Will do')).toBeVisible();
  await user.type(screen.getByRole('textbox', { name: 'Reply' }), 'Done');
  await user.click(screen.getByRole('button', { name: 'Submit reply' }));
  await waitFor(() => expect(api.commentOnReview).toHaveBeenCalledWith(review.id, 'Done', { parentId: 'c1' }));
  await user.click(screen.getByRole('button', { name: 'Resolve thread' }));
  await waitFor(() => expect(api.resolveReviewThread).toHaveBeenCalledWith(review.id, 'c1', true));
});

it('shows conversation comments and reviews as cards with replies and a resolved-thread summary', async () => {
  const user = userEvent.setup();
  api.commentOnReview.mockResolvedValue(review);
  renderDetail({
    ...review,
    events: [
      ...review.events,
      { id: 'issue-comment', type: 'commented', body: 'Looks good overall', actor: teammate, createdAt: '2026-09-01T01:00:00.000Z' },
      { id: 'summary', type: 'review_commented', body: 'Review notes', actor: teammate, createdAt: '2026-09-01T02:00:00.000Z' },
      { id: 'code-1', type: 'commented', body: 'Inline only', path: 'src/a.ts', line: 3, actor: teammate, createdAt: '2026-09-01T02:00:00.000Z' },
      { id: 'code-2', type: 'commented', body: 'Nit', path: 'src/a.ts', line: 9, resolved: true, actor: teammate, createdAt: '2026-09-01T02:00:00.000Z' },
    ],
  });
  expect(screen.getByText('Looks good overall')).toBeVisible();
  expect(screen.getByText('Reviewed')).toBeVisible();
  expect(screen.getByRole('link', { name: /1 of 2 code comments resolved/ })).toBeVisible();
  expect(screen.queryByText('Inline only')).not.toBeInTheDocument();
  await user.type(screen.getAllByRole('textbox', { name: 'Reply' })[0], 'Thanks{Enter}');
  await waitFor(() => expect(api.commentOnReview).toHaveBeenCalledWith(review.id, 'Thanks', { parentId: 'issue-comment' }));
});

it('describes renamed files without content changes and marks added and deleted files', async () => {
  renderDetail({
    ...review,
    files: [
      { path: 'lib/arithmetic.js', previousPath: 'src/math.js', status: 'renamed', additions: 0, deletions: 0, patch: '' },
      { path: 'lib/new.js', status: 'added', additions: 1, deletions: 0, patch: '@@ -0,0 +1 @@\n+export const a = 1;' },
      { path: 'src/old.test.js', status: 'deleted', additions: 0, deletions: 1, patch: '@@ -1 +0,0 @@\n-old();' },
    ],
  }, 'changes');
  expect(await screen.findByText('File renamed and moved')).toBeVisible();
  expect(document.querySelector('[data-diff-file="lib/new.js"] .review-file-name')).toHaveAttribute('data-file-status', 'added');
  expect(document.querySelector('[data-diff-file="src/old.test.js"] .review-file-name')).toHaveAttribute('data-file-status', 'deleted');
});

it("comments on removed lines, shows left-side threads and retries changes that did not reach GitHub", async () => {
  const user = userEvent.setup();
  const reviewWithThreads: CodeReview = {
    ...review,
    syncedAt: "2026-09-01T00:02:00.000Z",
    files: [
      {
        path: "src/index.ts",
        status: "modified",
        additions: 1,
        deletions: 1,
        patch: "@@ -1,2 +1,2 @@\n const value = 1;\n-const answer = value;\n+const answer = value + 1;",
      },
      { path: "assets/huge.json", status: "modified", additions: 9000, deletions: 3, patch: "", patchUnavailable: true },
    ],
    events: [
      ...review.events,
      {
        id: "left-thread",
        type: "commented",
        body: "Why was this removed?",
        path: "src/index.ts",
        line: 2,
        side: "LEFT",
        actor: teammate,
        createdAt: "2026-09-01T00:03:00.000Z",
        remote: { provider: "github", kind: "review_comment", id: "11" },
        postedAs: { provider: "github", login: "teammate-gh", via: "member" },
      },
      {
        id: "failed-reply",
        type: "commented",
        body: "It moved",
        parentId: "left-thread",
        path: "src/index.ts",
        line: 2,
        side: "LEFT",
        actor: viewer,
        createdAt: "2026-09-01T00:04:00.000Z",
        sync: { state: "failed", op: "create", error: "GitHub denied the change", attemptedAt: "2026-09-01T00:04:00.000Z" },
      },
    ],
  };
  api.syncReview.mockResolvedValue(reviewWithThreads);
  api.commentOnReview.mockResolvedValue(reviewWithThreads);
  render(
    <I18nProvider>
      <ReviewsPage
        data={makeBootstrap({
          viewer,
          users: [viewer, teammate],
          reviews: [reviewWithThreads],
          userSettings: {},
          integrationConnections: [{ id: "github", provider: "github" }] as never[],
        })}
        view="for-you"
        review={reviewWithThreads}
        tab="changes"
        onNavigate={vi.fn()}
        onReload={vi.fn().mockResolvedValue(undefined)}
        onOpenSidebar={vi.fn()}
      />
    </I18nProvider>,
  );
  // Opening the review refreshes it from the code host (not forced).
  await waitFor(() => expect(api.syncReview).toHaveBeenCalledWith(reviewWithThreads.id, false));
  // The left-side thread renders under the removed line, with the failed reply.
  await waitFor(() => expect(screen.getByText("Why was this removed?")).toBeVisible());
  expect(screen.getByText("Not synced to GitHub")).toBeVisible();
  // A written-back comment says which code host identity it was posted as.
  expect(screen.getByText("@teammate-gh")).toBeVisible();
  expect(screen.getByText("This diff is too large to display")).toBeVisible();
  await user.click(screen.getByRole("button", { name: "Retry" }));
  await waitFor(() => expect(api.syncReview).toHaveBeenCalledWith(reviewWithThreads.id, true));

  await user.click(screen.getByRole("button", { name: /Comment on removed line 2/ }));
  await user.type(screen.getByRole("textbox", { name: "Inline comment" }), "Keep this");
  await user.click(screen.getByRole("button", { name: "Comment" }));
  await waitFor(() =>
    expect(api.commentOnReview).toHaveBeenCalledWith(reviewWithThreads.id, "Keep this", { path: "src/index.ts", line: 2, side: "LEFT" }),
  );
});
