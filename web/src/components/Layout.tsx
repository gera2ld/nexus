import { LogOut, type LucideIcon } from "lucide-react";
import { Link, NavLink, Outlet, useLocation } from "react-router";
import { avatarURL, type Account } from "@/api/client";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useAuth } from "@/lib/auth";
import { cn } from "@/lib/utils";

export type NavItem = { to: string; label: string; icon: LucideIcon; end?: boolean };

export function Layout({
  title,
  home,
  nav,
  extra,
}: {
  title: string;
  /** Where the title links to. */
  home: string;
  nav: NavItem[];
  extra?: React.ReactNode;
}) {
  const { signOut, account } = useAuth();
  // The Files page fills the window: its tree and content scroll on their own.
  const fill = useLocation().pathname.startsWith("/files");
  const width = fill ? "" : "max-w-5xl";
  return (
    <div className={fill ? "flex h-svh flex-col" : "min-h-svh"}>
      <header className="shrink-0 border-b">
        <div className={cn("mx-auto flex flex-wrap items-center gap-x-6 gap-y-2 px-4 py-3", width)}>
          <Link to={home} className="flex items-center gap-2 font-semibold">
            <img src={`${import.meta.env.BASE_URL}favicon.svg`} alt="" className="size-6" />
            {title}
          </Link>
          <nav className="order-last flex w-full flex-wrap gap-1 md:order-none md:w-auto">
            {nav.map(({ to, label, icon: Icon, end }) => (
              <NavLink
                key={to}
                to={to}
                end={end}
                className={({ isActive }) =>
                  cn(
                    "flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-sm text-muted-foreground hover:bg-accent hover:text-accent-foreground",
                    isActive && "bg-accent font-medium text-accent-foreground",
                  )
                }
              >
                <Icon className="size-4" />
                {label}
              </NavLink>
            ))}
          </nav>
          <div className="ml-auto flex items-center gap-3 text-sm text-muted-foreground">
            {extra}
            {account && <AccountMenu account={account} onSignOut={signOut} />}
          </div>
        </div>
      </header>
      <main className={cn("mx-auto w-full px-4", fill ? "flex min-h-0 flex-1 flex-col py-4" : "py-6", width)}>
        <Outlet />
      </main>
    </div>
  );
}

/** Up to two initials from the name, else the email's first letter. */
function initials(a: Account) {
  const words = (a.name ?? "").trim().split(/\s+/).filter(Boolean);
  return (words.length ? words.slice(0, 2).map((w) => w[0]) : [a.email[0]]).join("").toUpperCase();
}

function AccountMenu({ account, onSignOut }: { account: Account; onSignOut: () => void }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        className="rounded-full outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50"
        aria-label={`Account: ${account.email}`}
      >
        <Avatar className="size-8">
          <AvatarImage src={avatarURL(account)} alt="" />
          <AvatarFallback className="text-xs font-medium">{initials(account)}</AvatarFallback>
        </Avatar>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-56">
        <DropdownMenuLabel className="font-normal">
          {account.name && <div className="text-sm font-medium break-words">{account.name}</div>}
          <div className="text-sm break-all text-muted-foreground">{account.email}</div>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={onSignOut}>
          <LogOut /> Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

export function PageHeader({ title, description, children }: { title: string; description?: string; children?: React.ReactNode }) {
  return (
    <div className="mb-6 flex flex-wrap items-end justify-between gap-4">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
        {description && <p className="mt-1 max-w-prose text-sm text-muted-foreground">{description}</p>}
      </div>
      {children}
    </div>
  );
}

export function NotFound({ home }: { home: string }) {
  return (
    <div className="py-16 text-center">
      <h1 className="text-2xl font-semibold tracking-tight">Not found</h1>
      <p className="mt-2 text-sm text-muted-foreground">There's no page at this address.</p>
      <Link to={home} className="mt-4 inline-block text-sm underline underline-offset-4">
        Go to the home page
      </Link>
    </div>
  );
}

export function Loading({ error }: { error?: Error | null }) {
  return <p className="text-sm text-muted-foreground">{error ? error.message : "Loading…"}</p>;
}
