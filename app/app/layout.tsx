import * as stylex from "@stylexjs/stylex";
import type { Metadata } from "next";
import { Work_Sans } from "next/font/google";
import { Toaster } from "@/components/toast";
import { color, font } from "@/components/tokens.stylex";
import { authorize } from "@/lib/authorize";
import "./globals.css";

export const metadata: Metadata = {
  title: { default: "Castor", template: "%s · Castor" },
};

const workSans = Work_Sans({
  subsets: ["latin"],
  variable: "--font-work-sans",
});

export default async function RootLayout({ children }: LayoutProps<"/">) {
  await authorize();
  return (
    <html lang="en" className={workSans.variable}>
      <body sx={styles.body}>
        {children}
        <Toaster />
      </body>
    </html>
  );
}

const styles = stylex.create({
  body: {
    backgroundColor: color.canvas,
    color: color.ink,
    fontFamily: font.sans,
    fontSize: 14,
    lineHeight: 1.5,
    "::selection": { backgroundColor: color.muzzle },
  },
});
