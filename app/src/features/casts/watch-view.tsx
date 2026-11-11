"use client";

import { durationMs } from "@bufbuild/protobuf/wkt";
import { createClient } from "@connectrpc/connect";
import * as stylex from "@stylexjs/stylex";
import {
  useEffect,
  useEffectEvent,
  useReducer,
  useRef,
  useState,
  useTransition,
} from "react";
import { Badge } from "@/components/badge";
import { Button, ButtonLink } from "@/components/button";
import { Card } from "@/components/card";
import { CopyButton } from "@/components/copy-button";
import { Row, Stack } from "@/components/layout";
import { Notice } from "@/components/notice";
import { Page } from "@/components/page";
import { Slider } from "@/components/slider";
import { Steps, type StepState } from "@/components/steps";
import { Kbd, Mono } from "@/components/text";
import { toast } from "@/components/toast";
import { color, space, text } from "@/components/tokens.stylex";
import {
  CastService,
  FailureCode,
  WatchResponseSchema,
  type CastStatusValid,
  type EndedValid,
  type PlayheadValid,
} from "@/gen/castor/v1/cast_pb";
import { RecoveryAction } from "@/gen/castor/v1/revision_pb";
import { browserTransport } from "@/lib/browser";
import { describe } from "@/lib/errors";
import { valid } from "@/lib/valid";
import { pauseAction, resumeAction, seekAction } from "./actions";
import { clock, journey, stateBadge } from "./labels";
import { useLivePosition } from "./live-playhead";
import { type Seek, seekShown } from "./playhead-state";
import { StopButton } from "./stop-button";
import { watchState } from "./watch-state";
import { watchUpdates } from "./watch-updates";

const client = createClient(CastService, browserTransport);
const jumpMs = 10_000;

const recoveryActions: Record<RecoveryAction, string> = {
  [RecoveryAction.UNSPECIFIED]: "unspecified",
  [RecoveryAction.SWITCH_CANDIDATE]: "switched stream",
  [RecoveryAction.DECODE_AXIS]: "changed decoding",
  [RecoveryAction.RELAX_READ]: "relaxed reading",
  [RecoveryAction.SERVE_INSTEAD]: "relayed through castor",
  [RecoveryAction.STAY]: "stayed",
  [RecoveryAction.SOFTWARE_ENCODE]: "encoded in software",
};

const failures: Record<FailureCode, string> = {
  [FailureCode.UNSPECIFIED]: "Failed",
  [FailureCode.PLAYBACK_FAILED]: "Playback failed",
  [FailureCode.DEVICE_UNREACHABLE]: "Device unreachable",
  [FailureCode.EXTRACTION_FAILED]: "No video found",
  [FailureCode.SERVER_SHUTDOWN]: "Server shut down",
  [FailureCode.INTERNAL]: "Internal error",
};

type Result = EndedValid["result"]["case"];

const endings = {
  completed: { label: "Completed", tone: "success", live: false },
  stopped: { label: "Stopped", tone: "neutral", live: false },
  failed: { label: "Failed", tone: "danger", live: false },
} as const satisfies Record<NonNullable<Result>, ReturnType<typeof stateBadge>>;

const disconnected = {
  label: "Disconnected",
  tone: "danger",
  live: false,
} as const;

