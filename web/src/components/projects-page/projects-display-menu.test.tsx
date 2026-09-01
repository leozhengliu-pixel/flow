import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { DEFAULT_PROJECTS_DISPLAY, projectLabelGroupProperty } from './projects-display-model'
import { ProjectsDisplayMenu } from './projects-display-menu'

describe('ProjectsDisplayMenu', () => {
  it('matches Linear project properties and adds a label-group column', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<ProjectsDisplayMenu labelGroups={[{ id: 'group-1', name: 'Work type' }]} onChange={onChange} settings={DEFAULT_PROJECTS_DISPLAY}/>)

    expect(screen.getByRole('button', { name: 'Initiatives' })).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Add label group…' }))
    await user.click(screen.getByRole('option', { name: 'Work type' }))
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ properties: expect.arrayContaining([projectLabelGroupProperty('group-1')]) }))
  })
})
