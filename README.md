GoVarFloat
==========

GoVarFloat is a tiny Go library that implements a variable‑length encoding for `float64` values. It lets you trade precision for size by tuning the number of mantissa bits, and also provides helpers for encoding bounded integers via the same mechanism.

The core type is just the standard `float64`; the library only defines functions for encoding/decoding to/from byte slices.

Float varfloat encode/decode
----------------------------

```go
import "github.com/Distortions81/goVarFloat/varfloat"

// Encode a float64 with default precision (10 mantissa bits).
var buf []byte
buf = varfloat.Append(buf, 3.14159)

// Decode it back.
v, n, err := varfloat.Consume(buf)
if err != nil {
    // handle error
}
_ = v   // decoded value
_ = n   // bytes consumed
```

If you prefer an all‑in‑one helper that takes the mantissa bit precision explicitly, you can use `EncodeFloat` / `DecodeFloat`:

```go
// Encode with 12 mantissa bits into a fresh buffer.
buf, err := varfloat.EncodeFloat(3.14159, 12)
if err != nil {
    // handle error
}

// Decode with the same mantissa precision.
v, _, err := varfloat.DecodeFloat(buf, 12)
if err != nil {
    // handle error
}
```

Controlling precision globally
------------------------------

You can configure the number of mantissa bits used to quantize the value. More bits → higher precision, but potentially larger encodings.

```go
// Use 16 mantissa bits instead of the default 10.
if err := varfloat.SetMantissaBits(16); err != nil {
    // handle error
}

var buf []byte
values := []float64{0, 1.0, 3.14159, -0.00123, 1e6}
for _, x := range values {
    buf = varfloat.Append(buf[:0], x)
    dec, _, err := varfloat.Consume(buf)
    if err != nil {
        // handle error
    }
    // use dec
}

If you want help choosing a mantissa bit count, you can derive it from either a target relative error or an integer range:

```go
// Choose bits for a desired max relative error (e.g. 0.1%).
bitsForErr, err := varfloat.BitsForMaxRelError(0.001) // ~10 bits
if err != nil {
    // handle error
}

// Choose bits that can distinguish every integer in [min,max].
bitsForRange, err := varfloat.BitsForIntRange(0, 1000)
if err != nil {
    // handle error
}

// Or choose bits for integers in [min,max] given a tolerated absolute error.
// For example, keep the lossy error within +/- 5 units over [0, 10_000].
bitsForIntErr, err := varfloat.BitsForIntMaxError(0, 10_000, 5)
if err != nil {
    // handle error
}
```

Using configs and concurrency
-----------------------------

The package exposes both:

- Package-level helpers (`Append`, `Consume`, `EncodeFloat`, etc.) that use a global `DefaultConfig`.
- A `Config` type that you can use directly for clearer, concurrency-safe code.

For simple, single-threaded usage, the package-level functions are fine:

```go
// Uses DefaultConfig.MantissaBits (10 by default).
buf := varfloat.Append(nil, 3.14159)
v, _, err := varfloat.Consume(buf)
```

To adjust the global default mantissa bits, you can call:

```go
// Not safe to change concurrently with other uses of the package-level helpers.
if err := varfloat.SetMantissaBits(16); err != nil {
    // handle error
}
```

For concurrent code, or when you want explicit control, prefer creating your own `Config`:

```go
// Create a config with 12 mantissa bits.
cfg, err := varfloat.NewConfig(12)
if err != nil {
    // handle error
}

// Use cfg.Append / cfg.Consume instead of the globals.
var buf []byte
buf = cfg.Append(buf, 42.0)
val, _, err := cfg.Consume(buf)
if err != nil {
    // handle error
}
```

You can keep a `Config` per goroutine or per data stream to avoid any global mutable state while still reusing the same encoding logic. The integer and slice helpers (`EncodeFloats`, `AppendIntBounded`, `AppendIntAuto`, etc.) already use per-call `Config` values internally, so they’re safe to call concurrently. 

Choosing bits and “auto bits”
-----------------------------

There are three ways to pick how many mantissa bits to use, depending on what you care about:

- **Target relative error (floats):**  
  Use `BitsForMaxRelError(maxRelErr)` when you care about relative precision, e.g. “keep the error < 0.1%”.
  - This is typically what you want for general `float64` values.

- **Lossless over a bounded int range:**  
  Use `BitsForIntRange(min, max)` when you want to be able to distinguish *every* integer in `[min,max]` with the bounded-int helpers.
  - `BitsForIntRange` is also what `AppendIntAuto` / `ConsumeIntAuto` use internally:
    - `AppendIntAuto(dst, n, min, max)` picks bits from `[min,max]` so the encoding is effectively lossless over that range.
    - `ConsumeIntAuto(b, min, max)` recomputes the same bits from the bounds.

- **Lossy int→float→int with max absolute error:**  
  Use `BitsForIntMaxError(min, max, maxAbsErr)` when you’re okay with lossy integer storage but want to bound the absolute error, e.g. “I’m fine being ±5 off”.
  - This is for cases where you intentionally accept lossy integer quantization to get even smaller encodings.
```

