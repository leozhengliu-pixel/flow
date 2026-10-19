import { useState } from "react";
import { Lock, Plus, X } from "lucide-react";
import { confirmAction } from "@/components/ui/action-dialog-service";
import { useI18n } from "@/i18n/i18n";
import type { CodingEnvVar, CodingMiseFile, CodingSecretRef } from "@/types/flow";
import { isValidEnvName, type ToolRow } from "./coding-sessions-model";

function RemoveButton({ label, disabled, onClick }: { label: string; disabled?: boolean; onClick: () => void }) {
  return (
    <button type="button" className="cs-icon-button" aria-label={label} disabled={disabled} onClick={onClick}>
      <X size={14} />
    </button>
  );
}

function AddButton({ children, disabled, onClick }: { children: string; disabled?: boolean; onClick: () => void }) {
  const { t } = useI18n();
  return (
    <button type="button" className="cs-text-button cs-add-button" disabled={disabled} onClick={onClick}>
      <Plus size={13} />
      {t(children)}
    </button>
  );
}

/** mise tool versions (`node = "22"`), written to `mise.toml` in the sandbox. */
export function ToolsEditor({ tools, disabled, onChange }: { tools: ToolRow[]; disabled?: boolean; onChange: (tools: ToolRow[]) => void }) {
  const { t } = useI18n();
  const set = (index: number, patch: Partial<ToolRow>) => onChange(tools.map((row, i) => (i === index ? { ...row, ...patch } : row)));
  return (
    <div className="cs-kv-list" data-testid="tools-editor">
      {tools.map((row, index) => (
        <div key={index} className="cs-kv-row">
          <input aria-label={t("Tool")} placeholder="node" value={row.tool} disabled={disabled} onChange={(event) => set(index, { tool: event.target.value })} />
          <input aria-label={t("Version")} placeholder="22" value={row.version} disabled={disabled} onChange={(event) => set(index, { version: event.target.value })} />
          <RemoveButton label={t("Remove tool")} disabled={disabled} onClick={() => onChange(tools.filter((_, i) => i !== index))} />
        </div>
      ))}
      <AddButton disabled={disabled} onClick={() => onChange([...tools, { tool: "", version: "" }])}>Add tool</AddButton>
    </div>
  );
}

/** Files placed in the sandbox home directory before setup (for example `.npmrc`). */
export function FilesEditor({ files, disabled, onChange }: { files: CodingMiseFile[]; disabled?: boolean; onChange: (files: CodingMiseFile[]) => void }) {
  const { t } = useI18n();
  const set = (index: number, patch: Partial<CodingMiseFile>) => onChange(files.map((file, i) => (i === index ? { ...file, ...patch } : file)));
  return (
    <div className="cs-file-list" data-testid="files-editor">
      {files.map((file, index) => (
        <div key={index} className="cs-file">
          <div className="cs-file-header">
            <span className="cs-file-prefix">~/</span>
            <input aria-label={t("File path")} placeholder=".npmrc" value={file.path} disabled={disabled} onChange={(event) => set(index, { path: event.target.value })} />
            <RemoveButton label={t("Remove file")} disabled={disabled} onClick={() => onChange(files.filter((_, i) => i !== index))} />
          </div>
          <textarea aria-label={t("File content")} className="cs-code" spellCheck={false} rows={4} value={file.content} disabled={disabled} onChange={(event) => set(index, { content: event.target.value })} />
        </div>
      ))}
      <AddButton disabled={disabled} onClick={() => onChange([...files, { path: "", content: "" }])}>Add file</AddButton>
    </div>
  );
}

/** Plain environment variables, visible to the agent at runtime. */
export function VariablesEditor({ variables, disabled, onChange }: { variables: CodingEnvVar[]; disabled?: boolean; onChange: (variables: CodingEnvVar[]) => void }) {
  const { t } = useI18n();
  const set = (index: number, patch: Partial<CodingEnvVar>) => onChange(variables.map((variable, i) => (i === index ? { ...variable, ...patch } : variable)));
  return (
    <div className="cs-kv-list" data-testid="variables-editor">
      {variables.map((variable, index) => (
        <div key={index} className="cs-kv-row">
          <input aria-label={t("Variable name")} aria-invalid={variable.name.trim() !== "" && !isValidEnvName(variable.name.trim())} placeholder="NAME" value={variable.name} disabled={disabled} onChange={(event) => set(index, { name: event.target.value })} />
          <input aria-label={t("Variable value")} placeholder={t("Value")} value={variable.value} disabled={disabled} onChange={(event) => set(index, { value: event.target.value })} />
          <RemoveButton label={t("Remove variable")} disabled={disabled} onClick={() => onChange(variables.filter((_, i) => i !== index))} />
        </div>
      ))}
      <AddButton disabled={disabled} onClick={() => onChange([...variables, { name: "", value: "" }])}>Add variable</AddButton>
    </div>
  );
}

const MASK = "••••••••••••";

/**
 * Secrets are encrypted on save, never shown again, and cannot be edited: a saved secret can
 * only be deleted and re-added. New secrets stay local (`pending`) until the environment is saved.
 */
export function SecretsEditor({ saved, pending, disabled, onPendingChange, onDelete }: { saved: CodingSecretRef[]; pending: CodingEnvVar[]; disabled?: boolean; onPendingChange: (pending: CodingEnvVar[]) => void; onDelete?: (name: string) => Promise<void> }) {
  const { t } = useI18n();
  const [deleting, setDeleting] = useState<string>();
  const set = (index: number, patch: Partial<CodingEnvVar>) => onPendingChange(pending.map((secret, i) => (i === index ? { ...secret, ...patch } : secret)));
  return (
    <div className="cs-kv-list" data-testid="secrets-editor">
      {saved.map((secret) => (
        <div key={secret.name} className="cs-kv-row is-saved" data-testid={`secret-${secret.name}`}>
          <input aria-label={t("Secret name")} value={secret.name} readOnly disabled data-i18n-ignore />
          <span className="cs-secret-mask" aria-label={t("Hidden value")}>
            <Lock size={12} aria-hidden="true" />
            {MASK}
          </span>
          {onDelete && (
            <RemoveButton
              label={t("Delete secret {name}").replace("{name}", secret.name)}
              disabled={disabled || deleting === secret.name}
              onClick={async () => {
                const confirmed = await confirmAction(t("Delete secret {name}?").replace("{name}", secret.name), {
                  description: t("Sessions and snapshots created after this will not receive it. This cannot be undone."),
                  confirmLabel: t("Delete"),
                });
                if (!confirmed) return;
                setDeleting(secret.name);
                try {
                  await onDelete(secret.name);
                } finally {
                  setDeleting(undefined);
                }
              }}
            />
          )}
        </div>
      ))}
      {pending.map((secret, index) => (
        <div key={`pending-${index}`} className="cs-kv-row is-pending">
          <input aria-label={t("Secret name")} placeholder="NAME" value={secret.name} disabled={disabled} onChange={(event) => set(index, { name: event.target.value })} />
          <input aria-label={t("Secret value")} type="password" autoComplete="new-password" placeholder={t("Value")} value={secret.value} disabled={disabled} onChange={(event) => set(index, { value: event.target.value })} />
          <RemoveButton label={t("Remove secret")} disabled={disabled} onClick={() => onPendingChange(pending.filter((_, i) => i !== index))} />
        </div>
      ))}
      <AddButton disabled={disabled} onClick={() => onPendingChange([...pending, { name: "", value: "" }])}>Add secret</AddButton>
    </div>
  );
}
