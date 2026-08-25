/**
 * KArbit - High-Frequency Binance Triangular Arbitrage Terminal Client
 */

let ws = null;
let reconnectTimer = null;
let currentMode = 'paper';
let liveAccountData = null;
let lastPingTime = 0;
let latencyMs = 0;

// DOM Elements
const wsStatusDot = document.getElementById('ws-status-dot');
const wsStatusText = document.getElementById('ws-status-text');
const wsLatencyVal = document.getElementById('ws-latency-val');
const modeBadge = document.getElementById('mode-badge');
const modeText = document.getElementById('mode-text');
const uptimeVal = document.getElementById('uptime-val');
const baseAssetVal = document.getElementById('base-asset-val');
const btnReconnect = document.getElementById('btn-reconnect');

// KPI Elements
const kpiPnlVal = document.getElementById('kpi-pnl-val');
const kpiPnlPct = document.getElementById('kpi-pnl-pct');
const kpiPnlSubtext = document.getElementById('kpi-pnl-subtext');
const kpiWalletTitle = document.getElementById('kpi-wallet-title');
const kpiWalletVal = document.getElementById('kpi-wallet-val');
const kpiCapitalVal = document.getElementById('kpi-capital-val');
const kpiEvalsSec = document.getElementById('kpi-evals-sec');
const kpiWsMsgs = document.getElementById('kpi-ws-msgs');
const kpiTriCount = document.getElementById('kpi-tri-count');
const kpiTradesCount = document.getElementById('kpi-trades-count');
const kpiWinrate = document.getElementById('kpi-winrate');
const kpiCircuit = document.getElementById('kpi-circuit');

// Radar & Executions
let currentRadarFilter = 'all'; // 'all' | 'triggered' | 'monitoring'
const oppsTbody = document.getElementById('opps-tbody');
const execTbody = document.getElementById('exec-tbody');
const radarCount = document.getElementById('radar-count');
const filterMinProfitTag = document.getElementById('filter-min-profit-tag');
const btnClearTable = document.getElementById('btn-clear-table');
const btnTestTrade = document.getElementById('btn-test-trade');
const tabRadarAll = document.getElementById('tab-radar-all');
const tabRadarTriggered = document.getElementById('tab-radar-triggered');
const tabRadarMonitoring = document.getElementById('tab-radar-monitoring');
const tabCountAll = document.getElementById('tab-count-all');
const tabCountTriggered = document.getElementById('tab-count-triggered');
const tabCountMonitoring = document.getElementById('tab-count-monitoring');

// Form & Controls
const formConfig = document.getElementById('form-config');
const btnModePaper = document.getElementById('btn-mode-paper');
const btnModeLive = document.getElementById('btn-mode-live');
const inputCapital = document.getElementById('input-capital');
const inputMinProfit = document.getElementById('input-min-profit');
const inputFeeRate = document.getElementById('input-fee-rate');
const inputBnbDiscount = document.getElementById('input-bnb-discount');
const inputMaxLatency = document.getElementById('input-max-latency');
const inputTrackedTriangles = document.getElementById('input-tracked-triangles');
const inputRadarLimit = document.getElementById('input-radar-limit');
const inputMaxSlippage = document.getElementById('input-max-slippage');
const inputMaxDailyLoss = document.getElementById('input-max-daily-loss');

// Live Binance Elements & Modal
const binanceApiCard = document.getElementById('binance-api-card');
const inputApiKey = document.getElementById('input-api-key');
const inputApiSecret = document.getElementById('input-api-secret');
const apiStatusBadge = document.getElementById('api-status-badge');
const liveBalanceBox = document.getElementById('live-balance-box');
const liveUsdtBal = document.getElementById('live-usdt-bal');
const liveBnbBal = document.getElementById('live-bnb-bal');
const liveCanTrade = document.getElementById('live-can-trade');
const btnToggleKey = document.getElementById('btn-toggle-key');
const btnToggleSecret = document.getElementById('btn-toggle-secret');
const btnReconfigureKeys = document.getElementById('btn-reconfigure-keys');

// Live Mode Auth Modal Elements
const modalLiveAuth = document.getElementById('modal-live-auth');
const btnModalCancel = document.getElementById('btn-modal-cancel');
const btnModalVerify = document.getElementById('btn-modal-verify');
const modalVerifyText = document.getElementById('modal-verify-text');
const modalAuthAlert = document.getElementById('modal-auth-alert');

// Toast Container
const toastContainer = document.getElementById('toast-container');

