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
