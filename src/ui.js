let SNAP = null;
let EDIT = null;
let DIAG = null;
let EVENT_DATA = null;
let EVENT_EXPANDED = new Set();
let ROUTE_STATS = null;
let DASH_STATS = null;
let DASH_EVENTS = null;
let ROUTE_PAGE = 1;
const ROUTE_PAGE_SIZE = 10;
let RULE_UI = new WeakMap();
const POLICY_PENDING = new Set();
let POLICY_STATUS = 'all';
let RESOURCE_FILTERS = {type:'all', provider:'all', status:'all'};
let POLICY_SAVING = false;
let EDIT_BASELINE = '';
let DRAG_CAND = null;
let CURRENT_VIEW = 'dashboard';

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

function showView(name) {
  if (CURRENT_VIEW === 'policyEditor' && EDIT && name !== 'policyEditor') {
    if (POLICY_SAVING) return;
    if (editorSignature() !== EDIT_BASELINE && !confirm('尚有未保存的修改，确定放弃吗？')) return;
    EDIT = null;
    RULE_UI = new WeakMap();
  }
  CURRENT_VIEW = name;
  for (const n of ['dashboard', 'policy', 'policyEditor', 'resources', 'usage', 'settings']) {
    const page = $(n + 'Page');
    if (page) page.classList.toggle('active', n === name);
  }
  for (const n of ['Dashboard', 'Policy', 'Resources', 'Usage', 'Settings']) {
    const nav = $('nav' + n);
    if (nav) nav.classList.toggle('active', n.toLowerCase() === name.toLowerCase());
  }
  if (name === 'policyEditor') {
    const nav = $('navPolicy');
    if (nav) nav.classList.add('active');
  }
  if (name === 'usage') loadEvents(true);
  if (name === 'resources') renderResources();
  window.scrollTo({ top: 0, behavior: 'instant' });
}

async function load() {
  SNAP = await api({ action: 'snapshot' });
  renderAll();
  await loadDashboard();
}

function renderAll() {
  $('versionBadge').textContent = 'v' + (SNAP.version || 'dev');
  renderPolicies();
  renderResources();
  renderObs();
  if (DASH_STATS) renderDashboard();
}

function dashboardSince() {
  return $('dashboardSince')?.value || '1h';
}

function routeStatsParams(since) {
  const q = { action: 'route_stats', since: since || '1h' };
  const policy = $('eventPolicy')?.value;
  const provider = $('eventProvider')?.value;
  const model = $('eventModel')?.value;
  if (policy && policy !== 'all') q.policy = policy;
  if (provider && provider !== 'all') q.provider = provider;
  if (model && model !== 'all') q.model = model;
  return q;
}

async function loadDashboard() {
  try {
    const since = dashboardSince();
    const [snapshot, stats, events] = await Promise.all([
      api({ action: 'snapshot' }),
      api({ action: 'route_stats', since }),
      api({ action: 'events', since, route_only: 'true', limit: '100', offset: '0' })
    ]);
    SNAP = snapshot;
    DASH_STATS = stats;
    DASH_EVENTS = events;
    renderAll();
    $('dashboardLastUpdated').textContent = '最后更新：' + new Date().toLocaleTimeString();
  } catch (e) {
    const target = $('dashboardIncidents') || $('dashboardPage');
    if (target) target.innerHTML = '<div class="note">仪表盘刷新失败：' + esc(e.message || e) + '</div>';
  }
}

function pct(n,d) {
  if (!d) return '0%';
  return (Number(n || 0) * 100 / Number(d)).toFixed(1) + '%';
}

function fmtDurationMs(ms) {
  ms = Number(ms || 0);
  if (!Number.isFinite(ms) || ms <= 0) return '—';
  if (ms < 1000) return Math.round(ms) + 'ms';
  if (ms < 60000) return (ms / 1000).toFixed(ms < 10000 ? 1 : 0) + 's';
  return Math.ceil(ms / 60000) + 'm';
}

function healthRank(state) {
  switch (String(state || '').toLowerCase()) {
    case 'unavailable': return 4;
    case 'open': return 3;
    case 'half_open': return 2;
    default: return 1;
  }
}

function healthLabel(state) {
  switch (String(state || '').toLowerCase()) {
    case 'unavailable': return {label:'不可用', cls:'bad'};
    case 'open': return {label:'OPEN', cls:'bad'};
    case 'half_open': return {label:'HALF_OPEN', cls:'warn'};
    default: return {label:'正常', cls:'ok'};
  }
}

function resourceHealthSummaries() {
  const health = SNAP.candidate_health || [];
  return (SNAP.resources || []).map((r) => {
    const matches = health.filter((h) =>
      String(h.provider || '').toLowerCase() === String(r.provider || '').toLowerCase() &&
      String(h.auth_index || '') === String(r.auth_index || '')
    );
    let state = (r.unavailable || String(r.status || '').toLowerCase() === 'disabled') ? 'unavailable' : 'closed';
    let failures = 0;
    let retry = 0;
    let nextProbe = '';
    for (const h of matches) {
      if (healthRank(h.state) > healthRank(state)) state = h.state;
      failures = Math.max(failures, Number(h.consecutive_failures || 0));
      const x = Number(h.retry_in_ms || 0);
      if (x > 0 && (!retry || x < retry)) retry = x;
      if (!nextProbe && h.next_probe_at) nextProbe = h.next_probe_at;
    }
    return {resource:r, state, failures, retry, nextProbe};
  });
}

function policyDashboardRule(p) {
  const rules = p.rules || [];
  return rules.find((r) => r.strategy !== 'cpa-default' && (r.models || []).includes('*')) ||
    rules.find((r) => r.strategy !== 'cpa-default') || rules[0] || null;
}

function candidateHealthState(p, rule, cand) {
  const h = (SNAP.candidate_health || []).find((x) =>
    x.key_fingerprint === p.key_fingerprint &&
    x.rule_id === rule.id &&
    x.candidate_id === cand.id
  );
  return h ? String(h.state || 'closed').toLowerCase() : 'closed';
}

function routeChainHTML(p) {
  const rule = policyDashboardRule(p);
  if (!rule) return '<span class="muted">—</span>';
  if (rule.strategy === 'cpa-default') return '<span class="route-node">CPA Default</span>';
  const xs = (rule.candidates || []).filter((c) => c.enabled !== false);
  if (!xs.length) return '<span class="muted">无启用候选</span>';
  return '<div class="route-chain">' + xs.map((c,i) => {
    const state = candidateHealthState(p, rule, c);
    const bad = state === 'open' || state === 'half_open';
    return (i ? '<span class="route-arrow">→</span>' : '') +
      '<span class="route-node ' + (bad ? 'bad' : '') + '">' + esc(c.name || c.provider || '-') + (bad ? ' ×' : '') + '</span>';
  }).join('') + '</div>';
}

function renderTrendChart(points) {
  const xs = points || [];
  if (!xs.length || !xs.some((x) => Number(x.direct||0)+Number(x.fallback||0)+Number(x.failed||0)>0)) {
    return '<div class="empty">当前时间范围暂无路由数据</div>';
  }
  const max = Math.max(1, ...xs.map((x) => Number(x.direct||0)+Number(x.fallback||0)+Number(x.failed||0)));
  const cols = xs.map((x) => {
    const direct = Number(x.direct || 0), fallback = Number(x.fallback || 0), failed = Number(x.failed || 0);
    const total = direct + fallback + failed;
    const height = Math.max(total ? 5 : 0, total * 100 / max);
    const dp = total ? direct * 100 / total : 0;
    const fp = total ? fallback * 100 / total : 0;
    const ep = total ? failed * 100 / total : 0;
    return '<div class="trend-col" title="直接成功 '+direct+' · Fallback '+fallback+' · 失败 '+failed+'"><div class="trend-stack" style="height:'+height+'%"><span class="trend-direct" style="height:'+dp+'%"></span><span class="trend-fallback" style="height:'+fp+'%"></span><span class="trend-failed" style="height:'+ep+'%"></span></div></div>';
  }).join('');
  const first = new Date(xs[0].at), last = new Date(xs[xs.length-1].at);
  const time = (d) => Number.isNaN(d.getTime()) ? '' : d.toLocaleTimeString([], {hour:'2-digit',minute:'2-digit'});
  return '<div class="legend"><span><i style="background:#2fc184"></i>直接成功</span><span><i style="background:#f7b955"></i>Fallback</span><span><i style="background:#f37b75"></i>失败</span></div><div class="dashboard-trend">'+cols+'</div><div class="trend-axis"><span>'+esc(time(first))+'</span><span>'+esc(time(last))+'</span></div>';
}

