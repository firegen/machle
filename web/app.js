'use strict';

/* ===========================================================================
   Football Team Balancer — frontend.
   Plain DOM, no framework, no build step. Talks to the Go REST API and mirrors
   its team statistics locally so manual moves feel instant.
   =========================================================================== */

const $ = (id) => document.getElementById(id);

const els = {
  teamSize: $('teamSize'),
  neededInfo: $('neededInfo'),
  weights: [$('wAttack'), $('wDefense'), $('wGoalkeeping'), $('wOverall')],
  weightsNormalised: $('weightsNormalised'),
  selectAll: $('selectAll'),
  clearSelection: $('clearSelection'),
  generate: $('generate'),
  selectionInfo: $('selectionInfo'),
  message: $('message'),
  playersBody: $('playersBody'),
  playersEmpty: $('playersEmpty'),
  playerCount: $('playerCount'),
  form: $('playerForm'),
  formTitle: $('form-title'),
  formSubmit: $('formSubmit'),
  formCancel: $('formCancel'),
  formError: $('formError'),
  fName: $('fName'),
  ratings: [$('fAttack'), $('fDefense'), $('fGoalkeeping'), $('fOverall')],
  results: $('results'),
  teams: $('teams'),
  balance: $('balance'),
  balanceMeter: $('balanceMeter'),
  difference: $('difference'),
  objective: $('objective'),
  method: $('method'),
  compareBody: $('compareBody'),
  resetTeams: $('resetTeams'),
  tweakNote: $('tweakNote'),
  gkThreshold: $('gkThreshold'),
};

const state = {
  players: [],
  selected: new Set(),
  config: null,
  result: null,   // live, manually editable copy of the last balance response
  generated: null, // pristine copy, used by "Reset to generated"
  manual: false, // true once the user has moved a player by hand
  weightsUsed: null, // exact raw weights sent with the last request
  weightsStale: false, // true when the weight inputs changed after a solve
  editingId: null,
  busy: false,
};

/* ================================ helpers ================================ */

const esc = (s) =>
  String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

// Rating sums read as one decimal, scores as two, balance as one. Anything more
// implies a precision the input does not have.
const f1 = (v) => (Math.round(v * 10) / 10).toFixed(1);
const f2 = (v) => (Math.round(v * 100) / 100).toFixed(2);

// An individual rating is shown as entered: 9, not 9.0.
const fr = (v) => String(Math.round(v * 100) / 100);

function readWeightsRaw() {
  return {
    attack: num(els.weights[0].value),
    defense: num(els.weights[1].value),
    goalkeeping: num(els.weights[2].value),
    overall: num(els.weights[3].value),
  };
}

function num(value) {
  const n = parseFloat(value);
  return Number.isFinite(n) ? n : 0;
}

// Same normalisation as model.Weights.Normalized(): a zeroed profile degrades
// into four equally important attributes instead of dividing by zero.
function normalizedWeights(raw) {
  const sum = raw.attack + raw.defense + raw.goalkeeping + raw.overall;
  if (sum <= 0) return { attack: 0.25, defense: 0.25, goalkeeping: 0.25, overall: 0.25 };
  return { attack: raw.attack / sum, defense: raw.defense / sum, goalkeeping: raw.goalkeeping / sum, overall: raw.overall / sum };
}

function isKeeper(player) {
  return player.goalkeeping >= (state.config?.goalkeeperThreshold ?? 5);
}

function statsFor(players, nw) {
  const s = { attack: 0, defense: 0, goalkeeping: 0, overall: 0, weightedScore: 0, goalkeepers: 0, bestGoalkeeping: 0 };
  for (const p of players) {
    s.attack += p.attack;
    s.defense += p.defense;
    s.goalkeeping += p.goalkeeping;
    s.overall += p.overall;
    s.weightedScore += p.attack * nw.attack + p.defense * nw.defense + p.goalkeeping * nw.goalkeeping + p.overall * nw.overall;
    if (isKeeper(p)) s.goalkeepers += 1;
    if (p.goalkeeping > s.bestGoalkeeping) s.bestGoalkeeping = p.goalkeeping;
  }
  return s;
}

function balancePercent(a, b) {
  const mean = (a + b) / 2;
  if (mean <= 0) return 100;
  return Math.min(100, Math.max(0, (1 - Math.abs(a - b) / mean) * 100));
}

