package pnb

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/bits"
)

// Version of the record set: it changes whenever a record type is added or a layout changes
// (docs/LIVE.md, Contract). 2: BookExt and TradeExt. 3: both carry one State (book + flow).
// 4: ProbnodeExt, the `probnode` object of the approved contract (2026-11-22).
const Version = 4

// Record types.
const (
	RBookLevel uint8 = 0x01
	RTrade     uint8 = 0x02
	RFill      uint8 = 0x03
	RWallet    uint8 = 0x04
	RSymbol    uint8 = 0x05
	RStats     uint8 = 0x10
	RSignal    uint8 = 0x11
	RBookExt   uint8 = 0x12
	RTradeExt  uint8 = 0x13
	RGap       uint8 = 0x20
	RHeartbeat uint8 = 0x21
)

// Venues.
const VenuePolymarket uint16 = 1

// Scales.
const (
	PriceScale = 1_000_000_000 // 1 price unit = 1e-9
	SizeScale  = 1_000_000     // 1 size unit = 1e-6
)

// USD is price (×1e9) × size (×1e6) in USD ×1e6, exact for any size: the product goes through 128
// bits. price × size in int64 overflows past ~9.2 million shares at price 1 (a long event market's
// book holds more), which is where negative depth came from (2026-11-22: 38 of 1,412 tokens on the
// 11-21 19:00 hour). Negative inputs give 0.
func USD(price, size int64) int64 {
	if price <= 0 || size <= 0 {
		return 0
	}
	hi, lo := bits.Mul64(uint64(price), uint64(size))
	if hi >= PriceScale { // more than ~1.8e28 / 1e9: no book holds that
		return math.MaxInt64
	}
	q, _ := bits.Div64(hi, lo, PriceScale)
	if q > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(q)
}

// Flags (body byte where a record has one).
const (
	FLast         uint8 = 128 // last record of one event for this instrument
	FSnapshot     uint8 = 32  // part of a snapshot (replay, warm-up), not a live change
	FBadTsRecv    uint8 = 8   // ts_recv may be wrong (clock step, reorder)
	FMaybeBadBook uint8 = 4   // an unrecoverable gap was seen on this instrument
)

// Sides and actions.
const (
	SideBid uint8 = 0 // book: bid; trade/fill: buy aggressor
	SideAsk uint8 = 1 // book: ask; trade/fill: sell aggressor

	ActionAdd    uint8 = 'A'
	ActionChange uint8 = 'C'
	ActionDelete uint8 = 'D'
)

// Sizes in bytes; every one a multiple of 8.
const (
	HeaderSize    = 16
	BookLevelSize = 48
	TradeSize     = 48
	FillSize      = 112
	WalletSize    = 48
	SymbolSize    = 176
	StatsSize     = 304
	SignalSize    = 160
	BookExtSize   = 32 + StateSize          // 168
	TradeExtSize  = 32 + StateSize + 8 + 16 // 192
	GapSize       = 32
	HeartbeatSize = 32
)

var (
	ErrShort   = errors.New("pnb: buffer shorter than the record")
	ErrLength  = errors.New("pnb: header length does not match the record type")
	ErrUnknown = errors.New("pnb: unknown record type")
)

var le = binary.LittleEndian

// Header starts every record.
type Header struct {
	RType        uint8
	VenueID      uint16
	InstrumentID uint32
	TsEvent      uint64
}

func (h Header) put(b []byte, size int) {
	b[0] = uint8(size / 4)
	b[1] = h.RType
	le.PutUint16(b[2:], h.VenueID)
	le.PutUint32(b[4:], h.InstrumentID)
	le.PutUint64(b[8:], h.TsEvent)
}

func getHeader(b []byte) Header {
	return Header{RType: b[1], VenueID: le.Uint16(b[2:]), InstrumentID: le.Uint32(b[4:]), TsEvent: le.Uint64(b[8:])}
}

// Record is any PNB record.
type Record interface {
	Hdr() *Header
	// AppendTo appends the record's bytes to b.
	AppendTo(b []byte) []byte
	MarshalJSON() ([]byte, error)
}