function renderDashboard() {
  if (!SNAP || !DASH_STATS) return;
  const policies = SNAP.policies || [];
  const resources = SNAP.resources || [];
  const healthRows = resourceHealthSummaries();
  const abnormal = healthRows.filter((x) => healthRank(x.state) > 1);
  const stats = DASH_STATS.stats || {};
  const cards = [
    ['Policy', policies.length, '当前配置'],
    ['上游资源', resources.length, '当前可见'],
    ['异常资源', abnormal.length, abnormal.length ? '需要关注' : '全部正常'],
    ['近 ' + (($('dashboardSince')?.selectedOptions?.[0]?.textContent || '1 小时').replace('最近 ','')) + ' Fallback', stats.fallback || 0, stats.total ? pct(stats.fallback, stats.total) : '暂无请求']
  ];
  $('dashboardStats').innerHTML = cards.map(([n,v,h],i) =>
    '<div class="stat"><div class="muted small">' + esc(n) + '</div><b>' + esc(v) + '</b><div class="hint ' + (i===2&&Number(v)>0?'red':'') + '">' + esc(h) + '</div></div>'
  ).join('');

  $('dashboardTrend').innerHTML = renderTrendChart(DASH_STATS.trend || []);
  $('dashboardTrendRange').textContent = $('dashboardSince')?.selectedOptions?.[0]?.textContent || '';

  const unavailable = healthRows.filter((x)=>x.state==='unavailable').length;
  const open = healthRows.filter((x)=>x.state==='open'||x.state==='half_open').length;
  const pf = DASH_STATS.policy_fallbacks || {};
  const fallbackPolicies = Object.keys(pf).filter((k)=>Number(pf[k])>0).length;
  const retryValues = healthRows.map((x)=>Number(x.retry||0)).filter((x)=>x>0);
  const nextProbe = retryValues.length ? Math.min(...retryValues) : 0;
  const attention = unavailable + open + fallbackPolicies;
  $('dashboardAttention').textContent = attention ? attention + ' 项需要关注' : '无异常';
  $('dashboardAttention').className = 'tag ' + (attention ? 'filtered' : 'ok');
  const anomalyRows = [
    ['red','!', '上游资源不可用', unavailable],
    ['orange','!', '资源处于 OPEN / HALF_OPEN', open],
    ['blue','↔', 'Policy 发生 Fallback', fallbackPolicies],
    ['blue','◷', '下一次探测', nextProbe ? fmtDurationMs(nextProbe) + ' 后' : '—']
  ];
  $('dashboardAnomalies').innerHTML = '<div class="anomaly-list">' + anomalyRows.map(([cls,icon,name,val]) =>
    '<div class="anomaly-row"><span class="anomaly-icon '+cls+'">'+icon+'</span><span class="grow">'+esc(name)+'</span><b>'+esc(val)+'</b></div>'
  ).join('') + '</div>';

  const pFallbacks = DASH_STATS.policy_fallbacks || {};
  let healthyPolicies = 0;
  $('dashboardPolicies').innerHTML = policies.length ? '<div class="table-wrap"><table class="data-table"><thead><tr><th>Policy 名称</th><th>客户端类型</th><th>Client Affinity</th><th>当前路由链</th><th>Fallback</th><th>状态</th><th>操作</th></tr></thead><tbody>' +
    policies.map((p) => {
      const rule=policyDashboardRule(p);
      const bad=(rule?.candidates||[]).some((cand)=>['open','half_open'].includes(candidateHealthState(p,rule,cand)));
      const fb=Number(pFallbacks[p.name]||0);
      const state=!p.enabled?{label:'停用',cls:'muted'}:bad?{label:'注意',cls:'warn'}:{label:'正常',cls:'green'};
      if(p.enabled&&!bad) healthyPolicies++;
      return '<tr><td><b>'+esc(p.name)+'</b></td><td>'+esc(clientTypeLabel(p.client_type||p.client_provider||'-'))+'</td><td><span class="tag '+(p.client_affinity==='strict'?'ok':'')+'">'+(p.client_affinity==='strict'?'ON':'OFF')+'</span></td><td>'+routeChainHTML(p)+'</td><td>'+fb+'</td><td><span class="'+state.cls+'">● '+esc(state.label)+'</span></td><td><button class="btn small" onclick="openPolicy(\''+esc(p.key_fingerprint)+'\')">编辑</button></td></tr>';
    }).join('') + '</tbody></table></div>' : '<div class="empty">尚未创建 Policy</div>';
  $('dashboardPolicyHealth').textContent = healthyPolicies + ' / ' + policies.length + ' 正常';

  const sortedHealth = healthRows.slice().sort((a,b)=>healthRank(b.state)-healthRank(a.state)||resourceLabel(a.resource).localeCompare(resourceLabel(b.resource)));
  $('dashboardResources').innerHTML = sortedHealth.length ? '<div class="table-wrap"><table class="data-table"><thead><tr><th>资源别名</th><th>类型</th><th>Provider</th><th>当前状态</th><th>连续失败</th><th>Backoff / Next Probe</th><th>操作</th></tr></thead><tbody>' +
    sortedHealth.slice(0,8).map((x)=>{
      const st=healthLabel(x.state);
      return '<tr><td><b>'+esc(resourceLabel(x.resource))+'</b></td><td><span class="tag '+(String(x.resource.kind).toLowerCase()==='oauth'?'oauth':'api')+'">'+esc(x.resource.kind||'-')+'</span></td><td>'+esc(x.resource.provider||'-')+'</td><td><span class="status-pill '+st.cls+'"><span class="health-dot '+st.cls+'"></span>'+esc(st.label)+'</span></td><td>'+x.failures+'</td><td>'+esc(x.retry?fmtDurationMs(x.retry)+' 后':'—')+'</td><td><button class="btn small" onclick="openResourceDrawer(\''+esc(x.resource.id)+'\')">查看</button></td></tr>';
    }).join('') + '</tbody></table></div>' : '<div class="empty">没有上游资源</div>';

  const incidents=(DASH_EVENTS?.events||[]).filter((e)=>!e.success||(e.attempts||[]).length>1||e.decision==='KCR_FALLBACK_TO_CPA').slice(0,5);
  $('dashboardIncidents').innerHTML = incidents.length ? '<div class="table-wrap"><table class="data-table incident-table"><thead><tr><th>时间</th><th>级别</th><th>Policy</th><th>事件</th><th>详情</th></tr></thead><tbody>' +
    incidents.map((e)=>{
      const path=eventRoutePath(e);
      const level=!e.success?['错误','red']:['警告','warn'];
      const event=!e.success?'路由失败':'触发 Fallback';
      const detail=path+(e.status?' · HTTP '+e.status:'');
      return '<tr><td class="mono">'+esc(fmtEventTime(e.at))+'</td><td><span class="'+level[1]+'">● '+level[0]+'</span></td><td>'+esc(e.policy_name||'-')+'</td><td>'+esc(event)+'</td><td>'+esc(detail)+'</td></tr>';
    }).join('')+'</tbody></table></div>' : '<div class="empty">当前时间范围没有异常路由事件</div>';

  $('envNotice').innerHTML = SNAP.config_error ? '<div class="note">CPA 配置读取异常：'+esc(SNAP.config_error)+'</div>' : '';
}

function restoreControlFocus(id) {
  const control = id && $(id);
  if (control && !control.disabled) control.focus?.({preventScroll:true});
}

function filterChip(label, value, active, count, handler, id) {
  return '<button type="button" '+(id?'id="'+esc(id)+'" ':'')+'class="filter-chip '+(active?'active':'')+'" aria-pressed="'+active+'" onclick="'+handler+'">'+esc(label)+(count===undefined?'':' <span class="chip-count">'+count+'</span>')+'</button>';
}

function renderPolicies() {
  const focused = document.activeElement?.id;
  const ps = SNAP.policies || [];
  const pending = POLICY_PENDING.size > 0;
  if ($('newPolicyButton')) $('newPolicyButton').disabled = pending;
  const q = String($('policySearch')?.value || '').trim().toLowerCase();
  const enabled = ps.filter((p)=>p.enabled).length;
  $('policyFilters').innerHTML = [['all','全部',ps.length],['enabled','已启用',enabled],['disabled','已停用',ps.length-enabled]].map(([v,n,c])=>filterChip(n,v,POLICY_STATUS===v,c,"POLICY_STATUS='"+v+"';renderPolicies()",'policy-filter-'+v)).join('');
  const xs = ps.filter((p)=>(POLICY_STATUS==='all'||Boolean(p.enabled)===(POLICY_STATUS==='enabled')) && (!q || [p.name,p.key_hint,p.client_type,p.client_provider,...(p.rules||[]).flatMap((r)=>r.models||[])].some((x)=>String(x||'').toLowerCase().includes(q))));
  $('policyCount').textContent = xs.length + ' / ' + ps.length + ' 个 Policy';
  if (!xs.length) {
    $('policies').innerHTML = '<div class="empty">'+(ps.length?'没有匹配的 Policy，请调整搜索或状态筛选。':'还没有 Policy。每个下游 API Key 最多创建一个。')+'</div>';
    restoreControlFocus(focused);
    return;
  }
  $('policies').innerHTML = xs.map((p) => {
    const strict = p.client_affinity === 'strict';
    const type = p.client_type || p.client_provider || '-';
    const busy = POLICY_PENDING.has(p.key_fingerprint);
    return '<div class="policy-row '+(p.enabled?'':'policy-disabled')+'"><div class="row"><div class="grow policy-content"><h3>' + esc(p.name) + '</h3><div class="policy-meta"><span>Key ' + esc(p.key_hint) + '</span><span>' + esc(clientTypeLabel(type)) + '</span><span>' + (p.rules || []).length + ' 条规则</span><span class="tag ' + (strict ? 'ok' : '') + '">Affinity ' + (strict ? 'Strict' : 'Off') + '</span></div><div class="policy-models">'+(p.rules||[]).map((r)=>'<span class="tag">'+esc((r.models||[]).join(', ')||'未设置模型')+'</span>').join('')+'</div></div><div class="policy-actions"><div class="switch-line policy-enable"><span class="small '+(p.enabled?'green':'muted')+'">'+(busy?'保存中…':p.enabled?'已启用':'已停用')+'</span><button type="button" id="policy-toggle-'+esc(p.key_fingerprint)+'" class="switch '+(p.enabled?'on':'')+'" role="switch" aria-checked="'+Boolean(p.enabled)+'" aria-label="'+esc((p.enabled?'停用 ':'启用 ')+p.name)+'" '+(pending?'disabled '+(busy?'aria-busy="true"':''):'')+' onclick="togglePolicyEnabled(\''+esc(p.key_fingerprint)+'\')"></button></div><button class="btn small" '+(pending?'disabled':'')+' onclick="diagnose(\'' + esc(p.key_fingerprint) + '\')">诊断</button><button class="btn small" '+(pending?'disabled':'')+' onclick="openPolicy(\'' + esc(p.key_fingerprint) + '\')">编辑</button><button class="btn small danger" '+(pending?'disabled':'')+' onclick="delPolicy(\'' + esc(p.key_fingerprint) + '\')">删除</button></div></div></div>';
  }).join('');
  restoreControlFocus(focused);
}