// Mirrors balancer.Objective so a manual move shows whether it helped.
function objectiveFor(a, b) {
  const c = state.config?.coefficients ?? {
    weightedScore: 1, attack: 0.12, defense: 0.12, goalkeeping: 0.08, overall: 0.15, goalkeeperCount: 0.5, goalkeeperGap: 0.2,
  };
  const keeperDiff = Math.abs(a.goalkeepers - b.goalkeepers);
  const gapDiff = Math.abs(a.bestGoalkeeping - b.bestGoalkeeping);
  return (
    c.weightedScore * Math.abs(a.weightedScore - b.weightedScore) +
    c.attack * Math.abs(a.attack - b.attack) +
    c.defense * Math.abs(a.defense - b.defense) +
    c.goalkeeping * Math.abs(a.goalkeeping - b.goalkeeping) +
    c.overall * Math.abs(a.overall - b.overall) +
    c.goalkeeperCount * keeperDiff +
    c.goalkeeperGap * gapDiff
  );
}

function showMessage(text, kind = 'error') {
  if (!text) {
    els.message.hidden = true;
    els.message.textContent = '';
    return;
  }
  els.message.hidden = false;
  els.message.textContent = text;
  els.message.classList.toggle('error', kind === 'error');
  els.message.classList.toggle('ok', kind === 'ok');
}

async function api(path, options = {}) {
  const res = await fetch(path, {
    headers: options.body ? { 'Content-Type': 'application/json' } : undefined,
    ...options,
  });
  if (res.status === 204) return null;
  const text = await res.text();
  let payload = null;
  try {
    payload = text ? JSON.parse(text) : null;
  } catch {
    throw new Error(`unexpected response from ${path}`);
  }
  if (!res.ok) {
    throw new Error(payload?.error || `request failed (${res.status})`);
  }
  return payload;
}

/* ============================== player list ============================== */

async function loadConfig() {
  state.config = await api('/api/config');
  const sizes = state.config.teamSizes ?? [];
  els.teamSize.innerHTML = sizes.map((n) => `<option value="${n}">${n} vs ${n}</option>`).join('');
  // Default to 6 vs 6 when the server offers it, otherwise the first option.
  els.teamSize.value = String(sizes.includes(6) ? 6 : sizes[0] ?? 6);
  const w = state.config.weights;
  els.weights[0].value = w.attack;
  els.weights[1].value = w.defense;
  els.weights[2].value = w.goalkeeping;
  els.weights[3].value = w.overall;
  els.gkThreshold.textContent = String(state.config.goalkeeperThreshold);
}

async function loadPlayers() {
  state.players = (await api('/api/players')) ?? [];
  // Drop stale selections (deleted players) before re-rendering.
  const ids = new Set(state.players.map((p) => p.id));
  state.selected = new Set([...state.selected].filter((id) => ids.has(id)));
  renderPlayers();
}

function renderPlayers() {
  const rows = state.players
    .map((p) => `
      <tr data-id="${p.id}" class="${state.selected.has(p.id) ? 'sel' : ''}">
        <td class="col-check">
          <input type="checkbox" data-role="pick" data-id="${p.id}" ${state.selected.has(p.id) ? 'checked' : ''}
                 aria-label="Select ${esc(p.name)}">
        </td>
        <td data-label="Name">
          <span class="pname">${esc(p.name)}${isKeeper(p) ? '<span class="gk-badge" title="Goalkeeper-capable">🧤</span>' : ''}</span>
        </td>
        <td data-label="Att">${f1(p.attack)}</td>
        <td data-label="Def">${f1(p.defense)}</td>
        <td data-label="Gk">${f1(p.goalkeeping)}</td>
        <td data-label="Ovr" class="ovr">${f1(p.overall)}</td>
        <td class="col-actions">
          <span class="row-actions">
            <button type="button" data-role="edit" data-id="${p.id}">Edit</button>
            <button type="button" data-role="delete" data-id="${p.id}">Delete</button>
          </span>
        </td>
      </tr>`)
    .join('');

  els.playersBody.innerHTML = rows;
  els.playersEmpty.hidden = state.players.length > 0;
  els.playerCount.textContent = `${state.players.length} player${state.players.length === 1 ? '' : 's'}`;
  updateSelection();
}

function selectedPlayers() {
  return state.players.filter((p) => state.selected.has(p.id));
}

function teamSize() {
  return parseInt(els.teamSize.value, 10) || 0;
}

