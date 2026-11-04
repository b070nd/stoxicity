package dashboard

import "testing"

func TestDefaultIsValid(t *testing.T) {
	if err := Validate(Default()); err != nil {
		t.Fatalf("Default() does not validate: %v", err)
	}
}

func TestValidate(t *testing.T) {
	if err := Validate([]Widget{Digest, "nonsense"}); err == nil {
		t.Error("unknown widget accepted")
	}
	if err := Validate([]Widget{Digest, Digest}); err == nil {
		t.Error("repeated widget accepted")
	}
	if err := Validate(nil); err != nil {
		t.Errorf("empty list should be valid (means default): %v", err)
	}
}
