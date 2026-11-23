// Package pnb is the probnode binary encoding: one fixed-width, little-endian record layout used
// in the engine's memory, on the wire and in the archive, so nothing is re-encoded between them
// (docs/LIVE.md, "Format: PNB"; after Databento's DBN).
//
// # Header (16 bytes, every record)
//
//	offset size field          meaning
//	0      1    Length         record length in 32-bit words, header included
//	1      1    RType          record type (constants below)
//	2      2    VenueID        1 = Polymarket
//	4      4    InstrumentID   the token, by id into the Symbol dictionary
//	8      8    TsEvent        event time, ns since the epoch: the venue's for book and trades, the block's for fills
//
// A reader skips a record type it does not know by Length, so new types never break old clients.
//
// # Units
//
// Prices are int64 in units of 1e-9 (PriceScale): 0.42 is 420_000_000. Sizes are int64 in units of
// 1e-6 (SizeScale): 120 shares is 120_000_000. Times are uint64 nanoseconds since the epoch.
// Wallets and tokens never appear as strings inside a record: they are uint32 ids into the Wallet
// and Symbol dictionaries, sent once per connection and again after every reconnect.
//
// # Timestamps
//
// TsEvent is in the header. TsRecv (our receive time) and, on the live stream, TsOut (gateway send
// time) are in the body where the record has them. TsOut − TsRecv is the time spent inside probnode.
//
// # JSON
//
// The JSON form (MarshalJSON on every record) is PNB-shaped: the same type names, field names and
// integer units. Nanosecond times are decimal strings, because they exceed 2^53 and a JSON number
// would round them; t_ms carries the event time in milliseconds as a number for display.
package pnb
