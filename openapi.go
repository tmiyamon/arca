package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func openapiCmd(inputPath string) int {
	prog, err := parseFile(inputPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	spec := generateOpenAPI(prog)
	out, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println(string(out))
	return 0
}

func generateOpenAPI(prog *Program) map[string]interface{} {
	schemas := map[string]interface{}{}

	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case TypeDecl:
			schema := typeToSchema(d)
			if schema != nil {
				schemas[d.Name] = schema
			}
		}
	}

	return map[string]interface{}{
		"openapi": "3.1.0",
		"info": map[string]interface{}{
			"title":   "Arca API",
			"version": "1.0.0",
		},
		"paths": map[string]interface{}{},
		"components": map[string]interface{}{
			"schemas": schemas,
		},
	}
}

func typeToSchema(td TypeDecl) map[string]interface{} {
	if isEnum(td) {
		return enumToSchema(td)
	}
	if len(td.Constructors) == 1 {
		return structToSchema(td)
	}
	if len(td.Constructors) > 1 {
		return sumTypeToSchema(td)
	}
	return nil
}

func structToSchema(td TypeDecl) map[string]interface{} {
	ctor := td.Constructors[0]
	properties := map[string]interface{}{}
	var required []string

	for _, f := range ctor.Fields {
		prop := fieldToSchema(f)
		jsonName := fieldJsonName(f, td.Tags)
		properties[jsonName] = prop
		if !isOptionField(f) {
			required = append(required, jsonName)
		}
	}

	result := map[string]interface{}{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		result["required"] = required
	}
	return result
}

func enumToSchema(td TypeDecl) map[string]interface{} {
	var values []string
	for _, c := range td.Constructors {
		values = append(values, c.Name)
	}
	return map[string]interface{}{
		"type": "string",
		"enum": values,
	}
}

func sumTypeToSchema(td TypeDecl) map[string]interface{} {
	var oneOf []interface{}
	for _, c := range td.Constructors {
		if len(c.Fields) == 0 {
			oneOf = append(oneOf, map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"type": map[string]interface{}{
						"type": "string",
						"enum": []string{c.Name},
					},
				},
				"required": []string{"type"},
			})
		} else {
			props := map[string]interface{}{
				"type": map[string]interface{}{
					"type": "string",
					"enum": []string{c.Name},
				},
			}
			req := []string{"type"}
			for _, f := range c.Fields {
				props[f.Name] = fieldToSchema(f)
				req = append(req, f.Name)
			}
			oneOf = append(oneOf, map[string]interface{}{
				"type":       "object",
				"properties": props,
				"required":   req,
			})
		}
	}
	return map[string]interface{}{
		"oneOf": oneOf,
	}
}

func fieldToSchema(f Field) map[string]interface{} {
	nt, ok := f.Type.(NamedType)
	if !ok {
		return map[string]interface{}{}
	}

	schema := map[string]interface{}{}

	switch nt.Name {
	case "Int":
		schema["type"] = "integer"
	case "Float":
		schema["type"] = "number"
	case "String":
		schema["type"] = "string"
	case "Bool":
		schema["type"] = "boolean"
	case "List":
		schema["type"] = "array"
		if len(nt.Params) > 0 {
			inner := typeRefToSchema(nt.Params[0])
			schema["items"] = inner
		}
	case "Option":
		if len(nt.Params) > 0 {
			inner := typeRefToSchema(nt.Params[0])
			schema["oneOf"] = []interface{}{
				inner,
				map[string]interface{}{"type": "null"},
			}
			return schema
		}
	default:
		schema["$ref"] = "#/components/schemas/" + nt.Name
	}

	// Add constraints
	for _, c := range nt.Constraints {
		switch c.Key {
		case "min":
			if lit, ok := c.Value.(IntLit); ok {
				schema["minimum"] = lit.Value
			}
		case "max":
			if lit, ok := c.Value.(IntLit); ok {
				schema["maximum"] = lit.Value
			}
		case "min_length":
			if lit, ok := c.Value.(IntLit); ok {
				schema["minLength"] = lit.Value
			}
		case "max_length":
			if lit, ok := c.Value.(IntLit); ok {
				schema["maxLength"] = lit.Value
			}
		case "pattern":
			if lit, ok := c.Value.(StringLit); ok {
				schema["pattern"] = lit.Value
			}
		}
	}

	return schema
}

func isOptionField(f Field) bool {
	if nt, ok := f.Type.(NamedType); ok {
		return nt.Name == "Option"
	}
	return false
}

func fieldJsonName(f Field, tags []TagRule) string {
	// Check tags block for json rule
	for _, rule := range tags {
		if rule.Name == "json" {
			if val, ok := rule.Overrides[f.Name]; ok {
				return val
			}
			// Apply case conversion
			switch rule.Case {
			case "snake":
				return camelToSnake(f.Name)
			case "kebab":
				return camelToKebab(f.Name)
			}
			return f.Name
		}
	}
	return f.Name
}

func typeRefToSchema(t Type) map[string]interface{} {
	nt, ok := t.(NamedType)
	if !ok {
		return map[string]interface{}{}
	}
	switch nt.Name {
	case "Int":
		return map[string]interface{}{"type": "integer"}
	case "Float":
		return map[string]interface{}{"type": "number"}
	case "String":
		return map[string]interface{}{"type": "string"}
	case "Bool":
		return map[string]interface{}{"type": "boolean"}
	default:
		return map[string]interface{}{"$ref": "#/components/schemas/" + nt.Name}
	}
}
