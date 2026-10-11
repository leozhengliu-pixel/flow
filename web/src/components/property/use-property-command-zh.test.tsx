import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeAll, beforeEach, describe, expect, it } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { loadChineseDictionary } from '@/i18n/translate'
import { usePropertyCommand } from './use-property-command'

const options = [
  { id: 'urgent', label: 'Urgent' },
  { id: 'status', label: 'Change status…', keywords: 'workflow' },
  { id: 'bug', label: 'Bug' },
]

function Harness() {
  const command = usePropertyCommand({ autoFocus: false, open: true, options, onOpenChange: () => {}, onSelect: () => {} })
  return <div>
    <input aria-label="Search options" value={command.query} onChange={event => command.onQueryChange(event.target.value)}/>
    <ul>{command.filteredOptions.map(option => <li key={option.id} data-id={option.id}>{option.label}</li>)}</ul>
  </div>
}

describe('usePropertyCommand with the Chinese locale', () => {
  beforeAll(async () => { await loadChineseDictionary() })
  beforeEach(() => localStorage.setItem('flow:locale', 'zh-CN'))

  const rows = () => screen.queryAllByRole('listitem').map(item => item.getAttribute('data-id'))

  it('matches the displayed Chinese label, the English label and keywords', async () => {
    const user = userEvent.setup()
    render(<I18nProvider><Harness/></I18nProvider>)
    const search = screen.getByRole('textbox', { name: /Search options|搜索/ })
    await user.type(search, '紧急')
    expect(rows()).toEqual(['urgent'])
    await user.clear(search)
    await user.type(search, '状态')
    expect(rows()).toEqual(['status'])
    await user.clear(search)
    await user.type(search, 'workflow')
    expect(rows()).toEqual(['status'])
    await user.clear(search)
    await user.type(search, 'urgent')
    expect(rows()).toEqual(['urgent'])
    await user.clear(search)
    await user.type(search, '没有这个')
    expect(rows()).toEqual([])
  })

  it('keeps English-only behaviour in the English locale', async () => {
    localStorage.setItem('flow:locale', 'en-US')
    const user = userEvent.setup()
    render(<I18nProvider><Harness/></I18nProvider>)
    const search = screen.getByRole('textbox', { name: 'Search options' })
    await user.type(search, '紧急')
    expect(rows()).toEqual([])
    await user.clear(search)
    await user.type(search, 'urg')
    expect(rows()).toEqual(['urgent'])
  })
})
