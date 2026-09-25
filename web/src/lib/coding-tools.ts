/**
 * Coding tools an issue can be opened in from the "Work on issue" menu. Each
 * tool is launched with a deep link that carries the rendered issue prompt.
 */
export type CodingToolId =
  | "claudeCodeDesktop"
  | "codex"
  | "cursor"
  | "devin"
  | "windsurf"
  | "factory"
  | "conductor"
  | "customUrl";

export interface CodingTool {
  id: CodingToolId;
  name: string;
  description: string;
  /** Builds the launch URL, or undefined when the tool is not configured. */
  link: (prompt: string, settings: CodingToolSettings) => string | undefined;
}

export interface CodingToolSettings {
  enabledCodingTools?: string[];
  customDeepLinkUrlTemplate?: string;
  codingPromptTemplate?: string;
}

export interface CodingPromptIssue {
  identifier: string;
  title: string;
  description?: string;
  branchName: string;
  url: string;
}

// Deep links carry the prompt in the URL, so keep it to a size apps accept.
const MAX_PROMPT = 2000;
const clip = (prompt: string, max = MAX_PROMPT) => (prompt.length > max ? `${prompt.slice(0, max - 1)}…` : prompt);
const enc = encodeURIComponent;

export const CODING_TOOLS: CodingTool[] = [
  { id: "claudeCodeDesktop", name: "Claude Code desktop", description: "Opens in the Claude desktop app", link: (prompt) => `claude://code/new?q=${enc(clip(prompt))}` },
  { id: "codex", name: "Codex desktop", description: "Opens in the Codex desktop app", link: (prompt) => `codex://new?prompt=${enc(clip(prompt))}` },
  { id: "cursor", name: "Cursor", description: "Opens in the Cursor desktop app", link: (prompt) => `cursor://anysphere.cursor-deeplink/prompt?text=${enc(enc(clip(prompt, 8000)))}` },
  { id: "devin", name: "Devin", description: "Opens on devin.ai", link: (prompt) => `https://app.devin.ai/?prompt=${enc(clip(prompt))}` },
  { id: "windsurf", name: "Windsurf", description: "Opens in the Windsurf desktop app", link: (prompt) => `windsurf://cascade?prompt=${enc(enc(clip(prompt)))}` },
  { id: "factory", name: "Factory", description: "Opens in the Factory desktop app", link: (prompt) => `factory-desktop://new?prompt=${enc(clip(prompt))}` },
  { id: "conductor", name: "Conductor", description: "Opens in the Conductor desktop app", link: (prompt) => `conductor://prompt=${enc(clip(prompt))}` },
  {
    id: "customUrl",
    name: "Custom link",
    description: "Open a web based coding tool with a custom link",
    link: (prompt, settings) => {
      const template = settings.customDeepLinkUrlTemplate?.trim();
      if (!template || !/^https?:\/\//i.test(template)) return undefined;
      return template.split("{{prompt}}").join(enc(clip(prompt)));
    },
  },
];

export const DEFAULT_CODING_PROMPT_TEMPLATE = "# {{issue.identifier}}: {{issue.title}}\n\n{{context}}";

/** Renders the user's prompt template for an issue. */
export function renderCodingPrompt(template: string | undefined, issue: CodingPromptIssue): string {
  const context = `${issue.description?.trim() || "No description provided."}\n\nIssue URL: ${issue.url}`;
  const values: Record<string, string> = {
    "issue.identifier": issue.identifier,
    "issue.title": issue.title,
    "issue.branchName": issue.branchName,
    "issue.url": issue.url,
    context,
  };
  return (template?.trim() || DEFAULT_CODING_PROMPT_TEMPLATE).replace(/\{\{\s*([\w.]+)\s*\}\}/g, (match, key: string) => values[key] ?? match);
}

export function enabledCodingTools(settings: CodingToolSettings | undefined): CodingTool[] {
  const enabled = new Set(settings?.enabledCodingTools ?? []);
  return CODING_TOOLS.filter((tool) => enabled.has(tool.id));
}
