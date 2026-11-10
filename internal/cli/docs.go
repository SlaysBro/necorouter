package cli

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"

	"necorouter/internal/config"
)

// DocNode is one command as the documentation embeds it: the word, its help, its children,
// whether it takes an argument (A) and whether it can be negated with "no" (Neg).
type DocNode struct {
	W   string    `json:"w"`
	H   string    `json:"h,omitempty"`
	K   []DocNode `json:"k,omitempty"`
	A   int       `json:"a,omitempty"`
	Neg bool      `json:"neg,omitempty"`
}

// DocTreeJSON returns the command tree of every mode as the JSON that docs/index.html embeds.
// Hidden commands are left out. A command is listed in the modes it is offered in (roles are described
// in the text, not per command).
func DocTreeJSON() ([]byte, error) {
	out := map[string][]DocNode{}
	for kind, nodes := range trees {
		for _, n := range nodes {
			if !n.Hidden && n.offeredIn(kind) {
				out[kind] = append(out[kind], docNodeOf(n, kind))
			}
		}
	}
	return json.Marshal(out)
}

func docNodeOf(n *Node, mode string) DocNode {
	d := DocNode{W: n.Word, H: n.Help, Neg: n.Neg != nil}
	if n.Arg != 0 {
		d.A = 1
	}
	for _, k := range n.Kids {
		if !k.Hidden && k.offeredIn(mode) {
			d.K = append(d.K, docNodeOf(k, mode))
		}
	}
	return d
}

// accessLabels are the words of the generated access table, per language.
var accessLabels = map[string]struct{ intro, cmd, feat string }{
	"en": {"Who may use which command, generated from the command tree (the same rule the CLI applies). A star marks a command that also needs a feature to be on.", "Command", "needs a feature"},
	"ru": {"Кто какой командой может пользоваться: таблица строится из дерева команд (по тому же правилу, что применяет CLI). Звёздочка означает, что для команды ещё нужно включить функцию.", "Команда", "нужна функция"},
}

var accessModeNames = map[string]map[string]string{
	"en": {modeExec: "User mode (>)", modePriv: "Privileged mode (#)"},
	"ru": {modeExec: "Пользовательский режим (>)", modePriv: "Привилегированный режим (#)"},
}

// DocAccessHTML returns the access table of the documentation: for the user and the privileged mode, each
// top-level command and each command that has its own role rule, with a plus for every role that may use it.
func DocAccessHTML(lang string) string {
	lb, ok := accessLabels[lang]
	if !ok {
		lb = accessLabels["en"]
		lang = "en"
	}
	var b strings.Builder
	b.WriteString("<p>" + html.EscapeString(lb.intro) + "</p><div class=\"scroll\"><table><thead><tr><th>" + html.EscapeString(lb.cmd) + "</th>")
	for _, r := range config.Roles {
		b.WriteString("<th>" + r + "</th>")
	}
	b.WriteString("</tr></thead><tbody>")
	for _, mode := range []string{modeExec, modePriv} {
		b.WriteString("<tr><th colspan=\"" + fmt.Sprint(len(config.Roles)+1) + "\">" + html.EscapeString(accessModeNames[lang][mode]) + "</th></tr>")
		for _, n := range trees[mode] {
			writeAccessRows(&b, n, nil, 0, mode)
		}
	}
	b.WriteString("</tbody></table></div>")
	return b.String()
}

func writeAccessRows(b *strings.Builder, n *Node, path []string, depth int, mode string) {
	if n.Hidden || n.Arg != 0 || !n.offeredIn(mode) {
		return
	}
	path = append(append([]string{}, path...), n.Word)
	if depth == 0 || n.access != nil {
		name := strings.Join(path, " ")
		if n.Gate != nil {
			name += " *"
		}
		b.WriteString("<tr><td><code>" + html.EscapeString(name) + "</code></td>")
		for _, r := range config.Roles {
			if n.visibleIn(mode, r) {
				b.WriteString("<td>+</td>")
			} else {
				b.WriteString("<td></td>")
			}
		}
		b.WriteString("</tr>")
	}
	for _, k := range n.Kids {
		writeAccessRows(b, k, path, depth+1, mode)
	}
}

// ReplaceAccessBlock rewrites the generated access table of one language in the page (between its markers).
func ReplaceAccessBlock(page []byte, lang string) ([]byte, error) {
	open, shut := "<!--access-table:"+lang+"-->", "<!--/access-table:"+lang+"-->"
	s := string(page)
	i := strings.Index(s, open)
	j := strings.Index(s, shut)
	if i < 0 || j < i || strings.Count(s, open) != 1 {
		return nil, fmt.Errorf("the access-table:%s markers are missing or not unique", lang)
	}
	return []byte(s[:i+len(open)] + DocAccessHTML(lang) + s[j:]), nil
}
