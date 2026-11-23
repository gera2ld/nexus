import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { CopyField } from "@/components/CopyField";
import { Loading, PageHeader } from "@/components/Layout";
import { useMe } from "@/hooks/queries";
import { Link } from "react-router";

export function Overview() {
  const me = useMe();
  if (!me.data) return <Loading error={me.error} />;
  const { id, server, email, mounts } = me.data;
  return (
    <>
      <PageHeader title="Overview" description="What to type into your apps. Every app signs in with your email and an App Password." />
      <div className="grid gap-4 md:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Calendars and contacts</CardTitle>
            <CardDescription>
              For Thunderbird, DAVx⁵, Apple Calendar and the like. They find everything from the server address.
            </CardDescription>
          </CardHeader>
          <CardContent className="grid gap-3 text-sm">
            <Row label="Server">
              <CopyField value={server} label="Server address" />
            </Row>
            <Row label="User name">
              <CopyField value={email} label="User name" />
            </Row>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Files</CardTitle>
            <CardDescription>For Finder, rclone, Windows Explorer and other WebDAV clients.</CardDescription>
          </CardHeader>
          <CardContent className="grid gap-3 text-sm">
            <Row label="Address">
              <CopyField value={`${server}/dav/files/`} label="Files address" />
            </Row>
            <Row label="Mounts">
              {mounts.length === 0 ? (
                <span className="text-muted-foreground">None yet. An admin adds them.</span>
              ) : (
                <ul className="flex flex-wrap gap-1.5">
                  {mounts.map((m) => (
                    <li key={m.name}>
                      <Badge variant="secondary" asChild>
                        <Link to={`/files/${id}/${encodeURIComponent(m.name)}/`}>
                          {m.name}
                          {m.readOnly && <span className="font-normal text-muted-foreground"> · read-only</span>}
                        </Link>
                      </Badge>
                    </li>
                  ))}
                </ul>
              )}
            </Row>
          </CardContent>
        </Card>
      </div>
    </>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[6rem_1fr] items-center gap-2">
      <span className="text-muted-foreground">{label}</span>
      <div className="min-w-0">{children}</div>
    </div>
  );
}
