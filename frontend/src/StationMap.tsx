import { useEffect, useRef, type ReactNode } from "react";
import L from "leaflet";
import { MapContainer, Marker, Polyline, Popup, TileLayer, useMap, useMapEvents } from "react-leaflet";
import type { LatLng } from "./api";
import { FitIcon, LocateIcon, MinusIcon, PlusIcon } from "./icons";

export interface MapMarker {
  id: string;
  pos: LatLng;
  label: string;
  tone: "good" | "warn" | "bad" | "muted";
}

interface Props {
  origin?: LatLng | null;
  originLabel?: string;
  markers: MapMarker[];
  /** "rank": numbered result pins. "status": small dots coloured by location quality. */
  pinStyle?: "rank" | "status";
  selectedId?: string | null;
  /** Card shown above the selected marker (desktop/tablet). */
  selectedPopup?: ReactNode;
  onPopupClose?: () => void;
  onMapClick?: (p: LatLng) => void;
  onMarkerClick?: (id: string) => void;
  fitKey?: string; // change to re-fit bounds
  crosshair?: boolean;
  /** Dashed straight line from the starting point to the selected station. */
  showLink?: boolean;
  compactControls?: boolean;
  fitLabel?: string;
  /** Zoom in (not just pan) to a newly selected marker. */
  zoomOnSelect?: boolean;
}

const SRI_LANKA_CENTER: [number, number] = [7.8731, 80.7718];

const escapeHtml = (s: string) =>
  s.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]!);

function rankIcon(label: string, selected: boolean) {
  const size = selected ? 34 : 28;
  return L.divIcon({
    className: "",
    html: `<div class="pin${selected ? " on" : ""}" style="width:${size}px;height:${size}px">${escapeHtml(label)}</div>`,
    iconSize: [size, size],
    iconAnchor: [size / 2, size / 2],
  });
}

function statusIcon(tone: string, selected: boolean) {
  const size = selected ? 22 : 14;
  return L.divIcon({
    className: "",
    html: `<div class="dot-pin t-${tone}${selected ? " on" : ""}"></div>`,
    iconSize: [size, size],
    iconAnchor: [size / 2, size / 2],
  });
}

function originIcon(label?: string) {
  return L.divIcon({
    className: "",
    html: `<div class="origin-pin"><span class="origin-halo"></span><span class="origin-core"></span>${
      label ? `<span class="origin-label">${escapeHtml(label)}</span>` : ""
    }</div>`,
    iconSize: [44, 44],
    iconAnchor: [22, 22],
  });
}

function boundsOf(markers: MapMarker[], origin?: LatLng | null): [number, number][] {
  const pts: [number, number][] = markers.map((m) => [m.pos.lat, m.pos.lng]);
  if (origin) pts.push([origin.lat, origin.lng]);
  return pts;
}

function fit(map: L.Map, pts: [number, number][]) {
  if (pts.length === 1) map.setView(pts[0], 11);
  else if (pts.length > 1) map.fitBounds(pts, { padding: [48, 48], maxZoom: 13 });
}

function ClickHandler({ onClick }: { onClick?: (p: LatLng) => void }) {
  useMapEvents({
    click(e) {
      onClick?.({ lat: e.latlng.lat, lng: e.latlng.lng });
    },
  });
  return null;
}

function FitBounds({ origin, markers, fitKey }: Pick<Props, "origin" | "markers" | "fitKey">) {
  const map = useMap();
  useEffect(() => {
    fit(map, boundsOf(markers, origin));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fitKey]);
  return null;
}

/** Keep the selected station in view without changing the zoom. */
function KeepInView({ pos, zoom }: { pos?: LatLng | null; zoom?: boolean }) {
  const map = useMap();
  useEffect(() => {
    if (!pos) return;
    if (zoom && map.getZoom() < 12) map.setView([pos.lat, pos.lng], 12);
    else if (!map.getBounds().pad(-0.1).contains([pos.lat, pos.lng])) map.panTo([pos.lat, pos.lng]);
  }, [pos, zoom, map]);
  return null;
}

/** Leaflet needs to re-measure when its container is resized (drawers, layout changes). */
function AutoResize() {
  const map = useMap();
  useEffect(() => {
    const ro = new ResizeObserver(() => map.invalidateSize());
    ro.observe(map.getContainer());
    return () => ro.disconnect();
  }, [map]);
  return null;
}

