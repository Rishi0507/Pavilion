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
  defendGrid: [],
  chaseGrid: [],
  busy: false,
};

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

/* Screens ----------------------------------------------------------------- */

function show(name) {
  for (const id of ['screen-start', 'screen-play', 'screen-result']) {
    el(id).hidden = id !== `screen-${name}`;
  }
}

/* Start ------------------------------------------------------------------- */

async function loadToday() {
  try {
    const p = await api('GET', '/api/v1/puzzle/today');
    state.puzzle = p;

    el('dateline').textContent = p.date;
    el('start-target').textContent = p.target;
    el('start-venue').textContent = p.venue;
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
  } catch (err) {
    showError(err.message);
  }
}

async function startRun() {
  clearError();
  try {
    const r = await api('POST', '/api/v1/run', {});
    state.runID = r.run_id;
    state.token = r.token;
    state.decisions = r.decisions;
    state.half = r.state.half;
    state.defendGrid = [];
    state.chaseGrid = [];

    buildStrips();
    render(r.state);
    show('play');
  } catch (err) {
    showError(err.message);
  }
}

/* The over strip ---------------------------------------------------------- */

function buildStrips() {
  for (const id of ['strip-defend', 'strip-chase']) {
    const strip = el(id);
    strip.replaceChildren();
    for (let i = 0; i < 20; i++) {
      const box = document.createElement('div');
      box.className = 'box';
      box.dataset.over = String(i);
      strip.append(box);
    }
  }
  el('strip-chase').hidden = true;
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

const overRuns = { defend: [], chase: [] };

/* Rendering --------------------------------------------------------------- */

function render(s) {
  state.decisions = s.decisions;
  state.half = s.half;

  // A finished run has no decision to offer and no innings in progress, so the
  // play screen has nothing to draw. Rendering it anyway is what produced a
  // crash on the very last over of the game, where it was most visible.
  if (s.half === 'finished') return;

  el('sb-score').textContent = s.score;
  el('sb-wkts').textContent = s.wickets;
  el('over-no').textContent = `OVER ${Math.min(s.over + 1, 20)}`;
  el('striker').textContent = s.striker ? `${s.striker} (${s.striker_balls})` : '';

  if (s.half === 'chase') {
    el('sb-need-label').textContent = 'NEED';
    el('sb-need').textContent = `${s.runs_needed} off ${s.balls_left}`;
    el('sb-wp-label').textContent = 'WINNING';
  } else {
    el('sb-need-label').textContent = 'THEY NEED';
    el('sb-need').textContent = `${s.runs_needed} off ${s.balls_left}`;
    el('sb-wp-label').textContent = 'DEFENDING';
  }
  el('sb-wp').textContent = `${Math.round(100 * s.win_probability)}%`;

  state.defendGrid = s.defend_grid || [];
  state.chaseGrid = s.chase_grid || [];

  const chasing = s.half === 'chase';
  el('strip-chase').hidden = !chasing;
  paintStrip('strip-defend', state.defendGrid, overRuns.defend, chasing ? -1 : s.over);
  paintStrip('strip-chase', state.chaseGrid, overRuns.chase, chasing ? s.over : -1);

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
    const legal = legalIDs.includes(i);

    const card = document.createElement('button');
    card.className = 'bowler';
    card.disabled = !legal || state.busy;
    card.onclick = () => playDefendOver(i);

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
    btn.onclick = () => playChaseOver(btn.dataset.intent);
  }
}

/* Playing an over --------------------------------------------------------- */

function setBusy(on) {
  state.busy = on;
  for (const b of document.querySelectorAll('.bowler, .intent')) b.disabled = on;
}

async function playDefendOver(bowler) {
  await playOver('defend', { bowler_id: bowler, decisions: state.decisions });
}

async function playChaseOver(intent) {
  await playOver('chase', { intent, decisions: state.decisions });
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

    if (wasHalf === 'defend' && r.state.half === 'chase') {
      await handover(r);
    }
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

/* The reveal.
 *
 * Six balls, one at a time, then a beat. This is the only orchestrated moment
 * in the interface, and it is deliberately the one that carries the drama: the
 * decision has already been made and cannot be taken back, so the pacing is
 * pure consequence. Reduced motion collapses it to an instant state change. */

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
  const wkts = r.wickets === 1 ? '1 wicket' : `${r.wickets} wickets`;
  summary.textContent = `${r.bowler} · ${r.runs} run${r.runs === 1 ? '' : 's'}` +
    (r.wickets ? `, ${wkts}` : '');
  wrap.append(summary);

  await sleep(BEAT_MS);
}

async function handover(r) {
  const box = el('handover');
  const res = r.state;
  box.textContent = res.runs_needed > 0
    ? `You defended it. Now chase the same target from the other chair.`
    : `They got there. Now see if you can do it from the other chair.`;
  box.hidden = false;
  el('balls').replaceChildren();
  await sleep(REDUCED ? 0 : 1200);
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
    el('result-pct').textContent = r.day.runs > 1 ? `${Math.round(r.percentile)}` : '—';
    el('result-streak').textContent = r.streak || '—';

    el('result-day').textContent = r.day.runs > 1
      ? `${r.day.runs} people have played today. ${Math.round(100 * r.day.defend_rate)}% defended it, ${Math.round(100 * r.day.chase_rate)}% chased it.`
      : 'You are the first to play today.';

    el('btn-copy').onclick = () => copyShare(r.share);
    show('result');
  } catch (err) {
    showError(err.message);
  }
}

function verdictOf(r) {
  if (r.defended && r.chased) return 'Both halves. Nobody does that twice.';
  if (r.defended) return 'Defended it, could not chase it.';
  if (r.chased) return 'Chased it down, could not defend it.';
  return 'Neither half. Tomorrow, then.';
}

async function copyShare(text) {
  const btn = el('btn-copy');
  try {
    await navigator.clipboard.writeText(text);
    btn.textContent = 'Copied';
  } catch {
    // Clipboard access is refused in plenty of ordinary situations, so fall
    // back to selecting the text rather than telling the player it failed.
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

el('btn-start').onclick = startRun;
el('btn-again').onclick = () => { clearError(); loadToday(); };
loadToday();
