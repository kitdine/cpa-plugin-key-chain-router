let SNAP = null;
let EDIT = null;
let DIAG = null;
let EVENT_DATA = null;
let EVENT_EXPANDED = new Set();
let RULE_SEARCH = {};
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
  if (name === 'usage') loadEvents();
  if (name === 'resources') renderResources();
  window.scrollTo({ top: 0, behavior: 'instant' });
}

async function load() {
  SNAP = await api({ action: 'snapshot' });
  renderAll();
  await loadEvents();
}

function renderAll() {
  $('versionBadge').textContent = 'v' + (SNAP.version || 'dev');
  renderDashboard();
  renderPolicies();
  renderResources();
  renderObs();
}

function renderDashboard() {
  const policies = SNAP.policies || [];
  const resources = SNAP.resources || [];
  const oauth = resources.filter((r) => String(r.kind || '').toLowerCase() === 'oauth').length;
  const strict = policies.filter((p) => p.client_affinity === 'strict').length;
  const cards = [
    ['下游 API Key', (SNAP.downstream_keys || []).length, 'CPA 原生认证入口'],
    ['Policy', policies.length, strict + ' 个启用 Client Affinity'],
    ['上游资源', resources.length, oauth + ' 个 OAuth'],
    ['配置状态', SNAP.config_error ? '异常' : '正常', SNAP.config_path || '未定位 config.yaml']
  ];
  $('dashboardStats').innerHTML = cards.map(([n,v,h]) =>
    '<div class="stat"><div class="muted small">' + esc(n) + '</div><b>' + esc(v) + '</b><div class="hint">' + esc(h) + '</div></div>'
  ).join('');

  $('dashboardPolicies').innerHTML = policies.length ? policies.slice(0,6).map((p) =>
    '<div class="policy-row"><div class="row"><div class="grow"><h3>' + esc(p.name) + '</h3><div class="policy-meta"><span>Key ' + esc(p.key_hint) + '</span><span>' + (p.rules || []).length + ' 条规则</span><span>客户端 ' + esc(clientTypeLabel(p.client_type || p.client_provider || '-')) + '</span><span>' + (p.client_affinity === 'strict' ? 'Affinity Strict' : 'Affinity Off') + '</span></div></div><button class="btn small" onclick="openPolicy(\'' + esc(p.key_fingerprint) + '\')">编辑</button></div></div>'
  ).join('') : '<div class="empty">尚未创建 Policy</div>';

  const grouped = {};
  resources.forEach((r) => {
    const k = String(r.kind || 'unknown');
    grouped[k] = (grouped[k] || 0) + 1;
  });
  $('dashboardResources').innerHTML = '<div class="data-table"><div class="row" style="justify-content:space-between;padding:8px 0"><span>API Provider</span><b>' + (grouped.API || 0) + '</b></div><div class="row" style="justify-content:space-between;padding:8px 0"><span>OAuth</span><b>' + (grouped.OAuth || 0) + '</b></div><div class="row" style="justify-content:space-between;padding:8px 0"><span>当前可见资源</span><b>' + resources.length + '</b></div></div><div style="margin-top:12px"><button class="btn small" onclick="showView(\'resources\')">查看全部资源</button></div>';

  $('envNotice').innerHTML = SNAP.config_error
    ? '<div class="note" style="margin-top:14px">CPA 配置读取异常：' + esc(SNAP.config_error) + '</div>'
    : '<div class="info" style="margin-top:14px">当前 CPA 配置：<span class="mono">' + esc(SNAP.config_path || '-') + '</span></div>';
}

