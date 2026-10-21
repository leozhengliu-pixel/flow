/**
 * Texts for the machine codes the pull/merge request import records (backoffs,
 * run failures) and answers requests with. English keys; zh-CN is in
 * translations-reviews.ts. An unknown code (a newer server) or none (a row
 * from before codes) shows the server's own message.
 */
const reviewImportErrorTexts: Record<string, string> = {
  rate_limited: "The code host's rate limit is reached; retrying later",
  upstream_error: "The code host had a temporary problem; retrying later",
  auth_failed: "The code host refused Flow's access; check or reconnect the connection",
  repository_not_found: "The code host could not find this repository or request, or Flow cannot see it",
  repository_no_access: "Flow's connection has no access to this repository",
  not_connected: "No code host connection is available; connect it first",
  no_source: "No connected {provider} source reaches this repository",
  request_rejected: "The code host rejected the request",
  network_unreachable: "The code host could not be reached",
  timeout: "The code host did not answer in time",
  too_many_files: "This request has too many changed files to sync",
  workspace_not_found: "The workspace was not found",
  unknown: "The import failed for an unknown reason",
  provider_invalid: "The provider must be GitHub or GitLab",
  repository_invalid: "{repository} is not a repository (owner/name)",
  source_not_connected: "Connect {provider} before approving its repositories",
  too_many_repositories: "Approve at most {max} repositories",
  already_running: "A sync with the code hosts is already running",
  server_off: "Pull and merge request import is turned off on this server",
  workspace_off: "Turn on pull request import in the GitHub or GitLab settings first",
  read_failed: "Could not read the pull request import",
  save_failed: "Could not save the pull request import",
  sync_failed: "Could not sync with the code hosts",
};

interface CodedError {
  code?: string;
  params?: Record<string, string>;
  message?: string;
}

/** The localized text of a coded error, or its message when the code is unknown. */
export function reviewImportErrorText(t: (key: string) => string, error: CodedError, fallback = ""): string {
  const template = error.code ? reviewImportErrorTexts[error.code] : undefined;
  if (!template) return error.message || fallback;
  return t(template).replace(/\{(\w+)\}/g, (_, key: string) => error.params?.[key] ?? "");
}
