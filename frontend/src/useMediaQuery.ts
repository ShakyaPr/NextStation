import { useEffect, useState } from "react";

/** Breakpoints. Keep in sync with the @media rules in styles.css. */
export const PHONE_QUERY = "(max-width: 700px)";
export const DESKTOP_QUERY = "(min-width: 1280px)";

export type Layout = "phone" | "tablet" | "desktop";

export function useMediaQuery(query: string): boolean {
  const get = () => typeof window !== "undefined" && window.matchMedia(query).matches;
  const [matches, setMatches] = useState(get);
  useEffect(() => {
    const mql = window.matchMedia(query);
    const onChange = () => setMatches(mql.matches);
    onChange();
    mql.addEventListener("change", onChange);
    return () => mql.removeEventListener("change", onChange);
  }, [query]);
  return matches;
}

/** phone ≤ 700 px < tablet < 1280 px ≤ desktop */
export function useLayout(): Layout {
  const phone = useMediaQuery(PHONE_QUERY);
  const desktop = useMediaQuery(DESKTOP_QUERY);
  return phone ? "phone" : desktop ? "desktop" : "tablet";
}

export const useIsPhone = () => useMediaQuery(PHONE_QUERY);

/** Smoothly scroll an element into view. */
export function scrollToEl(el: Element | null, block: ScrollLogicalPosition = "start") {
  el?.scrollIntoView({ behavior: "smooth", block });
}
