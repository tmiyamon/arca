package main

import (
	"fmt"
	"strconv"
	"strings"
)

type Parser struct {
	tokens []Token
	pos    int
}

func NewParser(tokens []Token) *Parser {
	return &Parser{tokens: tokens, pos: 0}
}

func (p *Parser) peek() Token {
	if p.pos >= len(p.tokens) {
		return Token{Kind: TkEOF}
	}
	return p.tokens[p.pos]
}

func (p *Parser) advance() Token {
	tok := p.peek()
	p.pos++
	return tok
}

func (p *Parser) expect(kind TokenKind) (Token, error) {
	tok := p.advance()
	if tok.Kind != kind {
		return tok, fmt.Errorf("%d:%d: expected %s, got %s", tok.Line, tok.Col, tokenNames[kind], tok)
	}
	return tok, nil
}

func (p *Parser) ParseProgram() (*Program, error) {
	var decls []Decl
	for p.peek().Kind != TkEOF {
		decl, err := p.parseDecl()
		if err != nil {
			return nil, err
		}
		decls = append(decls, decl)
	}
	return &Program{Decls: decls}, nil
}

func (p *Parser) parseDecl() (Decl, error) {
	switch p.peek().Kind {
	case TkImport:
		return p.parseImportDecl()
	case TkType:
		return p.parseTypeDecl()
	case TkFn:
		return p.parseFnDecl(false)
	case TkPub:
		p.advance()
		if p.peek().Kind == TkFn {
			return p.parseFnDecl(true)
		}
		return nil, fmt.Errorf("%d:%d: expected fn after pub, got %s", p.peek().Line, p.peek().Col, p.peek())
	default:
		return nil, fmt.Errorf("%d:%d: expected import, type, pub, or fn, got %s", p.peek().Line, p.peek().Col, p.peek())
	}
}

func (p *Parser) parseImportDecl() (Decl, error) {
	importTok := p.advance() // skip 'import'
	importPos := Pos{importTok.Line, importTok.Col}
	tok := p.peek()

	// Go package: import go "path" or import go _ "path"
	if tok.Kind == TkIdent && tok.Lit == "go" {
		p.advance() // skip 'go'
		sideEffect := false
		if p.peek().Kind == TkUnderscore {
			p.advance() // skip '_'
			sideEffect = true
		}
		pathTok, err := p.expect(TkString)
		if err != nil {
			return nil, fmt.Errorf("%d:%d: expected string path after 'import go', got %s", p.peek().Line, p.peek().Col, p.peek())
		}
		return ImportDecl{Pos: importPos, Path: "go/" + pathTok.Lit, SideEffect: sideEffect}, nil
	}

	// Arca module: import user, import user.{find, create}, import user as u
	if tok.Kind != TkIdent && tok.Kind != TkUpperIdent {
		return nil, fmt.Errorf("%d:%d: expected module path, got %s", tok.Line, tok.Col, tok)
	}
	p.advance()
	path := tok.Lit
	for p.peek().Kind == TkDot {
		// Check for selective import: import user.{find, create}
		if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == TkLBrace {
			p.advance() // skip '.'
			p.advance() // skip '{'
			var names []string
			for p.peek().Kind != TkRBrace {
				name := p.advance()
				if name.Kind != TkIdent && name.Kind != TkUpperIdent {
					return nil, fmt.Errorf("%d:%d: expected name in selective import, got %s", name.Line, name.Col, name)
				}
				names = append(names, name.Lit)
				if p.peek().Kind == TkComma {
					p.advance()
				}
			}
			p.advance() // skip '}'
			return ImportDecl{Pos: importPos, Path: path, Names: names}, nil
		}
		p.advance() // skip '.'
		next := p.advance()
		if next.Kind != TkIdent && next.Kind != TkUpperIdent {
			return nil, fmt.Errorf("%d:%d: expected identifier in import path, got %s", next.Line, next.Col, next)
		}
		path += "." + next.Lit
	}

	// Check for alias: import user as u
	if p.peek().Kind == TkIdent && p.peek().Lit == "as" {
		p.advance() // skip 'as'
		alias := p.advance()
		if alias.Kind != TkIdent {
			return nil, fmt.Errorf("%d:%d: expected alias name, got %s", alias.Line, alias.Col, alias)
		}
		return ImportDecl{Pos: importPos, Path: path, Alias: alias.Lit}, nil
	}

	return ImportDecl{Pos: importPos, Path: path}, nil
}

