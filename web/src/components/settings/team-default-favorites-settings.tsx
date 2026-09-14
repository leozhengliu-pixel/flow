import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { ArrowDown, ArrowUp, Circle, FileText, Hash, MoreHorizontal, Plus, RefreshCw, Star, X } from 'lucide-react'
import { toast } from 'sonner'
import { ProjectIcon, StatusIcon, TeamIcon } from '@/components/issue/issue-icons'
import { PropertyMenu, type PropertyOption } from '@/components/property/property-menu'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { FlowTooltip, TooltipProvider } from '@/components/ui/tooltip'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { useI18n } from '@/i18n/i18n'
import { fetchTeamDefaultFavorites, replaceTeamDefaultFavorites } from '@/lib/api'
import type { BootstrapData, Team, TeamDefaultFavorite } from '@/types/flow'
import { SettingsSelect } from './settings-primitives'
import './team-default-favorites-settings.css'

type Favorite = Pick<TeamDefaultFavorite, 'resourceType' | 'resourceId'>
type Props = { data: BootstrapData; team: Team }
const RESOURCE_TYPES = ['issue', 'project', 'team', 'document', 'view'] as const
const TYPE_LABELS: Record<string, string> = { issue: 'Issue', project: 'Project', team: 'Team', document: 'Document', view: 'View' }
const MAX_FAVORITES = 100
const resourceKey = (item: Favorite) => `${item.resourceType}:${item.resourceId}`
const signature = (items: Favorite[]) => JSON.stringify(items.map(resourceKey))

function resourceIcon(type: string): ReactNode {
  switch (type) {
    case 'project': return <ProjectIcon size={16} />
    case 'team': return <TeamIcon size={16} />
    case 'view': return <ViewGlyph />
    case 'document': return <FileText size={16} />
    default: return <Circle size={16} />
  }
}

export function DefaultFavoritesSettings(props: Props) {
  // Switching teams must discard local edits and ignore the previous team's requests.
  return <DefaultFavoritesEditor key={`${props.data.workspace.urlKey}:${props.team.id}`} {...props} />
}

