import { FlowGlyph } from "@/components/ui/menu-glyphs";
import * as SelectPrimitive from "@radix-ui/react-select";
import { Fragment, useContext } from "react";
import { createPortal } from "react-dom";

import { SettingsReadOnlyCrumbSlot } from "./settings-read-only-context";
import type {
  KeyboardEventHandler,
  MouseEventHandler,
  ReactNode,
} from "react";


import { Toggle } from "@/components/ui/toggle";
import { ViewGlyph } from "@/components/views/view-icon-picker";
import type { Team } from "@/types/flow";
import { isPeopleProperty } from '@/lib/people'
import { PropertyMenu } from '@/components/property/property-menu'
import { usePhoneLayout } from '@/hooks/use-phone-layout'
import "./settings-select.css";

export function SettingsPageTitle({
  action,
  children,
  className = "",
  description,
}: {
  action?: ReactNode;
  children: ReactNode;
  className?: string;
  description?: ReactNode;
}) {
  return (
    <header
      className={`settings-page-header${className ? ` ${className}` : ""}`}
    >
      <div>
        <h1>{children}</h1>
        {description && <p>{description}</p>}
      </div>
      {action}
    </header>
  );
}

export function SettingsSection({
  action,
  children,
  className = "",
  description,
  grouped = false,
  headerClassName = "",
  id,
  title,
}: {
  action?: ReactNode;
  children: ReactNode;
  className?: string;
  description?: ReactNode;
  /** Children are SettingsGroup blocks, each with its own card. */
  grouped?: boolean;
  headerClassName?: string;
  id?: string;
  title?: ReactNode;
}) {
  return (
    <section
      className={`settings-section${className ? ` ${className}` : ""}`}
      id={id}
    >
      {action ? (
        <header className={headerClassName || "settings-section-title"}>
          {title && <h3>{title}</h3>}
          {action}
        </header>
      ) : (
        title && <h3>{title}</h3>
      )}
      {description && (
        <p className="settings-section-description">{description}</p>
      )}
      {grouped ? children : <div className="settings-card">{children}</div>}
    </section>
  );
}

export function SettingsGroup({
  children,
  description,
  title,
}: {
  children: ReactNode;
  description?: ReactNode;
  title: ReactNode;
}) {
  return (
    <div className="settings-group">
      <header>
        <strong>{title}</strong>
        {description && <p>{description}</p>}
      </header>
      <div className="settings-card">{children}</div>
    </div>
  );
}

export function SettingsRow({
  children,
  className = "",
  control = true,
  danger = false,
  description,
  icon,
  onClick,
  onKeyDown,
  role,
  tabIndex,
  title,
}: {
  children?: ReactNode;
  className?: string;
  control?: boolean;
  danger?: boolean;
  description?: ReactNode;
  icon?: ReactNode;
  onClick?: MouseEventHandler<HTMLDivElement>;
  onKeyDown?: KeyboardEventHandler<HTMLDivElement>;
  role?: string;
  tabIndex?: number;
  title: ReactNode;
}) {
  return (
    <div
      className={`settings-row${danger ? " danger" : ""}${className ? ` ${className}` : ""}`}
      onClick={onClick}
      onKeyDown={onKeyDown}
      role={role}
      tabIndex={tabIndex}
    >
      {icon && <span className="settings-row-icon">{icon}</span>}
      <div className="settings-row-copy">
        <strong>{title}</strong>
        {(description || description === "") && <span>{description}</span>}
      </div>
      {children &&
        (control ? (
          <div className="settings-control">{children}</div>
        ) : (
          children
        ))}
    </div>
  );
}

export function SettingsToggle({
  checked,
  disabled,
  id,
  label,
  onChange,
}: {
  checked: boolean;
  disabled?: boolean;
  id?: string;
  label: string;
  onChange: (value: boolean) => void | Promise<void>;
}) {
  return (
    <Toggle
      checked={checked}
      disabled={disabled}
      id={id}
      label={label}
      onChange={onChange}
      size="regular"
    />
  );
}

export type SettingsSelectOption =
  | string
  | { value: string; label: string; disabled?: boolean; entityName?: boolean; /** Extra words the searchable menu matches. */ keywords?: string; icon?: ReactNode; /** Draws a separator above the option. */ divider?: boolean; /** Muted text after the label, shown in the menu only. */ hint?: string; /** Dimmed text after the label, e.g. "(default)". */ suffix?: string };

