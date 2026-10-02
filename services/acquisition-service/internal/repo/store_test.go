package repo

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestNotFound(t *testing.T) {
	if !errors.Is(NotFound(pgx.ErrNoRows), ErrNotFound) {
		t.Fatal("map")
	}
	if NotFound(errors.New("x")) == nil {
		t.Fatal("passthrough")
	}
}
