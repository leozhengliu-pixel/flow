import { createContext } from "react";

/**
 * Slot element above a read-only settings page (see ReadOnlySettings in
 * settings-page.tsx). Read-only pages render inside a disabled <fieldset>, so
 * navigation crumbs portal into this slot to stay usable.
 */
export const SettingsReadOnlyCrumbSlot = createContext<HTMLElement | null>(null);
