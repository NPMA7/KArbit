# KArbit: High-Frequency Binance Triangular Arbitrage Engine

**KArbit** adalah bot arbitrase segitiga (*Triangular Arbitrage*) berkinerja tinggi (*High-Frequency / Low-Latency*) yang dibangun khusus untuk **Binance Spot Market** menggunakan bahasa **Go**.

---

## ⚡ Fitur Utama

1. **True Event-Driven WebSocket Streaming:**
   - Terhubung langsung ke Binance Combined Stream `wss://stream.binance.com:9443/ws/!bookTicker`.
   - Mengonsumsi pembaruan harga *Best Bid / Best Ask* & volume likuiditas dari seluruh pasangan secara serentak (*zero-polling overhead*).
2. **Dynamic 3-Leg Cycle Graph:**
   - Memetakan secara otomatis seluruh kemungkinan siklus 3-kaki dari mata uang dasar (**USDT** atau lainnya) saat startup dari Binance `exchangeInfo`.
   - Menghasilkan 500+ jalur segitiga terindeks $O(1)$ untuk evaluasi instan.
3. **Advanced Risk & Math Engine:**
   - **Potongan Fee Maker/Taker & Diskon BNB:** Menghitung potongan fee $0.075\%$ (BNB) atau $0.1\%$ pada setiap 3 kaki transaksi.
   - **Slippage & Depth Liquidity Guard:** Memeriksa apakah ukuran order melebihi volume order book teratas (`AskQty` / `BidQty`).
   - **Stale Quote Discarder:** Membuang data harga jika latensi/umur paket bursa melebihi ambang batas (default $> 50\text{ ms}$).
   - **Precision & Filter Enforcer:** Membulatkan kuantitas sesuai aturan `LOT_SIZE` (*stepSize*), `PRICE_FILTER` (*tickSize*), dan memvalidasi `MIN_NOTIONAL`.
4. **Dual Execution Mode:**
   - **Paper Trading Mode (Default):** Simulasi eksekusi tanpa risiko dengan pencatatan PnL, slippage, dan saldo virtual secara akurat.
   - **Live Execution Ready:** Dukungan pengiriman order live dengan tipe **IOC (Immediate-or-Cancel)** dan penandatanganan HMAC-SHA256.
5. **Interactive Terminal TUI Dashboard:**
   - Monitor real-time berkecepatan tinggi menampilkan status WebSocket, ping bursa (ms), throughput evaluasi/detik, daftar peluang teratas, dan log transaksi.

---

## 📐 Rumus Matematika Triangular Arbitrage

Siklus Arbitrase Segitiga dengan modal dasar **USDT** (Contoh: `USDT -> BTC -> ETH -> USDT`):

1. **Leg 1 (Beli BTC dengan USDT):**
   $$Q_{\text{BTC}} = \lfloor \frac{\text{Modal USDT}}{\text{Ask}_{\text{BTCUSDT}}} / \text{stepSize}_1 \rfloor \times \text{stepSize}_1 \times (1 - \text{Fee})$$
2. **Leg 2 (Beli ETH dengan BTC via pasar ETHBTC):**
   $$Q_{\text{ETH}} = \lfloor \frac{Q_{\text{BTC}}}{\text{Ask}_{\text{ETHBTC}}} / \text{stepSize}_2 \rfloor \times \text{stepSize}_2 \times (1 - \text{Fee})$$
3. **Leg 3 (Jual ETH ke USDT via pasar ETHUSDT):**
   $$\text{Final USDT} = \lfloor Q_{\text{ETH}} / \text{stepSize}_3 \rfloor \times \text{stepSize}_3 \times \text{Bid}_{\text{ETHUSDT}} \times (1 - \text{Fee})$$

$$\text{Net Profit (\%)} = \frac{\text{Final USDT} - \text{Modal USDT}}{\text{Modal USDT}} \times 100\%$$

---

## 🌐 Web GUI Dashboard & PM2 Management

KArbit dilengkapi dengan **Web Dashboard UI** modern bertema *Dark Trading Terminal* dan server WebSocket real-time terintegrasi:

