package pnb

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

const tsEvent = uint64(1795280000418000000)
const tsRecv = uint64(1795280000431204711)

func hdr(inst uint32) Header {
	return Header{VenueID: VenuePolymarket, InstrumentID: inst, TsEvent: tsEvent}
}

func samples() []Record {
	var tx [32]byte
	for i := range tx {
		tx[i] = byte(i)
	}
	var addr [20]byte
	copy(addr[:], []byte{0x5e, 0x1f, 0x0a})
	var tok [32]byte
	tok[31] = 7
	var market [112]byte
	copy(market[:], "btc-updown-5m-1795280100")
	var outcome [16]byte
	copy(outcome[:], "Up")
	var id [48]byte
	copy(id[:], "live-liq-0123456789ab-1795280000")
	var cs [16]byte
	copy(cs[:], "sc-0001")
	st := &Stats{Header: hdr(3), TsRecv: tsRecv, Seq: 9}
	for i := range st.Fields {
		st.Fields[i] = StatField{Value: int64(i * 1000), Baseline: Missing}
	}
	st.Fields[StVolume1m].Baseline = 5_100_000_000
	return []Record{
		&BookLevel{Header: hdr(3), TsRecv: tsRecv, Price: 410_000_000, Size: 300_000_000, Seq: 1, Side: SideBid, Action: ActionChange, Flags: FLast},
		&Trade{Header: hdr(3), TsRecv: tsRecv, Price: 420_000_000, Size: 120_000_000, Seq: 2, Side: SideBid},
		&Fill{Header: hdr(3), TsRecv: tsRecv, Price: 420_000_000, Size: 120_000_000, Fee: 0, Block: 79123456, LogIndex: 3, Maker: 1, Taker: 2, TradeSeq: 2, Side: SideAsk, Tx: tx},
		&Wallet{Header: hdr(0), ID: 1, Addr: addr},
		&Symbol{Header: hdr(3), Token: tok, Outcome: outcome, Market: market},
		st,
		&Signal{Header: hdr(3), ID: id, Type: uint16(TypeIndex("liquidity_drop")), State: 1, Severity: 2, Score: ScoreNone,
			TStart: tsEvent, Value: 22_100_000_000, ValueWorst: 18_000_000_000, Baseline: 61_400_000_000,
			Wallets: [4]uint32{7}, NWallets: 1, Case: cs},
		&Gap{Header: hdr(3), From: tsEvent},
		&Heartbeat{Header: hdr(0), LastSeq: 88124, State: StateDegraded},
		&BookExt{Header: hdr(3), TsRecv: tsRecv, Seq: 1, State: testState()},
		&TradeExt{Header: hdr(3), TsRecv: tsRecv, TradeSeq: 2, Stage: StagePrint, Flags: TXWhaleSweep, State: testState(),
			Notional: 50_400_000, Markout: Missing},
		&TradeExt{Header: hdr(3), TsRecv: tsRecv, TradeSeq: 2, Stage: StageWallets, Security: TSNewWallet | TSKnownWallet,
			NMakers: 2, Taker: 9, State: NoState(), Notional: Missing, Markout: Missing},
		&TradeExt{Header: hdr(3), TsRecv: tsRecv, TradeSeq: 2, Stage: StageMarkout, Flags: TXToxic, State: NoState(),
			Notional: Missing, Markout: 15_000_000},
	}
}

// testState is a State with every field set (avg_trade with a normal, the rest without).
func testState() State {
	return State{BestBid: 410_000_000, BestAsk: 430_000_000, Mid: 420_000_000, Microprice: 421_000_000, Spread: 20_000_000,
		DepthBid: 1_200_000_000, DepthAsk: 800_000_000, Imbalance: 200_000, UpdatesPerS: 3_500_000,
		Flow: [4]StatField{{5_000_000_000, Missing}, {12, Missing}, {300_000, Missing}, {416_666_666, 4_000_000}}}
}

func TestRoundTripEveryType(t *testing.T) {
	for _, r := range samples() {
		b := r.AppendTo(nil)
		if len(b)%8 != 0 {
			t.Fatalf("%T: %d bytes, not a multiple of 8", r, len(b))
		}
		if int(b[0])*4 != len(b) {
			t.Fatalf("%T: header length %d words, record %d bytes", r, b[0], len(b))
		}
		got, n, err := Decode(b)
		if err != nil || n != len(b) {
			t.Fatalf("%T: decode n=%d err=%v", r, n, err)
		}
		if !reflect.DeepEqual(got, r) {
			t.Fatalf("%T: round trip differs\n got %#v\nwant %#v", r, got, r)
		}
	}
}

