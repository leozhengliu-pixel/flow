import { useMemo, useState } from 'react'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Archive, Copy, MoreHorizontal, Search, Star } from 'lucide-react'
import { findFavorite, toggleFavoriteFor } from '@/lib/favorites'
import { updateTeamLabel, updateWorkspaceLabel } from '@/lib/api'
import type { BootstrapData, IssueLabel } from '@/types/flow'
import { useI18n } from '@/i18n/i18n'
import './label-page-toolbar.css'

export type LabelPageToolbarProps = {
  data: BootstrapData
  label: IssueLabel
  resourceType: 'issue' | 'project' | 'initiative'
  search: string
  onSearchChange: (value: string) => void
  /** Issue-type only: filter to triage/backlog-untriaged when enabled. */
  triageOnly: boolean
  onTriageOnlyChange: (value: boolean) => void
  onReload?: () => Promise<void> | void
  onArchived?: () => void
}

/**
 * Label browse page chrome (LS-0370): icon + title + favorite + options,
 * issue-type enableTriageOption, progressive inline search.
 */
export function LabelPageToolbar({
  data,
  label,
  resourceType,
  search,
  onSearchChange,
  triageOnly,
  onTriageOnlyChange,
  onReload,
  onArchived,
}: LabelPageToolbarProps) {
  const { t } = useI18n()
  const [searchOpen, setSearchOpen] = useState(Boolean(search))
  const favorite = Boolean(findFavorite(data.favorites, data.viewer.id, 'label', label.id) || label.favorite)
  const showArchived = Boolean(label.archivedAt)
  const enableTriageOption = resourceType === 'issue' && triageOptionAvailable(data)

  const archive = async () => {
    const next = showArchived ? '' : new Date().toISOString()
    const teamId = label.scope && label.scope !== 'Workspace' ? label.scope : undefined
    const archivedAt = next // '' clears archive on REST
    if (teamId && data.teams.some((team) => team.id === teamId || team.key === teamId)) {
      const team = data.teams.find((item) => item.id === teamId || item.key === teamId)
      if (team) await updateTeamLabel(team.id, label.id, { archivedAt })
      else await updateWorkspaceLabel(label.id, { archivedAt })
    } else {
      await updateWorkspaceLabel(label.id, { archivedAt })
    }
    await onReload?.()
    if (!showArchived) onArchived?.()
  }

  return (
    <div className="label-page-toolbar">
      <div className="label-page-toolbar__title">
        <span aria-hidden="true" className="label-page-toolbar__icon" style={{ background: label.color }} />
        <div className="label-page-toolbar__heading">
          <h2 data-i18n-ignore>
            {label.name}
            {showArchived ? <small className="label-page-toolbar__archived">{t('Archived')}</small> : null}
          </h2>
          <p>{t('Items with this label')}</p>
        </div>
      </div>
      <div className="label-page-toolbar__actions">
        {(searchOpen || search) && (
          <label className="label-page-toolbar__search">
            <Search size={14} />
            <input
              aria-label={t('Search items')}
              autoFocus={!search}
              placeholder={t('Filter…')}
              value={search}
              onBlur={() => { if (!search) setSearchOpen(false) }}
              onChange={(event) => onSearchChange(event.target.value)}
            />
          </label>
        )}
        {!searchOpen && !search && (
          <button
            aria-label={t('Search')}
            className="label-page-toolbar__icon-btn"
            type="button"
            onClick={() => setSearchOpen(true)}
          >
            <Search size={15} />
          </button>
        )}
        {enableTriageOption && (
          <button
            aria-pressed={triageOnly}
            className={`label-page-toolbar__chip${triageOnly ? ' is-active' : ''}`}
            type="button"
            onClick={() => onTriageOnlyChange(!triageOnly)}
          >
            {t('Triage')}
          </button>
        )}
        <button
          aria-label={favorite ? t('Remove from favorites') : t('Add to favorites')}
          aria-pressed={favorite}
          className={`label-page-toolbar__icon-btn${favorite ? ' is-favorite' : ''}`}
          type="button"
          onClick={() => void toggleFavoriteFor(data, 'label', label.id, undefined, favorite)}
        >
          <Star size={15} fill={favorite ? 'currentColor' : 'none'} />
        </button>
        <DropdownMenu.Root>
          <DropdownMenu.Trigger asChild>
            <button aria-label={t('Label options')} className="label-page-toolbar__icon-btn" type="button">
              <MoreHorizontal size={15} />
            </button>
          </DropdownMenu.Trigger>
          <DropdownMenu.Portal>
            <DropdownMenu.Content
              align="end"
              className="label-page-toolbar__menu"
              data-flow-motion="floating"
              sideOffset={4}
            >
              <DropdownMenu.Item onSelect={() => void navigator.clipboard.writeText(location.href)}>
                <Copy size={14} />
                {t('Copy link')}
              </DropdownMenu.Item>
              <DropdownMenu.Separator />
              <DropdownMenu.Item onSelect={() => void archive().catch(() => undefined)}>
                <Archive size={14} />
                {showArchived ? t('Unarchive') : t('Archive')}
              </DropdownMenu.Item>
            </DropdownMenu.Content>
          </DropdownMenu.Portal>
        </DropdownMenu.Root>
      </div>
    </div>
  )
}

export function triageOptionAvailable(data: BootstrapData) {
  return Object.values(data.teamSettings ?? {}).some((settings) => settings.triageEnabled)
}

export function filterLabelItems<T extends { title?: string; name?: string; identifier?: string; triagedAt?: string; state?: { type: string }; team?: { id: string } }>(
  items: T[],
  opts: { search: string; triageOnly: boolean; resourceType: string; teamSettings?: BootstrapData['teamSettings'] },
) {
  const query = opts.search.trim().toLowerCase()
  return items.filter((item) => {
    if (opts.triageOnly && opts.resourceType === 'issue') {
      const teamId = item.team?.id
      const triageEnabled = teamId ? opts.teamSettings?.[teamId]?.triageEnabled : false
      if (!triageEnabled) return false
      if (item.triagedAt || item.state?.type !== 'backlog') return false
    }
    if (!query) return true
    const hay = `${item.identifier ?? ''} ${item.title ?? ''} ${item.name ?? ''}`.toLowerCase()
    return hay.includes(query)
  })
}

export function useLabelPageChrome(initialSearch = '') {
  const [search, setSearch] = useState(initialSearch)
  const [triageOnly, setTriageOnly] = useState(false)
  return useMemo(
    () => ({ search, setSearch, triageOnly, setTriageOnly }),
    [search, triageOnly],
  )
}
