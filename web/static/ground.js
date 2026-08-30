/* The ground, drawn.
 *
 * Every venue in the corpus carries a straight and a square boundary in metres,
 * and this draws the place from those two numbers rather than dropping the same
 * oval behind every fixture. Chinnaswamy really is rounder and smaller than
 * Chepauk, Ahmedabad really does dwarf both, and the difference is visible
 * before a ball is bowled, which is the point: the boundary lengths are part of
 * what makes a target hard.
 *
 * This began as a three-dimensional model with a camera in it. That was the
 * wrong tool twice over. A browser without hardware acceleration falls back to
 * a software rasteriser, where the scene was slow enough to freeze the page and
 * so coarse that it was not worth the wait; and a stadium rendered badly looks
 * far worse than a ground drawn well. So it is vector now: a few hundred bytes
 * of geometry, sharp at any size, instant on any machine, and with no
 * dependency behind it.
 *
 * It is drawn in evening light, because that is when the cricket is played. The
 * warmth comes from the palette and from one long shadow across the square, not
 * from anything glowing.
 */

const W = 1600;
const H = 900;

/* Evening. The sun is low and off to the left, so the grass is warm where it
 * catches the light and cool where the far stand shades it. Everything here is
 * a flat fill or a plain gradient; there is no bloom and no glow, because a
 * ground at seven in the evening does not have any. */
const P = {
  skyHigh: '#8FA9C4',
  skyLow: '#F3C888',
  haze: '#F8D9AA',

  standFar: '#A2957F',
  standNear: '#837A68',
  standRoof: '#6E6656',

  grassLit: '#93BD62',
  grassShade: '#82A954',
  grassDeep: '#6E9247',

  rope: '#F6F2E6',
  ring: '#E9E4D4',
  pitch: '#CBA875',
  pitchWorn: '#BC9A68',
  crease: '#FBF8EF',

  crowdA: '#D8CBB4',
  crowdB: '#B9A98F',
  crowdC: '#EFE6D2',

  mast: '#4A4740',
  lamp: '#FFEFC9',
  shadow: '#2E4030',
};

/* The home club's colour, worked into the seats and the roof.
 *
 * A ground is not a neutral box to the people in it: Chepauk is yellow and the
 * Wankhede is blue, and a drawing that ignores that is a diagram of an oval
 * rather than a picture of a place. The tint goes into the stand fabric and
 * nowhere near the grass, and it is mixed heavily with the base so it reads as
 * a coloured stadium rather than as a flat wash of brand colour.
 *
 * Neutral venues and the grounds abroad have no resident club and are left in
 * their own concrete.
 */
function mix(hex, base, amount) {
  const h = (c) => parseInt(c, 16);
  const a = [h(hex.slice(1, 3)), h(hex.slice(3, 5)), h(hex.slice(5, 7))];
  const b = [h(base.slice(1, 3)), h(base.slice(3, 5)), h(base.slice(5, 7))];
  const out = a.map((v, i) => Math.round(b[i] + (v - b[i]) * amount));
  return '#' + out.map((v) => v.toString(16).padStart(2, '0')).join('');
}

export function groundSVG(ground) {
  // A per-call copy, because the tint differs by ground and the palette is
  // shared.
  const C = { ...P };
  if (/^#[0-9a-fA-F]{6}$/.test(ground.colour || '')) {
    C.standFar = mix(ground.colour, P.standFar, 0.5);
    C.standNear = mix(ground.colour, P.standNear, 0.42);
    C.standRoof = mix(ground.colour, P.standRoof, 0.3);
  }

  const straight = ground.straight || 70;
  const square = ground.square || 64;
  const tiers = Math.max(1, Math.min(3, ground.tiers || 2));
  const capacity = ground.capacity || 30000;

  /* The view is from high behind square: a plan flattened vertically, which is
   * how a ground reads on a broadcast wide shot and how it reads on a map. The
   * squash is what turns a diagram into a place. */
  const squash = 0.52;

  // Metres to pixels, scaled so the bowl very nearly fills the frame. It is one
  // scale for every ground rather than one that fits each to the panel, because
  // the whole point is that the grounds are not the same size: Ahmedabad has to
  // look bigger than Sharjah, not merely differently proportioned.
  const unit = 8.6;
  const rx = straight * unit;
  const ry = square * unit * squash;

  const cx = W / 2;
  const cy = H * 0.565;

  // The bowl around the field. More capacity means deeper stands, and each tier
  // steps back and up.
  const depth = 46 + Math.min(70, capacity / 1100);
  const lift = 10 + tiers * 7;

  const parts = [];

  parts.push(`<defs>${defs(C)}</defs>`);
  parts.push(`<rect width="${W}" height="${H}" fill="url(#sky)"/>`);
  parts.push(horizon(cy, ry, rx));

  // The stands, drawn outermost first so each tier overlaps the one behind.
  for (let t = tiers - 1; t >= 0; t--) {
    const grow = depth * ((t + 1) / tiers);
    const rise = lift * ((t + 1) / tiers);
    parts.push(stand(cx, cy - rise, rx + grow, ry + grow * squash, t, tiers));
  }

  if (ground.roof && ground.roof !== 'none') {
    parts.push(roof(cx, cy - lift, rx + depth, ry + depth * squash, ground.roof, C));
  }

  parts.push(field(cx, cy, rx, ry));
  parts.push(mownStripes(cx, cy, rx, ry));
  parts.push(longShadow(cx, cy, rx, ry));
  parts.push(thirtyYards(cx, cy, rx, ry));
  parts.push(boundaryRope(cx, cy, rx, ry));
  parts.push(square22(cx, cy, unit, squash));

  parts.push(
    ground.lights === 'roof'
      ? roofLights(cx, cy - lift, rx + depth * 0.8, ry + depth * 0.8 * squash)
      : pylons(cx, cy, rx + depth, ry + depth * squash, tiers),
  );

  return `<svg viewBox="0 0 ${W} ${H}" xmlns="http://www.w3.org/2000/svg" ` +
    `preserveAspectRatio="xMidYMid slice" role="img" ` +
    `aria-label="${esc(ground.name)}, drawn to its own boundary lengths">` +
    parts.join('') +
    '</svg>';
}

