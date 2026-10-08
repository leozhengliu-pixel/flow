import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { ChevronDown, ChevronRight, MoreHorizontal, Plus } from 'lucide-react'
import { Fragment, useMemo, useState, type ReactElement } from 'react'
import { toast } from 'sonner'
import { LinearDropdownMenuContent, LinearMenuItem, LinearMenuOptions, LinearSubmenu } from '@/components/ui/row-context-menu'
import { LinearGlyph } from '@/components/ui/menu-glyphs'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { useI18n } from '@/i18n/i18n'
import { initiativePath } from '@/lib/app-routes'
import type { Initiative, InitiativeMutationInput, InitiativeStatus, IssueLabel, Project, ProjectUpdate, Team, User } from '@/types/flow'
import { initiativeGraph, initiativeTreeRows } from './initiative-hierarchy'
import { childOptions, directChildIds, parentOptions, saveInitiativeRelation, showNestingLimitError, toggleChild, toggleId, type UpdateInitiative } from './initiative-hierarchy-actions'
import { InitiativeStatusIcon } from './initiative-shared'
import { InitiativeCreateRow, InitiativeRow, type InitiativeListProperty } from './initiatives-page'
import { titleCase } from './initiative-model'
import './initiative-hierarchy.css'

/**
 * Linear's "Contributes to" row in the overview's property grid: one chip per parent initiative and a
 * "+" that opens the parent picker. Linear renders the row only when the initiative has a parent.
 */
export function InitiativeContributesTo({ initiative, initiatives, onOpen, onUpdate }: {
  initiative: Initiative; initiatives: Initiative[]
  onOpen: (initiative: Initiative) => void
  onUpdate: UpdateInitiative
}) {
  const { t } = useI18n()
  const graph = useMemo(() => initiativeGraph(initiatives), [initiatives])
  const ids = initiative.parentInitiativeIds ?? []
  const parents = ids.map(id => graph.byId.get(id)).filter((item): item is Initiative => Boolean(item))
  if (!parents.length) return null
  const setParents = (next: string[]) => void saveInitiativeRelation(() => onUpdate(initiative.id, { parentInitiativeIds: next }), t)
  return <section className="li-contributes-to">
    <h3>{t('Contributes to')}</h3>
    <div className="li-contributes-to__content">
      {parents.map(parent => <div className="li-parent-chip" key={parent.id}>
        <button className="li-parent-chip__link" type="button" onClick={() => onOpen(parent)}><ViewGlyph color={parent.color} icon={parent.icon || 'Initiative'}/><span data-i18n-ignore>{parent.name}</span></button>
        <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label={t('Menu')} className="li-parent-chip__menu" type="button"><MoreHorizontal size={14}/></button></DropdownMenu.Trigger><DropdownMenu.Portal>
          <LinearDropdownMenuContent align="end" label={t('Parent initiatives')}>
            <LinearMenuItem icon={<ViewGlyph icon="Initiative" color="currentColor"/>} label="Open initiative" onSelect={() => onOpen(parent)}/>
            <LinearMenuItem icon={<LinearGlyph name="delete"/>} label="Remove from parent" onSelect={() => setParents(ids.filter(id => id !== parent.id))}/>
          </LinearDropdownMenuContent>
        </DropdownMenu.Portal></DropdownMenu.Root>
      </div>)}
      <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label={t('Add parent initiative…')} className="li-overview-add" type="button"><Plus size={14}/></button></DropdownMenu.Trigger><DropdownMenu.Portal>
        <LinearDropdownMenuContent className="has-search" label={t('Parent initiatives')}>
          <LinearMenuOptions multiple placeholder="Change parent initiatives…" emptyLabel="No matching initiatives" selected={new Set(ids)} options={parentOptions(initiative, initiatives, graph)}
            onChoose={id => setParents(toggleId(ids, id))}/>
        </LinearDropdownMenuContent>
      </DropdownMenu.Portal></DropdownMenu.Root>
    </div>
  </section>
}

