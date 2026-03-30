package main

func (cg *CodeGen) genBuiltins() {
	if cg.usedBuiltins["result"] {
		cg.writeln("type Result_[T any, E any] struct {")
		cg.writeln("\tValue T")
		cg.writeln("\tErr   E")
		cg.writeln("\tIsOk  bool")
		cg.writeln("}")
		cg.writeln("")
		cg.writeln("func Ok_[T any, E any](v T) Result_[T, E] {")
		cg.writeln("\treturn Result_[T, E]{Value: v, IsOk: true}")
		cg.writeln("}")
		cg.writeln("")
		cg.writeln("func Err_[T any, E any](e E) Result_[T, E] {")
		cg.writeln("\treturn Result_[T, E]{Err: e}")
		cg.writeln("}")
		cg.writeln("")
	}
	if cg.usedBuiltins["option"] {
		cg.writeln("type Option_[T any] struct {")
		cg.writeln("\tValue T")
		cg.writeln("\tValid bool")
		cg.writeln("}")
		cg.writeln("")
		cg.writeln("func Some_[T any](v T) Option_[T] {")
		cg.writeln("\treturn Option_[T]{Value: v, Valid: true}")
		cg.writeln("}")
		cg.writeln("")
		cg.writeln("func None_[T any]() Option_[T] {")
		cg.writeln("\treturn Option_[T]{}")
		cg.writeln("}")
		cg.writeln("")
	}
	if cg.usedBuiltins["map"] {
		cg.writeln("func Map_[T any, U any](list []T, f func(T) U) []U {")
		cg.writeln("\tresult := make([]U, len(list))")
		cg.writeln("\tfor i, v := range list {")
		cg.writeln("\t\tresult[i] = f(v)")
		cg.writeln("\t}")
		cg.writeln("\treturn result")
		cg.writeln("}")
		cg.writeln("")
	}
	if cg.usedBuiltins["filter"] {
		cg.writeln("func Filter_[T any](list []T, f func(T) bool) []T {")
		cg.writeln("\tvar result []T")
		cg.writeln("\tfor _, v := range list {")
		cg.writeln("\t\tif f(v) {")
		cg.writeln("\t\t\tresult = append(result, v)")
		cg.writeln("\t\t}")
		cg.writeln("\t}")
		cg.writeln("\treturn result")
		cg.writeln("}")
		cg.writeln("")
	}
	if cg.usedBuiltins["fold"] {
		cg.writeln("func Fold_[T any, U any](list []T, init U, f func(U, T) U) U {")
		cg.writeln("\tacc := init")
		cg.writeln("\tfor _, v := range list {")
		cg.writeln("\t\tacc = f(acc, v)")
		cg.writeln("\t}")
		cg.writeln("\treturn acc")
		cg.writeln("}")
		cg.writeln("")
	}
}
