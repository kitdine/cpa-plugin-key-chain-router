'use strict';

// Dependency-free behavioral checks for the embedded browser UI. The DOM shim
// intentionally covers only the surfaces exercised here; browser QA still owns
// layout, keyboard interactions, and native <details> behavior.
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const source = fs.readFileSync(path.join(__dirname, '../src/ui.js'), 'utf8');
const bootstrap = source.lastIndexOf('\nload().catch(');
assert.ok(bootstrap > 0, 'UI bootstrap must be identifiable');

function harness() {
  const nodes = new Map();
  function node(id) {
    if (!nodes.has(id)) {
      const classes = new Set();
      nodes.set(id, {
        value: '', innerHTML: '', textContent: '', disabled: false,
        options: [], selectedIndex: 0, dataset: {}, style: {},
        classList: {
          add: (...xs) => xs.forEach(x => classes.add(x)),
          remove: (...xs) => xs.forEach(x => classes.delete(x)),
          contains: x => classes.has(x),
          toggle(x, force) {
            const enabled = force === undefined ? !classes.has(x) : force;
            enabled ? classes.add(x) : classes.delete(x);
            return enabled;
          },
        },
        setAttribute(name, value) { this[name] = String(value); },
        querySelectorAll() { return []; },
      });
    }
    return nodes.get(id);
  }
  const alerts = [];
  const context = vm.createContext({
    document: {getElementById: node, querySelectorAll: () => [], body: node('body')},
    window: {scrollTo() {}},
    alert: message => alerts.push(message), confirm: () => true,
    URLSearchParams, console, setTimeout, clearTimeout,
    fetch: async () => { throw new Error('Unexpected network request'); },
  });
  vm.runInContext(source.slice(0, bootstrap), context, {filename: 'ui.js'});
  return {
    context, node, alerts,
    run: code => vm.runInContext(code, context),
    read: expression => JSON.parse(vm.runInContext(`JSON.stringify(${expression})`, context)),
    set(name, value) { context.fixtureValue = value; vm.runInContext(`${name} = fixtureValue`, context); },
  };
}

function policy() {
  return {
    name: 'Production', key_fingerprint: 'fp-production', key_hint: 'sk-…123',
    enabled: true, client_type: 'claude', client_provider: 'claude', client_affinity: 'strict',
    rules: [{id: 'rule-a', name: 'Sensitive route', models: ['claude-*'], strategy: 'sticky',
      sticky_source: 'header', sticky_header: 'X-Session-Id',
      candidates: [{id: 'candidate-a', resource_id: 'api-claude', provider: 'claude',
        resource_kind: 'API', priority: 7, weight: 3, enabled: true, override_model: 'upstream-model'}],
      failover: {network: 'next', unauthorized: 'stop', timeout: 'next', conflict: 'next',
        rate_limit: 'next', server_error: 'next', other: 'stop', exhausted: 'error', max_attempts: 5}}],
  };
}

const resources = [
  {id: 'api-claude', kind: 'API', provider: 'claude', alias: 'Claude primary', base_url: 'https://claude.example', status: 'active'},
  {id: 'api-codex', kind: 'API', provider: 'codex', alias: 'Codex backup', base_url: 'https://codex.example', status: 'disabled'},
  {id: 'oauth-codex', kind: 'OAuth', provider: 'codex', alias: 'Codex account', display_name: 'account', status: 'active'},
];

function ready() {
  const h = harness();
  h.set('SNAP', {policies: [policy()], resources: structuredClone(resources), downstream_keys: []});
  h.set('EDIT', policy());
  return h;
}

test('rule rendering preserves configured candidate, sticky, and failover values', () => {
  const h = ready();
  const before = h.read('EDIT');
  h.run('renderRules(); renderRules()');
  assert.deepEqual(h.read('EDIT'), before);
  assert.match(h.node('rules').innerHTML, /X-Session-Id/);
  assert.match(h.node('rules').innerHTML, /upstream-model/);
});

test('resource search preserves selected candidates and affinity blocks other native APIs', () => {
  const h = ready();
  const before = h.read('EDIT');
  h.run("updateRuleResourceSearch(0, 'codex')");
  const html = h.node('rule-resource-pool-0').innerHTML;
  assert.match(html, /Codex backup/);
  assert.match(html, /Codex account/);
  assert.doesNotMatch(html, /Claude primary/);
  assert.equal(h.run('resourceAllowedByAffinity(SNAP.resources[1])'), false);
  assert.equal(h.run('resourceAllowedByAffinity(SNAP.resources[2])'), true);
  assert.deepEqual(h.read('EDIT'), before);
});

