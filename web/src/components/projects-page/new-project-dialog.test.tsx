import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { NewProjectDialog } from './new-project-dialog'

describe('NewProjectDialog', () => {
  it('keeps a selected icon color when the project name changes', async () => {
    const user = userEvent.setup()
    render(
      <I18nProvider>
        <NewProjectDialog
          open
          onClose={vi.fn()}
          onCreate={vi.fn()}
          teams={[{ id: 'team-1', label: 'Team', color: '#5e6ad2' }]}
        />
      </I18nProvider>,
    )

    await user.click(screen.getByRole('button', { name: 'Choose icon' }))
    await user.click(screen.getByRole('button', { name: 'Color #24b4c7' }))
    await user.type(screen.getByRole('textbox', { name: 'Project name' }), 'Cyan project')

    expect(screen.getByRole('button', { name: 'Choose icon' })).toHaveStyle({
      '--view-color': '#24b4c7',
    })
  })
})
