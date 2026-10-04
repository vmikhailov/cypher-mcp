package vector

import (
	"math"
	"testing"
)

func TestVector_MathAndEncoding(t *testing.T) {
	v1 := []float32{1.0, 0.0, 0.0}
	v2 := []float32{0.0, 1.0, 0.0}

	sim, err := DotProduct(v1, v2)
	if err != nil {
		t.Fatalf("DotProduct: %v", err)
	}
	if math.Abs(sim) > 1e-6 {
		t.Fatalf("expected orthogonal similarity ~0, got %f", sim)
	}

	blob, err := EncodeEmbedding([]float32{3.0, 4.0})
	if err != nil {
		t.Fatalf("EncodeEmbedding: %v", err)
	}
	decoded, err := DecodeEmbedding(blob)
	if err != nil {
		t.Fatalf("DecodeEmbedding: %v", err)
	}
	if len(decoded) != 2 {
		t.Fatalf("expected 2 elements, got %d", len(decoded))
	}
	// Normalized [3/5, 4/5] = [0.6, 0.8]
	if math.Abs(float64(decoded[0])-0.6) > 1e-5 || math.Abs(float64(decoded[1])-0.8) > 1e-5 {
		t.Fatalf("expected [0.6, 0.8], got %v", decoded)
	}
}

func TestVector_DimensionMismatch(t *testing.T) {
	_, err := DotProduct([]float32{1.0}, []float32{1.0, 2.0})
	if err == nil {
		t.Fatalf("expected error on dimension mismatch, got nil")
	}
}
