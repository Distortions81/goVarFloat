package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"

	"github.com/Distortions81/goVarFloat/varfloat"
)

type vec3 struct{ X, Y, Z int32 } // millimeters

func main() {
	fmt.Println("GoVarFloat demo")
	fmt.Println("================")
	fmt.Println()

	demoSparseFloatCoords()
	fmt.Println()
	demoPercentages()
	fmt.Println()
	demoTimeSeriesDeltas()
	fmt.Println()
	demoLossyIntsInteger()
	fmt.Println()
	demoVectorPrecision()
}

// demoSparseFloatCoords simulates sparse 2D pixel positions and shows how
// integer block quantization plus varfloats compare to full float64 storage.
func demoSparseFloatCoords() {
	fmt.Println("1) Sparse pixel coordinates with block-level precision")
	fmt.Println("------------------------------------------------------")

	rng := rand.New(rand.NewSource(1))

	// Think of a large, mostly empty image where we only need
	// ~8px block precision for where something is.
	type pt struct{ X, Y float64 } // pixel coordinates

	const (
		width     = 3840 // 4K-ish width
		height    = 2160 // 4K-ish height
		blockSize = 8    // we only care about ~8px blocks
	)

	positions := make([]pt, 0, 10000)
	for i := 0; i < cap(positions); i++ {
		if rng.Float64() < 0.9 {
			positions = append(positions, pt{0, 0}) // background
		} else {
			positions = append(positions, pt{
				X: rng.Float64() * width,
				Y: rng.Float64() * height,
			})
		}
	}

	// Baseline: fixed-size encoding (2 * float64).
	fixedBytes := len(positions) * 2 * 8

	// New lossy-int style: quantize to block centers using pure integer math,
	// store the quantized coordinates as ints, and then encode them as bounded
	// ints with varfloats.
	const bits = 12
	const minIntX, maxIntX = int64(0), int64(width)
	const minIntY, maxIntY = int64(0), int64(height)

	var vfBuf []byte
	var varintBuf []byte
	var tmp [10]byte

	for _, p := range positions {
		// Integer block quantization.
		qx := int64(math.Round(p.X/float64(blockSize))) * blockSize
		qy := int64(math.Round(p.Y/float64(blockSize))) * blockSize

		if qx < minIntX {
			qx = minIntX
		} else if qx > maxIntX {
			qx = maxIntX
		}
		if qy < minIntY {
			qy = minIntY
		} else if qy > maxIntY {
			qy = maxIntY
		}

		var err error
		vfBuf, err = varfloat.AppendIntBounded(vfBuf, qx, minIntX, maxIntX, bits)
		if err != nil {
			panic(err)
		}
		vfBuf, err = varfloat.AppendIntBounded(vfBuf, qy, minIntY, maxIntY, bits)
		if err != nil {
			panic(err)
		}

		// Plain varint encoding of quantized ints for comparison.
		used := binary.PutVarint(tmp[:], qx)
		varintBuf = append(varintBuf, tmp[:used]...)
		used = binary.PutVarint(tmp[:], qy)
		varintBuf = append(varintBuf, tmp[:used]...)
	}

	fmt.Printf("Scenario: 10,000 sparse pixel positions in a %dx%d image, 90%% at (0,0).\n", width, height)
	fmt.Printf("Fixed-size encoding (2 * float64): %d bytes\n", fixedBytes)
	fmt.Printf("Varint encoding of block-quantized ints: %d bytes\n", len(varintBuf))
	fmt.Printf("Varfloat encoding of block-quantized ints with %d bits: %d bytes\n", bits, len(vfBuf))
	fmt.Printf("Compression vs float64: varint ≈ %.2fx, varfloat ≈ %.2fx (with ≤ ~%.1fpx quantization error)\n",
		float64(fixedBytes)/float64(len(varintBuf)),
		float64(fixedBytes)/float64(len(vfBuf)),
		float64(blockSize)/2)

	// Show a few sample quantizations for intuition (integer block quantization).
	fmt.Println()
	fmt.Printf("Example block quantization of a few non-zero positions (block size ≈ %dpx):\n", blockSize)
	fmt.Println("  (orig X,Y) -> (block-quantized X,Y) [|err| in pixels]")

	samplesShown := 0
	for _, p := range positions {
		if p.X == 0 && p.Y == 0 {
			continue
		}
		qx := float64(int64(math.Round(p.X/float64(blockSize))) * blockSize)
		qy := float64(int64(math.Round(p.Y/float64(blockSize))) * blockSize)
		dx := math.Abs(qx - p.X)
		dy := math.Abs(qy - p.Y)
		fmt.Printf("  (%.1f, %.1f) -> (%.1f, %.1f) [|err| ≈ (%.2fpx, %.2fpx)]\n",
			p.X, p.Y, qx, qy, dx, dy)
		samplesShown++
		if samplesShown >= 5 {
			break
		}
	}
}

