package pnb

import (
	"math"
	"strconv"
)

// ProbnodeExt (0x14, record set version 4) is the `probnode` object of one book event or one print
// (owner, 2026-11-22, the approved contract): every metric's value against the norms of our archive
// - x_norm, the percentile among similar markets, the percentile against the market's own last 30
// minutes - and, for the risk factors, what the mid did over the next minute on similar markets at
// that percentile. Risk is statistics, never a verdict. It goes out right before the BookExt /
// TradeExt it belongs to (same Ref), is stored with them (D2: enrichment stored as Probe records)
// and is joined into book+ / trades+.
type ProbnodeExt struct {
	Header
	TsRecv  uint64
	Ref     uint32 // the BookExt's seq (Kind book) or the trade's seq (Kind trade)
	Kind    uint8  // PNKindBook | PNKindTrade
	Flag    uint8  // security: PNFlagNone | PNFlagAttack (a confirmed case names the market)
	Open    uint8  // security episodes open on the market
	Bucket  [32]byte
	Version [16]byte // the norms' version
	M       [PNMetrics]PNMetric
	After   [PNFactors]PNAfter
}

// PNMetric is one metric: value x1e6 (Missing), x_norm x1e4 (PNNone), percentiles 0..100 (255 none).
type PNMetric struct {
	Value   int64
	XNorm   int32
	PBucket uint8
	PSelf   uint8
}

// PNAfter is the next minute's absolute mid move at the factor's percentile, price x1e6 (PNNone).
type PNAfter struct{ P50, P90 int32 }

const (
	RProbnodeExt    uint8 = 0x14
	PNMetrics             = 19 // = probnode.NMetrics
	PNFactors             = 9  // = probnode.NFactors
	PNKindBook      uint8 = 0
	PNKindTrade     uint8 = 1
	PNFlagNone      uint8 = 0
	PNFlagAttack    uint8 = 1
	PNNone                = int32(math.MinInt32)
	PNNoPct         uint8 = 255
	ProbnodeExtSize       = 80 + PNMetrics*16 + PNFactors*8 // 456
)

// PNNames are the metrics' names, in record order (probnode.Names).
var PNNames = [PNMetrics]string{
	"spread", "depth_bid", "depth_ask", "imbalance", "microprice_lean", "updates_per_s",
	"volume_1m", "trades_1m", "taker_imbalance_1m", "avg_trade",
	"cost_to_trade.buy", "cost_to_trade.sell", "depth", "resilience", "volatility.10s", "volatility.1m",
	"adverse_selection", "quote_stability", "resolution_jump",
}

func (r *ProbnodeExt) Hdr() *Header { return &r.Header }

func (r *ProbnodeExt) AppendTo(b []byte) []byte {
	b, p := grow(b, ProbnodeExtSize)
	r.RType = RProbnodeExt
	r.Header.put(p, ProbnodeExtSize)
	le.PutUint64(p[16:], r.TsRecv)
	le.PutUint32(p[24:], r.Ref)
	p[28], p[29], p[30], p[31] = r.Kind, r.Flag, r.Open, 0
	copy(p[32:64], r.Bucket[:])
	copy(p[64:80], r.Version[:])
	o := 80
	for _, m := range r.M {
		le.PutUint64(p[o:], uint64(m.Value))
		le.PutUint32(p[o+8:], uint32(m.XNorm))
		p[o+12], p[o+13], p[o+14], p[o+15] = m.PBucket, m.PSelf, 0, 0
		o += 16
	}
	for _, a := range r.After {
		le.PutUint32(p[o:], uint32(a.P50))
		le.PutUint32(p[o+4:], uint32(a.P90))
		o += 8
	}
	return b
}

func decodeProbnodeExt(p []byte) *ProbnodeExt {
	r := &ProbnodeExt{Header: getHeader(p), TsRecv: le.Uint64(p[16:]), Ref: le.Uint32(p[24:]), Kind: p[28], Flag: p[29], Open: p[30]}
	copy(r.Bucket[:], p[32:64])
	copy(r.Version[:], p[64:80])
	o := 80
	for i := range r.M {
		r.M[i] = PNMetric{Value: int64(le.Uint64(p[o:])), XNorm: int32(le.Uint32(p[o+8:])), PBucket: p[o+12], PSelf: p[o+13]}
		o += 16
	}
	for i := range r.After {
		r.After[i] = PNAfter{P50: int32(le.Uint32(p[o:])), P90: int32(le.Uint32(p[o+4:]))}
		o += 8
	}
	return r
}

