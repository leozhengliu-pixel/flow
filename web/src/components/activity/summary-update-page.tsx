/**
 * LS-0570 SummaryUpdatePage — thin route host:
 * `/:workspaceSlug/update/:postId` → <UpdatePage update={post} />.
 */
import { useMemo } from 'react'
import type { BootstrapData, InitiativeUpdate, ProjectUpdate } from '@/types/flow'
import { pulsePath, summaryUpdatePath } from '@/lib/app-routes'
import { UpdatePage, toUpdatePageModel, type UpdatePageKind } from './update-page'

export type SummaryUpdateResolved =
  | { kind: 'project'; update: ProjectUpdate; projectId: string; projectName: string; projectSlugId: string }
  | { kind: 'initiative'; update: InitiativeUpdate; initiativeId: string; initiativeName: string; initiativeSlugId: string }
  | null

export function resolveSummaryUpdate(data: BootstrapData, postId: string): SummaryUpdateResolved {
  for (const project of data.projects) {
    const update = (data.projectUpdates[project.id] ?? []).find(item => item.id === postId)
    if (update) {
      return {
        kind: 'project',
        update,
        projectId: project.id,
        projectName: project.name,
        projectSlugId: project.slugId,
      }
    }
  }
  for (const initiative of data.initiatives) {
    const update = (data.initiativeUpdates[initiative.id] ?? []).find(item => item.id === postId)
    if (update) {
      return {
        kind: 'initiative',
        update,
        initiativeId: initiative.id,
        initiativeName: initiative.name,
        initiativeSlugId: initiative.slugId,
      }
    }
  }
  return null
}

export type SummaryUpdatePageProps = {
  data: BootstrapData
  postId: string
  onNavigate: (path: string) => void
}

export function SummaryUpdatePage({ data, postId, onNavigate }: SummaryUpdatePageProps) {
  const resolved = useMemo(() => resolveSummaryUpdate(data, postId), [data, postId])
  const workspace = data.workspace.urlKey

  if (!resolved) {
    return (
      <div className="flow-update-page" data-surface="LS-0570">
        <header className="flow-update-page__chrome">
          <div className="flow-update-page__title-header">
            <div className="flow-update-page__titles">
              <h1>Update not found</h1>
              <span className="flow-update-page__entity">This Pulse update may have been deleted.</span>
            </div>
          </div>
        </header>
        <button
          className="flow-update-page__back"
          onClick={() => onNavigate(pulsePath(workspace))}
          style={{ width: 'auto', padding: '0 10px' }}
          type="button"
        >
          Back to Pulse
        </button>
      </div>
    )
  }

  const kind: UpdatePageKind = 'summary'
  const entityName = resolved.kind === 'project' ? resolved.projectName : resolved.initiativeName
  const entityHref =
    resolved.kind === 'project'
      ? `/${workspace}/project/${resolved.projectSlugId}/activity`
      : `/${workspace}/initiative/${resolved.initiativeSlugId}/activity`

  return (
    <div data-surface="LS-0570">
      <UpdatePage
        entityHref={entityHref}
        entityName={entityName}
        kind={kind}
        onBack={() => onNavigate(pulsePath(workspace))}
        onCopyLink={() => {
          void navigator.clipboard.writeText(window.location.href)
        }}
        title="Your Pulse"
        update={toUpdatePageModel(resolved.update)}
      />
    </div>
  )
}

SummaryUpdatePage.displayName = 'SummaryUpdatePage'

export { summaryUpdatePath }
