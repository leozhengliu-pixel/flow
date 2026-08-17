export type ResolvedTheme = "light" | "dark";

export interface ThemeSettings {
  interfaceTheme?: string;
  lightTheme?: string;
  darkTheme?: string;
}

const STORAGE_KEY = "flow.theme";
export const THEME_CHANGE_EVENT = "flow-theme-change";
const media = window.matchMedia("(prefers-color-scheme: dark)");

let currentSettings: ThemeSettings = readStoredSettings();

function readStoredSettings(): ThemeSettings {
  try {
    return JSON.parse(window.localStorage.getItem(STORAGE_KEY) || "{}") as ThemeSettings;
  } catch {
    return {};
  }
}

function normalizePreference(value?: string) {
  const preference = value?.toLowerCase();
  return preference === "light" || preference === "dark" ? preference : "system";
}

function resolveTheme(settings: ThemeSettings): ResolvedTheme {
  const preference = normalizePreference(settings.interfaceTheme);
  if (preference === "light" || preference === "dark") return preference;
  return media.matches ? "dark" : "light";
}

function resolveVariant(settings: ThemeSettings, theme: ResolvedTheme) {
  const selected = theme === "light" ? settings.lightTheme : settings.darkTheme;
  return (selected || theme).toLowerCase().replaceAll(" ", "-");
}

function syncRoot(settings: ThemeSettings) {
  const theme = resolveTheme(settings);
  const root = document.documentElement;
  root.dataset.theme = theme;
  root.dataset.themePreference = normalizePreference(settings.interfaceTheme);
  root.dataset.themeVariant = resolveVariant(settings, theme);
  root.style.colorScheme = theme;
  window.dispatchEvent(new CustomEvent(THEME_CHANGE_EVENT, { detail: theme }));
}

export function initializeTheme() {
  syncRoot(currentSettings);
  media.addEventListener("change", () => {
    if (normalizePreference(currentSettings.interfaceTheme) === "system") syncRoot(currentSettings);
  });
}

export function applyTheme(settings: ThemeSettings) {
  currentSettings = {
    interfaceTheme: settings.interfaceTheme || "System preference",
    lightTheme: settings.lightTheme || "Light",
    darkTheme: settings.darkTheme || "Dark",
  };
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(currentSettings));
  } catch {
    // The DOM theme still applies when browser storage is unavailable.
  }
  syncRoot(currentSettings);
}

export function getResolvedTheme(): ResolvedTheme {
  return document.documentElement.dataset.theme === "light" ? "light" : "dark";
}
