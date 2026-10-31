// 干跑（node userscript/test/dry-run.mjs）：
//  假 DOM + 假 fetch + 合成图片，跑完整 solve()，重点统计「背景图/拼图块到底被下载了几次」。
//  断言的期望值：复用页面上已显示的题 = 0 次下载；自己换题 = 每张图正好 1 次；
//  重复 solve / #tips 反复重绘 = 不再产生任何图片请求。
import { readFileSync } from 'node:fs';

const SRC = readFileSync(new URL('../zf-jwglxt-slider-captcha.user.js', import.meta.url), 'utf8');

/* ---------------- 合成图片 ---------------- */
const PIECE_W = 64;
const PIECE_H = 200;
const BG_W = 304;
const BG_H = 200;
const GAP = 150; // 真缺口 x

function rng(seed) {
  let s = seed >>> 0;
  return () => {
    s = (s * 1664525 + 1013904223) >>> 0;
    return s / 4294967296;
  };
}

function makeImages() {
  const r = rng(20240513);
  const piece = new Uint8ClampedArray(PIECE_W * PIECE_H * 4).fill(255); // 默认纯白不透明
  const shape = [];
  for (let y = 70; y < 130; y++) {
    for (let x = 0; x < PIECE_W; x++) {
      const i = (y * PIECE_W + x) * 4;
      const v = Math.floor(r() * 120);
      piece[i] = v;
      piece[i + 1] = v + 40;
      piece[i + 2] = v + 80;
      piece[i + 3] = 255;
      shape.push([x, y, piece[i], piece[i + 1], piece[i + 2]]);
    }
  }
  const bg = new Uint8ClampedArray(BG_W * BG_H * 4);
  for (let i = 0; i < BG_W * BG_H; i++) {
    bg[i * 4] = Math.floor(r() * 256);
    bg[i * 4 + 1] = Math.floor(r() * 256);
    bg[i * 4 + 2] = Math.floor(r() * 256);
    bg[i * 4 + 3] = 255;
  }
  for (const [x, y, pr, pg, pb] of shape) {
    const i = (y * BG_W + (x + GAP)) * 4; // 半透明白蒙版 = 线性变换，NCC 不受影响
    bg[i] = 0.55 * pr + 110;
    bg[i + 1] = 0.55 * pg + 110;
    bg[i + 2] = 0.55 * pb + 110;
    bg[i + 3] = 255;
  }
  return {
    si: { w: BG_W, h: BG_H, data: bg },
    mi: { w: PIECE_W, h: PIECE_H, data: piece },
  };
}

const IMG = makeImages();
const registry = new Map([
  ['si', IMG.si],
  ['mi', IMG.mi],
]);

/* ---------------- 假 fetch / blob / canvas ---------------- */
const requests = []; // 所有经过 fetch 的 URL
const imageRequests = []; // 只统计图片
const objectUrlMap = new Map();
let objectUrlSeq = 0;

function textResp(text) {
  return { ok: true, status: 200, text: async () => text, blob: async () => null };
}
function blobResp(key) {
  return {
    ok: true,
    status: 200,
    text: async () => '',
    blob: async () => ({ __key: key, type: 'image/png' }),
  };
}

let lastTrack = null;
let lastTrackAt = 0;
globalThis.fetch = async (url, opts) => {
  const u = String(url);
  requests.push(u);
  if (opts && opts.method === 'POST') {
    const mt = new URLSearchParams(opts.body).get('mt');
    lastTrack = JSON.parse(new TextDecoder().decode(Uint8Array.from(atob(mt), (c) => c.charCodeAt(0))));
    lastTrackAt = Date.now();
    return textResp(JSON.stringify({ vs: 'verified', status: 'success' }));
  }
  if (u.includes('type=refresh')) return textResp(JSON.stringify({ si: 1, mi: 2, imtk: 'IMTK', t: 777 }));
  if (u.includes('type=image')) {
    const key = /id=1\b/.test(u) ? 'si' : 'mi';
    imageRequests.push(u);
    return blobResp(key);
  }
  return textResp('{}');
};

globalThis.URL.createObjectURL = (blob) => {
  const u = `blob:https://jw.example/${++objectUrlSeq}`;
  objectUrlMap.set(u, blob);
  return u;
};
globalThis.URL.revokeObjectURL = () => {};

globalThis.createImageBitmap = async (blob) => {
  const img = registry.get(blob.__key);
  return { width: img.w, height: img.h, __key: blob.__key, close() {} };
};

