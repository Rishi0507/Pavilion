/* The ground, drawn.
 *
 * Every venue in the corpus has a straight and a square boundary in metres, and
 * this builds the place from those two numbers rather than dropping the same
 * oval behind every fixture. Chinnaswamy really is rounder and smaller than
 * Chepauk, Ahmedabad really does dwarf both, and the difference is visible
 * before a ball is bowled — which is the point, because the boundary lengths
 * are part of what makes a target hard.
 *
 * Nothing here is photographic and nothing is licensed from anybody. It is a
 * diagram with a camera in it: geometry, four materials, and a crowd made of up
 * to twenty-six thousand coloured specks in a single instanced draw.
 *
 * Cost matters more than fidelity. The whole scene is about a dozen draw calls,
 * and more importantly it stops: the camera makes one establishing move lasting
 * three seconds and then the render loop ends, rather than orbiting for as long
 * as the tab is open. On a page whose actual job is a cricket puzzle, a
 * background that quietly eats a core for twenty minutes would be a bug, and a
 * page that never falls idle is one that never lets the browser rest.
 */

import * as THREE from './vendor/three.module.min.js';

const PITCH_LENGTH = 20.12; // metres, wicket to wicket
const PITCH_WIDTH = 3.05;
const INNER_RING = 27.4; // the thirty-yard circle

/* Palette. The grass is deliberately desaturated: this sits behind type, and a
 * broadcast green would fight every word on top of it. */
const COLOURS = {
  sky: 0x05070b,
  grass: 0x1c3326,
  grassAlt: 0x203a2b,
  pitch: 0xb9a276,
  crease: 0xdfe6dd,
  rope: 0xd8dee6,
  ring: 0x2f4a38,
  concrete: 0x22272f,
  seat: 0x33415a,
  roof: 0x10141a,
  steel: 0x2a3240,
  lamp: 0xfff4d6,
};

/* Crowd colours: a spread of muted clothing tones. Saturated confetti reads as
 * a bug rather than as people. */
const CROWD = [0xb8c0cc, 0x8f9aa8, 0xd9d2c4, 0x6d7b8c, 0xc4a68a, 0x9fb0a4, 0xe0dede, 0x7a6f78];

/* renderGround draws one frame of a venue and hands back an image.
 *
 * The first version of this kept a live canvas on the page and moved a camera
 * around it. That was wrong twice over. It never let the page fall idle, so the
 * browser could not rest between decisions; and on a machine without usable
 * GPU acceleration — where Chrome falls back to a software rasteriser — merely
 * compositing the canvas while scrolling made the whole page unresponsive.
 *
 * A ground does not need to move. What it needs to do is establish where the
 * match is being played, which one good frame does completely. So the scene is
 * built, rendered once, read out as an image, and the WebGL context is thrown
 * away immediately. What remains on the page is an <img>, which costs a browser
 * nothing, cannot leak a context, and scrolls like any other picture. The
 * entrance is then a CSS fade, which is free.
 */
