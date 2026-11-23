/** PocketBase dates ("2026-11-23 10:00:00.000Z") as a short local date. */
export function formatDate(s: string): string {
  if (!s) return "";
  const d = new Date(s.replace(" ", "T"));
  return Number.isNaN(d.getTime()) ? s : d.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

export const scopeNames: Record<string, string> = {
  cal: "Calendars",
  card: "Contacts",
  files: "Files",
};

/** A byte count for people: 512 B, 1.5 KB, 23 MB. */
export function formatSize(n: number) {
  if (n < 1024) return `${n} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let v = n;
  let u = -1;
  do {
    v /= 1024;
    u++;
  } while (v >= 1024 && u < units.length - 1);
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[u]}`;
}

/** A file's modification time, or a dash when the backend has none. */
export function formatTime(d?: Date) {
  return d && !Number.isNaN(d.getTime()) ? d.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" }) : "—";
}