func grow(b []byte, n int) ([]byte, []byte) {
	l := len(b)
	if cap(b)-l < n {
		nb := make([]byte, l, l+n+64)
		copy(nb, b)
		b = nb
	}
	b = b[:l+n]
	clear(b[l:])
	return b, b[l:]
}

// BookLevel is one change of one price level.
type BookLevel struct {
	Header
	TsRecv uint64
	Price  int64
	Size   int64 // new size of the level; 0 with ActionDelete
	Seq    uint32
	Side   uint8
	Action uint8
	Flags  uint8
}

func (r *BookLevel) Hdr() *Header { return &r.Header }
func (r *BookLevel) AppendTo(b []byte) []byte {
	b, p := grow(b, BookLevelSize)
	r.RType = RBookLevel
	r.Header.put(p, BookLevelSize)
	le.PutUint64(p[16:], r.TsRecv)
	le.PutUint64(p[24:], uint64(r.Price))
	le.PutUint64(p[32:], uint64(r.Size))
	le.PutUint32(p[40:], r.Seq)
	p[44], p[45], p[46] = r.Side, r.Action, r.Flags
	return b
}
func decodeBookLevel(p []byte) *BookLevel {
	return &BookLevel{Header: getHeader(p), TsRecv: le.Uint64(p[16:]), Price: int64(le.Uint64(p[24:])),
		Size: int64(le.Uint64(p[32:])), Seq: le.Uint32(p[40:]), Side: p[44], Action: p[45], Flags: p[46]}
}

// Trade is a print on the venue.
type Trade struct {
	Header
	TsRecv uint64
	Price  int64
	Size   int64
	Seq    uint32
	Side   uint8 // aggressor
	Flags  uint8
}

func (r *Trade) Hdr() *Header { return &r.Header }
func (r *Trade) AppendTo(b []byte) []byte {
	b, p := grow(b, TradeSize)
	r.RType = RTrade
	r.Header.put(p, TradeSize)
	le.PutUint64(p[16:], r.TsRecv)
	le.PutUint64(p[24:], uint64(r.Price))
	le.PutUint64(p[32:], uint64(r.Size))
	le.PutUint32(p[40:], r.Seq)
	p[44], p[45] = r.Side, r.Flags
	return b
}
func decodeTrade(p []byte) *Trade {
	return &Trade{Header: getHeader(p), TsRecv: le.Uint64(p[16:]), Price: int64(le.Uint64(p[24:])),
		Size: int64(le.Uint64(p[32:])), Seq: le.Uint32(p[40:]), Side: p[44], Flags: p[45]}
}

// Fill is an OrderFilled from our node, joined to the venue trade it belongs to (TradeSeq).
type Fill struct {
	Header
	TsRecv   uint64
	Price    int64
	Size     int64
	Fee      int64 // in size units of the asset paid
	Block    uint64
	LogIndex uint32
	Maker    uint32 // wallet id
	Taker    uint32 // wallet id
	TradeSeq uint32 // seq of the joined trade; 0 = not joined
	Side     uint8  // maker's order side
	Flags    uint8
	Tx       [32]byte
}

func (r *Fill) Hdr() *Header { return &r.Header }
func (r *Fill) AppendTo(b []byte) []byte {
	b, p := grow(b, FillSize)
	r.RType = RFill
	r.Header.put(p, FillSize)
	le.PutUint64(p[16:], r.TsRecv)
	le.PutUint64(p[24:], uint64(r.Price))
	le.PutUint64(p[32:], uint64(r.Size))
	le.PutUint64(p[40:], uint64(r.Fee))
	le.PutUint64(p[48:], r.Block)
	le.PutUint32(p[56:], r.LogIndex)
	le.PutUint32(p[60:], r.Maker)
	le.PutUint32(p[64:], r.Taker)
	le.PutUint32(p[68:], r.TradeSeq)
	p[72], p[73] = r.Side, r.Flags
	copy(p[80:112], r.Tx[:])
	return b
}
func decodeFill(p []byte) *Fill {
	r := &Fill{Header: getHeader(p), TsRecv: le.Uint64(p[16:]), Price: int64(le.Uint64(p[24:])),
		Size: int64(le.Uint64(p[32:])), Fee: int64(le.Uint64(p[40:])), Block: le.Uint64(p[48:]),
		LogIndex: le.Uint32(p[56:]), Maker: le.Uint32(p[60:]), Taker: le.Uint32(p[64:]),
		TradeSeq: le.Uint32(p[68:]), Side: p[72], Flags: p[73]}
	copy(r.Tx[:], p[80:112])
	return r
}