function ScaleBar() {
  const map = useMap();
  useEffect(() => {
    const c = L.control.scale({ imperial: false, position: "bottomleft" }).addTo(map);
    return () => {
      c.remove();
    };
  }, [map]);
  return null;
}

function Controls({
  markers,
  origin,
  compact,
  fitLabel,
}: {
  markers: MapMarker[];
  origin?: LatLng | null;
  compact?: boolean;
  fitLabel: string;
}) {
  const map = useMap();
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!ref.current) return;
    L.DomEvent.disableClickPropagation(ref.current);
    L.DomEvent.disableScrollPropagation(ref.current);
  }, []);
  return (
    <div ref={ref} className="map-controls">
      {markers.length > 0 ? (
        <button type="button" className="map-fit" onClick={() => fit(map, boundsOf(markers, origin))}>
          <FitIcon size={14} />
          {fitLabel}
        </button>
      ) : (
        <span />
      )}
      <div className="map-buttons">
        {!compact && (
          <div className="map-zoom">
            <button type="button" aria-label="Zoom in" onClick={() => map.zoomIn()}>
              <PlusIcon />
            </button>
            <button type="button" aria-label="Zoom out" onClick={() => map.zoomOut()}>
              <MinusIcon />
            </button>
          </div>
        )}
        {origin && (
          <button
            type="button"
            className="map-center"
            aria-label="Center on the starting point"
            onClick={() => map.setView([origin.lat, origin.lng], Math.max(map.getZoom(), 12))}
          >
            <LocateIcon size={compact ? 18 : 16} />
          </button>
        )}
      </div>
    </div>
  );
}

export default function StationMap({
  origin,
  originLabel,
  markers,
  pinStyle = "rank",
  selectedId,
  selectedPopup,
  onPopupClose,
  onMapClick,
  onMarkerClick,
  fitKey,
  crosshair,
  showLink,
  compactControls,
  fitLabel = "Fit to results",
  zoomOnSelect,
}: Props) {
  const selected = markers.find((m) => m.id === selectedId);
  // Unmounting the previous popup also fires "remove": only report a close for the current one.
  const selectedRef = useRef(selectedId);
  selectedRef.current = selectedId;
  return (
    <div className={`map-wrap${crosshair ? " crosshair" : ""}`}>
      <MapContainer center={SRI_LANKA_CENTER} zoom={7} scrollWheelZoom zoomControl={false} className="map">
        <TileLayer
          attribution='&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors'
          url="https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png"
        />
        <AutoResize />
        <ScaleBar />
        <ClickHandler onClick={onMapClick} />
        <FitBounds origin={origin} markers={markers} fitKey={fitKey} />
        {!selectedPopup && <KeepInView pos={selected?.pos} zoom={zoomOnSelect} />}
        <Controls markers={markers} origin={origin} compact={compactControls} fitLabel={fitLabel} />
        {showLink && origin && selected && (
          <Polyline
            positions={[
              [origin.lat, origin.lng],
              [selected.pos.lat, selected.pos.lng],
            ]}
            pathOptions={{ color: "#C24E00", weight: 3, opacity: 0.75, dashArray: "2 8", lineCap: "round" }}
            interactive={false}
          />
        )}
        {origin && (
          <Marker position={[origin.lat, origin.lng]} icon={originIcon(originLabel)} zIndexOffset={1000} interactive={false} />
        )}
        {markers.map((m) => (
          <Marker
            key={m.id}
            position={[m.pos.lat, m.pos.lng]}
            icon={pinStyle === "rank" ? rankIcon(m.label, m.id === selectedId) : statusIcon(m.tone, m.id === selectedId)}
            zIndexOffset={m.id === selectedId ? 900 : 0}
            title={pinStyle === "status" ? m.label : undefined}
            eventHandlers={{ click: () => onMarkerClick?.(m.id) }}
          />
        ))}
        {selected && selectedPopup && (
          <Popup
            key={selected.id}
            position={[selected.pos.lat, selected.pos.lng]}
            offset={[0, -12]}
            closeButton={false}
            autoPanPadding={[24, 24]}
            className="station-popup"
            eventHandlers={{
              remove: () => {
                if (selectedRef.current === selected.id) onPopupClose?.();
              },
            }}
          >
            {selectedPopup}
          </Popup>
        )}
      </MapContainer>
    </div>
  );
}
