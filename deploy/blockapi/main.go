package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// blockapi 是 BEpusdt 订单回溯接口的兼容自建实现。
//
//	GET /api/block/get_boundary_heights?network=tron&start=<unix>&end=<unix>
//	→ {"start":<区块号>,"end":<区块号>}
//
// 通过 eth 兼容 JSON-RPC 把时间戳映射为区块高度（Tron 也支持该协议），
// 不依赖官方 api.bepusdt.online。不支持的网络返回 0,0，BEpusdt 会跳过该次回溯。

type boundaryResp struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

type chainCfg struct {
	name string
	url  string
	key  string
	gap  time.Duration
}

type headInfo struct {
	number int64
	ts     int64 // unix 秒
	avg    float64
	at     time.Time
}

var (
	chains    = map[string]chainCfg{}
	httpCli   = &http.Client{Timeout: 10 * time.Second}
	headMu    sync.Mutex
	headCache = map[string]headInfo{}
	paceMu    sync.Mutex
	paceLast  = map[string]time.Time{}
	tsMu      sync.Mutex
	tsCache   = map[string]map[int64]int64{}
)

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}

	return def
}

func initChains() {
	// Tron 走公共 JSON-RPC，避免与 BEpusdt 扫块共用 TronGrid 的 3 rps 配额。
	chains["tron"] = chainCfg{
		name: "tron",
		url:  env("TRON_RPC", "https://tron-rpc.publicnode.com/jsonrpc"),
		key:  strings.TrimSpace(os.Getenv("TRON_API_KEY")),
		gap:  150 * time.Millisecond,
	}

	evmDefaults := map[string]string{
		"ethereum": env("EVM_RPC_ETHEREUM", "https://ethereum-rpc.publicnode.com"),
		"bsc":      env("EVM_RPC_BSC", "https://bsc-rpc.publicnode.com"),
		"polygon":  env("EVM_RPC_POLYGON", "https://polygon-bor-rpc.publicnode.com"),
		"arbitrum": env("EVM_RPC_ARBITRUM", "https://arb1.arbitrum.io/rpc"),
		"base":     env("EVM_RPC_BASE", "https://base-rpc.publicnode.com"),
		"xlayer":   env("EVM_RPC_XLAYER", "https://xlayerrpc.okx.com/"),
		"plasma":   env("EVM_RPC_PLASMA", "https://rpc.plasma.to/"),
	}
	for name, url := range evmDefaults {
		chains[name] = chainCfg{name: name, url: url, gap: 120 * time.Millisecond}
	}
}

// pace 按网络串行化并限速外部 RPC 调用。
func pace(name string, gap time.Duration) {
	paceMu.Lock()
	defer paceMu.Unlock()

	if d := gap - time.Since(paceLast[name]); d > 0 {
		time.Sleep(d)
	}
	paceLast[name] = time.Now()
}

func postJSON(ctx context.Context, url string, payload any, headers map[string]string, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := httpCli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}

	return json.Unmarshal(raw, out)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}

	return s[:n]
}

type rpcResp struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func rpcHeaders(c chainCfg) map[string]string {
	h := map[string]string{}
	if c.key != "" {
		h["TRON-PRO-API-KEY"] = c.key
	}

	return h
}

// rpc 调用 eth 兼容 JSON-RPC，短暂限流（429）时退避重试一次。
func rpc(ctx context.Context, c chainCfg, method string, params []any, out any) error {
	payload := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params}

	call := func() (rpcResp, error) {
		var r rpcResp
		pace(c.name, c.gap)

		return r, postJSON(ctx, c.url, payload, rpcHeaders(c), &r)
	}

	r, err := call()
	if err != nil && strings.Contains(err.Error(), "http 429") {
		time.Sleep(2 * time.Second)
		r, err = call()
	}
	if err != nil {
		return err
	}
	if r.Error != nil {
		return fmt.Errorf("rpc %s: %s", method, r.Error.Message)
	}

	return json.Unmarshal(r.Result, out)
}

func hexToInt64(s string) (int64, error) {
	return strconv.ParseInt(strings.TrimPrefix(s, "0x"), 16, 64)
}

func chainHead(ctx context.Context, c chainCfg) (int64, int64, error) {
	var numHex string
	if err := rpc(ctx, c, "eth_blockNumber", []any{}, &numHex); err != nil {
		return 0, 0, err
	}

	num, err := hexToInt64(numHex)
	if err != nil {
		return 0, 0, err
	}

	ts, err := blockTs(ctx, c, num)
	if err != nil {
		return 0, 0, err
	}

	return num, ts, nil
}

func blockTs(ctx context.Context, c chainCfg, num int64) (int64, error) {
	if ts, ok := cachedTs(c.name, num); ok {
		return ts, nil
	}

	var blk struct {
		Timestamp string `json:"timestamp"`
	}
	if err := rpc(ctx, c, "eth_getBlockByNumber", []any{fmt.Sprintf("0x%x", num), false}, &blk); err != nil {
		return 0, err
	}
	if blk.Timestamp == "" {
		return 0, fmt.Errorf("%s block %d 不存在", c.name, num)
	}

	ts, err := hexToInt64(blk.Timestamp)
	if err != nil {
		return 0, err
	}
	storeTs(c.name, num, ts)

	return ts, nil
}

