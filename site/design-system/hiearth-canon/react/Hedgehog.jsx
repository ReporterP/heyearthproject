// HiEarth mascot. One SVG for every mood; the mood is a class (see hiearth.css § 10).
// Usage: <Hedgehog mood="happy" size={140} />. Always decorative: the meaning lives in the text next to it.
const MOODS = ['idle', 'happy', 'sleep', 'worried', 'search']

export default function Hedgehog({ mood = 'idle', size = 132, className = '', style }) {
  const m = MOODS.includes(mood) ? mood : 'idle'
  return (
    <svg
      className={`hh hh--${m} ${className}`}
      viewBox="0 0 132 96"
      width={size}
      aria-hidden="true"
      focusable="false"
      style={style}
    >
      <path className="hh-spark" d="M30 4 L32 10 L38 12 L32 14 L30 20 L28 14 L22 12 L28 10 Z" />
      <path className="hh-spark" style={{ animationDelay: '.5s' }} d="M104 2 L105.5 7 L110 8.5 L105.5 10 L104 15 L102.5 10 L98 8.5 L102.5 7 Z" />
      <text className="hh-z" x="104" y="30">z</text>
      <text className="hh-z" x="116" y="18">z</text>
      <g className="hh-breath">
        <path className="hh-spikes" d="M18 58 L10 42 L26 45 L22 27 L38 35 L40 17 L53 29 L60 11 L69 27 L80 14 L83 32 L97 24 L96 42 L108 40" />
        <path className="hh-body" d="M14 62 C14 38 46 28 72 30 C96 32 110 46 116 60 C108 72 90 76 68 76 C42 76 16 74 14 62 Z" />
        <circle className="hh-fill" cx="117" cy="61" r="4.2" />
        <circle className="hh-cheek" cx="103" cy="62" r="4" />
        <circle className="hh-fill hh-eye-open" cx="96" cy="50" r="3.4" />
        <path className="hh-line hh-eye-happy" d="M91 51 Q96 45 101 51" />
        <path className="hh-line hh-eye-sleep" d="M91 50 H101" />
        <path className="hh-line hh-brow" d="M89 42 L100 45" />
        <path className="hh-line hh-smile" d="M104 67 Q108 70 112 67" />
        <path className="hh-drop" d="M110 30 C110 30 105 37 105 40 A5 5 0 0 0 115 40 C115 37 110 30 110 30 Z" />
      </g>
      <path className="hh-line" d="M44 76 V85 M62 77 V86 M86 76 V85" />
      <g className="hh-glass">
        <circle cx="122" cy="74" r="8" />
        <path d="M127 80 L132 86" />
      </g>
    </svg>
  )
}
