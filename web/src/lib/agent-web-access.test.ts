import { describe, expect, it } from "vitest";
import { agentWebAccessEnabled } from "./agent-web-access";

describe("agentWebAccessEnabled", () => {
  it("is on when an admin never set it", () => {
    expect(agentWebAccessEnabled({})).toBe(true);
    expect(agentWebAccessEnabled({ agentWebSearchDisabled: false })).toBe(true);
  });
  it("follows an explicit choice", () => {
    expect(agentWebAccessEnabled({ agentWebAccess: false })).toBe(false);
    expect(agentWebAccessEnabled({ agentWebAccess: true, agentWebSearchDisabled: true })).toBe(true);
  });
  it("respects the disabled flag older clients stored", () => {
    expect(agentWebAccessEnabled({ agentWebSearchDisabled: true })).toBe(false);
  });
});
