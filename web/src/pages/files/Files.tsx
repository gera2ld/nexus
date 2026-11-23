import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowUp,
  ChevronRight,
  Copy,
  ExternalLink,
  File,
  Folder,
  FolderInput,
  FolderPlus,
  HardDrive,
  MoreHorizontal,
  Pencil,
  Trash2,
  Upload,
  X,
} from "lucide-react";
import { useEffect, useRef, useState, type DragEvent, type FormEvent } from "react";
import { Link, useParams } from "react-router";
import { toast } from "sonner";
import { badName, list, mkcol, mountOf, parentOf, rawPath, remove, setUser, transfer, upload, type Entry } from "@/api/dav";
import { Confirm } from "@/components/Confirm";
import { Loading, NotFound } from "@/components/Layout";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useMe } from "@/hooks/queries";
import { useAuth } from "@/lib/auth";
import { formatSize, formatTime } from "@/lib/format";
import { cn } from "@/lib/utils";
import { Crumbs, useFilesLink, useReadOnly } from "./common";
import { FileView } from "./FileView";

/**
 * /files and /files/{uid}/…: the signed-in user's files. Both /files and
 * /files/{uid}/ show the mounts; a path ending in / is a folder,
 * anything else a file.
 */
export function Files() {
  const { account } = useAuth();
  const params = useParams();
  if (params.uid && params.uid !== account?.id) return <NotFound home="/" />;
  setUser(account!.id);
  const p = "/" + (params["*"] ?? "");
  const folder = p.endsWith("/");
  return (
    <div className="grid min-h-0 flex-1 gap-6 md:grid-cols-[14rem_minmax(0,1fr)]">
      <aside className="hidden min-h-0 overflow-y-auto md:block">
        <Tree current={folder ? p : parentOf(p)} />
      </aside>
      <section className="flex min-h-0 min-w-0 flex-col overflow-y-auto">{folder ? <FolderView key={p} path={p} /> : <FileView key={p} path={p} />}</section>
    </div>
  );
}

// ---- the tree ----

export const useListing = (p: string, enabled = true) =>
  useQuery({ queryKey: ["dav", p], queryFn: () => list(p), enabled, retry: false });

function Tree({ current }: { current: string }) {
  const top = useListing("/");
  const link = useFilesLink();
  return (
    <nav aria-label="Mounts and folders" className="text-sm">
      <Link to={link("/")} className={cn("block rounded-md px-2 py-1 font-medium hover:bg-accent", current === "/" && "bg-accent")}>
        Files
      </Link>
      <ul className="mt-1">
        {top.data?.map((m) => <TreeNode key={m.path} entry={m} current={current} depth={0} />)}
      </ul>
    </nav>
  );
}

function TreeNode({ entry, current, depth }: { entry: Entry; current: string; depth: number }) {
  const onPath = current.startsWith(entry.path);
  const [open, setOpen] = useState(onPath);
  useEffect(() => {
    if (onPath) setOpen(true);
  }, [onPath]);
  const q = useListing(entry.path, open);
  const dirs = q.data?.filter((e) => e.dir) ?? [];
  const link = useFilesLink();
  const Icon = depth === 0 ? HardDrive : Folder;
  return (
    <li>
      <div className={cn("flex items-center rounded-md hover:bg-accent", current === entry.path && "bg-accent font-medium")} style={{ paddingLeft: depth * 12 }}>
        <button
          type="button"
          className="grid size-6 shrink-0 place-items-center text-muted-foreground"
          aria-label={open ? `Collapse ${entry.name}` : `Expand ${entry.name}`}
          aria-expanded={open}
          onClick={() => setOpen(!open)}
        >
          <ChevronRight className={cn("size-3.5 transition-transform", open && "rotate-90")} />
        </button>
        <Link to={link(entry.path)} className="flex min-w-0 flex-1 items-center gap-1.5 py-1 pr-2">
          <Icon className="size-4 shrink-0 text-muted-foreground" />
          <span className="truncate">{entry.name}</span>
        </Link>
      </div>
      {open && dirs.length > 0 && (
        <ul>
          {dirs.map((d) => <TreeNode key={d.path} entry={d} current={current} depth={depth + 1} />)}
        </ul>
      )}
    </li>
  );
}

// ---- a folder ----

type Upload = { id: number; name: string; progress: number; error?: string; done?: boolean };
let uploadIds = 0;

