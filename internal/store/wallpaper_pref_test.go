package store

import "testing"

func TestParseWallpaperPref(t *testing.T) {
	p, err := ParseWallpaperPref(`{"v":1,"mode":"off"}`)
	if err != nil || p.Mode != "off" {
		t.Fatalf("off: %+v %v", p, err)
	}
	p, err = ParseWallpaperPref(`{"v":1,"mode":"color","hex":"#aabbcc"}`)
	if err != nil || p.Mode != "color" || p.Hex != "#aabbcc" {
		t.Fatalf("color: %+v %v", p, err)
	}
	p, err = ParseWallpaperPref(`{"v":1,"mode":"image","rev":123}`)
	if err != nil || p.Mode != "image" || p.Rev != 123 {
		t.Fatalf("image: %+v %v", p, err)
	}
	_, err = ParseWallpaperPref(`{"v":1,"mode":"image"}`)
	if err == nil {
		t.Fatal("expected error for image without rev")
	}
}
