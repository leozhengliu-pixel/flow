import { FlowConfigurationError, UnknownFlowError } from './errors.js'
import { Connection, cursorConnection, type PageInfo, type PageRequest, type Page } from './connection.js'
import { FlowApi, operations } from './generated/operations.js'
import type {
  GetAttachmentsQuery,
  GetIssueRecordsQuery,
  GetWebhooksIdDeliveriesQuery,
  GetWorkspaceAuditLogQuery,
  GetCommentsQuery,
  GetCustomerRequestsQuery,
  GetCustomersQuery,
  GetCyclesQuery,
  GetDocumentsQuery,
  GetInitiativesQuery,
  GetIssueLabelsQuery,
  GetProjectMilestonesQuery,
  GetProjectsQuery,
  GetReleasesQuery,
  GetTeamsQuery,
  GetUsersQuery,
  GetWebhooksQuery,
  GetWorkflowStatesQuery,
} from './generated/operations.js'
import type {
  Attachment,
  AuditEntryTypeCount,
  AuditLogEntry,
  Comment,
  ApplicationWebhookInput,
  ApplicationWebhookSettings,
  CommentCreateInput,
  CommentUpdateInput,
  Customer,
  CustomerInput,
  CustomerRequest,
  CustomerRequestInput,
  CustomerUpsertInput,
  Cycle,
  Document,
  DocumentCreateInput,
  DocumentUpdateInput,
  FileUpload,
  FileUploadInput,
  Initiative,
  InitiativeMutationInput,
  InitiativeUpdate,
  InitiativeUpdateCreateInput,
  Issue,
  IssueCreateInput,
  IssueFilter,
  IssueLabel,
  IssueUpdateInput,
  LinkAttachmentInput,
  Organization,
  Project,
  ProjectMilestone,
  ProjectMilestoneMutationInput,
  ProjectMutationInput,
  ProjectUpdate,
  ProjectUpdateCreateInput,
  ProjectUpdateMutationInput,
  Release,
  SignedFile,
  Team,
  User,
  Viewer,
  Webhook,
  WebhookCreateInput,
  WebhookDelivery,
  WebhookFailureEvent,
  WebhookUpdateInput,
  WebhookWithSecret,
  WorkflowState,
} from './generated/models.js'
import { HttpClient, type FetchLike, type RequestOptions, type RequestSpec, type TransportOptions } from './http.js'
import type { RateLimitInfo } from './rate-limit.js'
import type {
  CustomerUpsertResult,
  IssuesArgs,
  LinkAttachmentResult,
  UploadBody,
  UploadFileOptions,
  WebhookDeliveriesArgs,
} from './types.js'

export type TokenSource = string | (() => string | Promise<string>)

/** Credentials: a personal API key or an OAuth access token (or a function returning one, called for every attempt). */
export type FlowAuth = { apiKey: string; accessToken?: undefined } | { accessToken: TokenSource; apiKey?: undefined }

export interface FlowClientConnectionOptions extends TransportOptions {
  /**
   * Origin of your Flow deployment, for example `https://flow.example.com`
   * (a trailing `/` or `/api` is ignored). Defaults to the `FLOW_BASE_URL` or
   * `FLOW_API_URL` environment variable.
   */
  apiUrl?: string
  /**
   * Replace every `/uploads/…` URL in JSON responses (Markdown included) with a
   * signed URL valid for this many seconds (1 to 604800). Sends
   * `X-Flow-File-Urls-Expire-In`.
   */
  signedFileUrlsExpireIn?: number
  /** Called with the rate-limit state of every response that carries it. */
  onRateLimit?: (info: RateLimitInfo) => void
}

export type FlowClientOptions = FlowAuth & FlowClientConnectionOptions

export type TeamsArgs = GetTeamsQuery
export type UsersArgs = GetUsersQuery
export type WorkflowStatesArgs = GetWorkflowStatesQuery
export type IssueLabelsArgs = GetIssueLabelsQuery
export type CyclesArgs = GetCyclesQuery
export type ProjectsArgs = GetProjectsQuery
export type ProjectMilestonesArgs = GetProjectMilestonesQuery
export type InitiativesArgs = GetInitiativesQuery
export type DocumentsArgs = GetDocumentsQuery
export type CommentsArgs = GetCommentsQuery
export type AttachmentsArgs = GetAttachmentsQuery
export type CustomersArgs = GetCustomersQuery
export type CustomerRequestsArgs = GetCustomerRequestsQuery
export type ReleasesArgs = GetReleasesQuery
export type WebhooksArgs = GetWebhooksQuery
export type AuditLogArgs = GetWorkspaceAuditLogQuery

