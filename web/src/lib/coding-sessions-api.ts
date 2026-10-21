/**
 * Coding sessions API client (docs/coding-sessions-design.md §7.1).
 * Re-exported from "@/lib/api". Owner of each group noted in the design.
 */
import { jsonRequest, request } from "@/lib/api-client";
import type { AgentMessageAttachment } from "@/types/flow";
import type {
  CodeHostKind,
  CodeHostRepository,
  CodingArtifact,
  CodingEnvironment,
  CodingEnvironmentInput,
  CodingEnvironmentSnapshot,
  CodingEnvVar,
  CodingModelOption,
  CodingSession,
  CodingSessionDiff,
  CodingSessionState,
  CodingSettings,
  CodingStatus,
  CodingTriggerResult,
  CodingUsageSummary,
  CreateCodingSessionInput,
  GitHubAppInstallationSummary,
  GitHubAppStatus,
  ReviewCodingSessionKind,
} from "@/types/coding-sessions";

const id = encodeURIComponent;

function query(params: Record<string, string | number | boolean | undefined>): string {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) if (value !== undefined && value !== "") search.set(key, String(value));
  const text = search.toString();
  return text ? `?${text}` : "";
}

// Settings and status (track F reads, track B serves).
export const fetchCodingStatus = () => request<CodingStatus>("/api/coding/status");
export const fetchCodingSettings = () => request<CodingSettings>("/api/coding/settings");
export const updateCodingSettings = (input: Partial<CodingSettings>) => request<CodingSettings>("/api/coding/settings", jsonRequest("PATCH", input));
export const fetchCodingModels = () => request<CodingModelOption[]>("/api/coding/models");
export const fetchCodingUsage = (since?: string) => request<CodingUsageSummary>(`/api/coding/usage${query({ since })}`);

// Environments (track F reads, track K serves).
export const fetchCodingRepositories = (params: { codeHost: CodeHostKind; hostName?: string; q?: string; cursor?: string }) =>
  request<{ repositories: CodeHostRepository[]; nextCursor?: string }>(`/api/coding/repositories${query(params)}`);
export const fetchCodingEnvironments = (archived = false) => request<CodingEnvironment[]>(`/api/coding/environments${query({ archived: archived || undefined })}`);
export const fetchCodingEnvironment = (environmentId: string) => request<CodingEnvironment>(`/api/coding/environments/${id(environmentId)}`);
export const createCodingEnvironment = (input: CodingEnvironmentInput & { secrets?: CodingEnvVar[] }) => request<CodingEnvironment>("/api/coding/environments", jsonRequest("POST", input));
export const updateCodingEnvironment = (environmentId: string, input: CodingEnvironmentInput & { version: number }) =>
  request<CodingEnvironment>(`/api/coding/environments/${id(environmentId)}`, jsonRequest("PATCH", input));
export const archiveCodingEnvironment = (environmentId: string) => request<CodingEnvironment>(`/api/coding/environments/${id(environmentId)}/archive`, { method: "POST" });
export const unarchiveCodingEnvironment = (environmentId: string) => request<CodingEnvironment>(`/api/coding/environments/${id(environmentId)}/unarchive`, { method: "POST" });
export const addCodingEnvironmentSecrets = (environmentId: string, secrets: CodingEnvVar[]) =>
  request<CodingEnvironment>(`/api/coding/environments/${id(environmentId)}/secrets`, jsonRequest("POST", { secrets }));
export const deleteCodingEnvironmentSecret = (environmentId: string, name: string) =>
  request<CodingEnvironment>(`/api/coding/environments/${id(environmentId)}/secrets/${id(name)}`, { method: "DELETE" });
export const importCodingEnvironmentFile = (environmentId: string, content: string) =>
  request<{ variables: CodingEnvVar[]; secrets: CodingEnvVar[] }>(`/api/coding/environments/${id(environmentId)}/env-import`, jsonRequest("POST", { content }));
export const fetchCodingSnapshots = (environmentId: string) => request<CodingEnvironmentSnapshot[]>(`/api/coding/environments/${id(environmentId)}/snapshots`);
export const refreshCodingSnapshot = (environmentId: string) => request<CodingEnvironmentSnapshot>(`/api/coding/environments/${id(environmentId)}/snapshots`, { method: "POST" });
export const cancelCodingSnapshot = (snapshotId: string) => request<CodingEnvironmentSnapshot>(`/api/coding/snapshots/${id(snapshotId)}/cancel`, { method: "POST" });
export const fetchCodingSnapshotLogs = (snapshotId: string) => request<{ logs: string; complete: boolean }>(`/api/coding/snapshots/${id(snapshotId)}/logs`);
export const configureCodingEnvironmentWithAgent = (environmentId: string, prompt?: string) =>
  request<CodingSession>(`/api/coding/environments/${id(environmentId)}/configure-with-agent`, jsonRequest("POST", { prompt }));
export const fixCodingEnvironmentWithAgent = (environmentId: string, snapshotId?: string) =>
  request<CodingSession>(`/api/coding/environments/${id(environmentId)}/fix-with-agent`, jsonRequest("POST", { snapshotId }));
export const applyCodingEnvironmentProposal = (environmentId: string, discard = false) =>
  request<CodingEnvironment>(`/api/coding/environments/${id(environmentId)}/proposal/apply`, jsonRequest("POST", { discard }));
