/**
 * Pure helpers for Settings › AI & agents › Coding sessions
 * (docs/coding-sessions-design.md §1, §18, §24 track F).
 */
import { formatFileSize } from "@/lib/format-file-size";
import type {
  CodeHostKind,
  CodingAgentSettings,
  CodingDependencyManagement,
  CodingEnvironment,
  CodingEnvironmentInput,
  CodingEnvironmentProposal,
  CodingEnvVar,
  CodingHarness,
  CodingMiseFile,
  CodingModelOption,
  CodingNetworkMode,
  CodingReasoningEffort,
  CodingSandboxSize,
  CodingSettings,
  CodingSnapshotStatus,
  CodingStatus,
} from "@/types/flow";

export const HARNESS_OPTIONS: { value: CodingHarness; label: string }[] = [
  { value: "codex", label: "Codex" },
  { value: "claudeCode", label: "Claude Code" },
  { value: "openSource", label: "Open source" },
  { value: "pi", label: "Pi" },
];

export const SANDBOX_SIZES: CodingSandboxSize[] = ["small", "medium", "large"];
const DEFAULT_SIZES: Record<CodingSandboxSize, { cpu: string; memory: string }> = {
  small: { cpu: "1", memory: "8Gi" },
  medium: { cpu: "2", memory: "16Gi" },
  large: { cpu: "4", memory: "32Gi" },
};

export const NETWORK_OPTIONS: { value: CodingNetworkMode; label: string; description: string }[] = [
  { value: "default", label: "Default", description: "Code hosts, package registries and toolchain downloads" },
  { value: "extended", label: "Extended", description: "The default list plus the domains below" },
  { value: "open", label: "Open", description: "Any public internet address" },
  { value: "none", label: "None", description: "Flow only. No package installs or downloads" },
];

export const DEPENDENCY_OPTIONS: { value: CodingDependencyManagement; label: string; description: string }[] = [
  { value: "custom-only", label: "Custom only", description: "Only run the prepare script" },
  { value: "custom-and-generated", label: "Custom and generated", description: "Also run detected installers such as npm ci or go mod download" },
];

/** The reference external sources whose issues need a member's approval unless trusted (§15). */
export const TRUSTED_SOURCE_OPTIONS: { value: string; label: string }[] = [
  { value: "slack", label: "Slack" },
  { value: "intercom", label: "Intercom" },
  { value: "zendesk", label: "Zendesk" },
  { value: "front", label: "Front" },
  { value: "sentry", label: "Sentry" },
  { value: "email", label: "Email intake" },
  { value: "github", label: "GitHub issues" },
  { value: "gitlab", label: "GitLab issues" },
  { value: "api", label: "API and applications" },
];

export function trustedSourceLabel(value: string): string {
  return TRUSTED_SOURCE_OPTIONS.find((option) => option.value === value)?.label ?? value;
}

/** Legacy harness values are normalized on read (§1). */
export function normalizeHarness(value: string | undefined): CodingHarness {
  if (value === "claudeCode" || value === "claude") return "claudeCode";
  if (value === "openSource") return "openSource";
  if (value === "pi") return "pi";
  return "codex";
}

/** Settings fallback from the legacy workspace preferences while /api/coding/settings is unavailable. */
export function legacyCodingSettings(legacy: CodingAgentSettings | undefined, enabled: boolean): CodingSettings {
  return {
    enabled,
    harness: normalizeHarness(legacy?.harness),
    model: legacy?.model || "auto",
    requireSignedCommits: Boolean(legacy?.commitSigningEnabled),
    trustedExternalSources: [],
    loopRepositoryAccess: "none",
    loopRepositories: [],
  };
}

export function normalizeCodingSettings(settings: CodingSettings): CodingSettings {
  return {
    ...settings,
    harness: normalizeHarness(settings.harness),
    model: settings.model || "auto",
    trustedExternalSources: settings.trustedExternalSources ?? [],
    loopRepositoryAccess: settings.loopRepositoryAccess || "none",
    loopRepositories: settings.loopRepositories ?? [],
  };
}

export const AUTO_MODEL_DESCRIPTION = "Uses adaptive routing to pick the best model";
const MODEL_GROUP_LABEL: Record<"frontier" | "other", string> = { frontier: "Frontier", other: "Other models" };

export type ModelSelectOption = { value: string; label: string; detail?: string; group?: string; translate?: boolean };

/**
 * Model options for one harness, as the reference app's picker groups them: Auto (with its
 * description), then "Frontier" models with their one-line descriptions, then "Other models".
 * Servers that do not send groups get a flat list after Auto.
 */