Integer varfloat encode/decode
------------------------------

If you know your integers are within a specific range, you can encode them using the same varfloat machinery and an explicit precision:

```go
min, max := int64(0), int64(1000)
bits := 12 // mantissa bits for precision/size trade‑off

buf, err := varfloat.AppendIntBounded(nil, 42, min, max, bits)
if err != nil {
    // handle error
}

val, _, err := varfloat.ConsumeIntBounded(buf, min, max, bits)
if err != nil {
    // handle error
}
// val is the decoded integer (rounded and clamped to [min, max])

// If you don't want to pick mantissa bits yourself, you can let the
// library choose them from the bounds:
buf, err = varfloat.AppendIntAuto(nil, 42, min, max)
val, _, err = varfloat.ConsumeIntAuto(buf, min, max)
```

Fixed-size helpers (for comparison)
-----------------------------------

If you want to compare varfloat encodings against normal fixed-size encodings, or just need simple fixed-size helpers, the package also exposes:

```go
// 8-byte IEEE 754 float64 (big-endian)
f64Bytes := varfloat.EncodeFloat64Fixed(3.14159)
f64, _, err := varfloat.DecodeFloat64Fixed(f64Bytes)

// 4-byte IEEE 754 float32 (big-endian)
f32Bytes := varfloat.EncodeFloat32Fixed(1.5)
f32, _, err := varfloat.DecodeFloat32Fixed(f32Bytes)

// 8-byte and 4-byte signed integers (big-endian)
i64Bytes := varfloat.EncodeInt64Fixed(123456789)
i64, _, err := varfloat.DecodeInt64Fixed(i64Bytes)

i32Bytes := varfloat.EncodeInt32Fixed(12345)
i32, _, err := varfloat.DecodeInt32Fixed(i32Bytes)

// Slice encode/decode with a length prefix.
// EncodeFloats/DecodeFloats are aliases for EncodeFloatSlice/DecodeFloatSlice.
vals := []float64{0, 1, 3.14159}
sliceBytes, err := varfloat.EncodeFloats(vals, 10)
if err != nil {
    // handle error
}
decodedVals, _, err := varfloat.DecodeFloats(sliceBytes, 10)
if err != nil {
    // handle error
}
```

Space-saving examples
---------------------

Varfloats shine when you have:

- Bounded ranges.
- Limited precision requirements.
- Many zeros or repeated/small values.

Below are a few concrete examples, along with actual measured savings from small experiments.

### 1. Sparse 3D coordinates (≈3.4x smaller in a sample)

Imagine a stream of 3D positions where:

- Most points are at the origin `(0,0,0)`.
- Non-zero positions are within `[-1000, 1000]` mm in each axis.
- You only need ~1mm precision.

You can:

- Store coordinates as millimeters in `int32` (`[-1_000_000, 1_000_000]`).
- Encode them with `AppendIntBounded` for each axis.

```go
package main

import (
    "fmt"
    "math/rand"

    "github.com/Distortions81/goVarFloat/varfloat"
)

type Vec3 struct{ X, Y, Z int32 } // millimeters

func main() {
    // Generate sparse positions: 90% at origin, 10% random in [-1000,1000] mm.
    positions := make([]Vec3, 0, 10000)
    for i := 0; i < cap(positions); i++ {
        if rand.Float64() < 0.9 {
            positions = append(positions, Vec3{0, 0, 0})
        } else {
            positions = append(positions, Vec3{
                X: int32(rand.Intn(2001) - 1000),
                Y: int32(rand.Intn(2001) - 1000),
                Z: int32(rand.Intn(2001) - 1000),
            })
        }
    }

    // Baseline: fixed-size encoding (3 * int32).
    fixedBytes := 0
    for range positions {
        fixedBytes += 3 * 4 // 3 axes * 4 bytes each
    }

    // Varfloat encoding with bounded ints and 10 mantissa bits.
    const bits = 10
    const min, max = int64(-1_000_000), int64(1_000_000)

    var vfBuf []byte
    for _, p := range positions {
        var err error
        vfBuf, err = varfloat.AppendIntBounded(vfBuf, int64(p.X), min, max, bits)
        if err != nil {
            panic(err)
        }
        vfBuf, err = varfloat.AppendIntBounded(vfBuf, int64(p.Y), min, max, bits)
        if err != nil {
            panic(err)
        }
        vfBuf, err = varfloat.AppendIntBounded(vfBuf, int64(p.Z), min, max, bits)
        if err != nil {
            panic(err)
        }
    }

    fmt.Printf("fixed-size bytes: %d\n", fixedBytes)
    fmt.Printf("varfloat bytes:   %d\n", len(vfBuf))
    fmt.Printf("compression:      %.1fx smaller\n", float64(fixedBytes)/float64(len(vfBuf)))
}
```

