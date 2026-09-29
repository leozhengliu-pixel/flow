import { useEffect, useState } from "react";
import { toast } from "sonner";
import { getLoop, getLoopConfig, listLoopTemplates, listLoops } from "@/lib/api";
import { loopPath, newLoopPath } from "@/lib/app-routes";
import type { AgentMessagePart, AgentToolCall, BootstrapData, Loop, LoopConfig, LoopRun, LoopTemplate, Team } from "@/types/flow";
import { isLoopDraft } from "./loop-model";

const AUTOSTART_KEY = "flow:loop-agent-autostart:";

/** Remembers that the loop-builder agent should send its first message when the editor opens this draft. */
export function markLoopAgentAutostart(loopId: string) {
  try {
    sessionStorage.setItem(`${AUTOSTART_KEY}${loopId}`, "1");
  } catch {
    /* storage unavailable */
  }
}

export function takeLoopAgentAutostart(loopId: string) {
  try {
    const key = `${AUTOSTART_KEY}${loopId}`;
    const pending = sessionStorage.getItem(key) === "1";
    sessionStorage.removeItem(key);
    return pending;
  } catch {
    return false;
  }
}

const HANDOFF_KEY = "flow:loop-agent-handoff:";

/** The loop builder published this loop: the loop page opens with the same conversation docked, like Linear. */
export function markLoopAgentHandoff(loopId: string) {
  try {
    sessionStorage.setItem(`${HANDOFF_KEY}${loopId}`, "1");
  } catch {
    /* storage unavailable */
  }
}

export function takeLoopAgentHandoff(loopId: string) {
  try {
    const key = `${HANDOFF_KEY}${loopId}`;
    const pending = sessionStorage.getItem(key) === "1";
    sessionStorage.removeItem(key);
    return pending;
  } catch {
    return false;
  }
}

/** Templates are static for a session. */
let templateCache: LoopTemplate[] | undefined;
export function resetLoopTemplateCache() {
  templateCache = undefined;
}

export function useLoopTemplates() {
  const [templates, setTemplates] = useState<LoopTemplate[] | undefined>(templateCache);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    if (templateCache) return;
    let active = true;
    listLoopTemplates()
      .then((items) => {
        templateCache = Array.isArray(items) ? items : [];
        if (active) setTemplates(templateCache);
      })
      .catch(() => {
        if (active) {
          setTemplates([]);
          setFailed(true);
        }
      });
    return () => {
      active = false;
    };
  }, []);
  return { templates, failed };
}

/** Workspace Loops configuration (web search availability…); fetched once per session. */
let configCache: Promise<LoopConfig | undefined> | undefined;
export function resetLoopConfigCache() {
  configCache = undefined;
}

export function useLoopConfig() {
  const [config, setConfig] = useState<LoopConfig>();
  useEffect(() => {
    let active = true;
    configCache ??= getLoopConfig().catch(() => {
      configCache = undefined;
      return undefined;
    });
    void configCache.then((next) => active && setConfig(next));
    return () => {
      active = false;
    };
  }, []);
  return config;
}

/** Loads a loop from bootstrap, then keeps it fresh from the API. */
export function useLoopRecord(data: BootstrapData, loopId: string) {
  const cached = data.loops.find((item) => item.id === loopId);
  const [loop, setLoop] = useState<Loop | undefined>(cached);
  const [missing, setMissing] = useState(false);
  useEffect(() => {
    let active = true;
    getLoop(loopId)
      .then((next) => {
        if (!active) return;
        setLoop(next);
        setMissing(false);
      })
      .catch(() => active && !cached && setMissing(true));
    return () => {
      active = false;
    };
  }, [cached, loopId]);
  return { loop, setLoop, missing };
}

/** Loops from bootstrap, refreshed from the API (the list carries drafts and run counts). */
export function useLoops(data: Pick<BootstrapData, "loops">) {
  const [fetched, setFetched] = useState<Loop[]>();
  useEffect(() => {
    let active = true;
    listLoops()
      .then((items) => {
        if (active && Array.isArray(items)) setFetched(items);
      })
      .catch(() => undefined);
    return () => {
      active = false;
    };
  }, [data.loops]);
  return [fetched ?? data.loops ?? [], setFetched] as const;
}

export function loopOwner(data: Pick<BootstrapData, "users">, loop: Loop) {
  return data.users.find((user) => user.id === (loop.ownerId ?? loop.creator?.id)) ?? loop.creator;
}

