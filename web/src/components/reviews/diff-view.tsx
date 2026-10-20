/**
 * DiffView — the Diff tab: toolbar (Files / Commits / display options), the
 * optional file list, and one card per file with Reviewed state, inline
 * threads and the inline code comment composer.
 */
import * as Popover from "@radix-ui/react-popover";
import * as RadixMenu from "@radix-ui/react-dropdown-menu";
import { ChevronDown, Columns2, Copy, ExternalLink, FileCode2, GitCommitHorizontal, ListTree, MoreHorizontal, Rows2, Search } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";

import { DiffContentRows, DiffInlineComposer, type DiffCommentTarget } from "@/components/reviews/diff-content";
import { Toggle } from "@/components/ui/toggle";
import { DisplayIcon } from "@/components/ui/view-action-icons";
import { useDiffFilesQuery } from "@/hooks/use-diff-files-query";
import { useI18n } from "@/i18n/i18n";
import { commentOnReview } from "@/lib/api";
import { pairDiffLines } from "@/lib/diff-computer";
import { toast } from "@/lib/toast";
import type { CodeReview } from "@/types/flow";

import { useOptionalPullRequestReviewShortcuts } from "./pull-request-review-shortcuts";
import { reviewCodeThreads, splitReviewPath } from "./pull-request-review-model";
import { DiffCounts } from "./review-overview";
import { ReviewThread } from "./review-thread";
import "@/components/project-detail/project-action-menu.css";

export type DiffViewMode = "split" | "unified";

const reviewedKey = (reviewId: string) => `flow:review-reviewed:${reviewId}`;
const preferencesKey = "flow:review-diff-preferences";

function readReviewed(reviewId: string): Set<string> {
  try {
    const value = JSON.parse(localStorage.getItem(reviewedKey(reviewId)) ?? "[]");
    return new Set(Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : []);
  } catch {
    return new Set();
  }
}

function readPreferences(): { mode: DiffViewMode; wrap: boolean; hideReviewed: boolean } {
  try {
    const value = JSON.parse(localStorage.getItem(preferencesKey) ?? "{}");
    return { mode: value.mode === "split" ? "split" : "unified", wrap: value.wrap === true, hideReviewed: value.hideReviewed === true };
  } catch {
    return { mode: "unified", wrap: false, hideReviewed: false };
  }
}

