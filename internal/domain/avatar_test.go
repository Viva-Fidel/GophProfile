package domain

import "testing"

func TestPublicStatus(t *testing.T) {
	cases := map[string]string{
		ProcessingCompleted:  StatusReady,
		ProcessingFailed:     StatusFailed,
		ProcessingPending:    StatusProcessing,
		ProcessingProcessing: StatusProcessing,
	}
	for in, want := range cases {
		a := Avatar{ProcessingStatus: in}
		if a.PublicStatus() != want {
			t.Fatalf("%s: got %s want %s", in, a.PublicStatus(), want)
		}
	}
}

func TestThumbnailKey(t *testing.T) {
	a := Avatar{}
	if _, ok := a.ThumbnailKey(Size100); ok {
		t.Fatal("expected missing")
	}
	a.ThumbnailS3Keys = map[string]string{Size100: "k"}
	key, ok := a.ThumbnailKey(Size100)
	if !ok || key != "k" {
		t.Fatal(key, ok)
	}
	if _, ok := a.ThumbnailKey(Size300); ok {
		t.Fatal("expected missing 300")
	}
}
