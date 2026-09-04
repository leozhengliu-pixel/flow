import * as SelectPrimitive from "@radix-ui/react-select";
import type {
  KeyboardEventHandler,
  MouseEventHandler,
  ReactNode,
} from "react";

import { Toggle } from "@/components/ui/toggle";

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
  headerClassName = "",
  id,
  title,
}: {
  action?: ReactNode;
  children: ReactNode;
  className?: string;
  description?: ReactNode;
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
      <div className="settings-card">{children}</div>
    </section>
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
      <div>
        <strong>{title}</strong>
        {description && <span>{description}</span>}
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
  label,
  onChange,
}: {
  checked: boolean;
  disabled?: boolean;
  label: string;
  onChange: (value: boolean) => void | Promise<void>;
}) {
  return (
    <Toggle
      checked={checked}
      disabled={disabled}
      label={label}
      onChange={onChange}
      size="regular"
    />
  );
}

export type SettingsSelectOption =
  | string
  | { value: string; label: string; disabled?: boolean; entityName?: boolean; icon?: ReactNode };

export function SettingsSelect({
  align = "end",
  className = "",
  disabled,
  entityName,
  label,
  menuClassName = "",
  onChange,
  options,
  value,
}: {
  align?: "start" | "center" | "end";
  className?: string;
  disabled?: boolean;
  entityName?: (value: string) => boolean;
  label: string;
  menuClassName?: string;
  onChange: (value: string) => void;
  options: SettingsSelectOption[];
  value: string;
}) {
  const normalized = options.map((option) =>
    typeof option === "string"
      ? { value: option, label: option, entityName: entityName?.(option) }
      : option,
  );
  const selected = normalized.find((option) => option.value === value);
  return (
    <SelectPrimitive.Root
      disabled={disabled}
      onValueChange={onChange}
      value={value}
    >
      <SelectPrimitive.Trigger
        aria-label={label}
        className={className || "settings-select"}
      >
        <SelectPrimitive.Value>
          <span className="settings-select-value">
            {selected?.icon}
            <span data-i18n-ignore={selected?.entityName || undefined}>
              {selected?.label ?? value}
            </span>
          </span>
        </SelectPrimitive.Value>
        <SelectPrimitive.Icon>
          <SettingsChevron />
        </SelectPrimitive.Icon>
      </SelectPrimitive.Trigger>
      <SelectPrimitive.Portal>
        <SelectPrimitive.Content
          align={align}
          className={menuClassName || "settings-select-menu"}
          collisionPadding={8}
          position="popper"
          sideOffset={4}
        >
          <SelectPrimitive.Viewport>
            {normalized.map((option) => (
              <SelectPrimitive.Item
                className="settings-select-option"
                disabled={option.disabled}
                key={option.value}
                value={option.value}
              >
                <SelectPrimitive.ItemText>
                  <span className="settings-select-option-content">
                    {option.icon}
                    <span data-i18n-ignore={option.entityName || undefined}>
                      {option.label}
                    </span>
                  </span>
                </SelectPrimitive.ItemText>
                <SelectPrimitive.ItemIndicator className="settings-select-indicator">
                  <SettingsCheck />
                </SelectPrimitive.ItemIndicator>
              </SelectPrimitive.Item>
            ))}
          </SelectPrimitive.Viewport>
        </SelectPrimitive.Content>
      </SelectPrimitive.Portal>
    </SelectPrimitive.Root>
  );
}

function SettingsChevron() {
  return (
    <svg aria-hidden="true" viewBox="0 0 9 5">
      <path d="M1.915.557a.667.667 0 0 0-.943.943l2.862 2.862a.942.942 0 0 0 1.333 0L8.028 1.5a.667.667 0 0 0-.943-.943L4.5 3.14 1.915.557Z" />
    </svg>
  );
}
function SettingsCheck() {
  return (
    <svg aria-hidden="true" viewBox="0 0 16 16">
      <path d="M4.3 7.24a.75.75 0 1 0-1.1 1.02l3.25 3.5a.75.75 0 0 0 1.13-.04l5.25-6.5a.75.75 0 0 0-1.16-.94l-4.71 5.83L4.3 7.24Z" />
    </svg>
  );
}
