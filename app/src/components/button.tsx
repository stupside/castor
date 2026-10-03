const press = "transition hover:-translate-y-0.5 active:translate-y-1 active:shadow-none disabled:translate-y-0 disabled:opacity-40";
const ember = "bg-ember text-white shadow-[0_4px_0_#b9532a] hover:bg-[#ee8650]";

const variants = {
  ember: `px-6 py-3 text-sm ${ember} ${press}`,
  wide: `w-full py-4 text-base ${ember} ${press}`,
  send: `size-10 text-lg ${ember} ${press}`,
  glass: `px-6 py-3 text-sm text-white bg-white/10 shadow-[0_4px_0_rgb(0_0_0/0.35)] hover:bg-red-500/80 ${press}`,
  choice: `border-2 border-ember/70 bg-black/30 px-4 py-2 text-sm text-peach backdrop-blur hover:bg-ember hover:text-white ${press}`,
  ghost: "px-4 py-3 text-sm font-bold text-white/60 transition hover:bg-white/10 hover:text-white",
  stop: "px-4 py-2 text-xs font-bold text-white/60 transition hover:bg-red-500/20 hover:text-red-300",
};

export type Variant = keyof typeof variants;

export const buttonClass = (variant: Variant) => `inline-flex items-center justify-center gap-2 rounded-full font-extrabold ${variants[variant]}`;

export function Button({ variant, className = "", ...props }: { variant: Variant } & React.ComponentProps<"button">) {
  return <button {...props} className={`${buttonClass(variant)} ${className}`} />;
}
