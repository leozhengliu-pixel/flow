/**
 * Thin UpdatePage host (LS-0620 subset) used by SummaryUpdatePage (LS-0570).
 * PageContainer + UpdateTitleHeader chrome for a single project/initiative/team update.
 */
import { formatDistanceToNowStrict } from 'date-fns'
import { ArrowLeft, Copy } from 'lucide-react'
import type { ReactNode } from 'react'
import type { Initiative, Project, ProjectUpdate, InitiativeUpdate, User } from '@/types/flow'
import { Avatar } from '@/components/issue/issue-row'
import { RichComment } from '@/components/activity/rich-comment'
import './update-page.css'

export type UpdatePageKind = 'project' | 'initiative' | 'team' | 'summary'

export type UpdatePageModel = {
  id: string
  body: string
  bodyData?: Record<string, unknown>
  health?: Project['health']
  createdAt: string
  editedAt?: string
  user: User
}

export type UpdatePageProps = {
  update: UpdatePageModel
  kind: UpdatePageKind
  entityName?: string
  entityHref?: string
  project?: Project
  initiative?: Initiative
  title?: string
  headerActions?: ReactNode
  onBack?: () => void
  onCopyLink?: () => void
  className?: string
}

const HEALTH_LABEL: Record<Project['health'], string> = {
  onTrack: 'On track',
  atRisk: 'At risk',
  offTrack: 'Off track',
  noUpdate: 'No update',
}

export function UpdatePage({
  update,
  kind,
  entityName,
  entityHref,
  title,
  headerActions,
  onBack,
  onCopyLink,
  className,
}: UpdatePageProps) {
  const heading =
    title ??
    (kind === 'summary'
      ? 'Your Pulse'
      : kind === 'team'
        ? 'Team update'
        : kind === 'initiative'
          ? 'Initiative update'
          : 'Project update')

  return (
    <div className={['flow-update-page', className].filter(Boolean).join(' ')} data-surface="LS-0620" data-update-kind={kind}>
      <header className="flow-update-page__chrome">
        <div className="flow-update-page__title-header" data-update-title-header="">
          {onBack && (
            <button aria-label="Back" className="flow-update-page__back" onClick={onBack} type="button">
              <ArrowLeft size={14} />
            </button>
          )}
          <div className="flow-update-page__titles">
            <h1>{heading}</h1>
            {entityName && (
              entityHref ? (
                <a className="flow-update-page__entity" href={entityHref}>{entityName}</a>
              ) : (
                <span className="flow-update-page__entity">{entityName}</span>
              )
            )}
          </div>
          <div className="flow-update-page__actions">
            {onCopyLink && (
              <button aria-label="Copy link" onClick={onCopyLink} type="button">
                <Copy size={14} />
              </button>
            )}
            {headerActions}
          </div>
        </div>
      </header>

      <article className="flow-update-page__body">
        <div className="flow-update-page__byline">
          {update.health && update.health !== 'noUpdate' && (
            <span className={`flow-update-page__health is-${update.health}`}>
              <i />
              {HEALTH_LABEL[update.health]}
            </span>
          )}
          <Avatar name={update.user.displayName || update.user.name} />
          <strong>{update.user.displayName || update.user.name}</strong>
          <time title={new Date(update.createdAt).toLocaleString()}>
            {formatDistanceToNowStrict(new Date(update.createdAt), { addSuffix: true })}
            {update.editedAt ? ' · edited' : ''}
          </time>
        </div>
        <div className="flow-update-page__content">
          <RichComment body={update.body} data={update.bodyData} />
        </div>
      </article>
    </div>
  )
}

UpdatePage.displayName = 'UpdatePage'

export function toUpdatePageModel(update: ProjectUpdate | InitiativeUpdate): UpdatePageModel {
  return {
    id: update.id,
    body: update.body,
    bodyData: update.bodyData,
    health: update.health,
    createdAt: update.createdAt,
    editedAt: update.editedAt,
    user: update.user,
  }
}
