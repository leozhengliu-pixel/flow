import { useEffect, useRef, useState, type ChangeEvent } from "react";
import { Check, ChevronRight, Plus, Search, X } from "lucide-react";
import { fetchAgentStatus } from "@/lib/api";
import {
  streamAgentSessionMessage,
  streamNewAgentSession,
  type AgentStreamEvent,
} from "@/lib/agent-stream";
import type {
  AgentMessage,
  AgentSession,
  AgentStatus,
  PersonalAgentSkill,
} from "@/types/flow";
import { useI18n } from "@/i18n/i18n";
import {
  AgentAttachIcon,
  AgentChevronDownIcon,
  AgentPointerIcon,
  AgentSkillsIcon,
  AgentSubmitIcon,
} from "@/components/agent/agent-icons";
import { AgentRichText } from "@/components/agent/agent-rich-text";
import { ViewGlyph } from "@/components/views/view-icon-picker";
import type { NewProjectDraft } from "./new-project-dialog";
import { parseProjectAgentDraft, projectAgentPrompt } from "./project-agent-draft";
import "./project-creation-agent.css";

export type ProjectCreationAgentProps = {
  agentSkills?: PersonalAgentSkill[];
  draft?: NewProjectDraft;
  hidden?: boolean;
  onApplyDraft?: (patch: Partial<NewProjectDraft>) => void;
  onClose: () => void;
  onHide: () => void;
  workspaceName?: string;
  onCreateSkill?: () => void;
};

type ProjectCreationAgentMessage = AgentMessage & { pending?: boolean };

const SUGGESTIONS = [
  ["Outline the scope", "Outline the scope"],
  ["Research customer requests", "Research customer requests"],
  ["Plan timeline", "Plan timeline"],
  ["Choose the team", "Choose the team"],
] as const;

