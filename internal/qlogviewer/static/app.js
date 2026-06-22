(function(){
'use strict';

const timeline = document.getElementById('timeline');
const logEntries = document.getElementById('log-entries');
const pathList = document.getElementById('path-list');

const statusEl = document.getElementById('status');
const elSrtt = document.getElementById('metric-srtt');
const elCwnd = document.getElementById('metric-cwnd');
const elBytes = document.getElementById('metric-bytes');
const elPkts = document.getElementById('metric-pkts');
const elLocal = document.getElementById('local-addr');
const elRemote = document.getElementById('remote-addr');
const elSrcCid = document.getElementById('src-cid');
const elDstCid = document.getElementById('dst-cid');
const elTimeRange = document.getElementById('time-range');

const LANE_HEIGHT = 36;
const LANE_PAD = 32;
const MIN_PKT_W = 3;
const PX_PER_MS = 2;
const MAX_VIS_MS = 15000;
const MAX_LOG = 200;

let paused = false;
let lanes = {};       // pathId -> { div, label, rssiDots:[] }
let metrics = {};     // pathId -> MetricsInfo
let firstEventTime = null;
let lastEventTime = null;
let animFrame = null;

// ---- WebSocket ----
const ws = new WebSocket(`ws://${location.host}/ws`);
ws.onopen = () => { statusEl.className='online'; statusEl.textContent='online'; };
ws.onclose = () => { statusEl.className='offline'; statusEl.textContent='offline'; };
ws.onerror = () => { statusEl.className='offline'; statusEl.textContent='error'; };
ws.onmessage = (msg) => {
  try {
    const ev = JSON.parse(msg.data);
    if (ev.type === 'hello') return;
    handleEvent(ev);
  } catch(e) { /* ignore parse errors */ }
};

// ---- Event handling ----
function handleEvent(ev) {
  if (paused) return;
  const t = ev.t;
  if (firstEventTime === null) firstEventTime = t;
  lastEventTime = t;

  const pathId = ev.path !== undefined ? ev.path : 0;
  ensureLane(pathId);

  switch (ev.type) {
    case 'conn_started': onConnStarted(ev.payload); break;
    case 'conn_closed': onConnClosed(ev.payload); break;
    case 'packet_sent': onPacket(pathId, t, 'sent', ev.payload); break;
    case 'packet_recv': onPacket(pathId, t, 'recv', ev.payload); break;
    case 'metrics': onMetrics(pathId, ev.payload); break;
    case 'packet_acked': onAcked(pathId, t, ev.payload); break;
    case 'packet_lost': onLost(pathId, t, ev.payload); break;
    case 'path_rssi': onRSSI(pathId, t, ev.payload); break;
    case 'path_state': onPathState(pathId, ev.payload); break;
    case 'debug': appendLog('debug', ev.payload.name + ': ' + ev.payload.msg); break;
  }

  scheduleRender();
}

function ensureLane(id) {
  if (lanes[id]) return;
  const div = document.createElement('div');
  div.className = 'lane';
  div.style.top = (id * LANE_HEIGHT) + 'px';
  div.style.height = LANE_HEIGHT + 'px';
  const label = document.createElement('div');
  label.className = 'lane-label';
  label.textContent = 'Path ' + id;
  div.appendChild(label);
  timeline.appendChild(div);
  lanes[id] = { div, label, dots:[] };
  updatePathList();
}

function onConnStarted(p) {
  elLocal.textContent = p.local || '-';
  elRemote.textContent = p.remote || '-';
  elSrcCid.textContent = p.src_cid || '-';
  elDstCid.textContent = p.dst_cid || '-';
  appendLog('info', 'Connection started: ' + (p.remote || '?'));
}

function onConnClosed(p) {
  appendLog('info', 'Connection closed' + (p.error ? ': ' + p.error : ''));
}

function onPacket(pathId, t, dir, p) {
  appendLog('packet-' + dir, dir + ' pn=' + p.pn + ' size=' + p.size + ' ' + (p.enc_level||''));
  addPacketElement(pathId, t, dir, p);
}

function addPacketElement(pathId, t, dir, p) {
  const lane = lanes[pathId];
  if (!lane) return;
  const el = document.createElement('div');
  el.className = 'pkt ' + dir;
  const w = Math.max(MIN_PKT_W, Math.min(p.size / 20, 30));
  el.style.width = w + 'px';
  el.title = dir + ' pn=' + p.pn + ' size=' + p.size + ' ' + (p.enc_level||'');
  // position set during render
  el.dataset.t = t;
  lane.div.appendChild(el);
}

function onMetrics(pathId, m) {
  metrics[pathId] = m;
  if (pathId === 0) {
    elSrtt.textContent = (m.srtt/1000).toFixed(1) + ' ms';
    elCwnd.textContent = (m.cwnd/1024).toFixed(1) + ' KB';
    elBytes.textContent = m.bytes_in_flight + ' B';
    elPkts.textContent = m.packets_in_flight;
  }
  appendLog('metrics', 'srtt=' + (m.srtt/1000).toFixed(1) + 'ms cwnd=' + Math.round(m.cwnd/1024) + 'KB');
}

function onAcked(pathId, t, p) {
  appendLog('acked', 'acked pn=' + p.pn + ' ' + p.enc_level);
  const lane = lanes[pathId];
  if (!lane) return;
  const el = document.createElement('div');
  el.className = 'pkt acked';
  el.title = 'ACKed pn=' + p.pn;
  el.dataset.t = t;
  lane.div.appendChild(el);
}

function onLost(pathId, t, p) {
  appendLog('lost', 'LOST pn=' + p.pn + ' ' + (p.reason||''));
  const lane = lanes[pathId];
  if (!lane) return;
  const el = document.createElement('div');
  el.className = 'pkt lost';
  el.title = 'LOST pn=' + p.pn + ' reason=' + (p.reason||'');
  el.dataset.t = t;
  lane.div.appendChild(el);
}

function onRSSI(pathId, t, p) {
  const lane = lanes[pathId];
  if (!lane) return;
  if (!p || !p.has_rssi) return;
  const dot = document.createElement('div');
  dot.className = 'rssi-dot';
  dot.dataset.t = t;
  dot.dataset.rssi = p.rssi;
  lane.div.appendChild(dot);
  lane.dots.push(dot);
}

function onPathState(pathId, p) {
  appendLog('info', 'Path ' + pathId + ' status=' + (p.status||'?'));
  updatePathList();
}

function updatePathList() {
  const ids = Object.keys(lanes).sort((a,b)=>a-b);
  pathList.innerHTML = ids.map(id => {
    const m = metrics[id] || {};
    const rssi = '-';
    return `<div class="path-entry">
      <span class="path-id">Path ${id}</span>
      <span class="path-status">active</span>
      <span class="path-rssi">${rssi}</span>
    </div>`;
  }).join('');
}

function appendLog(cls, msg) {
  const line = document.createElement('div');
  line.className = 'log-line ' + cls;
  const t = new Date().toLocaleTimeString();
  line.innerHTML = '<span class="log-time">' + t + '</span> ' + escapeHtml(msg);
  logEntries.appendChild(line);
  while (logEntries.children.length > MAX_LOG) logEntries.removeChild(logEntries.firstChild);
  logEntries.scrollTop = logEntries.scrollHeight;
}

function escapeHtml(s) {
  const d = document.createElement('div');
  d.textContent = s;
  return d.innerHTML;
}

// ---- Rendering ----
function scheduleRender() {
  if (animFrame) return;
  animFrame = requestAnimationFrame(() => { animFrame = null; render(); });
}

function render() {
  if (lastEventTime === null) return;
  const now = performance.now();
  const visEnd = lastEventTime;
  const visStart = visEnd - MAX_VIS_MS;
  const range = visEnd - visStart;

  // resize timeline
  const container = timeline.parentElement;
  const lanesCount = Object.keys(lanes).length;
  const axisH = 18;
  const totalH = Math.max(lanesCount * LANE_HEIGHT + axisH, container.clientHeight);
  timeline.style.height = totalH + 'px';

  // update lanes
  for (const [id, lane] of Object.entries(lanes)) {
    const children = lane.div.children;
    for (let i = 0; i < children.length; i++) {
      const el = children[i];
      const t = parseFloat(el.dataset.t);
      if (t < visStart || t > visEnd) {
        el.style.display = 'none';
        continue;
      }
      el.style.display = '';
      const x = LANE_PAD + ((t - visStart) / range) * (timeline.clientWidth - LANE_PAD - 10);
      if (el.classList.contains('pkt')) {
        el.style.left = (x - parseFloat(el.style.width || MIN_PKT_W)/2) + 'px';
      } else if (el.classList.contains('acked')) {
        el.style.left = x + 'px';
        el.style.top = '65%';
      } else if (el.classList.contains('lost')) {
        el.style.left = x + 'px';
      } else if (el.classList.contains('rssi-dot')) {
        el.style.left = x + 'px';
        // position at bottom of lane area based on RSSI
        const rssi = parseFloat(el.dataset.rssi) || -70;
        const normalized = Math.max(0, Math.min(1, (rssi + 90) / 50)); // -90..-40 -> 0..1
        const laneH = LANE_HEIGHT - 10;
        el.style.top = (laneH * (1 - normalized)) + 'px';
      }
    }
    // Update lane label with latest RSSI
    const dots = lane.div.querySelectorAll('.rssi-dot:not([style*="display:none"])');
    if (dots.length > 0) {
      const lastDot = dots[dots.length - 1];
      lane.label.textContent = 'Path ' + id + ' RSSI:' + lastDot.dataset.rssi + ' dBm';
    } else {
      lane.label.textContent = 'Path ' + id;
    }
  }

  // update time axis
  let axis = document.getElementById('time-axis');
  if (!axis) {
    axis = document.createElement('div');
    axis.id = 'time-axis';
    timeline.appendChild(axis);
  }
  axis.innerHTML = '';
  axis.style.top = (lanesCount * LANE_HEIGHT) + 'px';
  const numTicks = 8;
  for (let i = 0; i <= numTicks; i++) {
    const tickT = visStart + (range * i / numTicks);
    const x = LANE_PAD + (i / numTicks) * (timeline.clientWidth - LANE_PAD - 10);
    const tick = document.createElement('div');
    tick.className = 'axis-tick';
    tick.style.left = x + 'px';
    let label = '';
    if (range > 10000) label = (tickT / 1000).toFixed(1) + 's';
    else if (range > 1000) label = tickT.toFixed(0) + 'ms';
    else label = tickT.toFixed(1) + 'ms';
    tick.textContent = label;
    axis.appendChild(tick);
  }

  elTimeRange.textContent = (range/1000).toFixed(1) + 's window';
}

// ---- Controls ----
document.getElementById('btn-pause').onclick = function() {
  paused = !paused;
  this.textContent = paused ? 'Resume' : 'Pause';
};
document.getElementById('btn-clear').onclick = function() {
  for (const id in lanes) {
    lanes[id].div.remove();
  }
  lanes = {};
  metrics = {};
  firstEventTime = null;
  lastEventTime = null;
  logEntries.innerHTML = '';
  pathList.innerHTML = '';
  // reset metrics display
  elSrtt.textContent = '-';
  elCwnd.textContent = '-';
  elBytes.textContent = '-';
  elPkts.textContent = '-';
  elLocal.textContent = '-';
  elRemote.textContent = '-';
  elSrcCid.textContent = '-';
  elDstCid.textContent = '-';
};

// ---- Resize ----
window.addEventListener('resize', () => scheduleRender());

})();
