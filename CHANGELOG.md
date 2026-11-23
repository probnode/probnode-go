# Changelog

## 0.1.1 - 2026-11-23

- go.mod declares go 1.24, the version the benchmarks need (`testing.B.Loop`); no API change.

## 0.1.0 - 2026-11-23

- First release: `Dial`, `DialWith` (reconnect with 0.5 s to 30 s backoff and resubscription,
  `OnGap`, permessage-deflate), `Subscribe`, `Unsubscribe`, `Next`, `Close`; JSON frames and Probe
  records (`pnb.Decode`).
