package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <Assets.car> [output_dir]\n", os.Args[0])
		os.Exit(1)
	}

	carPath := os.Args[1]
	outDir := "car_output"
	if len(os.Args) >= 3 {
		outDir = os.Args[2]
	}

	data, err := os.ReadFile(carPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
		os.Exit(1)
	}

	bom, err := ParseBOM(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing BOM: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("BOMStore: version=%d, blocks=%d\n", bom.Header.Version, bom.Header.NumberOfBlocks)

	// List variables
	fmt.Println("\nNamed blocks:")
	for _, v := range bom.Vars {
		ptr := bom.Pointers[v.Index]
		fmt.Printf("  %-20s -> block[%d] addr=0x%X len=%d\n", v.Name, v.Index, ptr.Address, ptr.Length)
	}

	// Parse CARHEADER
	if idx, data := bom.NamedBlock("CARHEADER"); data != nil {
		fmt.Printf("\nCARHEADER (block[%d], %d bytes):\n", idx, len(data))
		parseCARHeader(data)
	}

	// Parse EXTENDED_METADATA
	if idx, data := bom.NamedBlock("EXTENDED_METADATA"); data != nil {
		fmt.Printf("\nEXTENDED_METADATA (block[%d], %d bytes):\n", idx, len(data))
		parseExtendedMetadata(data)
	}

	// Parse KEYFORMAT
	if idx, data := bom.NamedBlock("KEYFORMAT"); data != nil {
		fmt.Printf("\nKEYFORMAT (block[%d], %d bytes):\n", idx, len(data))
		parseKeyFormat(data)
	}

	// Parse FACETKEYS tree
	if idx, data := bom.NamedBlock("FACETKEYS"); data != nil {
		fmt.Printf("\nFACETKEYS tree (block[%d]):\n", idx)
		parseFacetKeys(bom, data)
	}

	// Parse RENDITIONS tree and extract images
	if idx, data := bom.NamedBlock("RENDITIONS"); data != nil {
		fmt.Printf("\nRENDITIONS tree (block[%d]):\n", idx)
		err := extractRenditions(bom, data, outDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error extracting renditions: %v\n", err)
		}
	}
}

func parseCARHeader(data []byte) {
	// Tag(4), CoreUIVersion(4), StorageVersion(4), StorageTimestamp(4), RenditionCount(4)
	// MainVersionString(128), VersionString(256), UUID(16), AssociatedChecksum(4)
	// SchemaVersion(4), ColorSpaceID(4), KeySemantics(4)
	if len(data) < 436 {
		fmt.Printf("  (too small: %d bytes)\n", len(data))
		return
	}
	tag := string(data[0:4])
	if tag == "RATC" {
		// Big-endian, need swap
		fmt.Printf("  Tag: RATC (big-endian)\n")
	} else if tag == "CTAR" {
		fmt.Printf("  Tag: CTSR (little-endian)\n")
	}

	coreuiVer := binary.LittleEndian.Uint32(data[4:8])
	storageVer := binary.LittleEndian.Uint32(data[8:12])
	timestamp := binary.LittleEndian.Uint32(data[12:16])
	rendCount := binary.LittleEndian.Uint32(data[16:20])
	mainVer := cString(data[20:148])
	verStr := cString(data[148:404])
	schemaVer := binary.LittleEndian.Uint32(data[420:424])

	fmt.Printf("  CoreUIVersion: %d\n", coreuiVer)
	fmt.Printf("  StorageVersion: %d\n", storageVer)
	fmt.Printf("  Timestamp: %d\n", timestamp)
	fmt.Printf("  RenditionCount: %d\n", rendCount)
	fmt.Printf("  MainVersion: %s\n", mainVer)
	fmt.Printf("  VersionString: %s\n", verStr)
	fmt.Printf("  SchemaVersion: %d\n", schemaVer)
}

func parseExtendedMetadata(data []byte) {
	if len(data) < 1028 {
		fmt.Printf("  (too small: %d bytes)\n", len(data))
		return
	}
	tag := string(data[0:4])
	thinning := cString(data[4:260])
	platformVer := cString(data[260:516])
	platform := cString(data[516:772])
	authoring := cString(data[772:1028])
	fmt.Printf("  Tag: %s\n", tag)
	fmt.Printf("  Thinning: %s\n", thinning)
	fmt.Printf("  Platform: %s %s\n", platform, platformVer)
	fmt.Printf("  AuthoringTool: %s\n", authoring)
}