class FakeImage {
  constructor() {
    this.__isDomImg = true;
    this.complete = false;
    this.naturalWidth = 0;
    this.naturalHeight = 0;
    this._src = '';
    this.onload = null;
    this.onerror = null;
  }
  set src(v) {
    this._src = v;
    let key = null;
    if (v.startsWith('blob:')) {
      const blob = objectUrlMap.get(v);
      key = blob && blob.__key;
    } else if (/id=1\b/.test(v)) key = 'si';
    else if (/id=2\b/.test(v)) key = 'mi';
    if (key && registry.has(key)) {
      const img = registry.get(key);
      this.__key = key;
      this.naturalWidth = img.w;
      this.naturalHeight = img.h;
      this.complete = true;
      setTimeout(() => this.onload && this.onload(), 0);
    } else {
      this.complete = true;
      this.naturalWidth = 0;
    }
  }
  get src() {
    return this._src;
  }
  getAttribute(n) {
    return n === 'src' ? this._src : null;
  }
  getBoundingClientRect() {
    return { left: 0, top: 0, width: 152, height: 100 };
  }
}

function fakeCanvas() {
  const cv = { width: 0, height: 0, __key: null, __fromDom: false, style: {} };
  cv.getContext = () => ({
    drawImage: (src) => {
      cv.__key = src.__key;
      cv.__fromDom = !!src.__isDomImg;
    },
    getImageData: (x, y, w, h) => {
      if (taintDomCanvas && cv.__fromDom) {
        const e = new Error('The canvas has been tainted by cross-origin data.');
        e.name = 'SecurityError';
        throw e;
      }
      const img = registry.get(cv.__key);
      if (!img) throw new Error('空图');
      return { data: new Uint8ClampedArray(img.data), width: w, height: h };
    },
  });
  return cv;
}
let taintDomCanvas = false;

/* ---------------- 假 DOM ---------------- */
const pageBg = new FakeImage();
const pagePiece = new FakeImage();

const wrapper = {
  querySelector(sel) {
    if (sel === '.zfdun_bgimg_img') return pageBg;
    if (sel === '.zfdun_bgimg_jigsaw') return pagePiece;
    return null;
  },
};

const tipsEl = { textContent: '' };

let wrapperReady = false;
const listeners = [];
globalThis.document = {
  documentElement: {},
  body: { appendChild() {} },
  addEventListener: (t, fn) => listeners.push([t, fn]),
  querySelector: (sel) => (sel === '.captcha_wrapper' && wrapperReady ? wrapper : null),
  getElementById: (id) => (id === 'tips' ? tipsEl : null),
  createElement: (tag) => (tag === 'canvas' ? fakeCanvas() : { style: {}, classList: { add() {}, remove() {} } }),
};

let obsCallback = null;
globalThis.MutationObserver = class {
  constructor(cb) {
    obsCallback = cb;
  }
  observe() {}
};

globalThis.window = globalThis;
globalThis.zfdun_captcha_config = {
  requestUrl: 'https://jw.example/jwglxt/zfdun/api',
  rtk: 'RTK123',
  instanceId: 'zfcaptchaLogin',
};

// 屏蔽 boot 的轮询，测试里手动调 solve
const intervals = [];
const realSetInterval = globalThis.setInterval;
globalThis.setInterval = (fn, ms) => {
  const id = realSetInterval(fn, ms);
  intervals.push(id);
  return id;
};

new Function(SRC)();
intervals.forEach(clearInterval);

const api = globalThis.__zfSliderCaptcha;
api.CFG.debug = false;
api.CFG.cooldownMs = 0;
api.CFG.retryDelay = 10;

