import { describe, expect, it } from "vitest";
import type { CodingEnvironment, CodingModelOption } from "@/types/flow";
import {
  classifyEnv,
  draftFromEnvironment,
  draftToInput,
  emptyDraft,
  formatBytes,
  legacyCodingSettings,
  lineDiff,
  looksSecret,
  modelOptions,
  normalizeHarness,
  parseDotEnv,
  proposalChanges,
  reasoningOptions,
  repositoryConflict,
  sandboxSizeOptions,
  environmentOwners,
  formatDuration,
  snapshotDuration,
  toolLabel,
  toolVersions,
  splitDomains,
  validateDraft,
} from "./coding-sessions-model";

function makeEnvironment(overrides: Partial<CodingEnvironment> = {}): CodingEnvironment {
  return {
    id: "env1",
    name: "Web",
    codeHost: "github",
    hostName: "github.com",
    repository: "acme/web",
    mise: { tools: { node: "22" }, files: [{ path: ".npmrc", content: "x" }], environmentVariables: [{ name: "NODE_ENV", value: "test" }] },
    prepareScript: "npm ci",
    dependencyManagement: "custom-only",
    sandboxSize: "medium",
    network: "default",
    secrets: [],
    version: 3,
    createdAt: "2026-10-01T00:00:00Z",
    updatedAt: "2026-10-01T00:00:00Z",
    ...overrides,
  };
}

describe("parseDotEnv", () => {
  it("handles comments, export, quotes, escapes, inline comments and multiline values", () => {
    const parsed = parseDotEnv([
      "# comment",
      "",
      "export API_TOKEN=abc123",
      "PLAIN=value # trailing comment",
      "HASH=a#b",
      'QUOTED="hello world"',
      "SINGLE='raw \\n value'",
      'ESCAPED="line1\\nline2"',
      'MULTI="first',
      'second"',
      "EMPTY=",
      "1BAD=x",
      "no_equals",
      "DUP=1",
      "DUP=2",
    ].join("\r\n"));
    expect(parsed).toEqual([
      { name: "API_TOKEN", value: "abc123" },
      { name: "PLAIN", value: "value" },
      { name: "HASH", value: "a#b" },
      { name: "QUOTED", value: "hello world" },
      { name: "SINGLE", value: "raw \\n value" },
      { name: "ESCAPED", value: "line1\nline2" },
      { name: "MULTI", value: "first\nsecond" },
      { name: "EMPTY", value: "" },
      { name: "DUP", value: "2" },
    ]);
  });

  it("keeps an unterminated quote as the rest of the file", () => {
    expect(parseDotEnv('A="open\nB=2')).toEqual([{ name: "A", value: "open\nB=2" }]);
  });
});

describe("secret classification", () => {
  it.each([
    ["STRIPE_SECRET", "x", true],
    ["GITHUB_TOKEN", "x", true],
    ["AWS_ACCESS_KEY", "x", true],
    ["DB_PASSWORD", "x", true],
    ["SENTRY_DSN", "x", true],
    ["DATABASE_URL", "postgres://user:pass@db/app", true],
    ["DATABASE_URL", "postgres://db/app", false],
    ["NODE_ENV", "production", false],
    ["PORT", "3000", false],
  ])("%s=%s → secret %s", (name, value, expected) => {
    expect(looksSecret(name, value)).toBe(expected);
  });

  it("splits parsed entries into variables and secrets", () => {
    expect(classifyEnv([{ name: "PORT", value: "1" }, { name: "API_KEY", value: "k" }])).toEqual({
      variables: [{ name: "PORT", value: "1" }],
      secrets: [{ name: "API_KEY", value: "k" }],
    });
  });
});