export async function renderGround(ground, width, height) {
  const canvas = document.createElement('canvas');

  let renderer;
  try {
    renderer = new THREE.WebGLRenderer({
      canvas,
      // Antialiasing is deliberately off. Asking for a multisampled buffer and
      // for that buffer to be preserved is a combination some drivers, the
      // software rasteriser included, resolve to a black readback: the frame is
      // drawn correctly and then reads back empty. Rendering oversized and
      // letting the browser downscale the image gives smoother edges anyway.
      antialias: false,
      alpha: false,
      powerPreference: 'low-power',
      // Required so the frame survives long enough to be read back.
      preserveDrawingBuffer: true,
    });
  } catch {
    return null; // No WebGL. The fixture reads fine without a picture.
  }

  try {
    // A still can afford resolution that a moving picture could not, but only
    // up to a point: this is rendered once on the client, and on a software
    // rasteriser every extra pixel is paid for in wall-clock time. Two times is
    // enough to hide the aliasing that comes of not multisampling.
    const scale = 1;
    renderer.setPixelRatio(1);
    renderer.setSize(Math.round(width * scale), Math.round(height * scale), false);
    renderer.outputColorSpace = THREE.SRGBColorSpace;
    renderer.toneMapping = THREE.ACESFilmicToneMapping;
    renderer.toneMappingExposure = 1.15;

    const scene = new THREE.Scene();
    scene.background = new THREE.Color(COLOURS.sky);
    // The camera sits roughly 200 metres out and the far side of the bowl is
    // another 250 beyond that, so fog starting at 150 turned the entire ground
    // into background. It begins past the near stand and only ever touches the
    // far rim.
    scene.fog = new THREE.Fog(COLOURS.sky, 340, 1000);

    // The field: straight boundary along the pitch, square boundary across it.
    const a = ground.straight || 70;
    const b = ground.square || 64;
    const tiers = Math.max(1, Math.min(3, ground.tiers || 2));

    const world = new THREE.Group();
    scene.add(world);

    world.add(outfield(a, b));
    world.add(thirtyYardCircle(a, b));
    world.add(pitch());
    world.add(boundaryRope(a, b));
    world.add(stands(a, b, tiers).group);
    world.add(crowd(a, b, tiers, ground.capacity || 30000));
    if (ground.roof && ground.roof !== 'none') world.add(roof(a, b, tiers, ground.roof));
    world.add(ground.lights === 'roof' ? roofLights(a, b, tiers) : pylons(a, b, tiers));
    lighting(scene, a, b, tiers);

    // High and back, and square of the wicket rather than at the obvious
    // three-quarter angle: the floodlight pylons stand on the diagonals, so 45
    // degrees puts a sixty-metre mast directly between the camera and the pitch.
    const reach = Math.max(a, b);
    const camera = new THREE.PerspectiveCamera(38, width / height, 0.5, 1400);

    const angle = -Math.PI / 2;
    camera.position.set(Math.cos(angle) * reach * 2.55, reach * 1.5, Math.sin(angle) * reach * 2.55);
    camera.lookAt(0, 2, 0);

    /* Time-box the render.
     *
     * Chrome falls back to a software rasteriser when there is no usable GPU,
     * and there this scene can take seconds for a single frame — seconds during
     * which the main thread is blocked and the page does not respond to a
     * click. A picture of the ground is not worth that.
     *
     * So a thumbnail is drawn first and timed. It is the same scene through the
     * same camera, so the cost scales with the pixel count, and the full frame
     * can be predicted from it before being committed to. If the prediction is
     * too slow the render is abandoned and the caller falls back to the plain
     * fixture panel, which is the correct outcome: the puzzle is the product,
     * and the stadium is a nicety.
     */
    const probeW = 96;
    const probeH = Math.max(1, Math.round(96 * height / width));
    renderer.setSize(probeW, probeH, false);
    camera.aspect = probeW / probeH;
    camera.updateProjectionMatrix();

    const gl = renderer.getContext();
    const flush = () => gl.readPixels(0, 0, 1, 1, gl.RGBA, gl.UNSIGNED_BYTE, new Uint8Array(4));

    // The first render of any scene compiles its shaders and uploads its
    // buffers, which on a cold context dwarfs the drawing itself. Timing that
    // and extrapolating from it says the machine is far slower than it is, so
    // the first pass is thrown away and the second is the one measured.
    renderer.render(scene, camera);
    flush();

    const t0 = performance.now();
    renderer.render(scene, camera);
    flush();
    const probeMs = performance.now() - t0;

    const fullW = Math.round(width * scale);
    const fullH = Math.round(height * scale);
    // Fragment cost scales with area; the fixed per-frame work does not, so
    // this is an over-estimate rather than an under-estimate, which is the
    // right way round for a budget.
    const predictedMs = probeMs * (fullW * fullH) / (probeW * probeH);
    if (predictedMs > 1200) return null;

    renderer.setSize(fullW, fullH, false);
    camera.aspect = width / height;
    camera.updateProjectionMatrix();
    renderer.render(scene, camera);

    const url = canvas.toDataURL('image/jpeg', 0.82);

    // A frame that failed to draw reads back as flat black, which compresses to
    // almost nothing. Rather than show an empty rectangle and call it a
    // stadium, that is treated as a failure so the caller falls back to the
    // plain fixture panel.
    if (url.length < 3000) return null;

    scene.traverse((o) => {
      if (o.geometry) o.geometry.dispose();
      if (o.material) {
        (Array.isArray(o.material) ? o.material : [o.material]).forEach((m) => m.dispose());
      }
    });
    return url;
  } catch {
    return null;
  } finally {
    // Contexts are a capped resource in every browser, so this is released
    // whatever happened above.
    renderer.dispose();
    renderer.forceContextLoss?.();
  }
}

