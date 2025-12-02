package varfloat

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"math"
)

// Config controls how varfloats are encoded and decoded.
// MantissaBits is the number of bits used to quantize the mantissa.
type Config struct {
	MantissaBits int
}

// DefaultConfig is used by the package-level helpers (Append, Consume, etc).
// Changing it is not safe for concurrent use; for concurrent code, prefer
// creating your own Config value and using its methods.
var DefaultConfig = Config{MantissaBits: 10}

// NewConfig creates a Config with the given mantissa bit count.
// bits must be in [0, 52] (float64 has a 52-bit mantissa).
func NewConfig(bits int) (Config, error) {
	if bits < 0 || bits > 52 {
		return Config{}, errors.New("varfloat: mantissa bits must be between 0 and 52")
	}
	return Config{MantissaBits: bits}, nil
}

// SetMantissaBits configures the mantissa bits on DefaultConfig.
// This is a convenience for simple, non-concurrent use. For concurrent code,
// prefer using an explicit Config instead of mutating global state.
func SetMantissaBits(bits int) error {
	cfg, err := NewConfig(bits)
	if err != nil {
		return err
	}
	DefaultConfig = cfg
	return nil
}

// BitsForMaxRelError returns a mantissa bit count that targets a given maximum
// relative error for varfloat-encoded values.
//
// Roughly, the relative quantization step is ~1/(2^bits). This helper chooses:
//
//	bits ≈ ceil(log2(1 / maxRelErr))
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

// MaxRelErrorForBits returns an approximate maximum relative error for a given
// mantissa bit count. It is roughly the inverse of BitsForMaxRelError:
//
//	maxRelErr ≈ 1 / (2^bits)
//
// The result is always positive; for bits outside [0,52] it clamps as if the
// bits had first been clamped into that range.
func MaxRelErrorForBits(bits int) float64 {
	if bits < 0 {
		bits = 0
	} else if bits > 52 {
		bits = 52
	}
	// The quantization step is on the order of 2^-bits.
	return math.Pow(2, -float64(bits))
}

// QuantizationStep returns the approximate quantization step size for a given
// mantissa bit count, in units of "fractions of 1.0". For example, with bits=10
// the step is roughly 1/1024.
//
// This is just an alias for MaxRelErrorForBits, provided for clearer naming in
// some contexts.
func QuantizationStep(bits int) float64 {
	return MaxRelErrorForBits(bits)
}

// QuantizeIntDown quantizes v down to the nearest multiple of step using
// integer division, i.e. it returns floor(v/step)*step for non-negative v.
// This is useful when you only care about counts or buckets of size step and
// want a simple lossy representation.
//
// If step <= 0, QuantizeIntDown returns v unchanged.
func QuantizeIntDown(v, step int64) int64 {
	if step <= 0 {
		return v
	}
	return (v / step) * step
}

// MaxIntQuantizationError returns the maximum absolute error introduced by
// QuantizeIntDown for non-negative values when using the given step size.
// Since QuantizeIntDown always rounds down to a multiple of step, the error
// is in [0, step) and this helper simply returns step-1 for convenience.
//
// If step <= 0, it returns 0.
func MaxIntQuantizationError(step int64) int64 {
	if step <= 0 {
		return 0
	}
	return step - 1
}

// FloatEncoder is a convenience wrapper that holds a chosen mantissa precision
// and exposes helpers for encoding single floats or slices.
type FloatEncoder struct {
	Bits int
}

// NewFloatEncoder constructs a FloatEncoder from a desired maximum relative
// error. It simply calls BitsForMaxRelError and stores the chosen mantissa bit
// count.
func NewFloatEncoder(maxRelErr float64) (*FloatEncoder, error) {
	bits, err := BitsForMaxRelError(maxRelErr)
	if err != nil {
		return nil, err
	}
	return &FloatEncoder{Bits: bits}, nil
}

// Encode encodes a single float64 using the encoder's mantissa bits.
func (e *FloatEncoder) Encode(v float64) ([]byte, error) {
	return EncodeFloat(v, e.Bits)
}

// EncodeSlice encodes a slice of float64 values using the encoder's mantissa
// bits.
func (e *FloatEncoder) EncodeSlice(values []float64) ([]byte, error) {
	return EncodeFloats(values, e.Bits)
}

// Vec3Encoder is a convenience wrapper that holds a chosen mantissa precision
// and exposes helpers for encoding Vec3 values and slices.
type Vec3Encoder struct {
	Bits int
}

