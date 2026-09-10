import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { viewer } from '@/test/fixtures'
import type { Initiative } from '@/types/flow'
import { InitiativeHierarchySection } from './initiative-hierarchy-section'

const parent = { id: 'parent', name: 'Parent', status: 'active', color: '#ff0000', leadTeamId: 'team', parentInitiativeIds: [], projectIds: [] } as unknown as Initiative
const second = { ...parent, id: 'second', name: 'Second parent' }
const child = { ...parent, id: 'child', name: 'Child', parentInitiativeIds: ['parent', 'second'], projectIds: ['project'] }
it('removes only the selected parent edge and excludes descendants from parent candidates', async () => {
  localStorage.setItem('flow:locale', 'en-US')
  const user = userEvent.setup(), update = vi.fn()
  function Harness() {
    const [items, setItems] = useState([parent, second, child])
    return <InitiativeHierarchySection initiative={items[0]} initiatives={items} teams={[]} users={[viewer]} labels={[]} viewer={viewer} onOpen={vi.fn()} onCreate={vi.fn()} onCreateLabel={vi.fn()} onUpdate={async (id, input) => { update(id, input); const next = { ...items.find(item => item.id === id)!, ...input } as Initiative; setItems(current => current.map(item => item.id === id ? next : item)); return next }}/>
  }
  render(<I18nProvider><Harness/></I18nProvider>)
  await user.click(screen.getByRole('combobox', { name: 'Change parent initiatives' }))
  expect(screen.queryByRole('option', { name: 'Child' })).not.toBeInTheDocument()
  await user.keyboard('{Escape}')
  await user.click(screen.getByRole('button', { name: 'Remove sub-initiative: Child' }))
  await waitFor(() => expect(update).toHaveBeenCalledWith('child', { parentInitiativeIds: ['second'] }))
  expect(screen.getByText('No sub-initiatives')).toBeInTheDocument()
})

it('creates a child with the parent link and inherited lead team through the existing create controls', async () => {
  localStorage.setItem('flow:locale', 'en-US')
  const user = userEvent.setup(), create = vi.fn().mockResolvedValue(child)
  render(<I18nProvider><InitiativeHierarchySection initiative={parent} initiatives={[parent]} teams={[{ id: 'team', name: 'Team', key: 'T', color: '#ff0000' }]} users={[viewer]} labels={[]} viewer={viewer} onOpen={vi.fn()} onCreate={create} onCreateLabel={vi.fn()} onUpdate={vi.fn()}/></I18nProvider>)
  await user.click(screen.getByRole('button', { name: 'New sub-initiative' }))
  await user.type(screen.getByRole('textbox', { name: 'Initiative name' }), 'New child')
  await user.click(screen.getByRole('button', { name: 'Create' }))
  await waitFor(() => expect(create).toHaveBeenCalledWith(expect.objectContaining({ name: 'New child', leadTeamId: 'team', parentInitiativeIds: ['parent'] })))
})