async function togglePolicyEnabled(fp) {
  if (POLICY_PENDING.size || POLICY_SAVING) return;
  const focused = document.activeElement?.id;
  const current = (SNAP.policies || []).find((p)=>p.key_fingerprint===fp);
  if (!current) return;
  const enabled = !current.enabled;
  POLICY_PENDING.add(fp);
  $('policyNotice').textContent = '';
  renderPolicies();
  try {
    // Use the latest persisted definition, never the editor draft or an old list snapshot.
    const latest = await api({action:'snapshot'});
    const found = (latest.policies || []).find((p)=>p.key_fingerprint===fp);
    if (!found) throw new Error('Policy 已不存在，请刷新列表');
    const updated = JSON.parse(JSON.stringify(found));
    updated.enabled = enabled;
    SNAP = await api({action:'save_policy',payload:JSON.stringify(updated)});
    $('policyNotice').textContent = (updated.name || 'Policy') + (enabled?' 已启用':' 已停用');
  } catch (e) {
    // A disconnected response may follow a successful write. Read back, never retry blindly.
    try { SNAP = await api({action:'snapshot'}); } catch (_) { /* Keep last confirmed state. */ }
    $('policyNotice').textContent = '切换状态未确认：' + (e.message || e) + '。请核对当前状态后再操作。';
  } finally {
    POLICY_PENDING.delete(fp);
    renderAll();
    if (CURRENT_VIEW === 'policy') restoreControlFocus(focused);
  }
}

function availableKeys(editFp) {
  return (SNAP.downstream_keys || []).filter((k) => k.fingerprint === editFp || !(SNAP.policies || []).some((p) => p.key_fingerprint === k.fingerprint));
}

function providerValues() {
  const seen = new Set();
  const out = [];
  for (const r of SNAP.resources || []) {
    const p = String(r.provider || '').trim();
    const k = p.toLowerCase();
    if (p && !seen.has(k)) { seen.add(k); out.push(p); }
  }
  for (const p of ['claude','codex','gemini','openai-compatibility']) {
    if (!seen.has(p)) { seen.add(p); out.push(p); }
  }
  return out.sort((a,b) => a.localeCompare(b));
}

function clientTypeLabel(v) {
  const x = String(v || '').toLowerCase();
  if (x === 'claude') return 'Claude';
  if (x === 'codex' || x === 'openai') return 'Codex / OpenAI';
  if (x === 'gemini') return 'Gemini';
  if (x === 'openai-compatibility') return 'OpenAI Compatible';
  return v || '未设置';
}

function renderClientTypes() {
  const current = String(EDIT.client_type || EDIT.client_provider || '').trim();
  const xs = providerValues();
  if (current && !xs.some((x) => x.toLowerCase() === current.toLowerCase())) xs.unshift(current);
  if (!EDIT.client_type && current) EDIT.client_type = current;
  if (!EDIT.client_type && xs.length) EDIT.client_type = xs[0];
  $('pClientType').innerHTML = xs.map((x) => '<option value="' + esc(x) + '" ' + (x.toLowerCase() === String(EDIT.client_type || '').toLowerCase() ? 'selected' : '') + '>' + esc(clientTypeLabel(x)) + ' · ' + esc(x) + '</option>').join('');
}

function openPolicy(fp) {
  if (POLICY_SAVING || POLICY_PENDING.size) return;
  const found = fp ? (SNAP.policies || []).find((x) => x.key_fingerprint === fp) : null;
  EDIT = found ? JSON.parse(JSON.stringify(found)) : {
    name:'', key_fingerprint:'', key_hint:'', enabled:true,
    client_type:'', client_provider:'', client_affinity:'off', rules:[defaultRule()]
  };
  RULE_UI = new WeakMap();
  EDIT.client_affinity = EDIT.client_affinity || 'off';
  EDIT.client_type = EDIT.client_type || EDIT.client_provider || '';
  $('policyTitle').textContent = fp ? '编辑 Policy' : '新建 Policy';
  $('pName').value = EDIT.name || '';
  renderPolicyEnabled();
  const keys = availableKeys(fp);
  $('pKey').innerHTML = keys.map((k) => {
    const label = k.alias ? k.alias + ' · ' + k.hint : k.hint;
    return '<option value="' + esc(k.fingerprint) + '" data-hint="' + esc(k.hint) + '" data-alias="' + esc(k.alias || '') + '">' + esc(label) + '</option>';
  }).join('');
  if (EDIT.key_fingerprint) $('pKey').value = EDIT.key_fingerprint;
  renderClientTypes();
  renderAffinityState();
  renderRules();
  showView('policyEditor');
  EDIT_BASELINE = editorSignature();
}

function closePolicy() {
  if (POLICY_SAVING) return;
  if (EDIT && editorSignature() !== EDIT_BASELINE && !confirm('尚有未保存的修改，确定放弃吗？')) return;
  EDIT = null;
  RULE_UI = new WeakMap();
  showView('policy');
}

function renderPolicyEnabled() {
  if (!EDIT) return;
  const enabled = EDIT.enabled !== false;
  $('pEnabledToggle').classList.toggle('on', enabled);
  $('pEnabledToggle').setAttribute('aria-checked', String(enabled));
  $('pEnabledSummary').textContent = enabled ? '启用，保存后参与路由' : '停用，保存后不参与路由';
}

function toggleEditorEnabled() {
  if (!EDIT) return;
  EDIT.enabled = EDIT.enabled === false;
  renderPolicyEnabled();
}

function editorSignature() {
  return JSON.stringify({...EDIT, name:$('pName').value, key_fingerprint:$('pKey').value, client_type:$('pClientType').value});
}

function renderAffinityState() {
  if (!EDIT) return;
  const supported = EDIT.client_affinity === 'off' || EDIT.client_affinity === 'strict';
  const strict = EDIT.client_affinity === 'strict';
  $('pAffinityToggle').classList.toggle('on', strict);
  $('pAffinityToggle').setAttribute('aria-checked', String(strict));
  $('affinitySummary').textContent = strict ? '开启（严格模式）' : supported ? '关闭' : '当前值不受支持：' + EDIT.client_affinity;
  if (!supported) {
    $('affinityNote').className = 'info warnbox';
    $('affinityNote').innerHTML = '<b>当前 Policy 使用此版本不支持的 Client Affinity：' + esc(EDIT.client_affinity) + '。</b> 运行时保持 fail closed；请明确切换为 Off 或 Strict 后再保存。';
  } else if (strict) {
    $('affinityNote').className = 'info';
    $('affinityNote').innerHTML = '已启用 Client Affinity：客户端类型为 <b>' + esc(clientTypeLabel(EDIT.client_type)) + '</b>。仅允许 Provider=<span class="mono">' + esc(EDIT.client_type) + '</span> 的原生 API 资源，以及全部 OAuth 转换资源。被过滤资源仍显示，但不可添加。';
  } else {
    $('affinityNote').className = 'info';
    $('affinityNote').innerHTML = '当前未启用 Client Affinity，策略不限制 Provider 类型，可从所有上游资源中选择候选。客户端类型仍作为 Policy 元数据保存。';
  }
}

function toggleAffinity() {
  if (!EDIT) return;
  EDIT.client_affinity = EDIT.client_affinity === 'strict' ? 'off' : 'strict';
  renderAffinityState();
  renderRules();
}

function changeClientType() {
  EDIT.client_type = $('pClientType').value;
  renderAffinityState();
  renderRules();
}

function defaultRule() {
  return {
    id:'', name:'默认规则', models:['*'], strategy:'priority-weighted',
    sticky_source:'auto', sticky_header:'', candidates:[],
    failover:{network:'next',unauthorized:'next',timeout:'next',conflict:'next',rate_limit:'next',server_error:'next',other:'stop',exhausted:'error',max_attempts:3}
  };
}

function addRule() {
  EDIT.rules = EDIT.rules || [];
  const rule = defaultRule();
  EDIT.rules.push(rule);
  ruleUI(rule, EDIT.rules.length-1).open = true;
  renderRules();
  $('rule-card-'+(EDIT.rules.length-1))?.scrollIntoView({behavior:'smooth',block:'start'});
}

function moveRule(i,d) {
  const j=i+d;
  if (j<0 || j>=EDIT.rules.length) return;
  [EDIT.rules[i],EDIT.rules[j]]=[EDIT.rules[j],EDIT.rules[i]];
  renderRules();
}

function deleteRule(ri) {
  EDIT.rules.splice(ri,1);
  renderRules();
}

function ruleUI(rule, index) {
  if (!RULE_UI.has(rule)) RULE_UI.set(rule, {open:index===0, advanced:false, pool:!(rule.candidates||[]).length, filtered:false, search:''});
  return RULE_UI.get(rule);
}

function setRuleSection(ri, key, open) {
  if (EDIT?.rules?.[ri] && ['open','advanced','pool','filtered'].includes(key)) ruleUI(EDIT.rules[ri],ri)[key] = open;
}

const STRATEGIES = [
  ['ordered-failover','有序 Failover','按候选顺序依次尝试'],
  ['round-robin','轮询','轮流选择首选候选'],
  ['weighted-round-robin','加权轮询','按 Weight 分配流量'],
  ['priority-weighted','Priority + Weight','先优先级，再按权重分配'],
  ['sticky','Sticky','让同一会话尽量命中同一资源'],
  ['cpa-default','CPA 默认','不接管，交回 CPA 路由']
];