func parseKeyFormat(data []byte) {
	if len(data) < 12 {
		fmt.Printf("  (too small)\n")
		return
	}
	tag := string(data[0:4])
	version := binary.LittleEndian.Uint32(data[4:8])
	maxTokens := binary.LittleEndian.Uint32(data[8:12])
	fmt.Printf("  Tag: %s, Version: %d, MaxTokens: %d\n", tag, version, maxTokens)

	attrNames := map[uint32]string{
		0: "Look", 1: "Element", 2: "Part", 3: "Size", 4: "Direction",
		5: "Value", 6: "Appearance", 7: "Dimension1", 8: "Dimension2",
		9: "State", 10: "Layer", 11: "Scale", 12: "PresentationState",
		13: "Idiom", 14: "Subtype", 15: "Identifier", 16: "PreviousValue",
		17: "PreviousState", 18: "SizeClassH", 19: "SizeClassV",
		20: "MemoryClass", 21: "GraphicsClass", 22: "DisplayGamut",
		23: "DeploymentTarget", 24: "Localization", 25: "GlyphWeight",
		26: "GlyphSize",
	}
	for i := uint32(0); i < maxTokens && 12+i*4+4 <= uint32(len(data)); i++ {
		token := binary.LittleEndian.Uint32(data[12+i*4 : 16+i*4])
		name := attrNames[token]
		if name == "" {
			name = fmt.Sprintf("Unknown(%d)", token)
		}
		fmt.Printf("  Token[%d]: %s (%d)\n", i, name, token)
	}
}

func parseFacetKeys(bom *BOM, treeData []byte) {
	th, err := ParseTree(treeData)
	if err != nil {
		fmt.Printf("  Error: %v\n", err)
		return
	}
	fmt.Printf("  PathCount: %d, Child: block[%d]\n", th.PathCount, th.Child)

	// Traverse leaf nodes
	nodeData := bom.Block(th.Child)
	if nodeData == nil {
		fmt.Println("  (no root node)")
		return
	}
	for nodeData != nil {
		node, entries, err := ParseTreeNode(nodeData)
		if err != nil {
			fmt.Printf("  Error parsing node: %v\n", err)
			return
		}
		for _, entry := range entries {
			keyData := bom.Block(entry.KeyIndex)
			valData := bom.Block(entry.ValueIndex)
			name := cString(keyData)
			fmt.Printf("  '%s'", name)
			if valData != nil && len(valData) >= 4 {
				// Parse renditionkeytoken: cursorHotSpot(4), numAttrs(2), attrs[]
				numAttrs := binary.LittleEndian.Uint16(valData[4:6])
				fmt.Printf(" (attrs: %d)", numAttrs)
			}
			fmt.Println()
		}
		if node.Forward == 0 {
			break
		}
		nodeData = bom.Block(node.Forward)
	}
}

func extractRenditions(bom *BOM, treeData []byte, outDir string) error {
	th, err := ParseTree(treeData)
	if err != nil {
		return err
	}
	fmt.Printf("  PathCount: %d, Child: block[%d]\n", th.PathCount, th.Child)

	os.MkdirAll(outDir, 0755)

	rendIdx := 0
	nodeData := bom.Block(th.Child)
	for nodeData != nil {
		node, entries, err := ParseTreeNode(nodeData)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			csiData := bom.Block(entry.ValueIndex)
			if csiData == nil {
				continue
			}
			fmt.Printf("\n  Rendition[%d] (block[%d], %d bytes):\n", rendIdx, entry.ValueIndex, len(csiData))
			err := processRendition(csiData, outDir, rendIdx)
			if err != nil {
				fmt.Printf("    Error: %v\n", err)
			}
			rendIdx++
		}
		if node.Forward == 0 {
			break
		}
		nodeData = bom.Block(node.Forward)
	}
	return nil
}

