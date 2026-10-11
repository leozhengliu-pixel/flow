/**
 * Settings › Projects › Statuses — the workspace project status workflow, laid out like
 * The reference app's page: one card with a header per status type, an inline editor for create and
 * edit, a swatch / custom colour picker, a per-status "…" menu and drag-to-reorder.
 * Shared by the workspace page and the team project statuses page.
 */
import * as Popover from "@radix-ui/react-popover";
import {
  useMemo,
  useState,
  type CSSProperties,
  type KeyboardEvent,
} from "react";
import { toast } from "@/lib/toast";
import {
  createProjectStatus,
  deleteProjectStatus,
  reorderProjectStatuses,
  updateProjectStatus,
} from "@/lib/api";
import {
  PROJECT_STATUS_DEFAULT_COLORS,
  hexToLch,
  normalizeHexColor,
  projectStatusStrokeColor,
} from "@/lib/project-status-color";
import type { ResolvedTheme } from "@/lib/theme";
import { useResolvedTheme } from "@/hooks/use-resolved-theme";
import { ColorSwatchPicker } from "@/components/ui/color-swatch-picker";
import { useI18n } from "@/i18n/i18n";
import { ProjectStatusIcon } from "@/components/issue/issue-icons";
import { FlowGlyph } from "@/components/ui/menu-glyphs";
import { AppLink } from "@/components/ui/app-link";
import type { BootstrapData, ProjectStatus } from "@/types/flow";
import "./project-statuses-settings.css";

type StatusType = ProjectStatus["type"];
type StatusInput = { name: string; description: string; color: string; type: string };

const STATUS_SECTIONS: Array<{ type: StatusType; label: string }> = [
  { type: "backlog", label: "Backlog" },
  { type: "planned", label: "Planned" },
  { type: "started", label: "In Progress" },
  { type: "completed", label: "Completed" },
  { type: "canceled", label: "Canceled" },
];

/** Fraction of the progress ring an In Progress status shows: spread across the group, ½ when alone. */
function startedProgress(index: number, count: number) {
  return (index + 1) / (count + 1);
}

