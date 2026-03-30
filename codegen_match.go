package main

import "fmt"

// --- Match Expression ---

func (cg *CodeGen) isResultMatch(me MatchExpr) bool {
	for _, arm := range me.Arms {
		if cp, ok := arm.Pattern.(ConstructorPattern); ok {
			if cp.Name == "Ok" || cp.Name == "Error" {
				if _, isUserCtor := cg.ctorTypes[cp.Name]; !isUserCtor {
					return true
				}
			}
		}
	}
	return false
}

func (cg *CodeGen) genResultMatch(me MatchExpr, indent string, isReturn bool) {
	subject := cg.genExprStr(me.Subject)
	for _, arm := range me.Arms {
		cp, ok := arm.Pattern.(ConstructorPattern)
		if !ok {
			continue
		}
		usedVars := collectUsedIdents(arm.Body)
		if cp.Name == "Ok" {
			cg.writeln(fmt.Sprintf("%sif %s.IsOk {", indent, subject))
			if len(cp.Fields) > 0 {
				if _, used := usedVars[cp.Fields[0].Binding]; used {
					cg.writeln(fmt.Sprintf("%s\t%s := %s.Value", indent, snakeToCamel(cp.Fields[0].Binding), subject))
				}
			}
			cg.genArmBody(arm.Body, indent+"\t", isReturn)
		}
		if cp.Name == "Error" {
			cg.writeln(fmt.Sprintf("%s} else {", indent))
			if len(cp.Fields) > 0 {
				if _, used := usedVars[cp.Fields[0].Binding]; used {
					cg.writeln(fmt.Sprintf("%s\t%s := %s.Err", indent, snakeToCamel(cp.Fields[0].Binding), subject))
				}
			}
			cg.genArmBody(arm.Body, indent+"\t", isReturn)
		}
	}
	cg.writeln(fmt.Sprintf("%s}", indent))
}

func (cg *CodeGen) isOptionMatch(me MatchExpr) bool {
	for _, arm := range me.Arms {
		if cp, ok := arm.Pattern.(ConstructorPattern); ok {
			if cp.Name == "Some" || cp.Name == "None" {
				return true
			}
		}
	}
	return false
}

func (cg *CodeGen) genOptionMatch(me MatchExpr, indent string, isReturn bool) {
	subject := cg.genExprStr(me.Subject)
	for _, arm := range me.Arms {
		cp, ok := arm.Pattern.(ConstructorPattern)
		if !ok {
			continue
		}
		if cp.Name == "Some" {
			cg.writeln(fmt.Sprintf("%sif %s.Valid {", indent, subject))
			if len(cp.Fields) > 0 {
				cg.writeln(fmt.Sprintf("%s\t%s := %s.Value", indent, snakeToCamel(cp.Fields[0].Binding), subject))
			}
			if isReturn {
				cg.writeln(fmt.Sprintf("%s\treturn %s", indent, cg.genExprStr(arm.Body)))
			} else {
				cg.writeln(fmt.Sprintf("%s\t%s", indent, cg.genExprStr(arm.Body)))
			}
		}
		if cp.Name == "None" {
			cg.writeln(fmt.Sprintf("%s} else {", indent))
			if isReturn {
				cg.writeln(fmt.Sprintf("%s\treturn %s", indent, cg.genExprStr(arm.Body)))
			} else {
				cg.writeln(fmt.Sprintf("%s\t%s", indent, cg.genExprStr(arm.Body)))
			}
		}
	}
	cg.writeln(fmt.Sprintf("%s}", indent))
}

func (cg *CodeGen) isListMatch(me MatchExpr) bool {
	for _, arm := range me.Arms {
		if _, ok := arm.Pattern.(ListPattern); ok {
			return true
		}
	}
	return false
}

func (cg *CodeGen) genListMatch(me MatchExpr, indent string, isReturn bool) {
	subject := cg.genExprStr(me.Subject)
	first := true
	for _, arm := range me.Arms {
		lp, ok := arm.Pattern.(ListPattern)
		if !ok {
			if first {
				cg.writeln(fmt.Sprintf("%s{", indent))
			} else {
				cg.writeln(fmt.Sprintf("%s} else {", indent))
			}
			if isReturn {
				cg.writeln(fmt.Sprintf("%s\treturn %s", indent, cg.genExprStr(arm.Body)))
			} else {
				cg.writeln(fmt.Sprintf("%s\t%s", indent, cg.genExprStr(arm.Body)))
			}
			first = false
			continue
		}

		if len(lp.Elements) == 0 && lp.Rest == "" {
			keyword := "if"
			if !first {
				keyword = "} else if"
			}
			cg.writeln(fmt.Sprintf("%s%s len(%s) == 0 {", indent, keyword, subject))
		} else {
			keyword := "if"
			if !first {
				keyword = "} else if"
			}
			minLen := len(lp.Elements)
			if lp.Rest != "" {
				cg.writeln(fmt.Sprintf("%s%s len(%s) >= %d {", indent, keyword, subject, minLen))
			} else {
				cg.writeln(fmt.Sprintf("%s%s len(%s) == %d {", indent, keyword, subject, minLen))
			}
			usedVars := collectUsedIdents(arm.Body)
			for i, elemPat := range lp.Elements {
				if bp, ok := elemPat.(BindPattern); ok {
					if _, used := usedVars[bp.Name]; used {
						cg.writeln(fmt.Sprintf("%s\t%s := %s[%d]", indent, snakeToCamel(bp.Name), subject, i))
					}
				}
			}
			if lp.Rest != "" {
				if _, used := usedVars[lp.Rest]; used {
					cg.writeln(fmt.Sprintf("%s\t%s := %s[%d:]", indent, snakeToCamel(lp.Rest), subject, minLen))
				}
			}
		}
		if isReturn {
			cg.writeln(fmt.Sprintf("%s\treturn %s", indent, cg.genExprStr(arm.Body)))
		} else {
			cg.writeln(fmt.Sprintf("%s\t%s", indent, cg.genExprStr(arm.Body)))
		}
		first = false
	}
	cg.writeln(fmt.Sprintf("%s}", indent))
	if isReturn {
		cg.writeln(fmt.Sprintf("%spanic(\"unreachable\")", indent))
	}
}

