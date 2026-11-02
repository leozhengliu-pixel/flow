import { render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { loadChineseDictionary } from '@/i18n/translate'
import { makeBootstrap } from '@/test/fixtures'
import type { FlowDocument } from '@/types/flow'
import { DocumentSubscribersPopover } from './document-subscribers-popover'

afterEach(() => localStorage.clear())

describe('document subscriber entity names', () => {
  it.each(['General', 'General Documentation 李华'])('preserves the title %s while translating the chip label', async title => {
    await loadChineseDictionary()
    localStorage.setItem('flow:locale', 'zh-CN')
    const data = makeBootstrap()
    const document = { id: 'document-1', title, subscriberIds: [] } as unknown as FlowDocument
    render(<I18nProvider><DocumentSubscribersPopover ctx={{ data, reload: vi.fn(), navigate: vi.fn(), t: source => source }} document={document} subscribed={false} busy={false} onToggleViewer={vi.fn()} open onOpenChange={vi.fn()}/></I18nProvider>)
    await waitFor(() => expect(screen.getByTestId('document-subscriber-chip')).toHaveTextContent(`文档 ⋅ ${title}`))
    expect(screen.getByTestId('document-subscriber-chip').querySelector('[data-i18n-ignore]')).toHaveTextContent(title)
  })
})
