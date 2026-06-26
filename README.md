# car-unpacker-go

Cross-platform Go tool to list and extract assets from Apple `.car` (Asset Catalog) files. Works on Windows, macOS, and Linux without Apple's private CoreUI framework.

## Features

- Parses BOMStore container format (block table, named variables, B+ trees)
- Extracts CoreUI rendition metadata (CSI headers, TLV entries)
- Decodes deepmap2 (dmp2) image payloads with all 4 decode types
- Handles banded LZFSE streams with automatic band height detection
- Extracts palette-img (compression type 8) with LZFSE decompression
- Extracts raw data renditions (JPEG, HEIF, PDF, text) via RAWD tag
- Extracts named colors (COLR/RLOC) as JSON with RGBA float64 components
- Outputs PNG files (RGB, RGBA, or grayscale)

## Supported Rendition Types

| Type | Layout ID | Status | Output |
|------|-----------|--------|--------|
| deepmap2 (None/Default/Lossless/Palette) | 10-12 | ✅ | PNG |
| palette-img (compression 8) | 10-12 | ✅ | PNG |
| Raw data (JPEG/HEIF/PDF/text) | 1000 | ✅ | Original format |
| Color | 1009 | ✅ | JSON |

## Supported Compression Types

| ID | Name | Status |
|----|------|--------|
| 0 | uncompressed | ❌ |
| 1 | rle | ❌ |
| 2 | zip | ❌ |
| 3 | lzvn | ❌ |
| 4 | lzfse | ✅ (via external binary) |
| 5 | jpeg-lzfse | ❌ |
| 6 | blurred | ❌ |
| 7 | astc | ❌ |
| 8 | palette-img | ✅ |
| 9 | hevc | ❌ |
| 10 | deepmap-lzfse | ❌ |
| 11 | deepmap2 | ✅ |
| 12 | dxtc | ❌ |

## Prerequisites

- Go 1.21+
- `lzfse` binary in PATH or next to the executable

Build lzfse from the vendored source:

```bash
cd _tools/lzfse
gcc -O2 -o lzfse src/lzfse_*.c src/lzvn_*.c -Isrc
```

## Build

```bash
go build -o car_unpacker .
```

## Usage

```bash
./car_unpacker <Assets.car> [output_dir]
```

Lists all named blocks, trees, and renditions, then extracts assets to `output_dir/`.

## Architecture

| File | Purpose |
|------|---------|
| `bom.go` | BOMStore parser: header, block table, variables, B+ tree traversal |
| `csi.go` | CSI (Core Structured Image) header parser, TLV entries |
| `deepmap2.go` | Deepmap2 decoder: zigzag, predictors (None/Paeth/Left/Up/Mean), YCoCg→RGB, palette |
| `main.go` | CLI entry point, MLEC/RAWD/Color handling, LZFSE decompression, PNG output |

### Data Pipeline

```
Assets.car → BOMStore parser → RENDITIONS tree → CSI header
    → Bitmap tag dispatch:
        MLEC/CELM → compression type → palette-img or deepmap2
        RAWD/DWAR → raw data (JPEG/PDF/text/etc.)
        RLOC/COLR → named color (JSON)
        dmp2      → deepmap2 image
    → Output: PNG / original format / JSON
```

### Key Format Details

- **BOMStore header**: 512 bytes (32 real + 480 padding), big-endian
- **Block table**: at `indexOffset`, `numPointers(u32_be)` + `(addr:u32_be, len:u32_be)` pairs
- **Variables**: `count(u32_be)` + `(blockID:u32_be, nameLen:u8, name:[]byte)` entries
- **Tree header**: `tree(4) + version(4) + child(4) + blockSize(4) + pathCount(4) + unknown(1)` = 21 bytes
- **Tree node**: `isLeaf(u16_be) + count(u16_be) + forward(u32_be) + backward(u32_be)` + entries of `(valueIdx:u32_be, keyIdx:u32_be)`
- **CSI header**: 184 bytes, little-endian. Tag `ISTC`, pixel format as fourcc, layout type, name[128]
- **dmp2 header**: 12 bytes. Magic `dmp2`, decode_type(1), version(1), predictor_type(1), pixel_format(1), width(u16_le), height(u16_le)
- **palette-img**: MLEC header → LZFSE → `0xCAFEF00D(u32) + version(u32) + palette_count(u16) + BGRA[count] + u8[w*h]`
- **Color (RLOC)**: `RLOC(4) + flags(4) + color_type(4) + num_components(4) + f64[num_components]`
- **Banded streams**: `[u32_le size][data]` repeated after dmp2 header
- **YCoCg**: `chroma_scale = 1 if version != 0 else 0`
- **Pixel byte order**: BGR (not RGB) in RGBA buffer
- **Paeth predictor**: Apple's variant — only compares `left` vs `up` (not `up_left`)
- **trunc_div2**: Truncates toward zero (not Python-style floor toward -∞)