/**
 * Initialize Web Client
 */
function init() {
  connectWebSocket();
  setupEventListeners();
}

let isFirstConnect = true;

/**
 * Connect to Web GUI WebSocket Broadcast Stream
 */
function connectWebSocket() {
  if (ws) {
    try { ws.close(); } catch(e) {}
  }

  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  const wsUrl = `${protocol}//${window.location.host}/ws/stream`;

  updateConnectionStatus(false, 'CONNECTING...');
  lastPingTime = Date.now();

  try {
    ws = new WebSocket(wsUrl);

    ws.onopen = () => {
      latencyMs = Date.now() - lastPingTime;
      updateConnectionStatus(true, 'LIVE STREAM');
      if (isFirstConnect) {
        showToast('Connected to KArbit HFT telemetry engine', 'success');
        isFirstConnect = false;
      }
      if (reconnectTimer) {
        clearTimeout(reconnectTimer);
        reconnectTimer = null;
      }
    };

    ws.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data);
        renderDashboard(data);
      } catch (err) {
        console.error('Failed to parse frame:', err);
      }
    };

    ws.onclose = () => {
      updateConnectionStatus(false, 'DISCONNECTED');
      scheduleReconnect();
    };

    ws.onerror = () => {
      updateConnectionStatus(false, 'STREAM ERROR');
    };
  } catch (err) {
    updateConnectionStatus(false, 'OFFLINE');
    scheduleReconnect();
  }
}

function scheduleReconnect() {
  if (!reconnectTimer) {
    reconnectTimer = setTimeout(() => {
      reconnectTimer = null;
      connectWebSocket();
    }, 2000);
  }
}

function updateConnectionStatus(isConnected, label) {
  if (isConnected) {
    wsStatusDot.className = 'status-dot online';
    wsStatusText.textContent = label;
    wsLatencyVal.textContent = `${latencyMs || 2} ms`;
    wsLatencyVal.style.display = 'inline-block';
  } else {
    wsStatusDot.className = 'status-dot offline';
    wsStatusText.textContent = label;
    wsLatencyVal.textContent = '-- ms';
  }
}

/**
 * Render all dashboard telemetry from incoming frame
 */
