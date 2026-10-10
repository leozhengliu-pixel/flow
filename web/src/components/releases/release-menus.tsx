import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import type { ReactNode } from 'react'

import { IssueCircleIcon } from '@/components/customer-detail/customer-page-icons'
import { IssueActionGlyph } from '@/components/issue/issue-action-glyphs'
import { LinearGlyph } from '@/components/ui/menu-glyphs'
import { LinearContextMenuPortal, LinearContextMenuRoot, LinearContextMenuTrigger, LinearDropdownMenuContent, LinearMenuContent, LinearMenuItem, LinearMenuOptions, LinearMenuSeparator, LinearSubmenu } from '@/components/ui/row-context-menu'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { useI18n } from '@/i18n/i18n'
import type { Release, ReleasePipeline } from '@/types/flow'

import { ReleasesIcon, ReleaseStatusIcon, SettingsGearIcon } from './release-icons'
import { releaseStatusForStage } from './release-view-model'

/** Every release action Linear lists in the release "…" menu and the row context menu. */
export type ReleaseMenuActions = {
  onEdit: () => void
  onStage: (stage: string) => void
  onAddIssues: () => void
  onAddDocument: () => void
  onAddLink: () => void
  onToggleFavorite: () => void
  onCopyUrl: () => void
  onCopyVersion: () => void
  onDelete: () => void
}

const LinkIcon = () => <IssueActionGlyph label="Copy link" fallback={null}/>

/** "Change stage…" picker rows (Linear's stage menu: search, icon, check, number key). */
export function ReleaseStageOptions({ pipeline, value, onChoose }: { pipeline: ReleasePipeline; value?: string; onChoose: (stage: string) => void }) {
  return <LinearMenuOptions options={pipeline.stages.map((stage, index) => ({ id: stage, label: stage, translate: false, icon: <ReleaseStatusIcon status={releaseStatusForStage(pipeline, stage)}/>, shortcut: index < 9 ? String(index + 1) : undefined }))} selected={new Set(value ? [value] : [])} placeholder="Change stage…" onChoose={onChoose}/>
}

export function ReleaseMenuItems({ pipeline, release, favorite, actions }: { pipeline: ReleasePipeline; release: Release; favorite: boolean; actions: ReleaseMenuActions }) {
  const scheduled = pipeline.type === 'scheduled'
  return <>
    <LinearMenuItem icon={<LinearGlyph name="edit"/>} label="Edit…" onSelect={actions.onEdit}/>
    {scheduled && <LinearSubmenu icon={<ReleasesIcon/>} label="Stage" search className="flow-release-stage-submenu">
      <ReleaseStageOptions pipeline={pipeline} value={release.stage} onChoose={actions.onStage}/>
    </LinearSubmenu>}
    <LinearMenuSeparator/>
    {scheduled && <LinearMenuItem icon={<IssueCircleIcon/>} label="Add issues to release…" shortcut="⌥ R" onSelect={actions.onAddIssues}/>}
    <LinearMenuItem icon={<ViewGlyph icon="Page" color="currentColor"/>} label="Add document…" onSelect={actions.onAddDocument}/>
    <LinearMenuItem icon={<LinkIcon/>} label="Add link…" shortcut="Ctrl L" onSelect={actions.onAddLink}/>
    <LinearMenuSeparator/>
    <LinearMenuItem icon={<LinearGlyph name="favorite"/>} label={favorite ? 'Remove from favorites' : 'Favorite'} shortcut="⌥ F" onSelect={actions.onToggleFavorite}/>
    <LinearMenuItem icon={<LinkIcon/>} label="Copy URL" shortcut="⌘ ⇧ ," onSelect={actions.onCopyUrl}/>
    {release.version && <LinearMenuItem icon={<LinearGlyph name="copy"/>} label="Copy version" onSelect={actions.onCopyVersion}/>}
    <LinearMenuSeparator/>
    <LinearMenuItem icon={<LinearGlyph name="delete"/>} label="Delete" onSelect={actions.onDelete}/>
  </>
}

