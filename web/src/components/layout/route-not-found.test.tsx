import { fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { RouteNotFound } from './route-not-found'

const layout = { phone: true }
vi.mock('@/hooks/use-phone-layout', () => ({ usePhoneLayout: () => layout.phone }))

afterEach(() => { layout.phone = true })

describe('RouteNotFound', () => {
  it('shows the reference wording and, on a phone, a header that opens the navigation drawer', () => {
    const onOpenSidebar = vi.fn()
    render(<I18nProvider><RouteNotFound onOpenSidebar={onOpenSidebar}/></I18nProvider>)
    expect(screen.getByText('Not found')).toBeInTheDocument()
    expect(screen.getByText('We could not find the page you were looking for')).toBeInTheDocument()
    expect(document.querySelector('.route-not-found__illustration')).not.toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Open sidebar' }))
    expect(onOpenSidebar).toHaveBeenCalledOnce()
  })

  it('keeps the desktop page free of the phone header', () => {
    layout.phone = false
    render(<I18nProvider><RouteNotFound title="Issue not found" description="This issue does not exist or is no longer available." onOpenSidebar={vi.fn()}/></I18nProvider>)
    expect(screen.getByText('Issue not found')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Open sidebar' })).toBeNull()
  })
})
