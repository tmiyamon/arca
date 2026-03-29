package main

import (
	"fmt"
	"strings"
	"unicode"
)

type TokenKind int

const (
	// Literals
	TkInt TokenKind = iota
	TkFloat
	TkString
	TkStringInterpStart // "Hello ${
	TkStringInterpEnd   // }...rest"
	TkIdent
	TkUpperIdent // starts with uppercase

	// Keywords
	TkType
	TkFn
	TkMatch
	TkLet
	TkTrue
	TkFalse
	TkPub
	TkImport
	TkFor
	TkIn
	TkAssert

	// Symbols
	TkLParen
	TkRParen
	TkLBracket // [
	TkRBracket // ]
	TkLBrace
	TkRBrace
	TkColon
	TkComma
	TkArrow    // ->
	TkFatArrow // =>
	TkDot
	TkDotDot // ..
	TkEq
	TkUnderscore
	TkPipe     // |>
	TkQuestion // ?
	TkPlus     // +
	TkMinus    // - (also used in ->)
	TkStar     // *
	TkSlash    // /
	TkPercent  // %
	TkLt       // <
	TkGt       // >
	TkEqEq     // ==
	TkNotEq    // !=
	TkLtEq     // <=
	TkGtEq     // >=
	TkAnd      // &&
	TkOr       // ||
	TkBang     // !

	TkEOF
)

var tokenNames = map[TokenKind]string{
	TkInt: "Int", TkFloat: "Float", TkString: "String",
	TkStringInterpStart: "InterpStart", TkStringInterpEnd: "InterpEnd",
	TkIdent: "Ident", TkUpperIdent: "UpperIdent",
	TkType: "type", TkFn: "fn", TkMatch: "match",
	TkLet: "let", TkTrue: "True", TkFalse: "False",
	TkPub: "pub", TkImport: "import", TkFor: "for", TkIn: "in", TkAssert: "assert",
	TkLParen: "(", TkRParen: ")",
	TkLBracket: "[", TkRBracket: "]",
	TkLBrace: "{", TkRBrace: "}",
	TkColon: ":", TkComma: ",", TkArrow: "->", TkFatArrow: "=>",
	TkDot: ".", TkDotDot: "..", TkEq: "=", TkUnderscore: "_",
	TkPipe: "|>", TkQuestion: "?",
	TkPlus: "+", TkMinus: "-", TkStar: "*", TkSlash: "/", TkPercent: "%",
	TkLt: "<", TkGt: ">", TkEqEq: "==", TkNotEq: "!=", TkLtEq: "<=", TkGtEq: ">=",
	TkAnd: "&&", TkOr: "||", TkBang: "!",
	TkEOF: "EOF",
}

type Token struct {
	Kind TokenKind
	Lit  string
	Line int
	Col  int
}

func (t Token) String() string {
	if name, ok := tokenNames[t.Kind]; ok {
		if t.Lit != "" && t.Lit != name {
			return fmt.Sprintf("%s(%s)", name, t.Lit)
		}
		return name
	}
	return fmt.Sprintf("?(%s)", t.Lit)
}

var keywords = map[string]TokenKind{
	"type":   TkType,
	"fn":     TkFn,
	"match":  TkMatch,
	"let":    TkLet,
	"True":   TkTrue,
	"False":  TkFalse,
	"pub":    TkPub,
	"import": TkImport,
	"for":    TkFor,
	"in":     TkIn,
	"assert": TkAssert,
}

type Lexer struct {
	input []rune
	pos   int
	line  int
	col   int
}

func NewLexer(input string) *Lexer {
	return &Lexer{input: []rune(input), pos: 0, line: 1, col: 1}
}

func (l *Lexer) peek() rune {
	if l.pos >= len(l.input) {
		return 0
	}
	return l.input[l.pos]
}

func (l *Lexer) advance() rune {
	ch := l.input[l.pos]
	l.pos++
	if ch == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return ch
}

func (l *Lexer) skipWhitespaceAndComments() {
	for l.pos < len(l.input) {
		ch := l.peek()
		if unicode.IsSpace(ch) {
			l.advance()
		} else if ch == '/' && l.pos+1 < len(l.input) && l.input[l.pos+1] == '/' {
			// line comment
			for l.pos < len(l.input) && l.peek() != '\n' {
				l.advance()
			}
		} else {
			break
		}
	}
}