func processRendition(csiData []byte, outDir string, idx int) error {
	csi, err := ParseCSIHeader(csiData)
	if err != nil {
		return err
	}

	name := cString(csi.Name[:])
	pfStr := PixelFormatStr(csi.PixelFormat)
	layName := LayoutName(csi.Layout)

	fmt.Printf("    Name: %s\n", name)
	fmt.Printf("    Size: %dx%d, Scale: %d\n", csi.Width, csi.Height, csi.ScaleFactor)
	fmt.Printf("    PixelFormat: %s (0x%08X)\n", pfStr, csi.PixelFormat)
	fmt.Printf("    Layout: %s (%d)\n", layName, csi.Layout)
	fmt.Printf("    BitmapCount: %d, TVLength: %d, RenditionLength: %d\n",
		csi.BitmapCount, csi.TVLength, csi.RenditionLength)

	// Parse TLV
	tlvData := csiData[184 : 184+csi.TVLength]
	tlvs := ParseTLV(tlvData, csi.TVLength)
	for _, tlv := range tlvs {
		fmt.Printf("    TLV type=%d len=%d\n", tlv.Type, tlv.Length)
	}

	// Bitmap data starts after CSI header (184) + TLV
	bitmapStart := 184 + int(csi.TVLength)

	// Parse bitmap offset table
	if bitmapStart+4 > len(csiData) {
		return fmt.Errorf("bitmap data extends beyond CSI")
	}

	bmpTag := string(csiData[bitmapStart : bitmapStart+4])
	fmt.Printf("    Bitmap tag: %s\n", bmpTag)

	if bmpTag == "MLEC" || bmpTag == "CELM" {
		return extractMLECImage(csiData, bitmapStart, outDir, idx, name, csi)
	}

	if bmpTag == "RAWD" || bmpTag == "DWAR" {
		return extractRAWDData(csiData, bitmapStart, outDir, idx, name, csi)
	}

	if bmpTag == "RLOC" || bmpTag == "COLR" {
		return extractColorRendition(csiData, bitmapStart, outDir, idx, name, csi)
	}

	// Try to find dmp2 magic directly
	dm2Start := bytes.Index(csiData[bitmapStart:], []byte("dmp2"))
	if dm2Start >= 0 {
		dm2Start += bitmapStart
		return extractDm2Image(csiData, dm2Start, outDir, idx, name)
	}

	fmt.Printf("    (no recognized image format)\n")
	return nil
}

func extractRAWDData(csiData []byte, offset int, outDir string, idx int, name string, csi *CSIHeader) error {
	if offset+12 > len(csiData) {
		return fmt.Errorf("RAWD header too small")
	}

	flags := binary.LittleEndian.Uint32(csiData[offset+4 : offset+8])
	dataLen := binary.LittleEndian.Uint32(csiData[offset+8 : offset+12])
	rawData := csiData[offset+12 : offset+12+int(dataLen)]

	// If flags indicate compression, decompress
	if flags != 0 {
		lzfseBin := findLZFSEBinary()
		if lzfseBin != "" {
			if dec, err := decompressLZFSE(lzfseBin, rawData); err == nil {
				rawData = dec
			}
		}
	}

	// Determine file extension from content
	ext := ".bin"
	if len(rawData) >= 2 {
		if rawData[0] == 0xFF && rawData[1] == 0xD8 {
			ext = ".jpg"
		} else if len(rawData) >= 4 && string(rawData[:4]) == "\x89PNG" {
			ext = ".png"
		} else if len(rawData) >= 4 && string(rawData[:4]) == "%PDF" {
			ext = ".pdf"
		} else if len(rawData) >= 12 && string(rawData[4:8]) == "ftyp" {
			ext = ".heif"
		}
	}

	outPath := filepath.Join(outDir, fmt.Sprintf("%03d_%s%s", idx, sanitizeFilename(name), ext))
	if err := os.WriteFile(outPath, rawData, 0644); err != nil {
		return err
	}
	fmt.Printf("    RAWD: %d bytes -> %s\n", len(rawData), outPath)
	return nil
}

