// Headless screenshots of the bnk-forge UI: cluster registration + F5 BNK view.
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
  const shot = async (name) => { await page.screenshot({ path: `/home/mwiget/demo-rec/uishot/${name}` }); console.log('  shot', name); };
  const settle = (ms) => page.waitForTimeout(ms);

  // 1. login
  await page.goto(base + '/login', { waitUntil: 'domcontentloaded' });
  await page.fill('input[name="username"], input[id="username"]', 'admin');
  await page.fill('input[name="password"], input[id="password"]', 'changeme');
  await page.click('button[type="submit"]');
  await page.waitForURL('**/').catch(() => {});
  await settle(3000);
  await shot('01-dashboard.png');

  // 2. project detail (shows the registered cluster "demo")
  await page.goto(base + '/projects/16', { waitUntil: 'domcontentloaded' });
  await settle(4000);
  await shot('02-project.png');

  // 3. kubernetes cluster list
  await page.goto(base + '/kubernetes', { waitUntil: 'domcontentloaded' });
  await settle(4000);
  await shot('03-kubernetes.png');

  // 4. F5 BNK view (live scan — give it time)
  await page.goto(base + '/bnk', { waitUntil: 'domcontentloaded' });
  await settle(8000);
  await shot('04-bnk.png');

  // capture the page title/URL for sanity
  console.log('  final url', page.url(), '| title', await page.title());
  await browser.close();
})().catch(e => { console.error('CAPTURE ERROR', e.message); process.exit(1); });
