import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, Navigate, RouterProvider } from "react-router";
import { ApiError } from "@/api/client";
import { Layout, Loading } from "@/components/Layout";
import { Toaster } from "@/components/ui/sonner";
import { AuthProvider, useAuth } from "@/lib/auth";
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

const router = createBrowserRouter([
  {
    path: "/account",
    element: <Layout />,
    children: [
      { index: true, element: <Overview /> },
      { path: "app-passwords", element: <AppPasswords /> },
      { path: "calendars", element: <Collections key="calendars" kind="calendars" /> },
      { path: "contacts", element: <Collections key="addressbooks" kind="addressbooks" /> },
      { path: "*", element: <Navigate to="/account" replace /> },
    ],
  },
  { path: "*", element: <Navigate to="/account" replace /> },
]);

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
