package classify

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

func invisible(r rune) bool {
	switch {
	case r >= 0x200B && r <= 0x200F, r >= 0x2060 && r <= 0x2064, r == 0xFEFF,
		r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069,
		r >= 0xE0000 && r <= 0xE007F:
		return true
	}
	return unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Co, r)
}

// StripInvisible removes zero-width, bidi-control, tag and other format or private-use characters.
func StripInvisible(s string) string {
	return strings.Map(func(r rune) rune {
		if invisible(r) {
			return -1
		}
		return r
	}, s)
}

var htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)

// StripHTMLComments removes comments, including an unterminated one, which runs to the end.
func StripHTMLComments(s string) string {
	for {
		next := htmlComment.ReplaceAllString(s, "")
		if next == s {
			break
		}
		s = next
	}
	if i := strings.Index(s, "<!--"); i >= 0 {
		s = s[:i]
	}
	return s
}

// CleanProse prepares author-written text: invisible characters go first so they cannot hide a comment marker.
func CleanProse(s string) string {
	return StripHTMLComments(StripInvisible(s))
}

var sectionTag = regexp.MustCompile(`(?i)<\s*(/?)\s*(pr_title|pr_body|files|diff|tier_file|deterministic_decision)\b`)

// Neutralise stops data from opening or closing one of the prompt's section tags.
func Neutralise(s string) string {
	return sectionTag.ReplaceAllString(s, "&lt;$1$2")
}

// CapDiff cuts a diff to at most max bytes on a line boundary.
func CapDiff(d string, max int) (string, bool) {
	if len(d) <= max {
		return d, false
	}
	cut := d[:max]
	if i := strings.LastIndexByte(cut, '\n'); i >= 0 {
		return cut[:i+1], true
	}
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut, true
}

// ModelString makes text from the model safe to store and render: no control or invisible characters, no backticks.
func ModelString(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || invisible(r) {
			return -1
		}
		return r
	}, s)
	return strings.ReplaceAll(s, "`", "'")
}
