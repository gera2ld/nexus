import { useQuery } from "@tanstack/react-query";
import { ArrowUp, File, Folder } from "lucide-react";
import { useState, type FormEvent } from "react";
import { api, unwrap, type StorageAccount } from "@/api/client";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { parentPath, joinPath } from "@/hooks/admin";
import { formatSize } from "@/lib/format";

/**
 * Lists a storage account's folders. Without onPick it is the connection
 * test; with it, the remote path picker for a mount.
 */
export function FolderBrowser(props: {
  account: StorageAccount;
  initialPath?: string;
  onPick?: (path: string) => void;
  onClose: () => void;
}) {
  const { account } = props;
  const [path, setPath] = useState(props.initialPath ?? "");
  const [typed, setTyped] = useState(props.initialPath ?? "");
  const list = useQuery({
    queryKey: ["browse", account.id, path],
    queryFn: () => unwrap(api.POST("/admin/storage-accounts/{id}/browse", { params: { path: { id: account.id } }, body: { path } })),
    retry: false,
  });
  // The server fills in the top folder (/ for local disk).
  const shown = list.data?.path ?? path;

  const go = (p: string) => {
    setPath(p);
    setTyped(p);
  };
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setPath(typed.trim());
  };
  const atTop = account.type === "local" ? shown === "/" : shown === "";

  return (
    <Dialog open onOpenChange={(o) => !o && props.onClose()}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{props.onPick ? "Choose a folder" : `Browse ${account.name}`}</DialogTitle>
          <DialogDescription>
            {account.type === "s3" ? "Paths are bucket/prefix. The top lists your buckets." : "Paths are absolute folders on the server."}
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="flex gap-2">
          <Button type="button" variant="outline" size="icon" aria-label="Up one folder" disabled={atTop} onClick={() => go(parentPath(account.type, shown))}>
            <ArrowUp />
          </Button>
          <Input value={typed} onChange={(e) => setTyped(e.target.value)} className="font-mono" aria-label="Path" placeholder={account.type === "s3" ? "bucket/prefix" : "/"} />
          <Button type="submit" variant="outline">Go</Button>
        </form>
        <div className="h-80 overflow-y-auto rounded-md border">
          {list.isPending ? (
            <p className="p-4 text-sm text-muted-foreground">Listing…</p>
          ) : list.error ? (
            <div role="alert" className="p-4 text-sm">
              <p className="font-medium text-destructive">Couldn't list this folder.</p>
              <p className="mt-1 font-mono text-xs break-all text-muted-foreground">{list.error.message}</p>
            </div>
          ) : list.data.entries.length === 0 ? (
            <p className="p-4 text-sm text-muted-foreground">Empty folder. The connection works.</p>
          ) : (
            <ul className="divide-y text-sm">
              {list.data.entries.map((e) => (
                <li key={e.name}>
                  {e.dir ? (
                    <button type="button" className="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-accent" onClick={() => go(joinPath(account.type, shown, e.name))}>
                      <Folder className="size-4 shrink-0 text-muted-foreground" />
                      <span className="truncate">{e.name}</span>
                    </button>
                  ) : (
                    <div className="flex items-center gap-2 px-3 py-2 text-muted-foreground">
                      <File className="size-4 shrink-0" />
                      <span className="truncate">{e.name}</span>
                      <span className="ml-auto shrink-0 text-xs tabular-nums">{formatSize(e.size)}</span>
                    </div>
                  )}
                </li>
              ))}
              {list.data.truncated && <li className="px-3 py-2 text-xs text-muted-foreground">Only the first 1000 entries are shown.</li>}
            </ul>
          )}
        </div>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={props.onClose}>{props.onPick ? "Cancel" : "Close"}</Button>
          {props.onPick && (
            <Button type="button" disabled={!list.data || atTop} onClick={() => props.onPick!(shown)}>
              Use this folder
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
