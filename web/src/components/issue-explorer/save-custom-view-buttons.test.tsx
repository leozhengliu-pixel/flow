import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { I18nProvider } from '@/i18n/i18n'
import { filtersAreDirty, SaveCustomViewButtons } from './save-custom-view-buttons'

describe('SaveCustomViewButtons (LS-0540)', () => {
  it('detects dirty filters', () => {
    expect(filtersAreDirty([{ id: '1' }], [{ id: '1' }])).toBe(false)
    expect(filtersAreDirty([{ id: '1' }], [])).toBe(true)
  })

  it('offers Update view and Create new view actions', async () => {
    const user = userEvent.setup()
    const onUpdate = vi.fn()
    const onCreate = vi.fn()
    render(
      <I18nProvider>
        <SaveCustomViewButtons onUpdate={onUpdate} onCreate={onCreate} />
      </I18nProvider>,
    )
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(onUpdate).toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Save view options' }))
    await user.click(await screen.findByText('Create new view…'))
    expect(onCreate).toHaveBeenCalled()
  })
})
