package image

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestDecode(t *testing.T) {
	src := image.NewRGBA(image.Rect(3, 4, 5, 7))
	for _, encode := range []func(*bytes.Buffer) error{
		func(b *bytes.Buffer) error { return png.Encode(b, src) },
		func(b *bytes.Buffer) error { return jpeg.Encode(b, src, nil) },
	} {
		var b bytes.Buffer
		if err := encode(&b); err != nil {
			t.Fatal(err)
		}
		out, err := Decode(b.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		w, h, err := Size(out)
		if err != nil || w != 2 || h != 3 {
			t.Fatalf("%d %d %v", w, h, err)
		}
	}
	if _, err := Decode([]byte("bad")); err == nil {
		t.Fatal("invalid accepted")
	}
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	data := b.Bytes()
	binary.BigEndian.PutUint32(data[16:], 100000)
	binary.BigEndian.PutUint32(data[20:], 100000)
	binary.BigEndian.PutUint32(data[29:], crc32.ChecksumIEEE(data[12:29]))
	if _, err := Decode(data); err != ErrSize {
		t.Fatalf("size: %v", err)
	}
}
