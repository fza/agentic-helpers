package alternatives

import "strings"

// sedUsages are the GNU habits BSD sed fails on or silently mismatches, as
// verified against macOS's system sed. Arguments are read as BSD's getopt
// reads them: options before the first operand.
var sedUsages = []Usage{
	{
		Says:    "-i takes the next word as its backup suffix",
		Instead: "write -i '' for no backup file, or -i.bak",
		Matches: func(args []Arg) bool { return readSed(args).bareInPlace },
	},
	{
		Says:    "it takes no long option",
		Instead: "use the short one",
		Matches: func(args []Arg) bool { return readSed(args).long },
	},
	{
		Says:    "it has no -z",
		Instead: "install GNU sed (gsed), or work line by line",
		Matches: func(args []Arg) bool { return readSed(args).nulData },
	},
	{
		Says:    `\+ \? \| \s \w \b \< \> match nothing in a basic expression`,
		Instead: "use -E with + ? |, [[:space:]], [[:alnum:]_], [[:<:]] [[:>:]]",
		Matches: func(args []Arg) bool {
			read := readSed(args)

			return !read.extended && read.scan().gnuEscape
		},
	},
	{
		Says:    "a 0,/re/ address matches nothing",
		Instead: "use 1,/re/ where line 1 cannot match, or install GNU sed (gsed)",
		Matches: func(args []Arg) bool { return readSed(args).scan().zeroAddress },
	},
	{
		Says:    "a, i and c take their text only after a backslash and a newline",
		Instead: `write a\ and a newline before the text`,
		Matches: func(args []Arg) bool { return readSed(args).scan().oneLineText },
	},
}

var dateUsages = []Usage{
	{
		Says:    "it has no -d",
		Instead: "use gdate -d, or date -j -f <format> <value>",
		Matches: dateParses,
	},
}

type sedCall struct {
	scripts     []string
	extended    bool
	bareInPlace bool
	long        bool
	nulData     bool
}

// readSed reads a call's options up to its first operand, a script among
// them where no -e or -f gave one. A word the shell builds at run time ends
// the reading: nothing tells what it holds.
func readSed(args []Arg) sedCall {
	var read sedCall

	given := false

	for index := 0; index < len(args); index++ {
		arg := args[index]

		switch {
		case !arg.Literal:
			return read
		case arg.Text == "--":
			if !given && index+1 < len(args) && args[index+1].Literal {
				read.scripts = append(read.scripts, args[index+1].Text)
			}

			return read
		case strings.HasPrefix(arg.Text, "--"):
			read.long = true
		case strings.HasPrefix(arg.Text, "-") && len(arg.Text) > 1:
			var took bool

			index, took = read.group(args, index)
			given = given || took
		default:
			if !given {
				read.scripts = append(read.scripts, arg.Text)
			}

			return read
		}
	}

	return read
}

// group reads one group of short options, and returns the index of the last
// word it used and whether it gave a script or a script file.
func (read *sedCall) group(args []Arg, index int) (int, bool) {
	letters := args[index].Text[1:]

	for position := 0; position < len(letters); position++ {
		rest := letters[position+1:]

		switch letters[position] {
		case 'E', 'r':
			read.extended = true
		case 'z':
			read.nulData = true
		case 'e', 'f':
			value, next := valueOf(args, index, rest)
			if letters[position] == 'e' && value != nil {
				read.scripts = append(read.scripts, *value)
			}

			return next, true
		case 'i':
			if rest != "" {
				return index, false
			}

			if index+1 >= len(args) || !args[index+1].Literal || args[index+1].Text != "" {
				read.bareInPlace = true

				return index, false
			}

			return index + 1, false
		}
	}

	return index, false
}

// valueOf is an option's value, the rest of its group or the next word, nil
// where the shell builds it at run time, and the index of the last word used.
func valueOf(args []Arg, index int, rest string) (*string, int) {
	if rest != "" {
		return &rest, index
	}

	if index+1 >= len(args) {
		return nil, index
	}

	if !args[index+1].Literal {
		return nil, index + 1
	}

	return &args[index+1].Text, index + 1
}

type sedFindings struct {
	gnuEscape   bool
	zeroAddress bool
	oneLineText bool
}

func (read sedCall) scan() sedFindings {
	var found sedFindings

	for _, script := range read.scripts {
		scanSed(script, &found)
	}

	return found
}

// scanSed reads a script's commands far enough to tell its expressions from
// replacement text, labels, file names and appended text, and stops at the
// first command it does not know: a finding must hold for certain.
func scanSed(script string, found *sedFindings) {
	scanner := sedScanner{script: script, found: found}

	for {
		scanner.skip(" \t\n;")
		if scanner.done() {
			return
		}

		start := scanner.position
		if !scanner.address() {
			return
		}

		zero := scanner.script[start:scanner.position] == "0"

		if !scanner.done() && scanner.peek() == ',' {
			found.zeroAddress = found.zeroAddress || zero
			scanner.position++
			scanner.skip("+~")

			if !scanner.address() {
				return
			}
		}

		scanner.skip(" \t!")
		if scanner.done() || !scanner.command() {
			return
		}
	}
}

type sedScanner struct {
	script   string
	position int
	found    *sedFindings
}

func (scanner *sedScanner) done() bool { return scanner.position >= len(scanner.script) }

func (scanner *sedScanner) peek() byte { return scanner.script[scanner.position] }

