import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";

/** A destructive confirmation that opens next to its trigger. */
export function ConfirmPopover(props: {
  /** The button that opens it. */
  children: React.ReactNode;
  title: string;
  description: React.ReactNode;
  action: string;
  onConfirm: () => void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>{props.children}</PopoverTrigger>
      <PopoverContent align="end" className="grid w-72 gap-3">
        <div className="grid gap-1">
          <p className="text-sm font-medium break-words">{props.title}</p>
          <p className="text-sm text-muted-foreground">{props.description}</p>
        </div>
        <div className="flex justify-end gap-2">
          <Button variant="outline" size="sm" onClick={() => setOpen(false)}>
            Cancel
          </Button>
          <Button
            variant="destructive"
            size="sm"
            onClick={() => {
              setOpen(false);
              props.onConfirm();
            }}
          >
            {props.action}
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  );
}
