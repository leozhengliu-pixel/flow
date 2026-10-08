import { toast } from 'sonner'
import type { LinearMenuOption } from '@/components/ui/row-context-menu'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import type { Initiative, InitiativeMutationInput } from '@/types/flow'
import { MAX_INITIATIVE_NESTING, type initiativeGraph } from './initiative-hierarchy'

type Graph = ReturnType<typeof initiativeGraph>
type Translate = (source: string) => string
export type UpdateInitiative = (id: string, input: InitiativeMutationInput) => Promise<unknown>

export const initiativeOption = (item: Initiative): LinearMenuOption => ({ id: item.id, label: item.name, translate: false, icon: <ViewGlyph color={item.color} icon={item.icon || 'Initiative'}/> })

/** Linear's `canCreateSubInitiative` toast when the parent already sits on the deepest level. */
export function showNestingLimitError(t: Translate) {
  toast.error(t('Maximum initiative nesting level reached'), { description: t('Initiatives cannot be nested deeper than {n} levels').replace('{n}', String(MAX_INITIATIVE_NESTING)) })
}

export async function saveInitiativeRelation(update: () => Promise<unknown>, t: Translate) {
  try { await update() } catch (error) { toast.error(error instanceof Error ? error.message : t('Could not update initiative')) }
}

/** "Parent initiatives" picker: current parents plus every initiative that can take this one as a child. */
export function parentOptions(initiative: Initiative, initiatives: Initiative[], graph: Graph) {
  const parents = new Set(initiative.parentInitiativeIds ?? [])
  return initiatives.filter(item => parents.has(item.id) || graph.canParent(initiative.id, item.id)).map(initiativeOption)
}

export function directChildIds(initiative: Initiative, initiatives: Initiative[]) {
  return new Set(initiatives.filter(item => item.parentInitiativeIds?.includes(initiative.id)).map(item => item.id))
}

/** "Add existing sub-initiative" picker: direct children (checked) plus initiatives that fit under this one. */
export function childOptions(initiative: Initiative, initiatives: Initiative[], graph: Graph) {
  const current = directChildIds(initiative, initiatives)
  return [...initiatives.filter(item => current.has(item.id)), ...initiatives.filter(item => !current.has(item.id) && graph.canParent(item.id, initiative.id))].map(initiativeOption)
}

/** Adds or removes `initiative` as a parent of `child`, keeping the child's other parents. */
export function toggleChild(initiative: Initiative, child: Initiative | undefined, onUpdate: UpdateInitiative) {
  if (!child) return Promise.resolve()
  const ids = child.parentInitiativeIds ?? []
  return onUpdate(child.id, { parentInitiativeIds: ids.includes(initiative.id) ? ids.filter(id => id !== initiative.id) : [...ids, initiative.id] })
}

export function toggleId(ids: string[], id: string) {
  return ids.includes(id) ? ids.filter(item => item !== id) : [...ids, id]
}
