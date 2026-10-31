package onnx

import (
	"fmt"
	"math"
	"sync"
)

// parallelFor 把 [0,n) 均分给最多 workers 个 goroutine 执行。
func parallelFor(n, workers int, fn func(i int)) {
	if n <= 0 {
		return
	}
	if workers > n {
		workers = n
	}
	if workers <= 1 {
		for i := range n {
			fn(i)
		}
		return
	}
	chunk := (n + workers - 1) / workers
	var wg sync.WaitGroup
	for lo := 0; lo < n; lo += chunk {
		hi := min(lo+chunk, n)
		wg.Add(1)
		go func(lo, hi int) {
			defer wg.Done()
			for i := lo; i < hi; i++ {
				fn(i)
			}
		}(lo, hi)
	}
	wg.Wait()
}

// im2col 把输入 (c,h,w) 按卷积窗口展开成 (c*kh*kw, ho*wo)，越界处保持 0。
func im2col(dst, src []float32, c, h, w, kh, kw, sh, sw, ph, pw, ho, wo int) {
	for ic := range c {
		splane := src[ic*h*w : (ic+1)*h*w]
		base := ic * kh * kw
		for ky := range kh {
			for kx := range kw {
				row := dst[(base+ky*kw+kx)*ho*wo:]
				for oy := range ho {
					iy := oy*sh - ph + ky
					out := row[oy*wo:]
					if iy < 0 || iy >= h {
						continue
					}
					in := splane[iy*w:]
					if sw != 1 {
						for ox := range wo {
							if ix := ox*sw - pw + kx; ix >= 0 && ix < w {
								out[ox] = in[ix]
							}
						}
						continue
					}
					// sw == 1 时列索引连续，只有两端可能越界。
					first := -pw + kx // ox=0 时对应的输入列
					for ox := range wo {
						ix := first + ox
						if ix < 0 {
							continue
						}
						if ix >= w {
							break
						}
						out[ox] = in[ix]
					}
				}
			}
		}
	}
}

// gemmAB 计算 out(m×n) = a(m×k) · b(k×n) + bias，b 按行主序存放。
//
// 卷积展开后的矩阵很大（几百 MB/s 的流量），所以按 4 行输出分块：同一行 b 只读一次，
// 供 4 个输出通道复用，实际内存带宽大约降到 1/4。
func gemmAB(out, a, b []float32, m, k, n int, bias []float32, workers int) {
	const rows = 4
	blocks := (m + rows - 1) / rows
	parallelFor(blocks, workers, func(bi int) {
		i0 := bi * rows
		i1 := min(i0+rows, m)
		for i := i0; i < i1; i++ {
			o := out[i*n : (i+1)*n]
			if bias != nil {
				v := bias[i]
				for j := range o {
					o[j] = v
				}
			} else {
				for j := range o {
					o[j] = 0
				}
			}
		}
		for p := range k {
			br := b[p*n : (p+1)*n]
			for i := i0; i < i1; i++ {
				av := a[i*k+p]
				if av == 0 {
					continue // im2col 的 padding 有大量 0
				}
				o := out[i*n : (i+1)*n]
				j := 0
				for ; j+4 <= n; j += 4 {
					o[j] += av * br[j]
					o[j+1] += av * br[j+1]
					o[j+2] += av * br[j+2]
					o[j+3] += av * br[j+3]
				}
				for ; j < n; j++ {
					o[j] += av * br[j]
				}
			}
		}
	})
}

func dims4(t *Tensor, what string) (int, int, int, int, error) {
	if len(t.Dims) != 4 {
		return 0, 0, 0, 0, fmt.Errorf("%s 需要 4 维，实际是 %v", what, t.Dims)
	}
	return int(t.Dims[0]), int(t.Dims[1]), int(t.Dims[2]), int(t.Dims[3]), nil
}

