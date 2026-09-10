let SNAP = null;
let EDIT = null;
let DIAG = null;
let EVENT_DATA = null;
let EVENT_EXPANDED = new Set();

const $ = (id) => document.getElementById(id);

async function api(q) {
  const u = 'api?' + new URLSearchParams(q);
  const r = await fetch(u, { cache: 'no-store' });
  const j = await r.json();
  if (j && j.ok === false) throw new Error(j.error || '请求失败');
  return j;
}

function esc(s) {
  return String(s ?? '').replace(/[&<>"']/g, (c) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'
  }[c]));
}

async function load() {
  SNAP = await api({ action: 'snapshot' });
  render();
  await loadEvents();
}

function showTab(x) {
  for (const n of ['policy', 'obs', 'events']) {
    $(n + 'Pane').style.display = n === x ? 'block' : 'none';
    $('tab' + n[0].toUpperCase() + n.slice(1)).classList.toggle('active', n === x);
  }
  if (x === 'events') loadEvents();
}

function render() {
  $('versionBadge').textContent = 'v' + (SNAP.version || 'dev');
  $('env').innerHTML = '<div class="row">' +
    '<div><b>' + SNAP.downstream_keys.length + '</b><div class="muted">下游 API Key</div></div>' +
    '<div><b>' + SNAP.resources.length + '</b><div class="muted">上游资源</div></div>' +
    '<div><b>' + SNAP.policies.length + '</b><div class="muted">Policy</div></div>' +
    '<div class="grow"></div><div class="muted mono">' + esc(SNAP.config_path || '未找到 config.yaml') + '</div></div>' +
    (SNAP.config_error ? '<div class="note" style="margin-top:12px">' + esc(SNAP.config_error) + '</div>' : '');
  renderPolicies();
  renderObs();
}

function renderPolicies() {
  let out = '';
  for (const p of SNAP.policies) {
    out += '<div class="policy"><div class="row"><div class="grow"><h3 style="margin:0">' +
      esc(p.name) + ' <span class="' + (p.enabled ? 'green' : 'muted') + '">' +
      (p.enabled ? '● 启用' : '● 停用') + '</span></h3><div class="muted">Key ' +
      esc(p.key_hint) + ' · ' + p.rules.length + ' 条模型规则</div></div>' +
      '<button onclick="diagnose(\'' + esc(p.key_fingerprint) + '\')">测试策略</button>' +
      '<button onclick="openPolicy(\'' + esc(p.key_fingerprint) + '\')">编辑</button>' +
      '<button onclick="delPolicy(\'' + esc(p.key_fingerprint) + '\')">删除</button></div>';
    for (const r of p.rules) {
      out += '<div class="rule"><div class="row"><b>' + esc(r.name) + '</b><span class="mono muted">' +
        esc((r.models || ['*']).join(', ')) + '</span><span class="badge">' + esc(r.strategy) +
        '</span></div><div class="chain">';
      const enabled = (r.candidates || []).filter((c) => c.enabled !== false);
      enabled.forEach((c, i) => {
        out += '<div class="pill"><b>' + esc(c.name) + '</b><div class="muted small">' +
          candidateMetrics(r, c) + '</div></div>' + (i < enabled.length - 1 ? '→' : '');
      });
      if (r.strategy === 'cpa-default') out += '<div class="pill">CPA 默认路由</div>';
      out += '</div></div>';
    }
    out += '</div>';
  }
  if (!out) out = '<div class="card muted">还没有 Policy。每个下游 API Key 最多创建一个。</div>';
  $('policies').innerHTML = out;
}

function availableKeys(editFp) {
  return SNAP.downstream_keys.filter((k) => k.fingerprint === editFp || !SNAP.policies.some((p) => p.key_fingerprint === k.fingerprint));
}

