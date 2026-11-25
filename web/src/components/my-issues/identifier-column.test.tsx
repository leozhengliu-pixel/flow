import { render } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { identifierSamples, useIdentifierColumnWidth } from './identifier-column'

function Column({ identifiers, listId }: { identifiers: string[]; listId?: string }) {
  const column = useIdentifierColumnWidth(identifiers, listId)
  return <div id={listId} data-testid="column" data-width={column.width ?? ''}>{column.probe}</div>
}

describe('identifier column', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })

  it('keeps one sample per team key and digit count', () => {
    expect(identifierSamples(['FLO-1', 'FLO-4', 'FLO-59', 'FLO-60', 'PIC-2'])).toEqual(['FLO-4', 'FLO-60', 'PIC-2'])
  })

  it('fits the widest identifier measured without tracking, cut to whole pixels', () => {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      return { width: (this.textContent?.length ?? 0) * 8.4 } as DOMRect
    })
    const { getByTestId } = render(<Column identifiers={['FLO-1', 'FLO-59']}/>)
    expect(getByTestId('column').dataset.width).toBe(String(Math.floor(6 * 8.4)))
  })

  it('stays content-sized when nothing is measurable', () => {
    const { getByTestId } = render(<Column identifiers={['FLO-1']}/>)
    expect(getByTestId('column').dataset.width).toBe('')
  })

  it('applies the same measured width directly to the windowed list', () => {
    vi.stubGlobal('ResizeObserver', undefined)
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      return { width: (this.textContent?.length ?? 0) * 8.4 } as DOMRect
    })
    const { getByTestId, rerender } = render(<Column identifiers={['FLO-59']} listId="windowed" />)
    const width = () => getByTestId('column').style.getPropertyValue('--issue-identifier-width')
    expect(width()).toBe('50px')
    rerender(<Column identifiers={['FLO-590']} listId="windowed" />)
    expect(width()).toBe('58px')
  })

  it('uses the laid-out sample width without forcing a layout in windowed lists', () => {
    let resize: ResizeObserverCallback | undefined
    vi.stubGlobal('ResizeObserver', class {
      constructor(callback: ResizeObserverCallback) { resize = callback }
      observe() {}
      disconnect() {}
    })
    const measure = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect')
    const { getByTestId } = render(<Column identifiers={['FLO-59', 'FLOW-129']} listId="windowed" />)
    const column = getByTestId('column')
    expect(column.querySelectorAll('[data-identifier-sample]')).toHaveLength(2)
    resize?.([{ contentRect: { width: 58.9 } } as ResizeObserverEntry], {} as ResizeObserver)
    expect(column.style.getPropertyValue('--issue-identifier-width')).toBe('58px')
    expect(measure).not.toHaveBeenCalled()
  })
})
