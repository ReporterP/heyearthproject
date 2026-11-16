// Line icons for the whole site (spec 01 §8): 24×24, stroke 2, round caps, colour from the text.
// Paths are the ones drawn in the «Канон» artboards. Decorative by default (aria-hidden);
// an icon-only button carries its own aria-label.
const P = {
  arrowRight: <path d="M5 12h14M13 6l6 6-6 6" />,
  back: <path d="M15 6l-6 6 6 6" />,
  check: <path d="M5 12l5 5L20 7" />,
  x: <path d="M6 6l12 12M18 6L6 18" />,
  home: <path d="M3 11l9-7 9 7v9a1 1 0 0 1-1 1h-5v-6H9v6H4a1 1 0 0 1-1-1z" />,
  power: <><path d="M12 3v8" /><path d="M6.3 6.3a8 8 0 1 0 11.4 0" /></>,
  wallet: <><rect x="3" y="6" width="18" height="14" rx="2" /><path d="M3 10h18M16 15h2" /></>,
  receipt: <><path d="M6 3h12v18l-3-2-3 2-3-2-3 2z" /><path d="M9 8h6M9 12h6" /></>,
  gift: <><rect x="3" y="8" width="18" height="4" rx="1" /><path d="M12 8v13M19 12v9H5v-9" /></>,
  settings: <><circle cx="12" cy="12" r="3" /><path d="M12 2v3M12 19v3M2 12h3M19 12h3M4.9 4.9l2.1 2.1M17 17l2.1 2.1M4.9 19.1L7 17M17 7l2.1-2.1" /></>,
  help: <><circle cx="12" cy="12" r="9" /><path d="M9.5 9a2.5 2.5 0 1 1 3.5 2.3c-.6.3-1 .9-1 1.7M12 17h.01" /></>,
  building: <><rect x="4" y="3" width="16" height="18" rx="1" /><path d="M9 7h1M14 7h1M9 11h1M14 11h1M9 15h1M14 15h1M10 21v-3h4v3" /></>,
  qr: <><rect x="3" y="3" width="7" height="7" rx="1" /><rect x="14" y="3" width="7" height="7" rx="1" /><rect x="3" y="14" width="7" height="7" rx="1" /><path d="M14 14h3v3M21 14v7h-7" /></>,
  send: <><path d="m22 2-7 20-4-9-9-4Z" /><path d="M22 2 11 13" /></>,
  copy: <><rect x="9" y="9" width="12" height="12" rx="2" /><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" /></>,
  link: <><path d="M10 13a5 5 0 0 0 7.5.5l3-3a5 5 0 0 0-7-7l-1.7 1.7" /><path d="M14 11a5 5 0 0 0-7.5-.5l-3 3a5 5 0 0 0 7 7l1.7-1.7" /></>,
  info: <><circle cx="12" cy="12" r="9" /><path d="M12 11v5M12 8h.01" /></>,
  warn: <><path d="M12 3l9 16H3z" /><path d="M12 10v4M12 17h.01" /></>,
  bad: <><circle cx="12" cy="12" r="9" /><path d="M15 9l-6 6M9 9l6 6" /></>,
  ok: <><circle cx="12" cy="12" r="9" /><path d="M8 12l3 3 5-6" /></>,
  shield: <path d="M12 3l8 3v6c0 5-3.5 8.5-8 10-4.5-1.5-8-5-8-10V6z" />,
  lock: <><rect x="4" y="11" width="16" height="10" rx="2" /><path d="M8 11V7a4 4 0 0 1 8 0v4" /></>,
  phone: <><rect x="6" y="2" width="12" height="20" rx="3" /><path d="M11 18h2" /></>,
  android: <><rect x="5" y="8" width="14" height="11" rx="2" /><path d="M8 8a4 4 0 0 1 8 0M9 4l1 2M15 4l-1 2M9 19v2M15 19v2" /></>,
  monitor: <><rect x="3" y="4" width="18" height="12" rx="2" /><path d="M8 20h8M12 16v4" /></>,
  download: <><path d="M12 3v12M7 10l5 5 5-5" /><path d="M5 21h14" /></>,
  plus: <path d="M12 5v14M5 12h14" />,
  card: <><rect x="3" y="5" width="18" height="14" rx="2" /><path d="M3 10h18" /></>,
  topup: <><path d="M12 5v14M5 12h14" /><circle cx="12" cy="12" r="9" /></>,
  refund: <><path d="M4 9h11a5 5 0 0 1 0 10H9" /><path d="M8 5L4 9l4 4" /></>,
  mail: <><rect x="3" y="5" width="18" height="14" rx="2" /><path d="M3 7l9 6 9-6" /></>,
  logout: <><path d="M15 4h4a1 1 0 0 1 1 1v14a1 1 0 0 1-1 1h-4" /><path d="M10 17l-5-5 5-5M5 12h11" /></>,
  file: <><path d="M14 3H6a1 1 0 0 0-1 1v16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V8z" /><path d="M14 3v5h5" /></>,
  user: <><circle cx="12" cy="8" r="4" /><path d="M4 21a8 8 0 0 1 16 0" /></>,
  refresh: <><path d="M20 11a8 8 0 1 0-2.3 5.7" /><path d="M20 4v7h-7" /></>,
  external: <><path d="M14 4h6v6M20 4l-9 9" /><path d="M19 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1h5" /></>,
  circle: <circle cx="12" cy="12" r="8" />,
  clock: <><circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" /></>,
}

export default function Icon({ name, size, className = '', strokeWidth = 2, style, label }) {
  const body = P[name]
  if (!body) return null
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={strokeWidth}
      strokeLinecap="round"
      strokeLinejoin="round"
      width={size}
      height={size}
      className={className}
      style={style}
      {...(label ? { role: 'img', 'aria-label': label } : { 'aria-hidden': 'true', focusable: 'false' })}
    >
      {body}
    </svg>
  )
}
