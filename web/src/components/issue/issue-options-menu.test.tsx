import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import * as api from '@/lib/api'
import { makeBootstrap, makeIssue } from '@/test/fixtures'
import type { ReleasePipeline } from '@/types/flow'
import { IssueOptionsMenu, type IssueOptionsActions } from './issue-options-menu'

function setupActions(): IssueOptionsActions {
  return {addLink:vi.fn(),addCustomerRequest:vi.fn(),addDocument:vi.fn(),linkReview:vi.fn(),unlinkReview:vi.fn(),toggleRelease:vi.fn(),createRelated:vi.fn(),convert:vi.fn(),setRecurring:vi.fn(),toggleFavorite:vi.fn(),remind:vi.fn(),runLoop:vi.fn(),restoreDescription:vi.fn()}
}

function setup(releasePipelines: ReleasePipeline[] = []) {
  const actions: IssueOptionsActions = {addLink:vi.fn().mockResolvedValue(undefined),addCustomerRequest:vi.fn(),addDocument:vi.fn(),linkReview:vi.fn(),unlinkReview:vi.fn(),toggleRelease:vi.fn(),createRelated:vi.fn().mockResolvedValue(undefined),convert:vi.fn(),setRecurring:vi.fn(),configureRecurring:vi.fn(),toggleFavorite:vi.fn(),remind:vi.fn(),runLoop:vi.fn(),restoreDescription:vi.fn()}
  const issue = makeIssue()
  const onRelation = vi.fn()
  const data = makeBootstrap({customers:[],reviews:[],releases:[],releasePipelines})
  render(<I18nProvider><IssueOptionsMenu issue={issue} data={data} actions={actions} onRelation={onRelation} onUpdate={vi.fn()} onDelete={vi.fn()}/></I18nProvider>)
  return {actions,onRelation,issue}
}

