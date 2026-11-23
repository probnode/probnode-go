package pnb

import (
	"bytes"
	"encoding/hex"
	"math/big"
	"strconv"
)

// SignalType describes one signal type: its key, class (attack | watch | health) and lane
// (security | risk). The order is the wire index and never changes; new types are appended.
// It mirrors pmd_api.integrity.TAXONOMY.
type SignalType struct {
	Key, Class, Lane string
}

var SignalTypes = []SignalType{
	{"spoofing", "attack", "security"},
	{"layering", "attack", "security"},
	{"self_wash", "attack", "security"},
	{"ring_wash", "attack", "security"},
	{"prearranged_trade", "attack", "security"},
	{"insider_trading", "attack", "security"},
	{"copycat_flow", "watch", "security"},
	{"resolution_vote_attack", "attack", "security"},
	{"false_proposal", "attack", "security"},
	{"whale_push", "attack", "security"},
	{"push_before_resolution", "attack", "security"},
	{"attack_unclassified", "attack", "security"},
	{"known_wallet_active", "watch", "security"},
	{"liquidity_drop", "health", "risk"},
	{"spread_blowout", "health", "risk"},
	{"book_vanish", "health", "risk"},
	{"volume_depth_anomaly", "health", "risk"},
	{"toxic_flow", "health", "risk"},
	// Live detectors added with the Go engine (risk lane, emitted suspected until validated).
	{"book_collapse", "health", "risk"},
	// Spoofing family, suspected (owner 2026-11-22: events belong to security; risk is factors).
	{"phantom_liquidity", "attack", "security"},
	{"cancel_storm", "attack", "security"},
	{"one_sided_book", "health", "risk"},
	{"whale_sweep", "health", "risk"},
	{"stale_book", "health", "risk"},
}

// TypeIndex returns the wire index of a signal type key, or -1.
func TypeIndex(key string) int {
	for i, t := range SignalTypes {
		if t.Key == key {
			return i
		}
	}
	return -1
}

var signalStates = []string{"suspected", "confirmed", "retracted"}

var statNames = [NStats]string{
	"volume_1m", "trades_1m", "wallets_1m", "new_wallets_1m", "taker_imbalance_1m", "avg_trade",
	"orders_added_1m", "orders_cancelled_1m", "updates_per_s", "mid", "microprice", "spread",
	"depth_bid", "depth_ask", "price_chg_1m", "realized_vol_5m", "markout_10s",
}

// StatName returns the JSON name of a stats field.
func StatName(i int) string { return statNames[i] }

// jw writes one JSON object with a fixed key order, so output is byte-stable for golden tests.
type jw struct {
	b []byte
	n int
}

func (w *jw) key(k string) {
	if w.n > 0 {
		w.b = append(w.b, ',')
	}
	w.n++
	w.b = strconv.AppendQuote(w.b, k)
	w.b = append(w.b, ':')
}
func (w *jw) str(k, v string)        { w.key(k); w.b = strconv.AppendQuote(w.b, v) }
func (w *jw) i64(k string, v int64)  { w.key(k); w.b = strconv.AppendInt(w.b, v, 10) }
func (w *jw) u64(k string, v uint64) { w.key(k); w.b = strconv.AppendUint(w.b, v, 10) }
func (w *jw) null(k string)          { w.key(k); w.b = append(w.b, "null"...) }

// ns writes a nanosecond time as a decimal string: it does not fit a JSON number exactly.
func (w *jw) ns(k string, v uint64) {
	w.key(k)
	w.b = append(w.b, '"')
	w.b = strconv.AppendUint(w.b, v, 10)
	w.b = append(w.b, '"')
}
func (w *jw) maybe(k string, v int64) {
	if v == Missing {
		w.null(k)
		return
	}
	w.i64(k, v)
}

func (w *jw) header(typ string, h *Header) {
	w.b = append(w.b, '{')
	w.str("type", typ)
	w.u64("venue", uint64(h.VenueID))
	w.u64("instrument", uint64(h.InstrumentID))
	w.ns("ts_event", h.TsEvent)
	w.u64("t_ms", h.TsEvent/1_000_000)
}
func (w *jw) end() []byte { return append(w.b, '}') }

func side(s uint8, book bool) string {
	switch {
	case book && s == SideBid:
		return "bid"
	case book:
		return "ask"
	case s == SideBid:
		return "buy"
	default:
		return "sell"
	}
}

func text(b []byte) string { return string(bytes.TrimRight(b, "\x00")) }

func (r *BookLevel) MarshalJSON() ([]byte, error) {
	w := &jw{}
	w.header("book", &r.Header)
	w.ns("ts_recv", r.TsRecv)
	w.u64("seq", uint64(r.Seq))
	w.str("side", side(r.Side, true))
	w.str("action", string(rune(r.Action)))
	w.i64("price", r.Price)
	w.i64("size", r.Size)
	w.u64("flags", uint64(r.Flags))
	return w.end(), nil
}

