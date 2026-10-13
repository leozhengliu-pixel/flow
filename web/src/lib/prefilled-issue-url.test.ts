import { describe, expect, it } from 'vitest'
import type { Issue } from '@/types/flow'
import { buildPrefilledIssueURL, parsePrefilledIssueParams, parsePriority, prefilledIssueContext, resolvePrefilledIssue } from './prefilled-issue-url'
import { parseAppRoute } from './app-routes'

type Data = Parameters<typeof resolvePrefilledIssue>[1]

const viewer = { id: 'u-me', name: 'Mia Me', displayName: 'mia', username: 'mia', email: 'mia@acme.test', active: true, emailVerified: true }
const john = { id: 'u-john', name: 'John Doe', displayName: 'john', username: 'jdoe', email: 'john@acme.test', active: true, emailVerified: true }
const gone = { id: 'u-gone', name: 'Gone Away', displayName: 'gone', username: 'gone', email: 'gone@acme.test', active: false, emailVerified: true }
const eng = { id: 't-eng', key: 'ENG', name: 'Engineering', color: '#111' }
const des = { id: 't-des', key: 'DES', name: 'Design', color: '#222' }
const gone2 = { id: 't-old', key: 'OLD', name: 'Old', color: '#333', archivedAt: '2024-01-01' }

const data = {
  workspace: { id: 'w1', name: 'Acme', urlKey: 'acme' },
  viewer,
  users: [viewer, john, gone],
  teams: [eng, des, gone2],
  states: [
    { id: 's-eng-todo', teamId: 't-eng', name: 'Todo', type: 'unstarted', color: '#fff', position: 1 },
    { id: 's-eng-prog', teamId: 't-eng', name: 'In Progress', type: 'started', color: '#fff', position: 2 },
    { id: 's-des-todo', teamId: 't-des', name: 'Todo', type: 'unstarted', color: '#fff', position: 1 },
    { id: 's-des-review', teamId: 't-des', name: 'In Review', type: 'started', color: '#fff', position: 2 },
  ],
  labels: [
    { id: 'l-bug', name: 'Bug', color: '#f00' },
    { id: 'l-android', name: 'Android', color: '#0f0', scope: 't-eng' },
    { id: 'l-comments', name: 'Comments', color: '#00f', scope: 'Workspace' },
    { id: 'l-design-only', name: 'Pixels', color: '#00f', scope: 't-des' },
    { id: 'l-p1', name: 'P1', color: '#00f', groupId: 'g-prio' },
    { id: 'l-p2', name: 'P2', color: '#00f', groupId: 'g-prio' },
    { id: 'l-proj', name: 'ProjLabel', color: '#00f', resourceType: 'project' },
  ],
  labelGroups: [{ id: 'g-prio', name: 'Prio', color: '#000', resourceType: 'issue', createdAt: '' }],
  cycles: [
    { id: 'c-36', number: 36, name: 'Focus on bugs', teamId: 't-eng', status: 'current' },
    { id: 'c-37', number: 37, name: '', teamId: 't-eng', status: 'upcoming' },
    { id: 'c-d1', number: 36, name: 'Design sprint', teamId: 't-des', status: 'current' },
  ],
  projects: [
    { id: 'p-site', name: 'Project page improvements', teamIds: ['t-eng'], milestones: [{ id: 'm-beta', projectId: 'p-site', name: 'Beta' }] },
    { id: 'p-brand', name: 'Brand', teamIds: ['t-des'], milestones: [] },
    { id: 'p-old', name: 'Archived one', teamIds: ['t-eng'], milestones: [], archivedAt: '2024-01-01' },
  ],
  issues: [{ id: 'i-parent', identifier: 'ENG-1', team: eng }],
  issueTemplates: [
    { id: 'tpl-bug', name: 'Bug report', scope: 'team', teamId: 't-eng', title: 'Bug: ', body: '## Steps', priority: 2, stateId: 's-eng-prog', assigneeId: 'u-john', projectId: 'p-site', labelIds: ['l-bug'] },
    { id: 'tpl-ws', name: 'Workspace tpl', scope: 'workspace', teamId: 't-des', priority: 0, labelIds: [] },
  ],
  teamSettings: {
    't-eng': { estimateType: 'fibonacci' },
    't-des': { estimateType: 'notUsed' },
  },
} as unknown as Data

const resolve = (query: string, pathTeamKey?: string) => prefilledIssueContext(query, data, { pathTeamKey })

