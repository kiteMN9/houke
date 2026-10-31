// Package onnx 实现了一个只够跑「我们自己导出的 kaptcha 模型」的最小 ONNX 推理器。
//
// 之所以不用 github.com/yalue/onnxruntime_go：那个库要 cgo + onnxruntime 动态库，
// 部署时得额外塞一个几十 MB 的 DLL。而我们的模型图是自己导出的，算子集完全可控
// （Conv / Relu / MaxPool / Flatten / Gemm，外加推理期可当恒等的 Identity / Dropout），
// 所以直接用 Go 实现一个解释器就能做到
// 零依赖、单文件部署。
//
// 解析部分只实现 ONNX protobuf 里用得到的字段，遇到不认识的字段按 wire type 跳过，
// 因此不会因为导出器多写了几个元数据字段就崩。
package onnx

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
)

// 常用数据类型编号，见 onnx/TensorProto.DataType。
const (
	DataTypeFloat32 = 1
	DataTypeInt64   = 7
	DataTypeInt32   = 6
)

// Attribute 是 NodeProto 的一个属性，只保留我们关心的几种取值。
type Attribute struct {
	Name    string
	Type    int32
	F       float32
	I       int64
	S       string
	Floats  []float32
	Ints    []int64
	Strings []string
	Tensor  *Tensor
}

// Node 是计算图中的一个算子实例。
type Node struct {
	Name    string
	OpType  string
	Inputs  []string
	Outputs []string
	Attrs   map[string]*Attribute
}

// Ints 返回整数数组属性，缺失时返回 def。
func (n *Node) Ints(name string, def ...int64) []int64 {
	if a, ok := n.Attrs[name]; ok && len(a.Ints) > 0 {
		return a.Ints
	}
	return def
}

// Int 返回整数属性，缺失时返回 def。
func (n *Node) Int(name string, def int64) int64 {
	if a, ok := n.Attrs[name]; ok {
		return a.I
	}
	return def
}

// Float 返回浮点属性，缺失时返回 def。
func (n *Node) Float(name string, def float32) float32 {
	if a, ok := n.Attrs[name]; ok {
		return a.F
	}
	return def
}

// Tensor 是权重或中间结果。这里只支持 fp32 和整型，够用了。
type Tensor struct {
	Name     string
	DataType int32
	Dims     []int64
	Raw      []byte
	Floats   []float32
	Ints     []int64
}

// NewTensor 用现成的 float 切片构造张量（推理时的中间结果用）。
func NewTensor(dims []int64, data []float32) *Tensor {
	return &Tensor{Name: "", DataType: DataTypeFloat32, Dims: dims, Floats: data}
}

// Len 返回元素个数。
func (t *Tensor) Len() int {
	n := int64(1)
	for _, d := range t.Dims {
		n *= d
	}
	return int(n)
}

// Float32 返回 float32 视图，首次调用时把 raw_data 解析出来并缓存。
func (t *Tensor) Float32() ([]float32, error) {
	if t.Floats != nil {
		return t.Floats, nil
	}
	if t.DataType != DataTypeFloat32 {
		return nil, fmt.Errorf("张量 %q 数据类型 %d 不是 float32", t.Name, t.DataType)
	}
	n := t.Len()
	if len(t.Raw) != n*4 {
		return nil, fmt.Errorf("张量 %q 的 raw_data 长度 %d 与形状 %v 不符", t.Name, len(t.Raw), t.Dims)
	}
	out := make([]float32, n)
	for i := range n {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(t.Raw[i*4:]))
	}
	t.Floats = out
	return out, nil
}

// ValueInfo 描述图的输入输出。
type ValueInfo struct {
	Name     string
	ElemType int32
	Dims     []int64 // 动态维度记为 -1
}

// Graph 是 ModelProto.GraphProto 的子集。
type Graph struct {
	Name         string
	Nodes        []*Node
	Initializers map[string]*Tensor
	Inputs       []*ValueInfo
	Outputs      []*ValueInfo
}

// Model 是 ModelProto 的子集。
type Model struct {
	IRVersion    int64
	ProducerName string
	Opset        int64
	Graph        *Graph
}

