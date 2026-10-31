import { fireEvent, render, screen, within } from '@testing-library/react'
import { beforeEach, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { ProjectsPageSurface } from './projects-page-surface'

beforeEach(() => localStorage.setItem('flow:locale', 'en-US'))

it('keeps the Projects toolbar actions wired with separate phone sidebar glyphs', () => {
  const create = vi.fn(), addView = vi.fn(), openSidebar = vi.fn(), toggleSidebar = vi.fn()
  const { container } = render(<I18nProvider><ProjectsPageSurface activeViewId="all" views={[{ id: 'all', label: 'All projects', kind: 'all' }]} onCreateProject={create} onAddView={addView} onOpenAppSidebar={openSidebar} onToggleSidebar={toggleSidebar}/></I18nProvider>)
  fireEvent.click(screen.getByRole('button', { name: 'New project' }))
  fireEvent.click(screen.getByRole('button', { name: 'Add new view' }))
  fireEvent.click(screen.getByRole('button', { name: 'Open workspace sidebar' }))
  fireEvent.click(screen.getByRole('button', { name: 'Close sidebar' }))
  for (const action of [create, addView, openSidebar, toggleSidebar]) expect(action).toHaveBeenCalledOnce()
  expect(container.querySelector('[data-sidebar-trigger] path')).toHaveAttribute('d', expect.stringContaining('M4.25 2C2.45508'))
  expect(container.querySelector('.lp-projects__phone-sidebar-icon rect')).toHaveAttribute('x', '10')
  expect(container.querySelector('.lp-projects__desktop-sidebar-icon')).not.toBeNull()
})

it('keeps filter options driven by workspace data', () => {
  const onAddFilter = vi.fn()
  render(<I18nProvider><ProjectsPageSurface activeViewId="all" views={[{ id: 'all', label: 'All projects', kind: 'all' }]} filterOptions={{ Status: [{ id: 'custom', label: 'Custom workflow' }] }} onAddFilter={onAddFilter}/></I18nProvider>)
  fireEvent.click(screen.getByRole('button', { name: 'Add filter' }))
  fireEvent.click(screen.getByRole('option', { name: 'Status' }))
  const values = screen.getByRole('dialog', { name: 'Status filters' })
  fireEvent.click(within(values).getByRole('option', { name: 'Custom workflow' }))
  expect(onAddFilter).toHaveBeenCalledWith('Status', { id: 'custom', label: 'Custom workflow' })
  expect(screen.queryByRole('dialog', { name: 'Project filters' })).not.toBeInTheDocument()
})
