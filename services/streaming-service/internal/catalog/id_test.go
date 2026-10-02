package catalog

import "testing"

func TestUUIDFromSeedMatchesFrontend(t *testing.T) {
	if got := UUIDFromSeed("title:0"); got != "2677eb7b-513c-4ac8-8021-29add2525a7a" {
		t.Fatalf("title:0 = %s", got)
	}
	if got := UUIDFromSeed("hello"); got != "4f9f2cab-ff26-4b64-a095-c3dd6e484ca6" {
		t.Fatalf("hello = %s", got)
	}
}
