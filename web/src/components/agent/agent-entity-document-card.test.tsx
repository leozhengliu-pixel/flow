import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, project, teammate, viewer } from '@/test/fixtures'
import type { FlowDocument } from '@/types/flow'
import { AgentEntityCardBody } from './agent-entity-hover'

const makeDocument = (overrides: Partial<FlowDocument> = {}) => ({
  id: 'document-1', slugId: 'plan-abc', title: 'Launch plan', color: '#8b8b90', content: '# Launch plan\n\nShip **v1** on Friday.\n\nSecond paragraph.', creator: viewer,
  projectIds: [], teamIds: ['team-1'], subscriberIds: [], favorite: false, createdAt: '2026-09-01T00:00:00Z', updatedAt: '2026-09-03T00:00:00Z',
  revisions: [{ id: 'r1', documentId: 'document-1', title: 'Launch plan', content: '', author: viewer, createdAt: '2026-09-02T00:00:00Z' }, { id: 'r2', documentId: 'document-1', title: 'Launch plan', content: '', author: teammate, createdAt: '2026-09-03T00:00:00Z' }],
  ...overrides,
}) as FlowDocument

function card(doc: FlowDocument, data = makeBootstrap({ documents: [doc] })) {
  return render(<I18nProvider><AgentEntityCardBody data={data} entity={{ kind: 'document', document: doc }}/></I18nProvider>)
}

describe('document mention hover card', () => {
  it('shows the icon and title, a summary line, the parent row and the last editor', () => {
    card(makeDocument())
    expect(screen.getByText('Launch plan', { selector: 'span' })).toBeVisible()
    expect(document.body).toHaveTextContent('Ship v1 on Friday')
    expect(document.body).not.toHaveTextContent('Launch plan Ship')
    expect(document.body).toHaveTextContent('Test team')
    expect(document.body).toHaveTextContent(/Last edited .* by Teammate/)
  })

  it('shows the project, falls back to the creator without revisions and truncates long summaries', () => {
    card(makeDocument({ teamIds: [], projectIds: [project.id], revisions: [], content: 'x'.repeat(400) }), makeBootstrap({ projects: [project] }))
    expect(document.body).toHaveTextContent(project.name)
    expect(document.body).toHaveTextContent(/Last edited .* by Viewer/)
    expect(document.body.textContent).toContain(`${'x'.repeat(157)}…`)
  })
})
