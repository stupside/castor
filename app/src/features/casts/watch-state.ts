import type { CastStatusValid, EndedValid } from "@/gen/castor/v1/cast_pb";

type WatchState =
  | { phase: "watching"; status?: CastStatusValid }
  | { phase: "ended"; status?: CastStatusValid; ended: EndedValid }
  | { phase: "lost"; status?: CastStatusValid; error: string };

type WatchEvent =
  | { type: "restart" }
  | { type: "status"; status: CastStatusValid }
  | { type: "ended"; ended: EndedValid }
  | { type: "lost"; error: string };

// Ended is terminal until a restart; a later failure never overrides it.
export function watchState(state: WatchState, event: WatchEvent): WatchState {
  if (event.type === "restart") return { phase: "watching" };
  if (state.phase !== "watching") return state;
  switch (event.type) {
    case "status":
      return { phase: "watching", status: event.status };
    case "ended":
      return { phase: "ended", status: state.status, ended: event.ended };
    case "lost":
      return { phase: "lost", status: state.status, error: event.error };
  }
}
