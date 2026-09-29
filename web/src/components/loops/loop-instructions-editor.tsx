import { createContext, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Node as TiptapNode, createInlineMarkdownSpec } from "@tiptap/core";
import Placeholder from "@tiptap/extension-placeholder";
import { Markdown } from "@tiptap/markdown";
import { EditorContent, NodeViewWrapper, ReactNodeViewRenderer, useEditor, type Editor, type ReactNodeViewProps } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import { FileText } from "lucide-react";
import { ProjectIcon, StatusIcon } from "@/components/issue/issue-icons";
import { useIssueCandidates } from "@/components/issue/use-issue-candidates";
import { UserAvatar } from "@/components/ui/user-avatar";
import { linkAgentEntities } from "@/components/agent/agent-answer-content";
import { documentPath, issuePath, projectPath } from "@/lib/app-routes";
import { useI18n } from "@/i18n/i18n";
import type { BootstrapData } from "@/types/flow";

/** Inline entity reference inside loop instructions (Linear's "⁠Compose Test" chip). */
/** Same node as issue descriptions (`mention` with `mentionType`), so the server can list the references for the model. */
export const LOOP_ENTITY_NODE = "mention";
export type LoopEntityKind = "issue" | "project" | "user" | "document";
export type LoopEntityAttrs = { mentionType: LoopEntityKind; id: string; label: string; title?: string; href?: string };

type EntityContext = { data?: BootstrapData; onNavigate?: (path: string) => void };
const LoopEntityContext = createContext<EntityContext>({});

const entityMarkdown = createInlineMarkdownSpec({
  nodeName: LOOP_ENTITY_NODE,
  selfClosing: true,
  allowedAttributes: ["id", "label", "href", "title", "mentionType"],
});

/**
 * Markdown the model reads: issues as their identifier, people as @name, projects and documents as links.
 * The editor round-trips through `instructionsData`, so the chip identity is never lost.
 */
export function loopEntityMarkdown(attrs: Partial<LoopEntityAttrs>) {
  const label = String(attrs.label ?? "");
  if (attrs.mentionType === "user") return `@${label}`;
  if (attrs.mentionType === "issue") return label;
  return attrs.href ? `[${label}](${attrs.href})` : label;
}

export const LoopEntityNode = TiptapNode.create({
  name: LOOP_ENTITY_NODE,
  inline: true,
  group: "inline",
  atom: true,
  selectable: true,
  addAttributes() {
    return { id: { default: "" }, label: { default: "" }, href: { default: "" }, title: { default: "" }, mentionType: { default: "user" } };
  },
  parseHTML() {
    return [
      {
        tag: "span[data-loop-entity]",
        getAttrs: (node) =>
          node instanceof HTMLElement
            ? {
                mentionType: node.getAttribute("data-loop-entity") ?? "user",
                id: node.getAttribute("data-entity-id") ?? "",
                label: node.getAttribute("data-entity-label") ?? node.textContent ?? "",
                title: node.getAttribute("data-entity-title") ?? "",
                href: node.getAttribute("data-entity-href") ?? "",
              }
            : false,
      },
    ];
  },
  renderHTML({ HTMLAttributes }) {
    return [
      "span",
      {
        "data-loop-entity": HTMLAttributes.mentionType,
        "data-entity-id": HTMLAttributes.id,
        "data-entity-label": HTMLAttributes.label,
        "data-entity-title": HTMLAttributes.title,
        "data-entity-href": HTMLAttributes.href,
      },
      String(HTMLAttributes.label ?? ""),
    ];
  },
  renderText({ node }) {
    return loopEntityMarkdown(node.attrs as LoopEntityAttrs);
  },
  addNodeView() {
    return ReactNodeViewRenderer(LoopEntityChipView, { as: "span" });
  },
  markdownTokenName: entityMarkdown.markdownTokenizer.name,
  parseMarkdown: entityMarkdown.parseMarkdown,
  markdownTokenizer: entityMarkdown.markdownTokenizer,
  renderMarkdown: (node) => loopEntityMarkdown((node.attrs ?? {}) as LoopEntityAttrs),
});

