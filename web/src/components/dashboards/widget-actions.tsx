/**
 * LS-0653 WidgetActions — Edit / Duplicate / Delete(confirm+undo) / Copy-to-dashboard.
 */

import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Copy, Filter, LayoutDashboard, Plus, Trash2 } from 'lucide-react'
import { useMemo, useState, type ReactNode } from 'react'
import { toast } from 'sonner'
import { confirmAction } from '@/components/ui/action-dialog-service'
import { DropdownMenuContent } from '@/components/ui/dropdown-menu'
import { useI18n } from '@/i18n/i18n'
import type { Dashboard, DashboardWidget } from '@/types/flow'
import './widget-actions.css'

export type DashboardPickerGroup = {
  id: string
  label: string
  dashboards: Array<Pick<Dashboard, 'id' | 'name' | 'visibility' | 'teamIds' | 'updatedAt'>>
}

export function insightDefaultTitle(config: DashboardWidget['config']): string {
  const measure = String(config.measure ?? 'issue_count').replaceAll('_', ' ')
  const slice = config.slice && config.slice !== 'none' ? String(config.slice) : undefined
  const segment = config.segment && config.segment !== 'none' ? String(config.segment) : undefined
  if (slice && segment) return `${measure} by ${slice} and ${segment}`
  if (slice) return `${measure} by ${slice}`
  return measure.replace(/\b\w/g, char => char.toUpperCase())
}

export function formatOriginDescription(sourceName: string): string {
  return `Added from dashboard ${sourceName}`
}

export function groupDashboardsForCopy(options: {
  dashboards: Dashboard[]
  currentDashboardId?: string
  currentTeamId?: string
  recentIds?: string[]
}): DashboardPickerGroup[] {
  const { dashboards, currentDashboardId, currentTeamId, recentIds = [] } = options
  const others = dashboards.filter(item => item.id !== currentDashboardId)
  const recent = recentIds
    .map(id => others.find(item => item.id === id))
    .filter(Boolean) as Dashboard[]
  const team = currentTeamId
    ? others.filter(item => item.visibility === 'team' && item.teamIds.includes(currentTeamId) && !recent.some(r => r.id === item.id))
    : []
  const workspace = others.filter(
    item => item.visibility === 'workspace' && !recent.some(r => r.id === item.id) && !team.some(t => t.id === item.id),
  )
  const rest = others.filter(
    item => !recent.some(r => r.id === item.id) && !team.some(t => t.id === item.id) && !workspace.some(w => w.id === item.id),
  )
  const groups: DashboardPickerGroup[] = []
  if (recent.length) groups.push({ id: 'recent', label: 'Recent', dashboards: recent })
  if (team.length) groups.push({ id: 'team', label: 'Current team', dashboards: team })
  if (workspace.length) groups.push({ id: 'workspace', label: 'Workspace', dashboards: workspace })
  if (rest.length) groups.push({ id: 'other', label: 'Other dashboards', dashboards: rest })
  return groups
}

export function cloneWidget(widget: DashboardWidget, position: number): DashboardWidget {
  return {
    ...widget,
    id: typeof crypto !== 'undefined' && 'randomUUID' in crypto
      ? crypto.randomUUID()
      : `widget_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`,
    position,
    title: widget.title,
  }
}

export function WidgetActionsMenu({
  widget,
  dashboard,
  dashboards = [],
  recentDashboardIds = [],
  onEdit,
  onDuplicate,
  onDelete,
  onCopyToDashboard,
  onCreateDashboard,
  trigger,
}: {
  widget: DashboardWidget
  dashboard?: Pick<Dashboard, 'id' | 'name' | 'teamIds'>
  dashboards?: Dashboard[]
  recentDashboardIds?: string[]
  onEdit: () => void
  onDuplicate: () => void | Promise<void>
  onDelete: () => void | Promise<void>
  onCopyToDashboard: (dashboardId: string) => void | Promise<void>
  onCreateDashboard?: () => void | Promise<void>
  trigger?: ReactNode
}) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const groups = useMemo(
    () =>
      groupDashboardsForCopy({
        dashboards,
        currentDashboardId: dashboard?.id,
        currentTeamId: dashboard?.teamIds?.[0],
        recentIds: recentDashboardIds,
      }),
    [dashboards, dashboard, recentDashboardIds],
  )
  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase()
    if (!needle) return groups
    return groups
      .map(group => ({
        ...group,
        dashboards: group.dashboards.filter(item => item.name.toLowerCase().includes(needle)),
      }))
      .filter(group => group.dashboards.length)
  }, [groups, query])

  const remove = async () => {
    const confirmed = await confirmAction(`${t('Delete the insight')} "${widget.title}"?`, {
      confirmLabel: t('Delete insight'),
    })
    if (!confirmed) return
    await onDelete()
  }

  return (
    <DropdownMenu.Root onOpenChange={open => { if (!open) setQuery('') }}>
      <DropdownMenu.Trigger asChild>
        {trigger ?? (
          <button aria-label={t('Open insight menu')} type="button" className="widget-actions-trigger">
            ···
          </button>
        )}
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenuContent align="end" className="dashboard-menu widget-actions-menu" sideOffset={4} data-flow-motion="floating">
          <DropdownMenu.Item onSelect={onEdit}>
            <Filter />
            {t('Edit widget')}
          </DropdownMenu.Item>
          <DropdownMenu.Item
            onSelect={() => {
              void onDuplicate()
              toast.success(t('Insight duplicated'))
            }}
          >
            <Copy />
            {t('Duplicate widget')}
          </DropdownMenu.Item>
          <DropdownMenu.Sub>
            <DropdownMenu.SubTrigger>
              <LayoutDashboard />
              {t('Copy to dashboard…')}
            </DropdownMenu.SubTrigger>
            <DropdownMenu.Portal>
              <DropdownMenu.SubContent data-flow-motion="floating" className="dashboard-menu widget-actions-copy" sideOffset={6}>
                <label className="widget-actions-search">
                  <input
                    aria-label={t('Search dashboards')}
                    placeholder={t('Search dashboards')}
                    value={query}
                    onChange={event => setQuery(event.target.value)}
                    onKeyDown={event => event.stopPropagation()}
                  />
                </label>
                {filtered.map(group => (
                  <DropdownMenu.Group key={group.id}>
                    <DropdownMenu.Label>{t(group.label)}</DropdownMenu.Label>
                    {group.dashboards.map(item => (
                      <DropdownMenu.Item
                        key={item.id}
                        onSelect={() => {
                          void onCopyToDashboard(item.id)
                          toast.success(t('Saved to dashboard'))
                        }}
                      >
                        {item.name}
                      </DropdownMenu.Item>
                    ))}
                  </DropdownMenu.Group>
                ))}
                {onCreateDashboard && (
                  <>
                    <DropdownMenu.Separator />
                    <DropdownMenu.Item
                      onSelect={() => {
                        void onCreateDashboard()
                      }}
                    >
                      <Plus />
                      {t('New dashboard')}
                    </DropdownMenu.Item>
                  </>
                )}
                {!filtered.length && !onCreateDashboard && (
                  <div className="widget-actions-empty">{t('No dashboards')}</div>
                )}
              </DropdownMenu.SubContent>
            </DropdownMenu.Portal>
          </DropdownMenu.Sub>
          <DropdownMenu.Separator />
          <DropdownMenu.Item className="danger" onSelect={() => { void remove() }}>
            <Trash2 />
            {t('Delete insight')}
          </DropdownMenu.Item>
        </DropdownMenuContent>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  )
}
