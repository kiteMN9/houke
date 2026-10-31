package onnx

import (
	"encoding/binary"
	"fmt"
	"io"
)

// protobuf wire type，见 https://protobuf.dev/programming-guides/encoding/
const (
	wireVarint  = 0
	wireFixed64 = 1
	wireBytes   = 2
	wireFixed32 = 5
)

type reader struct {
	b []byte
	i int
}

func (r *reader) eof() bool { return r.i >= len(r.b) }

func (r *reader) uvarint() (uint64, error) {
	var x uint64
	var s uint
	for {
		if r.i >= len(r.b) {
			return 0, io.ErrUnexpectedEOF
		}
		c := r.b[r.i]
		r.i++
		if c < 0x80 {
			if s >= 64 {
				return 0, fmt.Errorf("onnx: varint 溢出")
			}
			return x | uint64(c)<<s, nil
		}
		x |= uint64(c&0x7f) << s
		s += 7
		if s >= 64 {
			return 0, fmt.Errorf("onnx: varint 溢出")
		}
	}
}

func (r *reader) field() (num, wire int, err error) {
	tag, err := r.uvarint()
	if err != nil {
		return 0, 0, err
	}
	return int(tag >> 3), int(tag & 7), nil
}

func (r *reader) bytes() ([]byte, error) {
	n, err := r.uvarint()
	if err != nil {
		return nil, err
	}
	if n > uint64(len(r.b)-r.i) {
		return nil, io.ErrUnexpectedEOF
	}
	s := r.b[r.i : r.i+int(n)]
	r.i += int(n)
	return s, nil
}

func (r *reader) fixed32() (uint32, error) {
	if r.i+4 > len(r.b) {
		return 0, io.ErrUnexpectedEOF
	}
	v := binary.LittleEndian.Uint32(r.b[r.i:])
	r.i += 4
	return v, nil
}

func (r *reader) fixed64() (uint64, error) {
	if r.i+8 > len(r.b) {
		return 0, io.ErrUnexpectedEOF
	}
	v := binary.LittleEndian.Uint64(r.b[r.i:])
	r.i += 8
	return v, nil
}

func (r *reader) skip(wire int) error {
	switch wire {
	case wireVarint:
		_, err := r.uvarint()
		return err
	case wireFixed64:
		_, err := r.fixed64()
		return err
	case wireBytes:
		_, err := r.bytes()
		return err
	case wireFixed32:
		_, err := r.fixed32()
		return err
	default:
		return fmt.Errorf("onnx: 未知的 wire type %d", wire)
	}
}
