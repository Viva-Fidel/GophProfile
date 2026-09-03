package broker

import "testing"

func TestNewRabbitInvalid(t *testing.T) {
	if _, err := NewRabbit(""); err == nil {
		t.Fatal("expected error")
	}
}
