import { useEffect, useMemo, useRef, useState } from "react";

import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { DateTimeControl } from "@/components/ui/date-time-control";
import { SelectControl } from "@/components/ui/select-control";
import {
  completeActionDialog,
  currentActionDialog,
  subscribeActionDialogs,
  validatePromptString,
  type DateCompareOption,
  type DialogRequest,
} from "./action-dialog-service";

import "./action-dialogs.css";

export function ActionDialogHost() {
  const [request, setRequest] = useState<DialogRequest>();
  const [value, setValue] = useState("");
  const [compare, setCompare] = useState<DateCompareOption>("after");
  const [selectedId, setSelectedId] = useState("");
  const [choice, setChoice] = useState("");
  const [error, setError] = useState<string>();
  const lastRequest = useRef<DialogRequest | undefined>(request);
  if (request) lastRequest.current = request;
  const displayRequest = request ?? lastRequest.current;

  useEffect(() => {
    const sync = () => setRequest(current => current ?? currentActionDialog());
    const unsubscribe = subscribeActionDialogs(sync);
    sync();
    return unsubscribe;
  }, []);

  useEffect(() => {
    if (!request) return;
    setError(undefined);
    switch (request.kind) {
      case "prompt":
      case "stringValidated":
        setValue(request.defaultValue);
        break;
      case "date":
      case "dateOrInterval":
        setValue(request.defaultValue);
        if (request.kind === "dateOrInterval") setCompare(request.initialCompare);
        break;
      case "dropdown":
        setSelectedId(request.selectedId ?? request.options[0]?.id ?? "");
        break;
      case "choice":
        setChoice(request.defaultChoice);
        break;
      default:
        break;
    }
  }, [request]);

  const finish = (result: Parameters<typeof completeActionDialog>[0]) => {
    completeActionDialog(result);
    setRequest(undefined);
  };

  const dropdownDefaultId = displayRequest?.kind === "dropdown" ? displayRequest.selectedId : undefined;
  const dropdownDisabled =
    displayRequest?.kind === "dropdown" &&
    displayRequest.disableOnDefault &&
    selectedId === dropdownDefaultId;

  const stringError = useMemo(() => {
    if (displayRequest?.kind !== "stringValidated") return undefined;
    return validatePromptString(value, displayRequest);
  }, [displayRequest, value]);

  const confirmDisabled = (() => {
    if (!request) return true;
    switch (request.kind) {
      case "prompt":
        return !value.trim();
      case "stringValidated":
        return Boolean(stringError) || (!request.optional && !value.trim());
      case "date":
      case "dateOrInterval":
        return !value.trim();
      case "dropdown":
        return !selectedId || Boolean(dropdownDisabled);
      default:
        return false;
    }
  })();

  const onConfirm = () => {
    if (!request) return;
    switch (request.kind) {
      case "confirm":
        finish(true);
        break;
      case "prompt":
        finish(value.trim());
        break;
      case "stringValidated": {
        const nextError = validatePromptString(value, request);
        if (nextError) {
          setError(nextError);
          return;
        }
        finish(value.trim());
        break;
      }
      case "date":
        finish(value.trim());
        break;
      case "dateOrInterval":
        finish({ date: value.trim(), compare });
        break;
      case "dropdown": {
        const option = request.options.find(item => item.id === selectedId);
        finish(option?.value ?? null);
        break;
      }
      case "diff":
        finish("accept");
        break;
      case "choice":
        finish(choice || null);
        break;
    }
  };

  const onCancel = () => {
    if (!request) return;
    if (request.kind === "confirm") finish(false);
    else if (request.kind === "diff") finish("cancel");
    else finish(null);
  };

  const openAutoFocus = (event: Event) => {
    if (!request) return;
    if (request.kind === "prompt" || request.kind === "stringValidated") {
      event.preventDefault();
      requestAnimationFrame(() => document.querySelector<HTMLInputElement>(".action-dialog input")?.focus());
    }
  };

  return (
    <Dialog open={Boolean(request)} onOpenChange={open => !open && onCancel()}>
      <DialogContent
        className="action-dialog"
        overlayClassName="action-dialog-overlay"
        aria-hidden={!request || undefined}
        inert={!request || undefined}
        onOpenAutoFocus={openAutoFocus}
      >
        <DialogTitle>{displayRequest?.title}</DialogTitle>
        {displayRequest?.description && <p>{displayRequest.description}</p>}

        {displayRequest?.kind === "prompt" && (
          <input
            aria-label={displayRequest.title}
            onChange={event => setValue(event.target.value)}
            onKeyDown={event => {
              if (event.key === "Enter" && value.trim()) {
                event.preventDefault();
                finish(value.trim());
              }
            }}
            value={value}
          />
        )}

        {displayRequest?.kind === "stringValidated" && (
          <>
            <input
              aria-invalid={Boolean(error || stringError) || undefined}
              aria-label={displayRequest.title}
              maxLength={displayRequest.maxLength}
              onChange={event => {
                setValue(event.target.value);
                setError(undefined);
              }}
              onKeyDown={event => {
                if (event.key !== "Enter") return;
                event.preventDefault();
                onConfirm();
              }}
              placeholder={displayRequest.placeholder}
              value={value}
            />
            {(error || stringError) && <p className="action-dialog-error" role="alert">{error || stringError}</p>}
          </>
        )}

        {displayRequest?.kind === "date" && (
          <div className="action-dialog-date">
            <DateTimeControl
              label={displayRequest.title}
              min={displayRequest.min}
              mode="date"
              onChange={setValue}
              value={value}
            />
          </div>
        )}

        {displayRequest?.kind === "dateOrInterval" && (
          <div className="action-dialog-date-interval">
            <div className="action-dialog-compare-tabs" role="tablist" aria-label="Compare option">
              {(["on", "before", "after"] as const).map(option => (
                <button
                  aria-label={option === "on" ? "On" : option === "before" ? "Before" : "After"}
                  aria-selected={compare === option}
                  className={compare === option ? "active" : undefined}
                  key={option}
                  onClick={() => setCompare(option)}
                  role="tab"
                  type="button"
                >
                  {option}
                </button>
              ))}
            </div>
            <DateTimeControl label={displayRequest.title} mode="date" onChange={setValue} value={value} />
          </div>
        )}

        {displayRequest?.kind === "dropdown" && (
          <div className="action-dialog-dropdown">
            <SelectControl
              label={displayRequest.title}
              onChange={setSelectedId}
              options={displayRequest.options.map(option => ({
                value: option.id,
                label: option.label,
                disabled: option.disabled,
                groupLabel: option.groupLabel,
              }))}
              value={selectedId}
            />
          </div>
        )}

        {displayRequest?.kind === "diff" && (
          <div className="action-dialog-diffs" role="list">
            {displayRequest.diffs.map((diff, index) => (
              <pre key={index} role="listitem">
                {diff}
              </pre>
            ))}
          </div>
        )}

        {displayRequest?.kind === "choice" && (
          <div
            className="action-dialog-choices"
            role="radiogroup"
            aria-label={displayRequest.title}
          >
            {displayRequest.choices.map((item) => (
              <label key={item.id} className="action-dialog-choice">
                <input
                  type="radio"
                  name="action-dialog-choice"
                  checked={choice === item.id}
                  onChange={() => setChoice(item.id)}
                />
                <span>{item.label}</span>
              </label>
            ))}
          </div>
        )}

        <footer>
          {displayRequest?.kind === "diff" ? (
            <>
              <button disabled={!request} onClick={() => finish("cancel")} type="button">
                {displayRequest.cancelLabel ?? "Cancel"}
              </button>
              {displayRequest.rejectLabel && (
                <button disabled={!request} onClick={() => finish("reject")} type="button">
                  {displayRequest.rejectLabel}
                </button>
              )}
              <button
                className={displayRequest.danger ? "danger" : "primary"}
                disabled={!request}
                onClick={() => finish("accept")}
                type="button"
              >
                {displayRequest.confirmLabel}
              </button>
            </>
          ) : (
            <>
              <button disabled={!request} onClick={onCancel} type="button">
                Cancel
              </button>
              <button
                className={displayRequest?.kind === "confirm" && displayRequest.danger ? "danger" : "primary"}
                disabled={confirmDisabled}
                onClick={onConfirm}
                type="button"
              >
                {displayRequest?.confirmLabel}
              </button>
            </>
          )}
        </footer>
      </DialogContent>
    </Dialog>
  );
}