function openPolicy(fp) {
  EDIT = fp ? JSON.parse(JSON.stringify(SNAP.policies.find((x) => x.key_fingerprint === fp))) : {
    name: '', key_fingerprint: '', key_hint: '', enabled: true, rules: []
  };
  $('policyTitle').textContent = fp ? '编辑 Policy' : '新建 Policy';
  $('pName').value = EDIT.name || '';
  $('pEnabled').value = String(EDIT.enabled !== false);
  const keys = availableKeys(fp);
  $('pKey').innerHTML = keys.map((k) => '<option value="' + esc(k.fingerprint) + '" data-hint="' +
    esc(k.hint) + '">' + esc(k.hint) + '</option>').join('');
  if (EDIT.key_fingerprint) $('pKey').value = EDIT.key_fingerprint;
  renderRules();
  $('policyModal').classList.add('open');
}

function closePolicy() { $('policyModal').classList.remove('open'); }

function defaultRule() {
  return {
    id: '', name: '新规则', models: ['*'], strategy: 'ordered-failover', sticky_source: 'auto', sticky_header: '',
    candidates: [],
    failover: {
      network: 'next', unauthorized: 'next', timeout: 'next', conflict: 'next', rate_limit: 'next',
      server_error: 'next', other: 'stop', exhausted: 'error', max_attempts: 0
    }
  };
}

function addRule() { EDIT.rules.push(defaultRule()); renderRules(); }

function moveRule(i, d) {
  const j = i + d;
  if (j < 0 || j >= EDIT.rules.length) return;
  [EDIT.rules[i], EDIT.rules[j]] = [EDIT.rules[j], EDIT.rules[i]];
  renderRules();
}

function renderRules() {
  let x = '';
  EDIT.rules.forEach((r, i) => {
    x += '<div class="rule"><div class="grid4"><div><label>Rule 名称</label><input value="' + esc(r.name || '') +
      '" oninput="EDIT.rules[' + i + '].name=this.value"></div><div><label>匹配模型</label><input value="' +
      esc((r.models || ['*']).join(',')) + '" oninput="EDIT.rules[' + i + '].models=this.value.split(/[,;\\n]+/)"></div>' +
      '<div><label>策略</label><select onchange="EDIT.rules[' + i + '].strategy=this.value;renderRules()">' +
      strategyOptions(r.strategy) + '</select></div><div class="row" style="align-items:end">' +
      '<button onclick="moveRule(' + i + ',-1)">↑</button><button onclick="moveRule(' + i + ',1)">↓</button>' +
      '<button onclick="EDIT.rules.splice(' + i + ',1);renderRules()">删除</button></div></div>';
    if (r.strategy === 'sticky') {
      x += '<div class="grid2" style="margin-top:10px"><div><label>Sticky 来源</label><select onchange="EDIT.rules[' + i +
        '].sticky_source=this.value"><option value="auto" ' + (r.sticky_source === 'auto' ? 'selected' : '') +
        '>自动：session/header</option><option value="session" ' + (r.sticky_source === 'session' ? 'selected' : '') +
        '>Session</option><option value="header" ' + (r.sticky_source === 'header' ? 'selected' : '') +
        '>指定 Header</option></select></div><div><label>自定义 Sticky Header</label><input value="' +
        esc(r.sticky_header || '') + '" oninput="EDIT.rules[' + i + '].sticky_header=this.value" placeholder="例如 X-Session-Id"></div></div>';
    }
    if (r.strategy !== 'cpa-default') {
      x += '<div class="row" style="margin-top:10px"><b class="grow">候选</b><button onclick="addCand(' + i +
        ')">添加候选</button></div><div id="cand-' + i + '">' + renderCands(r, i) +
        '</div><details style="margin-top:10px"><summary><b>Failover 行为</b></summary>' + renderFailover(r, i) + '</details>';
    } else {
      x += '<div class="oknote" style="margin-top:10px">命中此 Rule 时插件明确不接管，交给 CPA 默认路由。</div>';
    }
    x += '</div>';
  });
  $('rules').innerHTML = x;
}