/* Field ------------------------------------------------------------------- */

/* The outfield, as an ellipse with mown stripes.
 *
 * The stripes are radial rather than parallel because that is how an oval is
 * actually cut, and because parallel stripes on an ellipse look like a mistake
 * from every angle except directly overhead.
 */
function outfield(a, b) {
  const g = new THREE.Group();

  const base = new THREE.Mesh(
    ellipseGeometry(a + 6, b + 6, 96),
    new THREE.MeshStandardMaterial({ color: COLOURS.grass, roughness: 1, metalness: 0 }),
  );
  base.rotation.x = -Math.PI / 2;
  base.receiveShadow = false;
  g.add(base);

  const wedges = 16;
  const stripeMat = new THREE.MeshStandardMaterial({
    color: COLOURS.grassAlt,
    roughness: 1,
    metalness: 0,
    transparent: true,
    opacity: 0.55,
  });
  for (let i = 0; i < wedges; i += 2) {
    const from = (i / wedges) * Math.PI * 2;
    const to = ((i + 1) / wedges) * Math.PI * 2;
    const m = new THREE.Mesh(ellipseWedge(a + 6, b + 6, from, to), stripeMat);
    m.rotation.x = -Math.PI / 2;
    m.position.y = 0.01;
    g.add(m);
  }
  return g;
}

function thirtyYardCircle(a, b) {
  const shape = new THREE.Shape();
  const scale = Math.min(a, b);
  shape.absellipse(0, 0, INNER_RING * (a / scale) * 0.62, INNER_RING * (b / scale) * 0.62, 0, Math.PI * 2, false, 0);
  const pts = shape.getPoints(96).map((p) => new THREE.Vector3(p.x, 0.03, p.y));
  const line = new THREE.Line(
    new THREE.BufferGeometry().setFromPoints(pts),
    new THREE.LineBasicMaterial({ color: COLOURS.ring, transparent: true, opacity: 0.9 }),
  );
  return line;
}

function pitch() {
  const g = new THREE.Group();

  const square = new THREE.Mesh(
    new THREE.PlaneGeometry(PITCH_LENGTH + 4, PITCH_WIDTH + 7),
    new THREE.MeshStandardMaterial({ color: 0x2a4331, roughness: 1 }),
  );
  square.rotation.x = -Math.PI / 2;
  square.position.y = 0.02;
  g.add(square);

  const strip = new THREE.Mesh(
    new THREE.PlaneGeometry(PITCH_LENGTH, PITCH_WIDTH),
    new THREE.MeshStandardMaterial({ color: COLOURS.pitch, roughness: 0.95 }),
  );
  strip.rotation.x = -Math.PI / 2;
  strip.position.y = 0.04;
  g.add(strip);

  const creaseMat = new THREE.MeshBasicMaterial({ color: COLOURS.crease });
  for (const x of [-PITCH_LENGTH / 2, PITCH_LENGTH / 2]) {
    const crease = new THREE.Mesh(new THREE.PlaneGeometry(0.12, PITCH_WIDTH + 1.2), creaseMat);
    crease.rotation.x = -Math.PI / 2;
    crease.position.set(x + Math.sign(-x) * 1.22, 0.06, 0);
    g.add(crease);
    g.add(stumps(x));
  }
  return g;
}

