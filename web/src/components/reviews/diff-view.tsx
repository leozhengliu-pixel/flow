/**
 * LS-0201 / LS-0206 Diff + DiffView — split/unified table over computed diffs.
 */
import { FileCode2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { DiffContentRows, type DiffCommentTarget } from "@/components/reviews/diff-content";
import { useDiffFilesQuery } from "@/hooks/use-diff-files-query";
import { useI18n } from "@/i18n/i18n";
import { commentOnReview } from "@/lib/api";
import { pairDiffLines } from "@/lib/diff-computer";
import type { CodeReview } from "@/types/flow";

export type DiffViewMode = "split" | "unified";

export function DiffView({
  review,
  onReload,
  editable = false,
  dirtyLines,
  onEditLine,
  mode: controlledMode,
  onModeChange,
}: {
  review: CodeReview;
  onReload: () => Promise<void>;
  editable?: boolean;
  dirtyLines?: Set<string>;
  onEditLine?: (value: { path: string; lineNumber: number; content: string }) => void;
  mode?: DiffViewMode;
  onModeChange?: (mode: DiffViewMode) => void;
}) {
  const { t } = useI18n();
  const [uncontrolledMode, setUncontrolledMode] = useState<DiffViewMode>("split");
  const mode = controlledMode ?? uncontrolledMode;
  const setMode = onModeChange ?? setUncontrolledMode;
  const [commentLine, setCommentLine] = useState<DiffCommentTarget | null>(null);
  const [commentBody, setCommentBody] = useState("");
  const [commentBusy, setCommentBusy] = useState(false);
  const { files, ready } = useDiffFilesQuery(review, { variant: "basic", enableAgentContext: true });

  const submitInlineComment = async () => {
    if (!commentLine || !commentBody.trim()) return;
    setCommentBusy(true);
    try {
      await commentOnReview(review.id, commentBody.trim(), commentLine);
      await onReload();
      setCommentBody("");
      setCommentLine(null);
      toast.success(t("Comment added"));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not submit review"));
    } finally {
      setCommentBusy(false);
    }
  };

  return (
    <div className={`review-changes${editable ? " is-editable" : ""}`}>
      <header className="review-changes-toolbar">
        <div>
          <h2>{t("Files changed")}</h2>
          <span>{files.length}</span>
          {!ready ? <em className="review-diff-compute-status">{t("Computing diffs…")}</em> : null}
        </div>
        <div className="review-diff-mode" role="group" aria-label={t("Diff view")}>
          {(["split", "unified"] as const).map((value) => (
            <button
              aria-pressed={mode === value}
              key={value}
              onClick={() => setMode(value)}
              type="button"
            >
              {t(value === "split" ? "Split" : "Unified")}
            </button>
          ))}
        </div>
      </header>
      {files.length === 0 ? (
        <div className="review-diff-empty">{t("No files changed")}</div>
      ) : (
        files.map((file) => {
          const computed = file.computed;
          const lines = computed?.lines ?? [];
          const splitRows = computed?.splitRows ?? pairDiffLines(lines);
          return (
            <article key={file.path} data-diff-file={file.path} data-diff-kind={file.kind}>
              <header>
                <FileCode2 />
                <strong data-i18n-ignore>{file.path}</strong>
                <b>+{file.additions}</b>
                <i>-{file.deletions}</i>
                {computed?.state === "computing" || computed?.state === "pending" ? (
                  <small className="review-diff-compute-status">{t("Computing…")}</small>
                ) : null}
              </header>
              {file.kind === "pdf" ? (
                <div className="review-diff-empty">{t("PDF preview is not available in Reviews yet")}</div>
              ) : file.kind === "binary" ? (
                <div className="review-diff-empty">{t("Binary file changed")}</div>
              ) : !lines.length ? (
                <div className="review-diff-empty">
                  {computed?.state === "error"
                    ? t("Diff compute failed")
                    : computed?.state === "computing" || computed?.state === "pending" || !computed
                      ? t("Computing diffs…")
                      : t("No diff available")}
                </div>
              ) : (
                <DiffContentRows
                  path={file.path}
                  mode={mode}
                  lines={lines}
                  splitRows={splitRows}
                  hunks={computed?.hunks}
                  onComment={setCommentLine}
                  editable={editable}
                  dirtyLines={dirtyLines}
                  onEdit={(value) =>
                    onEditLine?.({
                      path: value.path,
                      lineNumber: value.line.newNumber ?? value.line.oldNumber ?? 0,
                      content: value.content,
                    })
                  }
                />
              )}
              {commentLine?.path === file.path && (
                <form
                  className="review-inline-comment"
                  onSubmit={(event) => {
                    event.preventDefault();
                    void submitInlineComment();
                  }}
                >
                  <label>
                    {t("Comment on line")} {commentLine.line}
                    <textarea
                      autoFocus
                      aria-label={t("Inline comment")}
                      value={commentBody}
                      onChange={(event) => setCommentBody(event.target.value)}
                      placeholder={t("Leave a comment…")}
                    />
                  </label>
                  <div>
                    <button
                      type="button"
                      onClick={() => {
                        setCommentLine(null);
                        setCommentBody("");
                      }}
                    >
                      {t("Cancel")}
                    </button>
                    <button disabled={commentBusy || !commentBody.trim()} type="submit">
                      {t("Comment")}
                    </button>
                  </div>
                </form>
              )}
            </article>
          );
        })
      )}
    </div>
  );
}