export function modelOptions(models: CodingModelOption[], harness: CodingHarness): ModelSelectOption[] {
  const options: ModelSelectOption[] = [{ value: "auto", label: "Auto", detail: AUTO_MODEL_DESCRIPTION }];
  const grouped = models.some((model) => model.harness === harness && (model.group === "frontier" || model.group === "other"));
  const entries = models.filter((model) => model.harness === harness && !model.auto && model.id !== "auto");
  const rank = (model: CodingModelOption) => (model.group === "frontier" ? 0 : 1);
  if (grouped) entries.sort((a, b) => rank(a) - rank(b));
  for (const model of entries) {
    const group = grouped ? MODEL_GROUP_LABEL[model.group === "frontier" ? "frontier" : "other"] : undefined;
    const description = model.group === "frontier" ? model.description?.trim() : undefined;
    options.push({ value: model.id, label: model.label || model.id, translate: false, ...(group ? { group } : {}), ...(description ? { detail: description } : {}) });
  }
  return options;
}

/** Reasoning efforts offered for the harness's models; empty when the catalog marks none. */
export function reasoningOptions(models: CodingModelOption[], harness: CodingHarness, model: string): CodingReasoningEffort[] {
  const candidates = models.filter((item) => item.harness === harness && (model === "auto" || item.id === model));
  const efforts = new Set<CodingReasoningEffort>();
  for (const candidate of candidates) for (const effort of candidate.reasoningEfforts ?? []) if (effort) efforts.add(effort);
  return (["low", "medium", "high"] as CodingReasoningEffort[]).filter((effort) => efforts.has(effort));
}

function cpuLabel(cpu: string) {
  return `${cpu} vCPU`;
}
function memoryLabel(memory: string) {
  return memory.replace(/Gi$/i, " GB").replace(/Mi$/i, " MB");
}
/** "Medium" / "2 vCPU · 16 GB memory" (the reference app's two-line options), from the server's sizes when known. */
export function sandboxSizeOptions(status?: CodingStatus): { value: CodingSandboxSize; label: string; detail: string }[] {
  return SANDBOX_SIZES.map((size) => {
    const spec = status?.sizes?.[size] ?? DEFAULT_SIZES[size];
    return { value: size, label: size[0].toUpperCase() + size.slice(1), detail: `${cpuLabel(spec.cpu)} · ${memoryLabel(spec.memory)} memory` };
  });
}

/** "Add tool…" presets: the reference app's list, as mise tool ids with a few pinned majors. */
export const TOOL_PRESETS: { tool: string; label: string; versions: string[] }[] = [
  { tool: "node", label: "Node.js", versions: ["latest", "26", "24", "22"] },
  { tool: "bun", label: "Bun", versions: ["latest", "1.3", "1.2"] },
  { tool: "deno", label: "Deno", versions: ["latest", "2"] },
  { tool: "python", label: "Python", versions: ["latest", "3.14", "3.13", "3.12", "3.11"] },
  { tool: "ruby", label: "Ruby", versions: ["latest", "3.4", "3.3"] },
  { tool: "go", label: "Go", versions: ["latest", "1.25", "1.24"] },
  { tool: "rust", label: "Rust", versions: ["latest", "1.90", "1.89"] },
  { tool: "java", label: "Java", versions: ["latest", "25", "21", "17"] },
  { tool: "dotnet", label: ".NET", versions: ["latest", "10", "9", "8"] },
  { tool: "postgres", label: "PostgreSQL", versions: ["latest", "18", "17", "16"] },
  { tool: "redis", label: "Redis", versions: ["latest", "8", "7"] },
  { tool: "mongodb", label: "MongoDB", versions: ["latest", "8", "7"] },
  { tool: "clickhouse", label: "ClickHouse", versions: ["latest"] },
];

export function toolLabel(tool: string): string {
  return TOOL_PRESETS.find((preset) => preset.tool === tool)?.label ?? tool;
}

/** Version choices for a tool: the preset's, plus the saved value when it is a custom one. */
export function toolVersions(tool: string, current: string): string[] {
  const versions = TOOL_PRESETS.find((preset) => preset.tool === tool)?.versions ?? ["latest"];
  const value = current.trim() || "latest";
  return versions.includes(value) ? versions : [...versions, value];
}

