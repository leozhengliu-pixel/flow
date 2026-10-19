import { Children, useRef, type ButtonHTMLAttributes, type ReactNode, type TextareaHTMLAttributes } from "react";
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
export function CodingSection({ title, titleAccessory, description, action, id, children, disabled, className }: { title: string; /** Rendered right after the title, e.g. an info tooltip. */ titleAccessory?: ReactNode; description?: ReactNode; action?: ReactNode; id?: string; children: ReactNode; disabled?: boolean; className?: string }) {
  const { t } = useI18n();
  return (
    <section id={id} className={`feature-section cs-section${disabled ? " is-disabled" : ""}${className ? ` ${className}` : ""}`} aria-disabled={disabled || undefined}>
      <header className="cs-section-header">
        <div>
          <h2>{t(title)}{titleAccessory}</h2>
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

export function CodingRow({ title, description, icon, figure, children, disabled, warning }: { title: string; description?: ReactNode; icon?: ReactNode; figure?: boolean; children?: ReactNode; disabled?: boolean; warning?: boolean }) {
  const { t } = useI18n();
  return (
    <div className={`feature-row cs-row${disabled ? " is-disabled" : ""}`}>
      {icon && <span className={figure ? "cs-row-figure" : "feature-row-icon"}>{icon}</span>}
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

export function CodingLinkRow({ title, description, icon, figure, detail, onClick }: { title: string; description?: string; icon?: ReactNode; /** Linear's 32px filled icon tile instead of the bordered 30px one. */ figure?: boolean; detail?: ReactNode; onClick: () => void }) {
  const { t } = useI18n();
  return (
    <button type="button" className={`feature-row feature-row-link cs-row${figure ? " cs-link-row" : ""}`} onClick={onClick}>
      {icon && <span className={figure ? "cs-row-figure" : "feature-row-icon"}>{icon}</span>}
      <div>
        <strong>{t(title)}</strong>
        {description && <span>{t(description)}</span>}
      </div>
      <aside>
        {detail && <span className="feature-state">{typeof detail === "string" ? t(detail) : detail}</span>}
        <ChevronRight size={figure ? 16 : 15} />
      </aside>
    </button>
  );
}

export function CodingButton({ children, primary, danger, ghost, small, ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { primary?: boolean; danger?: boolean; ghost?: boolean; /** The reference app's 24px control-filled pill (Add variable, Refresh environment, Retry terminal). */ small?: boolean }) {
  const { t } = useI18n();
  return (
    <button type="button" {...props} className={`feature-button${primary ? " primary" : ""}${danger ? " danger" : ""}${ghost ? " cs-ghost" : ""}${small ? " cs-small-button" : ""}${props.className ? ` ${props.className}` : ""}`}>
      {Children.map(children, (child) => (typeof child === "string" ? t(child) : child))}
    </button>
  );
}

export type CodingSelectOption = { value: string; label: string; detail?: string; translate?: boolean; icon?: ReactNode };

/**
 * Same trigger and menu as the other feature settings selects (`.feature-select`). `small` is the
 * reference app's 160x24 tool picker (12px text); options with `detail` render on two lines.
 */
export function CodingSelect({ label, value, options, onChange, disabled, placeholder, small }: { label: string; value: string; options: CodingSelectOption[]; onChange: (value: string) => void; disabled?: boolean; placeholder?: string; small?: boolean }) {
  const { t } = useI18n();
  const openedAt = useRef(0);
  const selected = options.find((item) => item.value === value);
  const text = (item: CodingSelectOption) => (item.translate === false ? item.label : t(item.label));
  return (
    <DropdownMenu onOpenChange={(open) => { if (open) openedAt.current = performance.now(); }}>
      <DropdownMenuTrigger asChild>
        <button type="button" role="combobox" aria-label={t(label)} disabled={disabled} className={`feature-select${small ? " cs-select-small" : ""}`}>
          <span data-i18n-ignore={selected?.translate === false ? true : undefined}>{selected ? text(selected) : placeholder ? t(placeholder) : value}</span>
          <FlowGlyph name="selectChevron" width={10} height={5} />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className={`feature-select-menu cs-select-menu${small ? " is-small" : ""}`}>
        {options.map((item) => (
          <DropdownMenuItem key={item.value} className={item.detail ? "has-detail" : undefined} data-i18n-ignore={item.translate === false ? true : undefined} onSelect={() => onChange(item.value)}>
            {item.icon}
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

/**
 * The reference app's code editor card: a 40px line-number gutter beside a monospace textarea
 * (13/19.5), 240px tall. The gutter follows the textarea's scroll position.
 */
export function CodingCodeEditor({ value, onChange, className = "", ...props }: Omit<TextareaHTMLAttributes<HTMLTextAreaElement>, "onChange" | "value"> & { value: string; onChange: (value: string) => void }) {
  const gutter = useRef<HTMLPreElement>(null);
  const lines = Math.max(1, value.split("\n").length);
  return (
    <div className={`cs-code-editor${props.disabled ? " is-disabled" : ""}${className ? ` ${className}` : ""}`}>
      <pre ref={gutter} className="cs-code-gutter" aria-hidden="true">{Array.from({ length: lines }, (_, index) => index + 1).join("\n")}</pre>
      <textarea
        spellCheck={false}
        autoCapitalize="off"
        autoCorrect="off"
        {...props}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        onScroll={(event) => {
          if (gutter.current) gutter.current.scrollTop = event.currentTarget.scrollTop;
        }}
      />
    </div>
  );
}