// conv2d 实现 ONNX 的 Conv（group 只支持 1，本模型的卷积都是普通卷积）。
func conv2d(x, w, b *Tensor, n *Node, workers int) (*Tensor, error) {
	nb, c, h, wd, err := dims4(x, "Conv 输入")
	if err != nil {
		return nil, err
	}
	m, cg, kh, kw, err := dims4(w, "Conv 权重")
	if err != nil {
		return nil, err
	}
	group := int(n.Int("group", 1))
	if group != 1 {
		return nil, fmt.Errorf("Conv 只支持 group=1，实际 %d", group)
	}
	if cg != c {
		return nil, fmt.Errorf("Conv 权重通道 %d 与输入通道 %d 不符", cg, c)
	}
	strides := n.Ints("strides", 1, 1)
	dil := n.Ints("dilations", 1, 1)
	pads := n.Ints("pads", 0, 0, 0, 0)
	if len(strides) != 2 || len(dil) != 2 || len(pads) != 4 {
		return nil, fmt.Errorf("Conv 属性长度不合法: strides=%v dilations=%v pads=%v", strides, dil, pads)
	}
	sh, sw := int(strides[0]), int(strides[1])
	dh, dw := int(dil[0]), int(dil[1])
	ph, pw, ph2, pw2 := int(pads[0]), int(pads[1]), int(pads[2]), int(pads[3])
	if dh != 1 || dw != 1 {
		return nil, fmt.Errorf("Conv 暂不支持 dilation != 1")
	}
	ho := (h+ph+ph2-(kh-1)*dh-1)/sh + 1
	wo := (wd+pw+pw2-(kw-1)*dw-1)/sw + 1

	var bias []float32
	if b != nil {
		bias = b.Floats
		if len(bias) != m {
			return nil, fmt.Errorf("Conv 偏置长度 %d 与输出通道 %d 不符", len(bias), m)
		}
	}
	wmat, err := w.Float32()
	if err != nil {
		return nil, err
	}
	xf, err := x.Float32()
	if err != nil {
		return nil, err
	}

	k := c * kh * kw
	plane := ho * wo
	out := &Tensor{DataType: DataTypeFloat32, Dims: []int64{int64(nb), int64(m), int64(ho), int64(wo)}}
	out.Floats = make([]float32, nb*m*plane)
	col := make([]float32, k*plane)
	for bi := range nb {
		if bi > 0 {
			// im2col 只写有效区域，padding 位置沿用上一批的残留值，必须先清零。
			for i := range col {
				col[i] = 0
			}
		}
		im2col(col, xf[bi*c*h*wd:(bi+1)*c*h*wd], c, h, wd, kh, kw, sh, sw, ph, pw, ho, wo)
		gemmAB(out.Floats[bi*m*plane:(bi+1)*m*plane], wmat, col, m, k, plane, bias, workers)
	}
	return out, nil
}

// relu 逐元素取 max(0)。
func relu(x *Tensor) (*Tensor, error) {
	xf, err := x.Float32()
	if err != nil {
		return nil, err
	}
	out := &Tensor{DataType: DataTypeFloat32, Dims: append([]int64(nil), x.Dims...)}
	out.Floats = make([]float32, len(xf))
	for i, v := range xf {
		if v > 0 {
			out.Floats[i] = v
		}
	}
	return out, nil
}

// maxpool 实现 ONNX 的 MaxPool（只支持二维、无 dilation）。
func maxpool(x *Tensor, n *Node, workers int) (*Tensor, error) {
	nb, c, h, wd, err := dims4(x, "MaxPool 输入")
	if err != nil {
		return nil, err
	}
	ks := n.Ints("kernel_shape")
	if len(ks) != 2 {
		return nil, fmt.Errorf("MaxPool 需要 kernel_shape，实际 %v", ks)
	}
	kh, kw := int(ks[0]), int(ks[1])
	strides := n.Ints("strides", ks[0], ks[1])
	sh, sw := int(strides[0]), int(strides[1])
	pads := n.Ints("pads", 0, 0, 0, 0)
	ph, pw := int(pads[0]), int(pads[1])
	ho := (h+ph+int(pads[2])-kh)/sh + 1
	wo := (wd+pw+int(pads[3])-kw)/sw + 1
	xf, err := x.Float32()
	if err != nil {
		return nil, err
	}
	out := &Tensor{DataType: DataTypeFloat32, Dims: []int64{int64(nb), int64(c), int64(ho), int64(wo)}}
	out.Floats = make([]float32, nb*c*ho*wo)
	parallelFor(nb*c, workers, func(idx int) {
		src := xf[idx*h*wd : (idx+1)*h*wd]
		dst := out.Floats[idx*ho*wo : (idx+1)*ho*wo]
		for oy := range ho {
			for ox := range wo {
				best := float32(math.Inf(-1))
				for ky := range kh {
					iy := oy*sh - ph + ky
					if iy < 0 || iy >= h {
						continue
					}
					row := src[iy*wd:]
					for kx := range kw {
						ix := ox*sw - pw + kx
						if ix < 0 || ix >= wd {
							continue
						}
						if v := row[ix]; v > best {
							best = v
						}
					}
				}
				if best == float32(math.Inf(-1)) {
					best = 0 // 整个窗口都在 padding 里，ONNX 语义是 0
				}
				dst[oy*wo+ox] = best
			}
		}
	})
	return out, nil
}

