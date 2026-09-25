import { describe, expect, it } from "vitest";

import { CODING_TOOLS, enabledCodingTools, renderCodingPrompt } from "./coding-tools";

const issue = { identifier: "FLO-7", title: "Fix login", description: "Steps", branchName: "me/flo-7-fix-login", url: "https://flow.test/i/FLO-7" };

describe("coding tools", () => {
  it("renders the default prompt", () => {
    expect(renderCodingPrompt(undefined, issue)).toBe("# FLO-7: Fix login\n\nSteps\n\nIssue URL: https://flow.test/i/FLO-7");
  });

  it("fills template variables and keeps unknown ones", () => {
    expect(renderCodingPrompt("{{issue.branchName}} {{ issue.identifier }} {{nope}}", issue)).toBe("me/flo-7-fix-login FLO-7 {{nope}}");
  });

  it("lists enabled tools in catalog order", () => {
    expect(enabledCodingTools({ enabledCodingTools: ["cursor", "codex"] }).map((tool) => tool.id)).toEqual(["codex", "cursor"]);
  });

  it("builds a custom link only from an http template", () => {
    const custom = CODING_TOOLS.find((tool) => tool.id === "customUrl")!;
    expect(custom.link("a b", { customDeepLinkUrlTemplate: "https://x.dev/new?p={{prompt}}" })).toBe("https://x.dev/new?p=a%20b");
    expect(custom.link("a", { customDeepLinkUrlTemplate: "javascript:alert(1)" })).toBeUndefined();
  });
});
