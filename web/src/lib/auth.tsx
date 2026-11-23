import { useQueryClient } from "@tanstack/react-query";
import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";
import { refresh, setUnauthorizedHandler, signIn as pbSignIn, token } from "@/api/client";

type Auth = {
  /** null while checking a stored token on load. */
  signedIn: boolean | null;
  /** The signed-in account's email. */
  email: string;
  signIn: (email: string, password: string) => Promise<void>;
  signOut: () => void;
};

const AuthContext = createContext<Auth | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const qc = useQueryClient();
  const [signedIn, setSignedIn] = useState<boolean | null>(token.get() ? null : false);
  const [email, setEmail] = useState("");

  const signOut = useCallback(() => {
    token.clear();
    qc.clear();
    setSignedIn(false);
  }, [qc]);

  useEffect(() => {
    setUnauthorizedHandler(signOut);
    // A stored token may have expired; refreshing checks it and extends it.
    if (token.get())
      refresh().then((e) => {
        setEmail(e);
        setSignedIn(true);
      }, signOut);
  }, [signOut]);

  const signIn = useCallback(async (email: string, password: string) => {
    setEmail(await pbSignIn(email, password));
    setSignedIn(true);
  }, []);

  return <AuthContext.Provider value={{ signedIn, email, signIn, signOut }}>{children}</AuthContext.Provider>;
}

export function useAuth() {
  const a = useContext(AuthContext);
  if (!a) throw new Error("useAuth outside AuthProvider");
  return a;
}