test('policy toggle persists only enabled against the latest snapshot', async () => {
  const h = ready();
  const latest = policy();
  latest.name = 'Updated in another tab';
  latest.rules[0].failover.max_attempts = 11;
  const calls = [];
  let persisted = structuredClone(latest);
  h.context.api = async request => {
    calls.push(request);
    if (request.action === 'snapshot') return {policies: [structuredClone(persisted)], resources: []};
    assert.equal(request.action, 'save_policy');
    persisted = JSON.parse(request.payload);
    return {policies: [persisted], resources: []};
  };
  h.run('renderAll = () => {}; loadDashboard = async () => {}');
  await h.run("togglePolicyEnabled('fp-production')");
  const saved = JSON.parse(calls.find(x => x.action === 'save_policy').payload);
  assert.deepEqual(saved, {...latest, enabled: false});
  assert.equal(h.read('SNAP.policies[0].enabled'), false);
  assert.deepEqual(h.read('EDIT'), policy(), 'quick toggle must not overwrite an open draft');
  await h.run("togglePolicyEnabled('fp-production')");
  assert.deepEqual(persisted, latest, 'second toggle re-enables the policy without altering its configuration');
  assert.equal(h.run('POLICY_PENDING.size'), 0);
});

test('policy toggle blocks duplicate requests while saving', async () => {
  const h = ready();
  let resolveSnapshot;
  const calls = [];
  h.context.api = request => {
    calls.push(request);
    if (request.action === 'snapshot') return new Promise(resolve => { resolveSnapshot = resolve; });
    return Promise.resolve({policies: [JSON.parse(request.payload)], resources: []});
  };
  h.run('renderAll = () => {}; loadDashboard = async () => {}');
  const first = h.run("togglePolicyEnabled('fp-production')");
  await h.run("togglePolicyEnabled('fp-production')");
  assert.equal(calls.length, 1);
  resolveSnapshot({policies: [policy()], resources: []});
  await first;
  assert.equal(calls.filter(x => x.action === 'save_policy').length, 1);
  assert.equal(h.run('POLICY_PENDING.size'), 0);
});

test('failed policy toggle preserves local snapshot and clears pending state', async () => {
  const h = ready();
  const before = h.read('SNAP');
  h.context.api = async request => {
    if (request.action === 'snapshot') return structuredClone(before);
    throw new Error('save refused');
  };
  h.run('renderAll = () => {}; loadDashboard = async () => {}');
  await h.run("togglePolicyEnabled('fp-production')");
  assert.deepEqual(h.read('SNAP'), before);
  assert.equal(h.run('POLICY_PENDING.size'), 0);
});

test('editor section state is presentation-only and follows rules when reordered', () => {
  const h = ready();
  h.run("EDIT.rules.push({...defaultRule(), id: 'rule-b', name: 'Second rule'})");
  const before = h.read('EDIT');
  h.run("setRuleSection(0, 'open', false); setRuleSection(0, 'advanced', true); setRuleSection(0, 'pool', true)");
  h.run("updateRuleResourceSearch(0, 'claude'); renderRules()");
  assert.deepEqual(h.read('EDIT'), before);
  assert.equal(h.run('ruleUI(EDIT.rules[0], 0).open'), false);
  h.run('moveRule(0, 1)');
  assert.equal(h.run('ruleUI(EDIT.rules[1], 1).advanced'), true);
  assert.equal(h.run('ruleUI(EDIT.rules[1], 1).search'), 'claude');
  assert.equal(h.run('ruleUI(EDIT.rules[0], 0).search'), '');
  h.run('deleteRule(0)');
  assert.deepEqual(h.read('EDIT.rules[0]'), before.rules[0]);
  assert.equal(h.run('ruleUI(EDIT.rules[0], 0).search'), 'claude');
});

test('switching strategies retains hidden candidate, sticky, and failover configuration', () => {
  const h = ready();
  const before = h.read('EDIT.rules[0]');
  h.run("selectRuleStrategy(0, 'cpa-default'); renderRules(); selectRuleStrategy(0, 'sticky'); renderRules()");
  assert.deepEqual(h.read('EDIT.rules[0]'), before);
});