/** Linear's "Add a sub-initiative" menu: Create sub-initiative, and Add existing sub-initiative ▸ picker. */
export function AddSubInitiativeMenu({ initiative, initiatives, trigger, onCreate, onUpdate }: { initiative: Initiative; initiatives: Initiative[]; trigger: ReactElement; onCreate: () => void; onUpdate: UpdateInitiative }) {
  const { t } = useI18n()
  const graph = useMemo(() => initiativeGraph(initiatives), [initiatives])
  return <DropdownMenu.Root><DropdownMenu.Trigger asChild>{trigger}</DropdownMenu.Trigger><DropdownMenu.Portal>
    <LinearDropdownMenuContent align="end" label={t('Add a sub-initiative')}>
      <LinearMenuItem icon={<LinearGlyph name="subInitiatives"/>} label="Create sub-initiative" onSelect={() => graph.canCreateChild(initiative.id) ? onCreate() : showNestingLimitError(t)}/>
      <LinearSubmenu icon={<ViewGlyph icon="Initiative" color="currentColor"/>} label="Add existing sub-initiative" search>
        <LinearMenuOptions multiple placeholder="Add existing sub-initiative…" emptyLabel="No matching initiatives" selected={directChildIds(initiative, initiatives)} options={childOptions(initiative, initiatives, graph)}
          onChoose={id => void saveInitiativeRelation(() => toggleChild(initiative, graph.byId.get(id), onUpdate), t)}/>
      </LinearSubmenu>
    </LinearDropdownMenuContent>
  </DropdownMenu.Portal></DropdownMenu.Root>
}

const STATUS_ORDER: InitiativeStatus[] = ['proposed', 'planned', 'active', 'completed', 'canceled']
// Linear's sub-initiative list defaults (status is the grouping, so its column is off).
const COLUMNS: InitiativeListProperty[] = ['priority', 'owner', 'leadTeam', 'target', 'health', 'projects', 'activeProjects']
const COLUMN_WIDTHS: Partial<Record<InitiativeListProperty, string>> = { priority: '28px', owner: '28px', leadTeam: 'minmax(min-content,100px)', target: '100px', health: '28px', projects: '59px', activeProjects: '48px' }
const PROPERTIES = new Set<InitiativeListProperty>(['description', ...COLUMNS])
const GRID = `0px 0px minmax(160px,1.4fr) ${COLUMNS.map(property => COLUMN_WIDTHS[property]).join(' ')}`

export type SubInitiativesProps = {
  initiative: Initiative; initiatives: Initiative[]; projects: Project[]; projectUpdates: Record<string, ProjectUpdate[]>
  users: User[]; teams: Team[]; labels: IssueLabel[]; viewer: User
  /** The parent of the inline create row, or undefined when it is closed. */
  creatingParentId?: string
  onCreatingParentIdChange: (id?: string) => void
  onOpen: (initiative: Initiative) => void
  onCreate: (input: InitiativeMutationInput & { name: string }) => Promise<unknown>
  onCreateLabel: (name: string) => Promise<IssueLabel>
  onCreateReminder: (id: string, remindAt: string) => Promise<unknown>
  onDelete: (id: string) => Promise<void>
  onUpdate: (id: string, input: InitiativeMutationInput) => Promise<unknown>
}

/**
 * Linear Enterprise's "Sub-initiatives" overview section: every descendant, grouped by status and
 * nested under its parent, with an inline create row. Like Linear it renders nothing while the
 * initiative has no sub-initiatives and nothing is being created.
 */
