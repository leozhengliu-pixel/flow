import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { SavedViewEditor, type SavedViewTarget } from './saved-view-editor'

const targets: SavedViewTarget[] = [{ scope: 'personal', label: 'Personal' }, { scope: 'workspace', label: 'Workspace' }, { scope: 'team', label: 'Personal', teamId: 'team-1', team: { icon: 'Team', color: '#777777' } }]
beforeEach(() => localStorage.setItem('flow:locale', 'en-US'))
it.each(['en-US', 'zh-CN'])('uses one language for system targets and preserves entity names in %s', async locale => {
  localStorage.setItem('flow:locale', locale)
  const user = userEvent.setup(), save = vi.fn(), chinese = locale === 'zh-CN'
  render(<I18nProvider><SavedViewEditor initialTarget={targets[1]} saveTargets={targets} onCancel={vi.fn()} onSave={save}/></I18nProvider>)
  await user.click(screen.getByRole('button', { name: chinese ? '保存到 工作区' : 'Save to Workspace' }))
  expect(screen.getByRole('menuitemradio', { name: chinese ? '工作区' : 'Workspace' })).toHaveAttribute('aria-checked', 'true')
  await user.click(screen.getAllByRole('menuitemradio', { name: chinese ? '个人' : 'Personal' })[0])
  expect(screen.getByRole('button', { name: chinese ? '保存到 个人' : 'Save to Personal' })).toBeVisible()
  await user.click(screen.getByRole('button', { name: chinese ? '保存到 个人' : 'Save to Personal' }))
  await user.click(screen.getAllByRole('menuitemradio', { name: 'Personal' }).at(-1)!)
  expect(screen.getByRole('button', { name: chinese ? '保存到 Personal' : 'Save to Personal' })).toBeVisible()
  await user.click(screen.getByRole('button', { name: chinese ? '保存' : 'Save', exact: true }))
  expect(save).toHaveBeenCalledWith(expect.any(String), '', targets[2], expect.any(Object))
})

it('supports keyboard choice, Escape focus return and disables changes while saving', async () => {
  const user = userEvent.setup(), save = vi.fn()
  const page = (saving: boolean) => <I18nProvider><SavedViewEditor saving={saving} initialTarget={targets[0]} saveTargets={targets} onCancel={vi.fn()} onSave={save}/></I18nProvider>
  const { rerender } = render(page(false))
  const trigger = screen.getByRole('button', { name: 'Save to Personal' })
  trigger.focus()
  await user.keyboard('{Enter}{ArrowDown}{Enter}')
  expect(screen.getByRole('button', { name: 'Save to Workspace' })).toHaveFocus()
  await user.keyboard('{Enter}{Escape}')
  await waitFor(() => expect(screen.queryByRole('menu')).not.toBeInTheDocument())
  expect(screen.getByRole('button', { name: 'Save to Workspace' })).toHaveFocus()
  rerender(page(true))
  expect(screen.getByRole('button', { name: 'Save to Workspace' })).toBeDisabled()
  await user.click(screen.getByRole('textbox', { name: 'View name' }))
  await user.keyboard('{Enter}')
  expect(save).not.toHaveBeenCalled()
})

it('does not translate a real workspace named Workspace', async () => {
  localStorage.setItem('flow:locale', 'zh-CN')
  const target: SavedViewTarget = { scope: 'workspace', label: 'Workspace', labelIsEntityName: true }
  render(<I18nProvider><SavedViewEditor initialTarget={target} saveTargets={[target]} onCancel={vi.fn()} onSave={vi.fn()}/></I18nProvider>)
  await userEvent.setup().click(screen.getByRole('button', { name: '保存到 Workspace' }))
  expect(screen.getByRole('menuitemradio', { name: 'Workspace' })).toBeVisible()
})
