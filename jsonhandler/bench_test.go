package jsonhandler

import (
	"context"
	"testing"
)

func BenchmarkNew_small(b *testing.B) {
	schema := []byte(`{"type":"string"}`)
	for b.Loop() {
		if _, err := New(context.Background(), schema); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNew(b *testing.B) {
	schema := []byte(definitions)
	for b.Loop() {
		if _, err := New(context.Background(), schema); err != nil {
			b.Fatal(err)
		}
	}
}