func cachedTs(network string, num int64) (int64, bool) {
	tsMu.Lock()
	defer tsMu.Unlock()
	if m, ok := tsCache[network]; ok {
		ts, ok := m[num]

		return ts, ok
	}

	return 0, false
}

func storeTs(network string, num, ts int64) {
	tsMu.Lock()
	defer tsMu.Unlock()
	m, ok := tsCache[network]
	if !ok || len(m) > 20000 {
		m = make(map[int64]int64, 256)
		tsCache[network] = m
	}
	m[num] = ts
}

// info 返回链头信息，30 秒内复用，避免一次请求内重复拉取。
func info(ctx context.Context, c chainCfg) (headInfo, error) {
	headMu.Lock()
	h, ok := headCache[c.name]
	headMu.Unlock()
	if ok && time.Since(h.at) < 30*time.Second && h.avg > 0 {
		return h, nil
	}

	headNum, headTs, err := chainHead(ctx, c)
	if err != nil {
		return headInfo{}, err
	}

	// 用 2000 块前的样本估算平均出块间隔
	avg := 0.0
	sample := headNum - 2000
	if sample > 0 {
		if ts, e := blockTs(ctx, c, sample); e == nil && headTs > ts {
			avg = float64(headTs-ts) / float64(headNum-sample)
		}
	}
	if avg <= 0 {
		avg = 3
	}

	h = headInfo{number: headNum, ts: headTs, avg: avg, at: time.Now()}
	headMu.Lock()
	headCache[c.name] = h
	headMu.Unlock()

	return h, nil
}

// findBlock 通过插值 + 牛顿迭代定位目标时间，并向下/向上留出安全余量，
// 宁可多重扫少量区块，也不能漏扫。
func findBlock(ctx context.Context, c chainCfg, h headInfo, target int64) (int64, int64, error) {
	if target >= h.ts {
		return h.number, h.number, nil
	}

	guess := int64(math.Round(float64(h.number) - float64(h.ts-target)/h.avg))
	if guess < 0 {
		guess = 0
	}
	if guess > h.number {
		guess = h.number
	}

	for i := 0; i < 4; i++ {
		ts, err := blockTs(ctx, c, guess)
		if err != nil {
			return 0, 0, err
		}

		diff := target - ts
		if diff >= -1 && diff <= 1 {
			break
		}

		step := int64(math.Round(float64(diff) / h.avg))
		if step == 0 {
			if diff > 0 {
				step = 1
			} else {
				step = -1
			}
		}
		guess += step

		if guess < 0 {
			guess = 0
		}
		if guess >= h.number {
			guess = h.number

			break
		}
	}

	// 按与链头的距离留 1% + 20 个区块余量（Tron 3 小时约 3600 块 → 约 56 块）
	margin := int64(math.Abs(float64(h.number-guess))*0.01) + 20
	if margin > 600 {
		margin = 600
	}

	lo := guess - margin
	if lo < 0 {
		lo = 0
	}
	hi := guess + margin
	if hi > h.number {
		hi = h.number
	}

	return lo, hi, nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func handleBoundary(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	q := r.URL.Query()
	network := strings.ToLower(strings.TrimSpace(q.Get("network")))
	startAt, err1 := strconv.ParseInt(q.Get("start"), 10, 64)
	endAt, err2 := strconv.ParseInt(q.Get("end"), 10, 64)

	if err1 != nil || err2 != nil || startAt <= 0 || endAt < startAt {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid params"})

		return
	}

	now := time.Now().Unix()
	if endAt > now {
		endAt = now
	}

	cfg, ok := chains[network]
	if !ok {
		writeJSON(w, http.StatusOK, boundaryResp{})

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	h, err := info(ctx, cfg)
	if err != nil {
		log.Printf("[%s] head failed: %v", network, err)
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})

		return
	}

	sb, _, err := findBlock(ctx, cfg, h, startAt)
	if err != nil {
		log.Printf("[%s] start=%d failed: %v", network, startAt, err)
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})

		return
	}

	_, eb, err := findBlock(ctx, cfg, h, endAt)
	if err != nil {
		log.Printf("[%s] end=%d failed: %v", network, endAt, err)
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})

		return
	}

	if eb < sb {
		eb = sb
	}
	if eb > h.number {
		eb = h.number
	}

	log.Printf("[%s] %d..%d -> blocks %d..%d (head=%d, %s)", network, startAt, endAt, sb, eb, h.number, time.Since(start).Round(time.Millisecond))
	writeJSON(w, http.StatusOK, boundaryResp{Start: sb, End: eb})
}

func main() {
	initChains()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/block/get_boundary_heights", handleBoundary)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	addr := env("LISTEN", ":8080")
	log.Printf("blockapi listening on %s, networks=%d", addr, len(chains))
	log.Fatal(http.ListenAndServe(addr, mux))
}
