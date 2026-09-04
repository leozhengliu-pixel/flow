import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'

import { PersonHoverPreview, type PersonPickerOption } from './core-property-pickers'

const person = (overrides: Partial<PersonPickerOption> = {}): PersonPickerOption => ({
  id: 'user-1',
  label: 'Zheng Liu',
  name: 'zheng.liu',
  email: 'zheng@example.com',
  active: true,
  ...overrides,
})

function renderPreview(overrides: Partial<PersonPickerOption> = {}) {
  return render(
    <I18nProvider>
      <PersonHoverPreview person={person(overrides)} workspaceName="Workspace" />
    </I18nProvider>,
  )
}

describe('PersonHoverPreview presence status', () => {
  it('does not infer online from an active account', () => {
    renderPreview({ active: true })

    const details = screen.getByText('Offline').closest('.assignee-hover-preview__details') as HTMLElement | null
    expect(details).not.toBeNull()
    expect(within(details!).getByText('Offline')).toBeVisible()
    expect(within(details!).queryByText('Online')).not.toBeInTheDocument()
    expect(within(details!).queryByText('Invited')).not.toBeInTheDocument()
  })

  it('shows online only when the picker option carries explicit presence', () => {
    renderPreview({ active: true, online: true })

    const details = screen.getByText('Online').closest('.assignee-hover-preview__details') as HTMLElement | null
    expect(details).not.toBeNull()
    expect(within(details!).getByText('Online')).toBeVisible()
    expect(within(details!).queryByText('Offline')).not.toBeInTheDocument()
  })

  it('keeps inactive accounts offline when a stale presence flag is supplied', () => {
    renderPreview({ active: false, online: true })

    const details = screen.getByText('Offline').closest('.assignee-hover-preview__details') as HTMLElement | null
    expect(details).not.toBeNull()
    expect(within(details!).getByText('Offline')).toBeVisible()
    expect(within(details!).queryByText('Online')).not.toBeInTheDocument()
  })

  it('gives pending invitations precedence over stale presence', () => {
    renderPreview({ active: true, online: true, invited: true, end: 'Invited' })

    const details = screen.getByText('Invited').closest('.assignee-hover-preview__details') as HTMLElement | null
    expect(details).not.toBeNull()
    expect(within(details!).getByText('Invited')).toBeVisible()
    expect(within(details!).getByText('Invitation pending')).toBeVisible()
    expect(within(details!).queryByText('Online')).not.toBeInTheDocument()
    expect(within(details!).queryByText('Offline')).not.toBeInTheDocument()
  })
})
