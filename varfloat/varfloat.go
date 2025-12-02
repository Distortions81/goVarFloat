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
