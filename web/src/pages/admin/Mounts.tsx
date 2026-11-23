import { FolderOpen, Plus } from "lucide-react";
import { useState, type FormEvent } from "react";
import { Link } from "react-router";
import { api, unwrap, type AdminMount } from "@/api/client";
import { Confirm } from "@/components/Confirm";
import { Loading, PageHeader } from "@/components/Layout";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { NativeSelect } from "@/components/ui/native-select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { typeNames, useAccounts, useMounts, useUsers } from "@/hooks/admin";
import { useAction } from "@/hooks/queries";
import { FolderBrowser } from "./FolderBrowser";

export function Mounts() {
  const list = useMounts();
  const accounts = useAccounts();
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<AdminMount | null>(null);
  const [deleting, setDeleting] = useState<AdminMount | null>(null);
  const remove = useAction(
    (id: string) => unwrap(api.DELETE("/admin/mounts/{id}", { params: { path: { id } } })),
    ["mounts", "storage-accounts"],
    "Mount removed",
  );

  if (!list.data || !accounts.data) return <Loading error={list.error ?? accounts.error} />;
  const noAccounts = accounts.data.length === 0;
  return (
    <>
      <PageHeader title="Mounts" description="Each mount is a folder a user sees in Files. Removing a mount leaves the files where they are.">
        <Button onClick={() => setCreating(true)} disabled={noAccounts}>
          <Plus /> New mount
        </Button>
      </PageHeader>
      {noAccounts ? (
        <p className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground">
          Mounts need a storage account. <Link to="/admin" className="underline underline-offset-4">Add one first.</Link>
        </p>
      ) : list.data.length === 0 ? (
        <p className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground">No mounts yet. Users see no files until they have one.</p>
      ) : (
        <div className="rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>User</TableHead>
                <TableHead>Folder</TableHead>
                <TableHead>Storage</TableHead>
                <TableHead />
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.data.map((m, i) => (
                <TableRow key={m.id}>
                  <TableCell className={i > 0 && list.data[i - 1].user.id === m.user.id ? "text-muted-foreground/60" : undefined}>{m.user.email}</TableCell>
                  <TableCell>
                    <span className="font-mono">{m.name}</span>
                    {m.readOnly && <Badge variant="outline" className="ml-2">read-only</Badge>}
                  </TableCell>
                  <TableCell className="max-w-64 whitespace-normal">
                    <div>{m.account.name}</div>
                    <div className="font-mono text-xs break-all text-muted-foreground">{m.remotePath || "all buckets"}</div>
                  </TableCell>
                  <TableCell className="text-right whitespace-nowrap">
                    <Button variant="ghost" size="sm" onClick={() => setEditing(m)}>Edit</Button>
                    <Button variant="ghost" size="sm" className="text-destructive" onClick={() => setDeleting(m)}>Remove</Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      {creating && <MountForm onClose={() => setCreating(false)} />}
      {editing && <MountForm key={editing.id} mount={editing} onClose={() => setEditing(null)} />}
      <Confirm
        open={!!deleting}
        onOpenChange={(o) => !o && setDeleting(null)}
        title={`Remove ${deleting?.name} from ${deleting?.user.email}?`}
        description="Their apps lose this folder at once. The files stay on the storage account."
        action="Remove"
        onConfirm={() => deleting && remove.mutate(deleting.id)}
      />
    </>
  );
}

function MountForm({ mount, onClose }: { mount?: AdminMount; onClose: () => void }) {
  const users = useUsers();
  const accounts = useAccounts();
  const [userId, setUserId] = useState("");
  const [name, setName] = useState(mount?.name ?? "");
  const [accountId, setAccountId] = useState(mount?.account.id ?? accounts.data?.[0]?.id ?? "");
  const [remotePath, setRemotePath] = useState(mount?.remotePath ?? "");
  const [readOnly, setReadOnly] = useState(mount?.readOnly ?? false);
  const [picking, setPicking] = useState(false);
  const account = accounts.data?.find((a) => a.id === accountId);

  const save = useAction(
    () =>
      mount
        ? unwrap(api.PATCH("/admin/mounts/{id}", { params: { path: { id: mount.id } }, body: { name: name.trim(), account: accountId, remotePath, readOnly } }))
        : unwrap(api.POST("/admin/mounts", { body: { user: userId, name: name.trim(), account: accountId, remotePath, readOnly } })),
    ["mounts", "storage-accounts"],
    mount ? "Mount saved" : "Mount created",
  );

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    save.mutate(undefined, { onSuccess: onClose });
  }

  return (
    <>
      <Dialog open={!picking} onOpenChange={(o) => !o && onClose()}>
        <DialogContent>
          <form onSubmit={submit} className="grid gap-5">
            <DialogHeader>
              <DialogTitle>{mount ? `Edit ${mount.name}` : "New mount"}</DialogTitle>
              <DialogDescription>{mount ? `For ${mount.user.email}.` : "The user sees it as a folder in Files."}</DialogDescription>
            </DialogHeader>
            {!mount && (
              <div className="grid gap-2">
                <Label htmlFor="m-user">User</Label>
                <NativeSelect id="m-user" required value={userId} onChange={(e) => setUserId(e.target.value)}>
                  <option value="" disabled>{users.data ? "Choose a user…" : "Loading…"}</option>
                  {users.data?.map((u) => (
                    <option key={u.id} value={u.id}>{u.name ? `${u.name} <${u.email}>` : u.email}</option>
                  ))}
                </NativeSelect>
              </div>
            )}
            <div className="grid gap-2">
              <Label htmlFor="m-name">Folder name</Label>
              <Input
                id="m-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                required
                maxLength={64}
                pattern="[a-z0-9][a-z0-9._\-]*"
                title="Lower-case letters, digits, dot, underscore and hyphen"
                className="font-mono"
                placeholder="docs"
              />
              <p className="text-xs text-muted-foreground">Lower-case letters, digits, dot, underscore and hyphen. Renaming it changes the address apps use.</p>
            </div>
            <div className="grid gap-2">
              <Label htmlFor="m-account">Storage account</Label>
              <NativeSelect id="m-account" value={accountId} onChange={(e) => setAccountId(e.target.value)} required>
                {accounts.data?.map((a) => (
                  <option key={a.id} value={a.id}>{`${a.name} (${typeNames[a.type]})`}</option>
                ))}
              </NativeSelect>
            </div>
            <div className="grid gap-2">
              <Label htmlFor="m-path">Remote path</Label>
              <div className="flex gap-2">
                <Input
                  id="m-path"
                  value={remotePath}
                  onChange={(e) => setRemotePath(e.target.value)}
                  required={account?.type !== "s3"}
                  className="font-mono"
                  placeholder={account?.type === "s3" ? "All buckets" : "/srv/nexus/ada"}
                />
                <Button type="button" variant="outline" disabled={!account} onClick={() => setPicking(true)}>
                  <FolderOpen /> Browse
                </Button>
              </div>
              {account?.type === "s3" && (
                <p className="text-xs text-muted-foreground">
                  bucket or bucket/prefix. Empty shows every bucket the account can list, and folders at the top are buckets:
                  creating or deleting one there creates or deletes a bucket.
                </p>
              )}
            </div>
            <Label className="font-normal">
              <Checkbox checked={readOnly} onCheckedChange={(c) => setReadOnly(!!c)} />
              Read-only
            </Label>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={onClose}>Cancel</Button>
              <Button type="submit" disabled={save.isPending}>{mount ? "Save" : "Create"}</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      {picking && account && (
        <FolderBrowser
          account={account}
          initialPath={remotePath}
          onClose={() => setPicking(false)}
          onPick={(p) => {
            setRemotePath(p);
            setPicking(false);
          }}
        />
      )}
    </>
  );
}
