package core

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

// IniFile is an order- and comment-preserving INI document.
//
// The SimpleX servers read their configuration with Haskell's Data.Ini,
// which is case-insensitive for sections and keys and accepts both
// "key = value" and "key: value" assignments. We preserve the original
// text (comments, order, key style) and only rewrite the lines we touch.
type IniFile struct {
	path    string
	section string // current section while parsing
	lines   []iniLine
}

type iniLine struct {
	raw     string // original text without trailing newline
	section string // section the line belongs to ("" = before first section)
	key     string // lowercased key name when the line is an assignment
}

// LoadIni reads an INI file.
func LoadIni(path string) (*IniFile, error) {
	f := &IniFile{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		sec, isSection := parseSection(line)
		if isSection {
			f.section = strings.ToLower(sec)
		}
		key, _ := parseKey(line)
		f.lines = append(f.lines, iniLine{raw: line, section: f.section, key: key})
	}
	return f, sc.Err()
}

// NewIni creates an empty INI rooted at path (for generating configs).
func NewIni(path string) *IniFile { return &IniFile{path: path} }

func parseSection(line string) (name string, ok bool) {
	t := strings.TrimSpace(line)
	if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
		return strings.TrimSpace(t[1 : len(t)-1]), true
	}
	return "", false
}

// parseKey returns the lowercased assignment key and whether the line is
// an active (uncommented) assignment.
func parseKey(line string) (key string, active bool) {
	t := strings.TrimSpace(line)
	if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, ";") ||
		strings.HasPrefix(t, "[") {
		return "", false
	}
	for _, sep := range []string{"=", ":"} {
		if i := strings.Index(t, sep); i > 0 {
			return strings.ToLower(strings.TrimSpace(t[:i])), true
		}
	}
	return "", false
}

// commentedKeyOf returns the key when the line is a commented assignment
// like "# control_port = 5226".
func commentedKeyOf(line string) (key string, ok bool) {
	t := strings.TrimSpace(line)
	for _, p := range []string{"#", ";"} {
		if strings.HasPrefix(t, p) {
			rest := strings.TrimSpace(t[len(p):])
			if k, active := parseKey(rest); active {
				return k, true
			}
			return "", false
		}
	}
	return "", false
}

// Get returns the raw value of an active assignment.
func (f *IniFile) Get(section, key string) (string, bool) {
	sec, k := strings.ToLower(section), strings.ToLower(key)
	for _, l := range f.lines {
		if l.section == sec && l.key == k {
			return valueOf(l.raw), true
		}
	}
	return "", false
}

func valueOf(raw string) string {
	t := strings.TrimSpace(raw)
	for _, sep := range []string{"=", ":"} {
		if i := strings.Index(t, sep); i >= 0 {
			return strings.TrimSpace(t[i+len(sep):])
		}
	}
	return ""
}

// Set updates an active assignment, enables a commented one, or appends
// a new "key = value" line to the section (creating the section if needed).
// It returns true when the document changed.
func (f *IniFile) Set(section, key, value string) bool {
	sec, k := strings.ToLower(section), strings.ToLower(key)
	// 1. active assignment exists -> rewrite preserving style
	for i := range f.lines {
		if f.lines[i].section == sec && f.lines[i].key == k {
			f.lines[i].raw = replaceValue(f.lines[i].raw, value)
			return true
		}
	}
	// 2. commented assignment exists -> enable with new value
	for i := range f.lines {
		if ck, ok := commentedKeyOf(f.lines[i].raw); ok && ck == k && f.lines[i].section == sec {
			f.lines[i].raw = key + " = " + value
			f.lines[i].key = k
			return true
		}
	}
	// 3. append into section
	idx := f.lastLineOfSection(sec)
	if idx < 0 {
		f.appendSection(sec)
		idx = len(f.lines) - 1
	}
	ins := iniLine{raw: key + " = " + value, section: sec, key: k}
	f.lines = append(f.lines, iniLine{})
	copy(f.lines[idx+1:], f.lines[idx:])
	f.lines[idx+1] = ins
	return true
}

func replaceValue(raw, value string) string {
	t := strings.TrimSpace(raw)
	lead := raw[:len(raw)-len(strings.TrimLeft(raw, " \t"))]
	for _, sep := range []string{"=", ":"} {
		if i := strings.Index(t, sep); i >= 0 {
			key := strings.TrimSpace(t[:i])
			return lead + key + " = " + value
		}
	}
	return lead + t
}

func (f *IniFile) lastLineOfSection(sec string) int {
	last := -1
	for i, l := range f.lines {
		if l.section == sec {
			last = i
		}
	}
	return last
}

func (f *IniFile) appendSection(sec string) {
	// trim trailing blank lines
	n := len(f.lines)
	for n > 0 && strings.TrimSpace(f.lines[n-1].raw) == "" {
		n--
	}
	f.lines = f.lines[:n]
	f.lines = append(f.lines,
		iniLine{raw: ""},
		iniLine{raw: "[" + strings.ToUpper(sec) + "]", section: sec},
	)
}

// Disable comments out an assignment (active or already commented).
func (f *IniFile) Disable(section, key string) bool {
	sec, k := strings.ToLower(section), strings.ToLower(key)
	changed := false
	for i := range f.lines {
		if f.lines[i].section == sec && f.lines[i].key == k {
			f.lines[i].raw = replaceValue(f.lines[i].raw, valueOf(f.lines[i].raw))
			f.lines[i].raw = "# " + f.lines[i].raw
			f.lines[i].key = ""
			changed = true
		}
	}
	return changed
}

// Sections lists section names in order of appearance.
func (f *IniFile) Sections() []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range f.lines {
		if name, ok := parseSection(l.raw); ok {
			lower := strings.ToLower(name)
			if !seen[lower] {
				seen[lower] = true
				out = append(out, name)
			}
		}
	}
	return out
}

// Keys lists active keys of a section.
func (f *IniFile) Keys(section string) []string {
	sec := strings.ToLower(section)
	var out []string
	for _, l := range f.lines {
		if l.section == sec && l.key != "" {
			out = append(out, l.key)
		}
	}
	return out
}

// Text renders the document.
func (f *IniFile) Text() string {
	var b strings.Builder
	for _, l := range f.lines {
		b.WriteString(l.raw)
		b.WriteByte('\n')
	}
	return b.String()
}

// Save writes the document atomically.
func (f *IniFile) Save() error {
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return err
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(f.Text()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.path)
}

// Path returns the file path.
func (f *IniFile) Path() string { return f.path }
