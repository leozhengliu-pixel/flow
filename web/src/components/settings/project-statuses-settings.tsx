/**
 * Settings › Projects › Statuses — the workspace project status workflow, laid out like
 * The reference app's page: one card with a header per status type, an inline editor for create and
 * edit, a swatch / custom colour picker, a per-status "…" menu and drag-to-reorder.
 * Shared by the workspace page and the team project statuses page.
 */
import * as Popover from "@radix-ui/react-popover";
import {
  useEffect,
  useMemo,
  useRef,
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
  PROJECT_STATUS_SWATCHES,
  hexToLch,
  normalizeHexColor,
  projectStatusStrokeColor,
} from "@/lib/project-status-color";
import { getResolvedTheme, THEME_CHANGE_EVENT, type ResolvedTheme } from "@/lib/theme";
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

function useResolvedTheme(): ResolvedTheme {
  const [theme, setTheme] = useState<ResolvedTheme>(getResolvedTheme);
  useEffect(() => {
    const update = () => setTheme(getResolvedTheme());
    window.addEventListener(THEME_CHANGE_EVENT, update);
    return () => window.removeEventListener(THEME_CHANGE_EVENT, update);
  }, []);
  return theme;
}

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
  const isSwatch = PROJECT_STATUS_SWATCHES.some((swatch) => swatch.color === normalizeHexColor(color));
  const [custom, setCustom] = useState(false);
  return (
    <Popover.Root
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (next) setCustom(false);
      }}
    >
      <Popover.Trigger asChild>
        <button type="button" className="pst-color-trigger" aria-label={t("Color")} data-color={color}>
          <StatusMark color={color} type={type} theme={theme} framed={false} progress={type === "started" ? 0.5 : undefined} />
        </button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content
          data-flow-motion="floating"
          className="pst-color-menu"
          data-mode={custom ? "custom" : "swatches"}
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
          {custom ? (
            <CustomColorPanel color={color} theme={theme} onChange={onChange} />
          ) : (
            <div className="pst-swatches" role="radiogroup" aria-label={t("Color")}>
              {PROJECT_STATUS_SWATCHES.map((swatch) => {
                const selected = normalizeHexColor(color) === swatch.color;
                return (
                  <button
                    key={swatch.color}
                    type="button"
                    role="radio"
                    aria-checked={selected}
                    aria-label={t(swatch.name)}
                    className="pst-swatch"
                    data-color={swatch.color}
                    style={{ "--palette-color": projectStatusStrokeColor(swatch.color, theme) } as CSSProperties}
                    onClick={() => {
                      onChange(swatch.color);
                      setOpen(false);
                    }}
                  >
                    {selected && <FlowGlyph name="swatchCheck" width={10} height={9} />}
                  </button>
                );
              })}
            </div>
          )}
          <div className="pst-custom-toggle-wrap" data-separated={!custom || undefined}>
            <button
              type="button"
              className="pst-custom-toggle"
              aria-label={t(custom ? "Select default color" : "Set custom color")}
              aria-pressed={custom}
              title={t(custom ? "Select default color" : "Set custom color")}
              data-custom-color={!isSwatch || undefined}
              onClick={() => setCustom((value) => !value)}
            >
              <span aria-hidden="true" />
            </button>
          </div>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}

