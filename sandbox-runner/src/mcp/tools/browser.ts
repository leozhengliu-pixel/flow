// Browser testing and artifact tools (track I) for the runner-local MCP server.
//
// Wiring: track C's `src/mcp/server.ts` calls
//   registerBrowserTools(registerTool, { enabled: bootstrap.browser.enabled })
// The `flow` server is a stdio MCP server registered in the Claude Code, Codex and OpenCode configs,
// so these tools reach every harness; in Claude Code they appear as `mcp__flow__browser_open`,
// `mcp__flow__attach_screenshot`, etc. When `enabled` is false (FLOW_CODING_BROWSER=off) or the env
// has FLOW_BROWSER_ENABLED=0, nothing is registered.
//
// Environment of the MCP server process (set by C in the MCP config `env`):
//   FLOW_RUNTIME_URL         Flow runtime API base URL (uploads go to /internal/coding-runtime/artifacts)
//   FLOW_TOKEN_FILE          file holding the sandbox token (sent as `Authorization: Bearer fcs_…`; never logged)
//   FLOW_TURN_ID             current turn id (fallback: contents of $HOME/.flow/current-turn)
//   FLOW_BROWSER_ENABLED     `0` disables the tools
//   PLAYWRIGHT_BROWSERS_PATH /opt/ms-playwright (browsers installed by deploy/sandbox-image/install-browser.sh)
//   FLOW_PLAYWRIGHT_MODULE   optional path/specifier to import Playwright from
//                            (default /opt/flow/playwright/node_modules/playwright/index.mjs, then `playwright`)
//   FLOW_CODING_ARTIFACT_MAX_BYTES  optional upload size limit (default 10485760, mirrors the server)
//   FLOW_BROWSER_APP_COMMAND / FLOW_BROWSER_APP_URL / FLOW_BROWSER_APP_CWD
//                            optional app start settings from the environment's variables
//   HTTPS_PROXY / HTTP_PROXY / NO_PROXY  egress proxy; loopback is always bypassed
// The repository checkout is /workspace/repo.
import { BrowserToolkit } from '../../browser/toolkit.js';
import { browserEnabledByEnv } from '../../browser/config.js';
import type { BrowserToolOptions, JSONSchemaObject, RegisterTool, ToolResult, ToolSchema } from '../../browser/types.js';

export type { BrowserToolOptions, RegisterTool, ToolResult, ToolSchema } from '../../browser/types.js';

export const BROWSER_TOOL_NAMES = [
  'browser_start_app',
  'browser_stop_app',
  'browser_open',
  'browser_click',
  'browser_type',
  'browser_press',
  'browser_wait_for',
  'browser_read',
  'browser_screenshot',
  'browser_record_start',
  'browser_record_stop',
  'browser_close',
  'attach_screenshot',
] as const;

export type BrowserToolName = (typeof BROWSER_TOOL_NAMES)[number];

const viewportSchema = {
  type: 'object',
  description: 'Viewport size in CSS pixels (default 1280x720).',
  properties: {
    width: { type: 'integer', minimum: 200, maximum: 3840 },
    height: { type: 'integer', minimum: 200, maximum: 2160 },
  },
  required: ['width', 'height'],
  additionalProperties: false,
};

const labelSchema = {
  type: 'string',
  maxLength: 120,
  description: 'Artifact label: "before" or "after" for comparison shots, or a short description.',
};
const groupSchema = {
  type: 'string',
  maxLength: 80,
  description: 'Optional flow name that pairs "before" and "after" captures (e.g. "login form").',
};

function obj(properties: Record<string, unknown>, required: string[] = []): JSONSchemaObject {
  return { type: 'object', properties, ...(required.length ? { required } : {}), additionalProperties: false };
}