// NewVec3Encoder constructs a Vec3Encoder from a desired maximum relative
// error on vector magnitudes. It uses BitsForMaxRelError under the hood.
func NewVec3Encoder(maxRelErr float64) (*Vec3Encoder, error) {
	bits, err := BitsForMaxRelError(maxRelErr)
	if err != nil {
		return nil, err
	}
	return &Vec3Encoder{Bits: bits}, nil
}

// Encode encodes a single Vec3 using the encoder's mantissa bits.
func (e *Vec3Encoder) Encode(v Vec3) ([]byte, error) {
	return EncodeVec3(v, e.Bits)
}

// EncodeSlice encodes a slice of Vec3 values using the encoder's mantissa
// bits.
func (e *Vec3Encoder) EncodeSlice(vs []Vec3) ([]byte, error) {
	return EncodeVec3Slice(vs, e.Bits)
}

// FloatStreamEncoder writes chunks of float64 slices to an io.Writer. Each chunk
// is encoded as:
//
//	[1-byte mantissa bits][uvarint byteLen][EncodeFloats payload...]
//
// where EncodeFloats payload is the usual length-prefixed varfloat encoding for
// the provided slice.
type FloatStreamEncoder struct {
	w io.Writer
}

// NewFloatStreamEncoder creates a FloatStreamEncoder that writes to w.
func NewFloatStreamEncoder(w io.Writer) *FloatStreamEncoder {
	return &FloatStreamEncoder{w: w}
}

// WriteChunk encodes a slice of float64 values with the given mantissa bits and
// writes it as a self-contained chunk to the underlying writer.
func (e *FloatStreamEncoder) WriteChunk(values []float64, bits int) error {
	if bits < 0 || bits > 52 {
		return errors.New("varfloat: mantissa bits must be between 0 and 52")
	}

	payload, err := EncodeFloats(values, bits)
	if err != nil {
		return err
	}

	var lenBuf [10]byte
	byteLen := uint64(len(payload))
	nLen := binary.PutUvarint(lenBuf[:], byteLen)

	header := []byte{byte(bits)}
	header = append(header, lenBuf[:nLen]...)

	if _, err := e.w.Write(header); err != nil {
		return err
	}
	if _, err := e.w.Write(payload); err != nil {
		return err
	}
	return nil
}

// FloatStreamDecoder reads chunks of float64 slices from an io.Reader that were
// written by FloatStreamEncoder.
type FloatStreamDecoder struct {
	r *bufio.Reader
}

// NewFloatStreamDecoder creates a FloatStreamDecoder that reads from r.
func NewFloatStreamDecoder(r io.Reader) *FloatStreamDecoder {
	return &FloatStreamDecoder{r: bufio.NewReader(r)}
}

// ReadChunk reads and decodes the next chunk from the stream, returning the
// decoded slice, the mantissa bits that were used to encode it, and an error.
// On EOF without any bytes read, it returns (nil, 0, io.EOF).
func (d *FloatStreamDecoder) ReadChunk() ([]float64, int, error) {
	bitsByte, err := d.r.ReadByte()
	if err != nil {
		return nil, 0, err
	}
	bits := int(bitsByte)
	if bits < 0 || bits > 52 {
		return nil, 0, errors.New("varfloat: invalid mantissa bits in stream header")
	}

	byteLen, err := binary.ReadUvarint(d.r)
	if err != nil {
		return nil, 0, err
	}

	if byteLen == 0 {
		return nil, bits, nil
	}

	buf := make([]byte, byteLen)
	if _, err := io.ReadFull(d.r, buf); err != nil {
		return nil, 0, err
	}

	values, _, err := DecodeFloats(buf, bits)
	if err != nil {
		return nil, 0, err
	}
	return values, bits, nil
}

// Vec3StreamEncoder writes chunks of Vec3 slices to an io.Writer using the same
// chunk format as FloatStreamEncoder but with EncodeVec3Slice payloads.
type Vec3StreamEncoder struct {
	w io.Writer
}

// NewVec3StreamEncoder creates a Vec3StreamEncoder that writes to w.
func NewVec3StreamEncoder(w io.Writer) *Vec3StreamEncoder {
	return &Vec3StreamEncoder{w: w}
}