function renderDashboard(d) {
  if (!d) return;
  window.latestDashboardData = d;

  // 1. Header & Telemetry
  if (d.ws_stats) {
    if (d.ws_stats.is_connected) {
      wsLatencyVal.textContent = `${d.ws_stats.last_latency_ms || 2} ms`;
    }
  }

  // Uptime calculation
  if (d.start_time) {
    const startMs = new Date(d.start_time).getTime();
    const diffSec = Math.floor((Date.now() - startMs) / 1000);
    if (diffSec >= 0) {
      const hrs = String(Math.floor(diffSec / 3600)).padStart(2, '0');
      const mins = String(Math.floor((diffSec % 3600) / 60)).padStart(2, '0');
      const secs = String(diffSec % 60).padStart(2, '0');
      uptimeVal.textContent = `${hrs}:${mins}:${secs}`;
    }
  }

  if (d.base_currency) {
    baseAssetVal.textContent = d.base_currency;
  }

  // Initial sync: populate Live Parameter form fields from server config on first load
  if (!window.hasInitializedFormConfig && d) {
    if (d.trade_amount_usdt !== undefined && inputCapital) inputCapital.value = d.trade_amount_usdt.toFixed(2);
    if (d.min_profit_percent !== undefined && inputMinProfit) inputMinProfit.value = d.min_profit_percent;
    if (d.fee_rate !== undefined && inputFeeRate) inputFeeRate.value = d.fee_rate;
    if (d.max_latency_ms !== undefined && inputMaxLatency) inputMaxLatency.value = d.max_latency_ms;
    if (d.use_bnb_discount !== undefined && inputBnbDiscount) inputBnbDiscount.checked = d.use_bnb_discount;
    if (d.max_tracked_triangles !== undefined && inputTrackedTriangles) inputTrackedTriangles.value = d.max_tracked_triangles;
    if (d.radar_display_limit !== undefined && inputRadarLimit) inputRadarLimit.value = d.radar_display_limit;
    if (d.max_slippage_tolerance !== undefined && inputMaxSlippage) inputMaxSlippage.value = (d.max_slippage_tolerance * 100).toFixed(2);
    if (d.max_daily_loss_usdt !== undefined && inputMaxDailyLoss) inputMaxDailyLoss.value = d.max_daily_loss_usdt.toFixed(2);
    window.hasInitializedFormConfig = true;
  }

  // Mode badge
  const isLive = d.trading_mode === 'live';
  currentMode = d.trading_mode || 'paper';
  if (isLive) {
    modeBadge.className = 'mode-pill live';
    modeText.textContent = 'LIVE BINANCE';
    kpiWalletTitle.textContent = 'BINANCE SPOT BALANCE';
    kpiPnlSubtext.textContent = 'Live Net Profit';
    binanceApiCard.style.display = 'block';
  } else {
    modeBadge.className = 'mode-pill';
    modeText.textContent = 'PAPER TRADING';
    kpiWalletTitle.textContent = 'WALLET BALANCE (PAPER)';
    kpiPnlSubtext.textContent = 'Simulated Net Profit';
  }

  // 2. KPI Cards
  const pnl = d.cumulative_pnl || 0;
  const pnlSign = pnl >= 0 ? '+' : '-';
  const pnlColorClass = pnl >= 0 ? 'text-emerald' : 'text-danger';
  kpiPnlVal.textContent = `${pnlSign}$${Math.abs(pnl).toFixed(4)}`;
  kpiPnlVal.className = `kpi-primary-val font-mono ${pnlColorClass}`;

  const initialCap = d.trade_amount_usdt || 100;
  const pnlPct = (pnl / initialCap) * 100;
  kpiPnlPct.textContent = `${pnlPct >= 0 ? '+' : ''}${pnlPct.toFixed(2)}%`;
  kpiPnlPct.className = `badge-pill ${pnl >= 0 ? 'bg-success-soft' : 'bg-danger-soft'}`;

  // Wallet
  if (isLive && d.has_live_api_keys && d.live_account_balance > 0) {
    kpiWalletVal.textContent = `$${d.live_account_balance.toFixed(2)}`;
  } else {
    kpiWalletVal.textContent = `$${(d.wallet_balance || 100).toFixed(2)}`;
  }
  kpiCapitalVal.textContent = `$${(d.trade_amount_usdt || 100).toFixed(2)}`;

  // Throughput
  kpiEvalsSec.innerHTML = `${(d.evaluations_per_sec || 0).toLocaleString()} <span class="val-unit">evals/s</span>`;
  if (d.ws_stats) {
    const msgsSec = d.ws_stats.messages_per_second || d.ws_stats.messages_per_sec || 0;
    kpiWsMsgs.textContent = msgsSec.toLocaleString();
  }
  kpiTriCount.textContent = (d.total_triangles || 1664).toLocaleString();

  // Trade stats
  const totalTrades = d.total_trades || 0;
  const profitTrades = d.profitable_trades || 0;
  kpiTradesCount.innerHTML = `${totalTrades} <span class="val-unit">trades</span>`;
  const winRate = totalTrades > 0 ? ((profitTrades / totalTrades) * 100).toFixed(0) : '100';
  kpiWinrate.textContent = `${winRate}% Win`;

  if (d.circuit_breaker) {
    kpiCircuit.textContent = 'TRIPPED';
    kpiCircuit.className = 'text-danger';
  } else {
    kpiCircuit.textContent = 'SAFE';
    kpiCircuit.className = 'text-emerald';
  }

  // Filter indicator
  if (d.min_profit_percent !== undefined) {
    filterMinProfitTag.textContent = `+${d.min_profit_percent.toFixed(2)}%`;
  }

  // 3. Live Triangular Spread Radar Table
  renderRadarTable(d);

  // 4. Recent Executions Log & Top Pairs Stats
  renderExecutionsTable(d.recent_execution_log);
  renderPairStats(d.recent_execution_log);
}

/**
 * Render Live Triangular Spread Radar
 */
