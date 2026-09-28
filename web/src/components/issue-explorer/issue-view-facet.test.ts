import { describe, expect, it } from 'vitest'
import type { SavedView } from '@/types/flow'
import { packIssueViewFacet, resolveIssueViewFacetMode } from './issue-view-facet'

describe('Issue view facet packing (LS-0155 / LS-0585 / LS-0587)', () => {
  it('packs browse team facet with split + triage', () => {
    const savedView = {
      id: 'v1',
      name: 'Bugs',
      description: '',
      scope: 'team',
      teamId: 't1',
      view: 'all',
      filters: [],
      display: {},
      createdAt: '',
      updatedAt: '',
    } as SavedView
    const pack = packIssueViewFacet({ teamScoped: true, savedView })
    expect(pack.enableSplitLayout).toBe(true)
    expect(pack.enableTriageOption).toBe(true)
    expect(pack.enableSubscriptions).toBe(true)
    expect(pack.showFacetHeader).toBe(false)
  })

  it('packs create/edit facet chrome', () => {
    expect(resolveIssueViewFacetMode({ creatingView: true })).toBe('create')
    const pack = packIssueViewFacet({ creatingView: true, teamScoped: true })
    expect(pack.showFacetHeader).toBe(true)
    expect(pack.enableSplitLayout).toBe(false)
    expect(pack.facetTitle).toBe('New view')
  })
})
