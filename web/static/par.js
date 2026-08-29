/* Par.
 *
 * The client draws the game and nothing else. It sends a decision, receives the
 * resolved over, and animates it. It holds no state the server does not, and it
 * could not compute an outcome even if it tried: the day's key never leaves the
 * server, so the deliveries genuinely are not knowable before the choice.
 *
 * The only interesting thing here is the over reveal. Cricket has a rhythm of
 * six balls and then a break, so the balls arrive one at a time with a beat at
 * the end, and that is the sole animated moment in the interface.
 */

const REDUCED = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
const BALL_MS = REDUCED ? 0 : 130;
const BEAT_MS = REDUCED ? 0 : 380;

const el = (id) => document.getElementById(id);

// Player marks are monograms. Match photography belongs to picture agencies and
// there is no licensed source for it, so initials set in the board's own face
// are both honest and a better fit than a borrowed headshot.
function monogram(name) {
  if (!name) return '—';
  const parts = name.trim().split(/\s+/);
  const last = parts[parts.length - 1];
  const first = parts.length > 1 ? parts[0] : '';
  const a = (first.match(/[A-Za-z]/) || [''])[0];
  const b = (last.match(/[A-Za-z]/) || [''])[0];
  return (a + b).toUpperCase() || last.slice(0, 2).toUpperCase();
}
/* The ground ---------------------------------------------------------------
 *
 * A drawing of the venue, built from its own boundary lengths. It is vector and
 * synchronous: no canvas, no WebGL, no dependency, nothing to wait for and
 * nothing to fall back to. The module is still imported on demand, because the
 * menu does not need it.
 */
let groundModule;

// ?ground=off hides the drawing. Keeping the switch in the product rather than
// in a scratch build means the page can always be reduced to the part that
// matters, which is the puzzle.
const GROUND_OFF = new URLSearchParams(location.search).get('ground') === 'off';

function loadGround() {
  if (GROUND_OFF) return Promise.resolve(null);
  if (!groundModule) groundModule = import('/static/ground.js').catch(() => null);
  return groundModule;
}

async function showGround(hostSelector, ground) {
  const panel = document.querySelector(hostSelector + ' .ground');
  const host = document.querySelector(hostSelector + ' [data-ground-host]');
  if (!host || !ground || !ground.name) return;

  const mod = await loadGround();
  if (!mod) {
    panel?.classList.add('no-stage');
    return;
  }
  panel?.classList.remove('no-stage');
  panel?.style.setProperty('--tint', ground.colour || '');
  host.innerHTML = mod.groundSVG(ground);
}

// fillGroundPlate writes the ground's name and measurements alongside it. The
// boundary lengths are there because they are part of the puzzle: the same
// target is a different problem at Chinnaswamy and at Chepauk.
function fillGroundPlate(prefix, ground) {
  if (!ground) return;
  el(prefix + '-venue').textContent = ground.name || '';
  const city = el(prefix + '-city');
  if (city) city.textContent = ground.city || '';
  const dims = el(prefix + '-dims');
  if (dims) {
    dims.textContent = ground.straight
      ? `${ground.straight} m straight · ${ground.square} m square`
      : '';
  }
}

const sleep = (ms) => (ms > 0 ? new Promise((r) => setTimeout(r, ms)) : Promise.resolve());

const state = {
  runID: null,
  token: null,
  decisions: 0,
  half: 'defend',
  puzzle: null,
  mode: 'daily',
  lastSituation: '',
  draft: null,
  draftSituation: '',
  pickedBowlers: [],
  pickedBatters: [],
  busy: false,
};

const overRuns = { defend: [], chase: [] };

/* Transport --------------------------------------------------------------- */

