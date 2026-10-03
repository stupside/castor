import { CastorLogo, type Mood } from "./castor-logo";

// What the cinema screen shows: waiting, a spinner while a cast loads, or the lit play button.
export type Screen = "idle" | "loading" | "playing";

type Layout = { id: string; w: number; h: number; moon: [number, number, number]; waterTop: number };

const wide: Layout = { id: "w", w: 2000, h: 800, moon: [1500, 120, 46], waterTop: 640 };
const tall: Layout = { id: "t", w: 600, h: 900, moon: [425, 62, 30], waterTop: 700 };

const flies = [[120, 560], [380, 610], [640, 700], [900, 590], [1150, 740], [1380, 580], [1640, 600], [1900, 560], [260, 760], [1000, 670]] as const;
const pine = "M12 0l9 17h-5l8 15h-6l9 16H-3l9-16H0l8-15H3Z";

const Stars = ({ w, h }: { w: number; h: number }) => Array.from({ length: Math.round(w / 28) }, (_, i) => {
  const x = (i * 97) % w, y = (i * 61) % (h * 0.42), big = i % 4 === 0;
  return <circle key={i} cx={x} cy={y} r={big ? 2 : 1.2} fill="#ecd6b6" className="animate-twinkle" style={{ animationDelay: `${(i % 9) * 0.5}s` }} />;
});

const Pines = ({ at, base }: { at: number[]; base: number }) => at.map((x, i) => {
  const s = 1.7 + (i % 3) * 0.45;
  return <path key={x} d={pine} fill="#0c1b1a" transform={`translate(${x} ${base - 52 * s}) scale(${s})`} />;
});

const Lily = ({ x, y, flower }: { x: number; y: number; flower?: boolean }) => <g className="animate-hop" style={{ animationDelay: `${x % 5}s` }}>
  <ellipse cx={x} cy={y} rx="26" ry="7" fill="#5d7a4f" />{flower && <circle cx={x + 6} cy={y - 7} r="5" fill="#f3a5b5" />}
</g>;

const Wave = ({ y, w }: { y: number; w: number }) => <path fill="#24504b" d={`M0 ${y}q50-12 100 0${"t100 0".repeat(Math.ceil(w / 100) + 3)}V${y + 40}H0Z`}>
  <animateTransform attributeName="transform" type="translate" from="0 0" to="-200 0" dur="9s" repeatCount="indefinite" />
</path>;

const Beaver = ({ x, y, size, mood }: { x: number; y: number; size: number; mood: Mood }) =>
  <g transform={`translate(${x} ${y})`}><CastorLogo size={size} mood={mood} /></g>;

// The family of lodge + beavers stands on a bank; feet sit on `ground`.
function Family({ cx, base, r }: { cx: number; base: number; r: number }) {
  const s = r * 0.42, door = r * 0.38;
  return <>
    <path d={`M${cx - r} ${base + 40}V${base}a${r} ${r} 0 0 1 ${2 * r} 0V${base + 40}Z`} fill="#5c4634" />
    <g stroke="#3f2f23" strokeWidth={r / 40} strokeLinecap="round" opacity=".7" fill="none">
      <path d={`M${cx - r * .85} ${base - r * .2}l${r * .6} -${r * .3}M${cx - r * .6} ${base - r * .05}l${r * .9} -${r * .45}M${cx} ${base - r * .85}l${r * .5} ${r * .3}M${cx + r * .1} ${base - r * .35}l${r * .7} ${r * .25}`} />
    </g>
    <path d={`M${cx - r * .28} ${base + 4}a${r * .28} ${r * .42} 0 0 1 ${r * .56} 0Z`} fill="#171a16" />
    <Beaver x={cx - door / 2} y={base + 6 - door * 0.91} size={door} mood="think" />
    <Beaver x={cx - r * 0.5 - s / 2} y={base - r * 0.866 - s * 0.7} size={s} mood="sleep" />
    <text x={cx - r * 0.5 + s * 0.45} y={base - r * 0.866 - s} className="animate-hop fill-cream/70 font-extrabold" fontSize={r / 8}>zz</text>
  </>;
}

