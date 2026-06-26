package main

import (
	"encoding/binary"
	"fmt"
)

// Deepmap2 decode types
const (
	Dm2TypeNone     = 1
	Dm2TypeDefault  = 2
	Dm2TypeLossless = 3
	Dm2TypePalette  = 4
)

// Deepmap2 pixel formats
const (
	Dm2PixG8       = 1
	Dm2PixGA88     = 2
	Dm2PixRgb888   = 3
	Dm2PixRgba8888 = 4
)

// Dm2Header is the 12-byte deepmap2 header (little-endian).
type Dm2Header struct {
	Magic        [4]byte // "dmp2"
	DecodeType   uint8
	Version      uint8 // chroma_scale toggle
	PredictorType uint8
	PixelFormat  uint8
	Width        uint16
	Height       uint16
	// Palette fields (only for Palette type)
	PaletteSize uint16
	PaletteType uint16
	Palette     []uint32
}

func (h *Dm2Header) HeaderSize() int {
	if h.DecodeType == Dm2TypePalette {
		return 16 + int(h.PaletteSize)*4
	}
	return 12
}

func (h *Dm2Header) ChromaScale() uint8 {
	if h.Version != 0 {
		return 1
	}
	return 0
}

func (h *Dm2Header) BytesPerPixel() int {
	switch h.PixelFormat {
	case Dm2PixG8:
		return 1
	case Dm2PixGA88:
		return 2
	case Dm2PixRgb888:
		return 3
	case Dm2PixRgba8888:
		return 4
	}
	return int(h.PixelFormat)
}

func (h *Dm2Header) HasAlpha() bool {
	return h.PixelFormat == Dm2PixGA88 || h.PixelFormat == Dm2PixRgba8888
}

func (h *Dm2Header) IsColor() bool {
	return h.PixelFormat == Dm2PixRgb888 || h.PixelFormat == Dm2PixRgba8888
}

func (h *Dm2Header) SplitStreamComponents() int {
	if h.IsColor() {
		return 3
	}
	return 1
}

// ParseDm2Header parses a deepmap2 header from the given data.
func ParseDm2Header(data []byte) (*Dm2Header, error) {
	if len(data) < 12 {
		return nil, fmt.Errorf("dmp2 data too small: %d", len(data))
	}
	if string(data[0:4]) != "dmp2" {
		return nil, fmt.Errorf("invalid dmp2 magic: %q", string(data[0:4]))
	}
	h := &Dm2Header{}
	copy(h.Magic[:], data[0:4])
	h.DecodeType = data[4]
	h.Version = data[5]
	h.PredictorType = data[6]
	h.PixelFormat = data[7]
	h.Width = binary.LittleEndian.Uint16(data[8:10])
	h.Height = binary.LittleEndian.Uint16(data[10:12])

	if h.DecodeType == Dm2TypePalette && len(data) >= 16 {
		h.PaletteSize = binary.LittleEndian.Uint16(data[12:14])
		h.PaletteType = binary.LittleEndian.Uint16(data[14:16])
		h.Palette = make([]uint32, h.PaletteSize)
		for i := uint16(0); i < h.PaletteSize; i++ {
			h.Palette[i] = binary.LittleEndian.Uint32(data[16+i*4 : 20+i*4])
		}
	}
	return h, nil
}

const (
	predictorGroupSize = 3
	lzvnThreshold      = 0x1000
)

// DecodeDm2 decodes a deepmap2 payload into RGBA pixels.
// decompressFunc is called to decompress LZFSE/LZVN data.
func DecodeDm2(header *Dm2Header, payload []byte, decompressFunc func([]byte, bool) ([]byte, error)) ([]byte, error) {
	w := int(header.Width)
	h := int(header.Height)

	switch header.DecodeType {
	case Dm2TypeNone:
		return decodeNoneOrLossless(header, payload, false, w, h, decompressFunc)
	case Dm2TypeDefault:
		return decodeDefault(header, payload, w, h, decompressFunc)
	case Dm2TypeLossless:
		return decodeNoneOrLossless(header, payload, true, w, h, decompressFunc)
	case Dm2TypePalette:
		return decodePalette(header, payload, w, h, decompressFunc)
	default:
		return nil, fmt.Errorf("unsupported deepmap2 decode type: %d", header.DecodeType)
	}
}

