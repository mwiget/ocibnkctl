// Capture F5 BNK -> Insights -> Traffic Flow (after scenarios populate gateways/routes).
const { chromium } = require('playwright');

(async () => {
  const browser = await chromium.launch({ args: ['--ignore-certificate-errors'] });
  const ctx = await browser.newContext({
    ignoreHTTPSErrors: true,
    viewport: { width: 1680, height: 1050 },
    deviceScaleFactor: 2,
  });
  const page = await ctx.newPage();
  const base = 'https://localhost';
  const shot = (n) => page.screenshot({ path: `/home/mwiget/demo-rec/uishot/${n}` }).then(() => console.log('  shot', n));
  const settle = (ms) => page.waitForTimeout(ms);

  // login
  await page.goto(base + '/login', { waitUntil: 'domcontentloaded' });
  await page.fill('input[name="username"], input[id="username"]', 'admin');
  await page.fill('input[name="password"], input[id="password"]', 'changeme');
  await page.click('button[type="submit"]');
  await page.waitForURL('**/').catch(() => {});
  await settle(2500);

  // F5 BNK page (cluster auto-selected)
  await page.goto(base + '/bnk', { waitUntil: 'domcontentloaded' });
  await settle(5000);
  await shot('bnk-health.png');

  // click the "Traffic Flow" nav item under Insights
  try {
    await page.getByText('Traffic Flow', { exact: true }).first().click({ timeout: 8000 });
  } catch (e) {
    console.log('  click Traffic Flow failed, trying role/link:', e.message);
    await page.locator('text=Traffic Flow').first().click({ timeout: 8000 }).catch(() => {});
  }
  await settle(8000); // traffic flow viz loads gateway/route/backend data
  await shot('bnk-trafficflow.png');

  // also refresh the cluster K8s dashboard post-scenarios
  await page.goto(base + '/kubernetes', { waitUntil: 'domcontentloaded' });
  await settle(5000);
  await shot('kubernetes-post-scenarios.png');

  console.log('  final', page.url(), '|', await page.title());
  await browser.close();
})().catch(e => { console.error('CAPTURE ERROR', e.message); process.exit(1); });
