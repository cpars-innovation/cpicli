package mask

import (
	"regexp"
	"strings"
)

var (
	// "name": "value" in JSON-like text
	reQuotedPair = regexp.MustCompile(`("([^"\\]*)"\s*:\s*)"((?:[^"\\]|\\.)*)"`)
	// name=value / name: value at the start of a line (properties, YAML,
	// headers); the value is not a call, a variable or a reference
	reLinePair = regexp.MustCompile(`^(\s*(?:-\s+)?["']?([\w.-]+)["']?\s*[=:]\s*)(.*?)\s*$`)
	// <name>value</name> on one line
	reElement  = regexp.MustCompile(`(<((?:[\w.-]+:)?([\w.-]+))(?:\s[^>]*)?>)([^<]+)(</[^>]+>)`)
	rePEMBegin = regexp.MustCompile(`-----BEGIN (?:[A-Z]+ )*PRIVATE KEY(?: BLOCK)?-----`)
	rePEMEnd   = regexp.MustCompile(`-----END (?:[A-Z]+ )*PRIVATE KEY(?: BLOCK)?-----`)
	reNotValue = regexp.MustCompile(`[($]|^\{\{|^\w+\.\w+\(`)
	// name=value / name: value inside a line (log messages); the value is a
	// bare token
	reInlinePair = regexp.MustCompile(`(^|[\s,;(\[{])([\w.-]+)(\s*[=:]\s*)([^\s,;"'()<>{}\[\]]+)`)
	reIdentifier = regexp.MustCompile(`^[A-Za-z_]+$`)
)

// InPlace masks text line by line and keeps its layout: the same number of
// lines, and every line without anything to mask byte for byte. A patch
// made against the masked view therefore still applies to the real text.
// It masks what the detectors find in a line, values under sensitive names
// in one-line forms ("name": "value", name=value at the start of a line,
// <name>value</name>, sensitive attributes) and every line inside a
// private key block. Path rules need structure and are not applied: use
// Text for JSON and XML documents.
func (m *Masker) InPlace(s string) (string, Stats) {
	st := Stats{}
	out := m.inPlace(s, st)
	if st.Total() == 0 {
		return s, st
	}
	return out, st
}

func (m *Masker) inPlace(s string, st Stats) string {
	lines := strings.Split(s, "\n")
	inKey := false
	for i, line := range lines {
		cr := strings.HasSuffix(line, "\r")
		body := strings.TrimSuffix(line, "\r")
		switch {
		case inKey:
			if rePEMEnd.MatchString(body) {
				inKey = false
				break
			}
			if strings.TrimSpace(body) != "" {
				st[DetectPrivateKey]++
				body = leadingSpace(body) + Dots
			}
		case rePEMBegin.MatchString(body) && !rePEMEnd.MatchString(body) && m.has(DetectPrivateKey):
			inKey = true
		default:
			body = m.line(body, st)
		}
		if cr {
			body += "\r"
		}
		lines[i] = body
	}
	return strings.Join(lines, "\n")
}

func (m *Masker) has(detector string) bool {
	for _, d := range m.detectors {
		if d == detector {
			return true
		}
	}
	return false
}

func leadingSpace(s string) string { return s[:len(s)-len(strings.TrimLeft(s, " \t"))] }

// line masks one line: sensitive names first (whole values), then the
// detectors over what is left.
func (m *Masker) line(s string, st Stats) string {
	s = reQuotedPair.ReplaceAllStringFunc(s, func(x string) string {
		p := reQuotedPair.FindStringSubmatch(x)
		if p[3] == "" || p[3] == Dots || !m.Sensitive(p[2]) || ReferenceKey(p[2]) {
			return x
		}
		st[CountName]++
		return p[1] + `"` + Dots + `"`
	})
	s = reElement.ReplaceAllStringFunc(s, func(x string) string {
		p := reElement.FindStringSubmatch(x)
		if strings.TrimSpace(p[4]) == "" || p[4] == Dots || !m.Sensitive(p[3]) {
			return x
		}
		st[CountName]++
		return p[1] + Dots + p[5]
	})
	if strings.Contains(s, "<") && strings.Contains(s, "=") {
		s = attrRE.ReplaceAllStringFunc(s, func(a string) string {
			p := attrRE.FindStringSubmatch(a)
			v := p[3][1 : len(p[3])-1]
			if v == "" || v == Dots || !m.Sensitive(localName(p[1])) || reNotValue.MatchString(v) {
				return a
			}
			st[CountName]++
			return p[1] + p[2] + p[3][:1] + Dots + p[3][:1]
		})
	}
	if p := reLinePair.FindStringSubmatch(s); p != nil && m.Sensitive(p[2]) {
		v := strings.Trim(p[3], `"',;`)
		if v != "" && v != Dots && !reNotValue.MatchString(p[3]) && !strings.Contains(p[3], Dots) && !strings.ContainsAny(p[3], " +") {
			st[CountName]++
			s = p[1] + strings.Replace(p[3], v, Dots, 1)
		}
	}
	s = reInlinePair.ReplaceAllStringFunc(s, func(x string) string {
		p := reInlinePair.FindStringSubmatch(x)
		v := p[4]
		if v == Dots || strings.Contains(v, Dots) || !m.Sensitive(p[2]) || reIdentifier.MatchString(v) || strings.Contains(v, ".") && reIdentifier.MatchString(strings.ReplaceAll(v, ".", "")) {
			return x
		}
		st[CountName]++
		return p[1] + p[2] + p[3] + Dots
	})
	return m.plain(s, st)
}