export function WatchView({
  castId,
  device,
  source,
}: {
  castId: string;
  device?: string;
  source?: string;
}) {
  const [watch, dispatch] = useReducer(watchState, { phase: "watching" });
  const [run, setRun] = useState(0);

  useEffect(() => {
    const abort = new AbortController();
    (async () => {
      dispatch({ type: "restart" });
      try {
        const updates = client.watch({ castId }, { signal: abort.signal });
        for await (const message of watchUpdates(updates, abort.signal)) {
          const { update } = valid(WatchResponseSchema, message);
          if (update.case === "status") {
            dispatch({ type: "status", status: update.value });
          } else if (update.case === "ended") {
            dispatch({ type: "ended", ended: update.value });
          }
        }
      } catch (e) {
        if (!abort.signal.aborted) {
          dispatch({ type: "lost", error: describe(e) });
        }
      }
    })();
    return () => abort.abort();
  }, [castId, run]);

  const ended = watch.phase === "ended" ? watch.ended.result : undefined;
  const lost = watch.phase === "lost";
  const state = watch.status?.state;
  const casting = state?.case === "casting" ? state.value : undefined;
  const progress =
    state?.case === "measuring" || state?.case === "casting"
      ? state.value
      : undefined;
  const at = Math.max(
    0,
    journey.findIndex((j) => j.state === state?.case),
  );
  const badge = ended?.case
    ? endings[ended.case]
    : lost
      ? disconnected
      : stateBadge(watch.status);

  return (
    <Page
      title={
        <>
          {device ?? "Cast"}
          <Badge tone={badge.tone} live={badge.live}>
            {badge.label}
          </Badge>
        </>
      }
      description={
        source && (
          <>
            <Mono>{source}</Mono>
            <CopyButton value={source} label="Copy the source link" />
          </>
        )
      }
      actions={
        <ButtonLink href="/" variant="ghost" size="sm">
          All casts
        </ButtonLink>
      }
    >
      <Stack gap="xl">
        <Card title="Progress">
          <Stack>
            <Steps
              steps={journey.map((j, i) => ({
                label: j.label,
                state: stepState(i, at, ended?.case, !ended && !lost),
              }))}
            />
            {progress && progress.streams > 0 && (
              <p sx={styles.fine}>
                {progress.streams} streams found
                {progress.castable !== undefined &&
                  `, ${progress.castable} playable`}
                {casting &&
                  casting.attempt > 1 &&
                  ` · attempt ${casting.attempt}`}
              </p>
            )}
            {casting?.revision && (
              <p sx={styles.fine}>
                Changed plan: {recoveryActions[casting.revision.action]},{" "}
                {casting.revision.why}
              </p>
            )}
          </Stack>
        </Card>

        {ended?.case === "failed" && (
          <Notice tone="danger">
            <strong>{failures[ended.value.code]}</strong>
            {ended.value.message}
          </Notice>
        )}
        {ended?.case === "completed" && (
          <Notice>
            Castor finished its work. The video may still be playing on the
            device.
          </Notice>
        )}
        {ended?.case === "stopped" && <Notice>Stopped.</Notice>}
        {watch.phase === "lost" && (
          <Notice tone="danger">
            <strong>Lost the status stream</strong>
            {watch.error}. Playback may still be running; reconnect to check.
          </Notice>
        )}

        {!ended && !lost ? (
          <Card title="Playback" actions={<StopButton castId={castId} />}>
            {casting?.playhead ? (
              <Controls castId={castId} playhead={casting.playhead} />
            ) : (
              <p sx={styles.fine}>
                {casting
                  ? "Waiting for the device to say where it plays."
                  : "Controls appear once the device plays."}
              </p>
            )}
          </Card>
        ) : (
          <Row>
            {lost && (
              <Button
                variant="primary"
                icon="refresh"
                onClick={() => setRun((n) => n + 1)}
              >
                Reconnect
              </Button>
            )}
            <ButtonLink
              href="/casts/new"
              variant={lost ? "secondary" : "primary"}
            >
              Cast something else
            </ButtonLink>
          </Row>
        )}
      </Stack>
    </Page>
  );
}

function stepState(
  i: number,
  at: number,
  result: Result | undefined,
  live: boolean,
): StepState {
  if (i < at || result === "completed") return "done";
  if (i > at) return "todo";
  if (result === "failed") return "failed";
  return live ? "current" : "todo";
}

