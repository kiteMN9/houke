package onnx

import (
	"fmt"
	"runtime"
)

// supportedOps 是本解释器实现的算子集合。模型只有这几种算子时才能跑。
var supportedOps = map[string]bool{
	"Conv": true, "Relu": true, "MaxPool": true, "Flatten": true, "Gemm": true,
	"Identity": true, "Dropout": true,
}

// Session 持有一份已解析的模型，可以重复执行。
//
// 同一个 Session 可以被多个 goroutine 并发调用 Run：Run 只在栈上保存中间张量，
// 不改动 Session 里的共享状态。
type Session struct {
	model   *Model
	nodes   []*Node
	init    map[string]*Tensor
	inName  string
	inDims  []int64
	outName string
	workers int
}

// SessionOption 用来微调 Session 的行为。
type SessionOption func(*Session)

// WithWorkers 设置算子内部并行度，默认取 GOMAXPROCS。
func WithWorkers(n int) SessionOption {
	return func(s *Session) {
		if n > 0 {
			s.workers = n
		}
	}
}

// NewSession 校验计算图并准备执行。
func NewSession(m *Model, opts ...SessionOption) (*Session, error) {
	if m == nil || m.Graph == nil {
		return nil, fmt.Errorf("onnx: 模型没有计算图")
	}
	s := &Session{model: m, workers: runtime.GOMAXPROCS(0)}
	for _, o := range opts {
		o(s)
	}

	s.init = make(map[string]*Tensor, len(m.Graph.Initializers))
	for name, t := range m.Graph.Initializers {
		if t.DataType != DataTypeFloat32 {
			return nil, fmt.Errorf("onnx: 权重 %q 不是 float32（data_type=%d）", name, t.DataType)
		}
		// 提前解析 raw_data：之后 Run 里只读，不会改写权重（也不需要加锁）。
		if _, err := t.Float32(); err != nil {
			return nil, err
		}
		s.init[name] = t
	}

	var ins []*ValueInfo
	for _, vi := range m.Graph.Inputs {
		if _, ok := s.init[vi.Name]; ok {
			continue // 旧 opset 会把权重也列进 input
		}
		ins = append(ins, vi)
	}
	if len(ins) != 1 {
		return nil, fmt.Errorf("onnx: 期望恰好 1 个输入，实际 %d 个", len(ins))
	}
	s.inName = ins[0].Name
	s.inDims = ins[0].Dims
	if len(m.Graph.Outputs) != 1 {
		return nil, fmt.Errorf("onnx: 期望恰好 1 个输出，实际 %d 个", len(m.Graph.Outputs))
	}
	s.outName = m.Graph.Outputs[0].Name

	// 按顺序校验节点，同时检查算子是否受支持、属性是否够用。
	produced := make(map[string]bool, len(s.init)+len(m.Graph.Nodes))
	for name := range s.init {
		produced[name] = true
	}
	produced[s.inName] = true
	for i, n := range m.Graph.Nodes {
		if !supportedOps[n.OpType] {
			return nil, fmt.Errorf("onnx: 第 %d 个节点 %q 使用了不支持的算子 %q", i, n.Name, n.OpType)
		}
		if n.OpType == "Conv" {
			if g := n.Int("group", 1); g != 1 {
				return nil, fmt.Errorf("onnx: Conv %q 的 group=%d 不受支持", n.Name, g)
			}
		}
		for _, in := range n.Inputs {
			if in == "" {
				continue
			}
			if !produced[in] {
				return nil, fmt.Errorf("onnx: 节点 %q(%s) 的输入 %q 未定义（检查图顺序）", n.Name, n.OpType, in)
			}
		}
		for _, out := range n.Outputs {
			produced[out] = true
		}
		s.nodes = append(s.nodes, n)
	}
	if !produced[s.outName] {
		return nil, fmt.Errorf("onnx: 图输出 %q 没有任何节点产生", s.outName)
	}
	return s, nil
}

// InputName 返回模型输入名。
func (s *Session) InputName() string { return s.inName }

// InputDims 返回模型输入形状，动态维度为 -1。
func (s *Session) InputDims() []int64 { return s.inDims }

// OutputName 返回模型输出名。
func (s *Session) OutputName() string { return s.outName }

// Run 用一份输入张量执行整张图，返回输出张量。
func (s *Session) Run(input *Tensor) (*Tensor, error) {
	if input == nil || len(input.Dims) != len(s.inDims) {
		return nil, fmt.Errorf("onnx: 输入形状 %v 与模型要求 %v 不符", dimsOf(input), s.inDims)
	}
	for i, want := range s.inDims {
		got := input.Dims[i]
		if want > 0 && got != want {
			return nil, fmt.Errorf("onnx: 输入第 %d 维是 %d，模型要求 %d", i, got, want)
		}
	}

	env := make(map[string]*Tensor, len(s.init)+len(s.nodes))
	for k, v := range s.init {
		env[k] = v
	}
	x := *input
	if x.DataType == 0 {
		x.DataType = DataTypeFloat32
	}
	env[s.inName] = &x

	for _, n := range s.nodes {
		out, err := s.eval(n, env)
		if err != nil {
			return nil, fmt.Errorf("onnx: 节点 %q(%s) 执行失败: %w", n.Name, n.OpType, err)
		}
		env[n.Outputs[0]] = out
	}
	res, ok := env[s.outName]
	if !ok {
		return nil, fmt.Errorf("onnx: 没有算出输出 %q", s.outName)
	}
	return res, nil
}

func (s *Session) eval(n *Node, env map[string]*Tensor) (*Tensor, error) {
	get := func(i int) *Tensor {
		if i >= len(n.Inputs) || n.Inputs[i] == "" {
			return nil
		}
		return env[n.Inputs[i]]
	}
	switch n.OpType {
	case "Conv":
		return conv2d(get(0), get(1), get(2), n, s.workers)
	case "Relu":
		return relu(get(0))
	case "MaxPool":
		return maxpool(get(0), n, s.workers)
	case "Flatten":
		return flatten(get(0), int(n.Int("axis", 1)))
	case "Gemm":
		return gemm(get(0), get(1), get(2), n, s.workers)
	case "Identity":
		return get(0), nil
	case "Dropout":
		return get(0), nil // 推理阶段等价于恒等
	default:
		return nil, fmt.Errorf("不支持的算子 %q", n.OpType)
	}
}

func dimsOf(t *Tensor) []int64 {
	if t == nil {
		return nil
	}
	return t.Dims
}