func TestAStreamDecodesInOrderAndSkipsUnknownTypes(t *testing.T) {
	var buf []byte
	for _, r := range samples() {
		buf = r.AppendTo(buf)
	}
	// A record type this reader does not know, 24 bytes, in the middle.
	unknown := make([]byte, 24)
	unknown[0], unknown[1] = 6, 0x7f
	buf = append(buf[:BookLevelSize], append(unknown, buf[BookLevelSize:]...)...)
	var kinds []string
	for len(buf) > 0 {
		r, n, err := Decode(buf)
		if errors.Is(err, ErrUnknown) {
			kinds = append(kinds, "skip")
			buf = buf[n:]
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, reflect.TypeOf(r).Elem().Name())
		buf = buf[n:]
	}
	want := []string{"BookLevel", "skip", "Trade", "Fill", "Wallet", "Symbol", "Stats", "Signal", "Gap", "Heartbeat", "BookExt", "TradeExt", "TradeExt", "TradeExt"}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("got %v", kinds)
	}
}

func TestDecodeRefusesShortAndInconsistentInput(t *testing.T) {
	b := (&Trade{Header: hdr(1)}).AppendTo(nil)
	if _, _, err := Decode(b[:10]); !errors.Is(err, ErrShort) {
		t.Fatalf("short header: %v", err)
	}
	if _, _, err := Decode(b[:40]); !errors.Is(err, ErrShort) {
		t.Fatalf("short body: %v", err)
	}
	bad := append([]byte(nil), b...)
	bad[0] = 4 // claims 16 bytes for a 48-byte type
	if _, _, err := Decode(bad); !errors.Is(err, ErrLength) {
		t.Fatalf("length mismatch: %v", err)
	}
	zero := make([]byte, 16)
	if _, _, err := Decode(zero); !errors.Is(err, ErrLength) {
		t.Fatalf("zero length: %v", err)
	}
}

func TestJSONIsPNBShapedAndStable(t *testing.T) {
	golden := map[string]string{
		"Trade":   `{"type":"trade","venue":1,"instrument":3,"ts_event":"1795280000418000000","t_ms":1795280000418,"ts_recv":"1795280000431204711","seq":2,"side":"buy","price":420000000,"size":120000000,"flags":0}`,
		"Signal":  `{"type":"signal","venue":1,"instrument":3,"ts_event":"1795280000418000000","t_ms":1795280000418,"id":"live-liq-0123456789ab-1795280000","kind":"liquidity_drop","class":"health","lane":"risk","state":"confirmed","severity":2,"score":null,"t_start":"1795280000418000000","t_end":null,"value":22100000000,"value_worst":18000000000,"baseline":61400000000,"wallets":[7],"case":"sc-0001"}`,
		"Gap":     `{"type":"gap","venue":1,"instrument":3,"ts_event":"1795280000418000000","t_ms":1795280000418,"from":"1795280000418000000","to":null}`,
		"BookExt": `{"type":"book_ext","venue":1,"instrument":3,"ts_event":"1795280000418000000","t_ms":1795280000418,"ts_recv":"1795280000431204711","seq":1,"bb":410000000,"ba":430000000,"mid":420000000,"microprice":421000000,"spread":20000000,"depth_bid":1200000000,"depth_ask":800000000,"imbalance":200000,"updates_per_s":3500000,"volume_1m":[5000000000,null],"trades_1m":[12,null],"taker_imbalance_1m":[300000,null],"avg_trade":[416666666,4000000],"flags":0}`,
	}
	stages := map[uint8]string{
		StagePrint:   `{"type":"trade_ext","venue":1,"instrument":3,"ts_event":"1795280000418000000","t_ms":1795280000418,"ts_recv":"1795280000431204711","trade_seq":2,"stage":"print","bb":410000000,"ba":430000000,"mid":420000000,"microprice":421000000,"spread":20000000,"depth_bid":1200000000,"depth_ask":800000000,"imbalance":200000,"updates_per_s":3500000,"volume_1m":[5000000000,null],"trades_1m":[12,null],"taker_imbalance_1m":[300000,null],"avg_trade":[416666666,4000000],"notional":50400000,"whale_sweep":true}`,
		StageWallets: `{"type":"trade_ext","venue":1,"instrument":3,"ts_event":"1795280000418000000","t_ms":1795280000418,"ts_recv":"1795280000431204711","trade_seq":2,"stage":"wallets","taker":9,"makers":2,"known_wallet":true,"new_wallet":true,"self_wash":false}`,
		StageMarkout: `{"type":"trade_ext","venue":1,"instrument":3,"ts_event":"1795280000418000000","t_ms":1795280000418,"ts_recv":"1795280000431204711","trade_seq":2,"stage":"markout","markout":15000000,"toxic":true}`,
	}
	for _, r := range samples() {
		if te, ok := r.(*TradeExt); ok {
			b, _ := te.MarshalJSON()
			if string(b) != stages[te.Stage] {
				t.Fatalf("TradeExt stage %d:\n got %s\nwant %s", te.Stage, b, stages[te.Stage])
			}
		}
	}
	for _, r := range samples() {
		name := reflect.TypeOf(r).Elem().Name()
		b, err := r.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		if !json.Valid(b) {
			t.Fatalf("%s: invalid JSON %s", name, b)
		}
		if want, ok := golden[name]; ok && string(b) != want {
			t.Fatalf("%s:\n got %s\nwant %s", name, b, want)
		}
	}
}

