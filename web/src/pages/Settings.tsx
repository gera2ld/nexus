import { PageHeader } from "@/components/Layout";
import { AppPasswords } from "@/pages/AppPasswords";

/** The user's settings, opened from the account menu. */
export function Settings() {
  return (
    <>
      <PageHeader title="Settings" />
      <section>
        <AppPasswords />
      </section>
    </>
  );
}
