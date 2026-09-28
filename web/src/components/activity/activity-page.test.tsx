import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import { ActivityPage } from './activity-page'

describe('LS-0018 ActivityPage', () => {
  beforeEach(() => {
    sessionStorage.clear()
    localStorage.clear()
  })

  it('exposes activity-sidebar restore key and initiative prefs', async () => {
    const user = userEvent.setup()
    render(
      <ActivityPage entityId="init-1" entityType="initiative">
        <article data-update-id="upd-1">Update one</article>
      </ActivityPage>,
    )
    expect(document.querySelector('[data-restore-scroll-view="activity-sidebar-init-1"]')).toBeTruthy()
    expect(screen.getByRole('group', { name: 'Activity preferences' })).toBeTruthy()
    const sub = screen.getByLabelText('Sub-initiative updates')
    expect(sub).toBeChecked()
    await user.click(sub)
    expect(sub).not.toBeChecked()
    expect(JSON.parse(localStorage.getItem('flow:activity-page-prefs:initiative:init-1')!).initiative.showSubInitiativeUpdates).toBe(false)
  })

  it('scrolls initialUpdateId into view', () => {
    render(
      <ActivityPage entityId="proj-1" entityType="project" initialUpdateId="upd-9">
        <article data-update-id="upd-9">Target</article>
      </ActivityPage>,
    )
    expect(document.querySelector('[data-update-id="upd-9"]')).toBeTruthy()
    expect(screen.getByLabelText('Include project activity')).toBeChecked()
  })
})
