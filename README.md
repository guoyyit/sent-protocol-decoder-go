# SENT Protocol Decoder (Go)

A hardware-free receiver and diagnostic engine for the **Single Edge Nibble
Transmission (SENT, SAE J2716 style)** protocol used by automotive sensors.
It reconstructs fast-channel frames from a falling-edge time series, assembles
slow-channel messages across frames, classifies timing/checksum/noise faults,
debounces sensor error flags, and runs many sensor channels concurrently.

Signal sources are simulated entirely in memory (an `Encoder` that synthesizes
edges and a buffered `Pipe` for local loopback). No real hardware or external
services are required.

## Quick start

```go
cfg := sent.DefaultConfig("sensor-1", sent.VariantRecommended)

// Simulate 20 frames and inject one corrupted CRC.
specs := make([]sent.FrameSpec, 20)
for i := range specs {
	specs[i] = sent.FrameSpec{RawA: 1000, RawB: 500}
}
edges := sent.NewEncoder(cfg, 42).Build(specs,
	[]sent.InjectOp{{Type: sent.OpCorruptCRC, FrameIndex: 7}})

// Pure, deterministic one-shot decode.
for _, ev := range sent.Decode(cfg, edges) {
	switch ev.Kind {
	case sent.EventReading:
		fmt.Println(ev.Reading.RawA, ev.Reading.PhysA, ev.Reading.UnitA)
	case sent.EventFrameError:
		fmt.Println("frame fault:", ev.FrameErr.Class)
	}
}
```