function renderRadarTable(d) {
  const opps = d.top_opportunities || [];
  const liveSpreads = d.live_spreads || [];
  const minProfit = d.min_profit_percent || 0.05;

  // Strict deduplication by triangle ID
  const seen = new Set();
  const allMerged = [];

  // 1. Add active triggered opportunities
  for (const o of opps) {
    if (o && o.triangle && o.triangle.id && !seen.has(o.triangle.id)) {
      seen.add(o.triangle.id);
      allMerged.push(o);
    }
  }

  // 2. Add live scanned spreads
  for (const sp of liveSpreads) {
    if (sp && sp.triangle && sp.triangle.id && !seen.has(sp.triangle.id)) {
      seen.add(sp.triangle.id);
      allMerged.push(sp);
    }
  }

  if (allMerged.length === 0) {
    const ticks = d.ws_stats ? (d.ws_stats.total_messages_received || 0) : 0;
    radarCount.textContent = `Buffering: ${ticks} ticks`;
    oppsTbody.innerHTML = `
      <tr class="row-empty">
        <td colspan="6">
          <div class="empty-state-text">
            <div class="spinner-sonar"></div>
            <strong>Mengumpulkan Data Order Book Real-Time Binance...</strong>
            <div class="mt-2 text-muted" style="font-size: 0.78rem;">
              Total stream diterima: <span class="text-cyan font-mono font-weight-700">${ticks.toLocaleString()} ticks</span> | Estimasi siap: <strong class="text-emerald">1 - 3 detik</strong>
            </div>
          </div>
        </td>
      </tr>
    `;
    return;
  }

  // Count metrics for tabs
  let triggeredCount = 0;
  let monitoringCount = 0;
  for (const item of allMerged) {
    if (item.net_profit_percent >= minProfit) {
      triggeredCount++;
    } else {
      monitoringCount++;
    }
  }

  if (tabCountAll) tabCountAll.textContent = allMerged.length;
  if (tabCountTriggered) tabCountTriggered.textContent = triggeredCount;
  if (tabCountMonitoring) tabCountMonitoring.textContent = monitoringCount;

  // Apply active tab filter
  let displayList = allMerged;
  if (currentRadarFilter === 'triggered') {
    displayList = allMerged.filter(item => item.net_profit_percent >= minProfit);
  } else if (currentRadarFilter === 'monitoring') {
    displayList = allMerged.filter(item => item.net_profit_percent < minProfit);
  }

  radarCount.textContent = `${displayList.length} active routes`;

  if (displayList.length === 0) {
    if (currentRadarFilter === 'triggered') {
      oppsTbody.innerHTML = `
        <tr class="row-empty">
          <td colspan="6">
            <div class="empty-state-text">
              <span style="font-size: 1.3rem;">⚡</span>
              <div class="mt-1"><strong>Belum Ada Rute yang Menyentuh Trigger Profit (&ge; +${minProfit.toFixed(2)}%)</strong></div>
              <div class="mt-1 text-muted" style="font-size: 0.74rem;">
                Bot sedang memindai ${allMerged.length} jalur segitiga secara aktif pada kecepatan ${d.evaluations_per_sec ? d.evaluations_per_sec.toLocaleString() : '15,000+'} evals/detik.
              </div>
            </div>
          </td>
        </tr>
      `;
    } else {
      oppsTbody.innerHTML = `
        <tr class="row-empty">
          <td colspan="6">
            <div class="empty-state-text">Tidak ada data rute untuk filter ini.</div>
          </td>
        </tr>
      `;
    }
    return;
  }

  let html = '';
  displayList.slice(0, 50).forEach((item) => {
    if (!item || !item.triangle) return;

    const t = item.triangle;
    const isTriggered = item.net_profit_percent >= minProfit;
    const netPct = item.net_profit_percent || 0;
    const grossPct = item.gross_profit_percent || 0;
    const profitUSDT = item.net_profit_usdt || 0;
    const netColorClass = netPct > 0 ? (isTriggered ? 'text-emerald font-weight-700' : 'text-cyan') : 'text-muted';

    const statusBadge = isTriggered
      ? `<span class="status-badge-triggered">⚡ EXECUTION TRIGGER</span>`
      : `<span class="status-badge-monitoring">MONITORING</span>`;

    html += `
      <tr class="${isTriggered ? 'row-highlight' : ''}">
        <td>
          <div class="route-cell">
            <span class="badge-tag">USDT</span>
            <span class="route-arrow">&rarr;</span>
            <span class="badge-tag tag-asset">${t.asset_a}</span>
            <span class="route-arrow">&rarr;</span>
            <span class="badge-tag tag-asset">${t.asset_b}</span>
            <span class="route-arrow">&rarr;</span>
            <span class="badge-tag">USDT</span>
          </div>
        </td>
        <td class="font-mono text-secondary">${grossPct >= 0 ? '+' : ''}${grossPct.toFixed(3)}%</td>
        <td class="font-mono ${netColorClass}">${netPct >= 0 ? '+' : ''}${netPct.toFixed(3)}%</td>
        <td class="font-mono ${profitUSDT >= 0 ? 'text-emerald' : 'text-danger'}">${profitUSDT >= 0 ? '+' : ''}$${profitUSDT.toFixed(4)}</td>
        <td>${statusBadge}</td>
        <td class="font-mono text-muted">${item.latency_ms || 0} ms</td>
      </tr>
    `;
  });

  oppsTbody.innerHTML = html;
}