describe('parsePrefilledIssueParams', () => {
  it('decodes + as a space and percent escapes', () => {
    const params = parsePrefilledIssueParams('?title=My+Title&description=a%20b%2Bc+d')
    expect(params.title).toBe('My Title')
    expect(params.description).toBe('a b+c d')
  })

  it('collects label and labels, comma separated and repeated', () => {
    expect(parsePrefilledIssueParams('label=bug&labels=android,comments&label=,x,').labels).toEqual(['bug', 'x', 'android', 'comments'])
  })

  it('treats empty values as absent', () => {
    const params = parsePrefilledIssueParams('title=&status=%20&labels=')
    expect(params.title).toBeUndefined()
    expect(params.status).toBeUndefined()
    expect(params.labels).toEqual([])
  })

  it('accepts a URLSearchParams instance', () => {
    expect(parsePrefilledIssueParams(new URLSearchParams({ title: 'x' })).title).toBe('x')
  })
})

describe('parsePriority', () => {
  it('maps words and Flow numbers', () => {
    expect(parsePriority('urgent')).toBe(1)
    expect(parsePriority('HIGH')).toBe(2)
    expect(parsePriority('Medium')).toBe(3)
    expect(parsePriority('low')).toBe(4)
    expect(parsePriority('none')).toBe(0)
    expect(parsePriority('0')).toBe(0)
    expect(parsePriority('3')).toBe(3)
    expect(parsePriority('4')).toBe(4)
  })

  it('ignores unknown values', () => {
    expect(parsePriority('5')).toBeUndefined()
    expect(parsePriority('-1')).toBeUndefined()
    expect(parsePriority('critical')).toBeUndefined()
    expect(parsePriority('')).toBeUndefined()
    expect(parsePriority(undefined)).toBeUndefined()
  })
})

