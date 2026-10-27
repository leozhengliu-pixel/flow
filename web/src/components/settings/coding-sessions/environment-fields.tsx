import { useResetOnChange } from '@/hooks/use-reset-on-change'
import { useId, useState } from "react";
import { ClipboardPaste, Pencil, Plus, Trash2, X } from "lucide-react";
import { confirmAction } from "@/components/ui/action-dialog-service";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { UserAvatar } from "@/components/ui/user-avatar";
import { useI18n } from "@/i18n/i18n";
import type { CodingMiseFile, CodingSecretRef, User } from "@/types/flow";
import { TOOL_PRESETS, isValidEnvName, toolLabel, toolVersions, type ToolRow } from "./coding-sessions-model";
import { CodingButton, CodingCodeEditor, CodingSelect } from "./coding-settings-primitives";

function TrashButton({ label, disabled, onClick }: { label: string; disabled?: boolean; onClick: () => void }) {
  return (
    <button type="button" className="cs-icon-button cs-trash-button" aria-label={label} title={label} disabled={disabled} onClick={onClick}>
      <Trash2 size={14} />
    </button>
  );
}

/**
 * Tool versions: the reference app's "Add tool…" select (160x24) over the preset list; each added
 * tool is a bordered 44px box with its version select and a trash button. Written to `mise.toml`.
 */
export function ToolsEditor({ tools, disabled, onChange }: { tools: ToolRow[]; disabled?: boolean; onChange: (tools: ToolRow[]) => void }) {
  const { t } = useI18n();
  const added = new Set(tools.map((row) => row.tool.trim()));
  const available = TOOL_PRESETS.filter((preset) => !added.has(preset.tool));
  const set = (index: number, patch: Partial<ToolRow>) => onChange(tools.map((row, i) => (i === index ? { ...row, ...patch } : row)));
  return (
    <div className="cs-tools" data-testid="tools-editor">
      {tools.map((row, index) => {
        const label = toolLabel(row.tool);
        return (
          <div key={`${row.tool}-${index}`} className="cs-tool-box" data-testid={`tool-${row.tool}`}>
            <strong data-i18n-ignore>{label}</strong>
            <CodingSelect
              small
              label={t("{tool} version").replace("{tool}", label)}
              value={row.version.trim() || "latest"}
              disabled={disabled}
              options={toolVersions(row.tool, row.version).map((version) => ({ value: version, label: version, translate: false }))}
              onChange={(version) => set(index, { version })}
            />
            <TrashButton label={t("Remove {tool}").replace("{tool}", label)} disabled={disabled} onClick={() => onChange(tools.filter((_, i) => i !== index))} />
          </div>
        );
      })}
      <CodingSelect
        small
        label="Add tool"
        placeholder="Add tool…"
        value=""
        disabled={disabled || !available.length}
        options={available.map((preset) => ({ value: preset.tool, label: preset.label, translate: false }))}
        onChange={(tool) => onChange([...tools, { tool, version: "latest" }])}
      />
    </div>
  );
}

/** "Add file" / "Rename file": a 480px modal with one path field; Enter submits. */
function FilePathDialog({ open, title, confirmLabel, initial, taken, onClose, onSubmit }: { open: boolean; title: string; confirmLabel: string; initial: string; taken: string[]; onClose: () => void; onSubmit: (path: string) => void }) {
  const { t } = useI18n();
  const descriptionId = useId();
  const [path, setPath] = useState(initial);
  useResetOnChange([open, initial], () => { if (open) setPath(initial); });
  const trimmed = path.trim();
  const invalid = !trimmed || (trimmed !== initial && taken.includes(trimmed));
  const submit = () => {
    if (invalid) return;
    onSubmit(trimmed);
    onClose();
  };
  return (
    <Dialog open={open} onOpenChange={(value) => !value && onClose()}>
      <DialogContent className="feature-dialog cs-file-dialog" aria-describedby={descriptionId} closeLabel={t("Close")}>
        <div className="cs-dialog-heading">
          <DialogTitle>{t(title)}</DialogTitle>
          <p id={descriptionId}>{t("Add files to the sandbox’s home directory")}</p>
        </div>
        <input
          autoFocus
          aria-label={t("File path")}
          className="cs-input cs-dialog-input"
          placeholder=".npmrc"
          spellCheck={false}
          value={path}
          onChange={(event) => setPath(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.preventDefault();
              submit();
            }
          }}
        />
        <footer className="cs-modal-footer">
          <CodingButton onClick={onClose}>Cancel</CodingButton>
          <CodingButton primary disabled={invalid} onClick={submit}>{confirmLabel}</CodingButton>
        </footer>
      </DialogContent>
    </Dialog>
  );
}