export function ProjectCreationAgent({
  agentSkills = [],
  draft,
  onApplyDraft,
  onClose,
  onHide,
  onCreateSkill,
  hidden = false,
  workspaceName,
}: ProjectCreationAgentProps) {
  const { t } = useI18n();
  const [input, setInput] = useState("");
  const [messages, setMessages] = useState<ProjectCreationAgentMessage[]>([]);
  const [conversationStartedAt, setConversationStartedAt] = useState<string>();
  const [durations, setDurations] = useState<Record<string, number>>({});
  const [session, setSession] = useState<AgentSession>();
  const [status, setStatus] = useState<AgentStatus>();
  const [busy, setBusy] = useState(false);
  const [skillsOpen, setSkillsOpen] = useState(false);
  const [skillQuery, setSkillQuery] = useState("");
  const [selectedSkills, setSelectedSkills] = useState<string[]>([]);
  const [attachments, setAttachments] = useState<File[]>([]);
  const [error, setError] = useState<string>();
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const abortRef = useRef<AbortController | undefined>(undefined);
  const conversationRef = useRef<HTMLDivElement>(null);
  const streamStartedAtRef = useRef<number | undefined>(undefined);

  useEffect(() => {
    if (hidden) return;
    let active = true;
    fetchAgentStatus()
      .then((next) => {
        if (active) setStatus(next);
      })
      .catch(() => {
        // The assistant remains usable as a draft composer when no provider is configured.
        if (active) setStatus({ enabled: false, model: "" });
      });
    requestAnimationFrame(() => inputRef.current?.focus());
    return () => {
      active = false;
      abortRef.current?.abort();
    };
  }, [hidden]);

  useEffect(() => {
    const viewport = conversationRef.current;
    if (!viewport) return;
    viewport.scrollTop = viewport.scrollHeight;
  }, [messages, busy]);

  useEffect(() => {
    if (!skillsOpen) return;
    const close = (event: PointerEvent) => {
      if (!(event.target as HTMLElement | null)?.closest(".project-creation-agent__skill-wrap")) setSkillsOpen(false);
    };
    document.addEventListener("pointerdown", close);
    return () => document.removeEventListener("pointerdown", close);
  }, [skillsOpen]);

  useEffect(() => {
    if (!skillsOpen) setSkillQuery("");
  }, [skillsOpen]);

  const chooseSuggestion = (message: string) => {
    setInput(message);
    void send(message);
  };

  const chooseSkill = (skillId: string) => {
    setSelectedSkills((current) =>
      current.includes(skillId)
        ? current.filter((id) => id !== skillId)
        : [...current, skillId],
    );
  };

  const createSkill = () => {
    setSkillsOpen(false);
    if (onCreateSkill) {
      onCreateSkill();
      return;
    }
    const workspaceKey = window.location.pathname.split("/").filter(Boolean)[0];
    if (workspaceKey) window.location.assign(`/${workspaceKey}/settings/skill/new`);
  };

  const visibleSkills = agentSkills.filter((skill) =>
    skill.name.toLocaleLowerCase().includes(skillQuery.trim().toLocaleLowerCase()),
  );

  const send = async (override?: string) => {
    const text = (override ?? input).trim();
    if (!text || busy || status?.enabled === false) {
      if (text && status?.enabled === false) setError(t("Flow Agent is not configured"));
      return;
    }
    const draftContext = draft
      ? `\n\nCurrent project draft:\n${JSON.stringify({
          name: draft.name,
          summary: draft.summary,
          description: draft.description,
          status: draft.status,
          priority: draft.priority,
          startDate: draft.startDate,
          targetDate: draft.targetDate,
          milestones: draft.milestones,
        })}`
      : "";
    let message: string;
    try {
      const attachmentContext = attachments.length
        ? `\n\n${(await Promise.all(attachments.map(projectAgentFileContext))).join("\n\n")}`
        : "";
      message = projectAgentPrompt(`${text}${draftContext}${attachmentContext}`);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : t("Could not read attachment"));
      return;
    }
    setInput("");
    setAttachments([]);
    setError(undefined);
    setMessages((current) => [
      ...current,
      {
        id: `project-agent-user-${Date.now()}`,
        role: "user",
        content: text,
        createdAt: new Date().toISOString(),
      },
    ]);
    setConversationStartedAt((current) => current ?? new Date().toISOString());
    setBusy(true);
    streamStartedAtRef.current = performance.now();
    const controller = new AbortController();
    abortRef.current = controller;
    let nextSession = session;
    let assistantMessageId = "";
    try {
      const onEvent = (event: AgentStreamEvent) => {
        if (event.session) {
          nextSession = event.session;
          setSession(event.session);
        }
        if (event.type === "session.started" && event.session) {
          setMessages(presentAgentMessages(event.session.messages));
        }
        if (event.type === "text.delta") {
          assistantMessageId = event.messageId || assistantMessageId || `project-agent-assistant-${Date.now()}`;
          setMessages((current) => {
            const index = current.findIndex((item) => item.id === assistantMessageId);
            if (index < 0) {
              return [
                ...current,
                {
                  id: assistantMessageId,
                  role: "assistant",
                  content: event.delta ?? "",
                  createdAt: new Date().toISOString(),
                  pending: true,
                },
              ];
            }
            return current.map((item, itemIndex) =>
              itemIndex === index
                ? { ...item, content: item.content + (event.delta ?? "") }
                : item,
            );
          });
        }
        if (event.type === "session.completed" && event.session) {
          setMessages(presentAgentMessages(event.session.messages));
          nextSession = event.session;
          setSession(event.session);
          const assistant = [...event.session.messages].reverse().find((item) => item.role === "assistant");
          if (assistant) {
            const elapsed = streamStartedAtRef.current === undefined ? 1 : Math.max(1, Math.round((performance.now() - streamStartedAtRef.current) / 1000));
            setDurations((current) => ({ ...current, [assistant.id]: elapsed }));
          }
          maybeApplyDraft(event.session.messages.at(-1)?.content, onApplyDraft);
        }
      };
      const result = nextSession
        ? await streamAgentSessionMessage(
            nextSession.id,
            message,
            onEvent,
            controller.signal,
          )
        : await streamNewAgentSession(
            {
              message,
              skillIds: selectedSkills,
              location: "page",
            },
            onEvent,
            controller.signal,
          );
      setSession(result);
      setMessages(presentAgentMessages(result.messages));
    } catch (reason) {
      if (reason instanceof DOMException && reason.name === "AbortError") return;
      setError(
        reason instanceof Error
          ? reason.message
          : t("Flow Agent is unavailable"),
      );
    } finally {
      abortRef.current = undefined;
      setBusy(false);
      requestAnimationFrame(() => inputRef.current?.focus());
    }
  };

  const onAttachmentChange = (event: ChangeEvent<HTMLInputElement>) => {
    const files = [...(event.target.files ?? [])].filter(
      (file) => file.size <= 2 * 1024 * 1024,
    );
    setAttachments((current) => [...current, ...files].slice(0, 8));
    event.target.value = "";
  };

  return (
    <aside
      aria-label={t("Project creation assistant")}
      className="project-creation-agent"
      data-hidden={hidden || undefined}
      onKeyDown={(event) => {
        if (event.key === "Escape" && skillsOpen) {
          event.preventDefault();
          event.stopPropagation();
          setSkillsOpen(false);
        }
      }}
      role="complementary"
    >
      <header className="project-creation-agent__header">
        <span />
        <button
          aria-expanded="true"
          aria-label={t("Hide agent")}
          className="project-creation-agent__hide"
          onClick={onHide}
          type="button"
        >
          <AgentPointerIcon size={14} />
          <span>{t("Hide")}</span>
        </button>
        <button
          aria-label={t("Close project creation")}
          className="project-creation-agent__close"
          onClick={onClose}
          type="button"
        >
          <X size={16} />
        </button>
      </header>
      <div className="project-creation-agent__body">
        {messages.length ? (
          <div
            aria-label={t("Agent conversation")}
            className="project-creation-agent__conversation"
            ref={conversationRef}
            role="log"
          >
            <div className="project-creation-agent__date">
              <time dateTime={conversationStartedAt ?? messages[0]?.createdAt}>
                {formatConversationTime(conversationStartedAt ?? messages[0]?.createdAt, t("Today"))}
              </time>
            </div>
            <div className="project-creation-agent__messages">
              {messages.map((message, index) => (
                <div className={`project-creation-agent__turn project-creation-agent__turn--${message.role}`} key={message.id}>
                  {message.role === "assistant" && durations[message.id] !== undefined && (
                    <div className="project-creation-agent__duration">
                      <button aria-expanded="false" type="button">
                        {t("Worked for")} {durations[message.id]} {t(durations[message.id] === 1 ? "second" : "seconds")}
                        <ChevronRight size={11} />
                      </button>
                    </div>
                  )}
                  <article className={`project-creation-agent__message project-creation-agent__message--${message.role}`}>
                    <AgentRichText className="project-creation-agent__rich-text" content={message.content} />
                  </article>
                  {message.role === "user" && index === 0 && (
                    <div className="project-creation-agent__context">
                      <ViewGlyph color="currentColor" icon="Project" />
                      <span>Untitled draft</span>
                      <span>{t("added to context")}</span>
                    </div>
                  )}
                </div>
              ))}
            </div>
            {busy && (
              <div className="project-creation-agent__thinking" aria-live="polite">
                <span className="project-creation-agent__spinner" />
                {t("Thinking…")}
              </div>
            )}
          </div>
        ) : (
          <div className="project-creation-agent__empty">
            <AgentBackgroundGlyph />
            <strong>{t("Draft a new project")}</strong>
            <p>
              {t(
                "Projects define a clear outcome and completion date, and group related issues and documents.",
              )}
            </p>
            <div className="project-creation-agent__suggestions">
              {SUGGESTIONS.map(([label, message]) => (
                <button
                  key={label}
                  onClick={() => chooseSuggestion(message)}
                  type="button"
                >
                  <span>{t(label)}</span>
                </button>
              ))}
            </div>
          </div>
        )}
      </div>
      <div className="project-creation-agent__composer">
        <div className="project-creation-agent__composer-card">
        {attachments.length > 0 && (
          <div className="project-creation-agent__attachments">
            {attachments.map((file) => (
              <span key={`${file.name}-${file.lastModified}`}>{file.name}</span>
            ))}
          </div>
        )}
        <textarea
          aria-label={t("Send a message to Linear AI")}
          disabled={busy}
          onChange={(event) => setInput(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter" && !event.shiftKey) {
              event.preventDefault();
              void send();
            }
          }}
          placeholder={t("Draft your project with Linear…")}
          ref={inputRef}
          rows={1}
          value={input}
        />
        <footer>
          <div className="project-creation-agent__skill-wrap">
            <button
              aria-expanded={skillsOpen}
              aria-haspopup="menu"
              aria-label={t("Skills")}
              className="project-creation-agent__skills"
              onClick={() => setSkillsOpen((value) => !value)}
              type="button"
            >
              <AgentSkillsIcon />
              <span>{t("Skills")}</span>
              <AgentChevronDownIcon />
            </button>
            {skillsOpen && (
              <div className="project-creation-agent__skills-menu" role="menu">
                <div className="project-creation-agent__skills-search">
                  <Search size={14} />
                  <input
                    aria-label={t("Search skills…")}
                    autoFocus
                    onChange={(event) => setSkillQuery(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === "Escape") {
                        event.preventDefault();
                        setSkillsOpen(false);
                      }
                    }}
                    placeholder={t("Search skills…")}
                    value={skillQuery}
                  />
                </div>
                {visibleSkills.length ? (
                  <>
                  {visibleSkills.map((skill) => (
                    <button
                      aria-checked={selectedSkills.includes(skill.id)}
                      key={skill.id}
                      onClick={() => chooseSkill(skill.id)}
                      role="menuitemcheckbox"
                      type="button"
                    >
                      <AgentSkillsIcon />
                      <span>{skill.name}</span>
                      {selectedSkills.includes(skill.id) && <Check />}
                    </button>
                  ))}
                  <button className="project-creation-agent__create-skill" onClick={createSkill} role="menuitem" type="button">
                    <Plus size={14} />
                    <span>{t("Create skill")}</span>
                  </button>
                  </>
                ) : (
                  <button className="project-creation-agent__create-skill" onClick={createSkill} role="menuitem" type="button">
                    <Plus size={14} />
                    <span>{t("Create skill")}</span>
                  </button>
                )}
              </div>
            )}
          </div>
          <span />
          <button
            aria-label={t("Attach images, files, or videos")}
            onClick={() => fileInputRef.current?.click()}
            type="button"
          >
            <AgentAttachIcon size={14} />
          </button>
          <input
            accept="image/*,video/*,text/*,application/json,application/pdf,.md,.markdown,.csv"
            className="project-creation-agent__file-input"
            multiple
            onChange={onAttachmentChange}
            ref={fileInputRef}
            type="file"
          />
          <button
            aria-label={busy ? t("Stop generating") : t("Submit comment")}
            disabled={!input.trim() && !busy}
            onClick={() => {
              if (busy) abortRef.current?.abort();
              else void send();
            }}
            type="button"
          >
            {busy ? <X size={14} /> : <AgentSubmitIcon size={16} />}
          </button>
        </footer>
        {error && <span className="project-creation-agent__error" role="alert">{error}</span>}
        {workspaceName && <span className="project-creation-agent__workspace" aria-hidden="true">{workspaceName}</span>}
        </div>
      </div>
    </aside>
  );
}

