import { describe, expect, it } from "vitest";
import { translateToChinese } from "@/i18n/translate";
import { zhCN } from "@/i18n/translations";

const sources = import.meta.glob(["./*.tsx", "./*.ts", "!./*.test.ts", "!./*.test.tsx", "!./*test-kit.tsx"], { query: "?raw", import: "default", eager: true }) as Record<string, string>;

const PATTERNS = [
  /\bt\(\s*"((?:[^"\\]|\\.)*)"\s*[,)]/g,
  /\b(?:title|description|label|hint|placeholder|detail)[=:]\s*"([^"{}]*[A-Za-z]{3,}[^"{}]*)"/g,
  />\s*([A-Z][A-Za-z ,.]+[a-z.])\s*<\/CodingButton>/g,
  /\?\s*"([A-Z][A-Za-z ]+)"\s*:\s*"([A-Z][A-Za-z ]+)"/g,
];
/** Product names, identifiers and examples stay as written. */
const LITERAL = new Set(["Codex", "Claude Code", "Slack", "Intercom", "Zendesk", "Front", "Sentry", "NAME", "node", ".npmrc", "Auto", "GitHub", "GitLab", "VARIABLE_NAME", "npm ci", ".example.com", ...["Node.js", "Bun", "Deno", "Python", "Ruby", "Go", "Rust", "Java", ".NET", "PostgreSQL", "Redis", "MongoDB", "ClickHouse"]]);

describe("coding sessions translations", () => {
  it("has a Chinese translation for every literal string", () => {
    const missing: string[] = [];
    for (const [path, source] of Object.entries(sources)) {
      for (const pattern of PATTERNS) {
        for (const match of source.matchAll(pattern)) {
          for (const raw of match.slice(1)) {
            if (!raw) continue;
            const text = raw.replace(/\\(["'])/g, "$1").trim();
            if (!/[A-Za-z]{2,}/.test(text) || LITERAL.has(text)) continue;
            if (!(text in zhCN) && translateToChinese(text) === text) missing.push(`${path}: ${text}`);
          }
        }
      }
    }
    expect([...new Set(missing)]).toEqual([]);
  });
});