/** Issues take `createAsUser` and `displayIconUrl` (actor=app tokens: "<name> via <App>") directly. */
export type CreateIssueInput = IssueCreateInput
export type CreateCommentInput = CommentCreateInput & {
  /** Issue id or identifier (`ENG-123`). Comments on documents, projects and initiatives are read-only in the SDK for now. */
  issueId: string
}
export type UpdateCommentInput = CommentUpdateInput & { issueId: string }
export type UpsertLinkAttachmentInput = LinkAttachmentInput
export type UpdateAttachmentInput = LinkAttachmentInput & { issueId: string }

export interface RawRequestOptions extends RequestOptions {
  query?: object
  body?: unknown
  /** `json` (default) parses the body, `text` returns it, `none` ignores it, `response` returns the `Response`. */
  expect?: RequestSpec['expect']
}

function normalizeApiUrl(value: string | undefined): string {
  const raw = value ?? (typeof process !== 'undefined' ? process.env?.FLOW_BASE_URL || process.env?.FLOW_API_URL : undefined)
  if (!raw) {
    throw new FlowConfigurationError('FlowClient needs the URL of your Flow deployment: pass { apiUrl: "https://flow.example.com" } or set FLOW_BASE_URL')
  }
  let url: URL
  try {
    url = new URL(raw)
  } catch {
    throw new FlowConfigurationError(`Invalid Flow API URL: ${raw}`)
  }
  if (url.protocol !== 'https:' && url.protocol !== 'http:') throw new FlowConfigurationError(`Flow API URL must be http(s): ${raw}`)
  return `${url.origin}${url.pathname.replace(/\/+$/, '').replace(/\/api$/, '')}`
}

async function resolveToken(source: TokenSource): Promise<string> {
  const token = typeof source === 'function' ? await source() : source
  if (!token) throw new FlowConfigurationError('The access token function returned an empty token')
  return token
}

function csv(value: string | string[] | undefined): string | undefined {
  if (value === undefined) return undefined
  return Array.isArray(value) ? value.join(',') : value
}

function pageOf<T>(result: unknown): Page<T> {
  if (Array.isArray(result)) return { nodes: result as T[], pageInfo: { hasNextPage: false, hasPreviousPage: false } }
  if (typeof result === 'object' && result !== null) {
    const record = result as { nodes?: T[]; items?: T[]; pageInfo?: PageInfo; totalCount?: number }
    if (Array.isArray(record.nodes)) {
      return { nodes: record.nodes, pageInfo: { hasNextPage: false, hasPreviousPage: false, ...record.pageInfo }, totalCount: record.totalCount }
    }
    if (Array.isArray(record.items)) return { nodes: record.items, pageInfo: { hasNextPage: false, hasPreviousPage: false } }
  }
  throw new UnknownFlowError({ message: 'Expected a connection ({nodes, pageInfo}) in the response', type: 'UNKNOWN', body: result })
}

/**
 * Client for the Flow REST API.
 *
 * ```ts
 * const client = new FlowClient({ apiKey: process.env.FLOW_API_KEY!, apiUrl: 'https://flow.example.com' })
 * const me = await client.viewer
 * const issues = await client.issues({ first: 25, filter: { field: 'priority', operator: 'is', values: ['urgent'] } })
 * ```
 */
export class FlowClient {
  /** The generated, one-to-one wrapper of every typed operation in the OpenAPI document. */
  readonly api: FlowApi
  /** Origin of the Flow deployment. */
  readonly apiUrl: string
  private readonly http: HttpClient

  /**
   * @param options credentials (`apiKey` or `accessToken`) and connection options (`apiUrl`, `fetch`, `headers`, `timeout`, `retry`)
   * @param extra connection options applied on top of `options`, for example `{ apiUrl }`
   */
  constructor(options: FlowClientOptions, extra: FlowClientConnectionOptions = {}) {
    const merged = { ...options, ...extra, headers: { ...options.headers, ...extra.headers } }
    const hasKey = typeof merged.apiKey === 'string' && merged.apiKey !== ''
    const hasToken = merged.accessToken !== undefined && merged.accessToken !== ''
    if (hasKey === hasToken) {
      throw new FlowConfigurationError(hasKey ? 'Pass either apiKey or accessToken, not both' : 'FlowClient needs credentials: pass { apiKey } or { accessToken }')
    }
    const source: TokenSource = hasKey ? (merged.apiKey as string) : (merged.accessToken as TokenSource)
    const fetchImpl: FetchLike | undefined = merged.fetch ?? (typeof fetch === 'function' ? (input, init) => fetch(input, init) : undefined)
    if (!fetchImpl) throw new FlowConfigurationError('No fetch implementation found: pass { fetch } (Node.js 18 or newer has one built in)')
    this.apiUrl = normalizeApiUrl(merged.apiUrl)
    this.http = new HttpClient({
      baseUrl: this.apiUrl,
      fetch: fetchImpl,
      timeout: merged.timeout,
      retry: merged.retry,
      userAgent: merged.userAgent,
      onRateLimit: merged.onRateLimit,
      headers: async () => ({
        ...(merged.signedFileUrlsExpireIn ? { 'X-Flow-File-Urls-Expire-In': String(merged.signedFileUrlsExpireIn) } : {}),
        ...merged.headers,
        Authorization: `Bearer ${await resolveToken(source)}`,
      }),
    })
    this.api = new FlowApi(this.http)
  }

