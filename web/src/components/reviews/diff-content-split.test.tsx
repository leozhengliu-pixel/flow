import { fireEvent, render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { I18nProvider } from "@/i18n/i18n";
import type { DiffLine } from "@/lib/diff-computer";

import { DiffContentRows } from "./diff-content";

const line = (kind: DiffLine["kind"], oldNumber: number | undefined, newNumber: number | undefined, content: string): DiffLine => ({ kind, oldNumber, newNumber, content }) as DiffLine;
const lines = [line("context", 1, 1, "const a = 1;"), line("remove", 2, undefined, "const long = 'x'.repeat(400);"), line("add", undefined, 2, "const long = 'y'.repeat(400);")];
const rows = [{ left: lines[0], right: lines[0] }, { left: lines[1], right: lines[2] }];

describe("split diff", () => {
  it("scrolls each side on its own while the line numbers stay outside the scrollers", () => {
    const { container } = render(
      <I18nProvider>
        <DiffContentRows path="a.ts" mode="split" lines={lines} splitRows={rows} />
      </I18nProvider>,
    );
    const scrollers = container.querySelectorAll<HTMLElement>(".review-diff-split-code");
    expect([...scrollers].map((node) => node.dataset.side)).toEqual(["old", "new"]);
    expect(container.querySelectorAll(".review-diff-split > .review-diff-part.is-gutter")).toHaveLength(4);
    for (const scroller of scrollers) expect(scroller.querySelector(".review-diff-number")).toBeNull();
    expect(scrollers[0].querySelectorAll("code")).toHaveLength(2);
  });

  it("keeps the same side of every run of rows at one scroll position", () => {
    const { container } = render(
      <I18nProvider>
        <DiffContentRows path="a.ts" mode="split" lines={lines} splitRows={rows} renderAfterLine={(number, side) => (number === 1 && side === "RIGHT" ? <p>thread</p> : null)} />
      </I18nProvider>,
    );
    const old = container.querySelectorAll<HTMLElement>('.review-diff-split-code[data-side="old"]');
    expect(old).toHaveLength(2);
    old[0].scrollLeft = 40;
    fireEvent.scroll(old[0]);
    expect(old[1].scrollLeft).toBe(40);
  });
});