func (r *Trade) MarshalJSON() ([]byte, error) {
	w := &jw{}
	w.header("trade", &r.Header)
	w.ns("ts_recv", r.TsRecv)
	w.u64("seq", uint64(r.Seq))
	w.str("side", side(r.Side, false))
	w.i64("price", r.Price)
	w.i64("size", r.Size)
	w.u64("flags", uint64(r.Flags))
	return w.end(), nil
}

func (r *Fill) MarshalJSON() ([]byte, error) {
	w := &jw{}
	w.header("fill", &r.Header)
	w.ns("ts_recv", r.TsRecv)
	w.u64("trade_seq", uint64(r.TradeSeq))
	w.u64("block", r.Block)
	w.u64("log_index", uint64(r.LogIndex))
	w.str("tx", "0x"+hex.EncodeToString(r.Tx[:]))
	w.u64("maker", uint64(r.Maker))
	w.u64("taker", uint64(r.Taker))
	w.str("side", side(r.Side, false))
	w.i64("price", r.Price)
	w.i64("size", r.Size)
	w.i64("fee", r.Fee)
	w.u64("flags", uint64(r.Flags))
	return w.end(), nil
}

func (r *Wallet) MarshalJSON() ([]byte, error) {
	w := &jw{}
	w.header("wallet", &r.Header)
	w.u64("id", uint64(r.ID))
	w.str("address", "0x"+hex.EncodeToString(r.Addr[:]))
	return w.end(), nil
}

func (r *Symbol) MarshalJSON() ([]byte, error) {
	w := &jw{}
	w.header("symbol", &r.Header)
	w.str("token", new(big.Int).SetBytes(r.Token[:]).String())
	w.str("market", text(r.Market[:]))
	w.str("outcome", text(r.Outcome[:]))
	return w.end(), nil
}

func (r *Stats) MarshalJSON() ([]byte, error) {
	w := &jw{}
	w.header("stats", &r.Header)
	w.ns("ts_recv", r.TsRecv)
	w.u64("seq", uint64(r.Seq))
	w.key("fields")
	w.b = append(w.b, '{')
	for i, f := range r.Fields {
		if i > 0 {
			w.b = append(w.b, ',')
		}
		w.b = strconv.AppendQuote(w.b, statNames[i])
		w.b = append(w.b, ':', '[')
		w.b = appendMaybe(w.b, f.Value)
		w.b = append(w.b, ',')
		w.b = appendMaybe(w.b, f.Baseline)
		w.b = append(w.b, ']')
	}
	w.b = append(w.b, '}')
	return w.end(), nil
}

func appendMaybe(b []byte, v int64) []byte {
	if v == Missing {
		return append(b, "null"...)
	}
	return strconv.AppendInt(b, v, 10)
}

func (r *Signal) MarshalJSON() ([]byte, error) {
	w := &jw{}
	w.header("signal", &r.Header)
	w.str("id", text(r.ID[:]))
	t := SignalType{Key: "unknown", Class: "unknown", Lane: "unknown"}
	if int(r.Type) < len(SignalTypes) {
		t = SignalTypes[r.Type]
	}
	w.str("kind", t.Key)
	w.str("class", t.Class)
	w.str("lane", t.Lane)
	st := "unknown"
	if int(r.State) < len(signalStates) {
		st = signalStates[r.State]
	}
	w.str("state", st)
	w.u64("severity", uint64(r.Severity))
	if r.Score == ScoreNone {
		w.null("score")
	} else {
		w.u64("score", uint64(r.Score))
	}
	w.ns("t_start", r.TStart)
	if r.TEnd == 0 {
		w.null("t_end")
	} else {
		w.ns("t_end", r.TEnd)
	}
	w.maybe("value", r.Value)
	w.maybe("value_worst", r.ValueWorst)
	w.maybe("baseline", r.Baseline)
	w.key("wallets")
	w.b = append(w.b, '[')
	for i := 0; i < int(r.NWallets) && i < len(r.Wallets); i++ {
		if i > 0 {
			w.b = append(w.b, ',')
		}
		w.b = strconv.AppendUint(w.b, uint64(r.Wallets[i]), 10)
	}
	w.b = append(w.b, ']')
	if c := text(r.Case[:]); c != "" {
		w.str("case", c)
	} else {
		w.null("case")
	}
	return w.end(), nil
}

// stateFields writes a State (the same keys on book_ext, trade_ext, book+ and trade+).
func (s *State) stateFields(w *jw) {
	w.maybe("bb", s.BestBid)
	w.maybe("ba", s.BestAsk)
	w.maybe("mid", s.Mid)
	w.maybe("microprice", s.Microprice)
	w.maybe("spread", s.Spread)
	w.i64("depth_bid", s.DepthBid)
	w.i64("depth_ask", s.DepthAsk)
	w.maybe("imbalance", s.Imbalance)
	w.i64("updates_per_s", s.UpdatesPerS)
	for i, f := range s.Flow {
		w.key(statNames[StateFlow[i]])
		w.b = append(w.b, '[')
		w.b = appendMaybe(w.b, f.Value)
		w.b = append(w.b, ',')
		w.b = appendMaybe(w.b, f.Baseline)
		w.b = append(w.b, ']')
	}
}

