import {
  darkHighContrastTheme,
  darkThemeRefresh,
  lightHighContrastTheme,
  lightThemeRefresh,
} from "./theme-generate";
import type { GeneratedTheme } from "./theme-apply";

export const LIGHT_THEME_OPTIONS = ["Light", "Light high contrast"] as const;
export const DARK_THEME_OPTIONS = ["Dark", "Dark high contrast"] as const;

export type LightThemeOption = (typeof LIGHT_THEME_OPTIONS)[number];
export type DarkThemeOption = (typeof DARK_THEME_OPTIONS)[number];

function normalizeVariantLabel(value: string | undefined, fallback: string): string {
  const raw = (value || fallback).trim().toLowerCase().replaceAll("_", " ").replaceAll("-", " ");
  return raw;
}

/**
 * Resolve dataset.themeVariant + which generator to run.
 * Accepts stored preference labels ("Light high contrast") or slug forms.
 */
export function resolveThemeVariant(
  mode: "light" | "dark",
  lightTheme?: string,
  darkTheme?: string,
): { variant: string; highContrast: boolean } {
  if (mode === "light") {
    const n = normalizeVariantLabel(lightTheme, "Light");
    const highContrast = n.includes("high contrast") || n === "light high contrast";
    return {
      variant: highContrast ? "light-high-contrast" : "light",
      highContrast,
    };
  }
  const n = normalizeVariantLabel(darkTheme, "Dark");
  const highContrast = n.includes("high contrast") || n === "dark high contrast";
  return {
    variant: highContrast ? "dark-high-contrast" : "dark",
    highContrast,
  };
}

/** Build a generated theme for the active mode + lightTheme/darkTheme prefs. */
export function themeFromSettings(
  mode: "light" | "dark",
  lightTheme?: string,
  darkTheme?: string,
): GeneratedTheme {
  const { highContrast } = resolveThemeVariant(mode, lightTheme, darkTheme);
  if (mode === "light") {
    return (highContrast ? lightHighContrastTheme() : lightThemeRefresh()) as GeneratedTheme;
  }
  return (highContrast ? darkHighContrastTheme() : darkThemeRefresh()) as GeneratedTheme;
}
