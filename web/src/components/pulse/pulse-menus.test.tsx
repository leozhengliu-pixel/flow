import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import { PulseNewViewEditor, PulseSubscriptionMenu } from './pulse-menus'
import type { PulseViewDraft } from './pulse-schedule'

describe('Pulse header menus', () => {
  it('exposes the measured subscription listbox and filters options from the hidden search', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<I18nProvider><PulseSubscriptionMenu cadence="never" onChange={onChange}/></I18nProvider>)

    await user.click(screen.getByRole('button', { name: 'Subscription' }))
    expect(screen.getByRole('dialog')).toBeVisible()
    expect(screen.getByText('Inbox notifications for Pulse summaries')).toBeVisible()
    expect(screen.getAllByRole('option').map(option => option.textContent)).toEqual(['Daily', 'Weekly', 'Never'])
    expect(screen.getByRole('option', { name: 'Never' })).toHaveAttribute('aria-checked', 'true')

    await user.type(screen.getByRole('searchbox', { name: 'Filter…' }), 'daily')
    expect(screen.getAllByRole('option')).toHaveLength(1)
    await user.click(screen.getByRole('option', { name: 'Daily' }))
    expect(onChange).toHaveBeenCalledWith('daily')
  })

  it('keeps view identity, actions, and filters in the expanded editor', async () => {
    const user = userEvent.setup()
    const onCancel = vi.fn()
    const onSave = vi.fn()
    const onChange = vi.fn()
    const draft: PulseViewDraft = { name: '', icon: 'CustomView', color: '#8a8f98', filters: [], match: 'all' }
    render(<I18nProvider><PulseNewViewEditor data={makeBootstrap()} draft={draft} onCancel={onCancel} onChange={onChange} onSave={onSave}/></I18nProvider>)

    expect(screen.getByRole('button', { name: 'Choose icon' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Add filter' })).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(onSave).toHaveBeenCalledOnce()
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(onCancel).toHaveBeenCalledOnce()
  })
})