function stumps(x) {
  const g = new THREE.Group();
  const mat = new THREE.MeshStandardMaterial({ color: 0xe8e4dc, roughness: 0.6 });
  const geo = new THREE.CylinderGeometry(0.045, 0.045, 0.71, 6);
  for (let i = -1; i <= 1; i++) {
    const s = new THREE.Mesh(geo, mat);
    s.position.set(x, 0.355, i * 0.1);
    g.add(s);
  }
  return g;
}

function boundaryRope(a, b) {
  const shape = new THREE.Shape();
  shape.absellipse(0, 0, a, b, 0, Math.PI * 2, false, 0);
  const pts = shape.getPoints(128).map((p) => new THREE.Vector3(p.x, 0.12, p.y));
  const curve = new THREE.CatmullRomCurve3(pts, true);
  return new THREE.Mesh(
    new THREE.TubeGeometry(curve, 160, 0.28, 6, true),
    new THREE.MeshStandardMaterial({ color: COLOURS.rope, roughness: 0.7 }),
  );
}

/* Stands ------------------------------------------------------------------ */

/* The bowl, as stepped concentric rings.
 *
 * Each tier is a ring lifted and pushed outward from the one below, which is
 * what a stand is: a slope broken into decks. Building it from rings rather than
 * from a lathe keeps the ellipse honest, so a ground with a long straight
 * boundary gets a stand that is genuinely further away down the ground.
 */
function stands(a, b, tiers) {
  const group = new THREE.Group();
  const concrete = new THREE.MeshStandardMaterial({ color: COLOURS.concrete, roughness: 0.95 });
  const seats = new THREE.MeshStandardMaterial({ color: COLOURS.seat, roughness: 0.85 });

  let inner = { a: a + 9, b: b + 9 };
  let y = 0;

  for (let t = 0; t < tiers; t++) {
    const depth = 15 + t * 5;
    const rise = 9 + t * 4;
    const outer = { a: inner.a + depth, b: inner.b + depth };

    // The rake: a sloped band from the front of the deck to the back.
    group.add(band(inner, outer, y, y + rise, seats));
    // The wall holding the next deck up.
    if (t < tiers - 1) {
      group.add(band(outer, { a: outer.a + 2.5, b: outer.b + 2.5 }, y + rise, y + rise + 3.5, concrete));
      inner = { a: outer.a + 2.5, b: outer.b + 2.5 };
      y += rise + 3.5;
    } else {
      // The outer skin of the ground.
      group.add(band(outer, { a: outer.a + 3, b: outer.b + 3 }, y + rise, 0, concrete));
      inner = outer;
      y += rise;
    }
  }

  return { group, top: y, outer: inner };
}

/* band builds a sloping ring between two ellipses and two heights. */
function band(from, to, y0, y1, material) {
  const segments = 96;
  const positions = [];
  const normals = [];
  const indices = [];

  for (let i = 0; i <= segments; i++) {
    const th = (i / segments) * Math.PI * 2;
    const c = Math.cos(th);
    const s = Math.sin(th);
    positions.push(from.a * c, y0, from.b * s);
    positions.push(to.a * c, y1, to.b * s);
    // A cheap outward-and-up normal; the surface is matte, so this is enough.
    const n = new THREE.Vector3(c, 0.6, s).normalize();
    normals.push(n.x, n.y, n.z, n.x, n.y, n.z);
  }
  for (let i = 0; i < segments; i++) {
    const k = i * 2;
    indices.push(k, k + 1, k + 2, k + 1, k + 3, k + 2);
  }

  const geo = new THREE.BufferGeometry();
  geo.setAttribute('position', new THREE.Float32BufferAttribute(positions, 3));
  geo.setAttribute('normal', new THREE.Float32BufferAttribute(normals, 3));
  geo.setIndex(indices);
  const mesh = new THREE.Mesh(geo, material);
  mesh.material.side = THREE.DoubleSide;
  return mesh;
}

