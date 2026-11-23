import { ChevronRight } from "lucide-react";
import { Fragment } from "react";
import { Link } from "react-router";
import { mountOf } from "@/api/dav";
import { useAuth } from "@/lib/auth";
import { useMe } from "@/hooks/queries";

/** The app's address for a path below /dav/files/{uid}: /files/{uid}/…. */
export function useFilesLink() {
  const { account } = useAuth();
  return (p: string) => `/files/${account!.id}` + p.split("/").map(encodeURIComponent).join("/");
}

/** Whether writes are refused at p: the top level and read-only mounts. */
export function useReadOnly(p: string) {
  const me = useMe();
  const mount = mountOf(p);
  if (!mount) return true;
  return !!me.data?.mounts.find((m) => m.name === mount)?.readOnly;
}

/** Files › mount › folder › … for p; the last part isn't a link. */
export function Crumbs({ path }: { path: string }) {
  const link = useFilesLink();
  const parts = path.split("/").filter(Boolean);
  return (
    <nav aria-label="Folder" className="flex min-w-0 flex-wrap items-center gap-1 text-sm">
      {parts.length ? (
        <Link to={link("/")} className="text-muted-foreground hover:text-foreground">
          Files
        </Link>
      ) : (
        <span className="font-medium">Files</span>
      )}
      {parts.map((name, i) => {
        const last = i === parts.length - 1;
        const to = "/" + parts.slice(0, i + 1).join("/") + "/";
        return (
          <Fragment key={to}>
            <ChevronRight className="size-3.5 shrink-0 text-muted-foreground" />
            {last ? (
              <span className="font-medium break-all">{name}</span>
            ) : (
              <Link to={link(to)} className="break-all text-muted-foreground hover:text-foreground">
                {name}
              </Link>
            )}
          </Fragment>
        );
      })}
    </nav>
  );
}
