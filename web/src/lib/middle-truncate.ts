/**
 * Shortens text to fit a width by keeping both ends and an ellipsis in the middle, the way the
 * reference app trims long branch names. `fits` measures a candidate in the caller's font.
 */
export function middleTruncate(text: string, fits: (candidate: string) => boolean, ellipsis = "…"): string {
  if (fits(text)) return text;
  let low = 1;
  let high = text.length - 1;
  let best = ellipsis;
  while (low <= high) {
    const keep = Math.floor((low + high) / 2);
    const head = Math.ceil(keep / 2);
    const tail = Math.floor(keep / 2);
    const candidate = `${text.slice(0, head)}${ellipsis}${tail ? text.slice(-tail) : ""}`;
    if (fits(candidate)) {
      best = candidate;
      low = keep + 1;
    } else {
      high = keep - 1;
    }
  }
  return best;
}