describe('resolvePrefilledIssue', () => {
  it('returns an empty context for an empty query', () => {
    expect(resolve('')).toEqual({})
  })

  it('sets title and description', () => {
    expect(resolve('title=My+issue&description=Line+one')).toEqual({ title: 'My issue', description: 'Line one' })
  })

  describe('team', () => {
    it('resolves by key, id and name, case-insensitively', () => {
      expect(resolve('team=eng').teamId).toBe('t-eng')
      expect(resolve('team=t-des').teamId).toBe('t-des')
      expect(resolve('team=Design').teamId).toBe('t-des')
    })

    it('lets the path team win over the query team', () => {
      expect(resolve('team=DES', 'ENG').teamId).toBe('t-eng')
      expect(resolve('', 'des').teamId).toBe('t-des')
    })

    it('falls back to the query team when the path team is unknown', () => {
      expect(resolve('team=DES', 'NOPE').teamId).toBe('t-des')
    })

    it('ignores unknown and archived teams', () => {
      expect(resolve('team=NOPE').teamId).toBeUndefined()
      expect(resolve('team=OLD').teamId).toBeUndefined()
    })
  })

  describe('status', () => {
    it('resolves by name within the chosen team', () => {
      expect(resolve('status=todo', 'ENG').stateId).toBe('s-eng-todo')
      expect(resolve('status=TODO', 'DES').stateId).toBe('s-des-todo')
      expect(resolve('status=in+review', 'DES').stateId).toBe('s-des-review')
    })

    it('resolves by id within the chosen team only', () => {
      expect(resolve('status=s-eng-prog', 'ENG').stateId).toBe('s-eng-prog')
      expect(resolve('status=s-eng-prog', 'DES').stateId).toBeUndefined()
    })

    it('uses the first team when none is given', () => {
      expect(resolve('status=In+Progress').stateId).toBe('s-eng-prog')
    })

    it('ignores unknown statuses', () => {
      expect(resolve('status=Nope', 'ENG').stateId).toBeUndefined()
    })
  })

  it('resolves priority words and numbers', () => {
    expect(resolve('priority=urgent').priority).toBe(1)
    expect(resolve('priority=2').priority).toBe(2)
    expect(resolve('priority=none').priority).toBe(0)
    expect(resolve('priority=bogus').priority).toBeUndefined()
  })

  describe('assignee', () => {
    it('resolves me, id, username, display name, full name and email', () => {
      expect(resolve('assignee=me').assigneeId).toBe('u-me')
      expect(resolve('assignee=ME').assigneeId).toBe('u-me')
      expect(resolve('assignee=u-john').assigneeId).toBe('u-john')
      expect(resolve('assignee=jdoe').assigneeId).toBe('u-john')
      expect(resolve('assignee=John').assigneeId).toBe('u-john')
      expect(resolve('assignee=John+Doe').assigneeId).toBe('u-john')
      expect(resolve('assignee=JOHN@acme.test').assigneeId).toBe('u-john')
    })

    it('ignores unknown and deactivated users', () => {
      expect(resolve('assignee=nobody').assigneeId).toBeUndefined()
      expect(resolve('assignee=gone').assigneeId).toBeUndefined()
    })
  })

  describe('estimate', () => {
    it('accepts values from the team scale', () => {
      expect(resolve('estimate=5', 'ENG').estimate).toBe(5)
    })

    it('ignores values outside the scale, non-numbers and teams without estimates', () => {
      expect(resolve('estimate=4', 'ENG').estimate).toBeUndefined()
      expect(resolve('estimate=abc', 'ENG').estimate).toBeUndefined()
      expect(resolve('estimate=-1', 'ENG').estimate).toBeUndefined()
      expect(resolve('estimate=2', 'DES').estimate).toBeUndefined()
    })
  })

  describe('cycle', () => {
    it('resolves by id, number and name within the team', () => {
      expect(resolve('cycle=c-37', 'ENG').cycleId).toBe('c-37')
      expect(resolve('cycle=36', 'ENG').cycleId).toBe('c-36')
      expect(resolve('cycle=36', 'DES').cycleId).toBe('c-d1')
      expect(resolve('cycle=focus+on+bugs', 'ENG').cycleId).toBe('c-36')
    })

    it('ignores cycles of other teams and unknown cycles', () => {
      expect(resolve('cycle=Design+sprint', 'ENG').cycleId).toBeUndefined()
      expect(resolve('cycle=99', 'ENG').cycleId).toBeUndefined()
    })
  })

  describe('labels', () => {
    it('resolves comma separated names and ids, ignoring unknown ones', () => {
      expect(resolve('labels=bug,android,comments,missing', 'ENG').labelIds).toEqual(['l-bug', 'l-android', 'l-comments'])
      expect(resolve('label=l-bug', 'ENG').labelIds).toEqual(['l-bug'])
    })

    it('only offers labels visible to the team and skips project labels', () => {
      expect(resolve('labels=android,projlabel', 'DES').labelIds).toBeUndefined()
      expect(resolve('labels=pixels', 'DES').labelIds).toEqual(['l-design-only'])
    })

    it('deduplicates and keeps one label per group', () => {
      expect(resolve('labels=bug,BUG,p1,p2', 'ENG').labelIds).toEqual(['l-bug', 'l-p1'])
    })
  })

  describe('project and milestone', () => {
    it('resolves by id or name and infers the team', () => {
      expect(resolve('project=p-site')).toEqual({ projectId: 'p-site' })
      expect(resolve('project=Brand')).toMatchObject({ projectId: 'p-brand', teamId: 't-des' })
    })

    it('is scoped to the chosen team', () => {
      expect(resolve('project=Brand', 'ENG').projectId).toBeUndefined()
      expect(resolve('project=project+page+improvements', 'ENG').projectId).toBe('p-site')
    })

    it('ignores archived and unknown projects', () => {
      expect(resolve('project=Archived+one').projectId).toBeUndefined()
      expect(resolve('project=nope').projectId).toBeUndefined()
    })

    it('resolves milestones inside the project only', () => {
      expect(resolve('project=p-site&projectMilestone=beta')).toMatchObject({ projectId: 'p-site', projectMilestoneId: 'm-beta' })
      expect(resolve('project=p-site&projectMilestone=m-beta').projectMilestoneId).toBe('m-beta')
      expect(resolve('project=p-site&projectMilestone=gamma').projectMilestoneId).toBeUndefined()
      expect(resolve('projectMilestone=beta').projectMilestoneId).toBeUndefined()
    })
  })

  describe('template', () => {
    it('applies the template values and lets explicit parameters override them', () => {
      expect(resolve('template=tpl-bug', 'ENG')).toMatchObject({
        templateId: 'tpl-bug', title: 'Bug: ', description: '## Steps', priority: 2, stateId: 's-eng-prog',
        assigneeId: 'u-john', projectId: 'p-site', labelIds: ['l-bug'],
      })
      const overridden = resolve('template=Bug+report&title=Crash&priority=low&status=todo&labels=android', 'ENG')
      expect(overridden).toMatchObject({ templateId: 'tpl-bug', title: 'Crash', priority: 4, stateId: 's-eng-todo', labelIds: ['l-bug', 'l-android'] })
    })

    it('ignores templates that belong to another team', () => {
      expect(resolve('template=tpl-bug', 'DES').templateId).toBeUndefined()
    })

    it('lets a workspace template choose the team when none is given', () => {
      expect(resolve('template=tpl-ws')).toMatchObject({ templateId: 'tpl-ws', teamId: 't-des' })
    })

    it('ignores unknown templates', () => {
      expect(resolve('template=nope')).toEqual({})
    })
  })

  it('supports parent and dueDate', () => {
    expect(resolve('parent=ENG-1')).toMatchObject({ parentId: 'i-parent', teamId: 't-eng' })
    expect(resolve('parent=i-parent').parentId).toBe('i-parent')
    expect(resolve('parent=ENG-99').parentId).toBeUndefined()
    expect(resolve('dueDate=2025-03-09').dueDate).toBe('2025-03-09')
    expect(resolve('dueDate=tomorrow').dueDate).toBeUndefined()
    expect(resolve('dueDate=2025-13-45').dueDate).toBeUndefined()
  })

  it('ignores every unknown value without throwing', () => {
    expect(resolve('foo=bar&status=%E0%A4%A&priority=!!&assignee=%20&cycle=x&labels=,,&project=%00&template=%')).toEqual({})
  })

  it('combines several parameters', () => {
    expect(resolve('title=Important+Bug&priority=high&assignee=me&labels=bug&estimate=3&status=Todo', 'ENG')).toEqual({
      teamId: 't-eng', title: 'Important Bug', priority: 2, assigneeId: 'u-me', labelIds: ['l-bug'], estimate: 3, stateId: 's-eng-todo',
    })
  })
})

