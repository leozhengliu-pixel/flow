import type { ReactNode } from "react";

/** Fills `{name}` placeholders with text or elements, keeping the translated sentence's word order. */
export function fillNodes(template: string, values: Record<string, ReactNode>): ReactNode {
  return template.split(/(\{\w+\})/g).map((part, index) => {
    const key = /^\{(\w+)\}$/.exec(part)?.[1];
    return key ? <span key={index}>{values[key]}</span> : part;
  });
}
