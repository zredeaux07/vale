package lint

import (
	"bytes"
	"os/exec"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/ValeLint/vale/core"
	"github.com/gobwas/glob"
	"github.com/jdkato/regexp"
	"github.com/russross/blackfriday"
	"golang.org/x/net/html"
)

// reStructuredText configuration.
//
// reCodeBlock is used to convert Sphinx-style code directives to the regular
// `::` for rst2html.
var reCodeBlock = regexp.MustCompile(`.. (?:raw|code(?:-block)?):: (\w+)`)
var rstArgs = []string{
	"--quiet",
	"--halt=5",
	"--report=5",
	"--link-stylesheet",
	"--no-file-insertion",
	"--no-toc-backlinks",
	"--no-footnote-backlinks",
	"--no-section-numbering",
}

// AsciiDoc configuration.
var adocArgs = []string{
	"-s",
	"--quiet",
	"--safe-mode",
	"secure",
	"-",
}

// Blackfriday configuration.
var commonHTMLFlags = 0 | blackfriday.HTML_USE_XHTML
var commonExtensions = 0 |
	blackfriday.EXTENSION_NO_INTRA_EMPHASIS |
	blackfriday.EXTENSION_TABLES |
	blackfriday.EXTENSION_FENCED_CODE
var renderer = blackfriday.HtmlRenderer(commonHTMLFlags, "", "")
var options = blackfriday.Options{Extensions: commonExtensions}
var reFrontMatter = regexp.MustCompile(`^(?s)---\n([^{]+?)\n---`)

// HTML configuration.
var heading = regexp.MustCompile(`^h\d$`)

// skipTags are tags that we don't want to lint.
var skipTags = []string{"script", "style", "pre", "figure"}

// skipClasses are classes that we don't want to lint:
// 	- `problematic` is added by rst2html to processing errors which, in our
// 	  case, could be things like file-insertion URLs.
// 	- `pre` is added by rst2html to code spans.
var skipClasses = []string{"problematic", "pre"}
var inlineTags = []string{
	"b", "big", "i", "small", "abbr", "acronym", "cite", "dfn", "em", "kbd",
	"strong", "a", "br", "img", "span", "sub", "sup", "code", "tt"}
var tagToScope = map[string]string{
	"th": "text.table.header",
	"td": "text.table.cell",
	"li": "text.list",
}

func (l Linter) lintHTMLTokens(f *core.File, ctx string, fsrc []byte, offset int) {
	var txt, attr, raw string
	var tokt html.TokenType
	var tok html.Token
	var inBlock, inline, skip, skipClass bool

	lines := len(f.Lines) + offset
	buf := bytes.NewBufferString("")
	act := bytes.NewBufferString("")

	// queue holds each segment of text we encounter in a block, which we then
	// use to sequentially update our context.
	queue := []string{}

	// tagHistory holds the HTML tags we encounter in a given block -- e.g.,
	// if we see <ul>, <li>, <p>, we'd get tagHistory = [ul li p]. It's reset
	// on every non-inline end tag.
	tagHistory := []string{}

	tokens := html.NewTokenizer(bytes.NewReader(fsrc))

	skipped := []string{"tt", "code"}
	if len(l.Config.IgnoredScopes) > 0 {
		skipped = l.Config.IgnoredScopes
	}

	for {
		tokt = tokens.Next()
		tok = tokens.Token()
		txt = html.UnescapeString(strings.TrimSpace(tok.Data))

		skipClass = core.StringInSlice(attr, skipClasses)
		if tokt == html.ErrorToken {
			break
		} else if tokt == html.StartTagToken && core.StringInSlice(txt, skipTags) {
			inBlock = true
		} else if inBlock && core.StringInSlice(txt, skipTags) {
			skip, inBlock = false, false
		} else if tokt == html.StartTagToken {
			inline = core.StringInSlice(txt, inlineTags)
			// skip = (txt == "tt" || txt == "code")
			skip = core.StringInSlice(txt, skipped)
			tagHistory = append(tagHistory, txt)
		} else if tokt == html.CommentToken {
			f.UpdateComments(txt)
		} else if tokt == html.TextToken {
			queue = append(queue, txt)
			if !inBlock {
				txt, raw, skip = clean(txt, f.NormedExt, skip, skipClass, inline)
				buf.WriteString(txt)
				act.WriteString(raw)
			}
		}

		if tokt == html.EndTagToken && !core.StringInSlice(txt, inlineTags) {
			content := buf.String()
			actual := act.String()
			if content != "" {
				l.lintScope(f, ctx, content, actual, tagHistory, lines)
			}
			for _, s := range queue {
				ctx = updateCtx(ctx, s, html.TextToken)
			}
			queue = []string{}
			tagHistory = []string{}
			buf.Reset()
			act.Reset()
		}

		attr = getAttribute(tok, "class")
		ctx = clearElements(ctx, tok)
	}

	summary := NewBlock("", f.Summary.String(), "", "summary."+f.RealExt)
	l.lintText(f, summary, lines, 0)
}

