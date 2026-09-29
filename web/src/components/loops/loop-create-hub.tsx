import { useRef, useState } from "react";
import { ArrowRight, ArrowUp, FileText, LoaderCircle, Paperclip } from "lucide-react";
import { toast } from "sonner";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { AttachmentRemoveButton } from "@/components/ui/attachment-remove-button";
import { createLoop, uploadLoopAttachment } from "@/lib/api";
import { newLoopPath } from "@/lib/app-routes";
import { useI18n } from "@/i18n/i18n";
import type { BootstrapData, Loop, LoopAttachment, LoopTemplate } from "@/types/flow";
import { markLoopAgentAutostart, useLoopTemplates } from "./loop-data";
import { LoopGlyph } from "./loop-glyph";
import { LoopLocationPicker, type LoopLocation } from "./loop-pickers";

type Entry = { kind: "scratch" } | { kind: "prompt"; prompt: string; attachmentIds: string[] } | { kind: "template"; template: LoopTemplate };

/** Server limit for prompt attachments (loops-api-contract.md → Attachments). */
const MAX_ATTACHMENT_BYTES = 20 * 1024 * 1024;
const MAX_ATTACHMENTS = 8;
type PendingAttachment = { key: string; name: string; contentType: string; uploaded?: LoopAttachment };