function selectRuleStrategy(ri, strategy) {
  if (!STRATEGIES.some(([key])=>key===strategy)) return;
  EDIT.rules[ri].strategy = strategy;
  renderRules();
}

function strategyLabel(value) { return STRATEGIES.find(([key])=>key===value)?.[1] || value; }

function refreshRuleSummary(ri) {
  const r = EDIT.rules[ri];
  const title = $('rule-title-'+ri), meta = $('rule-meta-'+ri);
  if (title) title.textContent = r.name || '未命名规则';
  if (meta) meta.textContent = (r.models||[]).join(', ') + ' · ' + strategyLabel(r.strategy) + ' · ' + (r.candidates||[]).length + ' 个候选';
}

function refreshFailoverSummary(ri) {
  const f = EDIT.rules[ri].failover;
  const summary = $('failover-summary-'+ri);
  if (summary) summary.textContent = '最大尝试 '+(f.max_attempts||'自动')+' 次 · 耗尽后'+(f.exhausted==='cpa-default'?'转 CPA 默认':'返回错误');
}

function strategyOptions(v) {
  return [
    ['ordered-failover','有序 Failover'],
    ['round-robin','轮询'],
    ['weighted-round-robin','平滑加权轮询'],
    ['priority-weighted','Priority + Weight'],
    ['sticky','Sticky / 一致性哈希'],
    ['cpa-default','CPA 默认路由']
  ].map(([k,n]) => '<option value="' + k + '" ' + (v===k?'selected':'') + '>' + n + '</option>').join('');
}

function failActionOptions(v) {
  return [
    ['next','下一个候选'],
    ['same-priority-first','同优先级优先'],
    ['next-priority','下一优先级'],
    ['stop','停止'],
    ['cpa-default','转 CPA 默认']
  ].map(([k,n]) => '<option value="' + k + '" ' + (v===k?'selected':'') + '>' + n + '</option>').join('');
}

function strategyFields(s) {
  return {
    priority:s==='priority-weighted'||s==='sticky',
    weight:s==='weighted-round-robin'||s==='priority-weighted'||s==='sticky'
  };
}

function resourceAllowedByAffinity(r) {
  if (!EDIT || EDIT.client_affinity !== 'strict') return true;
  if (String(r.kind || '').toLowerCase() === 'oauth') return true;
  return String(r.provider || '').toLowerCase() === String(EDIT.client_type || '').toLowerCase();
}

function resourceLabel(r) {
  return r ? (r.alias || r.display_name || r.id || '-') : '-';
}

function resourceEndpoint(r) {
  return r.base_url || r.display_name || r.key_hint || r.auth_index || '-';
}

function resourceStatus(r) {
  const disabled = r.unavailable || String(r.status || '').toLowerCase() === 'disabled';
  return disabled ? {ok:false,label:'不可用'} : {ok:true,label:'正常'};
}

function candidateSelected(rule, res) {
  return (rule.candidates || []).some((c) => c.resource_id === res.id || (c.auth_index && res.auth_index && c.auth_index === res.auth_index && String(c.provider).toLowerCase() === String(res.provider).toLowerCase()));
}

function ruleResourcePoolHTML(r,ri) {
  const query = ruleUI(r,ri).search.trim().toLowerCase();
  const resources = (SNAP.resources || []).filter((x) => {
    if (!query) return true;
    return [x.alias,x.display_name,x.provider,x.base_url,x.key_hint,x.auth_index].some((v) => String(v || '').toLowerCase().includes(query));
  });
  const available = resources.filter(resourceAllowedByAffinity);
  const filtered = resources.filter((x) => !resourceAllowedByAffinity(x));
  let h = '<div class="info" style="margin-bottom:8px">' +
    (EDIT.client_affinity==='strict'
      ? '严格模式：匹配客户端 Provider 的原生资源和全部 OAuth 可选；其他原生 API 资源保留展示但被过滤。'
      : 'Affinity 关闭：全部资源均可选择。') + '</div>';
  h += resourcePoolTable(r,ri,available,false);
  if (filtered.length) {
    h += '<details class="filtered-details" '+(ruleUI(r,ri).filtered?'open':'')+' ontoggle="if(this.isConnected)setRuleSection('+ri+',\'filtered\',this.open)"><summary>查看被 Client Affinity 过滤的资源（'+filtered.length+'）</summary>' + resourcePoolTable(r,ri,filtered,true) + '</details>';
  }
  return h;
}

function updateRuleResourceSearch(ri,value) {
  if (!EDIT?.rules?.[ri]) return;
  ruleUI(EDIT.rules[ri],ri).search = value;
  const pool = $('rule-resource-pool-' + ri);
  if (pool && EDIT && EDIT.rules && EDIT.rules[ri]) {
    pool.innerHTML = ruleResourcePoolHTML(EDIT.rules[ri],ri);
  }
}

function renderRules(focusID) {
  if (!EDIT) return;
  const focused = focusID || document.activeElement?.id;
  const rules = EDIT.rules || [];
  $('ruleCount').textContent = rules.length + ' 条规则';
  if (!rules.length) {
    $('rules').innerHTML = '<div class="empty" style="margin-top:14px">尚无模型规则，请新增规则。</div>';
    return;
  }
  $('rules').innerHTML = rules.map((r,ri) => {
    r.failover = r.failover || defaultRule().failover;
    const f = strategyFields(r.strategy), ui = ruleUI(r,ri);
    let h = '<details class="rule-card" id="rule-card-'+ri+'" '+(ui.open?'open':'')+' ontoggle="if(this.isConnected)setRuleSection('+ri+',\'open\',this.open)"><summary class="rule-summary"><span class="rule-num">'+(ri+1)+'</span><span class="grow"><b id="rule-title-'+ri+'">'+esc(r.name||'未命名规则')+'</b><span class="rule-summary-meta" id="rule-meta-'+ri+'">'+esc((r.models||[]).join(', '))+' · '+esc(strategyLabel(r.strategy))+' · '+(r.candidates||[]).length+' 个候选</span></span><span class="disclosure-arrow" aria-hidden="true">⌄</span></summary><div class="rule-body">';
    h += '<div class="rule-head"><span class="muted small grow">先设置匹配条件，再选择路由方式与候选资源。</span><div class="rule-actions"><button class="btn small" aria-label="上移规则" '+(ri===0?'disabled':'')+' onclick="moveRule('+ri+',-1)">↑</button><button class="btn small" aria-label="下移规则" '+(ri===rules.length-1?'disabled':'')+' onclick="moveRule('+ri+',1)">↓</button><button class="btn small danger" onclick="deleteRule('+ri+')">删除规则</button></div></div>';
    h += '<div class="grid2"><div><label for="rule-name-'+ri+'">Rule 名称 *</label><input id="rule-name-'+ri+'" value="'+esc(r.name||'')+'" oninput="EDIT.rules['+ri+'].name=this.value;refreshRuleSummary('+ri+')"></div><div><label for="rule-models-'+ri+'">匹配模型 *</label><input id="rule-models-'+ri+'" value="'+esc((r.models||['*']).join(','))+'" oninput="EDIT.rules['+ri+'].models=this.value.split(/[,;\\n]+/);refreshRuleSummary('+ri+')"><div class="field-help">支持 * / ? 通配符；多个模型用逗号分隔。</div></div></div>';
    h += '<div class="setting-section"><h3 class="section-label" id="strategy-label-'+ri+'">路由策略</h3><div class="strategy-grid" role="group" aria-labelledby="strategy-label-'+ri+'">'+STRATEGIES.map(([key,name,desc])=>'<button type="button" id="strategy-'+ri+'-'+key+'" class="strategy-option '+(r.strategy===key?'active':'')+'" aria-pressed="'+(r.strategy===key)+'" onclick="selectRuleStrategy('+ri+',\''+key+'\')"><b>'+name+'</b><span>'+desc+'</span></button>').join('')+'</div></div>';
    if (r.strategy === 'cpa-default') return h+'<div class="oknote" style="margin-top:16px">命中此 Rule 时不执行候选资源，直接交给 CPA 默认路由。原有候选与 Failover 配置仍会保留。</div></div></details>';
    if (r.strategy === 'sticky') {
      h += '<div class="advanced grid2" style="margin-top:14px"><div><label for="sticky-source-'+ri+'">Sticky 来源</label><select id="sticky-source-'+ri+'" onchange="EDIT.rules['+ri+'].sticky_source=this.value;renderRules()"><option value="auto" '+((r.sticky_source||'auto')==='auto'?'selected':'')+'>自动</option><option value="session" '+(r.sticky_source==='session'?'selected':'')+'>Session</option><option value="header" '+(r.sticky_source==='header'?'selected':'')+'>指定 Header</option></select></div>'+(r.sticky_source==='header'?'<div><label for="sticky-header-'+ri+'">Sticky Header *</label><input id="sticky-header-'+ri+'" value="'+esc(r.sticky_header||'')+'" placeholder="例如：X-Session-Id" oninput="EDIT.rules['+ri+'].sticky_header=this.value"></div>':'')+'</div>';
    }
    h += '<div class="candidate-section"><div class="section-heading"><h3 class="section-label">已选候选资源 <span class="tag">'+(r.candidates||[]).length+'</span></h3><span class="field-help">'+(f.priority?'Priority 越大越优先；':'按顺序排列；')+(f.weight?'Weight 为相对权重。':'可拖动或用箭头排序。')+'</span></div>'+selectedCandidatesTable(r,ri,f)+'</div>';
    h += '<details class="resource-picker" '+(ui.pool?'open':'')+' ontoggle="if(this.isConnected)setRuleSection('+ri+',\'pool\',this.open)"><summary>＋ 添加候选资源 <span class="muted small">从 '+(SNAP.resources||[]).length+' 个上游资源中选择</span></summary><div class="picker-body"><div class="toolbar"><input class="search" aria-label="搜索候选资源" value="'+esc(ui.search)+'" placeholder="搜索资源别名、Provider、URL 或账号…" oninput="updateRuleResourceSearch('+ri+',this.value)"></div><div id="rule-resource-pool-'+ri+'">'+ruleResourcePoolHTML(r,ri)+'</div></div></details>';
    h += '<details class="advanced failover-details" '+(ui.advanced?'open':'')+' ontoggle="if(this.isConnected)setRuleSection('+ri+',\'advanced\',this.open)"><summary><b>高级设置 · Failover</b><span class="muted small" id="failover-summary-'+ri+'">最大尝试 '+(r.failover.max_attempts||'自动')+' 次 · 耗尽后'+(r.failover.exhausted==='cpa-default'?'转 CPA 默认':'返回错误')+'</span></summary><div class="advanced-body"><div class="grid2"><div><label for="exhausted-'+ri+'">候选耗尽后</label><select id="exhausted-'+ri+'" onchange="EDIT.rules['+ri+'].failover.exhausted=this.value;refreshFailoverSummary('+ri+')"><option value="error" '+(r.failover.exhausted!=='cpa-default'?'selected':'')+'>返回错误</option><option value="cpa-default" '+(r.failover.exhausted==='cpa-default'?'selected':'')+'>转 CPA 默认</option></select></div><div><label for="attempts-'+ri+'">最大尝试次数</label><input id="attempts-'+ri+'" type="number" min="0" value="'+(r.failover.max_attempts||0)+'" oninput="EDIT.rules['+ri+'].failover.max_attempts=+this.value||0;refreshFailoverSummary('+ri+')"><div class="field-help">0 表示按候选数量自动决定。</div></div></div><h3 class="section-label" style="margin:18px 0 10px">按错误类型处理</h3><div class="failover-grid">';
    for (const [key,label] of [['network','网络错误'],['unauthorized','401 / 403'],['timeout','408 Timeout'],['conflict','409 Conflict'],['rate_limit','429 Rate Limit'],['server_error','5xx'],['other','其他错误']]) {
      h += '<div class="failover-field"><label for="fail-'+ri+'-'+key+'">'+label+'</label><select id="fail-'+ri+'-'+key+'" onchange="EDIT.rules['+ri+'].failover.'+key+'=this.value">'+failActionOptions(r.failover[key])+'</select></div>';
    }
    return h+'</div></div></details></div></details>';
  }).join('');
  restoreControlFocus(focused);
}

