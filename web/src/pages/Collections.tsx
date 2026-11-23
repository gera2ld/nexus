import { Plus } from "lucide-react";
import { useState, type FormEvent } from "react";
import { api, unwrap, type Collection } from "@/api/client";
import { Confirm } from "@/components/Confirm";
import { CopyField } from "@/components/CopyField";
import { Loading, PageHeader } from "@/components/Layout";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { useAction, useCollections, type Kind } from "@/hooks/queries";

type Component = "VEVENT" | "VTODO";
type Values = { name: string; description: string; color: string; components: Component[] };

const text = {
  calendars: {
    title: "Calendars",
    description: "Apps find new calendars on their next sync. Deleting one here removes it from every app.",
    noun: "calendar",
    items: (n: number, c?: string[]) => {
      const [one, many] =
        c?.length === 1 ? (c[0] === "VEVENT" ? ["event", "events"] : ["task", "tasks"]) : ["event or task", "events and tasks"];
      return `${n} ${n === 1 ? one : many}`;
    },
  },
  addressbooks: {
    title: "Contacts",
    description: "Address books. Apps find new ones on their next sync. Deleting one here removes it from every app.",
    noun: "address book",
    items: (n: number, _c?: string[]) => (n === 1 ? "1 contact" : `${n} contacts`),
  },
};

// The typed client needs literal paths, so each call picks its own.
const calls = {
  create: (k: Kind, v: Values) =>
    k === "calendars"
      ? unwrap(
          api.POST("/calendars", {
            body: { name: v.name, description: v.description, color: v.color || undefined, components: v.components },
          }),
        )
      : unwrap(api.POST("/addressbooks", { body: { name: v.name, description: v.description } })),
  update: (k: Kind, slug: string, v: Values) => {
    const params = { path: { slug } };
    return k === "calendars"
      ? unwrap(api.PATCH("/calendars/{slug}", { params, body: { name: v.name, description: v.description, color: v.color } }))
      : unwrap(api.PATCH("/addressbooks/{slug}", { params, body: { name: v.name, description: v.description } }));
  },
  remove: (k: Kind, slug: string) => {
    const params = { path: { slug } };
    return k === "calendars" ? unwrap(api.DELETE("/calendars/{slug}", { params })) : unwrap(api.DELETE("/addressbooks/{slug}", { params }));
  },
};

