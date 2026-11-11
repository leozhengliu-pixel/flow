/**
 * Width of a team-tree menu (create-team Parent team, Team property, create-issue team, invite members team select).
 * The menu hugs its widest row: the row widths are rounded up to whole pixels and limited to 400px (the list's item
 * maximum), the menu is never narrower than 175px, and its 0.5px border adds 1px around the list, so the outer width
 * is at most 401px.
 */
const TREE_MENU_BORDER = 1
export const TREE_MENU_MIN_WIDTH = 175
export const TREE_MENU_MAX_ROW_WIDTH = 400
export const TREE_MENU_MAX_WIDTH = TREE_MENU_MAX_ROW_WIDTH + TREE_MENU_BORDER

/** Right edge of the content after a row's key: 18px of padding, or, where the selected team ends in a tick, a reserved
 *  tick column (8px gap, 16px tick, 1px margin) plus 14px of padding on every row. */
export const TREE_ROW_END_PADDING = 18
export const TREE_ROW_END_PADDING_WITH_TICK = 39

/** The menu's outer width for its rows' natural (unrounded) widths, or `undefined` while nothing is measured. */
export function treeMenuWidth(rowWidths: readonly number[], limit = TREE_MENU_MAX_WIDTH): number | undefined {
  if (!rowWidths.length) return undefined
  const widest = Math.max(...rowWidths)
  if (!Number.isFinite(widest)) return undefined
  return Math.min(Math.max(Math.min(Math.ceil(widest), TREE_MENU_MAX_ROW_WIDTH) + TREE_MENU_BORDER, TREE_MENU_MIN_WIDTH), limit)
}

/** The horizontal scale the open animation currently applies (the menu grows from 0.98 to 1). */
export function currentScaleX(element: HTMLElement): number {
  const value = getComputedStyle(element).scale
  const x = value && value !== 'none' ? Number.parseFloat(value) : 1
  return Number.isFinite(x) && x > 0 ? x : 1
}

/** The natural width of a hugging menu element (its border box including the 1px border), divided by the open
 *  animation's current scale so the number is the settled one. */
export function measureNaturalWidth(element: HTMLElement): number {
  const { minWidth } = element.style
  element.style.minWidth = '0px'
  const width = element.getBoundingClientRect().width / currentScaleX(element)
  element.style.minWidth = minWidth
  return width
}

/** Pins `element` to the integer width a hugging tree menu takes; returns the width applied. */
export function fitTreeMenu(element: HTMLElement, limit?: number): number | undefined {
  const natural = measureNaturalWidth(element)
  if (!natural) return undefined
  const width = treeMenuWidth([natural - TREE_MENU_BORDER], limit)
  if (width !== undefined) element.style.minWidth = `${width}px`
  return width
}
