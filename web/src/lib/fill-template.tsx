import { Fragment } from "react";
import type { ReactNode } from "react";

/** Splits "a {x} b" into text and the supplied nodes, so sentences stay translatable as a whole. */
export function fillTemplate(text: string, parts: Record<string, ReactNode>) {
  return text.split(/(\{\w+\})/).map((piece, index) => {
    const key = /^\{(\w+)\}$/.exec(piece)?.[1];
    return <Fragment key={index}>{key && key in parts ? parts[key] : piece}</Fragment>;
  });
}
