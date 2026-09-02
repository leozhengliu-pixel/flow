import type {
  AccountBootstrap,
  AccountSessionInfo,
  APIKey,
  Ask,
  Attachment,
  AuthSession,
  BootstrapData,
  CodeReview,
  Comment,
  Customer,
  CustomerMutationInput,
  CustomerRequest,
  CustomEmoji,
  Cycle,
  CycleMutationInput,
  CycleSettings,
  CycleSettingsMutationInput,
  Draft,
  ExportJob,
  Favorite,
  ImportJob,
  Initiative,
  InitiativeMutationInput,
  InitiativeResource,
  InitiativeUpdate,
  IntegrationConnection,
  Invitation,
  InvitationPreview,
  Issue,
  IssueLabel,
  IssueRelation,
  IssueRelationType,
  IssueTemplate,
  IssueTemplateMutationInput,
  IssueUpdateInput,
  FlowDocument,
  LabelGroup,
  Loop,
  MigrationEntityMapping,
  MigrationJob,
  Notification,
  NotificationDelivery,
  NotificationList,
  NotificationMutationInput,
  NotificationPreferences,
  OAuthApplication,
  Presence,
  Project,
  ProjectMilestone,
  ProjectResource,
  ProjectStatus,
  ProjectTemplate,
  ProjectUpdate,
  Release,
  ReleasePipeline,
  ReleasePipelineAccessKey,
  SavedView,
  SavedViewMutationInput,
  SearchResourceType,
  SearchResponse,
  SLARule,
  Subscription,
  Team,
  TeamRole,
  TeamSettings,
  TeamSettingsMutationInput,
  User,
  UserSettings,
  Webhook,
  WorkflowState,
  WorkspaceMember,
  WorkspaceMembership,
  WorkspaceMutationInput,
  WorkspaceRole,
  WorkspaceSettings,
  WorkspaceUsage,
} from "@/types/flow";
import { jsonRequest, request } from "@/lib/api-client";

export { ApiError, realtimeClientId } from "@/lib/api-client";
import type {
  ProjectCreateInput,
  ProjectMutationInput,
} from "@/components/projects-page/projects-page";
import type { DocumentTemplate } from "@/types/flow";
import type {
  CursorPage,
  Dashboard,
  DashboardWidget,
  DocumentPermission,
  DashboardWidgetResult,
  FeedItem,
  FilterSuggestion,
  Meeting,
  SemanticSearchResponse,
  WorkspacePost,
} from "@/types/flow";
import type {
  AuthIdentity,
  GitAutomationState,
  IdentityProvider,
  IntegrationDelivery,
  TargetBranch,
} from "@/types/flow";
import type {
  EmailIntakeAddress,
  PushSubscription,
  TriageResponsibility,
  TriageRoutingRule,
  WorkflowAction,
  WorkflowDefinition,
  WorkflowRun,
} from "@/types/flow";

