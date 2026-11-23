import createClient, { type Middleware } from "openapi-fetch";
import type { components, paths } from "./schema";

export type Me = components["schemas"]["Me"];
export type AppPassword = components["schemas"]["AppPassword"];
export type Collection = components["schemas"]["Collection"];
export type StorageAccount = components["schemas"]["StorageAccount"];
export type OptionInfo = components["schemas"]["OptionInfo"];
export type AdminMount = components["schemas"]["AdminMount"];
export type AdminUser = components["schemas"]["AdminUser"];
export type Listing = components["schemas"]["Listing"];
type Problem = components["schemas"]["ErrorModel"];

/**
 * Which PocketBase auth collection this page signs in to: /admin is for
 * superusers, everything else (/ and /account) for users. The two never share a token, and
 * moving between them is a full page load.
 */
export const realm: "users" | "_superusers" = location.pathname.startsWith("/admin") ? "_superusers" : "users";

const TOKEN_KEY = `nexus.token.${realm}`;

// The PocketBase token lives in sessionStorage: one sign-in per tab, gone
// when the tab closes.
export const token = {
  get: () => sessionStorage.getItem(TOKEN_KEY),
  set: (t: string) => sessionStorage.setItem(TOKEN_KEY, t),
  clear: () => sessionStorage.removeItem(TOKEN_KEY),
};

/** Called when the server says the token is no longer good. */
let onUnauthorized = () => {};
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn;
}

const auth: Middleware = {
  onRequest({ request }) {
    const t = token.get();
    if (t) request.headers.set("Authorization", `Bearer ${t}`);
    return request;
  },
  onResponse({ response }) {
    if (response.status === 401) onUnauthorized();
    return response;
  },
};

export const api = createClient<paths>({ baseUrl: "/api/nexus" });
api.use(auth);

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

/** The message to show for an RFC 9457 problem from the API. */
function problemMessage(p: Problem | undefined, status: number): string {
  if (!p) return `Request failed (${status}).`;
  // Huma's own validation names the field as a location like body.name.
  const details = (p.errors ?? [])
    .map((e) => (e.location ? `${e.location.replace(/^body\./, "")}: ${e.message}` : e.message))
    .filter(Boolean);
  if (details.length && p.detail === "validation failed") return details.join("; ");
  return [p.detail || p.title, ...details].filter(Boolean).join(": ");
}

/** Returns the data of an openapi-fetch result, or throws its error. */
export async function unwrap<T>(
  call: Promise<{ data?: T; error?: unknown; response: Response }>,
): Promise<T> {
  const { data, error, response } = await call;
  if (!response.ok) throw new ApiError(problemMessage(error as Problem, response.status), response.status);
  return data as T;
}

// ---- PocketBase auth (not part of the Nexus API) ----

/** The signed-in PocketBase account; superusers have no name or avatar. */
export type Account = { id: string; collectionId: string; email: string; name?: string; avatar?: string };

type AuthResponse = { token: string; record: Account };

/** The account's avatar image, if it has one. Avatars aren't protected files, so no token is needed. */
export function avatarURL(a: Account): string | undefined {
  return a.avatar ? `/api/files/${a.collectionId}/${a.id}/${encodeURIComponent(a.avatar)}` : undefined;
}

async function pb(path: string, init: RequestInit): Promise<AuthResponse> {
  const r = await fetch(`/api/collections/${realm}/${path}`, {
    ...init,
    headers: { "Content-Type": "application/json", ...init.headers },
  });
  const body = await r.json().catch(() => ({}));
  if (!r.ok) throw new ApiError(body.message || `Sign-in failed (${r.status}).`, r.status);
  return body as AuthResponse;
}

/** Signs in; returns the account. */
export async function signIn(email: string, password: string): Promise<Account> {
  const res = await pb("auth-with-password", {
    method: "POST",
    body: JSON.stringify({ identity: email, password }),
  }).catch((e: ApiError) => {
    throw e.status === 400 ? new ApiError("Wrong email or password.", 400) : e;
  });
  token.set(res.token);
  return res.record;
}

/** Swaps the stored token for a fresh one and returns the account; throws if it's no longer good. */
export async function refresh(): Promise<Account> {
  const t = token.get();
  if (!t) throw new ApiError("Not signed in.", 401);
  const res = await pb("auth-refresh", { method: "POST", headers: { Authorization: t } });
  token.set(res.token);
  return res.record;
}
