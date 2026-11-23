// Small building blocks styled after the Oxygen UI (WSO2) design system.
import type { ReactNode } from "react";
import { InfoIcon, AlertIcon } from "./icons";
import type { ListSummary, LocationEntry, Vacancy } from "./api";
import { locationQuality } from "./api";

/** Chip colour per vacancy type (the legend in the results header uses the same mapping). */
const TYPE_COLORS: Record<string, string> = {
  "Difficult station": "primary",
  New: "default",
  "Special Requests": "info",
  "AT Replacement": "success",
};
export const typeColor = (type: string) => TYPE_COLORS[type] ?? "default";

export function Chip({ color = "default", dot, children, title }: { color?: string; dot?: boolean; children: ReactNode; title?: string }) {
  return (
    <span className={`chip c-${color}`} title={title}>
      {dot && <span className="chip-dot" />}
      {children}
    </span>
  );
}

export function VacancyChips({ vacancies }: { vacancies: Vacancy[] }) {
  return (
    <div className="chips">
      {vacancies.map((v, i) => (
        <Chip key={i} color={typeColor(v.type)} title={[v.designation, v.type].filter(Boolean).join(" · ")}>
          {/* Station-only lists (difficult stations) have no post name: show the type. */}
          {v.designation || v.type || "Vacancy"}
        </Chip>
      ))}
    </div>
  );
}

/** Legend for the vacancy types that appear in a list. */
export function TypeLegend({ vacancies }: { vacancies: Vacancy[] }) {
  const types = [...new Set(vacancies.map((v) => v.type).filter(Boolean))].sort(
    (a, b) => Object.keys(TYPE_COLORS).indexOf(a) - Object.keys(TYPE_COLORS).indexOf(b),
  );
  if (types.length === 0) return null;
  return (
    <div className="chips legend" aria-label="Vacancy types">
      {types.map((t) => (
        <Chip key={t} color={typeColor(t)} dot>
          {t}
        </Chip>
      ))}
    </div>
  );
}

const count = (n: number) => n.toLocaleString("en-US");

/** One line describing a list's size. */
export function listCounts(l: ListSummary): string {
  return l.hasDesignations
    ? `${count(l.vacancyCount)} vacancies at ${count(l.stationCount)} stations`
    : `${count(l.stationCount)} stations`;
}

/** Chooses which vacancy / station list the app works on. */
export function ListPicker({
  lists,
  value,
  onChange,
  id,
  label = "Station list",
  labelClassName,
}: {
  lists: ListSummary[];
  value: string;
  onChange: (id: string) => void;
  id: string;
  label?: string;
  labelClassName?: string;
}) {
  const current = lists.find((l) => l.id === value);
  if (lists.length < 2) return null;
  return (
    <div className="field">
      <label htmlFor={id} className={labelClassName}>
        {label}
      </label>
      <select id={id} className="select" value={value} onChange={(e) => onChange(e.target.value)}>
        {lists.map((l) => (
          <option key={l.id} value={l.id}>
            {l.name}
          </option>
        ))}
      </select>
      {current && <span className="field-help">{listCounts(current)}</span>}
    </div>
  );
}

/** Badge for a location that is approximate or doubtful; nothing for a good one. */
export function QualityChip({ entry, always }: { entry?: LocationEntry | null; always?: boolean }) {
  const q = locationQuality(entry);
  if (q.tone === "good" && !always) return null;
  const color = q.tone === "good" ? "success" : q.tone === "warn" ? "warning" : "error";
  return <Chip color={color}>{q.label}</Chip>;
}

export function Alert({
  severity = "info",
  title,
  children,
  actionLabel,
  onAction,
}: {
  severity?: "info" | "warning" | "error" | "success";
  title?: string;
  children?: ReactNode;
  actionLabel?: string;
  onAction?: () => void;
}) {
  return (
    <div className={`alert s-${severity}`} role={severity === "error" ? "alert" : "status"}>
      <span className="alert-icon">{severity === "info" ? <InfoIcon size={16} /> : <AlertIcon size={16} />}</span>
      <div className="alert-body">
        {title && <span className="alert-title">{title}</span>}
        {children && <span>{children}</span>}
        {actionLabel && (
          <button type="button" className="alert-action" onClick={onAction}>
            {actionLabel}
          </button>
        )}
      </div>
    </div>
  );
}

export function initials(name: string): string {
  const parts = name.replace(/@.*/, "").split(/[\s._-]+/).filter(Boolean);
  return ((parts[0]?.[0] ?? "?") + (parts.length > 1 ? parts[parts.length - 1][0] : "")).toUpperCase();
}

export function Avatar({ name }: { name: string }) {
  return (
    <span className="avatar" aria-hidden="true">
      {initials(name)}
    </span>
  );
}

export function BrandMark() {
  return (
    <span className="brand-mark" aria-hidden="true">
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <path d="M20 10c0 4.993-5.539 10.193-7.399 11.799a1 1 0 0 1-1.202 0C9.539 20.193 4 14.993 4 10a8 8 0 0 1 16 0" />
        <circle cx="12" cy="10" r="3" />
      </svg>
    </span>
  );
}

export function Segmented<T extends string>({
  options,
  value,
  onChange,
  label,
  stretch,
}: {
  options: { value: T; label: ReactNode }[];
  value: T;
  onChange: (v: T) => void;
  label: string;
  stretch?: boolean;
}) {
  return (
    // "full", not "stretch": .stretch is the clickable-card class, whose invisible ::after
    // layer would cover the whole panel and swallow taps.
    <div role="group" aria-label={label} className={`segmented${stretch ? " full" : ""}`}>
      {options.map((o) => (
        <button key={o.value} type="button" aria-pressed={o.value === value} onClick={() => onChange(o.value)}>
          {o.label}
        </button>
      ))}
    </div>
  );
}