func cstr(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// BucketName and NormsVersion read the fixed-size strings.
func (r *ProbnodeExt) BucketName() string   { return cstr(r.Bucket[:]) }
func (r *ProbnodeExt) NormsVersion() string { return cstr(r.Version[:]) }

func (r *ProbnodeExt) MarshalJSON() ([]byte, error) {
	w := &jw{}
	w.header("probnode", &r.Header)
	w.ns("ts_recv", r.TsRecv)
	kind := "book"
	if r.Kind == PNKindTrade {
		kind = "trade"
	}
	w.str("kind", kind)
	w.u64("ref", uint64(r.Ref))
	w.key("probnode")
	w.b = r.AppendObject(w.b)
	return w.end(), nil
}

func fx6(b []byte, v int64) []byte {
	if v == Missing {
		return append(b, "null"...)
	}
	return strconv.AppendFloat(b, float64(v)/1e6, 'f', -1, 64)
}

func (w *jw) dev(m PNMetric) {
	w.key("deviation")
	dw := &jw{b: append(w.b, '{')}
	dw.key("x_norm")
	if m.XNorm == PNNone {
		dw.b = append(dw.b, "null"...)
	} else {
		dw.b = strconv.AppendFloat(dw.b, float64(m.XNorm)/1e4, 'f', -1, 64)
	}
	for _, kv := range [2]struct {
		k string
		v uint8
	}{{"p_bucket", m.PBucket}, {"p_self", m.PSelf}} {
		if kv.v == PNNoPct {
			dw.null(kv.k)
		} else {
			dw.u64(kv.k, uint64(kv.v))
		}
	}
	w.b = append(dw.b, '}')
}

func (w *jw) metric(k string, m PNMetric, after *PNAfter) {
	w.key(k)
	mw := &jw{b: append(w.b, '{')}
	mw.key("value")
	mw.b = fx6(mw.b, m.Value)
	mw.dev(m)
	if after != nil {
		mw.key("after")
		aw := &jw{b: append(mw.b, '{')}
		aw.key("move_60s")
		if after.P50 == PNNone {
			aw.b = append(aw.b, "null"...)
		} else {
			bw := &jw{b: append(aw.b, '{')}
			bw.key("p50")
			bw.b = fx6(bw.b, int64(after.P50))
			bw.key("p90")
			bw.b = fx6(bw.b, int64(after.P90))
			aw.b = append(bw.b, '}')
		}
		mw.b = append(aw.b, '}')
	}
	w.b = append(mw.b, '}')
}

// Blocks of the probnode object a client can choose at subscribe (all by default).
const (
	BlockAnalytics uint8 = 1 << iota
	BlockRisk
	BlockSecurity
	BlockStats
	BlocksAll = BlockAnalytics | BlockRisk | BlockSecurity | BlockStats
)

// BlockNames maps the subscribe names to their bits.
var BlockNames = map[string]uint8{"analytics": BlockAnalytics, "risk": BlockRisk, "security": BlockSecurity, "stats": BlockStats}

// AppendObject appends the whole `probnode` JSON object.
func (r *ProbnodeExt) AppendObject(b []byte) []byte { return r.AppendBlocks(b, BlocksAll) }

// AppendBlocks appends the `probnode` JSON object with the chosen blocks: analytics, risk
// (factors), security, stats. Norms themselves are not repeated here: they go once per version
// (the `norms` message).
func (r *ProbnodeExt) AppendBlocks(b []byte, blocks uint8) []byte {
	w := &jw{b: append(b, '{')}
	if blocks&BlockAnalytics != 0 {
		r.analytics(w)
	}
	if blocks&BlockRisk != 0 {
		r.risk(w)
	}
	if blocks&BlockSecurity != 0 {
		r.security(w)
	}
	if blocks&BlockStats != 0 {
		r.stats(w)
	}
	return append(w.b, '}')
}

func (r *ProbnodeExt) analytics(w *jw) {
	w.key("analytics")
	aw := &jw{b: append(w.b, '{')}
	for i := 0; i < PNMetrics-PNFactors; i++ {
		aw.metric(PNNames[i], r.M[i], nil)
	}
	w.b = append(aw.b, '}')
}

func (r *ProbnodeExt) risk(w *jw) {
	w.key("risk")
	rw := &jw{b: append(w.b, '{')}
	rw.key("factors")
	fw := &jw{b: append(rw.b, '{')}
	f := PNMetrics - PNFactors
	pair := func(k, k1, k2 string, i int) {
		fw.key(k)
		pw := &jw{b: append(fw.b, '{')}
		pw.metric(k1, r.M[i], &r.After[i-f])
		pw.metric(k2, r.M[i+1], &r.After[i+1-f])
		fw.b = append(pw.b, '}')
	}
	pair("cost_to_trade", "buy", "sell", f)
	fw.metric("depth", r.M[f+2], &r.After[2])
	fw.metric("resilience", r.M[f+3], &r.After[3])
	pair("volatility", "10s", "1m", f+4)
	fw.metric("adverse_selection", r.M[f+6], &r.After[6])
	fw.metric("quote_stability", r.M[f+7], &r.After[7])
	fw.metric("resolution_jump", r.M[f+8], &r.After[8])
	rw.b = append(fw.b, '}')
	w.b = append(rw.b, '}')
}

func (r *ProbnodeExt) security(w *jw) {
	w.key("security")
	sw := &jw{b: append(w.b, '{')}
	flag := "none"
	if r.Flag == PNFlagAttack {
		flag = "attack"
	}
	sw.str("flag", flag)
	sw.u64("open", uint64(r.Open))
	w.b = append(sw.b, '}')
}

func (r *ProbnodeExt) stats(w *jw) {
	w.key("stats")
	tw := &jw{b: append(w.b, '{')}
	tw.str("bucket", r.BucketName())
	tw.str("stats_version", r.NormsVersion())
	w.b = append(tw.b, '}')
}