// Wallet maps a wallet id to its address, once per connection.
type Wallet struct {
	Header
	ID   uint32
	Addr [20]byte
}

func (r *Wallet) Hdr() *Header { return &r.Header }
func (r *Wallet) AppendTo(b []byte) []byte {
	b, p := grow(b, WalletSize)
	r.RType = RWallet
	r.Header.put(p, WalletSize)
	le.PutUint32(p[16:], r.ID)
	copy(p[24:44], r.Addr[:])
	return b
}
func decodeWallet(p []byte) *Wallet {
	r := &Wallet{Header: getHeader(p), ID: le.Uint32(p[16:])}
	copy(r.Addr[:], p[24:44])
	return r
}

// Symbol maps an instrument id to its token, market and outcome.
type Symbol struct {
	Header
	Token   [32]byte // uint256, big-endian
	Outcome [16]byte // zero-padded text
	Market  [112]byte
}

func (r *Symbol) Hdr() *Header { return &r.Header }
func (r *Symbol) AppendTo(b []byte) []byte {
	b, p := grow(b, SymbolSize)
	r.RType = RSymbol
	r.Header.put(p, SymbolSize)
	copy(p[16:48], r.Token[:])
	copy(p[48:64], r.Outcome[:])
	copy(p[64:176], r.Market[:])
	return b
}
func decodeSymbol(p []byte) *Symbol {
	r := &Symbol{Header: getHeader(p)}
	copy(r.Token[:], p[16:48])
	copy(r.Outcome[:], p[48:64])
	copy(r.Market[:], p[64:176])
	return r
}

// Stats fields, in their fixed order.
const (
	StVolume1m = iota // USD ×1e6
	StTrades1m
	StWallets1m
	StNewWallets1m
	StTakerImbalance1m // ×1e6, -1..1
	StAvgTrade         // USD ×1e6
	StOrdersAdded1m
	StOrdersCancelled1m
	StUpdatesPerS // ×1e6
	StMid         // price units
	StMicroprice  // price units
	StSpread      // price units
	StDepthBid    // USD ×1e6, top 10 levels
	StDepthAsk
	StPriceChg1m // price units
	StRealizedVol5m
	StMarkout10s // price units: volume-weighted, signed for the taker, 10 s after each print, over the minute
	NStats
)

// StatField is one statistic: the value now and this market's normal (Missing when unknown).
type StatField struct {
	Value    int64
	Baseline int64
}

// Missing marks a baseline (or value) that is not known.
const Missing = int64(-1 << 63)

// Stats is the whole market's statistics at one step.
type Stats struct {
	Header
	TsRecv uint64
	Seq    uint32
	Fields [NStats]StatField
}

func (r *Stats) Hdr() *Header { return &r.Header }
func (r *Stats) AppendTo(b []byte) []byte {
	b, p := grow(b, StatsSize)
	r.RType = RStats
	r.Header.put(p, StatsSize)
	le.PutUint64(p[16:], r.TsRecv)
	le.PutUint32(p[24:], r.Seq)
	for i, f := range r.Fields {
		o := 32 + i*16
		le.PutUint64(p[o:], uint64(f.Value))
		le.PutUint64(p[o+8:], uint64(f.Baseline))
	}
	return b
}
func decodeStats(p []byte) *Stats {
	r := &Stats{Header: getHeader(p), TsRecv: le.Uint64(p[16:]), Seq: le.Uint32(p[24:])}
	for i := range r.Fields {
		o := 32 + i*16
		r.Fields[i] = StatField{Value: int64(le.Uint64(p[o:])), Baseline: int64(le.Uint64(p[o+8:]))}
	}
	return r
}

