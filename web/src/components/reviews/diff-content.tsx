/**
 * DiffContent — line rows, unchanged-line separators, inline threads and the
 * inline code comment composer for one file of DiffView.
 */
import { ArrowUp, ArrowUpToLine, MessageSquarePlus, Minus, X } from "lucide-react";
import { Fragment, type ReactNode } from "react";

import { ReviewCode } from "@/components/reviews/review-code";
import type { DiffHunk, DiffLine } from "@/lib/diff-computer";
import { useI18n } from "@/i18n/i18n";

export type DiffCommentTarget = { path: string; line: number };

export function DiffHunkHeader({ hunk, hidden }: { hunk: DiffHunk; hidden: number }) {
  const { t } = useI18n();
  const context = hunk.header.replace(/^@@[^@]*@@\s?/, "");
  return (
    <div className={`review-diff-hunk-header${context ? "" : " is-bare"}`} role="presentation">
      <span className="review-diff-hunk-gutter">
        <ArrowUpToLine />
      </span>
      <span className="review-diff-hunk-context" data-i18n-ignore>
        {context}
      </span>
      {hidden > 0 && (
        <span className="review-diff-hunk-hidden">
          <ArrowUpToLine />
          {hidden} {t(hidden === 1 ? "unchanged line" : "unchanged lines")}
        </span>
      )}
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
  const lineNumber = side === "old" ? line?.oldNumber : side === "new" ? line?.newNumber : (line?.newNumber ?? line?.oldNumber);
  // Code comments anchor to the new side of the diff (removed lines have no new line number).
  const commentNumber = line?.newNumber;
  const canEdit = Boolean(editable && line && side !== "old" && line.kind !== "remove");

  return (
    <div className={`review-diff-cell is-${line?.kind ?? "empty"}${dirty ? " is-dirty" : ""}`} data-line-old={line?.oldNumber} data-line-new={line?.newNumber}>
      <span className="review-diff-number">
        {side === "unified" && line?.kind === "remove" ? <Minus className="review-diff-removed-mark" aria-label={`${t("Removed line")} ${line.oldNumber ?? ""}`} /> : (lineNumber ?? "")}
        {commentNumber && onComment ? (
          <button aria-label={`${t("Comment on line")} ${commentNumber}`} className="review-diff-comment-button" onClick={() => onComment({ path, line: commentNumber })} type="button">
            <MessageSquarePlus />
          </button>
        ) : null}
      </span>
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
    </div>
  );
}

/** Inline code comment composer card shown under the commented line. */
export function DiffInlineComposer({
  line,
  body,
  busy,
  onBodyChange,
  onCancel,
  onSubmit,
}: {
  line: number;
  body: string;
  busy: boolean;
  onBodyChange: (value: string) => void;
  onCancel: () => void;
  onSubmit: () => void;
}) {
  const { t } = useI18n();
  return (
    <form
      className="review-inline-comment"
      onSubmit={(event) => {
        event.preventDefault();
        onSubmit();
      }}
    >
      <textarea
        autoFocus
        aria-label={t("Inline comment")}
        aria-description={`${t("Comment on line")} ${line}`}
        value={body}
        onChange={(event) => onBodyChange(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Escape") onCancel();
          if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
            event.preventDefault();
            onSubmit();
          }
        }}
        placeholder={t("Leave a code comment...")}
      />
      <button type="button" className="review-ghost-button review-inline-comment-close" aria-label={t("Cancel")} onClick={onCancel}>
        <X />
      </button>
      <footer>
        <button type="submit" className="review-round-button" aria-label={t("Comment")} disabled={busy || !body.trim()}>
          <ArrowUp />
        </button>
      </footer>
    </form>
  );
}

function hiddenBefore(hunks: DiffHunk[], index: number) {
  const hunk = hunks[index];
  if (index === 0) return Math.max(0, hunk.newStart - 1);
  const previous = hunks[index - 1];
  return Math.max(0, hunk.newStart - (previous.newStart + previous.newCount));
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
  renderAfterLine,
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
  /** Threads and the inline composer, rendered under the row for a line number. */
  renderAfterLine?: (line: number) => ReactNode;
}) {
  const dirtyKey = (line?: DiffLine) => (line ? `${path}:${line.newNumber ?? line.oldNumber}:${line.kind}` : "");
  const after = (line?: DiffLine) => {
    const number = line?.newNumber;
    if (!number || !renderAfterLine) return null;
    const content = renderAfterLine(number);
    return content ? <div className="review-diff-annotation">{content}</div> : null;
  };
  const unifiedRows = (rows: DiffLine[]) =>
    rows.map((line, index) => (
      <Fragment key={`${line.oldNumber ?? ""}-${line.newNumber ?? ""}-${index}`}>
        <div className="review-diff-row">
          <DiffLineCell line={line} side="unified" path={path} onComment={onComment} editable={editable} dirty={dirtyLines?.has(dirtyKey(line))} onEdit={onEdit} />
        </div>
        {after(line)}
      </Fragment>
    ));
  const splitRowsFor = (rows: Array<{ left?: DiffLine; right?: DiffLine }>) =>
    rows.map((row, index) => (
      <Fragment key={`${row.left?.oldNumber ?? ""}-${row.right?.newNumber ?? ""}-${index}`}>
        <div className="review-diff-row">
          <DiffLineCell line={row.left} side="old" path={path} onComment={onComment} />
          <DiffLineCell line={row.right} side="new" path={path} onComment={onComment} editable={editable} dirty={dirtyLines?.has(dirtyKey(row.right))} onEdit={onEdit} />
        </div>
        {after(row.right)}
      </Fragment>
    ));
  const pair = (rows: DiffLine[]) => {
    const output: Array<{ left?: DiffLine; right?: DiffLine }> = [];
    let removed: DiffLine[] = [];
    let added: DiffLine[] = [];
    const flush = () => {
      for (let i = 0; i < Math.max(removed.length, added.length); i += 1) output.push({ left: removed[i], right: added[i] });
      removed = [];
      added = [];
    };
    for (const line of rows) {
      if (line.kind === "remove") removed.push(line);
      else if (line.kind === "add") added.push(line);
      else {
        flush();
        output.push({ left: line, right: line });
      }
    }
    flush();
    return output;
  };

  return (
    <div className={`review-diff-table is-${mode}`}>
      {hunks?.length
        ? hunks.map((hunk, index) => (
            <div key={`${hunk.header}-${index}`} className="review-diff-hunk">
              {(hiddenBefore(hunks, index) > 0 || hunk.header.replace(/^@@[^@]*@@\s?/, "")) && <DiffHunkHeader hunk={hunk} hidden={hiddenBefore(hunks, index)} />}
              {mode === "split" ? splitRowsFor(pair(hunk.lines)) : unifiedRows(hunk.lines)}
            </div>
          ))
        : mode === "split"
          ? splitRowsFor(splitRows)
          : unifiedRows(lines)}
    </div>
  );
}
