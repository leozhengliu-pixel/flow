/**
 * LS-0008 AccountShortcutsSettingsPage — Account settings shortcuts page with rebinding.
 */
import { useEffect, useMemo, useRef, useState } from "react";
import { Keyboard, RotateCcw, Search, X } from "lucide-react";

import {
  DEFAULT_SHORTCUT_ACTIONS,
  findShortcutConflict,
  formatShortcutKeys,
  keysEqual,
  resetCustomShortcut,
  resolveShortcutKeys,
  setCustomShortcut,
  useCustomShortcuts,
  type ShortcutAction,
} from "@/lib/custom-shortcuts";
import {
  SettingsPageTitle,
  SettingsSection,
} from "./settings-primitives";

import "./account-shortcuts-settings.css";

type Translate = (source: string) => string;

export function AccountShortcutsSettingsPage({ p }: { p: Translate }) {
  const customs = useCustomShortcuts();
  const [query, setQuery] = useState("");
  const [scope, setScope] = useState<"all" | "custom">("all");
  const [recordingId, setRecordingId] = useState<string | null>(null);
  const [draftKeys, setDraftKeys] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);
  const trapRef = useRef<HTMLDivElement>(null);

  const actions = DEFAULT_SHORTCUT_ACTIONS;

  const filtered = useMemo(() => {
    const needle = query.trim().toLocaleLowerCase();
    return actions.filter((action) => {
      const keys = resolveShortcutKeys(action, customs);
      const isCustom = !keysEqual(keys, action.defaultKeys);
      if (scope === "custom" && !isCustom) return false;
      if (!needle) return true;
      const haystack = [
        action.label,
        action.group,
        action.keywords ?? "",
        formatShortcutKeys(keys),
        keys.join(" "),
      ]
        .join(" ")
        .toLocaleLowerCase();
      return haystack.includes(needle);
    });
  }, [actions, customs, query, scope]);

  const grouped = useMemo(() => {
    const map = new Map<string, ShortcutAction[]>();
    for (const action of filtered) {
      const list = map.get(action.group) ?? [];
      list.push(action);
      map.set(action.group, list);
    }
    return [...map.entries()];
  }, [filtered]);

  useEffect(() => {
    if (!recordingId) return;
    const onKeyDown = (event: KeyboardEvent) => {
      event.preventDefault();
      event.stopPropagation();
      if (event.key === "Escape") {
        setRecordingId(null);
        setDraftKeys([]);
        setError(null);
        return;
      }
      if (event.key === "Enter" && draftKeys.length) {
        commitRecording(recordingId, draftKeys);
        return;
      }
      const parts = chordFromEvent(event);
      if (!parts.length) return;
      setDraftKeys(parts);
      const conflict = findShortcutConflict(
        recordingId,
        parts,
        actions,
        customs,
      );
      if (conflict) {
        setError(`${p("Shortcut already in use by")} ${conflict.label}`);
      } else {
        setError(null);
      }
    };
    const node = trapRef.current;
    node?.focus();
    window.addEventListener("keydown", onKeyDown, true);
    return () => window.removeEventListener("keydown", onKeyDown, true);
  }, [actions, customs, draftKeys, p, recordingId]);

  const commitRecording = (actionId: string, keys: string[]) => {
    const conflict = findShortcutConflict(actionId, keys, actions, customs);
    if (conflict) {
      setError(`${p("Shortcut already in use by")} ${conflict.label}`);
      return;
    }
    const action = actions.find((item) => item.id === actionId);
    if (!action) return;
    if (keysEqual(keys, action.defaultKeys)) resetCustomShortcut(actionId);
    else setCustomShortcut(actionId, keys);
    setRecordingId(null);
    setDraftKeys([]);
    setError(null);
  };

  return (
    <div className="account-shortcuts-settings">
      <SettingsPageTitle
        className="personal-page-header"
        description={p(
          "Customize keyboard shortcuts used across Flow. Conflicts are blocked.",
        )}
      >
        {p("Shortcuts")}
      </SettingsPageTitle>

      <div className="account-shortcuts-toolbar">
        <label className="account-shortcuts-filter">
          <Search aria-hidden="true" />
          <input
            role="searchbox"
            aria-label={p("Filter by name or keystroke")}
            placeholder={p("Filter by name or keystroke")}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
          {query ? (
            <button
              type="button"
              aria-label={p("Clear search")}
              onClick={() => setQuery("")}
            >
              <X />
            </button>
          ) : null}
        </label>
        <div className="account-shortcuts-scope" role="tablist" aria-label={p("Shortcut filter")}>
          <button
            type="button"
            role="tab"
            aria-selected={scope === "all"}
            data-active={scope === "all" || undefined}
            onClick={() => setScope("all")}
          >
            {p("All")}
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={scope === "custom"}
            data-active={scope === "custom" || undefined}
            onClick={() => setScope("custom")}
          >
            {p("Custom")}
          </button>
        </div>
      </div>

      {error ? (
        <div className="account-shortcuts-error" role="alert">
          {error}
        </div>
      ) : null}

      {recordingId ? (
        <div
          className="account-shortcuts-recording"
          ref={trapRef}
          tabIndex={-1}
          role="status"
        >
          <Keyboard size={14} />
          <span>
            {p("Recording…")}{" "}
            {draftKeys.length
              ? formatShortcutKeys(draftKeys)
              : p("Press keys, then Enter to save. Esc to cancel.")}
          </span>
        </div>
      ) : null}

      {!grouped.length ? (
        <div className="account-shortcuts-empty" role="status">
          <h3>{p("No shortcuts match")}</h3>
          <p>{p("Try another name or keystroke.")}</p>
        </div>
      ) : (
        grouped.map(([group, items]) => (
          <SettingsSection
            key={group}
            className="personal-section account-shortcuts-section"
            title={p(group)}
          >
            <ul className="account-shortcuts-list">
              {items.map((action) => {
                const keys = resolveShortcutKeys(action, customs);
                const isCustom = !keysEqual(keys, action.defaultKeys);
                const isRecording = recordingId === action.id;
                return (
                  <li key={action.id} className="account-shortcuts-row">
                    <div className="account-shortcuts-row__copy">
                      <strong>{p(action.label)}</strong>
                      {isCustom ? (
                        <span className="account-shortcuts-badge">{p("Custom")}</span>
                      ) : null}
                    </div>
                    <div className="account-shortcuts-row__keys">
                      {isRecording ? (
                        <kbd className="is-recording">{p("Recording…")}</kbd>
                      ) : (
                        keys.map((part, index) =>
                          part === "then" ? (
                            <small key={`then-${index}`}>{p("then")}</small>
                          ) : (
                            <kbd key={`${part}-${index}`}>{part}</kbd>
                          ),
                        )
                      )}
                    </div>
                    <div className="account-shortcuts-row__actions">
                      <button
                        type="button"
                        onClick={() => {
                          setRecordingId(action.id);
                          setDraftKeys([]);
                          setError(null);
                        }}
                      >
                        {p("Record shortcut")}
                      </button>
                      {isCustom ? (
                        <button
                          type="button"
                          aria-label={p("Restore default")}
                          onClick={() => resetCustomShortcut(action.id)}
                        >
                          <RotateCcw size={14} />
                          <span>{p("Restore default")}</span>
                        </button>
                      ) : null}
                    </div>
                  </li>
                );
              })}
            </ul>
          </SettingsSection>
        ))
      )}
    </div>
  );
}

function chordFromEvent(event: KeyboardEvent): string[] {
  if (["Shift", "Control", "Alt", "Meta"].includes(event.key)) return [];
  const parts: string[] = [];
  if (event.metaKey) parts.push("⌘");
  if (event.ctrlKey) parts.push("Ctrl");
  if (event.altKey) parts.push("⌥");
  if (event.shiftKey) parts.push("⇧");
  const key = event.key.length === 1 ? event.key.toUpperCase() : event.key;
  if (key === " ") parts.push("Space");
  else if (key !== "Meta" && key !== "Control" && key !== "Alt" && key !== "Shift")
    parts.push(key === "Escape" ? "Esc" : key);
  return parts;
}
