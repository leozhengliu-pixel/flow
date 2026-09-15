import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { ReleasePipelineIcon, ReleasesIcon, ReleaseStatusIcon } from './release-icons'

describe('release icons', () => {
  it('renders a distinct status glyph for each release status', () => {
    const markup = (['planned', 'inProgress', 'released', 'canceled'] as const).map(status => {
      const { container } = render(<ReleaseStatusIcon status={status} />)
      const svg = container.querySelector('svg')
      expect(svg).toHaveAttribute('data-icon', 'release-status')
      expect(svg).toHaveAttribute('data-status', status)
      return svg?.innerHTML ?? ''
    })
    expect(new Set(markup).size).toBe(4)
  })

  it('exposes pipeline and module icons for list and empty states', () => {
    const pipeline = render(<ReleasePipelineIcon />).container.querySelector('svg')
    const releases = render(<ReleasesIcon />).container.querySelector('svg')
    expect(pipeline).toHaveAttribute('data-icon', 'release-pipeline')
    expect(releases).toHaveAttribute('data-icon', 'releases')
  })
})
