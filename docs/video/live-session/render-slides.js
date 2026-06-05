// Render full-screen chapter slides (1920x1080) for the demo video.
const { chromium } = require('playwright');

const slides = [
  { id: 'title', kind: 'title',
    title: 'F5 BIG-IP Next for Kubernetes',
    badge: '2.3.0',
    sub: 'An agentic deployment on plain Docker —<br>driven end to end by Claude Code on a local model' },
  { id: 'ch1', kind: 'chapter', n: '01', title: 'Scaffold &amp; launch the agent',
    desc: 'One binary creates the PoC, then Claude Code starts on a local model' },
  { id: 'ch2', kind: 'chapter', n: '02', title: 'Prepare &amp; deploy F5 BNK 2.3.0',
    desc: 'Check the host, install license secrets, run the end-to-end pipeline' },
  { id: 'ch3', kind: 'chapter', n: '03', title: 'Inspect &amp; diagnose',
    desc: 'List every pod, then let the agent diagnose a stuck one' },
  { id: 'ch4', kind: 'chapter', n: '04', title: 'Validate — the scenario suite',
    desc: 'Twelve independent test cases, each mapping to an F5 how-to, then their reports' },
  { id: 'ch5', kind: 'chapter', n: '05', title: 'bnk-forge integration',
    desc: 'The cluster auto-registers — health and live traffic flow in the F5 BNK view' },
  { id: 'ch6', kind: 'chapter', n: '06', title: 'Tear it all down',
    desc: 'One prompt removes the cluster and restores the host' },
  { id: 'close', kind: 'close',
    title: 'github.com/mwiget/ocibnkctl',
    sub: 'Deploy F5 BIG-IP Next for Kubernetes on any Docker host — driven by Claude Code' },
];

const css = `
  * { margin:0; padding:0; box-sizing:border-box; }
  body { width:1920px; height:1080px; overflow:hidden;
    background: radial-gradient(1200px 800px at 30% 20%, #161b22 0%, #0d1117 60%, #090c10 100%);
    color:#e6edf3; font-family:'DejaVu Sans','Liberation Sans',sans-serif;
    display:flex; align-items:center; justify-content:center; }
  .wrap { width:1500px; }
  .accent { width:120px; height:8px; border-radius:4px;
    background:linear-gradient(90deg,#E4002B,#ff5a36); margin-bottom:48px; }
  .kicker { font-size:30px; letter-spacing:6px; text-transform:uppercase; color:#7d8590; margin-bottom:24px; }
  .title { font-size:84px; font-weight:800; line-height:1.05; letter-spacing:-1px; }
  .title.small { font-size:66px; }
  .badge { display:inline-block; margin-left:28px; font-size:40px; font-weight:700;
    color:#fff; background:#E4002B; padding:6px 22px; border-radius:14px; vertical-align:middle; }
  .sub { font-size:38px; color:#9da7b1; margin-top:40px; line-height:1.4; }
  .num { font-size:200px; font-weight:800; color:#21262d; line-height:1; margin-bottom:-30px; }
  .footer { position:absolute; bottom:60px; left:210px; font-size:26px; color:#586069;
    font-family:'DejaVu Sans Mono',monospace; }
  .mono { font-family:'DejaVu Sans Mono',monospace; color:#3fb950; }
`;

function html(s) {
  if (s.kind === 'title') return `<div class="wrap">
    <div class="accent"></div>
    <div class="kicker">Agentic infrastructure</div>
    <div class="title">${s.title}<span class="badge">${s.badge}</span></div>
    <div class="sub">${s.sub}</div></div>
    <div class="footer">ocibnkctl · Claude Code · local model</div>`;
  if (s.kind === 'chapter') return `<div class="wrap">
    <div class="num">${s.n}</div>
    <div class="accent"></div>
    <div class="title">${s.title}</div>
    <div class="sub">${s.desc}</div></div>
    <div class="footer">F5 BNK 2.3.0 · driven by Claude Code</div>`;
  return `<div class="wrap">
    <div class="accent"></div>
    <div class="kicker">Try it yourself</div>
    <div class="title small mono">${s.title}</div>
    <div class="sub">${s.sub}</div></div>
    <div class="footer">F5 BIG-IP Next for Kubernetes 2.3.0</div>`;
}

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1920, height: 1080 }, deviceScaleFactor: 1 });
  for (const s of slides) {
    await page.setContent(`<style>${css}</style>${html(s)}`, { waitUntil: 'networkidle' });
    await page.screenshot({ path: `/home/mwiget/demo-rec/slides/${s.id}.png` });
    console.log('  slide', s.id);
  }
  await browser.close();
})().catch(e => { console.error('ERR', e.message); process.exit(1); });
