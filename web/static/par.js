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
  today: null,
  draft: null,
  draftSituation: '',
  pickedBowlers: [],
  pickedBatters: [],
  busy: false,
};

/* The over log.
 *
 * One entry per completed over, holding what a scorer would write down: who
 * bowled, what it cost and how many went down. The Manhattan chart and the
 * bowling card are both drawn from this, so the two can never disagree with
 * each other about what happened.
 */
const log = { defend: [], chase: [] };

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
const RESUMABLE = { menu: true, draft: true, result: true, board: true };

function route(name) {
  switch (name) {
    case 'draft': return loadDraft();
    case 'board': return loadBoard();
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

const SCREENS = ['screen-menu', 'screen-board', 'screen-draft',
  'screen-start', 'screen-play', 'screen-result'];

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
  for (const id of SCREENS) {
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
    state.today = p;
    el('dateline').textContent = p.date;
    el('menu-target').textContent = p.target;

    const ground = p.ground || { name: p.venue };
    el('menu-where').textContent = ground.city
      ? `at ${ground.name}, ${ground.city}`
      : `at ${ground.name || ''}`;

    show('menu');
    // The ground sits behind the hero rather than beside it, so the first
    // screen is a place rather than a menu.
    showGround('#screen-menu', ground);
  } catch (err) {
    showError(err.message);
  }
}

/* The leaderboard ----------------------------------------------------------
 *
 * Solving means winning both halves, and the board says so plainly: solved
 * runs first, then everything else by decision score. Ranking on score alone
 * would put a clever defeat above a win.
 */
async function loadBoard() {
  clearError();
  try {
    const date = (state.today && state.today.date) || '';
    const b = await api('GET', `/api/v1/day/${date}/leaderboard`);
    el('board-date').textContent = b.date || date;

    const rows = b.leaders || [];
    const list = el('board-rows');
    list.replaceChildren();

    rows.forEach((r, i) => {
      const li = document.createElement('li');
      li.className = 'board-row' + (r.solved ? ' solved' : '');
      li.style.setProperty('--i', String(i));

      const rank = document.createElement('span');
      rank.className = 'board-rank';
      rank.textContent = i + 1;

      const who = document.createElement('span');
      who.className = 'board-who';
      who.textContent = r.player;

      const tag = document.createElement('span');
      tag.className = 'board-tag';
      tag.textContent = r.solved
        ? 'solved'
        : r.defended ? 'held only' : r.chased ? 'chased only' : 'neither';

      const score = document.createElement('span');
      score.className = 'board-score';
      score.textContent = (r.score >= 0 ? '+' : '') + r.score.toFixed(0);

      li.append(rank, who, tag, score);
      list.append(li);
    });

    el('board-empty').hidden = rows.length > 0;
    show('board');
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
  log.defend = [];
  log.chase = [];

  buildCharts();
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
    wireFilters('bowlers', 'draft-bowlers', d.bowlers);
    wireFilters('batters', 'draft-batters', d.batters);
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

/* Filtering the pool -------------------------------------------------------
 *
 * A hundred and seventy bowlers across eighteen seasons is a lot of list, and
 * a search box only helps somebody who already knows the name they want. The
 * filters answer the questions a selector actually has: who is left-arm, who
 * is a Chennai player, who is still playing, and what can I get for eight
 * credits.
 *
 * Everything runs on the pool already in memory, so it is instant and the
 * server is not asked again. A player who is already picked is never hidden:
 * a filter that concealed part of the side being assembled would make the
 * budget line and the visible cards disagree.
 */
const filters = {};

function optionsFrom(pool, key) {
  const seen = new Set();
  for (const p of pool) {
    const v = (p[key] || '').trim();
    if (v) seen.add(v);
  }
  return [...seen].sort();
}

function fill(select, values) {
  const keep = select.value;
  select.replaceChildren();
  const any = document.createElement('option');
  any.value = '';
  any.textContent = 'Any';
  select.append(any);
  for (const v of values) {
    const o = document.createElement('option');
    o.value = v;
    o.textContent = v;
    select.append(o);
  }
  select.value = values.includes(keep) ? keep : '';
}

// Somebody is "playing now" if their last recorded season is the most recent
// one in the data. Hard-coding a year would go stale the moment a season ends.
function latestSeason(pool) {
  let last = 0;
  for (const p of pool) {
    const end = Number(String(p.years || '').slice(-4));
    if (Number.isFinite(end) && end > last) last = end;
  }
  return last;
}

function wireFilters(which, containerID, pool) {
  const f = {
    q: '',
    team: '',
    style: '',
    era: '',
    cost: 20,
    sort: 'cost',
    pool,
    latest: latestSeason(pool),
    containerID,
  };
  filters[which] = f;

  fill(el(`f-${which}-team`), optionsFrom(pool, 'team'));
  fill(el(`f-${which}-style`), optionsFrom(pool, which === 'bowlers' ? 'style' : 'hand'));

  // The slider spans the prices that actually exist. A fixed 3-to-20 range left
  // the bottom third of the track dead, because the cheapest bowler with a real
  // record is not the cheapest price the scale allows.
  const maxCost = pool.reduce((m, p) => Math.max(m, p.cost), 3);
  const minCost = pool.reduce((m, p) => Math.min(m, p.cost), maxCost);
  const cost = el(`f-${which}-cost`);
  cost.min = String(minCost);
  cost.max = String(maxCost);
  cost.value = String(maxCost);
  f.cost = maxCost;
  f.maxCost = maxCost;
  el(`f-${which}-cost-out`).textContent = maxCost;

  const search = el(`search-${which}`);
  search.value = '';

  const apply = () => applyFilters(which);

  search.oninput = () => { f.q = search.value.trim().toLowerCase(); apply(); };
  el(`f-${which}-team`).onchange = (e) => { f.team = e.target.value; apply(); };
  el(`f-${which}-style`).onchange = (e) => { f.style = e.target.value; apply(); };
  el(`f-${which}-era`).onchange = (e) => { f.era = e.target.value; apply(); };
  el(`f-${which}-sort`).onchange = (e) => { f.sort = e.target.value; apply(); };
  cost.oninput = (e) => {
    f.cost = Number(e.target.value);
    el(`f-${which}-cost-out`).textContent = f.cost;
    apply();
  };
  el(`f-${which}-clear`).onclick = () => {
    f.q = ''; f.team = ''; f.style = ''; f.era = ''; f.sort = 'cost'; f.cost = f.maxCost;
    search.value = '';
    el(`f-${which}-team`).value = '';
    el(`f-${which}-style`).value = '';
    el(`f-${which}-era`).value = '';
    el(`f-${which}-sort`).value = 'cost';
    cost.value = String(maxCost);
    el(`f-${which}-cost-out`).textContent = maxCost;
    apply();
  };

  apply();
}

function matches(p, f, which) {
  if (f.team && p.team !== f.team) return false;
  if (f.style) {
    const v = which === 'bowlers' ? p.style : p.hand;
    if (v !== f.style) return false;
  }
  if (f.cost && p.cost > f.cost) return false;
  if (f.era) {
    const end = Number(String(p.years || '').slice(-4));
    const current = Number.isFinite(end) && end >= f.latest - 1;
    if (f.era === 'now' && !current) return false;
    if (f.era === 'past' && current) return false;
  }
  if (f.q) {
    const hay = `${p.name} ${p.team || ''} ${p.style || ''} ${p.hand || ''} ${p.years || ''}`.toLowerCase();
    if (!hay.includes(f.q)) return false;
  }
  return true;
}

function applyFilters(which) {
  const f = filters[which];
  if (!f) return;

  const order = {
    cost: (a, b) => b.cost - a.cost || a.name.localeCompare(b.name),
    rating: (a, b) => b.rating - a.rating || a.name.localeCompare(b.name),
    name: (a, b) => a.name.localeCompare(b.name),
  }[f.sort];

  const rank = new Map();
  [...f.pool].sort(order).forEach((p, i) => rank.set(p.id, i));

  let shown = 0;
  for (const btn of el(f.containerID).children) {
    const id = Number(btn.dataset.id);
    const p = f.pool.find((x) => x.id === id);
    const picked = btn.classList.contains('on');
    const ok = picked || (p && matches(p, f, which));
    btn.hidden = !ok;
    btn.style.order = String(rank.get(id) ?? 0);
    if (ok && !picked) shown++;
  }

  const total = f.pool.length;
  el(`count-${which}`).textContent = shown === total
    ? `all ${total}`
    : `${shown} of ${total}`;
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
  applyFilters(containerID === 'draft-bowlers' ? 'bowlers' : 'batters');
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

/* The Manhattan ------------------------------------------------------------
 *
 * A bar for every over: height is what the over cost, colour is what it did to
 * the win probability, and a notch across the top is a wicket. It is the chart
 * every scorecard in the sport draws, and the one this project is named after.
 *
 * The version before this was twenty small squares with a number in each. That
 * told you the runs and the colour and nothing else, and at the width of a
 * column it told you neither: a two-digit number in a fourteen-pixel box is not
 * a number, it is a smudge. A bar is read by its height, which survives being
 * small, and the shape of twenty of them is the shape of the innings — where
 * the powerplay went, where it stalled, which over broke it.
 */

// The tallest bar. Overs above this are clipped and marked, which is rarer than
// scaling every innings to its own maximum and much easier to compare across
// two halves of the same game.
const MH_MAX = 24;

function buildPlot(id) {
  const plot = el(id);
  plot.replaceChildren();
  for (let i = 0; i < 20; i++) {
    const col = document.createElement('div');
    col.className = 'mh-col';

    const bar = document.createElement('div');
    bar.className = 'mh-bar';

    const runs = document.createElement('span');
    runs.className = 'mh-runs';

    col.append(runs, bar);
    plot.append(col);
  }
}

function buildCharts() {
  buildPlot('mh-defend');
  buildPlot('mh-chase');
  el('mh-chase-wrap').hidden = true;
  el('bowling-card').hidden = true;
}

function paintChart(id, entries, grades, currentOver) {
  const cols = el(id).children;
  for (let i = 0; i < cols.length; i++) {
    const col = cols[i];
    const bar = col.querySelector('.mh-bar');
    const runs = col.querySelector('.mh-runs');
    const e = entries[i];

    col.className = 'mh-col';
    bar.replaceChildren();

    if (!e) {
      bar.style.height = '0%';
      runs.textContent = '';
      if (i === currentOver) col.classList.add('now');
      continue;
    }

    col.classList.add(grades[i] || 'level');
    // A maiden still needs to occupy the axis, or the innings appears to have a
    // hole in it where the best over of the day was.
    bar.style.height = `${Math.max(4, Math.min(100, (e.runs / MH_MAX) * 100))}%`;
    runs.textContent = e.runs;
    if (e.runs > MH_MAX) col.classList.add('over-scale');

    for (let w = 0; w < e.wickets; w++) {
      const notch = document.createElement('i');
      notch.className = 'mh-wkt';
      notch.style.setProperty('--n', String(w));
      bar.append(notch);
    }
  }
}

function paintCharts(s, chasing) {
  paintChart('mh-defend', log.defend, s.defend_grid || [], chasing ? -1 : s.over);
  paintChart('mh-chase', log.chase, s.chase_grid || [], chasing ? s.over : -1);

  el('mh-chase-wrap').hidden = !chasing;
  el('mh-defend-total').textContent = totalOf(log.defend);
  el('mh-chase-total').textContent = totalOf(log.chase);
}

function totalOf(entries) {
  if (!entries.length) return '';
  const runs = entries.reduce((a, e) => a + e.runs, 0);
  const wkts = entries.reduce((a, e) => a + e.wickets, 0);
  return `${runs}/${wkts} in ${entries.length}`;
}

/* The bowling card ---------------------------------------------------------
 *
 * Overs, runs, wickets and economy for each of the five, which is what a scorer
 * keeps and what a captain actually decides on. The game asked a player to
 * choose a bowler every over while showing them only how many overs each had
 * left, which is the least interesting of the four numbers.
 */
function renderBowlingCard() {
  const card = el('bowling-card');
  if (!state.puzzle || state.half !== 'defend' || !log.defend.length) {
    card.hidden = true;
    return;
  }

  const figures = new Map();
  for (const b of state.puzzle.attack) {
    figures.set(b.name, { name: b.name, team: b.team, colour: b.colour, overs: 0, runs: 0, wickets: 0 });
  }
  for (const e of log.defend) {
    const f = figures.get(e.bowler);
    if (!f) continue;
    f.overs += 1;
    f.runs += e.runs;
    f.wickets += e.wickets;
  }

  const rows = [...figures.values()]
    .filter((f) => f.overs > 0)
    .sort((a, b) => b.wickets - a.wickets || a.runs / a.overs - b.runs / b.overs);

  const body = el('bowling-rows');
  body.replaceChildren();
  for (const f of rows) {
    const tr = document.createElement('tr');
    if (f.colour) tr.style.setProperty('--tint', f.colour);

    const name = document.createElement('th');
    name.scope = 'row';
    name.textContent = f.name;

    tr.append(name);
    for (const v of [f.overs, f.runs, f.wickets, (f.runs / f.overs).toFixed(1)]) {
      const td = document.createElement('td');
      td.textContent = v;
      tr.append(td);
    }
    body.append(tr);
  }
  card.hidden = rows.length === 0;
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

  paintCharts(s, chasing);
  renderBowlingCard();

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

    (half === 'defend' ? log.defend : log.chase).push({
      over: r.over,
      bowler: r.bowler,
      runs: r.runs,
      wickets: r.wickets,
    });
    const wasHalf = state.half;
    render(r.state);

    if (r.runs >= 15 && !REDUCED) {
      const plot = wasHalf === 'defend' ? 'mh-defend' : 'mh-chase';
      const col = el(plot).children[r.over - 1];
      if (col) {
        col.classList.add('big');
        setTimeout(() => col.classList.remove('big'), 460);
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
    el('result-score').className = 'big-figure ' +
      (r.total_score > 2 ? 'good' : r.total_score < -2 ? 'bad' : 'level');

    paintHalf('defend', r.defended, r.defend_margin, r.defend_margin_wkts, r.defend_score);
    paintHalf('chase', r.chased, r.chase_margin, r.chase_margin_wkts, r.chase_score);

    // The same chart the game was played on, so the result is read in the same
    // terms as the decisions were made in.
    buildPlot('mh-r-defend');
    buildPlot('mh-r-chase');
    paintChart('mh-r-defend', log.defend, r.defend_grid || [], -1);
    paintChart('mh-r-chase', log.chase, r.chase_grid || [], -1);
    el('mh-r-defend-total').textContent = totalOf(log.defend);
    el('mh-r-chase-total').textContent = totalOf(log.chase);
    el('result-pct').textContent = r.counts && r.day.runs > 1 ? Math.round(r.percentile) : '—';
    el('result-streak').textContent = r.counts && r.streak ? r.streak : '—';

    el('result-day').textContent = !r.counts
      ? 'Practice runs are not added to the day’s figures, so today’s puzzle is still there to play.'
      : r.day.runs > 1
        ? `${r.day.runs} people have played today. ${Math.round(100 * r.day.defend_rate)}% held the target, ${Math.round(100 * r.day.chase_rate)}% chased it down.`
        : 'You are the first to play today.';

    el('btn-copy').onclick = () => copyShare(r.share);
    offerTheBoard(r);
    show('result');
  } catch (err) {
    showError(err.message);
  }
}

/* paintHalf summarises one innings: whether it was won, by how much, and what
 * the choices in it were worth.
 *
 * The margin is the part a scorecard leads with and the game never showed at
 * all. Holding a target by one run and holding it by forty are different
 * afternoons.
 *
 * Cricket reports a margin two ways and neither substitutes for the other: a
 * side that falls short loses by runs, and a side that gets there wins with
 * wickets in hand. Reading only the runs figure produced "short by 0 runs" for
 * a chase that succeeded, which is not a thing anybody says.
 */
function paintHalf(which, won, runs, wickets, score) {
  const box = el(`half-${which}`);
  box.classList.toggle('won', won);
  box.classList.toggle('lost', !won);

  const plural = (n, word) => `${n} ${word}${n === 1 ? '' : 's'}`;
  const decisions = `decisions ${score >= 0 ? '+' : ''}${score.toFixed(0)}`;

  let mark;
  let line;
  if (which === 'defend') {
    // Defending: won means they fell short by runs, lost means they got there
    // with wickets to spare.
    mark = won ? 'HELD' : 'LOST';
    line = won
      ? `by ${plural(Math.abs(runs), 'run')}`
      : `they got there with ${plural(Math.abs(wickets), 'wicket')} in hand`;
  } else {
    mark = won ? 'CHASED' : 'SHORT';
    line = won
      ? `with ${plural(Math.abs(wickets), 'wicket')} in hand`
      : `by ${plural(Math.abs(runs), 'run')}`;
  }

  el(`half-${which}-mark`).textContent = mark;
  el(`half-${which}-line`).textContent = `${line} · ${decisions}`;
}

/* Claiming a place on the board.
 *
 * The name is asked for only when the puzzle is actually solved, which means
 * both halves won. Asking on every result would be a form in the way of the
 * score, and a board that anybody can join by losing is not a board.
 *
 * It is asked for after the fact because that is when the achievement exists.
 * The run has already been saved by then, so the name is attached to it
 * separately, and the run identifier — random, and known only to this session —
 * is what entitles this page to attach it.
 */
function offerTheBoard(r) {
  const form = el('claim');
  const solved = r.defended && r.chased;

  form.hidden = !(solved && r.counts);
  if (form.hidden) return;

  const note = el('claim-note');
  const input = el('claim-name');
  const saved = localStorage.getItem('par.player') || '';

  input.value = saved;
  note.textContent = '';
  form.classList.remove('done');

  form.onsubmit = async (e) => {
    e.preventDefault();
    const name = input.value.trim();
    if (!name) return;
    try {
      const out = await api('POST', `/api/v1/run/${state.runID}/name`, { player: name });
      localStorage.setItem('par.player', out.player);
      form.classList.add('done');
      note.textContent = `On the board as ${out.player}.`;
      el('btn-claim').textContent = 'Saved';
    } catch (err) {
      note.textContent = err.message;
    }
  };
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

/* Keyboard ------------------------------------------------------------------
 *
 * A daily puzzle is played over and over, and forty mouse trips to five cards
 * is forty more than it needs to be. The digits pick a bowler while defending
 * and an intent while chasing; the numbering matches the order on screen, so
 * the keys are learned by using them rather than by reading about them.
 *
 * Nothing here fires while an over is being revealed, and nothing fires while a
 * search box has focus, or typing "little" into the bowler filter would bowl
 * four overs.
 */
window.addEventListener('keydown', (e) => {
  if (e.metaKey || e.ctrlKey || e.altKey) return;
  if (state.busy || el('screen-play').hidden) return;

  const tag = document.activeElement && document.activeElement.tagName;
  if (tag === 'INPUT' || tag === 'TEXTAREA') return;

  const n = Number(e.key);
  if (!Number.isInteger(n) || n < 1) return;

  const buttons = state.half === 'chase'
    ? [...el('controls-chase').querySelectorAll('.intent')]
    : [...el('bowlers').children];

  const btn = buttons[n - 1];
  if (!btn || btn.disabled) return;
  e.preventDefault();
  btn.click();
});

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
el('btn-board').onclick = loadBoard;
el('btn-board-back').onclick = loadMenu;

// The first screen replaces the entry the browser already has rather than
// adding one, so a single back press leaves the site as it would from any
// other page instead of stepping through an empty entry first.
history.replaceState({ screen: 'menu' }, '', location.hash || '#menu');
route((location.hash || '#menu').slice(1));
