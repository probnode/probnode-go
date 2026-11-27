# Changelog

## Unreleased

- Fix: with `Reconnect`, a dropped connection whose error text contained "401" (for example a TCP
  reset naming a port like 40122) was taken for a refused key and ended the stream instead of
  reconnecting. A refusal is now only the HTTP 401 answer to a dial, returned as `ErrRefused`;
  every other read error of a live connection is a drop and is reconnected.

## 0.1.1 - 2026-11-23

- go.mod declares go 1.24, the version the benchmarks need (`testing.B.Loop`); no API change.

## 0.1.0 - 2026-11-23

- First release: `Dial`, `DialWith` (reconnect with 0.5 s to 30 s backoff and resubscription,
  `OnGap`, permessage-deflate), `Subscribe`, `Unsubscribe`, `Next`, `Close`; JSON frames and Probe
  records (`pnb.Decode`).
