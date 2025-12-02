package varfloat

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
)

// mantBits controls the number of bits used to quantize the mantissa.
// It can be tuned via SetMantissaBits. The default is 10 bits
// (≈0.1% relative precision).
var mantBits = 10

// mantMax is derived from mantBits. It is updated whenever mantBits changes.
var mantMax = (1 << mantBits) - 1

// SetMantissaBits configures the number of bits used to quantize the mantissa.
// bits must be in [0, 52] (float64 has a 52-bit mantissa). Changing this
// affects both encoding and decoding; all values encoded with one setting
// must be decoded with the same setting.
func SetMantissaBits(bits int) error {
	if bits < 0 || bits > 52 {
		return errors.New("varfloat: mantissa bits must be between 0 and 52")
	}
	mantBits = bits
	if bits == 0 {
		mantMax = 0
	} else {
		mantMax = (1 << mantBits) - 1
	}
	return nil
}

// BitsForMaxRelError returns a mantissa bit count that targets a given maximum
// relative error for varfloat-encoded values.
//
// Roughly, the relative quantization step is ~1/(2^bits). This helper chooses:
//
//   bits ≈ ceil(log2(1 / maxRelErr))
//
// and clamps the result into [0, 52]. maxRelErr must be in (0, 1).
func BitsForMaxRelError(maxRelErr float64) (int, error) {
	if maxRelErr <= 0 || maxRelErr >= 1 {
		return 0, errors.New("varfloat: maxRelErr must be in (0, 1)")
	}
	bits := int(math.Ceil(math.Log2(1.0 / maxRelErr)))
	if bits < 0 {
		bits = 0
	} else if bits > 52 {
		bits = 52
	}
	return bits, nil
}

// Append encodes v as a varfloat and appends it to dst.
// It returns the extended slice.
func Append(dst []byte, v float64) []byte {
	// Special case zero: single-byte encoding 0x00
	if v == 0 {
		return append(dst, 0)
	}

	sign := 0
	if v < 0 {
		sign = 1
		v = -v
	}

	m, e := math.Frexp(v) // v = m * 2^e, 0.5 <= m < 1
	m *= 2
	e -= 1 // now v = (m/2) * 2^e', with 1 <= m < 2

	// Quantize mantissa in [1, 2) to mantBits.
	var mant uint64
	if mantMax > 0 {
		mant = uint64(math.Round((m - 1.0) * float64(mantMax)))
	}

	// ZigZag encode exponent.
	ez := zigZagEncode(int64(e))

	// Pack header: ((ez + 1) << 1) | sign.
	// ez+1 ensures header != 0 so 0x00 is reserved for zero.
	header := (ez + 1) << 1
	header |= uint64(sign)

	// Encode header and mant as standard uvarints.
	var buf [10]byte

	n := binary.PutUvarint(buf[:], header)
	dst = append(dst, buf[:n]...)

	n = binary.PutUvarint(buf[:], mant)
	dst = append(dst, buf[:n]...)

	return dst
}

// Consume decodes a varfloat from the beginning of b.
// It returns the decoded value, the number of bytes consumed, and an error.
func Consume(b []byte) (float64, int, error) {
	if len(b) == 0 {
		return 0, 0, io.ErrUnexpectedEOF
	}

	// Zero sentinel.
	if b[0] == 0 {
		return 0, 1, nil
	}

	// Decode header.
	header, n := binary.Uvarint(b)
	if n <= 0 {
		return 0, 0, errors.New("varfloat: invalid header uvarint")
	}

	sign := int(header & 1)
	ezPlus1 := header >> 1
	if ezPlus1 == 0 {
		return 0, 0, errors.New("varfloat: invalid header (zero ezPlus1)")
	}
	ez := ezPlus1 - 1

	e := zigZagDecode(ez) // exponent e'

	// Decode mantissa.
	mant, mlen := binary.Uvarint(b[n:])
	if mlen <= 0 {
		return 0, 0, errors.New("varfloat: invalid mantissa uvarint")
	}

	// Reconstruct mantissa m' in [1, 2).
	mPrime := 1.0
	if mantMax > 0 {
		mPrime = 1.0 + float64(mant)/float64(mantMax)
	}
	// m = m'/2, in [0.5, 1)
	m := mPrime * 0.5

	// v = m * 2^e'
	v := math.Ldexp(m, int(e))

	if sign == 1 {
		v = -v
	}

	return v, n + mlen, nil
}