/**
 * Render Executions Log Table
 */
function renderExecutionsTable(logs) {
  if (!logs || logs.length === 0) {
    execTbody.innerHTML = `
      <tr class="row-empty">
        <td colspan="6">
          <div class="empty-state-text">No trade executions logged yet in this session.</div>
        </td>
      </tr>
    `;
    return;
  }

  let html = '';
  // Sort by timestamp descending (most recent first)
  [...logs].sort((a, b) => new Date(b.executed_at) - new Date(a.executed_at)).forEach((log) => {
    const opp = log.opportunity || {};
    const t = opp.triangle || {};
    const timeStr = log.executed_at ? new Date(log.executed_at).toLocaleTimeString() : '--:--:--';
    const isWin = (opp.net_profit_usdt || 0) > 0;
    const modeBadgeClass = log.mode === 'live' ? 'badge-pill bg-danger-soft' : 'badge-pill bg-info-soft';

    html += `
      <tr>
        <td class="font-mono text-muted">${timeStr}</td>
        <td>
          <div class="route-cell">
            <span class="badge-tag">USDT</span>&rarr;<span>${t.asset_a || 'A'}</span>&rarr;<span>${t.asset_b || 'B'}</span>&rarr;<span class="badge-tag">USDT</span>
          </div>
        </td>
        <td class="font-mono ${isWin ? 'text-emerald' : 'text-danger'}">+${(opp.net_profit_percent || 0).toFixed(3)}%</td>
        <td class="font-mono ${isWin ? 'text-emerald' : 'text-danger'}">+$${(opp.net_profit_usdt || 0).toFixed(4)}</td>
        <td class="font-mono text-muted">${log.execution_time_ns ? (log.execution_time_ns / 1000000).toFixed(1) : '<1'} ms</td>
        <td>
          <span class="${modeBadgeClass}">${(log.mode || 'paper').toUpperCase()}</span>
          ${log.is_success ? '<span class="text-emerald">✔ FILLED</span>' : `<span class="text-danger">✖ ${log.error_message || 'FAILED'}</span>`}
        </td>
      </tr>
    `;
  });

  execTbody.innerHTML = html;
}

/**
 * Render Top Successful Arbitraged Pairs Bar Chart
 */
function renderPairStats(logs) {
  const pairStatWinrate = document.getElementById('pair-stat-winrate');
  const pairChartEmpty = document.getElementById('pair-chart-empty');
  const pairChartBars = document.getElementById('pair-chart-bars');

  if (!logs || logs.length === 0) {
    if (pairStatWinrate) pairStatWinrate.textContent = '100%';
    if (pairChartEmpty) pairChartEmpty.style.display = 'flex';
    if (pairChartBars) pairChartBars.style.display = 'none';
    return;
  }

  // Aggregate winning trades by triangular pair
  const pairMap = {};
  let totalProfit = 0;
  let successfulFills = 0;

  logs.forEach((log) => {
    if (log.is_success) {
      successfulFills++;
      const opp = log.opportunity || {};
      const t = opp.triangle || {};
      const routeKey = t.id || `${t.base_asset || 'USDT'}→${t.asset_a || 'A'}→${t.asset_b || 'B'}`;
      const pnl = log.actual_net_profit_usdt || opp.net_profit_usdt || 0;
      totalProfit += pnl;

      if (!pairMap[routeKey]) {
        pairMap[routeKey] = {
          routeKey,
          assetA: t.asset_a || 'A',
          assetB: t.asset_b || 'B',
          wins: 0,
          totalProfit: 0,
        };
      }
      pairMap[routeKey].wins++;
      pairMap[routeKey].totalProfit += pnl;
    }
  });

  const pairList = Object.values(pairMap).sort((a, b) => b.wins - a.wins || b.totalProfit - a.totalProfit);
  const winRate = logs.length > 0 ? ((successfulFills / logs.length) * 100).toFixed(0) : 100;

  if (pairStatWinrate) pairStatWinrate.textContent = `${winRate}%`;

  if (pairList.length === 0) {
    if (pairChartEmpty) pairChartEmpty.style.display = 'flex';
    if (pairChartBars) pairChartBars.style.display = 'none';
    return;
  }

  // Find max wins for proportional bar length
  const maxWins = Math.max(...pairList.map((p) => p.wins), 1);
  const topPairs = pairList.slice(0, 10);

  let html = '';
  topPairs.forEach((p, idx) => {
    const widthPct = Math.max(18, Math.round((p.wins / maxWins) * 100));
    html += `
      <div class="pair-bar-row">
        <div class="pair-bar-label" title="${p.routeKey}">
          <span class="rank-num">#${idx + 1}</span> ${p.assetA}→${p.assetB}
        </div>
        <div class="pair-bar-track">
          <div class="pair-bar-fill" style="width: ${widthPct}%;"></div>
        </div>
        <div class="pair-bar-val">
          +$${p.totalProfit.toFixed(3)} <span class="win-count">(${p.wins}x)</span>
        </div>
      </div>
    `;
  });

  if (pairChartBars) {
    pairChartBars.innerHTML = html;
    pairChartBars.style.display = 'flex';
  }
  if (pairChartEmpty) {
    pairChartEmpty.style.display = 'none';
  }
}