func extractColorRendition(csiData []byte, offset int, outDir string, idx int, name string, csi *CSIHeader) error {
	if offset+16 > len(csiData) {
		return fmt.Errorf("COLR header too small")
	}

	// RLOC/COLR: tag(4), flags(4), color_type(4), num_components(4), f64[num_components]
	flags := binary.LittleEndian.Uint32(csiData[offset+4 : offset+8])
	colorType := binary.LittleEndian.Uint32(csiData[offset+8 : offset+12])
	numComp := binary.LittleEndian.Uint32(csiData[offset+12 : offset+16])

	if numComp > 10 {
		return fmt.Errorf("too many color components: %d", numComp)
	}

	compEnd := 16 + int(numComp)*8
	if offset+compEnd > len(csiData) {
		return fmt.Errorf("COLR data too short for %d components", numComp)
	}

	comps := make([]float64, numComp)
	for i := uint32(0); i < numComp; i++ {
		comps[i] = math.Float64frombits(binary.LittleEndian.Uint64(csiData[offset+16+int(i)*8 : offset+24+int(i)*8]))
	}

	_ = flags
	_ = colorType

	// Output as JSON
	outPath := filepath.Join(outDir, fmt.Sprintf("%03d_%s.json", idx, sanitizeFilename(name)))
	rgba := make([]uint8, 4)
	for i := 0; i < int(numComp) && i < 4; i++ {
		rgba[i] = uint8(comps[i]*255 + 0.5)
	}

	jsonData := fmt.Sprintf(`{"name":"%s","components":[`, name)
	for i, c := range comps {
		if i > 0 {
			jsonData += ","
		}
		jsonData += fmt.Sprintf("%g", c)
	}
	jsonData += fmt.Sprintf(`],"rgba":[%d,%d,%d,%d]}`, rgba[0], rgba[1], rgba[2], rgba[3])

	if err := os.WriteFile(outPath, []byte(jsonData), 0644); err != nil {
		return err
	}
	fmt.Printf("    Color: %s -> %s\n", jsonData, outPath)
	return nil
}

func extractMLECImage(csiData []byte, offset int, outDir string, idx int, name string, csi *CSIHeader) error {
	if offset+16 > len(csiData) {
		return fmt.Errorf("MLEC header too small")
	}

	// MLEC header: tag(4), flags(4), compressionType(4), rawDataLength(4)
	flags := binary.LittleEndian.Uint32(csiData[offset+4 : offset+8])
	compType := binary.LittleEndian.Uint32(csiData[offset+8 : offset+12])
	rawLen := binary.LittleEndian.Uint32(csiData[offset+12 : offset+16])
	_ = flags

	lzfseBin := findLZFSEBinary()
	if lzfseBin == "" {
		return fmt.Errorf("lzfse binary not found; build _tools/lzfse first")
	}

	rawData := csiData[offset+16 : offset+16+int(rawLen)]

	switch compType {
	case 8: // palette-img
		return extractPaletteImage(rawData, lzfseBin, outDir, idx, name, csi)
	case 11: // deepmap2
		dm2Start := bytes.Index(rawData, []byte("dmp2"))
		if dm2Start < 0 {
			return fmt.Errorf("no dmp2 magic in MLEC block")
		}
		return extractDm2Image(rawData, dm2Start, outDir, idx, name)
	default:
		// Try to find dmp2 magic as fallback
		dm2Start := bytes.Index(rawData, []byte("dmp2"))
		if dm2Start >= 0 {
			return extractDm2Image(rawData, dm2Start, outDir, idx, name)
		}
		fmt.Printf("    (unsupported MLEC compression type %d)\n", compType)
		return nil
	}
}

func extractPaletteImage(compressed []byte, lzfseBin string, outDir string, idx int, name string, csi *CSIHeader) error {
	decompressed, err := decompressLZFSE(lzfseBin, compressed)
	if err != nil {
		return fmt.Errorf("palette lzfse decompress: %w", err)
	}

	// Palette-img decompressed format:
	// u32 LE magic (0xCAFEF00D), u32 LE version (1), u16 LE palette_count,
	// palette_count * 4 bytes BGRA entries, width*height index bytes
	if len(decompressed) < 10 {
		return fmt.Errorf("palette data too small: %d", len(decompressed))
	}

	palMagic := binary.LittleEndian.Uint32(decompressed[0:4])
	palVersion := binary.LittleEndian.Uint32(decompressed[4:8])
	palCount := binary.LittleEndian.Uint16(decompressed[8:10])
	_ = palMagic
	_ = palVersion

	w := int(csi.Width)
	h := int(csi.Height)
	expected := 10 + int(palCount)*4 + w*h
	if len(decompressed) < expected {
		return fmt.Errorf("palette data too short: need %d, got %d", expected, len(decompressed))
	}

	fmt.Printf("    palette-img: magic=0x%08X ver=%d count=%d %dx%d\n", palMagic, palVersion, palCount, w, h)

	// Build RGBA image from palette + indices
	rgba := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		idx := decompressed[10+int(palCount)*4+i]
		bgra := binary.LittleEndian.Uint32(decompressed[10+int(idx)*4 : 14+int(idx)*4])
		rgba[i*4+0] = byte(bgra >> 16) // R
		rgba[i*4+1] = byte(bgra >> 8)  // G
		rgba[i*4+2] = byte(bgra)       // B
		rgba[i*4+3] = byte(bgra >> 24) // A
	}

	outPath := filepath.Join(outDir, fmt.Sprintf("%03d_%s.png", idx, sanitizeFilename(name)))
	return writePNG(outPath, w, h, rgba)
}

