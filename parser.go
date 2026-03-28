package main

import (
	"fmt"
	"strconv"
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
	case TkType:
		return p.parseTypeDecl()
	case TkFn:
		return p.parseFnDecl()
	default:
		return nil, fmt.Errorf("%d:%d: expected type or fn, got %s", p.peek().Line, p.peek().Col, p.peek())
	}
}

func (p *Parser) parseTypeDecl() (Decl, error) {
	p.advance() // skip 'type'
	name, err := p.expect(TkUpperIdent)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(TkLBrace); err != nil {
		return nil, err
	}

	var constructors []Constructor
	for p.peek().Kind != TkRBrace {
		ctor, err := p.parseConstructor()
		if err != nil {
			return nil, err
		}
		constructors = append(constructors, ctor)
	}
	p.advance() // skip '}'
	return TypeDecl{Name: name.Lit, Constructors: constructors}, nil
}

func (p *Parser) parseConstructor() (Constructor, error) {
	name, err := p.expect(TkUpperIdent)
	if err != nil {
		return Constructor{}, err
	}
	var fields []Field
	if p.peek().Kind == TkLParen {
		p.advance() // skip '('
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
		p.advance() // skip ')'
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
	tok := p.advance()
	switch tok.Kind {
	case TkUpperIdent:
		name := tok.Lit
		// Check for type parameters: Option(String)
		if p.peek().Kind == TkLParen {
			p.advance() // skip '('
			var params []Type
			for p.peek().Kind != TkRParen {
				param, err := p.parseType()
				if err != nil {
					return nil, err
				}
				params = append(params, param)
				if p.peek().Kind == TkComma {
					p.advance()
				}
			}
			p.advance() // skip ')'
			return NamedType{Name: name, Params: params}, nil
		}
		return NamedType{Name: name}, nil
	case TkIdent:
		return NamedType{Name: tok.Lit}, nil
	default:
		return nil, fmt.Errorf("%d:%d: expected type, got %s", tok.Line, tok.Col, tok)
	}
}

func (p *Parser) parseFnDecl() (Decl, error) {
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

	if _, err := p.expect(TkArrow); err != nil {
		return nil, err
	}
	retType, err := p.parseType()
	if err != nil {
		return nil, err
	}

	body, err := p.parseBlockExpr()
	if err != nil {
		return nil, err
	}

	return FnDecl{Name: name.Lit, Params: params, ReturnType: retType, Body: body}, nil
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
	if _, err := p.expect(TkLBrace); err != nil {
		return nil, err
	}

	var stmts []Stmt
	var lastExpr Expr

	for p.peek().Kind != TkRBrace {
		if p.peek().Kind == TkLet {
			stmt, err := p.parseLetStmt()
			if err != nil {
				return nil, err
			}
			stmts = append(stmts, stmt)
		} else {
			expr, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			lastExpr = expr
		}
	}
	p.advance() // skip '}'

	if len(stmts) == 0 && lastExpr != nil {
		return lastExpr, nil
	}
	return Block{Stmts: stmts, Expr: lastExpr}, nil
}

func (p *Parser) parseLetStmt() (Stmt, error) {
	p.advance() // skip 'let'
	name, err := p.expect(TkIdent)
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
	return LetStmt{Name: name.Lit, Value: value}, nil
}

func (p *Parser) parseExpr() (Expr, error) {
	expr, err := p.parsePrimaryExpr()
	if err != nil {
		return nil, err
	}

	// Handle pipe operator: expr |> fn
	for p.peek().Kind == TkPipe {
		p.advance() // skip |>
		right, err := p.parsePrimaryExpr()
		if err != nil {
			return nil, err
		}
		expr = FnCall{Fn: right, Args: []Expr{expr}}
	}

	return expr, nil
}

func (p *Parser) parsePrimaryExpr() (Expr, error) {
	tok := p.peek()

	switch tok.Kind {
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
		return StringLit{Value: tok.Lit}, nil

	case TkTrue:
		p.advance()
		return BoolLit{Value: true}, nil

	case TkFalse:
		p.advance()
		return BoolLit{Value: false}, nil

	case TkMatch:
		return p.parseMatchExpr()

	case TkUpperIdent:
		return p.parseConstructorOrIdent()

	case TkIdent:
		p.advance()
		expr := Expr(Ident{Name: tok.Lit})
		// Handle function call: foo(args)
		if p.peek().Kind == TkLParen {
			p.advance() // skip '('
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
			p.advance() // skip ')'
			expr = FnCall{Fn: Ident{Name: tok.Lit}, Args: args}
		}
		// Handle field access: expr.field
		for p.peek().Kind == TkDot {
			p.advance()
			field, err := p.expect(TkIdent)
			if err != nil {
				return nil, err
			}
			expr = FieldAccess{Expr: expr, Field: field.Lit}
		}
		return expr, nil

	case TkLBrace:
		return p.parseBlockExpr()

	default:
		return nil, fmt.Errorf("%d:%d: expected expression, got %s", tok.Line, tok.Col, tok)
	}
}

func (p *Parser) parseConstructorOrIdent() (Expr, error) {
	name := p.advance() // UpperIdent

	if p.peek().Kind == TkLParen {
		p.advance() // skip '('
		var fields []FieldValue
		for p.peek().Kind != TkRParen {
			// Check if it's name: value or positional
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
		return ConstructorCall{Name: name.Lit, Fields: fields}, nil
	}

	return Ident{Name: name.Lit}, nil
}

func (p *Parser) parseMatchExpr() (Expr, error) {
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
	p.advance() // skip '}'
	return MatchExpr{Subject: subject, Arms: arms}, nil
}

func (p *Parser) parseMatchArm() (MatchArm, error) {
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
	return MatchArm{Pattern: pattern, Body: body}, nil
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
			p.advance() // skip '('
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
			p.advance() // skip ')'
			return ConstructorPattern{Name: tok.Lit, Fields: fields}, nil
		}
		return ConstructorPattern{Name: tok.Lit}, nil

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

func (p *Parser) parseFieldPattern() (FieldPattern, error) {
	name, err := p.expect(TkIdent)
	if err != nil {
		return FieldPattern{}, err
	}
	// For now, field name is also the binding
	return FieldPattern{Name: name.Lit, Binding: name.Lit}, nil
}