// WriteChunk encodes a slice of Vec3 values with the given mantissa bits and
// writes it as a self-contained chunk to the underlying writer.
func (e *Vec3StreamEncoder) WriteChunk(vs []Vec3, bits int) error {
	if bits < 0 || bits > 52 {
		return errors.New("varfloat: mantissa bits must be between 0 and 52")
	}

	payload, err := EncodeVec3Slice(vs, bits)
	if err != nil {
		return err
	}

	var lenBuf [10]byte
	byteLen := uint64(len(payload))
	nLen := binary.PutUvarint(lenBuf[:], byteLen)

	header := []byte{byte(bits)}
	header = append(header, lenBuf[:nLen]...)

	if _, err := e.w.Write(header); err != nil {
		return err
	}
	if _, err := e.w.Write(payload); err != nil {
		return err
	}
	return nil
}

// Vec3StreamDecoder reads chunks of Vec3 slices from an io.Reader that were
// written by Vec3StreamEncoder.
type Vec3StreamDecoder struct {
	r *bufio.Reader
}

// NewVec3StreamDecoder creates a Vec3StreamDecoder that reads from r.
func NewVec3StreamDecoder(r io.Reader) *Vec3StreamDecoder {
	return &Vec3StreamDecoder{r: bufio.NewReader(r)}
}

// ReadChunk reads and decodes the next Vec3 slice chunk from the stream. It
// returns the decoded vectors, the mantissa bits that were used to encode them,
// and an error. On EOF without any bytes read, it returns (nil, 0, io.EOF).
func (d *Vec3StreamDecoder) ReadChunk() ([]Vec3, int, error) {
	bitsByte, err := d.r.ReadByte()
	if err != nil {
		return nil, 0, err
	}
	bits := int(bitsByte)
	if bits < 0 || bits > 52 {
		return nil, 0, errors.New("varfloat: invalid mantissa bits in stream header")
	}

	byteLen, err := binary.ReadUvarint(d.r)
	if err != nil {
		return nil, 0, err
	}

	if byteLen == 0 {
		return nil, bits, nil
	}

	buf := make([]byte, byteLen)
	if _, err := io.ReadFull(d.r, buf); err != nil {
		return nil, 0, err
	}

	vs, _, err := DecodeVec3Slice(buf, bits)
	if err != nil {
		return nil, 0, err
	}
	return vs, bits, nil
}

// Append encodes v as a varfloat using DefaultConfig and appends it to dst.
// It returns the extended slice.
func Append(dst []byte, v float64) []byte {
	return DefaultConfig.Append(dst, v)
}