/* ---------------- 断言 ---------------- */
const results = [];
function check(name, ok, detail) {
  results.push({ name, ok, detail });
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? '  —— ' + detail : ''}`);
}

// 5. 轨迹：末点在未来 + 末点-首点 == 距离
{
  const dist = 180;
  const tr = api.genTrack(dist, 950, 480);
  const now = Date.now();
  check('genTrack 末点 t 落在未来', tr[tr.length - 1].t > now, `末点 t-now=${tr[tr.length - 1].t - now}ms`);
  check('genTrack 位移 = 缺口 x', tr[tr.length - 1].x - tr[0].x === dist, `${tr[0].x} → ${tr[tr.length - 1].x}`);
  check('genTrack 点数为 24', tr.length === 24 || tr.length > 10, `points=${tr.length}`);
}

// 从提交的 mt 里反推识别出的缺口（末点-首点）
function gapOf() {
  if (!lastTrack || !lastTrack.length) return null;
  return lastTrack[lastTrack.length - 1].x - lastTrack[0].x;
}

// 1. 复用页面上已经显示的那组题：0 次图片请求
{
  wrapperReady = true;
  pageBg.src = 'https://jw.example/jwglxt/zfdun/api?type=image&id=1&imtk=IMTK&t=1';
  pagePiece.src = 'https://jw.example/jwglxt/zfdun/api?type=image&id=2&imtk=IMTK&t=1';
  imageRequests.length = 0;
  api.solve('force');
  await new Promise((r) => setTimeout(r, 300));
  check('复用页面已加载的图：0 次图片下载', imageRequests.length === 0, `实际 ${imageRequests.length} 次`);
  check(
    '复用路径下页面图未被替换',
    pageBg.getAttribute('src').includes('type=image') && !pageBg.getAttribute('src').startsWith('blob:'),
    pageBg.getAttribute('src').slice(-40)
  );
  check('复用路径识别到缺口 150', gapOf() === 150, `gap=${gapOf()}`);
}

// 2. 页面上没有图（换题路径）：每张图只下载一次
{
  wrapperReady = true;
  pageBg.src = '';
  pagePiece.src = '';
  pageBg.complete = false;
  pagePiece.complete = false;
  imageRequests.length = 0;
  api.solve('force');
  await new Promise((r) => setTimeout(r, 400));
  const siReqs = imageRequests.filter((u) => /id=1\b/.test(u));
  const miReqs = imageRequests.filter((u) => /id=2\b/.test(u));
  check('换题路径：背景图只下载 1 次', siReqs.length === 1, `实际 ${siReqs.length} 次`);
  check('换题路径：拼图块只下载 1 次', miReqs.length === 1, `实际 ${miReqs.length} 次`);
  check('换题路径：总共只有 2 次图片请求', imageRequests.length === 2, `实际 ${imageRequests.length} 次`);
  check(
    '换题后页面用 blob 显示（不再触发第二次下载）',
    pageBg.getAttribute('src').startsWith('blob:') && pagePiece.getAttribute('src').startsWith('blob:'),
    `${pageBg.getAttribute('src')} / ${pagePiece.getAttribute('src')}`
  );
  check('换题路径识别到缺口 150', gapOf() === 150, `gap=${gapOf()}`);
}

// 3. 再次 solve（页面显示的就是上一步那份 blob）：仍然 0 次新请求
{
  imageRequests.length = 0;
  api.solve('force');
  await new Promise((r) => setTimeout(r, 300));
  check('重复 solve 不重复取图', imageRequests.length === 0, `实际 ${imageRequests.length} 次`);
  check('重复 solve 仍能识别缺口 150', gapOf() === 150, `gap=${gapOf()}`);
}

// 5. 页面图跨域、canvas 被污染时：回退成自己下载，且仍然每张只下一次
{
  wrapperReady = true;
  pageBg.src = 'https://jw.example/jwglxt/zfdun/api?type=image&id=1&imtk=IMTK&t=9';
  pagePiece.src = 'https://jw.example/jwglxt/zfdun/api?type=image&id=2&imtk=IMTK&t=9';
  taintDomCanvas = true;
  imageRequests.length = 0;
  api.solve('force');
  await new Promise((r) => setTimeout(r, 300));
  check('canvas 被跨域污染时回退到 fetch，每张仍只下 1 次', imageRequests.length === 2, `实际 ${imageRequests.length} 次`);
  check('污染回退时识别仍然正确', gapOf() === 150, `gap=${gapOf()}`);
  taintDomCanvas = false;
}

// 6. #tips 反复重绘：不重复触发（否则会反复 refresh + 反复取图）
{
  pageBg.src = '';
  pagePiece.src = '';
  pageBg.complete = false;
  pagePiece.complete = false;
  const refreshCount = () => requests.filter((u) => u.includes('type=refresh')).length;
  const base = refreshCount();
  tipsEl.textContent = '请先滑动图片进行验证！';
  for (let i = 0; i < 6; i++) obsCallback();
  await new Promise((r) => setTimeout(r, 400));
  const first = refreshCount() - base;
  for (let i = 0; i < 6; i++) obsCallback();
  await new Promise((r) => setTimeout(r, 400));
  const second = refreshCount() - base - first;
  check('tips 触发只换一次题', first === 1, `第一轮 ${first} 次`);
  check('tips 重复重绘不会反复取图', second === 0, `第二轮又跑了 ${second} 次`);
}

const failed = results.filter((r) => !r.ok);
console.log(`\n${results.length - failed.length}/${results.length} 通过`);
process.exit(failed.length ? 1 : 0);
