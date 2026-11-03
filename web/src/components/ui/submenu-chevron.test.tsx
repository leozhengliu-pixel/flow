import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { SubmenuChevron } from './submenu-chevron'

describe('SubmenuChevron', () => {
  it('is the reference triangle glyph at every width, not an icon', () => {
    const { container } = render(<SubmenuChevron />)
    const marker = container.querySelector('.flow-submenu-chevron')
    expect(marker?.textContent).toBe('▶')
    expect(marker?.getAttribute('aria-hidden')).toBe('true')
    expect(container.querySelector('svg')).toBeNull()
  })
})