/** "Create a new loop": start from scratch, ask Flow, or pick a template. Every entry asks where the loop lives first. */
export function LoopCreateHub({
  data,
  onNavigate,
  onReload,
  variant = "inline",
  onCreated,
}: {
  data: Pick<BootstrapData, "teams" | "workspace">;
  onNavigate: (path: string) => void;
  onReload?: () => Promise<void>;
  variant?: "inline" | "dialog";
  onCreated?: () => void;
}) {
  const { t } = useI18n();
  const { templates } = useLoopTemplates();
  const [prompt, setPrompt] = useState("");
  const [entry, setEntry] = useState<Entry>();
  const [busy, setBusy] = useState(false);
  const [attachments, setAttachments] = useState<PendingAttachment[]>([]);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const uploading = attachments.some((item) => !item.uploaded);

  const attach = (files: File[]) => {
    const room = MAX_ATTACHMENTS - attachments.length;
    const tooLarge = files.filter((file) => file.size > MAX_ATTACHMENT_BYTES);
    if (tooLarge.length) toast.error(t("Files must be 20 MB or smaller"));
    const accepted = files.filter((file) => file.size <= MAX_ATTACHMENT_BYTES).slice(0, Math.max(0, room));
    for (const file of accepted) {
      const key = `${file.name}-${file.lastModified}-${Math.random().toString(36).slice(2)}`;
      setAttachments((items) => [...items, { key, name: file.name, contentType: file.type }]);
      uploadLoopAttachment(file)
        .then((uploaded) => setAttachments((items) => items.map((item) => (item.key === key ? { ...item, uploaded } : item))))
        .catch((error) => {
          setAttachments((items) => items.filter((item) => item.key !== key));
          toast.error(error instanceof Error ? error.message : t("Could not upload file"));
        });
    }
  };

  const create = async (location: LoopLocation) => {
    if (!entry || busy) return;
    setBusy(true);
    try {
      const loop: Loop = await createLoop({
        level: location.level,
        ...(location.level === "team" ? { teamId: location.teamId } : {}),
        ...(entry.kind === "template" ? { templateId: entry.template.id } : {}),
        ...(entry.kind === "prompt" ? { prompt: entry.prompt, ...(entry.attachmentIds.length ? { attachmentIds: entry.attachmentIds } : {}) } : {}),
        status: "draft",
      });
      if (entry.kind !== "scratch") markLoopAgentAutostart(loop.id);
      setEntry(undefined);
      setPrompt("");
      if (entry.kind === "prompt") setAttachments([]);
      onCreated?.();
      // The editor fetches the draft itself; refresh lists in the background.
      void onReload?.().catch(() => undefined);
      onNavigate(`${newLoopPath(data.workspace.urlKey)}?draftId=${encodeURIComponent(loop.id)}`);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not create loop"));
    } finally {
      setBusy(false);
    }
  };
  const submitPrompt = () => {
    const text = prompt.trim();
    if (text && !uploading) setEntry({ kind: "prompt", prompt: text, attachmentIds: attachments.flatMap((item) => (item.uploaded ? [item.uploaded.id] : [])) });
  };
  const template = entry?.kind === "template" ? entry.template : undefined;

  return (
    <div className={`loops-hub is-${variant}`}>
      <header className="loops-hub-header">
        <div>
          <h2>{t("Create a new loop")}</h2>
          <p>{t("Automate manual work for your team and keep your process moving")}</p>
        </div>
        <button className="loops-secondary-button loops-hub-scratch" type="button" onClick={() => setEntry({ kind: "scratch" })}>
          {t("Start from scratch")}
        </button>
      </header>
      <form
        className="loops-hub-composer"
        onSubmit={(event) => {
          event.preventDefault();
          submitPrompt();
        }}
      >
        <textarea
          aria-label={t("Ask Flow to build the loop for you")}
          placeholder={t("Ask Flow to build the loop for you")}
          rows={2}
          value={prompt}
          onChange={(event) => setPrompt(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
              event.preventDefault();
              submitPrompt();
            }
          }}
        />
        {attachments.length > 0 && (
          <ul className="loops-hub-attachments" aria-label={t("Attachments")}>
            {attachments.map((item) => (
              <li key={item.key} className={item.uploaded ? undefined : "is-uploading"}>
                {item.uploaded ? (
                  item.contentType.startsWith("image/") ? <img alt="" src={item.uploaded.url} /> : <FileText size={13} />
                ) : (
                  <LoaderCircle aria-label={t("Uploading…")} className="loops-hub-attachment-spinner" size={13} />
                )}
                <span data-i18n-ignore>{item.name}</span>
                <AttachmentRemoveButton label={`${t("Remove attachment")} ${item.name}`} onClick={() => setAttachments((items) => items.filter((other) => other.key !== item.key))} />
              </li>
            ))}
          </ul>
        )}
        <footer>
          <button className="loops-hub-attach" type="button" aria-label={t("Attach images, files, or videos")} disabled={attachments.length >= MAX_ATTACHMENTS} onClick={() => fileInputRef.current?.click()}>
            <Paperclip size={14} />
          </button>
          <input
            ref={fileInputRef}
            className="loops-hub-file-input"
            type="file"
            multiple
            accept="image/*,video/*,text/*,application/json,application/xml,application/x-yaml,application/yaml,application/pdf,.md,.markdown,.mdx,.csv"
            tabIndex={-1}
            aria-hidden="true"
            onChange={(event) => {
              attach([...(event.target.files ?? [])]);
              event.target.value = "";
            }}
          />
          <button className="loops-hub-send" type="submit" aria-label={t("Create loop from prompt")} title={uploading ? t("Waiting for uploads to finish") : undefined} disabled={!prompt.trim() || busy || uploading}>
            <ArrowUp size={14} />
          </button>
        </footer>
      </form>
      {templates?.length !== 0 && <div className="loops-hub-templates">
        <h3>{t("or pick a template")}</h3>
        <div className="loops-template-grid">
          {templates === undefined
            ? Array.from({ length: 4 }, (_, index) => <div aria-hidden="true" className="loops-template-card is-loading" key={index} />)
            : templates.map((item) => (
                <button className="loops-template-card" key={item.id} type="button" onClick={() => setEntry({ kind: "template", template: item })}>
                  <span className="loops-template-icon" style={{ color: item.color || undefined }}>
                    <LoopGlyph icon={item.icon} />
                  </span>
                  <strong data-i18n-ignore>{item.name}</strong>
                  <span className="loops-template-description" data-i18n-ignore>
                    {item.description}
                  </span>
                  <span className="loops-template-footer">
                    <span className="loops-template-trigger">
                      <LoopGlyph icon={item.triggerIcon || item.triggerLabel} size={14} />
                      <span>{t(item.triggerLabel)}</span>
                    </span>
                    <ArrowRight aria-hidden="true" size={12} />
                    <span className="loops-template-action">
                      <LoopGlyph icon={item.actionIcon || item.actionLabel} size={14} />
                      <span data-i18n-ignore>{item.actionLabel}</span>
                    </span>
                  </span>
                </button>
              ))}
        </div>
      </div>}
      <LoopLocationPicker
        data={data}
        open={Boolean(entry) && !busy}
        requiresTeam={template?.requiresTeam}
        levelHint={template?.levelHint}
        onOpenChange={(open) => !open && setEntry(undefined)}
        onSelect={(location) => void create(location)}
      />
    </div>
  );
}

/** "+ New loop" opens the same hub as a dialog. */
export function LoopCreateDialog({
  data,
  open,
  onOpenChange,
  onNavigate,
  onReload,
}: {
  data: Pick<BootstrapData, "teams" | "workspace">;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onNavigate: (path: string) => void;
  onReload?: () => Promise<void>;
}) {
  const { t } = useI18n();
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="loops-hub-dialog" closeLabel={t("Close")}>
        <DialogTitle className="sr-only">{t("Create a new loop")}</DialogTitle>
        <LoopCreateHub data={data} variant="dialog" onNavigate={onNavigate} onReload={onReload} onCreated={() => onOpenChange(false)} />
      </DialogContent>
    </Dialog>
  );
}