func (p *Parser) parseTypeDecl() (Decl, error) {
	p.advance() // skip 'type'
	name, err := p.expect(TkUpperIdent)
	if err != nil {
		return nil, err
	}
	// Type parameters: type Pair[A, B] { ... }
	var params []string
	if p.peek().Kind == TkLBracket {
		p.advance() // skip '['
		for p.peek().Kind != TkRBracket {
			tok := p.advance()
			if tok.Kind != TkUpperIdent && tok.Kind != TkIdent {
				return nil, fmt.Errorf("%d:%d: expected type parameter, got %s", tok.Line, tok.Col, tok)
			}
			params = append(params, tok.Lit)
			if p.peek().Kind == TkComma {
				p.advance()
			}
		}
		p.advance() // skip ']'
	}

	// Type alias: type Name = Type
	if p.peek().Kind == TkEq {
		p.advance() // skip '='
		typ, err := p.parseType()
		if err != nil {
			return nil, err
		}
		return TypeAliasDecl{Name: name.Lit, Type: typ}, nil
	}

	// Short record: type Name(fields...)
	if p.peek().Kind == TkLParen {
		p.advance() // skip '('
		var fields []Field
		for p.peek().Kind != TkRParen {
			field, err := p.parseField()
			if err != nil {
				return nil, err
			}
			fields = append(fields, field)
			if p.peek().Kind == TkComma {
				p.advance()
			}
		}
		p.advance() // skip ')'
		// Short record may have tags/methods block
		var methods []FnDecl
		var tags []TagRule
		if p.peek().Kind == TkLBrace {
			p.advance() // skip '{'
			for p.peek().Kind != TkRBrace {
				if p.peek().Kind == TkTags {
					t, err := p.parseTagsBlock()
					if err != nil {
						return nil, err
					}
					tags = t
				} else if p.peek().Kind == TkStatic {
					method, err := p.parseStaticMethodDecl(name.Lit)
					if err != nil {
						return nil, err
					}
					methods = append(methods, method)
				} else if p.peek().Kind == TkFn {
					method, err := p.parseMethodDecl(name.Lit, false)
					if err != nil {
						return nil, err
					}
					methods = append(methods, method)
				} else if p.peek().Kind == TkPub {
					p.advance()
					if p.peek().Kind == TkStatic {
						method, err := p.parseStaticMethodDecl(name.Lit)
						if err != nil {
							return nil, err
						}
						method.Public = true
						methods = append(methods, method)
					} else {
						method, err := p.parseMethodDecl(name.Lit, true)
						if err != nil {
							return nil, err
						}
						methods = append(methods, method)
					}
				} else {
					return nil, fmt.Errorf("%d:%d: expected tags, fun, or }, got %s", p.peek().Line, p.peek().Col, p.peek())
				}
			}
			p.advance() // skip '}'
		}
		return TypeDecl{Name: name.Lit, Params: params, Constructors: []Constructor{{Name: name.Lit, Fields: fields}}, Methods: methods, Tags: tags}, nil
	}

	if _, err := p.expect(TkLBrace); err != nil {
		return nil, err
	}
	var constructors []Constructor
	var methods []FnDecl
	var tags []TagRule
	for p.peek().Kind != TkRBrace {
		// Tags block
		if p.peek().Kind == TkTags {
			t, err := p.parseTagsBlock()
			if err != nil {
				return nil, err
			}
			tags = t
			continue
		}
		// Method: fn, static fn, pub fn, pub static fn
		if p.peek().Kind == TkStatic {
			method, err := p.parseStaticMethodDecl(name.Lit)
			if err != nil {
				return nil, err
			}
			methods = append(methods, method)
		} else if p.peek().Kind == TkFn {
			method, err := p.parseMethodDecl(name.Lit, false)
			if err != nil {
				return nil, err
			}
			methods = append(methods, method)
		} else if p.peek().Kind == TkPub {
			p.advance() // skip 'pub'
			if p.peek().Kind == TkStatic {
				method, err := p.parseStaticMethodDecl(name.Lit)
				if err != nil {
					return nil, err
				}
				method.Public = true
				methods = append(methods, method)
			} else if p.peek().Kind == TkFn {
				method, err := p.parseMethodDecl(name.Lit, true)
				if err != nil {
					return nil, err
				}
				methods = append(methods, method)
			} else {
				return nil, fmt.Errorf("%d:%d: expected fn or static after pub, got %s", p.peek().Line, p.peek().Col, p.peek())
			}
		} else {
			// Constructor
			ctor, err := p.parseConstructor()
			if err != nil {
				return nil, err
			}
			constructors = append(constructors, ctor)
		}
	}
	p.advance() // skip '}'
	return TypeDecl{Name: name.Lit, Params: params, Constructors: constructors, Methods: methods, Tags: tags}, nil
}

