import { addDays, differenceInCalendarDays, startOfDay } from 'date-fns'

import type { Issue, ProjectProgressHistoryPoint } from '@/types/flow'

export type ProgressSeries = { id: 'Scope' | 'Started' | 'Completed'; data: { x: Date; y: number }[] }

export type ProgressForecast = {
  completed: number
  optimisticDate?: Date
  pessimisticDate?: Date
  total: number
}

export type PersistedProgressHistory = {
  completedScopeHistory?: ProjectProgressHistoryPoint[]
  inProgressScopeHistory?: ProjectProgressHistoryPoint[]
  issueCountHistory?: ProjectProgressHistoryPoint[]
  progressHistory?: ProjectProgressHistoryPoint[]
  scopeHistory?: ProjectProgressHistoryPoint[]
}

export function buildProgressData(issues: Issue[], start?: string, target?: string, persisted?: PersistedProgressHistory) {
  const today = startOfDay(new Date())
  const scopedIssues = issues.filter(issue => !['canceled', 'duplicate', 'triage'].includes(issue.state.type))
  const createdDates = scopedIssues.map(issue => startOfDay(new Date(issue.createdAt))).filter(date => !Number.isNaN(date.getTime()))
  const requestedStart = start ? startOfDay(new Date(`${start}T00:00:00`)) : createdDates[0]
  const startDate = requestedStart && !Number.isNaN(requestedStart.getTime()) ? requestedStart : today
  const requestedTarget = target ? startOfDay(new Date(`${target}T00:00:00`)) : addDays(startDate, 14)
  const targetDate = requestedTarget > startDate ? requestedTarget : addDays(startDate, 1)
  const baseEndDate = today > targetDate ? today : targetDate
  const currentDate = today < startDate ? startDate : today > baseEndDate ? baseEndDate : today
  const isCreated = (issue: Issue, date: Date) => startOfDay(new Date(issue.createdAt)) <= date
  const isStarted = (issue: Issue, date: Date) => ['started', 'completed'].includes(issue.state.type) && isCreated(issue, date)
  const isCompleted = (issue: Issue, date: Date) => {
    if (issue.state.type !== 'completed') return false
    const completedDate = startOfDay(new Date(issue.completedAt ?? issue.updatedAt))
    return completedDate <= date
  }
  const eventDays = uniqueDates([
    startDate,
    currentDate,
    targetDate,
    baseEndDate,
    ...scopedIssues.flatMap(issue => [startOfDay(new Date(issue.createdAt)), ...(issue.state.type === 'completed' ? [startOfDay(new Date(issue.completedAt ?? issue.updatedAt))] : [])]),
  ]).filter(date => date >= startDate && date <= baseEndDate)
  const activeDays = eventDays.filter(date => date <= currentDate)
  const completed = activeDays.map(date => ({ x: date, y: scopedIssues.filter(issue => isCompleted(issue, date)).length }))
  const completedCount = completed.at(-1)?.y ?? 0
  const historyDays = differenceInCalendarDays(currentDate, startDate)
  const inProgress = scopedIssues.filter(issue => issue.state.type === 'started' && isCreated(issue, currentDate)).length
  const progressEstimatePoints = Math.floor(completedCount + inProgress * .25)
  const persistedCompletedHistory = historyValues(persisted?.completedScopeHistory, startDate, currentDate)
  const completedScopeHistory = persistedCompletedHistory.length ? persistedCompletedHistory : historyDays >= 7 ? weeklyHistory(startDate, currentDate).map(date => scopedIssues.filter(issue => isCompleted(issue, date)).length) : []
  const persistedProgress = historyValues(persisted?.progressHistory, startDate, currentDate).at(-1)
  const progressVelocity = calculateProgressVelocity(completedScopeHistory, persistedProgress ?? progressEstimatePoints)
  const remainingPoints = Math.max(0, scopedIssues.length - progressEstimatePoints)
  const predictionVelocity = Math.max(progressVelocity, 2)
  const predictedDays = progressVelocity > 0 && remainingPoints > 0 ? Math.max(1, Math.ceil(remainingPoints / predictionVelocity * 7)) : 0
  const predictedDate = predictedDays ? addDays(currentDate, predictedDays) : undefined
  const optimisticDate = predictedDate ? addDays(currentDate, Math.max(1, Math.round(predictedDays * .6))) : undefined
  const pessimisticDate = predictedDate ? addDays(currentDate, Math.max(1, Math.round(predictedDays * 1.4))) : undefined
  const endDate = pessimisticDate && pessimisticDate > baseEndDate ? pessimisticDate : baseEndDate
  const finalEventDays = uniqueDates([...eventDays, endDate, ...(optimisticDate ? [optimisticDate] : []), ...(pessimisticDate ? [pessimisticDate] : [])]).filter(date => date >= startDate && date <= endDate)
  const scope = historySeries(persisted?.scopeHistory ?? persisted?.issueCountHistory, startDate, endDate) ?? finalEventDays.map(date => ({ x: date, y: scopedIssues.filter(issue => isCreated(issue, date)).length }))
  const finalActiveDays = finalEventDays.filter(date => date <= currentDate)
  const startedData = historySeries(persisted?.inProgressScopeHistory, startDate, currentDate) ?? finalActiveDays.map(date => ({ x: date, y: scopedIssues.filter(issue => isStarted(issue, date)).length }))
  const completedData = historySeries(persisted?.completedScopeHistory, startDate, currentDate) ?? finalActiveDays.map(date => ({ x: date, y: scopedIssues.filter(issue => isCompleted(issue, date)).length }))
  return {
    startDate,
    targetDate,
    endDate,
    currentDate,
    completedChanges: buildCompletionChanges(scopedIssues, startDate, currentDate),
    forecast: { completed: completedData.at(-1)?.y ?? 0, optimisticDate, pessimisticDate, total: scopedIssues.length } satisfies ProgressForecast,
    series: [{ id: 'Scope' as const, data: scope }, { id: 'Started' as const, data: startedData }, { id: 'Completed' as const, data: completedData }],
  }
}