// Append encodes v as a varfloat with the receiver configuration and appends it to dst.
// It returns the extended slice.
func (c Config) Append(dst []byte, v float64) []byte {
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

	// Quantize mantissa in [1, 2) to c.MantissaBits.
	var mant uint64
	mantMax := mantMaxForBits(c.MantissaBits)
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

// Vec3 represents a simple 3D vector stored as three float64 components.
type Vec3 struct {
	X, Y, Z float64
}

// Vec3Length returns the Euclidean length of v.
func Vec3Length(v Vec3) float64 {
	return math.Sqrt(v.X*v.X + v.Y*v.Y + v.Z*v.Z)
}

// Vec3Normalize returns a normalized copy of v. If v has zero length, it
// returns the zero vector.
func Vec3Normalize(v Vec3) Vec3 {
	n := Vec3Length(v)
	if n == 0 {
		return Vec3{}
	}
	return Vec3{
		X: v.X / n,
		Y: v.Y / n,
		Z: v.Z / n,
	}
}

// EncodeVec3 encodes a single 3D vector using the given mantissa bit
// precision. It is a small convenience wrapper around EncodeFloats.
func EncodeVec3(v Vec3, bits int) ([]byte, error) {
	return EncodeFloats([]float64{v.X, v.Y, v.Z}, bits)
}

// DecodeVec3 decodes a single 3D vector that was encoded with EncodeVec3
// and the same mantissa bit precision.
func DecodeVec3(b []byte, bits int) (Vec3, int, error) {
	values, n, err := DecodeFloats(b, bits)
	if err != nil {
		return Vec3{}, 0, err
	}
	if len(values) != 3 {
		return Vec3{}, 0, errors.New("varfloat: expected 3 components for Vec3")
	}
	return Vec3{X: values[0], Y: values[1], Z: values[2]}, n, nil
}

// EncodeVec3Slice encodes a slice of 3D vectors with a length prefix,
// similar to EncodeFloats but grouping values into triples.
func EncodeVec3Slice(vs []Vec3, bits int) ([]byte, error) {
	if len(vs) == 0 {
		return EncodeFloats(nil, bits)
	}
	flat := make([]float64, 0, len(vs)*3)
	for _, v := range vs {
		flat = append(flat, v.X, v.Y, v.Z)
	}
	return EncodeFloats(flat, bits)
}

// DecodeVec3Slice decodes a slice of 3D vectors that was encoded with
// EncodeVec3Slice and the same mantissa bit precision.
func DecodeVec3Slice(b []byte, bits int) ([]Vec3, int, error) {
	flat, n, err := DecodeFloats(b, bits)
	if err != nil {
		return nil, 0, err
	}
	if len(flat)%3 != 0 {
		return nil, 0, errors.New("varfloat: Vec3 slice encoding length is not a multiple of 3")
	}
	count := len(flat) / 3
	out := make([]Vec3, 0, count)
	for i := 0; i < count; i++ {
		base := i * 3
		out = append(out, Vec3{
			X: flat[base],
			Y: flat[base+1],
			Z: flat[base+2],
		})
	}
	return out, n, nil
}

// EncodeFloatsWithMantissa encodes a slice of float64 values with a 1-byte
// mantissa-bit header followed by the normal EncodeFloats payload. This makes
// it possible for a decoder to recover the mantissa bits from the stream.
func EncodeFloatsWithMantissa(values []float64, bits int) ([]byte, error) {
	if bits < 0 || bits > 52 {
		return nil, errors.New("varfloat: mantissa bits must be between 0 and 52")
	}
	payload, err := EncodeFloats(values, bits)
	if err != nil {
		return nil, err
	}
	// Prepend a single header byte with the mantissa bit count.
	out := make([]byte, 0, 1+len(payload))
	out = append(out, byte(bits))
	out = append(out, payload...)
	return out, nil
}

// DecodeFloatsWithMantissa decodes a slice of float64 values that was encoded
// with EncodeFloatsWithMantissa. It returns the decoded values, the mantissa
// bits recovered from the header, and the number of bytes consumed.
func DecodeFloatsWithMantissa(b []byte) ([]float64, int, int, error) {
	if len(b) == 0 {
		return nil, 0, 0, errors.New("varfloat: empty buffer for DecodeFloatsWithMantissa")
	}
	bits := int(b[0])
	if bits < 0 || bits > 52 {
		return nil, 0, 0, errors.New("varfloat: invalid mantissa bits in header")
	}
	values, n, err := DecodeFloats(b[1:], bits)
	if err != nil {
		return nil, 0, 0, err
	}
	return values, bits, n + 1, nil
}

// EncodeVec3SliceWithMantissa encodes a slice of Vec3 values with a 1-byte
// mantissa-bit header followed by the normal EncodeVec3Slice payload.
func EncodeVec3SliceWithMantissa(vs []Vec3, bits int) ([]byte, error) {
	if bits < 0 || bits > 52 {
		return nil, errors.New("varfloat: mantissa bits must be between 0 and 52")
	}
	payload, err := EncodeVec3Slice(vs, bits)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, 1+len(payload))
	out = append(out, byte(bits))
	out = append(out, payload...)
	return out, nil
}

// DecodeVec3SliceWithMantissa decodes a slice of Vec3 values that was encoded
// with EncodeVec3SliceWithMantissa. It returns the decoded vectors, the
// mantissa bits recovered from the header, and the number of bytes consumed.
func DecodeVec3SliceWithMantissa(b []byte) ([]Vec3, int, int, error) {
	if len(b) == 0 {
		return nil, 0, 0, errors.New("varfloat: empty buffer for DecodeVec3SliceWithMantissa")
	}
	bits := int(b[0])
	if bits < 0 || bits > 52 {
		return nil, 0, 0, errors.New("varfloat: invalid mantissa bits in header")
	}
	vs, n, err := DecodeVec3Slice(b[1:], bits)
	if err != nil {
		return nil, 0, 0, err
	}
	return vs, bits, n + 1, nil
}

