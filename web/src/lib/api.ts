import type { AccountBootstrap, AccountSessionInfo, APIKey, Ask, Attachment, AuthSession, BootstrapData, Comment, Customer, CustomerMutationInput, CustomerRequest, Cycle, CycleMutationInput, CycleSettings, CycleSettingsMutationInput, Draft, ExportJob, Favorite, ImportJob, Initiative, InitiativeMutationInput, InitiativeResource, InitiativeUpdate, IntegrationConnection, Invitation, InvitationPreview, Issue, IssueLabel, IssueRelation, IssueRelationType, IssueTemplate, IssueTemplateMutationInput, IssueUpdateInput, FlowDocument, LabelGroup, Notification, NotificationDelivery, NotificationList, NotificationMutationInput, NotificationPreferences, OAuthApplication, Presence, Project, ProjectMilestone, ProjectResource, ProjectStatus, ProjectTemplate, ProjectUpdate, Release, SavedView, SavedViewMutationInput, SearchResourceType, SearchResponse, SLARule, Subscription, Team, TeamRole, TeamSettings, TeamSettingsMutationInput, User, UserSettings, WorkflowState, WorkspaceMember, WorkspaceMutationInput, WorkspaceRole, WorkspaceSettings, WorkspaceUsage } from '@/types/flow'
import type { ProjectCreateInput, ProjectMutationInput } from '@/components/projects-page/projects-page'
import type { DocumentTemplate } from '@/types/flow'