export function fetchAccountBootstrap(): Promise<AccountBootstrap> {
  return request("/api/account/bootstrap");
}
export function setLastWorkspace(workspaceKey: string): Promise<void> {
  return request(
    "/api/account/last-workspace",
    jsonRequest("PUT", { workspaceKey }),
  );
}
export function fetchAuthSession(): Promise<AuthSession> {
  return request("/api/auth/session");
}
export type AuthProviderConfig = {
  email: boolean;
  providers: Array<{
    id: "google" | "oidc" | "saml";
    name: string;
    startUrl: string;
  }>;
};
export function fetchAuthProviders(): Promise<AuthProviderConfig> {
  return request("/api/auth/providers");
}
export function registerAccount(input: {
  name: string;
  email: string;
  password: string;
}): Promise<{
  user: AuthSession["user"];
  verificationRequired: boolean;
  verificationToken?: string;
}> {
  return request("/api/auth/register", jsonRequest("POST", input));
}
export function verifyEmail(token: string): Promise<{ verified: boolean }> {
  return request("/api/auth/verify-email", jsonRequest("POST", { token }));
}
export function resendVerification(
  email: string,
): Promise<{ sent: boolean; verificationToken?: string }> {
  return request(
    "/api/auth/resend-verification",
    jsonRequest("POST", { email }),
  );
}
export function loginAccount(
  email: string,
  password: string,
): Promise<AuthSession> {
  return request("/api/auth/login", jsonRequest("POST", { email, password }));
}
export function logoutAccount(): Promise<void> {
  return request("/api/auth/logout", { method: "POST" });
}
export function forgotPassword(
  email: string,
): Promise<{ sent: boolean; resetToken?: string }> {
  return request("/api/auth/forgot-password", jsonRequest("POST", { email }));
}
export function resetPassword(
  token: string,
  password: string,
): Promise<{ reset: boolean }> {
  return request(
    "/api/auth/reset-password",
    jsonRequest("POST", { token, password }),
  );
}
export function fetchInvitationPreview(
  token: string,
): Promise<InvitationPreview> {
  return request(`/api/invitations/preview/${encodeURIComponent(token)}`);
}
export function acceptInvitation(
  token: string,
): Promise<import("@/types/flow").WorkspaceMembership> {
  return request("/api/invitations/accept", jsonRequest("POST", { token }));
}
export function inviteMembers(
  workspaceKey: string,
  input: { emails: string[]; role: WorkspaceRole; teamIds: string[] },
): Promise<Invitation[]> {
  return request(
    `/api/workspaces/${encodeURIComponent(workspaceKey)}/invitations`,
    jsonRequest("POST", input),
  );
}
export function revokeInvitation(
  workspaceKey: string,
  invitationId: string,
): Promise<void> {
  return request(
    `/api/workspaces/${encodeURIComponent(workspaceKey)}/invitations/${encodeURIComponent(invitationId)}`,
    { method: "DELETE" },
  );
}
export function resendInvitation(
  workspaceKey: string,
  invitationId: string,
): Promise<Invitation> {
  return request(
    `/api/workspaces/${encodeURIComponent(workspaceKey)}/invitations/${encodeURIComponent(invitationId)}/resend`,
    { method: "POST" },
  );
}
export function updateMemberRole(
  workspaceKey: string,
  userId: string,
  role: WorkspaceRole,
): Promise<WorkspaceMember> {
  return request(
    `/api/workspaces/${encodeURIComponent(workspaceKey)}/members/${encodeURIComponent(userId)}`,
    jsonRequest("PATCH", { role }),
  );
}
export function updateMemberIdentity(
  workspaceKey: string,
  userId: string,
  input: { displayName?: string; username?: string; email?: string },
): Promise<WorkspaceMember> {
  return request(
    `/api/workspaces/${encodeURIComponent(workspaceKey)}/members/${encodeURIComponent(userId)}`,
    jsonRequest("PATCH", input),
  );
}
export function suspendMember(
  workspaceKey: string,
  userId: string,
): Promise<void> {
  return request(
    `/api/workspaces/${encodeURIComponent(workspaceKey)}/members/${encodeURIComponent(userId)}/suspend`,
    { method: "POST" },
  );
}
export function resumeMember(
  workspaceKey: string,
  userId: string,
): Promise<void> {
  return request(
    `/api/workspaces/${encodeURIComponent(workspaceKey)}/members/${encodeURIComponent(userId)}/resume`,
    { method: "POST" },
  );
}
export function removeMember(
  workspaceKey: string,
  userId: string,
): Promise<void> {
  return request(
    `/api/workspaces/${encodeURIComponent(workspaceKey)}/members/${encodeURIComponent(userId)}`,
    { method: "DELETE" },
  );
}
export function setTeamMembership(
  workspaceKey: string,
  teamId: string,
  userId: string,
  member: boolean,
  role: TeamRole = "member",
): Promise<void> {
  return request(
    `/api/workspaces/${encodeURIComponent(workspaceKey)}/teams/${encodeURIComponent(teamId)}/members/${encodeURIComponent(userId)}`,
    jsonRequest("PUT", { member, role }),
  );
}
export function fetchBootstrap(workspaceKey?: string): Promise<BootstrapData> {
  return request(
    "/api/bootstrap",
    workspaceKey ? { headers: { "X-Workspace-Key": workspaceKey } } : undefined,
  );
}
export function createWorkspace(
  input: WorkspaceMutationInput & { name: string; urlKey: string },
): Promise<BootstrapData> {
  return request("/api/workspaces", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}
export function updateWorkspace(
  workspaceKey: string,
  input: WorkspaceMutationInput,
): Promise<BootstrapData> {
  return request(`/api/workspaces/${encodeURIComponent(workspaceKey)}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}
export function uploadWorkspaceLogo(
  workspaceKey: string,
  file: File,
): Promise<BootstrapData> {
  const body = new FormData();
  body.append("file", file);
  return request(`/api/workspaces/${encodeURIComponent(workspaceKey)}/logo`, {
    method: "POST",
    body,
  });
}
export function deleteWorkspaceLogo(
  workspaceKey: string,
): Promise<BootstrapData> {
  return request(`/api/workspaces/${encodeURIComponent(workspaceKey)}/logo`, {
    method: "DELETE",
  });
}
export function deleteWorkspace(workspaceKey: string): Promise<void> {
  return request(`/api/workspaces/${encodeURIComponent(workspaceKey)}`, {
    method: "DELETE",
  });
}
export function updateWorkspaceSettings(
  settings: Record<string, unknown>,
): Promise<Record<string, unknown>> {
  return request("/api/workspace/settings", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(settings),
  });
}
export function fetchUserSettings(
  workspaceKey?: string,
): Promise<UserSettings> {
  return request(
    "/api/account/settings",
    workspaceKey ? { headers: { "X-Workspace-Key": workspaceKey } } : undefined,
  );
}
export function updateUserSettings(input: UserSettings): Promise<UserSettings> {
  return request("/api/account/settings", jsonRequest("PATCH", input));
}
export function updateAccountProfile(input: {
  displayName: string;
  username: string;
  avatarUrl?: string;
  jobTitle?: string;
}): Promise<User> {
  return request("/api/account/profile", jsonRequest("PATCH", input));
}
export function fetchAccountSessions(): Promise<AccountSessionInfo[]> {
  return request("/api/account/sessions");
}
export function revokeOtherAccountSessions(): Promise<void> {
  return request("/api/account/sessions/others", { method: "DELETE" });
}
export function revokeAccountSession(id: string): Promise<void> {
  return request(`/api/account/sessions/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}
export function fetchAccountIdentities(): Promise<AuthIdentity[]> {
  return request("/api/account/identities");
}
export function unlinkAccountIdentity(id: string): Promise<void> {
  return request(`/api/account/identities/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}
export function discoverWorkspaceSSO(email: string): Promise<
  Array<{
    id: string;
    name: string;
    type: string;
    workspace: string;
    startUrl: string;
  }>
> {
  return request(`/api/auth/discovery?email=${encodeURIComponent(email)}`);
}
export function changeAccountPassword(
  currentPassword: string,
  newPassword: string,
): Promise<{ changed: boolean }> {
  return request(
    "/api/account/change-password",
    jsonRequest("POST", { currentPassword, newPassword }),
  );
}
export function updateWorkspacePreferences(
  input: WorkspaceSettings,
): Promise<WorkspaceSettings> {
  return request("/api/workspace/preferences", jsonRequest("PATCH", input));
}
export function createWorkspaceLabel(input: {
  name: string;
  description?: string;
  color?: string;
  resourceType: IssueLabel["resourceType"];
  groupId?: string;
}): Promise<IssueLabel> {
  return request("/api/labels", jsonRequest("POST", input));
}
export function updateWorkspaceLabel(
  id: string,
  input: Partial<
    Pick<
      IssueLabel,
      "name" | "description" | "color" | "groupId" | "archivedAt"
    >
  >,
): Promise<IssueLabel> {
  return request(`/api/labels/${id}`, jsonRequest("PATCH", input));
}
export function deleteWorkspaceLabel(id: string): Promise<void> {
  return request(`/api/labels/${id}`, { method: "DELETE" });
}
export function createLabelGroup(input: {
  name: string;
  color?: string;
  description?: string;
  resourceType: "issue" | "project";
}): Promise<LabelGroup> {
  return request("/api/label-groups", jsonRequest("POST", input));
}
export function updateLabelGroup(
  id: string,
  input: Partial<
    Pick<LabelGroup, "name" | "color" | "description" | "archivedAt">
  >,
): Promise<LabelGroup> {
  return request(`/api/label-groups/${id}`, jsonRequest("PATCH", input));
}
export function deleteLabelGroup(id: string): Promise<void> {
  return request(`/api/label-groups/${id}`, { method: "DELETE" });
}
export function createProjectStatus(input: {
  name: string;
  description?: string;
  color?: string;
  type?: string;
}): Promise<ProjectStatus> {
  return request("/api/project-statuses", jsonRequest("POST", input));
}
export function updateProjectStatus(
  id: string,
  input: Partial<
    Pick<ProjectStatus, "name" | "description" | "color" | "type">
  >,
): Promise<ProjectStatus> {
  return request(`/api/project-statuses/${id}`, jsonRequest("PATCH", input));
}
export function deleteProjectStatus(id: string): Promise<void> {
  return request(`/api/project-statuses/${id}`, { method: "DELETE" });
}
export function reorderProjectStatuses(
  ids: string[],
): Promise<ProjectStatus[]> {
  return request("/api/project-statuses/reorder", jsonRequest("POST", { ids }));
}
export function createWorkspaceIssueTemplate(
  input: IssueTemplateMutationInput & { name: string },
): Promise<IssueTemplate> {
  return request("/api/issue-templates", jsonRequest("POST", input));
}
export function updateWorkspaceIssueTemplate(
  id: string,
  input: IssueTemplateMutationInput,
): Promise<IssueTemplate> {
  return request(`/api/issue-templates/${id}`, jsonRequest("PATCH", input));
}
export function deleteWorkspaceIssueTemplate(id: string): Promise<void> {
  return request(`/api/issue-templates/${id}`, { method: "DELETE" });
}
export function fetchAPIKeys(): Promise<APIKey[]> {
  return request("/api/api-keys");
}
export function createAPIKey(input: {
  name: string;
  scopes: string[];
  teamIds: string[];
}): Promise<{ key: APIKey; secret: string }> {
  return request("/api/api-keys", jsonRequest("POST", input));
}
export function revokeAPIKey(id: string): Promise<void> {
  return request(`/api/api-keys/${id}`, { method: "DELETE" });
}
export function fetchOAuthApplications(): Promise<OAuthApplication[]> {
  return request("/api/oauth-applications");
}
export function createOAuthApplication(input: {
  name: string;
  description?: string;
  redirectUris: string[];
  scopes: string[];
}): Promise<OAuthApplication> {
  return request("/api/oauth-applications", jsonRequest("POST", input));
}
export function updateOAuthApplication(
  id: string,
  input: Partial<
    Pick<OAuthApplication, "name" | "description" | "redirectUris" | "scopes">
  >,
): Promise<OAuthApplication> {
  return request(`/api/oauth-applications/${id}`, jsonRequest("PATCH", input));
}
export function deleteOAuthApplication(id: string): Promise<void> {
  return request(`/api/oauth-applications/${id}`, { method: "DELETE" });
}
export interface OAuthAuthorizationRequest {
  client: {
    client_id: string;
    client_name: string;
    client_uri?: string;
    logo_uri?: string;
  };
  redirectUri: string;
  scopes: string[];
  scopeLabels: string[];
  workspaces: WorkspaceMembership[];
  viewer: User;
}
export function fetchOAuthAuthorizationRequest(
  query: string,
): Promise<OAuthAuthorizationRequest> {
  return request(
    `/api/oauth/authorization-request?${query.replace(/^\?/, "")}`,
  );
}
export function decideOAuthAuthorization(input: {
  clientId: string;
  redirectUri: string;
  responseType: string;
  scope: string;
  state: string;
  codeChallenge: string;
  codeChallengeMethod: string;
  resource?: string;
  workspaceKey: string;
  approve: boolean;
}): Promise<{ redirect: string }> {
  return request(
    "/api/oauth/authorization-request",
    jsonRequest("POST", input),
  );
}
export function revokeOAuthAuthorization(id: string): Promise<void> {
  return request(`/api/oauth/authorizations/${id}`, { method: "DELETE" });
}
export function fetchIntegrations(): Promise<IntegrationConnection[]> {
  return request("/api/integrations");
}
export function fetchIdentityProviders(): Promise<IdentityProvider[]> {
  return request("/api/identity-providers");
}
export function createIdentityProvider(
  input: Omit<
    IdentityProvider,
    | "id"
    | "workspaceId"
    | "createdAt"
    | "updatedAt"
    | "discoveryStatus"
    | "lastVerifiedAt"
  >,
): Promise<IdentityProvider> {
  return request("/api/identity-providers", jsonRequest("POST", input));
}
export function updateIdentityProvider(
  id: string,
  input: Partial<IdentityProvider>,
): Promise<IdentityProvider> {
  return request(`/api/identity-providers/${id}`, jsonRequest("PATCH", input));
}
export function deleteIdentityProvider(id: string): Promise<void> {
  return request(`/api/identity-providers/${id}`, { method: "DELETE" });
}
export function verifyIdentityProvider(id: string): Promise<IdentityProvider> {
  return request(`/api/identity-providers/${id}/verify`, { method: "POST" });
}
export function retryIntegrationDelivery(
  id: string,
): Promise<IntegrationDelivery> {
  return request(`/api/integration-deliveries/${id}/retry`, { method: "POST" });
}
export function createIntegrationDelivery(input: {
  connectionId: string;
  eventType: string;
  resourceId?: string;
  channel?: string;
  payload: unknown;
  previousValues?: unknown;
}): Promise<IntegrationDelivery> {
  return request("/api/integration-deliveries", jsonRequest("POST", input));
}
export function upsertGitAutomation(
  input: Partial<GitAutomationState> &
    Pick<
      GitAutomationState,
      "teamId" | "repository" | "event" | "workflowStateId"
    >,
): Promise<GitAutomationState> {
  return request(
    input.id ? `/api/git-automations/${input.id}` : "/api/git-automations",
    jsonRequest(input.id ? "PUT" : "POST", input),
  );
}
export function deleteGitAutomation(id: string): Promise<void> {
  return request(`/api/git-automations/${id}`, { method: "DELETE" });
}
export function upsertTargetBranch(
  input: Partial<TargetBranch> &
    Pick<TargetBranch, "teamId" | "repository" | "branch">,
): Promise<TargetBranch> {
  return request(
    input.id ? `/api/target-branches/${input.id}` : "/api/target-branches",
    jsonRequest(input.id ? "PUT" : "POST", input),
  );
}
export function deleteTargetBranch(id: string): Promise<void> {
  return request(`/api/target-branches/${id}`, { method: "DELETE" });
}
export function findAttachmentsByURL(url: string): Promise<Attachment[]> {
  return request(`/api/attachments/by-url?url=${encodeURIComponent(url)}`);
}
export function connectIntegration(
  provider: string,
  input: { name?: string; config?: Record<string, string> },
): Promise<IntegrationConnection> {
  return request(`/api/integrations/${provider}`, jsonRequest("PUT", input));
}
export function startIntegrationOAuth(provider: string): Promise<{
  provider: string;
  connectionId: string;
  state: string;
  authorizationURL: string;
}> {
  return request(
    `/api/integrations/${provider}/oauth/start`,
    jsonRequest("POST", {}),
  );
}
export function disconnectIntegration(provider: string): Promise<void> {
  return request(`/api/integrations/${provider}`, { method: "DELETE" });
}
export function updateIntegrationConnection(
  provider: string,
  id: string,
  input: { name?: string; status?: string; config?: Record<string, string> },
): Promise<IntegrationConnection> {
  return request(
    `/api/integrations/${provider}/${id}`,
    jsonRequest("PATCH", input),
  );
}
export function testIntegrationConnection(
  provider: "github" | "gitlab",
  id?: string,
  input?: { token?: string; host?: string },
): Promise<{
  provider: string;
  connectionId?: string;
  status: string;
  username?: string;
  testedAt: string;
}> {
  const suffix = id ? `/${encodeURIComponent(id)}` : "";
  return request(
    `/api/integrations/${provider}${suffix}/test`,
    jsonRequest("POST", input ?? {}),
  );
}
export function disconnectIntegrationConnection(
  provider: string,
  id: string,
): Promise<void> {
  return request(`/api/integrations/${provider}/${id}`, { method: "DELETE" });
}
export function listReviews(): Promise<CodeReview[]> {
  return request("/api/reviews");
}
export function getReview(id: string): Promise<CodeReview> {
  return request(`/api/reviews/${id}`);
}
export function updateReview(
  id: string,
  input: Partial<
    Pick<
      CodeReview,
      | "title"
      | "status"
      | "reviewerIds"
      | "issueIds"
      | "favorite"
      | "draft"
      | "branchState"
    >
  >,
): Promise<CodeReview> {
  return request(`/api/reviews/${id}`, jsonRequest("PATCH", input));
}
export function submitReview(
  id: string,
  input: { decision: "approve" | "comment" | "requestChanges"; body: string },
): Promise<CodeReview> {
  return request(`/api/reviews/${id}/submit`, jsonRequest("POST", input));
}
export function commentOnReview(id: string, body: string): Promise<CodeReview> {
  return request(`/api/reviews/${id}/comments`, jsonRequest("POST", { body }));
}
export function fetchWorkspaceUsage(): Promise<WorkspaceUsage> {
  return request("/api/usage");
}
export function fetchWebhooks(): Promise<Webhook[]> {
  return request("/api/webhooks");
}
export function createWebhook(
  input: Pick<
    Webhook,
    "name" | "url" | "resourceTypes" | "teamIds" | "enabled"
  >,
): Promise<Webhook> {
  return request("/api/webhooks", jsonRequest("POST", input));
}
export function updateWebhook(
  id: string,
  input: Partial<
    Pick<Webhook, "name" | "url" | "resourceTypes" | "teamIds" | "enabled">
  >,
): Promise<Webhook> {
  return request(`/api/webhooks/${id}`, jsonRequest("PATCH", input));
}
export function deleteWebhook(id: string): Promise<void> {
  return request(`/api/webhooks/${id}`, { method: "DELETE" });
}
export function createTeam(
  workspaceKey: string,
  input: {
    name: string;
    key: string;
    color?: string;
    icon?: string;
    private?: boolean;
    parentTeamId?: string;
    copyFromTeamId?: string;
    timezone?: string;
  },
): Promise<Team> {
  return request(`/api/workspaces/${encodeURIComponent(workspaceKey)}/teams`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}
export function updateTeam(
  workspaceKey: string,
  teamId: string,
  input: Partial<Pick<Team, "name" | "key" | "color" | "icon" | "private">> & {
    retired?: boolean;
  },
): Promise<Team> {
  return request(
    `/api/workspaces/${encodeURIComponent(workspaceKey)}/teams/${teamId}`,
    {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    },
  );
}
export function deleteTeam(
  workspaceKey: string,
  teamId: string,
): Promise<void> {
  return request(
    `/api/workspaces/${encodeURIComponent(workspaceKey)}/teams/${teamId}`,
    { method: "DELETE" },
  );
}
export function createCustomer(
  input: CustomerMutationInput & { name: string },
): Promise<Customer> {
  return request("/api/customers", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}
export function updateCustomer(
  id: string,
  input: CustomerMutationInput,
): Promise<Customer> {
  return request(`/api/customers/${id}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}
export function deleteCustomer(id: string): Promise<void> {
  return request(`/api/customers/${id}`, { method: "DELETE" });
}
export function createCustomerRequest(input: {
  customerId: string;
  body: string;
  source?: string;
  sourceUrl?: string;
  issueId?: string;
  projectId?: string;
}): Promise<CustomerRequest> {
  return request("/api/customer-requests", jsonRequest("POST", input));
}
export function updateCustomerRequest(
  id: string,
  input: Partial<
    Pick<
      CustomerRequest,
      "body" | "source" | "sourceUrl" | "issueId" | "projectId"
    >
  >,
): Promise<CustomerRequest> {
  return request(`/api/customer-requests/${id}`, jsonRequest("PATCH", input));
}
export function deleteCustomerRequest(id: string): Promise<void> {
  return request(`/api/customer-requests/${id}`, { method: "DELETE" });
}
export function uploadCustomerRequestAttachment(
  id: string,
  file: File,
): Promise<Attachment> {
  const body = new FormData();
  body.append("file", file);
  return request(`/api/customer-requests/${id}/attachments`, {
    method: "POST",
    body,
  });
}
export function deleteCustomerRequestAttachment(
  id: string,
  attachmentId: string,
): Promise<void> {
  return request(`/api/customer-requests/${id}/attachments/${attachmentId}`, {
    method: "DELETE",
  });
}
export function createDocument(
  input: Partial<
    Pick<
      FlowDocument,
      | "title"
      | "icon"
      | "color"
      | "content"
      | "contentState"
      | "contentData"
      | "projectIds"
      | "teamIds"
      | "issueId"
      | "subscriberIds"
      | "favorite"
    >
  > & { templateId?: string },
): Promise<FlowDocument> {
  return request("/api/documents", jsonRequest("POST", input));
}
export function listDocuments(
  filters: {
    q?: string;
    teamId?: string;
    archived?: "true" | "false" | "all";
  } = {},
): Promise<FlowDocument[]> {
  const query = new URLSearchParams();
  if (filters.q) query.set("q", filters.q);
  if (filters.teamId) query.set("teamId", filters.teamId);
  if (filters.archived) query.set("archived", filters.archived);
  return request(`/api/documents${query.size ? `?${query}` : ""}`);
}
export function listDocumentPermissions(
  documentId: string,
): Promise<DocumentPermission[]> {
  return request(`/api/documents/${documentId}/permissions`);
}
export function replaceDocumentPermissions(
  documentId: string,
  permissions: Array<
    Pick<DocumentPermission, "subjectType" | "subjectId" | "role">
  >,
): Promise<DocumentPermission[]> {
  return request(
    `/api/documents/${documentId}/permissions`,
    jsonRequest("PUT", { permissions }),
  );
}
export function updateDocumentPermission(
  documentId: string,
  permissionId: string,
  input: Partial<Pick<DocumentPermission, "subjectType" | "subjectId" | "role">>,
): Promise<DocumentPermission> {
  return request(
    `/api/documents/${documentId}/permissions/${permissionId}`,
    jsonRequest("PATCH", input),
  );
}
export function deleteDocumentPermission(
  documentId: string,
  permissionId: string,
): Promise<void> {
  return request(`/api/documents/${documentId}/permissions/${permissionId}`, {
    method: "DELETE",
  });
}
type DocumentTemplateMutation = Partial<
  Pick<
    DocumentTemplate,
    | "teamId"
    | "name"
    | "description"
    | "title"
    | "icon"
    | "content"
    | "contentState"
    | "contentData"
  >
>;
export function createDocumentTemplate(
  input: DocumentTemplateMutation & { teamId: string; name: string },
): Promise<DocumentTemplate> {
  return request("/api/document-templates", jsonRequest("POST", input));
}
export function updateDocumentTemplate(
  id: string,
  input: DocumentTemplateMutation,
): Promise<DocumentTemplate> {
  return request(`/api/document-templates/${id}`, jsonRequest("PATCH", input));
}
export function deleteDocumentTemplate(id: string): Promise<void> {
  return request(`/api/document-templates/${id}`, { method: "DELETE" });
}
export function updateDocument(
  id: string,
  input: Partial<
    Pick<
      FlowDocument,
      | "title"
      | "icon"
      | "color"
      | "content"
      | "contentState"
      | "contentData"
      | "projectIds"
      | "teamIds"
      | "issueId"
      | "subscriberIds"
      | "favorite"
    >
  > & { archived?: boolean },
): Promise<FlowDocument> {
  return request(`/api/documents/${id}`, jsonRequest("PATCH", input));
}
export function deleteDocument(id: string): Promise<void> {
  return request(`/api/documents/${id}`, { method: "DELETE" });
}
export function restoreDocumentRevision(
  id: string,
  revisionId: string,
): Promise<FlowDocument> {
  return request(`/api/documents/${id}/restore/${revisionId}`, {
    method: "POST",
  });
}
export function createDocumentComment(
  id: string,
  input: Pick<Comment, "body" | "bodyData" | "parentId">,
): Promise<Comment> {
  return request(`/api/documents/${id}/comments`, jsonRequest("POST", input));
}
export function updateDocumentComment(
  documentId: string,
  commentId: string,
  input: Pick<Comment, "body" | "bodyData"> & { expectedVersion?: number },
): Promise<Comment> {
  return request(
    `/api/documents/${documentId}/comments/${commentId}`,
    jsonRequest("PATCH", input),
  );
}
export function deleteDocumentComment(
  documentId: string,
  commentId: string,
): Promise<void> {
  return request(`/api/documents/${documentId}/comments/${commentId}`, {
    method: "DELETE",
  });
}
export function toggleDocumentCommentReaction(
  documentId: string,
  commentId: string,
  emoji: string,
): Promise<Comment> {
  return request(
    `/api/documents/${documentId}/comments/${commentId}/reactions`,
    jsonRequest("POST", { emoji }),
  );
}
type ReleaseMutation = Partial<
  Pick<
    Release,
    | "name"
    | "version"
    | "description"
    | "status"
    | "pipelineId"
    | "stage"
    | "commitSha"
    | "releaseNotes"
    | "targetDate"
    | "projectIds"
    | "issueIds"
    | "subscriberIds"
    | "resources"
  >
>;
export function createRelease(
  input: ReleaseMutation & { name: string },
): Promise<Release> {
  return request("/api/releases", jsonRequest("POST", input));
}
export function listReleases(
  filters: {
    pipelineId?: string;
    status?: Release["status"];
    archived?: boolean | "all";
  } = {},
): Promise<Release[]> {
  const query = new URLSearchParams();
  if (filters.pipelineId) query.set("pipelineId", filters.pipelineId);
  if (filters.status) query.set("status", filters.status);
  if (filters.archived !== undefined)
    query.set("archived", String(filters.archived));
  return request(`/api/releases${query.size ? `?${query}` : ""}`);
}
export function getRelease(id: string): Promise<Release> {
  return request(`/api/releases/${id}`);
}
export function updateRelease(
  id: string,
  input: ReleaseMutation & { archived?: boolean; stageFrozen?: boolean },
): Promise<Release> {
  return request(`/api/releases/${id}`, jsonRequest("PATCH", input));
}
export function deleteRelease(id: string): Promise<void> {
  return request(`/api/releases/${id}`, { method: "DELETE" });
}
export function reorderReleases(
  pipelineId: string,
  ids: string[],
  archived = false,
): Promise<Release[]> {
  return request(
    "/api/releases/reorder",
    jsonRequest("POST", { pipelineId, ids, archived }),
  );
}
type ReleasePipelineMutation = Partial<
  Pick<
    ReleasePipeline,
    | "name"
    | "teamIds"
    | "type"
    | "production"
    | "stages"
    | "stageStatuses"
    | "pathFilters"
    | "releaseNotesTemplate"
    | "autoGenerateReleaseNotes"
    | "moveOpenIssuesToNextRelease"
  >
>;
export function listReleasePipelines(): Promise<ReleasePipeline[]> {
  return request("/api/release-pipelines");
}
export function getReleasePipeline(id: string): Promise<ReleasePipeline> {
  return request(`/api/release-pipelines/${id}`);
}
export function createReleasePipeline(
  input: ReleasePipelineMutation & { name: string },
): Promise<ReleasePipeline> {
  return request("/api/release-pipelines", jsonRequest("POST", input));
}
export function updateReleasePipeline(
  id: string,
  input: ReleasePipelineMutation,
): Promise<ReleasePipeline> {
  return request(`/api/release-pipelines/${id}`, jsonRequest("PATCH", input));
}
export function deleteReleasePipeline(id: string): Promise<void> {
  return request(`/api/release-pipelines/${id}`, { method: "DELETE" });
}
export function reorderReleasePipelines(
  ids: string[],
): Promise<ReleasePipeline[]> {
  return request(
    "/api/release-pipelines/reorder",
    jsonRequest("POST", { ids }),
  );
}
export function rotateReleasePipelineAccessKey(
  id: string,
): Promise<ReleasePipelineAccessKey> {
  return request(`/api/release-pipelines/${id}/access-key`, { method: "POST" });
}
export function createCustomEmoji(input: {
  name: string;
  imageUrl: string;
}): Promise<CustomEmoji> {
  return request("/api/custom-emojis", jsonRequest("POST", input));
}
export function updateCustomEmoji(
  id: string,
  input: { name?: string; imageUrl?: string; archived?: boolean },
): Promise<CustomEmoji> {
  return request(`/api/custom-emojis/${id}`, jsonRequest("PATCH", input));
}
export function createAsk(input: {
  title: string;
  body?: string;
  source?: string;
  teamId?: string;
  templateId?: string;
  issueId?: string;
}): Promise<Ask> {
  return request("/api/asks", jsonRequest("POST", input));
}
export function updateAsk(
  id: string,
  input: Partial<
    Pick<Ask, "title" | "body" | "source" | "teamId" | "templateId" | "issueId">
  >,
): Promise<Ask> {
  return request(`/api/asks/${id}`, jsonRequest("PATCH", input));
}
export function decideAsk(
  id: string,
  decision: "approved" | "rejected",
  note?: string,
): Promise<Ask> {
  return request(
    `/api/asks/${id}/decision`,
    jsonRequest("POST", { decision, note }),
  );
}
export function deleteAsk(id: string): Promise<void> {
  return request(`/api/asks/${id}`, { method: "DELETE" });
}
type ProjectTemplateMutation = Partial<
  Pick<
    ProjectTemplate,
    | "name"
    | "projectName"
    | "templateDescription"
    | "description"
    | "summary"
    | "icon"
    | "color"
    | "statusId"
    | "priority"
    | "teamIds"
    | "labelIds"
    | "leadId"
    | "memberIds"
    | "dependencyIds"
    | "initiativeIds"
    | "issueIds"
    | "milestones"
    | "visibility"
    | "visibilityTeamId"
  >
>;
export function createProjectTemplate(
  input: { name: string } & ProjectTemplateMutation,
): Promise<ProjectTemplate> {
  return request("/api/project-templates", jsonRequest("POST", input));
}
export function updateProjectTemplate(
  id: string,
  input: ProjectTemplateMutation,
): Promise<ProjectTemplate> {
  return request(`/api/project-templates/${id}`, jsonRequest("PATCH", input));
}
export function deleteProjectTemplate(id: string): Promise<void> {
  return request(`/api/project-templates/${id}`, { method: "DELETE" });
}
export function createSLARule(
  input: { name: string } & Partial<
    Pick<
      SLARule,
      | "teamIds"
      | "filters"
      | "targetMinutes"
      | "pauseStatuses"
      | "businessHours"
      | "enabled"
    >
  >,
): Promise<SLARule> {
  return request("/api/sla-rules", jsonRequest("POST", input));
}
export function updateSLARule(
  id: string,
  input: Partial<
    Pick<
      SLARule,
      | "name"
      | "teamIds"
      | "filters"
      | "targetMinutes"
      | "pauseStatuses"
      | "businessHours"
      | "enabled"
    >
  >,
): Promise<SLARule> {
  return request(`/api/sla-rules/${id}`, jsonRequest("PATCH", input));
}
export function deleteSLARule(id: string): Promise<void> {
  return request(`/api/sla-rules/${id}`, { method: "DELETE" });
}
export function updateSLASettings(input: {
  enabled: boolean;
}): Promise<{ enabled: boolean }> {
  return request("/api/sla-settings", jsonRequest("PUT", input));
}
export function updateProjectUpdateSettings(
  input: Record<string, unknown>,
): Promise<Record<string, unknown>> {
  return request("/api/project-update-settings", jsonRequest("PUT", input));
}
export function createDraft(input: {
  type?: string;
  resourceId?: string;
  title?: string;
  body?: string;
  contentData?: Record<string, unknown>;
  metadata?: Record<string, unknown>;
}): Promise<Draft> {
  return request("/api/drafts", jsonRequest("POST", input));
}
export function updateDraft(
  id: string,
  input: Partial<Omit<Draft, "id" | "userId" | "createdAt" | "updatedAt">>,
): Promise<Draft> {
  return request(`/api/drafts/${id}`, jsonRequest("PATCH", input));
}
export function deleteDraft(id: string): Promise<void> {
  return request(`/api/drafts/${id}`, { method: "DELETE" });
}
export function deleteAllDrafts(): Promise<void> {
  return request("/api/drafts", { method: "DELETE" });
}
export function addFavorite(type: string, id: string): Promise<Favorite> {
  return request(`/api/favorites/${type}/${id}`, { method: "PUT" });
}
export function removeFavorite(type: string, id: string): Promise<void> {
  return request(`/api/favorites/${type}/${id}`, { method: "DELETE" });
}
export function updateFavorite(
  type: string,
  id: string,
  input: { folderId?: string; position?: number },
): Promise<Favorite> {
  return request(`/api/favorites/${type}/${id}`, jsonRequest("PATCH", input));
}
export function createFavoriteFolder(
  name: string,
): Promise<import("@/types/flow").FavoriteFolder> {
  return request("/api/favorite-folders", jsonRequest("POST", { name }));
}
export function updateFavoriteFolder(
  id: string,
  input: { name?: string; position?: number },
): Promise<import("@/types/flow").FavoriteFolder> {
  return request(
    `/api/favorite-folders/${encodeURIComponent(id)}`,
    jsonRequest("PATCH", input),
  );
}
export function deleteFavoriteFolder(id: string): Promise<void> {
  return request(`/api/favorite-folders/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}
export function addSubscription(
  type: string,
  id: string,
  events?: string[],
): Promise<Subscription> {
  return request(`/api/subscriptions/${type}/${id}`, {
    method: "PUT",
    body: events ? JSON.stringify({ events }) : undefined,
  });
}
export function removeSubscription(type: string, id: string): Promise<void> {
  return request(`/api/subscriptions/${type}/${id}`, { method: "DELETE" });
}
export function restoreTrashEntry(id: string): Promise<unknown> {
  return request(`/api/trash/${id}/restore`, { method: "POST" });
}
export function purgeTrashEntry(id: string): Promise<void> {
  return request(`/api/trash/${id}`, { method: "DELETE" });
}
export function previewImport(file: File): Promise<ImportJob> {
  const body = new FormData();
  body.append("file", file);
  return request("/api/imports/preview", { method: "POST", body });
}
export function commitImport(
  id: string,
  mapping: Record<string, string>,
  teamId: string,
): Promise<ImportJob> {
  return request(
    `/api/imports/${id}/commit`,
    jsonRequest("POST", { mapping, teamId }),
  );
}
export function listImports(): Promise<ImportJob[]> {
  return request("/api/imports");
}
export function getImport(id: string): Promise<ImportJob> {
  return request(`/api/imports/${id}`);
}
export function cancelImport(id: string): Promise<ImportJob> {
  return request(`/api/imports/${id}/cancel`, { method: "POST" });
}
export function retryImport(id: string): Promise<ImportJob> {
  return request(`/api/imports/${id}/retry`, { method: "POST" });
}
export function resumeImport(id: string): Promise<ImportJob> {
  return request(`/api/imports/${id}/resume`, { method: "POST" });
}
export function createExport(
  format: "json" | "csv",
  includePrivate: boolean,
): Promise<ExportJob> {
  return request(
    "/api/exports",
    jsonRequest("POST", { format, includePrivate }),
  );
}
export function listExports(): Promise<ExportJob[]> {
  return request("/api/exports");
}
export function getExport(id: string): Promise<ExportJob> {
  return request(`/api/exports/${id}`);
}
export function retryExport(id: string): Promise<ExportJob> {
  return request(`/api/exports/${id}/retry`, { method: "POST" });
}
export function getAnalyticsOverview(
  since?: string,
): Promise<Record<string, unknown>> {
  return request(
    `/api/analytics/overview${since ? `?since=${encodeURIComponent(since)}` : ""}`,
  );
}
export function exportDownloadUrl(id: string) {
  return `/api/exports/${id}/download`;
}
export function previewMigration(file: File): Promise<MigrationJob> {
  const body = new FormData();
  body.append("file", file);
  return request("/api/migrations/preview", { method: "POST", body });
}
export function updateMigrationMappings(
  id: string,
  input: {
    mappings: Partial<MigrationEntityMapping>[];
    target?: "flow" | "linear";
    targetTeamId?: string;
  },
): Promise<MigrationJob> {
  return request(`/api/migrations/${id}/mappings`, jsonRequest("PATCH", input));
}
export function inviteMigrationUsers(id: string): Promise<MigrationJob> {
  return request(`/api/migrations/${id}/invite-users`, { method: "POST" });
}
export function scanLinearMigrationTarget(
  id: string,
  input: { apiToken: string; targetTeamId: string },
): Promise<MigrationJob> {
  return request(
    `/api/migrations/${id}/linear/scan`,
    jsonRequest("POST", input),
  );
}
export function executeMigration(
  id: string,
  input: {
    target: "flow" | "linear";
    targetTeamId?: string;
    apiToken?: string;
  },
): Promise<MigrationJob> {
  return request(`/api/migrations/${id}/execute`, jsonRequest("POST", input));
}
export function rollbackMigration(
  id: string,
  apiToken?: string,
): Promise<MigrationJob> {
  return request(
    `/api/migrations/${id}/rollback`,
    jsonRequest("POST", { apiToken }),
  );
}
export function migrationBundleDownloadUrl(workspaceKey: string) {
  return `/api/migrations/bundle?workspace=${encodeURIComponent(workspaceKey)}`;
}
export function migrationManifestDownloadUrl(id: string, workspaceKey: string) {
  return `/api/migrations/${id}/manifest?workspace=${encodeURIComponent(workspaceKey)}`;
}
type PageResult<T> = { nodes: T[]; nextCursor: string; total: number };
export function listProjectRelations(
  id: string,
): Promise<PageResult<import("@/types/flow").ProjectRelation>> {
  return request(`/api/projects/${id}/relations`);
}
export function createProjectRelation(
  id: string,
  input: Partial<import("@/types/flow").ProjectRelation>,
): Promise<import("@/types/flow").ProjectRelation> {
  return request(`/api/projects/${id}/relations`, jsonRequest("POST", input));
}
export function deleteProjectRelation(
  id: string,
  relationId: string,
): Promise<void> {
  return request(`/api/projects/${id}/relations/${relationId}`, {
    method: "DELETE",
  });
}
export function listInitiativeRelations(
  id: string,
): Promise<PageResult<import("@/types/flow").InitiativeRelation>> {
  return request(`/api/initiatives/${id}/relations`);
}
export function createInitiativeRelation(
  id: string,
  input: Partial<import("@/types/flow").InitiativeRelation>,
): Promise<import("@/types/flow").InitiativeRelation> {
  return request(
    `/api/initiatives/${id}/relations`,
    jsonRequest("POST", input),
  );
}
export function deleteInitiativeRelation(
  id: string,
  relationId: string,
): Promise<void> {
  return request(`/api/initiatives/${id}/relations/${relationId}`, {
    method: "DELETE",
  });
}
export function listDocumentDrafts(
  id: string,
): Promise<PageResult<import("@/types/flow").DocumentContentDraft>> {
  return request(`/api/documents/${id}/drafts`);
}
export function saveDocumentDraft(
  id: string,
  input: Partial<import("@/types/flow").DocumentContentDraft>,
): Promise<import("@/types/flow").DocumentContentDraft> {
  return request(`/api/documents/${id}/drafts`, jsonRequest("POST", input));
}
export function publishDocumentDraft(
  id: string,
  draftId: string,
): Promise<FlowDocument> {
  return request(`/api/documents/${id}/drafts/${draftId}/publish`, {
    method: "POST",
  });
}
export function deleteDocumentDraft(
  id: string,
  draftId: string,
): Promise<void> {
  return request(`/api/documents/${id}/drafts/${draftId}`, {
    method: "DELETE",
  });
}
export function fetchCustomerTaxonomy(): Promise<{
  statuses: import("@/types/flow").CustomerStatus[];
  tiers: import("@/types/flow").CustomerTier[];
}> {
  return request("/api/customer-taxonomy");
}
export function createCustomerStatus(input: {
  name: string;
  color: string;
}): Promise<import("@/types/flow").CustomerStatus> {
  return request("/api/customer-statuses", jsonRequest("POST", input));
}
export function updateCustomerStatus(
  id: string,
  input: Record<string, unknown>,
): Promise<import("@/types/flow").CustomerStatus> {
  return request(`/api/customer-statuses/${id}`, jsonRequest("PATCH", input));
}
export function deleteCustomerStatus(id: string): Promise<void> {
  return request(`/api/customer-statuses/${id}`, { method: "DELETE" });
}
export function createCustomerTier(input: {
  name: string;
  color: string;
}): Promise<import("@/types/flow").CustomerTier> {
  return request("/api/customer-tiers", jsonRequest("POST", input));
}
export function updateCustomerTier(
  id: string,
  input: Record<string, unknown>,
): Promise<import("@/types/flow").CustomerTier> {
  return request(`/api/customer-tiers/${id}`, jsonRequest("PATCH", input));
}
export function deleteCustomerTier(id: string): Promise<void> {
  return request(`/api/customer-tiers/${id}`, { method: "DELETE" });
}
export function archiveCustomerNeed(
  id: string,
  archived = true,
): Promise<CustomerRequest> {
  return request(`/api/customer-requests/${id}/archive`, {
    method: archived ? "POST" : "DELETE",
  });
}
export function listReleaseNotes(
  id: string,
): Promise<PageResult<import("@/types/flow").ReleaseNote>> {
  return request(`/api/releases/${id}/notes`);
}
export function createReleaseNote(
  id: string,
  input: Partial<import("@/types/flow").ReleaseNote>,
): Promise<import("@/types/flow").ReleaseNote> {
  return request(`/api/releases/${id}/notes`, jsonRequest("POST", input));
}
export function updateReleaseNote(
  id: string,
  noteId: string,
  input: Record<string, unknown>,
): Promise<import("@/types/flow").ReleaseNote> {
  return request(
    `/api/releases/${id}/notes/${noteId}`,
    jsonRequest("PATCH", input),
  );
}
export function fetchTeamResources(id: string): Promise<{
  sections: import("@/types/flow").TeamResourceSection[];
  resources: import("@/types/flow").TeamPinnedResource[];
}> {
  return request(`/api/teams/${id}/resources`);
}
export function createTeamResourceSection(
  id: string,
  name: string,
): Promise<import("@/types/flow").TeamResourceSection> {
  return request(
    `/api/teams/${id}/resource-sections`,
    jsonRequest("POST", { name }),
  );
}
export function updateTeamResourceSection(
  id: string,
  sectionId: string,
  input: { name?: string; position?: number },
): Promise<import("@/types/flow").TeamResourceSection> {
  return request(
    `/api/teams/${id}/resource-sections/${sectionId}`,
    jsonRequest("PATCH", input),
  );
}
export function deleteTeamResourceSection(
  id: string,
  sectionId: string,
): Promise<void> {
  return request(`/api/teams/${id}/resource-sections/${sectionId}`, {
    method: "DELETE",
  });
}
export function pinTeamResource(
  id: string,
  input: Partial<import("@/types/flow").TeamPinnedResource>,
): Promise<import("@/types/flow").TeamPinnedResource> {
  return request(`/api/teams/${id}/resources`, jsonRequest("POST", input));
}
export function updateTeamResource(
  id: string,
  resourceId: string,
  input: { title?: string; sectionId?: string; position?: number },
): Promise<import("@/types/flow").TeamPinnedResource> {
  return request(
    `/api/teams/${id}/resources/${resourceId}`,
    jsonRequest("PATCH", input),
  );
}
export function deleteTeamResource(
  id: string,
  resourceId: string,
): Promise<void> {
  return request(`/api/teams/${id}/resources/${resourceId}`, {
    method: "DELETE",
  });
}
export function listAgentActivities(
  sessionId?: string,
): Promise<PageResult<import("@/types/flow").AgentActivity>> {
  return request(
    `/api/agent/activities${sessionId ? `?sessionId=${encodeURIComponent(sessionId)}` : ""}`,
  );
}
export function createAgentActivity(
  input: Partial<import("@/types/flow").AgentActivity>,
): Promise<import("@/types/flow").AgentActivity> {
  return request("/api/agent/activities", jsonRequest("POST", input));
}
export function listAIConversations(): Promise<
  PageResult<import("@/types/flow").AIConversation>
> {
  return request("/api/ai/conversations");
}
export function createAIConversation(
  input: Partial<import("@/types/flow").AIConversation>,
): Promise<import("@/types/flow").AIConversation> {
  return request("/api/ai/conversations", jsonRequest("POST", input));
}
export function createAIPromptProgress(
  input: Partial<import("@/types/flow").AIPromptProgress>,
): Promise<import("@/types/flow").AIPromptProgress> {
  return request("/api/ai/prompt-progress", jsonRequest("POST", input));
}
export function listUsageAlerts(): Promise<
  PageResult<import("@/types/flow").UsageAlert>
> {
  return request("/api/usage-alerts");
}
export function getPaidSubscription(): Promise<
  import("@/types/flow").PaidSubscription | null
> {
  return request("/api/paid-subscription");
}

export function searchWorkspace(
  query: string,
  types: SearchResourceType[] = [],
  limit = 40,
): Promise<SearchResponse> {
  const params = new URLSearchParams({ q: query, limit: String(limit) });
  if (types.length) params.set("types", types.join(","));
  return request(`/api/search?${params}`);
}
export function fetchAgentStatus(): Promise<
  import("@/types/flow").AgentStatus
> {
  return request("/api/agent/status");
}
export function sendAgentMessage(input: {
  message: string;
  issueIds: string[];
  history: import("@/types/flow").AgentChatMessage[];
}): Promise<import("@/types/flow").AgentChatResponse> {
  return request("/api/agent/chat", jsonRequest("POST", input));
}
export function listAgentSessions(): Promise<
  import("@/types/flow").AgentSession[]
> {
  return request("/api/agent/sessions");
}
export function getAgentSession(
  id: string,
): Promise<import("@/types/flow").AgentSession> {
  return request(`/api/agent/sessions/${encodeURIComponent(id)}`);
}
export function createAgentSession(input: {
  message: string;
  issueIds?: string[];
  skillIds?: string[];
  location?: "page" | "toolbar";
}): Promise<import("@/types/flow").AgentSession> {
  return request("/api/agent/sessions", jsonRequest("POST", input));
}
export function createAgentSessionMessage(
  id: string,
  message: string,
): Promise<import("@/types/flow").AgentSession> {
  return request(
    `/api/agent/sessions/${encodeURIComponent(id)}/messages`,
    jsonRequest("POST", { message }),
  );
}
export function updateAgentSessionMessage(
  id: string,
  messageId: string,
  message: string,
): Promise<import("@/types/flow").AgentSession> {
  return request(
    `/api/agent/sessions/${encodeURIComponent(id)}/messages/${encodeURIComponent(messageId)}`,
    jsonRequest("PATCH", { message }),
  );
}
export function updateAgentSession(
  id: string,
  input: { title?: string; favorite?: boolean; location?: "page" | "toolbar" },
): Promise<import("@/types/flow").AgentSession> {
  return request(
    `/api/agent/sessions/${encodeURIComponent(id)}`,
    jsonRequest("PATCH", input),
  );
}
export function deleteAgentSession(id: string): Promise<void> {
  return request(`/api/agent/sessions/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}
export function listAgentSkills(): Promise<
  import("@/types/flow").PersonalAgentSkill[]
> {
  return request("/api/agent/skills");
}
export function createAgentSkill(input: {
  name: string;
  instructions: string;
}): Promise<import("@/types/flow").PersonalAgentSkill> {
  return request("/api/agent/skills", jsonRequest("POST", input));
}
export function updateAgentSkill(
  id: string,
  input: { name: string; instructions: string },
): Promise<import("@/types/flow").PersonalAgentSkill> {
  return request(
    `/api/agent/skills/${encodeURIComponent(id)}`,
    jsonRequest("PATCH", input),
  );
}
export function deleteAgentSkill(id: string): Promise<void> {
  return request(`/api/agent/skills/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}
export function clearSearchHistory(): Promise<void> {
  return request("/api/search/history", { method: "DELETE" });
}
export function recordRecentResource(
  type: SearchResourceType,
  id: string,
): Promise<void> {
  return request("/api/recent", jsonRequest("POST", { type, id }));
}
export function updatePresence(
  clientId: string,
  issueId: string | undefined,
  route: string,
  active = true,
): Promise<Presence[]> {
  return request(
    "/api/realtime/presence",
    jsonRequest("POST", { clientId, issueId, route, active }),
  );
}
export function updateDocumentPresence(
  clientId: string,
  documentId: string,
  route: string,
  active = true,
): Promise<Presence[]> {
  return request(
    "/api/realtime/presence",
    jsonRequest("POST", { clientId, documentId, route, active }),
  );
}

export async function createIssue(input: {
  title: string;
  description: string;
  descriptionState?: string;
  descriptionData?: Record<string, unknown>;
  contentState?: string;
  teamId: string;
  parentId?: string;
  stateId?: string;
  priority?: number;
  estimate?: number;
  assigneeId?: string;
  projectId?: string;
  projectMilestoneId?: string;
  cycleId?: string;
  dueDate?: string;
  labelIds?: string[];
  templateId?: string;
  recurrence?: "" | "daily" | "weekly" | "monthly";
  nextOccurrenceAt?: string;
}): Promise<Issue> {
  return request("/api/issues", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}

export function updateIssue(
  issueId: string,
  input: IssueUpdateInput,
): Promise<Issue> {
  return request(`/api/issues/${issueId}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}
export function shareIssue(issueId: string): Promise<{ issue: Issue; token: string; url: string }> {
  return request(`/api/issues/${issueId}/share`, jsonRequest("POST", {}));
}
export function unshareIssue(issueId: string): Promise<void> {
  return request(`/api/issues/${issueId}/share`, { method: "DELETE" });
}
export function setIssueReleases(
  issueId: string,
  releaseIds: string[],
): Promise<Release[]> {
  return request(
    `/api/issues/${issueId}/releases`,
    jsonRequest("PUT", { releaseIds }),
  );
}
export function updateCycle(
  id: string,
  input: CycleMutationInput,
): Promise<Cycle> {
  return request(`/api/cycles/${id}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}
export type CycleCapacity = {
  cycleId: string;
  capacity: number;
  weekdays: string[];
  members: Array<{ user: User; capacity: Record<string, number> }>;
};
export function getCycleCapacity(id: string): Promise<CycleCapacity> {
  return request(`/api/cycles/${id}/capacity`);
}
export function updateCycleCapacity(
  id: string,
  input: { capacity?: number; capacityByMember?: Record<string, Record<string, number>> },
): Promise<Cycle> {
  return request(`/api/cycles/${id}/capacity`, jsonRequest("PUT", input));
}
export function startCycle(id: string): Promise<Cycle> {
  return request(`/api/cycles/${id}/start`, { method: "POST" });
}
export function completeCycle(id: string): Promise<Cycle> {
  return request(`/api/cycles/${id}/complete`, { method: "POST" });
}
export function createCycleResource(
  id: string,
  input: {
    type: "link" | "document";
    title?: string;
    url?: string;
    documentId?: string;
  },
): Promise<import("@/types/flow").CycleResource> {
  return request(`/api/cycles/${id}/resources`, jsonRequest("POST", input));
}
export function deleteCycleResource(
  id: string,
  resourceId: string,
): Promise<void> {
  return request(`/api/cycles/${id}/resources/${resourceId}`, {
    method: "DELETE",
  });
}
export function getCycleCalendarFeed(id: string): Promise<{ url: string }> {
  return request(`/api/cycles/${id}/calendar-token`, { method: "POST" });
}
export function updateCycleSettings(
  teamId: string,
  input: CycleSettingsMutationInput,
): Promise<CycleSettings> {
  return request(`/api/teams/${teamId}/cycle-settings`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}

export function deleteIssue(issueId: string): Promise<void> {
  return request(`/api/issues/${issueId}`, { method: "DELETE" });
}
export function toggleIssueReaction(
  issueId: string,
  emoji: string,
): Promise<Issue> {
  return request(`/api/issues/${issueId}/reactions`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ emoji }),
  });
}
export function createIssueLink(
  issueId: string,
  input: { url: string; title?: string },
): Promise<Attachment> {
  return request(`/api/issues/${issueId}/links`, jsonRequest("POST", input));
}
export function createIssueReminder(
  issueId: string,
  remindAt: string,
): Promise<Notification> {
  return request(
    `/api/issues/${issueId}/reminders`,
    jsonRequest("POST", { remindAt }),
  );
}
export function createIssueLoopRun(
  issueId: string,
  prompt?: string,
): Promise<Ask> {
  return request(
    `/api/issues/${issueId}/loop-runs`,
    jsonRequest("POST", { prompt }),
  );
}
export type LoopMutation = Partial<
  Pick<
    Loop,
    | "name"
    | "icon"
    | "color"
    | "level"
    | "triggerType"
    | "triggerConfig"
    | "instructions"
    | "connectorIds"
    | "teamAccess"
    | "allowChangesOutsideTrigger"
    | "allowExternalSync"
    | "enabled"
  >
>;
export function listLoops(): Promise<Loop[]> {
  return request("/api/loops");
}
export function createLoop(
  input: LoopMutation & { name: string },
): Promise<Loop> {
  return request("/api/loops", jsonRequest("POST", input));
}
export function updateLoop(id: string, input: LoopMutation): Promise<Loop> {
  return request(
    `/api/loops/${encodeURIComponent(id)}`,
    jsonRequest("PATCH", input),
  );
}
export function deleteLoop(id: string): Promise<void> {
  return request(`/api/loops/${encodeURIComponent(id)}`, { method: "DELETE" });
}

export function batchUpdateIssues(
  issueIds: string[],
  update: IssueUpdateInput,
): Promise<Issue[]> {
  return request("/api/issues/batch", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ issueIds, update }),
  });
}

export async function createComment(
  issueId: string,
  body: string,
  bodyData?: Record<string, unknown>,
  parentId?: string,
): Promise<Comment> {
  return request(`/api/issues/${issueId}/comments`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ body, bodyData, parentId }),
  });
}

export function updateComment(
  issueId: string,
  commentId: string,
  body: string,
  bodyData?: Record<string, unknown>,
  expectedVersion?: number,
): Promise<Comment> {
  return request(`/api/issues/${issueId}/comments/${commentId}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ body, bodyData, expectedVersion }),
  });
}
export function deleteComment(
  issueId: string,
  commentId: string,
): Promise<void> {
  return request(`/api/issues/${issueId}/comments/${commentId}`, {
    method: "DELETE",
  });
}
export function toggleCommentReaction(
  issueId: string,
  commentId: string,
  emoji: string,
): Promise<Comment> {
  return request(`/api/issues/${issueId}/comments/${commentId}/reactions`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ emoji }),
  });
}

