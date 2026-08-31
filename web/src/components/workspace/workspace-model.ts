export function slugifyWorkspace(value: string) {
  return Array.from(value.normalize('NFKC').toLocaleLowerCase().replace(/[^\p{Letter}\p{Number}]+/gu, '-').replace(/^-+|-+$/g, '')).slice(0, 48).join('')
}
