// Icons: the SVG icon primitive and the shared icon set.
//
// The Icon component renders a single 24x24 stroked path (or array of
// paths) with a configurable size + stroke. The Icons object is the
// shared registry — every consumer references it by name to keep the
// visual language consistent and prevent one-off SVGs leaking into
// individual components.

type IconProps = { d: string | string[]; size?: number; strokeWidth?: number; fill?: string };

export function Icon({ d, size = 16, strokeWidth = 1.5, fill = "none" }: IconProps) {
  return (
    <svg
      aria-hidden="true"
      focusable="false"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill={fill}
      stroke="currentColor"
      strokeWidth={strokeWidth}
      strokeLinecap="round"
      strokeLinejoin="round"
      style={{ flexShrink: 0 }}
    >
      {Array.isArray(d) ? d.map((p, i) => <path key={i} d={p} />) : <path d={d} />}
    </svg>
  );
}

export const Icons = {
  chat: "M8 12h.01M12 12h.01M16 12h.01M21 12c0 4.418-4.03 8-9 8a9.863 9.863 0 01-4.255-.949L3 20l1.395-3.72C3.512 15.042 3 13.574 3 12c0-4.418 4.03-8 9-8s9 3.582 9 8z",
  tasks:
    "M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2m-6 9l2 2 4-4",
  providers: [
    "M5 12h14",
    "M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2",
    "M9 10h.01",
    "M9 16h.01",
  ],
  connections: [
    "M9.75 7.5v3.75m4.5-3.75v3.75",
    "M7.5 11.25h9v2.25a4.5 4.5 0 01-9 0v-2.25z",
    "M12 18v3",
    "M9 21h6",
    "M8.25 3.75v3.75",
    "M15.75 3.75v3.75",
  ],
  keys: "M15 7a2 2 0 012 2m4 0a6 6 0 01-7.743 5.743L11 17H9v2H7v2H4a1 1 0 01-1-1v-2.586a1 1 0 01.293-.707l5.964-5.964A6 6 0 1121 9z",
  observe:
    "M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z",
  settings: [
    "M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z",
    "M15 12a3 3 0 11-6 0 3 3 0 016 0z",
  ],
  user: ["M20 21a8 8 0 00-16 0", "M12 11a4 4 0 100-8 4 4 0 000 8z"],
  chevL: "M15 19l-7-7 7-7",
  chevR: "M9 5l7 7-7 7",
  chevD: "M19 9l-7 7-7-7",
  plus: "M12 4v16m8-8H4",
  copy: "M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z",
  check: "M5 13l4 4L19 7",
  x: "M6 18L18 6M6 6l12 12",
  refresh:
    "M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15",
  terminal: "M8 9l3 3-3 3m5 0h3M5 20h14a2 2 0 002-2V6a2 2 0 00-2-2H5a2 2 0 00-2 2v12a2 2 0 002 2z",
  folder: ["M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2V7z"],
  projects: [
    "M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v10a2 2 0 01-2 2H5a2 2 0 01-2-2V7z",
    "M8 12h8",
    "M8 16h5",
  ],
  open: ["M14 3h7v7", "M10 14L21 3", "M21 14v5a2 2 0 01-2 2H5a2 2 0 01-2-2V5a2 2 0 012-2h5"],
  send: "M12 19l9 2-9-18-9 18 9-2zm0 0v-8",
  stop: "M8 8h8v8H8z",
  volume: ["M11 5L6 9H3v6h3l5 4V5z", "M15.5 8.5a5 5 0 010 7", "M19 5a10 10 0 010 14"],
  microphone: [
    "M12 3a3 3 0 00-3 3v6a3 3 0 006 0V6a3 3 0 00-3-3z",
    "M19 10v2a7 7 0 01-14 0v-2",
    "M12 19v3",
    "M8 22h8",
  ],
  edit: "M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z",
  trash:
    "M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16",
  warning:
    "M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z",
  info: "M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z",
  activity: "M22 12h-4l-3 9L9 3l-3 9H2",
  approve: "M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z",
  deny: "M10 14l2-2m0 0l2-2m-2 2l-2-2m2 2l2 2m7-2a9 9 0 11-18 0 9 9 0 0118 0z",
  retry:
    "M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15",
  revert: ["M9 14l-5-5 5-5", "M4 9h11a5 5 0 010 10h-1"],
  model:
    "M21 16V8a2 2 0 00-1-1.73l-7-4a2 2 0 00-2 0l-7 4A2 2 0 003 8v8a2 2 0 001 1.73l7 4a2 2 0 002 0l7-4A2 2 0 0021 16z",
  branch: [
    "M6 3v12",
    "M18 9a3 3 0 100-6 3 3 0 000 6z",
    "M6 21a3 3 0 100-6 3 3 0 000 6z",
    "M18 9a9 9 0 01-9 9",
  ],
  eye: [
    "M15 12a3 3 0 11-6 0 3 3 0 016 0z",
    "M2.458 12C3.732 7.943 7.523 5 12 5c4.478 0 8.268 2.943 9.542 7-1.274 4.057-5.064 7-9.542 7-4.477 0-8.268-2.943-9.542-7z",
  ],
  search: "M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z",
  // Broom — used by cleanup actions. The shape: a diagonal handle, a
  // pentagonal brush head, three short vertical bristles below.
  broom: [
    "M19.5 4.5L11.5 12.5",
    "M11.5 12.5L8.5 15.5L8.5 18.5L11.5 18.5L14.5 15.5Z",
    "M8 19V22 M11 19V22 M14 19V22",
  ],
};