// EncodeFloat encodes v into a fresh buffer using the given mantissa precision (bits).
// It is a convenience wrapper around SetMantissaBits + Append.
func EncodeFloat(v float64, bits int) ([]byte, error) {
	// Save current mantissa configuration.
	prevBits := mantBits
	if err := SetMantissaBits(bits); err != nil {
		return nil, err
	}
	defer func() {
		mantBits = prevBits
		if prevBits == 0 {
			mantMax = 0
		} else {
			mantMax = (1 << prevBits) - 1
		}
	}()

	var buf []byte
	buf = Append(buf, v)
	return buf, nil
}

// DecodeFloat decodes a varfloat-encoded value from b using the given mantissa precision (bits).
// The same bits must have been used for encoding.
func DecodeFloat(b []byte, bits int) (float64, int, error) {
	// Save current mantissa configuration.
	prevBits := mantBits
	if err := SetMantissaBits(bits); err != nil {
		return 0, 0, err
	}
	defer func() {
		mantBits = prevBits
		if prevBits == 0 {
			mantMax = 0
		} else {
			mantMax = (1 << prevBits) - 1
		}
	}()

	return Consume(b)
}

// EncodeFloatSlice encodes a slice of float64 values with the given mantissa
// precision (bits) into a single buffer. It prefixes the data with the length
// of the slice encoded as a uvarint.
func EncodeFloatSlice(values []float64, bits int) ([]byte, error) {
	// Save current mantissa configuration.
	prevBits := mantBits
	if err := SetMantissaBits(bits); err != nil {
		return nil, err
	}
	defer func() {
		mantBits = prevBits
		if prevBits == 0 {
			mantMax = 0
		} else {
			mantMax = (1 << prevBits) - 1
		}
	}()

	var buf []byte
	// Prefix length.
	var lenBuf [10]byte
	n := binary.PutUvarint(lenBuf[:], uint64(len(values)))
	buf = append(buf, lenBuf[:n]...)

	for _, v := range values {
		buf = Append(buf, v)
	}
	return buf, nil
}

// DecodeFloatSlice decodes a slice of float64 values encoded by EncodeFloatSlice
// using the given mantissa precision (bits).
func DecodeFloatSlice(b []byte, bits int) ([]float64, int, error) {
	// Save current mantissa configuration.
	prevBits := mantBits
	if err := SetMantissaBits(bits); err != nil {
		return nil, 0, err
	}
	defer func() {
		mantBits = prevBits
		if prevBits == 0 {
			mantMax = 0
		} else {
			mantMax = (1 << prevBits) - 1
		}
	}()

	// Read length.
	length, n := binary.Uvarint(b)
	if n <= 0 {
		return nil, 0, errors.New("varfloat: invalid slice length uvarint")
	}
	b = b[n:]
	consumed := n

	values := make([]float64, 0, length)
	for i := uint64(0); i < length; i++ {
		v, used, err := Consume(b)
		if err != nil {
			return nil, 0, err
		}
		values = append(values, v)
		b = b[used:]
		consumed += used
	}

	return values, consumed, nil
}