function defs(C) {
  return `
    <linearGradient id="sky" x1="0" y1="0" x2="0.25" y2="1">
      <stop offset="0" stop-color="${P.skyHigh}"/>
      <stop offset="0.55" stop-color="${P.haze}"/>
      <stop offset="1" stop-color="${P.skyLow}"/>
    </linearGradient>

    <linearGradient id="turf" x1="0.1" y1="0" x2="1" y2="0.75">
      <stop offset="0" stop-color="${P.grassLit}"/>
      <stop offset="0.78" stop-color="${P.grassShade}"/>
      <stop offset="1" stop-color="${P.grassDeep}"/>
    </linearGradient>

    <linearGradient id="tierFill" x1="0" y1="0" x2="0.3" y2="1">
      <stop offset="0" stop-color="${C.standFar}"/>
      <stop offset="1" stop-color="${C.standNear}"/>
    </linearGradient>

    <linearGradient id="castShadow" x1="0" y1="0" x2="0.85" y2="0.6">
      <stop offset="0" stop-color="${P.shadow}" stop-opacity="0.22"/>
      <stop offset="0.42" stop-color="${P.shadow}" stop-opacity="0.07"/>
      <stop offset="0.75" stop-color="${P.shadow}" stop-opacity="0"/>
    </linearGradient>

    <!-- The crowd. A speckle rather than thousands of drawn people: at this
         size a full house is a texture, and drawing it as one costs a pattern
         definition instead of sixty thousand nodes. -->
    <pattern id="crowd" width="9" height="9" patternUnits="userSpaceOnUse">
      <rect width="9" height="9" fill="none"/>
      <circle cx="2" cy="2" r="1.5" fill="${P.crowdA}"/>
      <circle cx="6.5" cy="4" r="1.4" fill="${P.crowdB}"/>
      <circle cx="4" cy="7" r="1.5" fill="${P.crowdC}"/>
      <circle cx="8" cy="8" r="1.2" fill="${P.crowdA}"/>
    </pattern>`;
}

/* A band of haze where the stands meet the sky, so the bowl sits in something
 * rather than being cut out of it. */
function horizon(cy, ry, rx) {
  const y = cy - ry * 2.1;
  return `<rect x="0" y="${y}" width="${W}" height="${H - y}" fill="${P.haze}" opacity="0.1"/>`;
}

/* One deck of the bowl: a ring drawn as an outer ellipse with the field-side
 * ellipse punched out of it, so the tiers stack without overdrawing the grass. */