function Art({ l, back, children }: { l: Layout; back: React.ReactNode; children: React.ReactNode }) {
  const { id, w, h, moon: [mx, my, mr], waterTop } = l;
  return <>
    <defs>
      <linearGradient id={`${id}sky`} x1="0" y1="0" x2="0" y2="1"><stop offset="0" stopColor="#0b1214" /><stop offset=".6" stopColor="#13292b" /><stop offset="1" stopColor="#1d3d3a" /></linearGradient>
      <linearGradient id={`${id}water`} x1="0" y1="0" x2="0" y2="1"><stop offset="0" stopColor="#24504b" /><stop offset="1" stopColor="#10272a" /></linearGradient>
      <radialGradient id={`${id}glow`}><stop offset="0" stopColor="#f3d9b8" stopOpacity=".35" /><stop offset="1" stopColor="#f3d9b8" stopOpacity="0" /></radialGradient>
    </defs>
    <rect width={w} height={h} fill={`url(#${id}sky)`} />
    <Stars w={w} h={h} />
    <circle cx={mx} cy={my} r={mr * 4} fill={`url(#${id}glow)`} /><circle cx={mx} cy={my} r={mr} fill="#f3d9b8" />
    {back}
    <rect x="0" y={waterTop} width={w} height={h - waterTop} fill={`url(#${id}water)`} />
    <Wave y={waterTop} w={w} />
    {children}
    {flies.filter(([x, y]) => x < w && y < h - 60 && !(w > 1000 && x > 1450)).map(([x, y], i) => <g key={i} className="animate-firefly" style={{ animationDelay: `${i * 0.9}s`, animationDuration: `${5 + (i % 4)}s` }}><circle cx={x} cy={y} r="9" fill="#f6e58d" opacity=".25" /><circle cx={x} cy={y} r="3" fill="#f6e58d" /></g>)}
  </>;
}

// Wide: the pond runs left, the lodge and beavers sit bottom right, a screen shows on the far hill. Tall: everything stacked in the middle.
function WideArt({ mood, screen }: { mood: Mood; screen: Screen }) {
  const playing = screen === "playing";
  const ground = 655;
  return <Art l={wide} back={<>
    <path d="M0 800V470q250-80 500-40t500 10t500-50t500 40V800Z" fill="#102321" />
    <Pines at={[60, 150, 330, 700, 1180, 1530, 1650, 1790, 1900]} base={565} />
    <path d="M0 800V540q300-60 600-20t600 10t800-30V800Z" fill="#0c1b1a" />

    <g className="hidden xl:block" transform="translate(1340 520) scale(1.5) translate(-1340 -520)">
      <ellipse cx="1340" cy="480" rx="150" ry="90" fill="url(#wglow)" opacity={playing ? 1 : 0} className="transition-opacity duration-1000" />
      <rect x="1290" y="516" width="8" height="34" rx="3" fill="#4a382a" /><rect x="1382" y="516" width="8" height="34" rx="3" fill="#4a382a" />
      <rect x="1270" y="434" width="140" height="88" rx="16" fill="#5c4634" />
      <rect x="1280" y="444" width="120" height="68" rx="9" fill="#1f3532" />
      <rect x="1280" y="444" width="120" height="68" rx="9" fill="#f2c9a3" opacity={playing ? 1 : 0} className="transition-opacity duration-1000" />
      {screen === "loading"
        ? <circle cx="1340" cy="478" r="13" fill="none" stroke="#ffffffaa" strokeWidth="4" strokeLinecap="round" strokeDasharray="42 50"><animateTransform attributeName="transform" type="rotate" from="0 1340 478" to="360 1340 478" dur="0.9s" repeatCount="indefinite" /></circle>
        : playing ? <path d="M1333 466v24l20-12z" fill="#5c4634" strokeLinejoin="round" stroke="#5c4634" strokeWidth="4" />
        : <g fill="#ffffff66"><path d="M1349 464a15 15 0 1 0 0 28a11 11 0 0 1 0-28z" /><circle cx="1362" cy="468" r="2" /><circle cx="1369" cy="480" r="1.5" /></g>}
      <g fill="#060d0d" transform="translate(1340 520) scale(.7)">{[-46, 0, 46].map((x) => <g key={x}><circle cx={x - 8} cy="-14" r="5" /><circle cx={x + 8} cy="-14" r="5" /><circle cx={x} cy="-4" r="12" /><ellipse cx={x} cy="20" rx="16" ry="14" /></g>)}</g>
    </g>
  </>}>
    <Lily x={900} y={700} flower /><Lily x={1100} y={745} /><Lily x={700} y={765} flower /><Lily x={1290} y={695} />
    <g><animateTransform attributeName="transform" type="translate" values="0 0;260 0;0 0" dur="40s" repeatCount="indefinite" />
      <clipPath id="wswim"><rect x="780" y="640" width="90" height="56" /></clipPath>
      <g clipPath="url(#wswim)"><Beaver x={795} y={650} size={70} mood="happy" /></g>
      <ellipse cx="830" cy="698" rx="40" ry="7" fill="none" stroke="#fff" strokeOpacity=".3" className="animate-ping-soft transform-fill origin-center" />
    </g>

    <Family cx={1740} base={ground - 3} r={150} />
    <path d="M1380 800C1460 800 1500 700 1620 668C1720 640 1850 646 2000 644V800Z" fill="#2b3724" />
    <path d="M1620 668C1720 640 1850 646 2000 644" fill="none" stroke="#43552f" strokeWidth="6" />
    <g stroke="#7c9061" strokeWidth="5" strokeLinecap="round" opacity=".8"><path d="M1572 676l-8-62M1590 672l10-48" /></g>
    <Beaver x={1655} y={ground + 8 - 0.91 * 76} size={76} mood="cheer" />
    <Beaver x={1830} y={ground + 4 - 0.91 * 160} size={160} mood={mood} />
  </Art>;
}