function strategyOptions(v) {
  return [
    ['ordered-failover', '有序 Failover'], ['round-robin', '轮询'],
    ['weighted-round-robin', '平滑加权轮询'], ['priority-weighted', '优先级 + 权重'],
    ['sticky', 'Sticky / 一致性哈希'], ['cpa-default', 'CPA 默认路由']
  ].map(([k, n]) => '<option value="' + k + '" ' + (v === k ? 'selected' : '') + '>' + n + '</option>').join('');
}

function strategyFields(s) {
  return {
    priority: s === 'priority-weighted' || s === 'sticky',
    weight: s === 'weighted-round-robin' || s === 'priority-weighted' || s === 'sticky'
  };
}

function candidateMetrics(r, c) {
  const f = strategyFields(r.strategy);
  const xs = [];
  if (f.priority) xs.push('P' + (c.priority || 100));
  if (f.weight) xs.push('W' + (c.weight || 1));
  return (xs.length ? xs.join(' · ') + ' · ' : '') + esc(c.provider) +
    (c.override_model ? ' · → ' + esc(c.override_model) : ' · 原模型');
}

function resOptions(sel) {
  return SNAP.resources.map((r) => '<option value="' + esc(r.id) + '" ' + (r.id === sel ? 'selected' : '') + '>' +
    esc(r.kind + ' · ' + r.provider + ' · ' + r.display_name + (r.key_hint ? ' · ' + r.key_hint : '')) + '</option>').join('');
}

function renderCands(r, ri) {
  const f = strategyFields(r.strategy);
  const cols = f.priority && f.weight ? '1.5fr 1.1fr 90px 90px 1.2fr 105px' :
    f.weight ? '1.5fr 1.1fr 90px 1.2fr 105px' : '1.5fr 1.1fr 1.2fr 105px';
  return (r.candidates || []).map((c, ci) => '<div class="candidate" style="grid-template-columns:' + cols + '">' +
    '<div><label>上游资源</label><select onchange="pickRes(' + ri + ',' + ci + ',this.value)">' + resOptions(c.resource_id) + '</select></div>' +
    '<div><label>显示名称</label><input value="' + esc(c.name || '') + '" oninput="EDIT.rules[' + ri + '].candidates[' + ci + '].name=this.value"></div>' +
    (f.priority ? '<div><label>Priority</label><input type="number" value="' + (c.priority || 100) + '" oninput="EDIT.rules[' + ri + '].candidates[' + ci + '].priority=+this.value||100"></div>' : '') +
    (f.weight ? '<div><label>Weight</label><input type="number" min="1" value="' + (c.weight || 1) + '" oninput="EDIT.rules[' + ri + '].candidates[' + ci + '].weight=+this.value||1"></div>' : '') +
    '<div><label>覆盖模型 / alias</label><input value="' + esc(c.override_model || '') + '" placeholder="留空 = 客户端原模型" oninput="EDIT.rules[' + ri + '].candidates[' + ci + '].override_model=this.value"></div>' +
    '<div><label>状态</label><select onchange="EDIT.rules[' + ri + '].candidates[' + ci + '].enabled=this.value===\'true\'">' +
    '<option value="true" ' + (c.enabled !== false ? 'selected' : '') + '>启用</option><option value="false" ' +
    (c.enabled === false ? 'selected' : '') + '>停用</option></select><div class="row" style="gap:4px;margin-top:5px">' +
    '<button onclick="moveCand(' + ri + ',' + ci + ',-1)">↑</button><button onclick="moveCand(' + ri + ',' + ci + ',1)">↓</button>' +
    '<button onclick="EDIT.rules[' + ri + '].candidates.splice(' + ci + ',1);renderRules()">×</button></div></div></div>').join('');
}

