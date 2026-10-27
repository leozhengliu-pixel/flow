/**
 * Wall-clock reads for event handlers, effects and async callbacks (a save stamping "now", a deadline check on
 * click). Components must not read the clock while rendering: they use `useCurrentTime` from
 * `@/hooks/use-current-time` instead, so these helpers are for code that runs after render.
 */
export function nowMs(): number {
  return Date.now()
}

export function nowDate(): Date {
  return new Date()
}

export function nowISO(): string {
  return new Date().toISOString()
}
