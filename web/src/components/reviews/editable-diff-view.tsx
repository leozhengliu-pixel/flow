/**
 * LS-0231 EditableDiffView — changeset / editable overlays on DiffView.
 *
 * Local overlays only (REST). Edits are staged in-memory for review/agent
 * context; they are not written back to the remote PR unless a later REST
 * mutation lands.
 */
import { useCallback, useMemo, useState } from "react";

import { DiffView, type DiffViewMode } from "@/components/reviews/diff-view";
import { useI18n } from "@/i18n/i18n";
import type { CodeReview } from "@/types/flow";

export type EditableDiffChange = {
  path: string;
  lineNumber: number;
  content: string;
  previous?: string;
};

export function EditableDiffView({
  review,
  onReload,
  onChangesChange,
}: {
  review: CodeReview;
  onReload: () => Promise<void>;
  onChangesChange?: (changes: EditableDiffChange[]) => void;
}) {
  const { t } = useI18n();
  const [mode, setMode] = useState<DiffViewMode>("split");
  const [edits, setEdits] = useState<Map<string, EditableDiffChange>>(() => new Map());

  const dirtyLines = useMemo(() => {
    const set = new Set<string>();
    for (const change of edits.values()) {
      set.add(`${change.path}:${change.lineNumber}:add`);
      set.add(`${change.path}:${change.lineNumber}:context`);
    }
    return set;
  }, [edits]);

  const changes = useMemo(() => [...edits.values()], [edits]);

  const onEditLine = useCallback(
    (value: { path: string; lineNumber: number; content: string }) => {
      setEdits((current) => {
        const key = `${value.path}:${value.lineNumber}`;
        const next = new Map(current);
        const previous = current.get(key)?.previous ?? current.get(key)?.content;
        if (!value.content.trim() && previous == null) {
          next.delete(key);
        } else {
          next.set(key, {
            path: value.path,
            lineNumber: value.lineNumber,
            content: value.content,
            previous,
          });
        }
        onChangesChange?.([...next.values()]);
        return next;
      });
    },
    [onChangesChange],
  );

  const discardAll = () => {
    setEdits(new Map());
    onChangesChange?.([]);
  };

  return (
    <div className="editable-diff-view">
      <div className="editable-diff-toolbar" role="status">
        <span>
          {changes.length
            ? t("{count} local edit(s) staged").replace("{count}", String(changes.length))
            : t("Editable diff — click a modified line to stage a local change")}
        </span>
        {changes.length > 0 ? (
          <button type="button" onClick={discardAll}>
            {t("Discard local edits")}
          </button>
        ) : null}
      </div>
      <DiffView
        review={review}
        onReload={onReload}
        editable
        dirtyLines={dirtyLines}
        onEditLine={onEditLine}
        mode={mode}
        onModeChange={setMode}
      />
    </div>
  );
}
