export function formatTarget(value?: string) {
  if (!value) return ''
  const date = new Date(`${value}T00:00:00`)
  const quarter = Math.floor(date.getMonth() / 3) + 1
  return `Q${quarter} ${date.getFullYear()}`
}

export function titleCase(value: string) {
  return value.charAt(0).toUpperCase() + value.slice(1).replace(/([A-Z])/g, ' $1')
}