function Controls({
  castId,
  playhead,
}: {
  castId: string;
  playhead: PlayheadValid;
}) {
  const position = durationMs(playhead.position);
  const duration = playhead.duration
    ? durationMs(playhead.duration)
    : undefined;
  const [, startTransition] = useTransition();

  // What was asked shows at once and holds until the device reports it.
  const [wantPaused, setWantPaused] = useState<boolean>();
  if (wantPaused !== undefined && wantPaused === playhead.paused) {
    setWantPaused(undefined);
  }
  const [seek, setSeek] = useState<Seek>();
  const seeks = useRef(0);
  const shownSeek = seekShown(seek, position);
  if (shownSeek !== seek) setSeek(shownSeek);
  const [drag, setDrag] = useState<number>();

  const paused = wantPaused ?? playhead.paused;
  const live = useLivePosition(position, paused || !!seek, duration);
  const shown = drag ?? seek?.to ?? live;

  const toggle = () => {
    const next = !paused;
    setWantPaused(next);
    startTransition(async () => {
      const error = await (next ? pauseAction : resumeAction)(castId);
      if (error) {
        setWantPaused(undefined);
        toast(`Couldn’t ${next ? "pause" : "resume"}: ${error}`, "danger");
      }
    });
  };
  const commit = (to: number) => {
    setDrag(undefined);
    seeks.current += 1;
    const request = seeks.current;
    setSeek({ request, to, from: position, answered: false });
    startTransition(async () => {
      const error = await seekAction(castId, to);
      if (error) {
        setSeek((s) => (s?.request === request ? undefined : s));
        toast(`Couldn’t seek: ${error}`, "danger");
        return;
      }
      setSeek((s) => (s?.request === request ? { ...s, answered: true } : s));
    });
  };
  const jump = (delta: number) =>
    commit(Math.max(0, Math.min(duration ?? Infinity, shown + delta)));

  const onKey = useEffectEvent((e: KeyboardEvent) => {
    if (e.repeat || e.metaKey || e.ctrlKey || e.altKey || e.defaultPrevented)
      return;
    const typing =
      e.target instanceof Element &&
      e.target.closest(
        "input, textarea, button, a, [role=slider], [role=listbox]",
      );
    if (typing) return;
    const action = {
      " ": toggle,
      k: toggle,
      ArrowLeft: () => jump(-jumpMs),
      j: () => jump(-jumpMs),
      ArrowRight: () => jump(jumpMs),
      l: () => jump(jumpMs),
    }[e.key];
    if (!action) return;
    e.preventDefault();
    action();
  });
  useEffect(() => {
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  return (
    <Stack gap="xl">
      <Stack gap="sm">
        <div sx={styles.clock}>
          <span sx={styles.now}>{clock(shown)}</span>
          {duration && (
            <span sx={styles.fine}>
              −{clock(Math.max(0, duration - shown))}
            </span>
          )}
        </div>
        {duration && (
          <Slider
            label="Position"
            value={shown}
            max={duration}
            step={1000}
            keyStep={jumpMs}
            pageStep={60_000}
            format={clock}
            onChange={setDrag}
            onCommit={commit}
          />
        )}
      </Stack>
      <Row sx={styles.centered}>
        <Button icon="back" onClick={() => jump(-jumpMs)}>
          10s
        </Button>
        <Button
          variant="primary"
          icon={paused ? "play" : "pause"}
          onClick={toggle}
        >
          {paused ? "Resume" : "Pause"}
        </Button>
        <Button icon="forward" onClick={() => jump(jumpMs)}>
          10s
        </Button>
      </Row>
      <p sx={styles.keys}>
        <span sx={styles.key}>
          <Kbd>Space</Kbd> play or pause
        </span>
        <span sx={styles.key}>
          <Kbd>←</Kbd>
          <Kbd>→</Kbd> 10 seconds
        </span>
      </p>
    </Stack>
  );
}

const styles = stylex.create({
  fine: { fontSize: text.sm, color: color.inkMuted },
  clock: {
    display: "flex",
    alignItems: "baseline",
    justifyContent: "space-between",
    gap: space.sm,
    fontVariantNumeric: "tabular-nums",
  },
  now: {
    fontSize: text.xxl,
    fontWeight: 600,
    letterSpacing: "-0.02em",
    color: color.ink,
  },
  centered: { justifyContent: "center" },
  keys: {
    display: "flex",
    flexWrap: "wrap",
    justifyContent: "center",
    gap: space.lg,
    fontSize: text.xs,
    color: color.inkMuted,
  },
  key: { display: "inline-flex", alignItems: "center", gap: space.xs },
});