// flatten 把 axis 及其之后的维度合并。
func flatten(x *Tensor, axis int) (*Tensor, error) {
	if axis < 0 {
		axis += len(x.Dims)
	}
	if axis < 0 || axis > len(x.Dims) {
		return nil, fmt.Errorf("Flatten 的 axis=%d 超出维度 %v", axis, x.Dims)
	}
	var outer, inner int64 = 1, 1
	for _, d := range x.Dims[:axis] {
		outer *= d
	}
	for _, d := range x.Dims[axis:] {
		inner *= d
	}
	xf, err := x.Float32()
	if err != nil {
		return nil, err
	}
	out := &Tensor{DataType: DataTypeFloat32, Dims: []int64{outer, inner}, Floats: xf}
	return out, nil
}

// gemm 实现 ONNX 的 Gemm：Y = alpha * A' * B' + beta * C。
func gemm(a, bb, cm *Tensor, n *Node, workers int) (*Tensor, error) {
	if len(a.Dims) != 2 || len(bb.Dims) != 2 {
		return nil, fmt.Errorf("Gemm 只支持二维输入，实际 A=%v B=%v", a.Dims, bb.Dims)
	}
	transA := n.Int("transA", 0) != 0
	transB := n.Int("transB", 0) != 0
	alpha := n.Float("alpha", 1)
	beta := n.Float("beta", 1)

	m, k := int(a.Dims[0]), int(a.Dims[1])
	if transA {
		m, k = k, m
	}
	kb, nn := int(bb.Dims[0]), int(bb.Dims[1])
	if transB {
		kb, nn = nn, kb
	}
	if kb != k {
		return nil, fmt.Errorf("Gemm 维度不匹配：A=%v(transA=%v) B=%v(transB=%v)", a.Dims, transA, bb.Dims, transB)
	}
	af, err := a.Float32()
	if err != nil {
		return nil, err
	}
	bf, err := bb.Float32()
	if err != nil {
		return nil, err
	}
	out := &Tensor{DataType: DataTypeFloat32, Dims: []int64{int64(m), int64(nn)}}
	out.Floats = make([]float32, m*nn)

	// 把 A'、B' 的行访问统一成函数，避免为转置单独复制整块内存。
	aval := func(i, p int) float32 {
		if transA {
			return af[p*int(a.Dims[1])+i]
		}
		return af[i*k+p]
	}
	bvalRow := func(p int) []float32 {
		if !transB {
			// B 是 (k, nn)，第 p 行在内存里连续。
			return bf[p*nn : (p+1)*nn]
		}
		// B 是 (nn, k)，取它的第 p 列，需要跨行跳跃。
		return nil
	}
	parallelFor(m, workers, func(i int) {
		o := out.Floats[i*nn : (i+1)*nn]
		for p := 0; p < k; p++ {
			av := aval(i, p) * alpha
			if av == 0 {
				continue
			}
			if row := bvalRow(p); row != nil {
				for j := 0; j < nn; j++ {
					o[j] += av * row[j]
				}
				continue
			}
			for j := 0; j < nn; j++ {
				o[j] += av * bf[j*k+p]
			}
		}
	})

	if cm != nil && beta != 0 {
		cf, err := cm.Float32()
		if err != nil {
			return nil, err
		}
		if len(cf) != m*nn {
			return nil, fmt.Errorf("Gemm 的 C 长度 %d 与输出 %d×%d 不符", len(cf), m, nn)
		}
		for i := range out.Floats {
			out.Floats[i] += beta * cf[i]
		}
	}
	return out, nil
}
