package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"cpa-pool-manager/internal/config"
	"cpa-pool-manager/internal/engine"
	"cpa-pool-manager/internal/types"
)

const loginHTML = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>号池管理器登录</title>
  <style>
    :root {
      color-scheme: dark;
      --bg: #0b1020;
      --panel: #121a30;
      --muted: #8ea0c9;
      --text: #edf2ff;
      --accent: #7aa2ff;
      --border: #263252;
      --bad: #ff7b72;
      --ok: #3ddc97;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      min-height: 100vh;
      display: grid;
      place-items: center;
      padding: 24px;
      font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      background: var(--bg);
      color: var(--text);
    }
    .panel {
      width: min(460px, 100%);
      background: var(--panel);
      border: 1px solid var(--border);
      border-radius: 20px;
      padding: 24px;
      box-shadow: 0 18px 60px rgba(0,0,0,0.35);
    }
    h1 { margin: 0 0 10px; font-size: 28px; }
    p { margin: 0 0 16px; color: var(--muted); line-height: 1.5; }
    label { display: block; margin-bottom: 8px; font-size: 14px; color: var(--muted); }
    input {
      width: 100%;
      padding: 12px 14px;
      border-radius: 12px;
      border: 1px solid var(--border);
      background: rgba(255,255,255,0.04);
      color: var(--text);
      outline: none;
      font-size: 14px;
    }
    input:focus { border-color: var(--accent); }
    button {
      width: 100%;
      margin-top: 14px;
      border: 0;
      border-radius: 12px;
      padding: 12px 14px;
      background: var(--accent);
      color: white;
      font-weight: 700;
      cursor: pointer;
    }
    button:disabled { opacity: 0.7; cursor: wait; }
    .error {
      margin-top: 12px;
      color: var(--bad);
      font-size: 13px;
      min-height: 18px;
    }
    .ok {
      color: var(--ok);
    }
    .hint {
      margin-top: 14px;
      font-size: 12px;
      color: var(--muted);
    }
  </style>
</head>
<body>
  <div class="panel">
    <h1>号池管理器</h1>
    <p>请输入与 CPA 共用的管理密钥后进入控制台。浏览器不会在 URL 中暴露密钥，后续访问将通过服务端会话 Cookie 完成。</p>
    <form id="loginForm">
      <label for="keyInput">管理密钥</label>
      <input id="keyInput" type="password" autocomplete="current-password" placeholder="请输入 CPA 管理密钥" />
      <button id="loginBtn" type="submit">进入控制台</button>
      <div id="loginError" class="error"></div>
    </form>
    <div class="hint">提示：浏览器模式使用 HttpOnly 会话 Cookie；脚本模式仍可直接使用请求头传递管理密钥。</div>
  </div>
  <script>
    const form = document.getElementById('loginForm');
    const input = document.getElementById('keyInput');
    const button = document.getElementById('loginBtn');
    const error = document.getElementById('loginError');
    form.addEventListener('submit', async (event) => {
      event.preventDefault();
      const key = (input.value || '').trim();
      if (!key) {
        error.textContent = '请输入管理密钥';
        input.focus();
        return;
      }
      button.disabled = true;
      error.textContent = '';
      try {
        const res = await fetch('/auth/login', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ key }),
          credentials: 'same-origin',
        });
        const text = await res.text();
        let data = text;
        try { data = text ? JSON.parse(text) : null; } catch (_) {}
        if (!res.ok) {
          error.textContent = (data && data.error) ? data.error : '登录失败，请检查管理密钥';
          button.disabled = false;
          return;
        }
        error.textContent = '登录成功，正在进入控制台…';
        error.classList.add('ok');
        window.location.href = '/';
      } catch (_) {
        error.textContent = '登录请求失败，请稍后重试';
        button.disabled = false;
      }
    });
  </script>