function stand(cx, cy, rx, ry, tier, tiers) {
  const id = `deck${tier}`;
  const inner = 1 - 0.055 * (tiers - tier);
  const shade = 0.55 + 0.45 * ((tier + 1) / tiers);

  // Radial divisions: the vomitories and stand blocks, which is what stops a
  // ring of colour from reading as a racetrack.
  const blocks = [];
  const n = 44;
  for (let i = 0; i < n; i++) {
    const a = (i / n) * Math.PI * 2;
    const x1 = cx + Math.cos(a) * rx * inner;
    const y1 = cy + Math.sin(a) * ry * inner;
    const x2 = cx + Math.cos(a) * rx;
    const y2 = cy + Math.sin(a) * ry;
    // Darker on the far side, where the low sun does not reach.
    const lit = Math.sin(a) > 0 ? 0.1 : 0.22;
    blocks.push(`<line x1="${r(x1)}" y1="${r(y1)}" x2="${r(x2)}" y2="${r(y2)}" ` +
      `stroke="#2A2620" stroke-opacity="${lit}" stroke-width="2"/>`);
  }

  return `
    <mask id="${id}">
      <ellipse cx="${r(cx)}" cy="${r(cy)}" rx="${r(rx)}" ry="${r(ry)}" fill="#fff"/>
      <ellipse cx="${r(cx)}" cy="${r(cy)}" rx="${r(rx * inner)}" ry="${r(ry * inner)}" fill="#000"/>
    </mask>
    <g mask="url(#${id})">
      <ellipse cx="${r(cx)}" cy="${r(cy)}" rx="${r(rx)}" ry="${r(ry)}"
               fill="url(#tierFill)" opacity="${shade.toFixed(2)}"/>
      <ellipse cx="${r(cx)}" cy="${r(cy)}" rx="${r(rx)}" ry="${r(ry)}"
               fill="url(#crowd)" opacity="${(0.5 * shade).toFixed(2)}"/>
      ${blocks.join('')}
    </g>`;
}

function roof(cx, cy, rx, ry, kind, C) {
  const reach = kind === 'full' ? 0.86 : 0.92;
  return `
    <mask id="roofMask">
      <ellipse cx="${r(cx)}" cy="${r(cy - ry * 0.06)}" rx="${r(rx * 1.03)}" ry="${r(ry * 1.03)}" fill="#fff"/>
      <ellipse cx="${r(cx)}" cy="${r(cy - ry * 0.06)}" rx="${r(rx * reach)}" ry="${r(ry * reach)}" fill="#000"/>
    </mask>
    <g mask="url(#roofMask)">
      <ellipse cx="${r(cx)}" cy="${r(cy - ry * 0.06)}" rx="${r(rx * 1.03)}" ry="${r(ry * 1.03)}"
               fill="${C.standRoof}" opacity="0.9"/>
    </g>`;
}

function field(cx, cy, rx, ry) {
  return `
    <ellipse cx="${r(cx)}" cy="${r(cy + 6)}" rx="${r(rx * 1.01)}" ry="${r(ry * 1.01)}"
             fill="${P.shadow}" opacity="0.16"/>
    <ellipse cx="${r(cx)}" cy="${r(cy)}" rx="${r(rx)}" ry="${r(ry)}" fill="url(#turf)"/>`;
}

/* Mown stripes, cut radially because that is how a circular outfield is
 * actually mown, and because parallel stripes across an ellipse look like a
 * mistake from every angle but directly overhead. */
function mownStripes(cx, cy, rx, ry) {
  const wedges = 18;
  const paths = [];
  for (let i = 0; i < wedges; i += 2) {
    const a1 = (i / wedges) * Math.PI * 2;
    const a2 = ((i + 1) / wedges) * Math.PI * 2;
    paths.push(
      `<path d="M ${r(cx)} ${r(cy)} ` +
      `L ${r(cx + Math.cos(a1) * rx)} ${r(cy + Math.sin(a1) * ry)} ` +
      `A ${r(rx)} ${r(ry)} 0 0 1 ${r(cx + Math.cos(a2) * rx)} ${r(cy + Math.sin(a2) * ry)} Z"/>`,
    );
  }
  return `<g fill="${P.grassDeep}" opacity="0.13">${paths.join('')}</g>`;
}

/* The one piece of evening in the picture: the near stand throwing a long
 * shadow across the square as the sun drops. It is a soft gradient clipped to
 * the field, not a light source. */
function longShadow(cx, cy, rx, ry) {
  return `
    <clipPath id="fieldClip">
      <ellipse cx="${r(cx)}" cy="${r(cy)}" rx="${r(rx)}" ry="${r(ry)}"/>
    </clipPath>
    <g clip-path="url(#fieldClip)">
      <path d="M ${r(cx - rx)} ${r(cy - ry)}
               L ${r(cx - rx * 0.1)} ${r(cy - ry)}
               L ${r(cx - rx * 0.55)} ${r(cy + ry)}
               L ${r(cx - rx)} ${r(cy + ry)} Z"
            fill="url(#castShadow)"/>
    </g>`;
}

