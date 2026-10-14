import { useEffect, useRef, useState, type RefObject } from "react";
import { createPortal } from "react-dom";
import { useI18n } from "@/i18n/i18n";
import { membersPath, type SettingsPageId } from "@/lib/app-routes";
import { adminOnlyHint, type AdminHintControl } from "@/lib/settings-access";

const CONTROL_SELECTOR = 'button, input, select, textarea, [role="switch"], [role="combobox"]';

type Hint = { text: string; top: number; left: number };

/** The disabled control under the pointer, with how the reference app would describe it. */
function disabledControlAt(target: EventTarget | null, root: HTMLElement): { element: HTMLElement; control: AdminHintControl } | null {
  if (!(target instanceof Element)) return null;
  const element = target.closest<HTMLElement>(CONTROL_SELECTOR);
  if (!element || !root.contains(element)) return null;
  const disabled = (element as HTMLButtonElement).disabled || element.getAttribute("aria-disabled") === "true" || Boolean(element.closest("fieldset:disabled"));
  if (!disabled) return null;
  const type = element.getAttribute("type");
  const kind: AdminHintControl["kind"] =
    element.dataset.control === "toggle" || type === "checkbox" || element.getAttribute("role") === "switch" ? "toggle"
      : element.tagName === "SELECT" || element.getAttribute("role") === "combobox" ? "select"
      : element.tagName === "TEXTAREA" || element.tagName === "INPUT" ? "field"
      : "button";
  const label = (element.getAttribute("data-admin-hint-label") || element.getAttribute("aria-label") || element.textContent || "").trim();
  return { element, control: { kind, label } };
}

/**
 * The reference app explains settings a member cannot change with a hover card on the
 * disabled control — "Only admins can modify" and a "View admins →" link —
 * instead of a banner. Disabled controls do not reliably receive pointer
 * events, so the hint follows the pointer over the settings content.
 */
export function AdminOnlyHint({ containerRef, page, readOnly, workspaceSlug }: { containerRef: RefObject<HTMLElement | null>; page: SettingsPageId; readOnly: boolean; workspaceSlug: string }) {
  const { t } = useI18n();
  const [hint, setHint] = useState<Hint | null>(null);
  const current = useRef<HTMLElement | null>(null);
  const card = useRef<HTMLDivElement | null>(null);
  const hideTimer = useRef<number | undefined>(undefined);
  useEffect(() => {
    const root = containerRef.current;
    if (!root) return;
    const hide = () => {
      window.clearTimeout(hideTimer.current);
      hideTimer.current = window.setTimeout(() => { current.current = null; setHint(null); }, 120);
    };
    const move = (event: PointerEvent) => {
      const under = (typeof document.elementFromPoint === "function" ? document.elementFromPoint(event.clientX, event.clientY) : null) ?? event.target;
      if (card.current?.contains(under as Node)) { window.clearTimeout(hideTimer.current); return; }
      const found = disabledControlAt(under, root);
      const text = found ? adminOnlyHint(page, found.control, readOnly) : null;
      if (!found || !text) { if (current.current) hide(); return; }
      window.clearTimeout(hideTimer.current);
      if (current.current === found.element) return;
      current.current = found.element;
      const rect = found.element.getBoundingClientRect();
      setHint({ text, top: rect.bottom + 6, left: rect.left + rect.width / 2 });
    };
    root.addEventListener("pointermove", move);
    root.addEventListener("pointerleave", hide);
    window.addEventListener("scroll", hide, true);
    return () => {
      root.removeEventListener("pointermove", move);
      root.removeEventListener("pointerleave", hide);
      window.removeEventListener("scroll", hide, true);
      window.clearTimeout(hideTimer.current);
    };
  }, [containerRef, page, readOnly]);
  if (!hint) return null;
  return createPortal(
    <div
      ref={card}
      className="settings-admin-hint"
      role="tooltip"
      style={{ top: hint.top, left: hint.left }}
      onPointerEnter={() => window.clearTimeout(hideTimer.current)}
      onPointerLeave={() => { current.current = null; setHint(null); }}
    >
      <span>{t(hint.text)}</span>
      <a href={membersPath(workspaceSlug)}>{t("View admins →")}</a>
    </div>,
    document.body,
  );
}
