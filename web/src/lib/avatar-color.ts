const AVATAR_COLORS = ['#d15f5f', '#5e6ad2', '#4c9a67', '#d09b42']

/**
 * The background of a person's initials avatar. Every surface keys it by the person's id (never the display name, an index
 * or a per-page palette), so one person has one colour in lists, pickers, comments, activity and settings.
 */
export function avatarColor(userId: string) {
  return AVATAR_COLORS[[...userId].reduce((sum, character) => sum + character.charCodeAt(0), 0) % AVATAR_COLORS.length]
}