function renderPolicies() {
  const ps = SNAP.policies || [];
  if (!ps.length) {
    $('policies').innerHTML = '<div class="empty">还没有 Policy。每个下游 API Key 最多创建一个。<div style="margin-top:12px"><button class="btn primary" onclick="openPolicy()">＋ 新建 Policy</button></div></div>';
    return;
  }
  $('policies').innerHTML = ps.map((p) => {
    const strict = p.client_affinity === 'strict';
    const type = p.client_type || p.client_provider || '-';
    return '<div class="policy-row"><div class="row"><div class="grow"><h3>' + esc(p.name) + ' <span class="' + (p.enabled ? 'green' : 'muted') + '">' + (p.enabled ? '● 启用' : '● 停用') + '</span></h3><div class="policy-meta"><span>Key ' + esc(p.key_hint) + '</span><span>客户端：' + esc(clientTypeLabel(type)) + '</span><span>' + (p.rules || []).length + ' 条模型规则</span><span class="tag ' + (strict ? 'ok' : '') + '">Client Affinity ' + (strict ? 'Strict' : 'Off') + '</span></div></div><button class="btn small" onclick="diagnose(\'' + esc(p.key_fingerprint) + '\')">诊断</button><button class="btn small" onclick="openPolicy(\'' + esc(p.key_fingerprint) + '\')">编辑</button><button class="btn small danger" onclick="delPolicy(\'' + esc(p.key_fingerprint) + '\')">删除</button></div></div>';
  }).join('');
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
  const found = fp ? (SNAP.policies || []).find((x) => x.key_fingerprint === fp) : null;
  EDIT = found ? JSON.parse(JSON.stringify(found)) : {
    name:'', key_fingerprint:'', key_hint:'', enabled:true,
    client_type:'', client_provider:'', client_affinity:'off', rules:[defaultRule()]
  };
  RULE_SEARCH = {};
  EDIT.client_affinity = EDIT.client_affinity || 'off';
  EDIT.client_type = EDIT.client_type || EDIT.client_provider || '';
  $('policyTitle').textContent = fp ? '编辑 Policy' : '新建 Policy';
  $('pName').value = EDIT.name || '';
  $('pEnabled').value = String(EDIT.enabled !== false);
  const keys = availableKeys(fp);
  $('pKey').innerHTML = keys.map((k) => '<option value="' + esc(k.fingerprint) + '" data-hint="' + esc(k.hint) + '">' + esc(k.hint) + '</option>').join('');
  if (EDIT.key_fingerprint) $('pKey').value = EDIT.key_fingerprint;
  renderClientTypes();
  renderAffinityState();
  renderRules();
  showView('policyEditor');
}

function closePolicy() {
  EDIT = null;
  RULE_SEARCH = {};
  showView('policy');
}

function renderAffinityState() {
  if (!EDIT) return;
  const supported = EDIT.client_affinity === 'off' || EDIT.client_affinity === 'strict';
  const strict = EDIT.client_affinity === 'strict';
  $('pAffinityToggle').classList.toggle('on', strict);
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
  EDIT.rules.push(defaultRule());
  renderRules();
}

