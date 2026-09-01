import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { DocumentGlyph } from './document-icon'

describe('DocumentGlyph', () => {
  it('renders the saved document icon and color', () => {
    const { container } = render(<DocumentGlyph document={{ icon: 'BookOpen', color: '#eb5757' }}/>)

    expect(container.querySelector('use')).toHaveAttribute('href', '/flow-view-icons.svg#BookOpen')
    expect(container.querySelector('svg')).toHaveStyle({ color: '#eb5757' })
  })

  it('uses the document defaults only when no custom visual is set', () => {
    const { container } = render(<DocumentGlyph document={{}}/>)

    expect(container.querySelector('use')).toHaveAttribute('href', '/flow-view-icons.svg#Page')
    expect(container.querySelector('svg')).toHaveStyle({ color: '#8b8b90' })
  })
})