export function ProjectStatusesSettings({
  data,
  onReload,
}: {
  data: BootstrapData;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  return (
    <div className="pst-page" data-i18n-ignore>
      <header className="pst-header">
        <h1>{t("Project statuses")}</h1>
        <p>
          {t("Project statuses define the workflow that projects go through from start to completion")}
        </p>
      </header>
      <ProjectStatusesSection data={data} onReload={onReload} />
    </div>
  );
}

export function ProjectStatusesSection({
  data,
  onReload,
  disabled = false,
}: {
  data: BootstrapData;
  onReload: () => Promise<void>;
  disabled?: boolean;
}) {
  const { t } = useI18n();
  const theme = useResolvedTheme();
  const [creating, setCreating] = useState<StatusType | null>(null);
  const [editing, setEditing] = useState<string | null>(null);
  const [dragging, setDragging] = useState<string | null>(null);
  const ordered = useMemo(
    () => [...data.projectStatuses].sort((a, b) => (a.position ?? 0) - (b.position ?? 0)),
    [data.projectStatuses],
  );
  const run = async (action: () => Promise<unknown>) => {
    try {
      await action();
      await onReload();
      return true;
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not save setting"));
      return false;
    }
  };
  const reorder = (next: ProjectStatus[]) =>
    void run(() => reorderProjectStatuses(next.map((item) => item.id)));
  const move = (status: ProjectStatus, delta: number) => {
    const group = ordered.filter((item) => item.type === status.type);
    const index = group.findIndex((item) => item.id === status.id);
    const target = index + delta;
    if (disabled || target < 0 || target >= group.length) return;
    const next = [...ordered];
    const left = next.findIndex((item) => item.id === group[index].id);
    const right = next.findIndex((item) => item.id === group[target].id);
    [next[left], next[right]] = [next[right], next[left]];
    reorder(next);
  };
  const moveBefore = (status: ProjectStatus, target: ProjectStatus) => {
    if (disabled || status.id === target.id || status.type !== target.type) return;
    const next = ordered.filter((item) => item.id !== status.id);
    next.splice(next.findIndex((item) => item.id === target.id), 0, status);
    reorder(next);
  };
  const remove = (status: ProjectStatus, groupSize: number, usage: number) => {
    if (groupSize === 1) {
      toast(t("Can't delete status"), {
        description: t("You can't delete the last status of a type."),
      });
      return;
    }
    if (usage > 0) {
      toast(t('Can\'t delete the "{name}" project status').replace("{name}", status.name), {
        description: t(
          usage === 1
            ? "The status has {count} project assigned. Please archive or move them before deleting the status."
            : "The status has {count} projects assigned. Please archive or move them before deleting the status.",
        ).replace("{count}", String(usage)),
      });
      return;
    }
    void run(() => deleteProjectStatus(status.id));
  };
  const busy = disabled || creating !== null || editing !== null;
  return (
    <section
      className="pst-card"
      role="list"
      aria-label={t("Project statuses")}
      data-disabled={disabled || undefined}
    >
      {STATUS_SECTIONS.map((section) => {
        const statuses = ordered.filter((item) => item.type === section.type);
        const canReorder = !disabled && statuses.length > 1;
        return (
          <div className="pst-group" role="list" key={section.type} data-status-type={section.type}>
            <header className="pst-group-header">
              <h3>{t(section.label)}</h3>
              <button
                type="button"
                className="pst-icon-button pst-add"
                aria-label={t("Create new project status")}
                disabled={busy}
                onClick={() => setCreating(section.type)}
              >
                <FlowGlyph name="plus" size={14} />
              </button>
            </header>
            {statuses.map((status, index) => {
              const usage = data.projects.filter((item) => item.status.id === status.id).length;
              const progress = section.type === "started" ? startedProgress(index, statuses.length) : undefined;
              return editing === status.id ? (
                <StatusEditor
                  key={status.id}
                  type={section.type}
                  status={status}
                  theme={theme}
                  onCancel={() => setEditing(null)}
                  onSave={async (input) => {
                    if (await run(() => updateProjectStatus(status.id, input))) setEditing(null);
                  }}
                />
              ) : (
                <StatusRow
                  key={status.id}
                  status={status}
                  usage={usage}
                  progress={progress}
                  theme={theme}
                  canEdit={!disabled}
                  canReorder={canReorder}
                  dragging={dragging === status.id}
                  onDragStart={() => setDragging(status.id)}
                  onDragEnd={() => setDragging(null)}
                  onDrop={() => {
                    const source = ordered.find((item) => item.id === dragging);
                    if (source) moveBefore(source, status);
                    setDragging(null);
                  }}
                  onEdit={() => {
                    setCreating(null);
                    setEditing(status.id);
                  }}
                  onMove={(delta) => move(status, delta)}
                  onDelete={() => remove(status, statuses.length, usage)}
                />
              );
            })}
            {creating === section.type && (
              <StatusEditor
                type={section.type}
                theme={theme}
                onCancel={() => setCreating(null)}
                onSave={async (input) => {
                  if (await run(() => createProjectStatus(input))) setCreating(null);
                }}
              />
            )}
          </div>
        );
      })}
    </section>
  );
}

function StatusMark({
  color,
  type,
  progress,
  theme,
  framed = true,
}: {
  color: string;
  type: StatusType;
  progress?: number;
  theme: ResolvedTheme;
  framed?: boolean;
}) {
  const icon = (
    <ProjectStatusIcon
      aria-hidden
      color={projectStatusStrokeColor(color, theme)}
      progress={progress === undefined ? undefined : (progress - 0.15) / 0.7}
      type={type}
    />
  );
  if (!framed) return icon;
  return (
    <div className="pst-mark" style={{ "--status-color": color } as CSSProperties}>
      {icon}
    </div>
  );
}

function StatusRow({
  status,
  usage,
  progress,
  theme,
  canEdit,
  canReorder,
  dragging,
  onDragStart,
  onDragEnd,
  onDrop,
  onEdit,
  onMove,
  onDelete,
}: {
  status: ProjectStatus;
  usage: number;
  progress?: number;
  theme: ResolvedTheme;
  canEdit: boolean;
  canReorder: boolean;
  dragging: boolean;
  onDragStart: () => void;
  onDragEnd: () => void;
  onDrop: () => void;
  onEdit: () => void;
  onMove: (delta: number) => void;
  onDelete: () => void;
}) {
  const { t } = useI18n();
  const [menuOpen, setMenuOpen] = useState(false);
  const viewHref = `/${location.pathname.split("/")[1]}/projects/all?status=${encodeURIComponent(status.id)}`;
  const usageLabel = t(usage === 1 ? "{count} project" : "{count} projects").replace("{count}", String(usage));
  const menuKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const items = [...event.currentTarget.querySelectorAll<HTMLButtonElement>("[role=menuitem]")];
    const index = items.indexOf(document.activeElement as HTMLButtonElement);
    const focus = (next: number) => {
      event.preventDefault();
      items[(next + items.length) % items.length]?.focus();
    };
    if (event.key === "ArrowDown") focus(index < 0 ? 0 : index + 1);
    else if (event.key === "ArrowUp") focus(index < 0 ? items.length - 1 : index - 1);
    else if (event.key === "Home") focus(0);
    else if (event.key === "End") focus(items.length - 1);
  };
  const choose = (action: () => void) => {
    setMenuOpen(false);
    action();
  };
  return (
    <div
      className="pst-row"
      role="listitem"
      aria-roledescription={canReorder ? t("sortable") : undefined}
      data-status-id={status.id}
      data-dragging={dragging || undefined}
      data-menu-open={menuOpen || undefined}
      tabIndex={canReorder ? 0 : -1}
      draggable={canReorder}
      onDragStart={onDragStart}
      onDragEnd={onDragEnd}
      onDragOver={(event) => {
        if (canReorder) event.preventDefault();
      }}
      onDrop={(event) => {
        event.preventDefault();
        onDrop();
      }}
      onKeyDown={(event) => {
        if (!canReorder || !event.altKey || event.target !== event.currentTarget) return;
        if (event.key === "ArrowUp" || event.key === "ArrowDown") {
          event.preventDefault();
          onMove(event.key === "ArrowUp" ? -1 : 1);
        }
      }}
    >
      {canReorder && <StatusDragHandle />}
      <div className="pst-row-main">
        <StatusMark color={status.color} type={status.type} progress={progress} theme={theme} />
        <div className="pst-copy">
          <div className="pst-name-line">
            <span className="pst-name" data-i18n-ignore>{status.name}</span>
          </div>
          {(usage > 0 || status.description) && (
            <div className="pst-meta">
              {usage > 0 && (
                <AppLink className="pst-usage" aria-label={t("View projects")} href={viewHref}>
                  {usageLabel}
                </AppLink>
              )}
              {usage > 0 && status.description && <span aria-hidden="true">·</span>}
              {status.description && (
                <span className="pst-description" data-i18n-ignore>{status.description}</span>
              )}
            </div>
          )}
        </div>
      </div>
      <div className="pst-actions">
        {canEdit && (
          <Popover.Root open={menuOpen} onOpenChange={setMenuOpen}>
            <div className="pst-more-wrap">
              <Popover.Trigger asChild>
                <button type="button" className="pst-icon-button pst-more" aria-label={t("Open menu")}>
                  <FlowGlyph name="ellipsis" />
                </button>
              </Popover.Trigger>
            </div>
            <Popover.Portal>
              <Popover.Content
                data-flow-motion="floating"
                className="pst-menu"
                align="end"
                sideOffset={4}
                data-i18n-ignore
                onKeyDown={menuKeyDown}
                onOpenAutoFocus={(event) => {
                  // Like the reference app, nothing is highlighted until the pointer or the arrow keys pick a row.
                  event.preventDefault();
                  (event.currentTarget as HTMLElement | null)?.querySelector<HTMLElement>("[role=menu]")?.focus();
                }}
              >
                <div role="menu" tabIndex={-1} aria-label={t("Project status actions")}>
                  <button type="button" role="menuitem" data-menu-action="edit" onClick={() => choose(onEdit)}>
                    <span className="pst-menu-highlight" aria-hidden="true" />
                    <FlowGlyph name="edit" data-menu-glyph="edit" />
                    <span>{t("Edit")}</span>
                  </button>
                  <button type="button" role="menuitem" data-menu-action="delete" onClick={() => choose(onDelete)}>
                    <span className="pst-menu-highlight" aria-hidden="true" />
                    <FlowGlyph name="delete" data-menu-glyph="delete" />
                    <span>{t("Delete")}</span>
                  </button>
                </div>
              </Popover.Content>
            </Popover.Portal>
          </Popover.Root>
        )}
      </div>
    </div>
  );
}