function shortcode(attrs: LoopEntityAttrs) {
  const value = (text: string | undefined) => (text ?? "").replace(/["\]\n]/g, " ");
  const extra = `${attrs.title ? ` title="${value(attrs.title)}"` : ""}${attrs.href ? ` href="${value(attrs.href)}"` : ""}`;
  return `[${LOOP_ENTITY_NODE} id="${value(attrs.id)}" label="${value(attrs.label)}"${extra} mentionType="${attrs.mentionType}"]`;
}

function escapeRegExp(value: string) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/**
 * Markdown-only instructions (older loops, or instructions the loop builder wrote) turned into editor markdown:
 * issue identifiers and Flow issue / project links, document links and @people become chips.
 */
export function instructionsEditorMarkdown(markdown: string, data?: BootstrapData) {
  if (!markdown || !data) return markdown;
  const workspace = data.workspace.urlKey;
  let next = linkAgentEntities(markdown, data).markdown.replace(/\[agentEntity kind="(issue|project)" id="([^"]*)" label="([^"]*)"\]/g, (match, kind: string, id: string) => {
    if (kind === "issue") {
      const issue = data.issues.find((item) => item.id === id);
      return issue ? shortcode(issueAttrs(workspace, issue)) : match;
    }
    const project = data.projects.find((item) => item.id === id);
    return project ? shortcode(projectAttrs(workspace, project)) : match;
  });
  next = next.replace(/\[([^\]\n]+)\]\(<?([^)\s>]+)>?\)/g, (match, _text: string, url: string) => {
    const slug = url.match(/\/document\/([^/?#]+)/)?.[1];
    const document = slug ? (data.documents ?? []).find((item) => item.slugId === decodeURIComponent(slug) || item.id === slug) : undefined;
    return document ? shortcode(documentAttrs(workspace, document)) : match;
  });
  const people = data.users
    .filter((user) => user.active !== false && !user.app && (user.displayName || user.name))
    .sort((a, b) => (b.displayName || b.name).length - (a.displayName || a.name).length);
  for (const user of people) {
    const name = user.displayName || user.name;
    next = next.replace(new RegExp(`(^|[\\s(])@${escapeRegExp(name)}(?![\\w-])`, "g"), (_match, lead: string) => `${lead}${shortcode({ mentionType: "user", id: user.id, label: name })}`);
  }
  return next;
}

function issueAttrs(workspace: string, issue: { id: string; identifier: string; title: string }): LoopEntityAttrs {
  return { mentionType: "issue", id: issue.id, label: issue.identifier, title: issue.title, href: issuePath(workspace, issue) };
}
function projectAttrs(workspace: string, project: { id: string; name: string; slugId: string }): LoopEntityAttrs {
  return { mentionType: "project", id: project.id, label: project.name, href: projectPath(workspace, project) };
}
function documentAttrs(workspace: string, document: { id: string; title: string; slugId: string }): LoopEntityAttrs {
  return { mentionType: "document", id: document.id, label: document.title, href: documentPath(workspace, document) };
}

/** Chip for an entity reference; live data wins over the stored label. */
export function LoopEntityChip({ attrs, data, link, onNavigate }: { attrs: LoopEntityAttrs; data?: BootstrapData; link?: boolean; onNavigate?: (path: string) => void }) {
  let icon: ReactNode = <FileText size={14} />;
  let identifier: string | undefined;
  let title = attrs.label;
  if (attrs.mentionType === "issue") {
    const issue = data?.issues.find((item) => item.id === attrs.id);
    identifier = issue?.identifier ?? attrs.label;
    title = issue?.title ?? attrs.title ?? "";
    icon = issue ? <StatusIcon state={issue.state} size={14} /> : <StatusIcon state={{ id: "unknown", name: "", color: "var(--theme-text-tertiary)", type: "unstarted" }} size={14} />;
  } else if (attrs.mentionType === "project") {
    const project = data?.projects.find((item) => item.id === attrs.id);
    title = project?.name ?? attrs.label;
    icon = <ProjectIcon size={14} style={{ color: project?.color }} />;
  } else if (attrs.mentionType === "user") {
    const user = data?.users.find((item) => item.id === attrs.id);
    title = user ? user.displayName || user.name : attrs.label;
    icon = <UserAvatar avatarUrl={user?.avatarUrl} className="avatar loops-avatar" name={title} />;
  } else {
    const document = data?.documents?.find((item) => item.id === attrs.id);
    title = document?.title ?? attrs.label;
  }
  const content = (
    <>
      <span className="loops-entity-icon">{icon}</span>
      {identifier && <span className="loops-entity-identifier">{identifier}</span>}
      {title && <span className="loops-entity-title">{title}</span>}
    </>
  );
  if (link && attrs.href)
    return (
      <a
        className="loops-entity-chip"
        contentEditable={false}
        data-entity-kind={attrs.mentionType}
        data-i18n-ignore
        href={attrs.href}
        onClick={(event) => {
          if (!onNavigate || event.metaKey || event.ctrlKey || event.shiftKey) return;
          event.preventDefault();
          onNavigate(attrs.href!);
        }}
      >
        {content}
      </a>
    );
  return (
    <span className="loops-entity-chip" contentEditable={false} data-entity-kind={attrs.mentionType} data-i18n-ignore>
      {content}
    </span>
  );
}

function LoopEntityChipView({ node, editor }: ReactNodeViewProps) {
  const { data, onNavigate } = useContext(LoopEntityContext);
  return (
    <NodeViewWrapper as="span" className="loops-entity-wrap">
      <LoopEntityChip attrs={node.attrs as LoopEntityAttrs} data={data} link={!editor.isEditable} onNavigate={onNavigate} />
    </NodeViewWrapper>
  );
}

type MentionState = { query: string; from: number; to: number; left: number; top: number };
type MentionOption = LoopEntityAttrs & { key: string; group: string; icon: ReactNode; detail?: string };
const GROUP_LIMIT = 5;

function useMentionOptions(data: BootstrapData, query: string | undefined): MentionOption[] {
  const candidates = useIssueCandidates(data, query !== undefined);
  return useMemo(() => {
    if (query === undefined) return [];
    const workspace = data.workspace.urlKey;
    const needle = query.toLocaleLowerCase();
    const matches = (text: string) => !needle || text.toLocaleLowerCase().includes(needle);
    const users = data.users
      .filter((user) => user.active !== false && !user.app && matches(`${user.displayName} ${user.name} ${user.email ?? ""}`))
      .slice(0, GROUP_LIMIT)
      .map((user): MentionOption => {
        const name = user.displayName || user.name;
        return { key: `user:${user.id}`, mentionType: "user", id: user.id, label: name, group: "Users", icon: <UserAvatar avatarUrl={user.avatarUrl} className="avatar loops-avatar" name={name} /> };
      });
    const issues = candidates
      .filter((issue) => matches(`${issue.identifier} ${issue.title}`))
      .slice(0, GROUP_LIMIT)
      .map((issue): MentionOption => ({ key: `issue:${issue.id}`, ...issueAttrs(workspace, issue), group: "Issues", detail: issue.title, icon: <StatusIcon state={issue.state} size={14} /> }));
    const projects = data.projects
      .filter((project) => !project.archivedAt && matches(project.name))
      .slice(0, GROUP_LIMIT)
      .map((project): MentionOption => ({ key: `project:${project.id}`, ...projectAttrs(workspace, project), group: "Projects", icon: <ProjectIcon size={14} style={{ color: project.color }} /> }));
    const documents = (data.documents ?? [])
      .filter((document) => matches(document.title))
      .slice(0, GROUP_LIMIT)
      .map((document): MentionOption => ({ key: `document:${document.id}`, ...documentAttrs(workspace, document), group: "Documents", icon: <FileText size={14} /> }));
    return [...users, ...issues, ...projects, ...documents];
  }, [candidates, data.documents, data.projects, data.users, data.workspace.urlKey, query]);
}

function initialContent(value: string, valueData: Record<string, unknown> | undefined, data?: BootstrapData) {
  if (valueData && valueData.type === "doc") return { content: valueData, contentType: "json" as const };
  return { content: instructionsEditorMarkdown(value, data) || "", contentType: "markdown" as const };
}

/**
 * Loop instructions in the rich editor used across Flow: lists, bold, and "@" references to people, issues, projects
 * and documents rendered as chips. `onChange` reports the markdown the model reads plus the editor document.
 */
export function LoopInstructionsEditor({
  data,
  value,
  valueData,
  placeholder,
  readOnly = false,
  ariaLabel,
  className,
  onChange,
  onNavigate,
}: {
  data: BootstrapData;
  value: string;
  valueData?: Record<string, unknown>;
  placeholder?: string;
  readOnly?: boolean;
  ariaLabel: string;
  className?: string;
  onChange?: (markdown: string, document: Record<string, unknown>) => void;
  onNavigate?: (path: string) => void;
}) {
  const { t } = useI18n();
  const rootRef = useRef<HTMLDivElement>(null);
  const [mention, setMention] = useState<MentionState>();
  const [activeIndex, setActiveIndex] = useState(0);
  const options = useMentionOptions(data, mention?.query);
  const live = useRef({ mention, options, activeIndex, placeholder, onChange });
  live.current = { mention, options, activeIndex, placeholder, onChange };
  const initial = useMemo(() => initialContent(value, valueData, data), []); // eslint-disable-line react-hooks/exhaustive-deps

  const insert = (editor: Editor, option: MentionOption) => {
    const range = live.current.mention;
    if (!range) return;
    const { mentionType, id, label, title, href } = option;
    editor
      .chain()
      .focus()
      .insertContentAt({ from: range.from, to: range.to }, [
        { type: LOOP_ENTITY_NODE, attrs: { id, label, title: title ?? "", href: href ?? "", mentionType } },
        { type: "text", text: " " },
      ])
      .run();
    setMention(undefined);
  };

  const detect = (editor: Editor) => {
    const { $from, empty } = editor.state.selection;
    const match = empty && editor.isEditable && $from.parent.type.name !== "codeBlock" ? $from.parent.textBetween(0, $from.parentOffset, " ", " ").match(/(?:^|\s)@([^\s@]{0,40})$/) : null;
    if (!match) {
      setMention(undefined);
      return;
    }
    const from = $from.pos - match[1].length - 1;
    const root = rootRef.current?.getBoundingClientRect();
    let left = 0;
    let top = 24;
    try {
      const coords = editor.view.coordsAtPos(from);
      if (root) {
        left = Math.max(0, coords.left - root.left);
        top = coords.bottom - root.top + 4;
      }
    } catch {
      /* layout unavailable (tests) */
    }
    setMention((current) => {
      if (current?.query !== match[1]) setActiveIndex(0);
      return { query: match[1], from, to: $from.pos, left, top };
    });
  };

  const editor = useEditor({
    immediatelyRender: false,
    editable: !readOnly,
    extensions: [
      StarterKit.configure({ heading: { levels: [1, 2, 3] }, link: { openOnClick: false, autolink: true } }),
      Markdown,
      LoopEntityNode,
      Placeholder.configure({ placeholder: () => live.current.placeholder ?? "" }),
    ],
    content: initial.content,
    contentType: initial.contentType,
    editorProps: {
      attributes: {
        class: "loops-instructions-prosemirror",
        role: readOnly ? "document" : "textbox",
        "aria-label": ariaLabel,
        ...(readOnly ? {} : { "aria-multiline": "true" }),
      },
      handleKeyDown: (_view, event) => {
        const current = live.current;
        if (!current.mention || event.isComposing) return false;
        if (event.key === "Escape") {
          setMention(undefined);
          return true;
        }
        if (!current.options.length) return false;
        if (event.key === "ArrowDown" || event.key === "ArrowUp") {
          event.preventDefault();
          const step = event.key === "ArrowDown" ? 1 : -1;
          setActiveIndex((current.activeIndex + step + current.options.length) % current.options.length);
          return true;
        }
        if ((event.key === "Enter" || event.key === "Tab") && editorRef.current) {
          event.preventDefault();
          insert(editorRef.current, current.options[current.activeIndex] ?? current.options[0]);
          return true;
        }
        return false;
      },
    },
    onUpdate: ({ editor: next }) => {
      live.current.onChange?.(next.getMarkdown(), next.getJSON() as Record<string, unknown>);
      detect(next);
    },
    onSelectionUpdate: ({ editor: next }) => detect(next),
    onBlur: () => window.setTimeout(() => setMention(undefined), 120),
  });
  const editorRef = useRef<Editor | null>(null);
  editorRef.current = editor;

  // The placeholder follows the trigger; repaint it when it changes.
  useEffect(() => {
    if (editor && !editor.isDestroyed) editor.view.dispatch(editor.state.tr.setMeta("loopPlaceholder", placeholder));
  }, [editor, placeholder]);

  // Read-only views follow the saved loop.
  useEffect(() => {
    if (!readOnly || !editor || editor.isDestroyed) return;
    const next = initialContent(value, valueData, data);
    editor.commands.setContent(next.content, { contentType: next.contentType });
  }, [data, editor, readOnly, value, valueData]);

  let lastGroup = "";
  const context = useMemo(() => ({ data, onNavigate }), [data, onNavigate]);
  return (
    <LoopEntityContext.Provider value={context}>
      <div ref={rootRef} className={`loops-instructions-editor${readOnly ? " is-readonly" : ""}${className ? ` ${className}` : ""}`}>
        {editor ? (
          <EditorContent editor={editor} />
        ) : (
          <div className="loops-instructions-prosemirror" role={readOnly ? "document" : "textbox"} aria-label={ariaLabel}>
            {value}
          </div>
        )}
        {mention && editor && (
          <div className="loops-mention-menu" role="listbox" aria-label={t("Mention")} style={{ left: mention.left, top: mention.top }} onMouseDown={(event) => event.preventDefault()}>
            {options.length ? (
              options.map((option, index) => {
                const heading = option.group !== lastGroup ? option.group : "";
                lastGroup = option.group;
                return (
                  <div key={option.key}>
                    {heading && <div className="loops-mention-group">{t(heading)}</div>}
                    <button type="button" role="option" aria-selected={index === activeIndex} className="loops-mention-option" onMouseMove={() => setActiveIndex(index)} onClick={() => insert(editor, option)}>
                      <span className="loops-entity-icon">{option.icon}</span>
                      {option.detail ? (
                        <>
                          <span className="loops-entity-identifier" data-i18n-ignore>
                            {option.label}
                          </span>
                          <span className="loops-entity-title" data-i18n-ignore>
                            {option.detail}
                          </span>
                        </>
                      ) : (
                        <span className="loops-entity-title" data-i18n-ignore>
                          {option.label}
                        </span>
                      )}
                    </button>
                  </div>
                );
              })
            ) : (
              <div className="loops-mention-empty">{t("No results")}</div>
            )}
          </div>
        )}
      </div>
    </LoopEntityContext.Provider>
  );
}