</body>
</html>`

const managementHTML = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>号池管理器</title>
  <style>
    :root {
      color-scheme: dark;
      --bg: #0b1020;
      --panel: #121a30;
      --muted: #8ea0c9;
      --text: #edf2ff;
      --accent: #7aa2ff;
      --good: #3ddc97;
      --warn: #ffcc66;
      --bad: #ff7b72;
      --border: #263252;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      background: var(--bg);
      color: var(--text);
    }
    .wrap {
      max-width: 1280px;
      margin: 0 auto;
      padding: 24px;
    }
    h1, h2, h3 { margin: 0; }
    .topbar {
      display: flex;
      justify-content: space-between;
      gap: 16px;
      align-items: flex-start;
      margin-bottom: 20px;
    }
    .subtle { color: var(--muted); }
    .grid {
      display: grid;
      grid-template-columns: repeat(12, 1fr);
      gap: 16px;
    }
    .card {
      background: var(--panel);
      border: 1px solid var(--border);
      border-radius: 16px;
      padding: 16px;
      box-shadow: 0 8px 24px rgba(0,0,0,0.18);
    }
    .span-3 { grid-column: span 3; }
    .span-6 { grid-column: span 6; }
    .span-12 { grid-column: span 12; }
    .kpi {
      display: flex;
      flex-direction: column;
      gap: 8px;
    }
    .kpi .value {
      font-size: 32px;
      font-weight: 700;
    }
    .badge {
      display: inline-flex;
      align-items: center;
      gap: 8px;
      padding: 6px 10px;
      border-radius: 999px;
      font-size: 12px;
      border: 1px solid var(--border);
      color: var(--text);
      background: rgba(255,255,255,0.04);
      backdrop-filter: blur(8px);
    }
    .ok { color: var(--good); }
    .warn { color: var(--warn); }
    .bad { color: var(--bad); }
    .actions, .row-actions {
      display: flex;
      flex-wrap: wrap;
      gap: 12px;
      align-items: center;
    }
    .actions { margin-top: 14px; }
    button {
      border: 0;
      border-radius: 12px;
      padding: 10px 14px;
      background: var(--accent);
      color: white;
      font-weight: 600;
      cursor: pointer;
    }
    button.secondary {
      background: #1f2a48;
      color: var(--text);
      border: 1px solid var(--border);
    }
    button.danger {
      background: #7f1d1d;
    }
    button:disabled {
      opacity: 0.5;
      cursor: not-allowed;
    }
    table {
      width: 100%;
      border-collapse: collapse;
      margin-top: 12px;
      font-size: 14px;
    }
    th, td {
      text-align: left;
      padding: 12px 10px;
      border-bottom: 1px solid rgba(38,50,82,0.9);
      vertical-align: top;
    }
    th {
      color: var(--muted);
      font-weight: 600;
      position: sticky;
      top: 0;
      background: var(--panel);
      z-index: 1;
    }
    code, pre, .mono {
      font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    }
    pre {
      white-space: pre-wrap;
      word-break: break-word;
      background: rgba(255,255,255,0.03);
      border: 1px solid var(--border);
      border-radius: 12px;
      padding: 12px;
      max-height: 320px;
      overflow: auto;
      line-height: 1.45;
    }
    .small { font-size: 12px; }
    .issues-list, .proposal-items-list {
      display: flex;
      flex-direction: column;
      gap: 10px;
    }
    .issues-list {
      margin-top: 12px;
      max-height: 340px;
      overflow: auto;
      padding-right: 4px;
    }
    .issue-item, .proposal-item {
      border: 1px solid var(--border);
      border-radius: 14px;
      padding: 12px 14px;
      background: linear-gradient(180deg, rgba(255,255,255,0.045), rgba(255,255,255,0.02));
    }
    .issue-head, .proposal-item-head {
      display: flex;
      justify-content: space-between;
      gap: 12px;
      align-items: center;
      flex-wrap: wrap;
      margin-bottom: 10px;
    }
    .issue-name, .proposal-item-name {
      font-weight: 700;
      word-break: break-word;
      letter-spacing: 0.1px;
    }
    .issue-meta, .proposal-item-meta {
      display: grid;
      grid-template-columns: 72px 1fr;
      gap: 6px 10px;
      font-size: 13px;
    }
    .label-muted {
      color: var(--muted);
    }
    .col-mode {
      min-width: 120px;
    }
    .modal-backdrop {
      position: fixed;
      inset: 0;
      background: rgba(0,0,0,0.55);
      display: flex;
      align-items: center;
      justify-content: center;
      padding: 20px;
      z-index: 1000;
    }
    .modal-backdrop[hidden] {
      display: none;
    }
    .modal-panel {
      width: min(880px, 100%);
      max-height: 80vh;
      overflow: auto;
      background: var(--panel);
      border: 1px solid var(--border);
      border-radius: 18px;
      box-shadow: 0 20px 60px rgba(0,0,0,0.35);
      padding: 18px;
    }
    .modal-header {
      display: flex;
      justify-content: space-between;
      gap: 12px;
      align-items: center;
      margin-bottom: 12px;
    }
    .pill {
      display: inline-block;
      padding: 4px 8px;
      border-radius: 999px;
      background: rgba(122,162,255,0.12);
      border: 1px solid rgba(122,162,255,0.3);
      font-size: 12px;
      color: #cfe0ff;
      white-space: nowrap;
    }
    .danger-panel {
      border-color: rgba(255,123,114,0.35);
      box-shadow: 0 8px 24px rgba(127,29,29,0.18);
    }
    @media (max-width: 960px) {
      .span-3, .span-6, .span-12 { grid-column: span 12; }
      .topbar { flex-direction: column; }
    }
  </style>
</head>
<body>
  <div class="wrap">
    <div class="topbar">
      <div>
        <h1>号池管理器</h1>
        <div class="subtle">在一个页面里管理问题扫描、401 人工确认与执行记录，优先突出当前待处理事项。</div>
      </div>
      <div class="badge" id="refreshState">就绪</div>
    </div>

    <div class="grid">
      <section class="card span-3 kpi">
        <div class="subtle">运行模式</div>
        <div class="value" id="modeValue">-</div>
        <div id="modeHint" class="small subtle"></div>
      </section>
      <section class="card span-3 kpi">
        <div class="subtle">已存记录</div>
        <div class="value" id="proposalCount">-</div>
        <div class="small subtle">本地 SQLite 历史记录</div>
      </section>
      <section class="card span-3 kpi">
        <div class="subtle">自动问题扫描</div>
        <div class="value" id="issueAuto">-</div>
        <div class="small subtle" id="issueAutoHint">后台轻量巡检</div>
      </section>
      <section class="card span-3 kpi">
        <div class="subtle">自动重排</div>
        <div class="value" id="reorderAuto">-</div>
        <div class="small subtle" id="reorderAutoHint">后台全量重排循环</div>
      </section>

      <section class="card span-3 kpi">
        <div class="subtle">状态覆盖率</div>
        <div class="value" id="coverageValue">-</div>
        <div class="small subtle" id="coverageHint">已落库额度状态覆盖情况</div>
      </section>
      <section class="card span-3 kpi">
        <div class="subtle">下一波恢复高峰</div>
        <div class="value" id="recoveryPeakValue">-</div>
        <div class="small subtle" id="recoveryPeakHint">按 refresh_at 小时分桶</div>
      </section>

      <section class="card span-6">
        <h2>最近 1 小时自动循环摘要</h2>
        <div class="subtle small" style="margin-top:8px;">帮助判断系统是否在自动收敛，而不是只看静态状态。</div>
        <div class="issues-list" style="margin-top:12px;">
          <div class="issue-item">
            <div class="issue-head"><div class="issue-name">自动动作统计</div><span class="pill" id="recentActivityPill">最近 1 小时</span></div>
            <div class="issue-meta">
              <div class="label-muted">自动禁用</div><div id="recentAutoDisable">-</div>
              <div class="label-muted">自动启用</div><div id="recentAutoEnable">-</div>
              <div class="label-muted">自动转 401</div><div id="recentAuto401">-</div>
              <div class="label-muted">自动重排</div><div id="recentAutoReorder">-</div>
              <div class="label-muted">执行轮次</div><div id="recentProposalRuns">-</div>
            </div>
          </div>
        </div>
      </section>

      <section class="card span-6">
        <h2>最近会刷新额度的 5 个账号</h2>
        <div class="subtle small" style="margin-top:8px;">按 refresh_at 从近到远排序，帮助观察即将变化的账号和当前状态。</div>
        <div id="upcomingRefreshList" class="issues-list" style="margin-top:12px;"></div>
      </section>

      <section class="card span-6">
        <h2>最近恢复窗口概览</h2>
        <div class="subtle small" style="margin-top:8px;">把即将变化的账号与整体恢复节奏放在同一排，减少页面右侧留白。</div>
        <div class="issues-list" style="margin-top:12px;">
          <div class="issue-item">
            <div class="issue-head"><div class="issue-name">最近下一次刷新</div><span class="pill" id="nextRefreshPill">-</span></div>
            <div class="issue-meta">
              <div class="label-muted">时间</div><div id="nextRefreshTime">-</div>
              <div class="label-muted">下一波高峰</div><div id="nextRefreshPeak">-</div>
              <div class="label-muted">预计恢复数</div><div id="nextRefreshPeakCount">-</div>
              <div class="label-muted">当前受限账号</div><div id="limitedNowCount">-</div>
            </div>
          </div>
        </div>
      </section>

      <section class="card span-12">
        <h2>最近 Probe 记录</h2>
        <div class="subtle small" style="margin-top:8px;">帮助判断到底是不是 probe 触发了后续排序或状态变化。</div>
        <div id="recentProbeList" class="issues-list" style="margin-top:12px;"></div>
      </section>

      <section class="card span-12">
        <h2>自动循环状态</h2>
        <div class="subtle small" style="margin-top:8px;">高频任务负责快速禁用已满额账号与刷新过期额度状态；优先级重排改为较低频自动执行。</div>
        <div class="issues-list" style="margin-top:12px;">
          <div class="issue-item">
            <div class="issue-head"><div class="issue-name">配额维护循环</div><span class="pill" id="issueCyclePill">-</span></div>
            <div class="issue-meta">
              <div class="label-muted">频率</div><div id="issueCycleInterval">-</div>
              <div class="label-muted">说明</div><div>快速发现额度到顶、401、以及 refresh_at 到期后的状态变化。</div>
            </div>
          </div>
          <div class="issue-item">
            <div class="issue-head"><div class="issue-name">优先级重排循环</div><span class="pill" id="reorderCyclePill">-</span></div>
            <div class="issue-meta">
              <div class="label-muted">频率</div><div id="reorderCycleInterval">-</div>
              <div class="label-muted">说明</div><div>按最新 refresh_at 收敛 enabled 账号优先级，缺失或过期状态会自动补探测。</div>
            </div>
          </div>
        </div>
      </section>

      <section class="card span-6">
        <h2>扫描操作</h2>
        <div class="subtle small" style="margin-top:8px;">系统会自动循环维护配额与优先级；下面这些按钮更适合首次初始化、强制校准或人工立即触发。</div>
        <div class="actions">
          <button id="scanIssuesBtn">立即执行一次问题扫描</button>
          <button id="scanQuotaBtn" class="secondary">立即执行一次额度检查</button>
          <button id="scanReorderBtn" class="secondary">立即执行一次优先级重排</button>
          <button id="refreshBtn" class="secondary">刷新数据</button>
        </div>
        <div class="small subtle" id="fullProgressText" style="margin-top:10px;">全量任务空闲</div>
        <div style="margin-top:8px; border:1px solid var(--border); border-radius:999px; overflow:hidden; background:rgba(255,255,255,0.04);">
          <div id="fullProgressBar" style="height:10px; width:0%; background:linear-gradient(90deg, #7aa2ff, #3ddc97); transition:width 180ms ease;"></div>
        </div>
        <pre id="scanResult">当前页面尚未触发扫描。</pre>
      </section>

      <section class="card span-6">
        <h2>当前问题</h2>
        <div class="subtle small" style="margin-top:8px;">仅展示当前尚未处理的问题，按账号、原因、动作滚动查看。</div>
        <div id="issuesMeta" class="small subtle" style="margin-top:10px;">加载中…</div>
        <div id="issuesList" class="issues-list"></div>
      </section>

      <section class="card span-12 danger-panel">
        <div style="display:flex; justify-content:space-between; align-items:center; gap:12px; flex-wrap:wrap;">
          <div>
            <h2>401 待人工确认账号</h2>
            <div class="subtle small" style="margin-top:8px;">只有明确 401 才进入这里。系统会先自动禁用，删除仍需人工确认。</div>
          </div>
          <div class="badge"><span class="mono" id="review401Summary">0 条已加载</span></div>
        </div>
        <div style="overflow:auto; margin-top:10px;">
          <table>
            <thead>
              <tr>
                <th>账号</th>
                <th>Provider</th>
                <th>当前状态</th>
                <th>原因</th>
                <th>证据</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody id="review401TableBody"></tbody>
          </table>
        </div>
      </section>

      <section class="card span-12">
        <div style="display:flex; justify-content:space-between; align-items:center; gap:12px; flex-wrap:wrap;">
          <div>
            <h2>执行记录</h2>
            <div class="subtle small" style="margin-top:8px;">按时间倒序展示。当前模式为 <span class="mono">仅演练（不实际执行）</span> 时禁止执行。</div>
          </div>
          <div class="row-actions">
            <button id="proposalPrevBtn" class="secondary">上一页</button>
            <button id="proposalNextBtn" class="secondary">下一页</button>
            <div class="badge"><span class="mono" id="proposalSummary">0 条已加载</span></div>
          </div>
        </div>
        <div style="overflow:auto; margin-top:10px;">
          <table>
            <thead>
              <tr>
                <th>创建时间</th>
                <th class="col-mode">运行模式</th>
                <th>问题</th>
                <th>详情</th>
              </tr>
            </thead>
            <tbody id="proposalTableBody"></tbody>
          </table>
        </div>
      </section>
    </div>
  </div>

  <div id="proposalModal" class="modal-backdrop" hidden>
    <div class="modal-panel">
      <div class="modal-header">
        <div>
          <h3>记录详情</h3>
          <div id="proposalModalMeta" class="small subtle"></div>
        </div>
        <button id="proposalModalCloseBtn" class="secondary">关闭</button>
      </div>
      <div id="proposalModalBody" class="proposal-items-list"></div>
    </div>
  </div>

  <div id="evidenceModal" class="modal-backdrop" hidden>
    <div class="modal-panel">
      <div class="modal-header">
        <div>
          <h3>401 证据</h3>
          <div id="evidenceModalMeta" class="small subtle"></div>
        </div>
        <button id="evidenceModalCloseBtn" class="secondary">关闭</button>
      </div>
      <pre id="evidenceModalBody"></pre>
    </div>
  </div>

  <script>
    const els = {
      refreshState: document.getElementById('refreshState'),
      modeValue: document.getElementById('modeValue'),
      modeHint: document.getElementById('modeHint'),
      proposalCount: document.getElementById('proposalCount'),
      issueAuto: document.getElementById('issueAuto'),
      issueAutoHint: document.getElementById('issueAutoHint'),
      reorderAuto: document.getElementById('reorderAuto'),
      reorderAutoHint: document.getElementById('reorderAutoHint'),
      coverageValue: document.getElementById('coverageValue'),
      coverageHint: document.getElementById('coverageHint'),
      recoveryPeakValue: document.getElementById('recoveryPeakValue'),
      recoveryPeakHint: document.getElementById('recoveryPeakHint'),
      recentActivityPill: document.getElementById('recentActivityPill'),
      recentAutoDisable: document.getElementById('recentAutoDisable'),
      recentAutoEnable: document.getElementById('recentAutoEnable'),
      recentAuto401: document.getElementById('recentAuto401'),
      recentAutoReorder: document.getElementById('recentAutoReorder'),
      recentProposalRuns: document.getElementById('recentProposalRuns'),
      upcomingRefreshList: document.getElementById('upcomingRefreshList'),
      nextRefreshPill: document.getElementById('nextRefreshPill'),
      nextRefreshTime: document.getElementById('nextRefreshTime'),
      nextRefreshPeak: document.getElementById('nextRefreshPeak'),
      nextRefreshPeakCount: document.getElementById('nextRefreshPeakCount'),
      limitedNowCount: document.getElementById('limitedNowCount'),
      recentProbeList: document.getElementById('recentProbeList'),
      issueCyclePill: document.getElementById('issueCyclePill'),
      reorderCyclePill: document.getElementById('reorderCyclePill'),
      issueCycleInterval: document.getElementById('issueCycleInterval'),
      reorderCycleInterval: document.getElementById('reorderCycleInterval'),
      scanIssuesBtn: document.getElementById('scanIssuesBtn'),
      scanQuotaBtn: document.getElementById('scanQuotaBtn'),
      scanReorderBtn: document.getElementById('scanReorderBtn'),
      refreshBtn: document.getElementById('refreshBtn'),
      scanResult: document.getElementById('scanResult'),
      issuesMeta: document.getElementById('issuesMeta'),
      issuesList: document.getElementById('issuesList'),
      review401Summary: document.getElementById('review401Summary'),
      review401TableBody: document.getElementById('review401TableBody'),
      proposalTableBody: document.getElementById('proposalTableBody'),
      proposalSummary: document.getElementById('proposalSummary'),
      proposalPrevBtn: document.getElementById('proposalPrevBtn'),
      proposalNextBtn: document.getElementById('proposalNextBtn'),
      proposalModal: document.getElementById('proposalModal'),
      proposalModalMeta: document.getElementById('proposalModalMeta'),
      proposalModalBody: document.getElementById('proposalModalBody'),
      proposalModalCloseBtn: document.getElementById('proposalModalCloseBtn'),
      evidenceModal: document.getElementById('evidenceModal'),
      evidenceModalMeta: document.getElementById('evidenceModalMeta'),
      evidenceModalBody: document.getElementById('evidenceModalBody'),
      evidenceModalCloseBtn: document.getElementById('evidenceModalCloseBtn'),
      fullProgressText: document.getElementById('fullProgressText'),
      fullProgressBar: document.getElementById('fullProgressBar'),
    };

    let proposalOffset = 0;
    const proposalPageSize = 10;
    let proposalTotal = 0;
    let lastStatusData = null;
    let progressPollTimer = null;
    let fullProgressPending = false;

    function setState(text, cls='') {
      els.refreshState.className = 'badge ' + cls;
      els.refreshState.textContent = text;
    }

    function normalizeTimeValue(value) {
      if (typeof value === 'number' && Number.isFinite(value)) {
        const ms = value > 1e12 ? value : value * 1000;
        return formatShanghaiTime(new Date(ms).toISOString());
      }
      if (typeof value === 'string') {
        const trimmed = value.trim();
        if (/^\d{10}(\.\d+)?$/.test(trimmed)) {
          return formatShanghaiTime(new Date(Number(trimmed) * 1000).toISOString());
        }
        if (/^\d{13}$/.test(trimmed)) {
          return formatShanghaiTime(new Date(Number(trimmed)).toISOString());
        }
        if (/^\d{4}-\d{2}-\d{2}T/.test(trimmed) || /^[A-Z][a-z]{2},\s+\d{2}\s+[A-Z][a-z]{2}\s+\d{4}\s+\d{2}:\d{2}:\d{2}\s+GMT$/.test(trimmed)) {
          return formatShanghaiTime(trimmed);
        }
      }
      return value;
    }

    function normalizeTimeLikeFields(value, key = '') {
      if (Array.isArray(value)) {
        return value.map(item => normalizeTimeLikeFields(item, key));
      }
      if (value && typeof value === 'object') {
        const out = {};
        for (const [k, v] of Object.entries(value)) {
          out[k] = normalizeTimeLikeFields(v, k);
        }
        return out;
      }
      const lowerKey = String(key || '').toLowerCase();
      if (lowerKey.includes('time') || lowerKey.endsWith('_at') || lowerKey.endsWith('date') || lowerKey.includes('updated') || lowerKey.includes('reset')) {
        return normalizeTimeValue(value);
      }
      return value;
    }

    function pretty(value) {
      return JSON.stringify(normalizeTimeLikeFields(value), null, 2);
    }

    function modeLabel(mode) {
      if (mode === 'dry-run') return '仅演练';
      if (mode === 'apply') return '自动执行';
      return mode || '-';
    }

    function formatShanghaiTime(value) {
      if (!value) return '-';
      const date = new Date(value);
      if (Number.isNaN(date.getTime())) return value;
      return new Intl.DateTimeFormat('zh-CN', {
        timeZone: 'Asia/Shanghai',
        year: 'numeric',
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        hour12: false,
      }).format(date);
    }

    async function api(path, options = {}) {
      const res = await fetch(path, {
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        ...options,
      });
      const text = await res.text();
      let data = text;
      try { data = text ? JSON.parse(text) : null; } catch (_) {}
      if (!res.ok) {
        const message = typeof data === 'string' ? data : pretty(data);
        const err = new Error(path + ' → ' + res.status + '\n' + message);
        err.status = res.status;
        err.payload = data;
        throw err;
      }
      return data;
    }

    function actionLabel(action) {
      const mapping = {
        DISABLE_ACCOUNT: '禁用',
        ENABLE_ACCOUNT: '启用',
        DELETE_ACCOUNT: '删除',
        REORDER_PRIORITY: '重排优先级',
        MARK_401_REVIEW: '加入 401 人工确认',
      };
      return mapping[action] || action || '-';
    }

    function reasonLabel(reason) {
      const mapping = {
        quota_pending_disable: '额度已满，等待禁用',
        explicit_401_requires_manual_review: '明确 401，等待人工确认',
        'quota:usage_limit_reached': '额度已满',
        'quota:wham_limit_reached': '额度达到上限，自动禁用',
        'quota:wham_recovered': '额度恢复，自动启用',
        'auth:explicit_401_disable_first': '明确 401，先自动禁用',
        'auth:explicit_401_manual_review': '明确 401，等待人工确认',
        'reorder:refresh_at': '按额度刷新时间重排',
        'reorder:stored_refresh_at': '按已落库刷新时间重排',
        'reorder:next_retry_after_fallback': '按 next_retry_after 兜底重排',
        'reorder:fallback_name': '按名称兜底重排',
      };
      return mapping[reason] || reason || '-';
    }

    function renderIssues(items) {
      const list = Array.isArray(items) ? items : [];
      els.issuesMeta.textContent = list.length === 0 ? '当前没有待处理问题' : (list.length + ' 条待处理问题');
      els.issuesList.innerHTML = '';
      if (list.length === 0) {
        els.issuesList.innerHTML = '<div class="issue-item subtle">当前没有待处理问题。</div>';
        return;
      }
      for (const item of list) {
        const accountName = item.account_name || item.accountName || '-';
        const reason = reasonLabel(item.reason || '-');
        const action = item.reason === 'explicit_401_requires_manual_review' ? '人工确认 / 必要时删除' : '自动禁用';
        const div = document.createElement('div');
        div.className = 'issue-item';
        div.innerHTML = '' +
          '<div class="issue-head">' +
            '<div class="issue-name mono">' + accountName + '</div>' +
            '<span class="pill">' + (item.provider || '-') + '</span>' +
          '</div>' +
          '<div class="issue-meta">' +
            '<div class="label-muted">原因</div><div>' + reason + '</div>' +
            '<div class="label-muted">动作</div><div>' + action + '</div>' +
          '</div>';
        els.issuesList.appendChild(div);
      }
    }

    function renderUpcomingRefresh(items) {
      const list = Array.isArray(items) ? items : [];
      els.upcomingRefreshList.innerHTML = '';
      if (list.length === 0) {
        els.upcomingRefreshList.innerHTML = '<div class="issue-item subtle">当前没有可展示的刷新时间数据。</div>';
        return;
      }
      for (const item of list) {
        const disabled = item.allowed === false || item.limit_reached === true;
        const stateText = disabled ? '当前偏向受限/待恢复' : '当前可用';
        const div = document.createElement('div');
        div.className = 'issue-item';
        div.innerHTML = '' +
          '<div class="issue-head">' +
            '<div class="issue-name mono">' + (item.account_name || '-') + '</div>' +
            '<span class="pill">' + (item.plan_type || '-') + '</span>' +
          '</div>' +
          '<div class="issue-meta">' +
            '<div class="label-muted">刷新时间</div><div>' + formatShanghaiTime(item.refresh_at || '-') + '</div>' +
            '<div class="label-muted">当前状态</div><div>' + stateText + '</div>' +
            '<div class="label-muted">已用比例</div><div>' + ((item.used_percent ?? '-') + (typeof item.used_percent === 'number' ? '%' : '')) + '</div>' +
            '<div class="label-muted">最近探测</div><div>' + formatShanghaiTime(item.probed_at || '-') + '</div>' +
          '</div>';
        els.upcomingRefreshList.appendChild(div);
      }
    }

    function renderRecentProbes(items) {
      const list = Array.isArray(items) ? items : [];
      els.recentProbeList.innerHTML = '';
      if (list.length === 0) {
        els.recentProbeList.innerHTML = '<div class="issue-item subtle">最近没有 probe 记录。</div>';
        return;
      }
      for (const item of list) {
        const div = document.createElement('div');
        div.className = 'issue-item';
        div.innerHTML = '' +
          '<div class="issue-head">' +
            '<div class="issue-name mono">' + (item.account_name || '-') + '</div>' +
            '<span class="pill">' + (item.plan_type || '-') + '</span>' +
          '</div>' +
          '<div class="issue-meta">' +
            '<div class="label-muted">Probe 时间</div><div>' + formatShanghaiTime(item.probed_at || '-') + '</div>' +
            '<div class="label-muted">下次刷新</div><div>' + formatShanghaiTime(item.refresh_at || '-') + '</div>' +
            '<div class="label-muted">额度状态</div><div>' + ((item.allowed === false || item.limit_reached === true) ? '受限/已满' : '可用') + '</div>' +
            '<div class="label-muted">已用比例</div><div>' + ((item.used_percent ?? '-') + (typeof item.used_percent === 'number' ? '%' : '')) + '</div>' +
          '</div>';
        els.recentProbeList.appendChild(div);
      }
    }

    function renderProposalItems(items) {
      const list = Array.isArray(items) ? items : [];
      if (list.length === 0) {
        return '<div class="proposal-item subtle">这个提案当前没有问题项。</div>';
      }
      return list.map(item => {
        const name = item.account_name || item.accountName || item.account_id || item.accountId || '-';
        return '' +
          '<div class="proposal-item">' +
            '<div class="proposal-item-head">' +
              '<div class="proposal-item-name mono">' + name + '</div>' +
              '<span class="pill">' + actionLabel(item.action || '-') + '</span>' +
            '</div>' +
            '<div class="proposal-item-meta">' +
              '<div class="label-muted">原因</div><div>' + reasonLabel(item.reason || '-') + '</div>' +
            '</div>' +
          '</div>';
      }).join('');
    }

    function openProposalModal(proposal) {
      const items = Array.isArray(proposal && proposal.items) ? proposal.items : [];
      els.proposalModalMeta.textContent = formatShanghaiTime(proposal.created_at || proposal.createdAt || '-') + ' · ' + modeLabel(proposal.mode || '-') + ' · ' + items.length + ' 个问题项';
      els.proposalModalBody.innerHTML = renderProposalItems(items);
      els.proposalModal.hidden = false;
    }

    function closeProposalModal() {
      els.proposalModal.hidden = true;
      els.proposalModalBody.innerHTML = '';
      els.proposalModalMeta.textContent = '';
    }

    function openEvidenceModal(item) {
      els.evidenceModalMeta.textContent = (item.account_name || '-') + ' · ' + (item.provider || '-');
      els.evidenceModalBody.textContent = pretty(item.evidence || {});
      els.evidenceModal.hidden = false;
    }

    function closeEvidenceModal() {
      els.evidenceModal.hidden = true;
      els.evidenceModalBody.textContent = '';
      els.evidenceModalMeta.textContent = '';
    }

    async function disableAccount(name) {
      return api('/accounts/' + encodeURIComponent(name) + '/disable', { method: 'POST' });
    }

    async function deleteAccount(name) {
      return api('/accounts/' + encodeURIComponent(name) + '/delete', { method: 'POST' });
    }

    function render401Review(mode, items) {
      const list = Array.isArray(items) ? items : [];
      const dryRun = mode === 'dry-run';
      els.review401Summary.textContent = list.length + ' 条已加载';
      els.review401TableBody.innerHTML = '';
      for (const item of list) {
        const tr = document.createElement('tr');
        const disabled = !!item.disabled;
        tr.innerHTML = '' +
          '<td class="mono small">' + (item.account_name || '-') + '</td>' +
          '<td>' + (item.provider || '-') + '</td>' +
          '<td><span class="pill">' + (disabled ? 'disabled' : 'active') + '</span></td>' +
          '<td>' + (item.reason || '-') + '</td>' +
          '<td><button class="secondary small-btn" data-kind="evidence401" data-id="' + (item.account_name || '') + '">查看证据</button></td>' +
          '<td>' +
            '<div class="row-actions">' +
              '<button class="secondary" data-kind="disable401" data-id="' + (item.account_name || '') + '" ' + ((dryRun || disabled) ? 'disabled' : '') + '>禁用</button>' +
              '<button class="danger" data-kind="delete401" data-id="' + (item.account_name || '') + '" ' + (dryRun ? 'disabled' : '') + '>删除</button>' +
            '</div>' +
          '</td>';
        els.review401TableBody.appendChild(tr);
      }

      els.review401TableBody.querySelectorAll('button[data-kind="evidence401"]').forEach(btn => {
        btn.addEventListener('click', async () => {
          const id = btn.dataset.id;
          const item = list.find(x => (x.account_name || '') === id);
          if (item) openEvidenceModal(item);
        });
      });

      els.review401TableBody.querySelectorAll('button[data-kind="disable401"]').forEach(btn => {
        btn.addEventListener('click', async () => {
          const id = btn.dataset.id;
          if (!id) return;
          btn.disabled = true;
          try {
            setState('正在禁用 401 账号…', 'warn');
            const result = await disableAccount(id);
            els.scanResult.textContent = pretty(result);
            await refreshAll();
            setState('401 账号已禁用', 'ok');
          } catch (err) {
            els.scanResult.textContent = String(err.message || err);
            setState('禁用失败', 'bad');
            btn.disabled = false;
          }
        });
      });

      els.review401TableBody.querySelectorAll('button[data-kind="delete401"]').forEach(btn => {
        btn.addEventListener('click', async () => {
          const id = btn.dataset.id;
          if (!id) return;
          if (!confirm('确认删除 401 账号 ' + id + ' 吗？该动作不可恢复。')) return;
          btn.disabled = true;
          try {
            setState('正在删除 401 账号…', 'warn');
            const result = await deleteAccount(id);
            els.scanResult.textContent = pretty(result);
            await refreshAll();
            setState('401 账号已删除', 'ok');
          } catch (err) {
            els.scanResult.textContent = String(err.message || err);
            setState('删除失败', 'bad');
            btn.disabled = false;
          }
        });
      });
    }

    function renderProposals(mode, proposals) {
      const start = proposalTotal === 0 ? 0 : proposalOffset + 1;
      const end = proposalOffset + proposals.length;
      els.proposalSummary.textContent = proposalTotal === 0 ? '0 条已加载' : ('第 ' + start + '-' + end + ' 条 / 共 ' + proposalTotal + ' 条');
      els.proposalTableBody.innerHTML = '';
      for (const proposal of proposals) {
        const tr = document.createElement('tr');
        const items = Array.isArray(proposal.items) ? proposal.items : [];
        tr.innerHTML = '' +
          '<td>' + formatShanghaiTime(proposal.created_at || proposal.createdAt || '-') + '</td>' +
          '<td class="col-mode"><span class="pill">' + modeLabel(proposal.mode || '-') + '</span></td>' +
          '<td>' + items.length + '</td>' +
          '<td><button class="secondary small-btn" data-kind="details" data-id="' + (proposal.id || '') + '">查看列表</button></td>';
        els.proposalTableBody.appendChild(tr);
      }

      els.proposalTableBody.querySelectorAll('button[data-kind="details"]').forEach(btn => {
        btn.addEventListener('click', async () => {
          const id = btn.dataset.id;
          const proposal = proposals.find(p => p.id === id);
          if (proposal) openProposalModal(proposal);
        });
      });
    }

    async function refreshProposalPage() {
      els.proposalTableBody.style.opacity = '0.55';
      try {
        const proposalsResp = await api('/proposals?limit=' + proposalPageSize + '&offset=' + proposalOffset);
        proposalTotal = proposalsResp && typeof proposalsResp.total === 'number' ? proposalsResp.total : 0;
        renderProposals((lastStatusData && lastStatusData.mode) || '-', Array.isArray(proposalsResp.items) ? proposalsResp.items : []);
        els.proposalPrevBtn.disabled = proposalOffset <= 0;
        els.proposalNextBtn.disabled = proposalOffset + proposalPageSize >= proposalTotal;
      } finally {
        els.proposalTableBody.style.opacity = '1';
      }
    }

    async function refreshProgressOnly() {
      try {
        const progress = await api('/progress/full');
        renderFullProgress(progress);
      } catch (err) {
        if (err && err.status === 401) {
          logoutToLogin('登录已失效，或当前会话未授权。请返回登录页重新输入管理密钥。');
        }
      }
    }

    function ensureProgressPolling(shouldPoll) {
      if (shouldPoll) {
        if (progressPollTimer) return;
        progressPollTimer = setInterval(() => {
          refreshProgressOnly();
        }, 1500);
        return;
      }
      if (progressPollTimer) {
        clearInterval(progressPollTimer);
        progressPollTimer = null;
      }
    }

    function renderFullProgress(progress) {
      const p = progress || {};
      const percent = typeof p.percent === 'number' ? p.percent : 0;
      if (!p.running && fullProgressPending && percent <= 0) {
        els.fullProgressBar.style.width = '2%';
        ensureProgressPolling(true);
        els.fullProgressText.textContent = '任务已启动，正在获取进度…';
        return;
      }
      if (p.running) {
        fullProgressPending = false;
      }
      els.fullProgressBar.style.width = percent + '%';
      ensureProgressPolling(!!p.running || (percent > 0 && percent < 100));
      if (!p.running) {
        els.fullProgressText.textContent = percent >= 100 ? '全量任务已完成' : '全量任务空闲';
        return;
      }
      const current = p.current ? (' · 当前: ' + p.current) : '';
      els.fullProgressText.textContent = (p.stage || '全量处理中') + ' · ' + (p.done || 0) + '/' + (p.total || 0) + ' · ' + percent + '%' + current;
    }

    async function logoutToLogin(message) {
      setState('未授权，请重新登录', 'bad');
      els.scanResult.textContent = message || '登录已失效，请重新登录。';
      try {
        await fetch('/auth/logout', { method: 'POST', credentials: 'same-origin' });
      } catch (_) {}
      setTimeout(() => {
        window.location.href = '/';
      }, 600);
    }

    async function refreshAll() {
      setState('刷新中…', 'warn');
      try {
        const [status, issues, review401, proposalsResp, fullProgress] = await Promise.all([
          api('/status'),
          api('/issues'),
          api('/review/401'),
          api('/proposals?limit=' + proposalPageSize + '&offset=' + proposalOffset),
          api('/progress/full'),
        ]);
        lastStatusData = status;

        els.modeValue.textContent = modeLabel(status.mode || '-');
        els.modeValue.className = 'value ' + ((status.mode || '') === 'dry-run' ? 'warn' : 'ok');
        els.modeHint.textContent = (status.mode || '') === 'dry-run'
          ? '当前为仅演练模式，已主动禁止执行真实变更。'
          : '当前为自动执行模式，扫描出的可执行动作会直接落地。';
        els.proposalCount.textContent = status.proposal_count ?? 0;
        els.issueAuto.textContent = status.auto_issue_scan ? '开启' : '关闭';
        els.issueAuto.className = 'value ' + (status.auto_issue_scan ? 'ok' : 'subtle');
        els.issueAutoHint.textContent = '周期 ' + ((status.issue_scan_interval_seconds || 0) > 0 ? ((status.issue_scan_interval_seconds || 0) + ' 秒') : '-');
        els.reorderAuto.textContent = status.auto_reorder ? '开启' : '关闭';
        els.reorderAuto.className = 'value ' + (status.auto_reorder ? 'ok' : 'subtle');
        els.reorderAutoHint.textContent = '周期 ' + ((status.auto_reorder_interval_seconds || 0) > 0 ? ((status.auto_reorder_interval_seconds || 0) + ' 秒') : '-');
        const quotaSummary = status.quota_summary || {};
        const tracked = quotaSummary.tracked_accounts ?? 0;
        const unknown = quotaSummary.unknown_refresh_accounts ?? 0;
        const covered = Math.max(0, tracked - unknown);
        els.coverageValue.textContent = covered + ' / ' + tracked;
        els.coverageValue.className = 'value ok';
        els.coverageHint.textContent = '已建档 ' + covered + ' 个，待补探测 ' + unknown + ' 个';
        els.recoveryPeakValue.textContent = quotaSummary.recovery_peak_bucket ? formatShanghaiTime(quotaSummary.recovery_peak_bucket) : '-';
        els.recoveryPeakHint.textContent = quotaSummary.recovery_peak_bucket ? ('该小时预计恢复 ' + (quotaSummary.recovery_peak_count ?? 0) + ' 个账号') : '暂无恢复高峰数据';
        els.nextRefreshPill.textContent = quotaSummary.next_refresh_at ? '即将到点' : '暂无数据';
        els.nextRefreshTime.textContent = formatShanghaiTime(quotaSummary.next_refresh_at || '-');
        els.nextRefreshPeak.textContent = quotaSummary.recovery_peak_bucket ? formatShanghaiTime(quotaSummary.recovery_peak_bucket) : '-';
        els.nextRefreshPeakCount.textContent = quotaSummary.recovery_peak_count ?? 0;
        els.limitedNowCount.textContent = quotaSummary.limited_now_estimate ?? 0;
        const recent = status.recent_activity || {};
        els.recentAutoDisable.textContent = recent.auto_disable ?? 0;
        els.recentAutoEnable.textContent = recent.auto_enable ?? 0;
        els.recentAuto401.textContent = recent.auto_mark_401 ?? 0;
        els.recentAutoReorder.textContent = recent.auto_reorder ?? 0;
        els.recentProposalRuns.textContent = recent.proposal_runs ?? 0;
        renderUpcomingRefresh(status.upcoming_refresh || []);
        renderRecentProbes(status.recent_probes || []);
        els.issueCyclePill.textContent = status.auto_issue_scan ? '运行中' : '已关闭';
        els.issueCycleInterval.textContent = (status.issue_scan_interval_seconds || 0) > 0 ? ((status.issue_scan_interval_seconds || 0) + ' 秒 / 次') : '-';
        els.reorderCyclePill.textContent = status.auto_reorder ? '运行中' : '已关闭';
        els.reorderCycleInterval.textContent = (status.auto_reorder_interval_seconds || 0) > 0 ? ((status.auto_reorder_interval_seconds || 0) + ' 秒 / 次') : '-';
        renderIssues(issues);
        render401Review(status.mode || '', review401);
        renderFullProgress(fullProgress);
        proposalTotal = proposalsResp && typeof proposalsResp.total === 'number' ? proposalsResp.total : 0;
        renderProposals(status.mode || '', Array.isArray(proposalsResp.items) ? proposalsResp.items : []);
        els.proposalPrevBtn.disabled = proposalOffset <= 0;
        els.proposalNextBtn.disabled = proposalOffset + proposalPageSize >= proposalTotal;
        setState('已刷新', 'ok');
      } catch (err) {
        if (err && err.status === 401) {
          logoutToLogin('登录已失效，或当前会话未授权。请返回登录页重新输入管理密钥。');
          return;
        }
        setState('刷新失败', 'bad');
        els.scanResult.textContent = String(err.message || err);
      }
    }

    async function runScan(path, button, waitingText) {
      button.disabled = true;
      setState(waitingText, 'warn');
      els.scanResult.textContent = '等待 ' + path + ' 返回…';
      try {
        if (path.indexOf('/scan/full') === 0) {
          fullProgressPending = true;
          renderFullProgress({ running: false, total: 0, done: 0, percent: 0, stage: waitingText });
          refreshProgressOnly().catch(() => {});
        }
        const result = await api(path, { method: 'POST' });
        els.scanResult.textContent = pretty(result);
        proposalOffset = 0;
        await refreshAll();
        setState(path + ' 完成', 'ok');
      } catch (err) {
        if (err && err.status === 401) {
          logoutToLogin('登录已失效，或当前会话未授权。请重新登录后再试。');
          return;
        }
        els.scanResult.textContent = String(err.message || err);
        setState(path + ' 失败', 'bad');
      } finally {
        button.disabled = false;
      }
    }

    els.proposalModalCloseBtn.addEventListener('click', closeProposalModal);
    els.proposalModal.addEventListener('click', (event) => {
      if (event.target === els.proposalModal) closeProposalModal();
    });
    els.evidenceModalCloseBtn.addEventListener('click', closeEvidenceModal);
    els.evidenceModal.addEventListener('click', (event) => {
      if (event.target === els.evidenceModal) closeEvidenceModal();
    });
    els.scanIssuesBtn.addEventListener('click', () => runScan('/scan/issues', els.scanIssuesBtn, '正在执行问题扫描…'));
    els.scanQuotaBtn.addEventListener('click', () => runScan('/scan/full?mode=quota', els.scanQuotaBtn, '正在执行额度检查…'));
    els.scanReorderBtn.addEventListener('click', () => runScan('/scan/full?mode=reorder', els.scanReorderBtn, '正在执行优先级重排…'));
    els.refreshBtn.addEventListener('click', refreshAll);
    els.proposalPrevBtn.addEventListener('click', async () => {
      proposalOffset = Math.max(0, proposalOffset - proposalPageSize);
      await refreshProposalPage();
    });
    els.proposalNextBtn.addEventListener('click', async () => {
      if (proposalOffset + proposalPageSize < proposalTotal) {
        proposalOffset += proposalPageSize;
        await refreshProposalPage();
      }
    });

    refreshAll();
  </script>
</body>
</html>`

