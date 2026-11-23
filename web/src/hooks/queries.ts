import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api, unwrap } from "@/api/client";

export const useMe = () => useQuery({ queryKey: ["me"], queryFn: () => unwrap(api.GET("/me")) });

export const usePasswords = () =>
  useQuery({ queryKey: ["app-passwords"], queryFn: () => unwrap(api.GET("/app-passwords")) });

export type Kind = "calendars" | "addressbooks";

export const useCollections = (kind: Kind) =>
  useQuery({
    queryKey: [kind],
    queryFn: () => (kind === "calendars" ? unwrap(api.GET("/calendars")) : unwrap(api.GET("/addressbooks"))),
  });

/** A mutation that refreshes `keys` afterwards and toasts its error. */
/** A mutation that refreshes keys and toasts; `quiet` leaves errors to the caller. */
export function useAction<A, R>(fn: (a: A) => Promise<R>, keys: string[], done?: string, quiet = false) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSuccess: () => {
      for (const k of keys) qc.invalidateQueries({ queryKey: [k] });
      if (done) toast.success(done);
    },
    onError: (e) => {
      if (!quiet) toast.error(e.message);
    },
  });
}