Because:

- Zero values encode to a single byte (`0x00`).
- Small exponents/mantissas for bounded integers yield short varints.

In one sample run with 10,000 positions (90% at origin), this pattern produced:

- Fixed-size: 120,000 bytes
- Varfloat:   35,639 bytes
- Compression: **3.37x smaller**

Scaling that up (same distribution):

- 1 million points: from ~12 MB down to ~3.6 MB.

Actual numbers will vary with your sparsity and range, but this gives a realistic ballpark.

### 2. Percentages / probabilities (≈5.3x smaller in a sample)

Suppose you have a large array of percentages in `[0,1]` and you’re fine with ~0.1% absolute precision.

- Baseline: `float64` → 8 bytes per value.
- Varfloat: map to `[0, 10_000]` as ints with 0.01% steps, then use bounded int encodings.

```go
min, max := int64(0), int64(10_000) // 0.00%..100.00%
bits := 10                          // ~0.1%-ish relative precision

encodePercent := func(p float64) ([]byte, error) {
    if p < 0 {
        p = 0
    } else if p > 1 {
        p = 1
    }
    // store as 0..10000
    n := int64(math.Round(p * 10_000))
    return varfloat.AppendIntBounded(nil, n, min, max, bits)
}

decodePercent := func(b []byte) (float64, int, error) {
    n, used, err := varfloat.ConsumeIntBounded(b, min, max, bits)
    if err != nil {
        return 0, 0, err
    }
    return float64(n) / 10_000.0, used, nil
}
```

In practice, many realistic percentages cluster near a few values (0, 1, small probabilities), yielding very short encodings compared to fixed 8-byte floats.

In a sample with 10,000 values (70% exact zeros), encoding as bounded ints with 10 mantissa bits produced:

- Fixed-size: 80,000 bytes
- Varfloat:   15,010 bytes
- Compression: **5.33x smaller**

Scaling that up:

- 1 million percentages: from ~8.0 MB down to ~1.5 MB.

### 3. Time series deltas (≈3.8x smaller in a sample)

For many signals (e.g. sensor readings, audio levels, metrics), the absolute value may be large but *deltas* between samples are small.

- Baseline: store each sample as `float64` or `int64`.
- Varfloat: store the first sample as fixed-size, then encode deltas with `AppendIntBounded` in a small symmetric range.

```go
// Example: int64 samples with small step-to-step changes
samples := []int64{/* ... streaming data ... */}

const (
    bits     = 8
    deltaMin = -1000
    deltaMax = 1000
)

// Encode: first value fixed, then varfloat deltas.
var buf []byte
buf = append(buf, varfloat.EncodeInt64Fixed(samples[0])...)
prev := samples[0]
for _, s := range samples[1:] {
    delta := s - prev
    if delta < deltaMin {
        delta = deltaMin
    } else if delta > deltaMax {
        delta = deltaMax
    }
    var err error
    buf, err = varfloat.AppendIntBounded(buf, delta, deltaMin, deltaMax, bits)
    if err != nil {
        panic(err)
    }
    prev = s
}
```

If most deltas are small (e.g. within [-10,10]), the varfloat encoding will mostly use 1–2 bytes per delta instead of 8 bytes per `int64`.

In a sample with 10,000 `int64` samples and small step-to-step changes, this pattern produced:

- Fixed-size: 80,000 bytes
- Varfloat:   20,898 bytes
- Compression: **3.83x smaller**

Scaling that up:

- 1 million samples: from ~8.0 MB down to ~2.1 MB.

Adjust `bits`, ranges, and quantization schemes (e.g. millimeters, percent steps, delta bounds) to fit your data’s scale and acceptable error. This is where GoVarFloat delivers the largest practical space savings. 

Notes
-----

- `SetMantissaBits(bits)` accepts `0 <= bits <= 52` (float64 mantissa width).
- Encode and decode must use the same mantissa bit setting.
- For `AppendIntBounded` / `ConsumeIntBounded`, you must use the same `(min, max, bits)` triple for encoding and decoding.
