import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";

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
  expect(screen.getByText("const answer = value + 1;")).toBeVisible();
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