async function api(method, path, body) {
  const headers = { 'Content-Type': 'application/json' };
  if (state.token) headers['X-Par-Token'] = state.token;

  const res = await fetch(path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await res.text();
  let data = {};
  try { data = text ? JSON.parse(text) : {}; } catch { /* fall through */ }

  if (!res.ok) throw new Error(data.error || `request failed (${res.status})`);
  return data;
}

function showError(message) {
  const box = el('error');
  box.textContent = message;
  box.hidden = false;
}

function clearError() { el('error').hidden = true; }

// show swaps the visible screen, and lets the incoming one arrive.
//
// The entrance is deliberately slight: a short rise and a fade, on the same
// curve everywhere. A page that slides its whole contents around on every tap
// is tiring by the third puzzle, whereas no transition at all makes a single
// long page feel like five unrelated ones.
/* Routing --------------------------------------------------------------------
 *
 * The five screens are pushed onto the browser's history, so back and forward
 * do what they look like they should. Without it, back from the middle of a
 * game left the site entirely, which on a page that never navigates is a
 * surprising way to lose a run.
 *
 * A run in progress is the one case that cannot simply be re-shown: its state
 * lives on the server behind a token and a move counter, and re-entering it
 * from a history entry would need the run replayed. So stepping back out of a
 * game abandons it and returns to the menu, and stepping forward again offers
 * the menu rather than pretending. That is honest, and the daily puzzle is
 * still there to be started again.
 */
const RESUMABLE = { menu: true, draft: true, result: true };

function route(name) {
  switch (name) {
    case 'draft': return loadDraft();
    case 'result': return show('result');
    default: return loadMenu();
  }
}

window.addEventListener('popstate', (e) => {
  // An entry this page pushed carries its screen. One it did not — a typed
  // fragment, or a link into the middle of the site — carries nothing, so the
  // fragment itself is read rather than defaulting to the menu and quietly
  // ignoring where the reader asked to go.
  const name = (e.state && e.state.screen) || (location.hash || '#menu').slice(1);
  // A history entry for a screen that cannot be re-entered lands on the menu,
  // and the entry is rewritten so a second back press does not retry it.
  if (!RESUMABLE[name]) {
    history.replaceState({ screen: 'menu' }, '', '#menu');
    loadMenu();
    return;
  }
  route(name);
});

function show(name, push = true) {
  if (push) {
    const entry = { screen: name };
    // Replacing rather than pushing when the screen has not changed keeps the
    // history from filling with duplicates of the play screen, which would
    // otherwise gain an entry on every over.
    if (history.state && history.state.screen === name) {
      history.replaceState(entry, '', `#${name}`);
    } else {
      history.pushState(entry, '', `#${name}`);
    }
  }
  const target = `screen-${name}`;
  for (const id of ['screen-menu', 'screen-draft', 'screen-start', 'screen-play', 'screen-result']) {
    const node = el(id);
    const wanted = id === target;
    if (wanted && node.hidden) {
      node.hidden = false;
      node.classList.remove('entering');
      void node.offsetWidth;
      node.classList.add('entering');
    } else if (!wanted) {
      node.hidden = true;
      node.classList.remove('entering');
    }
  }
  window.scrollTo({ top: 0, behavior: REDUCED ? 'auto' : 'smooth' });
  clearError();
}

/* Menu -------------------------------------------------------------------- */

async function loadMenu() {
  try {
    const p = await api('GET', '/api/v1/puzzle/today');
    el('dateline').textContent = p.date;
    el('menu-target').textContent = p.target;
    show('menu');
  } catch (err) {
    showError(err.message);
  }
}

/* Starting a run ---------------------------------------------------------- */

const MODE_LABEL = {
  daily: "Today's puzzle · counts towards the day's scores",
  practice: 'Practice · this one is just for you',
  draft: 'Your XI · this one is just for you',
};

// showPuzzle presents a situation before it is played, so the attack can be
// read before the first decision rather than during it.
function showPuzzle(p) {
  state.puzzle = p;
  state.mode = p.mode || 'daily';
  if (p.situation_id) state.lastSituation = p.situation_id;

  el('start-mode').textContent = MODE_LABEL[state.mode] || '';
  el('start-target').textContent = p.target;
  el('start-need').textContent = `${p.target} runs`;
  el('start-unvalidated').hidden = p.validated;

  const ground = p.ground || { name: p.venue };
  fillGroundPlate('start', ground);
  el('start-ground-note').textContent = ground.note || '';

  const list = el('start-attack');
  list.replaceChildren();
  p.attack.forEach((b, i) => {
    const li = document.createElement('li');
    li.style.setProperty('--i', String(i));
    li.append(markTile(b), whoBlock(b.name, b.team, b.style, b.colour));
    list.append(li);
  });
  show('start');
  showGround('#screen-start', ground);
}

// markTile is the monogram square that stands in for a photograph. Match
// photography belongs to picture agencies and there is no licensed source for
// it, so initials set in the board's own face are both honest and a better fit.
function markTile(p) {
  const tile = document.createElement('span');
  tile.className = 'mark';
  tile.textContent = p.mark || monogram(p.name);
  // The club's colour, as a tint on the tile and nothing more. It places a
  // player at a glance, which is the whole job; a crest would be somebody
  // else's property and a full-colour card would read as an official one.
  if (p.colour) tile.style.setProperty('--tint', p.colour);
  return tile;
}

// whoBlock is a name with the side they are known for underneath it. The team
// is what places an unfamiliar name, especially now that the draft reaches back
// to players who retired a decade ago.
function whoBlock(name, team, sub, colour) {
  const wrap = document.createElement('span');
  wrap.className = 'who';

  const line = document.createElement('span');
  line.className = 'who-name';
  line.textContent = name;
  wrap.append(line);

  const under = document.createElement('span');
  under.className = 'who-sub';
  if (team) {
    const badge = document.createElement('span');
    badge.className = 'team';
    badge.textContent = team;
    if (colour) badge.style.setProperty('--tint', colour);
    under.append(badge);
  }
  if (sub) {
    const style = document.createElement('span');
    style.textContent = sub;
    under.append(style);
  }
  wrap.append(under);
  return wrap;
}

// pending holds the run opened by the server but not yet begun by the player,
// so the attack can be studied first without the clock or the state moving.
let pending = null;

async function openRun(body) {
  clearError();
  try {
    const r = await api('POST', '/api/v1/run', body);
    pending = r;
    showPuzzle(r.puzzle);
  } catch (err) {
    showError(err.message);
  }
}

function beginRun() {
  if (!pending) return;
  state.runID = pending.run_id;
  state.token = pending.token;
  state.decisions = pending.decisions;
  state.half = pending.state.half;
  overRuns.defend = [];
  overRuns.chase = [];

  buildStrips();
  render(pending.state);
  show('play');
  pending = null;
}

/* Draft ------------------------------------------------------------------- */

async function loadDraft() {
  clearError();
  try {
    const d = await api('GET', '/api/v1/draft');
    state.draft = d;
    state.pickedBowlers = [];
    state.pickedBatters = [];

    renderPicks('draft-bowlers', d.bowlers, state.pickedBowlers, d.pick_bowlers, d.bowler_budget);
    renderPicks('draft-batters', d.batters, state.pickedBatters, d.pick_batters, d.batter_budget);
    el('hint-bowlers').textContent = `pick ${d.pick_bowlers} of ${d.bowlers.length}`;
    el('hint-batters').textContent = `pick ${d.pick_batters} of ${d.batters.length}`;
    wireSearch('search-bowlers', 'draft-bowlers');
    wireSearch('search-batters', 'draft-batters');
    updateBudget();

    const ground = d.ground || { name: d.venue };
    // The situation is remembered so the side is played against the ground and
    // the score it was picked against, rather than against a fresh draw.
    state.draftSituation = d.situation_id || '';
    el('draft-target').textContent = d.target;
    fillGroundPlate('draft', ground);

    show('draft');
    showGround('#screen-draft', ground);
  } catch (err) {
    showError(err.message);
  }
}

// wireSearch filters a pool in place. With a hundred bowlers on offer, a list
// without a search box is a list nobody reads to the end of.
function wireSearch(inputID, containerID) {
  const input = el(inputID);
  input.value = '';
  input.oninput = () => {
    const q = input.value.trim().toLowerCase();
    for (const btn of el(containerID).children) {
      const name = btn.querySelector('.pick-name').textContent.toLowerCase();
      // The subtitle carries the team, the style and the years, so searching
      // "csk" or "2011" finds a side as readily as searching a surname does.
      const style = btn.querySelector('.pick-style').textContent.toLowerCase();
      // A selected player always stays visible, so a filter cannot hide part of
      // the side being assembled.
      btn.hidden = q !== '' && !btn.classList.contains('on') &&
        !name.includes(q) && !style.includes(q);
    }
  };
}

function costOfPicked(pool, picked) {
  return picked.reduce((sum, id) => {
    const p = pool.find((x) => x.id === id);
    return sum + (p ? p.cost : 0);
  }, 0);
}

function renderPicks(containerID, pool, picked, limit, budget) {
  const wrap = el(containerID);
  wrap.replaceChildren();

  for (const p of pool) {
    const btn = document.createElement('button');
    btn.className = 'pick';
    btn.dataset.id = String(p.id);

    const name = document.createElement('span');
    name.className = 'pick-name';
    name.textContent = p.name;

    const style = document.createElement('span');
    style.className = 'pick-style';
    // The years place a name the reader may not know. Somebody who last played
    // in 2013 is a different proposition from somebody in the side now, and the
    // price alone does not say which is which.
    style.textContent = [p.team, p.style || p.hand, p.years]
      .filter(Boolean).join(' · ');

    const cost = document.createElement('span');
    cost.className = 'pick-cost';
    cost.textContent = `${p.cost} cr`;

    if (p.colour) btn.style.setProperty('--tint', p.colour);
    btn.append(markTile(p), name, style, cost);
    // The card has room for an abbreviation only, so the hover carries the
    // reason for the price, which is the thing a picker actually wants.
    btn.title = [p.name, p.years, p.note].filter(Boolean).join(' · ');
    btn.onclick = () => togglePick(p, picked, pool, limit, budget, containerID);
    wrap.append(btn);
  }
  paintPicks(containerID, pool, picked, limit, budget);
}

function togglePick(p, picked, pool, limit, budget, containerID) {
  const i = picked.indexOf(p.id);
  if (i >= 0) {
    picked.splice(i, 1);
  } else {
    if (picked.length >= limit) return;
    if (costOfPicked(pool, picked) + p.cost > budget) return;
    picked.push(p.id);
  }
  paintPicks(containerID, pool, picked, limit, budget);
  updateBudget();
}

function paintPicks(containerID, pool, picked, limit, budget) {
  const spent = costOfPicked(pool, picked);
  for (const btn of el(containerID).children) {
    const id = Number(btn.dataset.id);
    const p = pool.find((x) => x.id === id);
    const on = picked.includes(id);
    btn.classList.toggle('on', on);
    // A player is greyed out when picking them is impossible, either because
    // the side is full or because they cost more than is left.
    btn.disabled = !on && (picked.length >= limit || spent + p.cost > budget);
  }
}

function updateBudget() {
  const d = state.draft;
  const bowlCost = costOfPicked(d.bowlers, state.pickedBowlers);
  const batCost = costOfPicked(d.batters, state.pickedBatters);

  el('draft-bowl-count').textContent = `${state.pickedBowlers.length}/${d.pick_bowlers}`;
  el('draft-bat-count').textContent = `${state.pickedBatters.length}/${d.pick_batters}`;
  el('draft-bowl-cost').textContent = `${bowlCost} / ${d.bowler_budget}`;
  el('draft-bat-cost').textContent = `${batCost} / ${d.batter_budget}`;
  el('draft-bowl-cost').classList.toggle('over', bowlCost > d.bowler_budget);
  el('draft-bat-cost').classList.toggle('over', batCost > d.batter_budget);

  const ready = state.pickedBowlers.length === d.pick_bowlers &&
    state.pickedBatters.length === d.pick_batters;
  el('btn-draft-start').disabled = !ready;
  el('draft-note').textContent = ready
    ? `Chasing ${d.target} at ${d.venue}.`
    : `Pick ${d.pick_bowlers - state.pickedBowlers.length} more bowler(s) and ` +
      `${d.pick_batters - state.pickedBatters.length} more batter(s).`;
}

/* The over strip ---------------------------------------------------------- */

function buildStrips() {
  for (const id of ['strip-defend', 'strip-chase']) {
    const strip = el(id);
    strip.replaceChildren();
    for (let i = 0; i < 20; i++) {
      const box = document.createElement('div');
      box.className = 'box';
      strip.append(box);
    }
  }
  el('strip-chase-row').hidden = true;
}

function paintStrip(id, grades, runs, currentOver) {
  const boxes = el(id).children;
  for (let i = 0; i < boxes.length; i++) {
    const box = boxes[i];
    box.className = 'box';
    if (i < grades.length) {
      box.classList.add(grades[i]);
      box.textContent = runs[i] === undefined ? '' : runs[i];
    } else if (i === currentOver) {
      box.classList.add('now');
      box.textContent = '';
    } else {
      box.textContent = '';
    }
  }
}

/* Rendering --------------------------------------------------------------- */

/* Motion ------------------------------------------------------------------
 *
 * The rule throughout: a number that changes should be seen to change. A score
 * that jumps from 41 to 58 between frames reads as a redraw; the same score
 * counted up over a third of a second reads as runs being scored, which is what
 * it is. Everything here is short, none of it blocks a decision, and all of it
 * is skipped under prefers-reduced-motion.
 */

// roll counts an element from its current value to a new one.
function roll(node, to, suffix = '', ms = 340) {
  const raw = node.textContent.replace(/[^0-9-]/g, '');
  const from = raw === '' ? NaN : Number(raw);
  const write = (v) => { node.textContent = v + suffix; };

  if (REDUCED || !Number.isFinite(from) || from === to || Math.abs(to - from) > 200) {
    write(to);
    return;
  }

  // A token per animation, so a roll started by a later over cancels this one
  // rather than the two fighting over the same element.
  const token = (node.dataset.roll = String(Number(node.dataset.roll || 0) + 1));
  const started = performance.now();
  const step = (now) => {
    if (node.dataset.roll !== token) return;
    const t = Math.min((now - started) / ms, 1);
    // Ease out: fast first, settling at the end, which is how a scoreboard
    // ticker behaves.
    const eased = 1 - Math.pow(1 - t, 3);
    write(Math.round(from + (to - from) * eased));
    if (t < 1) requestAnimationFrame(step);
    else write(to);
  };
  requestAnimationFrame(step);
}

// bump flashes an element to acknowledge that it changed.
function bump(node, cls = 'bumped') {
  if (REDUCED || !node) return;
  node.classList.remove(cls);
  // Reading the layout forces the class removal to take effect, so the
  // animation restarts rather than being ignored as a no-op.
  void node.offsetWidth;
  node.classList.add(cls);
}

function render(s) {
  state.decisions = s.decisions;
  state.half = s.half;

  // A finished run has no innings in progress and no decision to offer, so the
  // play screen has nothing to draw.
  if (s.half === 'finished') return;

  const chasing = s.half === 'chase';

  el('phase-banner').textContent = chasing
    ? 'YOU ARE BATTING · get there yourself'
    : 'YOU ARE BOWLING · stop them reaching the target';
  el('phase-banner').classList.toggle('batting', chasing);

  roll(el('sb-score'), s.score);
  if (el('sb-wkts').textContent !== String(s.wickets)) bump(el('scoreboard-wrap'), 'lost');
  el('sb-wkts').textContent = s.wickets;
  el('over-no').innerHTML =
    `OVER ${Math.min(s.over + 1, 20)} <span class="of-twenty">of 20</span>`;

  setBatter('striker', s.striker, s.striker_team, s.striker_mark,
    s.striker_runs, s.striker_balls, s.striker_colour);
  setBatter('nonstriker', s.non_striker, s.non_striker_team, s.non_striker_mark,
    s.non_striker_runs, s.non_striker_balls, s.non_striker_colour);

  el('sb-need-label').textContent = chasing ? 'YOU NEED' : 'THEY NEED';
  el('sb-need').textContent = `${s.runs_needed} off ${s.balls_left}`;
  el('sb-wp-label').textContent = chasing ? 'YOU WIN' : 'YOU HOLD';
  const wp = Math.round(100 * s.win_probability);
  const wpNode = el('sb-wp');
  // An em dash strips to an empty string, and Number('') is 0, which would
  // paint the very first reading green as though it had risen from nothing.
  const previous = wpNode.textContent.replace(/[^0-9-]/g, '');
  const was = previous === '' ? NaN : Number(previous);
  roll(wpNode, wp, '%');
  if (Number.isFinite(was)) {
    wpNode.classList.toggle('rising', wp > was);
    wpNode.classList.toggle('falling', wp < was);
  }
  el('sb-meter-fill').style.width = `${wp}%`;

  el('strip-chase-row').hidden = !chasing;
  paintStrip('strip-defend', s.defend_grid || [], overRuns.defend, chasing ? -1 : s.over);
  paintStrip('strip-chase', s.chase_grid || [], overRuns.chase, chasing ? s.over : -1);

  el('controls-defend').hidden = chasing;
  el('controls-chase').hidden = !chasing;

  if (chasing) renderIntents(s);
  else renderBowlers(s);
}

function setBatter(which, name, team, mark, runs, balls, colour) {
  const tile = el(`mark-${which}`);
  tile.textContent = mark || monogram(name);
  tile.style.setProperty('--tint', colour || '');
  el(`name-${which}`).textContent = name || '—';
  el(`team-${which}`).textContent = team || '';
  el(`figs-${which}`).textContent = name ? `${runs} (${balls})` : '';
}

function renderBowlers(s) {
  const wrap = el('bowlers');
  wrap.replaceChildren();

  const legalIDs = s.legal_bowlers || [];
  (s.overs_bowled || []).forEach((used, i) => {
    const b = state.puzzle.attack[i];
    if (!b) return;
    const legal = legalIDs.includes(i);

    const card = document.createElement('button');
    card.className = 'bowler';
    card.disabled = !legal || state.busy;
    card.onclick = () => playOver('defend', { bowler_id: i, decisions: state.decisions });

    card.style.setProperty('--i', String(i));

    const pips = document.createElement('span');
    pips.className = 'pips';
    for (let k = 0; k < 4; k++) {
      const pip = document.createElement('span');
      pip.className = k < used ? 'pip used' : 'pip';
      pips.append(pip);
    }
    card.setAttribute('aria-label',
      `${b.name}, ${b.style}, ${used} of 4 overs bowled`);
    card.append(markTile(b), whoBlock(b.name, b.team, b.style, b.colour), pips);
    wrap.append(card);
  });
}

function renderIntents(s) {
  const tokens = el('tokens');
  tokens.replaceChildren();
  for (let i = 0; i < 6; i++) {
    const t = document.createElement('span');
    t.className = i < s.attacks_left ? 'token' : 'token spent';
    tokens.append(t);
  }
  el('intent-attack').disabled = s.attacks_left === 0 || state.busy;
  for (const btn of document.querySelectorAll('.intent')) {
    if (btn.id !== 'intent-attack') btn.disabled = state.busy;
    btn.onclick = () => playOver('chase', { intent: btn.dataset.intent, decisions: state.decisions });
  }
}

/* Playing an over --------------------------------------------------------- */

function setBusy(on) {
  state.busy = on;
  for (const b of document.querySelectorAll('.bowler, .intent')) b.disabled = on;
}

async function playOver(half, body) {
  if (state.busy) return;
  clearError();
  setBusy(true);
  try {
    const r = await api('POST', `/api/v1/run/${state.runID}/${half}/over`, body);
    await revealOver(r);

    (half === 'defend' ? overRuns.defend : overRuns.chase).push(r.runs);
    const wasHalf = state.half;
    render(r.state);

    if (r.runs >= 15 && !REDUCED) {
      const strip = wasHalf === 'defend' ? 'strip-defend' : 'strip-chase';
      const box = el(strip).children[r.over - 1];
      if (box) {
        box.classList.add('big');
        setTimeout(() => box.classList.remove('big'), 420);
      }
    }

    if (wasHalf === 'defend' && r.state.half === 'chase') await handover();
    if (r.state.half === 'finished') {
      state.half = 'finished';
      await finish();
    }
  } catch (err) {
    showError(err.message);
  } finally {
    setBusy(false);
  }
}

/* The reveal: six balls, then a beat. The decision has already been made and
 * cannot be taken back, so the pacing is pure consequence. */

async function revealOver(r) {
  const wrap = el('balls');
  wrap.replaceChildren();

  for (const d of r.deliveries) {
    const b = document.createElement('span');
    b.className = 'ball';
    b.textContent = d.wicket ? 'W' : d.outcome;

    if (d.wicket) {
      b.classList.add('wicket');
      if (!REDUCED) {
        // Three lines that scatter: the stumps, drawn rather than fetched.
        const stumps = document.createElement('span');
        stumps.className = 'stumps';
        for (let i = 0; i < 3; i++) {
          const st = document.createElement('span');
          st.className = 'stump';
          stumps.append(st);
        }
        b.append(stumps);
        el('scoreboard-wrap').classList.remove('wicket');
        void el('scoreboard-wrap').offsetWidth;
        el('scoreboard-wrap').classList.add('wicket');
      }
    } else if (d.outcome === '4' || d.outcome === '6') {
      b.classList.add('boundary', d.outcome === '6' ? 'six' : 'four');
      if (!REDUCED) {
        const cherry = document.createElement('span');
        cherry.className = 'cherry';
        b.append(cherry);
      }
    } else if (!d.legal) {
      b.classList.add('extra');
    }

    wrap.append(b);
    await sleep(BALL_MS);
  }

  const summary = document.createElement('span');
  summary.className = 'over-summary';
  summary.textContent = `${r.bowler} · ${r.runs} run${r.runs === 1 ? '' : 's'}` +
    (r.wickets ? `, ${r.wickets} wicket${r.wickets === 1 ? '' : 's'}` : '');
  wrap.append(summary);

  await sleep(BEAT_MS);
}

async function handover() {
  const box = el('handover');
  box.textContent = 'Innings over. Now you bat, chasing the same target.';
  box.hidden = false;
  el('balls').replaceChildren();
  await sleep(REDUCED ? 0 : 1400);
  box.hidden = true;
}

/* Result ------------------------------------------------------------------ */

async function finish() {
  try {
    const player = localStorage.getItem('par.player') || '';
    const r = await api('POST', `/api/v1/run/${state.runID}/finish`, {
      decisions: state.decisions,
      player,
    });

    el('result-verdict').textContent = verdictOf(r);
    el('share').textContent = r.share;
    el('result-score').textContent = (r.total_score >= 0 ? '+' : '') + r.total_score.toFixed(0);
    el('result-pct').textContent = r.counts && r.day.runs > 1 ? Math.round(r.percentile) : '—';
    el('result-streak').textContent = r.counts && r.streak ? r.streak : '—';

    el('result-day').textContent = !r.counts
      ? 'Practice runs are not added to the day’s figures, so today’s puzzle is still there to play.'
      : r.day.runs > 1
        ? `${r.day.runs} people have played today. ${Math.round(100 * r.day.defend_rate)}% held the target, ${Math.round(100 * r.day.chase_rate)}% chased it down.`
        : 'You are the first to play today.';

    el('btn-copy').onclick = () => copyShare(r.share);
    show('result');
  } catch (err) {
    showError(err.message);
  }
}

function verdictOf(r) {
  if (r.defended && r.chased) return 'Both halves. That is a good day.';
  if (r.defended) return 'You held the target, but could not chase it.';
  if (r.chased) return 'You chased it down, but could not hold it.';
  return 'Neither half this time.';
}

async function copyShare(text) {
  const btn = el('btn-copy');
  try {
    await navigator.clipboard.writeText(text);
    btn.textContent = 'Copied';
  } catch {
    // Clipboard access is refused in plenty of ordinary situations, so select
    // the text rather than reporting a failure the player cannot act on.
    const range = document.createRange();
    range.selectNodeContents(el('share'));
    const sel = window.getSelection();
    sel.removeAllRanges();
    sel.addRange(range);
    btn.textContent = 'Select and copy';
  }
  setTimeout(() => { btn.textContent = 'Copy result'; }, 1800);
}

/* Boot -------------------------------------------------------------------- */

for (const btn of document.querySelectorAll('.mode')) {
  btn.onclick = () => {
    const mode = btn.dataset.mode;
    if (mode === 'draft') loadDraft();
    else openRun({ mode, avoid: state.lastSituation });
  };
}

el('btn-start').onclick = beginRun;
el('btn-start-back').onclick = loadMenu;
el('btn-draft-back').onclick = loadMenu;

el('btn-draft-start').onclick = () => openRun({
  mode: 'draft',
  bowler_ids: state.pickedBowlers,
  batter_ids: state.pickedBatters,
  situation_id: state.draftSituation,
});

// After a game: a different situation, the same one again, or back out.
el('btn-new').onclick = () => openRun({ mode: 'practice', avoid: state.lastSituation });
el('btn-same').onclick = () => {
  // The daily puzzle is fixed, so replaying it means the same match; a practice
  // situation gets a new key and therefore a genuinely different game.
  openRun({ mode: state.mode === 'daily' ? 'daily' : state.mode, avoid: '' });
};
el('btn-menu').onclick = loadMenu;

// The first screen replaces the entry the browser already has rather than
// adding one, so a single back press leaves the site as it would from any
// other page instead of stepping through an empty entry first.
history.replaceState({ screen: 'menu' }, '', location.hash || '#menu');
route((location.hash || '#menu').slice(1));
