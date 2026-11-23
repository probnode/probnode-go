# probnode-go

Go client for the [probnode Live API](https://probnode.com/docs/live-api?lang=go): every Polymarket
book update and trade of the markets you subscribe to, as it happens, over one WebSocket - each
enriched with the probnode object (analytics, risk against the normal for similar markets,
security, stats). JSON or Probe, our binary format; reconnects and resubscribes for you.

```sh
go get github.com/probnode/probnode-go
```

```go
c, err := probnode.DialWith(ctx, "wss://live.probnode.com/v2/live", key,
	probnode.Options{Format: probnode.Probe, Reconnect: true})
err = c.Subscribe(ctx, []string{"btc-updown-15m-1795366800"}, []string{"book+", "trades+"}, nil)
for {
	m, err := c.Next(ctx)
	// m.JSON for JSON frames; m.Records ([]pnb.Record) for Probe frames
}
```

Without a key you are on the Free plan. `pnb` decodes Probe records on their own
(`pnb.Decode`), for files of the archive as well as the stream.

- Docs: <https://probnode.com/docs/live-api?lang=go>
- Client reference: <https://probnode.com/docs/live-api/client?lang=go>
- Other clients: [Python](https://pypi.org/project/probnode-sdk/), [Rust](https://crates.io/crates/probnode), [TypeScript](https://www.npmjs.com/package/probnode), [C++](https://github.com/probnode/probnode-cpp)

Apache-2.0.