function fromRes(r) {
  return {
    id: '', name: r.display_name, resource_id: r.id, resource_kind: r.kind, provider: r.provider,
    auth_id: r.auth_id || '', auth_index: r.auth_index || '', override_model: '', enabled: true, priority: 100, weight: 1
  };
}

function addCand(ri) {
  const r = SNAP.resources[0];
  if (!r) return alert('当前没有可选上游资源');
  EDIT.rules[ri].candidates = EDIT.rules[ri].candidates || [];
  EDIT.rules[ri].candidates.push(fromRes(r));
  renderRules();
}

function pickRes(ri, ci, id) {
  const r = SNAP.resources.find((x) => x.id === id);
  if (!r) return;
  const old = EDIT.rules[ri].candidates[ci];
  const n = fromRes(r);
  n.priority = old.priority || 100;
  n.weight = old.weight || 1;
  n.override_model = old.override_model || '';
  n.enabled = old.enabled !== false;
  EDIT.rules[ri].candidates[ci] = n;
  renderRules();
}

function moveCand(ri, ci, d) {
  const a = EDIT.rules[ri].candidates;
  const j = ci + d;
  if (j < 0 || j >= a.length) return;
  [a[ci], a[j]] = [a[j], a[ci]];
  renderRules();
}

function failOptions(v) {
  return [
    ['next', '下一候选'], ['same-priority-first', '同优先级优先'], ['next-priority', '下一优先级'],
    ['stop', '停止'], ['cpa-default', '转 CPA 默认']
  ].map(([k, n]) => '<option value="' + k + '" ' + (v === k ? 'selected' : '') + '>' + n + '</option>').join('');
}

function renderFailover(r, i) {
  const f = r.failover || defaultRule().failover;
  return '<div class="grid4" style="margin-top:10px">' + [
    ['network', '网络错误'], ['unauthorized', '401/403'], ['timeout', '408'], ['conflict', '409'],
    ['rate_limit', '429'], ['server_error', '5xx'], ['other', '其他错误']
  ].map(([k, n]) => '<div><label>' + n + '</label><select onchange="EDIT.rules[' + i + '].failover.' + k + '=this.value">' +
    failOptions(f[k]) + '</select></div>').join('') +
    '<div><label>候选耗尽后</label><select onchange="EDIT.rules[' + i + '].failover.exhausted=this.value">' +
    '<option value="error" ' + (f.exhausted !== 'cpa-default' ? 'selected' : '') + '>返回错误</option>' +
    '<option value="cpa-default" ' + (f.exhausted === 'cpa-default' ? 'selected' : '') + '>转 CPA 默认</option></select></div>' +
    '<div><label>最大尝试次数</label><input type="number" min="0" value="' + (f.max_attempts || 0) +
    '" oninput="EDIT.rules[' + i + '].failover.max_attempts=+this.value||0"><div class="muted small">0 = 自动</div></div></div>';
}

async function savePolicy() {
  const sel = $('pKey');
  const hint = sel.options[sel.selectedIndex]?.dataset.hint || '';
  EDIT.name = $('pName').value;
  EDIT.key_fingerprint = sel.value;
  EDIT.key_hint = hint;
  EDIT.enabled = $('pEnabled').value === 'true';
  SNAP = await api({ action: 'save_policy', payload: JSON.stringify(EDIT) });
  closePolicy();
  render();
  await loadEvents();
}

async function delPolicy(fp) {
  if (!confirm('删除这套 API Key Policy？')) return;
  SNAP = await api({ action: 'delete_policy', fingerprint: fp });
  render();
  await loadEvents();
}

function diagnose(fp) {
  DIAG = fp;
  $('diagModel').value = '';
  $('diagModal').classList.add('open');
  runDiag();
}

function closeDiag() { $('diagModal').classList.remove('open'); }

