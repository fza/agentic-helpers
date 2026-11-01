package shellguard

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/shellwalk"
)

// judgeRemoval judges a removal that is the whole command: its targets must be
// literal and lie strictly inside the project or /tmp, or under an extra root
// the project opens.
func judgeRemoval(_ context.Context, args []*syntax.Word, workingDir string, env Env, options Options) Verdict {
	values := make([]string, 0, len(args))
	for _, arg := range args {
		value, isLiteral := shellwalk.Literal(arg)
		if !isLiteral {
			if options.Expansion {
				return Verdict{Decision: Deny, Reason: reasonExpansion}
			}

			return cannotPlace(options, shellwalk.Text(arg), whyExpansion)
		}

		values = append(values, value)
	}

	home := physical(env.Home)
	root := physical(env.ProjectDir)

	if root == "/" || root == home {
		root = ""
	}

	tmp := physical(env.Tmp)
	base := physical(workingDir)
	extra := extraRoots(options.ExtraRoots, root, env.Home)

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
		case !within(resolved, root) && !within(resolved, tmp) && !underAny(resolved, extra):
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
	case strings.ContainsAny(target, "{}") || dotGlob(target) || slices.Contains(strings.Split(target, "/"), ".."):
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

// dotGlob is whether a component of the target is a glob that can match a
// name starting with a dot, `..` among them where the shell does not skip it.
func dotGlob(target string) bool {
	for _, component := range strings.Split(target, "/") {
		if strings.HasPrefix(component, ".") && strings.ContainsAny(component, "*?[") {
			return true
		}
	}

	return false
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

// gitPlaces are git's options moving where it reads its paths from.
var gitPlaces = []string{"-C", "--git-dir", "--work-tree"}

// moveSources are the operands of a move that name what it moves: every one
// but the last, the target, or every one where -t or --target-directory names
// the target (mv's, where targetOption holds). -S and --suffix take a value.
// A word with an expansion is an operand, the side a guard errs on.
func moveSources(args []*syntax.Word, targetOption bool) []*syntax.Word {
	var operands []*syntax.Word

	targetGiven, optionsEnded := false, false

	for index := 0; index < len(args); index++ {
		value, isLiteral := shellwalk.Literal(args[index])

		switch {
		case optionsEnded || !isLiteral || !strings.HasPrefix(value, "-") || value == "-":
			operands = append(operands, args[index])
		case value == "--":
			optionsEnded = true
		case targetOption && (value == "-t" || value == "--target-directory"):
			targetGiven = true
			index++
		case targetOption && strings.HasPrefix(value, "--target-directory="):
			targetGiven = true
		case value == "-S" || value == "--suffix":
			index++
		case strings.HasPrefix(value, "--"):
		case targetOption && strings.Contains(value, "t"):
			// A short group ending in t takes the next word as the target; one
			// with more after the t carries it.
			targetGiven = true

			if strings.HasSuffix(value, "t") {
				index++
			}
		case strings.HasSuffix(value, "S"):
			index++
		}
	}

	if targetGiven {
		return operands
	}

	if len(operands) == 0 {
		return nil
	}

	return operands[:len(operands)-1]
}

// extraRoots is every root an entry for the project opens, resolved as the
// project is: `~` expanded, links followed, so a target reached through a
// link out of a root lies outside it. A root resolving to the filesystem root
// or the home directory opens nothing, as such a project is none.
func extraRoots(entries []pluginconfig.ExtraRoot, project string, home string) []string {
	resolvedHome := physical(home)

	var roots []string

	for _, entry := range entries {
		if physical(expandHome(entry.Project, home)) != project {
			continue
		}

		for _, root := range entry.Roots {
			resolved := physical(expandHome(root, home))
			if resolved != "/" && resolved != resolvedHome {
				roots = append(roots, resolved)
			}
		}
	}

	return roots
}

func expandHome(written string, home string) string {
	if strings.HasPrefix(written, "~/") {
		return filepath.Join(home, written[1:])
	}

	return written
}

// underAny holds for a target that is one of the roots or lies below one: an
// extra root, unlike the project, may itself be removed.
func underAny(target string, roots []string) bool {
	for _, root := range roots {
		if target == root || within(target, root) {
			return true
		}
	}

	return false
}

// within holds only strictly below the directory: the directory itself is
// never a target.
func within(target string, directory string) bool {
	return directory != "" && strings.HasPrefix(target, directory+string(filepath.Separator))
}
