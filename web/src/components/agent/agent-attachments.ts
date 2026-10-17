import type { Dispatch, SetStateAction } from "react";
import type { AgentFileAttachment } from "@/types/flow";
import { toast } from "@/lib/toast";

/** Files attached to an agent composer (Agent page, loop run replies). */

export const AGENT_ATTACHMENT_ACCEPT =
  "image/*,video/*,text/*,application/json,application/xml,application/javascript,application/x-yaml,application/yaml,application/pdf,application/rtf,application/vnd.oasis.opendocument.text,application/msword,application/vnd.apple.keynote,application/vnd.apple.pages,application/vnd.ms-powerpoint,application/vnd.ms-excel,application/vnd.openxmlformats-officedocument.wordprocessingml.document,application/vnd.openxmlformats-officedocument.presentationml.presentation,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet,.md,.markdown,.mdx,.csv";
export const AGENT_ATTACHMENT_MAX_BYTES = 2 * 1024 * 1024;
export const AGENT_ATTACHMENT_MAX_FILES = 8;
// The files travel base64-encoded inside one JSON request, which the server caps at 16 MB.
export const AGENT_ATTACHMENT_TOTAL_BYTES = 10 * 1024 * 1024;

/** Adds picked files to the composer's list: files over 2 MB are skipped, at most 8 are kept, 10 MB in all. */
export function addAgentAttachments(current: File[], picked: FileList | File[] | null | undefined) {
  let total = current.reduce((sum, file) => sum + file.size, 0);
  const files = [...(picked ?? [])].filter((file) => {
    if (file.size > AGENT_ATTACHMENT_MAX_BYTES || total + file.size > AGENT_ATTACHMENT_TOTAL_BYTES) return false;
    total += file.size;
    return true;
  });
  return [...current, ...files].slice(0, AGENT_ATTACHMENT_MAX_FILES);
}

/** What a file becomes in the composer: an inline image, video or file card; audio is ignored. */
export type AgentFileKind = "image" | "video" | "audio" | "file";
export const AGENT_INLINE_IMAGE_TYPES = /^image\/(png|jpeg|gif|webp|svg\+xml)$/;
/** Images (after shrinking) stay under 2 MB; every other uploaded file may be up to 20 MB. */
export const AGENT_UPLOAD_MAX_BYTES = 20 * 1024 * 1024;
export const AGENT_UPLOAD_MAX_ITEMS = 8;

/** Audio is ignored by MIME type alone, like the reference app: a file with an empty type becomes a file card. */
export function agentFileKind(file: Pick<File, "type">): AgentFileKind {
  if (AGENT_INLINE_IMAGE_TYPES.test(file.type)) return "image";
  if (file.type.startsWith("video/")) return "video";
  if (file.type.startsWith("audio/")) return "audio";
  return "file";
}

/** The most bytes an upload of this file may have: 2 MB for png/jpeg/gif/webp images, 20 MB for the rest. */
export function agentUploadLimit(file: Pick<File, "type">) {
  return /^image\/(png|jpeg|gif|webp)$/.test(file.type) ? AGENT_ATTACHMENT_MAX_BYTES : AGENT_UPLOAD_MAX_BYTES;
}

// Models downscale images to about this size anyway, so a larger screenshot loses nothing by shrinking first.
const AGENT_IMAGE_MAX_EDGE = 2048;
const SHRINKABLE_IMAGE = /^image\/(png|jpeg|webp)$/;

/**
 * Files on a paste's clipboard (screenshots, copied images or files); empty for a text paste. Office apps put text,
 * html and a picture of the selection on the clipboard together; that paste is text.
 */
export function pastedAgentFiles(data: DataTransfer | null | undefined) {
  if (!data || (data.getData("text/html") && data.getData("text/plain").trim())) return [];
  return [...(data.files ?? [])];
}

/**
 * Adds picked or pasted files to a composer's list. Large images are scaled down to fit the 2 MB limit; a file still over
 * it is reported, since an attachment that silently does not appear looks like paste or upload not working.
 */
export async function attachAgentFiles(picked: FileList | File[] | null | undefined, t: (text: string) => string, setFiles: Dispatch<SetStateAction<File[]>>) {
  // Copy now: a file input's list is cleared once the host resets its value.
  const files = await Promise.all([...(picked ?? [])].map(shrinkAgentImage));
  if (files.some((file) => file.size > AGENT_ATTACHMENT_MAX_BYTES)) toast.error(t("Files over 2 MB can't be attached"));
  setFiles((items) => {
    const next = addAgentAttachments(items, files);
    const fitting = files.filter((file) => file.size <= AGENT_ATTACHMENT_MAX_BYTES).length;
    if (next.length - items.length < fitting) queueMicrotask(() => toast.error(t("A message can carry up to 8 files, 10 MB in all"), { id: "agent-attachment-limit" }));
    return next;
  });
}

/** A PNG, JPEG or WebP image scaled to at most 2048 px on its long edge and re-encoded until it fits the limit. */
export async function shrinkAgentImage(file: File): Promise<File> {
  if (!SHRINKABLE_IMAGE.test(file.type)) return file;
  let bitmap: ImageBitmap;
  try {
    bitmap = await createImageBitmap(file);
  } catch {
    return file;
  }
  try {
    const scale = Math.min(1, AGENT_IMAGE_MAX_EDGE / Math.max(bitmap.width, bitmap.height));
    if (scale === 1 && file.size <= AGENT_ATTACHMENT_MAX_BYTES) return file;
    const canvas = document.createElement("canvas");
    canvas.width = Math.max(1, Math.round(bitmap.width * scale));
    canvas.height = Math.max(1, Math.round(bitmap.height * scale));
    const context = canvas.getContext("2d");
    if (!context) return file;
    context.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
    // Screenshots stay sharpest as PNG; photos and dense captures fall back to JPEG.
    for (const [type, quality] of [["image/png", undefined], ["image/jpeg", 0.85], ["image/jpeg", 0.7]] as const) {
      const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, type, quality));
      if (blob && blob.size <= AGENT_ATTACHMENT_MAX_BYTES) {
        const name = type === file.type ? file.name : file.name.replace(/\.[^.]+$/, "") + (type === "image/png" ? ".png" : ".jpg");
        return new File([blob], name, { type, lastModified: file.lastModified });
      }
    }
    return file;
  } finally {
    bitmap.close();
  }
}

function isAgentTextFile(file: File) {
  return file.type.startsWith("text/") || /\.(md|markdown|mdx|csv|json|xml|ya?ml)$/i.test(file.name);
}

/** The text the agent reads for an attached file. */
export async function agentFileContext(file: File) {
  if (isAgentTextFile(file)) return `Attached file ${file.name}:\n${await file.text()}`;
  if (file.type.startsWith("image/")) return `Attached image ${file.name} (${file.type}, ${file.size} bytes): ${await fileDataURL(file)}`;
  return `Attached file ${file.name} (${file.type || "application/octet-stream"}, ${file.size} bytes).`;
}

export async function agentFileAttachment(file: File): Promise<AgentFileAttachment> {
  const base = { name: file.name, contentType: file.type || "application/octet-stream", size: file.size };
  if (isAgentTextFile(file)) return { ...base, content: await file.text() };
  if (file.type.startsWith("image/")) return { ...base, content: await fileDataURL(file) };
  return base;
}

function fileDataURL(file: File) {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result ?? ""));
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(file);
  });
}
