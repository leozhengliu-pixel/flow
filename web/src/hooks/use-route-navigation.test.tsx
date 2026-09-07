import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import { useRouteNavigation } from './use-route-navigation'
import { preloadRoute } from '@/lib/route-preload'

vi.mock('@/lib/route-preload', async importOriginal => ({
  ...await importOriginal<typeof import('@/lib/route-preload')>(),
  preloadRoute: vi.fn().mockResolvedValue(undefined),
}))

function Harness() {
  const navigate = useRouteNavigation()
  const location = useLocation()
  return <>
    <output>{location.pathname + location.search}</output>
    <a href="/acme/projects/all?status=active">Projects</a>
    <a href="/acme/issues/all" onClick={event => event.preventDefault()}>Guarded</a>
    <button onClick={() => navigate('/acme/settings/account/security')}>Settings</button>
    <button onClick={() => navigate(-1)}>Back</button>
  </>
}

describe('client navigation', () => {
  it('handles native internal links and browser history without a document reload', async () => {
    render(<MemoryRouter initialEntries={['/acme/inbox']}><Harness/></MemoryRouter>)
    fireEvent.click(screen.getByText('Projects'))
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('/acme/projects/all?status=active'))
    fireEvent.click(screen.getByText('Back'))
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('/acme/inbox'))
    fireEvent.click(screen.getByText('Settings'))
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('/acme/settings/account/security'))
  })
  it('does not override a component navigation guard', () => {
    render(<MemoryRouter initialEntries={['/acme/inbox']}><Harness/></MemoryRouter>)
    fireEvent.click(screen.getByText('Guarded'))
    expect(screen.getByRole('status')).toHaveTextContent('/acme/inbox')
  })
  it('preloads on keyboard focus before a click', async () => {
    vi.mocked(preloadRoute).mockClear()
    render(<MemoryRouter><Harness/></MemoryRouter>)
    fireEvent.focusIn(screen.getByText('Projects'))
    await waitFor(() => expect(preloadRoute).toHaveBeenCalledWith('/acme/projects/all?status=active'))
  })
})