function AgentBackgroundGlyph() {
  const featuredIndex = 241;
  return (
    <>
      <div aria-hidden="true" className="project-creation-agent__background" data-grid-count="253">
        {Array.from({ length: 253 }, (_, index) => (
          <span aria-hidden={index === featuredIndex || undefined} className={index === featuredIndex ? "is-featured-slot" : undefined} data-grid-index={index} key={index}>
            <svg viewBox="0 0 16 16">
              <use href="#Project" />
            </svg>
          </span>
        ))}
      </div>
      <svg aria-hidden="true" className="project-creation-agent__featured" style={{ overflow: "visible" }} viewBox="0 0 16 16">
        <defs>
          <linearGradient id="project-agent-featured-gradient" x1="0" x2="0" y1="0" y2="1">
            <stop offset="0" stopColor="lch(10% 0 282)" />
            <stop offset="1" stopColor="lch(40% 1 282)" />
          </linearGradient>
          <filter id="project-agent-featured-blur-4" x="-50%" y="-50%" width="200%" height="200%">
            <feGaussianBlur stdDeviation="4" />
          </filter>
          <filter id="project-agent-featured-blur-2" x="-50%" y="-50%" width="200%" height="200%">
            <feGaussianBlur stdDeviation="2" />
          </filter>
        </defs>
        <g filter="url(#project-agent-featured-blur-4)" opacity=".15">
          <use fill="url(#project-agent-featured-gradient)" href="#Project" />
        </g>
        <g filter="url(#project-agent-featured-blur-2)" opacity=".3">
          <use fill="url(#project-agent-featured-gradient)" href="#Project" />
        </g>
        <use fill="url(#project-agent-featured-gradient)" href="#Project" />
      </svg>
    </>
  );
}

