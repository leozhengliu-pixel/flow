import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { DirectoryDisplayMenu, DirectoryFilterMenu } from './directory-menus'
import { useTeamDirectoryControls } from './use-team-directory-controls'

describe('directory menus', () => {
  it('opens people on hover and supports employee ID search, selection and escape', async () => {
    const user = userEvent.setup(), choose = vi.fn()
    render(<DirectoryFilterMenu groups={[{ id: 'members', label: 'Members', icon: null, choices: [{ id: 'one', label: 'Same name', keywords: 'EMP001', meta: '2 teams' }, { id: 'two', label: 'Same name', keywords: 'EMP002', meta: '1 team' }] }]} selected={{}} onAdvanced={vi.fn()} onChoice={choose}/>)
    await user.click(screen.getByRole('button', { name: 'Add filter' }))
    await user.hover(screen.getByRole('menuitem', { name: 'Members' }))
    const search = await screen.findByRole('textbox', { name: 'Filter…' })
    // jsdom has no submenu geometry for Radix's pointer grace polygon.
    search.focus()
    await user.type(search, 'EMP002', { skipClick: true })
    expect(screen.getAllByRole('menuitemcheckbox')).toHaveLength(1)
    await user.keyboard('{ArrowDown}{Enter}')
    expect(choose).toHaveBeenCalledWith('members', 'two', true)
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('textbox', { name: 'Filter…' })).not.toBeInTheDocument())
  })

  it('changes ordering and display columns without closing the outer display menu', async () => {
    const user = userEvent.setup()
    function Display() {
      const [ordering, setOrdering] = useState('name'), [descending, setDescending] = useState(false), [columns, setColumns] = useState(new Set<string>())
      return <DirectoryDisplayMenu ordering={ordering} orderingOptions={[{ id: 'name', label: 'Name' }, { id: 'updated', label: 'Updated' }]} descending={descending} properties={columns} propertyOptions={[{ id: 'owners', label: 'Owners' }]} onOrdering={setOrdering} onDirection={() => setDescending(!descending)} onProperty={id => setColumns(new Set([id]))}/>
    }
    render(<Display/>)
    await user.click(screen.getByRole('button', { name: 'Display options' }))
    await user.click(screen.getByRole('button', { name: 'Choose ordering' }))
    await user.click(screen.getByRole('menuitem', { name: 'Updated' }))
    expect(screen.getByRole('button', { name: 'Choose ordering' })).toHaveTextContent('Updated')
    await user.click(screen.getByRole('button', { name: 'Sort descending' }))
    expect(screen.getByRole('button', { name: 'Sort ascending' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Owners' }))
    expect(screen.getByRole('button', { name: 'Owners' })).toHaveAttribute('aria-pressed', 'true')
  })

  it('restores shared filters and persists viewer-scoped display preferences', async () => {
    localStorage.clear()
    const user = userEvent.setup()
    function Controls() {
      const controls = useTeamDirectoryControls('workspace', 'viewer'), location = useLocation()
      return <><output data-testid="filters">{controls.filters.owners.join(',')}</output><output data-testid="sort">{controls.preferences.ordering}</output><output data-testid="url">{location.search}</output><button onClick={() => controls.setPreferences(current => ({ ...current, ordering: 'updated', descending: true }))}>Order</button><button onClick={() => controls.setFilters(current => ({ ...current, members: ['member'] }))}>Filter</button></>
    }
    const path = '/teams?keep=value&teamFilters=' + encodeURIComponent(JSON.stringify({ owners: ['owner'] }))
    const mount = () => render(<I18nProvider><MemoryRouter initialEntries={[path]}><Controls/></MemoryRouter></I18nProvider>)
    const first = mount()
    expect(screen.getByTestId('filters')).toHaveTextContent('owner')
    await user.click(screen.getByText('Order'))
    await user.click(screen.getByText('Filter'))
    expect(screen.getByTestId('url')).toHaveTextContent('keep=value')
    first.unmount()
    mount()
    expect(screen.getByTestId('sort')).toHaveTextContent('updated')
    expect(JSON.parse(localStorage.getItem('flow:team-directory:workspace:viewer')!)).toMatchObject({ ordering: 'updated', descending: true })
  })
})
