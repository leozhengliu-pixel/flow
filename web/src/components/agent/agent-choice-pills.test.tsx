import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { LOCALE_STORAGE_KEY } from '@/i18n/locale'
import { recommendedChoiceValue } from '@/lib/application-agents'
import { AgentChoicePills } from './agent-answer'

const choices = [{ label: 'Web (acme/web)', value: 'env-web' }, { label: 'API (acme/api)', value: 'env-api' }]

function renderPills(props: Partial<Parameters<typeof AgentChoicePills>[0]> = {}) {
  const onSelect = vi.fn()
  render(<I18nProvider><AgentChoicePills choices={choices} onSelect={onSelect} {...props}/></I18nProvider>)
  return { onSelect }
}

describe('recommendedChoiceValue', () => {
  it.each([
    ['metadata.selected naming an option', { selected: 'b', options: [{ value: 'a' }, { value: 'b' }] }, 'b'],
    ['the option marked recommended', { options: [{ value: 'a' }, { value: 'b', recommended: true }] }, 'b'],
    ['selected over recommended', { selected: 'a', options: [{ value: 'a' }, { value: 'b', recommended: true }] }, 'a'],
    ['a selected value that is not an option', { selected: 'gone', options: [{ value: 'a' }] }, undefined],
    ['nothing recommended', { options: [{ value: 'a' }, { value: 'b' }] }, undefined],
    ['no metadata', undefined, undefined],
  ])('returns %s', (_, metadata, expected) => {
    expect(recommendedChoiceValue(metadata)).toBe(expected)
  })
})

describe('AgentChoicePills recommended option', () => {
  it('preselects, tags and focuses the recommended option; Enter confirms it', async () => {
    const user = userEvent.setup()
    const { onSelect } = renderPills({ recommended: 'env-api' })
    const recommended = screen.getByRole('button', { name: /API \(acme\/api\)/ })
    expect(recommended).toHaveAttribute('data-recommended')
    expect(recommended).toHaveTextContent('Recommended')
    expect(recommended).toHaveFocus()
    expect(screen.getByRole('button', { name: 'Web (acme/web)' })).not.toHaveAttribute('data-recommended')
    expect(screen.getAllByText('Recommended')).toHaveLength(1)
    await user.keyboard('{Enter}')
    expect(onSelect).toHaveBeenCalledWith(choices[1])
  })

  it('keeps the original order and lets the viewer pick another option', async () => {
    const user = userEvent.setup()
    const { onSelect } = renderPills({ recommended: 'env-api' })
    expect(screen.getAllByRole('button').map(button => button.textContent)).toEqual(['Web (acme/web)', 'API (acme/api)Recommended'])
    await user.click(screen.getByRole('button', { name: 'Web (acme/web)' }))
    expect(onSelect).toHaveBeenCalledWith(choices[0])
  })

  it('does not steal focus from a field the viewer is typing in', () => {
    const input = document.createElement('input')
    document.body.append(input)
    input.focus()
    renderPills({ recommended: 'env-api' })
    expect(input).toHaveFocus()
    input.remove()
  })

  it('tags but does not focus the recommended option while disabled', () => {
    renderPills({ recommended: 'env-api', disabled: true })
    expect(screen.getByRole('button', { name: /API/ })).toHaveTextContent('Recommended')
    expect(document.body).toHaveFocus()
  })

  it('collapses into the answer without the tag once answered', () => {
    renderPills({ recommended: 'env-api', selected: 'env-web' })
    expect(screen.getAllByRole('button')).toHaveLength(1)
    expect(screen.queryByText('Recommended')).toBeNull()
  })

  it('behaves as before when nothing is recommended', () => {
    renderPills({ recommended: 'missing' })
    expect(screen.queryByText('Recommended')).toBeNull()
    expect(screen.getAllByRole('button').some(button => button.hasAttribute('data-recommended'))).toBe(false)
    expect(document.body).toHaveFocus()
  })

  it('shows the tag in Chinese', async () => {
    localStorage.setItem(LOCALE_STORAGE_KEY, 'zh-CN')
    try {
      renderPills({ recommended: 'env-api' })
      expect(await screen.findByText('推荐')).toBeInTheDocument()
    } finally { localStorage.removeItem(LOCALE_STORAGE_KEY) }
  })
})
