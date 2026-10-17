import * as Dropdown from "@radix-ui/react-dropdown-menu";
import { ArrowUpRight, Check, ChevronDown } from "lucide-react";
import { useEffect, useState } from "react";

import { useI18n } from "@/i18n/i18n";
import { updateProjectUpdateSettings } from "@/lib/api";
import { toast } from "@/lib/toast";
import type { BootstrapData } from "@/types/flow";

import { SlackUpdates } from "./slack-updates";
import "./project-updates-settings.css";

const DAYS = [
  "Monday",
  "Tuesday",
  "Wednesday",
  "Thursday",
  "Friday",
  "Saturday",
  "Sunday",
] as const;
const DEFAULT_DAY = 5;
const DEFAULT_HOUR = 14;

function hourLabel(hour: number) {
  const point = (value: number) => {
    const normalized = value % 24;
    return `${normalized % 12 || 12}${normalized < 12 ? "AM" : "PM"}`;
  };
  return `${point(hour)} - ${point(hour + 1)}`;
}

function cadenceOptions(t: (value: string) => string): Array<[number, string]> {
  return [
    [0, t("No expectation for updates")],
    [7, t("Every week")],
    ...[2, 3, 4, 5, 6, 7, 8].map(
      (weeks) => [weeks * 7, t(`Every ${weeks} weeks`)] as [number, string],
    ),
  ];
}

function clampInt(value: unknown, min: number, max: number, fallback: number) {
  const number = Number(value);
  return Number.isInteger(number) && number >= min && number <= max
    ? number
    : fallback;
}

function centerInView(element: HTMLDivElement | null) {
  if (element) requestAnimationFrame(() => element.scrollIntoView?.({ block: "center" }));
}

function SelectMenu({
  label,
  value,
  options,
  onChange,
  ariaLabel,
}: {
  label: string;
  value: number;
  options: Array<[number, string]>;
  onChange: (value: number) => void;
  ariaLabel: string;
}) {
  return (
    <Dropdown.Root>
      <Dropdown.Trigger asChild>
        <button
          type="button"
          className="iu-select"
          role="combobox"
          aria-label={ariaLabel}
        >
          {label}
          <ChevronDown size={12} />
        </button>
      </Dropdown.Trigger>
      <Dropdown.Portal>
        <Dropdown.Content
          data-flow-motion="floating"
          className="iu-menu"
          align="start"
          sideOffset={4}
          collisionPadding={8}
          data-i18n-ignore
        >
          {options.map(([key, text]) => (
            <Dropdown.Item
              className="iu-menu-item"
              key={key}
              ref={key === value ? centerInView : undefined}
              onSelect={() => onChange(key)}
            >
              {text}
              {key === value && <Check size={14} />}
            </Dropdown.Item>
          ))}
        </Dropdown.Content>
      </Dropdown.Portal>
    </Dropdown.Root>
  );
}

export function ProjectUpdateSettingsPage({
  data,
  onReload,
}: {
  data: BootstrapData;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  const current = (data.settings?.projectUpdates ?? {}) as Record<
    string,
    unknown
  >;
  const saved = {
    cadence: clampInt(current.cadenceDays, 0, 365, 0),
    day: clampInt(current.updateDay, 1, 7, DEFAULT_DAY),
    hour: clampInt(current.updateHour, 0, 23, DEFAULT_HOUR),
  };
  const [editing, setEditing] = useState(false);
  const [cadence, setCadence] = useState(saved.cadence);
  const [day, setDay] = useState(saved.day);
  const [hour, setHour] = useState(saved.hour);
  const [saving, setSaving] = useState(false);
  const options = cadenceOptions(t);
  const optionLabel = (value: number) =>
    options.find((item) => item[0] === value)?.[1] ?? options[0][1];
  const dayOptions = DAYS.map(
    (name, index) => [index + 1, t(name)] as [number, string],
  );
  const hourOptions = Array.from(
    { length: 24 },
    (_, index) => [index, hourLabel(index)] as [number, string],
  );
  const summary = (c: number, d: number, h: number) =>
    c === 0
      ? optionLabel(0)
      : `${optionLabel(c)} ${t("on")} ${t(DAYS[d - 1])} ${t("between")} ${hourLabel(h)}`;

  useEffect(() => {
    if (editing) return;
    setCadence(saved.cadence);
    setDay(saved.day);
    setHour(saved.hour);
  }, [editing, saved.cadence, saved.day, saved.hour]);

  const cancel = () => {
    setCadence(saved.cadence);
    setDay(saved.day);
    setHour(saved.hour);
    setEditing(false);
  };
  const save = async () => {
    setSaving(true);
    try {
      await updateProjectUpdateSettings(
        cadence === 0
          ? { cadenceDays: 0 }
          : { cadenceDays: cadence, updateDay: day, updateHour: hour },
      );
      await onReload();
      setEditing(false);
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t("Could not save changes"),
      );
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="iu-page" data-i18n-ignore>
      <header className="iu-header">
        <h1>{t("Project updates")}</h1>
        <p>
          {t(
            "Short status reports about the progress and health of your projects. Project members regularly post updates, and subscribers automatically receive them in their inbox.",
          )}{" "}
          <a
            href="https://flow.app/docs/project-updates"
            target="_blank"
            rel="noreferrer"
          >
            {t("Docs")}
            <ArrowUpRight size={12} />
          </a>
        </p>
      </header>
      <section className="iu-section">
        <h2>{t("Update schedule")}</h2>
        <p>
          {t(
            "Configure how often updates are expected on projects. Project leads will receive reminders to post updates.",
          )}
        </p>
        <div className="iu-card iu-row">
          {editing ? (
            <>
              <div className="iu-editor">
                <SelectMenu
                  ariaLabel={t("Update frequency")}
                  label={optionLabel(cadence)}
                  value={cadence}
                  options={options}
                  onChange={setCadence}
                />
                {cadence > 0 && (
                  <>
                    <span>{t("on")}</span>
                    <SelectMenu
                      ariaLabel={t("Day of the week")}
                      label={t(DAYS[day - 1])}
                      value={day}
                      options={dayOptions}
                      onChange={setDay}
                    />
                    <span>{t("between")}</span>
                    <SelectMenu
                      ariaLabel={t("Time of day")}
                      label={hourLabel(hour)}
                      value={hour}
                      options={hourOptions}
                      onChange={setHour}
                    />
                  </>
                )}
              </div>
              <div className="iu-actions">
                <button type="button" className="iu-button" onClick={cancel}>
                  {t("Cancel")}
                </button>
                <button
                  type="button"
                  className="iu-button is-primary"
                  disabled={saving}
                  onClick={() => void save()}
                >
                  {t("Save")}
                </button>
              </div>
            </>
          ) : (
            <>
              <span>{summary(saved.cadence, saved.day, saved.hour)}</span>
              <button
                type="button"
                className="iu-button"
                onClick={() => setEditing(true)}
              >
                {t("Edit")}
              </button>
            </>
          )}
        </div>
      </section>
      <SlackUpdates
        data={data}
        kind="project"
        variant="row"
        disabled={saving}
        onReload={onReload}
      />
    </div>
  );
}