function resourcePoolTable(rule,ri,resources,filtered) {
  if (!resources.length) return '<div class="empty">没有符合当前条件的资源</div>';
  return '<div class="table-wrap"><table class="data-table candidate-table"><thead><tr><th class="add-cell"></th><th>资源别名</th><th>类型</th><th>Provider</th><th>接入方式</th><th>Endpoint / 账号</th><th>状态</th><th>操作</th></tr></thead><tbody>' +
    resources.map((x) => {
      const st=resourceStatus(x), selected=candidateSelected(rule,x);
      const reason=filtered?'不匹配当前客户端类型（仅允许 '+clientTypeLabel(EDIT.client_type)+' 原生资源）':'';
      return '<tr class="'+(filtered?'filtered':'')+'"><td><button class="add-btn" '+(filtered||selected?'disabled':'')+' onclick="addCand('+ri+',\''+esc(x.id)+'\')">＋</button></td><td><b>'+esc(resourceLabel(x))+'</b></td><td><span class="tag '+(String(x.kind).toLowerCase()==='oauth'?'oauth':'api')+'">'+esc(x.kind||'-')+'</span></td><td><span class="tag">'+esc(x.provider||'-')+'</span></td><td>'+esc(String(x.kind).toLowerCase()==='oauth'?'账号':'URL')+'</td><td class="mono">'+esc(resourceEndpoint(x))+'</td><td><span class="tag '+(filtered?'filtered':st.ok?'ok':'')+'">'+esc(filtered?'已过滤':st.label)+'</span></td><td>'+(filtered?'<span class="muted small" title="'+esc(reason)+'">过滤原因 ⓘ</span>':selected?'<span class="muted small">已添加</span>':'<button class="btn small" onclick="addCand('+ri+',\''+esc(x.id)+'\')">添加</button>')+'</td></tr>';
    }).join('') + '</tbody></table></div>';
}

function selectedCandidatesTable(r,ri,f) {
  const xs=r.candidates||[];
  if (!xs.length) return '<div class="empty">尚未选择候选资源，请在下方展开资源列表添加。</div>';
  return '<div class="table-wrap"><table class="data-table selected-table"><thead><tr><th>顺序</th><th>资源别名</th><th>类型</th><th>Provider</th><th>Endpoint / 账号</th>'+(f.priority?'<th>Priority</th>':'')+(f.weight?'<th>Weight</th>':'')+'<th>模型重写</th><th>状态</th><th>操作</th></tr></thead><tbody>'+
    xs.map((c,ci) => {
      const res=(SNAP.resources||[]).find((x)=>x.id===c.resource_id);
      const allowed=!res||resourceAllowedByAffinity(res);
      const ep=res?resourceEndpoint(res):(c.auth_index||'当前资源不可见');
      const name = res ? resourceLabel(res) : (c.name||'-');
      return '<tr draggable="true" ondragstart="dragCandidate('+ri+','+ci+')" ondragover="event.preventDefault()" ondrop="dropCandidate('+ri+','+ci+')" '+(!allowed?'class="filtered"':'')+'><td><div class="candidate-order"><span class="drag" title="拖动排序">⋮⋮</span><button class="btn small" id="candidate-up-'+ri+'-'+ci+'" aria-label="上移候选 '+esc(name)+'" '+(ci===0?'disabled':'')+' onclick="moveCandidate('+ri+','+ci+',-1)">↑</button><button class="btn small" id="candidate-down-'+ri+'-'+ci+'" aria-label="下移候选 '+esc(name)+'" '+(ci===xs.length-1?'disabled':'')+' onclick="moveCandidate('+ri+','+ci+',1)">↓</button></div></td><td><b>'+esc(name)+'</b>'+(!res?'<div class="warn small">当前资源不可见</div>':!allowed?'<div class="warn small">当前 Affinity 不允许</div>':'')+'</td><td><span class="tag '+(String(c.resource_kind||res?.kind).toLowerCase()==='oauth'?'oauth':'api')+'">'+esc(c.resource_kind||res?.kind||'-')+'</span></td><td><span class="tag">'+esc(c.provider||res?.provider||'-')+'</span></td><td class="mono">'+esc(ep)+'</td>'+(f.priority?'<td><input aria-label="'+esc(name)+' Priority" type="number" value="'+(c.priority??100)+'" oninput="EDIT.rules['+ri+'].candidates['+ci+'].priority=this.value===\'\'?100:Number(this.value)"></td>':'')+(f.weight?'<td><input aria-label="'+esc(name)+' Weight" type="number" min="1" value="'+(c.weight||1)+'" oninput="EDIT.rules['+ri+'].candidates['+ci+'].weight=+this.value||1"></td>':'')+'<td><input class="model-override" aria-label="'+esc(name)+' 模型重写" value="'+esc(c.override_model||'')+'" placeholder="留空 = 沿用请求模型" oninput="EDIT.rules['+ri+'].candidates['+ci+'].override_model=this.value"></td><td><div class="switch-line"><button type="button" id="candidate-toggle-'+ri+'-'+ci+'" class="switch '+(c.enabled!==false?'on':'')+'" role="switch" aria-checked="'+(c.enabled!==false)+'" aria-label="'+esc('启用候选 '+name)+'" onclick="toggleCandidate('+ri+','+ci+')"></button><span class="small muted">'+(c.enabled!==false?'启用':'停用')+'</span></div></td><td><button class="btn small danger" onclick="EDIT.rules['+ri+'].candidates.splice('+ci+',1);renderRules()">移除</button></td></tr>';
    }).join('')+'</tbody></table></div>';
}

function toggleCandidate(ri, ci) {
  const candidate = EDIT.rules[ri].candidates[ci];
  candidate.enabled = candidate.enabled === false;
  renderRules();
}

function moveCandidate(ri, ci, direction) {
  const candidates = EDIT.rules[ri].candidates;
  const target = ci + direction;
  if (target<0 || target>=candidates.length) return;
  const focused = document.activeElement?.id;
  let nextFocus;
  if (focused === 'candidate-up-'+ri+'-'+ci || focused === 'candidate-down-'+ri+'-'+ci) {
    const action = target === 0 ? 'down' : target === candidates.length-1 ? 'up' : direction < 0 ? 'up' : 'down';
    nextFocus = 'candidate-'+action+'-'+ri+'-'+target;
  }
  [candidates[ci],candidates[target]] = [candidates[target],candidates[ci]];
  renderRules(nextFocus);
}

function fromRes(r) {
  return {id:'',name:resourceLabel(r),resource_id:r.id,resource_kind:r.kind,provider:r.provider,auth_id:r.auth_id||'',auth_index:r.auth_index||'',override_model:'',enabled:true,priority:100,weight:1};
}

function addCand(ri,id) {
  const res=(SNAP.resources||[]).find((x)=>x.id===id);
  if (!res || !resourceAllowedByAffinity(res)) return;
  const rule=EDIT.rules[ri];
  rule.candidates=rule.candidates||[];
  if (!candidateSelected(rule,res)) rule.candidates.push(fromRes(res));
  renderRules();
}

