import { Children, useRef, type ButtonHTMLAttributes, type ReactNode } from "react";
import { Check, ChevronRight, TriangleAlert } from "lucide-react";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { FlowGlyph } from "@/components/ui/menu-glyphs";
import { useI18n } from "@/i18n/i18n";
import { SettingsToggle } from "../settings-primitives";

/**
 * Settings chrome for the coding sessions pages. Reuses feature-settings.css
 * (.feature-header/.feature-card/.feature-row), which mirrors the reference app's measured
 * settings layout: 640px column, 24/32 title, 65px rows, 10px card radius.
 */
export function CodingSection({ title, description, action, id, children, disabled }: { title: string; description?: ReactNode; action?: ReactNode; id?: string; children: ReactNode; disabled?: boolean }) {
  const { t } = useI18n();
  return (
    <section id={id} className={`feature-section cs-section${disabled ? " is-disabled" : ""}`} aria-disabled={disabled || undefined}>
      <header className="cs-section-header">
        <div>
          <h2>{t(title)}</h2>
          {description && <p>{typeof description === "string" ? t(description) : description}</p>}
        </div>
        {action}
      </header>
      {children}
    </section>
  );
}

export function CodingCard({ children, className = "" }: { children: ReactNode; className?: string }) {
  return <div className={`feature-card${className ? ` ${className}` : ""}`}>{children}</div>;
}

export function CodingRow({ title, description, icon, children, disabled, warning }: { title: string; description?: ReactNode; icon?: ReactNode; children?: ReactNode; disabled?: boolean; warning?: boolean }) {
  const { t } = useI18n();
  return (
    <div className={`feature-row cs-row${disabled ? " is-disabled" : ""}`}>
      {icon && <span className="feature-row-icon">{icon}</span>}
      <div>
        <strong>{t(title)}</strong>
        {description && (
          <span className={warning ? "cs-row-warning" : undefined}>
            {warning && <TriangleAlert size={12} aria-hidden="true" />}
            {typeof description === "string" ? t(description) : description}
          </span>
        )}
      </div>
      {children && <aside>{children}</aside>}
    </div>
  );
}

export function CodingLinkRow({ title, description, icon, detail, onClick }: { title: string; description?: string; icon?: ReactNode; detail?: ReactNode; onClick: () => void }) {
  const { t } = useI18n();
  return (
    <button type="button" className="feature-row feature-row-link cs-row" onClick={onClick}>
      {icon && <span className="feature-row-icon">{icon}</span>}
      <div>
        <strong>{t(title)}</strong>
        {description && <span>{t(description)}</span>}
      </div>
      <aside>
        {detail && <span className="feature-state">{typeof detail === "string" ? t(detail) : detail}</span>}
        <ChevronRight size={15} />
      </aside>
    </button>
  );
}

export function CodingButton({ children, primary, danger, ghost, ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { primary?: boolean; danger?: boolean; ghost?: boolean }) {
  const { t } = useI18n();
  return (
    <button type="button" {...props} className={`feature-button${primary ? " primary" : ""}${danger ? " danger" : ""}${ghost ? " cs-ghost" : ""}${props.className ? ` ${props.className}` : ""}`}>
      {Children.map(children, (child) => (typeof child === "string" ? t(child) : child))}
    </button>
  );
}

export type CodingSelectOption = { value: string; label: string; detail?: string; translate?: boolean };

/** Same trigger and menu as the other feature settings selects (`.feature-select`). */
export function CodingSelect({ label, value, options, onChange, disabled, placeholder }: { label: string; value: string; options: CodingSelectOption[]; onChange: (value: string) => void; disabled?: boolean; placeholder?: string }) {
  const { t } = useI18n();
  const openedAt = useRef(0);
  const selected = options.find((item) => item.value === value);
  const text = (item: CodingSelectOption) => (item.translate === false ? item.label : t(item.label));
  return (
    <DropdownMenu onOpenChange={(open) => { if (open) openedAt.current = performance.now(); }}>
      <DropdownMenuTrigger asChild>
        <button type="button" role="combobox" aria-label={t(label)} disabled={disabled} className="feature-select">
          <span data-i18n-ignore={selected?.translate === false ? true : undefined}>{selected ? text(selected) : placeholder ? t(placeholder) : value}</span>
          <FlowGlyph name="selectChevron" width={10} height={5} />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="feature-select-menu cs-select-menu">
        {options.map((item) => (
          <DropdownMenuItem key={item.value} data-i18n-ignore={item.translate === false ? true : undefined} onSelect={() => onChange(item.value)}>
            <span className="cs-select-option">
              <span>{text(item)}</span>
              {item.detail && <small>{t(item.detail)}</small>}
            </span>
            {item.value === value && <Check size={13} />}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

export function CodingToggle({ checked, disabled, label, onChange }: { checked: boolean; disabled?: boolean; label: string; onChange: (value: boolean) => void }) {
  const { t } = useI18n();
  return <SettingsToggle checked={checked} disabled={disabled} label={t(label)} onChange={onChange} />;
}

/** Labelled form field used by the environment editor. */
export function CodingField({ label, hint, children, htmlFor }: { label: string; hint?: ReactNode; children: ReactNode; htmlFor?: string }) {
  const { t } = useI18n();
  return (
    <div className="cs-field">
      <label htmlFor={htmlFor}>{t(label)}</label>
      {children}
      {hint && <p className="cs-field-hint">{typeof hint === "string" ? t(hint) : hint}</p>}
    </div>
  );
}
