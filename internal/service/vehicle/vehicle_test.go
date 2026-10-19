package vehicle

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/IvanLobanok/evnavigation/internal/models/evmodel"
	"github.com/IvanLobanok/evnavigation/internal/models/user"
)

var errBoom = errors.New("boom")

type fakeStore struct {
	createErr error
	getErr    error

	gotUserID, gotModelID, gotVehicleID int
	gotNickname                         *string
	createCalls                         int

	list []user.VehicleDetails
}

func (f *fakeStore) CreateVehicle(_ context.Context, userID, modelID int, nickname *string) (*user.VehicleDetails, error) {
	f.createCalls++
	f.gotUserID, f.gotModelID, f.gotNickname = userID, modelID, nickname
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &user.VehicleDetails{UserVehicle: user.UserVehicle{ID: 1, UserID: userID, EVModelID: modelID, Nickname: nickname}}, nil
}

func (f *fakeStore) GetVehicle(_ context.Context, userID, vehicleID int) (*user.VehicleDetails, error) {
	f.gotUserID, f.gotVehicleID = userID, vehicleID
	if f.getErr != nil {
		return nil, f.getErr
	}
	return &user.VehicleDetails{UserVehicle: user.UserVehicle{ID: vehicleID, UserID: userID}}, nil
}

func (f *fakeStore) ListVehicles(_ context.Context, userID int) ([]user.VehicleDetails, error) {
	f.gotUserID = userID
	return f.list, nil
}

type fakeCatalog struct {
	models []evmodel.EVModel
	err    error
}

func (f fakeCatalog) GetAllEVModels(context.Context) ([]evmodel.EVModel, error) {
	return f.models, f.err
}

func TestAddVehicle_Nickname(t *testing.T) {
	tests := []struct {
		name     string
		nickname string
		want     *string // nil => ожидаем NULL
		wantErr  error
	}{
		{"plain", "my car", ptr("my car"), nil},
		{"trimmed", "  my car  ", ptr("my car"), nil},
		{"empty becomes NULL", "", nil, nil},
		{"blank becomes NULL", "   ", nil, nil},
		{"exactly max length", strings.Repeat("a", MaxNicknameLen), ptr(strings.Repeat("a", MaxNicknameLen)), nil},
		{"one over max length", strings.Repeat("a", MaxNicknameLen+1), nil, ErrInvalidNickname},
		// лимит считается в символах, а не в байтах: 50 кириллических букв = 100 байт
		{"max length in multibyte runes", strings.Repeat("я", MaxNicknameLen), ptr(strings.Repeat("я", MaxNicknameLen)), nil},
		{"over max length in multibyte runes", strings.Repeat("я", MaxNicknameLen+1), nil, ErrInvalidNickname},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{}
			_, err := NewService(store, fakeCatalog{}).AddVehicle(context.Background(), 5, 3, tc.nickname)

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error: got %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				if store.createCalls != 0 {
					t.Error("store must not be called for an invalid nickname")
				}
				return
			}
			if (store.gotNickname == nil) != (tc.want == nil) || (tc.want != nil && *store.gotNickname != *tc.want) {
				t.Errorf("stored nickname: got %v, want %v", deref(store.gotNickname), deref(tc.want))
			}
			if store.gotUserID != 5 || store.gotModelID != 3 {
				t.Errorf("ids not passed through: user=%d model=%d", store.gotUserID, store.gotModelID)
			}
		})
	}
}

func TestAddVehicle_Errors(t *testing.T) {
	t.Run("unknown model is passed through unchanged", func(t *testing.T) {
		_, err := NewService(&fakeStore{createErr: evmodel.ErrNotFound}, fakeCatalog{}).
			AddVehicle(context.Background(), 1, 999, "")
		if !errors.Is(err, evmodel.ErrNotFound) {
			t.Fatalf("got %v, want evmodel.ErrNotFound", err)
		}
	})
	t.Run("other errors are wrapped", func(t *testing.T) {
		_, err := NewService(&fakeStore{createErr: errBoom}, fakeCatalog{}).
			AddVehicle(context.Background(), 1, 1, "")
		if !errors.Is(err, errBoom) {
			t.Fatalf("got %v, want wrapped errBoom", err)
		}
	})
}

func TestGetVehicle_PassesOwnerAndIDToStore(t *testing.T) {
	store := &fakeStore{}
	if _, err := NewService(store, fakeCatalog{}).GetVehicle(context.Background(), 5, 77); err != nil {
		t.Fatal(err)
	}
	if store.gotUserID != 5 || store.gotVehicleID != 77 {
		t.Errorf("got user=%d vehicle=%d, want 5 and 77", store.gotUserID, store.gotVehicleID)
	}
}

func TestGetVehicle_NotFoundIsPassedThrough(t *testing.T) {
	_, err := NewService(&fakeStore{getErr: user.ErrVehicleNotFound}, fakeCatalog{}).
		GetVehicle(context.Background(), 5, 77)
	if !errors.Is(err, user.ErrVehicleNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestListVehiclesAndModels(t *testing.T) {
	store := &fakeStore{list: []user.VehicleDetails{{}, {}}}
	cat := fakeCatalog{models: []evmodel.EVModel{{ID: 1}, {ID: 2}, {ID: 3}}}
	svc := NewService(store, cat)

	vs, err := svc.ListVehicles(context.Background(), 9)
	if err != nil || len(vs) != 2 || store.gotUserID != 9 {
		t.Errorf("ListVehicles: n=%d err=%v user=%d", len(vs), err, store.gotUserID)
	}

	ms, err := svc.ListModels(context.Background())
	if err != nil || len(ms) != 3 {
		t.Errorf("ListModels: n=%d err=%v", len(ms), err)
	}

	_, err = NewService(store, fakeCatalog{err: errBoom}).ListModels(context.Background())
	if !errors.Is(err, errBoom) {
		t.Errorf("catalog error must be returned, got %v", err)
	}
}

func ptr(s string) *string { return &s }

func deref(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}
