import { Suspense } from 'react'
import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { lazyPage } from './lazy-page'

describe('lazyPage preloading', () => {
  it('shares one request across preloads and rendering', async () => {
    const loader = vi.fn(async () => ({ Page: ({ title }: { title: string }) => <h1>{title}</h1> }))
    const Page = lazyPage(loader, 'Page')
    await Promise.all([Page.preload(), Page.preload()])
    render(<Suspense fallback="Loading"><Page title="Detail"/></Suspense>)
    expect(await screen.findByRole('heading', { name: 'Detail' })).toBeInTheDocument()
    expect(loader).toHaveBeenCalledTimes(1)
  })

  it('allows navigation to retry after a failed speculative load', async () => {
    const loader = vi.fn<() => Promise<{ Page: () => React.ReactElement }>>().mockRejectedValueOnce(new Error('offline')).mockResolvedValue({ Page: () => <h1>Recovered</h1> })
    const Page = lazyPage(loader, 'Page')
    await expect(Page.preload()).rejects.toThrow('offline')
    render(<Suspense fallback="Loading"><Page/></Suspense>)
    expect(await screen.findByRole('heading', { name: 'Recovered' })).toBeInTheDocument()
    expect(loader).toHaveBeenCalledTimes(2)
  })
})
