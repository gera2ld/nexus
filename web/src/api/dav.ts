import { onUnauthorized, token } from "./client";

/**
 * A small WebDAV client for /dav/files/{uid}/…, signed in with the
 * dashboard token (docs/WEB.md §6.1). Paths here are the part after
 * /dav/files/{uid}:
 * "/" is the list of mounts, "/docs/" a folder, "/docs/a.txt" a file.
 * Folders end with a slash.
 */

export type Entry = {
  name: string;
  /** Path below /dav/files/{uid}; folders end with "/". */
  path: string;
  dir: boolean;
  size: number;
  modified?: Date;
  type: string;
  etag: string;
};

export class DavError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

let uid = "";
/** Sets whose files the client works on: the signed-in user. */
export function setUser(id: string) {
  uid = id;
}

/** The URL path of p, each segment escaped. */
export function rawPath(p: string) {
  return `/dav/files/${uid}` + p.split("/").map(encodeURIComponent).join("/");
}

/** The absolute raw link, for "copy raw link" and MOVE/COPY destinations. */
export function rawURL(p: string) {
  return location.origin + rawPath(p);
}

async function send(method: string, p: string, init: RequestInit & { headers?: Record<string, string> } = {}) {
  const r = await fetch(rawPath(p), {
    ...init,
    method,
    headers: { Authorization: `Bearer ${token.get()}`, ...init.headers },
  });
  if (r.status === 401) onUnauthorized();
  return r;
}

async function fail(r: Response, what: string): Promise<never> {
  // The server's errors are short plain-text sentences.
  const text = (await r.text().catch(() => "")).trim();
  const msg = text && text.length < 300 && !text.startsWith("<") ? text : r.statusText;
  throw new DavError(`${what}: ${msg || `error ${r.status}`}`, r.status);
}

const PROPS = `<?xml version="1.0" encoding="utf-8"?>
<propfind xmlns="DAV:"><prop><resourcetype/><getcontentlength/><getlastmodified/><getcontenttype/><getetag/></prop></propfind>`;

/** Parses a PROPFIND multistatus into entries, by path. */
export function parseMultistatus(xml: string): Entry[] {
  const doc = new DOMParser().parseFromString(xml, "application/xml");
  const prefix = `/dav/files/${uid}`;
  const out: Entry[] = [];
  for (const res of Array.from(doc.getElementsByTagNameNS("DAV:", "response"))) {
    const href = res.getElementsByTagNameNS("DAV:", "href")[0]?.textContent ?? "";
    let p = new URL(href, location.origin).pathname;
    p = p.split("/").map(decodeURIComponent).join("/");
    if (!p.startsWith(prefix + "/")) continue;
    p = p.slice(prefix.length);
    // Only the 200 propstat holds values.
    const ok = Array.from(res.getElementsByTagNameNS("DAV:", "propstat")).find((ps) =>
      /\s200\s/.test(ps.getElementsByTagNameNS("DAV:", "status")[0]?.textContent ?? ""),
    );
    const get = (n: string) => ok?.getElementsByTagNameNS("DAV:", n)[0];
    const dir = !!get("resourcetype")?.getElementsByTagNameNS("DAV:", "collection").length;
    if (dir && !p.endsWith("/")) p += "/";
    const mod = get("getlastmodified")?.textContent;
    out.push({
      name: p.replace(/\/$/, "").split("/").pop() ?? "",
      path: p,
      dir,
      size: Number(get("getcontentlength")?.textContent ?? 0) || 0,
      modified: mod ? new Date(mod) : undefined,
      type: get("getcontenttype")?.textContent ?? "",
      etag: get("getetag")?.textContent ?? "",
    });
  }
  return out;
}

/** Lists a folder: its entries, folders first, then by name. */
export async function list(p: string): Promise<Entry[]> {
  const r = await send("PROPFIND", p, { headers: { Depth: "1", "Content-Type": "application/xml" }, body: PROPS });
  if (r.status !== 207) await fail(r, "Couldn't list this folder");
  return parseMultistatus(await r.text())
    .filter((e) => e.path !== p)
    .sort((a, b) => Number(b.dir) - Number(a.dir) || a.name.localeCompare(b.name, undefined, { numeric: true }));
}

