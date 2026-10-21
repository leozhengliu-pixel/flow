import * as Dialog from "@radix-ui/react-dialog";
import { useRef } from "react";
import type { ReactNode } from "react";

import { useOptionalTranslate } from "@/i18n/i18n";

import "./settings-confirm-dialog.css";

/**
 * A destructive confirmation on a personal settings page (disconnect an
 * account, remove a connector): a 480px card centered over the settings
 * content with a title, an optional line of detail, Cancel and a red action
 * that has focus. Escape or Cancel keeps things as they are.
 */
export function SettingsConfirmDialog({
  title,
  description,
  confirmLabel,
  busy = false,
  onCancel,
  onConfirm,
}: {
  title: ReactNode;
  description?: ReactNode;
  confirmLabel: string;
  busy?: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const t = useOptionalTranslate();
  const confirmRef = useRef<HTMLButtonElement>(null);
  return (
    <Dialog.Root
      open
      onOpenChange={(open) => {
        if (!open && !busy) onCancel();
      }}
    >
      <Dialog.Portal>
        <Dialog.Overlay className="settings-confirm-overlay" data-flow-motion="backdrop" />
        <Dialog.Content
          className="settings-confirm-dialog"
          data-flow-motion="dialog"
          data-tooltip-raised=""
          aria-describedby={description ? "settings-confirm-description" : undefined}
          onOpenAutoFocus={(event) => {
            event.preventDefault();
            confirmRef.current?.focus();
          }}
        >
          <Dialog.Title className="settings-confirm-title">{title}</Dialog.Title>
          {description && (
            <Dialog.Description className="settings-confirm-description" id="settings-confirm-description">
              {description}
            </Dialog.Description>
          )}
          <footer>
            <button type="button" className="settings-confirm-cancel" disabled={busy} onClick={onCancel}>
              {t("Cancel")}
            </button>
            <button type="button" className="settings-confirm-confirm" disabled={busy} ref={confirmRef} onClick={onConfirm}>
              {confirmLabel}
            </button>
          </footer>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
