import { Copy } from "lucide-react";
import { Button } from "@/components/ui/button";
import { copy } from "@/hooks/use-copy";

/** A value shown in monospace with a copy button. */
export function CopyField({ value, label }: { value: string; label?: string }) {
  return (
    <div className="flex min-w-0 items-start gap-1">
      <code className="min-w-0 rounded bg-muted px-2 py-1 font-mono text-sm break-all">{value}</code>
      <Button
        variant="ghost"
        size="icon"
        className="size-7 shrink-0"
        aria-label={`Copy ${label ?? "value"}`}
        onClick={() => copy(value, `${label ?? "Value"} copied`)}
      >
        <Copy className="size-3.5" />
      </Button>
    </div>
  );
}