func (p *Parser) parseTagsBlock() ([]TagRule, error) {
	p.advance() // skip 'tags'
	if _, err := p.expect(TkLBrace); err != nil {
		return nil, err
	}
	var rules []TagRule
	for p.peek().Kind != TkRBrace {
		nameTok := p.advance()
		if nameTok.Kind != TkIdent {
			return nil, fmt.Errorf("%d:%d: expected tag name, got %s", nameTok.Line, nameTok.Col, nameTok)
		}
		rule := TagRule{Name: nameTok.Lit, Overrides: map[string]string{}}

		// Global rule: db(snake)
		if p.peek().Kind == TkLParen {
			p.advance() // skip '('
			caseTok := p.advance()
			rule.Case = caseTok.Lit
			if _, err := p.expect(TkRParen); err != nil {
				return nil, err
			}
		}

		// Individual overrides: db { userName: "user_name" }
		if p.peek().Kind == TkLBrace {
			p.advance() // skip '{'
			for p.peek().Kind != TkRBrace {
				key, err := p.expect(TkIdent)
				if err != nil {
					return nil, err
				}
				if _, err := p.expect(TkColon); err != nil {
					return nil, err
				}
				val, err := p.expect(TkString)
				if err != nil {
					return nil, err
				}
				rule.Overrides[key.Lit] = val.Lit
				if p.peek().Kind == TkComma {
					p.advance()
				}
			}
			p.advance() // skip '}'
		}

		rules = append(rules, rule)
		if p.peek().Kind == TkComma {
			p.advance()
		}
	}
	p.advance() // skip '}'
	return rules, nil
}

func (p *Parser) parseConstructor() (Constructor, error) {
	name, err := p.expect(TkUpperIdent)
	if err != nil {
		return Constructor{}, err
	}
	var fields []Field
	if p.peek().Kind == TkLParen {
		p.advance()
		for p.peek().Kind != TkRParen {
			field, err := p.parseField()
			if err != nil {
				return Constructor{}, err
			}
			fields = append(fields, field)
			if p.peek().Kind == TkComma {
				p.advance()
			}
		}
		p.advance()
	}
	return Constructor{Name: name.Lit, Fields: fields}, nil
}

func (p *Parser) parseField() (Field, error) {
	name, err := p.expect(TkIdent)
	if err != nil {
		return Field{}, err
	}
	if _, err := p.expect(TkColon); err != nil {
		return Field{}, err
	}
	typ, err := p.parseType()
	if err != nil {
		return Field{}, err
	}
	return Field{Name: name.Lit, Type: typ}, nil
}

func (p *Parser) parseType() (Type, error) {
	if p.peek().Kind == TkLParen {
		return p.parseTupleType()
	}
	// Pointer type: *T
	if p.peek().Kind == TkStar {
		p.advance()
		inner, err := p.parseType()
		if err != nil {
			return nil, err
		}
		return PointerType{Inner: inner}, nil
	}
	tok := p.advance()
	pos := Pos{tok.Line, tok.Col}
	switch tok.Kind {
	case TkUpperIdent, TkIdent:
		name := tok.Lit
		// Qualified type: module.Type
		for p.peek().Kind == TkDot {
			p.advance()
			next := p.advance()
			name += "." + next.Lit
		}
		if p.peek().Kind == TkLBracket {
			p.advance() // skip '['
			var params []Type
			for p.peek().Kind != TkRBracket {
				param, err := p.parseType()
				if err != nil {
					return nil, err
				}
				params = append(params, param)
				if p.peek().Kind == TkComma {
					p.advance()
				}
			}
			p.advance() // skip ']'
			constraints, err := p.tryParseConstraints()
			if err != nil {
				return nil, err
			}
			return NamedType{Pos: pos, Name: name, Params: params, Constraints: constraints}, nil
		}
		constraints, err := p.tryParseConstraints()
		if err != nil {
			return nil, err
		}
		return NamedType{Pos: pos, Name: name, Constraints: constraints}, nil
	default:
		return nil, fmt.Errorf("%d:%d: expected type, got %s", tok.Line, tok.Col, tok)
	}
}

func (p *Parser) tryParseConstraints() ([]Constraint, error) {
	// Check if { follows and contains key: value pairs
	if p.peek().Kind != TkLBrace {
		return nil, nil
	}
	// Look ahead to distinguish constraints {key: val} from block {expr}
	if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == TkIdent {
		if p.pos+2 < len(p.tokens) && p.tokens[p.pos+2].Kind == TkColon {
			// It's a constraint block
			return p.parseConstraints()
		}
	}
	return nil, nil
}

func (p *Parser) parseConstraints() ([]Constraint, error) {
	p.advance() // skip '{'
	var constraints []Constraint
	for p.peek().Kind != TkRBrace {
		key, err := p.expect(TkIdent)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(TkColon); err != nil {
			return nil, err
		}
		value, err := p.parsePrimaryExpr()
		if err != nil {
			return nil, err
		}
		constraints = append(constraints, Constraint{Key: key.Lit, Value: value})
		if p.peek().Kind == TkComma {
			p.advance()
		}
	}
	p.advance() // skip '}'
	return constraints, nil
}

