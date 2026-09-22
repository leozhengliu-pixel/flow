/**
 * Bridge generated Linear-style palette tokens → Flow CSS custom properties.
 * Keeps Flow semantic names (--bg-*, --theme-*, --accent-*) rather than --sx-*.
 */

export type GeneratedTheme = {
  color: Record<string, string>;
  shadowLow?: string;
  shadowMedium?: string;
  shadowHigh?: string;
  shadowInset?: string;
  shadowBorder?: string;
  focusShadow?: string;
  contrast: number;
  isDark?: boolean;
  hash?: string;
};

/** Palette key → Flow CSS variable (subset used by tokens.css consumers). */
export const THEME_CSS_VAR_MAP: Record<string, string> = {
  bgBase: "--bg-panel",
  bgSub: "--bg-sidebar",
  bgShade: "--bg-subtle",
  bgBaseHover: "--bg-hover",
  bgShadeHover: "--bg-active",
  bgSelected: "--theme-surface-active",
  bgSelectedHover: "--theme-surface-hover",
  bgFocus: "--theme-surface-1",
  bgModalOverlay: "--theme-overlay",
  bgBorder: "--border",
  bgBorderStrong: "--border-strong",
  bgBorderSolid: "--theme-border",
  bgBorderSolidHover: "--theme-border-strong",
  labelTitle: "--bright",
  labelBase: "--text",
  labelMuted: "--muted",
  labelFaint: "--faint",
  labelLink: "--theme-text-secondary",
  controlPrimary: "--accent-primary",
  controlPrimaryHover: "--accent-primary-hover",
  controlPrimaryLabel: "--on-accent",
  controlSecondary: "--theme-control",
  controlSecondaryHover: "--theme-control-hover",
  controlTertiary: "--theme-control-active",
  focusColor: "--focus-ring",
  shadowColor: "--theme-shadow-color",
  sidebarLinkBg: "--theme-surface-1",
  sidebarLinkBgActive: "--theme-surface-2",
  scrollbarBg: "--scrollbar-thumb",
};

const SURFACE_MAP: Record<string, string> = {
  bgBase: "--theme-surface-0",
  bgShade: "--theme-surface-1",
  bgBaseHover: "--theme-surface-2",
};

const TEXT_MAP: Record<string, string> = {
  labelTitle: "--theme-text-primary",
  labelBase: "--issue-fg",
  labelMuted: "--theme-text-secondary",
  labelFaint: "--theme-text-tertiary",
};

const APPLIED_VARS_ATTR = "data-flow-theme-generated";

/**
 * Apply a generated theme palette onto an element (usually documentElement).
 * Returns the list of CSS variables that were set.
 */
export function applyGeneratedTheme(
  theme: GeneratedTheme,
  root: HTMLElement = document.documentElement,
): string[] {
  const { color } = theme;
  const applied: string[] = [];

  const set = (cssVar: string, value: string | undefined) => {
    if (!value) return;
    root.style.setProperty(cssVar, value);
    applied.push(cssVar);
  };

  for (const [key, cssVar] of Object.entries(THEME_CSS_VAR_MAP)) {
    set(cssVar, color[key]);
  }
  for (const [key, cssVar] of Object.entries(SURFACE_MAP)) {
    set(cssVar, color[key]);
  }
  for (const [key, cssVar] of Object.entries(TEXT_MAP)) {
    set(cssVar, color[key]);
  }

  // Soft accent fill used across the app
  if (color.controlPrimary) {
    set(
      "--accent-soft",
      `color-mix(in srgb, ${color.controlPrimary} 16%, transparent)`,
    );
  }

  root.dataset.themeGenerated = theme.hash || "1";
  root.setAttribute(APPLIED_VARS_ATTR, applied.join(" "));
  return applied;
}

/** Clear previously injected generated theme properties. */
export function clearGeneratedTheme(root: HTMLElement = document.documentElement): void {
  const listed = root.getAttribute(APPLIED_VARS_ATTR);
  if (listed) {
    for (const cssVar of listed.split(/\s+/).filter(Boolean)) {
      root.style.removeProperty(cssVar);
    }
  }
  // Also clear known maps in case attr was lost
  const known = new Set([
    ...Object.values(THEME_CSS_VAR_MAP),
    ...Object.values(SURFACE_MAP),
    ...Object.values(TEXT_MAP),
    "--accent-soft",
  ]);
  for (const cssVar of known) {
    root.style.removeProperty(cssVar);
  }
  delete root.dataset.themeGenerated;
  root.removeAttribute(APPLIED_VARS_ATTR);
}