// Load 读取并解析一个 .onnx 文件。
func Load(path string) (*Model, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(raw)
}

// Parse 解析 ONNX 模型字节。
func Parse(raw []byte) (*Model, error) {
	m := &Model{}
	r := &reader{b: raw}
	for !r.eof() {
		num, wire, err := r.field()
		if err != nil {
			return nil, err
		}
		switch {
		case num == 1 && wire == wireVarint: // ir_version
			v, err := r.uvarint()
			if err != nil {
				return nil, err
			}
			m.IRVersion = int64(v)
		case num == 2 && wire == wireBytes: // producer_name
			b, err := r.bytes()
			if err != nil {
				return nil, err
			}
			m.ProducerName = string(b)
		case num == 7 && wire == wireBytes: // graph
			b, err := r.bytes()
			if err != nil {
				return nil, err
			}
			g, err := parseGraph(b)
			if err != nil {
				return nil, err
			}
			m.Graph = g
		case num == 8 && wire == wireBytes: // opset_import
			b, err := r.bytes()
			if err != nil {
				return nil, err
			}
			v, err := parseOpset(b)
			if err != nil {
				return nil, err
			}
			if v > m.Opset {
				m.Opset = v
			}
		default:
			if err := r.skip(wire); err != nil {
				return nil, err
			}
		}
	}
	if m.Graph == nil {
		return nil, fmt.Errorf("onnx: 模型里没有计算图")
	}
	return m, nil
}

func parseOpset(b []byte) (int64, error) {
	r := &reader{b: b}
	var version int64
	for !r.eof() {
		num, wire, err := r.field()
		if err != nil {
			return 0, err
		}
		if num == 2 && wire == wireVarint {
			v, err := r.uvarint()
			if err != nil {
				return 0, err
			}
			version = int64(v)
			continue
		}
		if err := r.skip(wire); err != nil {
			return 0, err
		}
	}
	return version, nil
}

func parseGraph(b []byte) (*Graph, error) {
	g := &Graph{Initializers: make(map[string]*Tensor)}
	r := &reader{b: b}
	for !r.eof() {
		num, wire, err := r.field()
		if err != nil {
			return nil, err
		}
		switch {
		case num == 1 && wire == wireBytes: // node
			bb, err := r.bytes()
			if err != nil {
				return nil, err
			}
			n, err := parseNode(bb)
			if err != nil {
				return nil, err
			}
			g.Nodes = append(g.Nodes, n)
		case num == 2 && wire == wireBytes: // name
			bb, err := r.bytes()
			if err != nil {
				return nil, err
			}
			g.Name = string(bb)
		case num == 5 && wire == wireBytes: // initializer
			bb, err := r.bytes()
			if err != nil {
				return nil, err
			}
			t, err := parseTensor(bb)
			if err != nil {
				return nil, err
			}
			g.Initializers[t.Name] = t
		case num == 11 && wire == wireBytes: // input
			bb, err := r.bytes()
			if err != nil {
				return nil, err
			}
			v, err := parseValueInfo(bb)
			if err != nil {
				return nil, err
			}
			g.Inputs = append(g.Inputs, v)
		case num == 12 && wire == wireBytes: // output
			bb, err := r.bytes()
			if err != nil {
				return nil, err
			}
			v, err := parseValueInfo(bb)
			if err != nil {
				return nil, err
			}
			g.Outputs = append(g.Outputs, v)
		default:
			if err := r.skip(wire); err != nil {
				return nil, err
			}
		}
	}
	return g, nil
}

func parseNode(b []byte) (*Node, error) {
	n := &Node{Attrs: make(map[string]*Attribute)}
	r := &reader{b: b}
	for !r.eof() {
		num, wire, err := r.field()
		if err != nil {
			return nil, err
		}
		switch {
		case num == 1 && wire == wireBytes: // input
			bb, err := r.bytes()
			if err != nil {
				return nil, err
			}
			n.Inputs = append(n.Inputs, string(bb))
		case num == 2 && wire == wireBytes: // output
			bb, err := r.bytes()
			if err != nil {
				return nil, err
			}
			n.Outputs = append(n.Outputs, string(bb))
		case num == 3 && wire == wireBytes: // name
			bb, err := r.bytes()
			if err != nil {
				return nil, err
			}
			n.Name = string(bb)
		case num == 4 && wire == wireBytes: // op_type
			bb, err := r.bytes()
			if err != nil {
				return nil, err
			}
			n.OpType = string(bb)
		case num == 5 && wire == wireBytes: // attribute
			bb, err := r.bytes()
			if err != nil {
				return nil, err
			}
			a, err := parseAttribute(bb)
			if err != nil {
				return nil, err
			}
			n.Attrs[a.Name] = a
		default:
			if err := r.skip(wire); err != nil {
				return nil, err
			}
		}
	}
	return n, nil
}

