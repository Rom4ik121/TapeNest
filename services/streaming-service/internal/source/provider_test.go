package source

import (
	"context"
	"testing"
)

func TestEnabled(t *testing.T) {
	if !Enabled("p2p, licensed", "licensed") || Enabled("licensed", "p2p") {
		t.Fatal("parse")
	}
	var p Provider = Licensed{}
	got, err := p.Search(context.Background(), "янтарь")
	if err != nil || len(got) != 0 || p.Name() != "licensed" {
		t.Fatalf("%v %v", got, err)
	}
	p2 := P2P{}
	if _, err := p2.Search(context.Background(), "x"); err != nil || p2.Name() != "p2p" {
		t.Fatal(err)
	}
}
