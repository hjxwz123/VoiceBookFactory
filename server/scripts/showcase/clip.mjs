// Records the landing page's moving picture: the workspace of 《雾港来信》 in continuous playback,
// the playing line highlighted and followed. Frames come from Chrome's screencast; ffmpeg encodes them.
// usage: node scripts/showcase/clip.mjs <base> <bookId> <chapterId> <out.mp4>
import { spawn, execFileSync } from 'node:child_process'
import { mkdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const [base, bid, cid, out] = process.argv.slice(2)
const dir = join(tmpdir(), 'vb-clip-' + Date.now())
mkdirSync(dir, { recursive: true })
const port = 9800 + Math.floor(Math.random() * 100)
const chrome = spawn('google-chrome', ['--headless=new', '--no-sandbox', '--disable-gpu', '--hide-scrollbars', '--autoplay-policy=no-user-gesture-required', `--remote-debugging-port=${port}`, 'about:blank'], { stdio: 'ignore' })
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
const frames = []
let recording = false
ws.onmessage = (m) => {
  const d = JSON.parse(m.data)
  if (d.id && pending.has(d.id)) { pending.get(d.id)(d.result ?? d); pending.delete(d.id) }
  if (d.method === 'Page.screencastFrame') {
    ws.send(JSON.stringify({ id: 900000 + frames.length, method: 'Page.screencastFrameAck', params: { sessionId: d.params.sessionId } }))
    if (recording) frames.push({ t: d.params.metadata.timestamp, data: d.params.data })
  }
}
const send = (method, params = {}) => new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method, params })) })
const js = async (expression) => (await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true })).result?.value
await send('Page.enable'); await send('Runtime.enable')
await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1.5, mobile: false })
const THEME = process.env.VB_THEME === 'dark' ? 'dark' : 'light' // VB_THEME=dark captures the night version
await send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: THEME }] })
await send('Page.navigate', { url: base + '/login' }); await sleep(1500)
await js(`localStorage.setItem('vb-theme', '"${THEME}"')`)
await js(`fetch('/api/v1/auth/login',{method:'POST',credentials:'include',headers:{'content-type':'application/json'},body:JSON.stringify({email:'showcase@example.com',password:'Showcase#2026'})}).then(r=>r.status)`)
await send('Page.navigate', { url: `${base}/books/${bid}/chapters/${cid}` }); await sleep(3500)
const clickText = async (text) => {
  const p = await js(`(()=>{const el=[...document.querySelectorAll('button')].find(b=>b.textContent.trim()===${JSON.stringify(text)});if(!el)return null;const b=el.getBoundingClientRect();return {x:b.left+b.width/2,y:b.top+b.height/2}})()`)
  if (!p) return false
  for (const type of ['mousePressed', 'mouseReleased']) await send('Input.dispatchMouseEvent', { type, x: p.x, y: p.y, button: 'left', clickCount: 1 })
  return true
}
await send('Page.startScreencast', { format: 'jpeg', quality: 88, everyNthFrame: 1, maxWidth: 2160, maxHeight: 1350 })
await sleep(500)
recording = true
await sleep(600)
console.log('play', await clickText('连续播放'))
await sleep(11000)
recording = false
await send('Page.stopScreencast')
ws.close(); chrome.kill()

// constant-rate video from the timestamped frames
let list = ''
frames.forEach((f, i) => {
  const name = `f${String(i).padStart(5, '0')}.jpg`
  writeFileSync(join(dir, name), Buffer.from(f.data, 'base64'))
  const next = frames[i + 1]?.t ?? f.t + 0.04
  list += `file '${name}'\nduration ${Math.max(0.01, next - f.t).toFixed(4)}\n`
})
writeFileSync(join(dir, 'list.txt'), list)
console.log('frames', frames.length)
// the region the hero tile shows: speaker column, lines and the highlight (1440x900 layout, frames at 1.5x)
execFileSync('ffmpeg', ['-y', '-loglevel', 'error', '-f', 'concat', '-safe', '0', '-i', join(dir, 'list.txt'),
  '-vf', 'crop=iw*1500/2160:ih*838/1350:iw*90/2160:ih*330/1350,scale=1376:768:flags=lanczos,fps=30,format=yuv420p',
  '-c:v', 'libx264', '-preset', 'slow', '-crf', '27', '-movflags', '+faststart', '-an', out], { stdio: 'inherit' })
rmSync(dir, { recursive: true, force: true })
console.log('wrote', out)
