/* ==========================================================================
 * fan-video-tr · 前端应用逻辑
 * 无框架、无构建：原生 ES2018 + Fetch + DOM
 * 数据源全部来自 /api（options / capabilities 为表单的唯一字典）
 * ========================================================================== */
(function () {
  'use strict';

  /* ==================== 基础工具 ==================== */

  var $ = function (sel, root) { return (root || document).querySelector(sel); };
  var $$ = function (sel, root) {
    return Array.prototype.slice.call((root || document).querySelectorAll(sel));
  };
  var icon = function (id) { return '<svg><use href="#' + id + '"></use></svg>'; };
  var esc = function (s) {
    return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  };
  var clamp = function (v, lo, hi) { return Math.min(hi, Math.max(lo, v)); };

  /** 字节数 → 人类可读 */
  function fmtSize(bytes) {
    var b = Number(bytes) || 0;
    if (b <= 0) return '–';
    var units = ['B', 'KB', 'MB', 'GB', 'TB'];
    var i = 0;
    while (b >= 1024 && i < units.length - 1) { b /= 1024; i++; }
    return (b < 10 && i > 1 ? b.toFixed(1) : Math.round(b)) + ' ' + units[i];
  }

  /** 秒 → 时:分:秒 */
  function fmtTime(sec) {
    var s = Math.max(0, Math.floor(Number(sec) || 0));
    var h = Math.floor(s / 3600);
    var m = Math.floor((s % 3600) / 60);
    var r = s % 60;
    var pad = function (n) { return n < 10 ? '0' + n : String(n); };
    return h > 0 ? h + ':' + pad(m) + ':' + pad(r) : m + ':' + pad(r);
  }

  /** 秒 → 宽松时长（用于“预计耗时”这类粗略展示） */
  function fmtDurationCN(sec) {
    var s = Math.max(0, Math.round(Number(sec) || 0));
    if (s < 60) return s + ' 秒';
    var m = Math.floor(s / 60);
    if (m < 60) return m + ' 分' + (s % 60 ? ' ' + (s % 60) + ' 秒' : '');
    var h = Math.floor(m / 60);
    return h + ' 小时' + (m % 60 ? ' ' + (m % 60) + ' 分' : '');
  }

  /** bps → "4.0 Mbps" */
  function fmtBitrate(bps) {
    var b = Number(bps) || 0;
    if (b <= 0) return '–';
    if (b >= 1e6) return (b / 1e6).toFixed(b % 1e6 === 0 ? 0 : 1) + ' Mbps';
    return Math.round(b / 1e3) + ' kbps';
  }

  /** 解析 "4M" / "1500k" / "2000000" → bps */
  function parseBitrate(str) {
    var s = String(str || '').trim().toLowerCase().replace(/\s+/g, '');
    if (!s) return 0;
    var m = /^([\d.]+)([kmg])?$/.exec(s);
    if (!m) return 0;
    var n = parseFloat(m[1]);
    if (!isFinite(n) || n <= 0) return 0;
    var mult = m[2] === 'g' ? 1e9 : m[2] === 'm' ? 1e6 : m[2] === 'k' ? 1e3 : 1;
    return Math.round(n * mult);
  }

  /** 时间戳 → 相对时间 */
  function fmtAgo(iso) {
    if (!iso) return '';
    var t = new Date(iso).getTime();
    if (!isFinite(t)) return '';
    var d = Math.floor((Date.now() - t) / 1000);
    if (d < 5) return '刚刚';
    if (d < 60) return d + ' 秒前';
    if (d < 3600) return Math.floor(d / 60) + ' 分钟前';
    if (d < 86400) return Math.floor(d / 3600) + ' 小时前';
    return Math.floor(d / 86400) + ' 天前';
  }

  /* ==================== 应用状态 ==================== */

  var S = {
    opts: null,          // /api/options
    caps: null,          // /api/capabilities
    profiles: [],        // 模板列表
    activeProfile: '',   // 当前生效的模板名（手动改参数后清空）
    dir: '',             // 当前目录
    items: [],           // 当前目录条目
    filter: '',          // 名称筛选
    selected: {},        // 勾选的视频路径集合
    current: null,       // 正在预览的视频路径
    info: null,          // 当前视频的 MediaInfo
    infoCache: {},       // path → MediaInfo（预估算用）
    saveTarget: 'default',
    suffixTouched: false,
    tasks: {},           // id → 任务快照
    polling: false,
    starting: false
  };

  /* ==================== 网络 ==================== */

  function api(path, init) {
    var opt = init || {};
    var headers = opt.headers || {};
    if (opt.body && !headers['Content-Type']) headers['Content-Type'] = 'application/json';
    return fetch(path, { method: opt.method || 'GET', headers: headers, body: opt.body })
      .then(function (res) {
        return res.text().then(function (text) {
          var data = null;
          if (text) {
            try { data = JSON.parse(text); } catch (e) { data = { error: text.slice(0, 300) }; }
          }
          if (!res.ok) {
            throw new Error((data && data.error) || ('请求失败（HTTP ' + res.status + '）'));
          }
          return data || {};
        });
      });
  }

  /* ==================== 提示条 ==================== */

  function toast(msg, kind, timeout) {
    var box = $('#toasts');
    var el = document.createElement('div');
    el.className = 'toast ' + (kind || 'info');
    var ico = kind === 'ok' ? 'i-check' : kind === 'warn' ? 'i-warn' : kind === 'error' ? 'i-x' : 'i-info';
    el.innerHTML = icon(ico) + '<span>' + esc(msg) + '</span>';
    box.appendChild(el);
    setTimeout(function () {
      el.classList.add('out');
      setTimeout(function () { if (el.parentNode) el.parentNode.removeChild(el); }, 220);
    }, timeout || (kind === 'error' ? 6000 : 3200));
  }

  /* ==================== 主题 ==================== */

  var THEMES = ['auto', 'dark', 'light'];
  var THEME_LABEL = { auto: '跟随系统', dark: '深色', light: '浅色' };

  function applyTheme(t) {
    if (THEMES.indexOf(t) < 0) t = 'auto';
    document.documentElement.setAttribute('data-theme', t);
    try { localStorage.setItem('fvtr-theme', t); } catch (e) { /* 忽略 */ }
    var btn = $('#btn-theme');
    if (btn) btn.title = '主题：' + THEME_LABEL[t] + '（T 切换）';
  }

  function currentTheme() {
    try { return localStorage.getItem('fvtr-theme') || 'auto'; } catch (e) { return 'auto'; }
  }

  function cycleTheme() {
    var t = currentTheme();
    applyTheme(THEMES[(THEMES.indexOf(t) + 1) % THEMES.length]);
  }

  /* ==================== 弹窗 ==================== */

  function openModal(id) {
    var m = $('#' + id);
    if (!m) return;
    m.hidden = false;
    var focusable = m.querySelector('input, button:not([data-close])');
    if (focusable) setTimeout(function () { focusable.focus(); }, 30);
  }

  function closeModal(m) {
    (typeof m === 'string' ? $('#' + m) : m).hidden = true;
  }

  function anyModalOpen() {
    return $$('.modal:not([hidden])').length > 0;
  }

  /* ==================== 顶栏：版本与能力 ==================== */

  function loadVersion() {
    return api('/api/version').then(function (d) {
      var v = d.version || '';
      $('#version-badge').textContent = v ? 'v' + v : '–';
      document.title = 'fan-video-tr' + (v ? ' · v' + v : '') + ' · 视频转码';
    }).catch(function () {
      $('#version-badge').textContent = '离线';
    });
  }

  function renderCaps(caps) {
    var pillFF = $('#pill-ffmpeg');
    if (caps.ffmpeg) {
      var short = String(caps.ffmpeg).replace(/^ffmpeg version\s*/i, '').split(/\s+/)[0];
      pillFF.textContent = 'FFmpeg ' + short;
      pillFF.title = caps.ffmpeg + '\n' + (caps.ffmpeg_path || '');
    } else {
      pillFF.textContent = 'FFmpeg 不可用';
      pillFF.className = 'pill pill-danger';
    }

    var pillAcc = $('#pill-accel');
    var usable = (caps.hw_accel || []).filter(function (a) { return a.available; });
    if (usable.length) {
      pillAcc.textContent = usable.map(function (a) { return a.id.toUpperCase(); }).join(' + ');
      pillAcc.className = 'pill pill-ok';
      pillAcc.title = usable.map(function (a) {
        return a.name + (a.device ? '（' + a.device + '）' : '') + '：' + (a.codecs || []).join('/');
      }).join('\n');
    } else {
      pillAcc.textContent = '仅软件编码';
      pillAcc.className = 'pill pill-warn';
      pillAcc.title = '未检测到可用的硬件编码器，将使用软件编码转码';
    }
  }

  /* ==================== 选项字典 → 表单 ==================== */

  function codecOption(id) {
    var list = (S.opts && S.opts.codecs) || [];
    for (var i = 0; i < list.length; i++) if (list[i].id === id) return list[i];
    return null;
  }

  function containerOption(id) {
    var list = (S.opts && S.opts.containers) || [];
    for (var i = 0; i < list.length; i++) if (list[i].id === id) return list[i];
    return null;
  }

  function resolutionOption(id) {
    var list = (S.opts && S.opts.resolutions) || [];
    for (var i = 0; i < list.length; i++) if (list[i].id === id) return list[i];
    return null;
  }

  function accelOption(id) {
    var list = (S.opts && S.opts.accels) || [];
    for (var i = 0; i < list.length; i++) if (list[i].id === id) return list[i];
    return null;
  }

  function optionById(list, id) {
    list = list || [];
    for (var i = 0; i < list.length; i++) if (list[i].id === id) return list[i];
    return null;
  }

  /** 依当前编码 + 加速解析实际编码器名（与服务端 EncoderName 逻辑一致） */
  function resolveEncoder() {
    var family = codecOption(S.params.codec);
    if (!family) return '–';
    var accel = S.params.accel;
    if (accel !== 'none' && accel !== 'auto') {
      var hw = (family.encoders || []).filter(function (e) { return e.accel === accel; })[0];
      if (hw) return hw.name;
    }
    if (accel === 'auto') {
      var order = ['nvenc', 'qsv', 'vaapi'];
      for (var i = 0; i < order.length; i++) {
        var a = accelOption(order[i]);
        if (!a || !a.available) continue;
        var hit = (family.encoders || []).filter(function (e) { return e.accel === order[i]; })[0];
        if (hit) return hit.name;
      }
    }
    var sw = (family.encoders || []).filter(function (e) { return e.accel === 'none'; })[0];
    return sw ? sw.name : '–';
  }

  /** 依当前参数解析实际生效的加速方式（auto 已展开为具体值或 none） */
  function resolveAccel() {
    var accel = S.params.accel;
    if (accel !== 'auto') return accel;
    var family = codecOption(S.params.codec);
    if (family) {
      var order = ['nvenc', 'qsv', 'vaapi'];
      for (var i = 0; i < order.length; i++) {
        var a = accelOption(order[i]);
        if (!a || !a.available) continue;
        if ((a.codecs || []).indexOf(S.params.codec) < 0) continue;
        if ((family.encoders || []).some(function (e) { return e.accel === order[i]; })) return order[i];
      }
    }
    return 'none';
  }

  /** 分段控件渲染 */
  function renderSegmented(box, items, value, onPick) {
    box.innerHTML = '';
    items.forEach(function (it) {
      var b = document.createElement('button');
      b.type = 'button';
      b.setAttribute('role', 'radio');
      b.dataset.value = it.value;
      b.setAttribute('aria-checked', it.value === value ? 'true' : 'false');
      b.innerHTML = '<span>' + esc(it.label) + '</span>' + (it.sub ? '<small>' + esc(it.sub) + '</small>' : '');
      if (it.title) b.title = it.title;
      if (it.disabled) {
        b.disabled = true;
        if (it.disabledReason) b.title = it.disabledReason;
      }
      b.addEventListener('click', function () {
        if (b.disabled) return;
        $$('button', box).forEach(function (x) { x.setAttribute('aria-checked', x === b ? 'true' : 'false'); });
        onPick(it.value);
      });
      box.appendChild(b);
    });
  }

  function fillSelect(sel, items, value) {
    sel.innerHTML = '';
    items.forEach(function (it) {
      var o = document.createElement('option');
      o.value = it.value;
      o.textContent = it.label;
      if (it.disabled) {
        o.disabled = true;
        o.textContent += it.disabledReason ? '（' + it.disabledReason + '）' : '（不可用）';
      }
      if (it.title) o.title = it.title;
      if (it.value === value) o.selected = true;
      sel.appendChild(o);
    });
    sel.value = value;
  }

  function buildForm() {
    var o = S.opts;

    /* 编码格式 */
    renderSegmented($('#f-codec'), o.codecs.map(function (c) {
      return {
        value: c.id,
        label: c.name,
        title: c.note + (c.available ? '' : '（本机未检测到可用编码器）'),
        disabled: !c.available,
        disabledReason: '本机不可用'
      };
    }), S.params.codec, function (v) { S.params.codec = v; onCodecChange(); });

    /* 硬件加速 */
    var accels = o.accels.filter(function (a) { return a.id !== 'none'; });
    fillSelect($('#f-accel'), accels.map(function (a) {
      return {
        value: a.id,
        label: a.name + (a.available ? '' : '（不可用）'),
        title: a.available ? ('编码器：' + (a.encoders || []).join(', ')) : (a.reason || '不可用'),
        disabled: !a.available,
        disabledReason: ''
      };
    }).concat([{ value: 'none', label: '纯软件编码', title: '不使用任何硬件加速' }]),
      S.params.accel);

    /* 帧率 / 像素格式 / 音频 / 分辨率 / 容器 / 码率 */
    fillSelect($('#f-res'), o.resolutions.map(function (r) {
      return { value: r.id, label: r.name, title: r.width ? r.width + '×' + r.height + (r.label ? ' · ' + r.label : '') : r.label };
    }), S.params.resolution);

    fillSelect($('#f-fps'), o.fps.map(function (f) { return { value: f.id, label: f.name, title: f.label }; }), S.params.fps);

    fillSelect($('#f-pix'), (o.pixel_formats || []).map(function (p) {
      return { value: p, label: p };
    }), S.params.pixel_format);

    fillSelect($('#f-audio'), o.audio_codecs.map(function (a) { return { value: a.id, label: a.name, title: a.label }; }), S.params.audio_codec);

    fillSelect($('#f-abitrate'), (o.audio_bitrates || []).map(function (b) { return { value: b, label: b }; }), S.params.audio_bitrate);

    renderSegmented($('#f-channels'), o.audio_channels.map(function (c) {
      return { value: c.id, label: c.name, sub: c.label };
    }), S.params.audio_channels, function (v) { S.params.audio_channels = v; onFormChange(); });

    refreshContainerOptions();
    refreshBitratePresets();
  }

  /* ==================== 表单状态 ==================== */

  S.params = null;

  function defaultParams() {
    return defaultsFrom(null);
  }

  /** 用服务端下发的默认值覆盖本地缺省值 */
  function defaultsFrom(d) {
    d = d || {};
    return {
      codec: d.codec || 'h264',
      accel: d.accel || 'auto',
      rate_control: d.rate_control || 'crf',
      crf: d.crf || 23,
      video_bitrate: d.video_bitrate || '4M',
      maxrate: d.maxrate || '',
      bufsize: d.bufsize || '',
      preset: d.preset || 'veryfast',
      two_pass: !!d.two_pass,
      resolution: d.resolution || 'keep',
      custom_size: d.custom_size || '',
      fps: d.fps || 'keep',
      pixel_format: d.pixel_format || 'yuv420p',
      audio_codec: d.audio_codec || 'aac',
      audio_bitrate: d.audio_bitrate || '192k',
      audio_channels: d.audio_channels || 'keep',
      container: d.container || 'mp4',
      fast_start: d.fast_start !== false,
      start: 0,
      end: 0
    };
  }

  /** 服务端 resolveSuffix 的前端镜像：空后缀按参数推导，再退回默认 _转码 */
  function autoSuffix() {
    var p = S.params;
    var tags = [];
    if (p.resolution === 'custom') {
      tags.push(String(p.custom_size || '').replace(/[xX*×\s　]/g, ''));
    } else {
      var r = resolutionOption(p.resolution);
      if (r && r.id !== 'keep' && r.id !== 'custom') tags.push(r.id);
    }
    if (p.codec === 'h265') tags.push('H265');
    else if (p.codec === 'vp9') tags.push('VP9');
    else if (p.codec === 'av1') tags.push('AV1');
    if (!tags.length) return (S.opts && S.opts.name_suffix) || '_转码';
    return '_' + tags.join('');
  }

  /* ==================== 参数联动 ==================== */

  function onCodecChange() {
    var family = codecOption(S.params.codec);
    // 编码不支持当前加速方式时退回自动
    if (S.params.accel !== 'none' && S.params.accel !== 'auto') {
      var a = accelOption(S.params.accel);
      if (!a || !a.available || (a.codecs || []).indexOf(S.params.codec) < 0) S.params.accel = 'auto';
    }
    // 容器不支持该编码时切到推荐容器
    var ct = containerOption(S.params.container);
    if (!ct || (ct.video_codecs || []).indexOf(S.params.codec) < 0) {
      S.params.container = (family && family.container) || 'mp4';
    }
    // 音频不兼容时退回推荐音频
    ct = containerOption(S.params.container);
    if (ct && (ct.audio_codecs || []).indexOf(S.params.audio_codec) < 0) {
      S.params.audio_codec = (family && family.audio_codec) || 'aac';
    }
    refreshAccelOptions();
    refreshContainerOptions();
    applyParamsToForm();
    onFormChange(true);
  }

  function onAccelChange() {
    refreshAccelOptions();
    applyParamsToForm();
    onFormChange(true);
  }

  function onContainerChange() {
    var ct = containerOption(S.params.container);
    // 容器不支持当前视频编码时，切到该容器支持的编码（优先保留原编码族）
    if (ct && (ct.video_codecs || []).indexOf(S.params.codec) < 0) {
      var pool = ct.video_codecs || [];
      S.params.codec = pool.indexOf('h264') >= 0 ? 'h264'
        : pool.indexOf('vp9') >= 0 ? 'vp9'
          : pool.indexOf('av1') >= 0 ? 'av1' : (pool[0] || S.params.codec);
      refreshAccelOptions();
    }
    if (ct && (ct.audio_codecs || []).indexOf(S.params.audio_codec) < 0) {
      S.params.audio_codec = (ct.audio_codecs || []).indexOf('aac') >= 0 ? 'aac'
        : (ct.audio_codecs || []).indexOf('opus') >= 0 ? 'opus'
          : (ct.audio_codecs || []).indexOf('none') >= 0 ? 'none' : 'copy';
    }
    if (ct && !ct.fast_start) S.params.fast_start = false;
    applyParamsToForm();
    onFormChange(true);
  }

  function onFormChange(fromOption) {
    if (!S.params || !S.opts) return;
    if (!fromOption) S.activeProfile = '';
    renderProfileChips();
    updateVisibility();
    updateNotes();
    updateEstimate();
    updateSaveHint();
  }

  /** 把 S.params 写回 DOM */
  function applyParamsToForm() {
    var p = S.params;
    if (!p) return;

    $$('#f-codec button').forEach(function (b) {
      b.setAttribute('aria-checked', b.dataset.value === p.codec ? 'true' : 'false');
    });
    $$('#f-rate button').forEach(function (b) {
      b.setAttribute('aria-checked', b.dataset.value === p.rate_control ? 'true' : 'false');
    });
    $$('#f-channels button').forEach(function (b) {
      b.setAttribute('aria-checked', b.dataset.value === p.audio_channels ? 'true' : 'false');
    });

    $('#f-accel').value = p.accel;
    $('#f-preset').innerHTML = '';
    $('#f-res').value = p.resolution;
    $('#f-fps').value = p.fps;
    $('#f-pix').value = p.pixel_format;
    $('#f-audio').value = p.audio_codec;
    $('#f-abitrate').value = p.audio_bitrate;
    $('#f-crf').value = p.crf;
    $('#f-crf-out').textContent = p.crf;
    $('#f-bitrate').value = p.video_bitrate;
    $('#f-twopass').checked = !!p.two_pass;
    $('#f-faststart').checked = !!p.fast_start;
    $('#f-trim').checked = p.end > 0;
    $('#f-trim-start').value = p.start || 0;
    $('#f-trim-end').value = p.end || 0;

    if (!$('#f-suffix').dataset.touched) {
      $('#f-suffix').placeholder = autoSuffix();
    }
  }

  /** 刷新硬件加速下拉（按当前编码族过滤） */
  function refreshAccelOptions() {
    var sel = $('#f-accel');
    var prev = S.params.accel;
    var accels = S.opts.accels.filter(function (a) { return a.id !== 'none'; });
    fillSelect(sel, accels.map(function (a) {
      var support = !a.available ? false : ((a.codecs || []).indexOf(S.params.codec) >= 0);
      var reason = !a.available ? (a.reason || '不可用')
        : (support ? '' : '不支持 ' + codecName(S.params.codec));
      return {
        value: a.id,
        label: a.name + (support ? '' : '（' + reason + '）'),
        title: a.available ? ('设备：' + (a.device || '内置') + '；编码器：' + (a.encoders || []).join(', ')) : reason,
        disabled: !support,
        disabledReason: ''
      };
    }).concat([{ value: 'none', label: '纯软件编码', title: '不使用任何硬件加速' }]), prev);
    sel.value = prev;
  }

  function codecName(id) {
    var c = codecOption(id);
    return c ? c.name : id;
  }

  /** 刷新容器分段控件（不支持当前编码的容器禁用） */
  function refreshContainerOptions() {
    renderSegmented($('#f-container'), S.opts.containers.map(function (ct) {
      var ok = (ct.video_codecs || []).indexOf(S.params.codec) >= 0;
      return {
        value: ct.id,
        label: ct.name,
        title: ct.note + (ok ? '' : ' · 不支持 ' + codecName(S.params.codec)),
        disabled: !ok,
        disabledReason: '不支持 ' + codecName(S.params.codec)
      };
    }), S.params.container, function (v) { S.params.container = v; onContainerChange(); });
  }

  /** 刷新编码预设下拉：硬件路径使用各厂商自有档位 */
  function refreshPresetOptions() {
    if (!S.opts) return;
    var sel = $('#f-preset');
    var accel = resolveAccel();
    var family = codecOption(S.params.codec);

    if (accel === 'none' || accel === 'auto') {
      var presets = S.opts.presets || [];
      if (S.params.codec === 'av1') {
        fillSelect(sel, [
          { value: 'faster', label: 'faster（快）' },
          { value: 'fast', label: 'fast' },
          { value: 'medium', label: 'medium（均衡）' },
          { value: 'slow', label: 'slow（慢·压缩率高）' }
        ], S.params.preset);
      } else {
        fillSelect(sel, presets.map(function (p) { return { value: p, label: p }; }), S.params.preset);
      }
      sel.disabled = false;
      return;
    }
    if (accel === 'nvenc') {
      fillSelect(sel, [
        { value: 'ultrafast', label: 'P1 最快' },
        { value: 'veryfast', label: 'P2 很快' },
        { value: 'faster', label: 'P3 快' },
        { value: 'fast', label: 'P4 较快' },
        { value: 'medium', label: 'P5 均衡' },
        { value: 'slow', label: 'P6 较慢（质量更好）' },
        { value: 'slower', label: 'P7 最慢（质量最好）' }
      ], S.params.preset);
      sel.disabled = false;
      return;
    }
    // QSV / VAAPI 不接受 x264 档位
    var single = { value: S.params.preset, label: '由硬件自动决定' };
    if (accel === 'qsv' && family) {
      fillSelect(sel, [
        { value: 'medium', label: '默认（关闭前瞻，质量优先）' },
        { value: 'fast', label: '快速（降低约束，兼容优先）' }
      ], S.params.preset);
      return;
    }
    fillSelect(sel, [single], S.params.preset);
    sel.disabled = true;
  }

  /** 目标码率快捷值按输出分辨率分组 */
  function bitrateGroup() {
    if (!S.opts) return '1080p';
    var h = targetSize(S.params, currentInfo()).h;
    if (h >= 2000) return '2160p';
    if (h >= 1000) return '1080p';
    return '720p';
  }

  function refreshBitratePresets() {
    var group = bitrateGroup();
    var list = (S.opts.bitrates || {})[group] || [];
    fillSelect($('#f-bitrate-preset'), [{ value: '', label: group }].concat(
      list.map(function (b) { return { value: b, label: b }; })
    ), '');
  }

  /** 显隐与禁用联动 */
  function updateVisibility() {
    var p = S.params;
    var accel = resolveAccel();

    $('#wrap-crf').hidden = p.rate_control !== 'crf';
    $('#wrap-bitrate').hidden = p.rate_control !== 'bitrate';

    // 两遍编码仅软件路径生效
    var swOnly = accel === 'none';
    var twopass = $('#wrap-twopass');
    twopass.classList.toggle('is-disabled', accel !== 'none');
    $('#f-twopass').disabled = accel !== 'none';
    if (accel !== 'none') {
      twopass.querySelector('.switch-text').innerHTML = '两遍编码<small>当前为硬件编码，硬件路径不支持两遍，已自动关闭</small>';
      p.two_pass = false;
      $('#f-twopass').checked = false;
    } else {
      twopass.querySelector('.switch-text').innerHTML = '两遍编码<small>同码率下画质更佳、体积更小，耗时约翻倍</small>';
    }

    // 自定义分辨率
    $('#f-cussize').hidden = p.resolution !== 'custom';

    // 音频
    var aOff = p.audio_codec === 'copy' || p.audio_codec === 'none';
    $('#f-abitrate').disabled = aOff;
    $$('#f-channels button').forEach(function (b) { b.disabled = aOff; });

    // faststart 仅 MP4 / MOV
    var ct = containerOption(p.container);
    var fsOK = !!(ct && ct.fast_start);
    $('#f-faststart').checked = !!p.fast_start && fsOK;
    $('#f-faststart').disabled = !fsOK;
    p.fast_start = $('#f-faststart').checked;

    // 转码区间
    $('#wrap-trim').hidden = !S.current;
    $('#trim-row').hidden = !p.end;
  }

  /** 卡片角标说明 */
  function updateNotes() {
    var p = S.params;
    var info = currentInfo();
    var enc = resolveEncoder();
    var accel = resolveAccel();

    var note = enc;
    if (accel !== 'none') note += ' · ' + accel.toUpperCase();
    $('#enc-note').textContent = note;

    var size = targetSize(p, info);
    $('#res-note').textContent = p.resolution === 'keep' && info && info.video
      ? (info.video.width + '×' + info.video.height + ' 源')
      : (size.w + '×' + size.h);

    var a = info && info.audio;
    $('#audio-note').textContent = a
      ? (String(a.codec_name || '').toUpperCase() + ' · ' + (a.channel_layout || (a.channels + 'ch')) +
        (a.bit_rate ? ' · ' + fmtBitrate(a.bit_rate) : ''))
      : (info ? '无音轨' : '');

    refreshPresetOptions();
    refreshBitratePresets();
  }

  /* ==================== 输出估算 ==================== */

  function currentInfo() { return S.info; }

  /** 目标分辨率：与 targetResolution 一致（不放大小于等于源的档位） */
  function targetSize(p, info) {
    var sw = info && info.video ? info.video.width : 0;
    var sh = info && info.video ? info.video.height : 0;
    if (p.resolution === 'keep') return { w: sw, h: sh };
    var w = 0, h = 0;
    if (p.resolution === 'custom') {
      var m = /(\d+)\s*[xX*×]\s*(\d+)/.exec(String(p.custom_size || ''));
      if (m) { w = parseInt(m[1], 10); h = parseInt(m[2], 10); }
    } else {
      var r = resolutionOption(p.resolution);
      if (r) { w = r.width; h = r.height; }
    }
    if (!w || !h) return { w: sw, h: sh };
    if (sw > 0 && sh > 0) {
      if (sw <= w && sh <= h) return { w: sw, h: sh };
      if (sh * w > h * sw) w = Math.floor(h * sw / sh);
      else h = Math.floor(w * sh / sw);
    }
    if (w % 2) w--;
    if (h % 2) h--;
    if (w < 2 || h < 2) return { w: sw, h: sh };
    return { w: w, h: h };
  }

  /** 参与估算的时长（区间优先） */
  function estimateDuration(info) {
    if (!info || !(info.duration > 0)) return 0;
    var p = S.params;
    if (p.end > 0 && p.end > p.start) return p.end - p.start;
    return info.duration;
  }

  /** 粗估输出体积（与服务端 EstimateSize 同算法） */
  function estimateSize(p, info) {
    var dur = estimateDuration(info);
    if (!info || dur <= 0 || !(info.size > 0)) return 0;
    var out = targetSize(p, info);
    var sw = info.video ? info.video.width : 0;
    var sh = info.video ? info.video.height : 0;
    var pixelFactor = (sw > 0 && sh > 0 && out.w > 0 && out.h > 0) ? (out.w * out.h) / (sw * sh) : 1;

    var fpsFactor = 1;
    if (p.fps !== 'keep' && info.video && info.video.frame_rate > 0) {
      var fv = parseFloat(p.fps);
      if (fv > 0) fpsFactor = clamp(fv / info.video.frame_rate, 0.2, 4);
    }

    var bps;
    if (p.rate_control === 'bitrate') {
      bps = parseBitrate(p.video_bitrate);
    } else {
      var srcBps = info.bit_rate || 0;
      if (info.audio && info.audio.bit_rate > 0) srcBps -= info.audio.bit_rate;
      if (srcBps <= 0) srcBps = suggestBitrate(out.w, out.h, 30);
      var gain = p.codec === 'h265' ? 0.65 : p.codec === 'vp9' ? 0.70 : p.codec === 'av1' ? 0.55 : 1;
      var family = codecOption(p.codec);
      var maxQ = family && family.max_quality ? family.max_quality : 51;
      var crf = p.crf > 0 ? p.crf : 23;
      bps = srcBps * pixelFactor * fpsFactor * gain * (0.45 + 0.75 * (crf / maxQ));
    }
    var aBps = p.audio_codec === 'none' ? 0 : parseBitrate(p.audio_bitrate);
    return Math.max(0, (bps + aBps) * dur / 8);
  }

  function suggestBitrate(w, h, fps) {
    var px = Math.max(1, w * h);
    var bpp = 0.09;
    if (px >= 3840 * 2160) bpp = 0.055;
    else if (px >= 2560 * 1440) bpp = 0.07;
    else if (px >= 1920 * 1080) bpp = 0.09;
    else if (px >= 1280 * 720) bpp = 0.13;
    var f = fps > 0 ? fps : 30;
    if (f > 30) bpp *= f / 30;
    if (f < 25) bpp *= f / 25;
    return Math.round(px * f * bpp);
  }

  function updateEstimate() {
    var info = currentInfo();
    var p = S.params;
    var out = targetSize(p, info);
    var sizeEl = $('#est-size');
    var ratioEl = $('#est-ratio');
    var specEl = $('#est-spec');
    var encEl = $('#est-encoder');

    encEl.textContent = resolveEncoder();
    specEl.textContent = (out.w > 0 && out.h > 0) ? (out.w + '×' + out.h) : '–';

    if (!info) {
      sizeEl.textContent = '–';
      sizeEl.className = '';
      ratioEl.textContent = '–';
      ratioEl.className = '';
      return;
    }
    var outBytes = estimateSize(p, info);
    sizeEl.textContent = fmtSize(outBytes);
    sizeEl.className = '';
    if (info.size > 0) {
      var r = outBytes / info.size;
      ratioEl.textContent = (r * 100).toFixed(0) + '%';
      ratioEl.className = r < 0.85 ? 'good' : 'warn';
    } else {
      ratioEl.textContent = '–';
      ratioEl.className = '';
    }
  }

  /* ==================== 模板（Profiles） ==================== */

  function loadProfiles() {
    return api('/api/profiles').then(function (d) {
      S.profiles = d.profiles || [];
      renderProfileChips();
      if (!S.profiles.length && S.params) applyParams(defaultParams());
    }).catch(function (e) {
      toast('模板加载失败：' + e.message, 'error');
    });
  }

  function renderProfileChips() {
    var box = $('#profile-chips');
    box.innerHTML = '';
    S.profiles.forEach(function (p) {
      var b = document.createElement('button');
      b.type = 'button';
      b.className = 'chip-btn' + (p.builtin ? '' : ' custom') + (S.activeProfile === p.name ? ' on' : '');
      b.innerHTML = '<b>' + esc(p.name) + '</b><small>' + esc(p.description || summaryOf(p.params)) + '</small>';
      b.title = p.description || '';
      b.addEventListener('click', function () { applyProfile(p); });
      box.appendChild(b);
    });
  }

  function summaryOf(p) {
    var parts = [codecName(p.codec)];
    if (p.rate_control === 'bitrate') parts.push(p.video_bitrate);
    else parts.push('CRF' + p.crf);
    var r = resolutionOption(p.resolution);
    if (p.resolution === 'custom') parts.push(p.custom_size);
    else if (r && r.id !== 'keep') parts.push(r.id);
    return parts.join(' · ');
  }

  function applyProfile(p) {
    var base = defaultParams();
    Object.keys(base).forEach(function (k) {
      if (p.params && p.params[k] !== undefined) base[k] = p.params[k];
    });
    S.params = base;
    S.activeProfile = p.name;
    $('#f-suffix').dataset.touched = '';
    S.suffixTouched = false;
    onCodecChange();
    renderProfileChips();
    toast('已应用模板：' + p.name, 'ok', 1800);
  }

  function openProfileManager() {
    var list = $('#profile-list');
    list.innerHTML = '';
    if (!S.profiles.length) {
      list.innerHTML = '<p class="hint">暂无模板</p>';
    }
    S.profiles.forEach(function (p) {
      var row = document.createElement('div');
      row.className = 'profile-row';
      row.innerHTML =
        '<div class="pr-text">' +
          '<b>' + esc(p.name) + (p.builtin ? ' <span class="tag">内置</span>' : '') + '</b>' +
          '<small>' + esc(p.description || summaryOf(p.params)) + '</small>' +
          '<span class="tag' + (S.activeProfile === p.name ? ' tag-brand' : '') + '">' + esc(summaryOf(p.params)) + '</span>' +
        '</div>' +
        '<div class="profile-actions">' +
          '<button class="btn btn-ghost sm" data-act="apply">应用</button>' +
          '<button class="btn btn-ghost sm" data-act="over"' + (p.builtin ? ' disabled title="内置模板不可覆盖"' : '') + '>覆盖</button>' +
          '<button class="btn btn-ghost sm" data-act="del"' + (p.builtin ? ' disabled title="内置模板不可删除"' : '') + '>' + icon('i-trash') + '</button>' +
        '</div>';
      row.addEventListener('click', function (ev) {
        var act = ev.target.closest('button');
        if (!act || act.disabled) return;
        var a = act.dataset.act;
        if (a === 'apply') { applyProfile(p); closeModal('modal-profiles'); }
        else if (a === 'over') { S.pendingProfile = p; openProfileForm(p.name); }
        else if (a === 'del') { deleteProfile(p); }
      });
      list.appendChild(row);
    });
    openModal('modal-profiles');
  }

  function deleteProfile(p) {
    if (!confirm('确认删除模板「' + p.name + '」？')) return;
    api('/api/profiles/' + encodeURIComponent(p.name), { method: 'DELETE' })
      .then(function (d) {
        S.profiles = d.profiles || [];
        if (S.activeProfile === p.name) S.activeProfile = '';
        renderProfileChips();
        openProfileManager();
        toast('模板已删除', 'ok');
      })
      .catch(function (e) { toast('删除失败：' + e.message, 'error'); });
  }

  function openProfileForm(name) {
    S.pendingProfile = S.pendingProfile || null;
    var pre = S.pendingProfile;
    $('#modal-pf-title').textContent = pre ? '覆盖模板：' + pre.name : '新建模板';
    $('#pf-name').value = pre ? pre.name : '';
    $('#pf-desc').value = pre ? (pre.description || '') : '';
    $('#pf-summary').textContent = describeParams();
    closeModal('modal-profiles');
    openModal('modal-profile-form');
  }

  function describeParams() {
    var p = S.params;
    return [
      '编码 ' + resolveEncoder() + (resolveAccel() !== 'none' ? '（' + resolveAccel().toUpperCase() + '）' : '（软件）'),
      p.rate_control === 'crf' ? 'CRF ' + p.crf : '码率 ' + p.video_bitrate,
      (p.two_pass ? '两遍 · ' : '') + '预设 ' + p.preset,
      '分辨率 ' + (p.resolution === 'keep' ? '原始' : p.resolution === 'custom' ? p.custom_size : p.resolution),
      '帧率 ' + p.fps + ' · 像素 ' + p.pixel_format,
      '音频 ' + (p.audio_codec === 'none' ? '无' : p.audio_codec === 'copy' ? '复制' : p.audio_codec + ' ' + p.audio_bitrate + ' / ' + p.audio_channels),
      '容器 ' + (containerOption(p.container) || {}).name
    ].join('\n');
  }

  function saveProfile() {
    var name = $('#pf-name').value.trim();
    var desc = $('#pf-desc').value.trim();
    if (!name) { toast('请填写模板名称', 'warn'); return; }
    var body = JSON.stringify({ name: name, description: desc, params: S.params });
    api('/api/profiles', { method: 'POST', body: body })
      .then(function (d) {
        S.profiles = d.profiles || [];
        S.activeProfile = name;
        S.pendingProfile = null;
        renderProfileChips();
        closeModal('modal-profile-form');
        toast('模板已保存：' + name, 'ok');
      })
      .catch(function (e) { toast('保存失败：' + e.message, 'error'); });
  }

  /* ==================== 媒体库 ==================== */

  function loadDir(dir, keepSelection) {
    var q = dir === undefined ? S.dir : dir;
    var url = '/api/media/dir' + (q ? '?path=' + encodeURIComponent(q) : '');
    return api(url).then(function (d) {
      S.dir = d.current || '';
      S.items = d.items || [];
      S.home = d.path || '';
      $('#lib-count').textContent = d.total;
      if (!keepSelection) { /* 保留勾选，便于跨目录批量 */ }
      renderBreadcrumb();
      renderList();
    }).catch(function (e) {
      S.items = [];
      renderList();
      toast('目录读取失败：' + e.message, 'error');
    });
  }

  function renderBreadcrumb() {
    var box = $('#breadcrumb');
    var cur = S.dir || S.home || '';
    var root = S.home || cur;
    var html = '<span>' + esc(root) + '</span>';
    if (cur && cur !== root) html += '<span> / </span><b>' + esc(cur) + '</b>';
    box.innerHTML = html;
  }

  function renderList() {
    var box = $('#file-list');
    var items = S.items.filter(function (it) {
      if (!S.filter) return true;
      return String(it.name).toLowerCase().indexOf(S.filter) >= 0;
    });

    if (!items.length) {
      box.innerHTML = '<div class="empty">' + (S.items.length ? '没有匹配的文件' : '此目录下没有视频文件') + '</div>';
      return;
    }

    var upRow = S.dir && S.dir !== S.home;
    var html = '';
    if (upRow) {
      html += rowHTML({ name: '返回上级目录', is_dir: true, is_up: true, path: parentOf(S.dir) }, false, false);
    }
    items.forEach(function (it) {
      var isSel = !it.is_dir && !!S.selected[it.path];
      html += rowHTML(it, S.current === it.path, isSel);
    });
    box.innerHTML = html;

    $$('.lib-row', box).forEach(function (r) {
      var path = r.dataset.path;
      var isDir = r.dataset.dir === '1';
      r.addEventListener('click', function (ev) {
        var onCheck = ev.target.closest('.lib-check');
        if (onCheck || isDir) {
          if (isDir) loadDir(r.dataset.path);
          else toggleSelect(path);
          return;
        }
        preview(path);
      });
    });
    updateSelCount();
  }

  function rowHTML(it, active, checked) {
    var cls = 'lib-row' + (it.is_dir ? ' dir' : '') + (active ? ' active' : '') +
      (checked ? ' checked' : '') + (it.is_up ? ' is-up' : '');
    var sub = it.is_dir ? '文件夹' : (fmtSize(it.size) + (it.modified ? ' · ' + fmtAgo(new Date(it.modified * 1000).toISOString()) : ''));
    var ico = it.is_dir ? 'i-folder' : 'i-file';
    return '<div class="' + cls + '" data-path="' + esc(it.path) + '" data-dir="' + (it.is_dir ? '1' : '0') + '" role="option">' +
      '<span class="lib-check">' + icon('i-check') + '</span>' +
      '<span class="lib-icon">' + icon(ico) + '</span>' +
      '<span class="lib-meta">' +
        '<span class="lib-name">' + esc(it.name) + '</span>' +
        '<span class="lib-sub">' + esc(sub) + '</span>' +
      '</span></div>';
  }

  function parentOf(dir) {
    if (!dir) return '';
    var i = dir.replace(/\/+$/, '').lastIndexOf('/');
    if (i <= 0) return dir === '/' ? '' : '/';
    return dir.replace(/\/+$/, '').slice(0, i);
  }

  function toggleSelect(path) {
    if (S.selected[path]) delete S.selected[path];
    else S.selected[path] = true;
    renderList();
    updateQueueButton();
  }

  function updateSelCount() {
    var n = Object.keys(S.selected).length;
    var el = $('#sel-count');
    el.textContent = n ? ('已选 ' + n + ' 个文件') : '未选择';
    el.classList.toggle('has', n > 0);
    updateQueueButton();
    updateStartButton();
  }

  function updateQueueButton() {
    var n = Object.keys(S.selected).length;
    $('#btn-queue').disabled = n === 0;
  }

  /* ==================== 预览与探测 ==================== */

  function preview(path) {
    if (S.current === path) return;
    S.current = path;
    S.info = null;
    renderList();
    $('#stage-busy').hidden = false;
    $('#stage-busy-text').textContent = '分析中…';
    $('#srcinfo').hidden = true;

    api('/api/media/info?path=' + encodeURIComponent(path))
      .then(function (d) {
        if (S.current !== path) return;
        S.info = d.info || null;
        S.infoCache[path] = S.info;
        $('#stage-busy').hidden = true;
        var player = $('#player');
        player.src = d.stream;
        player.hidden = false;
        $('#stage-empty').hidden = true;
        $('#player-bar').hidden = false;
        renderSource();
        updateNotes();
        updateVisibility();
        updateEstimate();
        updateSaveHint();
        applyParamsToForm();
        updateStartButton();
      })
      .catch(function (e) {
        if (S.current !== path) return;
        $('#stage-busy').hidden = true;
        toast('无法读取视频信息：' + e.message, 'error');
      });
  }

  function renderSource() {
    var info = S.info;
    if (!info) { $('#srcinfo').hidden = true; return; }
    $('#srcinfo').hidden = false;
    $('#src-name').textContent = info.name || S.current;
    $('#src-name').title = S.current;

    var v = info.video;
    var chips = [];
    chips.push(chip('分辨率', v ? (v.width + '×' + v.height) : '无视频流', 'chip-brand'));
    if (v && v.frame_rate > 0) chips.push(chip('帧率', v.frame_rate.toFixed(2).replace(/\.00$/, '') + ' fps'));
    if (v) chips.push(chip('视频编码', String(v.codec_name || '').toUpperCase(), v.codec_name === 'hevc' ? 'chip-ok' : ''));
    if (v && v.pix_fmt) chips.push(chip('像素', v.pix_fmt));
    chips.push(chip('时长', fmtDurationCN(info.duration)));
    chips.push(chip('体积', fmtSize(info.size)));
    if (info.bit_rate) chips.push(chip('总码率', fmtBitrate(info.bit_rate)));
    if (info.audio) chips.push(chip('音频', String(info.audio.codec_name || '').toUpperCase() + ' ' + (info.audio.channel_layout || info.audio.channels + 'ch')));
    else chips.push(chip('音频', '无音轨', 'chip-warn'));

    $('#src-chips').innerHTML = chips.join('');
  }

  function chip(label, value, cls) {
    return '<span class="chip ' + (cls || '') + '">' + esc(label) + ' <b>' + esc(value) + '</b></span>';
  }

  /* ==================== 播放器 ==================== */

  function initPlayer() {
    var player = $('#player');
    var seek = $('#seek');
    var dragging = false;

    function paint() {
      var dur = player.duration || 0;
      var cur = player.currentTime || 0;
      var pct = dur > 0 ? (cur / dur) * 100 : 0;
      $('#seek-fill').style.width = pct + '%';
      $('#seek-knob').style.left = pct + '%';
      $('#time-now').textContent = fmtTime(cur);
      $('#time-total').textContent = fmtTime(dur);
      if (player.buffered.length && dur > 0) {
        var end = player.buffered.end(player.buffered.length - 1);
        $('#seek-buffer').style.width = ((end / dur) * 100) + '%';
      } else {
        $('#seek-buffer').style.width = '0';
      }
      paintPlayIcon();
    }

    function paintPlayIcon() {
      var b = $('#btn-play');
      var playing = !player.paused && !player.ended;
      b.innerHTML = icon(playing ? 'i-pause' : 'i-play');
      b.title = (playing ? '暂停' : '播放') + '（空格）';
    }

    player.addEventListener('timeupdate', paint);
    player.addEventListener('progress', paint);
    player.addEventListener('loadedmetadata', paint);
    player.addEventListener('play', paintPlayIcon);
    player.addEventListener('pause', paintPlayIcon);
    player.addEventListener('ended', paintPlayIcon);
    player.addEventListener('error', function () {
      if (S.current) toast('浏览器无法直接播放该格式，可下载后用本地播放器查看', 'warn', 5000);
    });

    $('#btn-play').addEventListener('click', function () {
      if (!player.src) return;
      if (player.paused) player.play(); else player.pause();
    });

    function seekTo(clientX) {
      var r = seek.getBoundingClientRect();
      var ratio = clamp((clientX - r.left) / r.width, 0, 1);
      if (player.duration > 0) player.currentTime = ratio * player.duration;
    }
    seek.addEventListener('mousedown', function (e) { dragging = true; seekTo(e.clientX); });
    seek.addEventListener('mousemove', function (e) { if (dragging) seekTo(e.clientX); });
    window.addEventListener('mouseup', function () { dragging = false; });
    seek.addEventListener('click', seekTo);

    $('#btn-mute').addEventListener('click', toggleMute);
    $('#volume').addEventListener('input', function () {
      player.volume = Number($('#volume').value) / 100;
      player.muted = false;
      paintVolume();
    });
    $('#btn-full').addEventListener('click', function () {
      var stage = $('#stage');
      if (document.fullscreenElement) document.exitFullscreen();
      else if (stage.requestFullscreen) stage.requestFullscreen();
    });

    function paintVolume() {
      var muted = player.muted || player.volume === 0;
      $('#btn-mute').innerHTML = icon(muted ? 'i-mute' : 'i-volume');
    }
    player.addEventListener('volumechange', paintVolume);
    paintVolume();

    function toggleMute() {
      player.muted = !player.muted;
      paintVolume();
    }

    $('#btn-trim-here').addEventListener('click', function () {
      var info = S.info;
      var cur = Math.floor((player.currentTime || 0) * 10) / 10;
      var dur = info && info.duration > 0 ? info.duration : 0;
      var end = dur > 0 ? Math.round((dur - 1) * 10) / 10 : cur + 60;
      if (end <= cur) end = cur + 10;
      S.params.start = cur;
      S.params.end = end;
      $('#f-trim').checked = true;
      applyParamsToForm();
      onFormChange(true);
      toast('已按播放位置设置区间：' + fmtTime(cur) + ' → ' + fmtTime(end), 'ok', 3000);
    });

    paint();
  }

  /* ==================== 保存位置 ==================== */

  function updateSaveHint() {
    var p = S.params;
    var el = $('#save-where');
    var o = S.opts || {};
    var dir = S.saveTarget === 'source'
      ? (S.current ? parentOf(S.current) : '与原视频同目录')
      : (o.output_dir || (o.data_dir ? o.data_dir + '/output' : '数据目录'));
    el.textContent = '保存到：' + dir;
    el.title = dir;

    var ct = containerOption(p.container);
    var suffix = S.suffixTouched && $('#f-suffix').value.trim()
      ? $('#f-suffix').value.trim()
      : autoSuffix();
    var base = S.current ? S.current.replace(/\.[^./\\]+$/, '') : '输出文件名';
    var ext = ct ? ct.ext : '.mp4';
    el.title = base + suffix + ext;
  }

  function openSaveModal() {
    var ref = S.current || Object.keys(S.selected)[0];
    if (!ref) { toast('请先选择要转码的视频', 'warn'); return; }
    var suffix = S.suffixTouched && $('#f-suffix').value.trim() ? $('#f-suffix').value.trim() : autoSuffix();
    api('/api/tasks/output-targets?path=' + encodeURIComponent(ref) +
        '&suffix=' + encodeURIComponent(suffix) +
        '&container=' + encodeURIComponent(S.params.container))
      .then(function (d) {
        var box = $('#save-opts');
        var targets = d.targets || [];
        box.innerHTML = '';
        targets.forEach(function (t) {
          var on = t.id === S.saveTarget && t.available;
          var div = document.createElement('div');
          div.className = 'save-opt' + (on ? ' on' : '') + (t.available ? '' : ' is-off');
          div.dataset.id = t.id;
          div.innerHTML =
            '<span class="save-radio"></span>' +
            '<span class="save-text"><b>' + esc(t.label) + '</b>' +
              (t.available
                ? '<code>' + esc(t.file || t.dir) + '</code>'
                : '<span class="why">' + esc(t.reason || '不可用') + '</span>') +
            '</span>';
          if (t.available) {
            div.addEventListener('click', function () {
              $$('.save-opt', box).forEach(function (x) { x.classList.toggle('on', x === div); });
            });
          }
          box.appendChild(div);
        });
        openModal('modal-save');
      })
      .catch(function (e) { toast('无法获取保存位置：' + e.message, 'error'); });
  }

  /* ==================== 提交任务 ==================== */

  function submit() {
    var paths = Object.keys(S.selected);
    if (!paths.length && S.current) paths = [S.current];
    if (!paths.length) { toast('请先选择要转码的视频', 'warn'); return; }
    if (S.starting) return;

    var suffix = $('#f-suffix').value.trim();
    S.starting = true;
    var btn = $('#btn-start');
    btn.disabled = true;
    var label = btn.querySelector('span');
    var old = label.textContent;
    label.textContent = '提交中…';

    var body = JSON.stringify({
      paths: paths,
      params: S.params,
      suffix: suffix,
      save_target: S.saveTarget
    });

    api('/api/tasks', { method: 'POST', body: body })
      .then(function (d) {
        S.selected = {};
        renderList();
        updateEstimate();
        toast('已加入 ' + (d.total || 0) + ' 个转码任务', 'ok');
        pollTasks(true);
      })
      .catch(function (e) {
        toast('提交失败：' + e.message, 'error', 8000);
      })
      .then(function () {
        S.starting = false;
        label.textContent = old;
        updateStartButton();
      });
  }

  function updateStartButton() {
    var has = Object.keys(S.selected).length > 0 || !!S.current;
    var btn = $('#btn-start');
    btn.disabled = !has || S.starting;
    var label = btn.querySelector('span');
    if (!S.starting) {
      var n = Object.keys(S.selected).length;
      label.textContent = n > 1 ? ('转码 ' + n + ' 个文件') : '开始转码';
    }
  }

  /* ==================== 队列 ==================== */

  function pollTasks(force) {
    return api('/api/tasks').then(function (d) {
      S.tasks = {};
      (d.tasks || []).forEach(function (t) { S.tasks[t.id] = t; });
      renderTasks(d.stats || {});
      if (force) scrollQueue();
    }).catch(function (e) {
      if (force) toast('队列刷新失败：' + e.message, 'error');
    });
  }

  function scrollQueue() {
    var list = $('#task-list');
    if (list.firstChild) list.scrollTop = 0;
  }

  function renderTasks(stats) {
    var box = $('#task-list');
    var list = Object.keys(S.tasks).map(function (k) { return S.tasks[k]; })
      .sort(function (a, b) { return new Date(b.created_at) - new Date(a.created_at); });

    $('#queue-count').textContent = stats.total != null ? stats.total : list.length;
    $$('#queue-stats .qs').forEach(function (el) {
      var k = el.dataset.k;
      var b = el.querySelector('b');
      if (b) b.textContent = stats[k] != null ? stats[k] : 0;
    });
    $('#queue-empty').hidden = list.length > 0;

    if (!box._bound) {
      box._bound = true;
      box.addEventListener('click', function (ev) {
        var act = ev.target.closest('button[data-act]');
        if (!act || !box.contains(act)) return;
        var card = act.closest('.task');
        if (!card) return;
        var id = card.dataset.id;
        var a = act.dataset.act;
        if (a === 'cancel') cancelTask(id);
        else if (a === 'download') downloadTask(id);
        else if (a === 'output') removeOutput(id);
        else if (a === 'remove') removeTask(id);
      });
    }

    var existing = {};
    Array.prototype.forEach.call(box.children, function (el) {
      if (el.dataset && el.dataset.id) existing[el.dataset.id] = el;
    });

    var order = [];
    list.forEach(function (t) {
      var el = existing[t.id];
      var html = taskHTML(t);
      if (el) {
        delete existing[t.id];
        if (el._html === html && el.className === 'task is-' + t.status) {
          order.push(el);
          return;
        }
        if (el.className !== 'task is-' + t.status) el.className = 'task is-' + t.status;
        el.innerHTML = html;
        el._html = html;
      } else {
        box.insertAdjacentHTML('beforeend', taskCard(t));
        el = box.lastElementChild;
        el._html = html;
      }
      order.push(el);
    });

    Object.keys(existing).forEach(function (id) {
      if (existing[id].parentNode === box) existing[id].remove();
    });

    var cur = Array.prototype.filter.call(box.children, function (el) {
      return el.dataset && el.dataset.id;
    });
    var reordered = cur.length !== order.length;
    for (var i = 0; !reordered && i < order.length; i++) {
      if (cur[i] !== order[i]) reordered = true;
    }
    if (reordered) {
      var frag = document.createDocumentFragment();
      order.forEach(function (el) { frag.appendChild(el); });
      box.appendChild(frag);
    }
  }

  var STATUS_META = {
    queued: { label: '排队中', ico: 'i-list' },
    running: { label: '转码中', ico: 'i-bolt' },
    done: { label: '已完成', ico: 'i-check' },
    failed: { label: '失败', ico: 'i-x' },
    cancelled: { label: '已取消', ico: 'i-x' }
  };

  function taskHTML(t) {
    var meta = STATUS_META[t.status] || STATUS_META.queued;
    var pct = clamp((t.progress || 0) * 100, 0, 100);
    var queued = t.status === 'queued';

    var stats = [];
    if (t.status === 'running') {
      if (t.speed) stats.push(span('i-bolt', t.speed));
      if (t.fps > 0) stats.push(span('i-film', t.fps.toFixed(0) + ' fps'));
      stats.push(span('i-list', pct.toFixed(0) + '%'));
      if (t.encoded_seconds > 0) stats.push(span('i-info', '已编 ' + fmtDurationCN(t.encoded_seconds)));
    } else if (t.status === 'done') {
      stats.push(span('i-save', fmtSize(t.output_size)));
      stats.push(span('i-info', '耗时 ' + fmtDurationCN(t.elapsed_seconds)));
      if (t.output) stats.push(span('i-download', t.output_name || t.output));
    } else {
      stats.push(span('i-info', fmtAgo(t.created_at)));
    }

    var actions = [];
    if (queued || t.status === 'running') {
      actions.push('<button class="btn btn-ghost sm" data-act="cancel">取消</button>');
    }
    if (t.status === 'done') {
      actions.push('<button class="btn btn-primary sm" data-act="download">' + icon('i-download') + '下载</button>');
      actions.push('<button class="btn btn-ghost sm" data-act="output">' + icon('i-trash') + '删除输出</button>');
    } else if (t.status === 'failed' || t.status === 'cancelled') {
      actions.push('<button class="btn btn-ghost sm" data-act="remove">移除</button>');
    }

    return '<div class="task-top">' +
      '<span class="task-name" title="' + esc(t.path) + '">' + esc(t.name || t.path) + '</span>' +
      '<span class="task-badge">' + icon(meta.ico) + meta.label + '</span>' +
    '</div>' +
    '<div class="task-meta">' + esc(t.summary || '') + '</div>' +
    '<div class="task-tags">' +
      (t.encoder ? '<span class="tag' + (t.accel && t.accel !== 'none' ? ' tag-brand' : '') + '">' + esc(t.encoder) + '</span>' : '') +
      (t.resolution_label ? '<span class="tag">' + esc(t.resolution_label) + '</span>' : '') +
      (t.estimate_size > 0 && t.status !== 'done' ? '<span class="tag">预估 ' + fmtSize(t.estimate_size) + '</span>' : '') +
    '</div>' +
    '<div class="bar' + (queued ? ' is-indeterminate' : '') + '"><div class="bar-fill" style="width:' + (queued ? 0 : pct) + '%"></div></div>' +
    '<div class="task-stats">' + stats.join('') + '</div>' +
    (t.error ? '<div class="task-error">' + esc(t.error) + '</div>' : '') +
    (actions.length ? '<div class="task-actions">' + actions.join('') + '</div>' : '');
  }

  function taskCard(t) {
    return '<div class="task is-' + t.status + '" data-id="' + esc(t.id) + '">' + taskHTML(t) + '</div>';
  }

  function span(ico, text) {
    var tip = text.length > 28 ? ' title="' + esc(text) + '"' : '';
    return '<span' + tip + '>' + icon(ico) + esc(text) + '</span>';
  }

  function cancelTask(id) {
    api('/api/tasks/' + encodeURIComponent(id), { method: 'DELETE' })
      .then(function () { toast('已请求取消任务', 'info', 2000); pollTasks(); })
      .catch(function (e) { toast('取消失败：' + e.message, 'error'); });
  }

  function downloadTask(id) {
    var a = document.createElement('a');
    a.href = '/api/tasks/' + encodeURIComponent(id) + '/download';
    a.download = '';
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
  }

  function removeOutput(id) {
    var t = S.tasks[id];
    if (!confirm('确认删除输出文件？\n' + (t && t.output ? t.output : ''))) return;
    api('/api/tasks/' + encodeURIComponent(id) + '/output', { method: 'DELETE' })
      .then(function () { toast('输出文件已删除', 'ok'); pollTasks(); })
      .catch(function (e) { toast('删除失败：' + e.message, 'error'); });
  }

  function removeTask(id) {
    var t = S.tasks[id];
    if (t && t.output && !confirm('移除记录的同时会删除输出文件：\n' + t.output + '\n\n确定继续？')) return;
    api('/api/tasks/' + encodeURIComponent(id) + '/output', { method: 'DELETE' })
      .then(function () { toast('已从队列移除', 'ok'); pollTasks(); })
      .catch(function (e) { toast('移除失败：' + e.message, 'error'); });
  }

  function cleanQueue() {
    var stats = { running: 0, queued: 0 };
    Object.keys(S.tasks).forEach(function (k) {
      var s = S.tasks[k].status;
      if (s === 'running') stats.running++;
      if (s === 'queued') stats.queued++;
    });
    var cancel = stats.running + stats.queued > 0;
    var msg = cancel
      ? ('队列中有 ' + (stats.running + stats.queued) + ' 个未完成任务，清理会一并取消它们，确定继续？')
      : '确认清空已完成 / 失败的任务记录？（不会删除已生成的视频文件）';
    if (!confirm(msg)) return;
    api('/api/tasks', { method: 'DELETE', body: JSON.stringify({ cancel_running: cancel }) })
      .then(function (d) {
        toast('已清理 ' + (d.removed || 0) + ' 条任务记录', 'ok');
        pollTasks();
      })
      .catch(function (e) { toast('清理失败：' + e.message, 'error'); });
  }

  /* ==================== 事件绑定 ==================== */

  function bindEvents() {
    // 媒体库
    $('#btn-refresh').addEventListener('click', function () { loadDir(S.dir); toast('已刷新', 'ok', 1500); });
    $('#lib-filter').addEventListener('input', function () {
      S.filter = this.value.trim().toLowerCase();
      $('#btn-clear-filter').hidden = !S.filter;
      renderList();
    });
    $('#btn-clear-filter').addEventListener('click', function () {
      $('#lib-filter').value = '';
      S.filter = '';
      this.hidden = true;
      renderList();
    });
    $('#btn-select-all').addEventListener('click', function () {
      S.items.forEach(function (it) { if (!it.is_dir) S.selected[it.path] = true; });
      renderList();
    });
    $('#btn-select-none').addEventListener('click', function () {
      S.selected = {};
      renderList();
    });
    $('#btn-queue').addEventListener('click', submit);

    // 编码格式
    $('#f-accel').addEventListener('change', function () { S.params.accel = this.value; onAccelChange(); });
    $('#f-preset').addEventListener('change', function () { S.params.preset = this.value; onFormChange(); });

    // 质量控制
    $('#f-rate').addEventListener('click', function (ev) {
      var b = ev.target.closest('button');
      if (!b || b.disabled) return;
      S.params.rate_control = b.dataset.value;
      onFormChange(true);
    });
    $('#f-crf').addEventListener('input', function () {
      S.params.crf = Number(this.value);
      $('#f-crf-out').textContent = this.value;
      onFormChange();
    });
    $('#f-bitrate').addEventListener('input', function () {
      S.params.video_bitrate = this.value.trim();
      var ok = parseBitrate(this.value) > 0;
      this.classList.toggle('is-invalid', !ok);
      onFormChange();
    });
    $('#f-bitrate-preset').addEventListener('change', function () {
      if (!this.value) return;
      S.params.video_bitrate = this.value;
      $('#f-bitrate').value = this.value;
      this.value = '';
      onFormChange();
    });
    $('#f-twopass').addEventListener('change', function () {
      S.params.two_pass = this.checked;
      onFormChange();
    });

    // 画面
    $('#f-res').addEventListener('change', function () {
      S.params.resolution = this.value;
      onFormChange(true);
    });
    $('#f-cussize').addEventListener('input', function () {
      S.params.custom_size = this.value.trim();
      var ok = /^\d+\s*[xX*×]\s*\d+$/.test(this.value);
      this.classList.toggle('is-invalid', !ok);
      onFormChange();
    });
    $('#f-fps').addEventListener('change', function () { S.params.fps = this.value; onFormChange(true); });
    $('#f-pix').addEventListener('change', function () { S.params.pixel_format = this.value; onFormChange(true); });

    // 区间
    $('#f-trim').addEventListener('change', function () {
      var on = this.checked;
      var info = S.info;
      if (on) {
        var start = Number($('#f-trim-start').value) || 0;
        var end = Number($('#f-trim-end').value) || 0;
        if (!(end > start)) {
          // 默认取当前播放位置到结尾前 60 秒，方便快速试转
          var cur = $('#player').currentTime || 0;
          start = Math.floor(cur * 10) / 10;
          end = info && info.duration > 0 ? Math.max(start + 1, info.duration - 1) : start + 60;
          $('#f-trim-start').value = start;
          $('#f-trim-end').value = Math.round(end * 10) / 10;
        }
        S.params.start = start;
        S.params.end = end;
      } else {
        S.params.start = 0;
        S.params.end = 0;
      }
      onFormChange(true);
    });
    $('#f-trim-start').addEventListener('input', function () { S.params.start = Number(this.value) || 0; onFormChange(); });
    $('#f-trim-end').addEventListener('input', function () { S.params.end = Number(this.value) || 0; onFormChange(); });

    // 音频
    $('#f-audio').addEventListener('change', function () { S.params.audio_codec = this.value; onFormChange(true); });
    $('#f-abitrate').addEventListener('change', function () { S.params.audio_bitrate = this.value; onFormChange(true); });

    // 输出
    $('#f-suffix').addEventListener('input', function () {
      this.dataset.touched = '1';
      S.suffixTouched = true;
      updateSaveHint();
    });
    $('#btn-suffix-reset').addEventListener('click', function () {
      $('#f-suffix').value = '';
      $('#f-suffix').dataset.touched = '';
      S.suffixTouched = false;
      $('#f-suffix').placeholder = autoSuffix();
      updateSaveHint();
      toast('后缀已恢复自动生成', 'ok', 1800);
    });
    $('#f-faststart').addEventListener('change', function () { S.params.fast_start = this.checked; onFormChange(); });
    $('#btn-save-where').addEventListener('click', openSaveModal);
    $('#save-confirm').addEventListener('click', function () {
      var on = $('#save-opts .save-opt.on');
      if (on) {
        S.saveTarget = on.dataset.id;
        updateSaveHint();
        toast('保存位置：' + $('.save-text b', on).textContent, 'ok', 2200);
      }
      closeModal('modal-save');
    });

    // 模板
    $('#btn-manage-profiles').addEventListener('click', openProfileManager);
    $('#btn-profile-add').addEventListener('click', function () { S.pendingProfile = null; openProfileForm(null); });
    $('#pf-save').addEventListener('click', saveProfile);
    $('#btn-profiles-reset').addEventListener('click', function () {
      if (!confirm('恢复出厂模板会清除全部自定义模板，确定继续？')) return;
      api('/api/profiles', { method: 'DELETE' })
        .then(function (d) {
          S.profiles = d.profiles || [];
          S.activeProfile = '';
          renderProfileChips();
          openProfileManager();
          toast('已恢复出厂模板', 'ok');
        })
        .catch(function (e) { toast('操作失败：' + e.message, 'error'); });
    });

    // 队列 / 顶栏
    $('#btn-clean').addEventListener('click', cleanQueue);
    $('#btn-start').addEventListener('click', submit);
    $('#btn-help').addEventListener('click', function () { openModal('modal-help'); });
    $('#btn-theme').addEventListener('click', cycleTheme);

    // 弹窗关闭
    $$('.modal').forEach(function (m) {
      m.addEventListener('click', function (ev) {
        if (ev.target === m || ev.target.closest('[data-close]')) closeModal(m);
      });
    });

    // 快捷键
    document.addEventListener('keydown', onKeydown);

    // 目录树键盘导航
    $('#file-list').addEventListener('keydown', function (ev) {
      if (ev.key !== 'ArrowDown' && ev.key !== 'ArrowUp') return;
      var rows = $$('.lib-row', this);
      if (!rows.length) return;
      var idx = rows.indexOf(document.activeElement.closest('.lib-row'));
      var next = ev.key === 'ArrowDown' ? Math.min(rows.length - 1, idx + 1) : Math.max(0, idx - 1);
      if (idx < 0) next = 0;
      rows[next].focus();
      rows[next].scrollIntoView({ block: 'nearest' });
      ev.preventDefault();
    });
  }

  function onKeydown(ev) {
    if (ev.key === 'Escape') {
      var m = $$('.modal:not([hidden])').pop();
      if (m) { closeModal(m); return; }
    }
    var tag = (ev.target.tagName || '').toLowerCase();
    if (tag === 'input' || tag === 'select' || tag === 'textarea' || ev.target.isContentEditable) return;
    if (ev.ctrlKey || ev.metaKey || ev.altKey) return;
    if (anyModalOpen() && ev.key !== '?') return;

    var player = $('#player');
    switch (ev.key) {
      case ' ':
        if (S.current) { ev.preventDefault(); if (player.paused) player.play(); else player.pause(); }
        break;
      case 'ArrowLeft': if (S.current) { ev.preventDefault(); player.currentTime = Math.max(0, player.currentTime - 5); } break;
      case 'ArrowRight': if (S.current) { ev.preventDefault(); player.currentTime = Math.min(player.duration || 0, player.currentTime + 5); } break;
      case 'ArrowUp': if (S.current) { ev.preventDefault(); player.volume = clamp(player.volume + 0.05, 0, 1); } break;
      case 'ArrowDown': if (S.current) { ev.preventDefault(); player.volume = clamp(player.volume - 0.05, 0, 1); } break;
      case 'm': case 'M': if (S.current) { ev.preventDefault(); player.muted = !player.muted; } break;
      case 'f': case 'F':
        if (S.current) {
          ev.preventDefault();
          var stage = $('#stage');
          if (document.fullscreenElement) document.exitFullscreen();
          else if (stage.requestFullscreen) stage.requestFullscreen();
        }
        break;
      case 'Enter':
        if (!$('#btn-start').disabled) { ev.preventDefault(); submit(); }
        break;
      case 'r': case 'R': ev.preventDefault(); loadDir(S.dir); break;
      case 't': case 'T': ev.preventDefault(); cycleTheme(); break;
      case '?': ev.preventDefault(); openModal('modal-help'); break;
    }
  }

  /* ==================== 轮询 ==================== */

  function startPolling() {
    setInterval(function () {
      if (document.hidden) return;
      if (S.polling) return;
      S.polling = true;
      pollTasks().then(function () { S.polling = false; });
    }, 2500);
  }

  /* ==================== 启动 ==================== */

  function boot() {
    applyTheme(currentTheme());
    S.params = defaultParams();

    bindEvents();
    initPlayer();
    updateStartButton();

    loadVersion();
    loadDir('');
    pollTasks(true);
    startPolling();

    // 表单结构依赖 /api/options 的选项字典，必须先取回再渲染
    Promise.all([api('/api/options'), api('/api/capabilities').catch(function () { return null; })])
      .then(function (res) {
        S.opts = res[0];
        S.caps = res[1];
        S.params = defaultsFrom(S.opts.defaults);
        if (S.caps) renderCaps(S.caps);
        buildForm();
        onCodecChange();
        loadProfiles();
      })
      .catch(function (e) {
        $('#pill-ffmpeg').textContent = '服务不可用';
        $('#pill-ffmpeg').className = 'pill pill-danger';
        $('#pill-ffmpeg').title = e.message;
        toast('无法读取服务端配置：' + e.message, 'error', 8000);
      });

    // 首次渲染完成后淡出启动遮罩
    setTimeout(function () {
      var s = $('#splash');
      if (!s) return;
      s.classList.add('auto-exit');
      setTimeout(function () { s.hidden = true; }, 2000);
    }, 240);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }
})();