const sessionCookieName = "cpa_pool_manager_session"

func sessionToken(secret string) string {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return ""
	}
	nonceBytes := make([]byte, 18)
	if _, err := rand.Read(nonceBytes); err != nil {
		return ""
	}
	nonce := base64.RawURLEncoding.EncodeToString(nonceBytes)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("cpa_pool_manager_session:" + nonce))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return "v1." + nonce + "." + sig
}

func setSessionCookie(c *gin.Context, cfg config.Config) {
	maxAge := 12 * 60 * 60
	secure := c.Request.TLS != nil || strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https")
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(sessionCookieName, sessionToken(cfg.WebSessionSecret), maxAge, "/", "", secure, true)
}

func clearSessionCookie(c *gin.Context) {
	secure := c.Request.TLS != nil || strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https")
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(sessionCookieName, "", -1, "/", "", secure, true)
}

func authorizedByHeader(c *gin.Context, sharedKey string) bool {
	sharedKey = strings.TrimSpace(sharedKey)
	if sharedKey == "" {
		return true
	}
	provided := strings.TrimSpace(c.GetHeader("X-CPA-Management-Key"))
	if provided == "" {
		provided = strings.TrimSpace(c.GetHeader("Authorization"))
		if strings.HasPrefix(strings.ToLower(provided), "bearer ") {
			provided = strings.TrimSpace(provided[7:])
		}
	}
	return provided == sharedKey
}

