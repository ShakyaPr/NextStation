import { useEffect, useState } from "react";

/** Phone / small-tablet layout. Keep in sync with the @media rule in styles.css. */
export const MOBILE_QUERY = "(max-width: 860px)";

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

export const useIsMobile = () => useMediaQuery(MOBILE_QUERY);

/** Smoothly scroll an element into view (used on phones, where the map is below the form). */
export function scrollToEl(el: Element | null, block: ScrollLogicalPosition = "start") {
  el?.scrollIntoView({ behavior: "smooth", block });
}
