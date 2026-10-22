const decimal = new Intl.NumberFormat('en-US', { maximumFractionDigits: 2 })

/**
 * File size as the reference app prints it (file cards, the agent composer, video and context-menu actions all share one
 * helper): binary units (1 KB = 1024 B) picked from the byte count itself, so 1,048,575 B stays "1,024 KB" and sizes
 * above 1 TB stay in GB ("1,024 GB"). The number has thousands separators and up to two decimals with trailing zeros
 * dropped ("1,000 B", "1.5 KB", "120 KB", "1.81 MB", "2.3 MB").
 */
export function formatFileSize(size: number) {
  const bytes = Math.max(0, Number(size) || 0)
  if (bytes >= 1024 ** 3) return `${decimal.format(bytes / 1024 ** 3)} GB`
  if (bytes >= 1024 ** 2) return `${decimal.format(bytes / 1024 ** 2)} MB`
  if (bytes >= 1024) return `${decimal.format(bytes / 1024)} KB`
  return `${decimal.format(bytes)} B`
}
