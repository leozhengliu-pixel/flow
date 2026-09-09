import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue } from '@/test/fixtures'
import { IssueOptionsMenu, type IssueOptionsActions } from './issue-options-menu'

function setup() {
  const actions: IssueOptionsActions = {addLink:vi.fn().mockResolvedValue(undefined),addCustomerRequest:vi.fn(),addDocument:vi.fn(),linkReview:vi.fn(),unlinkReview:vi.fn(),toggleRelease:vi.fn(),createRelated:vi.fn().mockResolvedValue(undefined),convert:vi.fn(),setRecurring:vi.fn(),configureRecurring:vi.fn(),toggleFavorite:vi.fn(),remind:vi.fn(),runLoop:vi.fn(),restoreDescription:vi.fn()}
  const issue = makeIssue()
  const onRelation = vi.fn()
  const data = makeBootstrap({customers:[],reviews:[],releases:[],releasePipelines:[]})
  render(<I18nProvider><IssueOptionsMenu issue={issue} data={data} actions={actions} onRelation={onRelation} onUpdate={vi.fn()} onDelete={vi.fn()}/></I18nProvider>)
  return {actions,onRelation,issue}
}

describe('issue action menu', () => {
  beforeEach(() => { localStorage.clear(); vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} }) })
  afterEach(() => vi.unstubAllGlobals())

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
    await user.click(within(child).getByRole('option',{name:/Copy content as Markdown/}))
    expect(copy).toHaveBeenCalledWith(`# ${issue.title}\n\n${issue.description}`)
  })

  it('opens the shared release picker and its pipeline submenu on hover', async () => {
    const user = userEvent.setup(); setup()
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
})