/**
 * Files placed in the sandbox home directory before setup (for example `.npmrc`): a toolbar with
 * one pill tab per file, rename/remove for the selected file and "Add file", over a code editor.
 */
export function FilesEditor({ files, disabled, onChange }: { files: CodingMiseFile[]; disabled?: boolean; onChange: (files: CodingMiseFile[]) => void }) {
  const { t } = useI18n();
  const [selected, setSelected] = useState(0);
  const [dialog, setDialog] = useState<"add" | "rename">();
  const index = Math.min(selected, files.length - 1);
  const current = files[index];
  const paths = files.map((file) => file.path.trim());
  return (
    <div className="feature-card cs-files" data-testid="files-editor">
      <div className="cs-files-toolbar">
        <div className="cs-file-tabs" role="tablist" aria-label={t("Files")}>
          {files.map((file, i) => (
            <button key={i} type="button" role="tab" aria-selected={i === index} className="cs-file-tab" data-i18n-ignore onClick={() => setSelected(i)}>
              {file.path || "untitled"}
            </button>
          ))}
        </div>
        <div className="cs-files-actions">
          {current && (
            <>
              <button type="button" className="cs-icon-button" aria-label={t("Rename file")} title={t("Rename file")} disabled={disabled} onClick={() => setDialog("rename")}>
                <Pencil size={14} />
              </button>
              <TrashButton
                label={t("Remove file")}
                disabled={disabled}
                onClick={() => {
                  onChange(files.filter((_, i) => i !== index));
                  setSelected(Math.max(0, index - 1));
                }}
              />
            </>
          )}
          <CodingButton ghost disabled={disabled} onClick={() => setDialog("add")}>
            <Plus size={14} />
            Add file
          </CodingButton>
        </div>
      </div>
      {current ? (
        <CodingCodeEditor
          aria-label={t("File content")}
          placeholder={t("File contents")}
          value={current.content}
          disabled={disabled}
          className="is-flush"
          onChange={(content) => onChange(files.map((file, i) => (i === index ? { ...file, content } : file)))}
        />
      ) : (
        <p className="cs-card-text cs-files-empty">{t("No files added. Add a file to set its path and contents.")}</p>
      )}
      <FilePathDialog
        open={dialog !== undefined}
        title={dialog === "rename" ? "Rename file" : "Add file"}
        confirmLabel={dialog === "rename" ? "Rename" : "Add file"}
        initial={dialog === "rename" ? current?.path ?? "" : ""}
        taken={paths}
        onClose={() => setDialog(undefined)}
        onSubmit={(path) => {
          if (dialog === "rename") onChange(files.map((file, i) => (i === index ? { ...file, path } : file)));
          else {
            onChange([...files, { path, content: "" }]);
            setSelected(files.length);
          }
        }}
      />
    </div>
  );
}

/** One unsaved environment variable row; `secret` rows are sent through the secrets API on save. */
export type EnvRow = { name: string; value: string; secret: boolean };

const MASK = "••••••••";

/**
 * Environment variables and secrets in one card, like the reference app: Name / Value / Secret
 * columns, saved secrets masked and read-only (delete only), and a footer with "Import from .env"
 * and "Add variable". Checking Secret on a new row makes it a pending secret.
 */
