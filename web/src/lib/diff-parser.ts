/**
 * LS-0205 DiffParser — sync patch → hunk/line parse pack for DiffView.
 *
 * REST + local patch only. Linear's DiffParser.worker (~1.1MB grammars) is not
 * vendored; language is inferred from filename for ReviewCode / DiffContent.
 */
export {
  languageFromFilename,
  parseDiff,
  parsePatchHunks,
  pairDiffLines,
  type DiffHunk,
  type DiffLine,
  type DiffLineKind,
} from "@/lib/diff-computer";

import { languageFromFilename, parsePatchHunks, type DiffHunk, type DiffLine } from "@/lib/diff-computer";

export type ParsedDiffFile = {
  filename: string;
  language: string;
  hunks: DiffHunk[];
  lines: DiffLine[];
  additions: number;
  deletions: number;
};

/** Parse a ReviewFile-style patch into a DiffContent-ready structure. */
export function parseDiffFile(filename: string, patch: string | undefined | null): ParsedDiffFile {
  const hunks = parsePatchHunks(patch);
  const lines = hunks.flatMap((hunk) => hunk.lines);
  let additions = 0;
  let deletions = 0;
  for (const line of lines) {
    if (line.kind === "add") additions += 1;
    if (line.kind === "remove") deletions += 1;
  }
  return {
    filename,
    language: languageFromFilename(filename),
    hunks,
    lines,
    additions,
    deletions,
  };
}

/** True when the patch has at least one parseable hunk / body line. */
export function hasParseableDiff(patch: string | undefined | null): boolean {
  return parsePatchHunks(patch).some((hunk) => hunk.lines.length > 0);
}
