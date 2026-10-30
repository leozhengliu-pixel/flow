import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { EstimateChip, EstimateIcon, EstimateValueIcon } from './estimate-icon'

const estimatePath = 'M3.741 14.5h8.521c1.691 0 2.778-1.795 1.993-3.293l-4.26-8.134c-.842-1.608-3.144-1.608-3.986 0l-4.26 8.134C.962 12.705 2.05 14.5 3.74 14.5ZM8 3.368a.742.742 0 0 0-.663.402l-4.26 8.134A.75.75 0 0 0 3.741 13H8V3.367Z'
const noEstimatePath = 'M8.666 3.77a.75.75 0 0 0-1.329 0L6.033 6.26l-1.329-.696 1.305-2.49c.842-1.609 3.144-1.609 3.986 0l1.305 2.49-1.33.696-1.304-2.49ZM9.545 13v1.5h2.717c1.691 0 2.778-1.795 1.993-3.293l-1.304-2.49-1.329.695 1.305 2.49a.75.75 0 0 1-.665 1.099H9.545ZM4.382 9.413l-1.33-.696-1.304 2.49c-.785 1.499.302 3.295 1.993 3.295H6.46V13H3.74a.75.75 0 0 1-.664-1.098l1.305-2.49Z'

describe('estimate glyphs', () => {
  it.each([false, true])('renders a single exact filled path (none=%s)', none => {
    const { container } = render(<EstimateIcon none={none}/>)
    const svg = container.querySelector('svg')!
    expect(svg).toHaveAttribute('viewBox', '0 0 16 16')
    expect(svg).toHaveAttribute('width', '16')
    expect(svg).toHaveAttribute('height', '16')
    expect(svg).toHaveAttribute('fill', 'currentColor')
    expect(svg.children).toHaveLength(1)
    expect(svg.firstElementChild).toHaveAttribute('d', none ? noEstimatePath : estimatePath)
    expect(svg.firstElementChild).toHaveAttribute('fill', 'currentColor')
    expect(svg.firstElementChild).toHaveAttribute('fill-rule', 'evenodd')
    expect(svg.querySelector('[stroke], polygon, [data-level]')).toBeNull()
  })

  it.each([0, 1, 2, 3, 5, 8, 13, 21, 64])('uses the same glyph for %s points', value => {
    const { container } = render(<EstimateValueIcon value={value}/>)
    expect(container.querySelector('path')).toHaveAttribute('d', estimatePath)
  })

  it.each([undefined, null, -1])('uses the broken outline for the unset menu value %s', value => {
    const { container } = render(<EstimateValueIcon value={value}/>)
    expect(container.querySelector('path')).toHaveAttribute('d', noEstimatePath)
  })

  it('keeps chips at 14px and preserves scale-specific labels', () => {
    const { container } = render(<EstimateChip value={5} settings={{ estimateType: 'tShirt' }}/>)
    expect(container.querySelector('svg')).toHaveAttribute('width', '14')
    expect(container.querySelector('svg')).toHaveAttribute('height', '14')
    expect(container.querySelector('path')).toHaveAttribute('d', estimatePath)
    expect(container.querySelector('.estimate-chip__value')).toHaveTextContent('L')
  })
})
