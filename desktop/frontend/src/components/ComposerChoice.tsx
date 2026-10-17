import { useEffect, useRef, useState, type ReactNode } from "react";
import { Check, ChevronDown } from "lucide-react";
import { AnchoredPopover } from "./AnchoredPopover";

export function ComposerChoice({ label, ariaLabel, icon, showChevron, value, options, disabled, dismissSignal, onPick, tone }: {
  label: string;
  ariaLabel?: string;
  showChevron?: boolean;
  icon?: ReactNode;
  value: string;
  options: { value: string; label: string; badge?: string; description?: string; icon?: ReactNode; title?: string }[];
  disabled?: boolean;
  dismissSignal?: number;
  onPick: (value: string) => void;
  tone?: string;
}) {
  const [open, setOpen] = useState(false);
  const anchor = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  useEffect(() => { if (disabled || dismissSignal !== undefined) setOpen(false); }, [disabled, dismissSignal]);
  useEffect(() => {
    if (!open || disabled) return;
    const frame = requestAnimationFrame(() => {
      const selected = menu.current?.querySelector<HTMLButtonElement>('[aria-checked="true"]');
      (selected ?? menu.current?.querySelector<HTMLButtonElement>("button"))?.focus();
    });
    return () => cancelAnimationFrame(frame);
  }, [open, disabled]);
  const closeAndFocus = () => { setOpen(false); anchor.current?.focus(); };
  return <>
    <button ref={anchor} type="button" className={`composer-choice ${tone || ""}`} disabled={disabled}
      aria-label={ariaLabel || label} aria-haspopup="menu" aria-expanded={open && !disabled} onClick={() => setOpen(!open)}
      onKeyDown={event => { if (event.key === "ArrowDown" || event.key === "ArrowUp") { event.preventDefault(); setOpen(true); } }}>
      {icon}<span>{label}</span>{(showChevron ?? !icon) && <ChevronDown size={12} />}
    </button>
    <AnchoredPopover open={open && !disabled} anchorRef={anchor} onClose={() => setOpen(false)} className="composer-access-menu composer-menu-surface" align="start">
      <div ref={menu} role="menu" aria-label={ariaLabel || label} onKeyDown={event => {
        if (event.key === "Escape" || event.key === "Tab") {
          event.preventDefault(); event.stopPropagation(); closeAndFocus(); return;
        }
        if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
        event.preventDefault();
        const items = [...event.currentTarget.querySelectorAll<HTMLButtonElement>("button")];
        const index = items.indexOf(document.activeElement as HTMLButtonElement);
        const next = event.key === "Home" ? 0 : event.key === "End" ? items.length - 1
          : (index + (event.key === "ArrowDown" ? 1 : -1) + items.length) % items.length;
        items[next]?.focus();
      }}>
        {options.map(option => <button key={option.value} type="button" role="menuitemradio"
          data-value={option.value} title={option.title}
          aria-checked={value === option.value} className={`composer-access-menu__item${value === option.value ? " composer-access-menu__item--active" : ""}`}
          tabIndex={-1} onClick={() => { closeAndFocus(); onPick(option.value); }}>
          {option.icon}<span className="composer-access-menu__copy"><span className="composer-access-menu__heading"><span className="composer-access-menu__title">{option.label}</span>
            {option.badge && <span className="composer-access-menu__badge">{option.badge}</span>}</span>
            {option.description && <span className="composer-access-menu__desc">{option.description}</span>}</span>
          {value === option.value && <Check size={14} />}
        </button>)}
      </div>
    </AnchoredPopover>
  </>;
}
