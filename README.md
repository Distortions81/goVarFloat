GoVarFloat
==========

GoVarFloat is a tiny Go library that implements a variable‑length encoding for `float64` values. It lets you trade precision for size by tuning the number of mantissa bits, and also provides helpers for encoding bounded integers via the same mechanism.

The core type is just the standard `float64`; the library only defines functions for encoding/decoding to/from byte slices.

Basic usage
-----------

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

Controlling precision
---------------------

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
```

Encoding bounded integers
-------------------------

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
```

Notes
-----

- `SetMantissaBits(bits)` accepts `0 <= bits <= 52` (float64 mantissa width).
- Encode and decode must use the same mantissa bit setting.
- For `AppendIntBounded` / `ConsumeIntBounded`, you must use the same `(min, max, bits)` triple for encoding and decoding.
