package storage

import (
	"context"
	"testing"
)

func TestNewS3(t *testing.T) {
	s, err := NewS3("localhost:9000", "access", "secret", "avatars", false)
	if err != nil {
		t.Fatal(err)
	}
	if s.bucket != "avatars" {
		t.Fatal(s.bucket)
	}
	if err := s.Delete(context.Background(), []string{"", ""}); err != nil {
		t.Fatal(err)
	}
}

func TestNewS3Invalid(t *testing.T) {
	if _, err := NewS3("://bad", "a", "b", "avatars", true); err == nil {
		t.Fatal("expected error")
	}
}
