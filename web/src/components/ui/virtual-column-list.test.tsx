import { act, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { VirtualColumnList } from './virtual-column-list'

afterEach(() => vi.unstubAllGlobals())

it('keeps the header viewport and overflow range synchronized with the rows', () => {
  let resize = () => {}
  const disconnect = vi.fn()
  vi.stubGlobal('ResizeObserver', class {
    constructor(callback: () => void) { resize = callback }
    observe() {}
    disconnect = disconnect
  })
  const { container, unmount } = render(<VirtualColumnList virtualize={false} header={<div>Columns</div>} data={['row']} ariaLabel="Rows" computeItemKey={(_, row) => row} itemContent={(_, row) => <div>{row}</div>}/>)
  const scroller = screen.getByRole('list', { name: 'Rows' })
  Object.defineProperties(scroller, { offsetWidth: { value: 600 }, clientWidth: { value: 584, configurable: true }, scrollWidth: { value: 920 } })
  act(resize)
  const header = container.querySelector<HTMLElement>('[data-virtual-column-header]')!
  expect(header.parentElement?.style.paddingRight).toBe('16px')
  expect((header.firstElementChild as HTMLElement).style.width).toBe('920px')
  scroller.scrollLeft = 180
  fireEvent.scroll(scroller)
  expect(header.scrollLeft).toBe(180)
  scroller.scrollLeft = 200
  fireEvent.scroll(header)
  expect(scroller.scrollLeft).toBe(200)
  fireEvent.scroll(scroller)
  header.scrollLeft = 240
  fireEvent.scroll(header)
  expect(scroller.scrollLeft).toBe(240)
  Object.defineProperty(scroller, 'clientWidth', { value: 600 })
  act(resize)
  expect(header.parentElement?.style.paddingRight).toBe('0px')
  unmount()
  expect(disconnect).toHaveBeenCalled()
})
