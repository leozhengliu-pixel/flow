/**
 * LS-0203 DiffContent — line cells / hunk chrome for DiffView.
 */
import { Plus } from "lucide-react";

import { ReviewCode } from "@/components/reviews/review-code";
import type { DiffHunk, DiffLine } from "@/lib/diff-computer";
import { useI18n } from "@/i18n/i18n";

export type DiffCommentTarget = { path: string; line: number };

export function DiffHunkHeader({ hunk }: { hunk: DiffHunk }) {
  return (
    <div className="review-diff-hunk-header" data-i18n-ignore>
      {hunk.header}
    </div>
  );
}

export function DiffLineCell({
  line,
  side,
  path,
  onComment,
  editable,
  dirty,
  onEdit,
}: {
  line?: DiffLine;
  side: "old" | "new" | "unified";
  path: string;
  onComment?: (value: DiffCommentTarget) => void;
  editable?: boolean;
  dirty?: boolean;
  onEdit?: (value: { path: string; line: DiffLine; content: string }) => void;
}) {
  const { t } = useI18n();
  const lineNumber = side === "old" ? line?.oldNumber : line?.newNumber;
  const commentNumber = line?.newNumber ?? line?.oldNumber;
  const canEdit = Boolean(editable && line && side !== "old" && line.kind !== "remove");

  return (
    <div
      className={`review-diff-cell is-${line?.kind ?? "empty"}${dirty ? " is-dirty" : ""}`}
      data-line-old={line?.oldNumber}
      data-line-new={line?.newNumber}
    >
      <span className="review-diff-number">{lineNumber ?? ""}</span>
      {line ? (
        <span className="review-diff-marker">
          {line.kind === "add" ? "+" : line.kind === "remove" ? "−" : " "}
        </span>
      ) : null}
      {canEdit && line ? (
        <code
          className="review-diff-editable"
          contentEditable
          suppressContentEditableWarning
          spellCheck={false}
          aria-label={t("Edit diff line")}
          onBlur={(event) => {
            const next = event.currentTarget.textContent ?? "";
            if (next !== line.content) onEdit?.({ path, line, content: next });
          }}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.preventDefault();
              (event.currentTarget as HTMLElement).blur();
            }
          }}
        >
          {line.content}
        </code>
      ) : (
        <ReviewCode content={line?.content ?? ""} path={path} />
      )}
      {commentNumber && onComment ? (
        <button
          aria-label={`${t("Comment on line")} ${commentNumber}`}
          className="review-diff-comment-button"
          onClick={() => onComment({ path, line: commentNumber })}
          type="button"
        >
          <Plus />
        </button>
      ) : null}
    </div>
  );
}

export function DiffContentRows({
  path,
  mode,
  lines,
  splitRows,
  hunks,
  onComment,
  editable,
  dirtyLines,
  onEdit,
}: {
  path: string;
  mode: "split" | "unified";
  lines: DiffLine[];
  splitRows: Array<{ left?: DiffLine; right?: DiffLine }>;
  hunks?: DiffHunk[];
  onComment?: (value: DiffCommentTarget) => void;
  editable?: boolean;
  dirtyLines?: Set<string>;
  onEdit?: (value: { path: string; line: DiffLine; content: string }) => void;
}) {
  const dirtyKey = (line?: DiffLine) =>
    line ? `${path}:${line.newNumber ?? line.oldNumber}:${line.kind}` : "";

  if (mode === "split") {
    return (
      <div className="review-diff-table is-split">
        {hunks?.length
          ? hunks.map((hunk) => (
              <div key={hunk.header} className="review-diff-hunk">
                <DiffHunkHeader hunk={hunk} />
              </div>
            ))
          : null}
        {splitRows.map((row, index) => (
          <div className="review-diff-row" key={`${path}-${index}`}>
            <DiffLineCell line={row.left} side="old" path={path} onComment={onComment} />
            <DiffLineCell
              line={row.right}
              side="new"
              path={path}
              onComment={onComment}
              editable={editable}
              dirty={dirtyLines?.has(dirtyKey(row.right))}
              onEdit={onEdit}
            />
          </div>
        ))}
      </div>
    );
  }

  return (
    <div className="review-diff-table is-unified">
      {hunks?.length
        ? hunks.map((hunk) => (
            <div key={hunk.header} className="review-diff-hunk">
              <DiffHunkHeader hunk={hunk} />
            </div>
          ))
        : null}
      {lines.map((line, index) => (
        <div className="review-diff-row" key={`${path}-${index}`}>
          <DiffLineCell
            line={line}
            side="unified"
            path={path}
            onComment={onComment}
            editable={editable}
            dirty={dirtyLines?.has(dirtyKey(line))}
            onEdit={onEdit}
          />
        </div>
      ))}
    </div>
  );
}