  /** Rate-limit state from the most recent response that carried it. */
  get rateLimit(): RateLimitInfo | undefined {
    return this.http.lastRateLimit
  }

  // -------------------------------------------------------------------------
  // Raw access
  // -------------------------------------------------------------------------

  /**
   * Calls any route, typed or not. `path` starts with `/api/…` and may be
   * absolute for URLs on the API origin. Errors are mapped like every other call.
   *
   * ```ts
   * const delivery = await client.request<{ id: string }>('POST', '/api/webhooks/webhook_1/test')
   * ```
   */
  request<T = unknown>(method: string, path: string, options: RawRequestOptions = {}): Promise<T> {
    const { query, body, expect, ...rest } = options
    const absolute = /^https?:\/\//i.test(path)
    return this.http.request<T>({ method, path: absolute ? '' : path, url: absolute ? path : undefined, query, body, expect, ...rest })
  }

  // -------------------------------------------------------------------------
  // Pagination plumbing
  // -------------------------------------------------------------------------

  private async list<T, Q extends object>(args: (Q & Partial<PageRequest>) | undefined, call: (query: Q) => Promise<unknown>): Promise<Connection<T>> {
    const { first, after, last, before, ...rest } = (args ?? {}) as Partial<PageRequest> & Record<string, unknown>
    const pageSize = last ?? first ?? 50
    // A page argument makes every list endpoint answer with a connection.
    // `last` and `before` page backward, `first` and `after` forward.
    const backward = last !== undefined || (before !== undefined && first === undefined)
    const initial: PageRequest = backward ? { last: pageSize, before } : { first: pageSize, after }
    const fetchPage = async (request: PageRequest): Promise<Page<T>> => pageOf<T>(await call({ ...rest, ...request } as unknown as Q))
    return new Connection<T>(await fetchPage(initial), fetchPage, pageSize)
  }

  // -------------------------------------------------------------------------
  // Identity
  // -------------------------------------------------------------------------

  /** The authenticated caller: user, workspace, role, scopes and (for OAuth) the application. `await client.viewer` */
  get viewer(): Promise<Viewer> {
    return this.api.getViewer()
  }

  /** The workspace with user and team counts. `await client.organization` */
  get organization(): Promise<Organization> {
    return this.api.getOrganization()
  }

  // -------------------------------------------------------------------------
  // Issues
  // -------------------------------------------------------------------------

  /**
   * Lists issues, newest first by default, with the issue query API: cursor
   * paging in both directions (`first`/`after`, `last`/`before`; at most 100
   * per page), so `fetchNext()` and `fetchPrevious()` both work. `last`
   * without `before` starts at the final page.
   */
  async issues(args: IssuesArgs = {}, options?: RequestOptions): Promise<Connection<Issue>> {
    const { first, after, last, before, orderBy, orderDirection, filter, query, teamId, projectId, stateId, includeArchived, includeTotal } = args
    const limit = (value: number | undefined) => (value === undefined ? undefined : Math.min(100, Math.max(1, value)))
    const encoded = filter === undefined ? undefined : typeof filter === 'string' ? filter : JSON.stringify(filter satisfies IssueFilter)
    const base: GetIssueRecordsQuery = {
      orderBy: orderBy ?? 'createdAt',
      orderDirection,
      q: query,
      teamId: csv(teamId),
      projectId: csv(projectId),
      stateId: csv(stateId),
      includeArchived: includeArchived ? true : undefined,
      includeTotal: includeTotal ? true : undefined,
      filter: encoded as unknown as GetIssueRecordsQuery['filter'],
    }
    const paged = await this.list<Issue, GetIssueRecordsQuery>(
      { ...base, first: limit(first), after, last: limit(last), before },
      request => this.api.getIssueRecords({ query: request }, options),
    )
    return paged
  }