// Signal is one Risk or Security signal: opened, moved or closed (a transition).
type Signal struct {
	Header
	ID         [48]byte // zero-padded text, e.g. live-liq-<hash>-<s>
	Type       uint16   // index into SignalTypes
	State      uint8    // 0 suspected, 1 confirmed, 2 retracted
	Severity   uint8    // 1..3
	Score      uint32   // 0..1 ×1e6; ScoreNone for a person's finding
	TStart     uint64   // ns
	TEnd       uint64   // ns; 0 = still open
	Value      int64    // the measurement now; Missing when none
	ValueWorst int64
	Baseline   int64
	Wallets    [4]uint32
	NWallets   uint8
	Case       [16]byte // zero-padded case id
}

func (r *Signal) Hdr() *Header { return &r.Header }
func (r *Signal) AppendTo(b []byte) []byte {
	b, p := grow(b, SignalSize)
	r.RType = RSignal
	r.Header.put(p, SignalSize)
	copy(p[16:64], r.ID[:])
	le.PutUint16(p[64:], r.Type)
	p[66], p[67] = r.State, r.Severity
	le.PutUint32(p[68:], r.Score)
	le.PutUint64(p[72:], r.TStart)
	le.PutUint64(p[80:], r.TEnd)
	le.PutUint64(p[88:], uint64(r.Value))
	le.PutUint64(p[96:], uint64(r.ValueWorst))
	le.PutUint64(p[104:], uint64(r.Baseline))
	for i, w := range r.Wallets {
		le.PutUint32(p[112+i*4:], w)
	}
	p[128] = r.NWallets
	copy(p[136:152], r.Case[:])
	return b
}
func decodeSignal(p []byte) *Signal {
	r := &Signal{Header: getHeader(p), Type: le.Uint16(p[64:]), State: p[66], Severity: p[67],
		Score: le.Uint32(p[68:]), TStart: le.Uint64(p[72:]), TEnd: le.Uint64(p[80:]),
		Value: int64(le.Uint64(p[88:])), ValueWorst: int64(le.Uint64(p[96:])), Baseline: int64(le.Uint64(p[104:])),
		NWallets: p[128]}
	copy(r.ID[:], p[16:64])
	for i := range r.Wallets {
		r.Wallets[i] = le.Uint32(p[112+i*4:])
	}
	copy(r.Case[:], p[136:152])
	return r
}

// ScoreNone marks a signal with no rule confidence: a person's finding.
const ScoreNone uint32 = 0xFFFFFFFF

// Gap: no data between From and To (To 0 = still open). Nothing is drawn inside it.
type Gap struct {
	Header
	From uint64
	To   uint64
}

func (r *Gap) Hdr() *Header { return &r.Header }
func (r *Gap) AppendTo(b []byte) []byte {
	b, p := grow(b, GapSize)
	r.RType = RGap
	r.Header.put(p, GapSize)
	le.PutUint64(p[16:], r.From)
	le.PutUint64(p[24:], r.To)
	return b
}
func decodeGap(p []byte) *Gap {
	return &Gap{Header: getHeader(p), From: le.Uint64(p[16:]), To: le.Uint64(p[24:])}
}

// Channel states carried by a heartbeat.
const (
	StateLive     uint8 = 0
	StateDegraded uint8 = 1
	StateDown     uint8 = 2
)

// Heartbeat: the channel is alive; LastSeq lets a client see what it missed.
type Heartbeat struct {
	Header
	LastSeq uint64
	State   uint8
}

func (r *Heartbeat) Hdr() *Header { return &r.Header }
func (r *Heartbeat) AppendTo(b []byte) []byte {
	b, p := grow(b, HeartbeatSize)
	r.RType = RHeartbeat
	r.Header.put(p, HeartbeatSize)
	le.PutUint64(p[16:], r.LastSeq)
	p[24] = r.State
	return b
}
func decodeHeartbeat(p []byte) *Heartbeat {
	return &Heartbeat{Header: getHeader(p), LastSeq: le.Uint64(p[16:]), State: p[24]}
}