func (l *Lexer) Tokenize() ([]Token, error) {
	var tokens []Token
	for {
		l.skipWhitespaceAndComments()
		if l.pos >= len(l.input) {
			tokens = append(tokens, Token{Kind: TkEOF, Line: l.line, Col: l.col})
			return tokens, nil
		}

		line, col := l.line, l.col
		ch := l.peek()

		switch {
		case ch == '(':
			l.advance()
			tokens = append(tokens, Token{TkLParen, "(", line, col})
		case ch == ')':
			l.advance()
			tokens = append(tokens, Token{TkRParen, ")", line, col})
		case ch == '[':
			l.advance()
			tokens = append(tokens, Token{TkLBracket, "[", line, col})
		case ch == ']':
			l.advance()
			tokens = append(tokens, Token{TkRBracket, "]", line, col})
		case ch == '{':
			l.advance()
			tokens = append(tokens, Token{TkLBrace, "{", line, col})
		case ch == '}':
			l.advance()
			tokens = append(tokens, Token{TkRBrace, "}", line, col})
		case ch == ':':
			l.advance()
			tokens = append(tokens, Token{TkColon, ":", line, col})
		case ch == ',':
			l.advance()
			tokens = append(tokens, Token{TkComma, ",", line, col})
		case ch == '?':
			l.advance()
			tokens = append(tokens, Token{TkQuestion, "?", line, col})
		case ch == '.':
			l.advance()
			if l.pos < len(l.input) && l.peek() == '.' {
				l.advance()
				tokens = append(tokens, Token{TkDotDot, "..", line, col})
			} else {
				tokens = append(tokens, Token{TkDot, ".", line, col})
			}
		case ch == '=':
			l.advance()
			if l.pos < len(l.input) && l.peek() == '>' {
				l.advance()
				tokens = append(tokens, Token{TkFatArrow, "=>", line, col})
			} else if l.pos < len(l.input) && l.peek() == '=' {
				l.advance()
				tokens = append(tokens, Token{TkEqEq, "==", line, col})
			} else {
				tokens = append(tokens, Token{TkEq, "=", line, col})
			}
		case ch == '+':
			l.advance()
			tokens = append(tokens, Token{TkPlus, "+", line, col})
		case ch == '*':
			l.advance()
			tokens = append(tokens, Token{TkStar, "*", line, col})
		case ch == '%':
			l.advance()
			tokens = append(tokens, Token{TkPercent, "%", line, col})
		case ch == '!':
			l.advance()
			if l.pos < len(l.input) && l.peek() == '=' {
				l.advance()
				tokens = append(tokens, Token{TkNotEq, "!=", line, col})
			} else {
				tokens = append(tokens, Token{TkBang, "!", line, col})
			}
		case ch == '<':
			l.advance()
			if l.pos < len(l.input) && l.peek() == '=' {
				l.advance()
				tokens = append(tokens, Token{TkLtEq, "<=", line, col})
			} else {
				tokens = append(tokens, Token{TkLt, "<", line, col})
			}
		case ch == '>':
			l.advance()
			if l.pos < len(l.input) && l.peek() == '=' {
				l.advance()
				tokens = append(tokens, Token{TkGtEq, ">=", line, col})
			} else {
				tokens = append(tokens, Token{TkGt, ">", line, col})
			}
		case ch == '/':
			l.advance()
			tokens = append(tokens, Token{TkSlash, "/", line, col})
		case ch == '&':
			l.advance()
			if l.pos < len(l.input) && l.peek() == '&' {
				l.advance()
				tokens = append(tokens, Token{TkAnd, "&&", line, col})
			} else {
				return nil, fmt.Errorf("%d:%d: unexpected '&'", line, col)
			}
		case ch == '-':
			l.advance()
			if l.pos < len(l.input) && l.peek() == '>' {
				l.advance()
				tokens = append(tokens, Token{TkArrow, "->", line, col})
			} else {
				tokens = append(tokens, Token{TkMinus, "-", line, col})
			}
		case ch == '|':
			l.advance()
			if l.pos < len(l.input) && l.peek() == '>' {
				l.advance()
				tokens = append(tokens, Token{TkPipe, "|>", line, col})
			} else {
				return nil, fmt.Errorf("%d:%d: unexpected '|'", line, col)
			}
		case ch == '_' && (l.pos+1 >= len(l.input) || !unicode.IsLetter(l.input[l.pos+1])):
			l.advance()
			tokens = append(tokens, Token{TkUnderscore, "_", line, col})
		case ch == '"':
			toks, err := l.readStringOrInterp()
			if err != nil {
				return nil, err
			}
			for i := range toks {
				toks[i].Line = line
				toks[i].Col = col
			}
			tokens = append(tokens, toks...)
		case unicode.IsDigit(ch):
			tok := l.readNumber()
			tok.Line = line
			tok.Col = col
			tokens = append(tokens, tok)
		case unicode.IsLetter(ch) || ch == '_':
			tok := l.readIdent()
			tok.Line = line
			tok.Col = col
			tokens = append(tokens, tok)
		default:
			return nil, fmt.Errorf("%d:%d: unexpected character '%c'", line, col, ch)
		}
	}
}