// AppendIntBounded encodes an integer n that is known to lie in [min, max]
// as a varfloat with a specific mantissa precision (bits).
//
// The same (min, max, bits) must be used when decoding via ConsumeIntBounded.
// bits controls the tradeoff between size and precision; it must be in [0, 52].
func AppendIntBounded(dst []byte, n, min, max int64, bits int) ([]byte, error) {
	if min > max {
		return nil, errors.New("varfloat: min must be <= max")
	}
	if n < min || n > max {
		return nil, errors.New("varfloat: value out of bounds")
	}

	// Save current mantissa configuration.
	prevBits := mantBits
	if err := SetMantissaBits(bits); err != nil {
		return nil, err
	}
	// Restore previous mantissa configuration after encoding.
	defer func() {
		mantBits = prevBits
		if prevBits == 0 {
			mantMax = 0
		} else {
			mantMax = (1 << prevBits) - 1
		}
	}()

	// Map integer to float64 in the same numeric space.
	v := float64(n)
	return Append(dst, v), nil
}

// ConsumeIntBounded decodes a varfloat produced by AppendIntBounded back into
// an integer in [min, max], using the same mantissa precision (bits).
//
// Because the varfloat encoding is approximate, the decoded float is rounded
// to the nearest integer and then clamped into [min, max].
func ConsumeIntBounded(b []byte, min, max int64, bits int) (int64, int, error) {
	if min > max {
		return 0, 0, errors.New("varfloat: min must be <= max")
	}

	// Save current mantissa configuration.
	prevBits := mantBits
	if err := SetMantissaBits(bits); err != nil {
		return 0, 0, err
	}
	// Restore previous mantissa configuration after decoding.
	defer func() {
		mantBits = prevBits
		if prevBits == 0 {
			mantMax = 0
		} else {
			mantMax = (1 << prevBits) - 1
		}
	}()

	v, n, err := Consume(b)
	if err != nil {
		return 0, 0, err
	}

	iv := int64(math.Round(v))
	if iv < min {
		iv = min
	} else if iv > max {
		iv = max
	}

	return iv, n, nil
}

// AppendIntAuto encodes an integer n in [min, max], choosing a mantissa
// precision automatically from the bounds. The same bounds must be used when
// decoding via ConsumeIntAuto.
//
// The heuristic aims to provide enough precision over the given range without
// forcing callers to think in terms of mantissa bits:
//   bits ≈ ceil(log2(max-min+1)), clamped to [0, 52].
func AppendIntAuto(dst []byte, n, min, max int64) ([]byte, error) {
	if min > max {
		return nil, errors.New("varfloat: min must be <= max")
	}
	if n < min || n > max {
		return nil, errors.New("varfloat: value out of bounds")
	}
	width := uint64(max - min)
	bits := autoBitsForWidth(width)
	return AppendIntBounded(dst, n, min, max, bits)
}

// ConsumeIntAuto decodes an integer previously encoded with AppendIntAuto,
// using the same bounds.
func ConsumeIntAuto(b []byte, min, max int64) (int64, int, error) {
	if min > max {
		return 0, 0, errors.New("varfloat: min must be <= max")
	}
	width := uint64(max - min)
	bits := autoBitsForWidth(width)
	return ConsumeIntBounded(b, min, max, bits)
}

// autoBitsForWidth chooses a mantissa bit count for a given integer width.
// width is assumed to be >= 0.
func autoBitsForWidth(width uint64) int {
	if width == 0 {
		return 0
	}
	// Need enough distinct steps to "cover" the range width+1.
	steps := width + 1
	// ceil(log2(steps))
	bits := 0
	for (uint64(1) << bits) < steps && bits < 52 {
		bits++
	}
	return bits
}

// BitsForIntRange returns a mantissa bit count that can distinguish all
// integer values in [min, max] when using the bounded-int helpers.
//
// It uses the same heuristic as AppendIntAuto:
//
//   bits ≈ ceil(log2(max-min+1)), clamped to [0, 52].
func BitsForIntRange(min, max int64) (int, error) {
	if min > max {
		return 0, errors.New("varfloat: min must be <= max")
	}
	width := uint64(max - min)
	return autoBitsForWidth(width), nil
}