function maybeApplyDraft(content: string | undefined, onApplyDraft?: (patch: Partial<NewProjectDraft>) => void) {
  if (!content || !onApplyDraft) return;
  const patch = parseProjectAgentDraft(content);
  if (patch) onApplyDraft(patch);
}

function presentAgentMessages(messages: AgentMessage[]): ProjectCreationAgentMessage[] {
  return messages.map((message) => message.role === "user"
    ? { ...message, content: presentUserMessage(message.content) }
    : message);
}

function presentUserMessage(content: string) {
  const marker = "Draft a new project from the request below.";
  if (!content.startsWith(marker)) return content;
  const request = content.slice(marker.length).replace(/^\s+/, "");
  const requestOnly = request.split(/\n\nCurrent project draft:/, 1)[0].split(/\n\n/).at(-1) ?? request;
  return requestOnly
    .replace(/\n\nAttached files?:[\s\S]*$/i, "")
    .trim();
}

async function projectAgentFileContext(file: File) {
  if (file.type.startsWith("text/") || /\.(md|markdown|mdx|csv|json|xml|ya?ml)$/i.test(file.name)) {
    return `Attached file ${file.name}:\n${await file.text()}`;
  }
  if (file.type.startsWith("image/")) {
    return `Attached image ${file.name} (${file.type}, ${file.size} bytes): ${await projectAgentDataUrl(file)}`;
  }
  return `Attached file ${file.name} (${file.type || "application/octet-stream"}, ${file.size} bytes).`;
}

function projectAgentDataUrl(file: File) {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result ?? ""));
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(file);
  });
}

function formatConversationTime(value: string | undefined, todayLabel: string) {
  if (!value) return todayLabel;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return todayLabel;
  return `${todayLabel} ${new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit", hour12: false }).format(date)}`;
}
