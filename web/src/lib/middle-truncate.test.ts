import { describe, expect, it } from "vitest";

import { middleTruncate } from "./middle-truncate";

const byCharacters = (width: number) => (value: string) => value.length <= width;

describe("middleTruncate", () => {
  it("keeps text that fits", () => {
    expect(middleTruncate("main", byCharacters(8))).toBe("main");
  });

  it("keeps both ends at the longest width that fits", () => {
    const value = "bcgroupdev/flo-58-qa-session-question-shoutname-punctuation-31f7";
    const result = middleTruncate(value, byCharacters(21));
    expect(result).toBe("bcgroupdev…ation-31f7");
    expect(result.length).toBeLessThanOrEqual(21);
    expect(value.startsWith(result.split("…")[0])).toBe(true);
    expect(value.endsWith(result.split("…")[1])).toBe(true);
  });

  it("only keeps the ellipsis when nothing else fits", () => {
    expect(middleTruncate("abcdef", byCharacters(0))).toBe("…");
  });
});
