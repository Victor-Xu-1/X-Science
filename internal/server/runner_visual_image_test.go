package server

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestVisualImageDecoderSupportsEveryInlineFormat(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for name, encode := range map[string]func(*bytes.Buffer) error{
		"png":  func(out *bytes.Buffer) error { return png.Encode(out, img) },
		"jpeg": func(out *bytes.Buffer) error { return jpeg.Encode(out, img, nil) },
		"gif":  func(out *bytes.Buffer) error { return gif.Encode(out, img, nil) },
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			if err := encode(&out); err != nil {
				t.Fatal(err)
			}
			if err := validateVisualArtifactImage(out.Bytes()); err != nil {
				t.Fatal(err)
			}
			if err := validateVisualArtifactImage(out.Bytes()[:len(out.Bytes())/2]); err == nil {
				t.Fatal("truncated image passed")
			}
		})
	}
	webp, err := base64.StdEncoding.DecodeString("UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateVisualArtifactImage(webp); err != nil {
		t.Fatalf("valid WebP rejected: %v", err)
	}
	if err := validateVisualArtifactImage(webp[:20]); err == nil {
		t.Fatal("truncated WebP passed")
	}
}

func TestVisualImageDecoderBoundsBytesAndDecodedPixels(t *testing.T) {
	if err := validateVisualArtifactImage(make([]byte, agentWorkspaceVisualMaxBytes+1)); err == nil {
		t.Fatal("oversized compressed image passed")
	}
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	content := append([]byte(nil), out.Bytes()...)
	binary.BigEndian.PutUint32(content[16:20], uint32(maxVisualArtifactPixels))
	binary.BigEndian.PutUint32(content[29:33], crc32.ChecksumIEEE(content[12:29]))
	if _, _, err := image.DecodeConfig(bytes.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	if err := validateVisualArtifactImage(content); err == nil {
		t.Fatal("decompression allocation limit was bypassed")
	}
	// A complete IHDR is still not a complete PNG pixel stream.
	if err := validateVisualArtifactImage(out.Bytes()[:33]); err == nil {
		t.Fatal("valid header with missing pixel data passed")
	}
}