export function DiffView({
  review,
  onReload,
  editable = false,
  onEditableChange,
  dirtyLines,
  onEditLine,
  mode: controlledMode,
  onModeChange,
}: {
  review: CodeReview;
  onReload: () => Promise<void>;
  editable?: boolean;
  onEditableChange?: (editable: boolean) => void;
  dirtyLines?: Set<string>;
  onEditLine?: (value: { path: string; lineNumber: number; content: string }) => void;
  mode?: DiffViewMode;
  onModeChange?: (mode: DiffViewMode) => void;
}) {
  const { t } = useI18n();
  const [preferences, setPreferences] = useState(readPreferences);
  const mode = controlledMode ?? preferences.mode;
  const updatePreferences = (next: Partial<typeof preferences>) =>
    setPreferences((current) => {
      const value = { ...current, ...next };
      try {
        localStorage.setItem(preferencesKey, JSON.stringify(value));
      } catch {
        /* Preferences still apply for this view when storage is unavailable. */
      }
      return value;
    });
  const setMode = (value: DiffViewMode) => (onModeChange ? onModeChange(value) : updatePreferences({ mode: value }));
  const [treeOpen, setTreeOpen] = useState(false);
  const [filter, setFilter] = useState("");
  const [displayOpen, setDisplayOpen] = useState(false);
  const [reviewed, setReviewed] = useState(() => readReviewed(review.id));
  const [collapsed, setCollapsed] = useState<Set<string>>(() => new Set(readReviewed(review.id)));
  const [hovered, setHovered] = useState<string | null>(null);
  const [commentLine, setCommentLine] = useState<DiffCommentTarget | null>(null);
  const [commentBody, setCommentBody] = useState("");
  const [commentBusy, setCommentBusy] = useState(false);
  const filterInput = useRef<HTMLInputElement>(null);
  const { files, ready } = useDiffFilesQuery(review, { variant: "basic", enableAgentContext: true });
  const threads = useMemo(() => reviewCodeThreads(review), [review]);
  const visibleFiles = files.filter((file) => !(preferences.hideReviewed && reviewed.has(file.path)));
  const treeFiles = visibleFiles.filter((file) => file.path.toLowerCase().includes(filter.trim().toLowerCase()));

  const persistReviewed = (next: Set<string>) => {
    setReviewed(next);
    try {
      localStorage.setItem(reviewedKey(review.id), JSON.stringify([...next]));
    } catch {
      /* Reviewed marks still apply for this view when storage is unavailable. */
    }
  };
  const toggleReviewed = (path: string) => {
    const next = new Set(reviewed);
    const marking = !next.has(path);
    if (marking) next.add(path);
    else next.delete(path);
    persistReviewed(next);
    // Marking a file reviewed collapses it; un-marking expands it again.
    setCollapsed((current) => {
      const value = new Set(current);
      if (marking) value.add(path);
      else value.delete(path);
      return value;
    });
  };
  const toggleCollapsed = (path: string) =>
    setCollapsed((current) => {
      const next = new Set(current);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });
  const scrollToFile = (path: string) => document.querySelector(`[data-diff-file="${CSS.escape(path)}"]`)?.scrollIntoView({ block: "start" });

  useEffect(() => {
    const match = /#file=([^&]+)/.exec(location.hash);
    if (!match || !ready) return;
    const path = decodeURIComponent(match[1]);
    const timer = window.setTimeout(() => scrollToFile(path), 30);
    return () => window.clearTimeout(timer);
  }, [ready]);

  const shortcuts = useOptionalPullRequestReviewShortcuts();
  useEffect(() => {
    if (!shortcuts) return;
    return shortcuts.register({
      focusFileFilter: () => {
        setTreeOpen(true);
        requestAnimationFrame(() => filterInput.current?.focus());
      },
      openDisplayOptions: () => setDisplayOpen(true),
      toggleLineWrapping: () => setPreferences((current) => ({ ...current, wrap: !current.wrap })),
      toggleHoveredFile: () => hovered && toggleCollapsed(hovered),
      markHoveredFileReviewed: () => hovered && toggleReviewed(hovered),
      toggleAllFiles: () => setCollapsed((current) => (current.size >= files.length ? new Set() : new Set(files.map((file) => file.path)))),
      markAllFilesReviewed: () => {
        persistReviewed(new Set(files.map((file) => file.path)));
        setCollapsed(new Set(files.map((file) => file.path)));
      },
    });
  });

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
      toast.error(error instanceof Error ? error.message : t("Could not submit comment"));
    } finally {
      setCommentBusy(false);
    }
  };

  return (
    <div className={`review-changes${editable ? " is-editable" : ""}${preferences.wrap ? " is-wrapped" : ""}`}>
      <header className="review-changes-toolbar">
        <button type="button" className="review-toolbar-pill" aria-pressed={treeOpen} onClick={() => setTreeOpen((value) => !value)}>
          <ListTree />
          <span>{t("Files")}</span>
          <small>{files.length}</small>
        </button>
        <span className="review-toolbar-pill is-static">
          <GitCommitHorizontal />
          <span>{t("Commits")}</span>
          <small>{review.commitCount}</small>
        </span>
        {!ready ? <em className="review-diff-compute-status">{t("Computing diffs…")}</em> : null}
        <Popover.Root open={displayOpen} onOpenChange={setDisplayOpen}>
          <Popover.Trigger asChild>
            <button type="button" className="review-ghost-button review-changes-display" aria-label={t("Display options")}>
              <DisplayIcon />
            </button>
          </Popover.Trigger>
          <Popover.Portal>
            <Popover.Content data-flow-motion="floating" className="review-list-options review-diff-options" align="end" sideOffset={5} collisionPadding={8}>
              <div className="review-list-options__section">
                <div className="review-diff-mode" role="group" aria-label={t("Diff view")}>
                  {(["split", "unified"] as const).map((value) => (
                    <button aria-pressed={mode === value} key={value} onClick={() => setMode(value)} type="button">
                      {value === "split" ? <Columns2 /> : <Rows2 />}
                      {t(value === "split" ? "Split" : "Unified")}
                    </button>
                  ))}
                </div>
              </div>
              <div className="review-list-options__section">
                <div className="review-list-options__row">
                  <span>{t("Wrap lines")}</span>
                  <Toggle label={t("Wrap lines")} checked={preferences.wrap} onChange={(wrap) => updatePreferences({ wrap })} />
                </div>
                <div className="review-list-options__row">
                  <span>{t("Hide reviewed")}</span>
                  <Toggle label={t("Hide reviewed")} checked={preferences.hideReviewed} onChange={(hideReviewed) => updatePreferences({ hideReviewed })} />
                </div>
                {onEditableChange && (
                  <div className="review-list-options__row">
                    <span>{t("Edit diff")}</span>
                    <Toggle label={t("Edit diff")} checked={editable} onChange={onEditableChange} />
                  </div>
                )}
              </div>
            </Popover.Content>
          </Popover.Portal>
        </Popover.Root>
      </header>
      <div className={`review-changes-body${treeOpen ? " has-tree" : ""}`}>
        {treeOpen && (
          <aside className="review-file-tree" aria-label={t("Files")}>
            <label>
              <Search />
              <input ref={filterInput} aria-label={t("Filter files...")} placeholder={t("Filter files...")} value={filter} onChange={(event) => setFilter(event.target.value)} />
            </label>
            <ul>
              {treeFiles.map((file) => {
                const { name, directory } = splitReviewPath(file.path);
                return (
                  <li key={file.path}>
                    <button type="button" aria-current={hovered === file.path || undefined} onClick={() => scrollToFile(file.path)}>
                      <FileCode2 />
                      <span className="review-file-name" data-i18n-ignore>
                        {name}
                      </span>
                      <span className="review-file-directory" data-i18n-ignore>
                        {directory}
                      </span>
                    </button>
                  </li>
                );
              })}
            </ul>
          </aside>
        )}
        <div className="review-changes-files">
          {visibleFiles.length === 0 ? (
            <div className="review-diff-empty">{t(files.length ? "All files are reviewed" : "No files changed")}</div>
          ) : (
            visibleFiles.map((file) => {
              const computed = file.computed;
              const lines = computed?.lines ?? [];
              const splitRows = computed?.splitRows ?? pairDiffLines(lines);
              const { name, directory } = splitReviewPath(file.path);
              const isCollapsed = collapsed.has(file.path);
              const isReviewed = reviewed.has(file.path);
              const lineContent = (line: number) => lines.find((item) => item.newNumber === line)?.content;
              return (
                <article key={file.path} data-diff-file={file.path} data-diff-kind={file.kind} onMouseEnter={() => setHovered(file.path)}>
                  <header>
                    <button
                      type="button"
                      className="review-file-collapse"
                      aria-expanded={!isCollapsed}
                      aria-label={t(isCollapsed ? "Expand file contents" : "Collapse file contents")}
                      onClick={() => toggleCollapsed(file.path)}
                    >
                      <FileCode2 className="review-file-icon" />
                      <ChevronDown className="review-file-chevron" />
                    </button>
                    <strong className="review-file-name" data-i18n-ignore>
                      {name}
                    </strong>
                    {directory && (
                      <span className="review-file-directory" data-i18n-ignore>
                        {directory}/
                      </span>
                    )}
                    {file.status === "added" || file.status === "deleted" || file.status === "renamed" ? (
                      <span className="review-file-status">{t(file.status === "added" ? "Added" : file.status === "deleted" ? "Deleted" : "Renamed")}</span>
                    ) : null}
                    {computed?.state === "computing" || computed?.state === "pending" ? <small className="review-diff-compute-status">{t("Computing…")}</small> : null}
                    <DiffCounts additions={file.additions} deletions={file.deletions} />
                    <label className="review-reviewed-toggle">
                      <input type="checkbox" checked={isReviewed} onChange={() => toggleReviewed(file.path)} />
                      <span>{t("Reviewed")}</span>
                    </label>
                    <FileActions path={file.path} url={review.url} />
                  </header>
                  {isCollapsed ? null : file.kind === "pdf" ? (
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
                      onComment={(target) => {
                        setCommentLine(target);
                        setCommentBody("");
                      }}
                      editable={editable}
                      dirtyLines={dirtyLines}
                      onEdit={(value) =>
                        onEditLine?.({
                          path: value.path,
                          lineNumber: value.line.newNumber ?? value.line.oldNumber ?? 0,
                          content: value.content,
                        })
                      }
                      renderAfterLine={(line) => {
                        const lineThreads = threads.get(`${file.path}:${line}`) ?? [];
                        const composing = commentLine?.path === file.path && commentLine.line === line;
                        if (!lineThreads.length && !composing) return null;
                        return (
                          <>
                            {lineThreads.map((thread) => (
                              <ReviewThread key={thread.root.id} review={review} thread={thread} originalLine={lineContent(line)} onReload={onReload} />
                            ))}
                            {composing && (
                              <DiffInlineComposer
                                line={line}
                                body={commentBody}
                                busy={commentBusy}
                                onBodyChange={setCommentBody}
                                onCancel={() => {
                                  setCommentLine(null);
                                  setCommentBody("");
                                }}
                                onSubmit={() => void submitInlineComment()}
                              />
                            )}
                          </>
                        );
                      }}
                    />
                  )}
                </article>
              );
            })
          )}
        </div>
      </div>
    </div>
  );
}

