import { describe, expect, it } from 'vitest'
import { TREE_MENU_MAX_WIDTH, TREE_MENU_MIN_WIDTH, TREE_ROW_END_PADDING, TREE_ROW_END_PADDING_WITH_TICK, treeMenuWidth } from './tree-menu-width'

describe('tree menu width', () => {
  it('hugs the widest row rounded up to a whole pixel, plus the 1px border', () => {
    // "Flow parity sibling" + "FPS": the name ends at 184.38 from the row edge and the key is 24.37 wide.
    const keyEnd = 184.38 + 24.37
    expect(treeMenuWidth([keyEnd + TREE_ROW_END_PADDING])).toBe(228) // create-team Parent team
    expect(treeMenuWidth([keyEnd + TREE_ROW_END_PADDING_WITH_TICK])).toBe(249) // Team property, create-issue team
    expect(treeMenuWidth([keyEnd + 22 + TREE_ROW_END_PADDING])).toBe(250) // invite members, checkbox rows
  })
  it('never goes below 175px or above 401px (400px rows), and an unmeasured menu has no width', () => {
    expect(treeMenuWidth([100, 120])).toBe(TREE_MENU_MIN_WIDTH)
    expect(treeMenuWidth([900])).toBe(TREE_MENU_MAX_WIDTH)
    expect(TREE_MENU_MAX_WIDTH).toBe(401)
    expect(treeMenuWidth([900], 320)).toBe(320)
    expect(treeMenuWidth([])).toBeUndefined()
  })
})