function StatusDragHandle() {
  const { t } = useI18n();
  return (
    <span className="pst-drag-handle" aria-hidden="true" title={t("Drag to reorder")}>
      <svg width="6" height="10" viewBox="0 0 6 10">
        <path
          fillRule="evenodd"
          d="M1 8a1 1 0 1 1 0 2 1 1 0 0 1 0-2Zm4 0a1 1 0 1 1 0 2 1 1 0 0 1 0-2ZM1 4a1 1 0 1 1 0 2 1 1 0 0 1 0-2Zm4 0a1 1 0 1 1 0 2 1 1 0 0 1 0-2ZM1 0a1 1 0 1 1 0 2 1 1 0 0 1 0-2Zm4 0a1 1 0 1 1 0 2 1 1 0 0 1 0-2Z"
        />
      </svg>
    </span>
  );
}

function StatusEditor({
  type,
  status,
  theme,
  onCancel,
  onSave,
}: {
  type: StatusType;
  status?: ProjectStatus;
  theme: ResolvedTheme;
  onCancel: () => void;
  onSave: (input: StatusInput) => Promise<void>;
}) {
  const { t } = useI18n();
  const [name, setName] = useState(status?.name ?? "");
  const [description, setDescription] = useState(status?.description ?? "");
  const [color, setColor] = useState(status?.color ?? PROJECT_STATUS_DEFAULT_COLORS[type]);
  const [saving, setSaving] = useState(false);
  const submit = async () => {
    if (!name.trim() || saving) return;
    setSaving(true);
    try {
      await onSave({ name: name.trim(), description: description.trim(), color, type });
    } finally {
      setSaving(false);
    }
  };
  return (
    <form
      className="pst-editor"
      data-status-id={status?.id}
      onSubmit={(event) => {
        event.preventDefault();
        void submit();
      }}
      onKeyDown={(event) => {
        if (event.key === "Escape" && !event.defaultPrevented) onCancel();
      }}
    >
      <StatusColorPicker color={color} type={type} theme={theme} onChange={setColor} />
      <input
        autoFocus
        required
        className="pst-field pst-field-name"
        aria-label={t("Name")}
        placeholder={t("Name")}
        value={name}
        onChange={(event) => setName(event.target.value)}
      />
      <input
        className="pst-field pst-field-description"
        aria-label={t("Description")}
        placeholder={t("Description…")}
        value={description}
        onChange={(event) => setDescription(event.target.value)}
      />
      <div className="pst-editor-actions">
        <button type="button" className="pst-button" aria-label={t("Cancel")} onClick={onCancel}>
          {t("Cancel")}
        </button>
        <button type="submit" className="pst-button pst-button-primary" aria-label={t("Submit")} disabled={saving}>
          {t(status ? "Save" : "Create")}
        </button>
      </div>
    </form>
  );
}

