import type { Release, ReleasePipeline } from '@/types/flow'

export type StageStatus = Release['status']

/** One stage while the pipeline form is being edited. */
export type StageDraft = {
  /** Stable key for React and drag-and-drop; stage names can change. */
  key: string
  name: string
  status: StageStatus
  color?: string
  frozen: boolean
  /** The persisted name, so a rename can carry its releases along. */
  originalName?: string
}

/** Linear's default color for started stages (getDefaultStageTypeColor). */
export const DEFAULT_STARTED_STAGE_COLOR = '#f2c94c'
export const NAME_MAX_LENGTH = 50

let stageKeySeed = 0
export const nextStageKey = () => `stage-${++stageKeySeed}`

export const DEFAULT_STAGES: StageDraft[] = [
  { key: 'default-planned', name: 'Planned', status: 'planned', frozen: false },
  { key: 'default-in-progress', name: 'In Progress', status: 'inProgress', frozen: false },
  { key: 'default-released', name: 'Released', status: 'released', frozen: false },
  { key: 'default-canceled', name: 'Canceled', status: 'canceled', frozen: false },
]

export const STATUS_ORDER: StageStatus[] = ['planned', 'inProgress', 'released', 'canceled']
/** The default names Linear shows as stage type labels; they translate, custom names don't. */
export const TYPE_LABELS = new Set(['Planned', 'In Progress', 'Started', 'Released', 'Canceled'])

export function stagesFromPipeline(pipeline?: ReleasePipeline): StageDraft[] {
  if (!pipeline) return DEFAULT_STAGES.map(stage => ({ ...stage }))
  const drafts = pipeline.stages.map(name => ({
    key: nextStageKey(),
    name,
    status: pipeline.stageStatuses[name] ?? 'planned',
    color: pipeline.stageColors?.[name],
    frozen: pipeline.frozenStages?.includes(name) ?? false,
    originalName: name,
  }))
  // Stages group by type, in Linear's planned → started → released → canceled order.
  return STATUS_ORDER.flatMap(status => drafts.filter(stage => stage.status === status))
}

/** Serializes drafts into the pipeline's name-keyed stage fields. */
export function stageMutation(stages: StageDraft[]) {
  const ordered = STATUS_ORDER.flatMap(status => stages.filter(stage => stage.status === status))
  const stageRenames = Object.fromEntries(ordered.filter(stage => stage.originalName && stage.originalName !== stage.name).map(stage => [stage.originalName!, stage.name]))
  return {
    stages: ordered.map(stage => stage.name),
    stageStatuses: Object.fromEntries(ordered.map(stage => [stage.name, stage.status])) as ReleasePipeline['stageStatuses'],
    stageColors: Object.fromEntries(ordered.filter(stage => stage.status === 'inProgress' && stage.color).map(stage => [stage.name, stage.color!])),
    frozenStages: ordered.filter(stage => stage.status === 'inProgress' && stage.frozen).map(stage => stage.name),
    stageRenames,
  }
}
