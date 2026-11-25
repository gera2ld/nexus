import { Cloud, HardDrive, Server, type LucideProps } from "lucide-react";
import type { StorageType } from "@/api/client";

export const typeNames: Record<StorageType, string> = { local: "Local disk", s3: "S3", webdav: "WebDAV" };

const icons = { local: HardDrive, s3: Cloud, webdav: Server } satisfies Record<StorageType, unknown>;

/** A mount's icon, by its storage type; a plain drive while the type is unknown. */
export function MountIcon({ type, ...props }: { type?: StorageType } & LucideProps) {
  const Icon = (type && icons[type]) || HardDrive;
  return <Icon aria-label={type ? typeNames[type] : undefined} role={type ? "img" : undefined} {...props} />;
}
