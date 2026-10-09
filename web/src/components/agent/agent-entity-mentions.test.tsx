import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue, project as baseProject, teammate, viewer } from '@/test/fixtures'
import type { BootstrapData, Issue, Project } from '@/types/flow'

const api = vi.hoisted(() => ({ fetchIssueRecord: vi.fn(), listProjectRecords: vi.fn() }))
vi.mock('@/lib/api', () => api)

import { AgentAnswerText, AgentReferencedResources } from './agent-answer'
import { linkAgentEntities, parseAgentAnswer } from './agent-answer-content'
import { resetAgentRecordCache } from './agent-entity-fetch'

const milestoneProject = { ...baseProject, milestones: [{ id: 'milestone-1', projectId: 'project-1', name: 'Alpha', description: '', targetDate: '2026-09-30', createdAt: '', updatedAt: '' }] } as unknown as Project

function fixture(overrides: Partial<BootstrapData> = {}) {
  return makeBootstrap({
    users: [{ ...viewer, username: 'viewer' }, teammate],
    projects: [milestoneProject],
    initiatives: [{ id: 'initiative-1', slugId: 'roadmap', name: 'Roadmap', summary: 'Ship it', description: '', color: '#f2c94c', icon: 'Initiative', status: 'active', priority: 2, priorityLabel: 'High', health: 'onTrack', owner: viewer, creator: viewer, projectIds: [], contributingTeamIds: [], labelIds: [], resources: [], comments: [], favorite: false, subscribed: false, createdAt: '', updatedAt: '' }] as never,
    documents: [{ id: 'document-1', slugId: 'plan-abc', title: 'Launch plan', color: '#8b8b90', content: '', creator: viewer, projectIds: ['project-1'], teamIds: [], subscriberIds: [], favorite: false, createdAt: '2026-09-01T00:00:00Z', updatedAt: '2026-09-29T00:00:00Z', revisions: [] }] as never,
    cycles: [{ id: 'cycle-1', number: 3, name: 'Cycle 3', description: '', teamId: 'team-1', startsAt: '2026-09-28T00:00:00Z', endsAt: '2026-10-04T00:00:00Z', status: 'current', capacity: 0, favorite: false, resources: [], createdAt: '', updatedAt: '' }] as never,
    customers: [{ id: 'customer-1234567890ab', name: 'Acme Corp', status: 'active', tier: 'Gold', domains: [], createdAt: '', updatedAt: '' }] as never,
    savedViews: [{ id: 'view-1', slugId: 'urgent-view', name: 'Urgent view', description: 'All urgent work', scope: 'workspace', ownerId: 'user-2', view: 'all', filters: [], display: {}, createdAt: '', updatedAt: '2026-09-27T00:00:00Z' }] as never,
    releasePipelines: [{ id: 'pipeline-1', slugId: 'web', name: 'Web', teamIds: [], type: 'scheduled', production: true, stages: [], stageStatuses: {}, position: 0, pathFilters: [], autoGenerateReleaseNotes: false, createdAt: '', updatedAt: '' }] as never,
    releases: [{ id: 'release-1', slugId: 'v1', name: 'Version one', version: '1.0', description: '', status: 'planned', pipelineId: 'pipeline-1', position: 0, projectIds: [], issueIds: [], subscriberIds: [], resources: [], creator: viewer, createdAt: '', updatedAt: '' }] as never,
    reviews: [{ id: 'review-1', slugId: 'fix-login', provider: 'github', externalId: '1', number: 7, title: 'Fix login', description: '', status: 'inReview', repositoryOwner: 'acme', repositoryName: 'web', url: '', author: teammate, reviewerIds: [], teamReviewers: [], issueIds: [], baseBranch: 'main', headBranch: 'fix', branchState: 'upToDate', additions: 0, deletions: 0, commitCount: 1, checks: [], files: [], events: [], favorite: false, draft: false, quickToReview: false, createdAt: '', updatedAt: '' }] as never,
    teamMembers: [{ teamId: 'team-1', userId: 'user-1', role: 'member', joinedAt: '' }] as never,
    ...overrides,
  })
}