export function fetchAccountBootstrap(): Promise<AccountBootstrap> { return request('/api/account/bootstrap') }
export function setLastWorkspace(workspaceKey: string): Promise<void> { return request('/api/account/last-workspace', jsonRequest('PUT', { workspaceKey })) }
export function fetchAuthSession(): Promise<AuthSession> { return request('/api/auth/session') }
export function registerAccount(input: { name: string; email: string; password: string }): Promise<{ user: AuthSession['user']; verificationRequired: boolean; verificationToken?: string }> { return request('/api/auth/register', jsonRequest('POST', input)) }
export function verifyEmail(token: string): Promise<{ verified: boolean }> { return request('/api/auth/verify-email', jsonRequest('POST', { token })) }
export function resendVerification(email: string): Promise<{ sent: boolean; verificationToken?: string }> { return request('/api/auth/resend-verification', jsonRequest('POST', { email })) }
export function loginAccount(email: string, password: string): Promise<AuthSession> { return request('/api/auth/login', jsonRequest('POST', { email, password })) }
export function logoutAccount(): Promise<void> { return request('/api/auth/logout', { method: 'POST' }) }
export function forgotPassword(email: string): Promise<{ sent: boolean; resetToken?: string }> { return request('/api/auth/forgot-password', jsonRequest('POST', { email })) }
export function resetPassword(token: string, password: string): Promise<{ reset: boolean }> { return request('/api/auth/reset-password', jsonRequest('POST', { token, password })) }
export function fetchInvitationPreview(token: string): Promise<InvitationPreview> { return request(`/api/invitations/preview/${encodeURIComponent(token)}`) }
export function acceptInvitation(token: string): Promise<import('@/types/flow').WorkspaceMembership> { return request('/api/invitations/accept', jsonRequest('POST', { token })) }
export function inviteMembers(workspaceKey: string, input: { emails: string[]; role: WorkspaceRole; teamIds: string[] }): Promise<Invitation[]> { return request(`/api/workspaces/${encodeURIComponent(workspaceKey)}/invitations`, jsonRequest('POST', input)) }
export function revokeInvitation(workspaceKey: string, invitationId: string): Promise<void> { return request(`/api/workspaces/${encodeURIComponent(workspaceKey)}/invitations/${encodeURIComponent(invitationId)}`, { method: 'DELETE' }) }
export function resendInvitation(workspaceKey: string, invitationId: string): Promise<Invitation> { return request(`/api/workspaces/${encodeURIComponent(workspaceKey)}/invitations/${encodeURIComponent(invitationId)}/resend`, { method: 'POST' }) }
export function updateMemberRole(workspaceKey: string, userId: string, role: WorkspaceRole): Promise<WorkspaceMember> { return request(`/api/workspaces/${encodeURIComponent(workspaceKey)}/members/${encodeURIComponent(userId)}`, jsonRequest('PATCH', { role })) }
export function suspendMember(workspaceKey: string, userId: string): Promise<void> { return request(`/api/workspaces/${encodeURIComponent(workspaceKey)}/members/${encodeURIComponent(userId)}/suspend`, { method: 'POST' }) }
export function removeMember(workspaceKey: string, userId: string): Promise<void> { return request(`/api/workspaces/${encodeURIComponent(workspaceKey)}/members/${encodeURIComponent(userId)}`, { method: 'DELETE' }) }
export function setTeamMembership(workspaceKey: string, teamId: string, userId: string, member: boolean, role: TeamRole = 'member'): Promise<void> { return request(`/api/workspaces/${encodeURIComponent(workspaceKey)}/teams/${encodeURIComponent(teamId)}/members/${encodeURIComponent(userId)}`, jsonRequest('PUT', { member, role })) }
export function fetchBootstrap(workspaceKey?: string): Promise<BootstrapData> { return request('/api/bootstrap', workspaceKey ? { headers: { 'X-Workspace-Key': workspaceKey } } : undefined) }
export function createWorkspace(input: WorkspaceMutationInput & { name: string; urlKey: string }): Promise<BootstrapData> { return request('/api/workspaces', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) }) }
export function updateWorkspace(workspaceKey: string, input: WorkspaceMutationInput): Promise<BootstrapData> { return request(`/api/workspaces/${encodeURIComponent(workspaceKey)}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) }) }
export function deleteWorkspace(workspaceKey: string): Promise<void> { return request(`/api/workspaces/${encodeURIComponent(workspaceKey)}`, { method: 'DELETE' }) }
export function updateWorkspaceSettings(settings: Record<string, unknown>): Promise<Record<string, unknown>> { return request('/api/workspace/settings', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(settings) }) }
export function fetchUserSettings(): Promise<UserSettings> { return request('/api/account/settings') }
export function updateUserSettings(input: UserSettings): Promise<UserSettings> { return request('/api/account/settings', jsonRequest('PATCH', input)) }
export function updateAccountProfile(input: { displayName: string; username: string; avatarUrl?: string; jobTitle?: string }): Promise<User> { return request('/api/account/profile', jsonRequest('PATCH', input)) }
export function fetchAccountSessions(): Promise<AccountSessionInfo[]> { return request('/api/account/sessions') }
export function revokeOtherAccountSessions(): Promise<void> { return request('/api/account/sessions/others', { method: 'DELETE' }) }
export function changeAccountPassword(currentPassword: string, newPassword: string): Promise<{changed:boolean}> { return request('/api/account/change-password', jsonRequest('POST', { currentPassword, newPassword })) }
export function updateWorkspacePreferences(input: WorkspaceSettings): Promise<WorkspaceSettings> { return request('/api/workspace/preferences', jsonRequest('PATCH', input)) }
export function createWorkspaceLabel(input: { name: string; description?: string; color?: string; resourceType: 'issue'|'project'; groupId?: string }): Promise<IssueLabel> { return request('/api/labels', jsonRequest('POST', input)) }
export function updateWorkspaceLabel(id: string, input: Partial<Pick<IssueLabel,'name'|'description'|'color'|'groupId'>>): Promise<IssueLabel> { return request(`/api/labels/${id}`, jsonRequest('PATCH', input)) }
export function deleteWorkspaceLabel(id: string): Promise<void> { return request(`/api/labels/${id}`, { method: 'DELETE' }) }
export function createLabelGroup(input: { name: string; color?: string; resourceType: 'issue'|'project' }): Promise<LabelGroup> { return request('/api/label-groups', jsonRequest('POST', input)) }
export function updateLabelGroup(id: string, input: Partial<Pick<LabelGroup,'name'|'color'>>): Promise<LabelGroup> { return request(`/api/label-groups/${id}`, jsonRequest('PATCH', input)) }
export function deleteLabelGroup(id: string): Promise<void> { return request(`/api/label-groups/${id}`, { method: 'DELETE' }) }
export function createProjectStatus(input: { name: string; color?: string; type?: string }): Promise<ProjectStatus> { return request('/api/project-statuses', jsonRequest('POST', input)) }
export function updateProjectStatus(id: string, input: Partial<Pick<ProjectStatus,'name'|'color'|'type'>>): Promise<ProjectStatus> { return request(`/api/project-statuses/${id}`, jsonRequest('PATCH', input)) }
export function deleteProjectStatus(id: string): Promise<void> { return request(`/api/project-statuses/${id}`, { method: 'DELETE' }) }
export function reorderProjectStatuses(ids: string[]): Promise<ProjectStatus[]> { return request('/api/project-statuses/reorder', jsonRequest('POST', { ids })) }
export function createWorkspaceIssueTemplate(input: IssueTemplateMutationInput & { name: string }): Promise<IssueTemplate> { return request('/api/issue-templates', jsonRequest('POST', input)) }
export function updateWorkspaceIssueTemplate(id: string, input: IssueTemplateMutationInput): Promise<IssueTemplate> { return request(`/api/issue-templates/${id}`, jsonRequest('PATCH', input)) }
export function deleteWorkspaceIssueTemplate(id: string): Promise<void> { return request(`/api/issue-templates/${id}`, { method: 'DELETE' }) }
export function fetchAPIKeys(): Promise<APIKey[]> { return request('/api/api-keys') }
export function createAPIKey(input: { name: string; scopes: string[]; teamIds: string[] }): Promise<{key:APIKey;secret:string}> { return request('/api/api-keys', jsonRequest('POST', input)) }
export function revokeAPIKey(id: string): Promise<void> { return request(`/api/api-keys/${id}`, { method: 'DELETE' }) }
export function fetchOAuthApplications(): Promise<OAuthApplication[]> { return request('/api/oauth-applications') }
export function createOAuthApplication(input: { name: string; description?: string; redirectUris: string[]; scopes: string[] }): Promise<OAuthApplication> { return request('/api/oauth-applications', jsonRequest('POST', input)) }
export function updateOAuthApplication(id: string, input: Partial<Pick<OAuthApplication,'name'|'description'|'redirectUris'|'scopes'>>): Promise<OAuthApplication> { return request(`/api/oauth-applications/${id}`, jsonRequest('PATCH', input)) }
export function deleteOAuthApplication(id: string): Promise<void> { return request(`/api/oauth-applications/${id}`, { method: 'DELETE' }) }
export function fetchIntegrations(): Promise<IntegrationConnection[]> { return request('/api/integrations') }
export function connectIntegration(provider: string, input: {name?:string;config?:Record<string,string>}): Promise<IntegrationConnection> { return request(`/api/integrations/${provider}`, jsonRequest('PUT', input)) }
export function disconnectIntegration(provider: string): Promise<void> { return request(`/api/integrations/${provider}`, { method: 'DELETE' }) }
export function fetchWorkspaceUsage(): Promise<WorkspaceUsage> { return request('/api/usage') }
export function createTeam(workspaceKey: string, input: { name: string; key: string; color?: string; icon?: string; private?: boolean }): Promise<Team> { return request(`/api/workspaces/${encodeURIComponent(workspaceKey)}/teams`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) }) }
export function updateTeam(workspaceKey: string, teamId: string, input: Partial<Pick<Team,'name'|'key'|'color'|'icon'|'private'>>): Promise<Team> { return request(`/api/workspaces/${encodeURIComponent(workspaceKey)}/teams/${teamId}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) }) }
export function deleteTeam(workspaceKey: string, teamId: string): Promise<void> { return request(`/api/workspaces/${encodeURIComponent(workspaceKey)}/teams/${teamId}`, { method: 'DELETE' }) }
export function createCustomer(input: CustomerMutationInput & { name: string }): Promise<Customer> { return request('/api/customers', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) }) }
export function updateCustomer(id: string, input: CustomerMutationInput): Promise<Customer> { return request(`/api/customers/${id}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) }) }
export function deleteCustomer(id: string): Promise<void> { return request(`/api/customers/${id}`, { method: 'DELETE' }) }
export function createCustomerRequest(input: { customerId: string; body: string; source?: string; sourceUrl?: string; issueId?: string; projectId?: string }): Promise<CustomerRequest> { return request('/api/customer-requests', jsonRequest('POST', input)) }
export function updateCustomerRequest(id: string, input: Partial<Pick<CustomerRequest,'body'|'source'|'sourceUrl'|'issueId'|'projectId'>>): Promise<CustomerRequest> { return request(`/api/customer-requests/${id}`, jsonRequest('PATCH', input)) }
export function deleteCustomerRequest(id: string): Promise<void> { return request(`/api/customer-requests/${id}`, { method: 'DELETE' }) }
export function uploadCustomerRequestAttachment(id: string, file: File): Promise<Attachment> { const body = new FormData(); body.append('file', file); return request(`/api/customer-requests/${id}/attachments`, { method: 'POST', body }) }
export function deleteCustomerRequestAttachment(id: string, attachmentId: string): Promise<void> { return request(`/api/customer-requests/${id}/attachments/${attachmentId}`, { method: 'DELETE' }) }
export function createDocument(input: Partial<Pick<FlowDocument,'title'|'icon'|'content'|'contentState'|'contentData'|'projectIds'|'teamIds'|'subscriberIds'|'favorite'>> & {templateId?:string}): Promise<FlowDocument> { return request('/api/documents', jsonRequest('POST', input)) }
type DocumentTemplateMutation = Partial<Pick<DocumentTemplate,'teamId'|'name'|'description'|'title'|'icon'|'content'|'contentState'|'contentData'>>
export function createDocumentTemplate(input: DocumentTemplateMutation & {teamId:string;name:string}):Promise<DocumentTemplate>{return request('/api/document-templates',jsonRequest('POST',input))}
export function updateDocumentTemplate(id:string,input:DocumentTemplateMutation):Promise<DocumentTemplate>{return request(`/api/document-templates/${id}`,jsonRequest('PATCH',input))}
export function deleteDocumentTemplate(id:string):Promise<void>{return request(`/api/document-templates/${id}`,{method:'DELETE'})}
export function updateDocument(id: string, input: Partial<Pick<FlowDocument,'title'|'icon'|'content'|'contentState'|'contentData'|'projectIds'|'teamIds'|'subscriberIds'|'favorite'>> & { archived?: boolean }): Promise<FlowDocument> { return request(`/api/documents/${id}`, jsonRequest('PATCH', input)) }
export function deleteDocument(id: string): Promise<void> { return request(`/api/documents/${id}`, { method: 'DELETE' }) }
export function restoreDocumentRevision(id: string, revisionId: string): Promise<FlowDocument> { return request(`/api/documents/${id}/restore/${revisionId}`, { method: 'POST' }) }
export function createRelease(input: { name: string } & Partial<Omit<Release,'id'|'creator'|'createdAt'|'updatedAt'>>): Promise<Release> { return request('/api/releases', jsonRequest('POST', input)) }
export function updateRelease(id: string, input: Partial<Omit<Release,'id'|'creator'|'createdAt'|'updatedAt'>> & { archived?: boolean }): Promise<Release> { return request(`/api/releases/${id}`, jsonRequest('PATCH', input)) }
export function deleteRelease(id: string): Promise<void> { return request(`/api/releases/${id}`, { method: 'DELETE' }) }
export function createAsk(input: { title: string; body?: string; source?: string; teamId?: string; templateId?: string; issueId?: string }): Promise<Ask> { return request('/api/asks', jsonRequest('POST', input)) }
export function updateAsk(id: string, input: Partial<Pick<Ask,'title'|'body'|'source'|'teamId'|'templateId'|'issueId'>>): Promise<Ask> { return request(`/api/asks/${id}`, jsonRequest('PATCH', input)) }
export function decideAsk(id: string, decision: 'approved'|'rejected', note?: string): Promise<Ask> { return request(`/api/asks/${id}/decision`, jsonRequest('POST', { decision, note })) }
export function deleteAsk(id: string): Promise<void> { return request(`/api/asks/${id}`, { method: 'DELETE' }) }
type ProjectTemplateMutation = Partial<Pick<ProjectTemplate,'name'|'description'|'summary'|'icon'|'color'|'statusId'|'priority'|'teamIds'|'labelIds'|'leadId'|'memberIds'|'dependencyIds'|'initiativeIds'|'issueIds'|'visibility'>>
export function createProjectTemplate(input: { name: string } & ProjectTemplateMutation): Promise<ProjectTemplate> { return request('/api/project-templates', jsonRequest('POST', input)) }
export function updateProjectTemplate(id: string, input: ProjectTemplateMutation): Promise<ProjectTemplate> { return request(`/api/project-templates/${id}`, jsonRequest('PATCH', input)) }
export function deleteProjectTemplate(id: string): Promise<void> { return request(`/api/project-templates/${id}`, { method: 'DELETE' }) }
export function createSLARule(input: { name: string } & Partial<Pick<SLARule,'teamIds'|'filters'|'targetMinutes'|'pauseStatuses'|'businessHours'|'enabled'>>): Promise<SLARule> { return request('/api/sla-rules', jsonRequest('POST', input)) }
export function updateSLARule(id: string, input: Partial<Pick<SLARule,'name'|'teamIds'|'filters'|'targetMinutes'|'pauseStatuses'|'businessHours'|'enabled'>>): Promise<SLARule> { return request(`/api/sla-rules/${id}`, jsonRequest('PATCH', input)) }
export function deleteSLARule(id: string): Promise<void> { return request(`/api/sla-rules/${id}`, { method: 'DELETE' }) }
export function updateProjectUpdateSettings(input: Record<string, unknown>): Promise<Record<string, unknown>> { return request('/api/project-update-settings', jsonRequest('PUT', input)) }
export function createDraft(input: { type?: string; resourceId?: string; title?: string; body?: string; contentData?: Record<string,unknown>; metadata?: Record<string,unknown> }): Promise<Draft> { return request('/api/drafts', jsonRequest('POST', input)) }
export function updateDraft(id: string, input: Partial<Omit<Draft,'id'|'userId'|'createdAt'|'updatedAt'>>): Promise<Draft> { return request(`/api/drafts/${id}`, jsonRequest('PATCH', input)) }
export function deleteDraft(id: string): Promise<void> { return request(`/api/drafts/${id}`, { method: 'DELETE' }) }
export function addFavorite(type: string, id: string): Promise<Favorite> { return request(`/api/favorites/${type}/${id}`, { method: 'PUT' }) }
export function removeFavorite(type: string, id: string): Promise<void> { return request(`/api/favorites/${type}/${id}`, { method: 'DELETE' }) }
export function addSubscription(type: string, id: string): Promise<Subscription> { return request(`/api/subscriptions/${type}/${id}`, { method: 'PUT' }) }
export function removeSubscription(type: string, id: string): Promise<void> { return request(`/api/subscriptions/${type}/${id}`, { method: 'DELETE' }) }
export function restoreTrashEntry(id: string): Promise<unknown> { return request(`/api/trash/${id}/restore`, { method: 'POST' }) }
export function purgeTrashEntry(id: string): Promise<void> { return request(`/api/trash/${id}`, { method: 'DELETE' }) }
export function previewImport(file: File): Promise<ImportJob> { const body = new FormData(); body.append('file', file); return request('/api/imports/preview', { method: 'POST', body }) }
export function commitImport(id: string, mapping: Record<string,string>, teamId: string): Promise<ImportJob> { return request(`/api/imports/${id}/commit`, jsonRequest('POST', { mapping, teamId })) }
export function createExport(format: 'json'|'csv', includePrivate: boolean): Promise<ExportJob> { return request('/api/exports', jsonRequest('POST', { format, includePrivate })) }
export function exportDownloadUrl(id: string) { return `/api/exports/${id}/download` }

async function request<T>(url: string, init?: RequestInit): Promise<T> {
  const workspaceKey = currentWorkspaceKey()
  const headers = new Headers(init?.headers)
  if (workspaceKey && !headers.has('X-Workspace-Key')) headers.set('X-Workspace-Key', workspaceKey)
  headers.set('X-Client-ID', realtimeClientId())
  const response = await fetch(url, { ...init, headers, credentials: 'same-origin' })
  if (!response.ok) {
    const payload = await response.json().catch(()=>null)
    throw new ApiError(payload?.error || `Request failed: ${response.status}`, response.status, payload?.code, payload?.current)
  }
  if (response.status === 204) return undefined as T
  return response.json()
}

export class ApiError<T = unknown> extends Error {
  status: number
  code?: string
  current?: T
  constructor(message: string, status: number, code?: string, current?: T) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.current = current
  }
}

export function realtimeClientId() {
  const key = 'flow.realtime-client-id'
  let value = sessionStorage.getItem(key)
  if (!value) { value = crypto.randomUUID(); sessionStorage.setItem(key, value) }
  return value
}

export function searchWorkspace(query: string, types: SearchResourceType[] = [], limit = 40): Promise<SearchResponse> {
  const params = new URLSearchParams({ q: query, limit: String(limit) })
  if (types.length) params.set('types', types.join(','))
  return request(`/api/search?${params}`)
}
export function clearSearchHistory(): Promise<void> { return request('/api/search/history', { method: 'DELETE' }) }
export function recordRecentResource(type: SearchResourceType, id: string): Promise<void> { return request('/api/recent', jsonRequest('POST', { type, id })) }
export function updatePresence(clientId: string, issueId: string | undefined, route: string, active = true): Promise<Presence[]> { return request('/api/realtime/presence', jsonRequest('POST', { clientId, issueId, route, active })) }

function jsonRequest(method: string, input: unknown): RequestInit {
  return { method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) }
}

export async function createIssue(input: { title: string; description: string; descriptionState?: string; descriptionData?: Record<string, unknown>; contentState?: string; teamId: string; parentId?: string; stateId?: string; priority?: number; assigneeId?: string; projectId?: string; cycleId?: string; dueDate?: string; labelIds?: string[]; templateId?: string }): Promise<Issue> {
  return request('/api/issues', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) })
}

export function updateIssue(issueId: string, input: IssueUpdateInput): Promise<Issue> {
  return request(`/api/issues/${issueId}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) })
}
export function updateCycle(id: string, input: CycleMutationInput): Promise<Cycle> { return request(`/api/cycles/${id}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) }) }
export function startCycle(id: string): Promise<Cycle> { return request(`/api/cycles/${id}/start`, { method: 'POST' }) }
export function completeCycle(id: string): Promise<Cycle> { return request(`/api/cycles/${id}/complete`, { method: 'POST' }) }
export function updateCycleSettings(teamId: string, input: CycleSettingsMutationInput): Promise<CycleSettings> { return request(`/api/teams/${teamId}/cycle-settings`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) }) }

export function deleteIssue(issueId: string): Promise<void> { return request(`/api/issues/${issueId}`, { method: 'DELETE' }) }
export function toggleIssueReaction(issueId: string, emoji: string): Promise<Issue> { return request(`/api/issues/${issueId}/reactions`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ emoji }) }) }

export function batchUpdateIssues(issueIds: string[], update: IssueUpdateInput): Promise<Issue[]> {
  return request('/api/issues/batch', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ issueIds, update }) })
}

export async function createComment(issueId: string, body: string, bodyData?:Record<string,unknown>, parentId?:string): Promise<Comment> {
  return request(`/api/issues/${issueId}/comments`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ body, bodyData, parentId }) })
}

function currentWorkspaceKey() {
  if (typeof window === 'undefined') return ''
  const key = decodeURIComponent(window.location.pathname.split('/').filter(Boolean)[0] ?? '')
  return ['join','login','signup','verify-email','forgot-password','reset-password','invite'].includes(key) ? '' : key
}
export function updateComment(issueId:string,commentId:string,body:string,bodyData?:Record<string,unknown>,expectedVersion?:number):Promise<Comment>{return request(`/api/issues/${issueId}/comments/${commentId}`,{method:'PATCH',headers:{'Content-Type':'application/json'},body:JSON.stringify({body,bodyData,expectedVersion})})}
export function deleteComment(issueId:string,commentId:string):Promise<void>{return request(`/api/issues/${issueId}/comments/${commentId}`,{method:'DELETE'})}
export function toggleCommentReaction(issueId:string,commentId:string,emoji:string):Promise<Comment>{return request(`/api/issues/${issueId}/comments/${commentId}/reactions`,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({emoji})})}

export function createRelation(issueId: string, type: IssueRelationType, relatedIssueId: string): Promise<IssueRelation> {
  return request(`/api/issues/${issueId}/relations`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ type, relatedIssueId }) })
}

export function deleteRelation(issueId: string, relationId: string): Promise<void> { return request(`/api/issues/${issueId}/relations/${relationId}`, { method: 'DELETE' }) }

export function uploadAttachment(issueId: string, file: File): Promise<Attachment> {
  const body = new FormData(); body.append('file', file)
  return request(`/api/issues/${issueId}/attachments`, { method: 'POST', body })
}

export function deleteAttachment(issueId: string, attachmentId: string): Promise<void> { return request(`/api/issues/${issueId}/attachments/${attachmentId}`, { method: 'DELETE' }) }
export function fetchInboxNotifications(query = ''): Promise<NotificationList> { return request(`/api/notifications${query}`) }
export function updateInboxNotification(notificationId: string, input: NotificationMutationInput): Promise<Notification> { return request(`/api/notifications/${notificationId}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) }) }
export function batchNotifications(action: 'delete'|'deleteAll'|'deleteRead'|'deleteReadCompleted'|'markRead'|'markUnread'|'unsnooze', ids?: string[]): Promise<{ updated: number }> { return request('/api/notifications/batch', jsonRequest('POST', { action, ids })) }
export function fetchNotificationPreferences(): Promise<NotificationPreferences> { return request('/api/notification-preferences') }
export function updateNotificationPreferences(input: NotificationPreferences): Promise<NotificationPreferences> { return request('/api/notification-preferences', jsonRequest('PATCH', input)) }
export function fetchNotificationDeliveries(): Promise<NotificationDelivery[]> { return request('/api/notification-deliveries') }
export function acknowledgeDesktopNotifications(notificationIds: string[]): Promise<void> { return request('/api/desktop-notifications/ack', jsonRequest('POST', { notificationIds })) }
export function retryNotificationDelivery(id: string): Promise<NotificationDelivery> { return request(`/api/notification-deliveries/${id}/retry`, { method: 'POST' }) }
export function fetchWorkflowStates(teamId: string): Promise<WorkflowState[]> { return request(`/api/teams/${teamId}/states`) }
export function createWorkflowState(teamId: string, input: Partial<WorkflowState> & Pick<WorkflowState,'name'|'type'>): Promise<WorkflowState> { return request(`/api/teams/${teamId}/states`, jsonRequest('POST', input)) }
export function updateWorkflowState(teamId: string, stateId: string, input: Partial<WorkflowState> & { replacementStateId?: string }): Promise<WorkflowState> { return request(`/api/teams/${teamId}/states/${stateId}`, jsonRequest('PATCH', input)) }
export function deleteWorkflowState(teamId: string, stateId: string, replacementStateId?: string): Promise<void> { return request(`/api/teams/${teamId}/states/${stateId}`, { method: 'DELETE', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ replacementStateId }) }) }
export function reorderWorkflowStates(teamId: string, stateIds: string[]): Promise<WorkflowState[]> { return request(`/api/teams/${teamId}/states/reorder`, jsonRequest('POST', { stateIds })) }
export function fetchTeamSettings(teamId: string): Promise<TeamSettings> { return request(`/api/teams/${teamId}/settings`) }
export function updateStructuredTeamSettings(teamId: string, input: TeamSettingsMutationInput): Promise<TeamSettings> { return request(`/api/teams/${teamId}/settings`, jsonRequest('PATCH', input)) }
export function fetchIssueTemplates(teamId: string): Promise<IssueTemplate[]> { return request(`/api/teams/${teamId}/templates`) }
export function createIssueTemplate(teamId: string, input: IssueTemplateMutationInput & { name: string }): Promise<IssueTemplate> { return request(`/api/teams/${teamId}/templates`, jsonRequest('POST', input)) }
export function updateIssueTemplate(teamId: string, templateId: string, input: IssueTemplateMutationInput): Promise<IssueTemplate> { return request(`/api/teams/${teamId}/templates/${templateId}`, jsonRequest('PATCH', input)) }
export function deleteIssueTemplate(teamId: string, templateId: string): Promise<void> { return request(`/api/teams/${teamId}/templates/${templateId}`, { method: 'DELETE' }) }
export function fetchTeamLabels(teamId: string): Promise<IssueLabel[]> { return request(`/api/teams/${teamId}/labels`) }
export function createTeamLabel(teamId: string, input: Pick<IssueLabel,'name'|'color'> & { description?: string }): Promise<IssueLabel> { return request(`/api/teams/${teamId}/labels`, jsonRequest('POST', input)) }
export function updateTeamLabel(teamId: string, labelId: string, input: Partial<Pick<IssueLabel,'name'|'description'|'color'>>): Promise<IssueLabel> { return request(`/api/teams/${teamId}/labels/${labelId}`, jsonRequest('PATCH', input)) }
export function deleteTeamLabel(teamId: string, labelId: string): Promise<void> { return request(`/api/teams/${teamId}/labels/${labelId}`, { method: 'DELETE' }) }
export function createProject(input: ProjectCreateInput): Promise<Project> { return request('/api/projects', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) }) }
export function updateProject(projectId: string, input: ProjectMutationInput): Promise<Project> { return request(`/api/projects/${projectId}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) }) }
export function deleteProject(projectId: string): Promise<void> { return request(`/api/projects/${projectId}`, { method: 'DELETE' }) }
export function createProjectResource(projectId: string, input: { type?: 'link'|'document'; title?: string; url: string }): Promise<ProjectResource> { return request(`/api/projects/${projectId}/resources`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) }) }
export function updateProjectResource(projectId: string, resourceId: string, input: { type?: 'link'|'document'; title?: string; url?: string }): Promise<ProjectResource> { return request(`/api/projects/${projectId}/resources/${resourceId}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) }) }
export function deleteProjectResource(projectId: string, resourceId: string): Promise<void> { return request(`/api/projects/${projectId}/resources/${resourceId}`, { method: 'DELETE' }) }
export function createProjectMilestone(projectId: string, input: { name: string; targetDate?: string }): Promise<ProjectMilestone> { return request(`/api/projects/${projectId}/milestones`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) }) }
export function updateProjectMilestone(projectId: string, milestoneId: string, input: { name?: string; targetDate?: string }): Promise<ProjectMilestone> { return request(`/api/projects/${projectId}/milestones/${milestoneId}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) }) }
export function deleteProjectMilestone(projectId: string, milestoneId: string): Promise<void> { return request(`/api/projects/${projectId}/milestones/${milestoneId}`, { method: 'DELETE' }) }
export function createProjectComment(projectId: string, body: string): Promise<Comment> { return request(`/api/projects/${projectId}/comments`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ body }) }) }
export function createProjectUpdate(projectId: string, input: { body: string; health?: Project['health'] }): Promise<ProjectUpdate> { return request(`/api/projects/${projectId}/updates`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) }) }
export function updateProjectUpdate(projectId: string, updateId: string, input: { body?: string; health?: Project['health'] }): Promise<ProjectUpdate> { return request(`/api/projects/${projectId}/updates/${updateId}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) }) }
export function deleteProjectUpdate(projectId: string, updateId: string): Promise<void> { return request(`/api/projects/${projectId}/updates/${updateId}`, { method: 'DELETE' }) }
export function createProjectUpdateComment(projectId: string, updateId: string, body: string): Promise<ProjectUpdate> { return request(`/api/projects/${projectId}/updates/${updateId}/comments`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ body }) }) }
export function toggleProjectUpdateReaction(projectId: string, updateId: string, emoji: string): Promise<ProjectUpdate> { return request(`/api/projects/${projectId}/updates/${updateId}/reactions`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ emoji }) }) }
export function setProjectDisplayDefault(display: Record<string, unknown>): Promise<Record<string, unknown>> { return request('/api/workspace/project-display-default', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ display }) }) }
export function createInitiative(input: InitiativeMutationInput & { name: string }): Promise<Initiative> { return request('/api/initiatives', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) }) }
export function updateInitiative(id: string, input: InitiativeMutationInput): Promise<Initiative> { return request(`/api/initiatives/${id}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) }) }
export function deleteInitiative(id: string): Promise<void> { return request(`/api/initiatives/${id}`, { method: 'DELETE' }) }
export function createInitiativeResource(id: string, input: { type?: 'link'|'document'; title?: string; url: string }): Promise<InitiativeResource> { return request(`/api/initiatives/${id}/resources`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) }) }
export function updateInitiativeResource(id: string, resourceId: string, input: { type?: 'link'|'document'; title?: string; url?: string }): Promise<InitiativeResource> { return request(`/api/initiatives/${id}/resources/${resourceId}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) }) }
export function deleteInitiativeResource(id: string, resourceId: string): Promise<void> { return request(`/api/initiatives/${id}/resources/${resourceId}`, { method: 'DELETE' }) }
export function createInitiativeComment(id: string, body: string): Promise<Comment> { return request(`/api/initiatives/${id}/comments`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ body }) }) }
export function updateInitiativeComment(id: string, commentId: string, body: string): Promise<Comment> { return request(`/api/initiatives/${id}/comments/${commentId}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ body }) }) }
export function deleteInitiativeComment(id: string, commentId: string): Promise<void> { return request(`/api/initiatives/${id}/comments/${commentId}`, { method: 'DELETE' }) }
export function toggleInitiativeCommentReaction(id: string, commentId: string, emoji: string): Promise<Comment> { return request(`/api/initiatives/${id}/comments/${commentId}/reactions`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ emoji }) }) }
export function createInitiativeUpdate(id: string, input: { body: string; health?: Project['health'] }): Promise<InitiativeUpdate> { return request(`/api/initiatives/${id}/updates`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) }) }
export function updateInitiativeUpdate(id: string, updateId: string, input: { body?: string; health?: Project['health'] }): Promise<InitiativeUpdate> { return request(`/api/initiatives/${id}/updates/${updateId}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) }) }
export function deleteInitiativeUpdate(id: string, updateId: string): Promise<void> { return request(`/api/initiatives/${id}/updates/${updateId}`, { method: 'DELETE' }) }
export function createInitiativeUpdateComment(id: string, updateId: string, body: string): Promise<InitiativeUpdate> { return request(`/api/initiatives/${id}/updates/${updateId}/comments`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ body }) }) }
export function toggleInitiativeUpdateReaction(id: string, updateId: string, emoji: string): Promise<InitiativeUpdate> { return request(`/api/initiatives/${id}/updates/${updateId}/reactions`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ emoji }) }) }
export function createSavedView(input: SavedViewMutationInput): Promise<SavedView> { return request('/api/views', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) }) }
export function updateSavedView(viewId: string, input: SavedViewMutationInput): Promise<SavedView> { return request(`/api/views/${viewId}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) }) }
export function deleteSavedView(viewId: string): Promise<void> { return request(`/api/views/${viewId}`, { method: 'DELETE' }) }