func authorizedBySession(c *gin.Context, cfg config.Config) bool {
	secret := strings.TrimSpace(cfg.WebSessionSecret)
	if secret == "" {
		return false
	}
	token, err := c.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "v1" || strings.TrimSpace(parts[1]) == "" || strings.TrimSpace(parts[2]) == "" {
		return false
	}
	nonce := parts[1]
	expectedMac := hmac.New(sha256.New, []byte(secret))
	_, _ = expectedMac.Write([]byte("cpa_pool_manager_session:" + nonce))
	expectedSig := base64.RawURLEncoding.EncodeToString(expectedMac.Sum(nil))
	return hmac.Equal([]byte(parts[2]), []byte(expectedSig))
}

func authMiddleware(cfg config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.TrimSpace(cfg.CPAManagementKey) == "" {
			c.Next()
			return
		}
		if authorizedByHeader(c, cfg.CPAManagementKey) || authorizedBySession(c, cfg) {
			c.Next()
			return
		}
		acceptsHTML := strings.Contains(strings.ToLower(c.GetHeader("Accept")), "text/html")
		if c.Request.Method == http.MethodGet && c.Request.URL.Path == "/" && acceptsHTML {
			c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(loginHTML))
			c.Abort()
			return
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
	}
}

func Router(cfg config.Config, e *engine.Engine) *gin.Engine {
	r := gin.Default()
	r.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	r.POST("/auth/login", func(c *gin.Context) {
		var req struct {
			Key string `json:"key"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		if strings.TrimSpace(cfg.CPAManagementKey) == "" || strings.TrimSpace(req.Key) != strings.TrimSpace(cfg.CPAManagementKey) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "管理密钥错误"})
			return
		}
		setSessionCookie(c, cfg)
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	r.POST("/auth/logout", func(c *gin.Context) {
		clearSessionCookie(c)
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	authed := r.Group("/")
	authed.Use(authMiddleware(cfg))
	authed.GET("/", func(c *gin.Context) { c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(managementHTML)) })
	authed.GET("/status", func(c *gin.Context) {
		proposalCount, err := e.Store.CountProposals()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		quotaSummary, err := e.Store.GetQuotaStateSummary()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		recentSummary, err := e.Store.GetRecentActivitySummary(time.Now().UTC().Add(-1 * time.Hour))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		upcomingRefresh, err := e.Store.ListUpcomingRefresh(5)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		recentProbes, err := e.Store.ListRecentProbes(10)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"mode": e.Cfg.AppMode,
			"proposal_count": proposalCount,
			"last_log_cursor": e.LastLogCursor,
			"auto_issue_scan": e.Cfg.EnableAutoIssueScan,
			"auto_reorder": e.Cfg.EnableAutoReorder,
			"issue_scan_interval_seconds": e.Cfg.IssueScanIntervalSeconds,
			"auto_reorder_interval_seconds": e.Cfg.AutoReorderIntervalSeconds,
			"quota_summary": quotaSummary,
			"recent_activity": recentSummary,
			"upcoming_refresh": upcomingRefresh,
			"recent_probes": recentProbes,
		})
	})
	authed.GET("/issues", func(c *gin.Context) {
		issues, err := e.ScanIssues()
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, issues)
	})
	authed.GET("/review/401", func(c *gin.Context) {
		items, err := e.ListManualReview401()
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, items)
	})
	authed.GET("/progress/full", func(c *gin.Context) {
		c.JSON(http.StatusOK, e.GetFullProgress())
	})
	authed.POST("/scan/issues", func(c *gin.Context) {
		proposal, err := e.ReconcileIssuesOnly()
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		if proposal.Items == nil {
			proposal.Items = []types.ProposalItem{}
		}
		c.JSON(http.StatusOK, proposal)
	})
	authed.POST("/scan/full", func(c *gin.Context) {
		mode := c.DefaultQuery("mode", "quota")
		if mode != "quota" && mode != "reorder" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "mode 仅支持 quota 或 reorder"})
			return
		}
		proposal, err := e.ReconcileFull(mode)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		if proposal.Items == nil {
			proposal.Items = []types.ProposalItem{}
		}
		c.JSON(http.StatusOK, proposal)
	})
	authed.GET("/proposals", func(c *gin.Context) {
		limit := 20
		offset := 0
		if v := c.Query("limit"); v != "" {
			fmt.Sscanf(v, "%d", &limit)
		}
		if v := c.Query("offset"); v != "" {
			fmt.Sscanf(v, "%d", &offset)
		}
		items, err := e.Store.ListProposals(limit, offset)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		total, err := e.Store.CountProposals()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": items, "total": total, "limit": limit, "offset": offset})
	})
	authed.POST("/accounts/:name/disable", func(c *gin.Context) {
		name := c.Param("name")
		if err := e.CPA.PatchAuthFileStatus(name, true); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "account": name, "action": "disabled"})
	})
	authed.POST("/accounts/:name/delete", func(c *gin.Context) {
		name := c.Param("name")
		if err := e.CPA.DeleteAuthFile(name); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "account": name, "action": "deleted"})
	})
	authed.POST("/proposals/:id/apply", func(c *gin.Context) {
		if e.Cfg.AppMode == "dry-run" {
			c.JSON(http.StatusConflict, gin.H{"error": "当前为仅演练模式，禁止执行真实变更"})
			return
		}
		if err := e.ApplyProposal(c.Param("id")); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}
