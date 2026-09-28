/**
 * LS-0783 useWidgetInsight — hover + drill + fullscreen hydrate for dashboard widgets.
 */

import { useCallback, useMemo, useState } from 'react'
import type { DashboardWidget, DashboardInsightConfig } from '@/types/flow'
import { ChartHover, type ChartHoverOrigin } from './chart-hover'
import type { InsightTarget } from '@/components/issue-explorer/insight-interaction'

export type WidgetInsightFilters = {
  teamIds?: string[]
  stateIds?: string[]
  assigneeIds?: string[]
  labelIds?: string[]
}

export type WidgetInsightDrill = {
  pathname?: string
  search?: string
  filters: WidgetInsightFilters
  target?: InsightTarget
}

function readSearchParam(search: string, key: string): string | null {
  try {
    return new URLSearchParams(search.startsWith('?') ? search.slice(1) : search).get(key)
  } catch {
    return null
  }
}

export function parseInsightSearchParam(search: string): WidgetInsightFilters | undefined {
  const raw = readSearchParam(search, 'insightFilter') ?? readSearchParam(search, 'insights')
  if (!raw) return undefined
  try {
    const parsed = JSON.parse(raw) as WidgetInsightFilters
    if (!parsed || typeof parsed !== 'object') return undefined
    return parsed
  } catch {
    return undefined
  }
}

export function getDashboardWidgetIssueFilter(widget: Pick<DashboardWidget, 'config'>): WidgetInsightFilters {
  const config = widget.config as DashboardInsightConfig & WidgetInsightFilters
  return {
    teamIds: config.teamIds,
    stateIds: config.stateIds,
    assigneeIds: config.assigneeIds,
    labelIds: config.labelIds,
  }
}

export function buildInsightDrillPath(
  workspaceIssuesPath: string,
  filters: WidgetInsightFilters,
  extras?: { fullscreen?: boolean; widgetId?: string },
): WidgetInsightDrill {
  const params = new URLSearchParams()
  params.set('insightFilter', JSON.stringify(filters))
  if (extras?.fullscreen) params.set('insights', '1')
  if (extras?.widgetId) params.set('widget', extras.widgetId)
  const search = `?${params.toString()}`
  return { pathname: workspaceIssuesPath, search, filters }
}

export function useWidgetInsight(options: {
  widget: Pick<DashboardWidget, 'id' | 'config'>
  workspaceIssuesPath: string
  locationSearch?: string
  origin?: ChartHoverOrigin
}) {
  const { widget, workspaceIssuesPath, locationSearch = '', origin = 'widget' } = options
  const hover = useMemo(() => new ChartHover(origin), [origin])
  const [hovered, setHovered] = useState<InsightTarget | undefined>()
  const [drill, setDrill] = useState<InsightTarget | undefined>()

  const baseFilters = useMemo(() => getDashboardWidgetIssueFilter(widget), [widget])
  const hydrated = useMemo(() => parseInsightSearchParam(locationSearch), [locationSearch])
  const filters = hydrated ?? baseFilters

  const onHover = useCallback((target?: InsightTarget) => {
    hover.setHover(target, origin)
    setHovered(target)
  }, [hover, origin])

  const onDrill = useCallback((target?: InsightTarget) => {
    if (target) hover.toggleDrill(target)
    else hover.setDrill(undefined)
    setDrill(hover.state.drill)
  }, [hover])

  const explore = useCallback((target?: InsightTarget): WidgetInsightDrill => {
    const nextFilters = { ...filters }
    if (target?.slice) nextFilters.stateIds = [target.slice]
    return buildInsightDrillPath(workspaceIssuesPath, nextFilters, { widgetId: widget.id })
  }, [filters, widget.id, workspaceIssuesPath])

  const fullscreen = useCallback((): WidgetInsightDrill => {
    return buildInsightDrillPath(workspaceIssuesPath, filters, { fullscreen: true, widgetId: widget.id })
  }, [filters, widget.id, workspaceIssuesPath])

  const reset = useCallback(() => {
    hover.reset()
    setHovered(undefined)
    setDrill(undefined)
  }, [hover])

  return {
    hover,
    hovered,
    drill,
    filters,
    highlight: hover.highlight.bind(hover),
    onHover,
    onDrill,
    explore,
    fullscreen,
    reset,
  }
}
