import { describe, expect, it } from "vitest";

import { mcpConnectorsEnabled } from "./mcp-connectors";
import { usageFeedbackEnabled } from "./usage-feedback";

describe("MCP connectors default", () => {
  it("is on until an admin turns it off, and always off in HIPAA mode", () => {
    expect(mcpConnectorsEnabled({})).toBe(true);
    expect(mcpConnectorsEnabled({ mcpConnectors: true })).toBe(true);
    expect(mcpConnectorsEnabled({ mcpConnectors: false })).toBe(false);
    expect(mcpConnectorsEnabled({ hipaaCompliance: true })).toBe(false);
    expect(mcpConnectorsEnabled({ hipaaCompliance: true, mcpConnectors: true })).toBe(false);
    expect(mcpConnectorsEnabled(undefined)).toBe(false);
  });
});

describe("usage feedback switch", () => {
  it("is on unless the flag is false", () => {
    expect(usageFeedbackEnabled(undefined)).toBe(true);
    expect(usageFeedbackEnabled({ featureFlags: {} })).toBe(true);
    expect(usageFeedbackEnabled({ featureFlags: { "usage-feedback": true } })).toBe(true);
    expect(usageFeedbackEnabled({ featureFlags: { "usage-feedback": false } })).toBe(false);
  });
});
