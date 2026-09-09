import type { Project, ProjectUpdateSchedule } from '@/types/flow'

export function projectSchedule(project: Project): ProjectUpdateSchedule {
  return project.updateSchedule ?? {mode:project.updateCadence && project.updateCadence !== 'none' ? 'custom' : 'default',frequencyDays:project.updateCadence === 'biweekly' ? 14 : project.updateCadence === 'monthly' ? 28 : 7,weekday:5,hour:14,timezone:Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'}
}

export function projectScheduleLabel(project: Project) {
  if (!project.updateSchedule) return ({none:'No expectation for updates',weekly:'Weekly',biweekly:'Every two weeks',monthly:'Monthly'})[project.updateCadence || 'none']
  const schedule = project.updateSchedule
  if (schedule.mode === 'default') return 'Default'
  if (schedule.mode === 'never') return 'Never'
  if (schedule.frequencyDays === 1) return 'Every day'
  if (schedule.frequencyDays === 7) return 'Every week'
  return `Every ${schedule.frequencyDays / 7} weeks`
}
