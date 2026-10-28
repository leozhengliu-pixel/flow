import { useEffect, useRef, useState } from "react";
import { AlertTriangle, Plus, RotateCcw, X } from "lucide-react";
import { toast } from "@/lib/toast";
import { SelectControl } from "@/components/ui/select-control";
import { Toggle } from "@/components/ui/toggle";
import { analyzeLoopOutputs, updateLoop } from "@/lib/api";
import { useI18n } from "@/i18n/i18n";
import { activeTeams } from "./loop-data";
import { CARDINALITY_LABELS, CONTRACT_FALLBACK_NOTICE, CONTRACT_KIND_LABELS, CONTRACT_OPERATION_LABELS, blankOutput, contractTargetText, operationsFor, personContract } from "./loop-output-contract-model";
import type { BootstrapData, Loop, LoopContractOutput, LoopOutputContract, LoopOutputKind, LoopOutputOperation } from "@/types/flow";

/** The contract's outputs, read-only: what each run makes, reads and must not touch, with the instruction text behind it. */
export function LoopContractSummary({ contract, data, showEvidence = true }: { contract: LoopOutputContract; data: Pick<BootstrapData, "teams" | "documents">; showEvidence?: boolean }) {
  const { t } = useI18n();
  const outputs = contract.readOnly ? contract.outputs.filter((output) => output.operation === "forbidden" || output.operation === "read") : contract.outputs;
  return (
    <div className="loops-contract-summary">
      {contract.readOnly && (
        <p className="loops-contract-readonly">
          <strong>{t("Reply only: the run changes nothing")}</strong>
          {showEvidence && contract.readOnlyEvidence && <q className="loops-contract-evidence" data-i18n-ignore>{contract.readOnlyEvidence}</q>}
        </p>
      )}
      {outputs.length > 0 && (
        <ul className="loops-contract-list" aria-label={t("Outputs")}>
          {outputs.map((output, index) => {
            const target = contractTargetText(output, data, t);
            return (
              <li key={`${output.kind}-${output.operation}-${index}`} className={`loops-contract-item is-${output.operation}`}>
                <span className="loops-contract-head">
                  <span className="loops-contract-operation">{t(CONTRACT_OPERATION_LABELS[output.operation] ?? output.operation)}</span>
                  <span className="loops-contract-kind">{t(CONTRACT_KIND_LABELS[output.kind] ?? output.kind)}</span>
                  {output.required && <span className="loops-contract-badge">{t("Must produce")}</span>}
                </span>
                {target && <small className="loops-contract-target" data-i18n-ignore>{target}</small>}
                {showEvidence && output.evidence && <q className="loops-contract-evidence" data-i18n-ignore>{output.evidence}</q>}
              </li>
            );
          })}
        </ul>
      )}
      {!contract.readOnly && outputs.length === 0 && <p className="loops-contract-empty">{t("No outputs")}</p>}
      {contract.always && <p className="loops-contract-note">{t("Required even when nothing changed")}{showEvidence && contract.alwaysEvidence && <q className="loops-contract-evidence" data-i18n-ignore>{contract.alwaysEvidence}</q>}</p>}
      {(contract.skipConditions ?? []).map((skip) => (
        <p className="loops-contract-note" key={skip.condition}>
          {t("Skips the run: {condition}").replace("{condition}", () => skip.evidence || skip.condition)}
        </p>
      ))}
    </div>
  );
}

