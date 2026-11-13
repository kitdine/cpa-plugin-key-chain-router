'use strict';

// Run with Playwright installed: node scripts/switch.browser.test.cjs
// Real layout coverage is required: a DOM shim cannot measure native buttons.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const {chromium, webkit} = require('playwright');
const root = path.resolve(__dirname, '..');
const template = fs.readFileSync(path.join(root, 'src/page.html'), 'utf8');
const css = template.match(/<style>([\s\S]*?)<\/style>/)[1];
const source = fs.readFileSync(path.join(root, 'src/ui.js'), 'utf8');
const bootstrap = source.lastIndexOf('\nload().catch(');
assert.ok(bootstrap > 0);

async function geometry(page, selector, expected) {
  const dimensions = await page.locator(selector).evaluate(button => {
    const rect = button.getBoundingClientRect();
    const thumb = getComputedStyle(button, '::after');
    return {
      width: rect.width, height: rect.height,
      thumbWidth: parseFloat(thumb.width), thumbHeight: parseFloat(thumb.height),
      thumbTop: parseFloat(thumb.top) + new DOMMatrix(thumb.transform).m42,
      thumbLeft: parseFloat(thumb.left),
      appearance: getComputedStyle(button).appearance,
    };
  });
  assert.equal(dimensions.width, expected.width);
  assert.equal(dimensions.height, expected.height);
  assert.equal(dimensions.thumbWidth, expected.thumb);
  assert.equal(dimensions.thumbHeight, expected.thumb);
  assert.equal(dimensions.thumbTop, (expected.height - expected.thumb) / 2);
  assert.equal(dimensions.thumbLeft, expected.on ? expected.width - expected.thumb - 3 : 3);
  assert.equal(dimensions.appearance, 'none');
}

(async () => {
  for (const [name, engine] of Object.entries({chromium, webkit})) {
    const browser = await engine.launch();
    try {
      for (const width of [1440, 390]) {
        const page = await browser.newPage({viewport: {width, height: 900}});
        // Simulate an inherited minimum button height and large font metrics.
        // These must not turn the compact 38x23 switch into a circle.
        await page.setContent('<style>button{min-height:34px;line-height:2;font-size:20px}' + css +
          '.switch:after{transition:none}</style><div id="ruleCount"></div><div id="rules"></div>' +
          '<button id="regular" class="switch on" role="switch" aria-checked="true"></button>');
        await page.addScriptTag({content: source.slice(0, bootstrap)});
        await page.evaluate(() => {
          SNAP = {resources: [], policies: [], downstream_keys: []};
          EDIT = {client_affinity: 'off', rules: [{id: 'r1', name: 'Test', models: ['*'],
            strategy: 'priority-weighted', candidates: [
              {id: 'a', name: 'First', enabled: false, priority: 100, weight: 1, override_model: 'model-a'},
              {id: 'b', name: 'Second', enabled: true, priority: 90, weight: 2, override_model: 'model-b'},
            ]}]};
          renderRules();
        });
        const first = page.locator('#candidate-toggle-0-0');
        await geometry(page, '#candidate-toggle-0-0', {width: 38, height: 23, thumb: 17, on: false});
        await geometry(page, '#candidate-toggle-0-1', {width: 38, height: 23, thumb: 17, on: true});
        await geometry(page, '#regular', {width: 48, height: 27, thumb: 21, on: true});
        const before = await page.evaluate(() => JSON.parse(JSON.stringify(EDIT)));
        await first.click();
        assert.equal(await first.getAttribute('aria-checked'), 'true');
        await geometry(page, '#candidate-toggle-0-0', {width: 38, height: 23, thumb: 17, on: true});
        await first.press('Space');
        assert.equal(await first.getAttribute('aria-checked'), 'false');
        await first.press('Enter');
        assert.equal(await first.getAttribute('aria-checked'), 'true');
        await first.press('Space');
        assert.deepEqual(await page.evaluate(() => JSON.parse(JSON.stringify(EDIT))), before);
        assert.equal(await page.evaluate(() => document.activeElement.id), 'candidate-toggle-0-0');
        // Disabled controls remain inert while a policy is being saved.
        await first.evaluate(button => { button.disabled = true; });
        await first.press('Space');
        assert.equal(await first.getAttribute('aria-checked'), 'false');
        await page.close();
        console.log(`${name} ${width}px: geometry, click, keyboard, focus and disabled state passed`);
      }
    } finally {
      await browser.close();
    }
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
