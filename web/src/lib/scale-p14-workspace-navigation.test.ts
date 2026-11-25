import { describe, expect, it, vi } from 'vitest'
import type { Locator, Page, Request } from '@playwright/test'
import { openWorkspaceWithNetworkRecovery } from '../../e2e/scale-p14-workspace-navigation'

function navigation() {
  let listener: (request: Request) => void = () => undefined
  const page = {
    goto: vi.fn(async () => null),
    reload: vi.fn(async () => null),
    on: vi.fn((_event: string, callback: typeof listener) => { listener = callback }),
    off: vi.fn(),
  }
  const ready = { waitFor: vi.fn(async (): Promise<void> => undefined) }
  return {
    page, ready,
    open: () => openWorkspaceWithNetworkRecovery(page as unknown as Page, '/workspace/reviews/created', ready as unknown as Locator),
    fail: (errorText = 'net::ERR_NETWORK_CHANGED', resourceType = 'script') => listener({ resourceType: () => resourceType, failure: () => ({ errorText }) } as Request),
  }
}

describe('P14 workspace navigation after a network change', () => {
  it('keeps an ordinary successful navigation to one request', async () => {
    const run = navigation()
    await run.open()
    expect(run.page.reload).not.toHaveBeenCalled()
    expect(run.page.off).toHaveBeenCalledWith('requestfailed', expect.any(Function))
  })

  it('recovers a module aborted during navigation', async () => {
    const run = navigation()
    run.page.goto.mockImplementationOnce(async () => { run.fail(); throw new Error('navigation interrupted') })
    await run.open()
    expect(run.page.reload).toHaveBeenCalledTimes(1)
    expect(run.ready.waitFor).toHaveBeenCalledWith({ state: 'visible' })
  })

  it('recovers a module aborted after the document loaded', async () => {
    const run = navigation()
    let finish!: () => void
    run.ready.waitFor.mockImplementationOnce(() => new Promise<void>(resolve => { finish = resolve }))
    const opened = run.open()
    await vi.waitFor(() => expect(run.ready.waitFor).toHaveBeenCalledTimes(1))
    run.fail()
    await opened
    finish()
    expect(run.page.reload).toHaveBeenCalledTimes(1)
  })

  it('does not retry unrelated navigation errors or missing controls', async () => {
    for (const stage of ['navigation', 'control']) {
      const run = navigation()
      const failure = new Error(stage)
      if (stage === 'navigation') run.page.goto.mockRejectedValueOnce(failure)
      else run.ready.waitFor.mockRejectedValueOnce(failure)
      await expect(run.open()).rejects.toBe(failure)
      expect(run.page.reload).not.toHaveBeenCalled()
    }
  })

  it('does not keep retrying a failed recovery', async () => {
    const run = navigation()
    run.page.goto.mockImplementationOnce(async () => { run.fail(); return null })
    run.page.reload.mockRejectedValueOnce(new Error('still offline'))
    await expect(run.open()).rejects.toThrow('still offline')
    expect(run.page.reload).toHaveBeenCalledTimes(1)
  })

  it('does not reload for an API request failure', async () => {
    const run = navigation()
    run.page.goto.mockImplementationOnce(async () => { run.fail('net::ERR_NETWORK_CHANGED', 'fetch'); return null })
    run.ready.waitFor.mockRejectedValueOnce(new Error('not ready'))
    await expect(run.open()).rejects.toThrow('not ready')
    expect(run.page.reload).not.toHaveBeenCalled()
  })
})