  /** One issue by id or identifier (`ENG-123`). */
  issue(id: string, options?: RequestOptions): Promise<Issue> {
    return this.api.getIssueRecordsId({ path: { id } }, options)
  }

  createIssue(input: CreateIssueInput, options?: RequestOptions): Promise<Issue> {
    return this.api.postIssues({ body: input }, options)
  }

  updateIssue(id: string, input: IssueUpdateInput, options?: RequestOptions): Promise<Issue> {
    return this.api.patchIssuesId({ path: { id }, body: input }, options)
  }

  archiveIssue(id: string, options?: RequestOptions): Promise<Issue> {
    return this.updateIssue(id, { archived: true }, options)
  }

  unarchiveIssue(id: string, options?: RequestOptions): Promise<Issue> {
    return this.updateIssue(id, { archived: false }, options)
  }

  // -------------------------------------------------------------------------
  // Comments
  // -------------------------------------------------------------------------

  /** Comments of one issue, document, project or initiative (pass exactly one of `issueId`, `documentId`, `projectId`, `initiativeId`). */
  comments(args: CommentsArgs = {}, options?: RequestOptions): Promise<Connection<Comment>> {
    return this.list<Comment, CommentsArgs>(args, query => this.api.getComments({ query }, options))
  }

  createComment(input: CreateCommentInput, options?: RequestOptions): Promise<Comment> {
    const { issueId, ...body } = input
    return this.api.postIssuesIdComments({ path: { id: issueId }, body }, options)
  }

  updateComment(commentId: string, input: UpdateCommentInput, options?: RequestOptions): Promise<Comment> {
    const { issueId, ...body } = input
    return this.api.patchIssuesIdCommentsCommentId({ path: { id: issueId, commentId }, body }, options)
  }

  deleteComment(issueId: string, commentId: string, options?: RequestOptions): Promise<void> {
    return this.api.deleteIssuesIdCommentsCommentId({ path: { id: issueId, commentId } }, options)
  }

  // -------------------------------------------------------------------------
  // Workspace structure
  // -------------------------------------------------------------------------

  teams(args?: TeamsArgs, options?: RequestOptions): Promise<Connection<Team>> {
    return this.list<Team, TeamsArgs>(args, query => this.api.getTeams({ query }, options))
  }

  /** One team by id or key (`ENG`). */
  team(id: string, options?: RequestOptions): Promise<Team> {
    return this.api.getTeamsId({ path: { id } }, options)
  }

  users(args?: UsersArgs, options?: RequestOptions): Promise<Connection<User>> {
    return this.list<User, UsersArgs>(args, query => this.api.getUsers({ query }, options))
  }

  /** One user by id; `me` is the caller. */
  user(id: string, options?: RequestOptions): Promise<User> {
    return this.api.getUsersId({ path: { id } }, options)
  }

  workflowStates(args?: WorkflowStatesArgs, options?: RequestOptions): Promise<Connection<WorkflowState>> {
    return this.list<WorkflowState, WorkflowStatesArgs>(args, query => this.api.getWorkflowStates({ query }, options))
  }

  workflowState(id: string, options?: RequestOptions): Promise<WorkflowState> {
    return this.api.getWorkflowStatesId({ path: { id } }, options)
  }

  issueLabels(args?: IssueLabelsArgs, options?: RequestOptions): Promise<Connection<IssueLabel>> {
    return this.list<IssueLabel, IssueLabelsArgs>(args, query => this.api.getIssueLabels({ query }, options))
  }

  issueLabel(id: string, options?: RequestOptions): Promise<IssueLabel> {
    return this.api.getIssueLabelsId({ path: { id } }, options)
  }

  cycles(args?: CyclesArgs, options?: RequestOptions): Promise<Connection<Cycle>> {
    return this.list<Cycle, CyclesArgs>(args, query => this.api.getCycles({ query }, options))
  }

  cycle(id: string, options?: RequestOptions): Promise<Cycle> {
    return this.api.getCyclesId({ path: { id } }, options)
  }

  // -------------------------------------------------------------------------
  // Projects, milestones, initiatives, documents, releases
  // -------------------------------------------------------------------------

  projects(args?: ProjectsArgs, options?: RequestOptions): Promise<Connection<Project>> {
    return this.list<Project, ProjectsArgs>(args, query => this.api.getProjects({ query }, options))
  }

  /** One project by id or `slugId`, with its milestones, comments and history. */
  project(id: string, options?: RequestOptions): Promise<Project> {
    return this.api.getProjectsId({ path: { id } }, options)
  }

  createProject(input: ProjectMutationInput, options?: RequestOptions): Promise<Project> {
    return this.api.postProjects({ body: input }, options)
  }