/** Editable row of one output. */
function ContractOutputEditor({ output, data, onChange, onRemove }: { output: LoopContractOutput; data: BootstrapData; onChange: (next: LoopContractOutput) => void; onRemove: () => void }) {
  const { t } = useI18n();
  const target = output.target ?? {};
  const setTarget = (patch: Partial<LoopContractOutput["target"]>) => onChange({ ...output, target: { ...target, ...patch } });
  const writes = output.operation === "create" || output.operation === "update" || output.operation === "append" || output.operation === "write";
  const isDocument = output.kind === "document";
  const documents = (data.documents ?? []).filter((document) => !document.archivedAt);
  return (
    <li className="loops-contract-edit-row">
      <div className="loops-contract-edit-line">
        <SelectControl
          label={t("Output kind")}
          value={output.kind}
          onChange={(value) => {
            const kind = value as LoopOutputKind;
            onChange({ ...output, kind, operation: operationsFor(kind).includes(output.operation) ? output.operation : "create", target: kind === "document" ? target : { mode: target.mode, team: target.team, teamId: target.teamId } });
          }}
          options={(Object.keys(CONTRACT_KIND_LABELS) as LoopOutputKind[]).map((kind) => ({ value: kind, label: t(CONTRACT_KIND_LABELS[kind]) }))}
        />
        <SelectControl
          label={t("Operation")}
          value={output.operation}
          onChange={(value) => {
            const operation = value as LoopOutputOperation;
            const mode = operation === "create" ? "new" : operation === "update" || operation === "append" ? (isDocument ? "existing" : target.mode) : target.mode;
            onChange({ ...output, operation, required: operation === "read" || operation === "forbidden" ? false : output.required, target: { ...target, mode } });
          }}
          options={operationsFor(output.kind).map((operation) => ({ value: operation, label: t(CONTRACT_OPERATION_LABELS[operation]) }))}
        />
        {writes && (
          <label className="loops-contract-check">
            <input type="checkbox" checked={output.required} onChange={(event) => onChange({ ...output, required: event.target.checked })} />
            {t("Must produce")}
          </label>
        )}
        <button type="button" className="loops-icon-button is-plain" aria-label={t("Remove output")} title={t("Remove output")} onClick={onRemove}>
          <X size={14} />
        </button>
      </div>
      {writes && (
        <div className="loops-contract-edit-line">
          {isDocument && (output.operation === "create" || output.operation === "write") && (
            <input
              className="loops-contract-input"
              aria-label={t("Title template")}
              placeholder={t("Title, e.g. Daily summary-{date}")}
              value={target.titleTemplate ?? ""}
              onChange={(event) => setTarget({ titleTemplate: event.target.value })}
            />
          )}
          {isDocument && (output.operation === "update" || output.operation === "append") && (
            <SelectControl
              label={t("Document")}
              value={target.documentId ?? ""}
              onChange={(value) => {
                const document = documents.find((item) => item.id === value);
                setTarget({ documentId: value || undefined, title: document?.title ?? target.title, mode: "existing" });
              }}
              options={[{ value: "", label: target.title ? t("Find by title: {title}").replace("{title}", () => target.title!) : t("Any existing document") }, ...documents.map((document) => ({ value: document.id, label: document.title || t("Untitled"), entityName: true }))]}
            />
          )}
          <SelectControl
            label={t("Team")}
            value={target.teamId ?? ""}
            onChange={(value) => setTarget({ teamId: value || undefined, team: value ? data.teams.find((team) => team.id === value)?.name : undefined })}
            options={[{ value: "", label: target.team ? target.team : t("The loop's team") }, ...activeTeams(data.teams).map((team) => ({ value: team.id, label: team.name, entityName: true }))]}
          />
          <SelectControl
            label={t("How many")}
            value={output.cardinality ?? "any"}
            onChange={(value) => onChange({ ...output, cardinality: value as LoopContractOutput["cardinality"] })}
            options={Object.entries(CARDINALITY_LABELS).map(([value, label]) => ({ value, label: t(label) }))}
          />
        </div>
      )}
      {output.evidence && <q className="loops-contract-evidence" data-i18n-ignore>{output.evidence}</q>}
    </li>
  );
}

/**
 * "What this loop will produce": the output contract read from the saved instructions, with the text behind each
 * output. It reads the contract when the loop has none for its current text, and a person can edit it, which makes
 * the edit authoritative, or go back to the automatic one.
 */