function StatusColorPicker({
  color,
  type,
  theme,
  onChange,
}: {
  color: string;
  type: StatusType;
  theme: ResolvedTheme;
  onChange: (color: string) => void;
}) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const hex = normalizeHexColor(color) ?? lchToHexFallback(color);
  const adjusted = projectStatusStrokeColor(hex, theme) !== hex;
  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Trigger asChild>
        <button type="button" className="pst-color-trigger" aria-label={t("Color")} data-color={color}>
          <StatusMark color={color} type={type} theme={theme} framed={false} progress={type === "started" ? 0.5 : undefined} />
        </button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content
          data-flow-motion="floating"
          className="csp-popover pst-color-menu"
          align="center"
          sideOffset={2}
          collisionPadding={8}
          data-i18n-ignore
          onCloseAutoFocus={(event) => event.preventDefault()}
          onKeyDown={(event) => {
            if (event.key === "Escape") {
              event.stopPropagation();
              event.preventDefault();
              setOpen(false);
            }
          }}
        >
          <ColorSwatchPicker
            color={hex}
            framed
            note={adjusted ? t("Contrast has been adjusted") : undefined}
            surface="elevated"
            onChange={(next, source) => {
              if (next !== hex) onChange(next);
              if (source === "swatch") setOpen(false);
            }}
          />
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}

/** Statuses written in LCH (no hex) start the custom picker from their closest sRGB hex. */
function lchToHexFallback(value: string) {
  const match = /^lch\(([\d.]+)%?\s+([\d.]+)\s+([\d.]+)/.exec(value.trim());
  if (!match) return "#8a8f98";
  // Search the hex whose LCH is nearest; coarse grid then refine is plenty for a picker start point.
  let best = "#8a8f98";
  let bestDistance = Infinity;
  const target = [Number(match[1]), Number(match[2]), Number(match[3])];
  for (let r = 0; r < 256; r += 17)
    for (let g = 0; g < 256; g += 17)
      for (let b = 0; b < 256; b += 17) {
        const candidate = `#${[r, g, b].map((part) => part.toString(16).padStart(2, "0")).join("")}`;
        const lch = hexToLch(candidate);
        if (!lch) continue;
        const dh = Math.min(Math.abs(lch[2] - target[2]), 360 - Math.abs(lch[2] - target[2]));
        const distance = (lch[0] - target[0]) ** 2 + (lch[1] - target[1]) ** 2 + (dh * lch[1] / 60) ** 2;
        if (distance < bestDistance) [best, bestDistance] = [candidate, distance];
      }
  return best;
}

