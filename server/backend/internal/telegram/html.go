package telegram

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

// The HTML subset of parse_mode=HTML (core.telegram.org/bots/api#html-style) that news may use.
var htmlTags = map[string]bool{
	"b": true, "strong": true, "i": true, "em": true, "u": true, "ins": true, "s": true, "strike": true,
	"del": true, "a": true, "code": true, "pre": true, "blockquote": true, "tg-spoiler": true,
}

var (
	htmlTagRe    = regexp.MustCompile(`^<(/?)([a-z-]+)((?:\s+[a-z-]+="[^"<>]*")*)\s*>`)
	htmlAttrRe   = regexp.MustCompile(`\s+([a-z-]+)="([^"<>]*)"`)
	htmlEntityRe = regexp.MustCompile(`^&(lt|gt|amp|quot|#[0-9]{1,6}|#x[0-9a-fA-F]{1,6});`)
)

// ValidateHTML checks that s uses only Telegram's HTML subset, with balanced tags, links to
// http(s)/tg URLs, and every «<», «>», «&» of the text escaped. The error says what to fix.
func ValidateHTML(s string) error {
	var stack []string
	for i := 0; i < len(s); {
		switch s[i] {
		case '<':
			m := htmlTagRe.FindStringSubmatch(s[i:])
			if m == nil {
				return fmt.Errorf("непонятный тег около «%s» — символ «<» в тексте пишется как &lt;", snippet(s[i:]))
			}
			closing, name, attrs := m[1] == "/", m[2], m[3]
			if !htmlTags[name] {
				return fmt.Errorf("тег <%s> Telegram не поддерживает: можно b, i, u, s, a, code, pre, blockquote, tg-spoiler", name)
			}
			if closing {
				if len(stack) == 0 || stack[len(stack)-1] != name {
					return fmt.Errorf("лишний или не по порядку закрывающий тег </%s>", name)
				}
				stack = stack[:len(stack)-1]
			} else {
				for _, a := range htmlAttrRe.FindAllStringSubmatch(attrs, -1) {
					if name != "a" || a[1] != "href" {
						return fmt.Errorf("у <%s> не может быть атрибута %s", name, a[1])
					}
					href := strings.ToLower(html.UnescapeString(a[2]))
					if !strings.HasPrefix(href, "https://") && !strings.HasPrefix(href, "http://") && !strings.HasPrefix(href, "tg://") {
						return fmt.Errorf("ссылка в <a href> должна начинаться с https://")
					}
				}
				if name == "a" && !strings.Contains(attrs, "href=") {
					return fmt.Errorf("у <a> нет адреса: <a href=\"https://…\">")
				}
				stack = append(stack, name)
			}
			i += len(m[0])
		case '>':
			return fmt.Errorf("символ «>» в тексте пишется как &gt;")
		case '&':
			m := htmlEntityRe.FindString(s[i:])
			if m == "" {
				return fmt.Errorf("символ «&» в тексте пишется как &amp; (около «%s»)", snippet(s[i:]))
			}
			i += len(m)
		default:
			i++
		}
	}
	if len(stack) > 0 {
		return fmt.Errorf("не закрыт тег <%s>", stack[len(stack)-1])
	}
	return nil
}

// StripHTML turns the subset into plain text (Happ's announce line, previews).
func StripHTML(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '<' {
			if m := htmlTagRe.FindString(s[i:]); m != "" {
				i += len(m)
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return html.UnescapeString(b.String())
}

func snippet(s string) string {
	r := []rune(s)
	if len(r) > 16 {
		r = r[:16]
	}
	return string(r)
}
