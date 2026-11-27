import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { DEFAULT_PROJECTS_DISPLAY, filterProjectsByLeadTeam, projectDisplayProperties, projectLabelGroupProperty, projectsDisplayEqual, type ProjectsDisplaySettings } from './projects-display-model'
import { ProjectsDisplayMenu } from './projects-display-menu'

const LINEAR_PROPERTY_ORDER = ['ID', 'Milestones', 'Summary', 'Priority', 'Status', 'Health', 'Teams', 'Lead', 'Members', 'Dependencies', 'Start date', 'Target date', 'Issues', 'Created', 'Updated', 'Completed', 'Labels']
const ungrouped: ProjectsDisplaySettings = { ...DEFAULT_PROJECTS_DISPLAY, grouping: 'No grouping', ordering: 'Manual' }

function propertyButtons() {
  const section = screen.getByRole('heading', { name: 'Display properties' }).parentElement!
  return within(section).getAllByRole('button').map(button => button.textContent)
}

describe('ProjectsDisplayMenu', () => {
  it('matches the reference app project properties and adds a label-group column', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<ProjectsDisplayMenu labelGroups={[{ id: 'group-1', name: 'Work type' }]} onChange={onChange} settings={DEFAULT_PROJECTS_DISPLAY}/>)

    expect(screen.getByRole('button', { name: 'Initiatives' })).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Add label group…' }))
    await user.click(screen.getByRole('option', { name: 'Work type' }))
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ properties: expect.arrayContaining([projectLabelGroupProperty('group-1')]) }))
  })

  it('lists properties in the reference app order and hides feature-dependent ones when the feature is off', () => {
    const properties = projectDisplayProperties({ initiatives: false, 'customer-requests': false })
    expect(properties).toEqual(LINEAR_PROPERTY_ORDER)
    render(<ProjectsDisplayMenu onChange={vi.fn()} properties={properties} settings={DEFAULT_PROJECTS_DISPLAY}/>)
    expect(propertyButtons()).toEqual(LINEAR_PROPERTY_ORDER)
    expect(projectDisplayProperties({})).toEqual(['ID', 'Milestones', 'Summary', 'Priority', 'Status', 'Health', 'Teams', 'Initiatives', 'Lead', 'Members', 'Dependencies', 'Start date', 'Target date', 'Issues', 'Created', 'Updated', 'Completed', 'Customers', 'Customer revenue', 'Labels'])
  })

  it('turns on empty groups when switching to board layout', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<ProjectsDisplayMenu onChange={onChange} settings={DEFAULT_PROJECTS_DISPLAY}/>)
    await user.click(screen.getByRole('tab', { name: 'Board' }))
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ layout: 'board', showEmptyGroups: true }))
  })

  it('hides direction buttons, sub-grouping and empty groups while they do not apply', () => {
    const { rerender } = render(<ProjectsDisplayMenu onChange={vi.fn()} settings={ungrouped}/>)
    expect(screen.queryByRole('button', { name: 'Group ordering' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Direction' })).toBeNull()
    expect(screen.queryByText('Sub-grouping')).toBeNull()
    expect(screen.getByRole('heading', { name: 'List options' })).toBeVisible()
    expect(screen.queryByRole('checkbox', { name: 'Show empty groups' })).toBeNull()

    rerender(<ProjectsDisplayMenu onChange={vi.fn()} settings={{ ...ungrouped, grouping: 'Status', layout: 'board', ordering: 'Name' }}/>)
    expect(screen.getByRole('button', { name: 'Group ordering' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Direction' })).toBeVisible()
    // Boards name the levels Columns and Rows, and the empty-group toggle "Show empty columns" (the design reference).
    expect(screen.getByText('Columns')).toBeVisible()
    expect(screen.getByText('Rows')).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Board options' })).toBeVisible()
    expect(screen.getByRole('checkbox', { name: 'Show empty columns' })).toBeVisible()
  })

  describe('sub-grouping by label group', () => {
    const stage = { id: 'stage', name: 'Stage', heading: 'Workspace' }
    const phase = { id: 'phase', name: 'Phase', heading: 'Workspace' }
    async function subOptions(settings: ProjectsDisplaySettings, groups: Array<{ id: string; name: string; heading: string }>, name = 'Sub-grouping') {
      const user = userEvent.setup()
      render(<ProjectsDisplayMenu groupingLabelGroups={groups} onChange={vi.fn()} settings={settings}/>)
      const row = screen.getByText(name).parentElement!
      await user.click(within(row).getByRole('combobox'))
      return screen.getAllByRole('option').map(option => option.textContent)
    }
    const byLabelGroup = { ...ungrouped, grouping: 'Label group', labelGroupId: 'stage' }

    it('leaves "Label group" out of the sub-grouping when the only group is the grouping', async () => {
      const options = await subOptions(byLabelGroup, [stage])
      expect(options).not.toContain('Label group')
      expect(options).toContain('Status')
      expect(options).toContain('Label')
    })

    it('offers "Label group" in the sub-grouping when the grouping is something else, or a second group exists', async () => {
      expect(await subOptions({ ...ungrouped, grouping: 'Priority' }, [stage])).toContain('Label group')
      document.body.innerHTML = ''
      expect(await subOptions(byLabelGroup, [stage, phase])).toContain('Label group')
    })

    it('leaves what the grouping already uses out of the sub-grouping', async () => {
      const options = await subOptions({ ...ungrouped, grouping: 'Status' }, [stage])
      expect(options).not.toContain('Status')
      expect(options).not.toContain('Status type')
      expect(options).toContain('Priority')
    })

    it('defaults the second selector to the other group and swaps when the same group is picked', async () => {
      const user = userEvent.setup()
      const onChange = vi.fn()
      render(<ProjectsDisplayMenu groupingLabelGroups={[stage, phase]} onChange={onChange} settings={{ ...byLabelGroup, subGrouping: 'Label group' }}/>)
      const rows = screen.getAllByText('Label group').map(label => label.parentElement!).filter(row => row.querySelector('[role="combobox"]'))
      expect(within(rows[1]!).getByRole('combobox')).toHaveTextContent('Phase')
      await user.click(within(rows[1]!).getByRole('combobox'))
      await user.click(screen.getByRole('option', { name: 'Stage' }))
      expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ labelGroupId: 'phase', subLabelGroupId: 'stage' }))
    })
  })

  it('offers "Only show lead team projects" only on a team page', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    const { rerender } = render(<ProjectsDisplayMenu onChange={onChange} settings={DEFAULT_PROJECTS_DISPLAY}/>)
    expect(screen.queryByRole('checkbox', { name: 'Only show lead team projects' })).toBeNull()

    rerender(<ProjectsDisplayMenu onChange={onChange} settings={DEFAULT_PROJECTS_DISPLAY} teamScoped/>)
    const toggle = screen.getByRole('checkbox', { name: 'Only show lead team projects' })
    expect(toggle).not.toBeChecked()
    await user.click(toggle)
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ onlyLeadTeamProjects: true }))
  })

  it('shows Reset and Set default only once settings differ from the view default', async () => {
    const user = userEvent.setup()
    const onReset = vi.fn()
    const viewDefault = { ...DEFAULT_PROJECTS_DISPLAY, properties: [...DEFAULT_PROJECTS_DISPLAY.properties].reverse() }
    const { rerender } = render(<ProjectsDisplayMenu defaultSettings={viewDefault} onChange={vi.fn()} onReset={onReset} settings={DEFAULT_PROJECTS_DISPLAY}/>)
    expect(screen.queryByRole('button', { name: 'Reset to view default' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Save as default for view' })).toBeNull()

    rerender(<ProjectsDisplayMenu defaultSettings={viewDefault} onChange={vi.fn()} onReset={onReset} settings={{ ...DEFAULT_PROJECTS_DISPLAY, onlyLeadTeamProjects: true }}/>)
    expect(screen.getByRole('button', { name: 'Save as default for view' })).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Reset to view default' }))
    expect(onReset).toHaveBeenCalled()
  })
})

describe('projects display model', () => {
  it('compares only what the display menu controls', () => {
    expect(projectsDisplayEqual(DEFAULT_PROJECTS_DISPLAY, { ...DEFAULT_PROJECTS_DISPLAY, timelineZoom: 'year' })).toBe(true)
    expect(projectsDisplayEqual(DEFAULT_PROJECTS_DISPLAY, { ...DEFAULT_PROJECTS_DISPLAY, onlyLeadTeamProjects: undefined })).toBe(true)
    expect(projectsDisplayEqual(DEFAULT_PROJECTS_DISPLAY, { ...DEFAULT_PROJECTS_DISPLAY, properties: [...DEFAULT_PROJECTS_DISPLAY.properties, 'ID'] })).toBe(false)
    expect(projectsDisplayEqual(DEFAULT_PROJECTS_DISPLAY, { ...DEFAULT_PROJECTS_DISPLAY, ordering: 'Name' })).toBe(false)
  })

  it('keeps only lead-team projects when the team-page toggle is on', () => {
    const projects = [{ id: 'led', leadTeamId: 'team-a' }, { id: 'contributing', leadTeamId: 'team-b' }, { id: 'no-team' }]
    expect(filterProjectsByLeadTeam(projects, { onlyLeadTeamProjects: false }, 'team-a')).toBe(projects)
    expect(filterProjectsByLeadTeam(projects, { onlyLeadTeamProjects: true }, undefined)).toBe(projects)
    expect(filterProjectsByLeadTeam(projects, { onlyLeadTeamProjects: true }, 'team-a').map(project => project.id)).toEqual(['led'])
  })
})