/** The release "…" button menu (header and side panel). */
export function ReleaseActionsDropdown({ trigger, label, align = 'start', ...props }: { trigger: ReactNode; label?: string; align?: 'start' | 'end'; pipeline: ReleasePipeline; release: Release; favorite: boolean; actions: ReleaseMenuActions }) {
  const { t } = useI18n()
  return <DropdownMenu.Root>
    <DropdownMenu.Trigger asChild>{trigger}</DropdownMenu.Trigger>
    <DropdownMenu.Portal>
      <LinearDropdownMenuContent align={align} label={label ?? t('Release options')} className="flow-release-actions-menu">
        <ReleaseMenuItems {...props}/>
      </LinearDropdownMenuContent>
    </DropdownMenu.Portal>
  </DropdownMenu.Root>
}

/** Right-click menu around a release row. */
export function ReleaseRowContextMenu({ children, ...props }: { children: ReactNode; pipeline: ReleasePipeline; release: Release; favorite: boolean; actions: ReleaseMenuActions }) {
  const { t } = useI18n()
  return <LinearContextMenuRoot modal={false}>
    <LinearContextMenuTrigger asChild>{children}</LinearContextMenuTrigger>
    <LinearContextMenuPortal>
      <LinearMenuContent label={t('Release options')} className="flow-release-actions-menu">
        <ReleaseMenuItems {...props}/>
      </LinearMenuContent>
    </LinearContextMenuPortal>
  </LinearContextMenuRoot>
}

export type PipelineMenuActions = {
  onCreateRelease: () => void
  onToggleFavorite: () => void
  onCopyUrl: () => void
  onOpenSettings: () => void
  onToggleArchive: () => void
  onOpenDeleted: () => void
}

export function PipelineMenuItems({ pipeline, favorite, archive, canManage, actions }: { pipeline: ReleasePipeline; favorite: boolean; archive?: boolean; canManage: boolean; actions: PipelineMenuActions }) {
  return <>
    {pipeline.type === 'scheduled' && canManage && <>
      <LinearMenuItem icon={<ReleasesIcon/>} label="Create new release" shortcut="N then R" onSelect={actions.onCreateRelease}/>
      <LinearMenuSeparator/>
    </>}
    <LinearMenuItem icon={<LinearGlyph name="favorite"/>} label={favorite ? 'Remove from favorites' : 'Favorite'} shortcut="⌥ F" onSelect={actions.onToggleFavorite}/>
    <LinearMenuItem icon={<LinkIcon/>} label="Copy URL" shortcut="⌘ ⇧ ," onSelect={actions.onCopyUrl}/>
    <LinearMenuSeparator/>
    {canManage && <LinearMenuItem icon={<SettingsGearIcon/>} label="Pipeline settings" onSelect={actions.onOpenSettings}/>}
    <LinearMenuItem icon={archive ? <ReleasesIcon/> : <LinearGlyph name="agentSessionDismissed"/>} label={archive ? 'View releases' : 'Open archive'} onSelect={actions.onToggleArchive}/>
    <LinearMenuItem icon={<LinearGlyph name="delete"/>} label="View recently deleted releases" onSelect={actions.onOpenDeleted}/>
  </>
}

export function PipelineActionsDropdown({ trigger, ...props }: { trigger: ReactNode; pipeline: ReleasePipeline; favorite: boolean; archive?: boolean; canManage: boolean; actions: PipelineMenuActions }) {
  const { t } = useI18n()
  return <DropdownMenu.Root>
    <DropdownMenu.Trigger asChild>{trigger}</DropdownMenu.Trigger>
    <DropdownMenu.Portal>
      <LinearDropdownMenuContent label={t('Pipeline options')} className="flow-release-actions-menu">
        <PipelineMenuItems {...props}/>
      </LinearDropdownMenuContent>
    </DropdownMenu.Portal>
  </DropdownMenu.Root>
}

export function PipelineRowContextMenu({ children, ...props }: { children: ReactNode; pipeline: ReleasePipeline; favorite: boolean; canManage: boolean; actions: PipelineMenuActions }) {
  const { t } = useI18n()
  return <LinearContextMenuRoot modal={false}>
    <LinearContextMenuTrigger asChild>{children}</LinearContextMenuTrigger>
    <LinearContextMenuPortal>
      <LinearMenuContent label={t('Pipeline options')} className="flow-release-actions-menu">
        <PipelineMenuItems {...props}/>
      </LinearMenuContent>
    </LinearContextMenuPortal>
  </LinearContextMenuRoot>
}
