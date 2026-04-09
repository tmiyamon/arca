package main

// Prelude defines built-in functions available without import.
// Each entry maps an Arca function name to its Go translation.

type BuiltinDef struct {
	GoFunc  string // Go function/expression name (e.g. "fmt.Println", "[]byte", "Map_")
	Import  string // Go import needed (e.g. "fmt"), empty if none
	Builtin string // helper to generate (e.g. "map", "result"), empty if none
	Lower   func(args []IRExpr) IRExpr // custom lowering, nil for simple call translation
}

var prelude = map[string]BuiltinDef{
	"println": {GoFunc: "fmt.Println", Import: "fmt"},
	"print":   {GoFunc: "fmt.Print", Import: "fmt"},
	"toBytes": {
		GoFunc: "[]byte",
		Lower: func(args []IRExpr) IRExpr {
			if len(args) == 1 {
				return IRFnCall{
					Func: "[]byte",
					Args: args,
					Type: IRListType{Elem: IRNamedType{GoName: "byte"}},
				}
			}
			return nil
		},
	},
	"map":       {GoFunc: "Map_", Builtin: "map"},
	"filter":    {GoFunc: "Filter_", Builtin: "filter"},
	"fold":      {GoFunc: "Fold_", Builtin: "fold"},
	"take":      {GoFunc: "Take_", Builtin: "take"},
	"takeWhile": {GoFunc: "TakeWhile_", Builtin: "takeWhile"},
	"len": {
		GoFunc: "len",
		Lower: func(args []IRExpr) IRExpr {
			if len(args) == 1 {
				return IRFnCall{Func: "len", Args: args, Type: IRNamedType{GoName: "int"}}
			}
			return nil
		},
	},
}