func (cg *CodeGen) isLiteralMatch(me MatchExpr) bool {
	for _, arm := range me.Arms {
		if _, ok := arm.Pattern.(LitPattern); ok {
			return true
		}
	}
	return false
}

func (cg *CodeGen) genLiteralMatch(me MatchExpr, indent string, isReturn bool) {
	subject := cg.genExprStr(me.Subject)
	cg.writeln(fmt.Sprintf("%sswitch %s {", indent, subject))
	for _, arm := range me.Arms {
		switch p := arm.Pattern.(type) {
		case LitPattern:
			cg.writeln(fmt.Sprintf("%scase %s:", indent, cg.genExprStr(p.Expr)))
			cg.genArmBody(arm.Body, indent+"\t", isReturn)
		case WildcardPattern:
			cg.writeln(fmt.Sprintf("%sdefault:", indent))
			cg.genArmBody(arm.Body, indent+"\t", isReturn)
		case BindPattern:
			cg.writeln(fmt.Sprintf("%sdefault:", indent))
			cg.genArmBody(arm.Body, indent+"\t", isReturn)
		}
	}
	cg.writeln(fmt.Sprintf("%s}", indent))
}

func (cg *CodeGen) isEnumMatch(me MatchExpr) bool {
	for _, arm := range me.Arms {
		if cp, ok := arm.Pattern.(ConstructorPattern); ok {
			typeName := cg.findTypeName(cp.Name)
			if td, ok := cg.types[typeName]; ok {
				return isEnum(td)
			}
		}
	}
	return false
}

func (cg *CodeGen) genArmBody(body Expr, indent string, isReturn bool) {
	if isReturn {
		cg.genReturnExpr(body, indent)
	} else {
		cg.genVoidBody(body, indent)
	}
}

func (cg *CodeGen) genMatchExpr(me MatchExpr, indent string, isReturn bool) {
	subject := cg.genExprStr(me.Subject)

	if cg.isResultMatch(me) {
		cg.genResultMatch(me, indent, isReturn)
		return
	}

	if cg.isListMatch(me) {
		cg.genListMatch(me, indent, isReturn)
		return
	}

	if cg.isOptionMatch(me) {
		cg.genOptionMatch(me, indent, isReturn)
		return
	}

	if cg.isLiteralMatch(me) {
		cg.genLiteralMatch(me, indent, isReturn)
		return
	}

	if cg.isEnumMatch(me) {
		cg.writeln(fmt.Sprintf("%sswitch %s {", indent, subject))
		for _, arm := range me.Arms {
			cp, ok := arm.Pattern.(ConstructorPattern)
			if ok {
				typeName := cg.findTypeName(cp.Name)
				cg.writeln(fmt.Sprintf("%scase %s%s:", indent, typeName, cp.Name))
			} else {
				cg.writeln(fmt.Sprintf("%sdefault:", indent))
			}
			cg.genArmBody(arm.Body, indent+"\t", isReturn)
		}
		if isReturn {
			cg.writeln(fmt.Sprintf("%sdefault:", indent))
			cg.writeln(fmt.Sprintf("%s\tpanic(\"unreachable\")", indent))
		}
		cg.writeln(fmt.Sprintf("%s}", indent))
		return
	}

	// Sum type match
	cg.writeln(fmt.Sprintf("%sswitch v := %s.(type) {", indent, subject))
	for _, arm := range me.Arms {
		switch pat := arm.Pattern.(type) {
		case ConstructorPattern:
			typeName := cg.findTypeName(pat.Name)
			variantName := typeName + pat.Name
			cg.writeln(fmt.Sprintf("%scase %s:", indent, variantName))
			usedVars := collectUsedIdents(arm.Body)
			var ctorFields []Field
			if td, ok := cg.types[typeName]; ok {
				for _, c := range td.Constructors {
					if c.Name == pat.Name {
						ctorFields = c.Fields
						break
					}
				}
			}
			for i, fp := range pat.Fields {
				if _, used := usedVars[fp.Binding]; used {
					goFieldName := capitalize(fp.Name)
					if i < len(ctorFields) {
						goFieldName = capitalize(ctorFields[i].Name)
					}
					cg.writeln(fmt.Sprintf("%s\t%s := v.%s", indent, snakeToCamel(fp.Binding), goFieldName))
				}
			}
			cg.genArmBody(arm.Body, indent+"\t", isReturn)
		case WildcardPattern:
			cg.writeln(fmt.Sprintf("%sdefault:", indent))
			cg.genArmBody(arm.Body, indent+"\t", isReturn)
		case BindPattern:
			cg.writeln(fmt.Sprintf("%sdefault:", indent))
			cg.writeln(fmt.Sprintf("%s\t%s := v", indent, snakeToCamel(pat.Name)))
			cg.genArmBody(arm.Body, indent+"\t", isReturn)
		}
	}
	cg.writeln(fmt.Sprintf("%s}", indent))
	if isReturn {
		cg.writeln(fmt.Sprintf("%spanic(\"unreachable\")", indent))
	}
}

func (cg *CodeGen) findTypeName(ctorName string) string {
	for typeName, td := range cg.types {
		for _, c := range td.Constructors {
			if c.Name == ctorName {
				return typeName
			}
		}
	}
	return ""
}