/** One file's or folder's properties. */
export async function stat(p: string): Promise<Entry> {
  const r = await send("PROPFIND", p, { headers: { Depth: "0", "Content-Type": "application/xml" }, body: PROPS });
  if (r.status !== 207) await fail(r, r.status === 404 ? "Not found" : "Couldn't open this");
  const [e] = parseMultistatus(await r.text());
  if (!e) throw new DavError("Not found", 404);
  return e;
}

export async function mkcol(p: string) {
  const r = await send("MKCOL", p);
  if (r.status === 405) throw new DavError("Something with that name is already there.", 405);
  if (!r.ok) await fail(r, "Couldn't create the folder");
}

export async function remove(p: string) {
  const r = await send("DELETE", p);
  if (!r.ok) await fail(r, `Couldn't delete ${p}`);
}

/** MOVE or COPY without overwriting; a 412 means the name is taken. */
export async function transfer(method: "MOVE" | "COPY", from: string, to: string) {
  const r = await send(method, from, { headers: { Destination: rawURL(to), Overwrite: "F" } });
  if (r.status === 412) throw new DavError(`${to} already exists.`, 412);
  if (!r.ok) await fail(r, `Couldn't ${method === "MOVE" ? "move" : "copy"} ${from}`);
}

/** Reads a file as text with its ETag; `range` reads only the start. */
export async function readText(p: string, range?: number): Promise<{ text: string; etag: string; bytes: Uint8Array }> {
  const r = await send("GET", p, { headers: range ? { Range: `bytes=0-${range - 1}` } : {}, cache: "no-store" });
  if (!r.ok) await fail(r, "Couldn't read the file");
  const bytes = new Uint8Array(await r.arrayBuffer());
  return { text: new TextDecoder().decode(bytes), etag: r.headers.get("ETag") ?? "", bytes };
}

/**
 * Saves text. With an ETag the save fails with a 412 DavError if the file
 * changed since; without one it overwrites. Returns the new ETag.
 */
export async function writeText(p: string, text: string, etag?: string): Promise<string> {
  const r = await send("PUT", p, { body: text, headers: etag ? { "If-Match": etag } : {} });
  if (r.status === 412) throw new DavError("The file changed on the server since you opened it.", 412);
  if (!r.ok) await fail(r, "Couldn't save");
  return r.headers.get("ETag") ?? (await stat(p)).etag;
}

/**
 * Uploads one file with progress (fetch can't report upload progress).
 * Resolves when stored; rejects with a DavError.
 */
export function upload(p: string, file: File, onProgress: (fraction: number) => void, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const x = new XMLHttpRequest();
    x.open("PUT", rawPath(p));
    x.setRequestHeader("Authorization", `Bearer ${token.get()}`);
    x.upload.onprogress = (e) => e.lengthComputable && onProgress(e.loaded / e.total);
    x.onload = () => {
      if (x.status === 401) onUnauthorized();
      if (x.status >= 200 && x.status < 300) resolve();
      else reject(new DavError(`Couldn't upload ${file.name}: ${x.responseText.trim() || x.statusText}`, x.status));
    };
    x.onerror = () => reject(new DavError(`Couldn't upload ${file.name}: the connection failed.`, 0));
    x.onabort = () => reject(new DavError(`Upload of ${file.name} cancelled.`, 0));
    signal?.addEventListener("abort", () => x.abort());
    x.send(file);
  });
}

// ---- paths ----

/** The folder holding p: "/docs/a/b.txt" → "/docs/a/". */
export function parentOf(p: string) {
  const t = p.replace(/\/$/, "");
  return t.slice(0, t.lastIndexOf("/") + 1) || "/";
}

/** The mount a path is in, or "" for the top level. */
export function mountOf(p: string) {
  return p.split("/")[1] ?? "";
}

/** Names that can't be used for files or folders. */
export function badName(name: string) {
  if (!name.trim()) return "Type a name.";
  if (name.includes("/")) return "Names can't contain /.";
  if (name === "." || name === "..") return "That name is reserved.";
  return "";
}