func extractDm2Image(csiData []byte, dm2Offset int, outDir string, idx int, name string) error {
	header, err := ParseDm2Header(csiData[dm2Offset:])
	if err != nil {
		return err
	}

	fmt.Printf("    dmp2: type=%d ver=%d pred=%d pixfmt=%d %dx%d\n",
		header.DecodeType, header.Version, header.PredictorType,
		header.PixelFormat, header.Width, header.Height)

	payload := csiData[dm2Offset+header.HeaderSize():]

	lzfseBin := findLZFSEBinary()
	if lzfseBin == "" {
		return fmt.Errorf("lzfse binary not found; build _tools/lzfse first")
	}

	decompressor := func(data []byte, tryLZVN bool) ([]byte, error) {
		return decompressLZFSE(lzfseBin, data)
	}

	// dmp2 payload may have size-prefixed LZFSE streams for banded images.
	// Format: [u32_le stream_size][stream_data] repeated
	// Parse the first stream to see if it decompresses correctly.
	if len(payload) < 4 {
		return fmt.Errorf("payload too small")
	}

	// Try size-prefixed stream format
	firstStreamSize := binary.LittleEndian.Uint32(payload[0:4])
	if firstStreamSize > 0 && firstStreamSize < uint32(len(payload)) && 4+int(firstStreamSize) <= len(payload) {
		// Check if stream data starts with bvx2 magic
		if string(payload[4:8]) == "bvx2" || string(payload[4:8]) == "bvxn" || string(payload[4:8]) == "bvx-" {
			return extractBandedStreams(csiData, dm2Offset, payload, header, lzfseBin, outDir, idx, name)
		}
	}

	// Single stream, try direct decompression
	rgba, err := DecodeDm2(header, payload, decompressor)
	if err != nil {
		return fmt.Errorf("decode dmp2: %w", err)
	}

	outPath := filepath.Join(outDir, fmt.Sprintf("%03d_%s.png", idx, sanitizeFilename(name)))
	return writePNG(outPath, int(header.Width), int(header.Height), rgba)
}

func extractBandedStreams(csiData []byte, dm2Offset int, payload []byte, firstHeader *Dm2Header, lzfseBin string, outDir string, idx int, name string) error {
	type band struct {
		header  *Dm2Header
		payload []byte
	}

	decompressor := func(data []byte, tryLZVN bool) ([]byte, error) {
		return decompressLZFSE(lzfseBin, data)
	}

	var bands []band
	cursor := uint32(0)
	bandIdx := 0

	for cursor+4 <= uint32(len(payload)) {
		streamSize := binary.LittleEndian.Uint32(payload[cursor : cursor+4])
		if streamSize == 0 || cursor+4+streamSize > uint32(len(payload)) {
			break
		}
		streamData := payload[cursor+4 : cursor+4+streamSize]
		cursor += 4 + streamSize

		// For the first band, use the dmp2 header we already parsed
		// For subsequent bands, they may have their own dmp2 header embedded in the stream
		if bandIdx == 0 {
			bands = append(bands, band{header: firstHeader, payload: streamData})
			fmt.Printf("    Band %d: %dx%d, %d compressed bytes\n", bandIdx, firstHeader.Width, firstHeader.Height, streamSize)
		} else {
			// Subsequent bands: the decompressed data is for the same image dimensions
			// but with a different height (remaining rows)
			// Create a modified header with adjusted height
			h := *firstHeader
			// The remaining height is total - sum of previous bands
			totalDecoded := 0
			for _, b := range bands {
				totalDecoded += int(b.header.Height)
			}
			_ = totalDecoded
			// But firstHeader.Height might be for the first band only
			// We need to figure out the actual height from the decompressed size
			bands = append(bands, band{header: &h, payload: streamData})
			fmt.Printf("    Band %d: %d compressed bytes\n", bandIdx, streamSize)
		}
		bandIdx++
	}

	if len(bands) == 0 {
		return fmt.Errorf("no bands found")
	}

	// Decode each band
	width := int(bands[0].header.Width)
	totalHeight := 0
	var allRGBA []byte

	for i, b := range bands {
		// Decompress to figure out the actual band height
		decompressed, err := decompressor(b.payload, false)
		if err != nil {
			return fmt.Errorf("decompress band %d: %w", i, err)
		}

		// For Default type, the decompressed data layout:
		// [alpha_plane: w*h] [predictor: h] [high_stream: w*h*comp] [low_stream: w*h*comp]
		// Total = w*h*(hasAlpha?1:0) + h + w*h*comp*2
		// For RGBA (pixfmt=4): comp=3, hasAlpha=true
		// Total = w*h + h + w*h*3*2 = w*h*7 + h
		comp := b.header.SplitStreamComponents()
		hasAlpha := b.header.HasAlpha()
		alphaSize := 0
		if hasAlpha {
			alphaSize = 1
		}
		// bytes_per_pixel_decompressed = alphaSize + comp*2, plus predictor byte per row
		// decompressed_size = w*h*(alphaSize + comp*2) + h
		// Solve for h: h = decompressed_size / (w*(alphaSize + comp*2) + 1)
		denom := width*(alphaSize+comp*2) + 1
		bandHeight := len(decompressed) / denom
		if bandHeight == 0 {
			bandHeight = int(b.header.Height)
		}
		fmt.Printf("    Band %d: decompressed %d bytes, calculated height=%d\n", i, len(decompressed), bandHeight)

		// Create a header with the correct band height
		bandHeader := *b.header
		bandHeader.Height = uint16(bandHeight)

		rgba, err := decodeDefaultDecompressed(&bandHeader, decompressed, width, bandHeight)
		if err != nil {
			return fmt.Errorf("decode band %d: %w", i, err)
		}
		allRGBA = append(allRGBA, rgba...)
		totalHeight += bandHeight
	}

	outPath := filepath.Join(outDir, fmt.Sprintf("%03d_%s.png", idx, sanitizeFilename(name)))
	return writePNG(outPath, width, totalHeight, allRGBA)
}