// BitsForIntMaxError returns a mantissa bit count that targets a given maximum
// absolute error for integers in [min, max] when using lossy bounded-int
// encoding (e.g. mapping ints to floats with some tolerated error).
//
// The idea:
//
//   - Let rangeWidth = max-min.
//   - The quantization step size is on the order of rangeWidth / 2^bits.
//   - To keep the absolute error <= maxAbsErr, we choose:
//
//       bits ≈ ceil(log2(rangeWidth / maxAbsErr)), clamped to [0, 52].
//
// maxAbsErr must be > 0. If maxAbsErr >= rangeWidth, this returns 0.
func BitsForIntMaxError(min, max, maxAbsErr int64) (int, error) {
	if min > max {
		return 0, errors.New("varfloat: min must be <= max")
	}
	if maxAbsErr <= 0 {
		return 0, errors.New("varfloat: maxAbsErr must be > 0")
	}
	width := float64(max - min)
	if width <= 0 {
		return 0, nil
	}
	if float64(maxAbsErr) >= width {
		return 0, nil
	}
	ratio := width / float64(maxAbsErr)
	bits := int(math.Ceil(math.Log2(ratio)))
	if bits < 0 {
		bits = 0
	} else if bits > 52 {
		bits = 52
	}
	return bits, nil
}

// zigZagEncode maps signed integers to unsigned so that small-magnitude
// negatives get small codes (like protobuf).
func zigZagEncode(x int64) uint64 {
	return uint64(uint64(x<<1) ^ uint64((x >> 63)))
}

// zigZagDecode reverses zigZagEncode.
func zigZagDecode(u uint64) int64 {
	// If LSB is 0, this is non-negative; if 1, negative.
	return int64((u >> 1) ^ uint64(-(int64(u & 1))))
}

// EncodeFloat64Fixed encodes v as an 8-byte IEEE 754 big-endian float64.
// This is a convenience helper for comparison with varfloat encodings.
func EncodeFloat64Fixed(v float64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], math.Float64bits(v))
	return buf[:]
}

// DecodeFloat64Fixed decodes an 8-byte IEEE 754 big-endian float64.
func DecodeFloat64Fixed(b []byte) (float64, int, error) {
	if len(b) < 8 {
		return 0, 0, io.ErrUnexpectedEOF
	}
	u := binary.BigEndian.Uint64(b[:8])
	return math.Float64frombits(u), 8, nil
}

// EncodeFloat32Fixed encodes v as a 4-byte IEEE 754 big-endian float32.
func EncodeFloat32Fixed(v float32) []byte {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], math.Float32bits(v))
	return buf[:]
}

// DecodeFloat32Fixed decodes a 4-byte IEEE 754 big-endian float32.
func DecodeFloat32Fixed(b []byte) (float32, int, error) {
	if len(b) < 4 {
		return 0, 0, io.ErrUnexpectedEOF
	}
	u := binary.BigEndian.Uint32(b[:4])
	return math.Float32frombits(u), 4, nil
}

// EncodeInt64Fixed encodes v as an 8-byte big-endian signed integer.
func EncodeInt64Fixed(v int64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(v))
	return buf[:]
}

// DecodeInt64Fixed decodes an 8-byte big-endian signed integer.
func DecodeInt64Fixed(b []byte) (int64, int, error) {
	if len(b) < 8 {
		return 0, 0, io.ErrUnexpectedEOF
	}
	u := binary.BigEndian.Uint64(b[:8])
	return int64(u), 8, nil
}

// EncodeInt32Fixed encodes v as a 4-byte big-endian signed integer.
func EncodeInt32Fixed(v int32) []byte {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], uint32(v))
	return buf[:]
}

// DecodeInt32Fixed decodes a 4-byte big-endian signed integer.
func DecodeInt32Fixed(b []byte) (int32, int, error) {
	if len(b) < 4 {
		return 0, 0, io.ErrUnexpectedEOF
	}
	u := binary.BigEndian.Uint32(b[:4])
	return int32(u), 4, nil
}
