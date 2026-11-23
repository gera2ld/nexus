import { toast } from "sonner";

export function copy(text: string, what = "Copied") {
  navigator.clipboard.writeText(text).then(
    () => toast.success(what),
    () => toast.error("Couldn't copy; select the text and copy it yourself."),
  );
}
