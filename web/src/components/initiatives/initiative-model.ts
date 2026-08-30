export function formatTarget(value?: string, resolution: 'halfYear'|'month'|'quarter'|'year' = 'quarter') {
  if (!value) return ''
  const date = new Date(`${value}T00:00:00`)
  if (resolution === 'month') return new Intl.DateTimeFormat('en', { month: 'short', year: 'numeric' }).format(date)
  if (resolution === 'halfYear') return `H${Math.floor(date.getMonth() / 6) + 1} ${date.getFullYear()}`
  if (resolution === 'year') return String(date.getFullYear())
  const quarter = Math.floor(date.getMonth() / 3) + 1
  return `Q${quarter} ${date.getFullYear()}`
}

export function titleCase(value: string) {
  return value.charAt(0).toUpperCase() + value.slice(1).replace(/([A-Z])/g, ' $1')
}
