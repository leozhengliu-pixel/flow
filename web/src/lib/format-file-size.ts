const decimal = new Intl.NumberFormat('en-US', { maximumFractionDigits: 2 })

/**
 * File size as the reference app prints it on issue file cards: binary units (1 KB = 1024 B), thousands separators
 * below 1 KB ("1,000 B"), and up to two decimals with trailing zeros dropped ("1.5 KB", "120 KB", "1.81 MB", "2.3 MB").
 */
export function formatFileSize(size: number) {
  const bytes = Math.max(0, Number(size) || 0)
  if (bytes < 1024) return `${decimal.format(bytes)} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let value = bytes / 1024
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) { value /= 1024; unit++ }
  return `${decimal.format(value)} ${units[unit]}`
}
