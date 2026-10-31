export function avatarInitials(name: string) {
  return name.split(/[\s@]+/)
    .map(part => part.match(/[\p{L}\p{N}]/u)?.[0])
    .filter(Boolean)
    .slice(0, 2)
    .join('')
    .toUpperCase() || '?'
}