function FileActions({ path, url }: { path: string; url: string }) {
  const { t } = useI18n();
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(path);
      toast.success(t("Copied file path to clipboard"));
    } catch {
      toast.error(t("Could not copy to clipboard"));
    }
  };
  return (
    <RadixMenu.Root>
      <RadixMenu.Trigger asChild>
        <button type="button" className="review-ghost-button review-file-actions" aria-label={t("File actions")}>
          <MoreHorizontal />
        </button>
      </RadixMenu.Trigger>
      <RadixMenu.Portal>
        <RadixMenu.Content data-flow-motion="floating" className="project-action-menu" align="end" sideOffset={4} collisionPadding={16}>
          <RadixMenu.Item onSelect={() => void copy()}>
            <span className="project-menu-icon">
              <Copy />
            </span>
            <span className="project-menu-label">{t("Copy file path")}</span>
          </RadixMenu.Item>
          <RadixMenu.Item onSelect={() => window.open(`${url}/files`, "_blank", "noopener,noreferrer")}>
            <span className="project-menu-icon">
              <ExternalLink />
            </span>
            <span className="project-menu-label">{t("View changes on code host")}</span>
          </RadixMenu.Item>
        </RadixMenu.Content>
      </RadixMenu.Portal>
    </RadixMenu.Root>
  );
}
