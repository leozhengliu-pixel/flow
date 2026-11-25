import { describe, expect, it } from 'vitest'
import type { PropertyOption } from './property-menu'
import { labelMenuModel, labelSearchWidth, type LabelMenuEntry } from './label-menu-model'

const teamId = 'team_1'
const options: PropertyOption[] = [
  { id: 'bug', label: 'Bug', color: '#eb5757', scope: 'Workspace' },
  { id: 'defect', label: '缺陷', color: '#26b5ce', scope: 'Workspace', groupId: 'kind', groupLabel: '事务类型与优先级分类标签组（客户可见）', groupColor: '#26b5ce' },
  { id: 'request', label: '功能请求', color: '#26b5ce', scope: 'Workspace', groupId: 'kind', groupLabel: '事务类型与优先级分类标签组（客户可见）', groupColor: '#26b5ce' },
  { id: 'sev1', label: 'Sev 1', color: '#eb5757', scope: 'Workspace', groupId: 'sev', groupLabel: 'Customer-facing severity classification' },
  { id: 'team-a', label: '团队A', color: '#4cb782', scope: teamId, groupId: 'team-group', groupLabel: '团队范围的标签组' },
  { id: 'team-plain', label: 'Team only', color: '#4cb782', scope: teamId },
  { id: 'docs', label: 'Documentation', color: '#4cb782', scope: 'Workspace' },
]
const names = (entries: LabelMenuEntry[]) => entries.map(entry => entry.type === 'group' ? `group:${entry.group.label}` : entry.groupName === 'path' ? `${entry.option.groupLabel} › ${entry.option.label}` : entry.option.label)
const fuzzy = (query: string) => {
  const needle = query.trim().toLocaleLowerCase()
  return { query, matches: (option: PropertyOption) => `${option.label} ${option.groupLabel ?? ''}`.toLocaleLowerCase().includes(needle), groupMatches: (name: string) => name.toLocaleLowerCase().includes(needle) }
}

describe('labelMenuModel', () => {
  it('collapses each label group into one row and lists groups before labels, team ones apart', () => {
    const model = labelMenuModel(options, new Set())
    expect(names(model.selected)).toEqual([])
    expect(names(model.team)).toEqual(['group:团队范围的标签组', 'Team only'])
    expect(names(model.workspace)).toEqual(['group:事务类型与优先级分类标签组（客户可见）', 'group:Customer-facing severity classification', 'Bug', 'Documentation'])
    const kind = model.workspace[0]
    expect(kind.type === 'group' && kind.group).toMatchObject({ id: 'kind', color: '#26b5ce', checked: false, teamScoped: false })
    expect(kind.type === 'group' && kind.group.options.map(option => option.id)).toEqual(['defect', 'request'])
  })

  it('moves a group with a selected label to the selected rows and names that label', () => {
    const model = labelMenuModel(options, new Set(['request', 'bug']))
    expect(names(model.selected)).toEqual(['group:事务类型与优先级分类标签组（客户可见）', 'Bug'])
    const group = model.selected[0]
    expect(group.type === 'group' && group.group).toMatchObject({ checked: true, detail: '功能请求' })
    expect(names(model.workspace)).toEqual(['group:Customer-facing severity classification', 'Documentation'])
  })

  it('keeps a group row only when its name matches a search', () => {
    expect(names(labelMenuModel(options, new Set(), fuzzy('se')).workspace)).toEqual(['group:Customer-facing severity classification'])
  })

  it('lists the matching labels of groups as "Group › Label" from three characters', () => {
    expect(names(labelMenuModel(options, new Set(), fuzzy('sev')).workspace)).toEqual(['group:Customer-facing severity classification', 'Customer-facing severity classification › Sev 1'])
    expect(names(labelMenuModel(options, new Set(), fuzzy('Se')).workspace)).not.toContain('Customer-facing severity classification › Sev 1')
  })

  it('counts a CJK character twice, so a two-character Chinese word searches inside groups', () => {
    expect(labelSearchWidth('缺陷')).toBe(4)
    expect(labelSearchWidth('缺')).toBe(2)
    expect(labelSearchWidth(' ab ')).toBe(2)
    expect(names(labelMenuModel(options, new Set(), fuzzy('缺陷')).workspace)).toEqual(['事务类型与优先级分类标签组（客户可见） › 缺陷'])
  })

  it('keeps a selected label found by a search in the selected rows', () => {
    const model = labelMenuModel(options, new Set(['defect']), fuzzy('缺陷'))
    expect(names(model.selected)).toEqual(['事务类型与优先级分类标签组（客户可见） › 缺陷'])
    expect(names(model.workspace)).toEqual([])
  })
})
