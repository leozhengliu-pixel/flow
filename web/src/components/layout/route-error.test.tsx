import { render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

const routeError = vi.hoisted(() => ({ current: undefined as unknown }))
vi.mock('react-router-dom', () => ({ useRouteError: () => routeError.current }))

import { I18nProvider } from '@/i18n/i18n'
import { RouteError } from './route-error'

afterEach(() => { vi.unstubAllGlobals(); sessionStorage.clear() })

describe('route error', () => {
  it('shows the can\'t-connect state for a file of the app that could not load', async () => {
    // A reload already happened (the stale-chunk guard), and the server is still unreachable.
    sessionStorage.setItem('flow:stale-chunk-reload-at', String(Date.now()))
    const fetch = vi.fn(async () => { throw new TypeError('Failed to fetch') })
    vi.stubGlobal('fetch', fetch)
    routeError.current = new Error('Unable to preload CSS for /assets/auth-page-DJhyg043.css')
    render(<I18nProvider><RouteError /></I18nProvider>)
    expect(await screen.findByText("Can't connect to Flow")).toBeInTheDocument()
    expect(screen.getByText('Part of Flow could not be loaded.')).toBeInTheDocument()
    // It watches for the server to come back.
    await vi.waitFor(() => expect(fetch).toHaveBeenCalledWith('/api/health', expect.anything()))
  })

  it('shows a plain error state for any other failure', () => {
    const fetch = vi.fn()
    vi.stubGlobal('fetch', fetch)
    routeError.current = new Error('boom')
    render(<I18nProvider><RouteError /></I18nProvider>)
    expect(screen.getByText('Something went wrong.')).toBeInTheDocument()
    expect(fetch).not.toHaveBeenCalled()
  })
})