/* The crowd: one instanced mesh of very small boxes scattered over the rake.
 *
 * A stadium without people in it reads as a model of a stadium. The count comes
 * from the ground's own capacity, so Ahmedabad is visibly fuller than Sharjah,
 * capped where more specks stop being distinguishable and start being cost.
 */
function crowd(a, b, tiers, capacity) {
  const count = Math.min(14000, Math.max(2200, Math.round(capacity / 6)));
  const geo = new THREE.BoxGeometry(0.75, 0.9, 0.75);
  const mat = new THREE.MeshStandardMaterial({ roughness: 1, metalness: 0 });
  const mesh = new THREE.InstancedMesh(geo, mat, count);
  mesh.instanceMatrix.setUsage(THREE.StaticDrawUsage);

  const m = new THREE.Matrix4();
  const colour = new THREE.Color();

  for (let i = 0; i < count; i++) {
    const t = Math.floor(Math.random() * tiers);
    const depth = 15 + t * 5;
    const rise = 9 + t * 4;

    // Where the deck starts, matching the bands above.
    let innerA = a + 9;
    let innerB = b + 9;
    let base = 0;
    for (let k = 0; k < t; k++) {
      const d = 15 + k * 5;
      const r = 9 + k * 4;
      innerA += d + 2.5;
      innerB += d + 2.5;
      base += r + 3.5;
    }

    const u = Math.random(); // 0 at the front of the deck, 1 at the back
    const th = Math.random() * Math.PI * 2;
    const c = Math.cos(th);
    const s = Math.sin(th);

    const ra = innerA + u * depth;
    const rb = innerB + u * depth;
    const jitter = 0.8;

    m.makeTranslation(
      ra * c + (Math.random() - 0.5) * jitter,
      base + u * rise + 0.9,
      rb * s + (Math.random() - 0.5) * jitter,
    );
    mesh.setMatrixAt(i, m);

    colour.setHex(CROWD[(Math.random() * CROWD.length) | 0]);
    // Vary the brightness so the mass has texture rather than reading flat.
    // Kept well under full brightness: a stand rendered at the colours people
    // actually wear comes out close to white, which then outshines the field
    // the picture is supposed to be of.
    const shade = 0.3 + Math.random() * 0.4;
    colour.multiplyScalar(shade);
    mesh.setColorAt(i, colour);
  }
  mesh.instanceMatrix.needsUpdate = true;
  if (mesh.instanceColor) mesh.instanceColor.needsUpdate = true;
  return mesh;
}

function roof(a, b, tiers, kind) {
  let outerA = a + 9;
  let outerB = b + 9;
  let y = 0;
  for (let t = 0; t < tiers; t++) {
    const depth = 15 + t * 5;
    const rise = 9 + t * 4;
    outerA += depth;
    outerB += depth;
    y += rise;
    if (t < tiers - 1) {
      outerA += 2.5;
      outerB += 2.5;
      y += 3.5;
    }
  }

  const g = new THREE.Group();
  const mat = new THREE.MeshStandardMaterial({
    color: COLOURS.roof,
    roughness: 0.6,
    metalness: 0.3,
    side: THREE.DoubleSide,
  });

  // A partial roof covers the back of the stand; a full one reaches further in.
  const reach = kind === 'full' ? 26 : 15;
  const inner = { a: outerA - reach, b: outerB - reach };
  const outer = { a: outerA + 4, b: outerB + 4 };
  g.add(band(inner, outer, y + 12, y + 15, mat));

  return g;
}