// EncodeIntsBoundedSlice encodes a slice of integers known to lie in [min,max]
// with a 1-byte mantissa-bit header, followed by a length prefix and the
// bounded-int payload. This is similar in spirit to EncodeFloatsWithMantissa
// but for bounded integers.
func EncodeIntsBoundedSlice(values []int64, min, max int64, bits int) ([]byte, error) {
	if bits < 0 || bits > 52 {
		return nil, errors.New("varfloat: mantissa bits must be between 0 and 52")
	}

	// Start with header byte for mantissa bits.
	out := make([]byte, 0, 1+10+len(values))
	out = append(out, byte(bits))

	// Length prefix for the slice.
	var buf [10]byte
	n := binary.PutUvarint(buf[:], uint64(len(values)))
	out = append(out, buf[:n]...)

	// Encode each value as a bounded int using the provided bits.
	for _, v := range values {
		var err error
		out, err = AppendIntBounded(out, v, min, max, bits)
		if err != nil {
			return nil, err
		}
	}

	return out, nil
}

// DecodeIntsBoundedSlice decodes a slice of integers that was encoded with
// EncodeIntsBoundedSlice. It returns the decoded values, the mantissa bits
// recovered from the header, and the number of bytes consumed.
func DecodeIntsBoundedSlice(b []byte, min, max int64) ([]int64, int, int, error) {
	if len(b) == 0 {
		return nil, 0, 0, errors.New("varfloat: empty buffer for DecodeIntsBoundedSlice")
	}
	bits := int(b[0])
	if bits < 0 || bits > 52 {
		return nil, 0, 0, errors.New("varfloat: invalid mantissa bits in header")
	}

	// Read length prefix.
	length, nLen := binary.Uvarint(b[1:])
	if nLen <= 0 {
		return nil, 0, 0, errors.New("varfloat: failed to decode length for ints slice")
	}

	values := make([]int64, 0, length)
	offset := 1 + nLen
	for i := uint64(0); i < length; i++ {
		v, consumed, err := ConsumeIntBounded(b[offset:], min, max, bits)
		if err != nil {
			return nil, 0, 0, err
		}
		values = append(values, v)
		offset += consumed
	}

	return values, bits, offset, nil
}

// Consume decodes a varfloat from the beginning of b using DefaultConfig.
// It returns the decoded value, the number of bytes consumed, and an error.
func Consume(b []byte) (float64, int, error) {
	return DefaultConfig.Consume(b)
}

// Consume decodes a varfloat from the beginning of b using the receiver configuration.
// It returns the decoded value, the number of bytes consumed, and an error.
func (c Config) Consume(b []byte) (float64, int, error) {
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
		return 0, 0, errors.New("varfloat: invalid header")
	}

	sign := int(header & 1)
	ezPlus1 := header >> 1
	if ezPlus1 == 0 {
		return 0, 0, errors.New("varfloat: invalid header")
	}
	ez := ezPlus1 - 1

	e := zigZagDecode(ez) // exponent e'

	// Decode mantissa.
	mant, mlen := binary.Uvarint(b[n:])
	if mlen <= 0 {
		return 0, 0, errors.New("varfloat: invalid mantissa")
	}

	// Reconstruct mantissa m' in [1, 2).
	mPrime := 1.0
	mantMax := mantMaxForBits(c.MantissaBits)
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

// mantMaxForBits returns (1<<bits)-1, or 0 if bits <= 0.
func mantMaxForBits(bits int) int {
	if bits <= 0 {
		return 0
	}
	if bits >= 63 {
		// Avoid undefined behavior from shifting into sign bit of int.
		bits = 63
	}
	return (1 << bits) - 1
}

// EncodeFloat encodes v into a fresh buffer using the given mantissa precision (bits).
// It is a convenience wrapper around SetMantissaBits + Append.
func EncodeFloat(v float64, bits int) ([]byte, error) {
	cfg, err := NewConfig(bits)
	if err != nil {
		return nil, err
	}
	var buf []byte
	buf = cfg.Append(buf, v)
	return buf, nil
}

// DecodeFloat decodes a varfloat-encoded value from b using the given mantissa precision (bits).
// The same bits must have been used for encoding.
func DecodeFloat(b []byte, bits int) (float64, int, error) {
	cfg, err := NewConfig(bits)
	if err != nil {
		return 0, 0, err
	}
	return cfg.Consume(b)
}

