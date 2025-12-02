package main

import (
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
	demoVectorPrecision()
	fmt.Println()
	demoTelemetry()
	fmt.Println()
	demoTimeSeriesDeltas()
}

// demoSparseFloatCoords simulates sparse 2D position vectors in a large game
// world and shows how integer block quantization plus varfloats compare to
// full float64 storage when most activity is near the origin.
func demoSparseFloatCoords() {
	fmt.Println("1) Large float64 game world: position vectors with block-level precision")
	fmt.Println("-------------------------------------------------------------------------")

	rng := rand.New(rand.NewSource(1))

	// Think of a large, mostly empty game world where coordinates are
	// float64, but most users only explore a smaller region around the origin
	// (e.g. within a 4k x 4k area: +/-2k units), and you only need coarse blocks there.
	type pt struct{ X, Y float64 } // world coordinates (e.g. meters or tiles)

	const blockSize = 32 // we only care about ~32-unit blocks

	positions := make([]pt, 0, 10000)
	for i := 0; i < cap(positions); i++ {
		// All positions lie in a smaller "gameplay" region around the origin
		// (e.g. +/- 2k units, i.e. ~4k x 4k total).
		positions = append(positions, pt{
			X: (rng.Float64()*4000 - 2000),
			Y: (rng.Float64()*4000 - 2000),
		})
	}

	// Baseline: fixed-size encoding (2 * float64).
	fixedBytes := len(positions) * 2 * 8

	// Quantize to block centers using pure integer math, store the quantized
	// coordinates as ints in a bounded region around the origin (e.g. +/- 64k),
	// and then encode them as bounded ints with varfloats.
	const bits = 12
	const minInt, maxInt = int64(-64_000), int64(64_000)

	var vfBuf []byte

	for _, p := range positions {
		// Integer block quantization.
		qx := int64(math.Round(p.X/float64(blockSize))) * blockSize
		qy := int64(math.Round(p.Y/float64(blockSize))) * blockSize

		if qx < minInt {
			qx = minInt
		} else if qx > maxInt {
			qx = maxInt
		}
		if qy < minInt {
			qy = minInt
		} else if qy > maxInt {
			qy = maxInt
		}

		var err error
		vfBuf, err = varfloat.AppendIntBounded(vfBuf, qx, minInt, maxInt, bits)
		if err != nil {
			panic(err)
		}
		vfBuf, err = varfloat.AppendIntBounded(vfBuf, qy, minInt, maxInt, bits)
		if err != nil {
			panic(err)
		}
	}

	fmt.Println("Scenario: 10,000 position vectors in a large float64 world.")
	fmt.Println("Premise: most users explore within a +/-2k x +/-2k gameplay region around the origin (~4k x 4k total).")
	fmt.Printf("Fixed-size encoding (2 * float64): %d bytes\n", fixedBytes)
	fmt.Printf("Varfloat encoding of block-quantized ints with %d bits: %d bytes\n", bits, len(vfBuf))
	fmt.Printf("Compression vs float64: ≈ %.2fx smaller (with ≤ ~%.1f units quantization error near the origin)\n",
		float64(fixedBytes)/float64(len(vfBuf)),
		float64(blockSize)/2)

	// Show a few sample quantizations for intuition (integer block quantization).
	fmt.Println()
	fmt.Printf("Example block quantization of a few position vectors (block size ≈ %d units):\n", blockSize)
	fmt.Println("  (orig X,Y) -> (block-quantized X,Y) [|err| in world units]")

	samplesShown := 0
	for _, p := range positions {
		qx := float64(int64(math.Round(p.X/float64(blockSize))) * blockSize)
		qy := float64(int64(math.Round(p.Y/float64(blockSize))) * blockSize)
		dx := math.Abs(qx - p.X)
		dy := math.Abs(qy - p.Y)
		fmt.Printf("  (%.1f, %.1f) -> (%.1f, %.1f) [|err| ≈ (%.2f, %.2f) units]\n",
			p.X, p.Y, qx, qy, dx, dy)
		samplesShown++
		if samplesShown >= 5 {
			break
		}
	}
}

// (percentage, delta, and lossy-int demos removed to keep the focus on cases
// where varfloats are a clear win over both fixed-size and plain varints.)

// demoVectorPrecision shows approximate storage of random 3D vectors
// where a small relative error in length/direction is acceptable.
func demoVectorPrecision() {
	fmt.Println("2) 3D vectors with limited precision")
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

// demoTelemetry shows bounded sensor/telemetry style floats where a small
// absolute error is acceptable and varfloats give a clear size win.
func demoTelemetry() {
	fmt.Println("3) Telemetry-style floats with bounded error")
	fmt.Println("-------------------------------------------")

	rng := rand.New(rand.NewSource(6))

	values := make([]float64, 0, 5000)
	for i := 0; i < cap(values); i++ {
		// Example: temperatures or loads in [0, 500] with some noise.
		base := rng.Float64() * 500
		noise := (rng.Float64() - 0.5) * 0.2 // +/-0.1 units
		values = append(values, base+noise)
	}

	fixedBytes := len(values) * 8 // float64

	// Target max absolute error of about 0.1 units in this range.
	// Use a relative error that is small enough over [0,500].
	bits, err := varfloat.BitsForMaxRelError(0.0005) // 0.05%
	if err != nil {
		panic(err)
	}

	var vfBuf []byte
	for _, v := range values {
		tmp, err := varfloat.EncodeFloat(v, bits)
		if err != nil {
			panic(err)
		}
		vfBuf = append(vfBuf, tmp...)
	}

	fmt.Println("Scenario: 5,000 telemetry-style readings in [0,500] with small tolerated error.")
	fmt.Printf("Fixed-size encoding (float64):           %6d bytes\n", fixedBytes)
	fmt.Printf("Varfloat encoding with %d mantissa bits: %6d bytes\n", bits, len(vfBuf))
	fmt.Printf("Compression vs float64: ≈ %.2fx smaller\n",
		float64(fixedBytes)/float64(len(vfBuf)))
}

// demoTimeSeriesDeltas shows a time series where step-to-step changes are
// small and varfloats encode deltas with controlled loss.
func demoTimeSeriesDeltas() {
	fmt.Println("4) Time-series deltas with controlled loss")
	fmt.Println("-----------------------------------------")

	rng := rand.New(rand.NewSource(7))

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
		prev = s
	}

	fmt.Println("Scenario: 10,000 int64 samples with small step-to-step changes, storing deltas.")
	fmt.Printf("Fixed-size encoding (int64):                          %6d bytes\n", fixedBytes)
	fmt.Printf("Varfloat (first fixed, deltas in [%d,%d] with %d bits): %6d bytes\n", deltaMin, deltaMax, bits, len(vfBuf))
	fmt.Printf("Compression vs int64: ≈ %.2fx smaller\n",
		float64(fixedBytes)/float64(len(vfBuf)))
}
