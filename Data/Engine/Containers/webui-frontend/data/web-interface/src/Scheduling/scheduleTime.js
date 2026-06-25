import dayjs from "dayjs";

const WALL_CLOCK_FORMAT = "YYYY-MM-DDTHH:mm";

export function normalizeEngineTimezone(value) {
  const text = String(value || "").trim();
  if (!text) return "";
  try {
    new Intl.DateTimeFormat("en-US", { timeZone: text }).format(new Date());
    return text;
  } catch {
    return "";
  }
}

function localDatetimeValue(date) {
  const offsetMs = date.getTimezoneOffset() * 60000;
  return new Date(date.getTime() - offsetMs).toISOString().slice(0, 16);
}

function datetimeValueInTimezone(date, timeZone) {
  const timezone = normalizeEngineTimezone(timeZone);
  if (!timezone) return localDatetimeValue(date);
  const parts = new Intl.DateTimeFormat("en-US", {
    timeZone: timezone,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  }).formatToParts(date);
  const lookup = Object.fromEntries(parts.map((part) => [part.type, part.value]));
  return `${lookup.year}-${lookup.month}-${lookup.day}T${lookup.hour}:${lookup.minute}`;
}

export function wallClockStringFromEpoch(epochSeconds, timeZone = "") {
  if (!epochSeconds) return "";
  const date = new Date(Number(epochSeconds) * 1000);
  if (Number.isNaN(date.getTime())) return "";
  return datetimeValueInTimezone(date, timeZone);
}

export function dayjsFromEpochInTimezone(epochSeconds, timeZone = "", fallback = dayjs()) {
  const text = wallClockStringFromEpoch(epochSeconds, timeZone);
  const parsed = text ? dayjs(text).second(0) : fallback;
  return parsed?.isValid?.() ? parsed : fallback;
}

export function dayjsFromEngineClock(clock, offsetSeconds = 0, fallback = dayjs()) {
  const epoch = Number(clock?.epoch);
  if (!Number.isFinite(epoch) || epoch <= 0) return fallback;
  return dayjsFromEpochInTimezone(epoch + Number(offsetSeconds || 0), clock?.timezone, fallback);
}

export function wallClockStringFromDayjs(value) {
  const parsed = value?.second ? value.second(0) : dayjs(value).second(0);
  return parsed?.isValid?.() ? parsed.format(WALL_CLOCK_FORMAT) : null;
}

export function wallClockStringFromDatetimeLocal(value) {
  const text = String(value || "").trim();
  if (!text) return null;
  const parsed = dayjs(text).second(0);
  return parsed?.isValid?.() ? parsed.format(WALL_CLOCK_FORMAT) : null;
}

export async function fetchEngineScheduleClock() {
  const response = await fetch("/api/server/time", {
    credentials: "include",
    cache: "no-store",
  });
  if (!response.ok) return { timezone: "", epoch: null };
  const body = await response.json().catch(() => ({}));
  return {
    timezone: normalizeEngineTimezone(body?.timezone_id),
    epoch: Number.isFinite(Number(body?.epoch)) ? Number(body.epoch) : null,
  };
}
