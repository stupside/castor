export function Bubble({ me, children }: { me?: boolean; children: React.ReactNode }) {
  return <div className={`animate-pop max-w-[85%] rounded-3xl px-4 py-2.5 text-[15px] font-semibold ${me ? "ml-auto rounded-br-md bg-ember text-white" : "rounded-bl-md bg-white/10 text-[#fffaf2] ring-1 ring-white/15 backdrop-blur-md"}`}>{children}</div>;
}