func TestNanosecondsSurviveJSONExactly(t *testing.T) {
	// 2^53 is about 9.0e15; an ns epoch is about 1.8e18. As a number it would be rounded.
	tr := &Trade{Header: hdr(1), TsRecv: tsRecv}
	b, _ := tr.MarshalJSON()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["ts_recv"] != "1795280000431204711" || m["ts_event"] != "1795280000418000000" {
		t.Fatalf("ns not exact: %v %v", m["ts_recv"], m["ts_event"])
	}
}

func TestStatsJSONCarriesValueAndBaseline(t *testing.T) {
	st := samples()[5].(*Stats)
	b, _ := st.MarshalJSON()
	if !bytes.Contains(b, []byte(`"volume_1m":[0,5100000000]`)) || !bytes.Contains(b, []byte(`"trades_1m":[1000,null]`)) {
		t.Fatalf("stats json: %s", b)
	}
}

func TestSignalTypesMatchTheAPI(t *testing.T) {
	// The first 18 keys are pmd_api.integrity.SignalType, in that order.
	want := []string{"spoofing", "layering", "self_wash", "ring_wash", "prearranged_trade", "insider_trading",
		"copycat_flow", "resolution_vote_attack", "false_proposal", "whale_push", "push_before_resolution",
		"attack_unclassified", "known_wallet_active", "liquidity_drop", "spread_blowout", "book_vanish",
		"volume_depth_anomaly", "toxic_flow"}
	for i, k := range want {
		if SignalTypes[i].Key != k {
			t.Fatalf("index %d: %s, want %s", i, SignalTypes[i].Key, k)
		}
	}
	seen := map[string]bool{}
	for _, s := range SignalTypes {
		if seen[s.Key] {
			t.Fatalf("duplicate %s", s.Key)
		}
		seen[s.Key] = true
		if s.Lane != "security" && s.Lane != "risk" {
			t.Fatalf("%s: lane %q", s.Key, s.Lane)
		}
	}
}

func FuzzDecode(f *testing.F) {
	for _, r := range samples() {
		f.Add(r.AppendTo(nil))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		r, n, err := Decode(b)
		if err != nil {
			return
		}
		if n <= 0 || n > len(b) {
			t.Fatalf("n=%d len=%d", n, len(b))
		}
		// Whatever decodes must encode back to the same bytes, padding aside.
		again := r.AppendTo(nil)
		r2, _, err := Decode(again)
		if err != nil || !reflect.DeepEqual(r, r2) {
			t.Fatalf("re-decode differs: %v", err)
		}
		if _, err := r.MarshalJSON(); err != nil {
			t.Fatal(err)
		}
	})
}

// The record set version moves with the record types (docs/LIVE.md, Contract).
func TestVersionCoversEveryType(t *testing.T) {
	if Version != 4 || len(sizes) != 12 {
		t.Fatalf("version %d with %d record types: a type was added without a version bump", Version, len(sizes))
	}
}

func FuzzRoundTrip(f *testing.F) {
	f.Add(uint32(1), uint64(tsEvent), int64(420_000_000), int64(1), uint32(2), uint8(0))
	f.Fuzz(func(t *testing.T, inst uint32, ts uint64, price, size int64, seq uint32, side uint8) {
		tr := &Trade{Header: Header{VenueID: 1, InstrumentID: inst, TsEvent: ts}, TsRecv: ts, Price: price, Size: size, Seq: seq, Side: side}
		got, _, err := Decode(tr.AppendTo(nil))
		if err != nil || !reflect.DeepEqual(got, tr) {
			t.Fatalf("round trip: %v", err)
		}
	})
}

func BenchmarkAppendTrade(b *testing.B) {
	tr := &Trade{Header: hdr(3), TsRecv: tsRecv, Price: 420_000_000, Size: 120_000_000, Seq: 2}
	buf := make([]byte, 0, 4096)
	b.ReportAllocs()
	for b.Loop() {
		buf = tr.AppendTo(buf[:0])
	}
}

func BenchmarkDecodeTrade(b *testing.B) {
	buf := (&Trade{Header: hdr(3), TsRecv: tsRecv}).AppendTo(nil)
	b.ReportAllocs()
	for b.Loop() {
		_, _, _ = Decode(buf)
	}
}
