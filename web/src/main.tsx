import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { CalendarDays, Contact, ExternalLink, HardDrive, House, KeyRound, Users } from "lucide-react";
import { createBrowserRouter, Navigate, RouterProvider } from "react-router";
import { ApiError, realm } from "@/api/client";
import { Layout, Loading, type NavItem } from "@/components/Layout";
import { Toaster } from "@/components/ui/sonner";
import { AuthProvider, useAuth } from "@/lib/auth";
import { Mounts } from "@/pages/admin/Mounts";
import { Storage } from "@/pages/admin/Storage";
import { AppPasswords } from "@/pages/AppPasswords";
import { Collections } from "@/pages/Collections";
import { Overview } from "@/pages/Overview";
import { SignIn } from "@/pages/SignIn";
import "./index.css";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // A 4xx won't get better by asking again.
      retry: (n, e) => !(e instanceof ApiError && e.status < 500) && n < 2,
      refetchOnWindowFocus: false,
    },
  },
});

const accountNav: NavItem[] = [
  { to: "/account", label: "Overview", icon: House, end: true },
  { to: "/account/app-passwords", label: "App Passwords", icon: KeyRound },
  { to: "/account/calendars", label: "Calendars", icon: CalendarDays },
  { to: "/account/contacts", label: "Contacts", icon: Contact },
];

const adminNav: NavItem[] = [
  { to: "/admin/storage", label: "Storage", icon: HardDrive },
  { to: "/admin/mounts", label: "Mounts", icon: Users },
];

// Users, logs and backups stay in PocketBase's own admin UI.
const pocketbaseLink = (
  <a href="/_/" className="flex items-center gap-1 hover:text-foreground" title="Users, logs and backups">
    PocketBase <ExternalLink className="size-3.5" />
  </a>
);

// /account and /admin are separate sign-ins (users and superusers), so each
// page load gets only its own routes; moving between them is a page load.
const router = createBrowserRouter(
  realm === "users"
    ? [
        {
          path: "/account",
          element: <Layout title="Nexus" nav={accountNav} />,
          children: [
            { index: true, element: <Overview /> },
            { path: "app-passwords", element: <AppPasswords /> },
            { path: "calendars", element: <Collections key="calendars" kind="calendars" /> },
            { path: "contacts", element: <Collections key="addressbooks" kind="addressbooks" /> },
            { path: "*", element: <Navigate to="/account" replace /> },
          ],
        },
        { path: "*", element: <Navigate to="/account" replace /> },
      ]
    : [
        {
          path: "/admin",
          element: <Layout title="Nexus admin" nav={adminNav} extra={pocketbaseLink} />,
          children: [
            { index: true, element: <Navigate to="/admin/storage" replace /> },
            { path: "storage", element: <Storage /> },
            { path: "mounts", element: <Mounts /> },
            { path: "*", element: <Navigate to="/admin/storage" replace /> },
          ],
        },
        { path: "*", element: <Navigate to="/admin" replace /> },
      ],
);

function App() {
  const { signedIn } = useAuth();
  if (signedIn === null) return <div className="p-6"><Loading /></div>;
  if (!signedIn) return <SignIn />;
  return <RouterProvider router={router} />;
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <AuthProvider>
        <App />
      </AuthProvider>
      <Toaster position="bottom-right" />
    </QueryClientProvider>
  </StrictMode>,
);