function updateSelection() {
  for (const row of els.playersBody.children) {
    const id = Number(row.dataset.id);
    row.classList.toggle('sel', state.selected.has(id));
    const box = row.querySelector('[data-role="pick"]');
    if (box) box.checked = state.selected.has(id);
  }

  const need = teamSize() * 2;
  const have = state.selected.size;
  els.neededInfo.textContent = need > 0 ? `exactly ${need} players required` : '';
  els.selectionInfo.textContent = `${have} selected · ${need} needed for ${teamSize()} vs ${teamSize()}`;
  els.selectionInfo.classList.toggle('bad', have !== need);
  els.selectionInfo.classList.toggle('ok', have === need);
  els.generate.disabled = have !== need || state.busy;

  if (have !== need && have > 0) {
    showMessage(`Select exactly ${need} players for ${teamSize()} vs ${teamSize()} — currently ${have}.`, 'error');
  } else {
    showMessage('');
  }
}

/* ================================ balance ================================ */

async function generateTeams() {
  const need = teamSize() * 2;
  const players = selectedPlayers();
  if (players.length !== need) {
    showMessage(`Select exactly ${need} players for ${teamSize()} vs ${teamSize()} — currently ${players.length}.`);
    return;
  }
  const raw = readWeightsRaw();
  if (raw.attack + raw.defense + raw.goalkeeping + raw.overall <= 0) {
    showMessage('Set at least one attribute weight above zero.');
    return;
  }

  state.busy = true;
  els.generate.disabled = true;
  els.generate.textContent = 'Balancing…';
  try {
    const result = await api('/api/balance', {
      method: 'POST',
      body: JSON.stringify({ teamSize: teamSize(), players, weights: raw }),
    });
    state.weightsUsed = raw; // reuse the exact input, not the server's rounded percentages
    state.weightsStale = false;
    state.generated = structuredClone(result);
    state.result = structuredClone(result);
    state.manual = false;
    renderResult();
    showMessage(`${result.teamSize} vs ${result.teamSize} solved by ${methodLabel(result.method)} (${result.explored.toLocaleString()} splits checked).`, 'ok');
    els.results.hidden = false;
    els.results.scrollIntoView({ behavior: 'smooth', block: 'start' });
  } catch (err) {
    showMessage(err.message);
  } finally {
    state.busy = false;
    els.generate.textContent = 'Generate teams';
    updateSelection();
  }
}

function methodLabel(method) {
  return method === 'exhaustive' ? 'exhaustive search' : 'local search fallback';
}

/* ================================ results ================================ */

function currentWeights() {
  // Right after a solve the readouts use the exact weights the search saw; as
  // soon as the user edits them, the numbers follow the inputs instead.
  return normalizedWeights(state.weightsUsed ?? readWeightsRaw());
}

function renderResult() {
  const r = state.result;
  if (!r) return;
  const nw = currentWeights();
  const a = statsFor(r.teamA.players, nw);
  const b = statsFor(r.teamB.players, nw);
  const balance = balancePercent(a.weightedScore, b.weightedScore);

  els.teams.innerHTML = [
    teamCard('A', r.teamA.players, a),
    teamCard('B', r.teamB.players, b),
  ].join('');

  els.balance.textContent = `${f1(balance)}%`;
  els.balanceMeter.style.width = `${Math.max(2, Math.min(100, balance))}%`;
  els.difference.textContent = `weighted score Δ ${f2(Math.abs(a.weightedScore - b.weightedScore))}`;
  els.objective.textContent = `objective ${f2(objectiveFor(a, b))}`;
  els.method.textContent = state.manual
    ? 'manually adjusted'
    : `${methodLabel(r.method)} · ${r.explored.toLocaleString()} splits`;
  els.resetTeams.hidden = !state.manual;

  const notes = [];
  if (state.manual) notes.push('Teams were adjusted by hand');
  if (state.weightsStale) notes.push('the weights changed after this split was solved');
  els.tweakNote.hidden = notes.length === 0;
  els.tweakNote.textContent = notes.length
    ? `${notes.join(' — ')}. Statistics recalculated live; press Generate teams to re-solve.`
    : '';

  renderCompare(a, b);
}

