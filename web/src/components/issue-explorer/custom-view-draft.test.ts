import { beforeEach, describe, expect, it } from 'vitest'
import {
  customViewDraftKey,
  discardCustomViewDraft,
  readCustomViewDraft,
  writeCustomViewDraft,
} from './custom-view-draft'

describe('custom view draft (LS-0224)', () => {
  beforeEach(() => localStorage.clear())

  it('persists and discards drafts under custom_view_draft_* keys', () => {
    expect(customViewDraftKey('acme', 'view-1')).toBe('custom_view_draft_acme_view-1')
    writeCustomViewDraft('acme', 'view-1', { name: 'Draft', filters: [{ id: 'f1' }] })
    expect(readCustomViewDraft('acme', 'view-1')).toMatchObject({ name: 'Draft', filters: [{ id: 'f1' }] })
    discardCustomViewDraft('acme', 'view-1')
    expect(readCustomViewDraft('acme', 'view-1')).toBeUndefined()
  })
})