function TallArt({ mood }: { mood: Mood }) {
  const ground = 806;
  return <Art l={tall} back={<>
    <path d="M0 900V600q150-70 300-30t300 10V900Z" fill="#102321" />
    <Pines at={[30, 90, 160, 450, 520, 570]} base={650} />
    <path d="M0 900V650q200-50 400-15t200 5V900Z" fill="#0c1b1a" />
  </>}>
    <Lily x={150} y={745} flower /><Lily x={470} y={765} />
    <g><animateTransform attributeName="transform" type="translate" values="0 0;200 0;0 0" dur="40s" repeatCount="indefinite" />
      <clipPath id="tswim"><rect x="130" y="708" width="70" height="44" /></clipPath>
      <g clipPath="url(#tswim)"><Beaver x={135} y={716} size={56} mood="happy" /></g>
      <ellipse cx="163" cy="754" rx="32" ry="6" fill="none" stroke="#fff" strokeOpacity=".3" className="animate-ping-soft transform-fill origin-center" />
    </g>
    <Family cx={255} base={ground - 4} r={125} />
    <path d="M0 900V812C120 800 300 796 600 806V900Z" fill="#2b3724" />
    <path d="M0 812C120 800 300 796 600 806" fill="none" stroke="#43552f" strokeWidth="5" />
    <Beaver x={150} y={ground + 4 - 0.91 * 60} size={60} mood="cheer" />
    <Beaver x={350} y={ground + 4 - 0.91 * 120} size={120} mood={mood} />
  </Art>;
}

export function Scene({ title, mood = "happy", screen = "idle", children }: { title: React.ReactNode; mood?: Mood; screen?: Screen; children: React.ReactNode }) {
  return <section className="relative isolate flex min-h-svh flex-col overflow-hidden bg-[#0b1214]">
    <svg aria-hidden viewBox={`0 0 ${tall.w} ${tall.h}`} preserveAspectRatio="xMidYMax slice" className="absolute inset-0 -z-10 size-full landscape:sm:hidden"><TallArt mood={mood} /></svg>
    <svg aria-hidden viewBox={`0 0 ${wide.w} ${wide.h}`} preserveAspectRatio="xMaxYMax slice" className="absolute inset-0 -z-10 hidden size-full landscape:sm:block"><WideArt mood={mood} screen={screen} /></svg>
    <div className="flex flex-1 flex-col justify-center gap-8 px-6 pb-72 pt-24 sm:px-10 lg:px-16 landscape:sm:max-w-184 landscape:sm:pb-24">
      <h1 className="text-5xl font-bold leading-[1.02] tracking-tighter text-[#fffaf2] sm:text-6xl">{title}</h1>
      <div className="w-full max-w-lg">{children}</div>
    </div>
  </section>;
}
