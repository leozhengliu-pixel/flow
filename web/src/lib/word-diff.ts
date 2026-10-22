/**
 * Changed-word highlighting for paired removed/added lines.
 *
 * Within a block of consecutive removed lines followed by added lines, the i-th removed line is paired with the i-th
 * added line (the same pairing the split view uses). A pair is diffed per character and the small unchanged runs
 * between edits are absorbed, so the highlight follows words and phrases rather than scattered letters. Pairs that
 * share little text are left alone, and very long lines are skipped, so the cost stays bounded.
 */

export type TextRange = [start: number, end: number];

/** Lines longer than this are not diffed. */
export const WORD_DIFF_MAX_LINE = 400;
/** Pairs whose unchanged text is below this share of both lines look unrelated and get no highlight. */
const MIN_SIMILARITY = 0.5;

const isWordChar = (char: string | undefined) => char !== undefined && /[\p{L}\p{N}_]/u.test(char);

/** Matched runs [aStart, bStart, length] of the longest common subsequence of a[from..] and b[from..]. */
function matchedRuns(a: string, b: string): Array<[number, number, number]> {
  let start = 0;
  while (start < a.length && start < b.length && a[start] === b[start]) start += 1;
  let endA = a.length;
  let endB = b.length;
  while (endA > start && endB > start && a[endA - 1] === b[endB - 1]) {
    endA -= 1;
    endB -= 1;
  }
  const runs: Array<[number, number, number]> = [];
  if (start > 0) runs.push([0, 0, start]);
  const n = endA - start;
  const m = endB - start;
  if (n > 0 && m > 0) {
    // lcs[i][j]: LCS length of the tails a[start+i..endA] and b[start+j..endB].
    const width = m + 1;
    const lcs = new Uint16Array((n + 1) * width);
    for (let i = n - 1; i >= 0; i -= 1) {
      for (let j = m - 1; j >= 0; j -= 1) {
        lcs[i * width + j] = a[start + i] === b[start + j] ? lcs[(i + 1) * width + j + 1] + 1 : Math.max(lcs[(i + 1) * width + j], lcs[i * width + j + 1]);
      }
    }
    let i = 0;
    let j = 0;
    while (i < n && j < m) {
      if (a[start + i] === b[start + j]) {
        const last = runs[runs.length - 1];
        if (last && last[0] + last[2] === start + i && last[1] + last[2] === start + j) last[2] += 1;
        else runs.push([start + i, start + j, 1]);
        i += 1;
        j += 1;
      } else if (lcs[(i + 1) * width + j] >= lcs[i * width + j + 1]) i += 1;
      else j += 1;
    }
  }
  if (endA < a.length) {
    const last = runs[runs.length - 1];
    if (last && last[0] + last[2] === endA && last[1] + last[2] === endB) last[2] += a.length - endA;
    else runs.push([endA, endB, a.length - endA]);
  }
  return runs;
}

/** Character ranges that differ between two lines, or null when the lines are too different (or too long) to highlight. */
export function wordDiffRanges(before: string, after: string): { before: TextRange[]; after: TextRange[] } | null {
  if (before === after || before.length > WORD_DIFF_MAX_LINE || after.length > WORD_DIFF_MAX_LINE) return null;
  if (!before.trim() || !after.trim()) return null;
  let runs = matchedRuns(before, after);
  const matched = runs.reduce((sum, run) => sum + run[2], 0);
  if ((2 * matched) / (before.length + after.length) < MIN_SIMILARITY) return null;
  // Absorb tiny unchanged runs that touch an edit: they only split one change into pieces.
  runs = runs.filter((run, index) => {
    const [aStart, bStart, length] = run;
    if (length > 2) return true;
    const previous = runs[index - 1];
    const next = runs[index + 1];
    const editBefore = previous ? previous[0] + previous[2] < aStart || previous[1] + previous[2] < bStart : aStart > 0 || bStart > 0;
    const editAfter = next ? aStart + length < next[0] || bStart + length < next[1] : aStart + length < before.length || bStart + length < after.length;
    if (editBefore && editAfter) return false;
    // A one or two letter tail of a word that was otherwise replaced reads as part of that change.
    const text = before.slice(aStart, aStart + length);
    const insideWord = isWordChar(text[0]) && ((editBefore && isWordChar(before[aStart - 1])) || (editAfter && isWordChar(before[aStart + length])));
    return !insideWord;
  });
  const gaps = (side: 0 | 1, total: number): TextRange[] => {
    const ranges: TextRange[] = [];
    let cursor = 0;
    for (const run of runs) {
      const from = run[side];
      if (from > cursor) ranges.push([cursor, from]);
      cursor = from + run[2];
    }
    if (cursor < total) ranges.push([cursor, total]);
    return ranges;
  };
  const left = gaps(0, before.length);
  const right = gaps(1, after.length);
  return left.length || right.length ? { before: left, after: right } : null;
}

/**
 * Highlight ranges for every changed line of a diff, keyed by the line's index in `lines`.
 * Blocks are runs of removed lines followed by added lines; unpaired lines get no ranges.
 */
export function pairedWordRanges(lines: ReadonlyArray<{ kind: "add" | "remove" | "context" }>, textOf: (index: number) => string): Map<number, TextRange[]> {
  const result = new Map<number, TextRange[]>();
  let removed: number[] = [];
  let added: number[] = [];
  const flush = () => {
    const pairs = Math.min(removed.length, added.length);
    for (let i = 0; i < pairs; i += 1) {
      const ranges = wordDiffRanges(textOf(removed[i]), textOf(added[i]));
      if (!ranges) continue;
      if (ranges.before.length) result.set(removed[i], ranges.before);
      if (ranges.after.length) result.set(added[i], ranges.after);
    }
    removed = [];
    added = [];
  };
  lines.forEach((line, index) => {
    if (line.kind === "remove") {
      if (added.length) flush();
      removed.push(index);
    } else if (line.kind === "add") added.push(index);
    else flush();
  });
  flush();
  return result;
}

const entities: Record<string, string> = { "&amp;": "&", "&lt;": "<", "&gt;": ">", "&quot;": '"', "&#x27;": "'", "&#39;": "'" };

/**
 * Wraps the given character ranges of highlighted code in `<span class="review-word-mark">`. `html` is highlight.js
 * output, whose text is the original line with HTML entities; ranges count characters of that original text.
 */
export function markHighlightedRanges(html: string, ranges: TextRange[]): string {
  if (!ranges.length) return html;
  let out = "";
  let position = 0; // characters of the original text consumed so far
  let rangeIndex = 0;
  let open = false;
  const closeMark = () => {
    if (open) {
      out += "</span>";
      open = false;
    }
  };
  let index = 0;
  while (index < html.length) {
    if (html[index] === "<") {
      const end = html.indexOf(">", index);
      closeMark(); // marks never straddle a token boundary, so the markup stays well formed
      out += html.slice(index, end + 1);
      index = end + 1;
      continue;
    }
    let unit = html[index];
    if (unit === "&") {
      const end = html.indexOf(";", index);
      const entity = end > index && end - index <= 6 ? html.slice(index, end + 1) : "";
      if (entity && entities[entity] !== undefined) unit = entity;
    }
    while (rangeIndex < ranges.length && position >= ranges[rangeIndex][1]) rangeIndex += 1;
    const inside = rangeIndex < ranges.length && position >= ranges[rangeIndex][0] && position < ranges[rangeIndex][1];
    if (inside && !open) {
      out += '<span class="review-word-mark">';
      open = true;
    } else if (!inside) closeMark();
    out += unit;
    index += unit.length;
    position += 1;
  }
  closeMark();
  return out;
}