func (p *Parser) parseTupleType() (Type, error) {
	p.advance() // skip '('
	var elements []Type
	for p.peek().Kind != TkRParen {
		t, err := p.parseType()
		if err != nil {
			return nil, err
		}
		elements = append(elements, t)
		if p.peek().Kind == TkComma {
			p.advance()
		}
	}
	p.advance() // skip ')'
	return TupleType{Elements: elements}, nil
}

func (p *Parser) parseStaticMethodDecl(receiverType string) (FnDecl, error) {
	p.advance() // skip 'static'
	fd, err := p.parseMethodDecl(receiverType, false)
	if err != nil {
		return fd, err
	}
	fd.Static = true
	return fd, nil
}

func (p *Parser) parseMethodDecl(receiverType string, public bool) (FnDecl, error) {
	pos := Pos{p.peek().Line, p.peek().Col}
	p.advance() // skip 'fn'
	name, err := p.expect(TkIdent)
	if err != nil {
		return FnDecl{}, err
	}
	if _, err := p.expect(TkLParen); err != nil {
		return FnDecl{}, err
	}
	var params []FnParam
	for p.peek().Kind != TkRParen {
		param, err := p.parseFnParam()
		if err != nil {
			return FnDecl{}, err
		}
		params = append(params, param)
		if p.peek().Kind == TkComma {
			p.advance()
		}
	}
	p.advance() // skip ')'

	var retType Type
	if p.peek().Kind == TkArrow {
		p.advance()
		retType, err = p.parseType()
		if err != nil {
			return FnDecl{}, err
		}
	}

	body, err := p.parseBlockExpr()
	if err != nil {
		return FnDecl{}, err
	}
	return FnDecl{Pos: pos, Name: name.Lit, Public: public, ReceiverType: receiverType, Params: params, ReturnType: retType, Body: body}, nil
}

func (p *Parser) parseFnDecl(public bool) (Decl, error) {
	pos := Pos{p.peek().Line, p.peek().Col}
	p.advance() // skip 'fn'
	name, err := p.expect(TkIdent)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(TkLParen); err != nil {
		return nil, err
	}
	var params []FnParam
	for p.peek().Kind != TkRParen {
		param, err := p.parseFnParam()
		if err != nil {
			return nil, err
		}
		params = append(params, param)
		if p.peek().Kind == TkComma {
			p.advance()
		}
	}
	p.advance() // skip ')'

	var retType Type
	if p.peek().Kind == TkArrow {
		p.advance()
		retType, err = p.parseType()
		if err != nil {
			return nil, err
		}
	}

	body, err := p.parseBlockExpr()
	if err != nil {
		return nil, err
	}
	return FnDecl{Pos: pos, Name: name.Lit, Public: public, Params: params, ReturnType: retType, Body: body}, nil
}

func (p *Parser) parseFnParam() (FnParam, error) {
	name, err := p.expect(TkIdent)
	if err != nil {
		return FnParam{}, err
	}
	if _, err := p.expect(TkColon); err != nil {
		return FnParam{}, err
	}
	typ, err := p.parseType()
	if err != nil {
		return FnParam{}, err
	}
	return FnParam{Name: name.Lit, Type: typ}, nil
}

func (p *Parser) parseBlockExpr() (Expr, error) {
	openTok, err := p.expect(TkLBrace)
	if err != nil {
		return nil, err
	}
	startPos := Pos{openTok.Line, openTok.Col}
	var stmts []Stmt
	var lastExpr Expr

	for p.peek().Kind != TkRBrace {
		if p.peek().Kind == TkLet {
			stmt, err := p.parseLetStmt()
			if err != nil {
				return nil, err
			}
			stmts = append(stmts, stmt)
		} else if p.peek().Kind == TkFor {
			expr, err := p.parseForExpr()
			if err != nil {
				return nil, err
			}
			stmts = append(stmts, ExprStmt{Expr: expr})
		} else if p.peek().Kind == TkAssert {
			p.advance() // skip 'assert'
			expr, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			stmts = append(stmts, AssertStmt{Expr: expr})
		} else if p.peek().Kind == TkDefer {
			p.advance() // skip 'defer'
			expr, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			stmts = append(stmts, DeferStmt{Expr: expr})
		} else {
			expr, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if p.peek().Kind != TkRBrace {
				stmts = append(stmts, ExprStmt{Expr: expr})
			} else {
				lastExpr = expr
			}
		}
	}
	closeTok := p.advance() // skip '}'
	endPos := Pos{closeTok.Line, closeTok.Col}

	if len(stmts) == 0 && lastExpr != nil {
		return lastExpr, nil
	}
	return Block{Pos: startPos, EndPos: endPos, Stmts: stmts, Expr: lastExpr}, nil
}

