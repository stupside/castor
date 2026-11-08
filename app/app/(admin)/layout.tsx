import { Shell } from "@/components/shell";

// Every admin page shares the shell; @modal shows an intercepted route over the page it was opened from.
export default function AdminLayout({
  children,
  modal,
}: {
  children: React.ReactNode;
  modal: React.ReactNode;
}) {
  return (
    <>
      <Shell>{children}</Shell>
      {modal}
    </>
  );
}
