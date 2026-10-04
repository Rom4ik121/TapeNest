package domain

import "testing"

func TestValidate(t *testing.T) {
	r := Identity()
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	r.Filter = "vivid"
	r.Text.Text = "Привет"
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	r.Exposure = 3
	if err := r.Validate(); err == nil {
		t.Fatal("exposure")
	}
	r = Identity()
	r.Rotate = 45
	if err := r.Validate(); err == nil {
		t.Fatal("rotate")
	}
	r = Identity()
	r.Format = "gif"
	if err := r.Validate(); err == nil {
		t.Fatal("format")
	}
}