export function LoopOutputContractPanel({
  data,
  loop,
  unsaved,
  onLoopChange,
  readDelayMs = 1500,
}: {
  data: BootstrapData;
  loop: Loop;
  /** The editor has changes to the name or instructions that are not saved yet. */
  unsaved: boolean;
  onLoopChange: (loop: Loop) => void;
  readDelayMs?: number;
}) {
  const { t } = useI18n();
  const contract = loop.outputContract;
  const [analyzing, setAnalyzing] = useState(false);
  const [editing, setEditing] = useState<LoopOutputContract | undefined>(undefined);
  const [saving, setSaving] = useState(false);
  const requested = useRef("");
  const hasInstructions = Boolean(loop.instructions.trim());
  const needsRead = hasInstructions && !unsaved && contract?.source !== "person" && (!contract || contract.stale);

  useEffect(() => {
    // One read per saved text (the server caches it by instructions hash), once the text has settled: a draft
    // saves as it is typed.
    const key = `${loop.id}:${loop.updatedAt}`;
    if (!needsRead || requested.current === key) return;
    let active = true;
    const timer = window.setTimeout(() => {
      requested.current = key;
      setAnalyzing(true);
      analyzeLoopOutputs(loop.id)
        .then((next) => {
          if (active) onLoopChange(next);
        })
        .catch(() => undefined)
        .finally(() => setAnalyzing(false));
    }, readDelayMs);
    return () => {
      active = false;
      window.clearTimeout(timer);
    };
  }, [loop.id, loop.updatedAt, needsRead, onLoopChange, readDelayMs]);

  const save = async (next: LoopOutputContract | null) => {
    setSaving(true);
    try {
      const saved = await updateLoop(loop.id, { outputContract: next ? personContract(next) : null });
      onLoopChange(saved);
      setEditing(undefined);
      if (!next) {
        setAnalyzing(true);
        onLoopChange(await analyzeLoopOutputs(loop.id).finally(() => setAnalyzing(false)));
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not save outputs"));
    } finally {
      setSaving(false);
    }
  };

  const reread = async () => {
    setAnalyzing(true);
    try {
      onLoopChange(await analyzeLoopOutputs(loop.id, true));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not read the outputs"));
    } finally {
      setAnalyzing(false);
    }
  };

  const fallback = contract?.source === "fallback";
  return (
    <section className="loops-section" aria-label={t("What this loop will produce")}>
      <div className="loops-section-heading">
        <h3 className="loops-section-title">{t("What this loop will produce")}</h3>
        {!editing && hasInstructions && (
          <div className="loops-contract-actions">
            {contract?.source === "person" ? (
              <button className="loops-ghost-button is-small" type="button" disabled={saving} onClick={() => void save(null)}>
                <RotateCcw size={12} />
                {t("Use automatic outputs")}
              </button>
            ) : (
              contract && !unsaved && (
                <button className="loops-ghost-button is-small" type="button" disabled={analyzing} onClick={() => void reread()}>
                  {t("Read again")}
                </button>
              )
            )}
            <button className="loops-ghost-button is-small" type="button" disabled={analyzing} onClick={() => setEditing(contract ? { ...contract, outputs: contract.outputs.map((output) => ({ ...output, target: { ...output.target } })) } : { source: "person", readOnly: false, outputs: [blankOutput()] })}>
              {t("Edit outputs")}
            </button>
          </div>
        )}
      </div>
      <div className="loops-card is-contract">
        {fallback && !editing && (
          <p className="loops-contract-warning" role="alert">
            <AlertTriangle size={14} />
            <span>{t(CONTRACT_FALLBACK_NOTICE)}</span>
          </p>
        )}
        {contract?.source === "person" && contract.stale && !editing && <p className="loops-contract-hint">{t("The instructions changed after these outputs were set. Check that they still fit.")}</p>}
        {unsaved && contract?.source !== "person" && <p className="loops-contract-hint">{t("Save the loop to read its outputs from the new instructions.")}</p>}
        {editing ? (
          <div className="loops-contract-editor">
            <div className="loops-permission-row">
              <span>
                <strong>{t("Reply only")}</strong>
                <small>{t("The run reports in its reply and changes nothing in Flow or in connectors.")}</small>
              </span>
              <Toggle size="regular" checked={editing.readOnly} label={t("Reply only")} onChange={(checked) => setEditing({ ...editing, readOnly: checked })} />
            </div>
            <ul className="loops-contract-edit-list" aria-label={t("Outputs")}>
              {editing.outputs.map((output, index) => (
                <ContractOutputEditor
                  key={index}
                  output={output}
                  data={data}
                  onChange={(next) => setEditing({ ...editing, outputs: editing.outputs.map((item, position) => (position === index ? next : item)) })}
                  onRemove={() => setEditing({ ...editing, outputs: editing.outputs.filter((_, position) => position !== index) })}
                />
              ))}
            </ul>
            <div className="loops-contract-edit-footer">
              <button className="loops-add-connector" type="button" onClick={() => setEditing({ ...editing, outputs: [...editing.outputs, blankOutput()] })}>
                <Plus size={14} />
                {t("Add output")}
              </button>
              <span className="loops-contract-edit-buttons">
                <button className="loops-ghost-button" type="button" onClick={() => setEditing(undefined)}>
                  {t("Cancel")}
                </button>
                <button className="loops-primary-button" type="button" disabled={saving} onClick={() => void save(editing)}>
                  {saving ? t("Saving…") : t("Save outputs")}
                </button>
              </span>
            </div>
          </div>
        ) : analyzing || (needsRead && !contract) ? (
          <p className="loops-contract-reading" role="status">{t("Reading the instructions…")}</p>
        ) : contract && !fallback ? (
          <>
            <LoopContractSummary contract={contract} data={data} />
            {contract.source === "person" && <p className="loops-contract-source">{t("Set by a person")}</p>}
          </>
        ) : !hasInstructions ? (
          <p className="loops-contract-empty">{t("Write the instructions to see what the loop will produce.")}</p>
        ) : null}
      </div>
    </section>
  );
}
