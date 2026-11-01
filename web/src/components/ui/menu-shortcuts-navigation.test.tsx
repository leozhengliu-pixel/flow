import { fireEvent, render } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { useFlowHotkeys } from './menu-shortcuts'

it('keeps issue letter commands from consuming app navigation sequences', () => {
  const priority = vi.fn()
  function Harness() { useFlowHotkeys({ P: priority }, true, undefined, true); return null }
  render(<Harness/>)
  fireEvent.keyDown(window, { key: 'g', code: 'KeyG' })
  fireEvent.keyDown(window, { key: 'p', code: 'KeyP' })
  fireEvent.keyDown(window, { key: 'o', code: 'KeyO' })
  fireEvent.keyDown(window, { key: 'p', code: 'KeyP' })
  expect(priority).not.toHaveBeenCalled()
  fireEvent.keyDown(window, { key: 'p', code: 'KeyP' })
  expect(priority).toHaveBeenCalledTimes(1)
})
