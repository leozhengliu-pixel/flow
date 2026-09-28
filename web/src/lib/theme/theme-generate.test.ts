import { describe, expect, it } from "vitest";
import { colorConverter } from "./color-converter";
import {
  darkHighContrastTheme,
  darkThemeRefresh,
  generateTheme,
  lightHighContrastTheme,
  lightThemeRefresh,
  DARK_THEME_BASE,
  DARK_THEME_ACCENT,
  LIGHT_THEME_BASE,
  LIGHT_THEME_ACCENT,
} from "./theme-generate";
import { applyGeneratedTheme, clearGeneratedTheme, THEME_CSS_VAR_MAP } from "./theme-apply";
import { resolveThemeVariant, themeFromSettings } from "./theme-variants";

describe("colorConverter (LS-0692)", () => {
  it("formats LCH and reports APCA contrast", () => {
    expect(colorConverter.toCss("LCH", [5.52, 0.4, 272])).toBe("lch(5.52% 0.4 272)");
    const contrast = colorConverter.apcaContrast([5.52, 0.4, 272], [91, 1.4, 272]);
    expect(contrast).toBeGreaterThan(38);
    expect(colorConverter.sufficientContrastForText([5.52, 0.4, 272], [91, 1.4, 272])).toBe(true);
  });

  it("adjusts and mixes LCH colors", () => {
    const adjusted = colorConverter.adjust([5.52, 0.4, 272], { l: 4.25, c: 0.5 });
    expect(adjusted[0]).toBeCloseTo(9.77, 1);
    const mixed = colorConverter.mix([5.52, 0.4, 272], [47.92, 59.3, 288.42], 0.05);
    expect(mixed[0]).toBeGreaterThan(5.52);
  });
});

describe("generateTheme / darkThemeRefresh (LS-0692)", () => {
  it("emits dark seed palette aligned with Flow tokens", () => {
    const theme = darkThemeRefresh("LCH");
    expect(theme.color.bgBase).toMatch(/lch\(5\.52%/);
    expect(theme.color.controlPrimary).toMatch(/lch\(47\.918%/);
    expect(theme.contrast).toBe(27);
    expect(theme.isDark).toBe(true);
    expect(theme.hash).toBeTruthy();
    expect(theme.color.labelMuted).toMatch(/^lch\(/);
    expect(theme.color.bgBorder).toMatch(/^lch\(/);
  });

  it("raises contrast for dark high-contrast variant", () => {
    const theme = darkHighContrastTheme("LCH");
    expect(theme.color.bgBase).toMatch(/lch\(8%/);
    expect(theme.contrast).toBe(90);
    const base = darkThemeRefresh("LCH");
    // High contrast muted text should be lighter (more readable on dark)
    const mutedHC = colorConverter.fromCss(theme.color.labelMuted);
    const mutedBase = colorConverter.fromCss(base.color.labelMuted);
    expect(mutedHC[0]).toBeGreaterThan(mutedBase[0]);
  });

  it("accepts custom accent", () => {
    const theme = generateTheme({
      base: DARK_THEME_BASE,
      accent: [55, 40, 30],
      contrast: 27,
      colorFormat: "LCH",
    });
    expect(theme.color.controlPrimary).toMatch(/lch\(55%/);
  });
});

describe("lightThemeRefresh (LS-0708)", () => {
  it("emits light seed palette", () => {
    const theme = lightThemeRefresh("LCH");
    expect(theme.color.bgBase).toMatch(/lch\(97\.94%/);
    expect(theme.color.controlPrimary).toMatch(/lch\(53%/);
    expect(theme.contrast).toBe(30);
    expect(theme.isDark).toBe(false);
  });

  it("emits light high-contrast with stronger label contrast", () => {
    const theme = lightHighContrastTheme("LCH");
    expect(theme.contrast).toBe(90);
    const base = lightThemeRefresh("LCH");
    const mutedHC = colorConverter.fromCss(theme.color.labelMuted);
    const mutedBase = colorConverter.fromCss(base.color.labelMuted);
    // On light bg, high-contrast muted is darker (lower L)
    expect(mutedHC[0]).toBeLessThan(mutedBase[0]);
  });
});

describe("theme variants + CSS bridge", () => {
  it("resolves high-contrast variant slugs", () => {
    expect(resolveThemeVariant("light", "Light high contrast").variant).toBe("light-high-contrast");
    expect(resolveThemeVariant("dark", undefined, "Dark high contrast").variant).toBe("dark-high-contrast");
    expect(resolveThemeVariant("dark", "Dark").variant).toBe("dark");
  });

  it("themeFromSettings picks the right factory", () => {
    expect(themeFromSettings("dark").color.bgBase).toMatch(/lch\(5\.52%/);
    expect(themeFromSettings("dark", undefined, "Dark high contrast").color.bgBase).toMatch(/lch\(8%/);
    expect(themeFromSettings("light").color.bgBase).toMatch(/lch\(97\.94%/);
    expect(themeFromSettings("light", "Light high contrast").contrast).toBe(90);
  });

  it("applyGeneratedTheme writes Flow CSS vars", () => {
    const root = document.documentElement;
    clearGeneratedTheme(root);
    const theme = darkThemeRefresh("LCH");
    const applied = applyGeneratedTheme(theme, root);
    expect(applied).toContain("--bg-panel");
    expect(root.style.getPropertyValue("--bg-panel")).toMatch(/lch\(5\.52%/);
    expect(root.style.getPropertyValue("--accent-primary")).toMatch(/lch\(47/);
    expect(root.dataset.themeGenerated).toBeTruthy();
    clearGeneratedTheme(root);
    expect(root.style.getPropertyValue("--bg-panel")).toBe("");
  });

  it("maps core palette keys", () => {
    expect(THEME_CSS_VAR_MAP.bgBase).toBe("--bg-panel");
    expect(THEME_CSS_VAR_MAP.controlPrimary).toBe("--accent-primary");
    expect(LIGHT_THEME_BASE[0]).toBeCloseTo(97.94);
    expect(LIGHT_THEME_ACCENT[2]).toBeCloseTo(286.91);
    expect(DARK_THEME_ACCENT[0]).toBeCloseTo(47.92, 0);
  });
});