function dragCandidate(ri,ci) { if (!POLICY_SAVING) DRAG_CAND={ri,ci}; }
function dropCandidate(ri,ci) {
  if (POLICY_SAVING) return;
  if (!DRAG_CAND || DRAG_CAND.ri!==ri || DRAG_CAND.ci===ci) return;
  const a=EDIT.rules[ri].candidates;
  const [item]=a.splice(DRAG_CAND.ci,1);
  a.splice(ci,0,item);
  DRAG_CAND=null;
  renderRules();
}

async function savePolicy() {
  if (POLICY_SAVING || !EDIT) return;
  POLICY_SAVING = true;
  DRAG_CAND = null;
  $('policyEditorFields').disabled = true;
  $('savePolicyButton').disabled = true;
  $('savePolicyButton').textContent = '保存中…';
  try {
    const sel=$('pKey');
    const hint=sel.options[sel.selectedIndex]?.dataset.hint||'';
    EDIT.name=$('pName').value.trim();
    EDIT.key_fingerprint=sel.value;
    EDIT.key_hint=hint;
    EDIT.client_type=$('pClientType').value;
    if (EDIT.client_affinity!=='off' && EDIT.client_affinity!=='strict') {
      throw new Error('当前 Client Affinity 模式不受支持，请明确选择关闭或 Strict');
    }
    EDIT.client_provider=EDIT.client_affinity==='strict'?EDIT.client_type:'';
    SNAP=await api({action:'save_policy',payload:JSON.stringify(EDIT)});
    EDIT=null;
    renderAll();
    showView('policy');
  } catch(e) {
    alert('保存失败：'+(e.message||e));
  } finally {
    POLICY_SAVING = false;
    $('policyEditorFields').disabled = false;
    $('savePolicyButton').disabled = false;
    $('savePolicyButton').textContent = '保存 Policy';
  }
}

async function delPolicy(fp) {
  if (POLICY_PENDING.size || POLICY_SAVING) return;
  if (!confirm('删除这套 API Key Policy？')) return;
  SNAP=await api({action:'delete_policy',fingerprint:fp});
  renderAll();
}

function diagnose(fp) {
  DIAG=fp;
  $('diagModel').value='';
  $('diagModal').classList.add('open');
  runDiag();
}
function closeDiag(){ $('diagModal').classList.remove('open'); }

async function runDiag() {
  const d=await api({action:'diagnose',fingerprint:DIAG,model:$('diagModel').value});
  if (!d.ok) { $('diagBody').innerHTML='<div class="note">'+esc(d.error)+'</div>'; return; }
  if (!d.matched) { $('diagBody').innerHTML='<div class="note">决策：'+esc(d.decision)+'。'+esc(d.note||'')+'</div>'; return; }
  const f=strategyFields(d.strategy);
  let h='<div class="oknote">决策：'+esc(d.decision)+'；Rule：'+esc(d.rule.name)+'；Strategy：'+esc(d.strategy)+'</div><div class="table-wrap" style="margin-top:12px"><table class="data-table"><thead><tr><th>#</th><th>候选</th><th>Provider</th>'+(f.priority?'<th>Priority</th>':'')+(f.weight?'<th>Weight</th>':'')+'<th>AuthIndex</th><th>健康</th><th>当前路由</th></tr></thead><tbody>';
  for (const c of d.ranked_candidates||[]) {
    const health=c.health||{}, state=String(health.state||'closed').toUpperCase();
    const route=c.effective?'✓ 当前首选':(c.selectable?'可选':'跳过');
    h+='<tr><td>'+c.order+'</td><td>'+esc(c.name)+'</td><td>'+esc(c.provider)+'</td>'+(f.priority?'<td>'+c.priority+'</td>':'')+(f.weight?'<td>'+c.weight+'</td>':'')+'<td class="mono">'+esc(c.auth_index)+'</td><td>'+esc(state)+'</td><td>'+esc(route)+'</td></tr>';
  }
  h+='</tbody></table></div><h3>真实验证</h3><div class="diag">'+esc(d.curl||'')+'</div>';
  $('diagBody').innerHTML=h;
}

function setResourceFilter(key, value) {
  if (!['type','provider','status'].includes(key)) return;
  RESOURCE_FILTERS[key] = value;
  renderResources();
}

function resetResourceFilters() {
  RESOURCE_FILTERS = {type:'all',provider:'all',status:'all'};
  $('resourceSearch').value = '';
  renderResources();
}

function filteredResources(resources, filters, query) {
  const q = String(query || '').trim().toLowerCase();
  return resources.filter((r)=>{
    const st=resourceStatus(r);
    if (filters.type!=='all' && String(r.kind).toLowerCase()!==filters.type.toLowerCase()) return false;
    if (filters.provider!=='all' && String(r.provider)!==filters.provider) return false;
    if (filters.status==='active' && !st.ok) return false;
    if (filters.status==='disabled' && st.ok) return false;
    return !q || [r.alias,r.display_name,r.provider,r.base_url,r.key_hint,r.auth_index].some((v)=>String(v||'').toLowerCase().includes(q));
  });
}

function renderResourceFilters() {
  const focused = document.activeElement?.id;
  const resources = SNAP.resources || [];
  const ps = [...new Set(resources.map((r)=>r.provider).filter(Boolean))].sort();
  if (RESOURCE_FILTERS.provider!=='all' && !ps.includes(RESOURCE_FILTERS.provider)) RESOURCE_FILTERS.provider='all';
  const groups = [['type','类型',[['all','全部'],['API','API'],['OAuth','OAuth']]],['provider','Provider',[['all','全部'],...ps.map((p)=>[p,p])]],['status','状态',[['all','全部'],['active','正常'],['disabled','不可用']]]];
  $('resourceFilters').innerHTML = groups.map(([key,label,options])=>'<div class="filter-row"><span class="filter-label">'+label+'</span><div class="filter-options" role="group" aria-label="'+label+'筛选">'+options.map(([value,name],index)=>{
    const count = filteredResources(resources, {...RESOURCE_FILTERS,[key]:value}, $('resourceSearch')?.value).length;
    return filterChip(name,value,RESOURCE_FILTERS[key]===value,count,'setResourceFilter('+esc(JSON.stringify(key))+','+esc(JSON.stringify(value))+')','resource-filter-'+key+'-'+index);
  }).join('')+'</div></div>').join('');
  restoreControlFocus(focused);
}

function renderResources() {
  if (!$('resourceTable') || !SNAP) return;
  renderResourceFilters();
  const xs=filteredResources(SNAP.resources||[],RESOURCE_FILTERS,$('resourceSearch')?.value);
  $('resourceCount').textContent = '显示 '+xs.length+' / '+(SNAP.resources||[]).length+' 个资源';
  $('resourceReset').hidden = !String($('resourceSearch')?.value||'') && Object.values(RESOURCE_FILTERS).every((x)=>x==='all');
  if (!xs.length) { $('resourceTable').innerHTML='<div class="empty">没有符合条件的上游资源<div style="margin-top:10px"><button class="btn small" onclick="resetResourceFilters()">清除筛选</button></div></div>'; return; }
  $('resourceTable').innerHTML='<div class="table-wrap"><table class="data-table"><thead><tr><th>资源别名</th><th>类型</th><th>Provider</th><th>Endpoint / 账号</th><th>Prefix</th><th>客户端适配</th><th>状态</th><th>操作</th></tr></thead><tbody>'+
    xs.map((r)=>{
      const st=resourceStatus(r);
      const compat=String(r.kind).toLowerCase()==='oauth'?'通用 / 转换':clientTypeLabel(r.provider);
      return '<tr><td><b>'+esc(resourceLabel(r))+'</b>'+(r.alias?'<div class="muted small">默认：'+esc(r.display_name||r.id)+'</div>':'')+'</td><td><span class="tag '+(String(r.kind).toLowerCase()==='oauth'?'oauth':'api')+'">'+esc(r.kind||'-')+'</span></td><td><span class="tag">'+esc(r.provider||'-')+'</span></td><td class="mono">'+esc(resourceEndpoint(r))+'</td><td>'+esc(r.prefix||'—')+'</td><td>'+esc(compat)+'</td><td><span class="dot '+(st.ok?'':'warn')+'"></span>'+esc(st.label)+'</td><td><button class="btn small" onclick="openResourceDrawer(\''+esc(r.id)+'\')">查看 / 修改</button></td></tr>';
    }).join('')+'</tbody></table></div>';
}

function openResourceDrawer(id) {
  const r=(SNAP.resources||[]).find((x)=>x.id===id);
  if (!r) return;
  const st=resourceStatus(r);
  $('resourceDrawerBody').innerHTML='<div class="info" style="margin-top:16px">KCR 从 CPA 实时读取凭据与 Endpoint；资源别名仅保存在 KCR，用于 Policy 选择和路由可读性，不修改 CPA config。</div>'+
    '<div class="kv"><label>资源别名</label><input id="resourceAliasInput" value="'+esc(r.alias||'')+'" placeholder="'+esc(r.display_name||r.id)+'"><div class="row" style="margin-top:8px"><button class="btn primary small" onclick="saveResourceAlias(\''+esc(r.id)+'\')">保存别名</button>'+(r.alias?'<button class="btn small" onclick="resetResourceAlias(\''+esc(r.id)+'\')">恢复默认</button>':'')+'</div><div class="muted small" style="margin-top:6px">默认名称：'+esc(r.display_name||r.id)+'</div></div>'+
    '<div class="grid2" style="margin-top:14px"><div class="kv" style="margin:0"><label>资源类型</label><div class="value">'+esc(r.kind||'-')+'</div></div><div class="kv" style="margin:0"><label>Provider</label><div class="value">'+esc(r.provider||'-')+'</div></div></div>'+
    '<div class="kv"><label>Endpoint / 账号</label><div class="value mono">'+esc(resourceEndpoint(r))+'</div></div>'+
    '<div class="grid2" style="margin-top:14px"><div class="kv" style="margin:0"><label>Prefix</label><div class="value">'+esc(r.prefix||'—')+'</div></div><div class="kv" style="margin:0"><label>状态</label><div class="value">'+esc(st.label)+'</div></div></div>'+
    '<div class="kv"><label>AuthIndex</label><div class="value mono">'+esc(r.auth_index||'—')+'</div></div>'+
    '<div class="kv"><label>客户端适配</label><div class="value">'+esc(String(r.kind).toLowerCase()==='oauth'?'通用 / 协议转换':clientTypeLabel(r.provider))+'</div></div>'+
    ((r.models||[]).length?'<div class="kv"><label>模型列表（只读）</label><div class="value">'+(r.models||[]).map((m)=>'<div>● '+esc(m)+'</div>').join('')+'</div></div>':'');
  $('resourceBackdrop').classList.add('open');
  $('resourceDrawer').classList.add('open');
}

