import { render } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { AppStartup } from './app-startup'

vi.mock('@/i18n/i18n', () => ({ useI18n: () => ({ t: (source: string) => source }) }))

afterEach(() => vi.unstubAllGlobals())

it('preserves elapsed hint time across rerenders and later mounts', () => {
  let now = 3100
  vi.stubGlobal('performance', {
    getEntriesByType: () => [{ domContentLoadedEventStart: 100 }],
    now: () => now,
  })
  const { container, rerender, unmount } = render(<AppStartup />)
  expect(container.querySelector<HTMLElement>('.flow-startup__text')!.style.getPropertyValue('--flow-startup-hint-delay')).toBe('5800ms')
  now = 10100
  rerender(<AppStartup />)
  expect(container.querySelector<HTMLElement>('.flow-startup__text')!.style.getPropertyValue('--flow-startup-hint-delay')).toBe('5800ms')
  unmount()
  const late = render(<AppStartup />)
  expect(late.container.querySelector<HTMLElement>('.flow-startup__text')!.style.getPropertyValue('--flow-startup-hint-delay')).toBe('-1200ms')
})

it('uses the paused document clock when mounted before DOM readiness', () => {
  vi.stubGlobal('performance', { getEntriesByType: () => [{ domContentLoadedEventStart: 0 }] })
  const { container } = render(<AppStartup />)
  expect(container.querySelector<HTMLElement>('.flow-startup__text')!.style.getPropertyValue('--flow-startup-hint-delay')).toBe('')
})