describe("drafts", () => {
  it("round-trips an environment and drops blank rows", () => {
    const draft = draftFromEnvironment(makeEnvironment({ allowedDomains: ["a.com"], network: "extended" }));
    draft.tools.push({ tool: " ", version: "1" }, { tool: "python", version: "" });
    draft.variables.push({ name: "", value: "" });
    draft.files.push({ path: "", content: "" });
    draft.allowedDomains += "\n.B.com, a.com";
    const input = draftToInput(draft);
    expect(input.mise).toEqual({ tools: { node: "22", python: "latest" }, files: [{ path: ".npmrc", content: "x" }], environmentVariables: [{ name: "NODE_ENV", value: "test" }] });
    expect(input.allowedDomains).toEqual(["a.com", ".b.com"]);
    expect(input).toMatchObject({ name: "Web", repository: "acme/web", sandboxSize: "medium", dependencyManagement: "custom-only" });
  });

  it("only sends allowed domains for the extended network", () => {
    const draft = { ...emptyDraft(), allowedDomains: "a.com" };
    expect(draftToInput(draft).allowedDomains).toEqual([]);
    expect(splitDomains(" a.com\n\nb.com ")).toEqual(["a.com", "b.com"]);
  });

  it("starts new environments on the first connected host", () => {
    const draft = emptyDraft({ codeHosts: [{ codeHost: "gitlab", hostName: "gitlab.acme.dev", connected: true }] } as never);
    expect(draft).toMatchObject({ codeHost: "gitlab", hostName: "gitlab.acme.dev", sandboxSize: "medium", dependencyManagement: "custom-and-generated" });
  });

  it("validates names, duplicates, secrets and file paths", () => {
    const draft = { ...emptyDraft(), name: "", repository: "", variables: [{ name: "1BAD", value: "" }, { name: "OK", value: "" }], files: [{ path: "/etc/passwd", content: "x" }, { path: "", content: "y" }, { path: "a/../b", content: "" }] };
    expect(validateDraft(draft, [{ name: "OK", value: "s" }, { name: "TOKEN", value: "t" }], ["TOKEN"])).toEqual([
      "Name is required",
      "Choose a repository",
      "Invalid variable name: 1BAD",
      "Duplicate variable: OK",
      "Duplicate variable: TOKEN",
      "Files are placed in the sandbox home: /etc/passwd",
      "File path is required",
      "Files are placed in the sandbox home: a/../b",
    ]);
    expect(validateDraft({ ...emptyDraft(), name: "x", repository: "a/b" }, [], [])).toEqual([]);
  });

  it("enforces one active environment per repository", () => {
    const environments = [makeEnvironment(), makeEnvironment({ id: "old", repository: "acme/api", archivedAt: "2026-01-01" })];
    expect(repositoryConflict(environments, { codeHost: "github", hostName: "GitHub.com", repository: " ACME/web " })?.id).toBe("env1");
    expect(repositoryConflict(environments, { id: "env1", codeHost: "github", hostName: "github.com", repository: "acme/web" })).toBeUndefined();
    expect(repositoryConflict(environments, { codeHost: "github", hostName: "github.com", repository: "acme/api" })).toBeUndefined();
    expect(repositoryConflict(environments, { codeHost: "gitlab", hostName: "github.com", repository: "acme/web" })).toBeUndefined();
    expect(repositoryConflict(environments, { codeHost: "github", hostName: "github.com", repository: "" })).toBeUndefined();
  });
});

