// v0.7 diagnostic renderer. Appended after ui.js so this definition replaces
// the legacy renderer without changing the rest of the management UI.
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
  let h = '<div class="oknote">决策：' + esc(d.decision) + '；Rule：' + esc(d.rule.name) + '；Strategy：' + esc(d.strategy) + '</div>';
  if (d.cpa_priority_warning) h += '<div class="note" style="margin-top:10px"><b>CPA Priority 警告：</b>' + esc(d.cpa_priority_warning) + '</div>';
  if (d.scope_warning) h += '<div class="note" style="margin-top:10px"><b>执行范围警告：</b>' + esc(d.scope_warning) + '</div>';
  if (d.priority_note) h += '<div class="muted small" style="margin-top:8px">' + esc(d.priority_note) + '</div>';
  h += '<table style="width:100%;margin-top:12px;border-collapse:collapse"><tr>' +
    '<th align=left>#</th><th align=left>候选</th><th align=left>Provider</th>' +
    (f.priority ? '<th align=left>KCR Priority</th>' : '') +
    '<th align=left>CPA Priority</th>' + (f.weight ? '<th align=left>Weight</th>' : '') +
    '<th align=left>live Auth.ID</th><th align=left>执行 Model</th><th align=left>CPA 状态</th><th align=left>健康</th><th align=left>当前路由</th></tr>';
  for (const c of d.ranked_candidates || []) {
    const health = c.health || {};
    const state = String(health.state || 'closed').toUpperCase();
    let healthText = state;
    if (health.probe_in_flight) healthText += ' · probe 进行中';
    else if (Number(health.retry_in_ms || 0) > 0) healthText += ' · ' + Number(health.retry_in_ms) + 'ms 后探测';
    const routeText = c.effective ? '✓ 当前首选' : (c.selectable ? '可选' : '跳过');
    const cpaPriority = c.cpa_priority_known ? String(c.cpa_priority) : '未知';
    let cpaState = c.cpa_status || (c.cpa_runtime_known ? 'unknown' : 'runtime 未暴露');
    if (c.cpa_unavailable) cpaState += ' · unavailable';
    h += '<tr><td>' + c.order + '</td><td>' + esc(c.name) + '</td><td>' + esc(c.provider) + '</td>' +
      (f.priority ? '<td>' + esc(c.priority) + '</td>' : '') + '<td>' + esc(cpaPriority) + '</td>' +
      (f.weight ? '<td>' + esc(c.weight) + '</td>' : '') +
      '<td class="mono" style="max-width:190px;word-break:break-all">' + esc(c.auth_id || '-') + '</td>' +
      '<td class="mono">' + esc(c.execution_model || '-') + (c.credential_prefix ? '<div class="muted small">prefix: ' + esc(c.credential_prefix) + '</div>' : '') + '</td>' +
      '<td>' + esc(cpaState) + '</td><td>' + esc(healthText) + '</td><td>' + esc(routeText) + '</td></tr>';
    if (c.identity_error || c.scope_note) {
      h += '<tr><td></td><td colspan="10" class="small ' + (c.identity_error ? 'red' : 'muted') + '">' +
        esc(c.identity_error || c.scope_note) + '</td></tr>';
    }
  }
  h += '</table><h3>真实验证</h3><div class="diag">' + esc(d.curl || '') +
    '</div><div class="muted">执行后到“路由记录”查看本次候选选择原因和 Failover 链。</div>';
  $('diagBody').innerHTML = h;
}
