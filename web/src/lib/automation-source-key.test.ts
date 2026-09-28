import { describe, expect, it } from "vitest";
import {
  EMAIL_TRUSTED_SOURCE_KEY,
  getTrustedSourcesMode,
  issueSourceMetadataToSourceKey,
  matcherToAllowlistSourceKey,
  normalizeSourceKey,
  trustedSourcesAllowlist,
} from "./automation-source-key";
import { AutomationBlockingSourceHelper } from "./automation-blocking-source-helper";
import { AutomationTrustedSourceEditorOptions } from "./automation-trusted-source-editor-options";
import {
  getChangeAutomationOwnerAction,
  resolveEffectiveOwner,
} from "./automation-owner";
import type { User } from "@/types/flow";

const users: User[] = [
  {
    id: "u1",
    name: "alice",
    displayName: "Alice",
    email: "a@x.com",
    active: true,
    emailVerified: true,
  },
  {
    id: "u2",
    name: "bot",
    displayName: "Helper Bot",
    email: "bot@x.com",
    active: true,
    emailVerified: true,
    app: true,
    appScopes: ["app:mentionable"],
    avatarUrl: "https://example.com/bot.png",
  },
];

describe("automation-source-key", () => {
  it("normalizes source keys", () => {
    expect(normalizeSourceKey("appUser:abc")).toBe("appUser:abc");
    expect(normalizeSourceKey("integration:slack")).toBe("integration:slack");
    expect(normalizeSourceKey("integration:nope")).toBeUndefined();
    expect(normalizeSourceKey(EMAIL_TRUSTED_SOURCE_KEY)).toBe(
      EMAIL_TRUSTED_SOURCE_KEY,
    );
    expect(normalizeSourceKey("oauthClient:oid")).toBe("oauthClient:oid");
  });

  it("maps issue metadata and matchers", () => {
    expect(
      issueSourceMetadataToSourceKey({ type: "email", subType: "email" }),
    ).toBe(EMAIL_TRUSTED_SOURCE_KEY);
    expect(
      issueSourceMetadataToSourceKey({
        type: "integration",
        subType: "slack",
      }),
    ).toBe("integration:slack");
    expect(
      matcherToAllowlistSourceKey({ type: "appUser", userId: "u2" }),
    ).toBe("appUser:u2");
    expect(
      matcherToAllowlistSourceKey({
        type: "sourceMetadata",
        sourceMetadata: {
          type: "email",
          subType: "email",
          emailIntakeMetadata: { trusted: true },
        },
      }),
    ).toBe(EMAIL_TRUSTED_SOURCE_KEY);
  });

  it("reads trusted sources mode / allowlist", () => {
    expect(getTrustedSourcesMode({})).toBe("none");
    expect(getTrustedSourcesMode({ trustedSourcesMode: "allowlist" })).toBe(
      "allowlist",
    );
    expect(
      trustedSourcesAllowlist({
        trustedSourcesAllowlist: ["appUser:u2", "bad", "integration:slack"],
      }),
    ).toEqual(["appUser:u2", "integration:slack"]);
  });
});

describe("AutomationBlockingSourceHelper", () => {
  it("resolves forIssue / isUnauthenticatedEmail / sourceName", () => {
    const blocking = AutomationBlockingSourceHelper.forIssue({
      sourceMetadata: { type: "email", subType: "email" },
    });
    expect(blocking?.key).toBe(EMAIL_TRUSTED_SOURCE_KEY);
    expect(blocking?.name).toBe("Trusted email");

    expect(
      AutomationBlockingSourceHelper.isUnauthenticatedEmail({
        sourceMetadata: {
          type: "email",
          emailIntakeMetadata: { trusted: false },
        },
      }),
    ).toBe(true);
    expect(
      AutomationBlockingSourceHelper.isUnauthenticatedEmail({
        sourceMetadata: {
          type: "email",
          emailIntakeMetadata: { trusted: true },
        },
      }),
    ).toBe(false);

    const app = AutomationBlockingSourceHelper.forIssue({
      creator: { id: "u2", name: "Helper Bot", app: true },
    });
    expect(app).toEqual({ key: "appUser:u2", name: "Helper Bot" });

    expect(
      AutomationBlockingSourceHelper.sourceName("integration:slack", {
        type: "integration",
        subType: "slack",
      }),
    ).toBe("Slack");
  });
});

describe("AutomationTrustedSourceEditorOptions", () => {
  it("builds sourcesForEditor with allowed flags", async () => {
    const ctx = {
      users,
      integrationServices: ["slack"],
      policySourceKeys: ["appUser:u2"],
      settings: {
        trustedSourcesMode: "allowlist" as const,
        trustedSourcesAllowlist: ["integration:slack"],
      },
    };
    await AutomationTrustedSourceEditorOptions.hydrateSources(ctx);
    const options = AutomationTrustedSourceEditorOptions.sourcesForEditor(ctx);
    const slack = options.find((item) => item.id === "integration:slack");
    const bot = options.find((item) => item.id === "appUser:u2");
    const email = options.find((item) => item.id === EMAIL_TRUSTED_SOURCE_KEY);
    expect(slack?.allowed).toBe(true);
    expect(bot?.allowed).toBe(false);
    expect(bot?.kind).toBe("agent");
    expect(email?.name).toBe("Trusted email");
  });

  it("returns empty allowed set when mode is none", () => {
    const allowed = AutomationTrustedSourceEditorOptions.allowedSourceKeys({
      trustedSourcesMode: "none",
      trustedSourcesAllowlist: ["integration:slack"],
    });
    expect(allowed.size).toBe(0);
  });
});

describe("automation-owner", () => {
  it("resolves effectiveOwner and change action", () => {
    expect(
      resolveEffectiveOwner({
        ownerId: "u2",
        creatorId: "u1",
        users,
      }).id,
    ).toBe("u2");
    expect(
      resolveEffectiveOwner({
        creatorId: "u1",
        users,
      }).id,
    ).toBe("u1");
    const action = getChangeAutomationOwnerAction({
      configure: { kind: "allowed" },
    });
    expect(action.id).toBe("change-automation-owner");
    expect(action.allowed).toBe(true);
  });
});