export function activeTeams(teams: Team[]) {
  return teams.filter((team) => !team.retiredAt && !team.archivedAt);
}

/** Absolute link to a loop page. */
export function loopUrl(workspaceSlug: string, loop: Pick<Loop, "id" | "status">) {
  const path = isLoopDraft(loop) ? `${newLoopPath(workspaceSlug)}?draftId=${encodeURIComponent(loop.id)}` : loopPath(workspaceSlug, loop.id);
  return `${window.location.origin}${path}`;
}

export async function copyText(text: string, message: string) {
  try {
    await navigator.clipboard.writeText(text);
    toast.success(message);
  } catch {
    toast.error("Could not copy to clipboard");
  }
}

/** Steps and tool calls in their shared order, as agent work-group parts. */
export function runParts(run: LoopRun): AgentMessagePart[] {
  const steps = (run.steps ?? []).map((step, index) => ({
    order: step.order ?? index,
    part: { id: `step-${step.order ?? index}`, type: "step", title: step.title, text: step.message, status: "completed" } as AgentMessagePart,
  }));
  const tools = (run.toolCalls ?? []).map((call, index) => ({
    order: call.order ?? steps.length + index,
    part: {
      id: `tool-${call.id ?? index}`,
      type: "toolCall",
      toolCall: {
        id: call.id ?? `tool-${index}`,
        name: call.name,
        title: call.label,
        arguments: call.args ? { title: call.args } : undefined,
        status: call.status === "blocked" ? "error" : call.status,
        error: call.status === "blocked" ? (call.error ?? "Blocked by loop permissions") : call.error,
      },
    } as AgentMessagePart,
  }));
  return [...steps, ...tools].sort((a, b) => a.order - b.order).map((item) => item.part);
}

/** `save_loop` tool result (see loops-api-contract.md → Agent). */
export type LoopToolResult = {
  id?: string;
  name?: string;
  description?: string;
  status?: string;
  enabled?: boolean;
  trigger?: string;
  level?: string;
  teamId?: string;
  teamName?: string;
  runCount30d?: number;
  url?: string;
  published?: boolean;
  icon?: string;
  color?: string;
};

export function loopToolResult(call: AgentToolCall | undefined): LoopToolResult | undefined {
  if (!call || call.name !== "save_loop" || call.status !== "completed") return undefined;
  let result = call.result;
  if (typeof result === "string") {
    try {
      result = JSON.parse(result);
    } catch {
      return undefined;
    }
  }
  if (!result || typeof result !== "object" || Array.isArray(result)) return undefined;
  const record = result as Record<string, unknown>;
  // Some tool envelopes nest the loop.
  const loop = (record.loop && typeof record.loop === "object" ? record.loop : record) as LoopToolResult;
  return loop;
}

export function isPublishedResult(result: LoopToolResult | undefined) {
  return Boolean(result && (result.published || result.status === "published"));
}

/** First message the loop builder receives (contract: template → "Set up this loop from the <name> template"). */
export function loopBuilderFirstMessage(loop: { templateId?: string; sourcePrompt?: string; name?: string }, templateName?: string) {
  if (loop.sourcePrompt) return loop.sourcePrompt;
  if (loop.templateId) return `Set up this loop from the ${templateName || loop.name || loop.templateId} template`;
  return "";
}

/** `save_loop` that published the loop ("Created automation"); the server titles it, older results carry `published`. */
export function isPublishCall(call: AgentToolCall | undefined) {
  if (!call) return false;
  if (call.title) return call.title === "Created automation";
  return isPublishedResult(loopToolResult(call)) || call.arguments?.publish === true;
}

/**
 * The builder's reply is the intro it says before its questions ("I've opened a draft…") followed by the final reply.
 * They arrive as one text: split at the first paragraph break, or where two turns were joined without a space.
 */
export function splitBuilderReply(content: string, hasQuestions: boolean) {
  const text = content.trim();
  if (!hasQuestions || !text) return { intro: "", reply: text };
  const paragraph = text.indexOf("\n\n");
  if (paragraph > 0) return { intro: text.slice(0, paragraph).trim(), reply: text.slice(paragraph).trim() };
  const joined = /[.!?:](?=[A-Z])/.exec(text);
  if (joined) return { intro: text.slice(0, joined.index + 1).trim(), reply: text.slice(joined.index + 1).trim() };
  return { intro: text, reply: "" };
}