  updateProject(id: string, input: ProjectMutationInput, options?: RequestOptions): Promise<Project> {
    return this.api.patchProjectsId({ path: { id }, body: input }, options)
  }

  deleteProject(id: string, options?: RequestOptions): Promise<void> {
    return this.api.deleteProjectsId({ path: { id } }, options)
  }

  projectMilestones(args?: ProjectMilestonesArgs, options?: RequestOptions): Promise<Connection<ProjectMilestone>> {
    return this.list<ProjectMilestone, ProjectMilestonesArgs>(args, query => this.api.getProjectMilestones({ query }, options))
  }

  createProjectMilestone(projectId: string, input: ProjectMilestoneMutationInput, options?: RequestOptions): Promise<ProjectMilestone> {
    return this.api.postProjectsIdMilestones({ path: { id: projectId }, body: input }, options)
  }

  updateProjectMilestone(projectId: string, milestoneId: string, input: ProjectMilestoneMutationInput, options?: RequestOptions): Promise<ProjectMilestone> {
    return this.api.patchProjectsIdMilestonesMilestoneId({ path: { id: projectId, milestoneId }, body: input }, options)
  }

  /** Issues in the milestone keep their project and lose the milestone. */
  deleteProjectMilestone(projectId: string, milestoneId: string, options?: RequestOptions): Promise<void> {
    return this.api.deleteProjectsIdMilestonesMilestoneId({ path: { id: projectId, milestoneId } }, options)
  }

  /** Orders the milestones of a project; `ids` lists every milestone exactly once. */
  reorderProjectMilestones(projectId: string, ids: string[], options?: RequestOptions): Promise<ProjectMilestone[]> {
    return this.api.postProjectsIdMilestonesReorder({ path: { id: projectId }, body: { ids } }, options)
  }

  /** The updates of a project, newest first (not paged). */
  projectUpdates(projectId: string, options?: RequestOptions): Promise<ProjectUpdate[]> {
    return this.api.getProjectsIdUpdates({ path: { id: projectId } }, options)
  }

  createProjectUpdate(projectId: string, input: ProjectUpdateCreateInput, options?: RequestOptions): Promise<ProjectUpdate> {
    return this.api.postProjectsIdUpdates({ path: { id: projectId }, body: input }, options)
  }

  updateProjectUpdate(projectId: string, updateId: string, input: ProjectUpdateMutationInput, options?: RequestOptions): Promise<ProjectUpdate> {
    return this.api.patchProjectsIdUpdatesUpdateId({ path: { id: projectId, updateId }, body: input }, options)
  }

  deleteProjectUpdate(projectId: string, updateId: string, options?: RequestOptions): Promise<void> {
    return this.api.deleteProjectsIdUpdatesUpdateId({ path: { id: projectId, updateId } }, options)
  }

  initiatives(args?: InitiativesArgs, options?: RequestOptions): Promise<Connection<Initiative>> {
    return this.list<Initiative, InitiativesArgs>(args, query => this.api.getInitiatives({ query }, options))
  }

  /** One initiative by id or `slugId`. */
  initiative(id: string, options?: RequestOptions): Promise<Initiative> {
    return this.api.getInitiativesId({ path: { id } }, options)
  }

  createInitiative(input: InitiativeMutationInput, options?: RequestOptions): Promise<Initiative> {
    return this.api.postInitiatives({ body: input }, options)
  }

  updateInitiative(id: string, input: InitiativeMutationInput, options?: RequestOptions): Promise<Initiative> {
    return this.api.patchInitiativesId({ path: { id }, body: input }, options)
  }

  deleteInitiative(id: string, options?: RequestOptions): Promise<void> {
    return this.api.deleteInitiativesId({ path: { id } }, options)
  }

  /** The updates of an initiative, newest first (not paged). */
  initiativeUpdates(initiativeId: string, options?: RequestOptions): Promise<InitiativeUpdate[]> {
    return this.api.getInitiativesIdUpdates({ path: { id: initiativeId } }, options)
  }

  createInitiativeUpdate(initiativeId: string, input: InitiativeUpdateCreateInput, options?: RequestOptions): Promise<InitiativeUpdate> {
    return this.api.postInitiativesIdUpdates({ path: { id: initiativeId }, body: input }, options)
  }

  updateInitiativeUpdate(initiativeId: string, updateId: string, input: ProjectUpdateMutationInput, options?: RequestOptions): Promise<InitiativeUpdate> {
    return this.api.patchInitiativesIdUpdatesUpdateId({ path: { id: initiativeId, updateId }, body: input }, options)
  }