function teamCard(side, players, stats) {
  const cls = side === 'A' ? 'team-a' : 'team-b';
  const dot = side === 'A' ? '🔵' : '🟠';
  const target = side === 'A' ? 'B' : 'A';
  const rows = players
    .map(
      (p) => `
        <li>
          <span class="who"><b>${esc(p.name)}</b>${isKeeper(p) ? '<span class="gk-badge" title="Goalkeeper-capable">🧤</span>' : ''}
            <span class="sub">A ${fr(p.attack)} · D ${fr(p.defense)} · GK ${fr(p.goalkeeping)} · OVR ${fr(p.overall)}</span></span>
          <button type="button" data-role="move" data-side="${side}" data-id="${p.id}">Move to Team ${target}</button>
        </li>`
    )
    .join('');

  return `
    <article class="team ${cls}">
      <header><span>${dot} Team ${side}</span><span class="pill">${players.length} players${stats.goalkeepers ? ` · ${stats.goalkeepers}🧤` : ''}</span></header>
      <ol>${rows}</ol>
      <div class="stats">
        <div><span class="k">Attack</span><span class="v">${f1(stats.attack)}</span></div>
        <div><span class="k">Defense</span><span class="v">${f1(stats.defense)}</span></div>
        <div><span class="k">Goalkeeping</span><span class="v">${f1(stats.goalkeeping)}</span></div>
        <div><span class="k">Overall</span><span class="v">${f1(stats.overall)}</span></div>
        <div class="weighted"><span class="k">Weighted score</span><span class="v">${f2(stats.weightedScore)}</span></div>
      </div>
    </article>`;
}

function renderCompare(a, b) {
  const lines = [
    ['Attack', a.attack, b.attack, 1],
    ['Defense', a.defense, b.defense, 1],
    ['Goalkeeping', a.goalkeeping, b.goalkeeping, 1],
    ['Overall', a.overall, b.overall, 1],
    ['Goalkeepers (🧤)', a.goalkeepers, b.goalkeepers, 0],
  ];
  const html = lines
    .map(([label, av, bv, dp]) => {
      const d = Math.abs(av - bv);
      const tight = dp === 0 ? d === 0 : d <= Math.max(0.5, (Math.abs(av) + Math.abs(bv)) * 0.01);
      return `<tr><td>${label}</td><td class="a">${dp ? av.toFixed(dp) : av}</td><td class="b">${dp ? bv.toFixed(dp) : bv}</td>
        <td class="d ${tight ? 'tight' : 'wide'}">${dp ? d.toFixed(dp) : d}</td></tr>`;
    })
    .join('');
  const d = Math.abs(a.weightedScore - b.weightedScore);
  els.compareBody.innerHTML = `${html}
    <tr class="total"><td>Weighted score</td><td class="a">${f2(a.weightedScore)}</td><td class="b">${f2(b.weightedScore)}</td>
    <td class="d ${d <= 0.05 ? 'tight' : 'wide'}">${f2(d)}</td></tr>`;
}

function movePlayer(side, id) {
  const r = state.result;
  if (!r) return;
  const from = side === 'A' ? r.teamA.players : r.teamB.players;
  const to = side === 'A' ? r.teamB.players : r.teamA.players;
  const i = from.findIndex((p) => p.id === id);
  if (i < 0) return;
  to.push(from.splice(i, 1)[0]);
  state.manual = true;
  renderResult();
}

function resetTeams() {
  if (!state.generated) return;
  state.result = structuredClone(state.generated);
  state.manual = false;
  renderResult();
  showMessage('Restored the generated teams.', 'ok');
}

// A displayed result is a snapshot of the roster taken at generation time, so
// editing or deleting a player has to be pushed into it. Without this the team
// statistics would silently describe ratings that no longer exist.
function syncResultWithRoster() {
  if (!state.result) return;
  const byId = new Map(state.players.map((p) => [p.id, p]));
  let touched = false;

  for (const snapshot of [state.generated, state.result]) {
    if (!snapshot) continue;
    for (const team of [snapshot.teamA, snapshot.teamB]) {
      const kept = [];
      for (const player of team.players) {
        const fresh = byId.get(player.id);
        if (!fresh) {
          touched = true;
          continue;
        }
        if (JSON.stringify(fresh) !== JSON.stringify(player)) touched = true;
        kept.push({ ...fresh });
      }
      team.players = kept;
    }
  }

  if (touched) renderResult();
}

/* =========================== player management =========================== */

function fillForm(player) {
  state.editingId = player ? player.id : null;
  els.formTitle.textContent = player ? `Edit player · ${player.name}` : 'Add player';
  els.formSubmit.textContent = player ? 'Save changes' : 'Add player';
  els.formCancel.hidden = !player;
  els.fName.value = player ? player.name : '';
  els.ratings[0].value = player ? player.attack : '';
  els.ratings[1].value = player ? player.defense : '';
  els.ratings[2].value = player ? player.goalkeeping : '';
  els.ratings[3].value = player ? player.overall : '';
  showFormError('');
}

function showFormError(text) {
  els.formError.hidden = !text;
  els.formError.textContent = text || '';
}