describe('issue action menu', () => {
  beforeEach(() => { localStorage.clear(); vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} }) })
  afterEach(() => vi.unstubAllGlobals())

  it('offers desktop team moves, marks the current team, and omits retired targets', async () => {
    const user = userEvent.setup()
    const issue = makeIssue()
    const target = { ...issue.team, id: 'target', key: 'NEXT', name: 'Next team' }
    const retired = { ...target, id: 'retired', name: 'Retired team', retiredAt: '2026-01-01' }
    const onUpdate = vi.fn().mockResolvedValue(undefined)
    render(<I18nProvider><IssueOptionsMenu issue={issue} data={makeBootstrap({teams:[issue.team,target,retired],reviews:[]})} onRelation={vi.fn()} onUpdate={onUpdate} onDelete={vi.fn()}/></I18nProvider>)
    await user.click(screen.getByRole('button',{name:'Issue options'}))
    await user.hover(screen.getByRole('option',{name:/^Team/}))
    const menu = await screen.findByRole('dialog',{name:'Team'})
    expect(within(menu).queryByText('Retired team')).not.toBeInTheDocument()
    expect(within(menu).getByRole('option',{name:issue.team.name}).querySelector('.issue-options-detail svg')).toBeInTheDocument()
    await user.click(within(menu).getByRole('option',{name:'Next team'}))
    await waitFor(()=>expect(onUpdate).toHaveBeenCalledWith({teamId:target.id}))
  })

  it('loads a relation target missing from the paged directory before offering removal', async () => {
    const user = userEvent.setup()
    const target = makeIssue({id:'target',identifier:'TST-2',title:'Other issue'})
    const issue = makeIssue({relations:[{id:'relation',issueId:'issue-1',relatedIssueId:target.id,type:'blocks'}]})
    const fetch = vi.spyOn(api,'fetchIssueRecord').mockResolvedValue(target)
    const remove = vi.spyOn(api,'deleteRelation').mockResolvedValue(undefined)
    render(<I18nProvider><IssueOptionsMenu issue={issue} data={makeBootstrap({issues:[issue],reviews:[]})} onRelation={vi.fn()} onUpdate={vi.fn()} onDelete={vi.fn()}/></I18nProvider>)
    await user.click(screen.getByRole('button',{name:'Issue options'}))
    await user.hover(await screen.findByRole('option',{name:'Remove'}))
    await user.click(within(await screen.findByRole('dialog',{name:'Remove'})).getByRole('option',{name:/Blocked issue TST-2.*Other issue/}))
    expect(fetch).toHaveBeenCalledWith(target.id,expect.any(AbortSignal),'workspace')
    expect(remove).toHaveBeenCalledWith(issue.id,'relation')
    fetch.mockRestore(); remove.mockRestore()
  })

  it('uses the detail refresh callback when removing an owned relation', async () => {
    const user = userEvent.setup()
    const target = makeIssue({id:'target',identifier:'TST-2',title:'Other issue'})
    const issue = makeIssue({relations:[{id:'relation',issueId:'issue-1',relatedIssueId:target.id,type:'blocks'}]})
    const onDeleteRelation = vi.fn().mockResolvedValue(undefined)
    render(<I18nProvider><IssueOptionsMenu issue={issue} data={makeBootstrap({issues:[issue,target],reviews:[]})} onRelation={vi.fn()} onUpdate={vi.fn()} onDelete={vi.fn()} onDeleteRelation={onDeleteRelation}/></I18nProvider>)
    await user.click(screen.getByRole('button',{name:'Issue options'}))
    await user.hover(screen.getByRole('option',{name:'Remove'}))
    await user.click(within(await screen.findByRole('dialog',{name:'Remove'})).getByRole('option',{name:/Blocked issue TST-2.*Other issue/}))
    expect(onDeleteRelation).toHaveBeenCalledWith('relation')
  })

  it('opens a portal submenu on hover and preserves the parent search focus', async () => {
    const user = userEvent.setup()
    const {onRelation} = setup()
    await user.click(screen.getByRole('button',{name:'Issue options'}))
    const root = screen.getByRole('dialog',{name:'Issue options'})
    await waitFor(()=>expect(within(root).getByRole('combobox')).toHaveFocus())
    await user.hover(screen.getByRole('option',{name:'Mark as'}))
    const child = await screen.findByRole('dialog',{name:'Mark as'})
    expect(root.contains(child)).toBe(false)
    expect(within(root).getByRole('combobox')).toHaveFocus()
    await user.hover(within(child).getByRole('option',{name:/Related to/}))
    await user.click(within(child).getByRole('option',{name:/Related to/}))
    await waitFor(()=>expect(onRelation).toHaveBeenCalledWith('related'))
    expect(screen.queryByRole('dialog',{name:'Issue options'})).not.toBeInTheDocument()
  })

  it('switches submenus and closes them when hovering a leaf action', async () => {
    const user = userEvent.setup(); setup()
    await user.click(screen.getByRole('button',{name:'Issue options'}))
    await user.hover(screen.getByRole('option',{name:'Create related'}))
    expect(await screen.findByRole('dialog',{name:'Create related'})).toBeVisible()
    await user.hover(screen.getByRole('option',{name:'Copy'}))
    expect(await screen.findByRole('dialog',{name:'Copy'})).toBeVisible()
    expect(screen.queryByRole('dialog',{name:'Create related'})).not.toBeInTheDocument()
    await user.hover(screen.getByRole('option',{name:/Add link/}))
    expect(screen.queryByRole('dialog',{name:'Copy'})).not.toBeInTheDocument()
  })

  it('supports filtered keyboard navigation, ArrowRight, ArrowLeft and Escape', async () => {
    const user = userEvent.setup(); setup()
    await user.click(screen.getByRole('button',{name:'Issue options'}))
    const input = within(screen.getByRole('dialog',{name:'Issue options'})).getByRole('combobox')
    await user.type(input,'Mark as')
    await user.keyboard('{ArrowRight}')
    const child = await screen.findByRole('dialog',{name:'Mark as'})
    await waitFor(()=>expect(within(child).getByRole('combobox')).toHaveFocus())
    await user.keyboard('{ArrowLeft}')
    await waitFor(()=>expect(input).toHaveFocus())
    expect(screen.queryByRole('dialog',{name:'Mark as'})).not.toBeInTheDocument()
    await user.keyboard('{Enter}')
    expect(await screen.findByRole('dialog',{name:'Mark as'})).toBeVisible()
    await user.keyboard('{Escape}')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('copies complete Markdown through the copy submenu', async () => {
    const user = userEvent.setup(); const copy = vi.spyOn(navigator.clipboard,'writeText').mockResolvedValue(); const {issue} = setup()
    await user.click(screen.getByRole('button',{name:'Issue options'}))
    await user.hover(screen.getByRole('option',{name:'Copy'}))
    const child = await screen.findByRole('dialog',{name:'Copy'})
    expect(within(child).getAllByRole('option')).toHaveLength(8)
    expect(within(child).getAllByRole('option').map(option => option.querySelector('.issue-options-label')?.textContent)).toEqual(['Copy ID','Copy URL','Copy title','Copy title as link','Copy issue as Markdown','Copy everything as Markdown','Copy git branch name','Copy as prompt'])
    await user.click(within(child).getByRole('option',{name:/Copy issue as Markdown/}))
    expect(copy).toHaveBeenCalledWith(`# ${issue.title}\n\n${issue.description}\n\n`)
    await user.click(screen.getByRole('button',{name:'Issue options'}))
    await user.hover(screen.getByRole('option',{name:'Copy'}))
    await user.click(within(await screen.findByRole('dialog',{name:'Copy'})).getByRole('option',{name:/Copy everything as Markdown/}))
    const everything = copy.mock.calls.at(-1)?.[0] ?? ''
    expect(everything.startsWith(`# ${issue.title}\n\n${issue.description}\n\n## Metadata\n- URL: [`)).toBe(true)
    expect(everything).toContain(`\n- Identifier: ${issue.identifier}\n- Status: ${issue.state.name}\n- Priority: `)
  })

  it('opens the shared release picker and its pipeline submenu on hover', async () => {
    const pipeline: ReleasePipeline = { id: 'pipeline_1', slugId: 'web', name: 'Web', teamIds: [], type: 'scheduled', production: true, stages: [], stageStatuses: {}, position: 0, pathFilters: [], autoGenerateReleaseNotes: false, createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:00Z' }
    const user = userEvent.setup(); setup([pipeline])
    await user.click(screen.getByRole('button',{name:'Issue options'}))
    await user.hover(screen.getByRole('option',{name:/^Release/}))
    expect(await screen.findByRole('searchbox',{name:'Add to release…'})).toBeVisible()
    const pipelines = screen.getByRole('option',{name:'All pipelines…'})
    await user.hover(pipelines)
    expect(pipelines).toHaveAttribute('aria-expanded','true')
    expect(screen.getByRole('dialog',{name:'Issue options'})).toBeVisible()
    await user.keyboard('{Escape}')
    expect(pipelines).toHaveAttribute('aria-expanded','false')
    expect(screen.getByRole('dialog',{name:'Issue options'})).toBeVisible()
    await user.keyboard('{Escape}')
    expect(screen.queryByRole('dialog',{name:'Issue options'})).not.toBeInTheDocument()
  })

  it('labels the conversion submenu "Convert to"', async () => {
    const user = userEvent.setup(); setup()
    await user.click(screen.getByRole('button',{name:'Issue options'}))
    expect(screen.queryByRole('option',{name:'Convert into'})).not.toBeInTheDocument()
    await user.hover(screen.getByRole('option',{name:'Convert to'}))
    const child = await screen.findByRole('dialog',{name:'Convert to'})
    expect(within(child).getByRole('option',{name:'Recurring issue…'})).toBeInTheDocument()
  })

  it('opens recurrence configuration rather than a replacement submenu', async () => {
    const user = userEvent.setup(); const {actions} = setup()
    await user.click(screen.getByRole('button',{name:'Issue options'}))
    await user.hover(screen.getByRole('option',{name:'Convert to'}))
    const child = await screen.findByRole('dialog',{name:'Convert to'})
    const recurring = within(child).getByRole('option',{name:'Recurring issue…'})
    expect(recurring).not.toHaveAttribute('data-submenu')
    await user.click(recurring)
    expect(actions.configureRecurring).toHaveBeenCalledOnce()
  })

  it('edits the recurring schedule in place and can stop it', async () => {
    const user = userEvent.setup()
    const actions = {addLink:vi.fn(),addCustomerRequest:vi.fn(),addDocument:vi.fn(),linkReview:vi.fn(),unlinkReview:vi.fn(),toggleRelease:vi.fn(),createRelated:vi.fn(),convert:vi.fn(),setRecurring:vi.fn().mockResolvedValue(undefined),toggleFavorite:vi.fn(),remind:vi.fn(),runLoop:vi.fn(),restoreDescription:vi.fn()} satisfies IssueOptionsActions
    const issue = makeIssue({recurrence:'FREQ=WEEKLY;BYDAY=MO,FR',dueDate:'2099-01-02',nextOccurrenceAt:'2099-01-03T00:01:00Z'})
    render(<I18nProvider><IssueOptionsMenu issue={issue} data={makeBootstrap({customers:[],reviews:[],releases:[]})} actions={actions} onRelation={vi.fn()} onUpdate={vi.fn()} onDelete={vi.fn()}/></I18nProvider>)
    await user.click(screen.getByRole('button',{name:'Issue options'}))
    await user.hover(screen.getByRole('option',{name:'Convert to'}))
    const child = await screen.findByRole('dialog',{name:'Convert to'})
    expect(within(child).getByRole('option',{name:'Stop recurring'})).toBeInTheDocument()
    await user.click(within(child).getByRole('option',{name:'Recurring issue…'}))
    const dialog = await screen.findByRole('dialog',{name:'Recurring issue'})
    expect(within(dialog).getByText('Weekly on Mon, Fri')).toBeInTheDocument()
    expect(within(dialog).getByLabelText('First due')).toHaveValue('2099-01-02')
    await user.click(within(dialog).getByRole('button',{name:'Save'}))
    // The first due date is sent as the issue's dueDate by the action.
    expect(actions.setRecurring).toHaveBeenCalledWith('FREQ=WEEKLY;BYDAY=MO,FR','2099-01-02')
  })

  it('prefills a copy and waits for confirmation before creating it', async () => {
    const user = userEvent.setup(); const {actions,issue} = setup()
    await user.click(screen.getByRole('button',{name:'Issue options'}))
    await user.click(screen.getByRole('option',{name:'Make a copy…'}))
    const dialog = screen.getByRole('dialog',{name:`Make a copy of ${issue.identifier}`})
    expect(within(dialog).getByRole('textbox',{name:'Issue title'})).toHaveValue(issue.title)
    expect(actions.createRelated).not.toHaveBeenCalled()
    await user.click(within(dialog).getByRole('button',{name:'Create issue'}))
    expect(actions.createRelated).toHaveBeenCalledWith('copy',issue.title)
  })

  it('parses a typed reminder without executing a different preset', async () => {
    const user = userEvent.setup(); const {actions} = setup()
    await user.click(screen.getByRole('button',{name:'Issue options'}))
    await user.hover(screen.getByRole('option',{name:/^Remind me/}))
    const menu = await screen.findByRole('dialog',{name:'Remind me'})
    await user.type(within(menu).getByRole('combobox'), 'in 2 days')
    const options = within(menu).getAllByRole('option')
    expect(options).toHaveLength(2)
    await user.click(options[0])
    expect(actions.remind).toHaveBeenCalledOnce()
    const reminder = new Date(vi.mocked(actions.remind).mock.calls[0][0])
    expect(reminder.getTime()-Date.now()).toBeGreaterThan(47*60*60*1000)
    expect(reminder.getTime()-Date.now()).toBeLessThan(49*60*60*1000)
  })

  it('lists the reference items in order, with Ctrl spelled out and Find suggestions when Triage Intelligence is on', async () => {
    const user = userEvent.setup()
    const issue = makeIssue()
    const base = makeBootstrap({customers:[],reviews:[],releases:[]})
    const data = {...base, workspaceSettings:{...base.workspaceSettings, featureFlags:{...base.workspaceSettings.featureFlags, 'triage-intelligence':true}}}
    render(<I18nProvider><IssueOptionsMenu issue={issue} data={data} actions={setupActions()} onRelation={vi.fn()} onUpdate={vi.fn()} onDelete={vi.fn()}/></I18nProvider>)
    await user.click(screen.getByRole('button',{name:'Issue options'}))
    const root = screen.getByRole('dialog',{name:'Issue options'})
    const labels = within(root).getAllByRole('option').map(option => option.querySelector('.issue-options-label')?.textContent)
    expect(labels).toEqual(['Due date','Release','Add link…','Add customer request…','Add document…','Create related','Mark as','Copy','Convert to','Make a copy…','Favorite','Remind me','Unsubscribe','Find suggestions','Run loop…','Show description history','Delete'])
    const addLink = within(root).getByRole('option',{name:/Add link/})
    expect([...addLink.querySelectorAll('kbd')].map(key => key.textContent)).toEqual(['Ctrl','L'])
    expect(within(root).getByRole('option',{name:/^Unsubscribe/}).querySelector('svg[data-action-glyph=unsubscribe]')).not.toBeNull()
    expect(within(root).getByRole('option',{name:'Find suggestions'}).querySelector('svg[data-action-glyph=findSuggestions]')).not.toBeNull()
  })

  it('offers "Remove from favorites" with the filled star once favorited', async () => {
    const user = userEvent.setup()
    render(<I18nProvider><IssueOptionsMenu issue={makeIssue()} data={makeBootstrap({customers:[],reviews:[],releases:[]})} actions={setupActions()} favorited onRelation={vi.fn()} onUpdate={vi.fn()} onDelete={vi.fn()}/></I18nProvider>)
    await user.click(screen.getByRole('button',{name:'Issue options'}))
    expect(screen.queryByRole('option',{name:/^Favorite/})).toBeNull()
    expect(screen.getByRole('option',{name:/^Remove from favorites/})).toBeInTheDocument()
  })

  it('removes the parent and relations from "Remove", grouped like the reference', async () => {
    const user = userEvent.setup()
    const remove = vi.spyOn(api,'deleteRelation').mockResolvedValue(undefined as never)
    const parent = makeIssue({id:'parent',identifier:'TST-2',title:'Parent work'})
    const blocked = makeIssue({id:'blocked',identifier:'TST-3',title:'Waits on us'})
    const related = makeIssue({id:'related',identifier:'TST-4',title:'Nearby work'})
    const blocker = makeIssue({id:'blocker',identifier:'TST-5',title:'Blocks us',relations:[{id:'rel-in',issueId:'blocker',relatedIssueId:'issue-1',type:'blocks'}] as never})
    const issue = makeIssue({parentId:'parent',relations:[{id:'rel-related',issueId:'issue-1',relatedIssueId:'related',type:'related'},{id:'rel-blocks',issueId:'issue-1',relatedIssueId:'blocked',type:'blocks'}] as never})
    const onUpdate = vi.fn().mockResolvedValue(undefined)
    render(<I18nProvider><IssueOptionsMenu issue={issue} data={makeBootstrap({customers:[],reviews:[],releases:[],issues:[issue,parent,blocked,related,blocker]})} actions={setupActions()} onRelation={vi.fn()} onUpdate={onUpdate} onDelete={vi.fn()}/></I18nProvider>)
    await user.click(screen.getByRole('button',{name:'Issue options'}))
    const root = screen.getByRole('dialog',{name:'Issue options'})
    const order = within(root).getAllByRole('option').map(option => option.querySelector('.issue-options-label')?.textContent)
    expect(order.slice(5,9)).toEqual(['Create related','Mark as','Remove','Copy'])
    await user.hover(within(root).getByRole('option',{name:'Remove'}))
    const child = await screen.findByRole('dialog',{name:'Remove'})
    expect(within(child).getAllByRole('option').map(option => option.textContent)).toEqual(['Parent issue TST-2Parent work','Blocked issue TST-3Waits on us','Related issue TST-4Nearby work','Blocking issue TST-5Blocks us'])
    expect(child.querySelectorAll('[role=separator]')).toHaveLength(1)
    await user.click(within(child).getByRole('option',{name:/Blocking issue TST-5/}))
    expect(remove).toHaveBeenCalledWith('blocker','rel-in')
    await user.click(screen.getByRole('button',{name:'Issue options'}))
    await user.hover(screen.getByRole('option',{name:'Remove'}))
    await user.click(within(await screen.findByRole('dialog',{name:'Remove'})).getByRole('option',{name:/Parent issue TST-2/}))
    expect(onUpdate).toHaveBeenCalledWith({parentId:''})
  })

  it('has no Remove submenu without a parent or relations', async () => {
    const user = userEvent.setup(); setup()
    await user.click(screen.getByRole('button',{name:'Issue options'}))
    expect(screen.queryByRole('option',{name:'Remove'})).toBeNull()
  })

  it('starts Find suggestions through the open Triage Intelligence card, or the API without one', async () => {
    const user = userEvent.setup()
    const refresh = vi.spyOn(api,'refreshIssueSuggestions').mockResolvedValue([])
    const base = makeBootstrap({customers:[],reviews:[],releases:[]})
    const data = {...base, workspaceSettings:{...base.workspaceSettings, featureFlags:{...base.workspaceSettings.featureFlags, 'triage-intelligence':true}}}
    render(<I18nProvider><IssueOptionsMenu issue={makeIssue()} data={data} actions={setupActions()} onRelation={vi.fn()} onUpdate={vi.fn()} onDelete={vi.fn()}/></I18nProvider>)
    await user.click(screen.getByRole('button',{name:'Issue options'}))
    await user.click(screen.getByRole('option',{name:'Find suggestions'}))
    await waitFor(()=>expect(refresh).toHaveBeenCalledWith('issue-1'))
    const card = vi.fn((event: Event) => event.preventDefault())
    window.addEventListener('flow:find-issue-suggestions', card)
    await user.click(screen.getByRole('button',{name:'Issue options'}))
    await user.click(screen.getByRole('option',{name:'Find suggestions'}))
    expect(card).toHaveBeenCalledOnce()
    expect(refresh).toHaveBeenCalledOnce()
    window.removeEventListener('flow:find-issue-suggestions', card)
  })
})