test('resource filters combine type, provider, status, and search without mutation', () => {
  const h = ready();
  const before = h.read('SNAP.resources');
  assert.deepEqual(h.read("filteredResources(SNAP.resources, {type:'OAuth', provider:'codex', status:'active'}, 'account').map(r => r.id)"), ['oauth-codex']);
  assert.deepEqual(h.read("filteredResources(SNAP.resources, {type:'all', provider:'codex', status:'disabled'}, '').map(r => r.id)"), ['api-codex']);
  assert.deepEqual(h.read("filteredResources(SNAP.resources, {type:'API', provider:'claude', status:'all'}, 'CODEX')"), []);
  assert.deepEqual(h.read("filteredResources(SNAP.resources, {type:'all', provider:'all', status:'all'}, 'CLAUDE').map(r => r.id)"), ['api-claude']);
  assert.deepEqual(h.read('SNAP.resources'), before);
});

test('chip filters expose selection and clear all restores resources', () => {
  const h = ready();
  h.run("setResourceFilter('provider', 'codex'); setResourceFilter('type', 'OAuth')");
  assert.equal(h.read('RESOURCE_FILTERS.provider'), 'codex');
  assert.equal(h.read('RESOURCE_FILTERS.type'), 'OAuth');
  assert.match(h.node('resourceTable').innerHTML, /Codex account/);
  assert.doesNotMatch(h.node('resourceTable').innerHTML, /Codex backup|Claude primary/);
  h.node('resourceSearch').value = 'not found';
  h.run('resetResourceFilters()');
  assert.deepEqual(h.read('RESOURCE_FILTERS'), {type: 'all', provider: 'all', status: 'all'});
  assert.equal(h.node('resourceSearch').value, '');
  assert.match(h.node('resourceTable').innerHTML, /Codex account/);
  assert.match(h.node('resourceTable').innerHTML, /Claude primary/);
});

test('uncertain toggle response reads back server state without repeating the write', async () => {
  const h = ready();
  let snapshots = 0;
  let writes = 0;
  h.context.api = async request => {
    if (request.action === 'snapshot') {
      const persisted = policy();
      persisted.enabled = ++snapshots === 1;
      return {policies: [persisted], resources: []};
    }
    writes++;
    throw new Error('response disconnected after commit');
  };
  h.run('renderAll = () => {}');
  await h.run("togglePolicyEnabled('fp-production')");
  assert.equal(writes, 1);
  assert.equal(snapshots, 2);
  assert.equal(h.read('SNAP.policies[0].enabled'), false);
  assert.equal(h.run('POLICY_PENDING.size'), 0);
  assert.match(h.node('policyNotice').textContent, /未确认/);
});

test('toggle does not recreate a policy deleted since the list loaded', async () => {
  const h = ready();
  const calls = [];
  h.context.api = async request => {
    calls.push(request.action);
    return {policies: [], resources: []};
  };
  h.run('renderAll = () => {}');
  await h.run("togglePolicyEnabled('fp-production')");
  assert.ok(calls.every(action => action === 'snapshot'));
  assert.deepEqual(h.read('SNAP.policies'), []);
  assert.equal(h.run('POLICY_PENDING.size'), 0);
});

function fillEditor(h) {
  h.node('pName').value = 'Production';
  h.node('pKey').value = 'fp-production';
  h.node('pKey').options = [{dataset: {hint: 'sk-…123'}}];
  h.node('pClientType').value = 'claude';
}

test('save guard blocks duplicate submission and cancellation until request completes', async () => {
  const h = ready();
  fillEditor(h);
  let finish;
  const requests = [];
  h.context.api = request => {
    requests.push(request);
    return new Promise(resolve => { finish = resolve; });
  };
  h.run('renderAll = () => {}');
  const saving = h.run('savePolicy()');
  assert.equal(h.node('savePolicyButton').disabled, true);
  assert.equal(h.node('policyEditorFields').disabled, true);
  await h.run('savePolicy()');
  h.run('closePolicy()');
  assert.notEqual(h.read('EDIT'), null);
  assert.equal(requests.length, 1);
  finish({policies: [JSON.parse(requests[0].payload)], resources: []});
  await saving;
  assert.equal(h.read('EDIT'), null);
  assert.equal(h.run('POLICY_SAVING'), false);
  assert.equal(h.node('savePolicyButton').disabled, false);
  assert.equal(h.node('policyEditorFields').disabled, false);
});

test('failed save retains draft and permits retry', async () => {
  const h = ready();
  fillEditor(h);
  const before = h.read('EDIT');
  h.context.api = async () => { throw new Error('server rejected policy'); };
  await h.run('savePolicy()');
  assert.deepEqual(h.read('EDIT'), before);
  assert.equal(h.run('POLICY_SAVING'), false);
  assert.equal(h.node('savePolicyButton').disabled, false);
  assert.equal(h.node('policyEditorFields').disabled, false);
  assert.match(h.alerts[0], /server rejected policy/);
});

