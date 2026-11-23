import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { CalendarDays, Contact, ExternalLink, FolderOpen, HardDrive, Users } from "lucide-react";
import { createBrowserRouter, RouterProvider } from "react-router";
import { ApiError, realm } from "@/api/client";
import { Layout, Loading, NotFound, type NavItem } from "@/components/Layout";
import { Toaster } from "@/components/ui/sonner";
import { AuthProvider, useAuth } from "@/lib/auth";
import { Mounts } from "@/pages/admin/Mounts";
import { Storage } from "@/pages/admin/Storage";
import { Settings } from "@/pages/Settings";
import { Files } from "@/pages/files/Files";
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
  { to: "/files", label: "Files", icon: FolderOpen },
  { to: "/account/calendars", label: "Calendars", icon: CalendarDays },
  { to: "/account/contacts", label: "Contacts", icon: Contact },
];

const adminNav: NavItem[] = [
  { to: "/admin", label: "Storage", icon: HardDrive, end: true },
  { to: "/admin/mounts", label: "Mounts", icon: Users },
];

// Users, logs and backups stay in PocketBase's own admin UI.
const pocketbaseLink = (
  <a href="/_/" className="flex items-center gap-1 hover:text-foreground" title="Users, logs and backups">
    PocketBase <ExternalLink className="size-3.5" />
  </a>
);

// The user pages (/, /account and /files) and /admin are separate sign-ins (users and superusers), so each
// page load gets only its own routes; moving between them is a page load.
const router = createBrowserRouter(
  realm === "users"
    ? [
        {
          // The overview is the home page; the rest live under /account.
          path: "/",
          element: <Layout title="Nexus" home="/" nav={accountNav} settings="/account/settings" />,
          children: [
            { index: true, element: <Overview /> },
            { path: "account/settings", element: <Settings /> },
            { path: "account/calendars", element: <Collections key="calendars" kind="calendars" /> },
            { path: "account/contacts", element: <Collections key="addressbooks" kind="addressbooks" /> },
            // /files shows the mounts too, so there's nothing to redirect.
            { path: "files", element: <Files /> },
            { path: "files/:uid/*", element: <Files /> },
            { path: "*", element: <NotFound home="/" /> },
          ],
        },
      ]
    : [
        {
          path: "/admin",
          element: <Layout title="Nexus admin" home="/admin" nav={adminNav} extra={pocketbaseLink} />,
          children: [
            // Storage is the admin home page, as the overview is the user's.
            { index: true, element: <Storage /> },
            { path: "mounts", element: <Mounts /> },
            { path: "*", element: <NotFound home="/admin" /> },
          ],
        },
      ],
);

function App() {
  const { signedIn } = useAuth();
  if (signedIn === null)
    return (
      <div className="p-6">
        <Loading />
      </div>
    );
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
