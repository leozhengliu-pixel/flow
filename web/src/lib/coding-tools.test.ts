import { describe, expect, it } from "vitest";

import { CODING_TOOLS, enabledCodingTools, renderCodingPrompt } from "./coding-tools";

const issue = { identifier: "FLO-7", title: "Fix login", description: "Steps", branchName: "me/flo-7-fix-login", url: "https://flow.test/i/FLO-7" };

describe("coding tools", () => {
  it("renders the default prompt: the branch, then the issue block", () => {
    expect(renderCodingPrompt(undefined, issue)).toBe(
      "Work on Flow issue FLO-7:\n\nSuggested branch name: me/flo-7-fix-login\n\n<issue identifier=\"FLO-7\">\n<title>Fix login</title>\n<description>Steps</description>\n</issue>",
    );
  });

  it("writes the team and the parent issue into the block, multi-line descriptions on their own lines", () => {
    const prompt = renderCodingPrompt(undefined, {
      ...issue,
      description: "",
      team: "Flow-app",
      parent: { id: "parent-1", identifier: "FLO-6", title: "Auth", description: "One\n\nTwo" },
    });
    expect(prompt.split("\n\n").slice(2).join("\n\n")).toBe(
      "<issue identifier=\"FLO-7\">\n<title>Fix login</title>\n<team name=\"Flow-app\"/>\n<parent-issue identifier=\"FLO-6\">\n<id>parent-1</id>\n<title>Auth</title>\n<description>\nOne\n\nTwo\n</description>\n</parent-issue>\n</issue>",
    );
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