  deleteInitiativeUpdate(initiativeId: string, updateId: string, options?: RequestOptions): Promise<void> {
    return this.api.deleteInitiativesIdUpdatesUpdateId({ path: { id: initiativeId, updateId } }, options)
  }

  documents(args?: DocumentsArgs, options?: RequestOptions): Promise<Connection<Document>> {
    return this.list<Document, DocumentsArgs>(args, query => this.api.getDocuments({ query }, options))
  }

  document(id: string, options?: RequestOptions): Promise<Document> {
    return this.api.getDocumentsId({ path: { id } }, options)
  }

  /** Creates a document; `content` is Markdown. An actor=app token may pass `createAsUser`. */
  createDocument(input: DocumentCreateInput, options?: RequestOptions): Promise<Document> {
    return this.api.postDocuments({ body: input }, options)
  }

  /** Send `expectedVersion` to detect a concurrent change (`ConflictFlowError` carries the current document). */
  updateDocument(id: string, input: DocumentUpdateInput, options?: RequestOptions): Promise<Document> {
    return this.api.patchDocumentsId({ path: { id }, body: input }, options)
  }

  deleteDocument(id: string, options?: RequestOptions): Promise<void> {
    return this.api.deleteDocumentsId({ path: { id } }, options)
  }

  releases(args?: ReleasesArgs, options?: RequestOptions): Promise<Connection<Release>> {
    return this.list<Release, ReleasesArgs>(args, query => this.api.getReleases({ query }, options))
  }

  // -------------------------------------------------------------------------
  // Attachments
  // -------------------------------------------------------------------------

  /** Attachments of an issue (`issueId`) and/or with a URL (`url`). */
  attachments(args?: AttachmentsArgs, options?: RequestOptions): Promise<Connection<Attachment>> {
    return this.list<Attachment, AttachmentsArgs>(args, query => this.api.getAttachments({ query }, options))
  }

  /** Every attachment that links to `url`, across issues. */
  attachmentsForURL(url: string, args: Omit<AttachmentsArgs, 'url'> = {}, options?: RequestOptions): Promise<Connection<Attachment>> {
    return this.attachments({ ...args, url }, options)
  }

  attachment(id: string, options?: RequestOptions): Promise<Attachment> {
    return this.api.getAttachmentsId({ path: { id } }, options)
  }

  /**
   * Links an issue to an external resource. The URL is the attachment's
   * identity on the issue: sending it again updates title, subtitle, icon and
   * metadata (`created: false`) instead of creating a duplicate.
   */
  async upsertLinkAttachment(issueId: string, input: UpsertLinkAttachmentInput, options?: RequestOptions): Promise<LinkAttachmentResult<Attachment>> {
    const operation = operations.postIssuesIdLinkAttachments
    const result = await this.http.requestWithMeta<Attachment>({ method: operation.method, path: operation.path, pathParams: { id: issueId }, body: input, ...options })
    return { attachment: result.data, created: result.status === 201 }
  }

  updateAttachment(attachmentId: string, input: UpdateAttachmentInput, options?: RequestOptions): Promise<Attachment> {
    const { issueId, ...body } = input
    return this.api.patchIssuesIdAttachmentsAttachmentId({ path: { id: issueId, attachmentId }, body }, options)
  }

  deleteAttachment(issueId: string, attachmentId: string, options?: RequestOptions): Promise<void> {
    return this.api.deleteIssuesIdAttachmentsAttachmentId({ path: { id: issueId, attachmentId } }, options)
  }

  // -------------------------------------------------------------------------
  // Customers
  // -------------------------------------------------------------------------

  customers(args?: CustomersArgs, options?: RequestOptions): Promise<Connection<Customer>> {
    return this.list<Customer, CustomersArgs>(args, query => this.api.getCustomers({ query }, options))
  }

  customer(id: string, options?: RequestOptions): Promise<Customer> {
    return this.api.getCustomersId({ path: { id } }, options)
  }

  deleteCustomer(id: string, options?: RequestOptions): Promise<void> {
    return this.api.deleteCustomersId({ path: { id } }, options)
  }

  createCustomer(input: CustomerInput, options?: RequestOptions): Promise<Customer> {
    return this.api.postCustomers({ body: input }, options)
  }

  updateCustomer(id: string, input: CustomerInput, options?: RequestOptions): Promise<Customer> {
    return this.api.patchCustomersId({ path: { id }, body: input }, options)
  }

