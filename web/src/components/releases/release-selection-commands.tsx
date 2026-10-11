import { toast } from '@/lib/toast'

import { useRegisterCommandContext } from '@/components/command/command-context'
import { datePageOptions, type ContextAction } from '@/components/command/context-commands'
import { IssueCircleIcon } from '@/components/customer-detail/customer-page-icons'
import { IssueActionGlyph } from '@/components/issue/issue-action-glyphs'
import { FlowGlyph } from '@/components/ui/menu-glyphs'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { useI18n } from '@/i18n/i18n'
import { updateRelease } from '@/lib/api'
import type { BootstrapData, Release, ReleasePipeline } from '@/types/flow'

import { isFavorite } from './release-actions-model'
import type { ReleaseMenuActions } from './release-menus'
import { ReleaseDateIcon, ReleasesIcon, ReleaseStatusIcon } from './release-icons'
import { dayValue, releaseStatusForStage } from './release-view-model'

/**
 * ⌘K context for a pipeline-list selection: the selection bar's "Actions" opens the palette on
 * The reference app's "Release · v1.2.0" chip with the release commands first.
 */
export function useReleaseSelectionCommands({ data, pipeline, releases, actions, onReload }: { data: BootstrapData; pipeline: ReleasePipeline; releases: Release[]; actions?: ReleaseMenuActions; onReload: () => Promise<void> }) {
  const { t, formatDate } = useI18n()
  const release = releases.length === 1 ? releases[0] : undefined
  const scheduled = pipeline.type === 'scheduled'
  const setDate = (value: string) => async () => {
    if (!release) return
    try { await updateRelease(release.id, { targetDate: value }); await onReload() } catch (error) { toast.error(error instanceof Error ? error.message : t('Could not save release')) }
  }
  const setStage = (stage: string) => async () => {
    try { await Promise.all(releases.map(item => updateRelease(item.id, { stage, status: releaseStatusForStage(pipeline, stage, item.status) }))); await onReload() } catch (error) { toast.error(error instanceof Error ? error.message : t('Could not save release')) }
  }
  const stageAction: ContextAction[] = scheduled ? [{ id: 'release-stage', label: 'Change release stage…', icon: <ReleasesIcon/>, page: { id: 'releaseStage', label: 'Change release stage…' } }] : []
  const list: ContextAction[] = release && actions ? [
    { id: 'release-edit', label: 'Edit release…', icon: <FlowGlyph name="edit"/>, run: actions.onEdit },
    { id: 'release-add-issues', label: 'Add issues to release…', icon: <IssueCircleIcon/>, shortcut: ['⌥', 'R'], run: actions.onAddIssues },
    ...stageAction,
    ...(scheduled ? [{ id: 'release-date', label: 'Change target date…', icon: <ReleaseDateIcon tone="set"/>, detail: release.targetDate ? formatDate(dayValue(release.targetDate), { month: 'short', day: 'numeric' }) : undefined, page: { id: 'releaseDate' as const, label: 'Change target date…' } }] : []),
    { id: 'release-add-link', label: 'Add a link…', icon: <IssueActionGlyph label="Copy link" fallback={null}/>, shortcut: ['Ctrl', 'L'], run: actions.onAddLink },
    { id: 'release-add-document', label: 'Create new document…', icon: <ViewGlyph icon="Page" color="currentColor"/>, run: actions.onAddDocument },
    { id: 'release-favorite', label: isFavorite(data, 'release', release.id) ? 'Remove release from favorites' : 'Favorite release', icon: <FlowGlyph name="favorite"/>, shortcut: ['⌥', 'F'], run: actions.onToggleFavorite },
    { id: 'release-copy-url', label: 'Copy release URL', icon: <IssueActionGlyph label="Copy link" fallback={null}/>, shortcut: ['⌘', '⇧', ','], run: actions.onCopyUrl },
    ...(release.version ? [{ id: 'release-copy-version', label: 'Copy release version', icon: <FlowGlyph name="copy"/>, run: actions.onCopyVersion }] : []),
    { id: 'release-delete', label: 'Delete release', icon: <FlowGlyph name="delete"/>, run: actions.onDelete },
  ] : stageAction
  useRegisterCommandContext(releases.length ? {
    kind: 'release',
    ids: releases.map(item => item.id),
    label: release ? release.name : t('{count} releases').replace('{count}', String(releases.length)),
    actions: list,
    pageOptions: (page, query) => {
      if (page.id === 'releaseDate' && release) return datePageOptions(query, release.targetDate ?? '', Boolean(release.targetDate), setDate, t, false, 'Remove target date')
      if (page.id === 'releaseStage') return pipeline.stages.map(stage => ({ id: stage, label: stage, entity: true, icon: <ReleaseStatusIcon status={releaseStatusForStage(pipeline, stage)}/>, current: releases.every(item => item.stage === stage), select: setStage(stage) }))
      return []
    },
  } : undefined)
}