func decodeDefault(header *Dm2Header, payload []byte, width, height int, decompress func([]byte, bool) ([]byte, error)) ([]byte, error) {
	decompressed, err := decompress(payload, false)
	if err != nil {
		return nil, fmt.Errorf("lzfse decompress: %w", err)
	}
	return decodeDefaultDecompressed(header, decompressed, width, height)
}

func decodeDefaultDecompressed(header *Dm2Header, decompressed []byte, width, height int) ([]byte, error) {
	hasAlpha := header.HasAlpha()
	components := header.SplitStreamComponents()
	pixelCount := width * height
	alphaSize := 0
	if hasAlpha {
		alphaSize = pixelCount
	}
	splitCount := pixelCount * components

	predictorOffset := alphaSize
	predictorEnd := predictorOffset + height
	highOffset := predictorEnd
	highEnd := highOffset + splitCount
	lowOffset := highEnd
	lowEnd := lowOffset + splitCount

	if len(decompressed) < lowEnd {
		return nil, fmt.Errorf("default decompressed data too short: need %d, got %d", lowEnd, len(decompressed))
	}

	var alphaPlane []byte
	if hasAlpha {
		alphaPlane = decompressed[:alphaSize]
	}
	predictorBytes := decompressed[predictorOffset:predictorEnd]
	highStream := decompressed[highOffset:highEnd]
	lowStream := decompressed[lowOffset:lowEnd]

	chromaScale := header.ChromaScale()
	rgba := make([]byte, pixelCount*4)
	var prevRow []int16

	splitRowWidth := width * components
	for row := 0; row < height; row++ {
		rowOff := row * splitRowWidth
		highRow := highStream[rowOff : rowOff+splitRowWidth]
		lowRow := lowStream[rowOff : rowOff+splitRowWidth]

		// Zigzag decode
		decodedSplitRow := make([]int16, splitRowWidth)
		for i := 0; i < splitRowWidth; i++ {
			combined := uint16(lowRow[i]) | (uint16(highRow[i]) << 8)
			magnitude := int16(combined >> 1)
			if combined&1 != 0 {
				decodedSplitRow[i] = -magnitude
			} else {
				decodedSplitRow[i] = magnitude
			}
		}

		// Expand grayscale to 3-component groups
		var expandedRow []int16
		if !header.IsColor() {
			expandedRow = make([]int16, width*predictorGroupSize)
			for i := 0; i < width; i++ {
				expandedRow[i*predictorGroupSize] = decodedSplitRow[i]
				expandedRow[i*predictorGroupSize+1] = 0
				expandedRow[i*predictorGroupSize+2] = 0
			}
		} else {
			expandedRow = decodedSplitRow
		}

		// Apply predictor
		predType := predictorBytes[row]
		predictedRow := applyPredictor(predType, expandedRow, prevRow)

		// Convert to RGBA
		var alphaRow []byte
		if hasAlpha {
			alphaRow = alphaPlane[row*width : (row+1)*width]
		}
		rowRGBA := rgba[row*width*4 : (row+1)*width*4]
		rowToRGBA(header.PixelFormat, predictedRow, alphaRow, chromaScale, rowRGBA, width)

		prevRow = predictedRow
	}
	return rgba, nil
}

func decodeNoneOrLossless(header *Dm2Header, payload []byte, compressed bool, width, height int, decompress func([]byte, bool) ([]byte, error)) ([]byte, error) {
	expected := width * height * header.BytesPerPixel()
	var outputBytes []byte
	if compressed {
		decompressed, err := decompress(payload, expected < lzvnThreshold)
		if err != nil {
			return nil, fmt.Errorf("decompress: %w", err)
		}
		outputBytes = decompressed
	} else {
		outputBytes = payload
	}
	if len(outputBytes) < expected {
		return nil, fmt.Errorf("output data too short: need %d, got %d", expected, len(outputBytes))
	}
	return outputBytesToRGBA(header.PixelFormat, width, height, outputBytes[:expected]), nil
}

