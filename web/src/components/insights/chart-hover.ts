/**
 * LS-0105 ChartHover — shared hover/filter model for explorer + dashboard widgets.
 * Adapts Flow InsightTarget / insightHighlight semantics with an origin tag.
 */

import {
  insightHighlight,
  sameInsightTarget,
  type InsightTarget,
} from '@/components/issue-explorer/insight-interaction'

export type ChartHoverOrigin =
  | 'graph'
  | 'table'
  | 'distribution'
  | 'segment'
  | 'latency'
  | 'widget'

export type ChartHoverState = {
  origin: ChartHoverOrigin
  hover?: InsightTarget
  drill?: InsightTarget
}

export type ChartHoverListener = (state: ChartHoverState) => void

/** Shared ChartHover store — one instance per explorer/widget surface. */
export class ChartHover {
  private origin: ChartHoverOrigin
  private hover?: InsightTarget
  private drill?: InsightTarget
  private listeners = new Set<ChartHoverListener>()

  constructor(origin: ChartHoverOrigin = 'graph') {
    this.origin = origin
  }

  get state(): ChartHoverState {
    return { origin: this.origin, hover: this.hover, drill: this.drill }
  }

  /** Active highlight prefers transient hover over persistent drill. */
  get active(): InsightTarget | undefined {
    return this.hover ?? this.drill
  }

  get isHovering(): boolean {
    return this.hover !== undefined
  }

  isHoveringRow(slice?: string): boolean {
    return Boolean(this.hover?.slice !== undefined && this.hover.slice === slice)
  }

  isHoveringColumn(segment?: string): boolean {
    return Boolean(this.hover?.segment !== undefined && this.hover.segment === segment)
  }

  isFilteringSegment(segment?: string): boolean {
    const target = this.active
    return Boolean(target?.segment !== undefined && target.segment === segment)
  }

  isFilteringDimension(slice?: string): boolean {
    const target = this.active
    return Boolean(target?.slice !== undefined && target.slice === slice)
  }

  isFilteringCell(cell: InsightTarget): boolean {
    return insightHighlight(this.active, cell) === 'strong'
  }

  setOrigin(origin: ChartHoverOrigin) {
    if (this.origin === origin) return
    this.origin = origin
    this.emit()
  }

  setHover(target?: InsightTarget, origin?: ChartHoverOrigin) {
    if (origin) this.origin = origin
    if (sameInsightTarget(this.hover, target)) return
    this.hover = target
    this.emit()
  }

  clearHover() {
    if (this.hover === undefined) return
    this.hover = undefined
    this.emit()
  }

  setDrill(target?: InsightTarget) {
    if (sameInsightTarget(this.drill, target)) return
    this.drill = target
    this.emit()
  }

  toggleDrill(target: InsightTarget) {
    this.setDrill(sameInsightTarget(this.drill, target) ? undefined : target)
  }

  reset() {
    if (!this.hover && !this.drill) return
    this.hover = undefined
    this.drill = undefined
    this.emit()
  }

  /** Adapter for existing insightHighlight consumers. */
  highlight(cell: InsightTarget): 'strong' | 'weak' | undefined {
    return insightHighlight(this.active, cell)
  }

  subscribe(listener: ChartHoverListener): () => void {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  private emit() {
    const snapshot = this.state
    for (const listener of this.listeners) listener(snapshot)
  }
}

export function createChartHover(origin: ChartHoverOrigin = 'graph') {
  return new ChartHover(origin)
}
