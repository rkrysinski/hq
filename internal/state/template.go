package state

import (
	"bytes"
	"encoding/json"
	"regexp"
)

// A hook of the sandbox's template that answers AskUserQuestion by itself
// keeps Claude from taking hq's answer (ADR 0013): Claude runs the
// PermissionRequest hooks side by side, takes the first decisive result,
// and for AskUserQuestion reads a bare allow as no decision and stops
// there. hq answer runs the template's hooks once with the dialog's payload
// to know, before it leaves an answer that would be ignored (#128).

// AskHook is a command hook the managed settings run on a PermissionRequest
// of AskUserQuestion.
type AskHook struct {
	Command string
	Args    []string
}

// Argv is how Claude runs the hook: its command line through the shell,
// or the command with its arguments when it has some.
func (h AskHook) Argv() []string {
	if len(h.Args) == 0 {
		return []string{"sh", "-c", h.Command}
	}
	return append([]string{h.Command}, h.Args...)
}

// AskHooks returns the command hooks that settings, one or more settings
// files one after the other, run on a PermissionRequest of AskUserQuestion,
// in their order. A file that cannot be read ends the reading.
func AskHooks(settings []byte) []AskHook {
	var found []AskHook
	dec := json.NewDecoder(bytes.NewReader(settings))
	for {
		var s struct {
			Hooks struct {
				PermissionRequest []struct {
					Matcher string `json:"matcher"`
					Hooks   []struct {
						Type    string   `json:"type"`
						Command string   `json:"command"`
						Args    []string `json:"args"`
					} `json:"hooks"`
				} `json:"PermissionRequest"`
			} `json:"hooks"`
		}
		if dec.Decode(&s) != nil {
			return found
		}
		for _, m := range s.Hooks.PermissionRequest {
			if !matches(m.Matcher, AskTool) {
				continue
			}
			for _, h := range m.Hooks {
				if h.Type == "command" && h.Command != "" {
					found = append(found, AskHook{Command: h.Command, Args: h.Args})
				}
			}
		}
	}
}

// matches reports whether a hook's matcher takes the tool: no matcher or
// "*" takes every tool; otherwise the tool's name or a pattern matching
// it whole ("Bash|AskUserQuestion").
func matches(matcher, tool string) bool {
	if matcher == "" || matcher == "*" || matcher == tool {
		return true
	}
	re, err := regexp.Compile("^(?:" + matcher + ")$")
	return err == nil && re.MatchString(tool)
}

// Decides reports whether a PermissionRequest hook's output is a decision,
// as the template's bare allow is.
func Decides(out []byte) bool {
	var o struct {
		HookSpecificOutput struct {
			Decision struct {
				Behavior string `json:"behavior"`
			} `json:"decision"`
		} `json:"hookSpecificOutput"`
	}
	return json.Unmarshal(bytes.TrimSpace(out), &o) == nil && o.HookSpecificOutput.Decision.Behavior != ""
}
