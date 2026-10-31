package main

// 这个文件是整个仓库的架构门禁，不是业务测试。
//
// 分层的目标依赖方向（单向）：
//
//	main -> web -> engine -> protocol
//
// 规则只能逐步收紧：protocol 里还留着若干历史遗留的打印与重试逻辑，
// 等继续净化 protocol 后再在这里增加「protocol 不得 import fmt/log/os.Stdout/
// os.Exit/survey/color」与「protocol 不得出现 time.Sleep」两条 AST 规则。

import (
	"os/exec"
	"strings"
	"testing"
)

type importGraph map[string][]string

func loadImportGraph(t *testing.T) importGraph {
	t.Helper()

	const format = `{{.ImportPath}}|{{join .Imports " "}}`
	out, err := exec.Command("go", "list", "-f", format, "./...").Output()
	if err != nil {
		t.Fatalf("go list 失败: %v", err)
	}

	graph := make(importGraph)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		pkg, rest, ok := strings.Cut(line, "|")
		if !ok {
			t.Fatalf("无法解析 go list 输出: %q", line)
		}
		if rest == "" {
			graph[pkg] = nil
			continue
		}
		graph[pkg] = strings.Fields(rest)
	}
	if len(graph) == 0 {
		t.Fatal("go list 没有返回任何包")
	}
	return graph
}

func (g importGraph) imports(pkg, target string) bool {
	for _, imp := range g[pkg] {
		if imp == target {
			return true
		}
	}
	return false
}

// TestDependencyDirection 守住 main -> web -> engine -> protocol 这个单向方向。
func TestDependencyDirection(t *testing.T) {
	graph := loadImportGraph(t)

	// 1) protocol 是地基，engine 只在其上：都不允许向上依赖适配层。
	forbidden := map[string][]string{
		"school_sdk/protocol": {"school_sdk/engine", "school_sdk/web"},
		"school_sdk/engine":   {"school_sdk/web"},
	}
	for pkg := range graph {
		for root, deps := range forbidden {
			if pkg != root && !strings.HasPrefix(pkg, root+"/") {
				continue
			}
			for _, dep := range deps {
				if graph.imports(pkg, dep) {
					t.Errorf("%s 依赖了 %s；%s 不允许反向依赖上层", pkg, dep, root)
				}
			}
		}
	}

	// 2) web 只能被 main 装配，避免出现 web 之间的循环依赖。
	for pkg := range graph {
		if pkg == "school_sdk" {
			continue
		}
		if graph.imports(pkg, "school_sdk/web") {
			t.Errorf("%s 依赖了 school_sdk/web；只有 main 可以装配 web", pkg)
		}
	}

	// 3) 任何库包都不允许 import main（包括 engine）。
	for _, pkg := range []string{"school_sdk/web", "school_sdk/protocol", "school_sdk/engine"} {
		if graph.imports(pkg, "school_sdk") {
			t.Errorf("%s 不允许 import main", pkg)
		}
	}
}