// State is the market state every enrichment record carries, the same on a tick and on a trade
// (docs/LIVE.md, Contract): a bot reads one shape whatever event it got. Book: the token's book now;
// flow: the minute's trade windows now (each with this market's normal for the hour of week,
// Missing until the box has one). Read live at the event, never from a stale cache.
type State struct {
	BestBid, BestAsk   int64        // price units; Missing on an empty side
	Mid, Microprice    int64        // price units; Missing unless both sides
	Spread             int64        // price units; Missing unless both sides
	DepthBid, DepthAsk int64        // USD x1e6, top 10 levels
	Imbalance          int64        // x1e6, -1..1: (bid - ask) / (bid + ask) depth; Missing on an empty book
	UpdatesPerS        int64        // x1e6, book updates per second over the last minute
	Flow               [4]StatField // StateFlow keys: value and normal
}

// StateSize is the State block's size inside a record.
const StateSize = 9*8 + 4*16

// StateFlow names State.Flow in order (the Stats keys they carry).
var StateFlow = [4]int{StVolume1m, StTrades1m, StTakerImbalance1m, StAvgTrade}

// NoState is a State with every field unknown (the trade amendments carry no state).
func NoState() State {
	s := State{BestBid: Missing, BestAsk: Missing, Mid: Missing, Microprice: Missing, Spread: Missing, Imbalance: Missing}
	for i := range s.Flow {
		s.Flow[i] = StatField{Value: Missing, Baseline: Missing}
	}
	return s
}

func (s *State) put(p []byte) {
	for i, v := range [9]int64{s.BestBid, s.BestAsk, s.Mid, s.Microprice, s.Spread, s.DepthBid, s.DepthAsk, s.Imbalance, s.UpdatesPerS} {
		le.PutUint64(p[8*i:], uint64(v))
	}
	for i, f := range s.Flow {
		le.PutUint64(p[72+16*i:], uint64(f.Value))
		le.PutUint64(p[80+16*i:], uint64(f.Baseline))
	}
}

func getState(p []byte) State {
	v := func(i int) int64 { return int64(le.Uint64(p[8*i:])) }
	s := State{BestBid: v(0), BestAsk: v(1), Mid: v(2), Microprice: v(3), Spread: v(4), DepthBid: v(5), DepthAsk: v(6),
		Imbalance: v(7), UpdatesPerS: v(8)}
	for i := range s.Flow {
		s.Flow[i] = StatField{Value: int64(le.Uint64(p[72+16*i:])), Baseline: int64(le.Uint64(p[80+16*i:]))}
	}
	return s
}

// BookExt is our enrichment of one venue book event (book.ext): the State after it. It goes out once
// per event, after the event's last level (Seq refers to it), and only when the event moved a book
// field of the State (a change below the top 10 levels moves none of them).
type BookExt struct {
	Header
	TsRecv uint64
	Seq    uint32 // seq of the event's last BookLevel
	Flags  uint8
	State
}

func (r *BookExt) Hdr() *Header { return &r.Header }
func (r *BookExt) AppendTo(b []byte) []byte {
	b, p := grow(b, BookExtSize)
	r.RType = RBookExt
	r.Header.put(p, BookExtSize)
	le.PutUint64(p[16:], r.TsRecv)
	le.PutUint32(p[24:], r.Seq)
	p[28] = r.Flags
	r.State.put(p[32:])
	return b
}
func decodeBookExt(p []byte) *BookExt {
	return &BookExt{Header: getHeader(p), TsRecv: le.Uint64(p[16:]), Seq: le.Uint32(p[24:]), Flags: p[28], State: getState(p[32:])}
}

// TradeExt stages: one trade gets up to three, each a TradeExt with the trade's seq.
const (
	StagePrint   uint8 = 0 // at the print: the State right after it, its notional, whale_sweep
	StageWallets uint8 = 1 // about a block later, once the chain's fills join: taker, makers, Security
	StageMarkout uint8 = 2 // MarkoutMs later: the mid then against the print
)

// TradeExt flags.
const (
	TXWhaleSweep uint8 = 1 // notional >= max(WhaleMinUSD, WhaleFactor x the minute's average trade before it)
	TXToxic      uint8 = 2 // stage markout: the taker was right by more than the spread at the print
)

