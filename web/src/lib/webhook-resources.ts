/**
 * Resource types a webhook can subscribe to, in the order the reference app's
 * "Data change events" list shows them. `id` is what the API stores,
 * `type` the payload `type` (and Flow-Event header) it delivers.
 */
export interface WebhookResourceType {
  id: string;
  type: string;
  label: string;
  /** Only an OAuth application's webhook can subscribe. */
  applicationOnly?: boolean;
}

export const WEBHOOK_RESOURCE_TYPES: WebhookResourceType[] = [
  { id: "issues", type: "Issue", label: "Issues" },
  { id: "attachments", type: "Attachment", label: "Issue attachments" },
  { id: "comments", type: "Comment", label: "Comments" },
  { id: "labels", type: "IssueLabel", label: "Issue labels" },
  { id: "reactions", type: "Reaction", label: "Emoji reactions" },
  { id: "relations", type: "IssueRelation", label: "Issue relations" },
  { id: "projects", type: "Project", label: "Projects" },
  { id: "project_updates", type: "ProjectUpdate", label: "Project updates" },
  { id: "milestones", type: "ProjectMilestone", label: "Project milestones" },
  { id: "documents", type: "Document", label: "Documents" },
  { id: "initiatives", type: "Initiative", label: "Initiatives" },
  { id: "initiative_updates", type: "InitiativeUpdate", label: "Initiative updates" },
  { id: "cycles", type: "Cycle", label: "Cycles" },
  { id: "customers", type: "Customer", label: "Customers" },
  { id: "customer_requests", type: "CustomerNeed", label: "Customer requests" },
  { id: "users", type: "User", label: "Users" },
  { id: "releases", type: "Release", label: "Releases" },
  { id: "release_notes", type: "ReleaseNote", label: "Release notes" },
  { id: "issue_sla", type: "IssueSLA", label: "Issue SLA" },
  {
    id: "agent_session_events",
    type: "AgentSessionEvent",
    label: "Agent session events",
    applicationOnly: true,
  },
  {
    id: "app_user_notifications",
    type: "AppUserNotification",
    label: "Inbox notifications",
    applicationOnly: true,
  },
  {
    id: "permission_changes",
    type: "PermissionChange",
    label: "Permission changes",
    applicationOnly: true,
  },
  {
    id: "oauth_app",
    type: "OAuthApp",
    label: "OAuth app revoked",
    applicationOnly: true,
  },
];

export function webhookResourceLabel(id: string): string {
  return WEBHOOK_RESOURCE_TYPES.find((item) => item.id === id)?.label ?? id;
}

/** HTTPS and not a local/private host, as the server requires. */
export function isAllowedWebhookUrl(value: string): boolean {
  try {
    const parsed = new URL(value);
    if (parsed.protocol !== "https:" || parsed.username || parsed.password)
      return false;
    const host = parsed.hostname.toLowerCase();
    if (
      host === "localhost" ||
      host.endsWith(".localhost") ||
      host.endsWith(".local") ||
      host.endsWith(".internal") ||
      host === "0.0.0.0" ||
      /^127\./.test(host) ||
      /^10\./.test(host) ||
      /^192\.168\./.test(host) ||
      /^172\.(1[6-9]|2\d|3[01])\./.test(host) ||
      host === "[::1]"
    ) {
      return false;
    }
    return Boolean(host);
  } catch {
    return false;
  }
}

type WebhookStateFields = {
  enabled: boolean;
  disabledReason?: string;
  failingSince?: string;
  managedBy?: string;
  signingStatus?: string;
  signingEnabled?: boolean;
  secretRevokedAt?: string;
};

/**
 * Signing state. The API computes signingStatus; webhooks read from
 * bootstrap only carry signingEnabled, which webhooks created before
 * signing lack.
 */
export function webhookSigningStatus(
  webhook: WebhookStateFields,
): "active" | "rotationRequired" | "revoked" {
  if (webhook.signingStatus === "active" || webhook.signingStatus === "revoked")
    return webhook.signingStatus;
  if (webhook.signingStatus === "rotationRequired") return "rotationRequired";
  if (webhook.signingEnabled || webhook.managedBy === "application") return "active";
  return webhook.secretRevokedAt ? "revoked" : "rotationRequired";
}

/** Short state of a webhook for lists ("" when healthy). */
export function webhookStateLabel(webhook: WebhookStateFields): string {
  if (!webhook.enabled && webhook.disabledReason === "deliveryFailures")
    return "Disabled after repeated failures";
  if (webhook.enabled && webhook.failingSince) return "Deliveries failing";
  if (webhook.managedBy === "application") return "Managed by application";
  if (webhookSigningStatus(webhook) === "rotationRequired")
    return "Unsigned until rotated";
  return "";
}