async function saveResourceAlias(id) {
  try {
    const alias = $('resourceAliasInput').value.trim();
    SNAP = await api({action:'save_resource_alias',payload:JSON.stringify({resource_id:id,alias})});
    renderAll();
    openResourceDrawer(id);
  } catch (e) {
    alert('保存资源别名失败：' + (e.message || e));
  }
}

async function resetResourceAlias(id) {
  try {
    SNAP = await api({action:'save_resource_alias',payload:JSON.stringify({resource_id:id,alias:''})});
    renderAll();
    openResourceDrawer(id);
  } catch (e) {
    alert('恢复默认别名失败：' + (e.message || e));
  }
}

function closeResourceDrawer(){ $('resourceBackdrop').classList.remove('open'); $('resourceDrawer').classList.remove('open'); }

function renderObs() {
  const o=SNAP.observability||{};
  $('oMemory').value=String(o.memory_enabled!==false);
  $('oMemoryLimit').value=o.memory_limit||500;
  $('oLog').value=String(o.log_enabled!==false);
  $('oLogLevel').value=o.log_level||'info';
  $('oSQLite').value=String(!!o.sqlite_enabled);
  $('oSQLitePath').value=o.sqlite_path||'key-chain-router.db';
  $('oRetention').value=o.sqlite_retention_days||30;
  $('oMaxRows').value=o.sqlite_max_rows||100000;
  $('oHeaders').value=String(!!o.response_headers);
  const h=SNAP.sqlite_status||{};
  if (!o.sqlite_enabled) $('sqliteHealth').innerHTML='<div class="muted small">SQLite 当前未开启。</div>';
  else {
    const bits=['数据库：'+(h.path||o.sqlite_path||'-'),'Events：'+(h.events||0),'Attempts：'+(h.attempts||0),h.journal_mode?'Journal：'+h.journal_mode:'',(h.last_write_at||h.last_persisted_at)?'最后写入：'+(h.last_write_at||h.last_persisted_at):'最后写入：暂无'].filter(Boolean);
    $('sqliteHealth').innerHTML='<div class="'+(h.active?'oknote':'note')+'"><b>SQLite '+(h.active?(h.write_healthy===false?'运行中（查询回退 Memory）':'运行中'):'未运行')+'</b><div class="small" style="margin-top:5px;word-break:break-all">'+esc(bits.join(' · '))+'</div>'+(h.last_error?'<div class="red small" style="margin-top:5px">'+esc(h.last_error)+'</div>':'')+'</div>';
  }
}

async function saveObs() {
  const o={memory_enabled:$('oMemory').value==='true',memory_limit:+$('oMemoryLimit').value||500,log_enabled:$('oLog').value==='true',log_level:$('oLogLevel').value,sqlite_enabled:$('oSQLite').value==='true',sqlite_path:$('oSQLitePath').value,sqlite_retention_days:+$('oRetention').value||30,sqlite_max_rows:+$('oMaxRows').value||100000,response_headers:$('oHeaders').value==='true'};
  SNAP=await api({action:'save_observability',payload:JSON.stringify(o)});
  renderAll();
  alert('已保存');
}
async function clearMemory(){ SNAP=await api({action:'clear_memory'}); renderAll(); await Promise.all([loadDashboard(), CURRENT_VIEW==='usage'?loadEvents(true):Promise.resolve()]); }

function eventParams() {
  const q={
    action:'events',
    route_only:'true',
    limit:String(ROUTE_PAGE_SIZE),
    offset:String((ROUTE_PAGE-1)*ROUTE_PAGE_SIZE),
    since:$('eventSince')?.value||'1h'
  };
  for (const [k,id] of [['policy','eventPolicy'],['provider','eventProvider'],['model','eventModel']]) {
    const v=$(id)?.value;
    if (v && v!=='all') q[k]=v;
  }
  return q;
}

async function loadEvents(resetPage=false) {
  if (!$('events')) return;
  if (resetPage) ROUTE_PAGE=1;
  try {
    const params=eventParams();
    const statsParams=routeStatsParams(params.since);
    const [events,stats]=await Promise.all([api(params),api(statsParams)]);
    EVENT_DATA=events;
    ROUTE_STATS=stats;
    renderEventFacets(EVENT_DATA.facets||{});
    renderEventStats(ROUTE_STATS.stats||{});
    renderRouteAnalysis();
    renderEvents();
    renderEventPager();
  } catch(e) {
    $('events').innerHTML='<div class="note">查询失败：'+esc(e.message||e)+'</div>';
  }
}

function setFacet(id,values,allLabel) {
  const el=$(id); if(!el) return;
  const current=el.value||'all';
  el.innerHTML='<option value="all">'+esc(allLabel)+'</option>'+(values||[]).map((v)=>'<option value="'+esc(v)+'">'+esc(v)+'</option>').join('');
  if ([...el.options].some((o)=>o.value===current)) el.value=current;
}
function renderEventFacets(f){
  setFacet('eventPolicy',f.policies,'全部');
  setFacet('eventProvider',f.providers,'全部');
  setFacet('eventModel',f.models,'全部');
}
function fmtNumber(v,d=0){const n=Number(v||0);return Number.isFinite(n)?n.toFixed(d):'0';}

function renderEventStats(s) {
  const total=Number(s.total||0), direct=Number(s.direct||0), fallback=Number(s.fallback||0), failed=Number(s.failed||0);
  const cards=[
    ['总请求',total,'当前筛选'],
    ['直接成功',direct,pct(direct,total)],
    ['Fallback',fallback,pct(fallback,total)],
    ['失败',failed,pct(failed,total)]
  ];
  $('eventStats').innerHTML=cards.map(([n,v,h],i)=>'<div class="stat"><div class="muted small">'+esc(n)+'</div><b>'+esc(v)+'</b><div class="hint '+(i===2?'warn':i===3?'red':'green')+'">'+esc(h)+'</div></div>').join('');
}

function renderRouteAnalysis() {
  if (!ROUTE_STATS) return;
  renderCandidateHits(ROUTE_STATS.candidate_hits||[]);
  renderFallbackPaths(ROUTE_STATS.fallback_paths||[]);
  renderFailureReasons(ROUTE_STATS.failure_reasons||[]);
  renderAttemptsDistribution(ROUTE_STATS.attempts||[], Number(ROUTE_STATS.stats?.total||0));
}

function renderCandidateHits(xs) {
  if (!xs.length) { $('candidateHitChart').innerHTML='<div class="empty">暂无候选命中数据</div>'; return; }
  $('candidateHitChart').innerHTML='<div class="bar-list">'+xs.slice(0,8).map((x)=>{
    const total=Number(x.direct||0)+Number(x.fallback||0)+Number(x.failed||0);
    const d=total?Number(x.direct||0)*100/total:0, f=total?Number(x.fallback||0)*100/total:0, e=total?Number(x.failed||0)*100/total:0;
    return '<div class="bar-row"><div><b>'+esc(x.name)+'</b></div><div class="bar-track" title="首选 '+x.direct+' · Fallback '+x.fallback+' · 失败 '+x.failed+'"><span class="bar-direct" style="width:'+d+'%"></span><span class="bar-fallback" style="width:'+f+'%"></span><span class="bar-failed" style="width:'+e+'%"></span></div><div class="bar-values">'+fmtNumber(d,0)+'% / '+fmtNumber(f,0)+'%</div></div>';
  }).join('')+'</div><div class="legend" style="margin-top:12px"><span><i style="background:#32bd82"></i>首选命中</span><span><i style="background:#f5bb58"></i>Fallback 命中</span><span><i style="background:#ee7773"></i>最终失败</span></div>';
}

function renderFallbackPaths(xs) {
  if (!xs.length) { $('fallbackPathChart').innerHTML='<div class="empty">当前筛选没有 Fallback</div>'; return; }
  const max=Math.max(1,...xs.map((x)=>Number(x.count||0)));
  $('fallbackPathChart').innerHTML='<div class="bar-list">'+xs.slice(0,8).map((x)=>
    '<div class="bar-row"><div class="ellipsis" title="'+esc(x.name)+'"><b>'+esc(x.name)+'</b></div><div class="bar-track"><span class="bar-single" style="width:'+(Number(x.count||0)*100/max)+'%"></span></div><div class="bar-values">'+esc(x.count)+' · '+fmtNumber(x.share,1)+'%</div></div>'
  ).join('')+'</div>';
}