export function InitiativeSubInitiatives(props: SubInitiativesProps) {
  const { initiative, initiatives, creatingParentId, onCreatingParentIdChange } = props
  const { t } = useI18n()
  const graph = useMemo(() => initiativeGraph(initiatives), [initiatives])
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const [collapsedGroups, setCollapsedGroups] = useState<Set<string>>(new Set())
  const descendants = useMemo(() => graph.descendants(initiative.id).map(id => graph.byId.get(id)!).sort((a, b) => (a.position ?? 0) - (b.position ?? 0)), [graph, initiative.id])
  const creating = creatingParentId !== undefined
  if (!descendants.length && !creating) return null
  const toggle = (set: (update: (current: Set<string>) => Set<string>) => void, id: string) => set(current => { const next = new Set(current); if (next.has(id)) next.delete(id); else next.add(id); return next })
  const workspaceSlug = location.pathname.split('/').filter(Boolean)[0] ?? ''
  const createChildOf = (parent: Initiative) => graph.canCreateChild(parent.id) ? onCreatingParentIdChange(parent.id) : showNestingLimitError(t)
  return <section aria-labelledby="initiative-sub-initiatives" className="li-overview-list li-subinitiatives">
    <header>
      <h2 id="initiative-sub-initiatives">{t('Sub-initiatives')}</h2>
      <AddSubInitiativeMenu initiative={initiative} initiatives={initiatives} onCreate={() => createChildOf(initiative)} onUpdate={props.onUpdate}
        trigger={<button aria-label={t('Add new sub-initiative')} className="li-overview-add" style={{ visibility: creating ? 'hidden' : 'visible' }} type="button"><Plus size={14}/></button>}/>
    </header>
    {creating && <InitiativeCreateRow initialLeadTeamId={graph.byId.get(creatingParentId)?.leadTeamId ?? initiative.leadTeamId} labels={props.labels} teams={props.teams} users={props.users} viewer={props.viewer} view="all"
      onCancel={() => onCreatingParentIdChange(undefined)} onCreateLabel={props.onCreateLabel}
      onCreate={async input => { try { await props.onCreate({ ...input, parentInitiativeIds: [creatingParentId] }); onCreatingParentIdChange(undefined) } catch (error) { toast.error(error instanceof Error ? error.message : t('Could not create initiative')) } }}/>}
    {descendants.length > 0 && <div className="li-subinitiatives__list">
      {STATUS_ORDER.map(status => {
        const items = descendants.filter(item => item.status === status)
        if (!items.length) return null
        const groupCollapsed = collapsedGroups.has(status)
        return <Fragment key={status}>
          <button aria-expanded={!groupCollapsed} className="li-subinitiatives__group" type="button" onClick={() => toggle(setCollapsedGroups, status)}>
            {groupCollapsed ? <ChevronRight size={12}/> : <ChevronDown size={12}/>}<InitiativeStatusIcon status={status}/><span>{t(titleCase(status))}</span><small>{items.length}</small>
          </button>
          {!groupCollapsed && initiativeTreeRows(items, graph, collapsed).map(({ initiative: item, depth, childCount }) => <InitiativeRow key={item.id}
            contextOnly={false} depth={depth} childCount={childCount} collapsed={collapsed.has(item.id)} onToggleChildren={() => toggle(setCollapsed, item.id)}
            projectIds={graph.projectIds(item.id)} columns={COLUMNS} grid={GRID} href={initiativePath(workspaceSlug, item)} initiative={item} initiatives={initiatives} canParent={graph.canParent}
            initiativeUpdates={[]} labels={props.labels} projects={props.projects} projectUpdates={props.projectUpdates} properties={PROPERTIES} selected={false} teams={props.teams} users={props.users}
            onCreateLabel={props.onCreateLabel} onCreateReminder={remindAt => props.onCreateReminder(item.id, remindAt)} onCreateSubInitiative={() => createChildOf(item)} onDelete={props.onDelete}
            onNewUpdate={() => props.onOpen(item)} onOpen={props.onOpen} onOpenUpdates={() => props.onOpen(item)}
            onUpdate={input => props.onUpdate(item.id, input)} onUpdateInitiative={props.onUpdate}/>)}
        </Fragment>
      })}
    </div>}
  </section>
}