/** A parenthesised suffix (a scale's points) is a quiet detail, smaller than the label. */
const suffixClass = (suffix: string) => (/^[(（]/.test(suffix) ? "settings-select-suffix is-detail" : "settings-select-suffix");
const suffixGap = (suffix: string) => (suffix.startsWith("（") ? "" : " ");
const withSuffix = (option: { label: string; suffix?: string }) =>
  option.suffix ? `${option.label}${suffixGap(option.suffix)}${option.suffix}` : option.label;

export function SettingsSelect({
  align = "end",
  alignOffset,
  className = "",
  disabled,
  entityName,
  label,
  menuClassName = "",
  onChange,
  options,
  placeholder,
  searchPlaceholder,
  nativeSuffix = true,
  value,
}: {
  align?: "start" | "center" | "end";
  /** Shifts the open menu along its alignment edge, in px. */
  alignOffset?: number;
  className?: string;
  disabled?: boolean;
  entityName?: (value: string) => boolean;
  label: string;
  menuClassName?: string;
  onChange: (value: string) => void;
  options: SettingsSelectOption[];
  /** Shown when the value matches no option. */
  placeholder?: string;
  /** Opens a searchable menu (long lists) instead of the plain select. */
  searchPlaceholder?: string;
  /** false: the phone's native select lists the plain labels, without the muted suffixes the desktop menu shows. */
  nativeSuffix?: boolean;
  value: string;
}) {
  const phone = usePhoneLayout();
  const normalized = options.map((option) =>
    typeof option === "string"
      ? { value: option, label: option, entityName: entityName?.(option), suffix: undefined }
      : option,
  );
  const selected = normalized.find((option) => option.value === value);
  if (!disabled && (searchPlaceholder || isPeopleProperty(label))) return <PropertyMenu label={label} ariaLabel={label} value={selected?.label} selectedId={value} options={normalized.map(option => ({ id: option.value, label: option.label, icon: option.icon, disabled: option.disabled, i18nIgnore: option.entityName, keywords: 'keywords' in option ? option.keywords : undefined }))} onChange={onChange} triggerClassName={className || 'settings-select'} surfaceClassName={menuClassName} align={searchPlaceholder ? align : undefined} searchPlaceholder={searchPlaceholder ?? "Search people…"} trigger={searchPlaceholder
    ? <><span className="settings-select-value">{selected?.icon}<span data-i18n-ignore={selected?.entityName || undefined}>{selected?.label ?? (placeholder || value)}</span></span><span aria-hidden="true"><SettingsChevron /></span></>
    : <span className="settings-select-value">{selected?.icon}{selected?.label ?? value}</span>}/>
  return (
    <>
      <SelectPrimitive.Root
      disabled={disabled}
      onValueChange={onChange}
      value={value}
    >
      <SelectPrimitive.Trigger
        aria-label={label}
        data-select-size={normalized.reduce((widest, option) => withSuffix(option).length > widest.length ? withSuffix(option) : widest, "")}
        data-select-label-size={normalized.reduce((widest, option) => option.label.length > widest.length ? option.label : widest, "")}
        className={`${className || "settings-select"} settings-select-radix${normalized.some((option) => (option.suffix && /^[(（]/.test(option.suffix) ? option.label : withSuffix(option)).length > 12) ? " is-wide" : ""}`}
      >
        <SelectPrimitive.Value>
          <span className="settings-select-value">
            {selected?.icon}
            <span data-i18n-ignore={selected?.entityName || undefined}>
              {selected?.label ?? (placeholder || value)}
              {selected?.suffix && <>{suffixGap(selected.suffix)}<span className={suffixClass(selected.suffix)}>{selected.suffix}</span></>}
            </span>
          </span>
        </SelectPrimitive.Value>
        <SelectPrimitive.Icon>
          <SettingsChevron />
        </SelectPrimitive.Icon>
      </SelectPrimitive.Trigger>
      <SelectPrimitive.Portal>
        <SelectPrimitive.Content data-flow-motion="floating"
          align={align}
          alignOffset={alignOffset}
          className={menuClassName || "settings-select-menu"}
          collisionPadding={8}
          position="popper"
          sideOffset={4}
        >
          <SelectPrimitive.Viewport>
            {normalized.map((option) => (
              <Fragment key={option.value}>
              {"divider" in option && option.divider && <SelectPrimitive.Separator className="settings-select-separator" />}
              <SelectPrimitive.Item
                className="settings-select-option"
                disabled={option.disabled}
                value={option.value}
              >
                <SelectPrimitive.ItemText>
                  <span className="settings-select-option-content">
                    {option.icon}
                    <span data-i18n-ignore={option.entityName || undefined}>
                      {option.label}
                      {option.suffix && <>{suffixGap(option.suffix)}<span className={suffixClass(option.suffix)}>{option.suffix}</span></>}
                    </span>
                    {"hint" in option && option.hint && <span className="settings-select-option-hint" data-i18n-ignore>{option.hint}</span>}
                  </span>
                </SelectPrimitive.ItemText>
                <SelectPrimitive.ItemIndicator className="settings-select-indicator">
                  <SettingsCheck />
                </SelectPrimitive.ItemIndicator>
              </SelectPrimitive.Item>
              </Fragment>
            ))}
          </SelectPrimitive.Viewport>
        </SelectPrimitive.Content>
      </SelectPrimitive.Portal>
      </SelectPrimitive.Root>
      {phone && <span className="settings-select-native-wrap">
        <select
          aria-label={label}
          className="settings-select settings-select-native"
          disabled={disabled}
          onChange={(event) => onChange(event.target.value)}
          value={value}
        >
          {/* A value that matches no option shows the placeholder, not the first option. */}
          {!selected && <option disabled hidden value={value}>{placeholder || value}</option>}
          {normalized.map((option) => (
            <option disabled={option.disabled} key={option.value} value={option.value}>{nativeSuffix ? withSuffix(option) : option.label}</option>
          ))}
        </select>
        <span aria-hidden="true"><SettingsChevron /></span>
      </span>}
    </>
  );
}

export function SettingsChevron() {
  return (
    <svg aria-hidden="true" width="9" height="5" viewBox="0 0 9 5">
      <path d="M1.915.557a.667.667 0 0 0-.943.943l2.862 2.862a.942.942 0 0 0 1.333 0L8.028 1.5a.667.667 0 0 0-.943-.943L4.5 3.14 1.915.557Z" />
    </svg>
  );
}
function SettingsCheck() {
  return (
    <svg aria-hidden="true" width="16" height="16" viewBox="0 0 16 16">
      <path d="M4.3 7.24a.75.75 0 1 0-1.1 1.02l3.25 3.5a.75.75 0 0 0 1.13-.04l5.25-6.5a.75.75 0 0 0-1.16-.94l-4.71 5.83L4.3 7.24Z" />
    </svg>
  );
}

/** Back link pinned to the panel's top-left corner, as on team sub-pages. */
export function SettingsCrumb({
  children,
  icon,
  onClick,
  pinned = false,
}: {
  children: ReactNode;
  icon?: ReactNode;
  onClick: () => void;
  /** Stays in the panel corner while the page scrolls (the reference app's titleBackLink). */
  pinned?: boolean;
}) {
  const readOnlySlot = useContext(SettingsReadOnlyCrumbSlot);
  const crumb = (
    <button className={`settings-crumb${pinned ? " is-pinned" : ""}`} type="button" onClick={onClick}>
      <FlowGlyph name="chevronLeft" size={14} aria-hidden="true" />
      {icon && <span className="settings-crumb-icon">{icon}</span>}
      <span className="settings-crumb-label">{children}</span>
    </button>
  );
  // Read-only settings render inside a disabled <fieldset>; navigation crumbs
  // stay usable by rendering into a slot above it.
  return readOnlySlot ? createPortal(crumb, readOnlySlot) : crumb;
}

export function TeamSettingsCrumb({
  team,
  onClick,
}: {
  team: Pick<Team, "name" | "color" | "icon">;
  onClick: () => void;
}) {
  return (
    <SettingsCrumb
      icon={<ViewGlyph color={team.color} icon={team.icon || "Team"} />}
      onClick={onClick}
    >
      <span data-i18n-ignore>{team.name}</span>
    </SettingsCrumb>
  );
}