function pylons(a, b, tiers) {
  const g = new THREE.Group();
  const height = 42 + tiers * 8;
  const mast = new THREE.MeshStandardMaterial({ color: COLOURS.steel, roughness: 0.7, metalness: 0.4 });
  const lamp = new THREE.MeshBasicMaterial({ color: COLOURS.lamp });

  // Four corner pylons, set back behind the stands and square of the wicket,
  // which is where they stand at every ground that still has them.
  for (const th of [Math.PI / 4, (3 * Math.PI) / 4, (5 * Math.PI) / 4, (7 * Math.PI) / 4]) {
    const x = Math.cos(th) * (a + 62);
    const z = Math.sin(th) * (b + 62);

    const column = new THREE.Mesh(new THREE.CylinderGeometry(1.1, 1.9, height, 8), mast);
    column.position.set(x, height / 2, z);
    g.add(column);

    const head = new THREE.Mesh(new THREE.BoxGeometry(16, 9, 1.6), lamp);
    head.position.set(x, height + 4, z);
    head.lookAt(0, 12, 0);
    g.add(head);

    const frame = new THREE.Mesh(new THREE.BoxGeometry(17.5, 10.5, 1), mast);
    frame.position.set(x, height + 4, z);
    frame.lookAt(0, 12, 0);
    frame.translateZ(-0.9);
    g.add(frame);
  }
  return g;
}

/* Newer grounds light from a ring on the roof rather than from corner pylons,
 * which is the single most recognisable difference between a stadium built in
 * the 1980s and one built since 2010. */
function roofLights(a, b, tiers) {
  const g = new THREE.Group();
  const lamp = new THREE.MeshBasicMaterial({ color: COLOURS.lamp });

  let outerA = a + 9;
  let outerB = b + 9;
  let y = 0;
  for (let t = 0; t < tiers; t++) {
    const depth = 15 + t * 5;
    const rise = 9 + t * 4;
    outerA += depth;
    outerB += depth;
    y += rise;
    if (t < tiers - 1) {
      outerA += 2.5;
      outerB += 2.5;
      y += 3.5;
    }
  }

  const n = 56;
  const geo = new THREE.BoxGeometry(3.2, 0.7, 0.7);
  const ring = new THREE.InstancedMesh(geo, lamp, n);
  const m = new THREE.Matrix4();
  const q = new THREE.Quaternion();
  const up = new THREE.Vector3(0, 1, 0);
  const scale = new THREE.Vector3(1, 1, 1);
  const pos = new THREE.Vector3();

  for (let i = 0; i < n; i++) {
    const th = (i / n) * Math.PI * 2;
    pos.set(Math.cos(th) * (outerA - 8), y + 13, Math.sin(th) * (outerB - 8));
    q.setFromAxisAngle(up, -th);
    m.compose(pos, q, scale);
    ring.setMatrixAt(i, m);
  }
  ring.instanceMatrix.needsUpdate = true;
  g.add(ring);
  return g;
}

function lighting(scene, a, b, tiers) {
  // A night match is lit from four high corners onto a green floor, so the
  // ambient term is a cool sky over a green bounce.
  scene.add(new THREE.HemisphereLight(0x9db4d2, 0x1b3a26, 0.85));

  // Two keys rather than four. Every light multiplies the work the fragment
  // shader does for every pixel of the bowl, and the difference between two and
  // four on a matte scene is not visible.
  const height = 46 + tiers * 8;
  for (const th of [Math.PI / 4, (5 * Math.PI) / 4]) {
    const l = new THREE.DirectionalLight(0xfff3dd, 0.85);
    l.position.set(Math.cos(th) * (a + 60), height, Math.sin(th) * (b + 60));
    l.target.position.set(0, 0, 0);
    scene.add(l);
    scene.add(l.target);
  }

  // A little fill straight down keeps the pitch from going muddy.
  const down = new THREE.DirectionalLight(0xffffff, 0.3);
  down.position.set(0, 120, 0);
  scene.add(down);
}

/* Geometry helpers -------------------------------------------------------- */

function ellipseGeometry(a, b, segments) {
  const shape = new THREE.Shape();
  shape.absellipse(0, 0, a, b, 0, Math.PI * 2, false, 0);
  return new THREE.ShapeGeometry(shape, segments);
}

function ellipseWedge(a, b, from, to) {
  const shape = new THREE.Shape();
  shape.moveTo(0, 0);
  shape.absellipse(0, 0, a, b, from, to, false, 0);
  shape.lineTo(0, 0);
  return new THREE.ShapeGeometry(shape, 24);
}
