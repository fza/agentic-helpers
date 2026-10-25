package shellguard

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// judgeRemoval judges a removal that is the whole command: its targets must be
// literal and lie strictly inside the project or /tmp.
func judgeRemoval(_ context.Context, args []*syntax.Word, workingDir string, env Env, options Options) Verdict {
	values := make([]string, 0, len(args))
	for _, arg := range args {
		value, isLiteral := literal(arg)
		if !isLiteral {
			if options.Expansion {
				return Verdict{Decision: Deny, Reason: reasonExpansion}
			}

			return cannotPlace(options, text(arg), whyExpansion)
		}

		values = append(values, value)
	}

	home := physical(env.Home)
	root := physical(env.ProjectDir)
	if root == "" {
		root = physical(workingDir)
	}

	if root == "/" || root == home {
		root = ""
	}

	tmp := physical(env.Tmp)
	base := physical(workingDir)

	var outside, unresolved []string

	targets := 0
	optionsEnded := false
	for _, value := range values {
		if !optionsEnded && value == "--" {
			optionsEnded = true

			continue
		}

		if !optionsEnded && strings.HasPrefix(value, "-") {
			continue
		}

		targets++

		resolved := resolve(value, base, env.Home)
		switch {
		case resolved == "":
			unresolved = append(unresolved, value)
		case !within(resolved, root) && !within(resolved, tmp):
			outside = append(outside, value)
		}
	}

	var reasons []string
	if len(unresolved) > 0 {
		reasons = append(reasons, "unresolvable: "+strings.Join(unresolved, " "))
	}

	if len(outside) > 0 {
		label := labelOutsideProject
		if root == "" {
			label = labelNoProject
		}

		reasons = append(reasons, label+strings.Join(outside, " "))
	}

	if targets == 0 {
		reasons = append(reasons, "no target")
	}

	if len(reasons) == 0 {
		return Verdict{Decision: Allow}
	}

	return mode(options.OutsideProject, strings.Join(reasons, "; "))
}

// resolve turns a target into a physical absolute path, or "" when it cannot
// be known before the shell expands it.
func resolve(target string, base string, home string) string {
	switch {
	case strings.ContainsAny(target, "{}"):
		return ""
	case target == "~":
		target = home
	case strings.HasPrefix(target, "~/"):
		target = filepath.Join(home, target[2:])
	case strings.HasPrefix(target, "~"):
		return ""
	case filepath.IsAbs(target):
	case base == "":
		return ""
	default:
		target = filepath.Join(base, target)
	}

	return physical(filepath.Clean(target))
}

// physical resolves the deepest existing directory of an absolute path through
// its symlinks, so a link inside the project pointing elsewhere counts as
// elsewhere.
func physical(target string) string {
	if !filepath.IsAbs(target) {
		return ""
	}

	head := filepath.Clean(target)
	tail := ""
	for head != "/" {
		info, err := os.Stat(head)
		if err == nil && info.IsDir() {
			break
		}

		tail = filepath.Join(filepath.Base(head), tail)
		head = filepath.Dir(head)
	}

	resolved, err := filepath.EvalSymlinks(head)
	if err != nil {
		return ""
	}

	return filepath.Join(resolved, tail)
}

// within holds only strictly below the directory: the directory itself is
// never a target.
func within(target string, directory string) bool {
	return directory != "" && strings.HasPrefix(target, directory+string(filepath.Separator))
}