function thirtyYards(cx, cy, rx, ry) {
  // Thirty yards from the middle, against a boundary measured in metres.
  const k = 27.4 / ((rx + ry / 0.52) / 2 / 8.6);
  return `<ellipse cx="${r(cx)}" cy="${r(cy)}" rx="${r(rx * k)}" ry="${r(ry * k)}"
           fill="none" stroke="${P.ring}" stroke-opacity="0.5"
           stroke-width="2.5" stroke-dasharray="10 9"/>`;
}

function boundaryRope(cx, cy, rx, ry) {
  return `
    <ellipse cx="${r(cx)}" cy="${r(cy)}" rx="${r(rx)}" ry="${r(ry)}"
             fill="none" stroke="${P.rope}" stroke-width="4"/>
    <ellipse cx="${r(cx)}" cy="${r(cy)}" rx="${r(rx - 9)}" ry="${r(ry - 9 * 0.52)}"
             fill="none" stroke="${P.rope}" stroke-opacity="0.18" stroke-width="1.5"/>`;
}

/* The square, the strip and the creases, to the same scale as everything else:
 * a pitch is 20.12 metres long, and at this size that is genuinely small. It
 * being small is the truthful part. */
function square22(cx, cy, unit, squash) {
  const len = 20.12 * unit;
  const wide = 3.05 * unit * squash;
  const squareLen = len * 1.22;
  const squareWide = wide * 4.2;

  const creases = [-1, 1].map((s) => {
    const x = cx + s * (len / 2 - 1.22 * unit);
    return `<rect x="${r(x - 1)}" y="${r(cy - wide * 1.5)}" width="2" height="${r(wide * 3)}" fill="${P.crease}" opacity="0.85"/>`;
  }).join('');

  return `
    <rect x="${r(cx - squareLen / 2)}" y="${r(cy - squareWide / 2)}"
          width="${r(squareLen)}" height="${r(squareWide)}"
          fill="${P.grassDeep}" opacity="0.35"/>
    <rect x="${r(cx - len / 2)}" y="${r(cy - wide / 2)}"
          width="${r(len)}" height="${r(wide)}" fill="${P.pitch}"/>
    <rect x="${r(cx - len / 2)}" y="${r(cy - wide / 2)}"
          width="${r(len)}" height="${r(wide / 2.4)}" fill="${P.pitchWorn}" opacity="0.55"/>
    ${creases}`;
}

/* Four corner masts. The lamp heads are pale rectangles rather than anything
 * that glows: at this hour the lights are on but the sky has not gone yet, so
 * they read as fittings, not as lens flare. */
function pylons(cx, cy, rx, ry, tiers) {
  const h = 74 + tiers * 16;
  const out = [];
  for (const a of [Math.PI / 4, (3 * Math.PI) / 4, (5 * Math.PI) / 4, (7 * Math.PI) / 4]) {
    const x = cx + Math.cos(a) * rx * 1.06;
    const y = cy + Math.sin(a) * ry * 1.06;
    // Masts behind the far stand are shorter on the page, because they are
    // further away.
    const scale = Math.sin(a) > 0 ? 1 : 0.72;
    const hh = h * scale;
    out.push(`
      <rect x="${r(x - 2.5)}" y="${r(y - hh)}" width="5" height="${r(hh)}" fill="${P.mast}" opacity="0.85"/>
      <rect x="${r(x - 21 * scale)}" y="${r(y - hh - 15 * scale)}"
            width="${r(42 * scale)}" height="${r(17 * scale)}" rx="2"
            fill="${P.lamp}" opacity="0.92"/>
      <rect x="${r(x - 21 * scale)}" y="${r(y - hh - 15 * scale)}"
            width="${r(42 * scale)}" height="${r(17 * scale)}" rx="2"
            fill="none" stroke="${P.mast}" stroke-opacity="0.5" stroke-width="1.5"/>`);
  }
  return out.join('');
}

/* Newer grounds light from a ring on the roof rather than from corner masts,
 * which is the most recognisable single difference between a stadium built in
 * the 1980s and one built since 2010. */
function roofLights(cx, cy, rx, ry) {
  const n = 46;
  const out = [];
  for (let i = 0; i < n; i++) {
    const a = (i / n) * Math.PI * 2;
    const x = cx + Math.cos(a) * rx;
    const y = cy + Math.sin(a) * ry;
    const s = Math.sin(a) > 0 ? 1 : 0.7;
    out.push(`<rect x="${r(x - 7 * s)}" y="${r(y - 2.5 * s)}" width="${r(14 * s)}" height="${r(5 * s)}" ` +
      `rx="1.5" fill="${P.lamp}" opacity="0.9"/>`);
  }
  return out.join('');
}

const r = (n) => Math.round(n * 10) / 10;

function esc(s) {
  return String(s || '').replace(/[&<>"]/g, (c) => (
    { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]
  ));
}
