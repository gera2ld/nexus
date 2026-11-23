import { Plus } from "lucide-react";
import { useState, type FormEvent } from "react";
import { api, unwrap, type AppPassword } from "@/api/client";
import { Confirm } from "@/components/Confirm";
import { CopyField } from "@/components/CopyField";
import { Loading, PageHeader } from "@/components/Layout";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { useAction, useMe, usePasswords } from "@/hooks/queries";
import { formatDate, scopeNames } from "@/lib/format";

type Scope = AppPassword["scopes"][number];
const allScopes: Scope[] = ["cal", "card", "files"];
type Values = { label: string; scopes: Scope[]; paths: string[] };

export function AppPasswords() {
  const list = usePasswords();
  const me = useMe();
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<AppPassword | null>(null);
  const [revoking, setRevoking] = useState<AppPassword | null>(null);
  const [created, setCreated] = useState<AppPassword | null>(null);
  const [showRevoked, setShowRevoked] = useState(false);

  const create = useAction((body: Values) => unwrap(api.POST("/app-passwords", { body })), ["app-passwords"], undefined, true);
  const update = useAction(
    ({ id, ...body }: Values & { id: string }) => unwrap(api.PATCH("/app-passwords/{id}", { params: { path: { id } }, body })),
    ["app-passwords"],
    "App Password saved",
    true,
  );
  const revoke = useAction(
    (id: string) => unwrap(api.POST("/app-passwords/{id}/revoke", { params: { path: { id } } })),
    ["app-passwords"],
    "App Password revoked",
  );

  if (!list.data) return <Loading error={list.error} />;
  const active = list.data.filter((p) => !p.revokedAt);
  const revoked = list.data.filter((p) => p.revokedAt);
  const shown = showRevoked ? list.data : active;

  return (
    <>
      <PageHeader
        title="App Passwords"
        description="Each app or device gets its own password, limited to what it needs. Revoking one stops only that app."
      >
        <Button
          onClick={() => {
            create.reset();
            setCreating(true);
          }}
        >
          <Plus /> New App Password
        </Button>
      </PageHeader>

      {shown.length === 0 ? (
        <p className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground">
          {revoked.length ? "No active App Passwords." : "No App Passwords yet. Create one for each app you connect."}
        </p>
      ) : (
        <div className="rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Can use</TableHead>
                <TableHead className="hidden md:table-cell">Created</TableHead>
                <TableHead className="hidden md:table-cell">Last used</TableHead>
                <TableHead />
              </TableRow>
            </TableHeader>
            <TableBody>
              {shown.map((p) => (
                <TableRow key={p.id} className={p.revokedAt ? "text-muted-foreground" : undefined}>
                  <TableCell className="font-medium">
                    {p.label}
                    {p.revokedAt && <Badge variant="outline" className="ml-2">Revoked</Badge>}
                  </TableCell>
                  <TableCell>
                    <div className="flex flex-wrap gap-1">
                      {p.scopes.map((s) => (
                        <Badge key={s} variant="secondary">{scopeNames[s]}</Badge>
                      ))}
                    </div>
                    {p.paths.length > 0 && (
                      <div className="mt-1 font-mono text-xs text-muted-foreground">only {p.paths.join(", ")}</div>
                    )}
                  </TableCell>
                  <TableCell className="hidden md:table-cell">{formatDate(p.created)}</TableCell>
                  <TableCell className="hidden md:table-cell">{p.lastUsedAt ? formatDate(p.lastUsedAt) : "Never"}</TableCell>
                  <TableCell className="text-right whitespace-nowrap">
                    {!p.revokedAt && (
                      <>
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => {
                            update.reset();
                            setEditing(p);
                          }}
                        >
                          Edit
                        </Button>
                        <Button variant="ghost" size="sm" className="text-destructive" onClick={() => setRevoking(p)}>Revoke</Button>
                      </>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      {revoked.length > 0 && (
        <Button variant="link" className="mt-2 px-0" onClick={() => setShowRevoked(!showRevoked)}>
          {showRevoked ? "Hide" : "Show"} {revoked.length} revoked
        </Button>
      )}

      <PasswordForm
        key={creating ? "new" : "closed"}
        open={creating}
        onOpenChange={setCreating}
        title="New App Password"
        submit="Create"
        busy={create.isPending}
        error={create.error?.message}
        initial={{ label: "", scopes: ["cal", "card"], paths: [] }}
        onSubmit={(v) =>
          create.mutate(v, {
            onSuccess: (p) => {
              setCreating(false);
              setCreated(p);
            },
          })
        }
      />
      {editing && (
        <PasswordForm
          key={editing.id}
          open
          onOpenChange={(o) => !o && setEditing(null)}
          title={`Edit ${editing.label}`}
          description="Changes apply at once. The password itself stays the same."
          submit="Save"
          busy={update.isPending}
          error={update.error?.message}
          initial={editing}
          onSubmit={(v) => update.mutate({ id: editing.id, ...v }, { onSuccess: () => setEditing(null) })}
        />
      )}
      <Confirm
        open={!!revoking}
        onOpenChange={(o) => !o && setRevoking(null)}
        title={`Revoke ${revoking?.label}?`}
        description="Apps using this password stop syncing at once. This can't be undone; create a new one to reconnect."
        action="Revoke"
        onConfirm={() => revoking && revoke.mutate(revoking.id)}
      />
      <Dialog open={!!created} onOpenChange={(o) => !o && setCreated(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{created?.label} is ready</DialogTitle>
            <DialogDescription>Copy the password into the app now. It won't be shown again.</DialogDescription>
          </DialogHeader>
          <div className="grid gap-3 text-sm">
            <Field label="Server"><CopyField value={me.data?.server ?? ""} label="Server address" /></Field>
            <Field label="User name"><CopyField value={me.data?.email ?? ""} label="User name" /></Field>
            <Field label="Password"><CopyField value={created?.secret ?? ""} label="Password" /></Field>
          </div>
          <DialogFooter>
            <Button onClick={() => setCreated(null)}>Done</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid gap-1">
      <span className="text-muted-foreground">{label}</span>
      {children}
    </div>
  );
}

function PasswordForm(props: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: string;
  submit: string;
  busy: boolean;
  /** The last save's error, shown in the form. */
  error?: string;
  initial: Values;
  onSubmit: (v: Values) => void;
}) {
  const [scopes, setScopes] = useState<Scope[]>(props.initial.scopes);

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const f = new FormData(e.currentTarget);
    const paths = String(f.get("paths"))
      .split(/[\n,]/)
      .map((s) => s.trim())
      .filter(Boolean);
    props.onSubmit({ label: String(f.get("label")).trim(), scopes, paths });
  }

  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent>
        <form onSubmit={submit} className="grid gap-5">
          <DialogHeader>
            <DialogTitle>{props.title}</DialogTitle>
            {props.description && <DialogDescription>{props.description}</DialogDescription>}
          </DialogHeader>
          <div className="grid gap-2">
            <Label htmlFor="pw-label">Name</Label>
            <Input id="pw-label" name="label" defaultValue={props.initial.label} placeholder="e.g. Phone, Laptop Thunderbird" required maxLength={100} autoFocus />
          </div>
          <fieldset className="grid gap-2">
            <legend className="mb-2 text-sm font-medium">Can use</legend>
            {allScopes.map((s) => (
              <Label key={s} className="font-normal">
                <Checkbox
                  checked={scopes.includes(s)}
                  onCheckedChange={(c) => setScopes(c ? [...scopes, s] : scopes.filter((x) => x !== s))}
                />
                {scopeNames[s]}
              </Label>
            ))}
          </fieldset>
          <div className="grid gap-2">
            <Label htmlFor="pw-paths">Only these paths (optional)</Label>
            <Textarea
              id="pw-paths"
              name="paths"
              defaultValue={props.initial.paths.join("\n")}
              placeholder={"/calendars/personal/\n/files/docs/"}
              className="font-mono text-sm"
              rows={3}
            />
            <p className="text-xs text-muted-foreground">
              One per line, under /files/, /calendars/ or /contacts/. A /dav prefix is removed. Leave empty to allow
              everything the boxes above allow.
            </p>
          </div>
          {props.error && (
            <p role="alert" className="text-sm text-destructive">
              {props.error}
            </p>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => props.onOpenChange(false)}>Cancel</Button>
            <Button type="submit" disabled={props.busy || scopes.length === 0}>{props.submit}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