func (r *BookExt) MarshalJSON() ([]byte, error) {
	w := &jw{}
	w.header("book_ext", &r.Header)
	w.ns("ts_recv", r.TsRecv)
	w.u64("seq", uint64(r.Seq))
	r.stateFields(w)
	w.u64("flags", uint64(r.Flags))
	return w.end(), nil
}

// BookPlusJSON is one book+ record (docs/LIVE.md, Channels): a venue book event's levels and the
// State after it in one JSON object, joined at delivery and never stored. The header is the event's
// last level's; seq is its seq.
func BookPlusJSON(levels []*BookLevel, x *BookExt, pn *ProbnodeExt, blocks uint8) []byte {
	w := &jw{}
	last := levels[len(levels)-1]
	w.header("book+", &last.Header)
	w.ns("ts_recv", last.TsRecv)
	w.u64("seq", uint64(last.Seq))
	w.key("levels")
	w.b = append(w.b, '[')
	for i, l := range levels {
		if i > 0 {
			w.b = append(w.b, ',')
		}
		lw := &jw{b: append(w.b, '{')}
		lw.u64("seq", uint64(l.Seq))
		lw.str("side", side(l.Side, true))
		lw.str("action", string(rune(l.Action)))
		lw.i64("price", l.Price)
		lw.i64("size", l.Size)
		lw.u64("flags", uint64(l.Flags))
		w.b = append(lw.b, '}')
	}
	w.b = append(w.b, ']')
	x.stateFields(w)
	if pn != nil && pn.Ref == x.Seq && blocks != 0 {
		w.key("probnode")
		w.b = pn.AppendBlocks(w.b, blocks)
	}
	return w.end()
}

// TradePlusJSON is one trades+ record: the trade and its print-stage TradeExt in one JSON object.
func TradePlusJSON(t *Trade, x *TradeExt, pn *ProbnodeExt, blocks uint8) []byte {
	w := &jw{}
	w.header("trade+", &t.Header)
	w.ns("ts_recv", t.TsRecv)
	w.u64("seq", uint64(t.Seq))
	w.str("side", side(t.Side, false))
	w.i64("price", t.Price)
	w.i64("size", t.Size)
	w.u64("flags", uint64(t.Flags))
	x.printFields(w)
	if pn != nil && pn.Ref == t.Seq && blocks != 0 {
		w.key("probnode")
		w.b = pn.AppendBlocks(w.b, blocks)
	}
	return w.end()
}

var stageNames = []string{"print", "wallets", "markout"}

func (w *jw) boolean(k string, v bool) { w.key(k); w.b = strconv.AppendBool(w.b, v) }

func (r *TradeExt) MarshalJSON() ([]byte, error) {
	w := &jw{}
	w.header("trade_ext", &r.Header)
	w.ns("ts_recv", r.TsRecv)
	w.u64("trade_seq", uint64(r.TradeSeq))
	st := "unknown"
	if int(r.Stage) < len(stageNames) {
		st = stageNames[r.Stage]
	}
	w.str("stage", st)
	switch r.Stage {
	case StagePrint:
		r.printFields(w)
	case StageWallets:
		w.u64("taker", uint64(r.Taker))
		w.u64("makers", uint64(r.NMakers))
		w.boolean("known_wallet", r.Security&TSKnownWallet != 0)
		w.boolean("new_wallet", r.Security&TSNewWallet != 0)
		w.boolean("self_wash", r.Security&TSSelfWash != 0)
	case StageMarkout:
		w.maybe("markout", r.Markout)
		w.boolean("toxic", r.Flags&TXToxic != 0)
	}
	return w.end(), nil
}

// printFields writes the print stage: the State, the notional, whale_sweep (shared with trades+).
func (r *TradeExt) printFields(w *jw) {
	r.stateFields(w)
	w.i64("notional", r.Notional)
	w.boolean("whale_sweep", r.Flags&TXWhaleSweep != 0)
}

func (r *Gap) MarshalJSON() ([]byte, error) {
	w := &jw{}
	w.header("gap", &r.Header)
	w.ns("from", r.From)
	if r.To == 0 {
		w.null("to")
	} else {
		w.ns("to", r.To)
	}
	return w.end(), nil
}

var stateNames = []string{"live", "degraded", "down"}

func (r *Heartbeat) MarshalJSON() ([]byte, error) {
	w := &jw{}
	w.header("heartbeat", &r.Header)
	w.u64("last_seq", r.LastSeq)
	st := "unknown"
	if int(r.State) < len(stateNames) {
		st = stateNames[r.State]
	}
	w.str("state", st)
	return w.end(), nil
}