export function EnvVarsEditor({ rows, saved, disabled, onChange, onDeleteSecret, onImport }: { rows: EnvRow[]; saved: CodingSecretRef[]; disabled?: boolean; onChange: (rows: EnvRow[]) => void; onDeleteSecret?: (name: string) => Promise<void>; onImport: () => void }) {
  const { t } = useI18n();
  const [deleting, setDeleting] = useState<string>();
  const set = (index: number, patch: Partial<EnvRow>) => onChange(rows.map((row, i) => (i === index ? { ...row, ...patch } : row)));
  const empty = !rows.length && !saved.length;
  return (
    <div className={`feature-card cs-env-vars${empty ? " is-empty" : ""}`} data-testid="variables-editor">
      {!empty && (
        <div className="cs-env-grid">
          <div className="cs-env-head" aria-hidden="true">
            <span>{t("Name")}</span>
            <span>{t("Value")}</span>
            <span>{t("Secret")}</span>
            <span />
          </div>
          {saved.map((secret) => (
            <div key={secret.name} className="cs-env-row is-saved" data-testid={`secret-${secret.name}`}>
              <input className="cs-input cs-mono" aria-label={t("Secret name")} value={secret.name} readOnly disabled data-i18n-ignore />
              <span className="cs-input cs-secret-mask" aria-label={t("Hidden value")}>{MASK}</span>
              <input type="checkbox" className="cs-checkbox" aria-label={t("Secret")} checked disabled readOnly />
              {onDeleteSecret ? (
                <TrashButton
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
                      await onDeleteSecret(secret.name);
                    } finally {
                      setDeleting(undefined);
                    }
                  }}
                />
              ) : <span />}
            </div>
          ))}
          {rows.map((row, index) => (
            <div key={`row-${index}`} className={`cs-env-row${row.secret ? " is-secret" : ""}`}>
              <input
                className="cs-input cs-mono"
                aria-label={t(row.secret ? "Secret name" : "Variable name")}
                aria-invalid={row.name.trim() !== "" && !isValidEnvName(row.name.trim())}
                placeholder="VARIABLE_NAME"
                spellCheck={false}
                value={row.name}
                disabled={disabled}
                onChange={(event) => set(index, { name: event.target.value })}
              />
              <input
                className="cs-input"
                aria-label={t(row.secret ? "Secret value" : "Variable value")}
                type={row.secret ? "password" : "text"}
                autoComplete={row.secret ? "new-password" : "off"}
                placeholder={t("Value")}
                value={row.value}
                disabled={disabled}
                onChange={(event) => set(index, { value: event.target.value })}
              />
              <input type="checkbox" className="cs-checkbox" aria-label={t("Secret")} checked={row.secret} disabled={disabled} onChange={(event) => set(index, { secret: event.target.checked })} />
              <TrashButton label={t("Remove variable")} disabled={disabled} onClick={() => onChange(rows.filter((_, i) => i !== index))} />
            </div>
          ))}
        </div>
      )}
      <footer className="cs-env-footer">
        <CodingButton ghost disabled={disabled} onClick={onImport}>
          <ClipboardPaste size={14} />
          Import from .env
        </CodingButton>
        <CodingButton small disabled={disabled} onClick={() => onChange([...rows, { name: "", value: "", secret: false }])}>
          <Plus size={14} />
          Add variable
        </CodingButton>
      </footer>
    </div>
  );
}

const userName = (user: User | undefined, id: string) => user?.displayName || user?.name || id;

/**
 * Permissions › Environment owners. Owners can change settings and open terminals; only
 * workspace admins can change the list (the API rejects `ownerIds` from anyone else).
 */
export function OwnersEditor({ ownerIds, users, canManage, onChange }: { ownerIds: string[]; users: User[]; canManage: boolean; onChange: (ownerIds: string[]) => void }) {
  const { t } = useI18n();
  const find = (id: string) => users.find((user) => user.id === id || user.userId === id);
  const candidates = users.filter((user) => user.active && !user.app && !user.builtinAgent && !ownerIds.some((id) => id === user.id || id === user.userId));
  return (
    <div className="feature-card cs-owners" data-testid="owners-editor">
      <div className="cs-owners-head">
        <strong>{t("Environment owners")}</strong>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <CodingButton small disabled={!canManage || !candidates.length}>
              <Plus size={14} />
              Add owner
            </CodingButton>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="feature-select-menu cs-select-menu cs-owner-menu">
            {candidates.map((user) => (
              <DropdownMenuItem key={user.id} data-i18n-ignore onSelect={() => onChange([...ownerIds, user.id])}>
                <span className="cs-owner-option">
                  <UserAvatar className="cs-owner-avatar" name={userName(user, user.id)} avatarUrl={user.avatarUrl} />
                  <span>{userName(user, user.id)}</span>
                </span>
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
      {ownerIds.map((id) => {
        const user = find(id);
        const name = userName(user, id);
        return (
          <div key={id} className="cs-owner-row" data-testid={`owner-${id}`}>
            <UserAvatar className="cs-owner-avatar" name={name} avatarUrl={user?.avatarUrl} />
            <div>
              <strong data-i18n-ignore>{name}</strong>
              {user?.email && <span data-i18n-ignore>{user.email}</span>}
            </div>
            {canManage && (
              <button type="button" className="cs-icon-button cs-owner-remove" aria-label={t("Remove {name}").replace("{name}", name)} title={t("Remove owner")} onClick={() => onChange(ownerIds.filter((owner) => owner !== id))}>
                <X size={14} />
              </button>
            )}
          </div>
        );
      })}
    </div>
  );
}
