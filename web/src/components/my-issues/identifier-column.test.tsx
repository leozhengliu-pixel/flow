import { render } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { identifierSamples, useIdentifierColumnWidth } from './identifier-column'

function Column({ identifiers }: { identifiers: string[] }) {
  const column = useIdentifierColumnWidth(identifiers)
  return <div data-testid="column" data-width={column.width ?? ''}>{column.probe}</div>
}

describe('identifier column', () => {
  afterEach(() => vi.restoreAllMocks())

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
})