// demoPercentages simulates percentages where many values are exactly zero and
// shows how bounded ints plus varfloat can shrink them.
func demoPercentages() {
	fmt.Println("2) Percentages / probabilities in [0,1]")
	fmt.Println("----------------------------------------")

	rng := rand.New(rand.NewSource(2))

	values := make([]float64, 0, 10000)
	for i := 0; i < cap(values); i++ {
		p := rng.Float64()
		if p < 0.7 {
			p = 0 // 70% zeros
		}
		values = append(values, p)
	}

	fixedBytes := len(values) * 8 // float64

	min, max := int64(0), int64(10_000)
	const bits = 10

	var vfBuf []byte
	var varintBuf []byte
	var tmp [10]byte

	for _, p := range values {
		if p < 0 {
			p = 0
		} else if p > 1 {
			p = 1
		}
		n := int64(math.Round(p * 10_000))

		// Varfloat as bounded ints.
		var err error
		vfBuf, err = varfloat.AppendIntBounded(vfBuf, n, min, max, bits)
		if err != nil {
			panic(err)
		}

		// Plain varint (for comparison).
		used := binary.PutVarint(tmp[:], n)
		varintBuf = append(varintBuf, tmp[:used]...)
	}

	fmt.Println("Scenario: 10,000 percentages, 70% exactly zero, 0.01% steps.")
	fmt.Printf("Fixed-size encoding (float64):  %6d bytes\n", fixedBytes)
	fmt.Printf("Varint encoding (int64 buckets): %6d bytes\n", len(varintBuf))
	fmt.Printf("Varfloat encoding in [%d,%d] with %d bits: %6d bytes\n", min, max, bits, len(vfBuf))
	fmt.Printf("Compression vs float64: varint ≈ %.2fx, varfloat ≈ %.2fx\n",
		float64(fixedBytes)/float64(len(varintBuf)),
		float64(fixedBytes)/float64(len(vfBuf)))
}

// demoTimeSeriesDeltas simulates an integer time series where step-to-step changes
// are small and encodes only the deltas with varfloat.
func demoTimeSeriesDeltas() {
	fmt.Println("3) Time series deltas")
	fmt.Println("---------------------")

	rng := rand.New(rand.NewSource(3))

	samples := make([]int64, 0, 10000)
	cur := int64(0)
	for i := 0; i < cap(samples); i++ {
		cur += int64(rng.Intn(11) - 5) // small steps
		samples = append(samples, cur)
	}

	fixedBytes := len(samples) * 8 // int64

	const (
		bits     = 8
		deltaMin = int64(-1000)
		deltaMax = int64(1000)
	)

	var vfBuf []byte
	var varintBuf []byte
	var tmp [10]byte
	// Store the first value as a fixed 8-byte int64.
	vfBuf = append(vfBuf, varfloat.EncodeInt64Fixed(samples[0])...)
	prev := samples[0]
	for _, s := range samples[1:] {
		delta := s - prev
		if delta < deltaMin {
			delta = deltaMin
		} else if delta > deltaMax {
			delta = deltaMax
		}
		var err error
		vfBuf, err = varfloat.AppendIntBounded(vfBuf, delta, deltaMin, deltaMax, bits)
		if err != nil {
			panic(err)
		}

		// Varint for comparison (store first sample + deltas as varints).
		if len(varintBuf) == 0 {
			used := binary.PutVarint(tmp[:], samples[0])
			varintBuf = append(varintBuf, tmp[:used]...)
		}
		used := binary.PutVarint(tmp[:], delta)
		varintBuf = append(varintBuf, tmp[:used]...)
		prev = s
	}

	fmt.Println("Scenario: 10,000 int64 samples, small step-to-step changes, encode deltas.")
	fmt.Printf("Fixed-size encoding (int64):           %6d bytes\n", fixedBytes)
	fmt.Printf("Varint encoding (first + deltas):      %6d bytes\n", len(varintBuf))
	fmt.Printf("Varfloat (first fixed, deltas in [%d,%d] with %d bits): %6d bytes\n", deltaMin, deltaMax, bits, len(vfBuf))
	fmt.Printf("Compression vs int64: varint ≈ %.2fx, varfloat ≈ %.2fx\n",
		float64(fixedBytes)/float64(len(varintBuf)),
		float64(fixedBytes)/float64(len(vfBuf)))
}

