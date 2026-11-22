// Package version reads and compares the tags releases are made from, such as v0.1.0 and
// v1.0.0-rc.1. tty1 needs to know which of two releases is the newer: to offer an update, and to
// refuse to go back to an older release that was put where the newest should be.
package version

import (
	"fmt"
	"strconv"
	"strings"
)

// V is a release's version: three numbers, and for a pre-release what follows the hyphen.
type V struct {
	Major, Minor, Patch int
	Pre                 []string // empty for a release proper
}

// Parse reads a tag of the form v1.2.3 or v1.2.3-rc.1, as Semantic Versioning writes them. It
// reads nothing looser: a tag tty1 cannot place among the others is one it must not act on.
func Parse(tag string) (V, error) {
	var v V
	rest, ok := strings.CutPrefix(tag, "v")
	if !ok {
		return v, fmt.Errorf("%q is not a version: it does not start with v", tag)
	}
	core, pre, hasPre := strings.Cut(rest, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return v, fmt.Errorf("%q is not a version: it has not three numbers", tag)
	}
	for i, dst := range []*int{&v.Major, &v.Minor, &v.Patch} {
		n, ok := number(parts[i])
		if !ok {
			return V{}, fmt.Errorf("%q is not a version: %q is not a number it can hold", tag, parts[i])
		}
		*dst = n
	}
	if hasPre {
		v.Pre = strings.Split(pre, ".")
		for _, id := range v.Pre {
			if _, isNumber := number(id); !isNumber && (id == "" || strings.Trim(id, "0123456789") == "" || strings.Trim(id, "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ-") != "") {
				return V{}, fmt.Errorf("%q is not a version: %q cannot follow the hyphen", tag, id)
			}
		}
	}
	return v, nil
}

// number reads digits with no leading zero, few enough that they fit an int anywhere.
func number(s string) (int, bool) {
	if s == "" || len(s) > 9 || (len(s) > 1 && s[0] == '0') || strings.Trim(s, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

// Compare returns -1 if v is older than w, 1 if it is newer, and 0 if they are the same release.
// A pre-release is older than the release it leads to, and among pre-releases a number is older
// than a word and fewer parts are older than more, as Semantic Versioning orders them.
func (v V) Compare(w V) int {
	for _, pair := range [][2]int{{v.Major, w.Major}, {v.Minor, w.Minor}, {v.Patch, w.Patch}} {
		if pair[0] != pair[1] {
			return sign(pair[0] - pair[1])
		}
	}
	switch {
	case len(v.Pre) == 0 && len(w.Pre) == 0:
		return 0
	case len(v.Pre) == 0:
		return 1
	case len(w.Pre) == 0:
		return -1
	}
	for i := 0; i < len(v.Pre) && i < len(w.Pre); i++ {
		a, b := v.Pre[i], w.Pre[i]
		an, aIsNumber := number(a)
		bn, bIsNumber := number(b)
		switch {
		case aIsNumber && bIsNumber:
			if an != bn {
				return sign(an - bn)
			}
		case aIsNumber:
			return -1
		case bIsNumber:
			return 1
		case a != b:
			return strings.Compare(a, b)
		}
	}
	return sign(len(v.Pre) - len(w.Pre))
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

// String writes the version as the tag it was read from.
func (v V) String() string {
	s := fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch)
	if len(v.Pre) > 0 {
		s += "-" + strings.Join(v.Pre, ".")
	}
	return s
}
