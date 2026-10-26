package yandex

import (
	"context"
	"errors"
	"testing"

	"github.com/IvanLobanok/evnavigation/internal/models/route"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr error
	}{
		{"no api key", Config{}, ErrAPIKeyRequired},
		{"api key given: the provider is still a placeholder", Config{APIKey: "key"}, ErrNotImplemented},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := New(tc.cfg)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v, want %v", err, tc.wantErr)
			}
			if p != nil {
				t.Error("no provider must be returned while it is not implemented")
			}
		})
	}
}

func TestBuildRoute_IsNotImplemented(t *testing.T) {
	segs, err := (&Provider{}).BuildRoute(context.Background(), route.RoutePoint{}, route.RoutePoint{})

	if !errors.Is(err, ErrNotImplemented) || segs != nil {
		t.Errorf("got %v, %v", segs, err)
	}
}
