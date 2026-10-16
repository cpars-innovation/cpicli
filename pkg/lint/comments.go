package lint

// blankComments returns a copy of a Groovy or JavaScript source with its
// comments (// and /* */) replaced by spaces, newlines kept: pattern rules
// then match code only, at the same offsets and line numbers. String
// literals ('…', "…", ”'…”', """…""") are left alone, so "http://" in a
// string is not a comment.
func blankComments(src []byte) []byte {
	out := append([]byte(nil), src...)
	blank := func(from, to int) {
		for i := from; i < to && i < len(out); i++ {
			if out[i] != '\n' && out[i] != '\r' {
				out[i] = ' '
			}
		}
	}
	for i := 0; i < len(src); i++ {
		switch c := src[i]; {
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			end := i
			for end < len(src) && src[end] != '\n' {
				end++
			}
			blank(i, end)
			i = end
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := i + 2
			for end+1 < len(src) && !(src[end] == '*' && src[end+1] == '/') {
				end++
			}
			end = min(end+2, len(src))
			blank(i, end)
			i = end - 1
		case c == '"' || c == '\'':
			q := string([]byte{c, c, c})
			if i+2 < len(src) && string(src[i:i+3]) == q { // triple-quoted, may span lines
				end := i + 3
				for end+2 < len(src) && string(src[end:end+3]) != q {
					end++
				}
				i = min(end+2, len(src)-1)
				continue
			}
			end := i + 1
			for end < len(src) && src[end] != c && src[end] != '\n' {
				if src[end] == '\\' {
					end++
				}
				end++
			}
			i = min(end, len(src)-1)
		}
	}
	return out
}
