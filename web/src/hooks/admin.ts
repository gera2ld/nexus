import { useQuery } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";

export const useAccounts = () =>
  useQuery({ queryKey: ["storage-accounts"], queryFn: () => unwrap(api.GET("/admin/storage-accounts")) });

export const useMounts = () => useQuery({ queryKey: ["mounts"], queryFn: () => unwrap(api.GET("/admin/mounts")) });

export const useUsers = () => useQuery({ queryKey: ["users"], queryFn: () => unwrap(api.GET("/admin/users")) });

export const useStorageOptions = (type: "local" | "s3") =>
  useQuery({
    queryKey: ["storage-options", type],
    queryFn: () => unwrap(api.GET("/admin/storage-types/{type}/options", { params: { path: { type } } })),
    staleTime: Infinity, // rclone's option list doesn't change while the server runs
  });

/**
 * rclone's provider filter: "a,b" or "!a,b"; empty applies to every
 * provider. With no provider chosen yet, only the unfiltered ones apply.
 */
export function forProvider(filter: string, provider: string): boolean {
  if (!filter) return true;
  if (!provider) return false;
  const neg = filter.startsWith("!");
  const hit = filter.replace(/^!/, "").split(",").includes(provider);
  return neg ? !hit : hit;
}

/** The first line of rclone's help text. */
export const firstLine = (help: string) => help.split("\n")[0].trim();

// Remote paths as mounts use them: absolute for local disk, bucket/prefix
// for S3 ("" lists the buckets).
export function joinPath(type: string, dir: string, name: string) {
  if (type === "local") return dir.endsWith("/") ? dir + name : `${dir}/${name}`;
  return dir ? `${dir}/${name}` : name;
}

export function parentPath(type: string, p: string) {
  const i = p.replace(/\/+$/, "").lastIndexOf("/");
  if (type === "local") return i <= 0 ? "/" : p.slice(0, i);
  return i < 0 ? "" : p.slice(0, i);
}

export const typeNames: Record<string, string> = { local: "Local disk", s3: "S3" };