// demoLossyIntsInteger shows lossy integer storage using pure integer
// quantization (no int->float->int), then encodes the quantized ints
// with varfloats.
func demoLossyIntsInteger() {
	fmt.Println("4) Lossy integers via integer buckets (pure int math)")
	fmt.Println("-----------------------------------------------------")

	// Imagine request-per-minute counts where being off by up to 10 requests
	// is fine, so we quantize into 10-sized buckets.
	const (
		min    = int64(0)
		max    = int64(100_000)
		step   = int64(10) // bucket size
		bits   = 12        // mantissa bits for bounded int varfloats
	)

	fmt.Printf("Range: [%"+"d, %"+"d], bucket size = %"+"d\n", min, max, step)
	fmt.Println("We quantize counts into 10-sized buckets using integer division/multiplication,")
	fmt.Println("then encode the quantized counts as bounded ints with varfloats.")

	values := []int64{0, 3, 7, 9, 10, 17, 123, 999, 12_345, 87_654}

	var vfBuf []byte
	for _, v := range values {
		// Pure integer quantization to nearest lower multiple of step.
		q := (v / step) * step

		var err error
		vfBuf, err = varfloat.AppendIntBounded(vfBuf, q, min, max, bits)
		if err != nil {
			panic(err)
		}

		errVal := v - q
		if errVal < 0 {
			errVal = -errVal
		}

		fmt.Printf("  original=%6d -> quantized=%6d (|error|=%2d)\n", v, q, errVal)
	}

	fmt.Printf("Total encoded size for %d bucketed values: %d bytes\n", len(values), len(vfBuf))
}

// demoVectorPrecision shows approximate storage of random 3D vectors
// where a small relative error in length/direction is acceptable.
func demoVectorPrecision() {
	fmt.Println("5) 3D vectors with limited precision")
	fmt.Println("------------------------------------")

	rng := rand.New(rand.NewSource(5))

	vectors := make([][3]float64, 0, 5000)
	for i := 0; i < cap(vectors); i++ {
		vectors = append(vectors, [3]float64{
			rng.NormFloat64() * 1000,
			rng.NormFloat64() * 1000,
			rng.NormFloat64() * 1000,
		})
	}

	// Fixed-size: 3 * float64 per vector.
	fixedBytes := len(vectors) * 3 * 8

	// Varfloat: encode each component with a limited number of mantissa bits,
	// targeting roughly ~0.1% relative precision.
	bits, err := varfloat.BitsForMaxRelError(0.001)
	if err != nil {
		panic(err)
	}

	var vfBuf []byte
	for _, v := range vectors {
		tmp, err := varfloat.EncodeFloats(v[:], bits)
		if err != nil {
			panic(err)
		}
		vfBuf = append(vfBuf, tmp...)
	}

	fmt.Println("Scenario: 5,000 random 3D vectors with roughly unit-normal-like distribution scaled to ~1000.")
	fmt.Printf("Fixed-size encoding (3 * float64): %d bytes\n", fixedBytes)
	fmt.Printf("Varfloat encoding with %d mantissa bits: %d bytes\n", bits, len(vfBuf))
	fmt.Printf("Compression: %.2fx smaller\n", float64(fixedBytes)/float64(len(vfBuf)))

	// Show a few sample vectors with their quantized versions and relative length error.
	fmt.Println()
	fmt.Println("Example vector quantization (showing relative length error):")

	samplesShown := 0
	for _, v := range vectors {
		tmp, err := varfloat.EncodeFloats(v[:], bits)
		if err != nil {
			panic(err)
		}
		dec, _, err := varfloat.DecodeFloats(tmp, bits)
		if err != nil {
			panic(err)
		}
		if len(dec) != 3 {
			continue
		}
		lenOrig := math.Sqrt(v[0]*v[0] + v[1]*v[1] + v[2]*v[2])
		lenDec := math.Sqrt(dec[0]*dec[0] + dec[1]*dec[1] + dec[2]*dec[2])
		relErr := 0.0
		if lenOrig > 0 {
			relErr = math.Abs(lenDec-lenOrig) / lenOrig
		}
		fmt.Printf("  orig=(%.1f, %.1f, %.1f), len≈%.1f -> dec=(%.1f, %.1f, %.1f), len≈%.1f (rel len err≈%.4f)\n",
			v[0], v[1], v[2], lenOrig,
			dec[0], dec[1], dec[2], lenDec, relErr)
		samplesShown++
		if samplesShown >= 3 {
			break
		}
	}
}
