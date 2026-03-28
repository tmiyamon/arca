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
	TkIdent
	TkUpperIdent // starts with uppercase

	// Keywords
	TkType
	TkFn
	TkMatch
	TkLet
	TkTrue
	TkFalse

	// Symbols
	TkLParen
	TkRParen
	TkLBrace
	TkRBrace
	TkColon
	TkComma
	TkArrow // ->
	TkFatArrow // =>
	TkDot
	TkEq
	TkUnderscore
	TkPipe // |>

	TkEOF
)

var tokenNames = map[TokenKind]string{
	TkInt: "Int", TkFloat: "Float", TkString: "String",
	TkIdent: "Ident", TkUpperIdent: "UpperIdent",
	TkType: "type", TkFn: "fn", TkMatch: "match",
	TkLet: "let", TkTrue: "True", TkFalse: "False",
	TkLParen: "(", TkRParen: ")", TkLBrace: "{", TkRBrace: "}",
	TkColon: ":", TkComma: ",", TkArrow: "->", TkFatArrow: "=>",
	TkDot: ".", TkEq: "=", TkUnderscore: "_", TkPipe: "|>",
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
	"type":  TkType,
	"fn":    TkFn,
	"match": TkMatch,
	"let":   TkLet,
	"True":  TkTrue,
	"False": TkFalse,
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
		case ch == '.':
			l.advance()
			tokens = append(tokens, Token{TkDot, ".", line, col})
		case ch == '=':
			l.advance()
			if l.pos < len(l.input) && l.peek() == '>' {
				l.advance()
				tokens = append(tokens, Token{TkFatArrow, "=>", line, col})
			} else {
				tokens = append(tokens, Token{TkEq, "=", line, col})
			}
		case ch == '-':
			l.advance()
			if l.pos < len(l.input) && l.peek() == '>' {
				l.advance()
				tokens = append(tokens, Token{TkArrow, "->", line, col})
			} else {
				return nil, fmt.Errorf("%d:%d: unexpected '-'", line, col)
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
			tok, err := l.readString()
			if err != nil {
				return nil, err
			}
			tok.Line = line
			tok.Col = col
			tokens = append(tokens, tok)
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

func (l *Lexer) readString() (Token, error) {
	l.advance() // skip "
	var buf strings.Builder
	for l.pos < len(l.input) && l.peek() != '"' {
		ch := l.advance()
		if ch == '\\' && l.pos < len(l.input) {
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
			buf.WriteRune(ch)
		}
	}
	if l.pos >= len(l.input) {
		return Token{}, fmt.Errorf("unterminated string")
	}
	l.advance() // skip closing "
	return Token{Kind: TkString, Lit: buf.String()}, nil
}

func (l *Lexer) readNumber() Token {
	var buf strings.Builder
	isFloat := false
	for l.pos < len(l.input) && (unicode.IsDigit(l.peek()) || l.peek() == '.') {
		if l.peek() == '.' {
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