function DefaultFavoritesEditor({ data, team }: Props) {
  const { t } = useI18n()
  const [items, setItems] = useState<Favorite[]>([])
  const [saved, setSaved] = useState<Favorite[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState(false)
  const [saveError, setSaveError] = useState('')
  const [attempt, setAttempt] = useState(0)
  const [saving, setSaving] = useState(false)
  const [manualOpen, setManualOpen] = useState(false)
  const [manualType, setManualType] = useState('issue')
  const [manualId, setManualId] = useState('')
  const mounted = useRef(true)

  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false }
  }, [])

  useEffect(() => {
    let active = true
    setLoading(true)
    setLoadError(false)
    void fetchTeamDefaultFavorites(team.id).then(result => {
      if (!active) return
      setItems(result.items)
      setSaved(result.items)
    }).catch(() => {
      if (active) setLoadError(true)
    }).finally(() => {
      if (active) setLoading(false)
    })
    return () => { active = false }
  }, [team.id, attempt])

  const resources = useMemo(() => {
    const entries: (PropertyOption & { favorite: Favorite })[] = []
    const add = (resourceType: string, resourceId: string, label: string, icon: ReactNode, description?: string) => {
      entries.push({ id: `${resourceType}:${resourceId}`, favorite: { resourceType, resourceId }, label, icon, description, end: description, i18nIgnore: true, keywords: `${t(TYPE_LABELS[resourceType])} ${resourceId} ${description ?? ''}` })
    }
    for (const issue of data.issues) add('issue', issue.id, issue.title, <StatusIcon state={issue.state} size={16} />, issue.identifier)
    for (const project of data.projects) add('project', project.id, project.name, <ViewGlyph icon={project.icon || 'Project'} color={project.color} />, t('Project'))
    for (const view of data.savedViews ?? []) add('view', view.id, view.name, <ViewGlyph icon={view.icon} color={view.color} />, t('View'))
    for (const document of data.documents ?? []) add('document', document.id, document.title, <FileText size={16} />, t('Document'))
    for (const item of data.teams) if (!item.retiredAt) add('team', item.id, item.name, <TeamIcon team={item} size={16} />, t('Team'))
    return new Map(entries.map(item => [item.id, item]))
  }, [data.issues, data.projects, data.savedViews, data.documents, data.teams, t])

  const existing = useMemo(() => new Set(items.map(resourceKey)), [items])
  const options = useMemo(() => Array.from(resources.values()).map(item => ({ ...item, disabled: existing.has(item.id) })), [resources, existing])
  const dirty = signature(items) !== signature(saved)
  const disabled = loading || loadError || saving
  const full = items.length >= MAX_FAVORITES
  const manualDuplicate = existing.has(resourceKey({ resourceType: manualType, resourceId: manualId.trim() }))

  const add = (item: Favorite) => {
    if (disabled || full || existing.has(resourceKey(item))) return
    setItems(current => [...current, { resourceType: item.resourceType, resourceId: item.resourceId }])
    setSaveError('')
  }
  const move = (index: number, direction: number) => {
    setItems(current => {
      const next = [...current]
      const target = index + direction
      if (target < 0 || target >= next.length) return current
      ;[next[index], next[target]] = [next[target], next[index]]
      return next
    })
    setSaveError('')
  }
  const save = async () => {
    if (disabled || !dirty) return
    setSaving(true)
    setSaveError('')
    try {
      const result = await replaceTeamDefaultFavorites(team.id, items.map(({ resourceType, resourceId }) => ({ resourceType, resourceId })))
      if (!mounted.current) return
      setItems(result.items)
      setSaved(result.items)
      toast.success(t('Default favorites saved'))
    } catch (error) {
      if (mounted.current) setSaveError(error instanceof Error ? error.message : t('Could not save default favorites'))
    } finally {
      if (mounted.current) setSaving(false)
    }
  }

  return <TooltipProvider delayDuration={300}>
    <div className="team-defaults">
      <p className="team-defaults-description">{t('Resources added here appear in Favorites for every current and future member of this team. Members can hide them without changing this team configuration.')}</p>
      <div className="team-defaults-toolbar">
        <h2>{t('Resources')}<span aria-label={t('Favorite count')}>{loading ? '' : items.length}</span></h2>
        <div className="team-defaults-actions">
          <PropertyMenu label={t('Add default favorite')} searchPlaceholder={t('Search resources…')} options={options}
            onChange={id => { const item = resources.get(id); if (item) add(item.favorite) }} align="end" surfaceClassName="team-defaults-picker"
            customTrigger={({ open, openMenu }) => <button type="button" className="settings-action" aria-expanded={open} aria-haspopup="dialog" disabled={disabled || full} onClick={() => openMenu()}><Plus size={14} />{t('Add favorite')}</button>} />
          <DropdownMenu><FlowTooltip label={t('More options')}><DropdownMenuTrigger asChild><button type="button" className="settings-icon-action" aria-label={t('More options')} disabled={disabled || full}><MoreHorizontal size={16} /></button></DropdownMenuTrigger></FlowTooltip>
            <DropdownMenuContent className="team-defaults-menu" align="end"><DropdownMenuItem onSelect={() => { setManualId(''); setManualOpen(true) }}><Hash size={14} />{t('Add by ID')}</DropdownMenuItem></DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>
      {loading ? <div className="team-defaults-state" role="status">{t('Loading…')}</div>
        : loadError ? <div className="team-defaults-state" role="alert"><p>{t('Could not load default favorites')}</p><button type="button" className="settings-action" onClick={() => setAttempt(current => current + 1)}><RefreshCw size={14} />{t('Try again')}</button></div>
          : items.length === 0 ? <div className="team-defaults-state"><Star size={24} aria-hidden="true" /><p>{t('No default favorites configured.')}</p></div>
            : <ul className="team-defaults-list" aria-label={t('Default favorites')}>
              {items.map((item, index) => {
                const key = resourceKey(item)
                const resource = resources.get(key)
                const title = resource?.label ?? item.resourceId
                return <li className="team-defaults-row" key={key}>
                  <span className="team-defaults-icon" aria-hidden="true">{resource?.icon ?? resourceIcon(item.resourceType)}</span>
                  <div className="team-defaults-copy"><span className="team-defaults-name" title={title} data-i18n-ignore>{title}</span><span className="team-defaults-meta">{t(TYPE_LABELS[item.resourceType] ?? item.resourceType)}{item.resourceType === 'issue' && resource?.description && <span data-i18n-ignore>{resource.description}</span>}</span></div>
                  <DropdownMenu><FlowTooltip label={t('More options')}><DropdownMenuTrigger asChild><button type="button" className="settings-icon-action team-defaults-row-action" aria-label={`${t('More options')}: ${title}`} disabled={saving}><MoreHorizontal size={16} /></button></DropdownMenuTrigger></FlowTooltip>
                    <DropdownMenuContent className="team-defaults-menu" align="end">
                      <DropdownMenuItem disabled={index === 0} onSelect={() => move(index, -1)}><ArrowUp size={14} />{t('Move up')}</DropdownMenuItem>
                      <DropdownMenuItem disabled={index === items.length - 1} onSelect={() => move(index, 1)}><ArrowDown size={14} />{t('Move down')}</DropdownMenuItem>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem onSelect={() => { setItems(current => current.filter(value => resourceKey(value) !== key)); setSaveError('') }}><X size={14} />{t('Remove')}</DropdownMenuItem>
                    </DropdownMenuContent>
                  </DropdownMenu>
                </li>
              })}
            </ul>}
      {full && <p className="team-defaults-note" role="status">{t('Up to 100 default favorites are allowed.')}</p>}
      {saveError && <p className="team-defaults-error" role="alert">{t(saveError)}</p>}
      {dirty && <footer className="team-defaults-footer"><span>{t('Unsaved changes')}</span><div className="team-defaults-actions"><button type="button" className="settings-action" disabled={saving} onClick={() => { setItems(saved); setSaveError('') }}>{t('Cancel')}</button><button type="button" className="settings-action primary" disabled={disabled} onClick={() => void save()}>{saving ? t('Saving…') : t('Save changes')}</button></div></footer>}
    </div>
    <Dialog open={manualOpen} onOpenChange={setManualOpen}><DialogContent className="team-defaults-dialog" closeLabel={t('Close')} aria-describedby={undefined}>
      <DialogTitle>{t('Add by ID')}</DialogTitle>
      <form onSubmit={event => { event.preventDefault(); if (disabled || full || !manualId.trim() || manualDuplicate) return; add({ resourceType: manualType, resourceId: manualId.trim() }); setManualOpen(false) }}>
        <label><span>{t('Resource type')}</span><SettingsSelect label={t('Resource type')} value={manualType} onChange={setManualType} options={RESOURCE_TYPES.map(type => ({ value: type, label: t(TYPE_LABELS[type]), icon: resourceIcon(type) }))} /></label>
        <label><span>{t('Resource ID')}</span><input className="settings-input" autoFocus value={manualId} onChange={event => setManualId(event.target.value)} autoComplete="off" spellCheck={false} /></label>
        {manualDuplicate && <p role="alert" className="team-defaults-error">{t('This resource is already added.')}</p>}
        <footer><button type="button" className="settings-action" onClick={() => setManualOpen(false)}>{t('Cancel')}</button><button type="submit" className="settings-action primary" disabled={disabled || full || !manualId.trim() || manualDuplicate}>{t('Add favorite')}</button></footer>
      </form>
    </DialogContent></Dialog>
  </TooltipProvider>
}
