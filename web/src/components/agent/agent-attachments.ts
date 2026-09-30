import type { AgentFileAttachment } from "@/types/flow";

/** Files attached to an agent composer (Agent page, loop run replies). */

export const AGENT_ATTACHMENT_ACCEPT =
  "image/*,video/*,text/*,application/json,application/xml,application/javascript,application/x-yaml,application/yaml,application/pdf,application/rtf,application/vnd.oasis.opendocument.text,application/msword,application/vnd.apple.keynote,application/vnd.apple.pages,application/vnd.ms-powerpoint,application/vnd.ms-excel,application/vnd.openxmlformats-officedocument.wordprocessingml.document,application/vnd.openxmlformats-officedocument.presentationml.presentation,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet,.md,.markdown,.mdx,.csv";
export const AGENT_ATTACHMENT_MAX_BYTES = 2 * 1024 * 1024;
export const AGENT_ATTACHMENT_MAX_FILES = 8;

/** Adds picked files to the composer's list: files over 2 MB are skipped and at most 8 are kept. */
export function addAgentAttachments(current: File[], picked: FileList | File[] | null | undefined) {
  const files = [...(picked ?? [])].filter((file) => file.size <= AGENT_ATTACHMENT_MAX_BYTES);
  return [...current, ...files].slice(0, AGENT_ATTACHMENT_MAX_FILES);
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