async function runDiag() {
  const d = await api({ action: 'diagnose', fingerprint: DIAG, model: $('diagModel').value });
  if (!d.ok) {
    $('diagBody').innerHTML = '<div class="note">' + esc(d.error) + '</div>';
    return;
  }
  if (!d.matched) {
    $('diagBody').innerHTML = '<div class="note">决策：' + esc(d.decision) + '。' + esc(d.note || '') + '</div>';
    return;
  }
  const f = strategyFields(d.strategy);
  let h = '<div class="oknote">决策：' + esc(d.decision) + '；Rule：' + esc(d.rule.name) + '；Strategy：' +
    esc(d.strategy) + '</div><table style="width:100%;margin-top:12px;border-collapse:collapse"><tr>' +
    '<th align=left>#</th><th align=left>候选</th><th align=left>Provider</th>' +
    (f.priority ? '<th align=left>Priority</th>' : '') + (f.weight ? '<th align=left>Weight</th>' : '') +
    '<th align=left>AuthIndex</th></tr>';
  for (const c of d.ranked_candidates || []) {
    h += '<tr><td>' + c.order + '</td><td>' + esc(c.name) + '</td><td>' + esc(c.provider) + '</td>' +
      (f.priority ? '<td>' + c.priority + '</td>' : '') + (f.weight ? '<td>' + c.weight + '</td>' : '') +
      '<td class="mono">' + esc(c.auth_index) + '</td></tr>';
  }
  h += '</table><h3>真实验证</h3><div class="diag">' + esc(d.curl || '') +
    '</div><div class="muted">执行后到“路由记录”查看本次候选选择原因和 Failover 链。</div>';
  $('diagBody').innerHTML = h;
}

function renderObs() {
  const o = SNAP.observability || {};
  $('oMemory').value = String(o.memory_enabled !== false);
  $('oMemoryLimit').value = o.memory_limit || 500;
  $('oLog').value = String(o.log_enabled !== false);
  $('oLogLevel').value = o.log_level || 'info';
  $('oSQLite').value = String(!!o.sqlite_enabled);
  $('oSQLitePath').value = o.sqlite_path || 'key-chain-router.db';
  $('oRetention').value = o.sqlite_retention_days || 30;
  $('oMaxRows').value = o.sqlite_max_rows || 100000;
  $('oHeaders').value = String(!!o.response_headers);
  const h = SNAP.sqlite_status || {};
  const health = $('sqliteHealth');
  if (health) {
    if (!o.sqlite_enabled) {
      health.innerHTML = '<div class="muted small">SQLite 当前未开启。</div>';
    } else {
      const cls = h.active ? 'oknote' : 'note';
      const size = Number(h.file_size || 0);
      const sizeText = size >= 1048576 ? (size / 1048576).toFixed(2) + ' MB' : size >= 1024 ? (size / 1024).toFixed(1) + ' KB' : size + ' B';
      const bits = [
        '数据库：' + (h.path || o.sqlite_path || '-'),
        '文件：' + (h.file_exists ? sizeText : '尚未创建'),
        'Events：' + (h.events || 0),
        'Attempts：' + (h.attempts || 0),
        h.journal_mode ? 'Journal：' + h.journal_mode : '',
        h.last_write_at ? '最后写入：' + h.last_write_at : '最后写入：暂无'
      ].filter(Boolean);
      health.innerHTML = '<div class="' + cls + '"><b>SQLite ' + (h.active ? '运行中' : '未运行') + '</b><div class="small" style="margin-top:5px;word-break:break-all">' + esc(bits.join(' · ')) + '</div>' +
        (h.last_error ? '<div class="red small" style="margin-top:5px">最后错误：' + esc(h.last_error) + (h.last_error_at ? ' · ' + esc(h.last_error_at) : '') + '</div>' : '') +
        (h.probe_error ? '<div class="red small" style="margin-top:5px">健康检查：' + esc(h.probe_error) + '</div>' : '') + '</div>';
    }
  }
}