describe('buildPrefilledIssueURL', () => {
  const issue = {
    id: 'i-1', identifier: 'ENG-7', title: 'Fix & "quote" it', description: 'Line one\nLine two + more', priority: 2,
    estimate: 5, dueDate: '2025-03-09T00:00:00Z', parentId: 'i-parent', cycleId: 'c-36', projectMilestoneId: 'm-beta',
    team: eng, state: data.states[1], assignee: john,
    labels: [data.labels[0], data.labels[1]], project: { id: 'p-site', name: 'Project page improvements', color: '#000' },
  } as unknown as Issue

  it('serialises into a team scoped /new URL', () => {
    const url = new URL(buildPrefilledIssueURL(issue, data, 'https://flow.test/'))
    expect(url.origin).toBe('https://flow.test')
    expect(url.pathname).toBe('/acme/team/ENG/new')
    expect(url.searchParams.get('title')).toBe('Fix & "quote" it')
    expect(url.searchParams.get('status')).toBe('In Progress')
    expect(url.searchParams.get('priority')).toBe('high')
    expect(url.searchParams.get('assignee')).toBe('u-john')
    expect(url.searchParams.get('cycle')).toBe('36')
    expect(url.search).toContain('labels=Bug,Android')
    expect(url.searchParams.get('dueDate')).toBe('2025-03-09')
  })

  it('omits empty properties', () => {
    const bare = { id: 'i-2', title: '', description: '', priority: 0, team: eng, state: data.states[0], labels: [] } as unknown as Issue
    expect(buildPrefilledIssueURL(bare, data, 'https://flow.test')).toBe('https://flow.test/acme/team/ENG/new?status=Todo')
  })

  it('round-trips through the route parser and the resolver', () => {
    const url = new URL(buildPrefilledIssueURL(issue, data, 'https://flow.test'))
    const route = parseAppRoute(url.pathname, url.search)
    expect(route).toEqual({ kind: 'new-issue', workspaceSlug: 'acme', teamKey: 'ENG' })
    const context = prefilledIssueContext(url.search, data, { pathTeamKey: route.kind === 'new-issue' ? route.teamKey : undefined })
    expect(context).toEqual({
      teamId: 't-eng', title: 'Fix & "quote" it', description: 'Line one\nLine two + more', stateId: 's-eng-prog', priority: 2,
      assigneeId: 'u-john', estimate: 5, cycleId: 'c-36', labelIds: ['l-bug', 'l-android'], projectId: 'p-site',
      projectMilestoneId: 'm-beta', parentId: 'i-parent', dueDate: '2025-03-09',
    })
  })

  it('uses ids for labels whose names are ambiguous', () => {
    const dup = { ...data, labels: [...data.labels, { id: 'l-bug2', name: 'bug', color: '#000', scope: 't-eng' }] } as Data
    const labelled = { ...issue, labels: [dup.labels[7]] } as Issue
    const url = new URL(buildPrefilledIssueURL(labelled, dup, 'https://flow.test'))
    expect(prefilledIssueContext(url.search, dup, { pathTeamKey: 'ENG' }).labelIds).toEqual(['l-bug2'])
  })
})
