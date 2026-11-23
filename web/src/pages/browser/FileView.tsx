import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Download, ExternalLink, File, Link2, Save } from "lucide-react";
import { lazy, Suspense, useEffect, useRef, useState } from "react";
import { useBlocker } from "react-router";
import { toast } from "sonner";
import { DavError, parentOf, rawPath, rawURL, readText, stat, writeText, type Entry } from "@/api/dav";
import { Confirm } from "@/components/Confirm";
import { Loading } from "@/components/Layout";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { copy } from "@/hooks/use-copy";
import { formatSize, formatTime } from "@/lib/format";
import { FolderView } from "./Browser";
import { Crumbs, useReadOnly } from "./common";

const Editor = lazy(() => import("./Editor"));

/** Files over this open read-only (docs/WEB.md §6.2). */
const EDIT_LIMIT = 5 << 20;
/** Files over this aren't loaded into the page at all; they'd stall the tab. */
const OPEN_LIMIT = 50 << 20;
/** How much of an unknown file is read to tell text from binary. */
const SNIFF_BYTES = 8192;

type Kind = "image" | "video" | "audio" | "pdf" | "text" | "unknown";

/** What a file is from its type; "unknown" files are sniffed for text. */
function kindOf(e: Entry): Kind {
  const t = e.type.split(";")[0].trim().toLowerCase();
  if (t.startsWith("image/")) return "image";
  if (t.startsWith("video/")) return "video";
  if (t.startsWith("audio/")) return "audio";
  if (t === "application/pdf") return "pdf";
  if (t.startsWith("text/") || /[/+](json|xml|yaml|toml|javascript|ecmascript|x-sh|x-shellscript|sql|graphql|x-ndjson)$/.test(t)) return "text";
  return "unknown";
}

/** A file under /browser: a preview, the editor for text, or a download. */
export function FileView({ path }: { path: string }) {
  const st = useQuery({ queryKey: ["dav-stat", path], queryFn: () => stat(path), retry: false, gcTime: 0 });
  if (st.data?.dir) return <FolderView path={path + "/"} />;
  if (!st.data) {
    return (
      <>
        <Crumbs path={path} />
        <div className="mt-4">{st.error ? <p role="alert" className="text-sm text-destructive">{st.error.message}</p> : <Loading />}</div>
      </>
    );
  }
  const e = st.data;
  const kind = kindOf(e);
  // The raw file is read with the session cookie; the ETag in the address
  // makes the browser fetch it again after a change.
  const src = `${rawPath(path)}?v=${encodeURIComponent(e.etag)}`;
  if (kind === "text" || kind === "unknown") return <TextFile entry={e} sniff={kind === "unknown"} />;
  return (
    <>
      <FileHeader entry={e} />
      <div className="grid place-items-center rounded-lg border bg-muted/30 p-4">
        {kind === "image" && <img src={src} alt={e.name} className="max-h-[75vh] max-w-full object-contain" />}
        {kind === "video" && <video src={src} controls className="max-h-[75vh] max-w-full" />}
        {kind === "audio" && <audio src={src} controls className="w-full max-w-xl" />}
        {kind === "pdf" && <iframe src={src} title={e.name} className="h-[80vh] w-full rounded bg-white" />}
      </div>
    </>
  );
}

function FileHeader({ entry: e, children }: { entry: Entry; children?: React.ReactNode }) {
  return (
    <div className="mb-4 grid gap-3">
      <Crumbs path={e.path} />
      <div className="flex flex-wrap items-center gap-2">
        <span className="mr-auto text-sm text-muted-foreground">
          {formatSize(e.size)} · {formatTime(e.modified)}
        </span>
        {children}
        <Button variant="outline" size="sm" asChild>
          <a href={rawPath(e.path) + "?download"}>
            <Download /> Download
          </a>
        </Button>
        <Button variant="outline" size="sm" asChild>
          <a href={rawPath(e.path)} target="_blank" rel="noreferrer">
            <ExternalLink /> Open raw
          </a>
        </Button>
        <Button variant="outline" size="sm" onClick={() => copy(rawURL(e.path), "Raw link copied")}>
          <Link2 /> Copy raw link
        </Button>
      </div>
    </div>
  );
}

function NoPreview({ entry, why }: { entry: Entry; why: string }) {
  return (
    <>
      <FileHeader entry={entry} />
      <div className="grid place-items-center gap-2 rounded-lg border border-dashed p-12 text-center text-sm text-muted-foreground">
        <File className="size-8" />
        <p>{why}</p>
      </div>
    </>
  );
}

type Loaded =
  | { ok: true; text: string; etag: string }
  | { ok: false; why: string; text?: undefined; etag?: undefined };

