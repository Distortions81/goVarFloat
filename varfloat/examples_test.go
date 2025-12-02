package varfloat

import (
	"fmt"
	"math"
	"math/rand"
)

type vec3 struct{ X, Y, Z int32 } // millimeters

// Example_sparseCoords demonstrates approximate savings for sparse 3D coordinates.
func Example_sparseCoords() {
	rand.Seed(1)

	positions := make([]vec3, 0, 10000)
	for i := 0; i < cap(positions); i++ {
		if rand.Float64() < 0.9 {
			positions = append(positions, vec3{0, 0, 0})
		} else {
			positions = append(positions, vec3{
				X: int32(rand.Intn(2001) - 1000),
				Y: int32(rand.Intn(2001) - 1000),
				Z: int32(rand.Intn(2001) - 1000),
			})
		}
	}

	// Baseline: fixed-size encoding (3 * int32).
	fixedBytes := len(positions) * 3 * 4

	// Varfloat encoding with bounded ints and 10 mantissa bits.
	const bits = 10
	const min, max = int64(-1_000_000), int64(1_000_000)

	var vfBuf []byte
	for _, p := range positions {
		var err error
		vfBuf, err = AppendIntBounded(vfBuf, int64(p.X), min, max, bits)
		if err != nil {
			panic(err)
		}
		vfBuf, err = AppendIntBounded(vfBuf, int64(p.Y), min, max, bits)
		if err != nil {
			panic(err)
		}
		vfBuf, err = AppendIntBounded(vfBuf, int64(p.Z), min, max, bits)
		if err != nil {
			panic(err)
		}
	}

	fmt.Println("Sparse coords:")
	fmt.Printf("  fixed-size bytes: %d\n", fixedBytes)
	fmt.Printf("  varfloat bytes:   %d\n", len(vfBuf))
	fmt.Printf("  compression:      %.2fx smaller\n", float64(fixedBytes)/float64(len(vfBuf)))
}

// Example_percentages demonstrates approximate savings for bounded percentages.
func Example_percentages() {
	rand.Seed(2)

	values := make([]float64, 0, 10000)
	for i := 0; i < cap(values); i++ {
		p := rand.Float64()
		if p < 0.7 {
			p = 0 // 70% zeros
		}
		values = append(values, p)
	}

	fixedBytes := len(values) * 8

	min, max := int64(0), int64(10_000)
	const bits = 10

	var buf []byte
	for _, p := range values {
		if p < 0 {
			p = 0
		} else if p > 1 {
			p = 1
		}
		n := int64(math.Round(p * 10_000))
		var err error
		buf, err = AppendIntBounded(buf, n, min, max, bits)
		if err != nil {
			panic(err)
		}
	}

	fmt.Println("Percentages:")
	fmt.Printf("  fixed-size bytes: %d\n", fixedBytes)
	fmt.Printf("  varfloat bytes:   %d\n", len(buf))
	fmt.Printf("  compression:      %.2fx smaller\n", float64(fixedBytes)/float64(len(buf)))
}

// Example_deltas demonstrates approximate savings for time series deltas.
func Example_deltas() {
	rand.Seed(3)

	samples := make([]int64, 0, 10000)
	cur := int64(0)
	for i := 0; i < cap(samples); i++ {
		cur += int64(rand.Intn(11) - 5) // small steps
		samples = append(samples, cur)
	}

	fixedBytes := len(samples) * 8

	const (
		bits     = 8
		deltaMin = int64(-1000)
		deltaMax = int64(1000)
	)

	var buf []byte
	buf = append(buf, EncodeInt64Fixed(samples[0])...)
	prev := samples[0]
	for _, s := range samples[1:] {
		delta := s - prev
		if delta < deltaMin {
			delta = deltaMin
		} else if delta > deltaMax {
			delta = deltaMax
		}
		var err error
		buf, err = AppendIntBounded(buf, delta, deltaMin, deltaMax, bits)
		if err != nil {
			panic(err)
		}
		prev = s
	}

	fmt.Println("Time series deltas:")
	fmt.Printf("  fixed-size bytes: %d\n", fixedBytes)
	fmt.Printf("  varfloat bytes:   %d\n", len(buf))
	fmt.Printf("  compression:      %.2fx smaller\n", float64(fixedBytes)/float64(len(buf)))
}
