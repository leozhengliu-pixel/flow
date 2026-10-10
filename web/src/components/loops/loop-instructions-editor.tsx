import { useEffect, useMemo, useRef, useState, type MouseEvent } from "react";
import Placeholder from "@tiptap/extension-placeholder";
import { Markdown } from "@tiptap/markdown";
import { EditorContent, useEditor, type Editor } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import { AgentEntityDataContext, useAgentEntityData } from "@/components/agent/agent-entity-data";
import { MentionLinksExtension } from "@/components/editor/mentions/mention-links-extension";
import { insertMentionOption, type MentionOption } from "@/components/editor/mentions/mention-options";
import { useMentionConversion } from "@/components/editor/mentions/use-mention-conversion";
import { useMentionOptions } from "@/components/editor/mentions/use-mention-options";
import { MentionMenu } from "@/components/issue/editor/mention-menu";
import "@/components/issue/issue-description-editor.css";
import type { BootstrapData } from "@/types/flow";
import { LoopEntityNode } from "./loop-entity-node";

type MentionState = { query: string; from: number; to: number; index: number };

/** The caret's viewport rectangle: the menu floats below it, outside any clipping container. */
function caretAnchor(editor: Editor, position: number) {
  try {
    const caret = editor.view.coordsAtPos(Math.min(position, editor.state.doc.content.size));
    return { left: caret.left, top: caret.top, bottom: caret.bottom };
  } catch {
    return { left: 8, top: 0, bottom: 24 };
  }
}

function initialContent(value: string, valueData: Record<string, unknown> | undefined) {
  if (valueData && valueData.type === "doc") return { content: valueData, contentType: "json" as const };
  return { content: value || "", contentType: "markdown" as const };
}

/**
 * Loop instructions in the rich editor used across Flow: lists, bold, and "@" references to every kind of resource
 * (people, issues, projects, documents, ...) rendered as the shared mention chips. `onChange` reports the markdown the
 * model reads plus the editor document.
 */
export function LoopInstructionsEditor(props: {
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
  return (
    <AgentEntityDataContext.Provider value={props.data}>
      <LoopInstructionsEditorBody {...props} />
    </AgentEntityDataContext.Provider>
  );
}

function LoopInstructionsEditorBody({
  data,
  value,
  valueData,
  placeholder,
  readOnly = false,
  ariaLabel,
  className,
  onChange,
  onNavigate,
}: Parameters<typeof LoopInstructionsEditor>[0]) {
  const rootRef = useRef<HTMLDivElement>(null);
  const [mention, setMention] = useState<MentionState>();
  const mentionRef = useRef<MentionState | undefined>(undefined);
  mentionRef.current = mention;
  const entityData = useAgentEntityData();
  const entityDataRef = useRef(entityData);
  entityDataRef.current = entityData;
  const users = useMemo(() => data.users.filter((user) => user.active !== false && !user.app), [data.users]);
  const options = useMentionOptions({ active: Boolean(mention), query: mention?.query ?? "", users });
  const optionsRef = useRef<MentionOption[]>([]);
  optionsRef.current = options;
  const live = useRef({ placeholder, onChange });
  live.current = { placeholder, onChange };
  const initial = useMemo(() => initialContent(value, valueData), []); // eslint-disable-line react-hooks/exhaustive-deps

  const close = () => {
    mentionRef.current = undefined;
    setMention(undefined);
  };

  const detect = (editor: Editor) => {
    const { $from, empty } = editor.state.selection;
    const match = empty && editor.isEditable && $from.parent.type.name !== "codeBlock" ? $from.parent.textBetween(0, $from.parentOffset, " ", " ").match(/(?:^|\s)@([^\s@]{0,40})$/) : null;
    if (!match) {
      if (mentionRef.current) close();
      return;
    }
    const from = $from.pos - match[1].length - 1;
    const previous = mentionRef.current;
    const next = { query: match[1], from, to: $from.pos, index: previous?.query === match[1] ? previous.index : 0 };
    mentionRef.current = next;
    setMention(next);
  };

  const pick = (editor: Editor, option: MentionOption) => {
    const range = mentionRef.current;
    if (!range) return;
    insertMentionOption(editor.view, range, option);
    editor.commands.focus();
    close();
  };

  const editorRef = useRef<Editor | null>(null);
  const editor = useEditor({
    immediatelyRender: false,
    editable: !readOnly,
    extensions: [
      StarterKit.configure({ heading: { levels: [1, 2, 3, 4] }, link: { openOnClick: false, autolink: true } }),
      Markdown,
      LoopEntityNode,
      MentionLinksExtension.configure({ getData: () => entityDataRef.current }),
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
        const current = mentionRef.current;
        if (!current || event.isComposing) return false;
        if (event.key === "Escape") {
          event.preventDefault();
          close();
          return true;
        }
        const list = optionsRef.current;
        if (!list.length) return false;
        if (event.key === "ArrowDown" || event.key === "ArrowUp") {
          event.preventDefault();
          const next = { ...current, index: (current.index + (event.key === "ArrowDown" ? 1 : -1) + list.length) % list.length };
          mentionRef.current = next;
          setMention(next);
          return true;
        }
        if ((event.key === "Enter" || event.key === "Tab") && editorRef.current) {
          event.preventDefault();
          pick(editorRef.current, list[current.index] ?? list[0]);
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
    onBlur: close,
  });
  editorRef.current = editor;

  // The placeholder follows the trigger; repaint it when it changes.
  useEffect(() => {
    if (editor && !editor.isDestroyed) editor.view.dispatch(editor.state.tr.setMeta("loopPlaceholder", placeholder));
  }, [editor, placeholder]);

  // Read-only views follow the saved loop.
  useEffect(() => {
    if (!readOnly || !editor || editor.isDestroyed) return;
    const next = initialContent(value, valueData);
    editor.commands.setContent(next.content, { contentType: next.contentType, emitUpdate: false });
  }, [editor, readOnly, value, valueData]);

  // Markdown-only instructions (older loops, or what the loop builder wrote): issue identifiers, links to Flow
  // resources and @people show as chips (display only, never an edit).
  useMentionConversion(editor, readOnly ? `${value}\n${JSON.stringify(valueData ?? "")}` : "initial", true, !valueData);

  // Chips are links; with `onNavigate` the page decides how to move (modified clicks still open a new tab).
  const navigate = (event: MouseEvent<HTMLDivElement>) => {
    if (!onNavigate || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    const anchor = (event.target as Element).closest?.("a[data-agent-entity]");
    const href = anchor?.getAttribute("href");
    if (!href || !event.currentTarget.contains(anchor)) return;
    event.preventDefault();
    event.stopPropagation();
    onNavigate(href);
  };

  return (
    <div ref={rootRef} className={`loops-instructions-editor${readOnly ? " is-readonly" : ""}${className ? ` ${className}` : ""}`} onClickCapture={navigate}>
      {editor ? (
        <EditorContent editor={editor} />
      ) : (
        <div className="loops-instructions-prosemirror" role={readOnly ? "document" : "textbox"} aria-label={ariaLabel}>
          {value}
        </div>
      )}
      {mention && editor && <MentionMenu options={options} selectedIndex={mention.index} anchor={caretAnchor(editor, mention.to)} query={mention.query} onSelect={(option) => pick(editor, option)} />}
    </div>
  );
}