// Global Window Helpers for Mode, Modal & Tabs
window.setModePaper = function() {
  setModeUI('paper');
  updateRuntimeConfig({ trading_mode: 'paper' });
  showToast('Mode simulasi (Paper Trading) aktif', 'info');
};

window.openLiveAuthModal = function() {
  if (liveAccountData && liveAccountData.canTrade) {
    setModeUI('live');
    updateRuntimeConfig({ trading_mode: 'live' });
    showToast('⚠️ LIVE Binance Trading AKTIF: Real orders dieksekusi di Binance Spot!', 'warning');
    return;
  }
  const modal = document.getElementById('modal-live-auth');
  const alertEl = document.getElementById('modal-auth-alert');
  if (alertEl) alertEl.style.display = 'none';
  if (modal) {
    modal.style.display = 'flex';
    const keyInput = document.getElementById('input-api-key');
    if (keyInput) setTimeout(() => keyInput.focus(), 50);
  }
};

window.closeLiveAuthModal = function() {
  const modal = document.getElementById('modal-live-auth');
  if (modal) modal.style.display = 'none';
  if (!liveAccountData || !liveAccountData.canTrade) {
    setModeUI('paper');
  }
};

window.toggleKeyVis = function(type) {
  const kIn = document.getElementById('input-api-key');
  const sIn = document.getElementById('input-api-secret');
  const kBtn = document.getElementById('btn-toggle-key');
  const sBtn = document.getElementById('btn-toggle-secret');
  if (type === 'key' && kIn && kBtn) {
    toggleInputVisibility(kIn, kBtn);
  } else if (sIn && sBtn) {
    toggleInputVisibility(sIn, sBtn);
  }
};

window.verifyAndActivateLive = async function() {
  const kIn = document.getElementById('input-api-key');
  const sIn = document.getElementById('input-api-secret');
  const modalVerifyBtn = document.getElementById('btn-modal-verify');
  const modalVerifyTxt = document.getElementById('modal-verify-text');
  const modalAlert = document.getElementById('modal-auth-alert');

  const key = kIn ? kIn.value.trim() : '';
  const secret = sIn ? sIn.value.trim() : '';

  if (!key || !secret) {
    if (modalAlert) {
      modalAlert.className = 'modal-auth-alert alert-error';
      modalAlert.innerHTML = '⚠️ Harap masukkan Binance API Key dan Secret Key lengkap.';
      modalAlert.style.display = 'block';
    }
    return;
  }

  if (modalVerifyBtn) modalVerifyBtn.disabled = true;
  if (modalVerifyTxt) modalVerifyTxt.textContent = 'Memverifikasi ke Binance...';
  if (modalAlert) {
    modalAlert.className = 'modal-auth-alert alert-loading';
    modalAlert.innerHTML = '⏳ Menghubungi API Binance dan memverifikasi izin Spot Trading...';
    modalAlert.style.display = 'block';
  }

  try {
    const resp = await fetch('/api/binance-auth', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ api_key: key, api_secret: secret }),
    });

    const res = await resp.json();
    if (res.success && res.account) {
      liveAccountData = res.account;

      if (modalAlert) {
        modalAlert.className = 'modal-auth-alert alert-success';
        modalAlert.innerHTML = `✅ Terverifikasi! Saldo Spot: <strong>$${(res.account.USDTBalance || 0).toFixed(2)} USDT</strong> | BNB: <strong>${(res.account.BNBBalance || 0).toFixed(4)} BNB</strong>`;
      }

      if (liveUsdtBal) liveUsdtBal.textContent = `$${(res.account.USDTBalance || 0).toFixed(2)} USDT`;
      if (liveBnbBal) liveBnbBal.textContent = `${(res.account.BNBBalance || 0).toFixed(4)} BNB`;
      if (liveCanTrade) liveCanTrade.textContent = res.account.canTrade ? 'ENABLED (SPOT)' : 'DISABLED';

      // Delay slightly so user sees success message, then activate Live mode
      setTimeout(async () => {
        const modal = document.getElementById('modal-live-auth');
        if (modal) modal.style.display = 'none';
        setModeUI('live');
        await updateRuntimeConfig({
          trading_mode: 'live',
          binance_api_key: key,
          binance_api_secret: secret,
        });
        showToast('🚀 Live Binance Trading BERHASIL DIAKTIFKAN!', 'success');
      }, 700);
    } else {
      if (modalAlert) {
        modalAlert.className = 'modal-auth-alert alert-error';
        modalAlert.innerHTML = `❌ Verifikasi Gagal: ${res.error || 'API Key / Secret salah atau IP tidak diizinkan.'}`;
        modalAlert.style.display = 'block';
      }
      setModeUI('paper');
    }
  } catch (err) {
    if (modalAlert) {
      modalAlert.className = 'modal-auth-alert alert-error';
      modalAlert.innerHTML = '❌ Gagal terhubung ke engine backend. Silakan coba lagi.';
      modalAlert.style.display = 'block';
    }
    setModeUI('paper');
  } finally {
    if (modalVerifyBtn) modalVerifyBtn.disabled = false;
    if (modalVerifyTxt) modalVerifyTxt.textContent = 'Verifikasi & Aktifkan Live Mode';
  }
};

