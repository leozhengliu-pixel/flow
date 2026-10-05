import type { UserSettings, WorkspaceSettings } from '@/types/flow'
import type { PulseFilter, PulseFilterMatch } from './pulse-model'

export type PulseCadence = 'default' | 'daily' | 'weekly' | 'never'
export type PulseSchedule = Exclude<PulseCadence, 'default'>
export type PulseViewDraft = { name:string;icon:string;color:string;filters:PulseFilter[];match:PulseFilterMatch }

export const PULSE_SCHEDULES: PulseSchedule[] = ['daily', 'weekly', 'never']
export const pulseScheduleLabels: Record<PulseSchedule, string> = { daily: 'Daily', weekly: 'Weekly', never: 'Never' }

function asSchedule(value?: string): PulseSchedule | undefined {
  return value === 'daily' || value === 'weekly' || value === 'never' ? value : undefined
}

/** Personal schedule, falling back to the workspace default ('' / 'default' = workspace default). */
export function effectivePulseSchedule(settings?: Pick<UserSettings, 'pulseSchedule'>, workspace?: Pick<WorkspaceSettings, 'featureSettings'>): PulseSchedule {
  return asSchedule(settings?.pulseSchedule) ?? asSchedule(workspace?.featureSettings?.pulseWorkspaceSchedule) ?? 'never'
}
