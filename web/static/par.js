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

function show(name) {
  for (const id of ['screen-menu', 'screen-draft', 'screen-start', 'screen-play', 'screen-result']) {
    el(id).hidden = id !== `screen-${name}`;
  }
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
  el('start-venue').textContent = p.venue;
  el('start-need').textContent = `${p.target} runs`;
  el('start-unvalidated').hidden = p.validated;

  const list = el('start-attack');
  list.replaceChildren();
  for (const b of p.attack) {
    const li = document.createElement('li');
    const name = document.createElement('span');
    name.textContent = b.name;
    const style = document.createElement('span');
    style.className = 'style';
    style.textContent = b.style;
    li.append(name, style);
    list.append(li);
  }
  show('start');
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
    updateBudget();
    show('draft');
  } catch (err) {
    showError(err.message);
  }
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
    style.textContent = p.style || p.hand || '';

    const cost = document.createElement('span');
    cost.className = 'pick-cost';
    cost.textContent = `${p.cost} cr`;

    btn.append(name, style, cost);
    btn.title = p.note || '';
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

  el('sb-score').textContent = s.score;
  el('sb-wkts').textContent = s.wickets;
  el('over-no').textContent = `OVER ${Math.min(s.over + 1, 20)}`;
  el('striker').textContent = s.striker ? `${s.striker} (${s.striker_balls})` : '';

  el('sb-need-label').textContent = chasing ? 'YOU NEED' : 'THEY NEED';
  el('sb-need').textContent = `${s.runs_needed} off ${s.balls_left}`;
  el('sb-wp-label').textContent = chasing ? 'YOU WIN' : 'YOU HOLD';
  el('sb-wp').textContent = `${Math.round(100 * s.win_probability)}%`;

  el('strip-chase-row').hidden = !chasing;
  paintStrip('strip-defend', s.defend_grid || [], overRuns.defend, chasing ? -1 : s.over);
  paintStrip('strip-chase', s.chase_grid || [], overRuns.chase, chasing ? s.over : -1);

  el('controls-defend').hidden = chasing;
  el('controls-chase').hidden = !chasing;

  if (chasing) renderIntents(s);
  else renderBowlers(s);
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

    const name = document.createElement('span');
    name.className = 'bowler-name';
    name.textContent = b.name;

    const style = document.createElement('span');
    style.className = 'bowler-style';
    style.textContent = b.style;

    const pips = document.createElement('span');
    pips.className = 'pips';
    for (let k = 0; k < 4; k++) {
      const pip = document.createElement('span');
      pip.className = k < used ? 'pip used' : 'pip';
      pips.append(pip);
    }
    card.setAttribute('aria-label', `${b.name}, ${b.style}, ${used} of 4 overs bowled`);
    card.append(name, style, pips);
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
    if (d.wicket) b.classList.add('wicket');
    else if (d.outcome === '4' || d.outcome === '6') b.classList.add('boundary');
    else if (!d.legal) b.classList.add('extra');
    b.textContent = d.wicket ? 'W' : d.outcome;
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
});

// After a game: a different situation, the same one again, or back out.
el('btn-new').onclick = () => openRun({ mode: 'practice', avoid: state.lastSituation });
el('btn-same').onclick = () => {
  // The daily puzzle is fixed, so replaying it means the same match; a practice
  // situation gets a new key and therefore a genuinely different game.
  openRun({ mode: state.mode === 'daily' ? 'daily' : state.mode, avoid: '' });
};
el('btn-menu').onclick = loadMenu;

loadMenu();
