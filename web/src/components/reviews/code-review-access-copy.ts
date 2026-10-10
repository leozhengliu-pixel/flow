import type { CodeReviewAccessReason } from "@/lib/code-access";

export type CodeReviewAccessActionTarget =
  | { kind: "connect"; provider: "github" | "gitlab" }
  | { kind: "grant-code-access"; provider: "github" }
  | { kind: "reconnect"; provider: "github" };

export function codeReviewAccessCopy(reason: CodeReviewAccessReason): {
  title: string;
  description: string;
  primary?: { label: string; target: CodeReviewAccessActionTarget };
  secondary?: { label: string; target: CodeReviewAccessActionTarget };
} {
  switch (reason) {
    case "workspace_connection_missing":
      return {
        title: "Connect a code host",
        description: "Connect GitHub or GitLab to sync pull and merge requests.",
        primary: { label: "Connect GitHub", target: { kind: "connect", provider: "github" } },
        secondary: { label: "Connect GitLab", target: { kind: "connect", provider: "gitlab" } },
      };
    case "workspace_code_access_missing":
      return {
        title: "Code access required",
        description: "Reviews and diffs need a GitHub integration with code access.",
        primary: {
          label: "Enable code access",
          target: { kind: "grant-code-access", provider: "github" },
        },
      };
    case "personal_connection_missing":
      return {
        title: "Personal GitHub connection required",
        description: "Connect your personal GitHub account to review private repositories.",
        primary: { label: "Connect GitHub", target: { kind: "connect", provider: "github" } },
      };
    case "personal_connection_reconnect_required":
      return {
        title: "Reconnect GitHub",
        description: "Your GitHub connection needs to be re-authorized before reviews can sync.",
        primary: { label: "Reconnect GitHub", target: { kind: "reconnect", provider: "github" } },
      };
    default:
      return {
        title: "Nothing to review",
        description: "Reviews assigned to you will appear here.",
      };
  }
}
