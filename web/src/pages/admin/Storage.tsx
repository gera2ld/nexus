import { FolderOpen, Plus, Trash2 } from "lucide-react";
import { useId, useMemo, useState, type FormEvent } from "react";
import { api, unwrap, type OptionInfo, type StorageAccount } from "@/api/client";
import { Confirm } from "@/components/Confirm";
import { Loading, PageHeader } from "@/components/Layout";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { NativeSelect } from "@/components/ui/native-select";
import { firstLine, forProvider, typeNames, useAccounts, useStorageOptions } from "@/hooks/admin";
import { useAction } from "@/hooks/queries";
import { FolderBrowser } from "./FolderBrowser";

type Type = "local" | "s3";

export function Storage() {
  const list = useAccounts();
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<StorageAccount | null>(null);
  const [browsing, setBrowsing] = useState<StorageAccount | null>(null);
  const [deleting, setDeleting] = useState<StorageAccount | null>(null);
  const remove = useAction(
    (id: string) => unwrap(api.DELETE("/admin/storage-accounts/{id}", { params: { path: { id } } })),
    ["storage-accounts"],
    "Storage account deleted",
  );

  if (!list.data) return <Loading error={list.error} />;
  return (
    <>
      <PageHeader
        title="Storage"
        description="Where files live. A mount gives one user a folder of a storage account; the same account can back many mounts."
      >
        <Button onClick={() => setCreating(true)}>
          <Plus /> New storage account
        </Button>
      </PageHeader>
      {list.data.length === 0 ? (
        <p className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground">
          No storage accounts yet. Add a local disk or an S3 account, then give users mounts on it.
        </p>
      ) : (
        <div className="grid gap-3">
          {list.data.map((a) => (
            <Card key={a.id} className="py-4">
              <CardContent className="flex flex-wrap items-start gap-x-4 gap-y-2">
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
                    <span className="font-medium">{a.name}</span>
                    <Badge variant="secondary">{typeNames[a.type]}</Badge>
                    <span className="text-sm text-muted-foreground">
                      {a.mounts === 0 ? "No mounts" : a.mounts === 1 ? "1 mount" : `${a.mounts} mounts`}
                    </span>
                  </div>
                  {Object.keys(a.options).length > 0 && (
                    <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 font-mono text-xs">
                      {Object.entries(a.options).map(([k, v]) => (
                        <div key={k} className="contents">
                          <dt className="text-muted-foreground">{k}</dt>
                          <dd className="break-all">{v}</dd>
                        </div>
                      ))}
                    </dl>
                  )}
                  {a.secrets.length > 0 && (
                    <p className="mt-1 text-xs text-muted-foreground">
                      Stored encrypted: <span className="font-mono">{a.secrets.join(", ")}</span>
                    </p>
                  )}
                </div>
                <div className="flex gap-1">
                  <Button variant="ghost" size="sm" onClick={() => setBrowsing(a)}>
                    <FolderOpen /> Browse
                  </Button>
                  <Button variant="ghost" size="sm" onClick={() => setEditing(a)}>
                    Edit
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="text-destructive"
                    disabled={a.mounts > 0}
                    title={a.mounts > 0 ? "Delete or move its mounts first" : undefined}
                    onClick={() => setDeleting(a)}
                  >
                    Delete
                  </Button>
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}

      {creating && <AccountForm onClose={() => setCreating(false)} onSaved={(a) => setBrowsing(a)} />}
      {editing && <AccountForm key={editing.id} account={editing} onClose={() => setEditing(null)} />}
      {browsing && <FolderBrowser account={browsing} onClose={() => setBrowsing(null)} />}
      <Confirm
        open={!!deleting}
        onOpenChange={(o) => !o && setDeleting(null)}
        title={`Delete ${deleting?.name}?`}
        description="Nexus forgets the account and its secrets. The files on it are not touched."
        action="Delete"
        onConfirm={() => deleting && remove.mutate(deleting.id)}
      />
    </>
  );
}

type Row = { key: number; name: string; value: string };

/**
 * Create or edit an account. The main options come from rclone for the
 * chosen type (and S3 provider); everything else goes under Advanced as
 * name and value. Secrets are write-only: an empty field keeps what's
 * stored.
 */
function AccountForm({
  account,
  onClose,
  onSaved,
}: {
  account?: StorageAccount;
  onClose: () => void;
  onSaved?: (a: StorageAccount) => void;
}) {
  const [type, setType] = useState<Type>((account?.type as Type) ?? "s3");
  const options = useStorageOptions(type);
  const byName = useMemo(() => new Map((options.data ?? []).map((o) => [o.name, o])), [options.data]);
  const basic = (options.data ?? []).filter((o) => !o.advanced);

  const [values, setValues] = useState<Record<string, string>>(() => ({ ...account?.options }));
  const [secrets, setSecrets] = useState<Record<string, string>>({}); // new values; "" on a stored one removes it
  const stored = new Set(account?.secrets ?? []);
  const provider = values.provider ?? "";
  // Advanced rows: options that aren't shown as main fields.
  const [rows, setRows] = useState<Row[]>(() =>
    Object.entries(account?.options ?? {})
      .filter(([k]) => k !== "provider")
      .map(([name, value], i) => ({ key: i, name, value })),
  );
  const shownBasic = basic.filter((o) => forProvider(o.provider, provider));
  const basicNames = new Set(shownBasic.map((o) => o.name));
  const advancedRows = rows.filter((r) => !basicNames.has(r.name));

  const save = useAction(
    (v: { name: string; options: Record<string, string>; secrets: Record<string, string> }) =>
      account
        ? unwrap(api.PATCH("/admin/storage-accounts/{id}", { params: { path: { id: account.id } }, body: v }))
        : unwrap(api.POST("/admin/storage-accounts", { body: { ...v, type } })),
    ["storage-accounts"],
    account ? "Storage account saved" : "Storage account created",
  );

  function setValue(name: string, value: string) {
    setValues((v) => ({ ...v, [name]: value }));
    setRows((rs) =>
      rs.some((r) => r.name === name) ? rs.map((r) => (r.name === name ? { ...r, value } : r)) : [...rs, { key: Date.now(), name, value }],
    );
  }

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const name = String(new FormData(e.currentTarget).get("name")).trim();
    const opts: Record<string, string> = {};
    const sec: Record<string, string> = {};
    const put = (k: string, v: string) => {
      if (!k || !v) return;
      if (byName.get(k)?.secret) sec[k] = v;
      else opts[k] = v;
    };
    if (provider) opts.provider = provider;
    for (const o of shownBasic) if (!o.secret) put(o.name, values[o.name] ?? "");
    for (const r of advancedRows) put(r.name.trim(), r.value.trim());
    for (const [k, v] of Object.entries(secrets)) {
      if (v) sec[k] = v;
      else if (stored.has(k)) sec[k] = ""; // removes it
    }
    save.mutate(
      { name, options: opts, secrets: sec },
      {
        onSuccess: (a) => {
          onClose();
          onSaved?.(a);
        },
      },
    );
  }

  const listId = useId();
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-xl">
        <form onSubmit={submit} className="grid gap-5">
          <DialogHeader>
            <DialogTitle>{account ? `Edit ${account.name}` : "New storage account"}</DialogTitle>
            <DialogDescription>
              {account
                ? "Mounts on it pick up the change on their next request."
                : "After saving, Nexus lists the account so you can see the connection works."}
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-2">
            <Label htmlFor="acc-name">Name</Label>
            <Input
              id="acc-name"
              name="name"
              defaultValue={account?.name}
              required
              maxLength={100}
              autoFocus
              placeholder="e.g. NAS, Backblaze"
            />
          </div>
          {!account && (
            <fieldset className="grid gap-2">
              <legend className="mb-2 text-sm font-medium">Type</legend>
              <div className="flex gap-4">
                {(["s3", "local"] as Type[]).map((t) => (
                  <Label key={t} className="font-normal">
                    <input type="radio" name="type" value={t} checked={type === t} onChange={() => setType(t)} className="accent-primary" />
                    {typeNames[t]}
                  </Label>
                ))}
              </div>
            </fieldset>
          )}
          {type === "local" && (
            <p className="text-sm text-muted-foreground">
              Local disk needs no settings. Each mount names an absolute folder on the server.
            </p>
          )}
          {options.error && <p className="text-sm text-destructive">{options.error.message}</p>}
          {shownBasic.map((o) => (
            <OptionField
              key={o.name}
              option={o}
              provider={provider}
              value={o.secret ? (secrets[o.name] ?? "") : (values[o.name] ?? "")}
              stored={stored.has(o.name) && secrets[o.name] !== ""}
              removed={stored.has(o.name) && secrets[o.name] === ""}
              onChange={(v) => (o.secret ? setSecrets((s) => ({ ...s, [o.name]: v })) : setValue(o.name, v))}
              onRemove={() => setSecrets((s) => ({ ...s, [o.name]: "" }))}
            />
          ))}

          {options.data && options.data.some((o) => o.advanced) && (
            <details className="group rounded-md border px-3 py-2" open={advancedRows.length > 0}>
              <summary className="cursor-pointer text-sm font-medium">Advanced options</summary>
              <div className="mt-3 grid gap-2">
                <p className="text-xs text-muted-foreground">
                  Any other rclone option for {typeNames[type]}, by name. Secret ones are stored encrypted.
                </p>
                <datalist id={listId}>
                  {options.data
                    .filter((o) => o.advanced)
                    .map((o) => (
                      <option key={o.name} value={o.name}>
                        {firstLine(o.help)}
                      </option>
                    ))}
                </datalist>
                {advancedRows.map((r) => (
                  <div key={r.key} className="flex gap-2">
                    <Input
                      aria-label="Option name"
                      list={listId}
                      value={r.name}
                      className="font-mono text-xs"
                      onChange={(e) => setRows((rs) => rs.map((x) => (x.key === r.key ? { ...x, name: e.target.value } : x)))}
                    />
                    <Input
                      aria-label={`Value of ${r.name || "option"}`}
                      type={byName.get(r.name)?.secret ? "password" : "text"}
                      value={r.value}
                      className="font-mono text-xs"
                      onChange={(e) => setRows((rs) => rs.map((x) => (x.key === r.key ? { ...x, value: e.target.value } : x)))}
                    />
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      aria-label={`Remove ${r.name || "option"}`}
                      onClick={() => setRows((rs) => rs.filter((x) => x.key !== r.key))}
                    >
                      <Trash2 />
                    </Button>
                  </div>
                ))}
                {[...stored]
                  .filter((k) => !basicNames.has(k))
                  .map((k) => (
                    <div key={k} className="flex items-center gap-2 text-xs">
                      <span className="font-mono">{k}</span>
                      <span className="text-muted-foreground">{secrets[k] === "" ? "will be removed" : "stored encrypted"}</span>
                      {secrets[k] !== "" && (
                        <Button
                          type="button"
                          variant="link"
                          size="sm"
                          className="h-auto p-0 text-xs"
                          onClick={() => setSecrets((s) => ({ ...s, [k]: "" }))}
                        >
                          Remove
                        </Button>
                      )}
                    </div>
                  ))}
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="justify-self-start"
                  onClick={() => setRows((rs) => [...rs, { key: Date.now(), name: "", value: "" }])}
                >
                  <Plus /> Add option
                </Button>
              </div>
            </details>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={save.isPending}>
              {account ? "Save" : "Create"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function OptionField(props: {
  option: OptionInfo;
  provider: string;
  value: string;
  stored: boolean;
  removed: boolean;
  onChange: (v: string) => void;
  onRemove: () => void;
}) {
  const { option: o } = props;
  const id = useId();
  const examples = o.examples.filter((e) => forProvider(e.provider, props.provider));
  const help = firstLine(o.help);
  const label = (
    <Label htmlFor={id} className="flex-wrap gap-x-2">
      <span className="font-mono">{o.name}</span>
      {o.secret && <Badge variant="outline">secret</Badge>}
    </Label>
  );
  // A yes/no option is a select of yes, no and the default.
  if (o.type === "bool" && !o.examples.length) {
    examples.push({ value: "true", help: "Yes", provider: "" }, { value: "false", help: "No", provider: "" });
  }
  // A fixed list (provider, ACLs, yes/no) is a select; otherwise examples are suggestions.
  const field =
    o.exclusive || o.name === "provider" || o.type === "bool" ? (
      <NativeSelect
        id={id}
        value={props.value}
        onChange={(e) => props.onChange(e.target.value)}
        required={o.required || o.name === "provider"}
      >
        <option value="">{o.default ? `Default (${o.default})` : "Choose…"}</option>
        {examples.map((e) => (
          <option key={e.value} value={e.value}>
            {e.value === firstLine(e.help) || !e.help ? e.value : `${e.value}: ${firstLine(e.help)}`}
          </option>
        ))}
      </NativeSelect>
    ) : (
      <>
        <Input
          id={id}
          type={o.secret ? "password" : "text"}
          autoComplete={o.secret ? "new-password" : "off"}
          value={props.value}
          onChange={(e) => props.onChange(e.target.value)}
          list={examples.length ? `${id}-ex` : undefined}
          placeholder={o.secret ? (props.stored ? "Stored; leave empty to keep" : "") : o.default && o.default !== "false" ? o.default : ""}
          className="font-mono"
        />
        {examples.length > 0 && (
          <datalist id={`${id}-ex`}>
            {examples.map((e) => (
              <option key={e.value} value={e.value}>
                {firstLine(e.help)}
              </option>
            ))}
          </datalist>
        )}
      </>
    );
  return (
    <div className="grid gap-1.5">
      {label}
      {field}
      <p className="text-xs text-muted-foreground">
        {help}
        {props.stored && !props.value && (
          <>
            {" "}
            <Button type="button" variant="link" size="sm" className="h-auto p-0 text-xs" onClick={props.onRemove}>
              Remove the stored value
            </Button>
          </>
        )}
        {props.removed && " The stored value will be removed."}
      </p>
    </div>
  );
}