async function load(e: Entry, sniff: boolean): Promise<Loaded> {
  if (sniff && e.size > 0) {
    const head = await readText(e.path, SNIFF_BYTES);
    if (head.bytes.includes(0)) return { ok: false, why: "No preview for this kind of file." };
  }
  if (e.size > OPEN_LIMIT) return { ok: false, why: `This file is too large to open here (${formatSize(e.size)}). Download it instead.` };
  const { bytes, etag } = await readText(e.path);
  try {
    return { ok: true, text: new TextDecoder("utf-8", { fatal: true }).decode(bytes), etag };
  } catch {
    return { ok: false, why: "This file isn't UTF-8 text, so it can't be edited here." };
  }
}

function TextFile({ entry: e, sniff }: { entry: Entry; sniff: boolean }) {
  const mountReadOnly = useReadOnly(e.path);
  const tooBig = e.size > EDIT_LIMIT;
  const readOnly = mountReadOnly || tooBig;
  const qc = useQueryClient();
  // The header's size and time, and the folder's listing, after a change.
  const refreshInfo = () => {
    void qc.invalidateQueries({ queryKey: ["dav-stat", e.path] });
    void qc.invalidateQueries({ queryKey: ["dav", parentOf(e.path)] });
  };
  const q = useQuery({ queryKey: ["dav-text", e.path], queryFn: () => load(e, sniff), retry: false, gcTime: 0, staleTime: Infinity, structuralSharing: false });

  const [etag, setEtag] = useState("");
  const [saved, setSaved] = useState("");
  const draft = useRef("");
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);
  const [conflict, setConflict] = useState(false);
  // Each load from the server starts a fresh editor.
  const [generation, setGeneration] = useState(0);

  useEffect(() => {
    if (!q.data?.ok) return;
    setEtag(q.data.etag);
    setSaved(q.data.text);
    draft.current = q.data.text;
    setDirty(false);
    setGeneration((g) => g + 1);
  }, [q.data]);

  // Unsaved changes: ask before leaving the page or the app's route.
  useEffect(() => {
    if (!dirty) return;
    const warn = (ev: BeforeUnloadEvent) => ev.preventDefault();
    addEventListener("beforeunload", warn);
    return () => removeEventListener("beforeunload", warn);
  }, [dirty]);
  const blocker = useBlocker(dirty);

  const save = async (overwrite = false) => {
    if (readOnly || saving) return;
    const text = draft.current;
    setSaving(true);
    try {
      setEtag(await writeText(e.path, text, overwrite ? undefined : etag));
      setSaved(text);
      setDirty(draft.current !== text);
      refreshInfo();
      toast.success("Saved");
    } catch (err) {
      if (err instanceof DavError && err.status === 412) setConflict(true);
      else toast.error((err as Error).message);
    } finally {
      setSaving(false);
    }
  };

  if (!q.data) {
    return (
      <>
        <FileHeader entry={e} />
        {q.error ? <p role="alert" className="text-sm text-destructive">{q.error.message}</p> : <Loading />}
      </>
    );
  }
  if (!q.data.ok) return <NoPreview entry={e} why={q.data.why} />;

  return (
    <>
      <FileHeader entry={e}>
        {!readOnly && (
          <Button size="sm" disabled={!dirty || saving} onClick={() => void save()}>
            <Save /> {saving ? "Saving…" : dirty ? "Save" : "Saved"}
          </Button>
        )}
      </FileHeader>
      {tooBig && !mountReadOnly && (
        <p className="mb-3 text-sm text-muted-foreground">This file is over {formatSize(EDIT_LIMIT)}, so it opens read-only.</p>
      )}
      <div className="h-[70vh] overflow-hidden rounded-lg border">
        <Suspense fallback={<p className="p-4 text-sm text-muted-foreground">Loading the editor…</p>}>
          <Editor
            key={generation}
            name={e.name}
            value={saved}
            readOnly={readOnly}
            onChange={(v) => {
              draft.current = v;
              setDirty(v !== saved);
            }}
            onSave={() => void save()}
          />
        </Suspense>
      </div>

      <AlertDialog open={conflict} onOpenChange={setConflict}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>The file changed on the server</AlertDialogTitle>
            <AlertDialogDescription>
              Someone or something saved {e.name} after you opened it. Reload to get their version and lose your changes, or overwrite it with
              yours.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <Button
              variant="outline"
              onClick={() => {
                setConflict(false);
                void q.refetch();
                refreshInfo();
              }}
            >
              Reload
            </Button>
            <Button
              variant="destructive"
              onClick={() => {
                setConflict(false);
                void save(true);
              }}
            >
              Overwrite
            </Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      <Confirm
        open={blocker.state === "blocked"}
        onOpenChange={(o) => !o && blocker.reset?.()}
        title="Discard your changes?"
        description={`${e.name} has changes that aren't saved.`}
        action="Discard"
        onConfirm={() => blocker.proceed?.()}
      />
    </>
  );
}