type DialogState =
  | { kind: "mkdir" }
  | { kind: "rename"; entry: Entry }
  | { kind: "move" | "copy"; entries: Entry[] }
  | { kind: "delete"; entries: Entry[] }
  | { kind: "replace"; files: File[]; names: string[] };

export function FolderView({ path }: { path: string }) {
  const q = useListing(path);
  const me = useMe();
  const qc = useQueryClient();
  const link = useFilesLink();
  const top = path === "/";
  const readOnly = useReadOnly(path);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [dialog, setDialog] = useState<DialogState | null>(null);
  const [uploads, setUploads] = useState<Upload[]>([]);
  const [dragging, setDragging] = useState(false);
  const picker = useRef<HTMLInputElement>(null);

  const entries = q.data ?? [];
  const chosen = entries.filter((e) => selected.has(e.path));
  const refresh = () => qc.invalidateQueries({ queryKey: ["dav"] });
  const toggle = (p: string, on: boolean) =>
    setSelected((s) => {
      const n = new Set(s);
      if (on) n.add(p);
      else n.delete(p);
      return n;
    });

  // Uploads go one at a time, each one PUT with progress.
  const startUploads = async (files: File[]) => {
    const batch = files.map((f) => ({ id: ++uploadIds, name: f.name, progress: 0 }));
    setUploads((u) => [...u.filter((x) => !x.done), ...batch]);
    const set = (id: number, v: Partial<Upload>) => setUploads((u) => u.map((x) => (x.id === id ? { ...x, ...v } : x)));
    for (const [i, f] of files.entries()) {
      const { id } = batch[i];
      try {
        await upload(path + f.name, f, (progress) => set(id, { progress }));
        set(id, { progress: 1, done: true });
      } catch (e) {
        set(id, { error: (e as Error).message });
      }
      qc.invalidateQueries({ queryKey: ["dav", path] });
    }
  };
  const addFiles = (files: File[]) => {
    if (!files.length) return;
    const taken = new Set(entries.map((e) => e.name));
    const clash = files.filter((f) => taken.has(f.name)).map((f) => f.name);
    if (clash.length) setDialog({ kind: "replace", files, names: clash });
    else void startUploads(files);
  };
  const onDrop = (e: DragEvent) => {
    e.preventDefault();
    setDragging(false);
    if (readOnly) return;
    const files: File[] = [];
    let folders = 0;
    for (const item of Array.from(e.dataTransfer.items)) {
      if (item.kind !== "file") continue;
      if (item.webkitGetAsEntry()?.isDirectory) folders++;
      else {
        const f = item.getAsFile();
        if (f) files.push(f);
      }
    }
    if (folders) toast.error("Folders can't be uploaded yet; drop the files inside them instead.");
    addFiles(files);
  };

  /** Runs one step per entry, then reports failures together. */
  const runAll = async (entries: Entry[], step: (e: Entry) => Promise<void>, done: string) => {
    const errors: string[] = [];
    for (const e of entries) await step(e).catch((err: Error) => errors.push(err.message));
    setSelected(new Set());
    refresh();
    if (errors.length) toast.error(errors.join("\n"));
    else toast.success(done);
  };

  const mountInfo = (name: string) => me.data?.mounts.find((m) => m.name === name);

  return (
    <div
      onDragOver={(e) => {
        if (readOnly || !e.dataTransfer.types.includes("Files")) return;
        e.preventDefault();
        setDragging(true);
      }}
      onDragLeave={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node)) setDragging(false);
      }}
      onDrop={onDrop}
      className={cn("flex-1 rounded-lg", dragging && "outline-2 -outline-offset-2 outline-primary outline-dashed")}
    >
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <Crumbs path={path} />
        {!readOnly && (
          <div className="flex gap-2">
            <Button variant="outline" size="sm" onClick={() => setDialog({ kind: "mkdir" })}>
              <FolderPlus /> New folder
            </Button>
            <Button size="sm" onClick={() => picker.current?.click()}>
              <Upload /> Upload
            </Button>
            <input
              ref={picker}
              type="file"
              multiple
              hidden
              onChange={(e) => {
                addFiles(Array.from(e.target.files ?? []));
                e.target.value = "";
              }}
            />
          </div>
        )}
      </div>

      {uploads.length > 0 && <Uploads items={uploads} onClear={() => setUploads((u) => u.filter((x) => !x.done && !x.error))} />}

      {chosen.length > 0 && (
        <div className="mb-3 flex flex-wrap items-center gap-2 rounded-md border bg-muted/50 px-3 py-2 text-sm">
          <span className="mr-auto">{chosen.length} selected</span>
          {!readOnly && (
            <Button variant="outline" size="sm" onClick={() => setDialog({ kind: "move", entries: chosen })}>
              <FolderInput /> Move
            </Button>
          )}
          <Button variant="outline" size="sm" onClick={() => setDialog({ kind: "copy", entries: chosen })}>
            <Copy /> Copy
          </Button>
          {!readOnly && (
            <Button variant="outline" size="sm" className="text-destructive" onClick={() => setDialog({ kind: "delete", entries: chosen })}>
              <Trash2 /> Delete
            </Button>
          )}
          <Button variant="ghost" size="icon-sm" aria-label="Clear selection" onClick={() => setSelected(new Set())}>
            <X />
          </Button>
        </div>
      )}

      {q.isPending ? (
        <Loading />
      ) : q.error ? (
        <p role="alert" className="text-sm text-destructive">{q.error.message}</p>
      ) : entries.length === 0 ? (
        <div className="rounded-lg border border-dashed p-10 text-center text-sm text-muted-foreground">
          {top ? "No mounts yet. An admin adds them." : readOnly ? "This folder is empty." : "This folder is empty. Drop files here or use Upload."}
        </div>
      ) : (
        <div className="rounded-md border">
          <Table>
            <TableHeader>
              <TableRow>
                {!top && (
                  <TableHead className="w-10">
                    <Checkbox
                      aria-label="Select all"
                      checked={chosen.length === entries.length ? true : chosen.length ? "indeterminate" : false}
                      onCheckedChange={(on) => setSelected(on === true ? new Set(entries.map((e) => e.path)) : new Set())}
                    />
                  </TableHead>
                )}
                <TableHead>Name</TableHead>
                <TableHead className="hidden w-24 text-right sm:table-cell">Size</TableHead>
                <TableHead className="hidden w-44 lg:table-cell">Modified</TableHead>
                <TableHead className="w-10" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {entries.map((e) => (
                <TableRow key={e.path} data-state={selected.has(e.path) ? "selected" : undefined}>
                  {!top && (
                    <TableCell>
                      <Checkbox aria-label={`Select ${e.name}`} checked={selected.has(e.path)} onCheckedChange={(on) => toggle(e.path, on === true)} />
                    </TableCell>
                  )}
                  <TableCell className="max-w-0">
                    <Link to={link(e.path)} className="flex min-w-0 items-center gap-2 hover:underline">
                      {top ? <HardDrive className="size-4 shrink-0 text-muted-foreground" /> : e.dir ? <Folder className="size-4 shrink-0 text-muted-foreground" /> : <File className="size-4 shrink-0 text-muted-foreground" />}
                      <span className="truncate">{e.name}</span>
                      {top && mountInfo(e.name)?.readOnly && <Badge variant="secondary">read-only</Badge>}
                    </Link>
                  </TableCell>
                  <TableCell className="hidden text-right text-muted-foreground tabular-nums sm:table-cell">{e.dir ? "" : formatSize(e.size)}</TableCell>
                  <TableCell className="hidden text-muted-foreground lg:table-cell">{e.dir ? "" : formatTime(e.modified)}</TableCell>
                  <TableCell>
                    {!top && (
                      <RowMenu
                        entry={e}
                        readOnly={readOnly}
                        onRename={() => setDialog({ kind: "rename", entry: e })}
                        onMove={() => setDialog({ kind: "move", entries: [e] })}
                        onCopy={() => setDialog({ kind: "copy", entries: [e] })}
                        onDelete={() => setDialog({ kind: "delete", entries: [e] })}
                      />
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      {dialog?.kind === "mkdir" && (
        <NameDialog
          title="New folder"
          action="Create"
          taken={entries.map((e) => e.name)}
          onClose={() => setDialog(null)}
          onSubmit={async (name) => {
            await mkcol(path + name + "/");
            refresh();
          }}
        />
      )}
      {dialog?.kind === "rename" && (
        <NameDialog
          title={`Rename ${dialog.entry.name}`}
          action="Rename"
          initial={dialog.entry.name}
          taken={entries.map((e) => e.name).filter((n) => n !== dialog.entry.name)}
          onClose={() => setDialog(null)}
          onSubmit={async (name) => {
            if (name === dialog.entry.name) return;
            await transfer("MOVE", dialog.entry.path, path + name + (dialog.entry.dir ? "/" : ""));
            refresh();
          }}
        />
      )}
      {(dialog?.kind === "move" || dialog?.kind === "copy") && (
        <DestinationDialog
          kind={dialog.kind}
          entries={dialog.entries}
          from={path}
          onClose={() => setDialog(null)}
          onPick={(dest) => {
            const { kind, entries } = dialog;
            setDialog(null);
            const method = kind === "move" ? "MOVE" : "COPY";
            void runAll(entries, (e) => transfer(method, e.path, dest + e.name + (e.dir ? "/" : "")), `${kind === "move" ? "Moved" : "Copied"} to ${dest}`);
          }}
        />
      )}
      <Confirm
        open={dialog?.kind === "delete"}
        onOpenChange={(o) => !o && setDialog(null)}
        title={dialog?.kind === "delete" && dialog.entries.length === 1 ? `Delete ${dialog.entries[0].name}?` : `Delete ${dialog?.kind === "delete" ? dialog.entries.length : 0} items?`}
        description={
          dialog?.kind === "delete" && dialog.entries.some((e) => e.dir)
            ? "Folders are deleted with everything in them. This can't be undone."
            : "This can't be undone."
        }
        action="Delete"
        onConfirm={() => dialog?.kind === "delete" && void runAll(dialog.entries, (e) => remove(e.path), "Deleted")}
      />
      <Confirm
        open={dialog?.kind === "replace"}
        onOpenChange={(o) => !o && setDialog(null)}
        title={dialog?.kind === "replace" && dialog.names.length === 1 ? `Replace ${dialog.names[0]}?` : `Replace ${dialog?.kind === "replace" ? dialog.names.length : 0} files?`}
        description={
          dialog?.kind === "replace" ? (
            <>
              Files with these names are already here and will be overwritten: <span className="break-all">{dialog.names.join(", ")}</span>
            </>
          ) : null
        }
        action="Replace"
        onConfirm={() => dialog?.kind === "replace" && void startUploads(dialog.files)}
      />
    </div>
  );
}

function Uploads({ items, onClear }: { items: Upload[]; onClear: () => void }) {
  const finished = items.every((x) => x.done || x.error);
  return (
    <div className="mb-3 rounded-md border px-3 py-2 text-sm">
      <div className="mb-1 flex items-center justify-between">
        <span className="font-medium">Uploads</span>
        {finished && (
          <Button variant="ghost" size="xs" onClick={onClear}>
            Clear
          </Button>
        )}
      </div>
      <ul className="grid gap-1.5">
        {items.map((u) => (
          <li key={u.id} className="grid gap-1">
            <div className="flex justify-between gap-2">
              <span className="truncate">{u.name}</span>
              <span className={cn("shrink-0 tabular-nums", u.error ? "text-destructive" : "text-muted-foreground")}>
                {u.error ? "Failed" : u.done ? "Done" : `${Math.round(u.progress * 100)}%`}
              </span>
            </div>
            {u.error ? (
              <p className="text-xs break-words text-destructive">{u.error}</p>
            ) : (
              !u.done && (
                <div className="h-1 overflow-hidden rounded bg-muted" role="progressbar" aria-label={`Uploading ${u.name}`} aria-valuenow={Math.round(u.progress * 100)}>
                  <div className="h-full bg-primary transition-[width]" style={{ width: `${u.progress * 100}%` }} />
                </div>
              )
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}

function RowMenu(props: { entry: Entry; readOnly: boolean; onRename: () => void; onMove: () => void; onCopy: () => void; onDelete: () => void }) {
  const { entry: e, readOnly } = props;
  // Dialogs open once the menu has closed: while it closes, the menu still
  // holds focus and would pull it out of the dialog's name field.
  const pending = useRef<(() => void) | null>(null);
  const dialog = (open: () => void) => () => {
    pending.current = open;
  };
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon-sm" aria-label={`Actions for ${e.name}`}>
          <MoreHorizontal />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent
        align="end"
        onCloseAutoFocus={(ev) => {
          const open = pending.current;
          pending.current = null;
          if (open) {
            ev.preventDefault();
            open();
          }
        }}
      >
        {!e.dir && (
          <>
            <DropdownMenuItem asChild>
              <a href={rawPath(e.path)} target="_blank" rel="noreferrer">
                <ExternalLink /> Raw
              </a>
            </DropdownMenuItem>
            <DropdownMenuSeparator />
          </>
        )}
        {!readOnly && (
          <DropdownMenuItem onSelect={dialog(props.onRename)}>
            <Pencil /> Rename
          </DropdownMenuItem>
        )}
        {!readOnly && (
          <DropdownMenuItem onSelect={dialog(props.onMove)}>
            <FolderInput /> Move
          </DropdownMenuItem>
        )}
        <DropdownMenuItem onSelect={dialog(props.onCopy)}>
          <Copy /> Copy
        </DropdownMenuItem>
        {!readOnly && (
          <DropdownMenuItem variant="destructive" onSelect={dialog(props.onDelete)}>
            <Trash2 /> Delete
          </DropdownMenuItem>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function NameDialog(props: {
  title: string;
  action: string;
  initial?: string;
  taken: string[];
  onClose: () => void;
  onSubmit: (name: string) => Promise<void>;
}) {
  const [name, setName] = useState(props.initial ?? "");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const n = name.trim();
    const bad = badName(n) || (props.taken.includes(n) ? "Something with that name is already here." : "");
    if (bad) return setError(bad);
    setBusy(true);
    try {
      await props.onSubmit(n);
      props.onClose();
    } catch (err) {
      setError((err as Error).message);
      setBusy(false);
    }
  };
  return (
    <Dialog open onOpenChange={(o) => !o && props.onClose()}>
      <DialogContent
        onOpenAutoFocus={(e) => {
          // Select the name without its extension, as file managers do.
          e.preventDefault();
          const el = input.current!;
          el.focus();
          const dot = el.value.lastIndexOf(".");
          el.setSelectionRange(0, dot > 0 ? dot : el.value.length);
        }}
      >
        <form onSubmit={submit} className="grid gap-4">
          <DialogHeader>
            <DialogTitle>{props.title}</DialogTitle>
          </DialogHeader>
          <div className="grid gap-2">
            <Label htmlFor="name">Name</Label>
            <Input
              id="name"
              ref={input}
              value={name}
              onChange={(e) => {
                setName(e.target.value);
                setError("");
              }}
              aria-invalid={!!error}
            />
            {error && <p className="text-sm text-destructive">{error}</p>}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={props.onClose}>Cancel</Button>
            <Button type="submit" disabled={busy}>{props.action}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/** Picks the folder to move or copy into; it can't be the top level or a read-only mount. */
function DestinationDialog(props: { kind: "move" | "copy"; entries: Entry[]; from: string; onClose: () => void; onPick: (dest: string) => void }) {
  const [at, setAt] = useState(props.from);
  const q = useListing(at);
  const me = useMe();
  const readOnly = !mountOf(at) || !!me.data?.mounts.find((m) => m.name === mountOf(at))?.readOnly;
  // A folder can't go inside itself.
  const inside = props.entries.some((e) => e.dir && at.startsWith(e.path));
  const same = props.kind === "move" && at === props.from;
  const what = props.entries.length === 1 ? props.entries[0].name : `${props.entries.length} items`;
  const verb = props.kind === "move" ? "Move" : "Copy";
  return (
    <Dialog open onOpenChange={(o) => !o && props.onClose()}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle className="break-all">{verb} {what}</DialogTitle>
          <DialogDescription>Choose the folder to {props.kind} into.</DialogDescription>
        </DialogHeader>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="icon-sm" aria-label="Up one folder" disabled={at === "/"} onClick={() => setAt(parentOf(at))}>
            <ArrowUp />
          </Button>
          <code className="min-w-0 truncate text-sm">{at === "/" ? "Files" : at}</code>
        </div>
        <div className="h-72 overflow-y-auto rounded-md border">
          {q.isPending ? (
            <p className="p-4 text-sm text-muted-foreground">Listing…</p>
          ) : q.error ? (
            <p className="p-4 text-sm text-destructive">{q.error.message}</p>
          ) : (
            <ul className="divide-y text-sm">
              {q.data.filter((e) => e.dir).map((e) => (
                <li key={e.path}>
                  <button type="button" className="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-accent" onClick={() => setAt(e.path)}>
                    {at === "/" ? <HardDrive className="size-4 shrink-0 text-muted-foreground" /> : <Folder className="size-4 shrink-0 text-muted-foreground" />}
                    <span className="truncate">{e.name}</span>
                  </button>
                </li>
              ))}
              {q.data.every((e) => !e.dir) && <li className="p-4 text-muted-foreground">No folders here.</li>}
            </ul>
          )}
        </div>
        {readOnly && at !== "/" && <p className="text-sm text-muted-foreground">This mount is read-only.</p>}
        <DialogFooter>
          <Button variant="outline" onClick={props.onClose}>Cancel</Button>
          <Button disabled={readOnly || inside || same} onClick={() => props.onPick(at)}>
            {verb} here
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