async function saveObs() {
  const o = {
    memory_enabled: $('oMemory').value === 'true', memory_limit: +$('oMemoryLimit').value || 500,
    log_enabled: $('oLog').value === 'true', log_level: $('oLogLevel').value,
    sqlite_enabled: $('oSQLite').value === 'true', sqlite_path: $('oSQLitePath').value,
    sqlite_retention_days: +$('oRetention').value || 30, sqlite_max_rows: +$('oMaxRows').value || 100000,
    response_headers: $('oHeaders').value === 'true'
  };
  SNAP = await api({ action: 'save_observability', payload: JSON.stringify(o) });
  render();
  await loadEvents();
  alert('已保存');
}

async function clearMemory() {
  SNAP = await api({ action: 'clear_memory' });
  await loadEvents();
}

function eventParams() {
  const q = { action: 'events', limit: $('eventLimit')?.value || '200' };
  const pairs = [
    ['q', 'eventSearch'], ['decision', 'eventDecision'], ['success', 'eventSuccess'], ['policy', 'eventPolicy'],
    ['strategy', 'eventStrategy'], ['provider', 'eventProvider'], ['model', 'eventModel'], ['status', 'eventStatus'], ['since', 'eventSince']
  ];
  for (const [k, id] of pairs) {
    const v = $(id)?.value;
    if (v && v !== 'all') q[k] = v;
  }
  return q;
}

async function loadEvents() {
  if (!$('events')) return;
  try {
    EVENT_DATA = await api(eventParams());
    renderEventFacets(EVENT_DATA.facets || {});
    renderEventStats(EVENT_DATA.stats || {});
    renderEvents();
  } catch (e) {
    $('events').innerHTML = '<div class="note">查询失败：' + esc(e.message || e) + '</div>';
  }
}

function setFacet(id, values, allLabel) {
  const el = $(id);
  if (!el) return;
  const current = el.value || 'all';
  el.innerHTML = '<option value="all">' + esc(allLabel) + '</option>' + (values || []).map((v) =>
    '<option value="' + esc(v) + '">' + esc(v) + '</option>').join('');
  if ([...el.options].some((o) => o.value === current)) el.value = current;
}

function renderEventFacets(f) {
  setFacet('eventPolicy', f.policies, '全部 Policy');
  setFacet('eventStrategy', f.strategies, '全部 Strategy');
  setFacet('eventProvider', f.providers, '全部 Provider');
  setFacet('eventModel', f.models, '全部 Model');
}

function fmtNumber(v, digits = 0) {
  const n = Number(v || 0);
  return Number.isFinite(n) ? n.toFixed(digits) : '0';
}

function renderEventStats(s) {
  const cards = [
    ['匹配请求', fmtNumber(s.total), '当前筛选'],
    ['成功率', fmtNumber(s.success_rate, 1) + '%', (s.success || 0) + ' 成功 / ' + (s.failed || 0) + ' 失败'],
    ['平均耗时', fmtNumber(s.avg_duration_ms) + ' ms', '端到端路由耗时'],
    ['P95 耗时', fmtNumber(s.p95_duration_ms) + ' ms', '当前筛选'],
    ['Fallback', fmtNumber(s.fallback), '转入 CPA 默认路由'],
    ['平均尝试', fmtNumber(s.avg_attempts, 2), '每个请求的候选次数']
  ];
  $('eventStats').innerHTML = cards.map(([n, v, note]) => '<div class="stat"><div class="muted small">' + esc(n) +
    '</div><b>' + esc(v) + '</b><div class="muted small">' + esc(note) + '</div></div>').join('');
}

function resetEventFilters() {
  for (const id of ['eventSearch']) if ($(id)) $(id).value = '';
  for (const id of ['eventDecision', 'eventSuccess', 'eventPolicy', 'eventStrategy', 'eventProvider', 'eventModel', 'eventStatus', 'eventSince']) {
    if ($(id)) $(id).value = 'all';
  }
  if ($('eventLimit')) $('eventLimit').value = '200';
  loadEvents();
}