function CustomColorPanel({
  color,
  theme,
  onChange,
}: {
  color: string;
  theme: ResolvedTheme;
  onChange: (color: string) => void;
}) {
  const { t } = useI18n();
  const saturationRef = useRef<HTMLDivElement>(null);
  const hueRef = useRef<HTMLDivElement>(null);
  const hex = normalizeHexColor(color) ?? lchToHexFallback(color);
  const hsv = hexToHsv(hex);
  const [draft, setDraft] = useState(hex);
  const [syncedHex, setSyncedHex] = useState(hex);
  if (syncedHex !== hex) {
    setSyncedHex(hex);
    setDraft(hex);
  }
  const commit = (next: string) => {
    const normalized = normalizeHexColor(next);
    if (normalized && normalized !== hex) onChange(normalized);
  };
  const pickSaturation = (clientX: number, clientY: number) => {
    const rect = saturationRef.current?.getBoundingClientRect();
    if (!rect) return;
    commit(hsvToHex(hsv.h, clamp((clientX - rect.left) / rect.width), clamp(1 - (clientY - rect.top) / rect.height)));
  };
  const pickHue = (clientY: number) => {
    const rect = hueRef.current?.getBoundingClientRect();
    if (!rect) return;
    commit(hsvToHex(clamp((clientY - rect.top) / rect.height) * 359.999, hsv.s, hsv.v));
  };
  const adjusted = projectStatusStrokeColor(hex, theme) !== hex;
  return (
    <div className="pst-custom">
      <div className="pst-custom-value">
        <span className="pst-custom-current" aria-hidden="true" style={{ "--palette-color": hex } as CSSProperties} />
        <label>
          <span>HEX</span>
          <input
            aria-label="HEX"
            spellCheck={false}
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            onBlur={() => (normalizeHexColor(draft) ? commit(draft) : setDraft(hex))}
            onKeyDown={(event) => {
              if (event.key === "Enter") {
                event.preventDefault();
                commit(draft);
              }
            }}
          />
        </label>
        {adjusted && <span className="pst-custom-note">{t("Contrast has been adjusted")}</span>}
      </div>
      <div className="pst-custom-controls">
        <div
          ref={saturationRef}
          className="pst-saturation"
          role="slider"
          tabIndex={0}
          aria-label={t("Saturation and brightness")}
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={Math.round(hsv.s * 100)}
          style={{ "--picker-hue": hsvToHex(hsv.h, 1, 1) } as CSSProperties}
          onPointerDown={(event) => {
            event.currentTarget.setPointerCapture?.(event.pointerId);
            pickSaturation(event.clientX, event.clientY);
          }}
          onPointerMove={(event) => {
            if (event.currentTarget.hasPointerCapture?.(event.pointerId)) pickSaturation(event.clientX, event.clientY);
          }}
          onKeyDown={(event) => {
            const step = { ArrowLeft: [-0.01, 0], ArrowRight: [0.01, 0], ArrowDown: [0, -0.01], ArrowUp: [0, 0.01] }[event.key];
            if (!step) return;
            event.preventDefault();
            commit(hsvToHex(hsv.h, clamp(hsv.s + step[0]), clamp(hsv.v + step[1])));
          }}
        >
          <i style={{ left: `${hsv.s * 100}%`, top: `${(1 - hsv.v) * 100}%`, background: hex }} />
        </div>
        <div
          ref={hueRef}
          className="pst-hue"
          role="slider"
          tabIndex={0}
          aria-label={t("Hue")}
          aria-valuemin={0}
          aria-valuemax={360}
          aria-valuenow={Math.round(hsv.h)}
          onPointerDown={(event) => {
            event.currentTarget.setPointerCapture?.(event.pointerId);
            pickHue(event.clientY);
          }}
          onPointerMove={(event) => {
            if (event.currentTarget.hasPointerCapture?.(event.pointerId)) pickHue(event.clientY);
          }}
          onKeyDown={(event) => {
            const delta = event.key === "ArrowUp" ? -2 : event.key === "ArrowDown" ? 2 : 0;
            if (!delta) return;
            event.preventDefault();
            commit(hsvToHex((hsv.h + delta + 360) % 360, hsv.s, hsv.v));
          }}
        >
          <i style={{ top: `${(hsv.h / 360) * 100}%` }} />
        </div>
      </div>
    </div>
  );
}

function clamp(value: number) {
  return Math.min(1, Math.max(0, value));
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

function hexToHsv(value: string) {
  const number = Number.parseInt((normalizeHexColor(value) ?? "#8a8f98").slice(1), 16);
  const r = ((number >> 16) & 255) / 255;
  const g = ((number >> 8) & 255) / 255;
  const b = (number & 255) / 255;
  const max = Math.max(r, g, b);
  const delta = max - Math.min(r, g, b);
  let h = 0;
  if (delta) {
    if (max === r) h = 60 * (((g - b) / delta) % 6);
    else if (max === g) h = 60 * ((b - r) / delta + 2);
    else h = 60 * ((r - g) / delta + 4);
  }
  return { h: h < 0 ? h + 360 : h, s: max === 0 ? 0 : delta / max, v: max };
}

function hsvToHex(h: number, s: number, v: number) {
  const c = v * s;
  const x = c * (1 - Math.abs(((h / 60) % 2) - 1));
  const m = v - c;
  const [r, g, b] =
    h < 60 ? [c, x, 0] : h < 120 ? [x, c, 0] : h < 180 ? [0, c, x] : h < 240 ? [0, x, c] : h < 300 ? [x, 0, c] : [c, 0, x];
  return `#${[r, g, b].map((part) => Math.round((part + m) * 255).toString(16).padStart(2, "0")).join("")}`;
}
