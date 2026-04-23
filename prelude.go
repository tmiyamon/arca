package main

// Prelude defines built-in functions available without import.
// Each entry maps an Arca function name to its Go translation.
//
// Signature — when set — drives arg lowering through the same
// hint-based path as any other call: fresh type vars per call, unified
// with arg types via HM, the lambda's param types filled from the
// IRFnType hint. Lower is reserved for builtins whose translation
// cannot be expressed as a signature (variadic println, polymorphic
// len across List/String/Map, etc.); Signature and Lower are
// mutually exclusive.

type BuiltinDef struct {
	GoFunc    string                        // Go function/expression name (e.g. "fmt.Println", "[]byte", "Map_")
	Import    string                        // Go import needed (e.g. "fmt"), empty if none
	Builtin   string                        // helper to generate (e.g. "map", "result"), empty if none
	Lower     func(args []IRExpr) IRExpr    // custom lowering, nil when Signature drives the call
	Signature func(l *Lowerer) PreludeSig   // generic-parameterised signature, fresh per call
}

// PreludeSig is the per-call-instantiated signature of a prelude builtin.
// Params and Ret may reference IRTypeVars that unify with arg types and
// each other during lowering; after args are lowered, Ret is resolveDeep'd
// to produce the call's return type.
type PreludeSig struct {
	Params []IRType
	Ret    IRType
}

var prelude = map[string]BuiltinDef{
	"println": {GoFunc: "fmt.Println", Import: "fmt"},
	"print":   {GoFunc: "fmt.Print", Import: "fmt"},
	"toBytes": {
		GoFunc: "[]byte",
		Lower: func(args []IRExpr) IRExpr {
			if len(args) == 1 {
				return IRFnCall{
					Fn:   IRIdent{GoName: "[]byte"},
					Args: args,
					Type: IRListType{Elem: IRNamedType{GoName: "byte"}},
				}
			}
			return nil
		},
	},
	"map": {
		GoFunc:    "Map_",
		Builtin:   "map",
		Signature: mapSig,
	},
	"filter": {
		GoFunc:    "Filter_",
		Builtin:   "filter",
		Signature: filterPredicateSig,
	},
	"takeWhile": {
		GoFunc:    "TakeWhile_",
		Builtin:   "takeWhile",
		Signature: filterPredicateSig,
	},
	"take": {
		GoFunc:    "Take_",
		Builtin:   "take",
		Signature: takeSig,
	},
	"fold": {
		GoFunc:    "Fold_",
		Builtin:   "fold",
		Signature: foldSig,
	},
	"len": {
		GoFunc: "len",
		Lower: func(args []IRExpr) IRExpr {
			if len(args) == 1 {
				return IRFnCall{Fn: IRIdent{GoName: "len"}, Args: args, Type: IRNamedType{GoName: "int"}}
			}
			return nil
		},
	},
}

// mapSig: `[T, U](List[T], T -> U) -> List[U]`.
func mapSig(l *Lowerer) PreludeSig {
	t := l.freshTypeVar()
	u := l.freshTypeVar()
	return PreludeSig{
		Params: []IRType{
			IRListType{Elem: t},
			IRFnType{Params: []IRType{t}, Ret: u},
		},
		Ret: IRListType{Elem: u},
	}
}

// filterPredicateSig: `[T](List[T], T -> Bool) -> List[T]`, shared by
// `filter` and `takeWhile` (same shape).
func filterPredicateSig(l *Lowerer) PreludeSig {
	t := l.freshTypeVar()
	return PreludeSig{
		Params: []IRType{
			IRListType{Elem: t},
			IRFnType{Params: []IRType{t}, Ret: IRNamedType{GoName: "bool"}},
		},
		Ret: IRListType{Elem: t},
	}
}

// takeSig: `[T](List[T], Int) -> List[T]`.
func takeSig(l *Lowerer) PreludeSig {
	t := l.freshTypeVar()
	return PreludeSig{
		Params: []IRType{
			IRListType{Elem: t},
			IRNamedType{GoName: "int"},
		},
		Ret: IRListType{Elem: t},
	}
}

// foldSig: `[T, U](List[T], U, (U, T) -> U) -> U`.
func foldSig(l *Lowerer) PreludeSig {
	t := l.freshTypeVar()
	u := l.freshTypeVar()
	return PreludeSig{
		Params: []IRType{
			IRListType{Elem: t},
			u,
			IRFnType{Params: []IRType{u, t}, Ret: u},
		},
		Ret: u,
	}
}