/**
 * Event Listeners & UI Controls
 */
function setupEventListeners() {
  // Reconnect Button
  if (btnReconnect) {
    btnReconnect.addEventListener('click', () => {
      connectWebSocket();
      showToast('Reconnecting WebSocket stream...', 'info');
    });
  }

  // Clear Executions Table
  if (btnClearTable) {
    btnClearTable.addEventListener('click', () => {
      execTbody.innerHTML = `
        <tr class="row-empty">
          <td colspan="6">
            <div class="empty-state-text">Execution log cleared from view.</div>
          </td>
        </tr>
      `;
      showToast('Execution view cleared', 'info');
    });
  }

  // Fallback DOM event listeners
  if (btnModePaper) btnModePaper.addEventListener('click', window.setModePaper);
  if (btnModeLive) btnModeLive.addEventListener('click', window.openLiveAuthModal);
  if (btnModalCancel) btnModalCancel.addEventListener('click', window.closeLiveAuthModal);
  if (btnModalVerify) btnModalVerify.addEventListener('click', window.verifyAndActivateLive);
  if (btnReconfigureKeys) btnReconfigureKeys.addEventListener('click', window.openLiveAuthModal);

  // Toggle Visibility Eye Buttons
  if (btnToggleKey) {
    btnToggleKey.addEventListener('click', () => {
      toggleInputVisibility(inputApiKey, btnToggleKey);
    });
  }

  if (btnToggleSecret) {
    btnToggleSecret.addEventListener('click', () => {
      toggleInputVisibility(inputApiSecret, btnToggleSecret);
    });
  }

  // Parameters Form Submit
  if (formConfig) {
    formConfig.addEventListener('submit', async (e) => {
      e.preventDefault();

      const payload = {
        trading_mode: currentMode,
        trade_amount_usdt: parseFloat(inputCapital.value),
        min_profit_percent: parseFloat(inputMinProfit.value),
        fee_rate: parseFloat(inputFeeRate.value),
        use_bnb_discount: inputBnbDiscount.checked,
        max_latency_ms: parseInt(inputMaxLatency.value, 10),
        max_tracked_triangles: inputTrackedTriangles ? parseInt(inputTrackedTriangles.value, 10) : 350,
        radar_display_limit: inputRadarLimit ? parseInt(inputRadarLimit.value, 10) : 50,
        max_slippage_tolerance: inputMaxSlippage ? (parseFloat(inputMaxSlippage.value) / 100) : 0.001,
        max_daily_loss_usdt: inputMaxDailyLoss ? parseFloat(inputMaxDailyLoss.value) : 50.0,
      };

      if (inputApiKey.value.trim() && inputApiSecret.value.trim()) {
        payload.binance_api_key = inputApiKey.value.trim();
        payload.binance_api_secret = inputApiSecret.value.trim();
      }

      await updateRuntimeConfig(payload);
    });
  }

  // Test Mock Execution Button
  if (btnTestTrade) {
    btnTestTrade.addEventListener('click', async () => {
      btnTestTrade.disabled = true;
      btnTestTrade.textContent = '⏳ Executing...';
      try {
        const resp = await fetch('/api/test-execution', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
        });
        const res = await resp.json();
        if (res.success && res.result) {
          const r = res.result;
          const opp = r.opportunity || {};
          const t = opp.triangle || {};
          showToast(`✅ Simulasi Eksekusi Berhasil! Rute: ${t.id || 'USDT->BTC->ETH->USDT'} | Profit: +$${(r.actual_net_profit_usdt || 0).toFixed(4)}`, 'success');
        } else {
          showToast(`⚠️ Eksekusi Ditolak: ${res.error || 'Unknown'}`, 'warning');
        }
      } catch (err) {
        showToast(`❌ Error: ${err.message}`, 'error');
      } finally {
        btnTestTrade.disabled = false;
        btnTestTrade.textContent = '🧪 Test Mock Execution';
      }
    });
  }

  // Clear Log Table Button
  if (btnClearTable) {
    btnClearTable.addEventListener('click', async () => {
      try {
        await fetch('/api/clear-log', { method: 'POST' });
        execTbody.innerHTML = `
          <tr class="row-empty">
            <td colspan="6">
              <div class="empty-state-text">No trade executions logged yet in this session.</div>
            </td>
          </tr>
        `;
        showToast('Riwayat eksekusi berhasil dibersihkan', 'info');
      } catch (err) {
        console.error(err);
      }
    });
  }

  // Radar Tab Filters (All, Triggered, Monitoring)
  window.setRadarFilterTab = function(filterMode) {
    currentRadarFilter = filterMode;
    const allTabBtns = document.querySelectorAll('.radar-tab-btn');
    allTabBtns.forEach((btn) => {
      if (btn.getAttribute('data-filter') === filterMode) {
        btn.classList.add('active');
      } else {
        btn.classList.remove('active');
      }
    });
    if (window.latestDashboardData) {
      renderRadarTable(window.latestDashboardData);
    }
  };

  if (tabRadarAll) tabRadarAll.addEventListener('click', () => window.setRadarFilterTab('all'));
  if (tabRadarTriggered) tabRadarTriggered.addEventListener('click', () => window.setRadarFilterTab('triggered'));
  if (tabRadarMonitoring) tabRadarMonitoring.addEventListener('click', () => window.setRadarFilterTab('monitoring'));
}

