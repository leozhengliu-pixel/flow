// Shared types for the browser-testing tools (track I).
//
// The MCP-facing types (RegisterTool, ToolSchema, ToolResult) mirror the contract of track C's
// `src/mcp/server.ts`: `registerTool(name, schema, handler)` where the handler returns an MCP
// CallToolResult. They are declared here so this module has no compile-time dependency on C's code.
//
// The driver types (BrowserLauncher, BrowserLike, ContextLike, PageLike, ...) are a deliberately
// minimal structural subset of Playwright's API. Playwright is loaded at runtime with a dynamic
// import (see playwright.ts) and never imported statically, so tests can inject fakes and the runner
// bundle does not need Playwright as a dependency.

/** A JSON Schema object describing a tool's input (always `type: "object"`). */
export interface JSONSchemaObject {
  type: 'object';
  properties?: Record<string, unknown>;
  required?: string[];
  additionalProperties?: boolean;
  [key: string]: unknown;
}

export interface ToolSchema {
  description: string;
  inputSchema: JSONSchemaObject;
}

export type ToolContent =
  | { type: 'text'; text: string }
  | { type: 'image'; data: string; mimeType: string };

/** MCP CallToolResult shape. */
export interface ToolResult {
  content: ToolContent[];
  isError?: boolean;
}

export type ToolHandler = (args: Record<string, unknown>) => Promise<ToolResult>;

export type RegisterTool = (name: string, schema: ToolSchema, handler: ToolHandler) => void;

// ---------------------------------------------------------------------------------------------
// Minimal Playwright-shaped driver interfaces.
// ---------------------------------------------------------------------------------------------

export interface Viewport {
  width: number;
  height: number;
}

export interface LaunchOptions {
  headless: boolean;
  chromiumSandbox: boolean;
  args: string[];
  timeout: number;
  proxy?: { server: string; bypass?: string; username?: string; password?: string };
}

export interface ContextOptions {
  viewport: Viewport;
  ignoreHTTPSErrors: boolean;
  recordVideo?: { dir: string; size: Viewport };
}

export interface BrowserLauncher {
  launch(options: LaunchOptions): Promise<BrowserLike>;
}

export interface BrowserLike {
  newContext(options: ContextOptions): Promise<ContextLike>;
  close(): Promise<void>;
  isConnected(): boolean;
}

export interface ContextLike {
  newPage(): Promise<PageLike>;
  close(): Promise<void>;
}

export interface ResponseLike {
  status(): number;
}

export interface VideoLike {
  path(): Promise<string>;
}

export interface ScreenshotOptions {
  type: 'png' | 'jpeg';
  quality?: number;
  fullPage?: boolean;
  timeout: number;
}

export interface LocatorLike {
  first(): LocatorLike;
  click(options: { timeout: number }): Promise<void>;
  fill(value: string, options: { timeout: number }): Promise<void>;
  pressSequentially(text: string, options: { timeout: number }): Promise<void>;
  press(key: string, options: { timeout: number }): Promise<void>;
  waitFor(options: { state: 'visible' | 'attached'; timeout: number }): Promise<void>;
  innerText(options: { timeout: number }): Promise<string>;
  screenshot(options: Omit<ScreenshotOptions, 'fullPage'>): Promise<Uint8Array>;
}

export interface PageLike {
  goto(url: string, options: { waitUntil: 'load'; timeout: number }): Promise<ResponseLike | null>;
  url(): string;
  title(): Promise<string>;
  locator(selector: string): LocatorLike;
  getByText(text: string, options?: { exact?: boolean }): LocatorLike;
  getByRole(role: string, options?: { name?: string }): LocatorLike;
  getByLabel(label: string, options?: { exact?: boolean }): LocatorLike;
  keyboard: { press(key: string): Promise<void> };
  screenshot(options: ScreenshotOptions): Promise<Uint8Array>;
  waitForURL(predicate: (url: URL) => boolean, options: { timeout: number }): Promise<void>;
  setViewportSize(viewport: Viewport): Promise<void>;
  video(): VideoLike | null;
  on(event: string, listener: (arg: unknown) => void): unknown;
  close(): Promise<void>;
}

/** Options accepted by registerBrowserTools. Everything except `enabled` exists for tests. */
export interface BrowserToolOptions {
  /** Bootstrap `browser.enabled`; false (FLOW_CODING_BROWSER=off) registers no tools. */
  enabled?: boolean;
  /** Environment to read configuration from (defaults to process.env). */
  env?: NodeJS.ProcessEnv;
  /** Override how Playwright's chromium launcher is obtained. */
  loadLauncher?: () => Promise<BrowserLauncher>;
  /** fetch implementation used for uploads (defaults to global fetch). */
  fetch?: typeof fetch;
  /** Directory screenshots and videos are written to (default /tmp/flow-browser). */
  outputDir?: string;
  /** Repository checkout (default /workspace/repo). */
  repoDir?: string;
  /** Roots attach_screenshot may read from (default /workspace, /tmp, $HOME). */
  allowedRoots?: string[];
  /** Port tried first for browser_start_app (default 3000). */
  defaultAppPort?: number;
  /** Sleep used between upload retries (tests make this instant). */
  sleep?: (ms: number) => Promise<void>;
}
