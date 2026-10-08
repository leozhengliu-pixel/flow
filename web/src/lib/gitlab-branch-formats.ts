/** Branch formats offered by the reference app, keyed by the value Flow stores. */
export const GITLAB_BRANCH_FORMATS = [
  "username/identifier-title",
  "username/identifier",
  "username-identifier-title",
  "username-identifier",
  "identifier-title",
  "title-identifier",
  "identifier",
  "feature/identifier-title",
  "feature/identifier",
] as const;

export function gitlabBranchExample(format: string) {
  return format
    .replace("username", "alex")
    .replace("identifier", "eng-123")
    .replace("title", "fix-login-error");
}
