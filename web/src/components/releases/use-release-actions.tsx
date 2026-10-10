import { useState, type ReactNode } from 'react'
import { toast } from 'sonner'

import { confirmAction } from '@/components/ui/action-dialog-service'
import { toggleFavoriteFor } from '@/lib/favorites'
import { deleteRelease, updateRelease } from '@/lib/api'
import { releasePath, releasePipelinePath } from '@/lib/app-routes'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, Release, ReleasePipeline, ReleaseResource } from '@/types/flow'

import { ReleaseAddIssuesDialog } from './release-add-issues-dialog'
import { ReleaseEditorDialog } from './release-editor-dialog'
import type { ReleaseMenuActions } from './release-menus'
import { ReleaseDocumentDialog, ReleaseLinkDialog } from './release-resource-dialogs'
import { releaseStatusForStage } from './release-view-model'
import { absoluteUrl, copyToClipboard, isFavorite } from './release-actions-model'

/**
 * The release actions shared by the "…" menus, the row context menu and page shortcuts, plus the
 * dialogs they open (edit, add issues, add document, add link).
 */
export function useReleaseActions({ data, onReload, onNavigate, afterDelete }: { data: BootstrapData; onReload: () => Promise<void>; onNavigate: (path: string) => void; afterDelete?: (release: Release, pipeline: ReleasePipeline) => void }) {
  const { t } = useI18n()
  const [dialog, setDialog] = useState<{ kind: 'edit' | 'issues' | 'document' | 'link'; release: Release; pipeline: ReleasePipeline }>()
  const close = () => setDialog(undefined)
  const saveResources = async (release: Release, next: ReleaseResource[]) => {
    try { await updateRelease(release.id, { resources: next }); await onReload() } catch (error) { toast.error(error instanceof Error ? error.message : t('Could not save release')) }
  }
  const actionsFor = (pipeline: ReleasePipeline, release: Release): ReleaseMenuActions => ({
    onEdit: () => setDialog({ kind: 'edit', release, pipeline }),
    onStage: stage => { void updateRelease(release.id, { stage, status: releaseStatusForStage(pipeline, stage, release.status) }).then(onReload).catch(error => toast.error(error instanceof Error ? error.message : t('Could not save release'))) },
    onAddIssues: () => setDialog({ kind: 'issues', release, pipeline }),
    onAddDocument: () => setDialog({ kind: 'document', release, pipeline }),
    onAddLink: () => setDialog({ kind: 'link', release, pipeline }),
    onToggleFavorite: () => { void toggleFavoriteFor(data, 'release', release.id, undefined, isFavorite(data, 'release', release.id)) },
    onCopyUrl: () => void copyToClipboard(absoluteUrl(releasePath(data.workspace.urlKey, pipeline.slugId, release.slugId)), t('URL copied to clipboard'), t('Could not copy URL')),
    onCopyVersion: () => void copyToClipboard(release.version, t('"{version}" copied to clipboard').replace('{version}', release.version), t('Could not copy to clipboard')),
    onDelete: () => {
      void (async () => {
        const confirmed = await confirmAction(t('Delete "{name}"?').replace('{name}', release.name), { description: t('Deleted releases are available in the "Recently deleted releases" view for 30 days, before they are permanently deleted.'), confirmLabel: t('Delete'), danger: true })
        if (!confirmed) return
        try {
          await deleteRelease(release.id)
          await onReload()
          afterDelete?.(release, pipeline)
          toast.success(t('Deleted release {name}').replace('{name}', release.name), {
            description: t('You can restore it from the recently deleted releases view.'),
            action: { label: t('View recently deleted releases'), onClick: () => onNavigate(releasePipelinePath(data.workspace.urlKey, pipeline.slugId, 'deleted')) },
          })
        } catch (error) { toast.error(error instanceof Error ? error.message : t('Could not delete release')) }
      })()
    },
  })
  let dialogs: ReactNode = null
  if (dialog?.kind === 'edit') dialogs = <ReleaseEditorDialog data={data} pipeline={dialog.pipeline} release={data.releases.find(item => item.id === dialog.release.id) ?? dialog.release} onClose={close} onSaved={onReload}/>
  if (dialog?.kind === 'issues') dialogs = <ReleaseAddIssuesDialog data={data} release={data.releases.find(item => item.id === dialog.release.id) ?? dialog.release} onClose={close} onReload={onReload}/>
  if (dialog?.kind === 'link') { const release = dialog.release; dialogs = <ReleaseLinkDialog onClose={close} onSave={async resource => { await saveResources(release, [...(release.resources ?? []), resource]); close() }}/> }
  if (dialog?.kind === 'document') { const release = dialog.release; dialogs = <ReleaseDocumentDialog data={data} onClose={close} onSave={async resource => { await saveResources(release, [...(release.resources ?? []), resource]); close() }}/> }
  return { actionsFor, dialogs, saveResources }
}
