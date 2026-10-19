import type { CodingArtifact } from '@/types/coding-sessions'

/** A "before" and an "after" screenshot of the same flow (same `group`); either half can be missing. */
export type ArtifactPair = { key: string; group: string; before?: CodingArtifact; after?: CodingArtifact }

/** The media of one turn (or of every turn when the session has only one). */
export type ArtifactSection = { key: string; turnId?: string; pairs: ArtifactPair[]; shots: CodingArtifact[]; videos: CodingArtifact[] }

const time = (artifact: CodingArtifact) => {
  const value = Date.parse(artifact.createdAt)
  return Number.isNaN(value) ? 0 : value
}

/** The URL an `<img>`/`<video>` loads: the stable cookie-authenticated one first, then the signed one. */
export const artifactSrc = (artifact: CodingArtifact) => artifact.contentUrl || artifact.url

export const pairRole = (artifact: CodingArtifact): 'before' | 'after' | undefined => {
  const label = artifact.label.trim().toLowerCase()
  return label === 'before' || label === 'after' ? label : undefined
}

function buildSection(key: string, turnId: string | undefined, artifacts: CodingArtifact[]): ArtifactSection {
  const pairs = new Map<string, ArtifactPair>()
  const shots: CodingArtifact[] = []
  const videos: CodingArtifact[] = []
  for (const artifact of artifacts) {
    if (artifact.kind === 'video') { videos.push(artifact); continue }
    const role = pairRole(artifact)
    if (!role) { shots.push(artifact); continue }
    const group = artifact.group?.trim() ?? ''
    const pair = pairs.get(group) ?? { key: `${key}:${group}`, group }
    // A later capture of the same half replaces the earlier one, which stays visible in the grid.
    const previous = pair[role]
    if (previous) shots.push(previous)
    pair[role] = artifact
    pairs.set(group, pair)
  }
  shots.sort((a, b) => time(a) - time(b))
  return { key, turnId, pairs: [...pairs.values()], shots, videos }
}

/**
 * Screenshots and videos (logs are ignored) in createdAt order, split by turn only when more than one turn is
 * present. Turns are ordered by their first capture; captures without a turn come last.
 */
export function buildArtifactSections(artifacts: readonly CodingArtifact[]): ArtifactSection[] {
  const media = artifacts.filter(artifact => artifact.kind === 'screenshot' || artifact.kind === 'video').sort((a, b) => time(a) - time(b))
  const turns = new Map<string, CodingArtifact[]>()
  for (const artifact of media) {
    const turn = artifact.turnId ?? ''
    turns.set(turn, [...(turns.get(turn) ?? []), artifact])
  }
  if (turns.size <= 1) return media.length ? [buildSection('all', undefined, media)] : []
  const ordered = [...turns.entries()].sort(([a], [b]) => (a === '' ? 1 : 0) - (b === '' ? 1 : 0))
  return ordered.map(([turn, items]) => buildSection(turn || 'no-turn', turn || undefined, items))
}

/** Every image of the gallery in display order, so the lightbox can page through all of them. */
export function galleryImages(sections: readonly ArtifactSection[]): CodingArtifact[] {
  return sections.flatMap(section => [
    ...section.pairs.flatMap(pair => [pair.before, pair.after].filter((item): item is CodingArtifact => Boolean(item))),
    ...section.shots,
  ])
}
