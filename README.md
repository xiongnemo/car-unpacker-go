# car-unpacker-go

Cross-platform Go tool to list and extract images from Apple `.car` (Asset Catalog) files. Works on Windows, macOS, and Linux without Apple's private CoreUI framework.

## Features

- Parses BOMStore container format (block table, named variables, B+ trees)
- Extracts CoreUI rendition metadata (CSI headers, TLV entries)
- Decodes deepmap2 (dmp2) image payloads with all 4 decode types
- Handles banded LZFSE streams with automatic band height detection
- Outputs PNG files (RGB or grayscale)

## Supported Formats

| Format | Status |
|--------|--------|
| deepmap2 Type 1 (None/raw) | ✅ |
| deepmap2 Type 2 (Default: LZFSE + zigzag + predictor + YCoCg) | ✅ |
| deepmap2 Type 3 (Lossless: LZFSE/LZVN) | ✅ |
| deepmap2 Type 4 (Palette) | ✅ |
| Banded images (multiple LZFSE streams) | ✅ |
| palette-img (older iOS) | ❌ (no dmp2) |
| JPEG/HEIF raw renditions | ❌ (not yet) |
| Data renditions (PDF, text) | ❌ (not yet) |

## Prerequisites

- Go 1.21+
- `lzfse` binary in PATH or at `../lzfse/lzfse[.exe]`

Build lzfse from the vendored source at `_tools/lzfse/`:

```bash
cd _tools/lzfse
gcc -O2 -o lzfse.exe src/lzfse_*.c src/lzvn_*.c -Isrc
```

## Build

```bash
go build -o car_unpacker .
```

## Usage

```bash
./car_unpacker <Assets.car> [output_dir]
```

Lists all named blocks, trees, and renditions, then extracts images to `output_dir/`.

## Architecture

| File | Purpose |
|------|---------|
| `bom.go` | BOMStore parser: header, block table, variables, B+ tree traversal |
| `csi.go` | CSI (Core Structured Image) header parser, TLV entries |
| `deepmap2.go` | Deepmap2 decoder: zigzag, predictors (None/Paeth/Left/Up/Mean), YCoCg→RGB, palette |
| `main.go` | CLI entry point, MLEC/RAWD handling, LZFSE decompression via external binary, PNG output |

### Data Pipeline

```
Assets.car → BOMStore parser → RENDITIONS tree → CSI header
    → MLEC/RAWD bitmap block → dmp2 header
    → size-prefixed LZFSE streams → decompress each band
    → zigzag decode → predictor → YCoCg→RGB → RGBA
    → stitch bands vertically → PNG
```

### Key Format Details

- **BOMStore header**: 512 bytes (32 real + 480 padding), big-endian
- **Block table**: at `indexOffset`, `numPointers(u32_be)` + `(addr:u32_be, len:u32_be)` pairs
- **Variables**: `count(u32_be)` + `(blockID:u32_be, nameLen:u8, name:[]byte)` entries
- **Tree header**: `tree(4) + version(4) + child(4) + blockSize(4) + pathCount(4) + unknown(1)` = 21 bytes
- **Tree node**: `isLeaf(u16_be) + count(u16_be) + forward(u32_be) + backward(u32_be)` + entries of `(valueIdx:u32_be, keyIdx:u32_be)`
- **CSI header**: 184 bytes, little-endian. Tag `ISTC`, pixel format as fourcc, layout type, name[128]
- **dmp2 header**: 12 bytes. Magic `dmp2`, decode_type(1), version(1), predictor_type(1), pixel_format(1), width(u16_le), height(u16_le)
- **Banded streams**: `[u32_le size][data]` repeated after dmp2 header
- **YCoCg**: `chroma_scale = 1 if version != 0 else 0`
- **Pixel byte order**: BGR (not RGB) in RGBA buffer
- **Paeth predictor**: Apple's variant — only compares `left` vs `up` (not `up_left`)
- **trunc_div2**: Truncates toward zero (not Python-style floor toward -∞)
