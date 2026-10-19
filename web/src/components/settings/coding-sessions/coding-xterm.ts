/**
 * Shared xterm.js setup for the coding settings pages (debug terminal, snapshot logs). xterm and
 * its stylesheet are loaded lazily so the settings bundle does not carry them.
 */

export type XTermTheme = Record<string, string>;

/** The slice of the xterm Terminal API the coding pages use. */
export type XTerm = {
  open: (element: HTMLElement) => void;
  write: (data: string | Uint8Array) => void;
  reset: () => void;
  onData: (listener: (data: string) => void) => { dispose: () => void };
  onResize: (listener: (size: { cols: number; rows: number }) => void) => { dispose: () => void };
  loadAddon: (addon: unknown) => void;
  focus: () => void;
  dispose: () => void;
  options: { theme?: XTermTheme } & Record<string, unknown>;
  cols: number;
  rows: number;
};

export type XTermOptions = {
  cursorBlink?: boolean;
  convertEol?: boolean;
  disableStdin?: boolean;
  cursorInactiveStyle?: "outline" | "block" | "bar" | "underline" | "none";
  scrollback?: number;
  fontSize?: number;
  lineHeight?: number;
};

function cssVar(name: string) {
  if (typeof window === "undefined") return "";
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

let colorProbe: CanvasRenderingContext2D | null | undefined;
/**
 * xterm only parses hex and rgb() colours; tokens may be lch() or hsl(), which xterm would treat
 * as black. A 1px canvas resolves any CSS colour to rgba(). Without canvas (jsdom) the value is
 * returned unchanged.
 */
function xtermColor(value: string) {
  if (/^(#|rgba?\()/i.test(value)) return value;
  if (colorProbe === undefined) {
    try {
      colorProbe = document.createElement("canvas").getContext("2d", { willReadFrequently: true });
    } catch {
      colorProbe = null;
    }
  }
  if (!colorProbe) return value;
  colorProbe.clearRect(0, 0, 1, 1);
  colorProbe.fillStyle = value;
  colorProbe.fillRect(0, 0, 1, 1);
  const [r, g, b, a] = colorProbe.getImageData(0, 0, 1, 1).data;
  return a === 255 ? `rgb(${r}, ${g}, ${b})` : `rgba(${r}, ${g}, ${b}, ${(a / 255).toFixed(3)})`;
}

export function terminalFont() {
  return cssVar("--font-monospace") || "ui-monospace, Menlo, monospace";
}

/**
 * Terminal colours from the design tokens, so the terminal follows light and dark themes. ANSI
 * colours map onto the semantic tokens; tokens that are not defined keep xterm's defaults.
 */
export function xtermTheme({ background = "--theme-surface-0", readOnly = false }: { background?: string; readOnly?: boolean } = {}): XTermTheme {
  const tokens: Record<string, string> = {
    background,
    foreground: "--theme-text-primary",
    cursor: readOnly ? background : "--accent-primary",
    cursorAccent: background,
    selectionBackground: "--rt-selection-bg",
    black: "--text-label",
    red: "--color-swatch-red",
    green: "--color-swatch-green",
    yellow: "--color-swatch-yellow",
    blue: "--accent-primary",
    magenta: "--color-swatch-purple",
    cyan: "--color-swatch-teal",
    white: "--theme-text-secondary",
    brightBlack: "--text-label",
    brightRed: "--color-swatch-red",
    brightGreen: "--color-swatch-green",
    brightYellow: "--color-swatch-yellow",
    brightBlue: "--accent-primary",
    brightMagenta: "--color-swatch-purple",
    brightCyan: "--color-swatch-teal",
    brightWhite: "--theme-text-primary",
  };
  const theme: XTermTheme = {};
  for (const [key, token] of Object.entries(tokens)) {
    const value = cssVar(token);
    if (value) theme[key] = xtermColor(value);
  }
  return theme;
}

/** Imports xterm, the fit addon and xterm.css, then creates a terminal with the shared font and theme. */
export async function createXTerm(options: XTermOptions & { theme?: XTermTheme }): Promise<{ terminal: XTerm; fit: () => void }> {
  const [{ Terminal }, { FitAddon }] = await Promise.all([import("@xterm/xterm"), import("@xterm/addon-fit"), import("@xterm/xterm/css/xterm.css")]);
  const terminal = new Terminal({ fontFamily: terminalFont(), ...options, theme: options.theme ?? xtermTheme() }) as unknown as XTerm;
  const addon = new FitAddon();
  terminal.loadAddon(addon);
  return {
    terminal,
    fit: () => {
      try {
        addon.fit();
      } catch {
        // jsdom or a hidden container
      }
    },
  };
}

/** Calls fit whenever the element resizes; returns the disconnect function. */
export function observeResize(element: HTMLElement, fit: () => void): () => void {
  if (typeof ResizeObserver === "undefined") return () => {};
  const observer = new ResizeObserver(() => fit());
  observer.observe(element);
  return () => observer.disconnect();
}

/** Lines kept above the viewport. */
export const SNAPSHOT_LOG_SCROLLBACK = 10000;

// eslint-disable-next-line no-control-regex
const ANSI = /\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]/g;

/** Log text without ANSI escape sequences (copy and the plain-text fallback). */
export function stripAnsi(text: string) {
  return text.replace(ANSI, "");
}