const urls = {
  issue: '/workspace/issue/TST-1/test-issue',
  project: '/workspace/project/project-one/overview',
  initiative: '/workspace/initiative/roadmap/overview',
  document: '/workspace/document/plan-abc',
  team: '/workspace/team/TST/overview',
  cycle: '/workspace/team/TST/cycle/3',
  label: '/workspace/issue-label/Feature',
  milestone: '/workspace/project/project-one/overview#milestone-milestone-1',
  customer: '/workspace/customer/acme-corp-1234567890ab',
  release: '/workspace/pipeline/web/release/v1/issues',
  view: '/workspace/view/urgent-view',
  review: '/workspace/review/fix-login',
  update: '/workspace/project/project-one/overview#update-abc',
  comment: '/workspace/issue/TST-1/test-issue#comment-xyz',
}

function Where() {
  const location = useLocation()
  return <output data-testid="location">{`${location.pathname}${location.hash}`}</output>
}

function renderAnswer(markdown: string, data: BootstrapData) {
  const answer = parseAgentAnswer(markdown, data)
  return render(
    <I18nProvider>
      <MemoryRouter>
        <AgentAnswerText ariaLabel="AI message" className="answer" data={data} markdown={answer.markdown}/>
        <AgentReferencedResources data={data} references={answer.references}/>
        <Where/>
      </MemoryRouter>
    </I18nProvider>,
  )
}

async function chipFor(kind: string) {
  return waitFor(() => {
    const chip = document.querySelector(`[role="document"] a[data-agent-entity="${kind}"]`)
    expect(chip, kind).not.toBeNull()
    return chip as HTMLAnchorElement
  })
}