export function Collections({ kind }: { kind: Kind }) {
  const t = text[kind];
  const list = useCollections(kind);
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<Collection | null>(null);
  const [deleting, setDeleting] = useState<Collection | null>(null);

  const create = useAction((v: Values) => calls.create(kind, v), [kind], `${cap(t.noun)} created`);
  const update = useAction(({ slug, v }: { slug: string; v: Values }) => calls.update(kind, slug, v), [kind], "Saved");
  const remove = useAction((slug: string) => calls.remove(kind, slug), [kind], `${cap(t.noun)} deleted`);

  if (!list.data) return <Loading error={list.error} />;
  return (
    <>
      <PageHeader title={t.title} description={t.description}>
        <Button onClick={() => setCreating(true)}>
          <Plus /> New {t.noun}
        </Button>
      </PageHeader>
      {list.data.length === 0 ? (
        <p className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground">No {t.noun}s.</p>
      ) : (
        <div className="grid gap-3">
          {list.data.map((c) => (
            <Card key={c.slug} className="py-4">
              <CardContent className="flex flex-wrap items-start gap-x-4 gap-y-2">
                {kind === "calendars" && (
                  <span
                    aria-hidden
                    className="mt-1.5 size-3 shrink-0 rounded-full border"
                    style={c.color ? { background: c.color.slice(0, 7), borderColor: "transparent" } : undefined}
                  />
                )}
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-baseline gap-x-2">
                    <span className="font-medium">{c.name || c.slug}</span>
                    <span className="text-sm text-muted-foreground">{t.items(c.items, c.components)}</span>
                    {c.components?.length === 1 && (
                      <Badge variant="secondary">{c.components[0] === "VEVENT" ? "Events only" : "Tasks only"}</Badge>
                    )}
                  </div>
                  {c.description && <p className="mt-0.5 text-sm text-muted-foreground">{c.description}</p>}
                  <div className="mt-2">
                    <CopyField value={c.url} label="Address" />
                  </div>
                </div>
                <div className="flex gap-1">
                  <Button variant="ghost" size="sm" onClick={() => setEditing(c)}>
                    Edit
                  </Button>
                  <Button variant="ghost" size="sm" className="text-destructive" onClick={() => setDeleting(c)}>
                    Delete
                  </Button>
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}

      <CollectionForm
        key={creating ? "new" : "closed"}
        kind={kind}
        open={creating}
        onOpenChange={setCreating}
        title={`New ${t.noun}`}
        submit="Create"
        busy={create.isPending}
        initial={{ name: "", description: "", color: kind === "calendars" ? "#3b82f6" : "", components: ["VEVENT", "VTODO"] }}
        onSubmit={(v) => create.mutate(v, { onSuccess: () => setCreating(false) })}
      />
      {editing && (
        <CollectionForm
          key={editing.slug}
          kind={kind}
          open
          onOpenChange={(o) => !o && setEditing(null)}
          title={`Edit ${editing.name || editing.slug}`}
          submit="Save"
          busy={update.isPending}
          editing
          initial={{
            name: editing.name,
            description: editing.description,
            color: editing.color ?? "",
            components: (editing.components ?? []) as Component[],
          }}
          onSubmit={(v) => update.mutate({ slug: editing.slug, v }, { onSuccess: () => setEditing(null) })}
        />
      )}
      <Confirm
        open={!!deleting}
        onOpenChange={(o) => !o && setDeleting(null)}
        title={`Delete ${deleting?.name || deleting?.slug}?`}
        description={
          deleting?.items
            ? `This deletes the ${t.noun} and the ${t.items(deleting.items, deleting.components)} in it, on the server and in every app that syncs it. It can't be undone.`
            : `The ${t.noun} is empty. It goes from the server and from every app that syncs it.`
        }
        action="Delete"
        onConfirm={() => deleting && remove.mutate(deleting.slug)}
      />
    </>
  );
}

function cap(s: string) {
  return s[0].toUpperCase() + s.slice(1);
}

function CollectionForm(props: {
  kind: Kind;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  submit: string;
  busy: boolean;
  editing?: boolean;
  initial: Values;
  onSubmit: (v: Values) => void;
}) {
  const isCal = props.kind === "calendars";
  const [color, setColor] = useState(props.initial.color);
  const [components, setComponents] = useState<Component[]>(props.initial.components);

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const f = new FormData(e.currentTarget);
    props.onSubmit({
      name: String(f.get("name")).trim(),
      description: String(f.get("description")).trim(),
      color,
      components,
    });
  }

  const toggle = (c: Component, on: boolean) => setComponents(on ? [...components, c] : components.filter((x) => x !== c));

  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent>
        <form onSubmit={submit} className="grid gap-5">
          <DialogHeader>
            <DialogTitle>{props.title}</DialogTitle>
            {!props.editing && <DialogDescription>The address is made from the name and doesn't change later.</DialogDescription>}
          </DialogHeader>
          <div className="grid gap-2">
            <Label htmlFor="c-name">Name</Label>
            <Input id="c-name" name="name" defaultValue={props.initial.name} required maxLength={255} autoFocus />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="c-desc">Description (optional)</Label>
            <Textarea id="c-desc" name="description" defaultValue={props.initial.description} rows={2} maxLength={2000} />
          </div>
          {isCal && (
            <div className="grid gap-2">
              <Label htmlFor="c-color">Colour</Label>
              <div className="flex items-center gap-3">
                <input
                  id="c-color"
                  type="color"
                  value={color ? color.slice(0, 7) : "#3b82f6"}
                  disabled={!color}
                  onChange={(e) => setColor(e.target.value)}
                  className="h-9 w-14 cursor-pointer rounded-md border bg-transparent p-1 disabled:cursor-not-allowed disabled:opacity-40"
                />
                <Label className="font-normal">
                  <Checkbox checked={!color} onCheckedChange={(c) => setColor(c ? "" : "#3b82f6")} />
                  No colour; let each app choose
                </Label>
              </div>
            </div>
          )}
          {isCal && !props.editing && (
            <fieldset className="grid gap-2">
              <legend className="mb-2 text-sm font-medium">Holds</legend>
              <Label className="font-normal">
                <Checkbox checked={components.includes("VEVENT")} onCheckedChange={(c) => toggle("VEVENT", !!c)} /> Events
              </Label>
              <Label className="font-normal">
                <Checkbox checked={components.includes("VTODO")} onCheckedChange={(c) => toggle("VTODO", !!c)} /> Tasks
              </Label>
            </fieldset>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => props.onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={props.busy || (isCal && components.length === 0)}>
              {props.submit}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