func (l *Lexer) readStringOrInterp() ([]Token, error) {
	l.advance() // skip opening "
	var tokens []Token
	var buf strings.Builder
	hasInterp := false

	for l.pos < len(l.input) && l.peek() != '"' {
		ch := l.peek()
		if ch == '$' && l.pos+1 < len(l.input) && l.input[l.pos+1] == '{' {
			// String interpolation
			hasInterp = true
			if buf.Len() > 0 {
				tokens = append(tokens, Token{Kind: TkString, Lit: buf.String()})
				buf.Reset()
			}
			l.advance() // skip $
			l.advance() // skip {
			// Read tokens until matching }
			depth := 1
			for l.pos < len(l.input) && depth > 0 {
				l.skipWhitespaceAndComments()
				if l.peek() == '}' {
					depth--
					if depth == 0 {
						l.advance()
						break
					}
				}
				if l.peek() == '{' {
					depth++
				}
				// Read a single token for the interpolated expression
				// For simplicity, we support only identifiers and field access in interpolation
				if unicode.IsLetter(l.peek()) || l.peek() == '_' {
					tok := l.readIdent()
					tokens = append(tokens, tok)
					// Handle field access chain
					for l.pos < len(l.input) && l.peek() == '.' {
						l.advance()
						tokens = append(tokens, Token{Kind: TkDot, Lit: "."})
						tok = l.readIdent()
						tokens = append(tokens, tok)
					}
				} else {
					return nil, fmt.Errorf("unsupported expression in string interpolation")
				}
			}
		} else if ch == '\\' && l.pos+1 < len(l.input) {
			l.advance()
			next := l.advance()
			switch next {
			case 'n':
				buf.WriteRune('\n')
			case 't':
				buf.WriteRune('\t')
			case '"':
				buf.WriteRune('"')
			case '\\':
				buf.WriteRune('\\')
			default:
				buf.WriteRune('\\')
				buf.WriteRune(next)
			}
		} else {
			buf.WriteRune(l.advance())
		}
	}
	if l.pos >= len(l.input) {
		return nil, fmt.Errorf("unterminated string")
	}
	l.advance() // skip closing "

	if !hasInterp {
		return []Token{{Kind: TkString, Lit: buf.String()}}, nil
	}
	// Trailing string part
	if buf.Len() > 0 {
		tokens = append(tokens, Token{Kind: TkString, Lit: buf.String()})
	}
	// Wrap in interp markers
	result := []Token{{Kind: TkStringInterpStart, Lit: ""}}
	result = append(result, tokens...)
	result = append(result, Token{Kind: TkStringInterpEnd, Lit: ""})
	return result, nil
}

func (l *Lexer) readNumber() Token {
	var buf strings.Builder
	isFloat := false
	for l.pos < len(l.input) && (unicode.IsDigit(l.peek()) || l.peek() == '.') {
		if l.peek() == '.' {
			// Check for .. (range operator)
			if l.pos+1 < len(l.input) && l.input[l.pos+1] == '.' {
				break
			}
			if isFloat {
				break
			}
			isFloat = true
		}
		buf.WriteRune(l.advance())
	}
	if isFloat {
		return Token{Kind: TkFloat, Lit: buf.String()}
	}
	return Token{Kind: TkInt, Lit: buf.String()}
}

func (l *Lexer) readIdent() Token {
	var buf strings.Builder
	for l.pos < len(l.input) && (unicode.IsLetter(l.peek()) || unicode.IsDigit(l.peek()) || l.peek() == '_') {
		buf.WriteRune(l.advance())
	}
	lit := buf.String()
	if kind, ok := keywords[lit]; ok {
		return Token{Kind: kind, Lit: lit}
	}
	if len(lit) > 0 && unicode.IsUpper([]rune(lit)[0]) {
		return Token{Kind: TkUpperIdent, Lit: lit}
	}
	return Token{Kind: TkIdent, Lit: lit}
}