func (l Linter) lintScope(f *core.File, ctx, txt, raw string, tags []string, lines int) {
	for _, tag := range tags {
		scope, match := tagToScope[tag]
		if match || heading.MatchString(tag) {
			if match {
				scope = scope + f.RealExt
			} else {
				scope = "text.heading." + tag + f.RealExt
			}
			txt = strings.TrimLeft(txt, " ")
			l.lintText(f, NewBlock(ctx, txt, raw, scope), lines, 0)
			return
		}
	}

	// NOTE: We don't include headings, list items, or table cells (which are
	// processed above) in our Summary content.
	f.Summary.WriteString(raw + " ")
	l.lintProse(f, ctx, txt, raw, lines, 0)
}

func codify(ext, text string) string {
	if ext == ".md" || ext == ".adoc" {
		return "`" + text + "`"
	} else if ext == ".rst" {
		return "``" + text + "``"
	}
	return text
}

func clean(txt, ext string, skip, skipClass, inline bool) (string, string, bool) {
	punct := []string{".", "?", "!", ",", ":", ";"}
	first, _ := utf8.DecodeRuneInString(txt)
	starter := core.StringInSlice(string(first), punct) && !skip
	raw := txt
	if skip || skipClass {
		raw = codify(ext, txt)
		txt, _ = core.Substitute(txt, txt, '*')
		txt = codify(ext, txt)
		skip = false
	}
	if inline && !starter {
		txt = " " + txt
		raw = " " + raw
	}
	return txt, raw, skip
}

func getAttribute(tok html.Token, key string) string {
	for _, attr := range tok.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func clearElements(ctx string, tok html.Token) string {
	if tok.Data == "img" || tok.Data == "a" || tok.Data == "p" {
		for _, a := range tok.Attr {
			if a.Key == "alt" || a.Key == "href" || a.Key == "id" {
				ctx = updateCtx(ctx, a.Val, html.TextToken)
			}
		}
	}
	return ctx
}

func updateCtx(ctx string, txt string, tokt html.TokenType) string {
	var found bool
	if (tokt == html.TextToken || tokt == html.CommentToken) && txt != "" {
		for _, s := range strings.Split(txt, "\n") {
			ctx, found = core.Substitute(ctx, s, '@')
			if !found {
				for _, w := range strings.Fields(s) {
					ctx, _ = core.Substitute(ctx, w, '@')
				}
			}
		}
	}
	return ctx
}

func (l Linter) lintHTML(f *core.File) {
	l.lintHTMLTokens(f, f.Content, []byte(f.Content), 0)
}

func (l Linter) lintMarkdown(f *core.File) {
	s := reFrontMatter.ReplaceAllString(f.Content, "```\n$1\n```")
	for syntax, regexes := range l.Config.IgnorePatterns {
		sec, err := glob.Compile(syntax)
		if core.CheckError(err) && sec.Match(".md") {
			for _, r := range regexes {
				pat, err := regexp.Compile(r)
				if err == nil {
					s = pat.ReplaceAllString(s, "\n```\n$1\n```\n")
				}
			}
		}
	}
	html := blackfriday.MarkdownOptions([]byte(s), renderer, options)
	l.lintHTMLTokens(f, f.Content, html, 0)
}

func (l Linter) lintRST(f *core.File, python string, rst2html string) {
	var out bytes.Buffer
	var cmd *exec.Cmd

	if runtime.GOOS == "windows" {
		// rst2html is executable by default on Windows.
		cmd = exec.Command(python, append([]string{rst2html}, rstArgs...)...)
	} else {
		cmd = exec.Command(rst2html, rstArgs...)
	}

	cmd.Stdin = strings.NewReader(reCodeBlock.ReplaceAllString(f.Content, "::"))
	cmd.Stdout = &out

	if core.CheckError(cmd.Run()) {
		html := bytes.Replace(out.Bytes(), []byte("\r"), []byte(""), -1)
		bodyStart := bytes.Index(html, []byte("<body>\n"))
		if bodyStart < 0 {
			bodyStart = -7
		}
		bodyEnd := bytes.Index(html, []byte("\n</body>"))
		if bodyEnd < 0 || bodyEnd >= len(html) {
			bodyEnd = len(html) - 1
			if bodyEnd < 0 {
				bodyEnd = 0
			}
		}
		l.lintHTMLTokens(f, f.Content, html[bodyStart+7:bodyEnd], 0)
	}
}

func (l Linter) lintADoc(f *core.File, asciidoctor string) {
	var out bytes.Buffer
	cmd := exec.Command(asciidoctor, adocArgs...)
	cmd.Stdin = strings.NewReader(f.Content)
	cmd.Stdout = &out
	if core.CheckError(cmd.Run()) {
		l.lintHTMLTokens(f, f.Content, out.Bytes(), 0)
	}
}