func decodePalette(header *Dm2Header, payload []byte, width, height int, decompress func([]byte, bool) ([]byte, error)) ([]byte, error) {
	if header.PaletteSize == 0 {
		return nil, fmt.Errorf("palette mode with no palette")
	}
	pixelCount := width * height
	var expected int
	switch header.PaletteType {
	case 3:
		expected = pixelCount * 2
	case 4:
		expected = pixelCount
	default:
		return nil, fmt.Errorf("unsupported palette type: %d", header.PaletteType)
	}

	decompressed, err := decompress(payload, false)
	if err != nil {
		return nil, fmt.Errorf("palette decompress: %w", err)
	}
	if len(decompressed) < expected {
		return nil, fmt.Errorf("palette data too short: need %d, got %d", expected, len(decompressed))
	}

	rgba := make([]byte, pixelCount*4)
	switch header.PaletteType {
	case 3:
		// alpha_plane + index_plane
		for i := 0; i < pixelCount; i++ {
			idx := decompressed[pixelCount+i]
			entry := header.Palette[idx]
			b := byte(entry & 0xFF)
			g := byte((entry >> 8) & 0xFF)
			r := byte((entry >> 16) & 0xFF)
			a := decompressed[i] // alpha plane
			rgba[i*4+0] = r
			rgba[i*4+1] = g
			rgba[i*4+2] = b
			rgba[i*4+3] = a
		}
	case 4:
		for i := 0; i < pixelCount; i++ {
			idx := decompressed[i]
			entry := header.Palette[idx]
			b := byte(entry & 0xFF)
			g := byte((entry >> 8) & 0xFF)
			r := byte((entry >> 16) & 0xFF)
			a := byte((entry >> 24) & 0xFF)
			rgba[i*4+0] = r
			rgba[i*4+1] = g
			rgba[i*4+2] = b
			rgba[i*4+3] = a
		}
	}
	return rgba, nil
}

// --- Predictor functions ---

func applyPredictor(predictorRaw uint8, row []int16, prevRow []int16) []int16 {
	count := len(row)
	switch predictorRaw {
	case 0:
		// None: identity
		out := make([]int16, count)
		copy(out, row)
		return out
	case 1:
		return unpredictPaeth(row, prevRow, count)
	case 2:
		return unpredictLeft(row, prevRow, count)
	case 3:
		return unpredictUp(row, prevRow, count)
	case 4:
		return unpredictMean(row, prevRow, count)
	default:
		out := make([]int16, count)
		copy(out, row)
		return out
	}
}

func unpredictLeft(data []int16, _ []int16, count int) []int16 {
	stride := predictorGroupSize
	out := make([]int16, count)
	head := stride
	if head > count {
		head = count
	}
	copy(out[:head], data[:head])
	for i := stride; i < count; i++ {
		out[i] = wrapI16(int32(data[i]) + int32(out[i-stride]))
	}
	return out
}

func unpredictUp(data []int16, prevRow []int16, count int) []int16 {
	out := make([]int16, count)
	for i := 0; i < count; i++ {
		up := int32(0)
		if prevRow != nil {
			up = int32(prevRow[i])
		}
		out[i] = wrapI16(int32(data[i]) + up)
	}
	return out
}

func unpredictMean(data []int16, prevRow []int16, count int) []int16 {
	stride := predictorGroupSize
	out := make([]int16, count)
	// First group uses Up prediction
	for i := 0; i < stride && i < count; i++ {
		up := int32(0)
		if prevRow != nil {
			up = int32(prevRow[i])
		}
		out[i] = wrapI16(int32(data[i]) + up)
	}
	for i := stride; i < count; i++ {
		left := int32(out[i-stride])
		up := int32(0)
		if prevRow != nil {
			up = int32(prevRow[i])
		}
		pred := truncDiv2(left + up + 1)
		out[i] = wrapI16(int32(data[i]) + pred)
	}
	return out
}

// Apple's variant of Paeth: only chooses between left and up
func paethPredictor(left, up, upLeft int32) int32 {
	distLeft := abs(up - upLeft)
	distUp := abs(left - upLeft)
	if distLeft <= distUp {
		return left
	}
	return up
}

func unpredictPaeth(data []int16, prevRow []int16, count int) []int16 {
	stride := predictorGroupSize
	out := make([]int16, count)
	// First group uses Up prediction
	for i := 0; i < stride && i < count; i++ {
		up := int32(0)
		if prevRow != nil {
			up = int32(prevRow[i])
		}
		out[i] = wrapI16(int32(data[i]) + up)
	}
	for i := stride; i < count; i += predictorGroupSize {
		groupSize := stride
		if i+groupSize > count {
			groupSize = count - i
		}
		left0 := int32(out[i-stride])
		up0 := int32(0)
		upLeft0 := int32(0)
		if prevRow != nil {
			up0 = int32(prevRow[i])
			if i >= stride {
				upLeft0 = int32(prevRow[i-stride])
			}
		}
		predictedFirst := paethPredictor(left0, up0, upLeft0)
		useLeft := predictedFirst == left0

		for offset := 0; offset < groupSize && i+offset < count; offset++ {
			left := int32(out[i+offset-stride])
			up := int32(0)
			if prevRow != nil {
				up = int32(prevRow[i+offset])
			}
			base := up
			if useLeft {
				base = left
			}
			out[i+offset] = wrapI16(int32(data[i+offset]) + base)
		}
	}
	return out
}

