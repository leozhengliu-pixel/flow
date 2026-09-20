/**
 * Interface theme resolution.
 *
 * Precedence (highest → lowest):
 * 1. Authoritative account settings — REST `UserSettings.interfaceTheme`
 *    from workspace bootstrap or `GET|PATCH /api/account/settings`.
 *    Explicit "Light" / "Dark" always wins and is write-through to the cache.
 * 2. localStorage `flow.theme` — first-paint cache used by `index.html` and
 *    `initializeTheme()` before account settings arrive. Also retained when
 *    account settings only carry the default "System preference", so a
 *    Light/Dark choice (Preferences or cache) is not clobbered on bootstrap.
 * 3. System color scheme — when both account and cache say system.
 *
 * Preferences UI calls {@link applyTheme} (authoritative write-through).
 * Bootstrap / OAuth call {@link applyAccountTheme} which reconciles (2) vs (1).
 */
export type ResolvedTheme = "light" | "dark";

export interface ThemeSettings {
  interfaceTheme?: string;
  lightTheme?: string;
  darkTheme?: string;
}

const STORAGE_KEY = "flow.theme";
export const THEME_CHANGE_EVENT = "flow-theme-change";

const CANONICAL = {
  light: "Light",
  dark: "Dark",
  system: "System preference",
} as const;

const media = window.matchMedia("(prefers-color-scheme: dark)");

let currentSettings: ThemeSettings = readStoredSettings();

function readStoredSettings(): ThemeSettings {
  try {
    return JSON.parse(window.localStorage.getItem(STORAGE_KEY) || "{}") as ThemeSettings;
  } catch {
    return {};
  }
}

/** Exported for bootstrap reconcile tests and callers that need the cache. */
export function readThemeSettings(): ThemeSettings {
  return { ...currentSettings };
}

function normalizePreference(value?: string): "light" | "dark" | "system" {
  const preference = value?.trim().toLowerCase();
  if (preference === "light" || preference === "dark") return preference;
  return "system";
}

function canonicalizeInterfaceTheme(value?: string): string {
  const preference = normalizePreference(value);
  if (preference === "light") return CANONICAL.light;
  if (preference === "dark") return CANONICAL.dark;
  // Preserve an explicit "System preference" label when present; otherwise default.
  if (value?.trim()) return value.trim();
  return CANONICAL.system;
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

function persist(settings: ThemeSettings) {
  currentSettings = settings;
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(currentSettings));
  } catch {
    // The DOM theme still applies when browser storage is unavailable.
  }
  syncRoot(currentSettings);
}

function normalizeThemeSettings(settings: ThemeSettings): ThemeSettings {
  return {
    interfaceTheme: canonicalizeInterfaceTheme(settings.interfaceTheme),
    lightTheme: settings.lightTheme?.trim() || "Light",
    darkTheme: settings.darkTheme?.trim() || "Dark",
  };
}

/**
 * Reconcile account settings with the first-paint cache.
 * Explicit account Light/Dark wins; System/missing yields to an explicit cache.
 */
export function reconcileThemeSettings(account: ThemeSettings, cached: ThemeSettings = currentSettings): ThemeSettings {
  const accountPreference = normalizePreference(account.interfaceTheme);
  const cachedPreference = normalizePreference(cached.interfaceTheme);
  if (accountPreference === "light" || accountPreference === "dark") {
    return normalizeThemeSettings({
      interfaceTheme: account.interfaceTheme,
      lightTheme: account.lightTheme ?? cached.lightTheme,
      darkTheme: account.darkTheme ?? cached.darkTheme,
    });
  }
  if (cachedPreference === "light" || cachedPreference === "dark") {
    return normalizeThemeSettings({
      interfaceTheme: cached.interfaceTheme,
      lightTheme: account.lightTheme ?? cached.lightTheme,
      darkTheme: account.darkTheme ?? cached.darkTheme,
    });
  }
  return normalizeThemeSettings({
    interfaceTheme: account.interfaceTheme || cached.interfaceTheme || CANONICAL.system,
    lightTheme: account.lightTheme ?? cached.lightTheme,
    darkTheme: account.darkTheme ?? cached.darkTheme,
  });
}

export function initializeTheme() {
  currentSettings = readStoredSettings();
  syncRoot(normalizeThemeSettings(currentSettings));
  media.addEventListener("change", () => {
    if (normalizePreference(currentSettings.interfaceTheme) === "system") syncRoot(currentSettings);
  });
}

/** Authoritative apply (Preferences). Always write-through to the cache. */
export function applyTheme(settings: ThemeSettings) {
  persist(normalizeThemeSettings({
    interfaceTheme: settings.interfaceTheme || currentSettings.interfaceTheme || CANONICAL.system,
    lightTheme: settings.lightTheme || currentSettings.lightTheme || "Light",
    darkTheme: settings.darkTheme || currentSettings.darkTheme || "Dark",
  }));
}

/**
 * Apply theme after account settings load (bootstrap / OAuth).
 * Preserves an explicit Light/Dark cache when the account still has System.
 * Returns the settings that were applied (callers may PATCH when cache won).
 */
export function applyAccountTheme(account: ThemeSettings): ThemeSettings {
  const next = reconcileThemeSettings(account, currentSettings);
  persist(next);
  return next;
}

export function getResolvedTheme(): ResolvedTheme {
  return document.documentElement.dataset.theme === "light" ? "light" : "dark";
}

export function themeNeedsAccountSync(account: ThemeSettings, applied: ThemeSettings): boolean {
  return normalizePreference(account.interfaceTheme) !== normalizePreference(applied.interfaceTheme);
}