// EncodeFloatSlice encodes a slice of float64 values with the given mantissa
// precision (bits) into a single buffer. It prefixes the data with the length
// of the slice encoded as a uvarint.
//
// Prefer EncodeFloats for a slightly nicer name; this function is kept for
// explicitness and symmetry with DecodeFloatSlice.
func EncodeFloatSlice(values []float64, bits int) ([]byte, error) {
	cfg, err := NewConfig(bits)
	if err != nil {
		return nil, err
	}

	var buf []byte
	// Prefix length.
	var lenBuf [10]byte
	n := binary.PutUvarint(lenBuf[:], uint64(len(values)))
	buf = append(buf, lenBuf[:n]...)

	for _, v := range values {
		buf = cfg.Append(buf, v)
	}
	return buf, nil
}

// DecodeFloatSlice decodes a slice of float64 values encoded by EncodeFloatSlice
// using the given mantissa precision (bits).
//
// Prefer DecodeFloats for a slightly nicer name; this function is kept for
// explicitness and symmetry with EncodeFloatSlice.
func DecodeFloatSlice(b []byte, bits int) ([]float64, int, error) {
	cfg, err := NewConfig(bits)
	if err != nil {
		return nil, 0, err
	}

	// Read length.
	length, n := binary.Uvarint(b)
	if n <= 0 {
		return nil, 0, errors.New("varfloat: invalid slice length")
	}
	b = b[n:]
	consumed := n

	values := make([]float64, 0, length)
	for i := uint64(0); i < length; i++ {
		v, used, err := cfg.Consume(b)
		if err != nil {
			return nil, 0, err
		}
		values = append(values, v)
		b = b[used:]
		consumed += used
	}

	return values, consumed, nil
}

// EncodeFloats is the preferred slice helper for most callers.
// It is a convenience alias for EncodeFloatSlice.
func EncodeFloats(values []float64, bits int) ([]byte, error) {
	return EncodeFloatSlice(values, bits)
}

// DecodeFloats is the preferred slice helper for most callers.
// It is a convenience alias for DecodeFloatSlice.
func DecodeFloats(b []byte, bits int) ([]float64, int, error) {
	return DecodeFloatSlice(b, bits)
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

	cfg, err := NewConfig(bits)
	if err != nil {
		return nil, err
	}

	// Map integer to float64 in the same numeric space.
	v := float64(n)
	return cfg.Append(dst, v), nil
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

	cfg, err := NewConfig(bits)
	if err != nil {
		return 0, 0, err
	}

	v, n, err := cfg.Consume(b)
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
//
//	bits ≈ ceil(log2(max-min+1)), clamped to [0, 52].
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

// EncodeIntLossy encodes an integer n in [min, max] allowing a bounded
// absolute error maxAbsErr when it is decoded via DecodeIntLossy.
//
// It chooses a mantissa precision using BitsForIntMaxError and then delegates
// to AppendIntBounded. Use this when you explicitly want lossy integer storage
// with a controlled maximum absolute error.
func EncodeIntLossy(dst []byte, n, min, max, maxAbsErr int64) ([]byte, error) {
	if min > max {
		return nil, errors.New("varfloat: min must be <= max")
	}
	if n < min || n > max {
		return nil, errors.New("varfloat: value out of bounds")
	}
	bits, err := BitsForIntMaxError(min, max, maxAbsErr)
	if err != nil {
		return nil, err
	}
	return AppendIntBounded(dst, n, min, max, bits)
}

// DecodeIntLossy decodes an integer that was encoded with EncodeIntLossy,
// using the same bounds and maxAbsErr. It recomputes the mantissa precision
// with BitsForIntMaxError and delegates to ConsumeIntBounded.
func DecodeIntLossy(b []byte, min, max, maxAbsErr int64) (int64, int, error) {
	if min > max {
		return 0, 0, errors.New("varfloat: min must be <= max")
	}
	bits, err := BitsForIntMaxError(min, max, maxAbsErr)
	if err != nil {
		return 0, 0, err
	}
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
	for (uint64(1)<<bits) < steps && bits < 52 {
		bits++
	}
	return bits
}

// BitsForIntRange returns a mantissa bit count that can distinguish all
// integer values in [min, max] when using the bounded-int helpers.
//
// It uses the same heuristic as AppendIntAuto:
//
//	bits ≈ ceil(log2(max-min+1)), clamped to [0, 52].
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
//
//   - The quantization step size is on the order of rangeWidth / 2^bits.
//
//   - To keep the absolute error <= maxAbsErr, we choose:
//
//     bits ≈ ceil(log2(rangeWidth / maxAbsErr)), clamped to [0, 52].
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
