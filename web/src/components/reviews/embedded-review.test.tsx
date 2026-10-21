import { fireEvent, render as renderUI, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter } from "react-router-dom";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";

import { I18nProvider } from "@/i18n/i18n";
import { BackgroundReviewProcessor } from "@/lib/background-review-processor";
import { makeBootstrap, teammate, viewer } from "@/test/fixtures";
import type { CodeReview } from "@/types/flow";

import { EmbeddedReview } from "./embedded-review";
import { nextEmbeddedAgentShown, parseEmbeddedReviewPath } from "./embedded-review-model";

const render = (ui: ReactNode) => renderUI(<MemoryRouter><I18nProvider>{ui}</I18nProvider></MemoryRouter>);
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
  title: "Add shout function",
  description: "Add shout(name).",
  status: "open",
  repositoryOwner: "flow",
  repositoryName: "flow",
  url: "https://github.com/flow/flow/pull/33",
  author: teammate,
  reviewerIds: [],
  teamReviewers: [],
  issueIds: [],
  baseBranch: "main",
  headBranch: "feature/shout",
  branchState: "upToDate",
  additions: 1,
  deletions: 0,
  commitCount: 1,
  checks: [],
  files: [],
  events: [],
  favorite: false,
  draft: false,
  quickToReview: false,
  createdAt: "2026-09-01T00:00:00.000Z",
  updatedAt: "2026-09-01T00:01:00.000Z",
};

const setup = () => {
  const data = makeBootstrap({ viewer, users: [viewer, teammate], reviews: [review], userSettings: {}, integrationConnections: [{ id: "github", provider: "github" }] as never[] });
  const onNavigate = vi.fn();
  const view = render(<EmbeddedReview data={data} review={review} sessionId="session-1" onReload={async () => undefined} onNavigate={onNavigate} />);
  return { data, onNavigate, ...view };
};

it("opens the real review page on its Diff tab and switches tabs inside the pane", async () => {
  const user = userEvent.setup();
  const { onNavigate } = setup();
  expect(screen.getByRole("link", { name: "Diff" }).getAttribute("aria-current")).toBe("page");
  expect(screen.getByRole("link", { name: "Guide" })).toBeTruthy();
  expect(screen.getByRole("link", { name: "Overview" })).toBeTruthy();
  await user.click(screen.getByRole("link", { name: "Overview" }));
  expect(screen.getByRole("link", { name: "Overview" }).getAttribute("aria-current")).toBe("page");
  expect(onNavigate).not.toHaveBeenCalled();
});

it("opens the full review page from the expand control", async () => {
  const user = userEvent.setup();
  const { data, onNavigate } = setup();
  await user.click(screen.getByRole("button", { name: "Review diff in full window" }));
  expect(onNavigate).toHaveBeenCalledWith(`/${data.workspace.urlKey}/review/review-one/changes?showAgent=true&agentSessionId=session-1`);
});

it("hosts a review request without an agent panel, on Overview, with a plain full-page path", async () => {
  const user = userEvent.setup();
  const gitlabReview = { ...review, provider: "gitlab" as const };
  const data = makeBootstrap({ viewer, users: [viewer, teammate], reviews: [gitlabReview], userSettings: {}, integrationConnections: [{ id: "gitlab", provider: "gitlab" }] as never[] });
  const onNavigate = vi.fn();
  render(<EmbeddedReview data={data} review={gitlabReview} onReload={async () => undefined} onNavigate={onNavigate} />);
  expect(screen.getByRole("link", { name: "Overview" }).getAttribute("aria-current")).toBe("page");
  expect(document.querySelector(".has-agent-panel")).toBeNull();
  await user.click(screen.getByRole("button", { name: "Review diff in full window" }));
  expect(onNavigate).toHaveBeenCalledWith(`/${data.workspace.urlKey}/review/review-one`);
});

it("keeps review shortcuts and inbox shortcuts apart", () => {
  const hostKeys = vi.fn();
  window.addEventListener("keydown", hostKeys);
  setup();
  // A review shortcut typed on the page body switches the tab and never reaches the host page's own shortcuts.
  fireEvent.keyDown(document.body, { key: "1", code: "Digit1" });
  expect(screen.getByRole("link", { name: "Overview" }).getAttribute("aria-current")).toBe("page");
  expect(hostKeys).not.toHaveBeenCalled();
  // Typed on an element of the host page outside the review, it is the host's.
  const outside = document.createElement("button");
  document.body.append(outside);
  fireEvent.keyDown(outside, { key: "3", code: "Digit3" });
  expect(screen.getByRole("link", { name: "Overview" }).getAttribute("aria-current")).toBe("page");
  expect(hostKeys).toHaveBeenCalledTimes(1);
  outside.remove();
  window.removeEventListener("keydown", hostKeys);
});

it("parses paths within the review and decides the agent panel for narrow panes", () => {
  expect(parseEmbeddedReviewPath("/flow/review/review-one/review?showAgent=false&agentSessionId=s", "flow", review)).toEqual({ tab: "review", showAgent: false });
  expect(parseEmbeddedReviewPath("/flow/review/review-one", "flow", review)).toEqual({ tab: "overview", showAgent: undefined });
  expect(parseEmbeddedReviewPath("/flow/issue/FLO-1/x", "flow", review)).toBeNull();
  expect(nextEmbeddedAgentShown(true, true, false)).toBe(true);
  expect(nextEmbeddedAgentShown(true, true, true)).toBe(false);
  expect(nextEmbeddedAgentShown(false, true, true)).toBe(true);
  expect(nextEmbeddedAgentShown(true, false, false)).toBe(false);
});
