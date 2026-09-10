import { render as renderUI, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from 'react-router-dom';
import type { ReactNode } from 'react';
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";

import { I18nProvider } from "@/i18n/i18n";
import { makeBootstrap, viewer, teammate } from "@/test/fixtures";
import type { CodeReview } from "@/types/flow";

const api = vi.hoisted(() => ({
  commentOnReview: vi.fn(),
  submitReview: vi.fn(),
  updateReview: vi.fn(),
}));
vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  ...api,
}));

import { ReviewsPage } from "./reviews-page";
const render = (ui: ReactNode) => renderUI(<MemoryRouter>{ui}</MemoryRouter>);
beforeEach(() => localStorage.clear());

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

  expect(screen.getByText("Opened by")).toBeVisible();
  expect(screen.getByText(/with 1 commit/)).toBeVisible();
  expect(screen.getByText("requested review from")).toBeVisible();
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

  expect(screen.getByText("!27 Improve release workflow")).toBeVisible();
  expect(screen.getByText("platform/api!27")).toBeVisible();
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

  expect(screen.getByRole("button", { name: "Split" })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  expect(screen.getByText((_text,element)=>element?.tagName==='CODE'&&element.textContent==='const answer = value + 1;')).toBeVisible();
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
  const approve = screen.getByRole("button", { name: "Quick approve" });
  await user.click(approve);
  await waitFor(() => expect(api.submitReview).toHaveBeenCalledWith(review.id, { decision: "approve", body: "" }));
  expect(approve).not.toBeDisabled();
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

it('uses Created view defaults and omits Focus and GitHub team review controls', async () => {
  const user = userEvent.setup();
  renderReviewList([{ ...review, author: viewer }], 'created');
  await user.click(screen.getByRole('button', { name: 'Display options' }));
  expect(screen.queryByRole('checkbox', { name: 'Show GitHub team reviews' })).not.toBeInTheDocument();
  expect(screen.getByRole('combobox', { name: 'Closed reviews' })).toHaveTextContent('Past week');
  await user.click(screen.getByRole('combobox', { name: 'Grouping' }));
  expect(screen.queryByRole('option', { name: 'Focus' })).not.toBeInTheDocument();
});