export const BROWSER_TOOL_SCHEMAS: Record<BrowserToolName, ToolSchema> = {
  browser_start_app: {
    description:
      "Start the project's web app (dev server) in the background and wait until it responds. Without arguments it uses FLOW_BROWSER_APP_COMMAND/URL, then .flow/browser.json, then the dev/start/preview/serve script in package.json. PORT and HOST=127.0.0.1 are passed to the command. Returns the app URL and recent output. Safe to call again: returns the running app.",
    inputSchema: obj({
      command: { type: 'string', description: 'Shell command to run, e.g. "pnpm dev". Overrides detection.' },
      url: { type: 'string', description: 'URL the app serves on, if it does not print it or ignores PORT.' },
      cwd: { type: 'string', description: 'Directory to run in, relative to /workspace/repo.' },
      port: { type: 'integer', minimum: 1, maximum: 65535, description: 'Port to pass as PORT (default 3000 or a free port).' },
      readyTimeoutSeconds: { type: 'integer', minimum: 1, maximum: 900, description: 'How long to wait for the app to respond (default 120).' },
    }),
  },
  browser_stop_app: {
    description: 'Stop the app started by browser_start_app (SIGTERM, then SIGKILL after 5s).',
    inputSchema: obj({}),
  },
  browser_open: {
    description:
      'Open a URL in headless Chromium. Relative paths ("/settings") resolve against the app started with browser_start_app. Only http(s) URLs. Returns the final URL, title, HTTP status and console errors.',
    inputSchema: obj(
      {
        url: { type: 'string', description: 'Absolute http(s) URL or a path relative to the started app.' },
        viewport: viewportSchema,
        record: { type: 'boolean', description: 'Start a video recording (label "recording") before navigating.' },
      },
      ['url'],
    ),
  },
  browser_click: {
    description: 'Click an element, located by exactly one of: CSS selector, visible text, or ARIA role (+ accessible name).',
    inputSchema: obj({
      selector: { type: 'string', description: 'CSS or Playwright selector.' },
      text: { type: 'string', description: 'Visible text of the element.' },
      role: { type: 'string', description: 'ARIA role, e.g. "button", "link", "tab".' },
      name: { type: 'string', description: 'Accessible name used with role.' },
    }),
  },
  browser_type: {
    description: 'Type text into an input, located by exactly one of: selector, role (+ name), or label. Replaces the current value unless clear is false.',
    inputSchema: obj(
      {
        selector: { type: 'string' },
        role: { type: 'string' },
        name: { type: 'string' },
        label: { type: 'string', description: 'Text of the associated <label> or aria-label.' },
        text: { type: 'string', description: 'Text to type.' },
        submit: { type: 'boolean', description: 'Press Enter afterwards.' },
        clear: { type: 'boolean', description: 'Replace the existing value (default true); false appends keystrokes.' },
      },
      ['text'],
    ),
  },
  browser_press: {
    description: 'Press a keyboard key or chord on the page, e.g. "Enter", "Escape", "Control+K".',
    inputSchema: obj({ key: { type: 'string' } }, ['key']),
  },
  browser_wait_for: {
    description: 'Wait until exactly one condition holds: a selector is visible, some text is visible, or the URL includes a substring.',
    inputSchema: obj({
      selector: { type: 'string' },
      text: { type: 'string' },
      urlIncludes: { type: 'string' },
      timeoutSeconds: { type: 'integer', minimum: 1, maximum: 120, description: 'Default 15.' },
    }),
  },
  browser_read: {
    description:
      'Read the visible text of the page (or of one element) plus recent console errors and failed network requests. Use it to inspect pages and find problems.',
    inputSchema: obj({
      selector: { type: 'string', description: 'Limit to the first element matching this selector.' },
      maxChars: { type: 'integer', minimum: 100, maximum: 100000, description: 'Truncate text to this many characters (default 8000).' },
    }),
  },
  browser_screenshot: {
    description:
      'Take a PNG screenshot of the current page, show it to you, and attach it to the coding session and the PR (unless upload is false). For UI changes take a "before" shot on the base ref and an "after" shot on the branch with the same group.',
    inputSchema: obj(
      {
        label: labelSchema,
        group: groupSchema,
        fullPage: { type: 'boolean', description: 'Capture the full scrollable page (default false).' },
        selector: { type: 'string', description: 'Capture only the first element matching this selector.' },
        upload: { type: 'boolean', description: 'Upload as an artifact (default true).' },
      },
      ['label'],
    ),
  },
  browser_record_start: {
    description: 'Start recording a video of the browser (reopens the current page in a recording context). Finish with browser_record_stop.',
    inputSchema: obj({ label: labelSchema, group: groupSchema, viewport: viewportSchema }, ['label']),
  },
  browser_record_stop: {
    description: 'Stop the recording, upload the WebM video to the session and the PR, and keep browsing on the same page.',
    inputSchema: obj({}),
  },
  browser_close: {
    description: 'Close the browser. An in-progress recording is finalized and uploaded first.',
    inputSchema: obj({}),
  },
  attach_screenshot: {
    description:
      "Attach an existing PNG, JPEG, WebM or MP4 file (for example output of the project's own Playwright tests) to the coding session and the PR. The file must be under /workspace, /tmp or $HOME.",
    inputSchema: obj(
      {
        path: { type: 'string', description: 'File path (relative paths resolve against /workspace/repo).' },
        label: labelSchema,
        group: groupSchema,
      },
      ['path', 'label'],
    ),
  },
};

/** Registers the browser tools with the runner's MCP server (no-op when disabled). */
export function registerBrowserTools(registerTool: RegisterTool, options: BrowserToolOptions = {}): void {
  createBrowserTools(registerTool, options);
}

/**
 * Same as registerBrowserTools but returns the toolkit (undefined when disabled), e.g. for tests or
 * a shutdown hook that closes the browser and stops the app.
 */
export function createBrowserTools(registerTool: RegisterTool, options: BrowserToolOptions = {}): BrowserToolkit | undefined {
  const env = options.env ?? process.env;
  if (options.enabled === false || !browserEnabledByEnv(env)) return undefined;
  const kit = new BrowserToolkit({ ...options, env });
  const handlers: Record<BrowserToolName, (args: Record<string, unknown>) => Promise<ToolResult>> = {
    browser_start_app: (a) => kit.startApp(a),
    browser_stop_app: () => kit.stopApp(),
    browser_open: (a) => kit.open(a),
    browser_click: (a) => kit.click(a),
    browser_type: (a) => kit.type(a),
    browser_press: (a) => kit.press(a),
    browser_wait_for: (a) => kit.waitFor(a),
    browser_read: (a) => kit.read(a),
    browser_screenshot: (a) => kit.screenshot(a),
    browser_record_start: (a) => kit.recordStart(a),
    browser_record_stop: () => kit.recordStop(),
    browser_close: () => kit.close(),
    attach_screenshot: (a) => kit.attach(a),
  };
  for (const name of BROWSER_TOOL_NAMES) {
    registerTool(name, BROWSER_TOOL_SCHEMAS[name], BrowserToolkit.safe(name, handlers[name]));
  }
  return kit;
}
