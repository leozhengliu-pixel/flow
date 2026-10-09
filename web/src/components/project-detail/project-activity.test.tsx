import type { ComponentProps } from 'react'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, project, viewer } from '@/test/fixtures'
import { MentionShell, pasteText, stubEditorEnvironment } from '@/test/mention-host-harness'
import { mentionFixture, mentionUrls } from '@/components/editor/mentions/mention-fixtures'
import type { AuditLogEntry, Comment, ProjectUpdate } from '@/types/flow'
import { ProjectActivity, projectUpdateChanges } from './project-activity'

const apiMocks = vi.hoisted(() => ({
  listProjectHistory: vi.fn(async () => ({ nodes: [] as unknown[], nextCursor: '', total: 0 })),
  deleteDraft: vi.fn(async () => undefined),
  fetchIssueRecord: vi.fn(),
  listProjectRecords: vi.fn(),
  listIssueRecords: vi.fn(async () => ({ items: [], hasMore: false, total: 0 })),
  uploadProjectCommentAttachment: vi.fn(async (_projectId: string, file: File) => ({ id: 'media-1', title: file.name, url: `/uploads/media-1_${file.name}`, contentType: file.type, size: file.size, createdAt: '2026-09-27T12:00:00.000Z' })),
}))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...apiMocks }))
vi.mock('@/lib/route-pages', () => ({
  AgentChatPanel: ({ autoSubmit, initialPrompt, onClose, onDraft, pageContext }: { autoSubmit?: boolean; initialPrompt?: string; onClose: () => void; onDraft?: (draft: string) => void; pageContext?: { label: string } }) => <div data-testid="agent-panel" data-auto-submit={autoSubmit ? 'true' : undefined}><p>{initialPrompt}</p><span>{pageContext?.label}</span><button onClick={onClose} type="button">Close agent</button>{onDraft && <button onClick={() => onDraft('Agent drafted update')} type="button">Finish draft</button>}</div>,
}))

function activityProps() {
  const data = makeBootstrap()
  return {
    activities: [],
    documents: data.documents,
    drafts: [],
    initiatives: [],
    integrationConnections: [],
    issues: [],
    labelGroups: [],
    labels: [],
    onCommentProject: async () => ({}) as never,
    onCommentProjectUpdate: async () => ({}) as never,
    onConvertMilestone: async () => project,
    onCreateMilestone: async () => ({}) as never,
    onCreateReminder: async () => ({}) as never,
    onCreateResource: async () => ({}) as never,
    onCreateSavedView: async () => ({}) as never,
    onCreateUpdate: async () => ({}) as never,
    onDelete: async () => undefined,
    onDeleteIssues: async () => undefined,
    onDeleteMilestone: async () => undefined,
    onDeleteProjectUpdateAttachment: async () => ({}) as never,
    onDeleteResource: async () => undefined,
    onDeleteSavedView: async () => undefined,
    onDeleteUpdate: async () => undefined,
    onMoveMilestone: async () => undefined,
    onOpenIssue: () => undefined,
    onReorderMilestones: async () => [],
    onReactProjectUpdate: async () => ({}) as never,
    onSetSubscriptionEvents: async () => undefined,
    onTabChange: () => undefined,
    onToggleFavorite: async () => undefined,
    onUpdate: async () => project,
    onUpdateIssue: async () => ({}) as never,
    onUpdateMilestone: async () => ({}) as never,
    onUpdateProjectUpdate: async () => ({}) as never,
    onUpdateResource: async () => ({}) as never,
    onUpdateSavedView: async () => ({}) as never,
    onUploadProjectUpdateAttachment: async () => ({}) as never,
    onCreateIssue: () => undefined,
    onDeleteProjectUpdate: async () => undefined,
    onOpenMilestoneIssues: () => undefined,
    onOpenSavedView: () => undefined,
    onEditSavedView: () => undefined,
    onCreateProjectUpdate: async () => ({}) as never,
    onUpdateProject: async () => project,
    project,
    projectRelations: [],
    projectStatuses: [project.status],
    projectUpdates: [],
    projects: [project],
    savedViews: [],
    tab: 'activity' as const,
    teams: data.teams,
    users: data.users,
    viewer,
  } as unknown as ComponentProps<typeof ProjectActivity>
}