function weeklyHistory(startDate: Date, currentDate: Date) {
  const dates: Date[] = []
  for (let date = startDate; date <= currentDate; date = addDays(date, 7)) dates.push(date)
  if (dates.at(-1)?.getTime() !== currentDate.getTime()) dates.push(currentDate)
  return dates
}

function historyValues(entries: ProjectProgressHistoryPoint[] | undefined, startDate: Date, endDate: Date) {
  return (entries ?? []).filter(entry => {
    const date = new Date(entry.date)
    return Number.isFinite(date.getTime()) && date >= startDate && date <= endDate
  }).sort((left, right) => left.date.localeCompare(right.date)).map(entry => entry.value)
}

function historySeries(entries: ProjectProgressHistoryPoint[] | undefined, startDate: Date, endDate: Date) {
  const points = (entries ?? []).map(entry => ({ x: new Date(entry.date), y: entry.value })).filter(point => Number.isFinite(point.x.getTime()) && point.x >= startDate && point.x <= endDate).sort((left, right) => left.x.getTime() - right.x.getTime())
  if (!points.length) return undefined
  if (points[0].x.getTime() > startDate.getTime()) points.unshift({ x: startDate, y: points[0].y })
  if (points.at(-1)!.x.getTime() < endDate.getTime()) points.push({ x: endDate, y: points.at(-1)!.y })
  return points
}

function calculateProgressVelocity(history: number[], progress: number) {
  if (history.length === 0) return 0
  if (history.length === 1) return Math.round(Math.max(history[0], progress - history[0]))
  const changes = history.map((value, index) => index === 0 ? value : value - history[index - 1])
  const currentChange = progress - history.at(-1)!
  if (changes.at(-1)! <= currentChange) changes.push(currentChange)
  let weightedTotal = 0
  let weightTotal = 0
  changes.forEach((value, index) => {
    const weight = Math.max(0, index + 6 - history.length)
    weightedTotal += value * weight
    weightTotal += weight
  })
  return weightTotal ? Math.max(Math.round(weightedTotal / weightTotal), 0) : 0
}

function buildCompletionChanges(issues: Issue[], startDate: Date, currentDate: Date) {
  const rangeDays = Math.max(1, differenceInCalendarDays(currentDate, startDate))
  const bucketDays = rangeDays > 90 ? 7 : 1
  const buckets = new Map<number, number>()
  for (const issue of issues) {
    if (issue.state.type !== 'completed') continue
    const completedDate = startOfDay(new Date(issue.completedAt ?? issue.updatedAt))
    const offset = differenceInCalendarDays(completedDate, startDate)
    if (offset < 0 || completedDate > currentDate) continue
    const bucketStart = addDays(startDate, Math.floor(offset / bucketDays) * bucketDays)
    const bucketDate = addDays(bucketStart, Math.floor((bucketDays - 1) / 2))
    buckets.set(bucketDate.getTime(), (buckets.get(bucketDate.getTime()) ?? 0) + 1)
  }
  return [...buckets].map(([timestamp, y]) => ({ x: new Date(timestamp), y })).sort((left, right) => left.x.getTime() - right.x.getTime())
}

function uniqueDates(values: Date[]) {
  return [...new Map(values.filter(date => !Number.isNaN(date.getTime())).map(date => [date.getTime(), date])).values()].sort((left, right) => left.getTime() - right.getTime())
}
