import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { viewer } from '@/test/fixtures'
import type { Initiative } from '@/types/flow'
import { InitiativeContributesTo, InitiativeSubInitiatives, type SubInitiativesProps } from './initiative-hierarchy-section'

const base = {
  slugId: '', summary: '', description: '', icon: 'Initiative', color: '#d6a526', status: 'active', priority: 0, priorityLabel: 'No priority', health: 'noUpdate',
  creator: viewer, contributingTeamIds: [], labelIds: [], projectIds: [], resources: [], comments: [], favorite: false, subscribed: false, parentInitiativeIds: [], createdAt: '', updatedAt: '',
} as unknown as Initiative
const item = (id: string, parents: string[] = [], extra: Partial<Initiative> = {}) => ({ ...base, id, slugId: id, name: id[0].toUpperCase() + id.slice(1), parentInitiativeIds: parents, ...extra }) as Initiative

beforeEach(() => localStorage.setItem('flow:locale', 'en-US'))

function renderSection(initiatives: Initiative[], props: Partial<SubInitiativesProps> = {}) {
  const all: SubInitiativesProps = {
    initiative: initiatives[0], initiatives, projects: [], projectUpdates: {}, users: [viewer], teams: [], labels: [], viewer,
    onCreatingParentIdChange: vi.fn(), onOpen: vi.fn(), onCreate: vi.fn().mockResolvedValue(undefined), onCreateLabel: vi.fn(), onCreateReminder: vi.fn(), onDelete: vi.fn(), onUpdate: vi.fn().mockResolvedValue(undefined),
    ...props,
  }
  return { ...render(<I18nProvider><InitiativeSubInitiatives {...all}/></I18nProvider>), props: all }
}

describe('Sub-initiatives overview section (Linear Enterprise)', () => {
  it('renders nothing while the initiative has no sub-initiatives and nothing is being created', () => {
    const { container } = renderSection([item('root'), item('other')])
    expect(container).toBeEmptyDOMElement()
    expect(screen.queryByText('Sub-initiatives')).not.toBeInTheDocument()
  })

  it('lists every descendant grouped by status and nested under its parent', () => {
    renderSection([item('root'), item('child', ['root']), item('grandchild', ['child']), item('done', ['root'], { status: 'completed' }), item('unrelated')])
    const section = screen.getByRole('region', { name: 'Sub-initiatives' })
    const groups = within(section).getAllByRole('button', { expanded: true }).filter(button => button.classList.contains('li-subinitiatives__group'))
    expect(groups.map(group => group.textContent)).toEqual(['Active2', 'Completed1'])
    const rows = [...section.querySelectorAll<HTMLElement>('.li-row')]
    expect(rows.map(row => row.querySelector('strong')?.textContent)).toEqual(['Child', 'Grandchild', 'Done'])
    expect(rows[1].querySelector<HTMLElement>('.li-name')?.style.paddingInlineStart).toBe('18px')
    expect(within(section).queryByText('Unrelated')).not.toBeInTheDocument()
  })

  it('collapses a status group', async () => {
    const user = userEvent.setup()
    renderSection([item('root'), item('child', ['root'])])
    await user.click(screen.getByRole('button', { name: /Active/, expanded: true }))
    expect(screen.queryByText('Child')).not.toBeInTheDocument()
  })

  it('offers Create sub-initiative and Add existing sub-initiative from the "+" menu', async () => {
    const user = userEvent.setup()
    const { props } = renderSection([item('root'), item('child', ['root']), item('loose')])
    await user.click(screen.getByRole('button', { name: 'Add new sub-initiative' }))
    const menu = screen.getByRole('menu', { name: 'Add new sub-initiative' })
    expect([...menu.querySelectorAll<HTMLElement>('[data-menu-item]')].map(row => row.dataset.menuItem)).toEqual(['Create sub-initiative', 'Add existing sub-initiative'])
    await user.click(within(menu).getByRole('menuitem', { name: /Create sub-initiative/ }))
    expect(props.onCreatingParentIdChange).toHaveBeenCalledWith('root')
  })

  it('adds an existing initiative as a sub-initiative, keeping its other parents', async () => {
    const user = userEvent.setup()
    const { props } = renderSection([item('root'), item('child', ['root']), item('loose', ['other']), item('other')])
    await user.click(screen.getByRole('button', { name: 'Add new sub-initiative' }))
    await user.click(screen.getByRole('menuitem', { name: /Add existing sub-initiative/ }))
    const picker = await screen.findByRole('menu', { name: 'Add existing sub-initiative' })
    expect(within(picker).queryByRole('menuitemcheckbox', { name: /Root/ })).not.toBeInTheDocument()
    await user.click(within(picker).getByRole('menuitemcheckbox', { name: /Loose/ }))
    expect(props.onUpdate).toHaveBeenCalledWith('loose', { parentInitiativeIds: ['other', 'root'] })
  })

  it('shows the inline create row while creating and links the new initiative to its parent', async () => {
    const user = userEvent.setup()
    const { props } = renderSection([item('root', [], { leadTeamId: 'team' })], { creatingParentId: 'root' })
    expect(document.querySelector('[aria-label="Add new sub-initiative"]')).toHaveStyle({ visibility: 'hidden' })
    await user.type(screen.getByRole('textbox', { name: 'Initiative name' }), 'New child')
    await user.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(props.onCreate).toHaveBeenCalledWith(expect.objectContaining({ name: 'New child', leadTeamId: 'team', parentInitiativeIds: ['root'] })))
    expect(props.onCreatingParentIdChange).toHaveBeenCalledWith(undefined)
  })

  it('refuses to create below the fifth level', async () => {
    const user = userEvent.setup()
    const chain = [item('l1'), item('l2', ['l1']), item('l3', ['l2']), item('l4', ['l3']), item('l5', ['l4'])]
    const { props } = renderSection([chain[4], ...chain.slice(0, 4), item('l6', ['l5'])])
    await user.click(screen.getByRole('button', { name: 'Add new sub-initiative' }))
    await user.click(screen.getByRole('menuitem', { name: /Create sub-initiative/ }))
    expect(props.onCreatingParentIdChange).not.toHaveBeenCalled()
  })
})