test('unsupported affinity is rejected locally without sending draft', async () => {
  const h = ready();
  fillEditor(h);
  h.run("EDIT.client_affinity = 'future-mode'");
  let calls = 0;
  h.context.api = async () => { calls++; };
  await h.run('savePolicy()');
  assert.equal(calls, 0);
  assert.equal(h.run('POLICY_SAVING'), false);
  assert.equal(h.read('EDIT.client_affinity'), 'future-mode');
  assert.match(h.alerts[0], /Client Affinity/);
});

test('presentation defaults reduce clutter and never serialize into policy data', () => {
  const h = ready();
  h.run('EDIT.rules.push(defaultRule()); renderRules()');
  assert.deepEqual(h.read('ruleUI(EDIT.rules[0], 0)'), {open: true, advanced: false, pool: false, filtered: false, search: ''});
  assert.deepEqual(h.read('ruleUI(EDIT.rules[1], 1)'), {open: false, advanced: false, pool: true, filtered: false, search: ''});
  const before = h.read('EDIT');
  h.run("setRuleSection(0, 'advanced', true); setRuleSection(0, 'filtered', true); selectRuleStrategy(0, 'invalid'); renderRules()");
  assert.deepEqual(h.read('EDIT'), before);
  assert.match(h.node('rules').innerHTML, /<details class="advanced failover-details" open/);
});

test('provider chips escape labels and retain explicit aria selection', () => {
  const h = ready();
  h.run(`SNAP.resources.push({id: 'special', kind: 'API', provider: 'quote"<provider>', alias: 'Special'})`);
  h.run(`setResourceFilter('provider', 'quote"<provider>')`);
  const html = h.node('resourceFilters').innerHTML;
  assert.match(html, /quote&amp;quot;|quote&quot;/);
  assert.doesNotMatch(html, /<provider>/);
  assert.match(html, /aria-pressed="true"/);
  assert.match(h.node('resourceTable').innerHTML, /Special/);
  h.run("SNAP.resources = SNAP.resources.filter(r => r.id !== 'special'); renderResources()");
  assert.equal(h.read('RESOURCE_FILTERS.provider'), 'all');
});

test('dirty navigation cancellation preserves draft and confirmation discards it', () => {
  const h = ready();
  fillEditor(h);
  h.run("CURRENT_VIEW = 'policyEditor'; EDIT_BASELINE = editorSignature(); EDIT.rules[0].name = 'Unsaved rule'");
  const before = h.read('EDIT');
  let prompts = 0;
  h.context.confirm = () => { prompts++; return false; };
  h.run("showView('policy')");
  assert.equal(h.run('CURRENT_VIEW'), 'policyEditor');
  assert.deepEqual(h.read('EDIT'), before);
  assert.equal(prompts, 1);
  h.context.confirm = () => { prompts++; return true; };
  h.run("showView('policy')");
  assert.equal(h.run('CURRENT_VIEW'), 'policy');
  assert.equal(h.read('EDIT'), null);
  assert.equal(prompts, 2);
});

test('unchanged editor exits without prompting', () => {
  const h = ready();
  fillEditor(h);
  h.run("CURRENT_VIEW = 'policyEditor'; EDIT_BASELINE = editorSignature()");
  h.context.confirm = () => { assert.fail('unchanged draft must not prompt'); };
  h.run("showView('policy')");
  assert.equal(h.run('CURRENT_VIEW'), 'policy');
  assert.equal(h.read('EDIT'), null);
});

test('pending save blocks sidebar navigation and opening another policy', () => {
  const h = ready();
  fillEditor(h);
  h.run("CURRENT_VIEW = 'policyEditor'; EDIT_BASELINE = editorSignature(); POLICY_SAVING = true");
  const before = h.read('EDIT');
  h.context.confirm = () => { assert.fail('pending save must not offer discard'); };
  h.run("showView('resources'); openPolicy(); closePolicy()");
  assert.equal(h.run('CURRENT_VIEW'), 'policyEditor');
  assert.deepEqual(h.read('EDIT'), before);
});