func findLZFSEBinary() string {
	// Try absolute path next to executable first
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, name := range []string{"lzfse.exe", "lzfse"} {
			p := filepath.Join(dir, name)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	// Try working directory
	for _, name := range []string{"lzfse.exe", "lzfse"} {
		if _, err := os.Stat(name); err == nil {
			abs, _ := filepath.Abs(name)
			return abs
		}
	}
	// Try PATH
	if p, err := exec.LookPath("lzfse"); err == nil {
		return p
	}
	return ""
}

func decompressLZFSE(lzfseBin string, data []byte) ([]byte, error) {
	// Write to temp file
	tmpIn, err := os.CreateTemp("", "lzfse_in_*.lzfse")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpIn.Name())
	tmpIn.Write(data)
	tmpIn.Close()

	tmpOut, err := os.CreateTemp("", "lzfse_out_*.raw")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpOut.Name())
	tmpOut.Close()

	cmd := exec.Command(lzfseBin, "-decode", "-i", tmpIn.Name(), "-o", tmpOut.Name())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("lzfse: %v: %s", err, stderr.String())
	}

	return os.ReadFile(tmpOut.Name())
}

func writePNG(path string, width, height int, rgba []byte) error {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	copy(img.Pix, rgba)

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Check if the image is actually grayscale for more efficient encoding
	isGray := true
	for i := 0; i < width*height; i++ {
		r := rgba[i*4]
		g := rgba[i*4+1]
		b := rgba[i*4+2]
		if r != g || g != b {
			isGray = false
			break
		}
	}

	if isGray {
		// Write as grayscale PNG
		gray := image.NewGray(image.Rect(0, 0, width, height))
		for i := 0; i < width*height; i++ {
			gray.Pix[i] = rgba[i*4]
		}
		return png.Encode(f, gray)
	}

	return png.Encode(f, img)
}

func cString(data []byte) string {
	if idx := bytes.IndexByte(data, 0); idx >= 0 {
		return string(data[:idx])
	}
	return string(data)
}

func sanitizeFilename(s string) string {
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	s = strings.ReplaceAll(s, ":", "_")
	s = strings.ReplaceAll(s, "*", "_")
	s = strings.ReplaceAll(s, "?", "_")
	s = strings.ReplaceAll(s, "\"", "_")
	s = strings.ReplaceAll(s, "<", "_")
	s = strings.ReplaceAll(s, ">", "_")
	s = strings.ReplaceAll(s, "|", "_")
	if s == "" {
		s = "unnamed"
	}
	return s
}

// Ensure io is used (for potential future use)
var _ io.Reader
// Ensure color is used
var _ color.Color
