package main

import (
	"encoding/binary"
	"fmt"
)

// BOMStore constants
const (
	BOMHeaderSize = 512
	MaxPointers   = 256
)

// BOMHeader is the 512-byte BOMStore file header (big-endian).
type BOMHeader struct {
	Magic          [8]byte
	Version        uint32
	NumberOfBlocks uint32
	IndexOffset    uint32
	IndexLength    uint32
	VarsOffset     uint32
	VarsLength     uint32
}

// BOMPointer is a (address, length) pair in the block table.
type BOMPointer struct {
	Address uint32
	Length  uint32
}

// BOMVar is a named entry in the variables table.
type BOMVar struct {
	Index uint32
	Name  string
}

// TreeHeader is the header of a B+ tree in BOM.
type TreeHeader struct {
	Tag       [4]byte // "tree"
	Version   uint32
	Child     uint32 // root node block index
	BlockSize uint32
	PathCount uint32
	Unknown3  uint8
}

// TreeNode is a node in the B+ tree.
type TreeNode struct {
	IsLeaf   uint16
	Count    uint16
	Forward  uint32
	Backward uint32
}

// TreeEntry is a key-value pair in a tree node.
type TreeEntry struct {
	ValueIndex uint32
	KeyIndex   uint32
}

// BOM represents a parsed BOM file.
type BOM struct {
	Header   BOMHeader
	Pointers []BOMPointer
	Vars     []BOMVar
	Data     []byte
}

// ParseBOM reads and parses a BOM file from the given data.
func ParseBOM(data []byte) (*BOM, error) {
	if len(data) < BOMHeaderSize {
		return nil, fmt.Errorf("file too small for BOM header: %d bytes", len(data))
	}

	bom := &BOM{Data: data}

	// Parse header (big-endian)
	bom.Header.Magic = [8]byte(data[0:8])
	if string(bom.Header.Magic[:]) != "BOMStore" {
		return nil, fmt.Errorf("invalid BOM magic: %q", string(bom.Header.Magic[:]))
	}
	bom.Header.Version = binary.BigEndian.Uint32(data[8:12])
	bom.Header.NumberOfBlocks = binary.BigEndian.Uint32(data[12:16])
	bom.Header.IndexOffset = binary.BigEndian.Uint32(data[16:20])
	bom.Header.IndexLength = binary.BigEndian.Uint32(data[20:24])
	bom.Header.VarsOffset = binary.BigEndian.Uint32(data[24:28])
	bom.Header.VarsLength = binary.BigEndian.Uint32(data[28:32])

	// Parse block table at IndexOffset
	// Format: NumberOfPointers(u32_be), then Pointer[] (addr:u32_be, len:u32_be)
	idx := bom.Header.IndexOffset
	numPtrs := binary.BigEndian.Uint32(data[idx : idx+4])
	bom.Pointers = make([]BOMPointer, numPtrs)
	for i := uint32(0); i < numPtrs; i++ {
		off := idx + 4 + i*8
		bom.Pointers[i].Address = binary.BigEndian.Uint32(data[off : off+4])
		bom.Pointers[i].Length = binary.BigEndian.Uint32(data[off+4 : off+8])
	}

	// Parse variables table at VarsOffset
	vIdx := bom.Header.VarsOffset
	varCount := binary.BigEndian.Uint32(data[vIdx : vIdx+4])
	p := vIdx + 4
	bom.Vars = make([]BOMVar, varCount)
	for i := uint32(0); i < varCount; i++ {
		bom.Vars[i].Index = binary.BigEndian.Uint32(data[p : p+4])
		p += 4
		nameLen := data[p]
		p++
		bom.Vars[i].Name = string(data[p : p+uint32(nameLen)])
		p += uint32(nameLen)
	}

	return bom, nil
}

// Block returns the data slice for the given block index.
func (b *BOM) Block(idx uint32) []byte {
	if idx >= uint32(len(b.Pointers)) {
		return nil
	}
	ptr := b.Pointers[idx]
	if ptr.Address == 0 && ptr.Length == 0 {
		return nil
	}
	end := ptr.Address + ptr.Length
	if int(end) > len(b.Data) {
		return nil
	}
	return b.Data[ptr.Address:end]
}

// NamedBlock returns the block index and data for a named variable.
func (b *BOM) NamedBlock(name string) (uint32, []byte) {
	for _, v := range b.Vars {
		if v.Name == name {
			return v.Index, b.Block(v.Index)
		}
	}
	return 0, nil
}

// ParseTree reads a tree header from the given block data.
func ParseTree(data []byte) (*TreeHeader, error) {
	if len(data) < 21 {
		return nil, fmt.Errorf("tree block too small: %d", len(data))
	}
	th := &TreeHeader{}
	copy(th.Tag[:], data[0:4])
	if string(th.Tag[:]) != "tree" {
		return nil, fmt.Errorf("invalid tree tag: %q", string(th.Tag[:]))
	}
	th.Version = binary.BigEndian.Uint32(data[4:8])
	th.Child = binary.BigEndian.Uint32(data[8:12])
	th.BlockSize = binary.BigEndian.Uint32(data[12:16])
	th.PathCount = binary.BigEndian.Uint32(data[16:20])
	th.Unknown3 = data[20]
	return th, nil
}

// ParseTreeNode reads a tree node header and its entries.
func ParseTreeNode(data []byte) (*TreeNode, []TreeEntry, error) {
	if len(data) < 12 {
		return nil, nil, fmt.Errorf("tree node too small: %d", len(data))
	}
	node := &TreeNode{
		IsLeaf:   binary.BigEndian.Uint16(data[0:2]),
		Count:    binary.BigEndian.Uint16(data[2:4]),
		Forward:  binary.BigEndian.Uint32(data[4:8]),
		Backward: binary.BigEndian.Uint32(data[8:12]),
	}
	entries := make([]TreeEntry, node.Count)
	p := uint32(12)
	for i := uint16(0); i < node.Count; i++ {
		entries[i].ValueIndex = binary.BigEndian.Uint32(data[p : p+4])
		entries[i].KeyIndex = binary.BigEndian.Uint32(data[p+4 : p+8])
		p += 8
	}
	return node, entries, nil
}
