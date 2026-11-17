package telegram

import (
	"strings"
	"testing"
)

func TestValidateHTML(t *testing.T) {
	good := []string{
		"",
		"Просто текст",
		`<b>Жирный</b> и <i>курсив</i>, <a href="https://hiearth.ru/help">ссылка</a>`,
		"<blockquote>Цитата <tg-spoiler>спойлер</tg-spoiler></blockquote>",
		"5 &lt; 6 &amp;&amp; 7 &gt; 3 &#127881;",
		"<pre><code>код</code></pre>",
	}
	for _, s := range good {
		if err := ValidateHTML(s); err != nil {
			t.Errorf("%q must be valid: %v", s, err)
		}
	}
	bad := map[string]string{
		"<script>x</script>":                  "не поддерживает",
		"<b>не закрыт":                        "не закрыт",
		"<b><i>x</b></i>":                     "закрывающий",
		"5 < 6":                               "&lt;",
		"a > b":                               "&gt;",
		"Tom & Jerry":                         "&amp;",
		`<a href="javascript:alert(1)">x</a>`: "https://",
		`<b onclick="x">x</b>`:                "атрибута",
		`<a>без ссылки</a>`:                   "нет адреса",
		`<span class="tg-spoiler">x</span>`:   "не поддерживает",
	}
	for s, want := range bad {
		err := ValidateHTML(s)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want an error about %q", s, err, want)
		}
	}
}

func TestStripHTML(t *testing.T) {
	if got := StripHTML(`<b>Новое</b>: вход для &lt;мобильных&gt; <a href="https://x.ru">сетей</a>`); got != "Новое: вход для <мобильных> сетей" {
		t.Fatalf("StripHTML = %q", got)
	}
}
