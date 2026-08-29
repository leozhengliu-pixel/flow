export const workspaceRegions = [
  { value: 'us', label: 'United States' },
  { value: 'eu', label: 'European Union' },
  { value: 'uk', label: 'United Kingdom' },
  { value: 'ca', label: 'Canada' },
  { value: 'cn', label: 'China' },
  { value: 'asia', label: 'Asia Pacific' },
  { value: 'jp', label: 'Japan' },
  { value: 'sg', label: 'Singapore' },
  { value: 'in', label: 'India' },
  { value: 'au', label: 'Australia' },
  { value: 'br', label: 'Brazil' },
] as const

export function workspaceRegionLabel(value?: string) {
  return workspaceRegions.find(region => region.value === value)?.label ?? value ?? 'United States'
}