async function submitForm(event) {
  event.preventDefault();
  const { min, max } = state.config?.rating ?? { min: 1, max: 10 };
  const name = els.fName.value.trim();
  if (!name) return showFormError('Name is required.');

  const values = {};
  const keys = ['attack', 'defense', 'goalkeeping', 'overall'];
  for (let i = 0; i < keys.length; i += 1) {
    const raw = els.ratings[i].value;
    const v = parseFloat(raw);
    if (raw === '' || !Number.isFinite(v)) return showFormError(`${labelOf(keys[i])} is required.`);
    if (v < min || v > max) return showFormError(`${labelOf(keys[i])} must be between ${min} and ${max} (got ${v}).`);
    values[keys[i]] = Math.round(v * 100) / 100;
  }
  showFormError('');

  const body = JSON.stringify({ id: state.editingId ?? 0, name, ...values });
  try {
    const saved = state.editingId
      ? await api(`/api/players/${state.editingId}`, { method: 'PUT', body })
      : await api('/api/players', { method: 'POST', body });
    fillForm(null);
    await loadPlayers();
    syncResultWithRoster();
    showMessage(`${saved.name} saved.`, 'ok');
  } catch (err) {
    showFormError(err.message);
  }
}

function labelOf(key) {
  return { attack: 'Attack', defense: 'Defense', goalkeeping: 'Goalkeeping', overall: 'Overall' }[key];
}

async function deletePlayer(id) {
  const player = state.players.find((p) => p.id === id);
  if (!player) return;
  if (!window.confirm(`Delete ${player.name}?`)) return;
  try {
    await api(`/api/players/${id}`, { method: 'DELETE' });
    state.selected.delete(id);
    if (state.editingId === id) fillForm(null);
    await loadPlayers();
    syncResultWithRoster();
    showMessage(`${player.name} deleted.`, 'ok');
  } catch (err) {
    showMessage(err.message);
  }
}

/* ================================ wiring ================================ */

function updateWeightsLabel() {
  const raw = readWeightsRaw();
  const sum = raw.attack + raw.defense + raw.goalkeeping + raw.overall;
  if (sum <= 0) {
    els.weightsNormalised.textContent = 'all weights are zero — they will be treated as equal (25% each)';
    return;
  }
  const pct = (v) => `${Math.round((v / sum) * 1000) / 10}%`;
  els.weightsNormalised.textContent =
    `${pct(raw.attack)} / ${pct(raw.defense)} / ${pct(raw.goalkeeping)} / ${pct(raw.overall)}`;
}

function applyPreset(value) {
  const parts = value.split(',');
  parts.forEach((v, i) => {
    if (els.weights[i]) els.weights[i].value = v.trim();
  });
  onWeightsInput();
}

// Weight edits change how a split is judged, so an open result is re-scored
// with the new profile and flagged as no longer the solver's optimum.
function onWeightsInput() {
  updateWeightsLabel();
  if (!state.result) return;
  state.weightsUsed = null;
  state.weightsStale = true;
  renderResult();
}

els.playersBody.addEventListener('change', (event) => {
  const box = event.target.closest('[data-role="pick"]');
  if (!box) return;
  const id = Number(box.dataset.id);
  if (box.checked) state.selected.add(id);
  else state.selected.delete(id);
  updateSelection();
});

els.playersBody.addEventListener('click', (event) => {
  const btn = event.target.closest('button[data-role]');
  if (!btn) return;
  const id = Number(btn.dataset.id);
  if (btn.dataset.role === 'edit') fillForm(state.players.find((p) => p.id === id));
  if (btn.dataset.role === 'delete') deletePlayer(id);
});

els.selectAll.addEventListener('click', () => {
  state.players.forEach((p) => state.selected.add(p.id));
  updateSelection();
});

els.clearSelection.addEventListener('click', () => {
  state.selected.clear();
  updateSelection();
});

els.teamSize.addEventListener('change', updateSelection);
els.weights.forEach((input) => input.addEventListener('input', onWeightsInput));
document.querySelectorAll('[data-preset]').forEach((btn) =>
  btn.addEventListener('click', () => applyPreset(btn.dataset.preset))
);
els.generate.addEventListener('click', generateTeams);
els.teams.addEventListener('click', (event) => {
  const btn = event.target.closest('[data-role="move"]');
  if (btn) movePlayer(btn.dataset.side, Number(btn.dataset.id));
});
els.resetTeams.addEventListener('click', resetTeams);
els.form.addEventListener('submit', submitForm);
els.formCancel.addEventListener('click', () => fillForm(null));

async function boot() {
  try {
    await loadConfig();
    await loadPlayers();
    updateWeightsLabel();
    updateSelection();
  } catch (err) {
    showMessage(`Could not reach the server: ${err.message}`);
  }
}

boot();
