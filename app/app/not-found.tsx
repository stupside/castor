import * as stylex from "@stylexjs/stylex";
import { ButtonLink } from "@/components/button";
import { Card } from "@/components/card";
import { EmptyState } from "@/components/empty-state";
import { space } from "@/components/tokens.stylex";

export default function NotFound() {
  return (
    <main sx={styles.center}>
      <Card>
        <EmptyState
          title="This page wandered off"
          action={
            <ButtonLink href="/" variant="primary">
              Back to casts
            </ButtonLink>
          }
        >
          Nothing lives at this address.
        </EmptyState>
      </Card>
    </main>
  );
}

const styles = stylex.create({
  center: {
    display: "grid",
    placeItems: "center",
    minHeight: "100svh",
    padding: space.lg,
  },
});
