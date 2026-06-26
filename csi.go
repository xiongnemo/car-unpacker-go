package main

import (
	"encoding/binary"
	"fmt"
)

// CSIHeader is the 184-byte Core Structured Image header (little-endian).
type CSIHeader struct {
	Tag            uint32 // 'ISTC' or 'CTSI'
	Version        uint32
	RenditionFlags uint32
	Width          uint32
	Height         uint32
	ScaleFactor    uint32 // Scale * 100
	PixelFormat    uint32 // fourcc
	ColorSpace     uint32

	// csimetadata (136 bytes)
	ModTime uint32
	Layout  uint16
	Zero    uint16
	Name    [128]byte

	// csibitmaplist
	TVLength        uint32
	BitmapCount     uint32
	Reserved        uint32
	RenditionLength uint32
}

// TLVEntry is a type-length-value entry in the CSI TLV section.
type TLVEntry struct {
	Type   uint32
	Length uint32
	Data   []byte
}

// ParseCSIHeader parses a CSI header from the given data.
func ParseCSIHeader(data []byte) (*CSIHeader, error) {
	if len(data) < 184 {
		return nil, fmt.Errorf("CSI data too small: %d bytes", len(data))
	}
	csi := &CSIHeader{}
	csi.Tag = binary.LittleEndian.Uint32(data[0:4])
	csi.Version = binary.LittleEndian.Uint32(data[4:8])
	csi.RenditionFlags = binary.LittleEndian.Uint32(data[8:12])
	csi.Width = binary.LittleEndian.Uint32(data[12:16])
	csi.Height = binary.LittleEndian.Uint32(data[16:20])
	csi.ScaleFactor = binary.LittleEndian.Uint32(data[20:24])
	csi.PixelFormat = binary.LittleEndian.Uint32(data[24:28])
	csi.ColorSpace = binary.LittleEndian.Uint32(data[28:32])

	csi.ModTime = binary.LittleEndian.Uint32(data[32:36])
	csi.Layout = binary.LittleEndian.Uint16(data[36:38])
	csi.Zero = binary.LittleEndian.Uint16(data[38:40])
	copy(csi.Name[:], data[40:168])

	csi.TVLength = binary.LittleEndian.Uint32(data[168:172])
	csi.BitmapCount = binary.LittleEndian.Uint32(data[172:176])
	csi.Reserved = binary.LittleEndian.Uint32(data[176:180])
	csi.RenditionLength = binary.LittleEndian.Uint32(data[180:184])

	return csi, nil
}

// ParseTLV parses the TLV section after the CSI header.
func ParseTLV(data []byte, length uint32) []TLVEntry {
	var entries []TLVEntry
	p := uint32(0)
	for p+8 <= length {
		t := binary.LittleEndian.Uint32(data[p : p+4])
		l := binary.LittleEndian.Uint32(data[p+4 : p+8])
		p += 8
		if p+l > length {
			break
		}
		entries = append(entries, TLVEntry{Type: t, Length: l, Data: data[p : p+l]})
		p += l
	}
	return entries
}

// PixelFormatStr returns a human-readable string for a pixel format fourcc.
func PixelFormatStr(pf uint32) string {
	b := [4]byte{
		byte(pf),
		byte(pf >> 8),
		byte(pf >> 16),
		byte(pf >> 24),
	}
	return string(b[:])
}

// LayoutName returns a human-readable name for a layout type.
func LayoutName(layout uint16) string {
	names := map[uint16]string{
		10:  "OnePartFixedSize",
		11:  "OnePartTile",
		12:  "OnePartScale",
		20:  "ThreePartHTile",
		21:  "ThreePartHScale",
		22:  "ThreePartHUniform",
		23:  "ThreePartVTile",
		24:  "ThreePartVScale",
		25:  "ThreePartVUniform",
		30:  "NinePartTile",
		31:  "NinePartScale",
		32:  "NinePartHT_VT",
		33:  "NinePartHT_VS",
		34:  "NinePartHS_VT",
		1000: "Data",
		1001: "ExternalLink",
		1002: "LayerStack",
		1004: "PackedImage",
		1005: "NamedContent",
		1006: "ThinningPlaceholder",
		1007: "Texture",
		1008: "TextureImage",
		1009: "Color",
		1010: "MultisizeImageSet",
		1011: "LayerReference",
		1012: "ContentRendition",
		1013: "RecognitionObject",
	}
	if n, ok := names[layout]; ok {
		return n
	}
	return fmt.Sprintf("Unknown(%d)", layout)
}