describe('linkAgentEntities for every resource type', () => {
  const data = fixture()
  const origin = window.location.origin

  it('turns each resource link into a typed shortcode', () => {
    const cases: Array<[string, string, string]> = [
      ['issue', urls.issue, 'id="issue-1" label="TST-1"'],
      ['project', urls.project, 'id="project-1" label="Project one"'],
      ['initiative', urls.initiative, 'id="initiative-1" label="Roadmap"'],
      ['document', urls.document, 'id="document-1" label="Launch plan"'],
      ['team', urls.team, 'id="team-1" label="Test team"'],
      ['cycle', urls.cycle, 'id="cycle-1" label="Cycle 3"'],
      ['label', urls.label, 'id="label-1" label="Feature"'],
      ['milestone', urls.milestone, 'id="milestone-1" label="Alpha"'],
      ['customer', urls.customer, 'id="customer-1234567890ab" label="Acme Corp"'],
      ['release', urls.release, 'id="release-1" label="Version one"'],
      ['view', urls.view, 'id="view-1" label="Urgent view"'],
      ['review', urls.review, 'id="review-1" label="Fix login"'],
    ]
    for (const [kind, url, attributes] of cases) {
      expect(linkAgentEntities(`See [thing](${url}).`, data, { all: true }).markdown, kind).toBe(`See [agentEntity kind="${kind}" ${attributes}].`)
    }
    // The model sometimes writes the customer's name, spaces and all, instead of the slug.
    expect(linkAgentEntities('See [Acme Corp](/workspace/customer/Acme Corp-1234567890ab).', data, { all: true }).markdown).toBe('See [agentEntity kind="customer" id="customer-1234567890ab" label="Acme Corp"].')
  })

  it('keeps updates and comments addressable by their link', () => {
    expect(linkAgentEntities(`[Weekly update](${urls.update})`, data, { all: true }).markdown).toBe(`[agentEntity kind="update" id="${urls.update}" label="Weekly update" href="${urls.update}"]`)
    expect(linkAgentEntities(`[the comment](${urls.comment})`, data, { all: true }).markdown).toContain('kind="comment"')
  })

  it('links absolute app URLs and @mentions of people but leaves code, other hosts and unknown resources alone', () => {
    const { markdown, references } = linkAgentEntities(`${origin}/workspace/document/plan-abc and @Viewer, \`TST-1\`, [x](https://elsewhere.test/workspace/document/plan-abc), [gone](/workspace/document/missing), [other ws](/other/document/plan-abc)\n\n\`\`\`\n[Launch plan](${urls.document})\n\`\`\``, data, { all: true })
    expect(markdown).toContain('[agentEntity kind="document" id="document-1" label="Launch plan"] and [agentEntity kind="user" id="user-1" label="Viewer"], `TST-1`')
    expect(markdown).toContain('[x](https://elsewhere.test/workspace/document/plan-abc), [gone](/workspace/document/missing), [other ws](/other/document/plan-abc)')
    expect(markdown).toContain(`\`\`\`\n[Launch plan](${urls.document})\n\`\`\``)
    expect(references.map(reference => reference.kind)).toEqual(['document', 'user'])
  })

  it('is idempotent and, without `all`, still links only issues and projects the data holds', () => {
    const once = linkAgentEntities(`[Launch plan](${urls.document}) TST-1`, data, { all: true }).markdown
    expect(linkAgentEntities(once, data, { all: true }).markdown).toBe(once)
    const legacy = linkAgentEntities(`[Launch plan](${urls.document}) TST-1 ${urls.team}`, data).markdown
    expect(legacy).toBe(`[Launch plan](${urls.document}) [agentEntity kind="issue" id="issue-1" label="TST-1"] ${urls.team}`)
  })

  it('treats team-key identifiers as issues the client may not hold (paged workspaces and records created this session)', () => {
    const paged = fixture({ issueCollectionPaged: true, issues: [] })
    expect(linkAgentEntities('See TST-77 and UTF-8 and DEV-1.', paged, { all: true }).markdown).toBe('See [agentEntity kind="issue" id="TST-77" label="TST-77"] and UTF-8 and DEV-1.')
    expect(linkAgentEntities('See TST-77 and DEV-1.', fixture({ issues: [] }), { all: true }).markdown).toBe('See [agentEntity kind="issue" id="TST-77" label="TST-77"] and DEV-1.')
    expect(linkAgentEntities('[x](/workspace/issue/TST-77/y) [z](/workspace/issue/DEV-1/y)', fixture(), { all: true }).markdown).toBe('[agentEntity kind="issue" id="TST-77" label="TST-77"] [z](/workspace/issue/DEV-1/y)')
    expect(linkAgentEntities('[x](/workspace/project/gone/overview)', fixture({ issueCollectionPaged: true, projects: [] }), { all: true }).markdown).toBe('[agentEntity kind="project" id="gone" label="x"]')
    expect(linkAgentEntities('[x](/workspace/project/gone/overview)', fixture(), { all: true }).markdown).toBe('[agentEntity kind="project" id="gone" label="x"]')
    // Without `all` (loop instructions) only records the data holds are linked.
    expect(linkAgentEntities('See TST-77.', fixture({ issues: [] })).markdown).toBe('See TST-77.')
  })
})