describe("settings helpers", () => {
  const models: CodingModelOption[] = [
    { id: "auto", label: "Auto", harness: "codex", auto: true },
    { id: "gpt-5-codex", label: "GPT-5 Codex", harness: "codex", reasoningEfforts: ["low", "high"] },
    { id: "gpt-5", label: "GPT-5", harness: "codex", reasoningEfforts: ["medium"] },
    { id: "claude-sonnet-4-5", label: "Claude Sonnet 4.5", harness: "claudeCode" },
  ];
  it("normalizes legacy harness values", () => {
    expect(normalizeHarness("auto")).toBe("codex");
    expect(normalizeHarness("claude")).toBe("claudeCode");
    expect(normalizeHarness("openSource")).toBe("openSource");
    expect(normalizeHarness(undefined)).toBe("codex");
    expect(legacyCodingSettings({ harness: "claude", commitSigningEnabled: true }, true)).toMatchObject({ harness: "claudeCode", model: "auto", requireSignedCommits: true, loopRepositoryAccess: "none" });
  });
  it("lists Auto first and filters by harness", () => {
    expect(modelOptions(models, "codex").map((option) => option.value)).toEqual(["auto", "gpt-5-codex", "gpt-5"]);
    expect(modelOptions(models, "openSource")).toEqual([{ value: "auto", label: "Auto", detail: "Uses adaptive routing to pick the best model" }]);
    // A server without groups gets a flat list (no section headers).
    expect(modelOptions(models, "codex").some((option) => option.group)).toBe(false);
  });
  it("groups models as Auto, Frontier with descriptions, then Other models", () => {
    const grouped: CodingModelOption[] = [
      { id: "auto", label: "Auto (gpt-5-codex)", harness: "codex", auto: true, group: "auto", description: "Uses adaptive routing to pick the best model" },
      { id: "gpt-4.1", label: "GPT-4.1", harness: "codex", group: "other", description: "ignored outside Frontier" },
      { id: "gpt-5-codex", label: "GPT-5 Codex", harness: "codex", group: "frontier", description: "Strong reasoning for multi-file changes" },
      { id: "glm", label: "GLM", harness: "codex", group: "frontier" },
      { id: "legacy", label: "", harness: "codex" },
    ];
    expect(modelOptions(grouped, "codex")).toEqual([
      { value: "auto", label: "Auto", detail: "Uses adaptive routing to pick the best model" },
      { value: "gpt-5-codex", label: "GPT-5 Codex", translate: false, group: "Frontier", detail: "Strong reasoning for multi-file changes" },
      { value: "glm", label: "GLM", translate: false, group: "Frontier" },
      { value: "gpt-4.1", label: "GPT-4.1", translate: false, group: "Other models" },
      { value: "legacy", label: "legacy", translate: false, group: "Other models" },
    ]);
  });
  it("offers reasoning efforts from the catalog", () => {
    expect(reasoningOptions(models, "codex", "auto")).toEqual(["low", "medium", "high"]);
    expect(reasoningOptions(models, "codex", "gpt-5")).toEqual(["medium"]);
    expect(reasoningOptions(models, "claudeCode", "auto")).toEqual([]);
  });
  it("labels sandbox sizes from the server or the defaults", () => {
    expect(sandboxSizeOptions().map((size) => size.detail)).toEqual(["1 vCPU · 8 GB memory", "2 vCPU · 16 GB memory", "4 vCPU · 32 GB memory"]);
    expect(sandboxSizeOptions({ sizes: { small: { cpu: "2", memory: "4Gi" }, medium: { cpu: "4", memory: "8Gi" }, large: { cpu: "8", memory: "512Mi" } } } as never)[2].detail).toBe("8 vCPU · 512 MB memory");
  });
  it("offers tool presets and keeps custom versions", () => {
    expect(toolLabel("dotnet")).toBe(".NET");
    expect(toolLabel("zig")).toBe("zig");
    expect(toolVersions("node", "22")).toEqual(["latest", "26", "24", "22"]);
    expect(toolVersions("node", "20.11")).toEqual(["latest", "26", "24", "22", "20.11"]);
    expect(toolVersions("zig", "")).toEqual(["latest"]);
  });
  it("formats snapshot durations", () => {
    expect(formatDuration(9000)).toBe("9s");
    expect(formatDuration(72_000)).toBe("1m 12s");
    expect(formatDuration(3_780_000)).toBe("1h 3m");
    expect(snapshotDuration({ createdAt: "2026-10-01T00:00:00Z", startedAt: "2026-10-01T00:00:01Z", finishedAt: "2026-10-01T00:00:10Z" })).toBe(9000);
    expect(snapshotDuration({ createdAt: "2026-10-01T00:00:00Z" })).toBeUndefined();
  });
  it("treats the creator as the owner of an environment without owners", () => {
    expect(environmentOwners({ createdBy: "u1" })).toEqual(["u1"]);
    expect(environmentOwners({ ownerIds: ["u2"], createdBy: "u1" })).toEqual(["u2"]);
    expect(environmentOwners({})).toEqual([]);
  });
  it("formats bytes", () => {
    expect(formatBytes(undefined)).toBe("");
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1536)).toBe("1.5 KB");
    expect(formatBytes(20 * 1024 * 1024)).toBe("20 MB");
  });
});

describe("proposal diff", () => {
  it("diffs lines with an LCS", () => {
    expect(lineDiff("a\nb\nc", "a\nc\nd")).toEqual([
      { kind: "same", text: "a" },
      { kind: "remove", text: "b" },
      { kind: "same", text: "c" },
      { kind: "add", text: "d" },
    ]);
    expect(lineDiff("", "x")).toEqual([{ kind: "add", text: "x" }]);
  });
  it("lists only changed fields", () => {
    const changes = proposalChanges(makeEnvironment(), {
      sessionId: "s",
      explanation: "",
      verified: true,
      createdAt: "",
      prepareScript: "npm ci\nnpm run build",
      mise: { tools: { node: "22", go: "1.23" } },
      dependencyManagement: "custom-only",
    });
    expect(changes.map((change) => change.field)).toEqual(["Tool versions", "Prepare script"]);
    expect(changes[0].after).toBe('go = "1.23"\nnode = "22"');
  });
});