func parseAttribute(b []byte) (*Attribute, error) {
	a := &Attribute{}
	r := &reader{b: b}
	for !r.eof() {
		num, wire, err := r.field()
		if err != nil {
			return nil, err
		}
		switch {
		case num == 1 && wire == wireBytes:
			bb, err := r.bytes()
			if err != nil {
				return nil, err
			}
			a.Name = string(bb)
		case num == 20 && wire == wireVarint:
			v, err := r.uvarint()
			if err != nil {
				return nil, err
			}
			a.Type = int32(v)
		case num == 2 && wire == wireFixed32:
			v, err := r.fixed32()
			if err != nil {
				return nil, err
			}
			a.F = math.Float32frombits(v)
		case num == 3 && wire == wireVarint:
			v, err := r.uvarint()
			if err != nil {
				return nil, err
			}
			a.I = int64(v)
		case num == 4 && wire == wireBytes:
			bb, err := r.bytes()
			if err != nil {
				return nil, err
			}
			a.S = string(bb)
		case num == 7 && wire == wireBytes: // floats（packed）
			bb, err := r.bytes()
			if err != nil {
				return nil, err
			}
			for i := 0; i+4 <= len(bb); i += 4 {
				a.Floats = append(a.Floats, math.Float32frombits(binary.LittleEndian.Uint32(bb[i:])))
			}
		case num == 7 && wire == wireFixed32: // floats（非 packed）
			v, err := r.fixed32()
			if err != nil {
				return nil, err
			}
			a.Floats = append(a.Floats, math.Float32frombits(v))
		case num == 8 && wire == wireBytes: // ints（packed）
			bb, err := r.bytes()
			if err != nil {
				return nil, err
			}
			rr := &reader{b: bb}
			for !rr.eof() {
				v, err := rr.uvarint()
				if err != nil {
					return nil, err
				}
				a.Ints = append(a.Ints, int64(v))
			}
		case num == 8 && wire == wireVarint:
			v, err := r.uvarint()
			if err != nil {
				return nil, err
			}
			a.Ints = append(a.Ints, int64(v))
		case num == 9 && wire == wireBytes: // strings
			bb, err := r.bytes()
			if err != nil {
				return nil, err
			}
			a.Strings = append(a.Strings, string(bb))
		case num == 5 && wire == wireBytes: // t（Tensor）
			bb, err := r.bytes()
			if err != nil {
				return nil, err
			}
			t, err := parseTensor(bb)
			if err != nil {
				return nil, err
			}
			a.Tensor = t
		default:
			if err := r.skip(wire); err != nil {
				return nil, err
			}
		}
	}
	return a, nil
}

