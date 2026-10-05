// Package llm is the single chokepoint for remote model calls. In chunk 3 it
// carries the embeddings used by search; Jev, chat and vision join the same gate
// in chunk 5 (ARCHITECTURE.md 7).
//
// Provider clients live here as unexported types. Nothing outside this package
// can reach a paid endpoint: the gate is the only exported caller, and a test
// asserts no other package names one of the provider paths.
package llm

import (
	"encoding/binary"
	"math"
)

// Vector is one embedding stored as int8, with the scale that converts a step
// back to the provider's units and the L2 norm of the original float vector.
// The default provider returns int8 natively; a provider that returns float32
// is quantised with one scale for the whole batch (ARCHITECTURE.md 3).
type Vector struct {
	Values []int8
	Scale  float64
	Norm   float64
	Dims   int
}

// int128Step is the step of the default provider, whose components are all
// multiples of 1/128.
const int128Step = 1.0 / 128.0

// Quantise turns a float32 embedding into a stored Vector. When every component
// is a multiple of 1/128 (the default provider) it keeps the exact native int8
// representation; otherwise it scales the largest magnitude to 127.
func Quantise(vals []float32) Vector {
	if len(vals) == 0 {
		return Vector{}
	}
	scale := int128Step
	native := true
	for _, v := range vals {
		q := math.Round(float64(v) / int128Step)
		if math.Abs(float64(v)-q*int128Step) > 1e-4 {
			native = false
			break
		}
	}
	maxAbs := 0.0
	for _, v := range vals {
		if a := math.Abs(float64(v)); a > maxAbs {
			maxAbs = a
		}
	}
	if !native {
		scale = maxAbs / 127
		if scale == 0 {
			scale = 1
		}
	}
	values := make([]int8, len(vals))
	var sumSq float64
	for i, v := range vals {
		f := float64(v)
		values[i] = clampInt8(math.Round(f / scale))
		sumSq += f * f
	}
	return Vector{Values: values, Scale: scale, Norm: math.Sqrt(sumSq), Dims: len(vals)}
}

func clampInt8(f float64) int8 {
	switch {
	case f > 127:
		return 127
	case f < -127:
		return -127
	default:
		return int8(f)
	}
}

// Cosine is the cosine similarity of two vectors from the same model. It is the
// integer dot product scaled back to the provider's units, divided by the two
// true norms. A zero norm (an empty or all-zero vector) has no direction and
// scores 0.
func (v Vector) Cosine(o Vector) float64 {
	if v.Norm == 0 || o.Norm == 0 || len(v.Values) == 0 || len(v.Values) != len(o.Values) {
		return 0
	}
	var dot float64
	for i, x := range v.Values {
		dot += float64(x) * float64(o.Values[i])
	}
	return dot * v.Scale * o.Scale / (v.Norm * o.Norm)
}

// Encode packs the int8 values for the vector BLOB column: length-prefixed so a
// truncated or corrupt blob is detected rather than silently misread.
func (v Vector) Encode() []byte {
	buf := make([]byte, 4+len(v.Values))
	binary.BigEndian.PutUint32(buf[:4], uint32(v.Dims)) //nolint:gosec // G115: a vector length is far below 2^31
	for i, x := range v.Values {
		buf[4+i] = byte(x) //nolint:gosec // G115: the two's-complement byte of an int8 is the intended encoding
	}
	return buf
}

// DecodeVector reads a vector BLOB. A blob whose length prefix disagrees with
// its payload or the expected dims is rejected, so a corrupt row cannot score
// against a query.
func DecodeVector(b []byte, scale, norm float64, dims int) (Vector, bool) {
	if len(b) < 4 {
		return Vector{}, false
	}
	n := int(binary.BigEndian.Uint32(b[:4]))
	if n != dims || len(b) != 4+n {
		return Vector{}, false
	}
	values := make([]int8, n)
	for i := range values {
		values[i] = int8(b[4+i]) //nolint:gosec // G115: the inverse of Encode, a byte reinterpreted as the int8 it was
	}
	return Vector{Values: values, Scale: scale, Norm: norm, Dims: n}, true
}