  /**
   * Creates or updates a customer, matching by `id`, then `externalId` /
   * `externalIds`, then domain, so a CRM sync can run repeatedly. `name` is
   * required when nothing matches.
   */
  async upsertCustomer(input: CustomerUpsertInput, options?: RequestOptions): Promise<CustomerUpsertResult<Customer>> {
    const operation = operations.putCustomers
    const result = await this.http.requestWithMeta<Customer>({ method: operation.method, path: operation.path, body: input, ...options })
    return { customer: result.data, created: result.status === 201 }
  }

  customerRequests(args?: CustomerRequestsArgs, options?: RequestOptions): Promise<Connection<CustomerRequest>> {
    return this.list<CustomerRequest, CustomerRequestsArgs>(args, query => this.api.getCustomerRequests({ query }, options))
  }

  createCustomerRequest(input: CustomerRequestInput, options?: RequestOptions): Promise<CustomerRequest> {
    return this.api.postCustomerRequests({ body: input }, options)
  }

  updateCustomerRequest(id: string, input: CustomerRequestInput, options?: RequestOptions): Promise<CustomerRequest> {
    return this.api.patchCustomerRequestsId({ path: { id }, body: input }, options)
  }

  // -------------------------------------------------------------------------
  // Files
  // -------------------------------------------------------------------------

  /** Step 1 of an upload: reserves an upload URL (write scope; `size` must be exact, at most 20 MB). */
  prepareFileUpload(input: FileUploadInput, options?: RequestOptions): Promise<FileUpload> {
    return this.api.postFiles({ body: input }, options)
  }

  /**
   * Uploads a file: prepares it, PUTs the bytes to the upload URL (without
   * your credentials: the token in the URL is the credential) and returns the
   * `FileUpload` whose `assetUrl` you can use in Markdown or attach to an issue.
   *
   * ```ts
   * const upload = await client.uploadFile(await readFile('shot.png'), { filename: 'shot.png', contentType: 'image/png' })
   * await client.createComment({ issueId: 'ENG-1', body: `![shot](${upload.assetUrl})` })
   * ```
   */
  async uploadFile(body: UploadBody, file: UploadFileOptions = {}, options?: RequestOptions): Promise<FileUpload> {
    const bytes = toUploadBytes(body)
    const filename = file.filename ?? bytes.name ?? 'upload'
    const contentType = file.contentType ?? (bytes.type || 'application/octet-stream')
    const upload = await this.prepareFileUpload({ filename, contentType, size: bytes.size }, options)
    const headers: Record<string, string> = { 'Content-Type': contentType }
    for (const { key, value } of upload.headers ?? []) headers[key] = value
    await this.http.request({ method: 'PUT', path: '', url: upload.uploadUrl, rawBody: bytes.data, auth: false, expect: 'none', ...options, headers: { ...headers, ...options?.headers } })
    return upload
  }

  /** Short-lived URLs that download without credentials (1 second to 7 days, default 1 hour). More than 100 URLs are sent in several requests. */
  async signFileUrls(urls: string | string[], expiresIn?: number, options?: RequestOptions): Promise<SignedFile[]> {
    const list = Array.isArray(urls) ? urls : [urls]
    const signed: SignedFile[] = []
    for (let index = 0; index < list.length; index += 100) {
      const result = await this.api.getFilesSign({ query: { url: list.slice(index, index + 100), expiresIn } }, options)
      signed.push(...(result.files ?? []))
    }
    return signed
  }

  /** Downloads a private file (`/uploads/…` on the API origin) with your credentials. */
  downloadFile(url: string, options?: RequestOptions): Promise<Response> {
    return this.http.request<Response>({ method: 'GET', path: '', url, expect: 'response', ...options })
  }

  // -------------------------------------------------------------------------
  // Webhooks (administrators)
  // -------------------------------------------------------------------------

  webhooks(args?: WebhooksArgs, options?: RequestOptions): Promise<Connection<Webhook>> {
    return this.list<Webhook, WebhooksArgs>(args, query => this.api.getWebhooks({ query }, options))
  }

  webhook(id: string, options?: RequestOptions): Promise<Webhook> {
    return this.api.getWebhooksId({ path: { id } }, options)
  }

  /** Creates a webhook. The result carries the signing `secret`, shown only once. */
  createWebhook(input: WebhookCreateInput, options?: RequestOptions): Promise<WebhookWithSecret> {
    return this.api.postWebhooks({ body: input }, options)
  }

  updateWebhook(id: string, input: WebhookUpdateInput, options?: RequestOptions): Promise<Webhook> {
    return this.api.patchWebhooksId({ path: { id }, body: input }, options)
  }

  deleteWebhook(id: string, options?: RequestOptions): Promise<void> {
    return this.api.deleteWebhooksId({ path: { id } }, options)
  }