function moveRule(i,d) {
  const j=i+d;
  if (j<0 || j>=EDIT.rules.length) return;
  [EDIT.rules[i],EDIT.rules[j]]=[EDIT.rules[j],EDIT.rules[i]];
  renderRules();
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

function renderRules() {
  if (!EDIT) return;
  const rules = EDIT.rules || [];
  if (!rules.length) {
    $('rules').innerHTML = '<div class="empty" style="margin-top:14px">尚无模型规则。<div style="margin-top:10px"><button class="btn" onclick="addRule()">＋ 新增规则</button></div></div>';
    return;
  }
  $('rules').innerHTML = rules.map((r,ri) => {
    r.failover = r.failover || defaultRule().failover;
    const query = String(RULE_SEARCH[ri] || '').toLowerCase();
    const resources = (SNAP.resources || []).filter((x) => {
      if (!query) return true;
      return [x.display_name,x.provider,x.base_url,x.key_hint,x.auth_index].some((v) => String(v || '').toLowerCase().includes(query));
    });
    const available = resources.filter(resourceAllowedByAffinity);
    const filtered = resources.filter((x) => !resourceAllowedByAffinity(x));
    const f = strategyFields(r.strategy);
    let h = '<div class="rule-card"><div class="rule-head"><span class="rule-num">' + (ri+1) + '</span><b>' + esc(r.name || '规则') + '</b><div class="rule-actions"><button class="btn small" onclick="moveRule(' + ri + ',-1)">↑</button><button class="btn small" onclick="moveRule(' + ri + ',1)">↓</button><button class="btn small danger" onclick="EDIT.rules.splice(' + ri + ',1);renderRules()">删除</button></div></div>';
    h += '<div class="grid3"><div><label>Rule 名称 *</label><input value="' + esc(r.name || '') + '" oninput="EDIT.rules['+ri+'].name=this.value"></div><div><label>匹配模型 *</label><input value="' + esc((r.models || ['*']).join(',')) + '" oninput="EDIT.rules['+ri+'].models=this.value.split(/[,;\\n]+/)"></div><div><label>路由策略 *</label><select onchange="EDIT.rules['+ri+'].strategy=this.value;renderRules()">' + strategyOptions(r.strategy) + '</select></div></div>';
    h += '<div class="advanced" style="margin-top:12px"><div class="grid3"><div><label>Sticky</label><select onchange="EDIT.rules['+ri+'].sticky_source=this.value"><option value="auto" '+((r.sticky_source||'auto')==='auto'?'selected':'')+'>自动</option><option value="session" '+(r.sticky_source==='session'?'selected':'')+'>Session</option><option value="header" '+(r.sticky_source==='header'?'selected':'')+'>指定 Header</option></select></div><div><label>候选耗尽后</label><select onchange="EDIT.rules['+ri+'].failover.exhausted=this.value"><option value="error" '+(r.failover.exhausted!=='cpa-default'?'selected':'')+'>返回错误</option><option value="cpa-default" '+(r.failover.exhausted==='cpa-default'?'selected':'')+'>转 CPA 默认</option></select></div><div><label>Max Attempts</label><input type="number" min="0" value="'+(r.failover.max_attempts||0)+'" oninput="EDIT.rules['+ri+'].failover.max_attempts=+this.value||0"></div></div></div>';

    if (r.strategy === 'cpa-default') {
      h += '<div class="oknote" style="margin-top:12px">命中此 Rule 时不执行候选资源，直接交给 CPA 默认路由。</div></div>';
      return h;
    }

    h += '<div class="candidate-section"><div class="candidate-title">候选资源</div><div class="toolbar"><input class="search" value="'+esc(RULE_SEARCH[ri]||'')+'" placeholder="搜索资源别名、Provider 或 URL…" oninput="RULE_SEARCH['+ri+']=this.value;renderRules()"><span class="muted small">客户端类型：'+esc(clientTypeLabel(EDIT.client_type))+' · Client Affinity '+(EDIT.client_affinity==='strict'?'已开启':'未开启')+'</span></div>';
    h += '<div class="info" style="margin-bottom:8px">'+(EDIT.client_affinity==='strict'?'严格模式：匹配客户端 Provider 的原生资源和全部 OAuth 可选；其他原生 API 资源保留展示但被过滤。':'Affinity 关闭：全部资源均可选择。')+'</div>';
    h += resourcePoolTable(r,ri,available,false);
    if (filtered.length) h += '<div class="candidate-title" style="margin-top:10px">已被 Client Affinity 过滤（'+filtered.length+'）</div>'+resourcePoolTable(r,ri,filtered,true);

    h += '<div class="candidate-title" style="margin-top:14px">已选候选资源（'+(r.candidates||[]).length+'）</div>';
    h += selectedCandidatesTable(r,ri,f);
    h += '</div></div>';
    return h;
  }).join('');
}

function resourcePoolTable(rule,ri,resources,filtered) {
  if (!resources.length) return '<div class="empty">没有符合当前条件的资源</div>';
  return '<div class="table-wrap"><table class="data-table candidate-table"><thead><tr><th class="add-cell"></th><th>资源别名</th><th>类型</th><th>Provider</th><th>接入方式</th><th>Endpoint / 账号</th><th>状态</th><th>操作</th></tr></thead><tbody>' +
    resources.map((x) => {
      const st=resourceStatus(x), selected=candidateSelected(rule,x);
      const reason=filtered?'不匹配当前客户端类型（仅允许 '+clientTypeLabel(EDIT.client_type)+' 原生资源）':'';
      return '<tr class="'+(filtered?'filtered':'')+'"><td><button class="add-btn" '+(filtered||selected?'disabled':'')+' onclick="addCand('+ri+',\''+esc(x.id)+'\')">＋</button></td><td><b>'+esc(x.display_name||x.id)+'</b></td><td><span class="tag '+(String(x.kind).toLowerCase()==='oauth'?'oauth':'api')+'">'+esc(x.kind||'-')+'</span></td><td><span class="tag">'+esc(x.provider||'-')+'</span></td><td>'+esc(String(x.kind).toLowerCase()==='oauth'?'账号':'URL')+'</td><td class="mono">'+esc(resourceEndpoint(x))+'</td><td><span class="tag '+(filtered?'filtered':st.ok?'ok':'')+'">'+esc(filtered?'已过滤':st.label)+'</span></td><td>'+(filtered?'<span class="muted small" title="'+esc(reason)+'">过滤原因 ⓘ</span>':selected?'<span class="muted small">已添加</span>':'<button class="btn small" onclick="addCand('+ri+',\''+esc(x.id)+'\')">添加</button>')+'</td></tr>';
    }).join('') + '</tbody></table></div>';
}

function selectedCandidatesTable(r,ri,f) {
  const xs=r.candidates||[];
  if (!xs.length) return '<div class="empty">尚未选择候选资源</div>';
  return '<div class="table-wrap"><table class="data-table selected-table"><thead><tr><th></th><th>资源别名</th><th>类型</th><th>Provider</th><th>Endpoint / 账号</th>'+(f.priority?'<th>Priority</th>':'')+(f.weight?'<th>Weight</th>':'')+'<th>模型重写</th><th>状态</th><th>操作</th></tr></thead><tbody>'+
    xs.map((c,ci) => {
      const res=(SNAP.resources||[]).find((x)=>x.id===c.resource_id);
      const allowed=!res||resourceAllowedByAffinity(res);
      const ep=res?resourceEndpoint(res):(c.auth_index||'当前资源不可见');
      return '<tr draggable="true" ondragstart="dragCandidate('+ri+','+ci+')" ondragover="event.preventDefault()" ondrop="dropCandidate('+ri+','+ci+')" '+(!allowed?'class="filtered"':'')+'><td class="drag">⋮⋮</td><td><b>'+esc(c.name||res?.display_name||'-')+'</b>'+(!res?'<div class="warn small">当前资源不可见</div>':!allowed?'<div class="warn small">当前 Affinity 不允许</div>':'')+'</td><td><span class="tag '+(String(c.resource_kind||res?.kind).toLowerCase()==='oauth'?'oauth':'api')+'">'+esc(c.resource_kind||res?.kind||'-')+'</span></td><td><span class="tag">'+esc(c.provider||res?.provider||'-')+'</span></td><td class="mono">'+esc(ep)+'</td>'+(f.priority?'<td><input type="number" value="'+(c.priority||100)+'" oninput="EDIT.rules['+ri+'].candidates['+ci+'].priority=+this.value||100"></td>':'')+(f.weight?'<td><input type="number" min="1" value="'+(c.weight||1)+'" oninput="EDIT.rules['+ri+'].candidates['+ci+'].weight=+this.value||1"></td>':'')+'<td><input value="'+esc(c.override_model||'')+'" placeholder="留空 = 沿用客户端请求模型" oninput="EDIT.rules['+ri+'].candidates['+ci+'].override_model=this.value"></td><td><select onchange="EDIT.rules['+ri+'].candidates['+ci+'].enabled=this.value===\'true\'"><option value="true" '+(c.enabled!==false?'selected':'')+'>● 启用</option><option value="false" '+(c.enabled===false?'selected':'')+'>停用</option></select></td><td><button class="btn small danger" onclick="EDIT.rules['+ri+'].candidates.splice('+ci+',1);renderRules()">删除</button></td></tr>';
    }).join('')+'</tbody></table></div>';
}

function fromRes(r) {
  return {id:'',name:r.display_name,resource_id:r.id,resource_kind:r.kind,provider:r.provider,auth_id:r.auth_id||'',auth_index:r.auth_index||'',override_model:'',enabled:true,priority:100,weight:1};
}

function addCand(ri,id) {
  const res=(SNAP.resources||[]).find((x)=>x.id===id);
  if (!res || !resourceAllowedByAffinity(res)) return;
  const rule=EDIT.rules[ri];
  rule.candidates=rule.candidates||[];
  if (!candidateSelected(rule,res)) rule.candidates.push(fromRes(res));
  renderRules();
}

function dragCandidate(ri,ci) { DRAG_CAND={ri,ci}; }
function dropCandidate(ri,ci) {
  if (!DRAG_CAND || DRAG_CAND.ri!==ri || DRAG_CAND.ci===ci) return;
  const a=EDIT.rules[ri].candidates;
  const [item]=a.splice(DRAG_CAND.ci,1);
  a.splice(ci,0,item);
  DRAG_CAND=null;
  renderRules();
}

async function savePolicy() {
  try {
    const sel=$('pKey');
    const hint=sel.options[sel.selectedIndex]?.dataset.hint||'';
    EDIT.name=$('pName').value.trim();
    EDIT.key_fingerprint=sel.value;
    EDIT.key_hint=hint;
    EDIT.enabled=$('pEnabled').value==='true';
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
  }
}

async function delPolicy(fp) {
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

function renderResourceFilters() {
  const el=$('resourceProvider');
  if (!el) return;
  const current=el.value||'all';
  const ps=[...new Set((SNAP.resources||[]).map((r)=>r.provider).filter(Boolean))].sort();
  el.innerHTML='<option value="all">全部 Provider</option>'+ps.map((p)=>'<option value="'+esc(p)+'">'+esc(p)+'</option>').join('');
  if ([...el.options].some((o)=>o.value===current)) el.value=current;
}

function renderResources() {
  if (!$('resourceTable') || !SNAP) return;
  renderResourceFilters();
  const q=String($('resourceSearch')?.value||'').toLowerCase();
  const type=$('resourceType')?.value||'all';
  const provider=$('resourceProvider')?.value||'all';
  const status=$('resourceStatus')?.value||'all';
  const xs=(SNAP.resources||[]).filter((r)=>{
    const st=resourceStatus(r);
    if (type!=='all' && String(r.kind)!==type) return false;
    if (provider!=='all' && String(r.provider)!==provider) return false;
    if (status==='active' && !st.ok) return false;
    if (status==='disabled' && st.ok) return false;
    if (q && ![r.display_name,r.provider,r.base_url,r.key_hint,r.auth_index].some((v)=>String(v||'').toLowerCase().includes(q))) return false;
    return true;
  });
  if (!xs.length) { $('resourceTable').innerHTML='<div class="empty">没有符合条件的上游资源</div>'; return; }
  $('resourceTable').innerHTML='<div class="table-wrap"><table class="data-table"><thead><tr><th>资源别名</th><th>类型</th><th>Provider</th><th>Endpoint / 账号</th><th>Prefix</th><th>客户端适配</th><th>状态</th><th>操作</th></tr></thead><tbody>'+
    xs.map((r)=>{
      const st=resourceStatus(r);
      const compat=String(r.kind).toLowerCase()==='oauth'?'通用 / 转换':clientTypeLabel(r.provider);
      return '<tr><td><b>'+esc(r.display_name||r.id)+'</b></td><td><span class="tag '+(String(r.kind).toLowerCase()==='oauth'?'oauth':'api')+'">'+esc(r.kind||'-')+'</span></td><td><span class="tag">'+esc(r.provider||'-')+'</span></td><td class="mono">'+esc(resourceEndpoint(r))+'</td><td>'+esc(r.prefix||'—')+'</td><td>'+esc(compat)+'</td><td><span class="dot '+(st.ok?'':'warn')+'"></span>'+esc(st.label)+'</td><td><button class="btn small" onclick="openResourceDrawer(\''+esc(r.id)+'\')">查看</button></td></tr>';
    }).join('')+'</tbody></table></div>';
}

function openResourceDrawer(id) {
  const r=(SNAP.resources||[]).find((x)=>x.id===id);
  if (!r) return;
  const st=resourceStatus(r);
  $('resourceDrawerBody').innerHTML='<div class="info" style="margin-top:16px">KCR 从 CPA 实时读取资源。凭据、Base URL 与 OAuth 授权请在 CPA 中维护；这里提供 Policy 路由视角的统一详情。</div>'+
    '<div class="kv"><label>资源别名</label><div class="value"><b>'+esc(r.display_name||r.id)+'</b></div></div>'+
    '<div class="grid2" style="margin-top:14px"><div class="kv" style="margin:0"><label>资源类型</label><div class="value">'+esc(r.kind||'-')+'</div></div><div class="kv" style="margin:0"><label>Provider</label><div class="value">'+esc(r.provider||'-')+'</div></div></div>'+
    '<div class="kv"><label>Endpoint / 账号</label><div class="value mono">'+esc(resourceEndpoint(r))+'</div></div>'+
    '<div class="grid2" style="margin-top:14px"><div class="kv" style="margin:0"><label>Prefix</label><div class="value">'+esc(r.prefix||'—')+'</div></div><div class="kv" style="margin:0"><label>状态</label><div class="value">'+esc(st.label)+'</div></div></div>'+
    '<div class="kv"><label>AuthIndex</label><div class="value mono">'+esc(r.auth_index||'—')+'</div></div>'+
    '<div class="kv"><label>客户端适配</label><div class="value">'+esc(String(r.kind).toLowerCase()==='oauth'?'通用 / 协议转换':clientTypeLabel(r.provider))+'</div></div>'+
    ((r.models||[]).length?'<div class="kv"><label>模型列表（只读）</label><div class="value">'+(r.models||[]).map((m)=>'<div>● '+esc(m)+'</div>').join('')+'</div></div>':'');
  $('resourceBackdrop').classList.add('open');
  $('resourceDrawer').classList.add('open');
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
async function clearMemory(){ SNAP=await api({action:'clear_memory'}); await loadEvents(); }

function eventParams() {
  const q={action:'events',limit:$('eventLimit')?.value||'200'};
  for (const [k,id] of [['q','eventSearch'],['decision','eventDecision'],['success','eventSuccess'],['policy','eventPolicy'],['strategy','eventStrategy'],['provider','eventProvider'],['model','eventModel'],['status','eventStatus'],['since','eventSince']]) {
    const v=$(id)?.value;
    if (v && v!=='all') q[k]=v;
  }
  return q;
}

async function loadEvents() {
  if (!$('events')) return;
  try {
    EVENT_DATA=await api(eventParams());
    renderEventFacets(EVENT_DATA.facets||{});
    renderEventStats(EVENT_DATA.stats||{});
    renderEvents();
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
function renderEventFacets(f){setFacet('eventPolicy',f.policies,'全部 Policy');setFacet('eventStrategy',f.strategies,'全部 Strategy');setFacet('eventProvider',f.providers,'全部 Provider');setFacet('eventModel',f.models,'全部 Model');}
function fmtNumber(v,d=0){const n=Number(v||0);return Number.isFinite(n)?n.toFixed(d):'0';}
function renderEventStats(s) {
  const cards=[['匹配请求',fmtNumber(s.total),'当前筛选'],['成功率',fmtNumber(s.success_rate,1)+'%',(s.success||0)+' 成功 / '+(s.failed||0)+' 失败'],['平均耗时',fmtNumber(s.avg_duration_ms)+' ms','端到端路由耗时'],['P95 耗时',fmtNumber(s.p95_duration_ms)+' ms','当前筛选'],['Fallback',fmtNumber(s.fallback),'转入 CPA 默认路由'],['平均尝试',fmtNumber(s.avg_attempts,2),'每个请求的候选次数']];
  $('eventStats').innerHTML=cards.map(([n,v,h])=>'<div class="stat"><div class="muted small">'+esc(n)+'</div><b>'+esc(v)+'</b><div class="hint">'+esc(h)+'</div></div>').join('');
}
function resetEventFilters(){for(const id of ['eventSearch']) if($(id))$(id).value='';for(const id of ['eventDecision','eventSuccess','eventPolicy','eventStrategy','eventProvider','eventModel','eventStatus','eventSince'])if($(id))$(id).value='all';if($('eventLimit'))$('eventLimit').value='200';loadEvents();}
function reasonLabel(r){return({no_matching_rule:'Policy 已配置，但 model 未命中任何 Rule，交给 CPA 默认路由',rule_cpa_default:'命中 Rule 明确配置为 CPA 默认路由',no_enabled_candidates:'命中 Rule，但没有启用候选',candidates_exhausted:'候选全部尝试后仍失败',policy_fallback_to_cpa:'Failover 规则转入 CPA 默认路由',policy_disabled:'该 API Key 的 KCR Policy 已停用',policy_lookup_miss:'检测到 Policy 查找异常',unsupported_client_affinity:'Client Affinity 模式不受支持，已 fail closed'})[r]||r||'';}
function statusLabel(a){if(a.status)return'HTTP '+a.status;if(a.error)return'ERROR';return'-';}
function renderAttempt(e,a,i){const reasons=e.selection_reasons||[];const reason=reasons[i]||(i===0?'按当前策略排序后选择此候选':'按 Failover 策略选择后续候选');const detail=[a.provider,a.auth_index?'AuthIndex '+a.auth_index:'',a.model?'model '+a.model:'',Number.isFinite(Number(a.duration_ms))?a.duration_ms+' ms':''].filter(Boolean).join(' · ');return '<div class="attempt"><div class="row"><b>#'+(i+1)+' '+esc(a.candidate||'-')+'</b><span class="tag">'+esc(statusLabel(a))+'</span><span class="muted small">'+esc(detail)+'</span></div><div class="reason"><b>选择原因：</b>'+esc(reason)+'</div>'+(a.error?'<div class="red small" style="margin-top:4px">'+esc(a.error)+'</div>':'')+'</div>';}
function fmtEventTime(at){if(!at)return'-';const d=new Date(String(at));if(Number.isNaN(d.getTime()))return String(at);const p=(n)=>String(n).padStart(2,'0');return p(d.getMonth()+1)+'-'+p(d.getDate())+' '+p(d.getHours())+':'+p(d.getMinutes())+':'+p(d.getSeconds());}
function eventState(e){if(e.success===false)return{cls:'event-state-error',label:'失败'};if(e.decision==='KCR_FALLBACK_TO_CPA')return{cls:'event-state-fallback',label:'Fallback'};if(e.decision==='KCR_BYPASS_CPA_DEFAULT')return{cls:'event-state-bypass',label:'Bypass'};return{cls:'event-state-ok',label:'成功'};}
function eventStatusText(e){if(e.status)return String(e.status);if(e.success===false)return'ERR';return'-';}
function toggleEvent(traceID){if(!traceID)return;if(EVENT_EXPANDED.has(traceID))EVENT_EXPANDED.delete(traceID);else EVENT_EXPANDED.add(traceID);renderEvents();}
function renderEventDetail(e){const eventReason=reasonLabel(e.reason);const final=e.final||(e.decision==='KCR_FALLBACK_TO_CPA'?'CPA Default':'-');const primary=['时间 '+fmtEventTime(e.at),'Model '+(e.model||'-'),'Policy '+(e.policy_name||'-'),'Rule '+(e.rule_name||'-'),'Strategy '+(e.strategy||'-'),'最终候选 '+final,'Provider '+(e.provider||'-'),'状态 '+eventStatusText(e),'耗时 '+(e.duration_ms||0)+' ms','Attempts '+((e.attempts||[]).length)].join(' · ');return '<div class="event-detail"><div class="small">'+esc(primary)+'</div>'+(eventReason?'<div style="margin-top:7px"><b>路由结果：</b>'+esc(eventReason)+'</div>':'')+'<div class="muted small" style="margin-top:6px">'+esc(e.trace_id?'Trace '+e.trace_id:'')+'</div><div style="font-weight:800;margin-top:12px">候选尝试</div><div class="attempts">'+((e.attempts||[]).length?(e.attempts||[]).map((a,i)=>renderAttempt(e,a,i)).join(''):'<div class="muted small">没有候选尝试记录</div>')+'</div></div>';}
function renderEvents(){if(!EVENT_DATA)return;const xs=EVENT_DATA.events||[];const source=EVENT_DATA.source==='sqlite'?'SQLite':'Memory';$('eventResultMeta').textContent='数据源 '+source+' · 匹配 '+(EVENT_DATA.matched||0)+' 条 · 返回 '+(EVENT_DATA.returned||0)+' 条';if(!xs.length){$('events').innerHTML='<div class="empty" style="margin-top:14px">当前条件下暂无路由记录</div>';return;}const rows=xs.map((e)=>{const key=e.trace_id||e.at,expanded=EVENT_EXPANDED.has(key),state=eventState(e),attempts=(e.attempts||[]).length,final=e.final||(e.decision==='KCR_FALLBACK_TO_CPA'?'CPA Default':'-');const row='<tr class="event-row '+(expanded?'expanded':'')+'" onclick="toggleEvent(\''+esc(key)+'\')"><td class="event-toggle"><span class="chevron">'+(expanded?'▾':'›')+'</span></td><td class="event-time mono">'+esc(fmtEventTime(e.at))+'</td><td><div class="event-primary ellipsis">'+esc(e.model||'-')+'</div></td><td><div class="event-primary">'+esc(e.policy_name||'-')+'</div><div class="muted small ellipsis">'+esc(e.rule_name||'-')+'</div></td><td><span class="strategy-chip">'+esc(e.strategy||'-')+'</span></td><td><div class="event-primary ellipsis">'+esc(final)+'</div></td><td>'+esc(e.provider||'-')+'</td><td><span class="event-state '+state.cls+'"><i></i>'+esc(state.label)+'</span> <span class="http-code">'+esc(eventStatusText(e))+'</span></td><td>'+(e.duration_ms||0)+' ms</td><td><span class="attempt-count '+(attempts>1?'multi':'')+'">'+attempts+'</span></td></tr>';return row+(expanded?'<tr class="event-detail-row"><td colspan="10">'+renderEventDetail(e)+'</td></tr>':'');}).join('');$('events').innerHTML='<div class="event-table-wrap"><table class="event-table"><thead><tr><th></th><th>时间（本地）</th><th>Model</th><th>Policy / Rule</th><th>Strategy</th><th>最终候选</th><th>Provider</th><th>状态</th><th>耗时</th><th>Attempts</th></tr></thead><tbody>'+rows+'</tbody></table></div>';}

load().catch((e)=>{
  const target=$('dashboardPage')||document.body;
  target.innerHTML='<div class="note">加载失败：'+esc(e.message||e)+'</div>';
});
