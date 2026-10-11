package content

import (
	"fmt"
	"strings"
)

// maxDiffCells bounds the work of UnifiedDiff (lines of a × lines of b).
const maxDiffCells = 4_000_000

// UnifiedDiff returns a unified diff of two texts with context lines
// around each change ("" when they are equal). ok is false when the texts
// are too large to diff.
func UnifiedDiff(a, b, nameA, nameB string, context int) (diff string, ok bool) {
	if a == b {
		return "", true
	}
	la, lb := splitLines(a), splitLines(b)
	if len(la)*len(lb) > maxDiffCells {
		return "", false
	}
	// longest common subsequence, from the end
	n, m := len(la), len(lb)
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if la[i] == lb[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	type op struct {
		kind byte // ' ', '-', '+'
		text string
		ai   int // line number in a (1-based) of ' ' and '-'
		bi   int // line number in b of ' ' and '+'
	}
	var ops []op
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && la[i] == lb[j]:
			ops = append(ops, op{' ', la[i], i + 1, j + 1})
			i, j = i+1, j+1
		case i < n && (j == m || lcs[i+1][j] >= lcs[i][j+1]):
			ops = append(ops, op{'-', la[i], i + 1, j})
			i++
		default:
			ops = append(ops, op{'+', lb[j], i, j + 1})
			j++
		}
	}
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", nameA, nameB)
	for k := 0; k < len(ops); {
		if ops[k].kind == ' ' {
			k++
			continue
		}
		// a hunk: from context lines before the change to context lines after
		// the last change that is at most 2*context lines away
		start := max(0, k-context)
		end := k
		for end < len(ops) {
			if ops[end].kind != ' ' {
				end++
				continue
			}
			next := end
			for next < len(ops) && ops[next].kind == ' ' {
				next++
			}
			if next == len(ops) || next-end > 2*context {
				break
			}
			end = next
		}
		stop := min(len(ops), end+context)
		aStart, bStart, aLen, bLen := 0, 0, 0, 0
		for _, o := range ops[start:stop] {
			if o.kind != '+' {
				if aStart == 0 {
					aStart = o.ai
				}
				aLen++
			}
			if o.kind != '-' {
				if bStart == 0 {
					bStart = o.bi
				}
				bLen++
			}
		}
		if aStart == 0 {
			aStart = ops[start].ai
		}
		if bStart == 0 {
			bStart = ops[start].bi
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", aStart, aLen, bStart, bLen)
		for _, o := range ops[start:stop] {
			out.WriteByte(o.kind)
			out.WriteString(o.text)
			out.WriteByte('\n')
		}
		k = stop
	}
	return out.String(), true
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}
