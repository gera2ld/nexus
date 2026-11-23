import { useQueryClient } from "@tanstack/react-query";
import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";
import { api, realm, refresh, setUnauthorizedHandler, signIn as pbSignIn, token, type Account } from "@/api/client";

/**
 * Sets (or clears) the cookie that lets raw file links open in a new tab
 * (docs/WEB.md §3). Users only; failures just mean raw links ask for a
 * password, so they're ignored.
 */
function syncSession(on: boolean) {
  if (realm !== "users") return Promise.resolve();
  // The token is read now: signing out clears it straight after.
  const headers = { Authorization: `Bearer ${token.get()}` };
  return (on ? api.POST("/session", { headers }) : api.DELETE("/session", { headers })).then(
    () => {},
    () => {},
  );
}

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
    // Clearing the cookie needs the token, so it goes first; an expired
    // token can't clear it, but then the cookie no longer works either.
    if (token.get()) void syncSession(false);
    token.clear();
    qc.clear();
    setSignedIn(false);
  }, [qc]);

  useEffect(() => {
    setUnauthorizedHandler(signOut);
    // A stored token may have expired; refreshing checks it and extends it.
    if (token.get())
      refresh().then((a) => {
        void syncSession(true);
        setAccount(a);
        setSignedIn(true);
      }, signOut);
  }, [signOut]);

  const signIn = useCallback(async (email: string, password: string) => {
    const a = await pbSignIn(email, password);
    void syncSession(true);
    setAccount(a);
    setSignedIn(true);
  }, []);

  return <AuthContext.Provider value={{ signedIn, account, signIn, signOut }}>{children}</AuthContext.Provider>;
}

export function useAuth() {
  const a = useContext(AuthContext);
  if (!a) throw new Error("useAuth outside AuthProvider");
  return a;
}