  /** Issues a new signing secret, returned once. Deploy it to your receiver right away. */
  rotateWebhookSecret(id: string, options?: RequestOptions): Promise<WebhookWithSecret> {
    return this.api.postWebhooksIdRotateSecret({ path: { id } }, options)
  }

  /** Removes the signing secret; deliveries go out unsigned until a new one is rotated in. */
  revokeWebhookSecret(id: string, options?: RequestOptions): Promise<void> {
    return this.api.postWebhooksIdRevokeSecret({ path: { id } }, options)
  }

  /** The delivery log of a webhook, newest first (cursor paging: forward only). */
  webhookDeliveries(id: string, args: WebhookDeliveriesArgs = {}, options?: RequestOptions): Promise<Connection<WebhookDelivery>> {
    const limit = args.first ?? 50
    return cursorConnection<WebhookDelivery>(
      async (cursor, size) => {
        const query: GetWebhooksIdDeliveriesQuery = { status: args.status, cursor, limit: size }
        const page = await this.api.getWebhooksIdDeliveries({ path: { id }, query }, options)
        return { items: page.items ?? [], hasMore: Boolean(page.hasMore), nextCursor: page.nextCursor }
      },
      { after: args.after, limit },
    )
  }

  /** One delivery with its request payload, every attempt and the start of the response body. */
  webhookDelivery(id: string, deliveryId: string, options?: RequestOptions): Promise<WebhookDelivery> {
    return this.api.getWebhooksIdDeliveriesDeliveryId({ path: { id, deliveryId } }, options)
  }

  /** Failed attempts, newest first (compatibility view of the delivery log). */
  webhookFailures(id: string, options?: RequestOptions): Promise<WebhookFailureEvent[]> {
    return this.api.getWebhooksIdFailures({ path: { id } }, options)
  }

  /** Sends a `WebhookTest` event; resolves to the delivery. */
  testWebhook(id: string, options?: RequestOptions): Promise<WebhookDelivery> {
    return this.api.postWebhooksIdTest({ path: { id } }, options)
  }

  /** Sends a delivery again as a new delivery with a new id, timestamp and signature. */
  redeliverWebhook(id: string, deliveryId: string, options?: RequestOptions): Promise<WebhookDelivery> {
    return this.api.postWebhooksIdDeliveriesDeliveryIdRedeliver({ path: { id, deliveryId } }, options)
  }

  /** The webhook of an OAuth application, set once for every workspace that installs it. */
  applicationWebhook(applicationId: string, options?: RequestOptions): Promise<ApplicationWebhookSettings> {
    return this.api.getOauthApplicationsIdWebhook({ path: { id: applicationId } }, options)
  }

  /** Saves the application webhook; enabling it the first time returns the signing `secret` once. */
  setApplicationWebhook(applicationId: string, input: ApplicationWebhookInput, options?: RequestOptions): Promise<ApplicationWebhookSettings> {
    return this.api.putOauthApplicationsIdWebhook({ path: { id: applicationId }, body: input }, options)
  }

  rotateApplicationWebhookSecret(applicationId: string, options?: RequestOptions): Promise<ApplicationWebhookSettings> {
    return this.api.postOauthApplicationsIdWebhookRotateSecret({ path: { id: applicationId } }, options)
  }

  // -------------------------------------------------------------------------
  // Audit log (administrators)
  // -------------------------------------------------------------------------

  auditLog(args?: AuditLogArgs, options?: RequestOptions): Promise<Connection<AuditLogEntry>> {
    return this.list<AuditLogEntry, AuditLogArgs>(args, query => this.api.getWorkspaceAuditLog({ query }, options))
  }

  /** The audit entry types present in the log with their counts. */
  auditLogTypes(options?: RequestOptions): Promise<AuditEntryTypeCount[]> {
    return this.api.getWorkspaceAuditLogTypes(options)
  }
}

interface UploadBytes {
  data: BodyInit
  size: number
  name?: string
  type?: string
}

function toUploadBytes(body: UploadBody): UploadBytes {
  if (typeof body === 'string') {
    const data = new TextEncoder().encode(body)
    return { data: data as unknown as BodyInit, size: data.byteLength }
  }
  if (typeof Blob !== 'undefined' && body instanceof Blob) {
    return { data: body, size: body.size, name: (body as { name?: string }).name, type: body.type }
  }
  if (ArrayBuffer.isView(body)) return { data: body as unknown as BodyInit, size: body.byteLength }
  if (body instanceof ArrayBuffer) return { data: body, size: body.byteLength }
  throw new TypeError('uploadFile expects a Blob, File, ArrayBuffer, typed array, Buffer or string')
}