function renderFailureReasons(xs) {
  if (!xs.length) { $('failureReasonChart').innerHTML='<div class="empty">当前筛选没有失败请求</div>'; return; }
  const max=Math.max(1,...xs.map((x)=>Number(x.count||0)));
  $('failureReasonChart').innerHTML='<div class="bar-list">'+xs.slice(0,7).map((x)=>
    '<div class="failure-row"><div>'+esc(x.name)+'</div><div class="bar-track"><span class="failure-bar" style="width:'+(Number(x.count||0)*100/max)+'%"></span></div><b>'+esc(x.count)+'</b><span class="muted">'+fmtNumber(x.share,1)+'%</span></div>'
  ).join('')+'</div>';
}

function renderAttemptsDistribution(xs,total) {
  if (!xs.length || !total) { $('attemptsChart').innerHTML='<div class="empty">暂无 Attempts 数据</div>'; return; }
  const colors=['#2fc184','#f5bb58','#4f8bf7','#ef6a67','#94a3b8'];
  let acc=0;
  const stops=[];
  xs.forEach((x,i)=>{
    const p=Number(x.share||0), start=acc; acc+=p;
    stops.push(colors[i%colors.length]+' '+start+'% '+acc+'%');
  });
  if(acc<100) stops.push('#edf2f7 '+acc+'% 100%');
  const legend=xs.map((x,i)=>'<div class="donut-legend-row"><span class="donut-swatch" style="background:'+colors[i%colors.length]+'"></span><span class="grow">'+esc(x.name==='4+'?'4+ 次':x.name+' 次')+'</span><b>'+fmtNumber(x.share,0)+'%</b></div>').join('');
  $('attemptsChart').innerHTML='<div class="donut-wrap"><div class="donut" style="background:conic-gradient('+stops.join(',')+')"><div class="donut-center"><b>'+esc(total)+'</b><span class="muted small">总请求</span></div></div><div class="donut-legend">'+legend+'</div></div>';
}

function reasonLabel(r){return({no_matching_rule:'Policy 已配置，但 model 未命中任何 Rule，交给 CPA 默认路由',rule_cpa_default:'命中 Rule 明确配置为 CPA 默认路由',no_enabled_candidates:'命中 Rule，但没有启用候选',candidates_exhausted:'候选全部尝试后仍失败',policy_fallback_to_cpa:'Failover 规则转入 CPA 默认路由',policy_disabled:'该 API Key 的 KCR Policy 已停用',policy_lookup_miss:'检测到 Policy 查找异常',unsupported_client_affinity:'Client Affinity 模式不受支持，已 fail closed'})[r]||r||'';}
function statusLabel(a){if(a.status)return'HTTP '+a.status;if(a.error)return'ERROR';return'-';}
function candidateAliasForAttempt(a){
  const r=(SNAP.resources||[]).find((x)=>String(x.provider||'').toLowerCase()===String(a.provider||'').toLowerCase()&&String(x.auth_index||'')===String(a.auth_index||''));
  return r?resourceLabel(r):(a.candidate||'-');
}
function renderAttempt(e,a,i){const reasons=e.selection_reasons||[];const reason=reasons[i]||(i===0?'按当前策略排序后选择此候选':'按 Failover 策略选择后续候选');const detail=[a.provider,a.auth_index?'AuthIndex '+a.auth_index:'',a.model?'model '+a.model:'',Number.isFinite(Number(a.duration_ms))?a.duration_ms+' ms':''].filter(Boolean).join(' · ');return '<div class="attempt"><div class="row"><b>#'+(i+1)+' '+esc(candidateAliasForAttempt(a))+'</b><span class="tag">'+esc(statusLabel(a))+'</span><span class="muted small">'+esc(detail)+'</span></div><div class="reason"><b>选择原因：</b>'+esc(reason)+'</div>'+(a.error?'<div class="red small" style="margin-top:4px">'+esc(a.error)+'</div>':'')+'</div>';}
function fmtEventTime(at){if(!at)return'-';const d=new Date(String(at));if(Number.isNaN(d.getTime()))return String(at);const p=(n)=>String(n).padStart(2,'0');return p(d.getMonth()+1)+'-'+p(d.getDate())+' '+p(d.getHours())+':'+p(d.getMinutes())+':'+p(d.getSeconds());}
function eventOutcome(e){if(e.success===false)return'failed';if((e.attempts||[]).length>1||e.decision==='KCR_FALLBACK_TO_CPA')return'fallback';return'direct';}
function eventState(e){const o=eventOutcome(e);if(o==='failed')return{cls:'event-state-error',label:'失败'};if(o==='fallback')return{cls:'event-state-fallback',label:'Fallback'};return{cls:'event-state-ok',label:'成功'};}
function eventStatusText(e){if(e.status)return String(e.status);if(e.success===false)return'ERR';return'-';}
function eventRoutePath(e){
  const xs=(e.attempts||[]).map(candidateAliasForAttempt).filter(Boolean);
  if(!xs.length)return e.final||'-';
  return xs.filter((x,i)=>i===0||x!==xs[i-1]).join(' → ');
}
function toggleEvent(traceID){if(!traceID)return;if(EVENT_EXPANDED.has(traceID))EVENT_EXPANDED.delete(traceID);else EVENT_EXPANDED.add(traceID);renderEvents();}
function renderEventDetail(e){const eventReason=reasonLabel(e.reason);const primary=['时间 '+fmtEventTime(e.at),'Model '+(e.model||'-'),'Policy '+(e.policy_name||'-'),'Rule '+(e.rule_name||'-'),'Strategy '+(e.strategy||'-'),'路由路径 '+eventRoutePath(e),'状态 '+eventStatusText(e),'耗时 '+(e.duration_ms||0)+' ms','Attempts '+((e.attempts||[]).length)].join(' · ');return '<div class="event-detail"><div class="small">'+esc(primary)+'</div>'+(eventReason?'<div style="margin-top:7px"><b>路由结果：</b>'+esc(eventReason)+'</div>':'')+'<div class="muted small" style="margin-top:6px">'+esc(e.trace_id?'Trace '+e.trace_id:'')+'</div><div style="font-weight:800;margin-top:12px">候选尝试</div><div class="attempts">'+((e.attempts||[]).length?(e.attempts||[]).map((a,i)=>renderAttempt(e,a,i)).join(''):'<div class="muted small">没有候选尝试记录</div>')+'</div></div>';}

function renderEvents(){
  if(!EVENT_DATA)return;
  const xs=EVENT_DATA.events||[], source=EVENT_DATA.source==='sqlite'?'SQLite':'Memory';
  $('eventResultMeta').textContent='数据源 '+source+' · 共 '+(EVENT_DATA.matched||0)+' 条';
  if(!xs.length){$('events').innerHTML='<div class="empty">当前条件下暂无路由记录</div>';return;}
  const rows=xs.map((e)=>{
    const key=e.trace_id||e.at, expanded=EVENT_EXPANDED.has(key), state=eventState(e), attempts=(e.attempts||[]).length, path=eventRoutePath(e);
    const row='<tr class="event-row '+(expanded?'expanded':'')+'" onclick="toggleEvent(\''+esc(key)+'\')"><td class="event-toggle"><span class="chevron">'+(expanded?'▾':'›')+'</span></td><td class="event-time mono">'+esc(fmtEventTime(e.at))+'</td><td><div class="event-primary ellipsis">'+esc(e.model||'-')+'</div></td><td><div class="event-primary">'+esc(e.policy_name||'-')+'</div></td><td class="route-path-cell"><div class="ellipsis" title="'+esc(path)+'">'+esc(path)+'</div></td><td><span class="event-state '+state.cls+'"><i></i>'+esc(state.label)+'</span></td><td><span class="attempt-count '+(attempts>1?'multi':'')+'">'+attempts+'</span></td><td>'+esc(e.duration_ms||0)+' ms</td></tr>';
    return row+(expanded?'<tr class="event-detail-row"><td colspan="8">'+renderEventDetail(e)+'</td></tr>':'');
  }).join('');
  $('events').innerHTML='<div class="event-table-wrap"><table class="event-table route-record-table"><thead><tr><th></th><th>时间</th><th>Model</th><th>Policy</th><th>路由路径</th><th>结果</th><th>Attempts</th><th>耗时</th></tr></thead><tbody>'+rows+'</tbody></table></div>';
}

function renderEventPager(){
  const total=Number(EVENT_DATA?.matched||0);
  const pages=Math.max(1,Math.ceil(total/ROUTE_PAGE_SIZE));
  if(ROUTE_PAGE>pages)ROUTE_PAGE=pages;
  const items=[];
  const add=(p)=>{if(p>=1&&p<=pages&&!items.includes(p))items.push(p);};
  add(1); for(let p=ROUTE_PAGE-2;p<=ROUTE_PAGE+2;p++)add(p); add(pages); items.sort((a,b)=>a-b);
  let last=0, html='<button '+(ROUTE_PAGE<=1?'disabled':'')+' onclick="goRoutePage('+(ROUTE_PAGE-1)+')">‹</button>';
  for(const p of items){if(last&&p-last>1)html+='<span class="muted">…</span>';html+='<button class="'+(p===ROUTE_PAGE?'active':'')+'" onclick="goRoutePage('+p+')">'+p+'</button>';last=p;}
  html+='<button '+(ROUTE_PAGE>=pages?'disabled':'')+' onclick="goRoutePage('+(ROUTE_PAGE+1)+')">›</button><span class="muted small">'+ROUTE_PAGE_SIZE+' 条/页</span>';
  $('eventPager').innerHTML=html;
}
async function goRoutePage(page){ROUTE_PAGE=Math.max(1,page);await loadEvents(false);}

load().catch((e)=>{
  const target=$('dashboardPage')||document.body;
  target.innerHTML='<div class="note">加载失败：'+esc(e.message||e)+'</div>';
});