- **Akses Web Dashboard:** Buka browser di `http://localhost:8080` (atau `http://IP_SERVER:8080`)
- **Fitur Web GUI:**
  - Pemantauan real-time status WebSocket stream & ping latensi (ms).
  - Kartu KPI: Cumulative PnL ($ & %), Wallet Balance, Throughput (evals/s & ticks/s), Win Rate %.
  - Tabel *Live Triangular Spread Radar* & riwayat eksekusi *Trade Log*.
  - Panel kontrol interaktif: Mengubah modal per siklus, ambang batas profit minimal, fee rate, dan toggle Paper/Live mode secara langsung (*on-the-fly*) tanpa perlu restart engine!

### 🔄 Menjalankan dengan PM2 (Background Daemon)

Program telah dikonfigurasi dengan [ecosystem.config.js](file:///var/www/KArbit/ecosystem.config.js):

```bash
# Menjalankan KArbit di background via PM2
pm2 start ecosystem.config.js

# Melihat status proses
pm2 status

# Melihat log real-time
pm2 logs karbit

# Merestart bot
pm2 restart karbit

# Menghentikan bot
pm2 stop karbit
```

---

## 🚀 Cara Menjalankan Manual (CLI)

### 1. Build Binary
```bash
go build -ldflags="-s -w" -o karbit ./cmd/karbit
```

### 2. Jalankan Mode Paper Trading (Simulasi)
```bash
# Menjalankan dengan Web GUI di port 8080
./karbit -mode paper -capital 100 -min-profit 0.05 -web-port 8080
```

### 3. Jalankan Mode Live Trading (Real Orders)
```bash
# Pastikan API Key & Secret telah diisi di config.json atau via ENV
export BINANCE_API_KEY="your_api_key"
export BINANCE_API_SECRET="your_api_secret"

./karbit -mode live -capital 50 -min-profit 0.08
```

---

## ⚙️ Parameter Konfigurasi (`config.json`)

| Parameter | Default | Keterangan |
| :--- | :--- | :--- |
| `base_currency` | `"USDT"` | Mata uang dasar arbitrase (`USDT`, `FDUSD`, `USDC`) |
| `trading_mode` | `"paper"` | Mode operasi: `"paper"` (simulasi) atau `"live"` (order riil) |
| `trade_amount_usdt` | `100.0` | Modal per siklus arbitrase (USDT) |
| `min_profit_percent` | `0.05` | Ambang batas profit bersih minimal ($+0.05\%$) setelah semua fee |
| `fee_rate` | `0.00075` | Potongan fee per kaki ($0.075\%$ untuk diskon BNB) |
| `use_bnb_discount` | `true` | Apakah menggunakan diskon potongan BNB |
| `max_latency_ms` | `50` | Batas maksimal usia quote (ms) sebelum didiskualifikasi |
| `max_slippage_tolerance` | `0.001` | Toleransi slippage maksimal ($0.1\%$) |
| `max_daily_loss_usdt` | `50.0` | Batas circuit breaker kerugian harian |
| `worker_count` | `8` | Jumlah worker goroutine evaluator paralel |
| `dashboard_refresh_ms` | `100` | Kecepatan refresh antarmuka Terminal TUI |

---

## 🧪 Menjalankan Pengujian (Unit & Benchmark Tests)

```bash
# Unit Tests & Race Condition Detection
go test -v -race ./...

# Benchmark Throughput Evaluator
go test -bench=. -benchmem ./internal/engine/
```

---

## 🔒 Manajemen Risiko Terintegrasi
- **Circuit Breaker:** Menghentikan eksekusi otomatis jika batas kerugian harian tercapai.
- **Atomic Depth Check:** Memastikan likuiditas harga terbaik di order book cukup untuk ukuran order.
- **Execution Cooldown:** Mencegah *double-execution* pada fluktuasi harga dalam tick yang sama.
- **Stale Quote Filter:** Mencegah eksekusi terhadap kuotasi lama yang berisiko *slippage phantom*.
