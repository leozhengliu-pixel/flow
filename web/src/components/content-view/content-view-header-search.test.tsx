import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import {
  ContentViewHeaderInlineSearch,
  ContentViewHeaderSearch,
} from "./content-view-header-search";

describe("ContentViewHeader search primitives", () => {
  it("InlineSearch returns null without inlineFilter (LS-0142)", () => {
    const { container } = render(
      <ContentViewHeaderInlineSearch
        placeholder="Find a view…"
        value=""
        onChange={vi.fn()}
      />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("InlineSearch renders Find a view… when inlineFilter is provided", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <ContentViewHeaderInlineSearch
        inlineFilter={{ lastSearchTerm: "" }}
        placeholder="Find a view…"
        value=""
        onChange={onChange}
        alwaysShowOnDesktop
      />,
    );
    const box = screen.getByRole("searchbox", { name: "Find a view…" });
    await user.type(box, "board");
    expect(onChange).toHaveBeenCalled();
  });

  it("HeaderSearch alwaysVisible shows unified placeholders (LS-0143)", () => {
    render(
      <ContentViewHeaderSearch
        alwaysVisible
        placeholder="Find members…"
        value=""
        onChange={vi.fn()}
      />,
    );
    expect(
      screen.getByRole("searchbox", { name: "Find members…" }),
    ).toBeVisible();
  });
});
