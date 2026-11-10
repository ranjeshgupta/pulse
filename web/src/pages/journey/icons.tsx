// Stroke icons for journey steps (24px grid, currentColor), named by the catalog.
import type { JIconName } from "../../journeyCatalog";

const PATHS: Record<JIconName, string> = {
  mail: "M3 5h18v14H3zM3 6l9 7 9-7",
  chat: "M4 4h16v12H8l-4 4zM8 9h8M8 12h5",
  whatsapp: "M4 20l1.4-4.2A8 8 0 1 1 8.2 18.6zM9 9c0 3 3 6 6 6l1-1.5-2-1-1 1c-1-.5-2-1.5-2.5-2.5l1-1-1-2z",
  bell: "M6 16V11a6 6 0 1 1 12 0v5l2 2H4zM10 20a2 2 0 0 0 4 0",
  inbox: "M3 13l3-8h12l3 8v6H3zM3 13h5l1 2h6l1-2h5",
  send: "M21 3L10 14M21 3l-7 18-4-7-7-4z",
  "user-mail": "M9 11a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7zM2 20a7 7 0 0 1 11-5.7M14 15h8v6h-8zM14 15.5l4 3 4-3",
  focus: "M3 5h18v14H3zM7 9h10v6H7z",
  layout: "M3 4h18v16H3zM3 9h18M9 9v11",
  webhook: "M10 14a4 4 0 0 1 0-5.7l3-3a4 4 0 0 1 5.7 5.7l-1.5 1.5M14 10a4 4 0 0 1 0 5.7l-3 3a4 4 0 0 1-5.7-5.7l1.5-1.5",
  user: "M12 12a4 4 0 1 0 0-8 4 4 0 0 0 0 8zm-7 8a7 7 0 0 1 14 0",
  "user-check": "M10 12a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM3 20a7 7 0 0 1 11-5.7M15 18l2 2 4-4",
  tag: "M3 12V4h8l10 10-8 8zM7.5 7.5h.01",
  star: "M12 3l2.7 5.6 6.1.9-4.4 4.3 1 6.1L12 17l-5.4 2.9 1-6.1-4.4-4.3 6.1-.9z",
  flag: "M5 21V4M5 4h11l-2 4 2 4H5",
  layers: "M12 3l9 5-9 5-9-5zM3 13l9 5 9-5",
  route: "M6 19a2 2 0 1 0 0-4 2 2 0 0 0 0 4zM18 9a2 2 0 1 0 0-4 2 2 0 0 0 0 4zM6 15V9a4 4 0 0 1 4-4h6M18 9v6a4 4 0 0 1-4 4H8",
  shield: "M12 3l8 3v6c0 4.5-3.4 8-8 9-4.6-1-8-4.5-8-9V6l8-3zM8 12h8",
  "shield-off": "M12 3l8 3v6c0 4.5-3.4 8-8 9-4.6-1-8-4.5-8-9V6l8-3zM9 12l2 2 4-4",
  building: "M4 21V5l8-3v19M12 9h8v12M7 8h2M7 12h2M7 16h2M15 13h2M15 17h2M2 21h20",
  "building-plus": "M4 21V5l8-3v19M12 11h4M7 8h2M7 12h2M7 16h2M2 21h12M19 13v8M15 17h8",
  trend: "M3 17l6-6 4 4 8-8M15 7h6v6",
  jump: "M7 20V10a4 4 0 0 1 4-4h9M16 2l4 4-4 4",
  trash: "M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13M10 11v6M14 11v6",
  plug: "M9 2v5M15 2v5M6 7h12v4a6 6 0 0 1-12 0zM12 17v5",
  exit: "M10 4H5v16h5M14 8l4 4-4 4M18 12H9",
  clock: "M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM12 7v5l3 2",
  calendar: "M4 5h16v16H4zM4 10h16M8 3v4M16 3v4",
  split: "M12 3v6M12 9l-6 6v6M12 9l6 6v6",
  percent: "M19 5L5 19M7 9a2 2 0 1 0 0-4 2 2 0 0 0 0 4zM17 19a2 2 0 1 0 0-4 2 2 0 0 0 0 4z",
  eye: "M2 12s4-7 10-7 10 7 10 7-4 7-10 7S2 12 2 12zm10 3a3 3 0 1 0 0-6 3 3 0 0 0 0 6z",
  cursor: "M5 3l14 7-6 2-2 6zM13 13l5 5",
  reply: "M9 14L4 9l5-5M4 9h10a6 6 0 0 1 6 6v4",
  download: "M12 3v12M7 10l5 5 5-5M4 20h16",
  form: "M5 3h14v18H5zM8 7h8M8 11h8M8 15h5",
  globe: "M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM3 12h18M12 3c2.5 2.5 3.5 5.5 3.5 9s-1 6.5-3.5 9c-2.5-2.5-3.5-5.5-3.5-9s1-6.5 3.5-9z",
  device: "M7 2h10a1 1 0 0 1 1 1v18a1 1 0 0 1-1 1H7a1 1 0 0 1-1-1V3a1 1 0 0 1 1-1zM11 18h2",
  code: "M8 8l-4 4 4 4M16 8l4 4-4 4M14 4l-4 16",
  bolt: "M13 2L4 14h7l-1 8 9-12h-7z",
  filter: "M3 4h18l-7 9v6l-4 2v-8z",
  "check-mail": "M3 5h18v8M3 5v14h9M3 6l9 7 9-7M15 18l2 2 4-4",
  hourglass: "M6 2h12M6 22h12M7 2c0 5 10 5 10 10S7 17 7 22M17 2c0 5-10 5-10 10s10 5 10 10",
  play: "M6 4l14 8-14 8z",
  segment: "M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM12 3v9l6.4 6.4",
};

export function JIcon({ name, size = 16 }: { name: JIconName; size?: number }) {
  return (
    <svg className="j-icon" width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false">
      <path d={PATHS[name]} />
    </svg>
  );
}