// --- Color conversion ---

func ycocgToRGB(y, co, cg int32, scale uint8) (uint8, uint8, uint8) {
	coScaled := co << scale
	cgScaled := cg << scale
	coHalf := truncDiv2(coScaled)
	cgHalf := truncDiv2(cgScaled)
	temp := y - cgHalf
	r := clampU8(temp + coScaled - coHalf)
	g := clampU8(temp + cgScaled)
	b := clampU8(temp - coHalf)
	return r, g, b
}

func rowToRGBA(pixelFormat uint8, decodedRow []int16, alphaRow []byte, chromaScale uint8, dst []byte, width int) {
	for px := 0; px < width; px++ {
		sampleBase := px * predictorGroupSize
		rgbaBase := px * 4
		luminance := int32(decodedRow[sampleBase])

		switch pixelFormat {
		case Dm2PixG8:
			gray := uint8(luminance)
			dst[rgbaBase] = gray
			dst[rgbaBase+1] = gray
			dst[rgbaBase+2] = gray
			dst[rgbaBase+3] = 0xFF
		case Dm2PixGA88:
			gray := uint8(luminance)
			alpha := byte(0xFF)
			if alphaRow != nil {
				alpha = alphaRow[px]
			}
			dst[rgbaBase] = gray
			dst[rgbaBase+1] = gray
			dst[rgbaBase+2] = gray
			dst[rgbaBase+3] = alpha
		case Dm2PixRgb888:
			r, g, b := ycocgToRGB(luminance, int32(decodedRow[sampleBase+1]), int32(decodedRow[sampleBase+2]), chromaScale)
			dst[rgbaBase] = b
			dst[rgbaBase+1] = g
			dst[rgbaBase+2] = r
			dst[rgbaBase+3] = 0xFF
		case Dm2PixRgba8888:
			r, g, b := ycocgToRGB(luminance, int32(decodedRow[sampleBase+1]), int32(decodedRow[sampleBase+2]), chromaScale)
			alpha := byte(0xFF)
			if alphaRow != nil {
				alpha = alphaRow[px]
			}
			dst[rgbaBase] = b
			dst[rgbaBase+1] = g
			dst[rgbaBase+2] = r
			dst[rgbaBase+3] = alpha
		}
	}
}

func outputBytesToRGBA(pixelFormat uint8, width, height int, bytes []byte) []byte {
	pixelCount := width * height
	rgba := make([]byte, pixelCount*4)

	switch pixelFormat {
	case Dm2PixG8:
		for i := 0; i < pixelCount; i++ {
			gray := bytes[i]
			rgba[i*4] = gray
			rgba[i*4+1] = gray
			rgba[i*4+2] = gray
			rgba[i*4+3] = 0xFF
		}
	case Dm2PixGA88:
		for i := 0; i < pixelCount; i++ {
			gray := bytes[i*2]
			alpha := bytes[i*2+1]
			rgba[i*4] = gray
			rgba[i*4+1] = gray
			rgba[i*4+2] = gray
			rgba[i*4+3] = alpha
		}
	case Dm2PixRgb888:
		for i := 0; i < pixelCount; i++ {
			b := bytes[i*3]
			g := bytes[i*3+1]
			r := bytes[i*3+2]
			rgba[i*4] = r
			rgba[i*4+1] = g
			rgba[i*4+2] = b
			rgba[i*4+3] = 0xFF
		}
	case Dm2PixRgba8888:
		for i := 0; i < pixelCount; i++ {
			b := bytes[i*4]
			g := bytes[i*4+1]
			r := bytes[i*4+2]
			a := bytes[i*4+3]
			rgba[i*4] = r
			rgba[i*4+1] = g
			rgba[i*4+2] = b
			rgba[i*4+3] = a
		}
	}
	return rgba
}

// --- Helpers ---

func clampU8(v int32) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

func truncDiv2(v int32) int32 {
	return v / 2
}

func wrapI16(v int32) int16 {
	return int16(v)
}

func abs(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