func (p *Parser) parseLetStmt() (Stmt, error) {
	p.advance() // skip 'let'

	// Check for tuple destructuring: let (x, y) = expr
	if p.peek().Kind == TkLParen {
		saved := p.pos
		p.advance() // skip '('
		var names []string
		isTuplePat := true
		for p.peek().Kind != TkRParen {
			if p.peek().Kind == TkIdent {
				names = append(names, p.advance().Lit)
				if p.peek().Kind == TkComma {
					p.advance()
				}
			} else {
				isTuplePat = false
				break
			}
		}
		if isTuplePat && p.peek().Kind == TkRParen {
			p.advance() // skip ')'
			if p.peek().Kind == TkEq {
				p.advance() // skip '='
				value, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				var pats []Pattern
				for _, n := range names {
					pats = append(pats, BindPattern{Name: n})
				}
				return LetStmt{Pattern: TuplePattern{Elements: pats}, Value: value}, nil
			}
		}
		p.pos = saved
	}

	// Check for list destructuring: let [first, ..rest] = expr
	if p.peek().Kind == TkLBracket {
		pat, err := p.parseListPattern()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(TkEq); err != nil {
			return nil, err
		}
		value, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return LetStmt{Pattern: pat, Value: value}, nil
	}

	// let _ = expr (discard)
	if p.peek().Kind == TkUnderscore {
		p.advance()
		if _, err := p.expect(TkEq); err != nil {
			return nil, err
		}
		value, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return LetStmt{Name: "_", Value: value}, nil
	}

	name, err := p.expect(TkIdent)
	if err != nil {
		return nil, err
	}
	// Optional type annotation: let name: Type = expr
	var typ Type
	if p.peek().Kind == TkColon {
		p.advance() // skip ':'
		typ, err = p.parseType()
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(TkEq); err != nil {
		return nil, err
	}
	value, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return LetStmt{Name: name.Lit, Type: typ, Value: value}, nil
}

func (p *Parser) parseForExpr() (Expr, error) {
	p.advance() // skip 'for'
	binding, err := p.expect(TkIdent)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(TkIn); err != nil {
		return nil, err
	}
	iter, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	body, err := p.parseBlockExpr()
	if err != nil {
		return nil, err
	}
	return ForExpr{Binding: binding.Lit, Iter: iter, Body: body}, nil
}

func isBinaryOp(kind TokenKind) bool {
	switch kind {
	case TkPlus, TkMinus, TkStar, TkSlash, TkPercent,
		TkEqEq, TkNotEq, TkLt, TkGt, TkLtEq, TkGtEq,
		TkAnd, TkOr:
		return true
	}
	return false
}

func (p *Parser) parseExpr() (Expr, error) {
	expr, err := p.parsePrimaryExpr()
	if err != nil {
		return nil, err
	}

	// Binary operators
	for isBinaryOp(p.peek().Kind) {
		op := p.advance()
		right, err := p.parsePrimaryExpr()
		if err != nil {
			return nil, err
		}
		expr = BinaryExpr{Op: op.Lit, Left: expr, Right: right}
	}

	// ? operator
	if p.peek().Kind == TkQuestion {
		p.advance()
		expr = FnCall{Pos: Pos{p.peek().Line, p.peek().Col}, Fn: Ident{Name: "__try", Pos: Pos{p.peek().Line, p.peek().Col}}, Args: []Expr{expr}}
	}

	// Pipe operator
	for p.peek().Kind == TkPipe {
		pipePos := Pos{p.peek().Line, p.peek().Col}
		p.advance()
		right, err := p.parsePrimaryExpr()
		if err != nil {
			return nil, err
		}
		if call, ok := right.(FnCall); ok {
			call.Args = append([]Expr{expr}, call.Args...)
			expr = call
		} else {
			expr = FnCall{Pos: pipePos, Fn: right, Args: []Expr{expr}}
		}
	}

	// Range operator
	if p.peek().Kind == TkDotDot {
		p.advance()
		end, err := p.parsePrimaryExpr()
		if err != nil {
			return nil, err
		}
		expr = RangeExpr{Start: expr, End: end}
	}

	return expr, nil
}

func (p *Parser) parsePrimaryExpr() (Expr, error) {
	tok := p.peek()

	switch tok.Kind {
	case TkAmpersand:
		p.advance()
		expr, err := p.parsePrimaryExpr()
		if err != nil {
			return nil, err
		}
		return RefExpr{Expr: expr}, nil

	case TkInt:
		p.advance()
		val, _ := strconv.ParseInt(tok.Lit, 10, 64)
		return IntLit{Value: val}, nil

	case TkFloat:
		p.advance()
		val, _ := strconv.ParseFloat(tok.Lit, 64)
		return FloatLit{Value: val}, nil

	case TkString:
		p.advance()
		return StringLit{Value: tok.Lit, Multiline: strings.Contains(tok.Lit, "\n")}, nil

	case TkStringInterpStart:
		return p.parseStringInterp()

	case TkTrue:
		p.advance()
		return BoolLit{Value: true}, nil

	case TkFalse:
		p.advance()
		return BoolLit{Value: false}, nil

	case TkMatch:
		return p.parseMatchExpr()

	case TkUpperIdent:
		expr, err := p.parseConstructorOrIdent()
		if err != nil {
			return nil, err
		}
		// Postfix chain: .field, .method()
		for p.peek().Kind == TkDot {
			p.advance()
			field := p.advance()
			if field.Kind != TkIdent && field.Kind != TkUpperIdent {
				return nil, fmt.Errorf("%d:%d: expected field name, got %s", field.Line, field.Col, field)
			}
			expr = FieldAccess{Expr: expr, Field: field.Lit}
			if p.peek().Kind == TkLParen {
				p.advance()
				var args []Expr
				for p.peek().Kind != TkRParen {
					arg, err := p.parseExpr()
					if err != nil {
						return nil, err
					}
					args = append(args, arg)
					if p.peek().Kind == TkComma {
						p.advance()
					}
				}
				p.advance()
				expr = FnCall{Pos: Pos{tok.Line, tok.Col}, Fn: expr, Args: args}
			}
		}
		return expr, nil

	case TkLParen:
		return p.parseTupleOrLambda()

	case TkLBracket:
		return p.parseListLit()

	case TkIdent:
		p.advance()
		expr := Expr(Ident{Name: tok.Lit, Pos: Pos{tok.Line, tok.Col}})
		for {
			if p.peek().Kind == TkLParen {
				p.advance()
				var args []Expr
				for p.peek().Kind != TkRParen {
					arg, err := p.parseExpr()
					if err != nil {
						return nil, err
					}
					args = append(args, arg)
					if p.peek().Kind == TkComma {
						p.advance()
					}
				}
				p.advance()
				expr = FnCall{Pos: Pos{tok.Line, tok.Col}, Fn: expr, Args: args}
			} else if p.peek().Kind == TkDot {
				p.advance()
				field := p.advance()
				if field.Kind != TkIdent && field.Kind != TkUpperIdent {
					return nil, fmt.Errorf("%d:%d: expected field name, got %s", field.Line, field.Col, field)
				}
				expr = FieldAccess{Expr: expr, Field: field.Lit}
			} else {
				break
			}
		}
		return expr, nil

	case TkLBrace:
		return p.parseBlockExpr()

	default:
		return nil, fmt.Errorf("%d:%d: expected expression, got %s", tok.Line, tok.Col, tok)
	}
}

func (p *Parser) parseListLit() (Expr, error) {
	p.advance() // skip '['
	var elements []Expr
	var spread Expr
	for p.peek().Kind != TkRBracket {
		// Check for ..spread
		if p.peek().Kind == TkDotDot {
			p.advance()
			var err error
			spread, err = p.parseExpr()
			if err != nil {
				return nil, err
			}
			break
		}
		elem, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		elements = append(elements, elem)
		if p.peek().Kind == TkComma {
			p.advance()
		}
	}
	p.advance() // skip ']'
	return ListLit{Elements: elements, Spread: spread}, nil
}

func (p *Parser) parseStringInterp() (Expr, error) {
	p.advance() // skip InterpStart
	var parts []Expr
	multiline := false
	for p.peek().Kind != TkStringInterpEnd {
		if p.peek().Kind == TkString {
			tok := p.advance()
			if strings.Contains(tok.Lit, "\n") {
				multiline = true
			}
			parts = append(parts, StringLit{Value: tok.Lit, Multiline: multiline})
		} else {
			expr, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			parts = append(parts, expr)
		}
	}
	p.advance() // skip InterpEnd
	return StringInterp{Parts: parts, Multiline: multiline}, nil
}

func (p *Parser) parseTupleOrLambda() (Expr, error) {
	saved := p.pos
	p.advance() // skip '('

	// () -> Type => ... or () => ...
	if p.peek().Kind == TkRParen {
		p.advance()
		var retType Type
		if p.peek().Kind == TkArrow {
			p.advance()
			var err error
			retType, err = p.parseType()
			if err != nil {
				return nil, err
			}
		}
		if p.peek().Kind == TkFatArrow {
			p.advance()
			body, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			return Lambda{Params: nil, ReturnType: retType, Body: body}, nil
		}
		return TupleExpr{Elements: nil}, nil
	}

	// Try lambda: (ident[: Type], ...) =>
	if p.peek().Kind == TkIdent {
		type lambdaParam struct {
			name string
			typ  Type
		}
		var lparams []lambdaParam
		lp := lambdaParam{name: p.advance().Lit}
		if p.peek().Kind == TkColon {
			p.advance()
			t, err := p.parseType()
			if err != nil {
				p.pos = saved
				goto parseTuple
			}
			lp.typ = t
		}
		lparams = append(lparams, lp)
		isLambda := true
		for p.peek().Kind == TkComma {
			p.advance()
			if p.peek().Kind == TkIdent {
				lp2 := lambdaParam{name: p.advance().Lit}
				if p.peek().Kind == TkColon {
					p.advance()
					t, err := p.parseType()
					if err != nil {
						isLambda = false
						break
					}
					lp2.typ = t
				}
				lparams = append(lparams, lp2)
			} else {
				isLambda = false
				break
			}
		}
		if isLambda && p.peek().Kind == TkRParen {
			p.advance()
			var retType Type
			if p.peek().Kind == TkArrow {
				p.advance()
				t, err := p.parseType()
				if err != nil {
					p.pos = saved
					goto parseTuple
				}
				retType = t
			}
			if p.peek().Kind == TkFatArrow {
				p.advance()
				body, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				var params []LambdaParam
				for _, lp := range lparams {
					params = append(params, LambdaParam{Name: lp.name, Type: lp.typ})
				}
				return Lambda{Params: params, ReturnType: retType, Body: body}, nil
			}
		}
		p.pos = saved
	}
parseTuple:

	// Parse as tuple
	p.pos = saved
	p.advance() // skip '('
	var elements []Expr
	for p.peek().Kind != TkRParen {
		elem, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		elements = append(elements, elem)
		if p.peek().Kind == TkComma {
			p.advance()
		}
	}
	p.advance()
	return TupleExpr{Elements: elements}, nil
}

var builtinConstructors = map[string]bool{
	"Ok": true, "Error": true, "Some": true, "None": true,
}

func (p *Parser) parseConstructorOrIdent() (Expr, error) {
	name := p.advance()

	if p.peek().Kind == TkDot {
		p.advance()
		member := p.advance()

		// Type.Constructor — qualified constructor (with or without args)
		if len(member.Lit) > 0 && member.Lit[0] >= 'A' && member.Lit[0] <= 'Z' {
			if p.peek().Kind == TkLParen {
				return p.parseQualifiedConstructor(name, member)
			}
			// Enum variant: Color.Red (no parens)
			return ConstructorCall{
				Pos:      Pos{name.Line, name.Col},
				TypeName: name.Lit,
				Name:     member.Lit,
			}, nil
		}

		// Otherwise: field access or method call (e.g. foo.bar, fmt.Println(...))
		qualifiedName := name.Lit + "." + member.Lit
		expr := Expr(Ident{Name: qualifiedName, Pos: Pos{name.Line, name.Col}})
		if p.peek().Kind == TkLParen {
			p.advance()
			var args []Expr
			for p.peek().Kind != TkRParen {
				arg, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				args = append(args, arg)
				if p.peek().Kind == TkComma {
					p.advance()
				}
			}
			p.advance()
			return FnCall{Pos: Pos{name.Line, name.Col}, Fn: expr, Args: args}, nil
		}
		return expr, nil
	}

	if p.peek().Kind == TkLParen {
		// Builtin constructors: Ok(...), Error(...), Some(...), None(...)
		if builtinConstructors[name.Lit] {
			return p.parseBuiltinConstructor(name)
		}
		// Unqualified constructor: single-constructor type or type alias
		if len(name.Lit) > 0 && name.Lit[0] >= 'A' && name.Lit[0] <= 'Z' {
			return p.parseUnqualifiedConstructor(name)
		}
	}

	return Ident{Name: name.Lit, Pos: Pos{name.Line, name.Col}}, nil
}

func (p *Parser) parseQualifiedConstructor(typeName Token, ctorName Token) (Expr, error) {
	p.advance() // skip '('
	fields, err := p.parseFieldValues()
	if err != nil {
		return nil, err
	}
	return ConstructorCall{
		Pos:      Pos{typeName.Line, typeName.Col},
		TypeName: typeName.Lit,
		Name:     ctorName.Lit,
		Fields:   fields,
	}, nil
}

func (p *Parser) parseBuiltinConstructor(name Token) (Expr, error) {
	p.advance() // skip '('
	fields, err := p.parseFieldValues()
	if err != nil {
		return nil, err
	}
	return ConstructorCall{
		Pos:    Pos{name.Line, name.Col},
		Name:   name.Lit,
		Fields: fields,
	}, nil
}

func (p *Parser) parseUnqualifiedConstructor(name Token) (Expr, error) {
	p.advance() // skip '('
	fields, err := p.parseFieldValues()
	if err != nil {
		return nil, err
	}
	return ConstructorCall{
		Pos:    Pos{name.Line, name.Col},
		Name:   name.Lit,
		Fields: fields,
	}, nil
}

func (p *Parser) parseFieldValues() ([]FieldValue, error) {
	var fields []FieldValue
	for p.peek().Kind != TkRParen {
		if p.peek().Kind == TkIdent && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == TkColon {
			fieldName := p.advance()
			p.advance() // skip ':'
			value, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			fields = append(fields, FieldValue{Name: fieldName.Lit, Value: value})
		} else {
			value, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			fields = append(fields, FieldValue{Value: value})
		}
		if p.peek().Kind == TkComma {
			p.advance()
		}
	}
	p.advance() // skip ')'
	return fields, nil
}

func (p *Parser) parseMatchExpr() (Expr, error) {
	pos := Pos{p.peek().Line, p.peek().Col}
	p.advance() // skip 'match'
	subject, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(TkLBrace); err != nil {
		return nil, err
	}
	var arms []MatchArm
	for p.peek().Kind != TkRBrace {
		arm, err := p.parseMatchArm()
		if err != nil {
			return nil, err
		}
		arms = append(arms, arm)
	}
	p.advance()
	return MatchExpr{Pos: pos, Subject: subject, Arms: arms}, nil
}

func (p *Parser) parseMatchArm() (MatchArm, error) {
	startTok := p.peek()
	startPos := Pos{startTok.Line, startTok.Col}
	pattern, err := p.parsePattern()
	if err != nil {
		return MatchArm{}, err
	}
	if _, err := p.expect(TkArrow); err != nil {
		return MatchArm{}, err
	}
	body, err := p.parseExpr()
	if err != nil {
		return MatchArm{}, err
	}
	// End position: use block EndPos if available, otherwise use current position
	endPos := Pos{p.peek().Line, p.peek().Col}
	if b, ok := body.(Block); ok {
		endPos = b.EndPos
	}
	return MatchArm{Pos: startPos, EndPos: endPos, Pattern: pattern, Body: body}, nil
}

func (p *Parser) parsePattern() (Pattern, error) {
	tok := p.peek()
	switch tok.Kind {
	case TkUnderscore:
		p.advance()
		return WildcardPattern{}, nil
	case TkUpperIdent:
		p.advance()
		if p.peek().Kind == TkLParen {
			p.advance()
			var fields []FieldPattern
			for p.peek().Kind != TkRParen {
				field, err := p.parseFieldPattern()
				if err != nil {
					return nil, err
				}
				fields = append(fields, field)
				if p.peek().Kind == TkComma {
					p.advance()
				}
			}
			p.advance()
			return ConstructorPattern{Name: tok.Lit, Fields: fields}, nil
		}
		return ConstructorPattern{Name: tok.Lit}, nil
	case TkLBracket:
		return p.parseListPattern()
	case TkIdent:
		p.advance()
		return BindPattern{Name: tok.Lit}, nil
	case TkString:
		p.advance()
		return LitPattern{Expr: StringLit{Value: tok.Lit}}, nil
	case TkInt:
		p.advance()
		val, _ := strconv.ParseInt(tok.Lit, 10, 64)
		return LitPattern{Expr: IntLit{Value: val}}, nil
	default:
		return nil, fmt.Errorf("%d:%d: expected pattern, got %s", tok.Line, tok.Col, tok)
	}
}

func (p *Parser) parseListPattern() (Pattern, error) {
	p.advance() // skip '['
	if p.peek().Kind == TkRBracket {
		p.advance()
		return ListPattern{}, nil // empty list pattern []
	}
	var elements []Pattern
	rest := ""
	for p.peek().Kind != TkRBracket {
		// Check for ..rest
		if p.peek().Kind == TkDotDot {
			p.advance()
			tok, err := p.expect(TkIdent)
			if err != nil {
				return nil, err
			}
			rest = tok.Lit
			break
		}
		pat, err := p.parsePattern()
		if err != nil {
			return nil, err
		}
		elements = append(elements, pat)
		if p.peek().Kind == TkComma {
			p.advance()
		}
	}
	if _, err := p.expect(TkRBracket); err != nil {
		return nil, err
	}
	return ListPattern{Elements: elements, Rest: rest}, nil
}

func (p *Parser) parseFieldPattern() (FieldPattern, error) {
	if p.peek().Kind == TkUnderscore {
		p.advance()
		return FieldPattern{Name: "_", Binding: "_"}, nil
	}
	name, err := p.expect(TkIdent)
	if err != nil {
		return FieldPattern{}, err
	}
	return FieldPattern{Name: name.Lit, Binding: name.Lit}, nil
}
