// Package layout parses the view layout language sdd's `view` reads: sections
// separated by commas, each a colon-chained run of functions, a function a
// word with an optional parenthesised argument list. The grammar is sdd's,
// owned here since sdd's parser is internal to its own module:
//
//	layout   := section ("," section)*
//	section  := function (":" function)*
//	function := name ("(" arguments? ")")?
//
// An argument is a function or a double-quoted string. No whitespace stands
// outside a quoted string. What a word means is the reader's to decide; this
// package knows no vocabulary.
package layout

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Call is one function: its word, where the word starts (1-based, in
// characters), and its arguments. Parens says whether an argument list was
// written, empty or not.
type Call struct {
	Name     string
	Position int
	Parens   bool
	Args     []Arg
}

// Arg is one argument: a quoted string, or a function, which a bare word is.
type Arg struct {
	Quoted   bool
	Text     string
	Call     *Call
	Position int
}

// Word is the argument as a word: a quoted string's text, a bare word's name.
// A function with arguments has none.
func (arg Arg) Word() (string, bool) {
	switch {
	case arg.Quoted:
		return arg.Text, true
	case arg.Call != nil && !arg.Call.Parens:
		return arg.Call.Name, true
	}

	return "", false
}

// Section is one comma-separated part of a layout and the text it was written
// as.
type Section struct {
	Text     string
	Position int
	Calls    []Call
}

// Error is why a layout does not parse: the token at fault, where it stands,
// and what was expected there.
type Error struct {
	Token    string
	Position int
	Expected string
}

func (problem *Error) Error() string {
	return fmt.Sprintf("unexpected %s at position %d; expected %s", quoteToken(problem.Token), problem.Position,
		problem.Expected)
}

// EndOfLayout is the token an Error names where the layout ended too soon.
const EndOfLayout = "end of layout"

func quoteToken(token string) string {
	if token == EndOfLayout {
		return token
	}

	return fmt.Sprintf("%q", token)
}

type parser struct {
	text  []rune
	index int
}

// Parse reads a layout into its sections.
func Parse(text string) ([]Section, error) {
	if !utf8.ValidString(text) {
		return nil, &Error{Token: text, Position: 1, Expected: "UTF-8 text"}
	}

	reading := &parser{text: []rune(text)}

	var sections []Section

	for {
		start := reading.index

		calls, err := reading.section()
		if err != nil {
			return nil, err
		}

		sections = append(sections, Section{
			Text: string(reading.text[start:reading.index]), Position: start + 1, Calls: calls,
		})

		if reading.done() {
			return sections, nil
		}

		if reading.peek() != ',' {
			return nil, reading.unexpected(`":", "," or the end of the layout`)
		}

		reading.index++
	}
}

func (reading *parser) done() bool {
	return reading.index >= len(reading.text)
}

func (reading *parser) peek() rune {
	return reading.text[reading.index]
}

func (reading *parser) section() ([]Call, error) {
	var calls []Call

	for {
		call, err := reading.function()
		if err != nil {
			return nil, err
		}

		calls = append(calls, call)

		if reading.done() || reading.peek() != ':' {
			return calls, nil
		}

		reading.index++
	}
}

func (reading *parser) function() (Call, error) {
	start := reading.index

	for !reading.done() && wordRune(reading.peek()) {
		reading.index++
	}

	if reading.index == start {
		return Call{}, reading.unexpected("a word")
	}

	call := Call{Name: string(reading.text[start:reading.index]), Position: start + 1}

	if reading.done() || reading.peek() != '(' {
		return call, nil
	}

	call.Parens = true
	reading.index++

	if !reading.done() && reading.peek() == ')' {
		reading.index++

		return call, nil
	}

	for {
		arg, err := reading.argument()
		if err != nil {
			return Call{}, err
		}

		call.Args = append(call.Args, arg)

		if reading.done() {
			return Call{}, reading.unexpected(`"," or ")"`)
		}

		switch reading.peek() {
		case ',':
			reading.index++
		case ')':
			reading.index++

			return call, nil
		default:
			return Call{}, reading.unexpected(`"," or ")"`)
		}
	}
}

func (reading *parser) argument() (Arg, error) {
	if reading.done() || reading.peek() != '"' {
		position := reading.index + 1

		call, err := reading.function()
		if err != nil {
			return Arg{}, err
		}

		return Arg{Call: &call, Position: position}, nil
	}

	start := reading.index
	reading.index++

	var text strings.Builder

	for !reading.done() {
		char := reading.peek()
		reading.index++

		switch char {
		case '"':
			return Arg{Quoted: true, Text: text.String(), Position: start + 1}, nil
		case '\\':
			if reading.done() {
				return Arg{}, reading.unexpected(`an escaped character`)
			}

			text.WriteRune(reading.peek())
			reading.index++
		default:
			text.WriteRune(char)
		}
	}

	return Arg{}, &Error{Token: EndOfLayout, Position: len(reading.text) + 1, Expected: `the closing '"' of the string at position ` +
		fmt.Sprint(start+1)}
}

// unexpected is an Error naming the token standing where the parser is.
func (reading *parser) unexpected(expected string) *Error {
	if reading.done() {
		return &Error{Token: EndOfLayout, Position: len(reading.text) + 1, Expected: expected}
	}

	end := reading.index + 1
	if wordRune(reading.peek()) {
		for end < len(reading.text) && wordRune(reading.text[end]) {
			end++
		}
	}

	return &Error{Token: string(reading.text[reading.index:end]), Position: reading.index + 1, Expected: expected}
}

// wordRune is a character a bare word may hold: a letter, mark or digit of
// any script, a dash, an underscore or a dot.
func wordRune(char rune) bool {
	return unicode.IsLetter(char) || unicode.IsDigit(char) || unicode.IsMark(char) || char == '-' || char == '_' ||
		char == '.'
}

// IsWord is whether text reads as a bare word.
func IsWord(text string) bool {
	if text == "" {
		return false
	}

	for _, char := range text {
		if !wordRune(char) {
			return false
		}
	}

	return true
}
