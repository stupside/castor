import { notFound } from "next/navigation";

// A URL only @modal's catch-all matches is no admin page.
export default function Unknown() {
  notFound();
}
