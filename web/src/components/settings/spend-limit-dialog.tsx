import { useState } from "react";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { useI18n } from "@/i18n/i18n";
import { parseLimitInput } from "./spend-limits-model";

/** Set (or remove) a spend limit in USD; also used for the per-member and per-loop overrides. */
export function LimitDialog({
  title,
  initial,
  inheritedLabel,
  onSave,
  onClose,
}: {
  title: string;
  initial?: number;
  /** Shown instead of "No limit" when the limit is cleared and an inherited limit applies. */
  inheritedLabel?: string;
  onSave: (amount: number) => Promise<void>;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const [text, setText] = useState(initial ? String(initial) : "");
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  const commit = async (amount: number | null) => {
    if (amount === null) {
      setError(t("Enter a dollar amount, for example 250"));
      return;
    }
    setSaving(true);
    try {
      await onSave(amount);
      onClose();
    } catch {
      setSaving(false);
    }
  };
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="usage-dialog usage-limit-dialog">
        <DialogTitle>{title}</DialogTitle>
        <p className="usage-dialog-description">{inheritedLabel ?? t("Spending stops when the limit is reached until the next reset")}</p>
        <label className="usage-limit-input">
          <span>{t("Limit")}</span>
          <span className="usage-money">
            <i aria-hidden="true">$</i>
            <input
              aria-label={t("Limit")}
              inputMode="decimal"
              autoFocus
              placeholder="0.00"
              value={text}
              onChange={(event) => {
                setText(event.target.value);
                setError("");
              }}
              onKeyDown={(event) => {
                if (event.key === "Enter") void commit(text.trim() === "" ? 0 : parseLimitInput(text));
              }}
            />
          </span>
        </label>
        {error && <p className="usage-dialog-error" role="alert">{error}</p>}
        <footer>
          {initial ? (
            <button type="button" className="usage-pill-button usage-remove" disabled={saving} onClick={() => void commit(0)}>
              {t("Remove limit")}
            </button>
          ) : null}
          <button type="button" className="usage-pill-button usage-raised" onClick={onClose}>
            {t("Cancel")}
          </button>
          <button type="button" className="usage-primary-button" disabled={saving} onClick={() => void commit(text.trim() === "" ? 0 : parseLimitInput(text))}>
            {t("Save")}
          </button>
        </footer>
      </DialogContent>
    </Dialog>
  );
}