function reasonLabel(r) {
  return ({
    no_matching_rule: 'Policy 已配置，但 model 未命中任何 Rule，交给 CPA 默认路由',
    rule_cpa_default: '命中 Rule 明确配置为 CPA 默认路由',
    no_enabled_candidates: '命中 Rule，但没有启用候选',
    candidates_exhausted: '候选全部尝试后仍失败',
    policy_fallback_to_cpa: 'Failover 规则转入 CPA 默认路由',
    policy_disabled: '该 API Key 的 KCR Policy 已停用',
    policy_lookup_miss: '检测到 Policy 查找异常'
  })[r] || r || '';
}

function statusLabel(a) {
  if (a.status) return 'HTTP ' + a.status;
  if (a.error) return 'ERROR';
  return '-';
}

function renderAttempt(e, a, i) {
  const reasons = e.selection_reasons || [];
  const reason = reasons[i] || (i === 0 ? '按当前策略排序后选择此候选' : '按 Failover 策略选择后续候选');
  const detail = [a.provider, a.auth_index ? 'AuthIndex ' + a.auth_index : '', a.model ? 'model ' + a.model : '',
    Number.isFinite(Number(a.duration_ms)) ? a.duration_ms + ' ms' : ''].filter(Boolean).join(' · ');
  return '<div class="attempt"><div class="row"><b>#' + (i + 1) + ' ' + esc(a.candidate || '-') + '</b>' +
    '<span class="badge">' + esc(statusLabel(a)) + '</span><span class="muted small">' + esc(detail) + '</span></div>' +
    '<div class="reason"><b>选择原因：</b>' + esc(reason) + '</div>' +
    (a.error ? '<div class="red small" style="margin-top:4px">' + esc(a.error) + '</div>' : '') + '</div>';
}

function fmtEventTime(at) {
  if (!at) return '-';
  const s = String(at);
  const m = s.match(/^(\d{4})-(\d{2})-(\d{2})T(\d{2}:\d{2}:\d{2})/);
  return m ? m[2] + '-' + m[3] + ' ' + m[4] : s;
}

function eventState(e) {
  if (e.success === false) return { cls: 'event-state-error', label: '失败' };
  if (e.decision === 'KCR_FALLBACK_TO_CPA') return { cls: 'event-state-fallback', label: 'Fallback' };
  if (e.decision === 'KCR_BYPASS_CPA_DEFAULT') return { cls: 'event-state-bypass', label: 'Bypass' };
  return { cls: 'event-state-ok', label: '成功' };
}

function eventStatusText(e) {
  if (e.status) return String(e.status);
  if (e.success === false) return 'ERR';
  return '-';
}

function toggleEvent(traceID) {
  if (!traceID) return;
  if (EVENT_EXPANDED.has(traceID)) EVENT_EXPANDED.delete(traceID);
  else EVENT_EXPANDED.add(traceID);
  renderEvents();
}

function renderEventDetail(e) {
  const eventReason = reasonLabel(e.reason);
  const details = [
    e.decision ? 'Decision ' + e.decision : '',
    e.auth_index ? '最终 AuthIndex ' + e.auth_index : '',
    e.key_hint ? 'API Key ' + e.key_hint : '',
    e.trace_id ? 'Trace ' + e.trace_id : ''
  ].filter(Boolean).join(' · ');
  return '<div class="event-detail">' +
    (eventReason ? '<div class="event-detail-reason"><b>路由结果：</b>' + esc(eventReason) + '</div>' : '') +
    '<div class="muted small event-detail-meta">' + esc(details) + '</div>' +
    '<div class="event-detail-title">候选尝试</div>' +
    '<div class="attempts">' + ((e.attempts || []).length ? (e.attempts || []).map((a, i) => renderAttempt(e, a, i)).join('') : '<div class="muted small">没有候选尝试记录</div>') + '</div>' +
    '</div>';
}

