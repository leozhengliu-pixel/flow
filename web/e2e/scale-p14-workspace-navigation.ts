import type { Locator, Page, Request } from '@playwright/test'

// A network-interface change can abort Vite modules before bootstrap starts.
// Recover once for that browser error; keep every other failure unchanged.
export async function openWorkspaceWithNetworkRecovery(page: Page, url: string, ready: Locator) {
  let interrupted = false
  let signal!: () => void
  const networkChanged = new Promise<void>(resolve => { signal = resolve })
  const onFailed = (request: Request) => {
    if (request.resourceType() === 'script' && request.failure()?.errorText === 'net::ERR_NETWORK_CHANGED') {
      interrupted = true
      signal()
    }
  }
  page.on('requestfailed', onFailed)
  try {
    try {
      await page.goto(url)
    } catch (error) {
      if (!interrupted) throw error
    }
    const loaded = !interrupted && await Promise.race([
      ready.waitFor({ state: 'visible' }).then(() => true),
      networkChanged.then(() => false),
    ])
    if (!loaded) {
      await page.reload()
      await ready.waitFor({ state: 'visible' })
    }
  } finally {
    page.off('requestfailed', onFailed)
  }
}
