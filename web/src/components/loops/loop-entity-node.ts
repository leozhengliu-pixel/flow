import { MentionChipNode } from "@/components/editor/mentions/mention-chip-extension";

/** Same `mention` node as every other rich-text surface; `mentionType` names the resource kind, so the server can list the references for the model. */
export const LOOP_ENTITY_NODE = "mention";
export type LoopEntityKind = string;
export type LoopEntityAttrs = { mentionType: LoopEntityKind; id: string; label: string; title?: string; href?: string };

/**
 * Markdown the model reads: issues as their identifier, people as @name, every other resource as a link.
 * The editor round-trips through `instructionsData`, so the chip identity is never lost.
 */
export function loopEntityMarkdown(attrs: Partial<LoopEntityAttrs>) {
  const label = String(attrs.label ?? "");
  if (attrs.mentionType === "user") return `@${label}`;
  if (attrs.mentionType === "issue") return label;
  return attrs.href ? `[${label}](${attrs.href})` : label;
}

/** The shared mention chip node, exporting the compact markdown the loop server reads. */
export const LoopEntityNode = MentionChipNode.extend({
  renderMarkdown: (node) => loopEntityMarkdown((node.attrs ?? {}) as LoopEntityAttrs),
});
