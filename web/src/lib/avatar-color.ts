// Initials avatars (people, apps and agents without a picture, workspaces without a logo) are painted from one palette of
// 108 colours: 36 hues 10° apart, each at lightness 40, 55 and 70, all at chroma 60. Nothing is stored: the colour is
// derived from the entity's id, so it is the same on every surface and every device, and two people with the same name
// still get their own colour.
const HUES = 36
const LIGHTNESSES = 3

/** The key used when an avatar has no id (for example an author known only by name). */
const MISSING_ID = 'rdm'

/** A 32-bit djb2-xor hash of the id, read from its last character to its first. */
function avatarHash(id: string) {
  let hash = 5381
  for (let index = id.length; index > 0;) hash = (hash * 33) ^ id.charCodeAt(--index)
  return hash >>> 0
}

/** The palette entry for an id: its hue in degrees and its LCH lightness. */
export function avatarTone(id: string | null | undefined) {
  const hash = avatarHash(id || MISSING_ID)
  return { hue: (hash % HUES) * (360 / HUES), lightness: 40 + (Math.floor(hash / HUES) % LIGHTNESSES) * 15 }
}

/**
 * The background of an initials avatar. Every surface keys it by the entity's id (never the display name, an index or a
 * per-page palette), so one person, app or workspace has one colour in lists, pickers, comments, activity and settings.
 */
export function avatarColor(id: string | null | undefined) {
  const { hue, lightness } = avatarTone(id)
  return `lch(${lightness}% 60 ${hue})`
}

/** A workspace's initials background: a colour set through the API wins, otherwise the colour its id maps to. */
export function workspaceAvatarColor(workspace: { id: string; color?: string }) {
  return workspace.color || avatarColor(workspace.id)
}
