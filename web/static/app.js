'use strict';

/* =========================================================================
 * 工具
 * ========================================================================= */

const $  = (s, r = document) => r.querySelector(s);
const $$ = (s, r = document) => Array.from(r.querySelectorAll(s));

function esc(s) {
    if (s == null) return '';
    return String(s).replace(/[&<>"']/g,
        c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

// 教务系统的上课时间 / 地点是多段 HTML（"星期二第7-10节{2-3周}<br/>星期三..."）。
// 面板按纯文本渲染，直接 esc 会把 <br/> 原样显示成标签，所以先换成换行，
// 再由 .multiline（white-space: pre-line）把换行显示出来。
function brLines(s) {
    if (s == null) return '';
    return String(s)
        .replace(/<br\s*\/?>/gi, '\n')
        .replace(/\r\n?/g, '\n')
        .replace(/[ \t]*\n[ \t]*/g, '\n')
        .trim();
}

function pick(obj, ...keys) {
    if (!obj) return undefined;
    for (const k of keys) {
        const v = obj[k];
        if (v !== undefined && v !== null && v !== '') return v;
    }
    return undefined;
}

// 停留时间：绿色（成功）短一些，红色（失败）长一些，其余用默认值。
const TOAST_MS = { ok: 1100, err: 3500 };
const TOAST_MS_DEFAULT = 2500;

function toast(msg, kind = 'info') {
    const el = document.createElement('div');
    el.className = 'toast ' + kind;
    el.textContent = msg;
    $('#toast').appendChild(el);
    setTimeout(() => el.remove(), TOAST_MS[kind] || TOAST_MS_DEFAULT);
}

/* =========================================================================
 * API
 * ========================================================================= */

// apiErrorFrom 把后端的错误响应变成 Error：error 是给人看的文案，
// code 是给代码用的稳定语义（unauthorized / login_failed / no_permission…）。
function apiErrorFrom(res, data, path) {
    const msg = (data && data.error) || res.statusText || ('HTTP ' + res.status);
    const err = new Error(msg);
    err.status = res.status;
    err.code = (data && data.code) || '';
    // 会话失效只有一个出口：所有 API 调用都经过这里，调用方不必各自处理 401。
    if (err.code === 'unauthorized') {
        notifyUnauthorized(path, err);
    }
    return err;
}

const API = {
    async req(method, path, body, signal) {
        const opts = { method, headers: {} };
        if (body !== undefined) {
            opts.headers['Content-Type'] = 'application/json';
            opts.body = JSON.stringify(body);
        }
        if (signal) opts.signal = signal;
        const res = await fetch('/api' + path, opts);
        const text = await res.text();
        let data = null;
        try { data = text ? JSON.parse(text) : null; } catch { data = text; }
        if (!res.ok) throw apiErrorFrom(res, data, path);
        return data;
    },

    // 二进制响应（大头照）：错误仍按 JSON 解析，成功则返回 Blob 和 Content-Type。
    async blob(path) {
        const res = await fetch('/api' + path);
        if (!res.ok) {
            const text = await res.text();
            let data = null;
            try { data = text ? JSON.parse(text) : null; } catch { data = null; }
            throw apiErrorFrom(res, data, path);
        }
        return {
            blob: await res.blob(),
            contentType: res.headers.get('Content-Type') || '',
        };
    },

    // multipart 上传（模型文件 / 测试图片）：不能走 req，否则会被 JSON 序列化。
    async upload(path, file, field = 'file') {
        const fd = new FormData();
        fd.append(field, file, file.name || field);
        const res = await fetch('/api' + path, { method: 'POST', body: fd });
        const text = await res.text();
        let data = null;
        try { data = text ? JSON.parse(text) : null; } catch { data = text; }
        if (!res.ok) throw apiErrorFrom(res, data, path);
        return data;
    },

    listAccounts:   ()            => API.req('GET',    '/accounts'),
    createAccount:  (a)           => API.req('POST',   '/accounts', a),
    getAccount:     (id)          => API.req('GET',    '/accounts/' + id),
    updateAccount:  (id, a)       => API.req('PUT',    '/accounts/' + id, a),
    deleteAccount:  (id)          => API.req('DELETE', '/accounts/' + id),
    setAutoRelogin: (id, enabled) => API.req('PUT',    '/accounts/' + id + '/auto-relogin', { enabled }),
    setAutoNotify:  (id, enabled) => API.req('PUT',    '/accounts/' + id + '/auto-notify', { enabled }),

    status:         (id)          => API.req('GET',    `/accounts/${id}/status`),
    pendingInput:     (id)        => API.req('GET',  `/accounts/${id}/login/pending`),
    submitLoginInput: (id, value) => API.req('POST', `/accounts/${id}/login/input`, { value }),
    ping:           (id)          => API.req('GET',    `/accounts/${id}/ping`),
    login:          (id)          => API.req('POST',   `/accounts/${id}/login`),
    cancelLogin:    (id)          => API.req('POST',   `/accounts/${id}/login/cancel`),
    logout:         (id)          => API.req('POST',   `/accounts/${id}/logout`),
    init:           (id, force)   => API.req('POST',   `/accounts/${id}/init${force ? '?force=1' : ''}`),
    cancelInit:     (id)          => API.req('POST',   `/accounts/${id}/init/cancel`),

    tabs:           (id)          => API.req('GET',    `/accounts/${id}/tabs`),
    selectTab:      (id, i)       => API.req('POST',   `/accounts/${id}/tabs/${i}`),

    // 课程（新语义）
    courses:        (id, force)   => API.req('GET',
        `/accounts/${id}/courses${force ? '?force=1' : ''}`),
    cachedCourses:  (id)          => API.req('GET',
        `/accounts/${id}/courses/cache`),
    detail:         (id, kch)     => API.req('GET',
        `/accounts/${id}/courses/${encodeURIComponent(kch)}`),
    clearCourses:   (id)          => API.req('DELETE', `/accounts/${id}/courses`),

    choose:         (id, body)    => API.req('POST',   `/accounts/${id}/choose`, body),
    quit:           (id, body)    => API.req('POST',   `/accounts/${id}/quit`, body),
    selected:       (id, xnm, xqm) =>
        API.req('GET', `/accounts/${id}/selected?xnm=${encodeURIComponent(xnm)}&xqm=${encodeURIComponent(xqm)}`),

    // 高级选课：愿望清单持久化在账号里，自动选课按清单反复尝试
    getWish:        (id)          => API.req('GET',    `/accounts/${id}/wish`),
    updateWish:     (id, w)       => API.req('PUT',    `/accounts/${id}/wish`, w),
    autoChoose:     (id, body)    => API.req('POST',   `/accounts/${id}/auto-choose`, body),
    cancelAuto:     (id)          => API.req('POST',   `/accounts/${id}/auto-choose/cancel`),

    scores:        (id, xnm, xqm) =>
        API.req('GET', `/accounts/${id}/score?xnm=${encodeURIComponent(xnm)}&xqm=${encodeURIComponent(xqm)}`),
    scoreSuggest:  (id) => API.req('GET', `/accounts/${id}/score/suggest`),

    // 其他工具：个人信息 / 大头照 / Cookie / 会话诊断
    userInfo:      (id) => API.req('GET', `/accounts/${id}/tools/info`),
    cookies:       (id) => API.req('GET', `/accounts/${id}/tools/cookies`),
    toolsDebug:    (id) => API.req('GET', `/accounts/${id}/tools/debug`),
    photo:         (id) => API.blob(`/accounts/${id}/tools/photo`),

    // 微信扫码登录：不经过账号，扫码成功后才按 ticket 里的身份绑定或新建
    wechatStart:    ()            => API.req('POST',   '/wechat/login'),
    wechatStatus:   ()            => API.req('GET',    '/wechat/status'),
    wechatCancel:   ()            => API.req('POST',   '/wechat/cancel'),

    // 服务端配置（全局）：目前是 SMTP 邮件通知
    server:         ()            => API.req('GET',    '/server'),
    saveServer:     (s)           => API.req('PUT',    '/server', s),
    testSMTP:       (s)           => API.req('POST',   '/server/smtp/test', s),

    // 字符验证码模型（全局）
    kaptcha:        ()            => API.req('GET',    '/kaptcha'),
    saveKaptcha:    (s)           => API.req('PUT',    '/kaptcha', s),
    reloadKaptcha:  ()            => API.req('POST',   '/kaptcha/reload'),
    kaptchaModel:   (file)        => API.upload('/kaptcha/model', file),
    kaptchaTest:    (file)        => API.upload('/kaptcha/test', file, 'image'),
};
/* =========================================================================
 * 全局状态
 * ========================================================================= */

const state = {
    accounts: [],
    currentId: null,
    current: null,
    status: null,
    modeName: null,      // 服务端上次返回的页签名（用于判断是否需要重新拉页签列表）
    tabs: [],
    courses: [],         // ← CustomCourseDic[]（完整信息）
    coursesLoading: false, // 正在从学校拉取课程列表（避免重复触发）
    scores: [],
    loadingKch: new Set(), // 防止重复点击「详情」
    queryBusy: {},          // 查询按钮忙态，按账号记（`id:kind`）：切账号不继承上一个账号的 spinner
    loggingIn: false,
    loginCancelled: false, // 本次登录是否由用户主动取消（用于避免重复提示）
    loginToken: 0,
    initializing: false,
    initForce: false,
    initToken: 0,
    tools: {
        info: null,
        photoUrl: null,
        photoType: '',
        cookies: null,
        debug: null,
    },
};

// 其他工具按钮的忙态。按请求发起时的账号校验回包，切账号不会把结果画错。
const toolsBusy = { info: false, photo: false, cookies: false, debug: false };

// /status 的单飞标记：多个调用点（任务收尾、兜底轮询）不会叠着拉
let statusInflight = null;
// statusGen：账号状态的"代"。本地改动一次状态（登录成功 / 登出成功…）就 +1，
// 用来作废"改动之前发出"的响应，否则旧快照会把界面改回去。
let statusGen = 0;
let statusInflightGen = -1;

// 已应用的帧标记。task.rev 只在同一次任务内单调，
// 因此必须和 task.id 配对使用：新任务从 rev≈0 重新开始，
// 只看 rev 会把新任务的帧全丢掉。
let appliedTaskId = 0;
let appliedRev = 0;

// staleFrame 判断一帧是不是"旧的"。旧的直接丢弃，避免界面被回退。
function staleFrame(t) {
    if (!t || typeof t.rev !== 'number') return false;
    const id = t.id || 0;
    if (id < appliedTaskId) return true;
    return id === appliedTaskId && t.rev < appliedRev;
}

function markApplied(t) {
    if (!t || typeof t.rev !== 'number') return;
    const id = t.id || 0;
    if (id > appliedTaskId) {
        appliedTaskId = id;
        appliedRev = t.rev;
        return;
    }
    appliedRev = t.rev;
}

const dialogState = { mode: 'create', id: null };

/* =========================================================================
 * 长任务（登录 / 初始化）运行态
 *
 * 设计要点：状态的唯一真相在后端。
 *   - 正常路径：一条 SSE 长连接（/accounts/:id/events）把后端快照推过来，
 *     任务期间不再按 600ms 轮询 /status；
 *   - 兜底路径：SSE 不可用（老浏览器 / 服务端异常）或页面在后台时才退回
 *     定时拉 /status；
 *   - 刷新页面或切账号都会重建连接，第一帧就是完整快照，因此能直接接管
 *     正在跑的任务。
 * ========================================================================= */

const Task = {
    last: null,     // 上一次拿到的 task 快照
    base: null,     // { elapsedMs, stageMs, at } —— 用于本地平滑推进计时
    timer: null,    // 1s 计时器（只重绘，不发请求）
    stream: null,   // EventSource
    streamId: null, // 连接对应的账号
    connected: false,// 当前是否连通（断开时用兜底轮询顶上）
    poll: null,     // 兜底轮询定时器
    pollMs: 4000,
    inFlight: false,
    needsRefresh: false, // 任务刚结束时补一次完整状态刷新
};

const TASK_STATUS_TEXT = {
    done: '已完成',
    cancelled: '已取消',
    failed: '失败',
    timeout: '超时',
};

const TASK_KIND_TEXT = { login: '登录', init: '初始化', auto: '自动选课' };

/* ---------- 展示 ---------- */

function fmtDur(ms) {
    if (!isFinite(ms) || ms < 0) ms = 0;
    const s = ms / 1000;
    if (s < 60) return s.toFixed(s < 10 ? 1 : 0) + 's';
    const m = Math.floor(s / 60);
    return m + 'm' + String(Math.floor(s % 60)).padStart(2, '0') + 's';
}

function eventClass(e) {
    const k = e.kind || '';
    if (k.indexOf('task.input.') === 0) return 'ev-input';
    if (k === 'task.failed') return 'ev-failed';
    if (k === 'task.timeout') return 'ev-timeout';
    if (k === 'task.cancelled') return 'ev-cancelled';
    if (k === 'task.done') return 'ev-done';
    if (k === 'task.begin') return 'ev-begin';
    return '';
}

// 本地追加的提示事件（不出现在后端快照里，刷新后自然消失）
Task.apiEvents = function (kind, texts) {
    if (!Task.last) return;
    const evs = Task.last.events || (Task.last.events = []);
    const at = evs.length ? Math.max(Task.last.elapsedMs || 0, evs[evs.length - 1].atMs || 0) : 0;
    texts.forEach((t, i) => {
        evs.push({ atMs: at + i, timestamp: Date.now(), kind: kind, text: t, local: true });
    });
    Task.renderLog();
};

// 当前账号是否正在跑指定类型的任务
Task.running = function (kind) {
    const t = Task.last;
    return !!(t && t.running && (!kind || t.kind === kind));
};

/* ---------- 与后端同步 ---------- */

// apply 应用一帧任务快照。
// opts.fromStream=true 表示这帧来自推送：走秒与收尾刷新由推送驱动，不需要再拉一次。
Task.apply = function (snap, opts) {
    const fromStream = !!(opts && opts.fromStream);
    const prev = Task.last;
    const prevId = prev ? prev.id : 0;
    const next = snap || null;
    const switched = next && prevId && next.id !== prevId;

    if (switched) {
        // 后端开了新任务（或页面刚刷新后接管）：日志整体替换
        Task.last = next;
        Task.renderLog(true);
    } else if (!next) {
        return;
    } else {
        Task.last = next;
        Task.renderLog(false);
    }

    Task.base = {
        elapsedMs: next.elapsedMs || 0,
        stageMs: next.stageMs || 0,
        at: Date.now(),
    };
    Task.renderBar();

    const running = !!next.running;
    if (running) {
        if (Task.timer === null) {
            Task.timer = setInterval(() => { Task.renderBar(); }, 1000);
        }
    } else {
        if (Task.timer !== null) { clearInterval(Task.timer); Task.timer = null; }
    }

    // 只在"运行中 → 刚结束"这一帧记一笔：收尾后补一次完整刷新（页签 / 课程都变了）。
    // 不能在 running 期间一直置位：刷新 /status 时 applyStatusPayload 会走到
    // maybeRefreshDetails，而它又要走 refreshStatus 的单飞，等于让这一轮刷新
    // await 自己，永远不结束（ping 按钮转圈不消失的根因）。
    if (prev && prev.running && !running) {
        Task.needsRefresh = true;
    }

    // 状态跟着服务端走：刷新后按钮/徽标能自动回到正确形态
    state.loggingIn = !!(running && next.kind === 'login');
    state.initializing = !!(running && next.kind === 'init');
    if (!state.initializing) state.initForce = false;
    renderInitButtons();
    renderStatus();

    // 推送路径下，收尾后补一次完整刷新（页签、课程缓存都变了）
    if (!fromStream && !running) {
        Task.maybeRefreshDetails();
    }
};

Task.clear = function () {
    Task.last = null;
    Task.base = null;
    if (Task.timer !== null) { clearInterval(Task.timer); Task.timer = null; }
    const panel = $('#task-panel');
    if (panel) panel.hidden = true;
    const ev = $('#task-events');
    if (ev) ev.innerHTML = '';
};

/* ---------- 推送（SSE）：任务期间不轮询 ---------- */

// setStream 建立 / 重建推送连接。
// 每次重建都会重新收到一帧完整快照，所以切账号、页面从后台恢复都不需要额外拉取。
Task.setStream = function () {
    if (typeof EventSource === 'undefined') return; // 老浏览器：走兜底轮询
    if (Task.stream) return;
    if (!state.currentId) return;

    Task.connected = false;  // 当前是否处于连通状态
    const es = new EventSource(`/api/accounts/${state.currentId}/events`);

    es.addEventListener('task', (ev) => {
        if (!ev.data) return;
        Task.connected = true;
        Task.setPoll(false);
        let frame = null;
        try { frame = JSON.parse(ev.data); } catch (e) { return; }
        // 期间切了账号：结果作废（连接本身也会被重建）
        if (!frame || String(state.currentId) !== String(Task.streamId)) return;
        if (frame.type === 'gone') { loadAccounts(); return; }
        applyTaskFrame(frame);
    });

    // 连接断开：EventSource 会自己重试。重试期间不能让界面停更，
    // 所以立刻开兜底轮询；连接一旦恢复（上面的 task 事件）就把轮询关掉。
    es.onerror = () => {
        Task.connected = false;
        Task.setPoll(true);
    };

    Task.stream = es;
    Task.streamId = String(state.currentId);
};

// setPoll 开关兜底轮询（推送不可用 / 页面在后台时用）。
// 一旦确认推送连通（connected）就立刻关掉，避免两条路径同时打 /status。
Task.setPoll = function (on) {
    if (on && !Task.poll) {
        // 慢启动：给 EventSource 一点握手时间，别刚开页面就先拉一次
        Task.poll = setTimeout(() => {
            Task.poll = null;
            if (Task.connected || !state.currentId) return;
            Task.tick();
            Task.setPoll(true);
        }, Task.pollMs);
    } else if (!on && Task.poll) {
        clearTimeout(Task.poll);
        Task.poll = null;
    }
};

Task.closeStream = function () {
    if (Task.stream) {
        try { Task.stream.close(); } catch (e) { /* 忽略 */ }
        Task.stream = null;
    }
    Task.connected = false;
};

// ensure 在"需要看状态"的场景（启动 / 切账号）调用一次
Task.ensure = function () {
    Task.setStream();
};

// retry 页面从后台恢复、或推送断开后重新接管
Task.retry = function () {
    Task.closeStream();
    Task.setStream();
};

// stop 彻底停掉（当前没有调用点，保留给将来）
Task.stop = function () {
    Task.closeStream();
    Task.setPoll(false);
    if (Task.timer !== null) { clearInterval(Task.timer); Task.timer = null; }
};

Task.tick = async function () {
    if (Task.inFlight || !state.currentId) return;
    Task.inFlight = true;
    try {
        await refreshStatus({ quiet: true });
    } catch (e) {
        /* 网络抖动忽略，下一轮继续 */
    } finally {
        Task.inFlight = false;
    }
};

// 任务刚跑完时补一次完整刷新（页签、课程缓存等都变了）
//
// 注意：不能在"正在刷新的那一轮"里直接 refreshStatus()——单飞会把这轮自己
// 返回给调用方，await 自己就永远不结束。此时挂到它结束之后再来一轮。
Task.maybeRefreshDetails = function () {
    if (!Task.needsRefresh) return;
    Task.needsRefresh = false;
    if (statusInflight) {
        statusInflight.finally(() => {
            Task.needsRefresh = true;
            Task.maybeRefreshDetails();
        }).catch(() => { /* 上一轮失败与否都不影响这次补刷 */ });
        return;
    }
    refreshStatus({ quiet: true }).catch(() => { /* 忽略 */ });
};

/* 页面在前台时用推送；切到后台就断开连接、退回低频轮询，
   避免挂着没人看的标签页还在持续收流。 */
document.addEventListener('visibilitychange', () => {
    if (document.hidden) {
        Task.closeStream();
        Task.setPoll(true);
    } else {
        Task.setPoll(false);
        Task.retry();
        refreshStatus({ quiet: true }).catch(() => {});
    }
});

/* ---------- 渲染 ---------- */

Task.renderBar = function () {
    const panel = $('#task-panel');
    const snap = Task.last;
    if (!panel) return;
    if (!snap) { panel.hidden = true; return; }

    panel.hidden = false;
    panel.className = 'task-panel' + (snap.running ? ' running' : ' ' + (snap.status || ''));

    const running = !!snap.running;
    $('#task-spinner').hidden = !running;
    $('#task-title').textContent = (TASK_KIND_TEXT[snap.kind] || '任务') +
        (running ? '进行中' : ' · ' + (TASK_STATUS_TEXT[snap.status] || snap.status || ''));

    // 阶段：优先用本地计时推进，避免每秒拉一次接口
    const stageText = snap.stageText || '';
    let stageMs = snap.stageMs || 0;
    let elapsedMs = snap.elapsedMs || 0;
    if (running && Task.base) {
        const d = Date.now() - Task.base.at;
        elapsedMs = Task.base.elapsedMs + d;
        stageMs = Task.base.stageMs + d;
    }
    $('#task-stage').textContent = stageText;

    const meta = [];
    meta.push((running ? '已等待 ' : '用时 ') + fmtDur(elapsedMs));
    if (running && stageMs > 4000) meta.push('本阶段 ' + fmtDur(stageMs));
    if (snap.requests) meta.push('请求 ' + snap.requests + ' 次');
    $('#task-meta').textContent = meta.join(' · ');

    const cancelBtn = $('#task-cancel');
    cancelBtn.hidden = !running;
    cancelBtn.disabled = false;
};

Task.renderLog = function (replace) {
    const ol = $('#task-events');
    const snap = Task.last;
    if (!ol || !snap) return;
    const evs = snap.events || [];

    // 事件条数没变就不动 DOM，避免每次轮询都打断用户的滚动/复制
    if (!replace && ol.childElementCount === evs.length && ol.dataset.taskId === String(snap.id)) {
        Task.markCurrent();
        return;
    }
    if (replace) ol.innerHTML = '';

    const nearBottom = ol.scrollHeight - ol.scrollTop - ol.clientHeight < 24;
    const start = (snap.startedAt || Date.now()) - (snap.elapsedMs || 0);
    ol.innerHTML = evs.map(e => {
        const at = typeof e.atMs === 'number' ? e.atMs
            : (e.timestamp ? e.timestamp - start : 0);
        const cls = eventClass(e);
        const repeat = (e.repeat && e.repeat > 1)
            ? ` <span class="ev-repeat">×${e.repeat}</span>` : '';
        return `<li class="${cls}${e.local ? ' ev-local' : ''}">` +
            `<span class="ev-t">+${fmtDur(at)}</span>` +
            `<span class="ev-text">${esc(e.text)}${repeat}` +
            (e.detail ? ` <span class="ev-detail">${esc(e.detail)}</span>` : '') +
            `</span></li>`;
    }).join('');
    ol.dataset.taskId = String(snap.id);

    $('#task-log-summary').textContent =
        `执行日志（${evs.length} 条${snap.running ? '，进行中' : ''}）`;

    if (nearBottom) ol.scrollTop = ol.scrollHeight;
    Task.markCurrent();
};

// 给"当前阶段"那条事件加个标记，便于一眼看到进度位置
Task.markCurrent = function () {
    const ol = $('#task-events');
    if (!ol) return;
    $$('li.ev-current', ol).forEach(li => li.classList.remove('ev-current'));
    const items = ol.children;
    if (items.length && Task.last && Task.last.running) {
        items[items.length - 1].classList.add('ev-current');
    }
};


/* =========================================================================
 * 侧边栏
 * ========================================================================= */

async function loadAccounts() {
    try {
        state.accounts = (await API.listAccounts()) || [];
    } catch (e) {
        toast('加载账号失败: ' + e.message, 'err');
        state.accounts = [];
    }
    renderAccountList();
    // 列表刷新后把当前账号对象换成新的（编辑账号后 autoRelogin 等字段会变）
    if (state.currentId) {
        state.current = state.accounts.find(a => String(a.id) === String(state.currentId)) || null;
        renderAutoRelogin();
        renderAutoNotify();
    }
}

function renderAccountList() {
    const ul = $('#account-list');
    const empty = $('#sidebar-empty');
    if (!state.accounts.length) {
        ul.innerHTML = '';
        empty.hidden = false;
        return;
    }
    empty.hidden = true;

    ul.innerHTML = state.accounts.map(a => `
    <li data-id="${esc(a.id)}" class="${String(a.id) === String(state.currentId) ? 'active' : ''}">
      <div class="acc-name">${esc(a.name || a.username)}</div>
      <div class="acc-user">${esc(a.username)}</div>
      <div class="acc-actions">
        <button type="button" title="编辑账号" data-act="edit" data-id="${esc(a.id)}">✎</button>
      </div>
    </li>`).join('');

    $$('#account-list li').forEach(li => {
        li.onclick = () => selectAccount(li.dataset.id);
    });
    $$('#account-list .acc-actions button').forEach(btn => {
        btn.onclick = (e) => {
            e.stopPropagation();
            if (btn.dataset.act === 'edit') openEditAccountDialog(btn.dataset.id);
        };
    });
}

function highlightAccount(id) {
    const target = String(id);
    $$('#account-list li').forEach(li => {
        li.classList.toggle('active', li.dataset.id === target);
    });
}

function showPanel(show) {
    $('#panel').hidden = !show;
    $('#empty-state').hidden = show;
    if (show) $('#server-page').hidden = true;
}

async function selectAccount(id) {
    // 切账号等于放弃这次扫码：弹窗共用，不能让后台扫码继续把界面拽回去
    if (wechatFlow.active) {
        stopWechatFlow();
        API.wechatCancel().catch(() => {});
    }
    state.currentId = String(id);
    state.current = state.accounts.find(a => String(a.id) === state.currentId) || null;
    renderAutoRelogin();
    renderAutoNotify();
    state.status = null;
    state.tabs = [];
    state.courses = [];
    state.coursesLoading = false;
    state.scores = [];
    state.loadingKch.clear();
    state.initializing = false;
    state.initForce = false;
    state.initToken++;
    state.loggingIn = false;
    state.loginToken++;
    appliedTaskId = 0;
    appliedRev = 0;
    dismissInputDialog();
    dismissQRDialog();
    // 换账号：清掉上一个账号的任务面板，稍后由推送/首次快照决定是否重新接管
    Task.clear();
    // 愿望清单按账号缓存：换账号必须重新拉，避免把上个账号的清单写进新账号
    wishLoadedFor = null;
    setWishHint('');
    setAutoHint('');
    renderInitButtons();
    showPanel(true);
    highlightAccount(id);

    if (state.current) {
        $('#acc-name').textContent = state.current.name || state.current.username;
        $('#acc-url').textContent  = state.current.baseUrl || '';
        $('#acc-user').textContent = state.current.username || '';
        $('#acc-mode').textContent = '';
        $('#acc-window').textContent = '';
        $('#acc-window').title = '';
    }

    renderTabs();
    renderCourses();
    renderChosen(null);
    renderScore(null);
    resetToolsUI();       // 个人信息/照片/Cookie/诊断都不能跨账号残留
    syncQueryButtons();   // 新账号没有在跑的查询 → 按钮立刻恢复，不继承上一个账号的转圈
    loadScoreSuggest();

    // 连接切到新账号：首帧就会带来完整快照（含"正在登录"的接管）
    Task.retry();
    await refreshStatus();

    // 切账号后仍停在「课程浏览」：按新账号重新加载（优先服务端缓存）
    const activeTab = $('.tabs button.active');
    if (activeTab && activeTab.dataset.tab === 'courses') {
        ensureCoursesLoaded().catch(() => {});
    }
    if (activeTab && activeTab.dataset.tab === 'auto') {
        loadWish(true).catch(() => {});
    }
    if (activeTab && activeTab.dataset.tab === 'tools') {
        ensureToolsLocal();
    }
}

/* =========================================================================
 * 服务端配置页（全局，不依赖当前账号）
 *
 * 「验证码模型」和「邮件通知」是同一张页面的两个子页：它们都是进程级配置，
 * 和账号无关，因此共用一套导航、状态徽标与加载入口。
 * ========================================================================= */

let kaptchaPreviewURL = null;

const serverState = {
    tab: 'kaptcha',
    kaptcha: null,
    server: null,
    kaptchaError: false,
    serverError: false,
};

function showServerPage() {
    $('#panel').hidden = true;
    $('#empty-state').hidden = true;
    $('#server-page').hidden = false;
    $$('#account-list li').forEach(li => li.classList.remove('active'));
    selectServerTab(serverState.tab);
    loadServerStatus(true).catch(() => {});
}

function hideServerPage() {
    $('#server-page').hidden = true;
}

async function backFromServer() {
    hideServerPage();
    if (state.currentId) {
        await selectAccount(state.currentId);
        return;
    }
    if (state.accounts.length) {
        await selectAccount(state.accounts[0].id);
        return;
    }
    showPanel(false);
}

// selectServerTab 切换「验证码模型 / 邮件通知」，只影响界面显示。
function selectServerTab(tab) {
    if (tab !== 'smtp' && tab !== 'kaptcha') tab = 'kaptcha';
    serverState.tab = tab;
    $$('#server-tabs button').forEach(btn => {
        btn.classList.toggle('active', btn.dataset.serverTab === tab);
    });
    $$('#server-page .server-panel').forEach(panel => {
        panel.hidden = panel.dataset.serverPanel !== tab;
    });
    updateServerBadge();
}

// setServerBadge 直接改顶部徽标（读取失败等一次性状态用）。
function setServerBadge(kind, text) {
    const b = $('#server-badge');
    if (!b) return;
    b.className = 'badge' + (kind ? ' ' + kind : '');
    b.textContent = text;
}

// updateServerBadge 按当前子页选择要显示的摘要。
function updateServerBadge() {
    if (serverState.tab === 'smtp') {
        if (serverState.serverError) return setServerBadge('err', '读取失败');
        const st = serverState.server;
        if (!st) return setServerBadge('wait', '读取中…');
        if (!st.smtp || !st.smtp.enabled) return setServerBadge('', '邮件通知已关闭');
        if (st.smtpReady) return setServerBadge('ok', 'SMTP 已启用');
        return setServerBadge('err', 'SMTP 配置不完整');
    }
    if (serverState.kaptchaError) return setServerBadge('err', '读取失败');
    const st = serverState.kaptcha;
    if (!st) return setServerBadge('wait', '读取中…');
    const s = st.settings || {};
    if (st.enabled) return setServerBadge('ok', '自动识别已启用');
    if (s.enabled && !st.loaded) return setServerBadge('err', '模型未加载');
    if (!s.enabled) return setServerBadge('', '自动识别已关闭');
    setServerBadge('wait', '未启用');
}

// loadServerStatus 并行拉两份配置：两个接口都很轻，一次取齐后切子页不再闪。
async function loadServerStatus(withSpinner) {
    if (withSpinner) {
        $('#kaptcha-status').innerHTML = '<p class="muted">正在读取配置…</p>';
        $('#smtp-status').innerHTML = '<p class="muted">正在读取配置…</p>';
    }
    await Promise.allSettled([
        loadKaptchaStatus(false),
        loadServerSettings(false),
    ]);
}

function formatBytes(n) {
    if (!n || n < 0) return '—';
    if (n >= 1024 * 1024) return (n / (1024 * 1024)).toFixed(1) + ' MB';
    if (n >= 1024) return (n / 1024).toFixed(1) + ' KB';
    return n + ' B';
}

function formatTime(ms) {
    if (!ms) return '—';
    try { return new Date(ms).toLocaleString(); } catch (e) { return '—'; }
}

function renderKaptchaStatus(st) {
    serverState.kaptcha = st;
    serverState.kaptchaError = false;
    updateServerBadge();

    const s = (st && st.settings) || {};
    const err = (st && st.error) || '';

    $('#kaptcha-enabled').checked = !!s.enabled;
    $('#kaptcha-model-path').value = s.modelPath || '';
    $('#kaptcha-threshold').value = s.threshold != null ? s.threshold : 0.9;
    $('#kaptcha-workers').value = s.workers != null ? s.workers : 0;

    const modelText = st.modelExists
        ? `${formatBytes(st.modelSize)} · 修改于 ${esc(formatTime(st.modelModified))}`
        : '文件不存在';
    $('#kaptcha-status').innerHTML = `
        <div class="kv-grid">
            <div class="kv"><span>模型</span><b>${esc(st.modelName || '未找到')}</b></div>
            <div class="kv"><span>路径</span><b class="mono">${esc(st.modelPath || '—')}</b></div>
            <div class="kv"><span>大小</span><b>${modelText}</b></div>
            <div class="kv"><span>规格</span><b>${st.numChars || 6} 字符 · ${st.imageWidth || 200}×${st.imageHeight || 50} 灰度</b></div>
            <div class="kv"><span>上次加载</span><b>${esc(formatTime(st.loadedAt))}</b></div>
            <div class="kv"><span>配置文件</span><b class="mono">${esc(st.settingsFile || '—')}</b></div>
        </div>`;

    const errBox = $('#kaptcha-error');
    if (err) {
        errBox.textContent = err;
        errBox.hidden = false;
    } else {
        errBox.hidden = true;
        errBox.textContent = '';
    }
}

async function loadKaptchaStatus(withSpinner) {
    if (withSpinner) {
        $('#kaptcha-status').innerHTML = '<p class="muted">正在读取配置…</p>';
    }
    try {
        renderKaptchaStatus(await API.kaptcha());
    } catch (e) {
        serverState.kaptchaError = true;
        updateServerBadge();
        $('#kaptcha-status').innerHTML =
            `<p class="notice">读取验证码配置失败：${esc(e.message)}</p>`;
        toast('读取验证码配置失败: ' + e.message, 'err');
    }
}

function collectKaptchaSettings() {
    return {
        enabled: $('#kaptcha-enabled').checked,
        modelPath: $('#kaptcha-model-path').value.trim(),
        threshold: Number($('#kaptcha-threshold').value) || 0.9,
        workers: Math.max(0, Math.floor(Number($('#kaptcha-workers').value) || 0)),
    };
}

async function saveKaptcha() {
    const btn = $('#btn-kaptcha-save');
    const hint = $('#kaptcha-save-hint');
    const orig = btn.textContent;
    btn.disabled = true;
    btn.innerHTML = '<span class="spinner"></span> 保存中...';
    hint.textContent = '';
    try {
        const st = await API.saveKaptcha(collectKaptchaSettings());
        renderKaptchaStatus(st);
        toast(st.enabled ? '验证码配置已保存，模型已加载 ✅'
                         : '验证码配置已保存（自动识别已关闭）', 'ok');
    } catch (e) {
        toast('保存失败: ' + e.message, 'err');
        hint.textContent = e.message;
        loadKaptchaStatus(false).catch(() => {});
    } finally {
        btn.disabled = false;
        btn.textContent = orig;
    }
}

async function reloadKaptcha() {
    const btn = $('#btn-kaptcha-reload');
    const orig = btn.textContent;
    btn.disabled = true;
    btn.innerHTML = '<span class="spinner dark"></span> 加载中...';
    try {
        const st = await API.reloadKaptcha();
        renderKaptchaStatus(st);
        toast('模型已重新加载 ✅', 'ok');
    } catch (e) {
        toast('重新加载失败: ' + e.message, 'err');
        loadKaptchaStatus(false).catch(() => {});
    } finally {
        btn.disabled = false;
        btn.textContent = orig;
    }
}

async function uploadKaptchaModel() {
    const input = $('#kaptcha-upload-file');
    const file = input.files && input.files[0];
    if (!file) return;
    const hint = $('#kaptcha-upload-hint');
    if (!/\.onnx$/i.test(file.name)) {
        toast('请选择 .onnx 模型文件', 'warn');
        input.value = '';
        return;
    }
    hint.textContent = `正在上传 ${file.name}（${formatBytes(file.size)}）…`;
    try {
        const st = await API.kaptchaModel(file);
        renderKaptchaStatus(st);
        toast('模型已上传并启用 ✅', 'ok');
        hint.textContent = '';
    } catch (e) {
        toast('上传失败: ' + e.message, 'err');
        hint.textContent = '';
        loadKaptchaStatus(false).catch(() => {});
    } finally {
        input.value = '';
    }
}

function clearKaptchaPreview() {
    if (kaptchaPreviewURL) {
        URL.revokeObjectURL(kaptchaPreviewURL);
        kaptchaPreviewURL = null;
    }
    $('#kaptcha-test-preview').innerHTML = '';
}

async function testKaptcha() {
    const input = $('#kaptcha-test-file');
    const file = input.files && input.files[0];
    if (!file) return;
    const result = $('#kaptcha-test-result');
    const hint = $('#kaptcha-test-hint');
    clearKaptchaPreview();
    kaptchaPreviewURL = URL.createObjectURL(file);
    $('#kaptcha-test-preview').innerHTML =
        `<img class="kaptcha-preview" src="${kaptchaPreviewURL}" alt="待测试验证码">`;
    hint.textContent = '识别中…';
    result.innerHTML = '<p class="muted">正在推理…</p>';
    try {
        const r = await API.kaptchaTest(file);
        const accepted = !!r.accepted;
        result.innerHTML = `
            <div class="kaptcha-test">
                <div class="kaptcha-test-text">${esc(r.text || '—')}</div>
                <div class="kaptcha-test-meta">
                    <span>置信度 <b>${((r.confidence || 0) * 100).toFixed(2)}%</b></span>
                    <span>阈值 ${((r.threshold || 0) * 100).toFixed(0)}%</span>
                    <span class="badge ${accepted ? 'ok' : 'err'}">${accepted ? '会直接提交' : '转人工输入'}</span>
                </div>
                ${r.reason ? `<p class="muted" style="margin:8px 0 0">${esc(r.reason)}</p>` : ''}
            </div>`;
        hint.textContent = '';
    } catch (e) {
        hint.textContent = '';
        result.innerHTML = `<p class="notice">识别失败：${esc(e.message)}</p>`;
        toast('测试识别失败: ' + e.message, 'err');
    } finally {
        input.value = '';
    }
}

/* =========================================================================
 * 服务端配置：SMTP 邮件通知
 * ========================================================================= */

function splitRecipients(value) {
    return String(value || '')
        .split(/[\r\n,;，；]+/)
        .map(s => s.trim())
        .filter(Boolean);
}

function collectSMTPSettings() {
    return {
        enabled: $('#smtp-enabled').checked,
        notifyAutoChoose: $('#smtp-notify-auto').checked,
        host: $('#smtp-host').value.trim(),
        port: Math.floor(Number($('#smtp-port').value) || 0),
        from: $('#smtp-from').value.trim(),
        username: $('#smtp-username').value.trim(),
        password: $('#smtp-password').value,
        to: splitRecipients($('#smtp-to').value),
    };
}

function renderServerSettings(view) {
    serverState.server = view;
    serverState.serverError = false;
    const s = (view && view.smtp) || {};

    $('#smtp-enabled').checked = !!s.enabled;
    $('#smtp-notify-auto').checked = !!s.notifyAutoChoose;
    $('#smtp-host').value = s.host || '';
    $('#smtp-port').value = s.port || 587;
    $('#smtp-from').value = s.from || '';
    $('#smtp-username').value = s.username || '';
    $('#smtp-to').value = (s.to || []).join('\n');
    $('#smtp-password').value = '';
    $('#smtp-password').placeholder = s.hasPassword ? '已保存，留空不修改' : '未设置';
    $('#smtp-password-hint').textContent = s.hasPassword
        ? '已保存授权码；留空表示不修改。'
        : '密码只保存在服务端，不会返回给浏览器。';

    const legacy = $('#smtp-legacy-hint');
    if (view && view.legacyImported) {
        legacy.innerHTML = '已从旧版 <code>' + esc(view.legacyFile || 'SMTP.json') +
            '</code> 导入配置；点「保存配置」后写入 <code>' +
            esc(view.settingsFile || 'server.json') + '</code>。';
        legacy.hidden = false;
    } else {
        legacy.hidden = true;
        legacy.textContent = '';
    }

    let stateBadge = '<span class="badge">未启用</span>';
    if (s.enabled) {
        stateBadge = view.smtpReady
            ? '<span class="badge ok">配置完整</span>'
            : '<span class="badge err">配置不完整</span>';
    }
    const recipients = (s.to || []).length ? esc((s.to || []).join('、')) : '—';
    $('#smtp-status').innerHTML =
        '<div class="kv-grid">' +
            '<div class="kv"><span>状态</span><b>' + stateBadge + '</b></div>' +
            '<div class="kv"><span>配置文件</span><b class="mono">' +
                esc(view.settingsFile || '—') + '</b></div>' +
            '<div class="kv"><span>收件人</span><b>' + recipients + '</b></div>' +
            '<div class="kv"><span>自动选课通知</span><b>' +
                (s.notifyAutoChoose ? '开启' : '关闭') + '</b></div>' +
            '<div class="kv"><span>授权码</span><b>' +
                (s.hasPassword ? '已保存' : '未设置') + '</b></div>' +
        '</div>';

    const errBox = $('#smtp-error');
    if (s.enabled && view.smtpError) {
        errBox.textContent = '要启用邮件通知，还需要：' + view.smtpError;
        errBox.hidden = false;
    } else {
        errBox.hidden = true;
        errBox.textContent = '';
    }
    updateServerBadge();
}

async function loadServerSettings(withSpinner) {
    if (withSpinner) {
        $('#smtp-status').innerHTML = '<p class="muted">正在读取配置…</p>';
    }
    try {
        renderServerSettings(await API.server());
    } catch (e) {
        serverState.serverError = true;
        updateServerBadge();
        $('#smtp-status').innerHTML =
            '<p class="notice">读取服务端配置失败：' + esc(e.message) + '</p>';
        if (withSpinner) toast('读取服务端配置失败: ' + e.message, 'err');
    }
}

async function saveSMTPSettings() {
    const btn = $('#btn-smtp-save');
    const hint = $('#smtp-save-hint');
    const orig = btn.textContent;
    btn.disabled = true;
    btn.innerHTML = '<span class="spinner"></span> 保存中...';
    hint.textContent = '';
    try {
        const view = await API.saveServer({ smtp: collectSMTPSettings() });
        renderServerSettings(view);
        toast(view.smtpReady ? 'SMTP 配置已保存，配置完整 ✅'
                             : 'SMTP 配置已保存（通知未启用或信息不完整）',
              view.smtpReady ? 'ok' : 'warn');
    } catch (e) {
        // 校验失败时保留用户刚填的内容，只把原因显示出来；不要用旧配置覆盖表单。
        hint.textContent = e.message;
        toast('保存 SMTP 配置失败: ' + e.message, 'err');
    } finally {
        btn.disabled = false;
        btn.textContent = orig;
    }
}

async function testSMTPSettings() {
    const btn = $('#btn-smtp-test');
    const hint = $('#smtp-save-hint');
    const orig = btn.textContent;
    btn.disabled = true;
    btn.innerHTML = '<span class="spinner dark"></span> 发送中...';
    hint.textContent = '正在连接 SMTP 服务器，可能需要几秒…';
    try {
        const r = await API.testSMTP({ smtp: collectSMTPSettings() });
        const to = (r && r.to) || [];
        hint.textContent = '';
        toast(to.length ? '测试邮件已发送到 ' + to.join('、') : '测试邮件已发送', 'ok');
    } catch (e) {
        hint.textContent = e.message;
        toast('测试邮件发送失败: ' + e.message, 'err');
    } finally {
        btn.disabled = false;
        btn.textContent = orig;
    }
}

function toggleSMTPPassword() {
    const input = $('#smtp-password');
    const btn = $('#smtp-eye');
    const show = input.type === 'password';
    input.type = show ? 'text' : 'password';
    btn.classList.toggle('visible', show);
    btn.title = show ? '隐藏密码' : '显示密码';
    btn.setAttribute('aria-label', btn.title);
}

/* =========================================================================
 * 高级选课（愿望清单 + 自动选课）
 * ========================================================================= */

// wishLines 把输入框内容拆成条目：每行一项，也接受逗号分隔。
function wishLines(value) {
    return String(value || '')
        .split(/[\r\n,]+/)
        .map(s => s.trim())
        .filter(Boolean);
}

// wishLoadedFor 记录愿望清单是按哪个账号填的：切账号必须重新拉，
// 否则会把上一个账号的清单显示在新账号下，用户一保存就写错账号。
let wishLoadedFor = null;

function setWishHint(text) {
    $('#wish-hint').textContent = text || '';
}

// loadWish 拉取并回填愿望清单；force=false 时同一账号只拉一次。
async function loadWish(force) {
    if (!state.currentId) return;
    if (!force && wishLoadedFor === state.currentId) return;
    try {
        const w = await API.getWish(state.currentId) || {};
        $('#wish-classes').value  = (w.wantClasses  || []).join('\n');
        $('#wish-teachers').value = (w.wantTeachers || []).join('\n');
        $('#wish-types').value    = (w.wantTypes    || []).join('\n');
        wishLoadedFor = state.currentId;
        setWishHint('');
    } catch (e) {
        toast('读取愿望清单失败: ' + e.message, 'err');
    }
}

async function saveWish() {
    if (!state.currentId) { toast('请先选择账号', 'err'); return; }
    $('#btn-save-wish').disabled = true;
    setWishHint('保存中…');
    try {
        await API.updateWish(state.currentId, {
            wantClasses:  wishLines($('#wish-classes').value),
            wantTeachers: wishLines($('#wish-teachers').value),
            wantTypes:    wishLines($('#wish-types').value),
        });
        wishLoadedFor = state.currentId;
        setWishHint('已保存');
        toast('愿望清单已保存');
    } catch (e) {
        setWishHint('');
        toast('保存失败: ' + e.message, 'err');
    } finally {
        $('#btn-save-wish').disabled = false;
    }
}

function setAutoHint(text) {
    $('#auto-hint').textContent = text || '';
}

// localDatetimeValue 把 Date 写成 datetime-local 需要的本地时间字符串。
function localDatetimeValue(d) {
    const pad = n => String(n).padStart(2, '0');
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T` +
           `${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

// syncAutoLimitMode 按"运行上限"的选择显示时长/结束时间输入框。
function syncAutoLimitMode() {
    const mode = $('#auto-limit-mode').value;
    $('#auto-limit-duration-wrap').hidden = mode !== 'duration';
    $('#auto-limit-endat-wrap').hidden = mode !== 'endAt';

    const endAt = $('#auto-limit-endat');
    const now = new Date();
    endAt.min = localDatetimeValue(now);
    if (mode === 'endAt' && !endAt.value) {
        // 默认给一个 6 小时后的时间，避免用户面对空白框
        endAt.value = localDatetimeValue(new Date(now.getTime() + 6 * 3600 * 1000));
    }
}

// autoLimitBody 把"运行上限"转成请求字段；参数不合法时直接抛错，不发请求。
function autoLimitBody(now) {
    const mode = $('#auto-limit-mode').value;
    if (mode === 'duration') {
        const hours = Number($('#auto-limit-hours').value);
        if (!isFinite(hours) || hours <= 0) throw new Error('运行时长必须大于 0 小时');
        return { durationSeconds: Math.round(hours * 3600) };
    }
    if (mode === 'endAt') {
        const raw = $('#auto-limit-endat').value;
        if (!raw) throw new Error('请选择结束时间');
        const ms = new Date(raw).getTime();
        if (!isFinite(ms)) throw new Error('结束时间格式不正确');
        if (ms <= now.getTime()) throw new Error('结束时间必须晚于当前时间');
        return { endAt: ms };
    }
    // default：服务端用 24 小时默认值
    return {};
}

async function startAutoChoose() {
    if (!state.currentId) { toast('请先选择账号', 'err'); return; }

    let limit;
    try {
        limit = autoLimitBody(new Date());
    } catch (e) {
        toast(e.message, 'err');
        return;
    }

    // 不传愿望：服务端用账号里保存的清单。这样"编辑并保存"是唯一条件来源，
    // 不会出现页面上改了一半就启动、实际用的是旧清单的情况。
    const body = {
        intervalSeconds: Number($('#auto-interval').value) || 0,
        refreshSeconds:  Number($('#auto-refresh').value) || 0,
        maxRounds:       Number($('#auto-rounds').value) || 0,
        once:            $('#auto-once').checked,
        ...limit,
    };
    $('#btn-auto-start').disabled = true;
    setAutoHint('正在自动选课…');
    try {
        const res = await API.autoChoose(state.currentId, body);
        setAutoHint(autoResultText(res));
    } catch (e) {
        setAutoHint('');
        toast('自动选课失败: ' + e.message, 'err');
    } finally {
        $('#btn-auto-start').disabled = false;
    }
}

function autoResultText(res) {
    if (!res) return '';
    if (res.status === 'ok') {
        const c = res.course || {};
        return '已选上：' + (c.kcmc || c.jxbmc || '') + '（' + (c.jxbmc || '') + '）';
    }
    if (res.status === 'not_chosen') return '轮次用尽，未选上';
    if (res.status === 'cancelled') return '已取消';
    return res.msg || res.status || '';
}

// cancelAutoResultText 把取消接口的结果映射成提示。
// ok 才代表"确实停掉了一个在跑的任务"；noop 是"本来就没有在跑的任务"，
// 同样要落一句明确的话，不能把它当成"取消已发出"。
function cancelAutoResultText(res) {
    const status = res && res.status;
    if (status === 'ok') return '已取消';
    if (status === 'noop') return '没有正在进行的自动选课';
    return (res && (res.msg || status)) || '';
}

// cancelAutoChoose 取消自动选课。
//
// 取消接口是幂等的：没有在跑的任务时也返回 200 + noop。所以"正在取消…"只能
// 是请求期间的临时提示，拿到响应后必须落成确定的结果——noop 也一样。否则这句
// 提示没有任何收尾路径：收尾原本只挂在 /auto-choose 那个长请求的响应上，而页面
// 刷新过、任务早已结束、或在别的标签页里取消过时，本页根本没有那个响应在等。
async function cancelAutoChoose() {
    if (!state.currentId) return;
    const id = state.currentId;
    const btn = $('#btn-auto-cancel');
    if (btn.disabled) return;          // 上一次取消还在路上，别叠第二次
    const prevHint = $('#auto-hint').textContent;
    btn.disabled = true;
    setAutoHint('正在取消…');
    try {
        const res = await API.cancelAuto(id);
        if (state.currentId !== id) return;   // 期间切了账号：这句提示不属于新账号
        setAutoHint(cancelAutoResultText(res));
        // 取消改的是服务端任务状态：作废取消前发出的旧快照，再补一次状态刷新，
        // 让任务面板立刻收尾（推送断开时也不会一直显示"进行中"）。
        invalidateStatus();
        await refreshStatus({ quiet: true }).catch(() => {});
    } catch (e) {
        if (state.currentId !== id) return;
        setAutoHint(prevHint);         // 取消没成功：把原来的提示还回去
        toast('取消失败: ' + e.message, 'err');
    } finally {
        btn.disabled = false;
    }
}

/* =========================================================================
 * 状态 / 登录
 * ========================================================================= */

// refreshStatus 拉取账号状态 + 任务快照（首屏、显式操作、兜底轮询用）。
// quiet=true 时用于后台轮询：失败不打扰用户。
//
// 单飞：多个调用点（任务收尾 / 兜底轮询）不会叠着重复拉取。
// 但状态被本地改动过之后必须先 invalidateStatus()：
// 单飞会把"改动之前发出的那次请求"直接还给你，而那份响应里的状态是旧的，
// 落回界面就会把刚更新的按钮/徽标改回去（登出后按钮不更新的根因）。
async function refreshStatus(opts) {
    const quiet = !!(opts && opts.quiet);
    if (!state.currentId) return;
    // 单飞只复用"本代"的请求；被 invalidate 过的旧请求一律不认
    if (statusInflight && statusInflightGen === statusGen) return statusInflight;

    const id = state.currentId;
    const gen = statusGen;
    const run = (async () => {
        try {
            const st = await API.status(id);
            if (state.currentId !== id) return;        // 期间切了账号，结果作废
            if (gen !== statusGen) return;             // 期间状态被改过，这份是旧快照
            await applyStatusPayload(st);
        } catch (e) {
            if (!quiet) toast('获取状态失败: ' + e.message, 'err');
        } finally {
            if (statusInflight === run) statusInflight = null;
        }
    })();
    statusInflight = run;
    statusInflightGen = gen;
    return run;
}

// invalidateStatus 声明"账号状态刚刚被本地改动"（登录完成、登出成功…）：
// 之前发出的状态请求全部作废，下一次 refreshStatus 必须重新发一次。
function invalidateStatus() {
    statusGen++;
}

// notifyUnauthorized 是所有"会话失效"的统一处理：
// 后端用 code=unauthorized 表示"这次请求证明登录态已经失效"（初始化、课程、
// 成绩、工具页…都走同一个错误出口）。这里一次性把徽标切到未登录、作废旧状态
// 请求、提示一次，再拉权威快照——否则每个 catch 各写一遍，漏一个入口就会
// 出现"弹了错误但登录状态没刷新"。
let unauthorizedToastAt = 0;

// isUnauthorized 给调用方判断"这条错误已经由 notifyUnauthorized 统一提示过"，
// 避免同一次失效又弹一条"初始化失败/查询失败"的红字。
function isUnauthorized(err) {
    return !!(err && err.code === 'unauthorized');
}

function notifyUnauthorized(path, err) {
    // 请求可能是切账号前发出的：旧账号的失效不能算到新账号头上。
    const m = /^\/accounts\/([^/]+)(?:\/|$)/.exec(path || '');
    if (m && state.currentId && String(m[1]) !== String(state.currentId)) return;

    const wasLoggedIn = !!(state.status && state.status.loggedIn);
    if (state.status) state.status.loggedIn = false;
    invalidateStatus();
    renderStatus();

    // 同一轮失效会有多个并发请求返回 401，只提示第一条。
    const now = Date.now();
    if (wasLoggedIn && now - unauthorizedToastAt > 3000) {
        unauthorizedToastAt = now;
        toast((err && err.message) || '登录已失效，请重新登录', 'warn');
    }
    // 后端已经按"服务器说未登录"更新登录态；拉一次快照让页签/任务面板
    // 也落到一致状态（如果期间刚好完成重登，这里会把徽标改回已登录）。
    refreshStatus({ quiet: true }).catch(() => {});
}

// applyTaskFrame 应用一帧推送快照。
function applyTaskFrame(frame) {
    const t = frame.task || null;
    if (staleFrame(t)) return;
    markApplied(t);
    state.status = Object.assign({}, state.status || {}, {
        loggedIn: !!(frame.loggedIn !== undefined
            ? frame.loggedIn
            : (state.status && state.status.loggedIn)),
    });
    // 快照帧带 pending：远端发起的登录（ping 自动重登 / 初始化）也能弹输入框。
    // progress 帧只走秒、不带 pending，别用它去清状态。
    if (frame.type !== 'progress' && frame.pending !== undefined) {
        state.status.pending = frame.pending || null;
    }
    // 二维码同理：snapshot 按 qr 元数据开关弹窗，progress 帧不动它。
    if (frame.type !== 'progress' && frame.qr !== undefined) {
        state.status.qr = frame.qr || null;
    }
    Task.apply(t, { fromStream: true });
    maybePromptPending(state.status.pending);
    maybeShowQR(state.status.qr);
    renderStatus();
    Task.maybeRefreshDetails();
}

// applyStatusPayload 把 /status 或推送的结果落到界面上（两条路径共用）。
async function applyStatusPayload(st) {
    state.status = st;
    const t = st.task || null;
    markApplied(t);
    Task.apply(t, { fromStream: true });
    maybePromptPending(st.pending);
    maybeShowQR(st.qr);
    renderStatus();

    const mode = st.modeName || '';
    if (mode !== state.modeName) {
        state.modeName = mode;
        if (st.initialized) {
            await loadTabs();
        } else {
            state.tabs = [];
            renderTabs();
        }
    }
    // 任务刚结束的补刷放到下一轮，不能在这一轮里 await（会自己等自己）
    Task.maybeRefreshDetails();
}

// maybePromptPending 若后端仍在等待验证码/新密码，弹出输入框
function maybePromptPending(pending) {
    if (pending && !inputDialogResolve) {
        promptLoginInput(pending).then(handleLoginInput);
    }
}

// renderAutoRelogin 让头部开关跟当前账号同步。
// 老账号 / 老数据没有 autoRelogin 字段时按开启处理（与后端一致）。
function renderAutoRelogin() {
    const el = $('#chk-auto-relogin');
    if (!el) return;
    el.checked = !state.current || state.current.autoRelogin !== false;
    el.disabled = !state.currentId;
}

// renderAutoNotify 让「完成后发送邮件通知」跟当前账号同步。
// 老账号 / 老数据没有 autoNotify 字段时按开启处理（与后端一致）；
// 服务端 SMTP 总开关不在这个勾选框里。
function renderAutoNotify() {
    const el = $('#chk-auto-notify');
    if (!el) return;
    el.checked = !state.current || state.current.autoNotify !== false;
    el.disabled = !state.currentId;
}

function renderStatus() {
    const st = state.status || {};
    const badge = $('#login-state');
    const btnLogin  = $('#btn-login');
    const btnLogout = $('#btn-logout');

    // 登录按钮在任务进行中也要可点——点了就是"取消登录"
    btnLogin.disabled = false;

    const busy = Task.running();
    if (busy) {
        badge.className = 'badge wait';
        badge.textContent = Task.running('init') ? '初始化中…' : '登录中…';
        btnLogin.hidden  = false;
        btnLogout.hidden = true;
    } else if (st.loggedIn) {
        badge.className = 'badge ok';
        badge.textContent = '已登录';
        btnLogin.hidden  = true;
        btnLogout.hidden = false;
    } else {
        badge.className = 'badge err';
        badge.textContent = '未登录';
        btnLogin.hidden  = false;
        btnLogout.hidden = true;
    }

    // 文案必须跟着"任务是否真在跑"走，不能只看点击时置的 state.loggingIn：
    // 切到别的账号再切回来，本地状态被重置、按钮已回到「登录」，
    // 此时只能靠快照知道后端仍在登录——否则会显示成「登录」但点下去是取消。
    // state.loggingIn 保留用于"刚点下登录、首帧快照还没到"的空窗期。
    if (busy || state.loggingIn) setLoginBtnBusy(btnLogin);
    else setLoginBtnIdle(btnLogin);

    $('#acc-mode').textContent = st.modeName ? ('页签：' + st.modeName) : '';

    // 选课时间窗由 /status 的 window 带回来（初始化完成或切页签后更新）。
    // 头部只显示结束时间；开始时间放进 title，窄屏下不必把 meta 撑成两行。
    const win = st.window || {};
    const accWindow = $('#acc-window');
    accWindow.textContent = win.xkjssj ? ('选课结束：' + win.xkjssj) : '';
    accWindow.title = win.xkkssj ? ('选课开始：' + win.xkkssj) : '';

    // 二维码弹窗里的阶段文案/倒计时跟着任务快照/进度帧刷新（运行中约每秒一帧）
    syncQRStatus();
}

/* =========================================================================
 * 页签
 * ========================================================================= */

async function loadTabs() {
    if (!state.currentId) return;
    try {
        state.tabs = (await API.tabs(state.currentId)) || [];
    } catch {
        state.tabs = [];
    }
    renderTabs();
}

function renderTabs() {
    const c = $('#tabs-container');
    if (!state.tabs || state.tabs.length === 0) {
        c.innerHTML = '<p class="muted">暂无页签，请先初始化。</p>';
        return;
    }
    const activeName = state.status && state.status.modeName;
    c.innerHTML = '<div class="tabbar">' + state.tabs.map((t, i) => {
        const active = activeName && t.kklxmc === activeName;
        return `<button class="tab-chip${active ? ' active' : ''}" data-tab-index="${i}">
            ${esc(t.kklxmc || t.kklxdm || ('页签 ' + i))}
        </button>`;
    }).join('') + '</div>';

    $$('.tab-chip', c).forEach(btn => {
        btn.onclick = async () => {
            const idx = +btn.dataset.tabIndex;
            const all = $$('.tab-chip', c);
            all.forEach(b => b.disabled = true);
            try {
                await API.selectTab(state.currentId, idx);
                toast('页签已切换', 'ok');
                state.courses = [];
                state.loadingKch.clear();
                renderCourses();
                await refreshStatus();
            } catch (e) {
                toast('切换失败: ' + e.message, 'err');
            } finally {
                all.forEach(b => b.disabled = false);
            }
        };
    });
}

/* =========================================================================
 * 课程列表
 * ========================================================================= */

function isBoardCourse(courses) {
    if (!courses || courses.length < 2) return false;
    const ids = courses.map(co => String(pick(co, 'kch_id', 'Kch_id', 'kchId') || ''));
    if (ids.some(id => !id)) return false;
    return ids.every(id => id === ids[0]);
}

/* ---------- 单元格工具 ---------- */

function capCell(yxrs, rl) {
    const hasY = yxrs !== undefined && yxrs !== null && yxrs !== '';
    const hasR = rl   !== undefined && rl   !== null && rl   !== '';

    if (!hasY && !hasR) return '<span class="muted">—</span>';
    if (!hasR)          return `<span class="cap-num">${esc(yxrs)}</span>`;
    if (!hasY)          return '<span class="muted">—</span>' +
        '<span class="cap-sep">/</span>' +
        `<span class="cap-total">${esc(rl)}</span>`;

    const full = Number(yxrs) >= Number(rl);
    return `<span class="cap-num${full ? ' full' : ''}">${esc(yxrs)}</span>` +
        `<span class="cap-sep">/</span>` +
        `<span class="cap-total">${esc(rl)}</span>`;
}

/* ---------- 板块课分组头 ---------- */

function renderCourseGroupHead(courses) {
    const first = courses[0] || {};
    const kcmc  = pick(first, 'kcmc', 'Kcmc') || '';
    const kchId = pick(first, 'kch_id', 'Kch_id') || '';
    const kzmc  = pick(first, 'kzmc', 'Kzmc') || '';
    const xf    = pick(first, 'xf', 'XF');

    const meta = [];
    if (kzmc) meta.push(esc(kzmc));
    if (xf)   meta.push(esc(xf) + ' 学分');
    meta.push(courses.length + ' 个教学班');

    const loading = !!kchId && state.loadingKch.has(String(kchId));
    const refreshBtn = kchId
        ? `<button type="button" class="btn btn-mini" data-refresh-detail="1"
                   data-kch="${esc(kchId)}" ${loading ? 'disabled' : ''}>${
            loading ? '<span class="spinner dark"></span>' : '🔄 刷新详情'
        }</button>`
        : '';

    return `<div class="group-head">
        <div class="group-title">
            <b>${esc(kcmc)}</b>
            ${kchId ? `<span class="tag">${esc(kchId)}</span>` : ''}
        </div>
        <div class="group-meta">
            ${meta.map(m => `<span>${m}</span>`).join('')}
            ${refreshBtn}
        </div>
    </div>`;
}

/* ---------- 单行渲染（对齐 CLI FullPrint 的信息密度） ---------- */

function renderCourseRow(co, i, board) {
    const kchId  = pick(co, 'kch_id', 'Kch_id', 'kchId') || '';
    const kcmc   = pick(co, 'kcmc', 'Kcmc') || '';
    const jxbmc  = pick(co, 'jxbmc', 'Jxbmc') || '';
    const jxbId  = pick(co, 'jxb_id', 'JxbId', 'jxbId') || '';
    const doJxb  = pick(co, 'do_jxb_id', 'Do_jxb_id') || '';
    const loaded = !!doJxb;

    // 教师 / 时间 / 地点（详情字段）
    const teach  = loaded ? (pick(co, 'Jsxx', 'jsxx') || '') : '';
    const sksj   = loaded ? brLines(pick(co, 'sksj', 'Sksj')) : '';
    const jxdd   = loaded ? brLines(pick(co, 'jxdd', 'Jxdd')) : '';
    const xqumc  = loaded ? (pick(co, 'xqumc', 'Xqumc') || '') : '';

    // 容量
    const yxrs = pick(co, 'yxzrs', 'Yxzrs');
    const rl   = loaded ? pick(co, 'jxbrl', 'Jxbrl') : undefined;

    // 学分 / 性质
    const xf   = pick(co, 'xf', 'XF', 'Xf');
    const kzmc = pick(co, 'kzmc', 'Kzmc') || '';
    const kcxz = pick(co, 'kcxzmc', 'Kcxzmc') || '';

    // 名称
    let nameCell;
    if (board) {
        nameCell = `<span class="c-title">${esc(jxbmc || kcmc)}</span>`;
    } else {
        const main = kcmc || jxbmc;
        const sub  = (jxbmc && jxbmc !== main) ? jxbmc : '';
        nameCell =
            `<div class="c-line">` +
            `<span class="c-title">${esc(main)}</span>` +
            (sub ? `<span class="c-tag">${esc(sub)}</span>` : '') +
            `</div>` +
            (kchId ? `<div class="c-code">${esc(kchId)}</div>` : '');
    }

    // 教师单元格
    const teachCell = teach ? esc(teach) : '<span class="muted">—</span>';

    // 时间 / 地点单元格
    let timeCell;
    if (loaded) {
        const parts = [];
        if (xqumc) parts.push(esc(xqumc));
        if (sksj)  parts.push(esc(sksj));
        if (jxdd)  parts.push(esc(jxdd));
        timeCell = parts.length
            ? `<div class="time-line">${parts.join(' · ')}</div>`
            : '<span class="muted">—</span>';
    } else {
        timeCell = '<span class="muted">—</span>';
    }

    // 学分 / 性质
    const creditParts = [];
    if (xf) creditParts.push(`<b>${esc(xf)}</b> 分`);
    if (kzmc) creditParts.push(`<span class="muted">${esc(kzmc)}</span>`);
    else if (kcxz) creditParts.push(`<span class="muted">${esc(kcxz)}</span>`);
    const creditCell = creditParts.length
        ? creditParts.join(' ')
        : '<span class="muted">—</span>';

    // 操作列
    let actionCell;
    if (loaded) {
        actionCell = `<button type="button" class="btn btn-mini primary" ` +
            `data-kch="${esc(kchId)}" data-jxb="${esc(doJxb)}">选课</button>`;
    } else {
        actionCell = `<button type="button" class="btn btn-mini" ` +
            `data-load="1" data-kch="${esc(kchId)}">详情</button>`;
    }

    return `<tr class="course-row${loaded ? '' : ' unloaded'}" data-idx="${i}" data-jxb="${esc(jxbId)}">
        <td class="col-idx">${i + 1}</td>
        <td>${nameCell}</td>
        <td class="col-teacher">${teachCell}</td>
        <td class="col-time">${timeCell}</td>
        <td class="col-cap">${capCell(yxrs, rl)}</td>
        <td class="col-credit">${creditCell}</td>
        <td class="col-act">${actionCell}</td>
    </tr>`;
}

/* ---------- 渲染主入口 ---------- */

function renderCourses() {
    const c = $('#courses-container');
    const count = $('#course-count');

    if (!state.courses.length) {
        count.textContent = '';
        c.innerHTML = state.coursesLoading
            ? '<p class="muted"><span class="spinner dark"></span> 正在从学校拉取课程列表…</p>'
            : '<p class="muted">暂无课程。打开本页会自动加载服务端缓存；缓存为空时点「获取课程列表」从学校拉取。</p>';
        return;
    }

    count.textContent = `共 ${state.courses.length} 个教学班`;
    const board = isBoardCourse(state.courses);

    let html = '';
    if (board) html += renderCourseGroupHead(state.courses);

    html += '<table class="course-table"><thead><tr>' +
        '<th class="col-idx">#</th>' +
        (board ? '<th>教学班</th>' : '<th>课程 / 教学班</th>') +
        '<th class="col-teacher">教师</th>' +
        '<th class="col-time">时间 / 地点</th>' +
        '<th class="col-cap">已选 / 容量</th>' +
        '<th class="col-credit">学分 / 性质</th>' +
        '<th class="col-act"></th>' +
        '</tr></thead><tbody>';

    state.courses.forEach((co, i) => {
        html += renderCourseRow(co, i, board);
    });

    html += '</tbody></table>';
    c.innerHTML = html;

    bindCourseEvents(c);
}

function bindCourseEvents(c) {
    // 行点击：未加载详情的行 → 加载
    $$('tr.course-row', c).forEach(tr => {
        tr.onclick = (e) => {
            if (e.target.closest('button')) return;
            const idx = +tr.dataset.idx;
            const co = state.courses[idx];
            if (!co) return;
            if (!pick(co, 'do_jxb_id', 'Do_jxb_id')) {
                const kchId = pick(co, 'kch_id', 'Kch_id', 'kchId');
                if (kchId) loadCourseDetail(kchId);
            }
        };
    });

    // 「详情」按钮
    $$('tr.course-row button[data-load]', c).forEach(btn => {
        btn.onclick = (e) => {
            e.stopPropagation();
            if (btn.dataset.kch) loadCourseDetail(btn.dataset.kch);
        };
    });

    // 「选课」按钮
    $$('tr.course-row button[data-jxb]', c).forEach(btn => {
        btn.onclick = (e) => {
            e.stopPropagation();
            doChoose(btn.dataset.kch, btn.dataset.jxb, btn);
        };
    });

    // 分组头「刷新详情」
    $$('.group-head button[data-refresh-detail]', c).forEach(btn => {
        btn.onclick = (e) => {
            e.stopPropagation();
            if (btn.dataset.kch) loadCourseDetail(btn.dataset.kch);
        };
    });
}

/* ---------- 打开「课程浏览」时的加载策略 ---------- */

// ensureCoursesLoaded 进入课程浏览页时调用：
//   1. 页面里已有列表 → 直接渲染，不发请求；
//   2. 否则先取服务端内存缓存（/courses/cache，不访问学校接口）；
//   3. 缓存也为空且已初始化，才自动向学校拉取一次。
async function ensureCoursesLoaded() {
    if (!state.currentId) return;
    if (state.courses.length) {
        renderCourses();
        return;
    }

    const id = state.currentId;
    let cached = [];
    try {
        cached = (await API.cachedCourses(id)) || [];
    } catch {
        cached = [];
    }
    if (state.currentId !== id) return;   // 期间切了账号：这份缓存属于旧账号

    if (Array.isArray(cached) && cached.length) {
        state.courses = cached;
        renderCourses();
        return;
    }

    if (!state.status || !state.status.initialized) {
        renderCourses();   // 未初始化：保持空提示，别发注定失败的查询
        return;
    }
    await fetchCourseList();
}

// fetchCourseList 从学校拉取当前页签的课程列表（结果同时写入服务端缓存）。
async function fetchCourseList() {
    if (!state.currentId) return;
    if (!state.status || !state.status.initialized) {
        toast('请先在「初始化」标签页完成初始化', 'warn');
        switchTab('init');
        return;
    }
    if (state.coursesLoading) return;

    const id = state.currentId;
    const btn = $('#btn-fetch-courses');
    const orig = btn.textContent;
    state.coursesLoading = true;
    btn.disabled = true;
    btn.innerHTML = '<span class="spinner dark"></span> 加载中...';
    renderCourses();   // 列表区也给一个加载反馈
    try {
        const list = (await API.courses(id)) || [];
        if (state.currentId !== id) return;
        state.courses = list;
    } catch (e) {
        if (state.currentId === id && !isUnauthorized(e)) toast('获取课程失败: ' + e.message, 'err');
    } finally {
        state.coursesLoading = false;
        btn.disabled = false;
        btn.textContent = orig;
        if (state.currentId === id) renderCourses();
    }
}

/* ---------- 加载某课程号下的所有教学班详情 ---------- */

async function loadCourseDetail(kchId) {
    if (!kchId || !state.currentId) return;
    kchId = String(kchId);
    if (state.loadingKch.has(kchId)) return;

    state.loadingKch.add(kchId);
    renderCourses();   // ← 立即把「刷新详情」置灰 + spinner

    try {
        const list = await API.detail(state.currentId, kchId);
        if (Array.isArray(list)) {
            state.courses = list;
        }
    } catch (e) {
        if (!isUnauthorized(e)) toast('加载详情失败: ' + e.message, 'err');
    } finally {
        state.loadingKch.delete(kchId);
        renderCourses();
    }
}

/* =========================================================================
 * 选课按钮：选课 / 退课 双态
 * ========================================================================= */

function setChooseBtnState(btn, chosen) {
    btn.disabled = false;
    btn.classList.remove('primary', 'danger');
    if (chosen) {
        btn.classList.add('danger');
        btn.textContent = '退课';
        btn.onclick = (e) => {
            e.stopPropagation();
            doQuit(btn.dataset.kch, btn.dataset.jxb, btn);
        };
    } else {
        btn.classList.add('primary');
        btn.textContent = '选课';
        btn.onclick = (e) => {
            e.stopPropagation();
            doChoose(btn.dataset.kch, btn.dataset.jxb, btn);
        };
    }
}

async function doQuit(kchId, jxbId, btn) {
    if (!confirm(`确认退选该教学班？\n课程号: ${kchId}\n教学班: ${jxbId}`)) return;
    btn.disabled = true;
    btn.innerHTML = '<span class="spinner"></span>';
    try {
        const r = await API.quit(state.currentId, { kchId, jxbId });
        if (r && r.ok) {
            toast('退课成功 ✅', 'ok');
            setChooseBtnState(btn, false);
        } else {
            toast('退课失败: ' + ((r && r.msg) || ''), 'err');
            setChooseBtnState(btn, true);
        }
    } catch (e) {
        if (!isUnauthorized(e)) toast('退课出错: ' + e.message, 'err');
        setChooseBtnState(btn, true);
    }
}

async function doChoose(kchId, jxbId, btn) {
    // 选课不弹确认框：点一下就直接提交，按钮上的 spinner 就是反馈。
    // 退课仍保留 confirm（见 doQuit / 已选课程里的退课按钮）。
    btn.disabled = true;
    btn.innerHTML = '<span class="spinner"></span>';
    try {
        const r = await API.choose(state.currentId, { kchId, jxbId });
        const flag = pick(r, 'Flag', 'flag');
        const msg  = pick(r, 'Msg', 'msg') || '';
        if (flag === '1') {
            toast('选课成功 ✅ ' + msg, 'ok');
            setChooseBtnState(btn, true);
            return;
        }
        toast('选课结果: ' + (msg || ('flag=' + flag)), flag === '0' ? 'warn' : 'err');
        setChooseBtnState(btn, false);
    } catch (e) {
        if (!isUnauthorized(e)) toast('选课出错: ' + e.message, 'err');
        setChooseBtnState(btn, false);
    }
}

/* =========================================================================
 * 查询类按钮的账号级忙态
 *
 * 「已选课程 / 考试成绩」的查询在后端可能触发一次耗时登录。忙态必须记在
 * 发起查询的账号上：
 *   - 切到别的账号，按钮立刻恢复，不能继承上一个账号的 spinner；
 *   - 请求回来时账号已经切走，结果直接丢弃，不能渲染进新账号的面板。
 * ========================================================================= */

function queryBusyKey(id, kind) { return id + ':' + kind; }

function isQueryBusy(kind, id = state.currentId) {
    return !!(id && state.queryBusy[queryBusyKey(id, kind)]);
}

// syncQueryButton 按当前账号的忙态刷新按钮外观（label 只在第一次调用时记下）。
function syncQueryButton(btn, kind, label) {
    if (!btn) return;
    if (!btn.dataset.label) btn.dataset.label = label;
    if (isQueryBusy(kind)) {
        btn.disabled = true;
        btn.innerHTML = '<span class="spinner dark"></span>';
        return;
    }
    btn.disabled = false;
    btn.textContent = btn.dataset.label;
}

function syncQueryButtons() {
    syncQueryButton($('#btn-fetch-chosen'), 'chosen', '查询');
    syncQueryButton($('#btn-fetch-score'), 'score', '查询');
    syncQueryButton($('#btn-ping'), 'ping', 'Ping');
}

// 未登录时不要发查询：后端的"按需登录"不在任务面板里，会静默卡很久。
function ensureLoggedInForQuery() {
    if (state.status && state.status.loggedIn === false) {
        toast('尚未登录，请先点击「登录」', 'warn');
        return false;
    }
    return true;
}

// pingAccount 探测当前账号的会话。服务端发现会话失效会自动起一个可见的
// 登录任务（任务面板里能看到进度、可取消），所以这里不阻塞等登录结束。
async function pingAccount() {
    const id = state.currentId;
    if (!id) return;
    if (isQueryBusy('ping', id)) return;

    state.queryBusy[queryBusyKey(id, 'ping')] = true;
    syncQueryButton($('#btn-ping'), 'ping', 'Ping');
    // 只要 Ping 判定"未登录"（无论是否自动重登），都要重拉一次权威状态：
    // Ping 已经改了 client 的登录态，界面不能停在旧徽标上。
    let needRefresh = false;
    try {
        const r = await API.ping(id);
        // 期间切了账号：这份探测结果和它的 toast 都只属于旧账号
        if (state.currentId !== id) return;

        if (r && r.ok && r.loggedIn) {
            toast(`Ping 成功 · ${r.latencyMs} ms · 会话有效`, 'ok');
            return;
        }
        if (r && r.relogin) {
            needRefresh = true;
            toast(`Ping · ${r.latencyMs} ms · ${r.message || '会话已失效'}，正在自动重新登录`, 'warn');
            return;
        }
        if (r && r.ok) {
            needRefresh = true;
            // hint 是后端给"不能自动重登"的账号（微信扫码）留的说明：
            // 这种账号只能在界面上重新扫码，点一下侧栏的微信入口。
            const hint = (r.hint && `（${r.hint}）`) || '';
            toast(`Ping 通 · ${r.latencyMs} ms · ${r.message || '未登录'}${hint}`, 'warn');
        } else {
            toast(`Ping 失败: ${(r && r.message) || '未知错误'}`, 'err');
        }
    } catch (e) {
        if (state.currentId === id) toast('Ping 出错: ' + e.message, 'err');
    } finally {
        // 忙态只跟 ping 请求本身绑定：先还原按钮，再去做状态刷新。
        // 刷新失败/变慢都不能拖着按钮一直转圈。
        delete state.queryBusy[queryBusyKey(id, 'ping')];
        syncQueryButton($('#btn-ping'), 'ping', 'Ping');
        if (needRefresh && state.currentId === id) {
            invalidateStatus();
            refreshStatus({ quiet: true }).catch(() => {});
        }
    }
}

/* =========================================================================
 * 已选课程
 * ========================================================================= */

function canQuitCourse(it) {
    const sfktk  = pick(it, 'Sfktk', 'sfktk');
    const inxksj = pick(it, 'IsInxksj', 'isInxksj');
    if (sfktk === undefined && inxksj === undefined) return true;
    return String(sfktk) === '1' || String(inxksj) === '1';
}

function renderChosen(list) {
    const c = $('#chosen-container');
    if (!list || !list.length) {
        c.innerHTML = '<p class="muted">没有已选课程。</p>';
        return;
    }
    // 教师列经常很长（"320075/吴静/讲师;…" 一串），自动布局会把时间列挤到逐字换行。
    // 这里改成固定列宽：时间列拿剩余宽度，教师列自己换行。
    let html = '<table class="chosen-table">' +
        '<colgroup>' +
        '<col class="col-idx"><col class="col-name">' +
        '<col class="col-teacher"><col class="col-when"><col class="col-act">' +
        '</colgroup>' +
        '<thead><tr>' +
        '<th>#</th><th>课程</th><th>教师</th><th>时间</th><th></th>' +
        '</tr></thead><tbody>';

    list.forEach((it, i) => {
        const kch   = pick(it, 'Kch_id', 'kch_id', 'kchId') || '';
        const name  = pick(it, 'Kcmc', 'kcmc', 'Jxbmc', 'jxbmc') || '';
        const teach = pick(it, 'Jsxx', 'jsxx') || '';
        const jxbId = pick(it, 'Jxb_id', 'jxb_id', 'Do_jxb_id', 'do_jxb_id') || '';
        const sksj  = brLines(pick(it, 'Sksj', 'sksj'));

        let action;
        if (!jxbId) {
            action = '<span class="muted">-</span>';
        } else if (canQuitCourse(it)) {
            action = `<button type="button" class="btn danger" ` +
                `data-kch="${esc(kch)}" data-jxb="${esc(jxbId)}">退课</button>`;
        } else {
            action = '<span class="muted" title="当前不在可退课时间 / 不允许退课">不可退</span>';
        }

        html += `<tr>
            <td>${i + 1}</td>
            <td>${esc(name)}</td>
            <td class="chosen-teacher">${esc(teach)}</td>
            <td class="multiline">${esc(sksj)}</td>
            <td>${action}</td>
        </tr>`;
    });
    html += '</tbody></table>';
    c.innerHTML = html;

    c.querySelectorAll('button[data-jxb]').forEach(btn => {
        btn.onclick = async () => {
            if (!confirm('确认退选该课程？')) return;
            btn.disabled = true;
            btn.innerHTML = '<span class="spinner"></span>';
            try {
                const r = await API.quit(state.currentId, {
                    kchId: btn.dataset.kch,
                    jxbId: btn.dataset.jxb,
                });
                if (r.ok) {
                    toast('退课成功 ✅', 'ok');
                    await fetchChosen();
                } else {
                    toast('退课失败: ' + (r.msg || ''), 'err');
                    btn.disabled = false;
                    btn.textContent = '退课';
                }
            } catch (e) {
                toast('退课出错: ' + e.message, 'err');
                btn.disabled = false;
                btn.textContent = '退课';
            }
        };
    });
}

async function fetchChosen() {
    const id = state.currentId;
    if (!id) return;
    const xnm = ($('#chosen-xnm').value || '').trim();
    const xqm = $('#chosen-xqm').value;
    if (!xnm) { toast('请输入学年', 'warn'); return; }
    if (!ensureLoggedInForQuery()) return;
    if (isQueryBusy('chosen', id)) return;

    state.queryBusy[queryBusyKey(id, 'chosen')] = true;
    syncQueryButton($('#btn-fetch-chosen'), 'chosen', '查询');
    try {
        const list = await API.selected(id, xnm, xqm);
        // 期间切了账号：这份结果属于旧账号，不能渲染进新账号的面板
        if (state.currentId !== id) return;
        renderChosen(list);
    } catch (e) {
        if (state.currentId !== id) return;
        if (e && e.code === 'no_permission') {
            renderNoPermission('chosen');
        } else if (!isUnauthorized(e)) {
            toast('查询失败: ' + e.message, 'err');
        }
    } finally {
        delete state.queryBusy[queryBusyKey(id, 'chosen')];
        // 按"当前"账号重绘：可能已经切走，也可能切走又切回来了
        syncQueryButton($('#btn-fetch-chosen'), 'chosen', '查询');
    }
}

// 学校返回“无功能权限”时，在结果区说明原因：这不是“没有数据”，
// 而是该账号（常见于公共账号）没有开通这个功能，因此不弹红色 toast。
function renderNoPermission(kind) {
    if (kind === 'chosen') {
        const c = $('#chosen-container');
        if (c) c.innerHTML = '<p class="notice">该账号没有「已选课程」功能权限。</p>';
        return;
    }
    if (kind === 'tools-info') {
        const c = $('#tools-info-container');
        if (c) c.innerHTML = '<p class="notice">该账号没有「个人信息」功能权限。</p>';
        return;
    }
    if (kind === 'tools-photo') {
        const c = $('#tools-photo-container');
        if (c) c.innerHTML = '<p class="notice">该账号没有「大头照」功能权限。</p>';
        return;
    }
    state.scores = [];
    const summary = $('#score-summary');
    if (summary) summary.textContent = '';
    const c = $('#score-container');
    if (c) c.innerHTML = '<p class="notice">该账号没有「考试成绩」功能权限。</p>';
}

/* =========================================================================
 * 考试成绩
 * ========================================================================= */

async function loadScoreSuggest() {
    const id = state.currentId;
    if (!id) return;
    try {
        const s = await API.scoreSuggest(id);
        if (state.currentId !== id) return;   // 期间切了账号：别把旧账号的建议值写进新账号
        if (s && s.xnm) $('#score-xnm').value = s.xnm;
        if (s && s.xqm) $('#score-xqm').value = s.xqm;
    } catch (e) { /* 建议值失败不打扰用户 */ }
}

async function fetchScore() {
    const id = state.currentId;
    if (!id) return;
    const xnm = ($('#score-xnm').value || '').trim();
    const xqm = $('#score-xqm').value;
    if (!xnm) { toast('请输入学年', 'warn'); return; }
    if (!ensureLoggedInForQuery()) return;
    if (isQueryBusy('score', id)) return;

    state.queryBusy[queryBusyKey(id, 'score')] = true;
    syncQueryButton($('#btn-fetch-score'), 'score', '查询');
    try {
        const list = await API.scores(id, xnm, xqm);
        if (state.currentId !== id) return;   // 期间切了账号：结果丢弃
        state.scores = list || [];
        renderScore(state.scores);
    } catch (e) {
        if (state.currentId !== id) return;
        if (e && e.code === 'no_permission') {
            renderNoPermission('score');
        } else if (!isUnauthorized(e)) {
            toast('查询失败: ' + e.message, 'err');
        }
    } finally {
        delete state.queryBusy[queryBusyKey(id, 'score')];
        syncQueryButton($('#btn-fetch-score'), 'score', '查询');
    }
}

function renderScore(list) {
    const c = $('#score-container');
    const summary = $('#score-summary');
    if (!list || !list.length) {
        c.innerHTML = '<p class="muted">没有查询到成绩。</p>';
        summary.textContent = '';
        return;
    }
    let passCount = 0, failCount = 0, creditSum = 0;
    let weightedGPA = 0, weightSum = 0;

    let html = '<table><thead><tr>' +
        '<th style="width:40px">#</th><th>课程名称</th>' +
        '<th style="width:110px">教师</th><th style="width:70px">学分</th>' +
        '<th style="width:80px">成绩</th><th style="width:70px">绩点</th>' +
        '<th style="width:110px">课程性质</th></tr></thead><tbody>';

    list.forEach((it, i) => {
        const kcmc  = pick(it, 'kcmc', 'Kcmc') || '';
        const jsxm  = pick(it, 'jsxm', 'Jsxm') || '';
        const xf    = pick(it, 'xf', 'Xf') || '';
        const bfzcj = pick(it, 'bfzcj', 'Bfzcj') || '';
        const cj    = pick(it, 'cj', 'Cj') || '';
        const jd    = pick(it, 'jd', 'Jd') || '';
        const kcxz  = pick(it, 'kcxzmc', 'Kcxzmc', 'kclbmc', 'Kclbmc') || '';
        const score = bfzcj || cj;
        const num   = parseFloat(bfzcj);
        const failed = !isNaN(num) && num < 60;

        if (failed) failCount++;
        else if (!isNaN(num)) passCount++;

        const xfNum = parseFloat(xf);
        if (!isNaN(xfNum) && xfNum > 0) {
            if (!failed) creditSum += xfNum;
            const jdNum = parseFloat(jd);
            if (!isNaN(jdNum)) { weightedGPA += xfNum * jdNum; weightSum += xfNum; }
        }

        html += `<tr class="${failed ? 'score-fail' : ''}">
            <td>${i + 1}</td><td>${esc(kcmc)}</td><td>${esc(jsxm)}</td>
            <td>${esc(xf)}</td><td><b>${esc(score)}</b></td>
            <td>${esc(jd)}</td><td>${esc(kcxz)}</td>
        </tr>`;
    });
    html += '</tbody></table>';
    c.innerHTML = html;

    const avgGPA = weightSum > 0 ? weightedGPA / weightSum : 0;
    summary.textContent =
        `共 ${list.length} 门 · 通过 ${passCount} · 挂科 ${failCount} · ` +
        `获得学分 ${creditSum.toFixed(1)} · 平均绩点 ${avgGPA.toFixed(2)}`;
}

/* =========================================================================
 * 其他工具（个人信息 / 大头照 / Cookie / 会话诊断）
 * ========================================================================= */

// 只做展示用的字段顺序；原始 JSON 仍完整保留在折叠区里。
const TOOLS_INFO_FIELDS = [
    ['姓名', 'xm'], ['学号', 'xh'], ['性别', 'xbm'], ['民族', 'mzm'],
    ['出生日期', 'csrq'], ['政治面貌', 'zzmmm'], ['学院', 'jg_id'],
    ['专业', 'zyh_id'], ['班级', 'bh_id'], ['年级', 'njdm_id'],
    ['学制', 'xz'], ['学籍状态', 'xjztdm'], ['入学日期', 'rxrq'],
    ['学期', 'xqmc'], ['生源地', 'syd'], ['毕业中学', 'byzx'],
    ['考生号', 'ksh'], ['手机号', 'sjhm'], ['固定电话', 'gddh'],
    ['QQ', 'qqhm'], ['家庭电话', 'jtdh'], ['家庭地址', 'jtdz'],
    ['通讯地址', 'txdz'], ['辅导员', 'fdyjgh'], ['证件类型', 'zjlxm'],
    ['证件号码', 'zjhm'], ['姓名拼音', 'xmpy'],
];

const TOOLS_SEX_TEXT = { '1': '男', '2': '女' };

function toolsText(value) {
    if (value == null) return '';
    if (Array.isArray(value)) return value.join('、');
    if (typeof value === 'object') return JSON.stringify(value);
    return String(value);
}

function formatToolsTime(value) {
    if (value == null || value === '') return '';
    const d = (typeof value === 'number' || /^\d+$/.test(String(value)))
        ? new Date(Number(value))
        : new Date(value);
    return isNaN(d.getTime()) ? String(value) : d.toLocaleString();
}

async function copyText(text) {
    if (!text) return false;
    try {
        if (navigator.clipboard && window.isSecureContext) {
            await navigator.clipboard.writeText(text);
            return true;
        }
    } catch (e) { /* 回退到 execCommand，兼容非安全上下文 */ }
    try {
        const ta = document.createElement('textarea');
        ta.value = text;
        ta.setAttribute('readonly', '');
        ta.style.position = 'fixed';
        ta.style.top = '-9999px';
        ta.style.opacity = '0';
        document.body.appendChild(ta);
        ta.select();
        const ok = document.execCommand('copy');
        ta.remove();
        return ok;
    } catch (e) {
        return false;
    }
}

async function copyToolsJSON(value, okMsg) {
    if (value == null) return;
    const ok = await copyText(JSON.stringify(value, null, 2));
    toast(ok ? okMsg : '复制失败，请手动选择文本', ok ? 'ok' : 'warn');
    return ok;
}

function syncToolsButton(btn, busy, label) {
    if (!btn) return;
    if (!btn.dataset.label) btn.dataset.label = label;
    btn.disabled = busy;
    if (busy) {
        btn.innerHTML = '<span class="spinner dark"></span>';
    } else {
        btn.textContent = btn.dataset.label;
    }
}

function revokeToolsPhoto() {
    if (state.tools.photoUrl) {
        try { URL.revokeObjectURL(state.tools.photoUrl); } catch (e) {}
        state.tools.photoUrl = null;
    }
}

// resetToolsUI 清空上一个账号的所有工具结果：照片 blob URL 必须显式释放，
// 否则切账号后旧照片会一直占内存。
function resetToolsUI() {
    revokeToolsPhoto();
    state.tools.info = null;
    state.tools.photoType = '';
    state.tools.cookies = null;
    state.tools.debug = null;
    Object.keys(toolsBusy).forEach(k => { toolsBusy[k] = false; });

    $('#tools-info-container').innerHTML =
        '<p class="muted">点击「获取个人信息」后显示姓名、学号、学院、专业等字段。</p>';
    $('#tools-photo-container').innerHTML = '<p class="muted">尚未加载。</p>';
    $('#tools-cookies-container').innerHTML =
        '<p class="muted">打开本页会自动读取当前 Cookie。</p>';
    $('#tools-debug-container').innerHTML =
        '<p class="muted">打开本页会自动生成诊断快照。</p>';

    $('#btn-tools-info-copy').disabled = true;
    $('#btn-tools-photo-download').disabled = true;
    $('#btn-tools-cookies-copy').disabled = true;
    $('#btn-tools-debug-copy').disabled = true;

    $('#tools-info-hint').textContent = '';
    $('#tools-photo-hint').textContent = '';
    $('#tools-cookies-hint').textContent = '';
    $('#tools-debug-hint').textContent = '';

    syncToolsButton($('#btn-tools-info'), false, '获取个人信息');
    syncToolsButton($('#btn-tools-photo'), false, '获取大头照');
    syncToolsButton($('#btn-tools-cookies'), false, '刷新 Cookie');
    syncToolsButton($('#btn-tools-debug'), false, '刷新诊断');
}

function renderToolsInfo(info) {
    const c = $('#tools-info-container');
    if (!c) return;
    info = info || {};
    const rows = TOOLS_INFO_FIELDS.map(([label, key]) => {
        let value = info[key];
        if (key === 'xbm' && TOOLS_SEX_TEXT[value]) value = TOOLS_SEX_TEXT[value];
        value = toolsText(value);
        if (!value) return '';
        return '<div class="info-item">' +
            '<span class="info-label">' + esc(label) + '</span>' +
            '<span class="info-value">' + esc(value) + '</span>' +
            '</div>';
    }).join('');
    const raw = JSON.stringify(info, null, 2);
    c.innerHTML =
        '<div class="info-grid">' +
        (rows || '<p class="muted">学校没有返回可展示的字段。</p>') +
        '</div>' +
        '<details class="raw-details"><summary>原始 JSON</summary>' +
        '<pre class="code-block">' + esc(raw) + '</pre></details>';
}

async function fetchToolsInfo() {
    const id = state.currentId;
    if (!id || toolsBusy.info) return;
    if (!ensureLoggedInForQuery()) return;
    toolsBusy.info = true;
    syncToolsButton($('#btn-tools-info'), true, '获取个人信息');
    $('#tools-info-hint').textContent = '';
    try {
        const info = await API.userInfo(id);
        if (state.currentId !== id) return;
        state.tools.info = info || {};
        renderToolsInfo(state.tools.info);
        $('#btn-tools-info-copy').disabled = false;
        $('#tools-info-hint').textContent = '已获取';
    } catch (e) {
        if (state.currentId !== id) return;
        if (e && e.code === 'no_permission') {
            state.tools.info = null;
            $('#btn-tools-info-copy').disabled = true;
            renderNoPermission('tools-info');
        } else if (isUnauthorized(e)) {
            // 登录态切换与提示已经由 API.req 统一处理，这里只更新局部文案。
            $('#tools-info-hint').textContent = e.message || '尚未登录';
        } else {
            $('#tools-info-hint').textContent = (e && e.message) || '获取失败';
            toast('获取个人信息失败: ' + ((e && e.message) || e), 'err');
        }
    } finally {
        toolsBusy.info = false;
        if (state.currentId === id) {
            syncToolsButton($('#btn-tools-info'), false, '获取个人信息');
        }
    }
}

async function fetchToolsPhoto() {
    const id = state.currentId;
    if (!id || toolsBusy.photo) return;
    if (!ensureLoggedInForQuery()) return;
    toolsBusy.photo = true;
    syncToolsButton($('#btn-tools-photo'), true, '获取大头照');
    $('#tools-photo-hint').textContent = '';
    try {
        const r = await API.photo(id);
        if (state.currentId !== id) return;
        revokeToolsPhoto();
        state.tools.photoUrl = URL.createObjectURL(r.blob);
        state.tools.photoType = (r.contentType || r.blob.type || '').split(';')[0].trim();
        $('#tools-photo-container').innerHTML =
            '<img class="student-photo" alt="学生大头照" src="' +
            state.tools.photoUrl + '">';
        $('#btn-tools-photo-download').disabled = false;
        const kb = r.blob.size / 1024;
        $('#tools-photo-hint').textContent = '已获取（' + kb.toFixed(1) + ' KB）';
    } catch (e) {
        if (state.currentId !== id) return;
        $('#btn-tools-photo-download').disabled = true;
        if (e && e.code === 'no_permission') {
            renderNoPermission('tools-photo');
        } else if (isUnauthorized(e)) {
            $('#tools-photo-hint').textContent = e.message || '尚未登录';
        } else {
            $('#tools-photo-hint').textContent = (e && e.message) || '获取失败';
            toast('获取大头照失败: ' + ((e && e.message) || e), 'err');
        }
    } finally {
        toolsBusy.photo = false;
        if (state.currentId === id) {
            syncToolsButton($('#btn-tools-photo'), false, '获取大头照');
        }
    }
}

function downloadToolsPhoto() {
    if (!state.tools.photoUrl) return;
    const type = state.tools.photoType || '';
    let ext = 'jpg';
    if (type.indexOf('png') >= 0) ext = 'png';
    else if (type.indexOf('webp') >= 0) ext = 'webp';
    else if (type.indexOf('gif') >= 0) ext = 'gif';
    const a = document.createElement('a');
    a.href = state.tools.photoUrl;
    a.download = 'student-photo.' + ext;
    document.body.appendChild(a);
    a.click();
    a.remove();
}

function toolsCookieExpiry(expiresAt) {
    return expiresAt ? formatToolsTime(expiresAt) : '会话';
}

function renderToolsCookies(data) {
    const c = $('#tools-cookies-container');
    if (!c) return;
    const list = (data && Array.isArray(data.cookies)) ? data.cookies : [];
    if (!list.length) {
        c.innerHTML = '<p class="muted">当前会话没有 Cookie（可能尚未登录，或 Cookie 已过期）。</p>';
        return;
    }
    const rows = list.map(cookie => {
        const flags = [cookie.secure ? 'Secure' : '', cookie.httpOnly ? 'HttpOnly' : '']
            .filter(Boolean).join(' / ') || '-';
        return '<tr>' +
            '<td>' + esc(cookie.name) + '</td>' +
            '<td class="cookie-value" title="' + esc(cookie.value) + '">' +
                esc(cookie.value) + '</td>' +
            '<td>' + esc(cookie.domain || '-') + '</td>' +
            '<td>' + esc(cookie.path || '/') + '</td>' +
            '<td>' + esc(toolsCookieExpiry(cookie.expiresAt)) + '</td>' +
            '<td>' + esc(flags) + '</td>' +
            '</tr>';
    }).join('');
    c.innerHTML = '<div style="overflow-x:auto"><table>' +
        '<thead><tr><th>名称</th><th>值</th><th>域</th><th>路径</th>' +
        '<th>过期时间</th><th>属性</th></tr></thead>' +
        '<tbody>' + rows + '</tbody></table></div>';
}

async function fetchToolsCookies(auto) {
    const id = state.currentId;
    if (!id || toolsBusy.cookies) return;
    toolsBusy.cookies = true;
    syncToolsButton($('#btn-tools-cookies'), true, '刷新 Cookie');
    if (!auto) $('#tools-cookies-hint').textContent = '';
    try {
        const data = await API.cookies(id);
        if (state.currentId !== id) return;
        state.tools.cookies = data || null;
        renderToolsCookies(state.tools.cookies);
        const count = (data && typeof data.count === 'number') ? data.count : 0;
        $('#tools-cookies-hint').textContent = data && data.loggedIn
            ? ('已读取 ' + count + ' 个 Cookie')
            : ('当前未登录，本地会话 Cookie ' + count + ' 个');
        $('#btn-tools-cookies-copy').disabled = !(data && data.header);
    } catch (e) {
        if (state.currentId !== id) return;
        $('#tools-cookies-hint').textContent = (e && e.message) || '读取失败';
        toast('读取 Cookie 失败: ' + ((e && e.message) || e), 'err');
    } finally {
        toolsBusy.cookies = false;
        if (state.currentId === id) {
            syncToolsButton($('#btn-tools-cookies'), false, '刷新 Cookie');
        }
    }
}

function renderToolsDebug(data) {
    const c = $('#tools-debug-container');
    if (!c) return;
    data = data || {};
    const account = data.account || {};
    const engine = data.engine || {};
    const session = data.session || {};
    const tabs = Array.isArray(engine.tabs) ? engine.tabs : [];
    const identity = engine.identity || {};
    const identityText = [identity.name, identity.sub, identity.org]
        .filter(Boolean).join(' / ');
    const rows = [
        ['生成时间', formatToolsTime(data.generatedAt)],
        ['账号', account.name],
        ['用户名', account.username],
        ['教务地址', account.baseUrl],
        ['登录方式', account.loginMode],
        ['登录态', engine.loggedIn ? '已登录' : '未登录'],
        ['已初始化', engine.inited ? '是' : '否'],
        ['页签名', engine.modeName],
        ['页签数', tabs.length ? String(tabs.length) : ''],
        ['课程缓存', String(engine.courseCacheLen || 0) + ' 条'],
        ['身份', identityText],
        ['JWT', session.hasTicketJWT ? ('有（' + (session.ticketJWTLen || 0) + ' 字节）') : '无'],
        ['学习路由', Array.isArray(session.learnedRoutes) ? session.learnedRoutes.join(', ') : ''],
        ['修正地址', session.correctedBaseURL],
        ['密码', account.hasPassword ? '已保存' : '未保存'],
        ['CAS 密码', account.hasCasPassword ? '已保存' : '未保存'],
    ].map(([label, value]) => {
        value = toolsText(value);
        if (!value) return '';
        return '<div class="info-item">' +
            '<span class="info-label">' + esc(label) + '</span>' +
            '<span class="info-value">' + esc(value) + '</span>' +
            '</div>';
    }).join('');
    const raw = JSON.stringify(data, null, 2);
    c.innerHTML =
        '<div class="info-grid">' + rows + '</div>' +
        '<details class="raw-details"><summary>完整诊断 JSON</summary>' +
        '<pre class="code-block">' + esc(raw) + '</pre></details>';
}

async function fetchToolsDebug(auto) {
    const id = state.currentId;
    if (!id || toolsBusy.debug) return;
    toolsBusy.debug = true;
    syncToolsButton($('#btn-tools-debug'), true, '刷新诊断');
    if (!auto) $('#tools-debug-hint').textContent = '';
    try {
        const data = await API.toolsDebug(id);
        if (state.currentId !== id) return;
        state.tools.debug = data || null;
        renderToolsDebug(state.tools.debug);
        $('#btn-tools-debug-copy').disabled = false;
        $('#tools-debug-hint').textContent = '已生成';
    } catch (e) {
        if (state.currentId !== id) return;
        $('#tools-debug-hint').textContent = (e && e.message) || '生成失败';
        toast('生成诊断信息失败: ' + ((e && e.message) || e), 'err');
    } finally {
        toolsBusy.debug = false;
        if (state.currentId === id) {
            syncToolsButton($('#btn-tools-debug'), false, '刷新诊断');
        }
    }
}

// 打开「其他工具」时只自动拉本地快照（Cookie / 诊断），学校接口留给按钮。
function ensureToolsLocal() {
    if (!state.currentId) return;
    if (!state.tools.cookies) fetchToolsCookies(true).catch(() => {});
    if (!state.tools.debug) fetchToolsDebug(true).catch(() => {});
}

/* =========================================================================
 * 初始化
 * ========================================================================= */

function renderInitButtons() {
    const b1 = $('#btn-init');
    const b2 = $('#btn-reinit');
    b1.classList.remove('danger');
    b2.classList.remove('danger');

    if (!state.initializing) {
        b1.classList.add('primary');
        b1.textContent = '初始化';
        b1.disabled = false;
        b2.classList.remove('primary');
        b2.textContent = '重新初始化';
        b2.disabled = false;
        return;
    }
    if (state.initForce) {
        b1.disabled = true;
        b2.classList.remove('primary');
        b2.classList.add('danger');
        b2.textContent = '取消初始化';
        b2.disabled = false;
    } else {
        b2.disabled = true;
        b1.classList.remove('primary');
        b1.classList.add('danger');
        b1.textContent = '取消初始化';
        b1.disabled = false;
    }
}

// reportTaskResult 按任务最终快照给一次提示（用于初始化这种"后台跑完"的场景）。
// lastTaskToast 保证同一次任务只提示一次，避免重复轮询时刷屏。
let lastTaskToast = { id: 0, status: '' };

function reportTaskResult(t) {
    if (!t || !t.kind) return;
    if (lastTaskToast.id === t.id && lastTaskToast.status === t.status) return;
    lastTaskToast = { id: t.id, status: t.status || '' };

    const label = TASK_KIND_TEXT[t.kind] || '任务';
    const lastEv = (t.events && t.events.length) ? t.events[t.events.length - 1] : null;
    const text = (lastEv && lastEv.text) || (label + (TASK_STATUS_TEXT[t.status] || ''));
    switch (t.status) {
        case 'done':      toast(label + '完成 ✅', 'ok'); break;
        case 'cancelled': toast(text, 'warn'); break;
        case 'timeout':   toast(text, 'err'); break;
        default:          toast(label + '失败: ' + text, 'err'); break;
    }
    // 任务结束后补一次完整状态刷新（页签/课程都变了）
    Task.needsRefresh = false;
    refreshStatus().catch(() => {});
}

async function doInit(force) {
    if (!state.currentId || state.initializing) return;
    const id = state.currentId;

    // 后端会立刻建立任务，这里只负责乐观地把按钮切成"取消"
    state.initializing = true;
    state.initForce = !!force;
    renderInitButtons();
    renderStatus();

    let result = null;
    try {
        // 请求本身会一直阻塞到任务结束；期间界面由推送驱动。
        // 刷新页面后这条 promise 消失也不影响：新页面会重新接管任务。
        result = await API.init(id, force);
    } catch (e) {
        // 会话失效已经由 API.req 统一提示并把徽标切成未登录；
        // 这里再弹一条"初始化请求失败"只会让同一次失效出现两条红字。
        if (state.currentId === id && !isUnauthorized(e)) {
            toast('初始化请求失败: ' + e.message, 'err');
        }
    } finally {
        if (state.currentId === id) {
            // 初始化（可能顺带登录）刚结束：作废过程中的旧状态请求再拉一次
            invalidateStatus();
            await refreshStatus({ quiet: true }).catch(() => {});
            // 重新初始化后页签可能已经换了；modeName 没变时 applyStatusPayload
            // 不会重载，这里显式拉一次，保证「重新初始化」真的刷新页签。
            if (force && result && result.status === 'ok') {
                await loadTabs().catch(() => {});
            }
            const t = Task.last;
            if (t && t.kind === 'init' && !t.running) reportTaskResult(t);
        }
    }
    return result;
}

async function cancelInit() {
    if (!state.currentId) return;
    const id = state.currentId;
    // 初始化内部可能先做登录，两个取消都发一遍，谁的 noop 都不影响
    await Promise.allSettled([
        API.cancelInit(id),
        API.cancelLogin(id),
    ]);
    invalidateStatus();
    await refreshStatus({ quiet: true }).catch(() => {});
}

// cancelCurrentTask 取消当前正在跑的任务（登录或初始化）。
async function cancelCurrentTask() {
    if (!state.currentId) return;
    const id = state.currentId;
    const kind = Task.last && Task.last.kind;
    dismissInputDialog();
    try {
        if (kind === 'init') {
            await Promise.allSettled([API.cancelInit(id), API.cancelLogin(id)]);
        } else {
            await API.cancelLogin(id);
        }
    } catch (e) {
        toast('取消请求失败: ' + e.message, 'err');
    }
    // 取消会改任务状态：作废取消前发出的旧状态请求
    invalidateStatus();
    await refreshStatus({ quiet: true }).catch(() => {});
}

/* =========================================================================
 * 侧边栏折叠 / 顶部 Tab
 * ========================================================================= */

const SIDEBAR_KEY = 'schoolSdk.sidebarCollapsed';

function setSidebarCollapsed(collapsed) {
    collapsed = !!collapsed;
    $('#app').classList.toggle('collapsed', collapsed);
    $('#sidebar').classList.toggle('collapsed', collapsed);
    $('#btn-expand').hidden = !collapsed;
    try { localStorage.setItem(SIDEBAR_KEY, collapsed ? '1' : '0'); } catch (e) {}
}

function initSidebar() {
    let collapsed = false;
    try { collapsed = localStorage.getItem(SIDEBAR_KEY) === '1'; } catch (e) {}
    if (!collapsed) return;
    $('#app').classList.add('no-anim');
    setSidebarCollapsed(true);
    requestAnimationFrame(() => requestAnimationFrame(() => {
        $('#app').classList.remove('no-anim');
    }));
}

function switchTab(name) {
    $$('.tabs button').forEach(b => b.classList.toggle('active', b.dataset.tab === name));
    $$('.tab-content').forEach(s => s.classList.toggle('active', s.id === 'tab-' + name));
    // 打开「课程浏览」：先取服务端缓存（不发学校接口），缓存为空再自动拉一次
    if (name === 'courses') ensureCoursesLoaded().catch(() => {});
    // 打开「自动选课」：回填该账号已保存的愿望清单
    if (name === 'auto') loadWish(false).catch(() => {});
    // 打开「其他工具」：本地快照（Cookie/诊断）自动拉，学校接口按按钮触发
    if (name === 'tools') ensureToolsLocal();
}

/* =========================================================================
 * 账号弹窗
 * ========================================================================= */

// syncHedgingFields 根据 hedging 勾选状态显示/隐藏对冲延迟输入。
function syncHedgingFields(checked) {
    $('#field-hedging-delay').hidden = !checked;
}

// 登录方式下拉：password / cas / wechat。三种方式都已贯通到后端，
// wechat 的二维码由登录任务通过 SSE 推送、/login/qr 提供图片。
const LOGIN_MODE_TEXT = {
    password: '账号密码（教务系统）',
    cas: '统一身份认证（CAS 密码）',
    wechat: '微信扫码登录',
};

function resetLoginModeOptions() {
    const sel = $('#acc-loginMode');
    for (const opt of [...sel.options]) {
        if (opt.dataset.dynamic === '1') opt.remove();
    }
}

function syncLoginModeFields() {
    const mode = $('#acc-loginMode').value;
    $('#field-cas-pass').hidden = mode !== 'cas';
    // 微信扫码不需要账号密码；登录时由扫码流程建立会话
    $('#field-password').hidden = mode === 'wechat';
}

function setLoginMode(mode) {
    const sel = $('#acc-loginMode');
    if (![...sel.options].some(o => o.value === mode)) {
        const opt = new Option(LOGIN_MODE_TEXT[mode] || mode, mode);
        opt.dataset.dynamic = '1';
        sel.add(opt);
    }
    sel.value = mode;
    syncLoginModeFields();
}

function openCreateAccountDialog() {
    dialogState.mode = 'create';
    dialogState.id = null;
    const form = $('#form-account');
    form.reset();
    form.elements.baseUrl.value       = 'https://jwglxt.ycit.edu.cn/';
    form.elements.timeout.value       = '43s';
    form.elements.loginTimeout.value  = '10m';
    form.elements.userAgent.value     = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:157.0) Gecko/20100101 Firefox/157.0';
    form.elements.hedgingDelay.value  = '21s';
    form.elements.routes.value        = '';
    resetPasswordEyes();
    resetLoginModeOptions();
    setLoginMode('password');
    $('#acc-hedging').checked = false;
    syncHedgingFields(false);
    $('#pass-hint').textContent = '';
    $('#caspass-hint').textContent = '';
    $('#dlg-title').textContent  = '新建账号';
    $('#dlg-submit').textContent = '创建';
    $('#dlg-delete').hidden = true;
    $('#dlg-account').showModal();
}

function openEditAccountDialog(id) {
    const a = state.accounts.find(x => String(x.id) === String(id));
    if (!a) { toast('账号不存在', 'err'); return; }
    dialogState.mode = 'edit';
    dialogState.id = a.id;
    const form = $('#form-account');
    form.reset();
    form.elements.name.value        = a.name || '';
    form.elements.baseUrl.value     = a.baseUrl || '';
    form.elements.username.value    = a.username || '';
    form.elements.password.value    = a.password    || '';
    form.elements.casPassword.value = a.casPassword || '';
    form.elements.userAgent.value   = a.userAgent || '';
    form.elements.timeout.value     = a.timeout || '43s';
    form.elements.loginTimeout.value = a.loginTimeout || '10m';
    form.elements.hedgingDelay.value = a.hedgingDelay || '21s';
    form.elements.routes.value      = Array.isArray(a.routes) ? a.routes.join('\n') : '';
    resetPasswordEyes();
    resetLoginModeOptions();
    setLoginMode(a.loginMode || (a.casLogin ? 'cas' : 'password'));
    $('#acc-hedging').checked = !!a.hedging;
    syncHedgingFields(!!a.hedging);
    $('#pass-hint').textContent    = '（留空表示不修改）';
    $('#caspass-hint').textContent = '（留空表示不修改）';
    $('#dlg-title').textContent  = '编辑账号';
    $('#dlg-submit').textContent = '保存';
    $('#dlg-delete').hidden = false;
    $('#dlg-account').showModal();
}

async function confirmDeleteAccount(id) {
    const a = state.accounts.find(x => String(x.id) === String(id));
    const label = a ? (a.name || a.username) : id;
    if (!confirm(`确定删除账号「${label}」？该操作不可恢复。`)) return false;
    try {
        await API.deleteAccount(id);
        toast('已删除', 'ok');
        if ($('#dlg-account').open) $('#dlg-account').close();
        const wasCurrent = String(state.currentId) === String(id);
        if (wasCurrent) {
            dismissQRDialog();
            state.currentId = null;
            state.current = null;
            showPanel(false);
        }
        await loadAccounts();
        if (wasCurrent) {
            const next = state.accounts[0];
            if (next) await selectAccount(next.id);
        }
        return true;
    } catch (e) {
        toast('删除失败: ' + e.message, 'err');
        return false;
    }
}

// submitInputToLogin 处理输入对话框的结果：
//   - null 表示用户取消 → 视为取消整次登录（与旧行为一致）
//   - 否则提交给后端
async function handleLoginInput(value) {
    const id = state.currentId;
    if (!id) return;
    if (value === null) {
        try { await API.cancelLogin(id); } catch (e) { /* 忽略 */ }
        return;
    }
    try {
        await API.submitLoginInput(id, value);
    } catch (e) {
        toast('提交输入失败: ' + e.message, 'err');
    }
    await refreshStatus({ quiet: true }).catch(() => {});
}

function setLoginBtnBusy(btn) {
    btn.classList.remove('primary');
    btn.classList.add('danger');
    btn.textContent = '取消登录';
}
function setLoginBtnIdle(btn) {
    btn.classList.remove('danger');
    btn.classList.add('primary');
    btn.textContent = '登录';
}

/* =========================================================================
 * 登录过程中的交互输入（验证码 / 新密码）
 * ========================================================================= */

let inputDialogResolve = null;

// promptLoginInput 弹出对话框，等待用户输入，返回：
//   - 用户提交的内容（字符串）
//   - null 表示用户取消（点取消按钮 / 按 Esc / 被外部 dismiss）
function promptLoginInput(pending) {
    const dlg     = $('#dlg-input');
    const form    = $('#form-input');
    const input   = $('#input-value');
    const imgWrap = $('#input-image-wrap');
    const img     = $('#input-image');

    $('#dlg-input-title').textContent =
        pending.kind === 'password' ? '密码错误' : '需要验证码';
    $('#input-prompt').textContent     = pending.prompt || '';
    $('#input-value-label').textContent =
        pending.kind === 'password' ? '新密码' : '验证码';

    if (pending.imageBase64) {
        img.src = 'data:image/png;base64,' + pending.imageBase64;
        imgWrap.hidden = false;
    } else {
        img.removeAttribute('src');
        imgWrap.hidden = true;
    }

    input.value = '';
    input.type  = pending.kind === 'password' ? 'password' : 'text';

    return new Promise((resolve) => {
        let settled = false;
        const finish = (val) => {
            if (settled) return;
            settled = true;
            inputDialogResolve = null;
            form.onsubmit              = null;
            $('#dlg-input-cancel').onclick = null;
            dlg.oncancel = null;
            dlg.onclose  = null;
            if (dlg.open) dlg.close();
            resolve(val);
        };
        inputDialogResolve = finish;

        form.onsubmit = (e) => {
            e.preventDefault();
            const v = (input.value || '').trim();
            if (!v) { toast('请输入内容', 'warn'); return; }
            finish(v);
        };
        $('#dlg-input-cancel').onclick = () => finish(null);
        // Esc 等同于取消
        dlg.oncancel = (e) => { e.preventDefault(); finish(null); };

        dlg.showModal();
        setTimeout(() => input.focus(), 30);
    });
}

// dismissInputDialog 由外部主动关闭（比如点「取消登录」或登录流程自然结束）
function dismissInputDialog() {
    if (inputDialogResolve) inputDialogResolve(null);
    const dlg = $('#dlg-input');
    if (dlg.open) dlg.close();
}

/* =========================================================================
 * 微信扫码登录
 *
 * 二维码图片不进 SSE（每帧全量重发太重），推送只带 rev/创建/过期时间；
 * 图片由 GET /api/accounts/:id/login/qr?rev=N 单独取。rev 变化才换 img.src，
 * 避免同一张图被反复请求、也避免旧图盖掉新图。
 * ========================================================================= */

let qrShownRev = null;
let qrShownId  = null;
let qrCancelling = false;

function maybeShowQR(qr) {
    const dlg = $('#dlg-qr');
    if (!dlg) return;
    // 无账号扫码自己管这个弹窗：当前账号的推送不能把它关掉或覆盖
    if (wechatFlow.active) return;
    if (!qr || !state.currentId) {
        dismissQRDialog();
        return;
    }
    const id = String(state.currentId);
    if (qrShownId !== id || qrShownRev !== qr.rev) {
        qrShownId  = id;
        qrShownRev = qr.rev;
        const img = $('#qr-image');
        if (img) {
            img.src = '/api/accounts/' + encodeURIComponent(id) +
                '/login/qr?rev=' + encodeURIComponent(qr.rev);
        }
    }
    if (!dlg.open) dlg.showModal();
    syncQRStatus();
}

// syncQRStatus 更新弹窗里的阶段文案和过期倒计时；由 renderStatus 在每帧快照/进度里调用。
function syncQRStatus() {
    const dlg = $('#dlg-qr');
    if (!dlg || !dlg.open) return;
    if (wechatFlow.active) return; // 文案归 renderWechatFlow 管
    const el = $('#qr-status');
    if (!el) return;
    let text = (Task.last && Task.last.stageText) || '正在获取二维码…';
    const qr = state.status && state.status.qr;
    if (qr && qr.expiresAt > 0) {
        const left = Math.ceil((qr.expiresAt - Date.now()) / 1000);
        if (left > 0) text += ' · ' + left + ' 秒后过期';
    }
    el.textContent = text;
}

// 二维码弹窗只提供"在这个窗口里扫"：微信登录的 uuid 是服务端打开扫码页时
// 生成的，服务端只轮询这一个 uuid。把 pageUrl 复制到另一个标签页去扫，那个
// 页面会自己再申请一个 uuid，扫的是另一张码——它的 wx_code 只会回到那个标签页，
// 服务端这边的轮询永远等不到，于是"扫了却没登录、一直等待二维码"。
// 所以这里不给任何复制入口；pageUrl 只在 /status 里留着排查问题用。

function dismissQRDialog() {
    const dlg = $('#dlg-qr');
    if (!dlg) return;
    if (dlg.open) dlg.close();
    qrShownRev = null;
    qrShownId  = null;
    qrCancelling = false;
    const img = $('#qr-image');
    if (img) img.removeAttribute('src');
    const st = $('#qr-status');
    if (st) st.textContent = '';
    const btn = $('#dlg-qr-cancel');
    if (btn) btn.disabled = false;
}

// cancelQRLogin 取消整次登录（不是只关二维码弹窗）。
// 不主动 close：等后端清空二维码后的 snapshot/status 把弹窗关掉，
// 这样取消请求失败时二维码还在，用户可以直接重试。
async function cancelQRLogin() {
    if (qrCancelling) return;
    const id = state.currentId;
    if (!id) { dismissQRDialog(); return; }

    qrCancelling = true;
    state.loginCancelled = true;
    const btn = $('#dlg-qr-cancel');
    const st  = $('#qr-status');
    if (btn) btn.disabled = true;
    if (st) st.textContent = '正在取消登录…';
    try {
        await API.cancelLogin(id);
    } catch (e) {
        qrCancelling = false;
        state.loginCancelled = false;
        if (state.currentId !== id) return;
        if (btn) btn.disabled = false;
        if (st) st.textContent = '取消失败，可以再试一次';
        toast('取消登录失败: ' + e.message, 'err');
        return;
    }
    qrCancelling = false;
    if (state.currentId !== id) return;
    invalidateStatus();
    await refreshStatus({ quiet: true }).catch(() => {});
}

/* =========================================================================
 * 微信扫码登录（不需要账号）
 *
 * 和账号登录的二维码流程分开：扫码前没有账号可选，二维码、登录阶段都属于
 * "这次扫码"本身。扫码成功后服务端按 ticket 里的身份绑定或新建账号，前端拿到
 * accountId 再切过去 —— 所以整个流程不需要填任何东西，点一下就开始。
 * ========================================================================= */

const wechatFlow = {
    active:  false,
    timer:   null,
    token:   0,      // 每次开始/结束自增，用来丢弃上一轮的迟到响应
    rev:     null,
};

async function startWechatLogin() {
    if (wechatFlow.active) {
        const dlg = $('#dlg-qr');
        if (dlg && !dlg.open) dlg.showModal();
        return;
    }
    const btn = $('#btn-wechat');
    if (btn) btn.disabled = true;
    try {
        await API.wechatStart();
    } catch (e) {
        toast('发起扫码登录失败: ' + e.message, 'err');
        return;
    } finally {
        if (btn) btn.disabled = false;
    }

    wechatFlow.active  = true;
    wechatFlow.rev     = null;
    wechatFlow.token++;
    // 弹窗与账号登录共用：先清掉上一次的残留，再从零开始显示
    qrShownId = null;
    qrShownRev = null;
    qrCancelling = false;
    const img = $('#qr-image');
    if (img) img.removeAttribute('src');
    const st = $('#qr-status');
    if (st) st.textContent = '正在获取二维码…';
    const dlg = $('#dlg-qr');
    if (dlg && !dlg.open) dlg.showModal();

    wechatFlow.timer = setInterval(pollWechatFlow, 1000);
    pollWechatFlow();
}

async function pollWechatFlow() {
    if (!wechatFlow.active) return;
    const token = wechatFlow.token;
    let st;
    try {
        st = await API.wechatStatus();
    } catch (e) {
        return; // 网络抖动：下一轮再试，不打断扫码
    }
    if (!wechatFlow.active || token !== wechatFlow.token) return;
    if (st.accountId || !st.active) {
        finishWechatLogin(st);
        return;
    }
    renderWechatFlow(st);
}

function renderWechatFlow(st) {
    const qr = st.qr || null;
    if (qr && wechatFlow.rev !== qr.rev) {
        wechatFlow.rev = qr.rev;
        const img = $('#qr-image');
        if (img) img.src = '/api/wechat/qr?rev=' + encodeURIComponent(qr.rev);
    }
    const el = $('#qr-status');
    if (el) {
        let text = (st.task && st.task.stageText) || '正在获取二维码…';
        if (qr && qr.expiresAt > 0) {
            const left = Math.ceil((qr.expiresAt - Date.now()) / 1000);
            if (left > 0) text += ' · ' + left + ' 秒后过期';
        }
        el.textContent = text;
    }
}

// finishWechatLogin 收尾：成功就切到绑定的账号，失败就把原因说出来。
function finishWechatLogin(st) {
    const acc = st.account || null;
    const created = !!st.created;
    const err = st.error || '';
    stopWechatFlow();
    dismissQRDialog();

    if (!acc) {
        // 自己点的取消不用再报一次错，弹窗关掉就够了
        if (err && err !== '已取消') toast('扫码登录未完成: ' + err, 'err');
        return;
    }
    const label = acc.name || acc.username || '';
    toast(created ? `扫码成功，已新建账号「${label}」` : `扫码成功，已绑定账号「${label}」`, 'ok');
    loadAccounts()
        .then(() => selectAccount(acc.id))
        .catch(e => toast('刷新账号列表失败: ' + e.message, 'err'));
}

function stopWechatFlow() {
    wechatFlow.active = false;
    wechatFlow.token++;
    if (wechatFlow.timer) {
        clearInterval(wechatFlow.timer);
        wechatFlow.timer = null;
    }
}

// cancelWechatLogin 取消整次扫码。不主动关弹窗：等轮询看到 active=false 再收尾，
// 和账号登录保持一致（取消请求失败时二维码还在，可以再试一次）。
async function cancelWechatLogin() {
    const btn = $('#dlg-qr-cancel');
    const st = $('#qr-status');
    if (btn) btn.disabled = true;
    if (st) st.textContent = '正在取消扫码登录…';
    try {
        await API.wechatCancel();
    } catch (e) {
        if (btn) btn.disabled = false;
        if (st) st.textContent = '取消失败，可以再试一次';
        toast('取消扫码登录失败: ' + e.message, 'err');
    }
}

// cancelQROrWechat 二维码弹窗被两个流程共用：按当前是哪一个，取消对的那一个。
function cancelQROrWechat() {
    if (wechatFlow.active) return cancelWechatLogin();
    return cancelQRLogin();
}

function resetPasswordEyes() {
    const form = $('#form-account');
    $$('.eye-btn', form).forEach(btn => {
        btn.classList.remove('visible');
        btn.title = '显示密码';
        btn.setAttribute('aria-label', '显示密码');
        const input = form.elements.namedItem(btn.dataset.target);
        if (input && 'type' in input) input.type = 'password';
    });
}

/* =========================================================================
 * 事件绑定
 * ========================================================================= */

function bindEvents() {
    $('#btn-new').onclick = openCreateAccountDialog;
    $('#btn-wechat').onclick = startWechatLogin;
    $('#btn-collapse').onclick = () => setSidebarCollapsed(true);
    $('#btn-expand').onclick   = () => setSidebarCollapsed(false);

    // 服务端配置页（全局）：验证码模型 / 邮件通知
    $('#btn-server').onclick = showServerPage;
    $('#btn-server-back').onclick = backFromServer;
    $$('#server-tabs button').forEach(btn => {
        btn.onclick = () => selectServerTab(btn.dataset.serverTab);
    });
    $('#smtp-eye').onclick = toggleSMTPPassword;
    $('#btn-smtp-save').onclick = saveSMTPSettings;
    $('#btn-smtp-test').onclick = testSMTPSettings;
    $('#btn-kaptcha-save').onclick = saveKaptcha;
    $('#btn-kaptcha-reload').onclick = reloadKaptcha;
    $('#btn-kaptcha-upload').onclick = () => $('#kaptcha-upload-file').click();
    $('#kaptcha-upload-file').onchange = uploadKaptchaModel;
    $('#btn-kaptcha-test').onclick = () => $('#kaptcha-test-file').click();
    $('#kaptcha-test-file').onchange = testKaptcha;

    $('#dlg-cancel').onclick = () => $('#dlg-account').close();
    $('#acc-loginMode').onchange = syncLoginModeFields;

    // 二维码弹窗：按钮和 Esc 都必须真正取消登录，不能只把弹窗关掉
    $('#dlg-qr-cancel').onclick = cancelQROrWechat;
    $('#dlg-qr').oncancel = (e) => { e.preventDefault(); cancelQROrWechat(); };
    $('#qr-image').addEventListener('error', () => {
        if (!$('#dlg-qr').open) return;
        const st = $('#qr-status');
        if (st) st.textContent = '二维码加载失败，正在等待刷新…';
    });
    $('#acc-hedging').onchange = (e) => {
        syncHedgingFields(e.target.checked);
    };
    $('#dlg-delete').onclick = () => {
        if (dialogState.mode !== 'edit' || !dialogState.id) return;
        confirmDeleteAccount(dialogState.id);
    };

    const form = $('#form-account');
    $$('.eye-btn', form).forEach(btn => {
        btn.onclick = () => {
            const input = form.elements.namedItem(btn.dataset.target);
            if (!input || !('type' in input)) return;
            const show = input.type === 'password';
            input.type = show ? 'text' : 'password';
            btn.classList.toggle('visible', show);
            btn.title = show ? '隐藏密码' : '显示密码';
            btn.setAttribute('aria-label', btn.title);
        };
    });

    form.onsubmit = async (e) => {
        e.preventDefault();
        const fd = new FormData(e.target);
        const loginMode = (fd.get('loginMode') || 'password').trim() || 'password';
        const body = {
            name:        (fd.get('name') || '').trim(),
            baseUrl:     (fd.get('baseUrl') || '').trim(),
            username:    (fd.get('username') || '').trim(),
            password:    fd.get('password') || '',
            casPassword: fd.get('casPassword') || '',
            loginMode,
            // 兼容仍只读 casLogin 的旧代码 / 旧 accounts.json 工具
            casLogin:    loginMode === 'cas',
            userAgent:   (fd.get('userAgent') || '').trim(),
            timeout:     (fd.get('timeout') || '43s').trim() || '43s',
            // 登录/初始化任务的最长等待时间（Go duration，空值由后端回落默认 10m）
            loginTimeout: (fd.get('loginTimeout') || '10m').trim() || '10m',
            hedging:     fd.get('hedging') === 'on',
            hedgingDelay: (fd.get('hedgingDelay') || '21s').trim() || '21s',
            // 每行一个 route；空行忽略。空数组 = 使用内置默认 route。
            routes:      String(fd.get('routes') || '')
                             .split(/[\r\n,]+/)
                             .map(s => s.trim())
                             .filter(Boolean),
            existVerify: true,
        };
        const submit = $('#dlg-submit');
        const orig = submit.textContent;
        submit.disabled = true;
        submit.innerHTML = '<span class="spinner"></span> ' +
            (dialogState.mode === 'edit' ? '保存中...' : '创建中...');
        try {
            if (dialogState.mode === 'edit') {
                await API.updateAccount(dialogState.id, body);
                toast('账号已更新 ✅', 'ok');
                $('#dlg-account').close();
                await loadAccounts();
                if (String(state.currentId) === String(dialogState.id)) {
                    await selectAccount(dialogState.id);
                }
            } else {
                const created = await API.createAccount(body);
                toast('账号已创建 ✅', 'ok');
                $('#dlg-account').close();
                await loadAccounts();
                if (created && created.id) selectAccount(created.id);
            }
        } catch (err) {
            toast((dialogState.mode === 'edit' ? '更新' : '创建') + '失败: ' + err.message, 'err');
        } finally {
            submit.disabled = false;
            submit.textContent = orig;
        }
    };

    $('#btn-login').onclick = async () => {
        if (!state.currentId) return;
        const btn = $('#btn-login');
        const id = state.currentId;

        // ---- 取消分支：任务进行中再点 = 取消 ----
        if (Task.running()) {
            state.loginCancelled = true;
            await cancelCurrentTask();
            return;
        }

        // ---- 开始登录 ----
        state.loggingIn = true;
        state.loginCancelled = false;
        state.loginToken++;
        setLoginBtnBusy(btn);
        // 确保推送连接在场：登录期间靠它驱动界面，不再轮询 /status
        Task.retry();
        try {
            // 阻塞到登录结束；进度与验证码输入由推送统一驱动。
            // 刷新页面后本 promise 消失也没关系，新页面会自动接管任务。
            const r = await API.login(id);
            if (state.currentId !== id) return;
            // 登录成功不再弹绿色提示：登录态已经由按钮和徽标直接体现。
            if (r && r.status === 'cancelled') toast('已取消登录', 'warn');
        } catch (e) {
            if (state.currentId !== id) return;
            const msg = (e && e.message) ? e.message : '未知错误';
            // 超时是"登录没做完"，不是"密码/流程错了"：直接展示后端文案
            // （形如「登录超时（10 分钟）」），别再加一层"登录失败"前缀。
            // 也不看 Task.running()：超时后本地快照可能还没更新，别把提示吞掉。
            const timeout = msg.indexOf('登录超时') === 0;
            if (!state.loginCancelled && (timeout || !Task.running())) {
                toast(timeout ? msg : '登录失败: ' + msg, 'err');
            }
        } finally {
            dismissInputDialog();
            dismissQRDialog();
            if (state.currentId === id) {
                state.loggingIn = false;
                // 登录刚结束，登录态变了：作废登录期间发出的旧状态请求，
                // 否则它们落回来会把"已登录"的徽标又按回"未登录"
                invalidateStatus();
                await refreshStatus({ quiet: true }).catch(() => {});
            }
            state.loginCancelled = false;
        }
    };

    $('#btn-logout').onclick = async () => {
        if (!state.currentId) return;
        const id = state.currentId;
        try {
            await API.logout(id);
            if (state.currentId !== id) return; // 期间切了账号
            toast('已登出', 'ok');
            dismissQRDialog();
            state.courses = [];
            state.loadingKch.clear();

            // 登出已经生效，界面必须立刻反映"未登录"。
            // invalidateStatus 同时作废登出前发出的那次 /status：
            // 它带回来的是 loggedIn=true 的旧快照，落回来就会把按钮改回去。
            invalidateStatus();
            state.status = Object.assign({}, state.status || {}, {
                loggedIn: false,
                initialized: false,
                modeName: '',
                window: null,
            });
            state.modeName = '';
            state.tabs = [];
            resetToolsUI();          // 登出后旧照片/信息/Cookie 一律作废
            renderTabs();
            renderStatus();

            await refreshStatus();   // 再拉一次权威状态（这一次是新的一代，必然重发）
            renderCourses();
            const activeTab = $('.tabs button.active');
            if (activeTab && activeTab.dataset.tab === 'tools') {
                ensureToolsLocal();
            }
        } catch (e) {
            toast(e.message, 'err');
        }
    };

    $('#btn-ping').onclick = pingAccount;

    // 自动选课结果邮件开关：账号级，随账号保存，不重建运行态；
    // 服务端配置里的「自动选课结果通知」仍是总开关。
    $('#chk-auto-notify').onchange = async (e) => {
        const id = state.currentId;
        if (!id) {
            e.target.checked = !e.target.checked;
            return;
        }
        const enabled = e.target.checked;
        e.target.disabled = true;
        try {
            const r = await API.setAutoNotify(id, enabled);
            const value = !!(r && r.autoNotify);
            const acc = state.accounts.find(a => String(a.id) === String(id));
            if (acc) acc.autoNotify = value;
            if (state.current && String(state.current.id) === String(id)) {
                state.current.autoNotify = value;
            }
            if (state.currentId === id) e.target.checked = value;
            toast(value ? '已开启自动选课邮件通知' : '已关闭自动选课邮件通知', 'ok');
        } catch (err) {
            if (state.currentId === id) e.target.checked = !enabled;
            toast('切换自动选课邮件通知失败: ' + err.message, 'err');
        } finally {
            if (state.currentId === id) e.target.disabled = false;
        }
    };

    // 自动重登开关：即时生效，不重建运行态（后端会热更新当前 engine）
    $('#chk-auto-relogin').onchange = async (e) => {
        const id = state.currentId;
        if (!id) {
            e.target.checked = !e.target.checked;
            return;
        }
        const enabled = e.target.checked;
        e.target.disabled = true;
        try {
            const r = await API.setAutoRelogin(id, enabled);
            const value = !!(r && r.autoRelogin);
            const acc = state.accounts.find(a => String(a.id) === String(id));
            if (acc) acc.autoRelogin = value;
            if (state.current && String(state.current.id) === String(id)) {
                state.current.autoRelogin = value;
            }
            if (state.currentId === id) e.target.checked = value;
            toast(value ? '已开启自动重登' : '已关闭自动重登', 'ok');
        } catch (err) {
            if (state.currentId === id) e.target.checked = !enabled;
            toast('切换自动重登失败: ' + err.message, 'err');
        } finally {
            if (state.currentId === id) e.target.disabled = false;
        }
    };

    $('#btn-init').onclick = () => {
        if (state.initializing) {
            if (!state.initForce) cancelInit();
            return;
        }
        doInit(false);
    };
    $('#btn-reinit').onclick = () => {
        if (state.initializing) {
            if (state.initForce) cancelInit();
            return;
        }
        doInit(true);
    };

    // 任务面板上的取消按钮：类型由快照决定，登录/初始化都能取消
    $('#task-cancel').onclick = () => { cancelCurrentTask(); };

    // 点开日志时滚到底部
    $('#task-log-wrap').addEventListener('toggle', (e) => {
        if (e.target.open) {
            const ol = $('#task-events');
            if (ol) ol.scrollTop = ol.scrollHeight;
        }
    });

    // ---------- 课程按钮 ----------
    $('#btn-fetch-courses').onclick = fetchCourseList;

    $('#btn-refresh-courses').onclick = async () => {
        if (!state.status || !state.status.initialized) {
            toast('请先在「初始化」标签页完成初始化', 'warn');
            switchTab('init');
            return;
        }
        const btn = $('#btn-refresh-courses');
        const orig = btn.textContent;
        btn.disabled = true;
        btn.innerHTML = '<span class="spinner dark"></span> 刷新中...';
        try {
            state.courses = (await API.courses(state.currentId, true)) || [];
            state.loadingKch.clear();
            renderCourses();
        } catch (e) {
            if (!isUnauthorized(e)) toast('强制刷新失败: ' + e.message, 'err');
        } finally {
            btn.disabled = false;
            btn.textContent = orig;
        }
    };

    $('#btn-clear-courses').onclick = async () => {
        if (!state.currentId) return;
        if (!confirm('确认清空课程缓存？')) return;
        try {
            await API.clearCourses(state.currentId);
            state.courses = [];
            state.loadingKch.clear();
            renderCourses();
            toast('已清空课程缓存', 'ok');
        } catch (e) {
            toast('清空失败: ' + e.message, 'err');
        }
    };

    // ---------- 其他工具 ----------
    $('#btn-tools-info').onclick = () => { fetchToolsInfo(); };
    $('#btn-tools-info-copy').onclick = () => {
        copyToolsJSON(state.tools.info, '个人信息 JSON 已复制');
    };
    $('#btn-tools-photo').onclick = () => { fetchToolsPhoto(); };
    $('#btn-tools-photo-download').onclick = downloadToolsPhoto;
    $('#btn-tools-cookies').onclick = () => { fetchToolsCookies(false); };
    $('#btn-tools-cookies-copy').onclick = async () => {
        const header = state.tools.cookies && state.tools.cookies.header;
        if (!header) return;
        const ok = await copyText(header);
        toast(ok ? 'Cookie 请求头已复制' : '复制失败，请手动选择文本',
            ok ? 'ok' : 'warn');
    };
    $('#btn-tools-debug').onclick = () => { fetchToolsDebug(false); };
    $('#btn-tools-debug-copy').onclick = () => {
        copyToolsJSON(state.tools.debug, '诊断 JSON 已复制');
    };

    $('#btn-fetch-chosen').onclick = fetchChosen;
    $('#btn-fetch-score').onclick = fetchScore;
    $('#btn-save-wish').onclick = saveWish;
    $('#btn-auto-start').onclick = startAutoChoose;
    $('#btn-auto-cancel').onclick = cancelAutoChoose;
    $('#auto-limit-mode').onchange = syncAutoLimitMode;
    syncAutoLimitMode();

    $$('.tabs button').forEach(btn => {
        btn.onclick = () => switchTab(btn.dataset.tab);
    });
}

/* =========================================================================
 * 启动
 * ========================================================================= */

async function init() {
    initSidebar();
    bindEvents();
    $('#chosen-xnm').value = String(new Date().getFullYear());
    await loadAccounts();

    // 有选中项时默认选第一个
    if (!state.currentId && state.accounts.length) {
        await selectAccount(state.accounts[0].id);
    }
    // 启动状态轮询：若后端有进行中的任务（例如刚刷新过页面），会立刻被接管
    Task.ensure();
    await refreshStatus();
}

init().catch(err => {
    console.error(err);
    toast('初始化失败: ' + (err && err.message ? err.message : err), 'err');
});