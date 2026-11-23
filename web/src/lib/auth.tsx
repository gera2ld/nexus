import { useQueryClient } from "@tanstack/react-query";
import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";
import { refresh, setUnauthorizedHandler, signIn as pbSignIn, token, type Account } from "@/api/client";

type Auth = {
  /** null while checking a stored token on load. */
  signedIn: boolean | null;
  /** The signed-in account, once known. */
  account: Account | null;
  signIn: (email: string, password: string) => Promise<void>;
  signOut: () => void;
};

const AuthContext = createContext<Auth | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const qc = useQueryClient();
  const [signedIn, setSignedIn] = useState<boolean | null>(token.get() ? null : false);
  const [account, setAccount] = useState<Account | null>(null);

  const signOut = useCallback(() => {
    token.clear();
    qc.clear();
    setSignedIn(false);
  }, [qc]);

  useEffect(() => {
    setUnauthorizedHandler(signOut);
    // A stored token may have expired; refreshing checks it and extends it.
    if (token.get())
      refresh().then((a) => {
        setAccount(a);
        setSignedIn(true);
      }, signOut);
  }, [signOut]);

  const signIn = useCallback(async (email: string, password: string) => {
    setAccount(await pbSignIn(email, password));
    setSignedIn(true);
  }, []);

  return <AuthContext.Provider value={{ signedIn, account, signIn, signOut }}>{children}</AuthContext.Provider>;
}

export function useAuth() {
  const a = useContext(AuthContext);
  if (!a) throw new Error("useAuth outside AuthProvider");
  return a;
}