func (scanner *sedScanner) skip(set string) {
	for !scanner.done() && strings.IndexByte(set, scanner.peek()) >= 0 {
		scanner.position++
	}
}

func (scanner *sedScanner) restOfLine() {
	for !scanner.done() && scanner.peek() != '\n' {
		scanner.position++
	}
}

func (scanner *sedScanner) address() bool {
	switch {
	case scanner.done():
	case scanner.peek() == '$':
		scanner.position++
	case scanner.peek() >= '0' && scanner.peek() <= '9':
		scanner.skip("0123456789~")
	case scanner.peek() == '/':
		scanner.position++

		if !scanner.expression('/') {
			return false
		}

		scanner.skip("IM")
	case scanner.peek() == '\\' && scanner.position+1 < len(scanner.script):
		delimiter := scanner.script[scanner.position+1]
		scanner.position += 2

		if !scanner.expression(delimiter) {
			return false
		}

		scanner.skip("IM")
	}

	return true
}

func (scanner *sedScanner) command() bool {
	command := scanner.peek()
	scanner.position++

	switch {
	case strings.IndexByte("{}pdDnNgGhHxPz=Fv", command) >= 0:
	case strings.IndexByte("qQlL", command) >= 0:
		scanner.skip(" \t0123456789")
	case strings.IndexByte("bTt:", command) >= 0:
		for !scanner.done() && strings.IndexByte(";\n", scanner.peek()) < 0 {
			scanner.position++
		}
	case strings.IndexByte("rRwWe", command) >= 0:
		scanner.restOfLine()
	case strings.IndexByte("aic", command) >= 0:
		scanner.skip(" \t")
		if scanner.done() || scanner.peek() != '\\' {
			scanner.found.oneLineText = true
		}

		scanner.text()
	case command == 's' || command == 'y':
		return scanner.substitution(command)
	default:
		return false
	}

	return true
}

// text moves past appended text: to the end of a line no backslash continues.
func (scanner *sedScanner) text() {
	for !scanner.done() {
		character := scanner.peek()
		scanner.position++

		switch character {
		case '\\':
			scanner.position++
		case '\n':
			return
		}
	}
}

func (scanner *sedScanner) substitution(command byte) bool {
	if scanner.done() || scanner.peek() == '\\' || scanner.peek() == '\n' {
		return false
	}

	delimiter := scanner.peek()
	scanner.position++

	if command == 's' {
		if !scanner.expression(delimiter) {
			return false
		}
	} else if !scanner.plain(delimiter) {
		return false
	}

	if !scanner.plain(delimiter) {
		return false
	}

	if command == 's' {
		scanner.skip("gpiImMe0123456789")

		if !scanner.done() && (scanner.peek() == 'w') {
			scanner.restOfLine()
		}
	}

	return true
}

// plain moves past text up to an unescaped delimiter: a replacement or a y
// string, never an expression.
func (scanner *sedScanner) plain(delimiter byte) bool {
	for !scanner.done() {
		character := scanner.peek()
		scanner.position++

		switch character {
		case '\\':
			scanner.position++
		case delimiter:
			return true
		}
	}

	return false
}

// expression moves past a regular expression up to an unescaped delimiter,
// noting a GNU escape outside a bracket expression. An escaped delimiter is
// the delimiter's own character on both flavours.
func (scanner *sedScanner) expression(delimiter byte) bool {
	for !scanner.done() {
		character := scanner.peek()
		scanner.position++

		switch character {
		case '\\':
			if scanner.done() {
				return false
			}

			escaped := scanner.peek()
			scanner.position++

			if escaped != delimiter && strings.IndexByte(`+?|swb<>`, escaped) >= 0 {
				scanner.found.gnuEscape = true
			}
		case '[':
			scanner.bracket()
		case delimiter:
			return true
		}
	}

	return false
}

// bracket moves past a bracket expression, whose backslash is text: a `]`
// first, or after `^`, belongs to the set, and `[:class:]` holds its own `]`.
func (scanner *sedScanner) bracket() {
	if !scanner.done() && scanner.peek() == '^' {
		scanner.position++
	}

	if !scanner.done() && scanner.peek() == ']' {
		scanner.position++
	}

	for !scanner.done() {
		character := scanner.peek()
		scanner.position++

		switch {
		case character == ']':
			return
		case character == '[' && !scanner.done() && strings.IndexByte(":.=", scanner.peek()) >= 0:
			closing := string(scanner.peek()) + "]"
			end := strings.Index(scanner.script[scanner.position+1:], closing)
			if end < 0 {
				return
			}

			scanner.position += end + 1 + len(closing)
		}
	}
}

// dateParses is whether a date call carries -d among its options, read up to
// its first operand: GNU's date string, an illegal option to BSD's date.
func dateParses(args []Arg) bool {
	for index := 0; index < len(args); index++ {
		arg := args[index]

		switch {
		case !arg.Literal || arg.Text == "--" || arg.Text == "-" || !strings.HasPrefix(arg.Text, "-"):
			return false
		case strings.HasPrefix(arg.Text, "--"):
			continue
		}

		letters := arg.Text[1:]

	group:
		for position := 0; position < len(letters); position++ {
			switch letters[position] {
			case 'd':
				return true
			case 'f', 'r', 's', 'v':
				if position+1 == len(letters) {
					index++
				}

				break group
			case 'I':
				break group
			}
		}
	}

	return false
}