function toggleInputVisibility(inputEl, btnEl) {
  if (inputEl.type === 'password') {
    inputEl.type = 'text';
    btnEl.textContent = '🔒';
  } else {
    inputEl.type = 'password';
    btnEl.textContent = '👁';
  }
}

function setModeUI(mode) {
  currentMode = mode;
  if (mode === 'live') {
    btnModeLive.classList.add('active');
    btnModePaper.classList.remove('active');
    if (binanceApiCard) binanceApiCard.style.display = 'block';
  } else {
    btnModePaper.classList.add('active');
    btnModeLive.classList.remove('active');
    if (binanceApiCard) binanceApiCard.style.display = 'none';
  }
}

async function updateRuntimeConfig(payload) {
  try {
    const res = await fetch('/api/config', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });

    const data = await res.json();
    if (data.success) {
      showToast('Engine parameters applied successfully!', 'success');
    } else {
      showToast(data.message || 'Failed to update configuration', 'error');
    }
  } catch (err) {
    showToast('Failed to reach engine API', 'error');
  }
}

/**
 * Toast Notification Helper
 */
function showToast(message, type = 'info') {
  const toast = document.createElement('div');
  toast.className = 'toast';

  let icon = 'ℹ️';
  if (type === 'success') icon = '✅';
  if (type === 'error') icon = '❌';
  if (type === 'warning') icon = '⚠️';

  toast.innerHTML = `<span>${icon}</span><span>${message}</span>`;
  toastContainer.appendChild(toast);

  setTimeout(() => {
    toast.style.transition = 'opacity 0.3s ease, transform 0.3s ease';
    toast.style.opacity = '0';
    toast.style.transform = 'translateY(10px)';
    setTimeout(() => toast.remove(), 300);
  }, 4000);
}

// Boot up
document.addEventListener('DOMContentLoaded', init);
