import { useEffect } from "react";
import L from "leaflet";
import { MapContainer, Marker, Popup, TileLayer, useMap, useMapEvents } from "react-leaflet";
import type { LatLng } from "./api";

export interface MapMarker {
  id: string;
  pos: LatLng;
  label: string;
  tone: "good" | "warn" | "bad" | "muted";
  popup: React.ReactNode;
}

interface Props {
  origin?: LatLng | null;
  markers: MapMarker[];
  selectedId?: string | null;
  onMapClick?: (p: LatLng) => void;
  onMarkerClick?: (id: string) => void;
  fitKey?: string; // change to re-fit bounds
  crosshair?: boolean;
}

const SRI_LANKA_CENTER: [number, number] = [7.8731, 80.7718];

function pinIcon(label: string, tone: string, selected: boolean) {
  return L.divIcon({
    className: "",
    html: `<div class="pin pin-${tone}${selected ? " pin-selected" : ""}">${label}</div>`,
    iconSize: [26, 26],
    iconAnchor: [13, 13],
    popupAnchor: [0, -14],
  });
}

const originIcon = L.divIcon({
  className: "",
  html: `<div class="pin-origin"></div>`,
  iconSize: [20, 20],
  iconAnchor: [10, 10],
});

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
    const pts: [number, number][] = markers.map((m) => [m.pos.lat, m.pos.lng]);
    if (origin) pts.push([origin.lat, origin.lng]);
    if (pts.length === 1) map.setView(pts[0], 11);
    else if (pts.length > 1) map.fitBounds(pts, { padding: [40, 40], maxZoom: 13 });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fitKey]);
  return null;
}

function PanTo({ pos }: { pos?: LatLng | null }) {
  const map = useMap();
  useEffect(() => {
    if (pos) map.setView([pos.lat, pos.lng], Math.max(map.getZoom(), 12));
  }, [pos, map]);
  return null;
}

export default function StationMap({ origin, markers, selectedId, onMapClick, onMarkerClick, fitKey, crosshair }: Props) {
  const selected = markers.find((m) => m.id === selectedId);
  return (
    <div className={`map-wrap${crosshair ? " crosshair" : ""}`}>
      <MapContainer center={SRI_LANKA_CENTER} zoom={7} scrollWheelZoom className="map">
        <TileLayer
          attribution='&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors'
          url="https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png"
        />
        <ClickHandler onClick={onMapClick} />
        <FitBounds origin={origin} markers={markers} fitKey={fitKey} />
        <PanTo pos={selected?.pos} />
        {origin && (
          <Marker position={[origin.lat, origin.lng]} icon={originIcon} zIndexOffset={1000}>
            <Popup>Starting point</Popup>
          </Marker>
        )}
        {markers.map((m) => (
          <Marker
            key={m.id}
            position={[m.pos.lat, m.pos.lng]}
            icon={pinIcon(m.label, m.tone, m.id === selectedId)}
            zIndexOffset={m.id === selectedId ? 900 : 0}
            eventHandlers={{ click: () => onMarkerClick?.(m.id) }}
          >
            <Popup>{m.popup}</Popup>
          </Marker>
        ))}
      </MapContainer>
    </div>
  );
}
