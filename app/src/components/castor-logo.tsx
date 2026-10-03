export type Mood = "happy" | "think" | "oops" | "cheer" | "sleep";

const ink = "#2b1d13";

function Eyes({ mood }: { mood: Mood }) {
  if (mood === "cheer") return <path d="M35 36q5-8 10 0M55 36q5-8 10 0" fill="none" stroke={ink} strokeWidth="3" strokeLinecap="round" />;
  if (mood === "sleep") return <path d="M35 34q5 5 10 0M55 34q5 5 10 0" fill="none" stroke={ink} strokeWidth="2.5" strokeLinecap="round" />;
  const look = mood === "think" ? -1.5 : 0;
  return <g className="animate-blink origin-center transform-fill">
    <circle cx="40" cy="34" r="5" fill={ink} /><circle cx="60" cy="34" r="5" fill={ink} />
    <circle cx="42" cy={32 + look} r="2" fill="white" /><circle cx="62" cy={32 + look} r="2" fill="white" />
    {mood === "oops" && <path d="M33 25l11 3M67 25l-11 3" stroke={ink} strokeWidth="2" strokeLinecap="round" />}
  </g>;
}

function Mouth({ mood }: { mood: Mood }) {
  const line = { fill: "none", stroke: "#33241a", strokeWidth: 1.6, strokeLinecap: "round" } as const;
  if (mood === "cheer") return <path d="M41 47q9 10 18 0z" fill="#7a3a2a" stroke="#33241a" strokeWidth="1.4" strokeLinejoin="round" />;
  if (mood === "oops") return <path d="M42 51q8-7 16 0" {...line} />;
  if (mood === "think") return <path d="M44 48h12" {...line} />;
  return <path d="M50 44q-5 5-9 2m9-2q5 5 9 2" {...line} />;
}

export function CastorLogo({ size = 44, mood = "happy" }: { size?: number; mood?: Mood }) {
  return <svg width={size} height={size} viewBox="0 0 100 100" role="img" aria-label="Castor" className="shrink-0 overflow-visible">
    <circle cx="33" cy="18" r="7" fill="#a5744d" /><circle cx="67" cy="18" r="7" fill="#a5744d" />
    <path d="M50 12C33 12 26 27 26 43c0 13-6 19-5 30 1 12 13 18 29 18s28-6 29-18c1-11-5-17-5-30C74 27 67 12 50 12Z" fill="#a5744d" />
    <path d="M64 70c12-6 28-2 30 9 2 9-9 14-19 10-9-3-14-11-11-19Z" fill="#5c4634" />
    <ellipse cx="50" cy="70" rx="15" ry="14" fill="#ecd6b6" /><ellipse cx="50" cy="45" rx="13" ry="10" fill="#ecd6b6" />
    <Eyes mood={mood} />
    <ellipse cx="50" cy="41" rx="4" ry="3" fill="#33241a" />
    <Mouth mood={mood} />
  </svg>;
}