describe('agent entity chips', () => {
  beforeEach(() => {
    api.fetchIssueRecord.mockReset()
    api.listProjectRecords.mockReset()
    resetAgentRecordCache()
  })

  it('renders a chip with the resource icon and label for every type, linking to its Flow route', async () => {
    const data = fixture()
    const markdown = Object.entries(urls).map(([kind, url]) => `${kind}: [${kind} link](${url})`).join('\n\n')
    renderAnswer(`${markdown}\n\nPerson: @Viewer`, data)
    const expected: Record<string, [string, string]> = {
      issue: ['TST-1 Test issue', urls.issue],
      project: ['Project one', '/workspace/project/project-one/overview'],
      initiative: ['Roadmap', urls.initiative],
      document: ['Launch plan', urls.document],
      user: ['@Viewer', '/workspace/profiles/viewer'],
      team: ['Test team', urls.team],
      cycle: ['Cycle 3', urls.cycle],
      label: ['Feature', urls.label],
      milestone: ['Alpha · Project one', urls.milestone],
      customer: ['Acme Corp', urls.customer],
      release: ['Version one', '/workspace/pipeline/web/release/v1/issues'],
      view: ['Urgent view', urls.view],
      review: ['Fix login', urls.review],
      update: ['update link', urls.update],
      comment: ['comment link', urls.comment],
    }
    for (const [kind, [text, href]] of Object.entries(expected)) {
      const chip = await chipFor(kind)
      expect(chip.textContent?.replace(/ /g, ' ').trim(), kind).toBe(text)
      expect(chip, kind).toHaveAttribute('href', href)
    }
    // Every chip is a real link a keyboard user can reach.
    const answer = screen.getByRole('document', { name: 'AI message' })
    expect(answer.querySelectorAll('a[data-agent-entity]').length).toBeGreaterThanOrEqual(15)
    expect(answer.querySelectorAll('a[data-agent-entity][tabindex="-1"]')).toHaveLength(0)
  })

  it('does not chip mentions inside code spans or code blocks', async () => {
    renderAnswer(`Inline \`TST-1\` and\n\n\`\`\`\nTST-1 ${urls.project}\n\`\`\``, fixture())
    await screen.findByRole('document', { name: 'AI message' })
    expect(document.querySelector('a[data-agent-entity]')).toBeNull()
    expect(document.querySelector('pre')).toHaveTextContent('TST-1')
  })

  it('renders chips inside tables and lists', async () => {
    renderAnswer(`| Project | Issue |\n| --- | --- |\n| [Project one](${urls.project}) | TST-1 |\n\n- [Launch plan](${urls.document})\n- TST-1`, fixture())
    const table = await screen.findByRole('table')
    await waitFor(() => expect(within(table).getAllByRole('link')).toHaveLength(2))
    expect(within(screen.getByRole('list')).getAllByRole('link')).toHaveLength(2)
  })

  it('falls back to plain text for resources that cannot be found', async () => {
    renderAnswer(`Unknown [Missing doc](/workspace/document/nope) TST-1`, fixture())
    await chipFor('issue')
    expect(screen.getByRole('document', { name: 'AI message' })).toHaveTextContent('Unknown Missing doc')
    expect(document.querySelectorAll('a[data-agent-entity]')).toHaveLength(1)
  })

  it('navigates inside the app on click, but leaves modified clicks to the browser', async () => {
    const user = userEvent.setup()
    renderAnswer(`[Launch plan](${urls.document})`, fixture())
    const chip = await chipFor('document')
    await user.click(chip)
    expect(screen.getByTestId('location')).toHaveTextContent('/workspace/document/plan-abc')
    await user.click(await chipFor('document'))
    const next = await chipFor('document')
    await user.keyboard('{Meta>}')
    const before = screen.getByTestId('location').textContent
    await user.click(next)
    await user.keyboard('{/Meta}')
    expect(screen.getByTestId('location').textContent).toBe(before)
    expect(next.getAttribute('target')).toBeNull()
  })

  it('opens the hover card when the chip is focused or hovered', async () => {
    const user = userEvent.setup()
    const data = fixture({ issues: [makeIssue()] })
    renderAnswer(`[A](${urls.issue}) [B](${urls.project})`, data)
    const issue = await chipFor('issue')
    await user.tab()
    expect(issue).toHaveFocus()
    await screen.findByRole('tooltip')
    expect(document.querySelector('[data-agent-entity-card="issue"]')).toHaveTextContent('TST-1')
    expect(document.querySelector('[data-agent-entity-card="issue"]')).toHaveTextContent('Test issue')
    expect(document.querySelector('[data-agent-entity-card="issue"]')).toHaveTextContent('In progress')
    expect(document.querySelector('[data-agent-entity-card="issue"]')).toHaveTextContent('High')
    await user.keyboard('{Escape}')
    await waitFor(() => expect(document.querySelector('[data-agent-entity-card]')).toBeNull())
    await user.hover(await chipFor('project'))
    await waitFor(() => expect(document.querySelector('[data-agent-entity-card="project"]')).not.toBeNull(), { timeout: 2000 })
    expect(document.querySelector('[data-agent-entity-card="project"]')).toHaveTextContent('Project one')
    expect(document.querySelector('[data-agent-entity-card="project"]')).toHaveTextContent('High')
  })

  it('shows each type\'s own details in its hover card', async () => {
    const user = userEvent.setup()
    const data = fixture()
    const expectations: Array<[string, string, string[]]> = [
      ['initiative', urls.initiative, ['Roadmap', 'Active', 'High', 'Viewer']],
      ['document', urls.document, ['Launch plan', 'Project one', 'Last edited', 'Viewer']],
      ['team', urls.team, ['Test team', '(TST)', 'Project one', 'No documents']],
      ['cycle', urls.cycle, ['Cycle 3', 'Test team', 'Current']],
      ['label', urls.label, ['Feature']],
      ['milestone', urls.milestone, ['Alpha', 'Project one']],
      ['customer', urls.customer, ['Acme Corp', 'Active', 'Gold']],
      ['release', urls.release, ['Version one', 'Web', '1.0']],
      ['view', urls.view, ['Urgent view', 'All urgent work', 'Teammate']],
      ['review', urls.review, ['acme/web#7', 'Fix login', 'In review', 'Teammate']],
    ]
    for (const [kind, url, texts] of expectations) {
      const { unmount } = renderAnswer(`[x](${url})`, data)
      const chip = await chipFor(kind)
      await act(async () => { chip.focus() })
      const card = await waitFor(() => {
        const found = document.querySelector(`[data-agent-entity-card="${kind}"]`)
        expect(found, kind).not.toBeNull()
        return found as HTMLElement
      })
      for (const text of texts) expect(card, `${kind}: ${text}`).toHaveTextContent(text)
      await user.keyboard('{Escape}')
      unmount()
    }
  })

  it('shows a person\'s card for an @mention', async () => {
    renderAnswer('@Viewer', fixture())
    const chip = await chipFor('user')
    expect(chip).toHaveAttribute('href', '/workspace/profiles/viewer')
    await act(async () => { chip.focus() })
    const card = await waitFor(() => document.querySelector('[data-agent-entity-card="user"]') as HTMLElement)
    expect(card).toHaveTextContent('Viewer')
    expect(card).toHaveTextContent('@viewer')
    expect(card).toHaveTextContent('Test team')
  })

  it('lists referenced issues as rows and documents / views as cards with hover cards', async () => {
    const data = fixture({ issues: [makeIssue(), makeIssue({ id: 'issue-2', identifier: 'TST-2', number: 2, title: 'Second issue', assignee: undefined })] })
    renderAnswer(`TST-1 TST-2 [Launch plan](${urls.document}) [Urgent view](${urls.view})`, data)
    const rows = await screen.findByRole('list', { name: 'Referenced issues' })
    expect(within(rows).getAllByRole('link')).toHaveLength(2)
    expect(within(rows).getAllByRole('link')[0]).toHaveAttribute('href', urls.issue)
    const documentCard = document.querySelector('a[data-agent-entity="document"][class*="referenceCard"]') as HTMLElement
    expect(documentCard).toHaveTextContent('Launch plan')
    expect(documentCard).toHaveTextContent('Project one')
    expect(documentCard).toHaveTextContent(/Last edited .* by Viewer/)
    const viewCard = document.querySelector('a[data-agent-entity="view"][class*="referenceCard"]') as HTMLElement
    expect(viewCard).toHaveTextContent('Urgent view')
    expect(viewCard).toHaveTextContent('Teammate')
    await act(async () => { within(rows).getAllByRole('link')[1].focus() })
    await waitFor(() => expect(document.querySelector('[data-agent-entity-card="issue"]')).toHaveTextContent('Second issue'))
  })

  describe('records created after the page loaded (non-paged workspace data lacks them)', () => {
    it('fetches an issue the agent just created by identifier and renders its chip', async () => {
      api.fetchIssueRecord.mockResolvedValue(makeIssue({ id: 'issue-25', identifier: 'TST-25', title: 'Fresh issue' }))
      renderAnswer('Created TST-25 for you', fixture())
      const chip = await chipFor('issue')
      expect(chip).toHaveTextContent('TST-25 Fresh issue')
      expect(api.fetchIssueRecord).toHaveBeenCalledWith('TST-25', undefined, 'workspace')
    })

    it('fetches a project created this session by slug', async () => {
      api.listProjectRecords.mockResolvedValue({ items: [{ ...baseProject, id: 'project-9', slugId: 'brand-new', name: 'Brand new' }], hasMore: false, total: 1 })
      renderAnswer('[Brand new](/workspace/project/brand-new/overview)', fixture())
      expect(await chipFor('project')).toHaveTextContent('Brand new')
    })

    it('keeps identifiers that are not team keys plain without a request, and unfetchable ones plain after a failed fetch', async () => {
      api.fetchIssueRecord.mockRejectedValue(new Error('not found'))
      renderAnswer('DEV-404 and UTF-8 and TST-404', fixture())
      await waitFor(() => expect(api.fetchIssueRecord).toHaveBeenCalledTimes(1))
      expect(api.fetchIssueRecord).toHaveBeenCalledWith('TST-404', undefined, 'workspace')
      await waitFor(() => expect(screen.getByRole('document', { name: 'AI message' })).toHaveTextContent('DEV-404 and UTF-8 and TST-404'))
      expect(document.querySelector('a[data-agent-entity]')).toBeNull()
    })
  })

  describe('paged workspaces (issues and projects are not held by the client)', () => {
    const paged = () => fixture({ issueCollectionPaged: true, issues: [], projects: [] })

    it('fetches an issue by identifier once, shares it between chips, and shows its card', async () => {
      api.fetchIssueRecord.mockResolvedValue(makeIssue({ id: 'issue-9', identifier: 'TST-9', title: 'Remote issue' }))
      renderAnswer('First TST-9 then [again](/workspace/issue/TST-9/x)', paged())
      await waitFor(() => expect(document.querySelectorAll('a[data-agent-entity="issue"]')).toHaveLength(2))
      expect(api.fetchIssueRecord).toHaveBeenCalledTimes(1)
      expect(api.fetchIssueRecord).toHaveBeenCalledWith('TST-9', undefined, 'workspace')
      const chip = document.querySelector('a[data-agent-entity="issue"]') as HTMLElement
      expect(chip).toHaveTextContent('TST-9 Remote issue')
      await act(async () => { chip.focus() })
      await waitFor(() => expect(document.querySelector('[data-agent-entity-card="issue"]')).toHaveTextContent('Remote issue'))
    })

    it('fetches a project missing from workspace data by slug', async () => {
      api.listProjectRecords.mockResolvedValue({ items: [milestoneProject], hasMore: false, total: 1 })
      renderAnswer('[Whatever](/workspace/project/project-one/overview)', paged())
      const chip = await chipFor('project')
      expect(chip).toHaveTextContent('Project one')
      expect(api.listProjectRecords).toHaveBeenCalledWith(expect.objectContaining({ filter: [{ field: 'project', operator: 'is', values: ['project-one'] }], limit: 1 }), undefined, 'workspace')
    })

    it('falls back to the plain label when the record cannot be fetched', async () => {
      api.fetchIssueRecord.mockRejectedValue(new Error('not found'))
      renderAnswer('See TST-404 now', paged())
      await waitFor(() => expect(api.fetchIssueRecord).toHaveBeenCalled())
      await waitFor(() => expect(screen.getByRole('document', { name: 'AI message' })).toHaveTextContent('See TST-404 now'))
      expect(document.querySelector('a[data-agent-entity]')).toBeNull()
    })

    it('does not fetch when the workspace data already holds the issue', async () => {
      renderAnswer('TST-1', fixture({ issueCollectionPaged: true }))
      await chipFor('issue')
      expect(api.fetchIssueRecord).not.toHaveBeenCalled()
    })

    it('lists fetched issues in the referenced rows too', async () => {
      api.fetchIssueRecord.mockImplementation(async (id: string) => makeIssue({ id: `issue-${id}`, identifier: id, title: `Title ${id}` }) as Issue)
      renderAnswer('TST-5 and TST-6', paged())
      const rows = await screen.findByRole('list', { name: 'Referenced issues' })
      await waitFor(() => expect(within(rows).getAllByRole('link')).toHaveLength(2))
      expect(within(rows).getAllByRole('link')[1]).toHaveTextContent('Title TST-6')
    })
  })
})