describe('Contributes to property row', () => {
  it('is hidden when the initiative has no parent', () => {
    const { container } = render(<I18nProvider><InitiativeContributesTo initiative={item('root')} initiatives={[item('root')]} onOpen={vi.fn()} onUpdate={vi.fn()}/></I18nProvider>)
    expect(container).toBeEmptyDOMElement()
  })

  it('shows parent chips, opens a parent and removes only that parent edge', async () => {
    const user = userEvent.setup(), open = vi.fn(), update = vi.fn().mockResolvedValue(undefined)
    const items = [item('child', ['parent', 'second']), item('parent'), item('second'), item('grandchild', ['child'])]
    render(<I18nProvider><InitiativeContributesTo initiative={items[0]} initiatives={items} onOpen={open} onUpdate={update}/></I18nProvider>)
    expect(screen.getByRole('heading', { name: 'Contributes to' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Parent' }))
    expect(open).toHaveBeenCalledWith(items[1])
    await user.click(screen.getAllByRole('button', { name: 'Menu' })[0])
    await user.click(screen.getByRole('menuitem', { name: /Remove from parent/ }))
    expect(update).toHaveBeenCalledWith('child', { parentInitiativeIds: ['second'] })
  })

  it('offers only valid parents in the "+" picker', async () => {
    const user = userEvent.setup(), update = vi.fn().mockResolvedValue(undefined)
    function Harness() {
      const [items, setItems] = useState([item('child', ['parent']), item('parent'), item('grandchild', ['child']), item('free')])
      return <InitiativeContributesTo initiative={items[0]} initiatives={items} onOpen={vi.fn()} onUpdate={async (id, input) => { update(id, input); setItems(current => current.map(entry => entry.id === id ? { ...entry, ...input } as Initiative : entry)) }}/>
    }
    render(<I18nProvider><Harness/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Add parent initiative…' }))
    const picker = screen.getByRole('menu', { name: 'Add parent initiative…' })
    expect(within(picker).queryByRole('menuitemcheckbox', { name: /Grandchild/ })).not.toBeInTheDocument()
    await user.click(within(picker).getByRole('menuitemcheckbox', { name: /Free/ }))
    expect(update).toHaveBeenCalledWith('child', { parentInitiativeIds: ['parent', 'free'] })
  })
})
