import { useState } from "react";
import { Upload } from "lucide-react";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { useI18n } from "@/i18n/i18n";
import { importCodingEnvironmentFile } from "@/lib/api";
import type { CodingEnvVar } from "@/types/flow";
import { classifyEnv, parseDotEnv } from "./coding-sessions-model";
import { errorMessage } from "./use-coding-settings";

type Row = CodingEnvVar & { secret: boolean; include: boolean };

/**
 * "Import from .env": paste or drop a file, review which keys become secrets, then add them to
 * the draft. Nothing is stored until the environment is saved (§18). An existing environment
 * parses on the server (`/env-import`); a new one parses locally with the same rules.
 */
export function EnvImportDialog({ environmentId, open, onClose, onImport }: { environmentId?: string; open: boolean; onClose: () => void; onImport: (result: { variables: CodingEnvVar[]; secrets: CodingEnvVar[] }) => void }) {
  const { t } = useI18n();
  const [content, setContent] = useState("");
  const [rows, setRows] = useState<Row[]>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  const reset = () => {
    setContent("");
    setRows(undefined);
    setError(undefined);
  };
  const close = () => {
    reset();
    onClose();
  };
  const parse = async () => {
    setBusy(true);
    setError(undefined);
    try {
      const result = environmentId ? await importCodingEnvironmentFile(environmentId, content) : classifyEnv(parseDotEnv(content));
      const next = [
        ...(result.variables ?? []).map((entry) => ({ ...entry, secret: false, include: true })),
        ...(result.secrets ?? []).map((entry) => ({ ...entry, secret: true, include: true })),
      ];
      if (!next.length) setError(t("No variables found in this file"));
      setRows(next);
    } catch (reason) {
      setError(errorMessage(reason, "Could not parse the file"));
    } finally {
      setBusy(false);
    }
  };
  const readFile = async (file: File | undefined) => {
    if (!file) return;
    setContent(await file.text());
    setRows(undefined);
  };
  const selected = (rows ?? []).filter((row) => row.include);

  return (
    <Dialog open={open} onOpenChange={(value) => !value && close()}>
      <DialogContent className="feature-dialog cs-import-dialog" aria-describedby={undefined}>
        <DialogTitle>{t("Import from .env")}</DialogTitle>
        {!rows ? (
          <>
            <label className="cs-dropzone" onDragOver={(event) => event.preventDefault()} onDrop={(event) => { event.preventDefault(); void readFile(event.dataTransfer.files?.[0]); }}>
              <textarea
                aria-label={t(".env contents")}
                className="cs-code"
                spellCheck={false}
                placeholder={"DATABASE_URL=postgres://localhost/app\nAPI_TOKEN=…"}
                value={content}
                onChange={(event) => setContent(event.target.value)}
              />
            </label>
            <label className="cs-file-input">
              <Upload size={13} />
              <span>{t("Choose file")}</span>
              <input type="file" aria-label={t("Choose .env file")} accept=".env,text/plain" onChange={(event) => void readFile(event.target.files?.[0])} />
            </label>
            <p className="cs-field-hint">{t("Keys that look like credentials are suggested as secrets. You can review them before importing.")}</p>
          </>
        ) : (
          <div className="cs-import-review">
            {rows.map((row, index) => (
              <div key={`${row.name}-${index}`} className="cs-import-row">
                <input type="checkbox" aria-label={t("Import {name}").replace("{name}", row.name)} checked={row.include} onChange={(event) => setRows(rows.map((item, i) => (i === index ? { ...item, include: event.target.checked } : item)))} />
                <code data-i18n-ignore>{row.name}</code>
                <span className="cs-import-value" data-i18n-ignore>{row.secret ? "••••••••" : row.value}</span>
                <label className="cs-import-secret">
                  <input type="checkbox" aria-label={t("{name} is a secret").replace("{name}", row.name)} checked={row.secret} onChange={(event) => setRows(rows.map((item, i) => (i === index ? { ...item, secret: event.target.checked } : item)))} />
                  {t("Secret")}
                </label>
              </div>
            ))}
          </div>
        )}
        {error && <p className="cs-inline-error" role="alert">{error}</p>}
        <footer className="cs-dialog-footer">
          {rows && <button type="button" className="feature-button" onClick={() => setRows(undefined)}>{t("Back")}</button>}
          <span />
          <button type="button" className="feature-button" onClick={close}>{t("Cancel")}</button>
          {!rows ? (
            <button type="button" className="feature-button primary" disabled={busy || !content.trim()} onClick={() => void parse()}>{t("Continue")}</button>
          ) : (
            <button
              type="button"
              className="feature-button primary"
              disabled={!selected.length}
              onClick={() => {
                onImport({
                  variables: selected.filter((row) => !row.secret).map(({ name, value }) => ({ name, value })),
                  secrets: selected.filter((row) => row.secret).map(({ name, value }) => ({ name, value })),
                });
                close();
              }}
            >
              {t("Import {count}").replace("{count}", String(selected.length))}
            </button>
          )}
        </footer>
      </DialogContent>
    </Dialog>
  );
}
