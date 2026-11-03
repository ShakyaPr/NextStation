import { useEffect, useRef, useState } from "react";
import { api, titleCase } from "./api";
import type { LatLng, PlaceSuggestion, StationSuggestion } from "./api";

export interface SelectedPlace extends LatLng {
  label: string;
}

type Item =
  | { kind: "place"; key: string; place: PlaceSuggestion }
  | { kind: "station"; key: string; station: StationSuggestion };

interface Props {
  text: string;
  onTextChange: (t: string) => void;
  onSelect: (p: SelectedPlace) => void;
  placeholder?: string;
}

const newSession = () =>
  typeof crypto !== "undefined" && "randomUUID" in crypto
    ? crypto.randomUUID()
    : `${Date.now()}-${Math.random().toString(36).slice(2)}`;

/**
 * Search box with Google Places suggestions (restricted to Sri Lanka) plus
 * matching stations from the vacancy list. A session token groups the typing
 * and the final details lookup so Google bills them as one session.
 */
export default function PlaceSearch({ text, onTextChange, onSelect, placeholder }: Props) {
  const [items, setItems] = useState<Item[]>([]);
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const [loading, setLoading] = useState(false);
  const [resolving, setResolving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const session = useRef(newSession());
  const typed = useRef(false); // only fetch suggestions after the user types

  useEffect(() => {
    const q = text.trim();
    if (!typed.current || q.length < 2) {
      setItems([]);
      return;
    }
    const ctrl = new AbortController();
    const t = setTimeout(async () => {
      setLoading(true);
      try {
        const r = await api.autocomplete(q, session.current, ctrl.signal);
        const next: Item[] = [
          ...r.places.map((p) => ({ kind: "place" as const, key: `p-${p.placeId}`, place: p })),
          ...r.stations.map((s) => ({ kind: "station" as const, key: `s-${s.id}`, station: s })),
        ];
        setItems(next);
        setActive(0);
        setOpen(true);
        setError(r.error && r.places.length === 0 ? r.error : null);
      } catch (e) {
        if ((e as Error).name !== "AbortError") setError((e as Error).message);
      } finally {
        if (!ctrl.signal.aborted) setLoading(false);
      }
    }, 250);
    return () => {
      clearTimeout(t);
      ctrl.abort();
    };
  }, [text]);

  async function choose(item: Item) {
    setOpen(false);
    typed.current = false;
    setError(null);
    if (item.kind === "station") {
      const label = `${titleCase(item.station.name)}, ${titleCase(item.station.district)}`;
      onTextChange(label);
      onSelect({ ...item.station.location, label });
      return;
    }
    const p = item.place;
    const label = p.secondaryText ? `${p.mainText}, ${p.secondaryText}` : p.mainText;
    onTextChange(label);
    setResolving(true);
    try {
      const d = await api.placeDetails(p.placeId, session.current);
      onSelect({ ...d.location, label });
    } catch (e) {
      setError(`Could not get location: ${(e as Error).message}`);
    } finally {
      setResolving(false);
      session.current = newSession(); // a session ends with the details call
    }
  }

  function onKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    if (!open || items.length === 0) return;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setActive((a) => (a + 1) % items.length);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive((a) => (a - 1 + items.length) % items.length);
    } else if (e.key === "Enter") {
      e.preventDefault();
      choose(items[active]);
    } else if (e.key === "Escape") {
      setOpen(false);
    }
  }

  const firstStation = items.findIndex((i) => i.kind === "station");

  return (
    <div className="place-search">
      <div className="place-input">
        <span className="place-icon" aria-hidden>
          🔍
        </span>
        <input
          className="input"
          value={text}
          placeholder={placeholder}
          onChange={(e) => {
            typed.current = true;
            onTextChange(e.target.value);
          }}
          onKeyDown={onKeyDown}
          onFocus={() => items.length > 0 && setOpen(true)}
          onBlur={() => setOpen(false)}
          role="combobox"
          aria-expanded={open}
          aria-autocomplete="list"
          autoComplete="off"
        />
        {(loading || resolving) && <span className="spinner" aria-label="Loading" />}
      </div>
      {open && items.length > 0 && (
        <ul className="suggestions" role="listbox">
          {items.map((item, i) => (
            <li key={item.key} role="option" aria-selected={i === active}>
              {i === firstStation && <div className="suggest-group">Stations in the vacancy list</div>}
              <button
                type="button"
                className={i === active ? "active" : ""}
                onMouseDown={(e) => e.preventDefault()} // keep focus so blur doesn't close first
                onMouseEnter={() => setActive(i)}
                onClick={() => choose(item)}
              >
                {item.kind === "place" ? (
                  <>
                    <span className="suggest-icon">📍</span>
                    <span>
                      <span className="suggest-main">{item.place.mainText}</span>
                      {item.place.secondaryText && <span className="muted small"> {item.place.secondaryText}</span>}
                    </span>
                  </>
                ) : (
                  <>
                    <span className="suggest-icon">🏥</span>
                    <span>
                      <span className="suggest-main">{titleCase(item.station.name)}</span>
                      <span className="muted small"> {titleCase(item.station.district)}</span>
                    </span>
                  </>
                )}
              </button>
            </li>
          ))}
          {items.some((i) => i.kind === "place") && (
            <li className="suggest-footer muted small">Place suggestions by Google</li>
          )}
        </ul>
      )}
      {error && <p className="error small">{error}</p>}
    </div>
  );
}
