import { readFileSync } from 'node:fs'
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

  it('loads the stylesheet that right-aligns the glyph in its 16px box', () => {
    expect(readFileSync('src/components/ui/submenu-chevron.tsx', 'utf8')).toContain("import './submenu-chevron.css'")
    expect(readFileSync('src/components/ui/submenu-chevron.css', 'utf8')).toMatch(/\.flow-submenu-chevron\s*\{[^}]*width: 16px[^}]*text-align: right/)
  })
})
