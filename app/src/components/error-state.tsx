"use client";

import { Button } from "./button";
import { Card } from "./card";
import { EmptyState } from "./empty-state";

// An error boundary's view: only its segment broke, the rest of the page still works.
export function ErrorState({ retry }: { retry: () => void }) {
  return (
    <Card>
      <EmptyState
        title="Something broke here"
        action={
          <Button variant="primary" icon="refresh" onClick={retry}>
            Try again
          </Button>
        }
      >
        The rest of castor still works. If this keeps happening, the server logs
        say why.
      </EmptyState>
    </Card>
  );
}