// TradeExt security bits (stage wallets).
const (
	TSKnownWallet uint8 = 1 // a wallet of the trade is on a case watchlist
	TSNewWallet   uint8 = 2 // the taker's first fill this engine has seen
	TSSelfWash    uint8 = 4 // maker and taker are the same wallet
)

// TradeExt is our enrichment of one trade (trades.ext). Stage print: the State right after the trade
// (its book and flow), the notional, whale_sweep. Stage wallets: the taker, the number of makers, the
// Security bits (each maker's wallet is in its Fill, by the same TradeSeq). Stage markout: the
// mark-out. Fields a stage does not set are Missing (ids and counts 0).
type TradeExt struct {
	Header
	TsRecv   uint64
	TradeSeq uint32
	Stage    uint8
	Flags    uint8
	Security uint8
	NMakers  uint8
	State
	Taker    uint32 // wallet id, stage wallets
	Notional int64  // USD x1e6
	Markout  int64  // price units, signed for the taker; stage markout
}

func (r *TradeExt) Hdr() *Header { return &r.Header }
func (r *TradeExt) AppendTo(b []byte) []byte {
	b, p := grow(b, TradeExtSize)
	r.RType = RTradeExt
	r.Header.put(p, TradeExtSize)
	le.PutUint64(p[16:], r.TsRecv)
	le.PutUint32(p[24:], r.TradeSeq)
	p[28], p[29], p[30], p[31] = r.Stage, r.Flags, r.Security, r.NMakers
	r.State.put(p[32:])
	o := 32 + StateSize
	le.PutUint32(p[o:], r.Taker)
	le.PutUint64(p[o+8:], uint64(r.Notional))
	le.PutUint64(p[o+16:], uint64(r.Markout))
	return b
}
func decodeTradeExt(p []byte) *TradeExt {
	o := 32 + StateSize
	return &TradeExt{Header: getHeader(p), TsRecv: le.Uint64(p[16:]), TradeSeq: le.Uint32(p[24:]),
		Stage: p[28], Flags: p[29], Security: p[30], NMakers: p[31], State: getState(p[32:]),
		Taker: le.Uint32(p[o:]), Notional: int64(le.Uint64(p[o+8:])), Markout: int64(le.Uint64(p[o+16:]))}
}

var sizes = map[uint8]int{
	RBookLevel: BookLevelSize, RTrade: TradeSize, RFill: FillSize, RWallet: WalletSize,
	RSymbol: SymbolSize, RStats: StatsSize, RSignal: SignalSize, RBookExt: BookExtSize, RTradeExt: TradeExtSize, RGap: GapSize, RHeartbeat: HeartbeatSize,
	RProbnodeExt: ProbnodeExtSize,
}

// Decode reads one record from the front of b and returns it and its length in bytes. An unknown
// record type returns ErrUnknown and its length, so a reader can skip it.
func Decode(b []byte) (Record, int, error) {
	if len(b) < HeaderSize {
		return nil, 0, ErrShort
	}
	n := int(b[0]) * 4
	if n < HeaderSize {
		return nil, 0, ErrLength
	}
	if len(b) < n {
		return nil, 0, ErrShort
	}
	want, ok := sizes[b[1]]
	if !ok {
		return nil, n, ErrUnknown
	}
	if n != want {
		return nil, 0, fmt.Errorf("%w: type 0x%02x is %d bytes, header says %d", ErrLength, b[1], want, n)
	}
	p := b[:n]
	switch b[1] {
	case RBookLevel:
		return decodeBookLevel(p), n, nil
	case RTrade:
		return decodeTrade(p), n, nil
	case RFill:
		return decodeFill(p), n, nil
	case RWallet:
		return decodeWallet(p), n, nil
	case RSymbol:
		return decodeSymbol(p), n, nil
	case RStats:
		return decodeStats(p), n, nil
	case RSignal:
		return decodeSignal(p), n, nil
	case RBookExt:
		return decodeBookExt(p), n, nil
	case RTradeExt:
		return decodeTradeExt(p), n, nil
	case RGap:
		return decodeGap(p), n, nil
	case RProbnodeExt:
		return decodeProbnodeExt(p), n, nil
	default:
		return decodeHeartbeat(p), n, nil
	}
}
