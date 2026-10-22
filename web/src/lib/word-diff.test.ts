import { describe, expect, it } from "vitest";

import { markHighlightedRanges, pairedWordRanges, wordDiffRanges } from "./word-diff";

const slice = (text: string, ranges: Array<[number, number]>) => ranges.map(([from, to]) => text.slice(from, to));

describe("wordDiffRanges", () => {
  it("highlights the whole changed word, not scattered letters", () => {
    const result = wordDiffRanges("        default: true", "        default: false");
    expect(slice("        default: true", result!.before)).toEqual(["true"]);
    expect(slice("        default: false", result!.after)).toEqual(["false"]);
  });

  it("highlights an inserted phrase on one side only", () => {
    const result = wordDiffRanges("return greet(name);", "return greet(name, punctuation);");
    expect(result!.before).toEqual([]);
    expect(slice("return greet(name, punctuation);", result!.after)).toEqual([", punctuation"]);
  });

  it("leaves unrelated, identical and very long lines alone", () => {
    expect(wordDiffRanges("alpha beta", "alpha beta")).toBeNull();
    expect(wordDiffRanges("const a = 1;", "export default class Widget extends Base {}")).toBeNull();
    expect(wordDiffRanges("x".repeat(500), "y".repeat(500))).toBeNull();
    expect(wordDiffRanges("   ", "foo")).toBeNull();
  });
});

describe("pairedWordRanges", () => {
  it("pairs the i-th removed line with the i-th added line of a block", () => {
    const lines = [
      { kind: "context" as const, text: "a" },
      { kind: "remove" as const, text: "let value = 1;" },
      { kind: "remove" as const, text: "let other = 2;" },
      { kind: "add" as const, text: "let value = 10;" },
      { kind: "context" as const, text: "b" },
      { kind: "add" as const, text: "lonely added line" },
    ];
    const result = pairedWordRanges(lines, (index) => lines[index].text);
    // Only the second "1"-> "10" change is an insertion on the added side; the unpaired second removed line stays plain.
    expect([...result.keys()]).toEqual([3]);
    expect(slice(lines[3].text, result.get(3)!)).toEqual(["0"]);
  });
});

describe("markHighlightedRanges", () => {
  it("wraps ranges by original characters, through tags and entities", () => {
    const html = '<span class="hljs-keyword">if</span> (a &lt; b &amp;&amp; c)';
    // "if (a < b && c)" -> mark "< b &&"
    expect(markHighlightedRanges(html, [[6, 12]])).toBe('<span class="hljs-keyword">if</span> (a <span class="review-word-mark">&lt; b &amp;&amp;</span> c)');
    expect(markHighlightedRanges(html, [[0, 2]])).toBe('<span class="hljs-keyword"><span class="review-word-mark">if</span></span> (a &lt; b &amp;&amp; c)');
    expect(markHighlightedRanges(html, [])).toBe(html);
  });
});