test('candidate switches and keyboard ordering preserve routing configuration', () => {
  const h = ready();
  h.run("EDIT.rules[0].candidates.push({...EDIT.rules[0].candidates[0], id:'candidate-b', resource_id:'oauth-codex', priority:0, weight:9, enabled:false})");
  const before = h.read('EDIT');
  h.run('toggleCandidate(0, 0)');
  assert.deepEqual(h.read('EDIT.rules[0].candidates[0]'), {...before.rules[0].candidates[0], enabled:false});
  h.run('toggleCandidate(0, 0); moveCandidate(0, 0, 1)');
  assert.deepEqual(h.read('EDIT.rules[0].candidates'), [...before.rules[0].candidates].reverse());
  h.run('moveCandidate(0, 1, 1); moveCandidate(0, 0, -1)');
  assert.deepEqual(h.read('EDIT.rules[0].candidates'), [...before.rules[0].candidates].reverse());
  h.run('moveCandidate(0, 1, -1)');
  assert.deepEqual(h.read('EDIT'), before);
  const html = h.run("selectedCandidatesTable(EDIT.rules[0], 0, strategyFields('priority-weighted'))");
  assert.match(html, /Priority" type="number" value="0"/);
  assert.match(html, /role="switch" aria-checked="false"/);
  assert.match(html, /aria-label="上移候选/);
  assert.match(html, /aria-label="下移候选/);
});

test('editor fieldset encloses basic controls, generated rules, and save action', () => {
  const page = fs.readFileSync(path.join(__dirname, '../src/page.html'), 'utf8');
  const fieldset = page.match(/<fieldset\b[^>]*id="policyEditorFields"[^>]*>([\s\S]*?)<\/fieldset>/);
  assert.ok(fieldset, 'disabled editor container must be a native fieldset');
  for (const id of ['pName', 'pKey', 'pClientType', 'pEnabledToggle', 'pAffinityToggle', 'rules', 'savePolicyButton']) {
    assert.ok(fieldset[1].includes(`id="${id}"`), `${id} must be within the disabled fieldset`);
  }
});

test('pending save ignores candidate drag/drop, including an already-started drag', () => {
  const h = ready();
  h.run("EDIT.rules[0].candidates.push({...EDIT.rules[0].candidates[0], id:'candidate-b'}); dragCandidate(0, 0)");
  const before = h.read('EDIT');
  h.run('POLICY_SAVING = true; dropCandidate(0, 1)');
  assert.deepEqual(h.read('EDIT'), before);
  h.run('DRAG_CAND = null; dragCandidate(0, 0)');
  assert.equal(h.read('DRAG_CAND'), null);
});

test('quick toggles serialize across policies so old snapshots cannot overwrite newer saves', async () => {
  const h = ready();
  const second = {...policy(), name:'Second', key_fingerprint:'fp-second'};
  h.context.second = second;
  h.run('SNAP.policies.push(second)');
  let resolveSnapshot;
  const calls = [];
  h.context.api = request => {
    calls.push(request);
    if (request.action === 'snapshot') return new Promise(resolve => { resolveSnapshot = resolve; });
    return Promise.resolve({policies:[JSON.parse(request.payload),second],resources:[]});
  };
  h.run('renderAll = () => {}');
  const first = h.run("togglePolicyEnabled('fp-production')");
  assert.equal(h.node('newPolicyButton').disabled, true);
  assert.match(h.node('policies').innerHTML, /id="policy-toggle-fp-second"[^>]*disabled/);
  await h.run("togglePolicyEnabled('fp-second')");
  assert.equal(calls.length, 1, 'a different policy must also wait for the active mutation');
  resolveSnapshot({policies:[policy(),second],resources:[]});
  await first;
  assert.equal(calls.filter(x=>x.action==='save_policy').length, 1);
  assert.equal(h.read('SNAP.policies[1].enabled'), true);
});

test('rerender restores keyboard focus to the selected strategy and filter', () => {
  const h = ready();
  const focused = [];
  h.context.document.activeElement = {id:'strategy-0-round-robin'};
  h.node('strategy-0-round-robin').focus = () => focused.push('strategy');
  h.run("selectRuleStrategy(0,'round-robin')");
  h.context.document.activeElement = {id:'resource-filter-type-2'};
  h.node('resource-filter-type-2').focus = () => focused.push('filter');
  h.run("setResourceFilter('type','OAuth')");
  assert.deepEqual(focused, ['strategy','filter']);
});

test('keyboard candidate movement follows the same candidate and chooses an enabled arrow', () => {
  const h = ready();
  h.run("EDIT.rules[0].candidates.push({...EDIT.rules[0].candidates[0],id:'second'})");
  const focused = [];
  h.context.document.activeElement = {id:'candidate-down-0-0'};
  h.node('candidate-up-0-1').focus = () => focused.push('moved');
  h.run('moveCandidate(0,0,1)');
  assert.deepEqual(focused, ['moved']);
  h.context.document.activeElement = {id:'candidate-toggle-0-1'};
  h.node('candidate-toggle-0-1').focus = () => focused.push('toggled');
  h.run('toggleCandidate(0,1)');
  assert.deepEqual(focused, ['moved','toggled']);
});
