export function projectStatusOptionColor(status: { id: string; name: string; color: string }, current: { id: string; name: string; color: string }) {
  return status.id === current.id || status.name === current.name ? current.color : status.color
}
