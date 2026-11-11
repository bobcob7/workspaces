package loop

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

// PromptData is what a run's prompt is rendered with besides the loop's
// name and vars: the gate script's trimmed output, and the loop's memory.
type PromptData struct {
	Stdout string
	Stderr string
	Memory map[string]string
}

// RenderPrompt is the prompt a run starts with. A Version1 prompt is
// returned as it is. Otherwise the prompt is parsed with text/template,
// Option("missingkey=error"), and executed with a map holding "Name",
// "Title" (the title, or the name when there is none), "Vars" (a
// map[string]string, never nil), and, only when l.Script != "",
// "Stdout" and "Stderr" from data. A templated prompt also has a "memory"
// function: {{memory "KEY"}} is data.Memory["KEY"], or "" when unset. A
// parse or execute error, or a result that is blank after
// strings.TrimSpace, is ErrInvalid.
func (l *Loop) RenderPrompt(data PromptData) (string, error) {
	return l.render(data, false)
}

// CheckPrompt renders the prompt as RenderPrompt does with empty data,
// for ws loop add, but doesn't refuse a blank result (a prompt that is
// only {{.Stdout}} is blank until the script prints something).
func (l *Loop) CheckPrompt() error {
	_, err := l.render(PromptData{}, true)
	return err
}

func (l *Loop) render(data PromptData, allowBlank bool) (string, error) {
	if !l.Templated() {
		return l.Prompt, nil
	}
	tmpl, err := template.New("prompt").Funcs(template.FuncMap{
		"memory": func(key string) string { return data.Memory[key] },
	}).Option("missingkey=error").Parse(l.Prompt)
	if err != nil {
		return "", fmt.Errorf("%w: prompt: %w", ErrInvalid, err)
	}
	vars := l.Vars
	if vars == nil {
		vars = map[string]string{}
	}
	title := l.Title
	if title == "" {
		title = l.Name
	}
	values := map[string]any{"Name": l.Name, "Title": title, "Vars": vars}
	if l.Script != "" {
		values["Stdout"] = data.Stdout
		values["Stderr"] = data.Stderr
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, values); err != nil {
		return "", fmt.Errorf("%w: prompt: %w", ErrInvalid, err)
	}
	if !allowBlank && strings.TrimSpace(out.String()) == "" {
		return "", fmt.Errorf("%w: prompt renders empty", ErrInvalid)
	}
	return out.String(), nil
}