function renderEvents() {
  if (!EVENT_DATA) return;
  const xs = EVENT_DATA.events || [];
  const source = EVENT_DATA.source === 'sqlite' ? 'SQLite' : 'Memory';
  const health = EVENT_DATA.sqlite_status || {};
  const window = source === 'SQLite' ? '数据库记录 ' : '当前内存窗口 ';
  let meta = '数据源 ' + source + ' · ' + window + (EVENT_DATA.window_total || 0) + ' 条 · 匹配 ' + (EVENT_DATA.matched || 0) +
    ' 条 · 返回 ' + (EVENT_DATA.returned || 0) + ' 条';
  if (source === 'Memory') meta += ' · 内存上限 ' + (EVENT_DATA.memory_limit || 0) + (EVENT_DATA.memory_on ? '' : '（内存记录已关闭）');
  if (health.last_error) meta += ' · SQLite 异常：' + health.last_error;
  $('eventResultMeta').textContent = meta;

  if (!xs.length) {
    $('events').innerHTML = '<div class="muted" style="padding:18px 0">当前条件下暂无路由记录</div>';
    return;
  }

  const rows = xs.map((e) => {
    const key = e.trace_id || e.at;
    const expanded = EVENT_EXPANDED.has(key);
    const state = eventState(e);
    const attempts = (e.attempts || []).length;
    const policyRule = '<div class="event-primary">' + esc(e.policy_name || '-') + '</div>' +
      '<div class="muted small ellipsis">' + esc(e.rule_name || '-') + '</div>';
    const final = e.final || (e.decision === 'KCR_FALLBACK_TO_CPA' ? 'CPA Default' : '-');
    const row = '<tr class="event-row ' + (expanded ? 'expanded' : '') + '" onclick="toggleEvent(\'' + esc(key) + '\')" title="点击查看路由详情">' +
      '<td class="event-toggle"><span class="chevron">' + (expanded ? '▾' : '›') + '</span></td>' +
      '<td class="event-time mono" title="' + esc(e.at || '') + '">' + esc(fmtEventTime(e.at)) + '</td>' +
      '<td><div class="event-primary ellipsis" title="' + esc(e.model || '') + '">' + esc(e.model || '-') + '</div></td>' +
      '<td>' + policyRule + '</td>' +
      '<td><span class="strategy-chip" title="' + esc(e.strategy || '') + '">' + esc(e.strategy || '-') + '</span></td>' +
      '<td><div class="event-primary ellipsis" title="' + esc(final) + '">' + esc(final) + '</div></td>' +
      '<td><span class="ellipsis" title="' + esc(e.provider || '') + '">' + esc(e.provider || '-') + '</span></td>' +
      '<td><span class="event-state ' + state.cls + '"><i></i>' + esc(state.label) + '</span><span class="http-code">' + esc(eventStatusText(e)) + '</span></td>' +
      '<td class="event-duration">' + esc(Number.isFinite(Number(e.duration_ms)) ? e.duration_ms + ' ms' : '-') + '</td>' +
      '<td class="event-attempt-count"><span class="attempt-count ' + (attempts > 1 ? 'multi' : '') + '">' + attempts + '</span></td>' +
      '</tr>';
    const detail = expanded ? '<tr class="event-detail-row"><td colspan="10">' + renderEventDetail(e) + '</td></tr>' : '';
    return row + detail;
  }).join('');

  $('events').innerHTML = '<div class="event-table-wrap"><table class="event-table"><thead><tr>' +
    '<th></th><th>时间</th><th>Model</th><th>Policy / Rule</th><th>Strategy</th><th>最终候选</th><th>Provider</th><th>状态</th><th>耗时</th><th>Attempts</th>' +
    '</tr></thead><tbody>' + rows + '</tbody></table></div>';
}

load().catch((e) => $('env').innerHTML = '<div class="note">加载失败：' + esc(e.message || e) + '</div>');
