import type { Initiative, IssueLabel } from '@/types/flow'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { InitiativeLabelsPicker } from './initiative-shared'

const initiative = {
  id: 'initiative-1',
  name: 'Roadmap',
  labelIds: [],
} as unknown as Initiative

const label = {
  id: 'label-1',
  name: 'Launch',
  color: '#5e6ad2',
  resourceType: 'initiative',
  issueCount: 0,
} as unknown as IssueLabel

describe('InitiativeLabelsPicker', () => {
  it('opens the label command with a two-key shortcut and preserves selection', async () => {
    const previousLocale = localStorage.getItem('flow:locale')
    localStorage.setItem('flow:locale', 'zh-CN')
    const user = userEvent.setup()
    const onUpdate = vi.fn().mockResolvedValue(undefined)
    try {
      const { container } = render(<I18nProvider><InitiativeLabelsPicker compact initiative={initiative} labels={[label]} onUpdate={onUpdate} /></I18nProvider>)

      await user.click(screen.getByRole('button', { name: '添加标签' }))
      expect(container.querySelector('.li-label-picker--compact')).toBeInTheDocument()
      const dialog = await screen.findByRole('dialog', { name: '更改标签' })
      const search = within(dialog).getByRole('textbox')
      await waitFor(() => expect(search).toHaveAttribute('placeholder', '添加标签…'))

      const shortcut = dialog.querySelector('.property-command-search-shortcut')
      expect(shortcut).toBeInTheDocument()
      expect(shortcut?.querySelectorAll('kbd')).toHaveLength(2)
      expect(shortcut?.querySelector('kbd:first-child')).toHaveTextContent('N')
      expect(shortcut?.querySelector('kbd:last-child')).toHaveTextContent('L')
      expect(shortcut?.querySelector('span')).toHaveAttribute('data-i18n-ignore', 'true')
      expect(shortcut?.querySelector('span')).toHaveTextContent('then')

      await user.click(await within(dialog).findByRole('option', { name: 'Launch' }))
      expect(onUpdate).toHaveBeenCalledWith({ labelIds: ['label-1'] })
    } finally {
      if (previousLocale) localStorage.setItem('flow:locale', previousLocale)
      else localStorage.removeItem('flow:locale')
    }
  })

  it('creates a new label from the empty-state command', async () => {
    const previousLocale = localStorage.getItem('flow:locale')
    localStorage.setItem('flow:locale', 'zh-CN')
    const user = userEvent.setup()
    const onUpdate = vi.fn().mockResolvedValue(undefined)
    const onCreateLabel = vi.fn().mockResolvedValue({ ...label, id: 'label-new', name: 'Roadmap' })
    try {
      render(<I18nProvider><InitiativeLabelsPicker compact initiative={initiative} labels={[]} onCreateLabel={onCreateLabel} onUpdate={onUpdate} /></I18nProvider>)
      await user.click(screen.getByRole('button', { name: '添加标签' }))
      const dialog = await screen.findByRole('dialog', { name: '更改标签' })
      const search = within(dialog).getByRole('textbox')
      await user.type(search, 'Roadmap')
      const createOption = await within(dialog).findByRole('option', { name: /Create new workspace label.*Roadmap/ })
      await user.click(createOption)
      await waitFor(() => expect(onCreateLabel).toHaveBeenCalledWith('Roadmap'))
      await waitFor(() => expect(onUpdate).toHaveBeenCalledWith({ labelIds: ['label-new'] }))
    } finally {
      if (previousLocale) localStorage.setItem('flow:locale', previousLocale)
      else localStorage.removeItem('flow:locale')
    }
  })

  it('renders a disabled plus hint before a label name is entered', async () => {
    const previousLocale = localStorage.getItem('flow:locale')
    localStorage.setItem('flow:locale', 'zh-CN')
    const user = userEvent.setup()
    try {
      render(<I18nProvider><InitiativeLabelsPicker compact initiative={initiative} labels={[]} onUpdate={vi.fn()} /></I18nProvider>)
      await user.click(screen.getByRole('button', { name: '添加标签' }))
      const dialog = await screen.findByRole('dialog', { name: '更改标签' })
      const hint = within(dialog).getByRole('option', { name: 'Start typing to create a new label' })
      expect(hint).toHaveAttribute('aria-disabled', 'true')
      expect(hint).toHaveAttribute('data-i18n-ignore', 'true')
      expect(hint.querySelector('svg')).toHaveAttribute('width', '16')
      expect(hint.querySelector('svg')).toHaveAttribute('height', '16')
    } finally {
      if (previousLocale) localStorage.setItem('flow:locale', previousLocale)
      else localStorage.removeItem('flow:locale')
    }
  })
})

describe('New initiative property pills', () => {
  it('render the shared create-pill style: values strong, empty properties as placeholders', async () => {
    const { InitiativeCreateRow } = await import('./initiatives-page')
    const previousLocale = localStorage.getItem('flow:locale')
    localStorage.setItem('flow:locale', 'zh-CN')
    try {
      const viewer = { id: 'user-1', name: 'Ada', displayName: 'Ada', email: 'ada@example.com', active: true } as unknown as Parameters<typeof InitiativeCreateRow>[0]['viewer']
      const { container } = render(<I18nProvider><InitiativeCreateRow labels={[label]} teams={[]} users={[viewer]} viewer={viewer} view="active" onCancel={vi.fn()} onCreate={vi.fn()} onCreateLabel={vi.fn()}/></I18nProvider>)
      const pills = [...container.querySelectorAll('.li-create-properties > button.create-pill')]
      expect(pills).toHaveLength(6)
      expect(container.querySelector('.li-create-properties')).toHaveClass('create-pill-row')
      await waitFor(() => expect(pills.map(pill => pill.textContent)).toEqual(['进行中', '无优先级', '所有者', '主导团队', '目标日期', '标签']))
      // Status and priority are set values; owner, lead team, target date and labels are placeholders.
      expect(pills.map(pill => pill.classList.contains('is-placeholder'))).toEqual([false, false, true, true, true, true])
      // Every pill carries a 14px icon (status, priority dashes, owner, team, calendar, label tag).
      for (const pill of pills) expect(pill.querySelector('svg')).toBeInTheDocument()
      expect(container.querySelector('.li-label-picker')).not.toBeInTheDocument()
    } finally {
      if (previousLocale === null) localStorage.removeItem('flow:locale')
      else localStorage.setItem('flow:locale', previousLocale)
    }
  })

  it('shows a chosen label on the Labels pill', async () => {
    const { InitiativeLabelsPill } = await import('./initiative-shared')
    const { container } = render(<I18nProvider><InitiativeLabelsPill initiative={{ ...initiative, labelIds: [label.id] } as Initiative} labels={[label]} onUpdate={vi.fn()}/></I18nProvider>)
    const pill = container.querySelector('button.create-pill')
    expect(pill).toHaveTextContent('Launch')
    expect(pill).not.toHaveClass('is-placeholder')
    expect(pill?.querySelector('.create-pill__label-dot')).toHaveStyle({ background: '#5e6ad2' })
  })
})
