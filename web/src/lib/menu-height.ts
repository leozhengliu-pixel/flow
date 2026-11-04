/** Fit the complete menu or end at a partial row that signals more content below. */
export function partialMenuHeight(rows: readonly number[], leading: number, trailing: number, available: number): number | undefined {
  let total = leading
  let last = 0
  for (const height of rows) {
    const partial = Math.ceil(total + height * .55)
    if (partial <= available) last = partial
    total += height
  }
  return total + trailing <= available ? undefined : last || available
}