describe('ProjectActivity', () => {
  it('keeps translated update status controls as one horizontal header row', async () => {
    const previousLocale = localStorage.getItem('flow:locale')
    localStorage.setItem('flow:locale', 'zh-CN')
    try {
      const { container } = render(<I18nProvider><ProjectActivity {...activityProps()} /></I18nProvider>)
      const composer = container.querySelector<HTMLElement>('.project-activity__composer')
      await userEvent.click(composer!.querySelector<HTMLButtonElement>('[role="tab"]:last-child')!)
      const header = composer?.querySelector(':scope > header')
      const tablist = header?.querySelector('[role="tablist"]')
      const health = header?.querySelector<HTMLButtonElement>('.project-activity__health')

      expect(composer).toHaveAttribute('data-mode', 'update')
      expect(tablist?.children).toHaveLength(2)
      expect(health).toBeTruthy()
      expect(health?.parentElement).toBe(header)
      expect(health).toHaveClass('is-onTrack')
      expect(health?.querySelector('svg.project-activity__health-icon')).toBeInTheDocument()
      await waitFor(() => expect(health).toHaveTextContent('进展正常'))
      expect(health?.textContent?.trim()).toBe('进展正常')
    } finally {
      if (previousLocale) localStorage.setItem('flow:locale', previousLocale)
      else localStorage.removeItem('flow:locale')
    }
  })

  it('defaults to the comment composer and lists the project creation under it', () => {
    const props = activityProps()
    const { container } = render(<I18nProvider><ProjectActivity {...props} project={{ ...props.project, lead: undefined, createdAt: '2026-09-27T12:00:00.000Z' }} /></I18nProvider>)
    const composer = container.querySelector<HTMLElement>('.project-activity__composer')
    expect(composer).toHaveAttribute('data-mode', 'comment')
    expect(composer?.querySelector('.project-activity__health')).toBeNull()
    expect(screen.getByRole('button', { name: 'Comment' })).toHaveClass('is-submit')
    const feed = container.querySelector('.project-activity__feed')
    expect(feed).toHaveTextContent(`${props.viewer.displayName} created the project`)
    expect(feed?.querySelector('.project-activity__event svg')).toBeInTheDocument()
  })

  it('does not duplicate the creation entry when the API supplies one', () => {
    const props = activityProps()
    const created = { id: 'activity-created', type: 'project.created', createdAt: '2026-09-27T12:00:00.000Z', actor: props.viewer, metadata: {} }
    const { container } = render(<I18nProvider><ProjectActivity {...props} activities={[created] as never} /></I18nProvider>)
    expect(container.querySelectorAll('.project-activity__event')).toHaveLength(1)
  })

  it('names the recorded project creator instead of the lead', () => {
    const props = activityProps()
    const creator = { ...props.viewer, id: 'user-creator', displayName: 'Skyler Anderson', name: 'Skyler Anderson' }
    const { container } = render(<I18nProvider><ProjectActivity {...props} project={{ ...props.project, creatorId: creator.id, creator, createdAt: '2026-09-27T12:00:00.000Z' }} /></I18nProvider>)
    const feed = container.querySelector('.project-activity__feed')
    expect(feed).toHaveTextContent('Skyler Anderson created the project')
    expect(feed).not.toHaveTextContent(`${props.viewer.displayName} created the project`)
  })

  it('does not group the activity feed under month headings', () => {
    const props = activityProps()
    const activities = [
      { id: 'a1', type: 'project.updated', createdAt: '2026-08-02T12:00:00.000Z', actor: props.viewer, metadata: {} },
      { id: 'a2', type: 'project.updated', createdAt: '2026-09-02T12:00:00.000Z', actor: props.viewer, metadata: {} },
    ]
    const { container } = render(<I18nProvider><ProjectActivity {...props} activities={activities as never} project={{ ...props.project, createdAt: '2026-07-20T12:00:00.000Z' }} /></I18nProvider>)
    const feed = container.querySelector('.project-activity__feed')!
    expect(feed.querySelectorAll('h2')).toHaveLength(0)
    expect(feed).not.toHaveTextContent('September')
    expect(feed.querySelectorAll('.project-activity__event')).toHaveLength(3)
  })

  it('uses the framed editor with Linear placeholders in both modes and no property diff', async () => {
    const { container } = render(<I18nProvider><ProjectActivity {...activityProps()} /></I18nProvider>)
    const composer = container.querySelector<HTMLElement>('.project-activity__composer')!
    expect(composer.querySelector('.project-activity__editor [data-placeholder="Leave a comment…"]')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('tab', { name: 'Update' }))
    await waitFor(() => expect(composer.querySelector('.project-activity__editor [data-placeholder="Write a project update…"]')).toBeInTheDocument())
    expect(composer.querySelector('textarea')).toBeNull()
    expect(composer.querySelector('.project-activity__metadata')).toBeNull()
    expect(composer).not.toHaveTextContent('No priority')
  })

  it('shows Write with Agent and keeps Post update neutral without Cancel while empty', async () => {
    render(<I18nProvider><ProjectActivity {...activityProps()} /></I18nProvider>)
    await userEvent.click(screen.getByRole('tab', { name: 'Update' }))
    const post = screen.getByRole('button', { name: 'Post update' })
    expect(post).toBeDisabled()
    expect(post).not.toHaveClass('is-primary')
    expect(screen.queryByRole('button', { name: 'Cancel' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Attach images, files, or videos' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Write with Agent' }))
    const panel = await screen.findByTestId('agent-panel')
    expect(panel).toHaveTextContent('Help me write an update for this project: Project one')
    expect(panel).toHaveAttribute('data-auto-submit', 'true')
  })

  it('writes the agent draft straight into the update composer', async () => {
    const { container } = render(<I18nProvider><ProjectActivity {...activityProps()} /></I18nProvider>)
    await userEvent.click(screen.getByRole('tab', { name: 'Update' }))
    await userEvent.click(screen.getByRole('button', { name: 'Write with Agent' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Finish draft' }))
    // Linear keeps the agent panel open next to the drafted update.
    expect(screen.getByTestId('agent-panel')).toBeInTheDocument()
    await waitFor(() => expect(container.querySelector('.project-activity__editor')).toHaveTextContent('Agent drafted update'))
    expect(screen.getByRole('button', { name: 'Post update' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Post update' })).toHaveClass('is-primary')
  })

  it('turns the submit primary with a Cancel that clears the composer once it has content', async () => {
    const props = activityProps()
    const draft = { id: 'draft-update', type: 'project_update', resourceId: props.project.id, title: props.project.name, body: 'Shipped the beta', metadata: { resourceType: 'project', health: 'atRisk' }, createdAt: '2026-09-27T12:00:00.000Z', updatedAt: '2026-09-27T12:00:00.000Z' }
    const { container } = render(<I18nProvider><ProjectActivity {...props} drafts={[draft] as never} /></I18nProvider>)
    expect(container.querySelector('.project-activity__composer')).toHaveAttribute('data-mode', 'update')
    const post = screen.getByRole('button', { name: 'Post update' })
    expect(post).toBeEnabled()
    expect(post).toHaveClass('is-primary')
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Post update' })).not.toHaveClass('is-primary'))
    expect(screen.getByRole('button', { name: 'Post update' })).toBeDisabled()
    expect(screen.queryByRole('button', { name: 'Cancel' })).toBeNull()
    expect(apiMocks.deleteDraft).toHaveBeenCalledWith('draft-update')
  })

  it('applies the same neutral/primary + Cancel behaviour to the comment submit', () => {
    const props = activityProps()
    const draft = { id: 'draft-comment', type: 'comment', resourceId: props.project.id, title: props.project.name, body: 'Looks good', metadata: { resourceType: 'project' }, createdAt: '2026-09-27T12:00:00.000Z', updatedAt: '2026-09-27T12:00:00.000Z' }
    render(<I18nProvider><ProjectActivity {...props} drafts={[draft] as never} /></I18nProvider>)
    expect(screen.getByRole('button', { name: 'Comment' })).toHaveClass('is-submit', 'is-primary')
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument()
  })

  it('renders property changes since the previous update from project history', async () => {
    const props = activityProps()
    const update = (id: string, createdAt: string): ProjectUpdate => ({ id, projectId: props.project.id, body: `Body ${id}`, health: 'onTrack', createdAt, user: props.viewer, comments: [], reactions: {}, attachments: [] })
    const history: AuditLogEntry[] = [
      { id: 'h1', actor: props.viewer, action: 'updated', resourceType: 'project', resourceId: props.project.id, metadata: { changes: [{ field: 'priority', from: '', to: 'Low' }] }, createdAt: '2026-09-01T00:00:00.000Z' },
      { id: 'h2', actor: props.viewer, action: 'updated', resourceType: 'project', resourceId: props.project.id, metadata: { changes: [{ field: 'priority', from: 'Low', to: 'High' }, { field: 'targetDate', from: '', to: '2026-12-01' }] }, createdAt: '2026-09-02T00:00:00.000Z' },
      { id: 'h3', actor: props.viewer, action: 'updated', resourceType: 'project', resourceId: props.project.id, metadata: { changes: [{ field: 'lead', from: 'Ada', to: 'Grace' }] }, createdAt: '2026-09-10T00:00:00.000Z' },
    ]
    apiMocks.listProjectHistory.mockResolvedValueOnce({ nodes: history, nextCursor: '', total: history.length })
    const updates = [update('u2', '2026-09-12T00:00:00.000Z'), update('u1', '2026-09-05T00:00:00.000Z')]
    const { container } = render(<I18nProvider><ProjectActivity {...props} project={{ ...props.project, createdAt: '2026-08-01T00:00:00.000Z' }} projectUpdates={updates} /></I18nProvider>)
    await waitFor(() => expect(container.querySelectorAll('.project-activity__changes')).toHaveLength(2))
    const [latest, first] = Array.from(container.querySelectorAll('.project-activity__update'))
    expect(first.querySelector('.project-activity__changes')).toHaveTextContent('PriorityNo priority→High')
    expect(first.querySelector('.project-activity__changes')).toHaveTextContent('Target dateNone→Dec 1')
    expect(latest.querySelector('.project-activity__changes')).toHaveTextContent('LeadAda→Grace')
    expect(latest.querySelector('.project-activity__changes')).not.toHaveTextContent('Priority')
    expect(apiMocks.listProjectHistory).toHaveBeenCalledWith(props.project.id)
  })

  it('shows the attach button in Comment mode and inserts picked files inline via the project upload', async () => {
    URL.createObjectURL = vi.fn(() => 'blob:project-comment-media')
    URL.revokeObjectURL = vi.fn()
    const props = activityProps()
    const { container } = render(<I18nProvider><ProjectActivity {...props} /></I18nProvider>)
    const composer = container.querySelector<HTMLElement>('.project-activity__composer')!
    expect(composer).toHaveAttribute('data-mode', 'comment')
    const attach = screen.getByRole('button', { name: 'Attach images, files, or videos' })
    expect(attach).toHaveClass('project-activity__attach')
    // Same slot as Update mode: right side of the footer, directly before the submit.
    expect(attach.parentElement).toBe(screen.getByRole('button', { name: 'Comment' }).parentElement)
    await waitFor(() => expect(composer.querySelector('.project-activity__editor .ProseMirror')).toBeInTheDocument())
    const image = new File([new Uint8Array([137, 80, 78, 71])], 'diagram.png', { type: 'image/png' })
    await userEvent.upload(composer.querySelector<HTMLInputElement>('input[type="file"]')!, image)
    await waitFor(() => expect(apiMocks.uploadProjectCommentAttachment).toHaveBeenCalledWith(props.project.id, image))
    await waitFor(() => expect(screen.getByRole('img', { name: 'diagram.png' })).toHaveAttribute('src', '/uploads/media-1_diagram.png'))
    // Comment-mode files are embedded in the body, not queued as update attachments.
    expect(composer.querySelector('.project-activity__files')).toBeNull()
    await waitFor(() => expect(screen.getByRole('button', { name: 'Comment' })).toBeEnabled())
  })

  it('keeps queuing Update-mode files as update attachments', async () => {
    apiMocks.uploadProjectCommentAttachment.mockClear()
    const { container } = render(<I18nProvider><ProjectActivity {...activityProps()} /></I18nProvider>)
    await userEvent.click(screen.getByRole('tab', { name: 'Update' }))
    const composer = container.querySelector<HTMLElement>('.project-activity__composer')!
    const file = new File(['notes'], 'notes.txt', { type: 'text/plain' })
    await userEvent.upload(composer.querySelector<HTMLInputElement>('input[type="file"]')!, file)
    expect(composer.querySelector('.project-activity__files')).toHaveTextContent('notes.txt')
    expect(apiMocks.uploadProjectCommentAttachment).not.toHaveBeenCalled()
  })

  it('drops changes that revert within the same update window', () => {
    const entry = (id: string, from: string, to: string, createdAt: string): AuditLogEntry => ({ id, actor: viewer, action: 'updated', resourceType: 'project', resourceId: 'p', metadata: { changes: [{ field: 'status', from, to }] }, createdAt })
    const changes = projectUpdateChanges([{ id: 'u', createdAt: '2026-09-05T00:00:00.000Z' } as ProjectUpdate], [entry('a', 'Backlog', 'Started', '2026-09-02T00:00:00.000Z'), entry('b', 'Started', 'Backlog', '2026-09-03T00:00:00.000Z')], '2026-09-01T00:00:00.000Z')
    expect(changes.get('u')).toEqual([])
  })
})

describe('ProjectActivity comment cards', () => {
  const at = (msAgo: number) => new Date(Date.now() - msAgo).toISOString()
  const other = { ...viewer, id: 'user-2', name: 'other', displayName: 'Other person' }
  function commentProps(comments: Comment[], overrides: Record<string, unknown> = {}) {
    const props = activityProps()
    return { ...props, project: { ...props.project, createdAt: '2026-01-01T00:00:00.000Z', comments }, onCommentProject: vi.fn(async () => ({}) as never), onUpdateProjectComment: vi.fn(async () => ({}) as never), onDeleteProjectComment: vi.fn(async () => undefined), onReactProjectComment: vi.fn(async () => ({}) as never), ...overrides } as ComponentProps<typeof ProjectActivity>
  }
  const root = { id: 'c-root', version: 1, body: 'Root comment', user: viewer, createdAt: at(8 * 60_000 + 5_000), reactions: { '👍': [viewer.id] } } as Comment

  it('renders a posted comment as a Linear card above the timeline events with compact time and header actions', () => {
    const props = commentProps([root, { ...root, id: 'c-new', body: 'Fresh', createdAt: at(5_000), reactions: {} }])
    const { container } = render(<I18nProvider><ProjectActivity {...props} /></I18nProvider>)
    const cards = container.querySelectorAll<HTMLElement>('.project-activity__comment-card')
    expect(cards).toHaveLength(2)
    expect(cards[0].querySelector('header time')).toHaveTextContent(/^just now$/)
    expect(cards[1].querySelector('header time')).toHaveTextContent(/^8min ago$/)
    const card = cards[1]
    expect(card).toHaveAttribute('id', 'comment-c-root')
    expect(card.querySelector('header .avatar')).toBeInTheDocument()
    expect(card.querySelector('header strong')).toHaveTextContent(viewer.displayName)
    expect(within(card).getByRole('button', { name: 'Add reaction' })).toHaveClass('project-activity__comment-action')
    expect(within(card).getByRole('button', { name: 'Comment options' })).toHaveClass('project-activity__comment-action')
    expect(within(card).getByRole('button', { name: 'Open comments' })).toHaveAttribute('aria-expanded', 'false')
    expect(card.querySelector('.project-activity__comment-body')).toBeInTheDocument()
    // The timeline events keep their own style after the last card.
    expect(cards[1].nextElementSibling).toHaveClass('project-activity__event')
  })

  it('nests replies in the thread and posts new replies with the root parentId', async () => {
    const user = userEvent.setup()
    const replies = [{ ...root, id: 'c-reply-1', parentId: root.id, body: 'First reply', reactions: {} }, { ...root, id: 'c-reply-2', parentId: root.id, body: 'Second reply', user: other, reactions: {} }] as Comment[]
    const props = commentProps([root, ...replies])
    const { container } = render(<I18nProvider><ProjectActivity {...props} /></I18nProvider>)
    expect(container.querySelectorAll('.project-activity__comment-card')).toHaveLength(1)
    const toggle = screen.getByRole('button', { name: 'Open 2 comments' })
    expect(toggle).toHaveTextContent('2')
    expect(container.querySelector('.project-activity__thread')).toBeNull()
    await user.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    const thread = container.querySelector<HTMLElement>('.project-activity__thread')!
    expect(thread.querySelectorAll('.project-activity__reply')).toHaveLength(2)
    expect(await within(thread).findByText('Second reply')).toBeInTheDocument()
    const editor = within(thread).getByRole('textbox', { name: 'Leave a reply…' }) as HTMLElement & { editor?: { commands: { setContent: (value: string) => void } } }
    await waitFor(() => expect(editor.editor).toBeTruthy())
    editor.editor!.commands.setContent('<p>Thanks!</p>')
    await user.click(within(thread).getByRole('button', { name: 'Submit comment' }))
    await waitFor(() => expect(props.onCommentProject).toHaveBeenCalledWith(props.project.id, 'Thanks!', expect.objectContaining({ type: 'doc' }), root.id))
  })

  it('offers edit and delete only on own comments and routes them to the project handlers', async () => {
    const user = userEvent.setup()
    const props = commentProps([root, { ...root, id: 'c-other', body: 'Not mine', user: other, reactions: {} }])
    const { container } = render(<I18nProvider><ProjectActivity {...props} /></I18nProvider>)
    const [ownCard, otherCard] = [container.querySelector<HTMLElement>('#comment-c-root')!, container.querySelector<HTMLElement>('#comment-c-other')!]

    await user.click(within(otherCard).getByRole('button', { name: 'Comment options' }))
    expect(screen.getByRole('menuitem', { name: 'Copy link to comment' })).toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: 'Copy content as Markdown' })).toBeInTheDocument()
    expect(screen.queryByRole('menuitem', { name: 'Edit' })).toBeNull()
    expect(screen.queryByRole('menuitem', { name: 'Delete' })).toBeNull()
    await user.keyboard('{Escape}')

    await user.click(within(ownCard).getByRole('button', { name: 'Comment options' }))
    await user.click(screen.getByRole('menuitem', { name: 'Edit' }))
    expect(ownCard.querySelector('.project-activity__comment-body')).toBeNull()
    await user.click(within(ownCard).getByRole('button', { name: 'Submit comment' }))
    await waitFor(() => expect(props.onUpdateProjectComment).toHaveBeenCalledWith(props.project.id, root.id, 'Root comment', expect.anything()))
    await waitFor(() => expect(ownCard.querySelector('.project-activity__comment-body')).toBeInTheDocument())

    await user.click(within(ownCard).getByRole('button', { name: 'Comment options' }))
    await user.click(screen.getByRole('menuitem', { name: 'Delete' }))
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Delete comment' }))
    await waitFor(() => expect(props.onDeleteProjectComment).toHaveBeenCalledWith(props.project.id, root.id))
  })

  const menuRows = (menu: HTMLElement) => Array.from(menu.querySelectorAll('[role=menuitem],[role=separator]')).map(node => node.getAttribute('role') === 'separator' ? '---' : node.textContent)

  it('lists Linear\'s comment options in order for the viewer\'s own comment', async () => {
    const user = userEvent.setup()
    render(<I18nProvider><ProjectActivity {...commentProps([root])} /></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Comment options' }))
    const menu = screen.getByRole('menu')
    expect(menu).toHaveClass('project-action-menu', 'project-comment-menu')
    expect(menuRows(menu)).toEqual(['Edit', 'Unsubscribe from thread', '---', 'Resolve thread', '---', 'Copy link to comment', 'Copy content as Markdown', '---', 'New issue from comment…', '---', 'Delete'])
    const remove = screen.getByRole('menuitem', { name: 'Delete' })
    expect(remove).not.toHaveClass('is-danger')
    expect(remove).not.toHaveClass('danger')
    for (const item of screen.getAllByRole('menuitem')) expect(item.querySelector('.project-menu-icon svg')).toBeInTheDocument()
  })

  it('hides Edit and Delete on others\' comments and offers Subscribe to thread', async () => {
    const user = userEvent.setup()
    render(<I18nProvider><ProjectActivity {...commentProps([{ ...root, user: other }])} /></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Comment options' }))
    expect(menuRows(screen.getByRole('menu'))).toEqual(['Subscribe to thread', '---', 'Resolve thread', '---', 'Copy link to comment', 'Copy content as Markdown', '---', 'New issue from comment…'])
  })

  it('lets workspace admins delete others\' comments', async () => {
    const user = userEvent.setup()
    render(<I18nProvider><ProjectActivity {...commentProps([{ ...root, user: other }], { viewerRole: 'admin' })} /></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Comment options' }))
    expect(screen.getByRole('menuitem', { name: 'Delete' })).toBeInTheDocument()
    expect(screen.queryByRole('menuitem', { name: 'Edit' })).toBeNull()
  })

  it('toggles the viewer\'s thread subscription', async () => {
    const user = userEvent.setup()
    const onProjectCommentThreadSubscription = vi.fn(async () => undefined)
    // Authors follow their own thread implicitly, so unsubscribing mutes it.
    const { unmount } = render(<I18nProvider><ProjectActivity {...commentProps([root], { onProjectCommentThreadSubscription })} /></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Comment options' }))
    await user.click(screen.getByRole('menuitem', { name: 'Unsubscribe from thread' }))
    expect(onProjectCommentThreadSubscription).toHaveBeenLastCalledWith(project.id, root.id, 'muted')
    unmount()

    // A muted author re-subscribes by clearing the explicit choice.
    const muted = [{ id: 'sub-1', userId: viewer.id, projectId: project.id, commentId: root.id, state: 'muted', createdAt: root.createdAt, updatedAt: root.createdAt }]
    const second = render(<I18nProvider><ProjectActivity {...commentProps([root], { onProjectCommentThreadSubscription, threadSubscriptions: muted })} /></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Comment options' }))
    await user.click(screen.getByRole('menuitem', { name: 'Subscribe to thread' }))
    expect(onProjectCommentThreadSubscription).toHaveBeenLastCalledWith(project.id, root.id, null)
    second.unmount()

    // Non-participants subscribe explicitly and unsubscribe by clearing it.
    const theirs = { ...root, user: other }
    const third = render(<I18nProvider><ProjectActivity {...commentProps([theirs], { onProjectCommentThreadSubscription })} /></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Comment options' }))
    await user.click(screen.getByRole('menuitem', { name: 'Subscribe to thread' }))
    expect(onProjectCommentThreadSubscription).toHaveBeenLastCalledWith(project.id, root.id, 'subscribed')
    third.unmount()
    render(<I18nProvider><ProjectActivity {...commentProps([theirs], { onProjectCommentThreadSubscription, threadSubscriptions: [{ ...muted[0], state: 'subscribed' }] })} /></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Comment options' }))
    await user.click(screen.getByRole('menuitem', { name: 'Unsubscribe from thread' }))
    expect(onProjectCommentThreadSubscription).toHaveBeenLastCalledWith(project.id, root.id, null)
  })

  it('resolves a thread, collapses it, and offers Unresolve thread', async () => {
    const user = userEvent.setup()
    const onResolveProjectComment = vi.fn(async () => ({}) as never)
    const { unmount } = render(<I18nProvider><ProjectActivity {...commentProps([root], { onResolveProjectComment })} /></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Comment options' }))
    await user.click(screen.getByRole('menuitem', { name: 'Resolve thread' }))
    expect(onResolveProjectComment).toHaveBeenCalledWith(project.id, root.id, true)
    unmount()

    const { container } = render(<I18nProvider><ProjectActivity {...commentProps([{ ...root, resolved: true }], { onResolveProjectComment })} /></I18nProvider>)
    const resolved = container.querySelector<HTMLElement>('.resolved-comment--resolved')!
    expect(resolved).toBeInTheDocument()
    expect(resolved.querySelector('.resolved-comment__thread')).toHaveAttribute('data-expanded', 'false')
    await user.click(within(resolved).getByRole('button', { name: 'Show thread' }))
    await user.click(within(resolved).getByRole('button', { name: 'Comment options' }))
    await user.click(screen.getByRole('menuitem', { name: 'Unresolve thread' }))
    expect(onResolveProjectComment).toHaveBeenLastCalledWith(project.id, root.id, false)
  })

  it('opens the create-issue flow prefilled with the comment and this project', async () => {
    const user = userEvent.setup()
    const onCreateIssue = vi.fn()
    render(<I18nProvider><ProjectActivity {...commentProps([root], { onCreateIssue })} /></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Comment options' }))
    await user.click(screen.getByRole('menuitem', { name: 'New issue from comment…' }))
    expect(onCreateIssue).toHaveBeenCalledWith(project.id, undefined, { description: 'Root comment' })
  })

  it('uses the reply menu without thread-level actions', async () => {
    const user = userEvent.setup()
    const reply = { ...root, id: 'c-reply', parentId: root.id, body: 'A reply', reactions: {} } as Comment
    render(<I18nProvider><ProjectActivity {...commentProps([root, reply])} /></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Open 1 comment' }))
    const replyNode = document.getElementById('comment-c-reply')!
    await user.click(within(replyNode).getByRole('button', { name: 'Comment options' }))
    expect(menuRows(screen.getByRole('menu'))).toEqual(['Edit', '---', 'Copy link to comment', 'Copy content as Markdown', '---', 'New issue from comment…', '---', 'Delete'])
  })

  it('toggles reactions through the reaction pills', async () => {
    const user = userEvent.setup()
    const props = commentProps([root])
    render(<I18nProvider><ProjectActivity {...props} /></I18nProvider>)
    const pill = screen.getByRole('button', { name: /👍/ })
    expect(pill).toHaveAttribute('aria-pressed', 'true')
    await user.click(pill)
    expect(props.onReactProjectComment).toHaveBeenCalledWith(props.project.id, root.id, '👍')
  })
})

describe('ProjectActivity mentions in update edits and update comments', () => {
  const projectUpdate = (props: ReturnType<typeof activityProps>, body: string): ProjectUpdate => ({ id: 'update-1', projectId: props.project.id, body, health: 'onTrack', createdAt: new Date().toISOString(), user: props.viewer, comments: [], reactions: {}, attachments: [] })
  beforeEach(() => { stubEditorEnvironment() })
  afterEach(() => { vi.unstubAllGlobals() })

  it('edits a project update in the dialog with a mention and keeps the chip when the dialog is reopened', async () => {
    const user = userEvent.setup()
    const data = mentionFixture()
    const props = { ...activityProps(), documents: data.documents, users: data.users }
    const onUpdateProjectUpdate = vi.fn(async () => ({}) as never)
    const saved = projectUpdate(props, 'Status: [Launch plan](/workspace/document/plan-abc)')
    render(<MentionShell data={data}><ProjectActivity {...props} onUpdateProjectUpdate={onUpdateProjectUpdate} projectUpdates={[saved]}/></MentionShell>)

    await user.click(screen.getByRole('button', { name: 'Open update menu' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Edit' }))
    const dialog = await screen.findByRole('dialog')
    const box = within(dialog).getByRole('textbox', { name: 'Edit project update' })
    await waitFor(() => expect(box.querySelector('a[data-agent-entity="document"]')).toHaveTextContent('Launch plan'))
    await waitFor(() => expect(box).toHaveFocus())
    // The dialog is modal (the page behind it is inert), so the "@" option is picked with the keyboard.
    await user.keyboard(' and @Road')
    await screen.findByRole('option', { name: /Roadmap/ })
    await user.keyboard('{Enter}')
    pasteText(box, `${window.location.origin}${mentionUrls.project}`)
    await waitFor(() => expect(box.querySelector('a[data-agent-entity="project"]')).toHaveTextContent('Project one'))
    await user.keyboard('{Control>}{Enter}{/Control}')

    await waitFor(() => expect(onUpdateProjectUpdate).toHaveBeenCalled())
    const [, , input] = onUpdateProjectUpdate.mock.calls[0] as unknown as [string, string, { body: string }]
    expect(input.body).toContain('[Launch plan](/workspace/document/plan-abc)')
    expect(input.body).toContain('[Roadmap](/workspace/initiative/roadmap/overview)')
    expect(input.body).toContain(`[Project one](${mentionUrls.project})`)
  })

  it('writes a comment on an update with a mention and posts the markdown', async () => {
    const user = userEvent.setup()
    const data = mentionFixture()
    const props = { ...activityProps(), documents: data.documents, users: data.users }
    const onCommentProjectUpdate = vi.fn(async () => ({}) as never)
    render(<MentionShell data={data}><ProjectActivity {...props} onCommentProjectUpdate={onCommentProjectUpdate} projectUpdates={[projectUpdate(props, 'Shipped')]}/></MentionShell>)

    await user.click(screen.getByRole('button', { name: '0 comments' }))
    const box = await screen.findByRole('textbox', { name: 'Add comment' })
    await user.click(box)
    await user.keyboard('Plan @Launch')
    await user.click(await screen.findByRole('option', { name: /Launch plan/ }))
    await user.click(within(box.closest('.project-activity__comment-box') as HTMLElement).getByRole('button', { name: 'Comment' }))
    await waitFor(() => expect(onCommentProjectUpdate).toHaveBeenCalledWith(props.project.id, 'update-1', 'Plan [Launch plan](/workspace/document/plan-abc)'))
  })
})