For streaming/multi-channel use, see [`Manager`](#concurrent-channels).

## Frame format

Every frame is a sequence of falling-edge intervals measured in **unit ticks**:

| Section        | Width (ticks) | Contents                                  |
|----------------|---------------|-------------------------------------------|
| Sync           | 56            | fixed calibration pulse                   |
| Status         | 12–27         | 4-bit status nibble                       |
| Data `d0..d2`  | 12–27 each    | fast channel A, nibble value = width − 12 |
| Data `d3..d5`  | 12–27 each    | fast channel B                            |
| CRC            | 12–27         | 4-bit checksum nibble                     |
| Pause (opt.)   | configurable  | fixed pad, carries no data                |

A nibble value `v` (0–15) is transmitted as a pulse `12 + v` ticks wide.
Pulse widths **28–31 ticks** encode no legal nibble and are reported as an
illegal-nibble fault.

The reference edge at `t = 0` precedes the first sync pulse; each subsequent
edge delimits one interval.

### Status nibble layout

```
 bit:   3     2     1     0
     [ slow bit 1 | slow bit 0 | rolling seq ]
```

- **Bits 3..2** carry two slow-channel bits per frame (MSB first over the
  whole message).
- **Bits 1..0** are a rolling frame sequence counter (`0,1,2,3,0,...`) used to
  detect lost, duplicated and reordered frames during slow assembly.

### Fast channel payload

Each 3-nibble channel is 12 bits:

- **Bit 11** is the per-channel sensor **error flag** (`ErrA` / `ErrB`).
- **Bits 10..0** are the raw 11-bit measurement (`RawA` / `RawB`, 0–2047).

Physical values are a linear map
`Phys = Min + Raw/2047 · (Max − Min)` using the per-channel `FastScale`.

## Variants

`DefaultConfig` selects one of three format variants. Checksum coverage and
slow-message length differ per variant; a stream whose slow-channel tag does
not match the declared variant is rejected with
`SlowDiscardVariantMismatch` on first detection.

| Variant                | CRC poly (x^4…)   | Init | Status in CRC | Slow message | Frames |
|------------------------|-------------------|------|---------------|--------------|--------|
| `VariantRecommended`   | `0x13` (x⁴+x+1)   | 5    | yes           | 16 bits      | 8      |
| `VariantLegacy`        | `0x1D` (x⁴+x³+x²+1)| 5   | no            | 12 bits      | 6      |
| `VariantExtended`      | `0x07` (x⁴+x²+x+1)| 0    | yes           | 24 bits      | 12     |

A slow message is laid out MSB-first as
`tag(2) | id(2) | data | crc(4)`, with tag `00/01/10` for
recommended/legacy/extended respectively. The 4-bit CRC covers every preceding
bit of that message using the variant's polynomial.

## Tolerance parameters (`ChannelConfig`)

| Field             | Meaning                                                                 |
|-------------------|-------------------------------------------------------------------------|
| `TickUS`          | nominal unit-tick duration in microseconds (default `3.0`).             |
| `SyncTol`         | relative sync-pulse/clock-rate tolerance, e.g. `0.02` = ±2%.            |
| `WidthTol`        | per-pulse width tolerance in ticks; a pulse must snap within this many ticks of an integer (default `0.6`). Must exceed injected phase jitter. |
| `PauseTicks`      | optional trailing pause pulse width; `0` disables it.                   |
| `PersistThreshold`| consecutive error-flag frames before a transient becomes persistent.    |

Per-frame the receiver calibrates its measured tick time from the 56-tick
sync pulse, absorbing frame-to-frame clock-rate variation. Data pulses are
then quantized to the nearest integer tick using the calibrated rate; a pulse
farther than `WidthTol` from any integer is a timing fault.

## Fault classification

### Frame / timing faults (`EventFrameError`)

| Class                     | Meaning                                                            |
|---------------------------|--------------------------------------------------------------------|
| `FrameErrSyncMissing`     | A legal nibble-width (or malformed 57–64 tick) pulse where the 56-tick sync must be. |
| `FrameErrPulseWidth`      | A pulse left the legal timing window (32–55 ticks mid-frame, or sync-rate out of `SyncTol`). |
| `FrameErrIllegalNibble`   | A reserved 28–31 tick pulse where a nibble is required.           |
| `FrameErrCRC`             | Frame checksum mismatch — **the frame produces no reading**.      |
| `FrameErrNoiseGlitch`     | Too-short/non-integer or excessively wide pulse (spurious edge).   |

Any frame-level failure invalidates the in-progress slow message, reported
with `SlowDiscardLinkCorrupt`.

### Slow-channel discards (`EventSlowDiscard`)

| Reason                       | Meaning                                                        |
|------------------------------|----------------------------------------------------------------|
| `SlowDiscardLostFrame`       | Sequence counter jumped by 2 — an intervening frame vanished. |
| `SlowDiscardDuplicate`       | Same sequence counter as the previous accepted frame.          |
| `SlowDiscardOutOfOrder`      | A stale/older counter arrived (frames reordered).             |
| `SlowDiscardCRC`             | Assembled message failed its 4-bit CRC.                       |
| `SlowDiscardVariantMismatch` | Message tag differs from the declared variant.                |
| `SlowDiscardLinkCorrupt`     | A fast-frame CRC/timing fault invalidated partial assembly.   |

Partial assembly is reset on every discard, so an incorrect message is never
produced.

### Sensor error flags (`EventSensorFault`)

`FaultTracker` debounces the OR of `ErrA`/`ErrB` over accepted frames:

- `FaultTransient` — the first erroring frame after a healthy run.
- `FaultPersistent` — `PersistThreshold` consecutive erroring frames.
- `FaultRecovered` — the first healthy frame after an error run; the state
  immediately returns to normal and is observable via this report.

Stable states (ongoing healthy or ongoing persistent) emit no report.

## Concurrent channels

`Manager` runs one independent receiver goroutine per channel and merges
events onto a single output channel.

```go
out := make(chan sent.Event, 256)
mgr := sent.NewManager(out)
mgr.Add(sent.DefaultConfig("a", sent.VariantRecommended))
mgr.Add(sent.DefaultConfig("b", sent.VariantLegacy))

go func() {
	for ev := range out { /* events from all channels */ }
}()

mgr.Push("a", edges)        // deliver edges from a signal source
mgr.Remove("a")             // stop and fully reclaim the channel
mgr.Close()                 // stop all channels and wait for cleanup
```

Isolation guarantees:

- A slow downstream consumer never blocks a receiver: events that cannot be
  sent are dropped and counted in that channel's `ChannelStats.Overflow`.
- A faulty channel does not contaminate others; state is per-channel.
- Channels may be added and removed at runtime. `Remove` closes the input,
  waits for the goroutine to exit, and deletes all of its state.
- Backpressure on `Push` returns `ErrChannelBackpressure` instead of blocking
  the caller.

## Determinism and replay

- `Encoder` uses a seedable, stateless `xorshift64*` RNG. The same seed and
  inputs produce a byte-identical edge stream, and `Decode` is a pure
  function, so reports are reproducible event-by-event.
- `Recording` persists config, specs, injection ops, seed and edges as JSON.
  `Replay` decodes a recording; if only specs/ops/seed are stored, the edge
  stream is regenerated identically.

## Fault injection

`InjectOp` mutates the encoded stream:

`OpDropFrame`, `OpCorruptCRC`, `OpIllegalNibble`, `OpPulseWidth`,
`OpMissingSync`, `OpGlitch`, `OpDuplicateFrame`, `OpSwapFrames`.

Nibble-level injections take an optional `With` slot (0–5).

## Tests

```
go test ./...        # add -race for the concurrency tests
go test -cover ./...
```

Coverage includes frame errors, slow-channel cross-frame assembly and all
discard reasons, variant polynomials and lengths, fault debounce/recovery,
concurrent channel isolation with dynamic add/remove, and seeded replay.
