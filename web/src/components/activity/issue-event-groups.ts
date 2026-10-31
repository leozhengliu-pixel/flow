/** Comments, sessions and the creation row delimit expandable runs of three or more events. */
export function issueEventGroups<T extends { id: string; kind: string; type?: string }>(items: T[]) {
  const groups = new Map<string, T[]>()
  let run: T[] = []
  const finish = () => {
    if (run.length >= 3) groups.set(run[0].id, run)
    run = []
  }
  for (const item of items) {
    if (item.kind === 'event' && item.type !== 'issue.created') run.push(item)
    else finish()
  }
  finish()
  return groups
}
