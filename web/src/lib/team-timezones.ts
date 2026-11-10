export type TimezoneOption = { value: string; label: string; keywords: string };

const UTC_ZONE = "Etc/UTC";
const UTC_LABEL = "GMT+0:00 – Coordinated Universal Time";
// Used where the runtime cannot list its time zones.
const FALLBACK_ZONES = [
  "Pacific/Honolulu", "America/Anchorage", "America/Los_Angeles", "America/Denver", "America/Chicago", "America/New_York",
  "America/Sao_Paulo", "Atlantic/Azores", "Europe/London", "Europe/Paris", "Europe/Berlin", "Africa/Cairo", "Europe/Istanbul",
  "Europe/Moscow", "Asia/Dubai", "Asia/Karachi", "Asia/Kolkata", "Asia/Dhaka", "Asia/Bangkok", "Asia/Shanghai", "Asia/Tokyo",
  "Australia/Sydney", "Pacific/Auckland",
];

let cached: TimezoneOption[] | undefined;

function partOf(zone: string, style: "longOffset" | "longGeneric", at: Date) {
  return new Intl.DateTimeFormat("en-US", { timeZone: zone, timeZoneName: style })
    .formatToParts(at)
    .find((part) => part.type === "timeZoneName")?.value;
}

/** "GMT-08:00" becomes "GMT-8:00" and a bare "GMT" becomes "GMT+0:00". */
function offsetLabel(raw: string | undefined) {
  const match = /^GMT([+-])(\d{1,2})(?::(\d{2}))?$/.exec(raw ?? "");
  if (!match) return "GMT+0:00";
  return `GMT${match[1]}${Number(match[2])}:${match[3] ?? "00"}`;
}

function describeZone(zone: string, at: Date): TimezoneOption | undefined {
  if (zone === UTC_ZONE) return { value: zone, label: UTC_LABEL, keywords: "utc coordinated universal time" };
  try {
    const offset = offsetLabel(partOf(zone, "longOffset", at));
    const generic = partOf(zone, "longGeneric", at) ?? zone;
    const city = zone.split("/").pop()!.replaceAll("_", " ");
    return { value: zone, label: `${offset} – ${generic} - ${city}`, keywords: `${zone} ${generic} ${city}`.toLowerCase() };
  } catch {
    return undefined;
  }
}

/** Every time zone the runtime knows, ordered by UTC offset then name, with labels like "GMT-8:00 – Pacific Time - Los Angeles". */
export function timezoneOptions(): TimezoneOption[] {
  if (cached) return cached;
  const at = new Date();
  const supported = (Intl as { supportedValuesOf?: (key: string) => string[] }).supportedValuesOf?.("timeZone") ?? FALLBACK_ZONES;
  const zones = [...new Set([...supported.filter((zone) => zone !== "UTC"), UTC_ZONE])];
  const options = zones.map((zone) => describeZone(zone, at)).filter((option): option is TimezoneOption => Boolean(option));
  const minutes = (option: TimezoneOption) => {
    const match = /^GMT([+-])(\d+):(\d{2})/.exec(option.label);
    return match ? (match[1] === "-" ? -1 : 1) * (Number(match[2]) * 60 + Number(match[3])) : 0;
  };
  cached = options.sort((left, right) => minutes(left) - minutes(right) || left.label.localeCompare(right.label));
  return cached;
}

export function timezoneLabel(zone: string) {
  return timezoneOptions().find((option) => option.value === zone)?.label ?? describeZone(zone, new Date())?.label ?? zone;
}

export function browserTimezone() {
  try {
    const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
    return zone === "UTC" ? UTC_ZONE : zone;
  } catch {
    return UTC_ZONE;
  }
}

/** The default for a new team: the most used zone among the workspace's teams (UTC without teams), replaced by the browser zone when no team exists yet or one already uses it. */
export function defaultTeamTimezone(used: string[], browser = browserTimezone()) {
  const counts = new Map<string, number>();
  for (const zone of used) if (zone) counts.set(zone, (counts.get(zone) ?? 0) + 1);
  const mostUsed = [...counts.entries()].sort((left, right) => right[1] - left[1])[0]?.[0] ?? UTC_ZONE;
  const zone = counts.size === 0 || counts.has(browser) ? browser : mostUsed;
  return timezoneOptions().some((option) => option.value === zone) ? zone : UTC_ZONE;
}
