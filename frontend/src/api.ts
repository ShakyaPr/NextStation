import { apiFetch } from "./auth";

export interface LatLng {
  lat: number;
  lng: number;
}

export interface Vacancy {
  province: string;
  district: string;
  institution: string;
  designation: string;
  type: string;
  page: number;
}

export interface LocationEntry {
  status: "ok" | "failed";
  location?: LatLng;
  source?: "places" | "geocode" | "geocode-town" | "manual";
  query?: string;
  matchedName?: string;
  address?: string;
  districtDistanceKm?: number;
  suspect?: boolean;
  error?: string;
}

export interface Station {
  id: string;
  name: string;
  district: string;
  province: string;
  category: string;
  vacancies: Vacancy[];
  location?: LocationEntry | null;
}

export interface CountItem {
  name: string;
  count: number;
}

export interface GeocodeProgress {
  running: boolean;
  total: number;
  done: number;
  failed: number;
  lastError?: string;
}

export interface Meta {
  apiKeyConfigured: boolean;
  travelMode: string;
  defaultLimit: number;
  stationCount: number;
  vacancyCount: number;
  locatedCount: number;
  suspectCount: number;
  provinces: CountItem[];
  districts: CountItem[];
  districtProvince: Record<string, string>;
  types: CountItem[];
  categories: CountItem[];
  geocoding: GeocodeProgress;
}

export interface PlaceSuggestion {
  placeId: string;
  mainText: string;
  secondaryText?: string;
}

export interface StationSuggestion {
  id: string;
  name: string;
  district: string;
  category: string;
  location: LatLng;
}

export interface AutocompleteResponse {
  places: PlaceSuggestion[];
  stations: StationSuggestion[];
  error?: string;
}

export interface PlaceDetails {
  location: LatLng;
  name?: string;
  address?: string;
  placeId?: string;
}

export interface SearchRequest {
  origin: { query?: string; lat?: number; lng?: number; label?: string };
  maxDistanceKm?: number;
  maxDurationMin?: number;
  limit?: number;
  sortBy?: "duration" | "distance";
  provinces?: string[];
  districts?: string[];
  types?: string[];
  categories?: string[];
  designation?: string;
}

export interface SearchResult {
  rank: number;
  station: Station;
  location: LocationEntry;
  distanceKm: number;
  durationMin: number;
  straightLineKm: number;
  vacancies: Vacancy[];
}

export interface SearchResponse {
  origin: LatLng & { address?: string };
  results: SearchResult[];
  stats: {
    stationsMatchingFilters: number;
    withoutLocation: number;
    routeCandidates: number;
    withinLimits: number;
    cacheHits: number;
  };
  warnings?: string[];
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await apiFetch(path, {
    method,
    headers: body ? { "Content-Type": "application/json" } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error ?? `HTTP ${res.status}`);
  return data as T;
}

export const api = {
  meta: () => request<Meta>("GET", "api/meta"),
  stations: () => request<Station[]>("GET", "api/stations"),
  search: (req: SearchRequest) => request<SearchResponse>("POST", "api/search", req),
  geocode: (retryFailed: boolean) =>
    request<GeocodeProgress>("POST", `api/geocode${retryFailed ? "?retryFailed=true" : ""}`),
  geocodeStatus: () => request<GeocodeProgress>("GET", "api/geocode/status"),
  setLocation: (id: string, p: LatLng) =>
    request<LocationEntry>("PUT", `api/stations/${encodeURIComponent(id)}/location`, p),
  autocomplete: (q: string, session: string, signal?: AbortSignal) =>
    apiFetch(`api/places/autocomplete?q=${encodeURIComponent(q)}&session=${encodeURIComponent(session)}`, { signal }).then(
      async (r) => {
        const data = await r.json();
        if (!r.ok) throw new Error(data.error ?? `HTTP ${r.status}`);
        return data as AutocompleteResponse;
      },
    ),
  placeDetails: (placeId: string, session: string) =>
    request<PlaceDetails>(
      "GET",
      `api/places/details?placeId=${encodeURIComponent(placeId)}&session=${encodeURIComponent(session)}`,
    ),
  clearLocation: (id: string) =>
    request<LocationEntry | null>("DELETE", `api/stations/${encodeURIComponent(id)}/location`),
};

export function formatDuration(min: number): string {
  const m = Math.round(min);
  if (m < 60) return `${m} min`;
  const h = Math.floor(m / 60);
  return m % 60 ? `${h} h ${m % 60} min` : `${h} h`;
}

export function titleCase(s: string): string {
  return s
    .toLowerCase()
    .replace(/\b([a-z])/g, (c) => c.toUpperCase())
    .replace(/\b(Dh|Bh|Dgh|Pmcu|Moh|Rdhs|Th|Std|Icu|Etu|Opd|Mo|Sho|Moic|Fmo|Pbu|Pcu|Ent|Gu|Gi)\b/g, (w) =>
      w.toUpperCase(),
    );
}

export function locationQuality(e?: LocationEntry | null): { label: string; tone: "good" | "warn" | "bad" } {
  if (!e || !e.location) return { label: e?.status === "failed" ? "Not found" : "Pending", tone: "bad" };
  if (e.source === "manual") return { label: "Manual", tone: "good" };
  if (e.suspect) return { label: "Check location", tone: "bad" };
  if (e.source === "geocode-town") return { label: "Town centre", tone: "warn" };
  return { label: "Located", tone: "good" };
}
