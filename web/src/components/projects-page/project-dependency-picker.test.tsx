import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, it } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { ProjectDependencyPicker, type ProjectDependencyValue } from './project-dependency-picker'

const projects = [
  { id: 'project-1', label: 'Project one', color: '#5e6ad2', group: 'your' as const },
  { id: 'project-2', label: 'Project two', color: '#4cb782', group: 'other' as const },
]

function Harness({ closeOnSelect = true }: { closeOnSelect?: boolean } = {}) {
  const [value, setValue] = useState<ProjectDependencyValue[]>([])
  return <ProjectDependencyPicker closeOnSelect={closeOnSelect} projects={projects} value={value} onChange={setValue}/>
}

describe('ProjectDependencyPicker', () => {
  it('uses the compact direction menu and expands the root after selecting a project', async () => {
    const user = userEvent.setup()
    render(<I18nProvider><Harness/></I18nProvider>)

    await user.click(screen.getByRole('combobox', { name: 'Add dependencies' }))
    expect(screen.getByRole('option', { name: 'Blocked by…' })).toBeVisible()
    expect(screen.getByRole('option', { name: 'Blocking…' })).toBeVisible()
    expect(screen.getByRole('dialog')).toHaveClass('project-dependency-picker__surface')
    expect(screen.getByRole('dialog')).not.toHaveClass('has-selection')

    await user.click(screen.getByRole('option', { name: 'Blocked by…' }))
    await user.click(screen.getByRole('option', { name: 'Project one' }))

    expect(screen.getByRole('combobox', { name: 'Add dependencies' })).toHaveTextContent('Project one')
    await user.click(screen.getByRole('combobox', { name: 'Add dependencies' }))
    const root = screen.getByRole('dialog')
    expect(root).toHaveClass('has-selection')
    expect(screen.getByPlaceholderText('Dependencies…')).toBeVisible()
    expect(screen.getByText('Blocked by', { selector: '.project-dependency-picker__group-label' })).toBeVisible()
  })

  it('keeps selected projects checked when a submenu stays open', async () => {
    const user = userEvent.setup()
    render(<I18nProvider><Harness closeOnSelect={false}/></I18nProvider>)

    await user.click(screen.getByRole('combobox', { name: 'Add dependencies' }))
    await user.click(screen.getByRole('option', { name: 'Blocked by…' }))
    await user.click(screen.getByRole('option', { name: 'Project one' }))

    const child = document.querySelector('.project-dependency-picker__projects')
    expect(child?.querySelector('[role="option"]')).toHaveAttribute('aria-checked', 'true')
  })
})
