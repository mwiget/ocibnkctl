// Render transparent "user prompt" banners to overlay on each beat.
const { chromium } = require('playwright');

const banners = [
  { id: 'b0', tag: 'COMMAND', text: 'ocibnkctl init demo' },
  { id: 'b1', tag: 'YOU', text: 'Read AGENTS.md and check if this host is ready to deploy' },
  { id: 'b2', tag: 'YOU', text: 'Install my F5 license keys, then validate the PoC' },
  { id: 'b3', tag: 'YOU', text: 'Deploy the full BNK stack, end to end' },
  { id: 'b4', tag: 'YOU', text: 'Show me all pods in all namespaces' },
  { id: 'b5', tag: 'YOU', text: 'A pod is stuck Pending — what is the root cause?' },
  { id: 'b6', tag: 'YOU', text: 'Run all the green scenarios and report the pass count' },
  { id: 'b7', tag: 'YOU', text: 'Open one scenario report and show what it verified' },
  { id: 'b9', tag: 'YOU', text: 'Tear it all down' },
];

const css = `
  * { margin:0; padding:0; box-sizing:border-box; }
  html,body { background:transparent; }
  body { width:1680px; height:96px; display:flex; align-items:center;
    font-family:'DejaVu Sans Mono',monospace; }
  .pill { display:flex; align-items:center; gap:18px; width:100%;
    background:rgba(13,17,23,0.92); border:1px solid #30363d; border-left:6px solid #3fb950;
    border-radius:14px; padding:16px 26px; box-shadow:0 6px 24px rgba(0,0,0,0.5); }
  .tag { flex:none; font-size:24px; font-weight:700; letter-spacing:2px; color:#0d1117;
    background:#3fb950; padding:5px 14px; border-radius:8px; }
  .tag.cmd { background:#58a6ff; }
  .caret { color:#3fb950; font-size:30px; font-weight:700; }
  .text { font-size:30px; color:#e6edf3; white-space:nowrap; overflow:hidden; }
`;

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1680, height: 96 } });
  for (const b of banners) {
    const cmd = b.tag === 'COMMAND' ? 'cmd' : '';
    const caret = b.tag === 'COMMAND' ? '$' : '❯';
    await page.setContent(`<style>${css}</style>
      <div class="pill"><span class="tag ${cmd}">${b.tag}</span>
      <span class="caret">${caret}</span><span class="text">${b.text}</span></div>`,
      { waitUntil: 'networkidle' });
    await page.screenshot({ path: `/home/mwiget/demo-rec/slides/${b.id}.png`, omitBackground: true });
    console.log('  banner', b.id);
  }
  await browser.close();
})().catch(e => { console.error('ERR', e.message); process.exit(1); });
