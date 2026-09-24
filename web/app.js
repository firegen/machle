'use strict';

/* ===========================================================================
   Football Team Balancer — frontend.
   Plain DOM, no framework, no build step. Talks to the Go REST API and mirrors
   its team statistics locally so manual moves feel instant.
   The interface is Bulgarian; backend error strings stay English.
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
  matches: $('matches'),
  matchesPanel: $('matchesPanel'),
  matchesEmpty: $('matchesEmpty'),
  matchCount: $('matchCount'),
  matchFilter: $('matchFilter'),
  matchLink: $('matchLink'),
  historyPanel: $('historyPanel'),
  historyTitle: $('history-title'),
  historySummary: $('historySummary'),
  historyBody: $('historyBody'),
  historyClose: $('historyClose'),
};

const state = {
  players: [],
  selected: new Set(),
  config: null,
  result: null,   // live, manually editable copy of the last balance response
  generated: null, // pristine copy, used by "Възстанови генерираното"
  manual: false, // true once the user has moved a player by hand
  weightsUsed: null, // exact raw weights sent with the last request
  weightsStale: false, // true when the weight inputs changed after a solve
  matchId: null, // the fixture the current draw was written to
  matchStatus: null,
  redrawTimer: null, // coalesces rapid manual moves into one re-draw
  matches: [], // saved fixtures, newest first
  openMatchId: null, // which match card shows its result form
  draft: null, // unsaved edits to the open match
  savingMatch: false,
  historyFor: null, // player id whose history is displayed
  redrawNote: null, // last outcome of pushing the current teams into the pending match
  editingId: null,
  busy: false,
  messageKind: null, // 'error' | 'ok' | null, so re-renders don't eat the last status
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

// Bulgarian counts: one играч, but two играча, twelve играча.
const playersWord = (n) => (n === 1 ? 'играч' : 'играча');

// The team letters follow the UI (А/Б), not the API payload (A/B).
const teamLabel = (side) => (side === 'A' ? 'А' : 'Б');

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
  state.messageKind = text ? kind : null;
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
    throw new Error(`изненадващ отговор от ${path}`);
  }
  if (!res.ok) {
    // The API answers with English messages; they are shown as they arrive.
    throw new Error(payload?.error || `заявката не успя (${res.status})`);
  }
  return payload;
}

/* ============================== player list ============================== */

