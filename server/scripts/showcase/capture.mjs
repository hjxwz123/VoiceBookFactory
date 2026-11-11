// Captures the landing-page screenshots from the running app (showcase account, light theme, 2x).
// usage: node scripts/showcase/capture.mjs <base> <outdir> <bookId> <chapterId> [only]
// Writes <name>.webp files. Needs google-chrome and Node 22.
import { spawn } from 'node:child_process'
import { mkdirSync, writeFileSync } from 'node:fs'

const [base, out, bid, cid, only] = process.argv.slice(2)
mkdirSync(out, { recursive: true })
const port = 9400 + Math.floor(Math.random() * 400)
const chrome = spawn('google-chrome', ['--headless=new', '--no-sandbox', '--disable-gpu', '--hide-scrollbars', `--remote-debugging-port=${port}`, 'about:blank'], { stdio: 'ignore' })
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
let ws
for (let i = 0; i < 60; i++) {
  try {
    const list = await (await fetch(`http://127.0.0.1:${port}/json`)).json()
    ws = new WebSocket(list.find((t) => t.type === 'page').webSocketDebuggerUrl)
    await new Promise((r, j) => { ws.onopen = r; ws.onerror = j })
    break
  } catch { await sleep(200) }
}
let id = 0
const pending = new Map()
ws.onmessage = (m) => { const d = JSON.parse(m.data); if (d.id && pending.has(d.id)) { pending.get(d.id)(d.result ?? d); pending.delete(d.id) } }
const send = (method, params = {}) => new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method, params })) })
const js = async (expression) => (await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true })).result?.value
await send('Page.enable'); await send('Runtime.enable')
const viewport = (w, h) => send('Emulation.setDeviceMetricsOverride', { width: w, height: h, deviceScaleFactor: 2, mobile: false })
const THEME = process.env.VB_THEME === 'dark' ? 'dark' : 'light' // VB_THEME=dark captures the night version
await send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: THEME }] })
await viewport(1440, 900)
await send('Page.navigate', { url: base + '/login' }); await sleep(1500)
await js(`localStorage.setItem('vb-theme', '"${THEME}"')`)
console.log('login', await js(`fetch('/api/v1/auth/login',{method:'POST',credentials:'include',headers:{'content-type':'application/json'},body:JSON.stringify({email:'showcase@example.com',password:'Showcase#2026'})}).then(r=>r.status)`))

const click = (text) => js(`(()=>{const t=${JSON.stringify(text)};const els=[...document.querySelectorAll('button,a,[role=tab],[role=menuitem],label,span,div')].filter(e=>e.offsetParent&&e.textContent.trim()===t);const el=els[els.length-1];if(!el)return 'missing';el.scrollIntoView({block:'center'});el.click();return 'ok'})()`)
const shot = async (name, clip) => {
  await js(`document.querySelectorAll('[data-sonner-toaster]').forEach(e=>e.remove())`)
  const r = await send('Page.captureScreenshot', { format: 'webp', quality: 90, ...(clip ? { clip: { ...clip, scale: 1 } } : {}) })
  writeFileSync(`${out}/${name}.webp`, Buffer.from(r.data, 'base64'))
  console.log('shot', name)
}
// navigate, then wait until skeletons are gone and every image has loaded
const go = async (path, wait = 1500) => {
  await send('Page.navigate', { url: base + path }); await sleep(wait)
  for (let i = 0; i < 40; i++) {
    const busy = await js(`document.querySelectorAll('.skeleton').length + [...document.images].filter(i=>!i.complete).length`)
    if (!busy) break
    await sleep(250)
  }
  await sleep(600)
}

const scenes = {
  // the editor: multi-role script with a dialogue line selected
  async workspace() {
    await viewport(1440, 900)
    await go(`/books/${bid}/chapters/${cid}`, 3000)
    // select the line where 周伯 lowers his voice (a real mouse click), so the side panel shows its direction, emotion and voice
    const pos = await js(`(()=>{const rows=[...document.querySelectorAll('[data-index]')];const r=rows.find(e=>e.textContent.includes('灯塔那边'));if(!r)return null;const b=r.getBoundingClientRect();return {x:b.left+90,y:b.top+20}})()`)
    if (pos) {
      for (const type of ['mousePressed', 'mouseReleased']) await send('Input.dispatchMouseEvent', { type, x: pos.x, y: pos.y, button: 'left', clickCount: 1 })
    }
    await sleep(1400)
    await shot('workspace')
  },
  async characters() { await viewport(1440, 900); await go(`/books/${bid}/characters`); await shot('characters') },
  async lexicon() { await viewport(1440, 900); await go(`/books/${bid}/lexicon`); await shot('lexicon') },
  async review() { await viewport(1440, 900); await go(`/books/${bid}/review`); await shot('review') },
  async deliver() { await viewport(1440, 900); await go(`/books/${bid}/deliver`); await shot('deliver') },
  async overview() { await viewport(1440, 900); await go(`/books/${bid}`); await shot('overview') },
  async chapters() { await viewport(1440, 900); await go(`/books/${bid}/chapters`); await shot('chapters') },
  async crew() { await viewport(1440, 900); await go(`/books/${bid}/crew`); await shot('crew') },
  async voices() { await viewport(1440, 900); await go(`/voices`, 3000); await shot('voices') },
  async autobook() { await viewport(1440, 900); await go(`/autobook`); await shot('autobook') },
  async dashboard() { await viewport(1440, 900); await go(`/`); await shot('dashboard') },
  async books() { await viewport(1440, 900); await go(`/books`); await shot('books') },
}
for (const [name, fn] of Object.entries(scenes)) {
  if (only && !only.split(',').includes(name)) continue
  try { await fn() } catch (e) { console.log('fail', name, e.message) }
}
ws.close(); chrome.kill()
