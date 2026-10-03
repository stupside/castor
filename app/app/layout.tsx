import type { Metadata } from "next";
import Link from "next/link";
import "./globals.css";

export const metadata: Metadata = { title: "Castor" };

export default function RootLayout({ children }: LayoutProps<"/">) {
  return (
    <html lang="en">
      <body className="bg-[#1b1d1a] antialiased **:[button,a,summary]:cursor-pointer **:[button:disabled]:cursor-not-allowed motion-reduce:**:animate-none motion-reduce:**:transition-none">
        <header className="absolute inset-x-0 top-0 z-20 px-6 pt-6 sm:px-10 lg:px-16">
          <Link href="/" className="text-xl font-extrabold tracking-[-0.04em] text-[#fffaf2]">castor</Link>
        </header>
        <main>{children}</main>
      </body>
    </html>
  );
}
