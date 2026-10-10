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
import { groupDashboardsForCopy } from './widget-actions-model'
import './widget-actions.css'

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