/** WebSocket URL of the debug terminal (binary frames = TTY bytes; JSON text frames = {type:"resize",cols,rows}). */
export function codingTerminalURL(environmentId: string): string {
  const url = new URL(`/api/coding/environments/${id(environmentId)}/terminal`, location.href);
  url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
  return url.toString();
}

// Sessions (track G reads, track B serves).
export const fetchCodingSessions = (params: { issueId?: string; state?: CodingSessionState; creatorId?: string } = {}) =>
  request<CodingSession[]>(`/api/coding/sessions${query(params)}`);
export const fetchCodingSession = (sessionId: string) => request<CodingSession>(`/api/coding/sessions/${id(sessionId)}`);
export const createCodingSession = (input: CreateCodingSessionInput, idempotencyKey?: string) =>
  request<CodingSession>("/api/coding/sessions", { ...jsonRequest("POST", input), headers: { "Content-Type": "application/json", ...(idempotencyKey ? { "Idempotency-Key": idempotencyKey } : {}) } });
/** A follow-up; `files` are composer uploads (`/uploads/…`, from uploadAgentFile) the runner gets as signed URLs. */
export const promptCodingSession = (sessionId: string, prompt: string, attachments?: string[], files?: AgentMessageAttachment[]) =>
  request<CodingSession>(`/api/coding/sessions/${id(sessionId)}/prompt`, jsonRequest("POST", { prompt, attachments, files: files?.length ? files : undefined }));
export const fetchCodingSessionDiff = (sessionId: string, signal?: AbortSignal) => request<CodingSessionDiff>(`/api/coding/sessions/${id(sessionId)}/diff`, { signal });
export const setCodingSessionSubscription = (sessionId: string, subscribed: boolean) =>
  request<CodingSession>(`/api/coding/sessions/${id(sessionId)}/subscription`, jsonRequest("POST", { subscribed }));
export const stopCodingSession = (sessionId: string) => request<CodingSession>(`/api/coding/sessions/${id(sessionId)}/stop`, { method: "POST" });
/** `useDefaultModel` restarts with Auto; `model` starts the task over from its first prompt with that model. */
export const restartCodingSession = (sessionId: string, options: { useDefaultModel?: boolean; model?: string } = {}) =>
  request<CodingSession>(`/api/coding/sessions/${id(sessionId)}/restart`, jsonRequest("POST", options));
export const approveCodingSession = (sessionId: string, reject = false) =>
  request<CodingSession>(`/api/coding/sessions/${id(sessionId)}/approve`, jsonRequest("POST", { reject }));
/** Rejects a pending approval, or ends a session waiting for an answer. */
export const dismissCodingSession = (sessionId: string) => request<CodingSession>(`/api/coding/sessions/${id(sessionId)}/dismiss`, { method: "POST" });
export const createCodingSessionPullRequest = (sessionId: string) =>
  request<CodingSession>(`/api/coding/sessions/${id(sessionId)}/pull-request`, { method: "POST" });
export const fetchCodingSandboxLogs = (sessionId: string, sandboxId: string) =>
  request<{ logs: string; complete: boolean }>(`/api/coding/sessions/${id(sessionId)}/sandboxes/${id(sandboxId)}/logs`);
export const fetchCodingSessionArtifacts = (sessionId: string) => request<CodingArtifact[]>(`/api/coding/sessions/${id(sessionId)}/artifacts`);

// Triggers (track G calls, track L serves).
/**
 * The session a trigger started or continued. The endpoints answer `{ outcome, session, … }`, not the session itself;
 * a trigger that started nothing rejects with the server's reason.
 */
export function triggerSession(result: CodingTriggerResult | null | undefined): CodingSession {
  const session = result?.session
  if (session?.id) return session
  throw new Error(result?.message || "Could not start coding session");
}
export const startIssueCodingSession = (issueId: string, input: { prompt?: string; environmentId?: string } = {}) =>
  request<CodingTriggerResult>(`/api/issues/${id(issueId)}/coding-session`, jsonRequest("POST", input)).then(triggerSession);
export const startReviewCodingSession = (reviewId: string, input: { kind: ReviewCodingSessionKind; prompt?: string }) =>
  request<CodingTriggerResult>(`/api/reviews/${id(reviewId)}/coding-session`, jsonRequest("POST", input)).then(triggerSession);

// GitHub App (track F calls, track D serves).
export const fetchGitHubApp = () => request<GitHubAppStatus>("/api/integrations/github-app");
export const startGitHubAppInstall = () => request<{ url: string }>("/api/integrations/github-app/install", { method: "POST" });
export const removeGitHubAppInstallation = (installationId: string) =>
  request<void>(`/api/integrations/github-app/installations/${id(installationId)}`, { method: "DELETE" });
/** App installations the admin's linked GitHub account can access that the workspace has not linked yet. */
export const fetchAvailableGitHubAppInstallations = () =>
  request<{ installations: GitHubAppInstallationSummary[] }>("/api/integrations/github-app/available-installations");
/** Links an existing installation (GitHub sends no setup redirect when the App is already installed). */
export const linkGitHubAppInstallation = (installationId: string) =>
  request<GitHubAppInstallationSummary>("/api/integrations/github-app/installations", jsonRequest("POST", { installationId }));
/** Links the viewer's GitHub account (user-to-server OAuth) for personal access checks. */
export const startGitHubAppAuthorization = () => request<{ url: string }>("/api/integrations/github-app/authorize", { method: "POST" });
