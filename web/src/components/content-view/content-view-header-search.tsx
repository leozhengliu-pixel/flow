/**
 * LS-0142 ContentViewHeaderInlineSearch + LS-0143 ContentViewHeaderSearch.
 * Shared header find primitive: conditional inline vs always-visible directory search.
 */
import { Search, X } from "lucide-react";
import { useEffect, useId, useRef, type CSSProperties } from "react";

import "./content-view-header-search.css";

export type ContentViewHeaderSearchProps = {
  value: string;
  onChange: (value: string) => void;
  placeholder: string;
  /** When false/undefined and value empty, InlineSearch may hide (unless alwaysVisible). */
  alwaysVisible?: boolean;
  /** Alias used by Linear InlineSearch consumers. */
  alwaysShowOnDesktop?: boolean;
  /** When absent, InlineSearch returns null (Linear parity). */
  inlineFilter?: { lastSearchTerm?: string } | null;
  maxWidth?: number;
  disableKeyboardShortcut?: boolean;
  className?: string;
  "aria-label"?: string;
};

export function ContentViewHeaderSearch({
  value,
  onChange,
  placeholder,
  alwaysVisible = true,
  alwaysShowOnDesktop,
  inlineFilter,
  maxWidth = 160,
  disableKeyboardShortcut = false,
  className,
  "aria-label": ariaLabel,
}: ContentViewHeaderSearchProps) {
  const inputRef = useRef<HTMLInputElement>(null);
  const reactId = useId();
  const visible =
    alwaysVisible ||
    alwaysShowOnDesktop ||
    Boolean(value) ||
    Boolean(inlineFilter?.lastSearchTerm);

  useEffect(() => {
    if (disableKeyboardShortcut || alwaysVisible) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "f" && event.key !== "F") return;
      if (!(event.metaKey || event.ctrlKey) || event.shiftKey || event.altKey) return;
      const target = event.target as HTMLElement | null;
      if (
        target?.closest(
          'input, textarea, select, [contenteditable="true"], [role="textbox"], [role="searchbox"]',
        )
      )
        return;
      event.preventDefault();
      inputRef.current?.focus();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [alwaysVisible, disableKeyboardShortcut]);

  if (!alwaysVisible && inlineFilter == null && !value) return null;
  if (!visible && !alwaysVisible) return null;

  const style = {
    "--cvh-search-max-width": `${maxWidth}px`,
  } as CSSProperties;

  return (
    <label
      className={`cvh-search${alwaysVisible ? " is-always-visible" : " is-inline"}${className ? ` ${className}` : ""}`}
      data-has-value={value ? "" : undefined}
      style={style}
    >
      <Search aria-hidden="true" />
      <input
        ref={inputRef}
        id={reactId}
        role="searchbox"
        type="search"
        aria-label={ariaLabel ?? placeholder}
        placeholder={placeholder}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Escape" && value) {
            event.preventDefault();
            onChange("");
          }
        }}
      />
      {value ? (
        <button
          type="button"
          aria-label="Clear search"
          onClick={() => onChange("")}
        >
          <X aria-hidden="true" />
        </button>
      ) : null}
    </label>
  );
}

/** Conditional inline find (LS-0142). Requires inlineFilter; returns null when missing. */
export function ContentViewHeaderInlineSearch(
  props: Omit<ContentViewHeaderSearchProps, "alwaysVisible"> & {
    inlineFilter?: ContentViewHeaderSearchProps["inlineFilter"];
  },
) {
  if (props.inlineFilter == null) return null;
  return (
    <ContentViewHeaderSearch
      {...props}
      alwaysVisible={false}
      maxWidth={props.maxWidth ?? 160}
    />
  );
}