/** "9s", "1m 12s", "1h 3m". */
export function formatDuration(ms: number): string {
  const total = Math.max(0, Math.round(ms / 1000));
  if (total < 60) return `${total}s`;
  const minutes = Math.floor(total / 60);
  if (minutes < 60) return `${minutes}m ${total % 60}s`;
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

/** Wall time of a finished snapshot build, or undefined while it runs. */
export function snapshotDuration(snapshot: { startedAt?: string; createdAt: string; finishedAt?: string }): number | undefined {
  if (!snapshot.finishedAt) return undefined;
  const start = Date.parse(snapshot.startedAt || snapshot.createdAt);
  const end = Date.parse(snapshot.finishedAt);
  return Number.isNaN(start) || Number.isNaN(end) ? undefined : Math.max(0, end - start);
}

/** Environment owners: the saved list, or the creator, who owns an environment without one. */
export function environmentOwners(environment: { ownerIds?: string[]; createdBy?: string }): string[] {
  if (environment.ownerIds?.length) return [...environment.ownerIds];
  return environment.createdBy ? [environment.createdBy] : [];
}

export function codeHostLabel(host: CodeHostKind): string {
  return host === "gitlab" ? "GitLab" : "GitHub";
}

export function hasConnectedCodeHost(status: CodingStatus | undefined): boolean {
  return Boolean(status?.codeHosts?.some((host) => host.connected));
}

/** One active environment per repository (§6): another active environment on the same repo. */
export function repositoryConflict(environments: CodingEnvironment[], draft: { id?: string; codeHost: CodeHostKind; hostName: string; repository: string }): CodingEnvironment | undefined {
  const key = repoKey(draft);
  if (!draft.repository.trim()) return undefined;
  return environments.find((environment) => !environment.archivedAt && environment.id !== draft.id && repoKey(environment) === key);
}
function repoKey(value: { codeHost: CodeHostKind; hostName: string; repository: string }) {
  return `${value.codeHost}\u0000${(value.hostName || "").toLowerCase()}\u0000${value.repository.trim().toLowerCase()}`;
}

export type ToolRow = { tool: string; version: string };

/** Editor state for one environment; secrets are tracked separately because they are add-only. */
export interface EnvironmentDraft {
  id?: string;
  version: number;
  name: string;
  codeHost: CodeHostKind;
  hostName: string;
  repository: string;
  repositoryId?: string;
  defaultBranch?: string;
  harness: CodingHarness | "";
  model: string;
  sandboxSize: CodingSandboxSize;
  network: CodingNetworkMode;
  allowedDomains: string;
  tools: ToolRow[];
  files: CodingMiseFile[];
  variables: CodingEnvVar[];
  prepareScript: string;
  customGuidance: string;
  dependencyManagement: CodingDependencyManagement;
  /** Shown under Permissions; sent only when changed (admins only, §18). */
  ownerIds: string[];
}

export function emptyDraft(status?: CodingStatus): EnvironmentDraft {
  const host = status?.codeHosts?.find((item) => item.connected) ?? status?.codeHosts?.[0];
  return {
    version: 0,
    name: "",
    codeHost: host?.codeHost ?? "github",
    hostName: host?.hostName ?? "github.com",
    repository: "",
    harness: "",
    model: "auto",
    sandboxSize: "medium",
    network: "default",
    allowedDomains: "",
    tools: [],
    files: [],
    variables: [],
    prepareScript: "",
    customGuidance: "",
    dependencyManagement: "custom-and-generated",
    ownerIds: [],
  };
}

export function draftFromEnvironment(environment: CodingEnvironment): EnvironmentDraft {
  return {
    id: environment.id,
    version: environment.version,
    name: environment.name,
    codeHost: environment.codeHost,
    hostName: environment.hostName,
    repository: environment.repository,
    repositoryId: environment.repositoryId,
    defaultBranch: environment.defaultBranch,
    harness: environment.harness ?? "",
    model: environment.model || "auto",
    sandboxSize: environment.sandboxSize ?? "medium",
    network: environment.network ?? "default",
    allowedDomains: (environment.allowedDomains ?? []).join("\n"),
    tools: Object.entries(environment.mise?.tools ?? {}).map(([tool, version]) => ({ tool, version })),
    files: (environment.mise?.files ?? []).map((file) => ({ ...file })),
    variables: (environment.mise?.environmentVariables ?? []).map((variable) => ({ ...variable })),
    prepareScript: environment.prepareScript ?? "",
    customGuidance: environment.customGuidance ?? "",
    dependencyManagement: environment.dependencyManagement ?? "custom-and-generated",
    ownerIds: environmentOwners(environment),
  };
}

export function splitDomains(value: string): string[] {
  return [...new Set(value.split(/[\s,]+/).map((item) => item.trim().toLowerCase()).filter(Boolean))];
}

/** The PATCH/POST body: blank rows are dropped and names trimmed. Owners are sent separately (admins only). */
export function draftToInput(draft: EnvironmentDraft): CodingEnvironmentInput {
  const tools: Record<string, string> = {};
  for (const row of draft.tools) if (row.tool.trim()) tools[row.tool.trim()] = row.version.trim() || "latest";
  return {
    name: draft.name.trim(),
    codeHost: draft.codeHost,
    hostName: draft.hostName,
    repository: draft.repository.trim(),
    ...(draft.repositoryId ? { repositoryId: draft.repositoryId } : {}),
    ...(draft.defaultBranch ? { defaultBranch: draft.defaultBranch } : {}),
    harness: draft.harness,
    model: draft.model,
    sandboxSize: draft.sandboxSize,
    network: draft.network,
    allowedDomains: draft.network === "extended" ? splitDomains(draft.allowedDomains) : [],
    mise: {
      tools,
      files: draft.files.filter((file) => file.path.trim()).map((file) => ({ path: file.path.trim(), content: file.content })),
      environmentVariables: draft.variables.filter((variable) => variable.name.trim()).map((variable) => ({ name: variable.name.trim(), value: variable.value })),
    },
    prepareScript: draft.prepareScript,
    customGuidance: draft.customGuidance,
    dependencyManagement: draft.dependencyManagement,
  };
}

const ENV_NAME = /^[A-Za-z_][A-Za-z0-9_]*$/;
export function isValidEnvName(name: string): boolean {
  return ENV_NAME.test(name);
}

/** Field-level problems that block saving. */
export function validateDraft(draft: EnvironmentDraft, pendingSecrets: CodingEnvVar[], existingSecretNames: string[]): string[] {
  const errors: string[] = [];
  if (!draft.name.trim()) errors.push("Name is required");
  if (!draft.repository.trim()) errors.push("Choose a repository");
  const names = new Set<string>();
  for (const variable of [...draft.variables, ...pendingSecrets]) {
    const name = variable.name.trim();
    if (!name) continue;
    if (!isValidEnvName(name)) errors.push(`Invalid variable name: ${name}`);
    else if (names.has(name) || existingSecretNames.includes(name)) errors.push(`Duplicate variable: ${name}`);
    names.add(name);
  }
  for (const file of draft.files) {
    const path = file.path.trim();
    if (!path && !file.content) continue;
    if (!path) errors.push("File path is required");
    else if (path.startsWith("/") || path.split("/").includes("..")) errors.push(`Files are placed in the sandbox home: ${path}`);
  }
  return errors;
}

/** Keys that look like credentials are suggested as secrets (§18). */
const SECRET_KEY = /(_KEY|_SECRET|_TOKEN|PASSWORD|_DSN)$|PASSWORD|^(SECRET|TOKEN|API_KEY)$/i;
const URL_WITH_CREDENTIALS = /^[a-z][a-z0-9+.-]*:\/\/[^/\s:@]+:[^/\s@]+@/i;
export function looksSecret(name: string, value: string): boolean {
  if (SECRET_KEY.test(name)) return true;
  return /_URL$/i.test(name) && URL_WITH_CREDENTIALS.test(value);
}

/**
 * Client-side dotenv parser (quotes, `export`, comments, multiline double-quoted values,
 * escapes). The server endpoint is authoritative once an environment exists; this keeps
 * "Import from .env" working for a new, unsaved environment.
 */
export function parseDotEnv(content: string): CodingEnvVar[] {
  const result = new Map<string, string>();
  const lines = content.replace(/\r\n?/g, "\n").split("\n");
  for (let index = 0; index < lines.length; index++) {
    let line = lines[index].trim();
    if (!line || line.startsWith("#")) continue;
    if (line.startsWith("export ")) line = line.slice(7).trim();
    const eq = line.indexOf("=");
    if (eq <= 0) continue;
    const name = line.slice(0, eq).trim();
    if (!isValidEnvName(name)) continue;
    let rest = line.slice(eq + 1).trimStart();
    let value: string;
    const quote = rest[0];
    if (quote === '"' || quote === "'" || quote === "`") {
      rest = rest.slice(1);
      let closing = findClosingQuote(rest, quote);
      while (closing < 0 && index + 1 < lines.length) {
        index++;
        rest += "\n" + lines[index];
        closing = findClosingQuote(rest, quote);
      }
      value = closing < 0 ? rest : rest.slice(0, closing);
      if (quote === '"') value = value.replace(/\\(.)/g, (_, char: string) => (char === "n" ? "\n" : char === "r" ? "\r" : char === "t" ? "\t" : char));
    } else {
      const comment = rest.search(/\s#/);
      value = (comment >= 0 ? rest.slice(0, comment) : rest).trim();
    }
    result.set(name, value);
  }
  return [...result].map(([name, value]) => ({ name, value }));
}
function findClosingQuote(text: string, quote: string): number {
  for (let index = 0; index < text.length; index++) {
    if (text[index] === "\\" && quote === '"') {
      index++;
      continue;
    }
    if (text[index] === quote) return index;
  }
  return -1;
}

export function classifyEnv(entries: CodingEnvVar[]): { variables: CodingEnvVar[]; secrets: CodingEnvVar[] } {
  const variables: CodingEnvVar[] = [];
  const secrets: CodingEnvVar[] = [];
  for (const entry of entries) (looksSecret(entry.name, entry.value) ? secrets : variables).push(entry);
  return { variables, secrets };
}

export const SNAPSHOT_STATUS_LABEL: Record<CodingSnapshotStatus, string> = {
  queued: "Queued",
  building: "Building",
  ready: "Ready",
  failed: "Failed",
  canceled: "Canceled",
  expired: "Expired",
  superseded: "Superseded",
};
export function snapshotInProgress(status: CodingSnapshotStatus | undefined): boolean {
  return status === "queued" || status === "building";
}

export function formatBytes(bytes: number | undefined): string {
  if (!bytes) return "";
  return formatFileSize(bytes);
}

export type DiffLine = { kind: "same" | "add" | "remove"; text: string };
/** Line diff (LCS) used by the proposal review. */
export function lineDiff(before: string, after: string): DiffLine[] {
  const a = before ? before.split("\n") : [];
  const b = after ? after.split("\n") : [];
  const dp: number[][] = Array.from({ length: a.length + 1 }, () => new Array<number>(b.length + 1).fill(0));
  for (let i = a.length - 1; i >= 0; i--)
    for (let j = b.length - 1; j >= 0; j--) dp[i][j] = a[i] === b[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]);
  const out: DiffLine[] = [];
  let i = 0;
  let j = 0;
  while (i < a.length && j < b.length) {
    if (a[i] === b[j]) {
      out.push({ kind: "same", text: a[i] });
      i++;
      j++;
    } else if (dp[i + 1][j] >= dp[i][j + 1]) out.push({ kind: "remove", text: a[i++] });
    else out.push({ kind: "add", text: b[j++] });
  }
  while (i < a.length) out.push({ kind: "remove", text: a[i++] });
  while (j < b.length) out.push({ kind: "add", text: b[j++] });
  return out;
}

function toolsText(tools: Record<string, string> | undefined) {
  return Object.entries(tools ?? {}).sort(([a], [b]) => a.localeCompare(b)).map(([tool, version]) => `${tool} = "${version}"`).join("\n");
}
function filesText(files: CodingMiseFile[] | undefined) {
  return (files ?? []).map((file) => `# ${file.path}\n${file.content}`).join("\n");
}
function variablesText(variables: CodingEnvVar[] | undefined) {
  return (variables ?? []).map((variable) => `${variable.name}=${variable.value}`).join("\n");
}

/** Changed fields of a Configure/Fix with agent proposal, as before/after text. */
export function proposalChanges(environment: CodingEnvironment, proposal: CodingEnvironmentProposal): { field: string; before: string; after: string }[] {
  const changes: { field: string; before: string; after: string }[] = [];
  const push = (field: string, before: string, after: string | undefined) => {
    if (after !== undefined && after !== before) changes.push({ field, before, after });
  };
  if (proposal.mise) {
    push("Tool versions", toolsText(environment.mise?.tools), proposal.mise.tools ? toolsText(proposal.mise.tools) : undefined);
    push("Files", filesText(environment.mise?.files), proposal.mise.files ? filesText(proposal.mise.files) : undefined);
    push("Environment variables", variablesText(environment.mise?.environmentVariables), proposal.mise.environmentVariables ? variablesText(proposal.mise.environmentVariables) : undefined);
  }
  push("Prepare script", environment.prepareScript ?? "", proposal.prepareScript);
  push("Custom guidance", environment.customGuidance ?? "", proposal.customGuidance);
  push("Dependency management", environment.dependencyManagement ?? "", proposal.dependencyManagement);
  push("Sandbox size", environment.sandboxSize ?? "", proposal.sandboxSize);
  return changes;
}

/** Spend is stored in micro-USD (§20); credits are shown as USD. */
export function formatMicros(micros: number): string {
  return new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", minimumFractionDigits: 2 }).format((micros || 0) / 1_000_000);
}