async function loadConfig() {
  state.config = await api('/api/config');
  const sizes = state.config.teamSizes ?? [];
  els.teamSize.innerHTML = sizes.map((n) => `<option value="${n}">${n} срещу ${n}</option>`).join('');
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
                 aria-label="Избери ${esc(p.name)}">
        </td>
        <td data-label="Име">
          <span class="pname">${esc(p.name)}${isKeeper(p) ? '<span class="gk-badge" title="Може да пази вратата">🧤</span>' : ''}</span>
        </td>
        <td data-label="Атака">${fr(p.attack)}</td>
        <td data-label="Защита">${fr(p.defense)}</td>
        <td data-label="Голкипер">${fr(p.goalkeeping)}</td>
        <td data-label="Общо" class="ovr">${fr(p.overall)}</td>
        <td data-label="Мачове">${historyCell(p.id)}</td>
        <td class="col-actions">
          <span class="row-actions">
            <button type="button" data-role="edit" data-id="${p.id}">Редактирай</button>
            <button type="button" data-role="delete" data-id="${p.id}">Изтрий</button>
          </span>
        </td>
      </tr>`)
    .join('');

  els.playersBody.innerHTML = rows;
  els.playersEmpty.hidden = state.players.length > 0;
  els.playerCount.textContent = `${state.players.length} ${playersWord(state.players.length)}`;
  updateSelection();
}

// A player's row carries how often they have turned out and their average mark.
// Clicking it loads the full history from the server.
function historyCell(playerID) {
  const s = playerMatchStats(playerID);
  if (!s.matches) return '<span class="muted">—</span>';
  const avg = s.rated ? ` · ${f1(s.average)}` : '';
  return `<button type="button" class="link" data-role="history" data-id="${playerID}"
            title="История на играча">${s.matches} · ⭐${s.awards}${avg}</button>`;
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
  els.neededInfo.textContent = need > 0 ? `нужни са точно ${need} ${playersWord(need)}` : '';
  els.selectionInfo.textContent = `${have} избрани · нужни са ${need} за ${teamSize()} срещу ${teamSize()}`;
  els.selectionInfo.classList.toggle('bad', have !== need);
  els.selectionInfo.classList.toggle('ok', have === need);
  els.generate.disabled = have !== need || state.busy;

  if (have !== need && have > 0) {
    showMessage(countMessage(need, have), 'error');
  } else if (state.messageKind === 'error') {
    // Only clear a leftover validation complaint; a success line from the last
    // run survives re-rendering the selection state.
    showMessage('');
  }
}

function countMessage(need, have) {
  return `Избери точно ${need} ${playersWord(need)} за ${teamSize()} срещу ${teamSize()} — в момента ${have}.`;
}

/* ================================ balance ================================ */

async function generateTeams() {
  const need = teamSize() * 2;
  const players = selectedPlayers();
  if (players.length !== need) {
    showMessage(countMessage(need, players.length));
    return;
  }
  const raw = readWeightsRaw();
  if (raw.attack + raw.defense + raw.goalkeeping + raw.overall <= 0) {
    showMessage('Задай тежест поне на един показател.');
    return;
  }

  state.busy = true;
  els.generate.disabled = true;
  els.generate.textContent = 'Изчислявам…';
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
    state.redrawNote = null;
    renderResult();
    await recordMatch();
    showMessage(
      `${result.teamSize} срещу ${result.teamSize} — готово чрез ${methodLabel(result.method)} ` +
      `(проверени са ${result.explored.toLocaleString('bg-BG')} варианта).`,
      'ok',
    );
    els.results.hidden = false;
    els.results.scrollIntoView({ behavior: 'smooth', block: 'start' });
  } catch (err) {
    showMessage(err.message);
  } finally {
    state.busy = false;
    els.generate.textContent = 'Създай отборите';
    updateSelection();
  }
}

function methodLabel(method) {
  return method === 'exhaustive' ? 'пълен преглед' : 'локално търсене';
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
  els.difference.textContent = `разлика ${f2(Math.abs(a.weightedScore - b.weightedScore))}`;
  els.objective.textContent = `целева стойност ${f2(objectiveFor(a, b))}`;
  els.method.textContent = state.manual
    ? 'променено ръчно'
    : `${methodLabel(r.method)} · ${r.explored.toLocaleString('bg-BG')} варианта`;
  els.resetTeams.hidden = !state.manual;

  const notes = [];
  if (state.manual) notes.push('Отборите са разпределени ръчно');
  if (state.weightsStale) notes.push('тежестите са променени след изчисляването');
  if (state.redrawNote) {
    notes.push(state.redrawNote.kind === 'updated'
      ? `мач №${state.redrawNote.id} е обновлен с тези отбори`
      : `мач №${state.redrawNote.id} не може да се обнови: ${state.redrawNote.text}`);
  }
  els.tweakNote.hidden = notes.length === 0;
  els.tweakNote.textContent = notes.length
    ? `${notes.join(' · ')}. Статистиката е преизчислена веднага — натисни „Създай отборите“, за да подновиш решението.`
    : '';

  renderCompare(a, b);
}

function teamCard(side, players, stats) {
  const cls = side === 'A' ? 'team-a' : 'team-b';
  const dot = side === 'A' ? '🔵' : '🟠';
  const target = side === 'A' ? 'Б' : 'А';
  const rows = players
    .map(
      (p) => `
        <li>
          <span class="who"><b>${esc(p.name)}</b>${isKeeper(p) ? '<span class="gk-badge" title="Може да пази вратата">🧤</span>' : ''}
            <span class="sub">Ат ${fr(p.attack)} · За ${fr(p.defense)} · ГК ${fr(p.goalkeeping)} · Об ${fr(p.overall)}</span></span>
          <button type="button" data-role="move" data-side="${side}" data-id="${p.id}">Премести в отбор ${target}</button>
        </li>`
    )
    .join('');

  return `
    <article class="team ${cls}">
      <header><span>${dot} Отбор ${teamLabel(side)}</span><span class="pill">${players.length} ${playersWord(players.length)}${stats.goalkeepers ? ` · ${stats.goalkeepers} 🧤` : ''}</span></header>
      <ol>${rows}</ol>
      <div class="stats">
        <div><span class="k">Атака</span><span class="v">${f1(stats.attack)}</span></div>
        <div><span class="k">Защита</span><span class="v">${f1(stats.defense)}</span></div>
        <div><span class="k">Голкипер</span><span class="v">${f1(stats.goalkeeping)}</span></div>
        <div><span class="k">Общо</span><span class="v">${f1(stats.overall)}</span></div>
        <div class="weighted"><span class="k">Претеглен резултат</span><span class="v">${f2(stats.weightedScore)}</span></div>
      </div>
    </article>`;
}

function renderCompare(a, b) {
  const lines = [
    ['Атака', a.attack, b.attack, 1],
    ['Защита', a.defense, b.defense, 1],
    ['Голкипер', a.goalkeeping, b.goalkeeping, 1],
    ['Общо', a.overall, b.overall, 1],
    ['Голкипери (🧤)', a.goalkeepers, b.goalkeepers, 0],
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
    <tr class="total"><td>Претеглен резултат</td><td class="a">${f2(a.weightedScore)}</td><td class="b">${f2(b.weightedScore)}</td>
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
  schedulePendingRedraw();
}

function resetTeams() {
  if (!state.generated) return;
  state.result = structuredClone(state.generated);
  state.manual = false;
  renderResult();
  schedulePendingRedraw();
  showMessage('Генерираните отбори са възстановени.', 'ok');
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
  els.formTitle.textContent = player ? `Редакция на ${player.name}` : 'Нов играч';
  els.formSubmit.textContent = player ? 'Запази промените' : 'Добави играча';
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
  if (!name) return showFormError('Името е задължително.');

  const values = {};
  const keys = ['attack', 'defense', 'goalkeeping', 'overall'];
  for (let i = 0; i < keys.length; i += 1) {
    const raw = els.ratings[i].value;
    const v = parseFloat(raw);
    if (raw === '' || !Number.isFinite(v)) return showFormError(`Попълни „${labelOf(keys[i])}“.`);
    if (v < min || v > max) return showFormError(`„${labelOf(keys[i])}“ трябва да е между ${min} и ${max} (въведено: ${v}).`);
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
    showMessage(`Промените за ${saved.name} са запазени.`, 'ok');
  } catch (err) {
    showFormError(err.message);
  }
}

function labelOf(key) {
  return { attack: 'Атака', defense: 'Защита', goalkeeping: 'Голкипер', overall: 'Общо' }[key];
}

async function deletePlayer(id) {
  const player = state.players.find((p) => p.id === id);
  if (!player) return;
  if (!window.confirm(`Изтриване на ${player.name}?`)) return;
  try {
    await api(`/api/players/${id}`, { method: 'DELETE' });
    state.selected.delete(id);
    if (state.editingId === id) fillForm(null);
    await loadPlayers();
    syncResultWithRoster();
    showMessage(`Играчът ${player.name} е изтрит.`, 'ok');
  } catch (err) {
    showMessage(err.message);
  }
}

/* ================================ matches ================================ */

const idsOf = (players) => players.map((p) => p.id);

const STATUS_LABEL = { upcoming: 'предстоящ', played: 'изигран', rated: 'оценен' };
const statusLabel = (s) => STATUS_LABEL[s] || s;

function fmtDate(iso) {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '—';
  return d.toLocaleString('bg-BG', {
    day: '2-digit', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit',
  });
}

// datetime-local speaks local time; the API speaks RFC 3339 UTC.
function toLocalInput(iso) {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const p = (n) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
}

function fromLocalInput(value) {
  if (!value) return null;
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? null : d.toISOString();
}

async function loadMatches() {
  state.matches = (await api('/api/matches')) ?? [];
  renderMatches();
}

// playerMatchStats aggregates the saved fixtures for the roster table. Wins and
// losses are computed by the server for the detailed history; the table only
// needs the count and the average mark.
function playerMatchStats(playerID) {
  let matches = 0;
  let rated = 0;
  let sum = 0;
  let awards = 0;
  for (const m of state.matches) {
    if (m.status === 'upcoming') continue;
    const on = m.teamA.players.some((p) => p.id === playerID) || m.teamB.players.some((p) => p.id === playerID);
    if (!on) continue;
    matches += 1;
    const rating = (m.ratings || []).find((r) => r.playerId === playerID);
    if (rating) {
      rated += 1;
      sum += rating.rating;
    }
    if (m.manOfTheMatch === playerID) awards += 1;
  }
  return { matches, rated, average: rated ? sum / rated : 0, awards };
}

function renderMatches() {
  const list = state.matches;
  els.matchCount.textContent = `${list.length} ${list.length === 1 ? 'мач' : 'мача'}`;
  const pending = list.filter((m) => m.status === 'upcoming').length;
  els.matchFilter.textContent = pending ? `${pending} чакат резултат` : '';
  els.matchFilter.hidden = !pending;
  els.matchesEmpty.hidden = list.length > 0;
  els.matches.innerHTML = list.map(matchCard).join('');
}

function matchScore(m) {
  if (m.teamA.goals === null || m.teamA.goals === undefined || m.teamB.goals === null || m.teamB.goals === undefined) {
    return '— : —';
  }
  return `${m.teamA.goals} : ${m.teamB.goals}`;
}

function matchRoster(side) {
  return side.players.map((p) => esc(p.name)).join(', ');
}

function matchCard(m) {
  const open = state.openMatchId === m.id;
  const keeper = m.manOfTheMatch
    ? [...m.teamA.players, ...m.teamB.players].find((p) => p.id === m.manOfTheMatch)
    : null;
  return `
    <article class="match${m.status === 'upcoming' ? ' is-pending' : ''}" data-match="${m.id}">
      <header>
        <span class="match-when">${fmtDate(m.date)}</span>
        <span class="badge badge-${m.status}">${statusLabel(m.status)}</span>
        <span class="pill">${m.teamSize} срещу ${m.teamSize}</span>
        <span class="pill">баланс ${f1(m.balance)}%</span>
        ${keeper ? `<span class="pill">⭐ ${esc(keeper.name)}</span>` : ''}
        <span class="pill">№${m.id}</span>
      </header>
      <div class="match-score">
        <span class="side side-a">🔵 ${matchRoster(m.teamA)}</span>
        <strong>${matchScore(m)}</strong>
        <span class="side side-b">${matchRoster(m.teamB)} 🟠</span>
      </div>
      <div class="match-actions">
        <button type="button" data-role="match-toggle">${open ? 'Скрий формуляра' : 'Резултат и оценки'}</button>
        ${m.status === 'upcoming' ? '' : '<button type="button" data-role="match-clear">Изчисти резултата</button>'}
        <button type="button" data-role="match-delete">Изтрий мача</button>
      </div>
      ${open ? matchForm(m) : ''}
    </article>`;
}

function matchForm(m) {
  const d = state.draft ?? {};
  const everyone = [
    ...m.teamA.players.map((p) => ({ ...p, team: 'А' })),
    ...m.teamB.players.map((p) => ({ ...p, team: 'Б' })),
  ];
  const ratingRows = everyone
    .map((p) => {
      const value = d.ratings?.[p.id] ?? '';
      return `
        <label class="rating-row">
          <span>${p.team === 'А' ? '🔵' : '🟠'} ${esc(p.name)}</span>
          <input type="number" min="1" max="10" step="0.1" data-rating="${p.id}" value="${value}" placeholder="—">
        </label>`;
    })
    .join('');

  const options = ['<option value="">—</option>']
    .concat(everyone.map((p) => `<option value="${p.id}"${String(d.motm ?? '') === String(p.id) ? ' selected' : ''}>${p.team} · ${esc(p.name)}</option>`));

  return `
    <form class="match-form" data-role="match-form">
      <div class="match-form-grid">
        <label>Дата и час
          <input type="datetime-local" data-field="date" value="${d.date ?? ''}">
        </label>
        <label class="goals">Голове 🔵
          <input type="number" min="0" step="1" data-field="goalsA" value="${d.goalsA ?? ''}" placeholder="—">
        </label>
        <label class="goals">Голове 🟠
          <input type="number" min="0" step="1" data-field="goalsB" value="${d.goalsB ?? ''}" placeholder="—">
        </label>
        <label>Играч на мача
          <select data-field="motm">${options.join('')}</select>
        </label>
      </div>
      <p class="help">Оценките са само за историята — те не променят показателите на играча.</p>
      <div class="ratings-grid">${ratingRows}</div>
      <p class="status error" data-role="match-error" hidden></p>
      <div class="form-actions">
        <button type="submit" class="primary">Запази мача</button>
        <button type="button" data-role="match-toggle">Затвори</button>
      </div>
    </form>`;
}

// draftFor mirrors a stored match into the form model.
function draftFor(m) {
  return {
    date: toLocalInput(m.date),
    goalsA: m.teamA.goals ?? '',
    goalsB: m.teamB.goals ?? '',
    motm: m.manOfTheMatch ?? '',
    ratings: Object.fromEntries((m.ratings || []).map((r) => [r.playerId, r.rating])),
  };
}

// resetDraft re-reads an open form from storage, so a change that did not come
// from the form itself (a cleared result) cannot be re-submitted by accident.
function resetDraft(m) {
  if (state.openMatchId !== m.id || !state.draft) return;
  state.draft = draftFor(m);
  renderMatches();
}

function openMatchForm(id) {
  const m = state.matches.find((x) => x.id === id);
  if (!m) return;
  if (state.openMatchId === id) {
    state.openMatchId = null;
    state.draft = null;
    renderMatches();
    return;
  }
  state.openMatchId = id;
  state.draft = draftFor(m);
  renderMatches();
}

// buildMatchPayload turns the draft into a PUT body, or returns an error message
// when the input cannot be saved. Both goal numbers must arrive together, and
// every rating must sit on the 1..10 band.
function buildMatchPayload(m) {
  const d = state.draft;
  const payload = {};

  const date = fromLocalInput(d.date);
  if (!date) return { error: 'Изберете валидна дата и час.' };
  payload.date = date;

  const hasA = String(d.goalsA ?? '') !== '';
  const hasB = String(d.goalsB ?? '') !== '';
  if (hasA !== hasB) return { error: 'Въведете голова разлика и за двата отбора.' };
  if (hasA && hasB) {
    const goalsA = Math.trunc(num(d.goalsA));
    const goalsB = Math.trunc(num(d.goalsB));
    if (goalsA < 0 || goalsB < 0) return { error: 'Головете не могат да са отрицателни.' };
    payload.goalsA = goalsA;
    payload.goalsB = goalsB;
  }

  const ratings = [];
  for (const [playerId, raw] of Object.entries(d.ratings || {})) {
    if (String(raw ?? '') === '') continue;
    const value = parseFloat(raw);
    if (!Number.isFinite(value) || value < 1 || value > 10) {
      const player = [...m.teamA.players, ...m.teamB.players].find((p) => p.id === Number(playerId));
      return { error: `Оценката за ${player ? player.name : 'играча'} трябва да е между 1 и 10.` };
    }
    ratings.push({ playerId: Number(playerId), rating: Math.round(value * 10) / 10 });
  }
  payload.ratings = ratings;

  if (d.motm === '' || d.motm == null) {
    payload.clearManOfTheMatch = true;
  } else {
    payload.manOfTheMatch = Number(d.motm);
  }
  return { payload };
}

function matchFormError(text) {
  const box = els.matches.querySelector('[data-role="match-error"]');
  if (!box) {
    showMessage(text);
    return;
  }
  box.hidden = !text;
  box.textContent = text || '';
}

async function saveMatch(id) {
  if (state.savingMatch) return;
  const m = state.matches.find((x) => x.id === id);
  if (!m || !state.draft) return;

  const { payload, error } = buildMatchPayload(m);
  if (error) return matchFormError(error);
  matchFormError('');

  state.savingMatch = true;
  try {
    const updated = await api(`/api/matches/${id}`, { method: 'PUT', body: JSON.stringify(payload) });
    replaceMatch(updated);
    state.openMatchId = null;
    state.draft = null;
    renderMatches();
    renderPlayers();
    if (state.historyFor != null) await openHistory(state.historyFor, true);
    const rated = updated.status === 'rated' ? ' и оценките' : '';
    showMessage(`Мач №${updated.id} е записан с резултат ${matchScore(updated)}${rated}.`, 'ok');
  } catch (err) {
    matchFormError(err.message);
  } finally {
    state.savingMatch = false;
  }
}

async function clearMatchResult(id) {
  const m = state.matches.find((x) => x.id === id);
  if (!m) return;
  if (!window.confirm(`Изчистване на резултата от мач №${id}? Оценките също се изтриват.`)) return;
  try {
    const updated = await api(`/api/matches/${id}`, { method: 'PUT', body: JSON.stringify({ clearResult: true }) });
    replaceMatch(updated);
    resetDraft(updated);
    renderMatches();
    renderPlayers();
    showMessage(`Резултатът от мач №${id} е изчистен.`, 'ok');
  } catch (err) {
    showMessage(err.message);
  }
}

async function deleteMatch(id) {
  const m = state.matches.find((x) => x.id === id);
  if (!m) return;
  if (!window.confirm(`Изтриване на мач №${id} завинаги?`)) return;
  try {
    await api(`/api/matches/${id}`, { method: 'DELETE' });
    state.matches = state.matches.filter((x) => x.id !== id);
    if (state.matchId === id) {
      state.matchId = null;
      state.matchStatus = null;
      els.matchLink.textContent = '';
    }
    if (state.openMatchId === id) {
      state.openMatchId = null;
      state.draft = null;
    }
    renderMatches();
    renderPlayers();
    showMessage(`Мач №${id} е изтрит.`, 'ok');
  } catch (err) {
    showMessage(err.message);
  }
}

function replaceMatch(updated) {
  const i = state.matches.findIndex((x) => x.id === updated.id);
  if (i >= 0) state.matches[i] = updated;
  else state.matches.unshift(updated);
  // Keep the newest-first order the server returns, so the list does not jump.
  state.matches.sort((a, b) => new Date(b.date) - new Date(a.date) || b.id - a.id);
  if (state.matchId === updated.id) state.matchStatus = updated.status;
}

/* ============================ recording the draw ========================== */

// Every generated split becomes a pending fixture, so "who did we play" and the
// result afterwards are recorded without a second step.
async function recordMatch() {
  const r = state.result;
  if (!r) return;
  const body = JSON.stringify({
    teamSize: r.teamSize,
    teamA: idsOf(r.teamA.players),
    teamB: idsOf(r.teamB.players),
    weights: state.weightsUsed ?? readWeightsRaw(),
  });
  try {
    const created = await api('/api/matches', { method: 'POST', body });
    replaceMatch(created);
    state.matchId = created.id;
    state.matchStatus = created.status;
    els.matchLink.textContent = `Мач №${created.id} · ${statusLabel(created.status)}`;
    renderMatches();
    renderPlayers();
  } catch (err) {
    // The teams are still usable; only the history entry failed.
    showMessage(`Отборите са готови, но мачът не се записа: ${err.message}`);
  }
}

// Manual moves edit a pending fixture in place, so the saved match always
// matches what the user is about to play. Coalesced: dragging five players
// across is one write, not five.
function schedulePendingRedraw() {
  if (!state.matchId || state.matchStatus !== 'upcoming' || !state.result) return;
  clearTimeout(state.redrawTimer);
  const a = state.result.teamA.players.length;
  const b = state.result.teamB.players.length;
  if (a !== b) {
    // A one-sided move leaves 5 v 7, which is not a fixture: say so instead of
    // quietly keeping the older, even draw on file.
    state.redrawNote = { kind: 'blocked', id: state.matchId, text: `отборите са с различен брой играчи (${a} срещу ${b})` };
    renderResult();
    return;
  }
  state.redrawNote = null;
  state.redrawTimer = setTimeout(syncPendingRedraw, 400);
}

async function syncPendingRedraw() {
  const r = state.result;
  if (!r || !state.matchId || state.matchStatus !== 'upcoming') return;
  if (r.teamA.players.length !== r.teamB.players.length) return;
  const body = JSON.stringify({
    teamSize: r.teamSize,
    teamA: idsOf(r.teamA.players),
    teamB: idsOf(r.teamB.players),
  });
  try {
    const updated = await api(`/api/matches/${state.matchId}`, { method: 'PUT', body });
    replaceMatch(updated);
    renderMatches();
    els.matchLink.textContent = `Мач №${updated.id} · ${statusLabel(updated.status)}`;
    state.redrawNote = { kind: 'updated', id: updated.id };
    renderResult();
  } catch (err) {
    state.redrawNote = { kind: 'blocked', id: state.matchId, text: err.message };
    renderResult();
  }
}

/* ============================== player history ============================ */

async function openHistory(playerID, keepOpen) {
  const player = state.players.find((p) => p.id === playerID);
  if (!player) {
    closeHistory();
    return;
  }
  try {
    const history = await api(`/api/players/${playerID}/history`);
    state.historyFor = playerID;
    els.historyPanel.hidden = false;
    els.historyTitle.textContent = `История · ${player.name}`;
    const s = history.summary;
    els.historySummary.textContent = s.matches
      ? `${s.matches} мача · средно ${s.averageRating || '—'} · ${s.wins}П ${s.draws}Р ${s.losses}З · ⭐ ${s.manOfTheMatch}`
      : 'няма изиграни мачове';
    els.historyBody.innerHTML = history.appearances.length
      ? `<table class="history"><thead><tr>
           <th scope="col">Дата</th><th scope="col">Отбор</th><th scope="col">Резултат</th>
           <th scope="col">Изход</th><th scope="col">Оценка</th><th scope="col">Баланс</th>
         </tr></thead><tbody>${history.appearances.map(historyRow).join('')}</tbody></table>`
      : '<p class="empty">Все още няма изигран мач с този играч.</p>';
    if (!keepOpen) els.historyPanel.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
  } catch (err) {
    showMessage(err.message);
  }
}

function historyRow(a) {
  const outcome = { win: 'победа', draw: 'равен', loss: 'загуба' }[a.outcome] || '—';
  const score = a.goalsFor === undefined || a.goalsFor === null ? '—' : `${a.goalsFor}:${a.goalsAgainst}`;
  return `<tr>
      <td>${fmtDate(a.date)}</td>
      <td class="${a.team === 'A' ? 'a' : 'b'}">${a.team === 'A' ? '🔵 А' : '🟠 Б'}</td>
      <td>${score}</td>
      <td class="outcome-${a.outcome || 'none'}">${outcome}${a.manOfTheMatch ? ' ⭐' : ''}</td>
      <td>${a.rating ? fr(a.rating) : '—'}</td>
      <td>${f1(a.balance)}%</td>
    </tr>`;
}

function closeHistory() {
  state.historyFor = null;
  els.historyPanel.hidden = true;
  els.historyBody.innerHTML = '';
}

/* ================================ wiring ================================ */

function updateWeightsLabel() {
  const raw = readWeightsRaw();
  const sum = raw.attack + raw.defense + raw.goalkeeping + raw.overall;
  if (sum <= 0) {
    els.weightsNormalised.textContent = 'всички тежести са нула — ще се считат за равни (по 25%)';
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
  if (btn.dataset.role === 'history') openHistory(id);
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

els.matches.addEventListener('click', (event) => {
  const btn = event.target.closest('button[data-role]');
  if (!btn) return;
  const card = btn.closest('[data-match]');
  if (!card) return;
  const id = Number(card.dataset.match);
  if (btn.dataset.role === 'match-toggle') openMatchForm(id);
  if (btn.dataset.role === 'match-clear') clearMatchResult(id);
  if (btn.dataset.role === 'match-delete') deleteMatch(id);
});

// The draft lives in state, not in the DOM, so re-rendering the list never
// throws away a half-typed score.
els.matches.addEventListener('input', (event) => {
  const field = event.target.dataset;
  if (!state.draft) return;
  if (field.field) state.draft[field.field] = event.target.value;
  else if (field.rating) state.draft.ratings[Number(field.rating)] = event.target.value;
});

els.matches.addEventListener('submit', (event) => {
  const form = event.target.closest('[data-role="match-form"]');
  if (!form) return;
  event.preventDefault();
  const card = form.closest('[data-match]');
  if (card) saveMatch(Number(card.dataset.match));
});

els.historyClose.addEventListener('click', closeHistory);

async function boot() {
  try {
    await loadConfig();
    await loadPlayers();
    await loadMatches();
    updateWeightsLabel();
    updateSelection();
  } catch (err) {
    showMessage(`Няма връзка със сървъра: ${err.message}`);
  }
}

boot();