export function createRelation(
  issueId: string,
  type: IssueRelationType,
  relatedIssueId: string,
): Promise<IssueRelation> {
  return request(`/api/issues/${issueId}/relations`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ type, relatedIssueId }),
  });
}

export function deleteRelation(
  issueId: string,
  relationId: string,
): Promise<void> {
  return request(`/api/issues/${issueId}/relations/${relationId}`, {
    method: "DELETE",
  });
}

export function uploadAttachment(
  issueId: string,
  file: File,
): Promise<Attachment> {
  const body = new FormData();
  body.append("file", file);
  return request(`/api/issues/${issueId}/attachments`, {
    method: "POST",
    body,
  });
}

export function deleteAttachment(
  issueId: string,
  attachmentId: string,
): Promise<void> {
  return request(`/api/issues/${issueId}/attachments/${attachmentId}`, {
    method: "DELETE",
  });
}
export function fetchInboxNotifications(query = ""): Promise<NotificationList> {
  return request(`/api/notifications${query}`);
}
export function updateInboxNotification(
  notificationId: string,
  input: NotificationMutationInput,
): Promise<Notification> {
  return request(`/api/notifications/${notificationId}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}
export function batchNotifications(
  action:
    | "delete"
    | "deleteAll"
    | "deleteRead"
    | "deleteReadCompleted"
    | "markRead"
    | "markAllRead"
    | "markUnread"
    | "archive"
    | "archiveAll"
    | "unarchive"
    | "snooze"
    | "snoozeAll"
    | "unsnooze",
  ids?: string[],
  snoozedUntil?: string,
): Promise<{ updated: number }> {
  return request(
    "/api/notifications/batch",
    jsonRequest("POST", { action, ids, snoozedUntil }),
  );
}
export function listPushSubscriptions(): Promise<PushSubscription[]> {
  return request("/api/push-subscriptions");
}
export function createPushSubscription(input: {
  endpoint: string;
  p256dh: string;
  auth: string;
}): Promise<PushSubscription> {
  return request("/api/push-subscriptions", jsonRequest("POST", input));
}
export function deletePushSubscription(id: string): Promise<void> {
  return request(`/api/push-subscriptions/${id}`, { method: "DELETE" });
}
export function listTriageResponsibilities(
  teamId: string,
): Promise<TriageResponsibility[]> {
  return request(`/api/teams/${teamId}/triage-responsibilities`);
}
export function createTriageResponsibility(
  teamId: string,
  input: {
    name: string;
    mode: "individual" | "roundRobin";
    userIds: string[];
    enabled?: boolean;
  },
): Promise<TriageResponsibility> {
  return request(
    `/api/teams/${teamId}/triage-responsibilities`,
    jsonRequest("POST", input),
  );
}
export function updateTriageResponsibility(
  teamId: string,
  id: string,
  input: {
    name: string;
    mode: "individual" | "roundRobin";
    userIds: string[];
    enabled?: boolean;
  },
): Promise<TriageResponsibility> {
  return request(
    `/api/teams/${teamId}/triage-responsibilities/${id}`,
    jsonRequest("PATCH", input),
  );
}
export function deleteTriageResponsibility(
  teamId: string,
  id: string,
): Promise<void> {
  return request(`/api/teams/${teamId}/triage-responsibilities/${id}`, {
    method: "DELETE",
  });
}
export function listTriageRules(teamId: string): Promise<TriageRoutingRule[]> {
  return request(`/api/teams/${teamId}/triage-rules`);
}
export function createTriageRule(
  teamId: string,
  input: {
    name: string;
    position?: number;
    enabled?: boolean;
    conditions: Record<string, string>;
    responsibilityId: string;
    priority?: number;
    labelIds: string[];
  },
): Promise<TriageRoutingRule> {
  return request(
    `/api/teams/${teamId}/triage-rules`,
    jsonRequest("POST", input),
  );
}
export function updateTriageRule(
  teamId: string,
  id: string,
  input: {
    name: string;
    position?: number;
    enabled?: boolean;
    conditions: Record<string, string>;
    responsibilityId: string;
    priority?: number;
    labelIds: string[];
  },
): Promise<TriageRoutingRule> {
  return request(
    `/api/teams/${teamId}/triage-rules/${id}`,
    jsonRequest("PATCH", input),
  );
}
export function deleteTriageRule(teamId: string, id: string): Promise<void> {
  return request(`/api/teams/${teamId}/triage-rules/${id}`, {
    method: "DELETE",
  });
}
export function listEmailIntakeAddresses(
  teamId: string,
): Promise<EmailIntakeAddress[]> {
  return request(`/api/teams/${teamId}/email-intake-addresses`);
}
export function createEmailIntakeAddress(
  teamId: string,
  input: { localPart: string; domain: string },
): Promise<{
  address: EmailIntakeAddress;
  inboundToken: string;
  dnsRecord: { type: string; name: string; value: string };
}> {
  return request(
    `/api/teams/${teamId}/email-intake-addresses`,
    jsonRequest("POST", input),
  );
}
export function verifyEmailIntakeAddress(
  teamId: string,
  id: string,
  txtValue?: string,
): Promise<EmailIntakeAddress> {
  return request(
    `/api/teams/${teamId}/email-intake-addresses/${id}/verify`,
    jsonRequest("POST", { txtValue: txtValue ?? "" }),
  );
}
export function rotateEmailIntakeAddress(
  teamId: string,
  id: string,
): Promise<{ address: EmailIntakeAddress; inboundToken: string }> {
  return request(`/api/teams/${teamId}/email-intake-addresses/${id}/rotate`, {
    method: "POST",
  });
}
export function deleteEmailIntakeAddress(
  teamId: string,
  id: string,
): Promise<void> {
  return request(`/api/teams/${teamId}/email-intake-addresses/${id}`, {
    method: "DELETE",
  });
}
export type WorkflowDefinitionInput = {
  name: string;
  description?: string;
  teamId?: string;
  trigger: "manual" | "schedule" | "issueCreated";
  schedule?: string;
  conditions?: Record<string, string>;
  actions: WorkflowAction[];
  enabled?: boolean;
  maxAttempts?: number;
};
export function listWorkflowDefinitions(): Promise<WorkflowDefinition[]> {
  return request("/api/workflows");
}
export function createWorkflowDefinition(
  input: WorkflowDefinitionInput,
): Promise<WorkflowDefinition> {
  return request("/api/workflows", jsonRequest("POST", input));
}
export function updateWorkflowDefinition(
  id: string,
  input: WorkflowDefinitionInput,
): Promise<WorkflowDefinition> {
  return request(`/api/workflows/${id}`, jsonRequest("PATCH", input));
}
export function deleteWorkflowDefinition(id: string): Promise<void> {
  return request(`/api/workflows/${id}`, { method: "DELETE" });
}
export function runWorkflowDefinition(id: string): Promise<WorkflowRun> {
  return request(`/api/workflows/${id}/run`, { method: "POST" });
}
export function listWorkflowRuns(workflowId?: string): Promise<WorkflowRun[]> {
  return request(
    `/api/workflow-runs${workflowId ? `?workflowId=${encodeURIComponent(workflowId)}` : ""}`,
  );
}
export function retryWorkflowRun(id: string): Promise<WorkflowRun> {
  return request(`/api/workflow-runs/${id}/retry`, { method: "POST" });
}
export function fetchNotificationPreferences(): Promise<NotificationPreferences> {
  return request("/api/notification-preferences");
}
export function updateNotificationPreferences(
  input: NotificationPreferences,
): Promise<NotificationPreferences> {
  return request("/api/notification-preferences", jsonRequest("PATCH", input));
}
export function fetchNotificationDeliveries(): Promise<NotificationDelivery[]> {
  return request("/api/notification-deliveries");
}
export function acknowledgeDesktopNotifications(
  notificationIds: string[],
): Promise<void> {
  return request(
    "/api/desktop-notifications/ack",
    jsonRequest("POST", { notificationIds }),
  );
}
export function retryNotificationDelivery(
  id: string,
): Promise<NotificationDelivery> {
  return request(`/api/notification-deliveries/${id}/retry`, {
    method: "POST",
  });
}
export function fetchWorkflowStates(teamId: string): Promise<WorkflowState[]> {
  return request(`/api/teams/${teamId}/states`);
}
export function createWorkflowState(
  teamId: string,
  input: Partial<WorkflowState> & Pick<WorkflowState, "name" | "type">,
): Promise<WorkflowState> {
  return request(`/api/teams/${teamId}/states`, jsonRequest("POST", input));
}
export function updateWorkflowState(
  teamId: string,
  stateId: string,
  input: Partial<WorkflowState> & { replacementStateId?: string },
): Promise<WorkflowState> {
  return request(
    `/api/teams/${teamId}/states/${stateId}`,
    jsonRequest("PATCH", input),
  );
}
export function deleteWorkflowState(
  teamId: string,
  stateId: string,
  replacementStateId?: string,
): Promise<void> {
  return request(`/api/teams/${teamId}/states/${stateId}`, {
    method: "DELETE",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ replacementStateId }),
  });
}
export function reorderWorkflowStates(
  teamId: string,
  stateIds: string[],
): Promise<WorkflowState[]> {
  return request(
    `/api/teams/${teamId}/states/reorder`,
    jsonRequest("POST", { stateIds }),
  );
}
export function fetchTeamSettings(teamId: string): Promise<TeamSettings> {
  return request(`/api/teams/${teamId}/settings`);
}
export function updateStructuredTeamSettings(
  teamId: string,
  input: TeamSettingsMutationInput,
): Promise<TeamSettings> {
  return request(`/api/teams/${teamId}/settings`, jsonRequest("PATCH", input));
}
export function fetchIssueTemplates(teamId: string): Promise<IssueTemplate[]> {
  return request(`/api/teams/${teamId}/templates`);
}
export function createIssueTemplate(
  teamId: string,
  input: IssueTemplateMutationInput & { name: string },
): Promise<IssueTemplate> {
  return request(`/api/teams/${teamId}/templates`, jsonRequest("POST", input));
}
export function updateIssueTemplate(
  teamId: string,
  templateId: string,
  input: IssueTemplateMutationInput,
): Promise<IssueTemplate> {
  return request(
    `/api/teams/${teamId}/templates/${templateId}`,
    jsonRequest("PATCH", input),
  );
}
export function deleteIssueTemplate(
  teamId: string,
  templateId: string,
): Promise<void> {
  return request(`/api/teams/${teamId}/templates/${templateId}`, {
    method: "DELETE",
  });
}
export function fetchTeamLabels(teamId: string): Promise<IssueLabel[]> {
  return request(`/api/teams/${teamId}/labels`);
}
export function createTeamLabel(
  teamId: string,
  input: Pick<IssueLabel, "name" | "color"> & { description?: string },
): Promise<IssueLabel> {
  return request(`/api/teams/${teamId}/labels`, jsonRequest("POST", input));
}
export function updateTeamLabel(
  teamId: string,
  labelId: string,
  input: Partial<
    Pick<IssueLabel, "name" | "description" | "color" | "archivedAt">
  >,
): Promise<IssueLabel> {
  return request(
    `/api/teams/${teamId}/labels/${labelId}`,
    jsonRequest("PATCH", input),
  );
}
export function deleteTeamLabel(
  teamId: string,
  labelId: string,
): Promise<void> {
  return request(`/api/teams/${teamId}/labels/${labelId}`, {
    method: "DELETE",
  });
}
export function moveWorkspaceLabelToTeams(
  labelId: string,
): Promise<IssueLabel[]> {
  return request(
    `/api/labels/${labelId}/move-to-teams`,
    jsonRequest("POST", {}),
  );
}
export function createProject(input: ProjectCreateInput): Promise<Project> {
  return request("/api/projects", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}
export function updateProject(
  projectId: string,
  input: ProjectMutationInput,
): Promise<Project> {
  return request(`/api/projects/${projectId}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}
export function deleteProject(projectId: string): Promise<void> {
  return request(`/api/projects/${projectId}`, { method: "DELETE" });
}
export function createProjectReminder(
  projectId: string,
  remindAt: string,
): Promise<Notification> {
  return request(
    `/api/projects/${projectId}/reminders`,
    jsonRequest("POST", { remindAt }),
  );
}
export function createProjectResource(
  projectId: string,
  input: { type?: "link" | "document"; title?: string; url: string },
): Promise<ProjectResource> {
  return request(`/api/projects/${projectId}/resources`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}
export function updateProjectResource(
  projectId: string,
  resourceId: string,
  input: {
    type?: "link" | "document";
    title?: string;
    url?: string;
    pinnedTeamIds?: string[];
  },
): Promise<ProjectResource> {
  return request(`/api/projects/${projectId}/resources/${resourceId}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}
export function deleteProjectResource(
  projectId: string,
  resourceId: string,
): Promise<void> {
  return request(`/api/projects/${projectId}/resources/${resourceId}`, {
    method: "DELETE",
  });
}
export function createProjectMilestone(
  projectId: string,
  input: { name: string; description?: string; targetDate?: string },
): Promise<ProjectMilestone> {
  return request(`/api/projects/${projectId}/milestones`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}
export function reorderProjectMilestones(
  projectId: string,
  ids: string[],
): Promise<ProjectMilestone[]> {
  return request(`/api/projects/${projectId}/milestones/reorder`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ ids }),
  });
}
export function updateProjectMilestone(
  projectId: string,
  milestoneId: string,
  input: { name?: string; description?: string; targetDate?: string },
): Promise<ProjectMilestone> {
  return request(`/api/projects/${projectId}/milestones/${milestoneId}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}
export function deleteProjectMilestone(
  projectId: string,
  milestoneId: string,
): Promise<void> {
  return request(`/api/projects/${projectId}/milestones/${milestoneId}`, {
    method: "DELETE",
  });
}
export function createProjectComment(
  projectId: string,
  body: string,
): Promise<Comment> {
  return request(`/api/projects/${projectId}/comments`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ body }),
  });
}
export function createProjectUpdate(
  projectId: string,
  input: {
    body: string;
    bodyData?: Record<string, unknown>;
    health?: Project["health"];
  },
): Promise<ProjectUpdate> {
  return request(`/api/projects/${projectId}/updates`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}
export function updateProjectUpdate(
  projectId: string,
  updateId: string,
  input: {
    body?: string;
    bodyData?: Record<string, unknown>;
    health?: Project["health"];
  },
): Promise<ProjectUpdate> {
  return request(`/api/projects/${projectId}/updates/${updateId}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}
export function deleteProjectUpdate(
  projectId: string,
  updateId: string,
): Promise<void> {
  return request(`/api/projects/${projectId}/updates/${updateId}`, {
    method: "DELETE",
  });
}
export function createProjectUpdateComment(
  projectId: string,
  updateId: string,
  body: string,
): Promise<ProjectUpdate> {
  return request(`/api/projects/${projectId}/updates/${updateId}/comments`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ body }),
  });
}
export function toggleProjectUpdateReaction(
  projectId: string,
  updateId: string,
  emoji: string,
): Promise<ProjectUpdate> {
  return request(`/api/projects/${projectId}/updates/${updateId}/reactions`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ emoji }),
  });
}
export function uploadProjectUpdateAttachment(
  projectId: string,
  updateId: string,
  file: File,
): Promise<ProjectUpdate> {
  const body = new FormData();
  body.append("file", file);
  return request(`/api/projects/${projectId}/updates/${updateId}/attachments`, {
    method: "POST",
    body,
  });
}
export function deleteProjectUpdateAttachment(
  projectId: string,
  updateId: string,
  attachmentId: string,
): Promise<ProjectUpdate> {
  return request(
    `/api/projects/${projectId}/updates/${updateId}/attachments/${attachmentId}`,
    { method: "DELETE" },
  );
}
export function setProjectDisplayDefault(
  display: Record<string, unknown>,
): Promise<Record<string, unknown>> {
  return request("/api/workspace/project-display-default", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ display }),
  });
}
export function createInitiative(
  input: InitiativeMutationInput & { name: string },
): Promise<Initiative> {
  return request("/api/initiatives", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}
export function updateInitiative(
  id: string,
  input: InitiativeMutationInput,
): Promise<Initiative> {
  return request(`/api/initiatives/${id}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}
export function deleteInitiative(id: string): Promise<void> {
  return request(`/api/initiatives/${id}`, { method: "DELETE" });
}
export function createInitiativeReminder(
  id: string,
  remindAt: string,
): Promise<Notification> {
  return request(
    `/api/initiatives/${id}/reminders`,
    jsonRequest("POST", { remindAt }),
  );
}
export function createInitiativeResource(
  id: string,
  input: {
    type?: "link" | "document";
    title?: string;
    url?: string;
    documentId?: string;
  },
): Promise<InitiativeResource> {
  return request(`/api/initiatives/${id}/resources`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}
export function updateInitiativeResource(
  id: string,
  resourceId: string,
  input: {
    type?: "link" | "document";
    title?: string;
    url?: string;
    documentId?: string;
  },
): Promise<InitiativeResource> {
  return request(`/api/initiatives/${id}/resources/${resourceId}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}
export function deleteInitiativeResource(
  id: string,
  resourceId: string,
): Promise<void> {
  return request(`/api/initiatives/${id}/resources/${resourceId}`, {
    method: "DELETE",
  });
}
export function createInitiativeComment(
  id: string,
  body: string,
): Promise<Comment> {
  return request(`/api/initiatives/${id}/comments`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ body }),
  });
}
export function updateInitiativeComment(
  id: string,
  commentId: string,
  body: string,
): Promise<Comment> {
  return request(`/api/initiatives/${id}/comments/${commentId}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ body }),
  });
}
export function deleteInitiativeComment(
  id: string,
  commentId: string,
): Promise<void> {
  return request(`/api/initiatives/${id}/comments/${commentId}`, {
    method: "DELETE",
  });
}
export function toggleInitiativeCommentReaction(
  id: string,
  commentId: string,
  emoji: string,
): Promise<Comment> {
  return request(`/api/initiatives/${id}/comments/${commentId}/reactions`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ emoji }),
  });
}
export function createInitiativeUpdate(
  id: string,
  input: {
    body: string;
    bodyData?: Record<string, unknown>;
    health?: Project["health"];
  },
): Promise<InitiativeUpdate> {
  return request(`/api/initiatives/${id}/updates`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}
export function updateInitiativeUpdate(
  id: string,
  updateId: string,
  input: {
    body?: string;
    bodyData?: Record<string, unknown>;
    health?: Project["health"];
  },
): Promise<InitiativeUpdate> {
  return request(`/api/initiatives/${id}/updates/${updateId}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}
export function deleteInitiativeUpdate(
  id: string,
  updateId: string,
): Promise<void> {
  return request(`/api/initiatives/${id}/updates/${updateId}`, {
    method: "DELETE",
  });
}
export function createInitiativeUpdateComment(
  id: string,
  updateId: string,
  body: string,
): Promise<InitiativeUpdate> {
  return request(`/api/initiatives/${id}/updates/${updateId}/comments`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ body }),
  });
}
export function toggleInitiativeUpdateReaction(
  id: string,
  updateId: string,
  emoji: string,
): Promise<InitiativeUpdate> {
  return request(`/api/initiatives/${id}/updates/${updateId}/reactions`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ emoji }),
  });
}
export function uploadInitiativeUpdateAttachment(
  id: string,
  updateId: string,
  file: File,
): Promise<InitiativeUpdate> {
  const body = new FormData();
  body.append("file", file);
  return request(`/api/initiatives/${id}/updates/${updateId}/attachments`, {
    method: "POST",
    body,
  });
}
export function deleteInitiativeUpdateAttachment(
  id: string,
  updateId: string,
  attachmentId: string,
): Promise<InitiativeUpdate> {
  return request(
    `/api/initiatives/${id}/updates/${updateId}/attachments/${attachmentId}`,
    { method: "DELETE" },
  );
}
export function createSavedView(
  input: SavedViewMutationInput,
): Promise<SavedView> {
  return request("/api/views", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}
export function updateSavedView(
  viewId: string,
  input: SavedViewMutationInput,
): Promise<SavedView> {
  return request(`/api/views/${viewId}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}
export function shareSavedView(
  viewId: string,
): Promise<{ view: SavedView; token: string; url: string }> {
  return request(`/api/views/${viewId}/share`, jsonRequest("POST", {}));
}
export function unshareSavedView(viewId: string): Promise<void> {
  return request(`/api/views/${viewId}/share`, { method: "DELETE" });
}
export function deleteSavedView(viewId: string): Promise<void> {
  return request(`/api/views/${viewId}`, { method: "DELETE" });
}

export function fetchDashboards(cursor = ""): Promise<CursorPage<Dashboard>> {
  return request(
    `/api/dashboards${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ""}`,
  );
}
export function createDashboard(
  input: Pick<Dashboard, "name" | "visibility"> &
    Partial<
      Pick<
        Dashboard,
        "description" | "teamIds" | "filters" | "hideFilters" | "widgets"
      >
    >,
): Promise<Dashboard> {
  return request("/api/dashboards", jsonRequest("POST", input));
}
export function updateDashboard(
  id: string,
  input: Partial<
    Pick<
      Dashboard,
      | "name"
      | "description"
      | "ownerId"
      | "visibility"
      | "teamIds"
      | "filters"
      | "hideFilters"
      | "widgets"
    >
  >,
): Promise<Dashboard> {
  return request(`/api/dashboards/${id}`, jsonRequest("PATCH", input));
}
export function deleteDashboard(id: string): Promise<void> {
  return request(`/api/dashboards/${id}`, { method: "DELETE" });
}
export function fetchDashboardResults(
  id: string,
): Promise<{ dashboard: Dashboard; results: DashboardWidgetResult[] }> {
  return request(`/api/dashboards/${id}/results`);
}
export function previewDashboardWidget(
  id: string,
  widget: DashboardWidget,
): Promise<DashboardWidgetResult> {
  return request(`/api/dashboards/${id}/preview`, jsonRequest("POST", widget));
}
export function subscribeDashboard(
  id: string,
  subscribed: boolean,
): Promise<Dashboard> {
  return request(`/api/dashboards/${id}/subscription`, {
    method: subscribed ? "PUT" : "DELETE",
  });
}
export function shareDashboard(
  id: string,
  shared: boolean,
): Promise<Dashboard> {
  return request(`/api/dashboards/${id}/share`, {
    method: shared ? "POST" : "DELETE",
  });
}
export function dashboardExportURL(id: string): string {
  return `/api/dashboards/${encodeURIComponent(id)}/export`;
}
export function fetchPosts(cursor = ""): Promise<CursorPage<WorkspacePost>> {
  return request(
    `/api/posts${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ""}`,
  );
}
export function createPost(
  input: Pick<WorkspacePost, "body"> &
    Partial<
      Pick<
        WorkspacePost,
        "title" | "teamIds" | "projectId" | "initiativeId" | "subscriberIds"
      >
    >,
): Promise<WorkspacePost> {
  return request("/api/posts", jsonRequest("POST", input));
}
export function updatePost(
  id: string,
  input: Partial<
    Pick<
      WorkspacePost,
      | "title"
      | "body"
      | "teamIds"
      | "projectId"
      | "initiativeId"
      | "subscriberIds"
    >
  > & { archived?: boolean },
): Promise<WorkspacePost> {
  return request(`/api/posts/${id}`, jsonRequest("PATCH", input));
}
export function deletePost(id: string): Promise<void> {
  return request(`/api/posts/${id}`, { method: "DELETE" });
}
export function fetchFeed(cursor = ""): Promise<CursorPage<FeedItem>> {
  return request(
    `/api/feed${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ""}`,
  );
}
export function fetchMeetings(cursor = ""): Promise<CursorPage<Meeting>> {
  return request(
    `/api/meetings${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ""}`,
  );
}
export function createMeeting(
  input: Pick<Meeting, "title" | "startsAt" | "durationMinutes"> &
    Partial<
      Pick<
        Meeting,
        | "description"
        | "attendeeIds"
        | "teamIds"
        | "projectIds"
        | "issueIds"
        | "url"
        | "notes"
        | "transcript"
        | "subscriberIds"
      >
    >,
): Promise<Meeting> {
  return request("/api/meetings", jsonRequest("POST", input));
}
export function updateMeeting(
  id: string,
  input: Partial<
    Omit<Meeting, "id" | "organizerId" | "createdAt" | "updatedAt">
  >,
): Promise<Meeting> {
  return request(`/api/meetings/${id}`, jsonRequest("PATCH", input));
}
export function deleteMeeting(id: string): Promise<void> {
  return request(`/api/meetings/${id}`, { method: "DELETE" });
}
export function semanticSearch(
  query: string,
  types: string[] = [],
): Promise<SemanticSearchResponse> {
  const params = new URLSearchParams({ q: query });
  if (types.length) params.set("types", types.join(","));
  return request(`/api/search/semantic?${params}`);
}
export function fetchFilterSuggestions(
  field = "",
  query = "",
): Promise<FilterSuggestion[]> {
  const params = new URLSearchParams();
  if (field) params.set("field", field);
  if (query) params.set("q", query);
  return request(`/api/search/filter-suggestions?${params}`);
}