func parseTensor(b []byte) (*Tensor, error) {
	t := &Tensor{}
	r := &reader{b: b}
	for !r.eof() {
		num, wire, err := r.field()
		if err != nil {
			return nil, err
		}
		switch {
		case num == 1 && wire == wireVarint: // dims
			v, err := r.uvarint()
			if err != nil {
				return nil, err
			}
			t.Dims = append(t.Dims, int64(v))
		case num == 1 && wire == wireBytes: // dims（packed）
			bb, err := r.bytes()
			if err != nil {
				return nil, err
			}
			rr := &reader{b: bb}
			for !rr.eof() {
				v, err := rr.uvarint()
				if err != nil {
					return nil, err
				}
				t.Dims = append(t.Dims, int64(v))
			}
		case num == 2 && wire == wireVarint: // data_type
			v, err := r.uvarint()
			if err != nil {
				return nil, err
			}
			t.DataType = int32(v)
		case num == 4 && wire == wireBytes: // float_data
			bb, err := r.bytes()
			if err != nil {
				return nil, err
			}
			for i := 0; i+4 <= len(bb); i += 4 {
				t.Floats = append(t.Floats, math.Float32frombits(binary.LittleEndian.Uint32(bb[i:])))
			}
		case num == 7 && wire == wireBytes: // int64_data
			bb, err := r.bytes()
			if err != nil {
				return nil, err
			}
			rr := &reader{b: bb}
			for !rr.eof() {
				v, err := rr.uvarint()
				if err != nil {
					return nil, err
				}
				t.Ints = append(t.Ints, int64(v))
			}
		case num == 8 && wire == wireBytes: // name
			bb, err := r.bytes()
			if err != nil {
				return nil, err
			}
			t.Name = string(bb)
		case num == 9 && wire == wireBytes: // raw_data
			bb, err := r.bytes()
			if err != nil {
				return nil, err
			}
			t.Raw = bb
		case num == 14 && wire == wireVarint: // data_location
			v, err := r.uvarint()
			if err != nil {
				return nil, err
			}
			if v != 0 {
				return nil, fmt.Errorf("onnx: 不支持 external_data")
			}
		default:
			if err := r.skip(wire); err != nil {
				return nil, err
			}
		}
	}
	return t, nil
}

func parseValueInfo(b []byte) (*ValueInfo, error) {
	v := &ValueInfo{}
	r := &reader{b: b}
	for !r.eof() {
		num, wire, err := r.field()
		if err != nil {
			return nil, err
		}
		switch {
		case num == 1 && wire == wireBytes: // name
			bb, err := r.bytes()
			if err != nil {
				return nil, err
			}
			v.Name = string(bb)
		case num == 2 && wire == wireBytes: // type -> tensor_type
			bb, err := r.bytes()
			if err != nil {
				return nil, err
			}
			if err := parseTypeProto(bb, v); err != nil {
				return nil, err
			}
		default:
			if err := r.skip(wire); err != nil {
				return nil, err
			}
		}
	}
	return v, nil
}

func parseTypeProto(b []byte, v *ValueInfo) error {
	r := &reader{b: b}
	for !r.eof() {
		num, wire, err := r.field()
		if err != nil {
			return err
		}
		if num == 1 && wire == wireBytes { // tensor_type
			bb, err := r.bytes()
			if err != nil {
				return err
			}
			return parseTensorType(bb, v)
		}
		if err := r.skip(wire); err != nil {
			return err
		}
	}
	return nil
}

func parseTensorType(b []byte, v *ValueInfo) error {
	r := &reader{b: b}
	for !r.eof() {
		num, wire, err := r.field()
		if err != nil {
			return err
		}
		switch {
		case num == 1 && wire == wireVarint: // elem_type
			val, err := r.uvarint()
			if err != nil {
				return err
			}
			v.ElemType = int32(val)
		case num == 2 && wire == wireBytes: // shape
			bb, err := r.bytes()
			if err != nil {
				return err
			}
			dims, err := parseShape(bb)
			if err != nil {
				return err
			}
			v.Dims = dims
		default:
			if err := r.skip(wire); err != nil {
				return err
			}
		}
	}
	return nil
}

func parseShape(b []byte) ([]int64, error) {
	var dims []int64
	r := &reader{b: b}
	for !r.eof() {
		num, wire, err := r.field()
		if err != nil {
			return nil, err
		}
		if num == 1 && wire == wireBytes { // dim
			bb, err := r.bytes()
			if err != nil {
				return nil, err
			}
			dr := &reader{b: bb}
			d := int64(-1) // 动态维度
			for !dr.eof() {
				n, w, err := dr.field()
				if err != nil {
					return nil, err
				}
				if n == 1 && w == wireVarint {
					val, err := dr.uvarint()
					if err != nil {
						return nil, err
					}
					d = int64(val)
					continue
				}
				if err := dr.skip(w); err != nil {
					return nil, err
				}
			}
			dims = append(dims, d)
			continue
		}
		if err := r.skip(wire); err != nil {
			return nil, err
		}
	}
	return dims, nil
}
